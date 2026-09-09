package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// countGitSubprocesses puts a shim ahead of git on PATH and returns a reader of
// every argv that reached it. Counting subprocesses is the only measurement
// that answers "how many times did this read the refs": a call graph can be
// read wrongly, a comment can be stale, and an in-process counter measures the
// seam a test author chose rather than the work the machine did.
func countGitSubprocesses(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH to shim")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	// Both paths are quoted for the shell that runs this. Interpolating them
	// bare works until a temporary directory has a space in it, and then the
	// shim fails silently: the count comes back zero and reads as "nothing ran"
	// rather than as "the measurement is broken".
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(log) + "\nexec " + shellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		content, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var lines []string
		for _, line := range strings.Split(string(content), "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
		return lines
	}
}

// shellQuote wraps one path for /bin/sh, closing and reopening the quote around
// any quote of its own.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func countMatching(argv []string, needle string) int {
	n := 0
	for _, line := range argv {
		if strings.Contains(line, needle) {
			n++
		}
	}
	return n
}

// One request, one ref inventory. Reading the refs a second time answers about
// a different repository whenever a branch moves in between, and two answers
// that disagree about which commit a branch is at are not two views of one
// world. The count is taken from the git subprocesses that actually ran.
func TestOneCapturedReadServesBothJudgments(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("shared")
	f.git("checkout", "-qb", "request/shared")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	snapshot := f.snapshot()

	argv := countGitSubprocesses(t)
	read, err := f.w.CaptureCheckouts(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	f.w.ClassifyCheckouts(read, snapshot.Projection)
	f.w.AssociateCheckouts(read, snapshot)

	if reads := countMatching(argv(), "for-each-ref"); reads != 1 {
		t.Fatalf("the ref inventory was read %d times for one captured read, want 1:\n%s",
			reads, strings.Join(argv(), "\n"))
	}
	if listings := countMatching(argv(), "worktree list"); listings > 1 {
		t.Fatalf("the checkout listing was taken %d times for one captured read", listings)
	}

	// Both judgments annotated the same slice, so every checkout carries both
	// answers and neither pass worked from its own copy of the listing.
	for i := range read.Worktrees {
		view := read.Worktrees[i]
		if view.Classification == "" {
			t.Fatalf("checkout %q was not classified: %+v", view.Checkout, view)
		}
		if view.Grade == "" {
			t.Fatalf("checkout %q was not graded: %+v", view.Checkout, view)
		}
	}
	_ = head
}

// The captured read is what both judgments answer about, so a branch that
// moves after the capture changes neither of them. Without one capture the
// classifier and the association each re-read and disagree.
func TestBothJudgmentsAnswerAboutTheCapturedRefs(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("captured")
	f.git("checkout", "-qb", "request/captured")
	captured := f.commit("captured work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	// A real linked checkout on that branch, so the assertions below are about
	// a checkout the capture actually listed rather than about an empty loop.
	f.git("worktree", "add", "-q", filepath.Join(filepath.Dir(f.repo), "captured"), "request/captured")
	snapshot := f.snapshot()

	read, err := f.w.CaptureCheckouts(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	linked := false
	for _, view := range read.Worktrees {
		if view.Checkout == "captured" && view.Branch == "request/captured" {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("the capture did not list the linked checkout: %+v", read.Worktrees)
	}

	// The world moves after the capture and before either judgment runs. The
	// commit is made in the linked checkout, which is where that branch lives.
	linkedPath := filepath.Join(filepath.Dir(f.repo), "captured")
	landingTestGit(t, linkedPath, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-q", "-m", "work after the capture"+restsOn(request.ID))
	moved := landingTestGit(t, linkedPath, "rev-parse", "HEAD")
	if moved == captured {
		t.Fatal("the fixture did not move the branch")
	}

	f.w.ClassifyCheckouts(read, snapshot.Projection)
	table := f.w.AssociateCheckouts(read, snapshot)
	row := f.row(table, "request/captured")
	if row.Head != captured {
		t.Fatalf("the association answered about %s, not the captured tip %s", row.Head, captured)
	}
	classified := 0
	for _, view := range read.Worktrees {
		if view.Branch != "request/captured" {
			continue
		}
		classified++
		if view.Head != captured {
			t.Fatalf("the classification refreshed a tip the capture did not see: %+v", view)
		}
		if view.Classification == "" {
			t.Fatalf("the linked checkout was listed and not classified: %+v", view)
		}
		if view.Grade != GradeClaimed {
			t.Fatalf("the linked checkout was listed and not graded: %+v", view)
		}
	}
	if classified != 1 {
		t.Fatalf("expected one checkout on the moved branch, saw %d", classified)
	}
}

// The bound belongs to the request, not to whichever judgment reached it
// first. A budget spent by one is spent for the other, and the other says
// unknown rather than answering from an allowance nobody counted.
func TestTheSharedBudgetIsSpentByBothJudgments(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("budget")
	f.git("checkout", "-qb", "request/budget")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	snapshot := f.snapshot()
	views := func() []WorktreeView {
		return []WorktreeView{{Checkout: "budget", Branch: "request/budget", Head: head, State: "clean"}}
	}

	// Classification first, with nothing left for the association.
	drained := f.w.captureAround(f.ctx, views())
	defer drained.Close()
	f.w.ClassifyCheckouts(drained, snapshot.Projection)
	spent := worktreeInspectionLimit - drained.budget.remaining
	if spent <= 0 {
		t.Fatal("classification spent nothing from the shared budget")
	}
	drained.budget.remaining = 0
	if table := f.w.AssociateCheckouts(drained, snapshot); table.Complete {
		t.Fatalf("the association answered from a budget the classifier had spent: %+v", table)
	}
	if drained.Worktrees[0].Grade != GradeUnknown {
		t.Fatalf("an exhausted shared budget left a checkout graded: %+v", drained.Worktrees[0])
	}

	// And the other way round: the association spends, the classifier sees it.
	reversed := f.w.captureAround(f.ctx, views())
	defer reversed.Close()
	f.w.AssociateCheckouts(reversed, snapshot)
	if worktreeInspectionLimit-reversed.budget.remaining <= 0 {
		t.Fatal("the association spent nothing from the shared budget")
	}
	reversed.budget.remaining = 0
	if deletable := f.w.ClassifyCheckouts(reversed, snapshot.Projection); len(deletable) != 0 {
		t.Fatalf("an exhausted shared budget still produced deletion advice: %v", deletable)
	}
	if reversed.Worktrees[0].Classification != "unknown" {
		t.Fatalf("an exhausted shared budget left a checkout classified: %+v", reversed.Worktrees[0])
	}

	// A fresh capture answers both, so the exhaustion above is the bound and
	// not a permanent refusal.
	fresh := f.w.captureAround(f.ctx, views())
	defer fresh.Close()
	f.w.ClassifyCheckouts(fresh, snapshot.Projection)
	if table := f.w.AssociateCheckouts(fresh, snapshot); !table.Complete {
		t.Fatalf("a fresh capture did not answer both judgments: %+v", table)
	}
	if fresh.Worktrees[0].Grade != GradeClaimed || fresh.Worktrees[0].Classification == "" {
		t.Fatalf("a fresh capture left a checkout half answered: %+v", fresh.Worktrees[0])
	}
}

// Sharing the observation must not share authority. The classification is the
// same whether or not the association ran, and a checkout whose only claim is
// an unsigned trailer is not deletable because of it.
func TestSharingTheReadDoesNotShareAuthority(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("authority")
	f.git("checkout", "-qb", "request/authority")
	head := f.commit("unsigned claim" + restsOn(request.ID))
	f.git("update-ref", "refs/heads/main", head)
	f.git("checkout", "-q", "main")
	repository := "git:" + f.w.config.ObjectFormat + ":" + f.w.config.Genesis
	p := workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "settled", Status: "satisfied", Terminal: "landed",
			TargetRepo: repository, TargetRef: "refs/heads/main", Candidate: head, LandingReceipt: "receipt"}},
		Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{
			"merge_head": head, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}},
	}
	views := func() []WorktreeView {
		return []WorktreeView{{Checkout: "authority", Branch: "request/authority", Head: head, State: "clean"}}
	}

	alone := f.w.captureAround(f.ctx, views())
	defer alone.Close()
	classifiedAlone := f.w.ClassifyCheckouts(alone, p)

	together := f.w.captureAround(f.ctx, views())
	defer together.Close()
	table := f.w.AssociateCheckouts(together, f.snapshot())
	classifiedTogether := f.w.ClassifyCheckouts(together, p)
	if fmt.Sprint(classifiedAlone) != fmt.Sprint(classifiedTogether) {
		t.Fatalf("running the association changed the cleanup advice: %v against %v", classifiedAlone, classifiedTogether)
	}
	if !table.Complete {
		t.Fatalf("the association did not run: %+v", table)
	}
	if together.Worktrees[0].Grade != GradeClaimed {
		t.Fatalf("the fixture did not produce an unsigned claim: %+v", together.Worktrees[0])
	}

	// The claim is present and the checkout is a candidate only because a
	// receipt witnesses its exact tip. Take the receipt away and the same
	// claim protects nothing and licenses nothing.
	unsettled := workroom.Projection{Commitments: []workroom.Commitment{{Request: "open", Status: "promised", Candidate: head}}}
	guarded := f.w.captureAround(f.ctx, views())
	defer guarded.Close()
	f.w.AssociateCheckouts(guarded, f.snapshot())
	if deletable := f.w.ClassifyCheckouts(guarded, unsettled); len(deletable) != 0 {
		t.Fatalf("an unsigned claim made a checkout deletable: %v", deletable)
	}
}

// A capture around a caller's own listing is the same mechanism as a capture
// of this repository's: one ref inventory, one deadline, one budget. The
// single-judgment entry points are built on it, so no caller gets a second
// read by using the convenient shape.
func TestTheSingleJudgmentEntryPointsUseOneCaptureEach(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("single")
	f.git("checkout", "-qb", "request/single")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	snapshot := f.snapshot()
	views := []WorktreeView{{Checkout: "single", Branch: "request/single", Head: head, State: "clean"}}

	argv := countGitSubprocesses(t)
	f.w.Associations(f.ctx, snapshot, views)
	if reads := countMatching(argv(), "for-each-ref"); reads != 1 {
		t.Fatalf("one association read the ref inventory %d times, want 1", reads)
	}
	if views[0].Grade != GradeClaimed {
		t.Fatalf("the single-judgment association did not answer: %+v", views[0])
	}
}

// The whole batch that gets discarded on exhaustion is the response, not the
// judgment that happened to notice. A classification that finished before the
// budget ran out is still an answer derived from a read that never finished,
// and offering its candidate would be a deletion suggestion made without
// having looked for the thing that would have protected the checkout.
//
// The fixture is a genuinely eligible checkout, so the test can only pass by
// withholding an answer that was really there. The cost of classifying it is
// measured first against its own capture, and the read under test is then given
// exactly that much: nothing is adjusted between the two judgment calls.
func TestExhaustionAfterClassificationWithholdsTheWholeResponse(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("whole-batch")
	f.git("checkout", "-qb", "request/whole-batch")
	head := f.commit("work" + restsOn(request.ID))
	f.git("update-ref", "refs/heads/main", head)
	f.git("checkout", "-q", "main")
	repository := "git:" + f.w.config.ObjectFormat + ":" + f.w.config.Genesis
	snapshot := f.snapshot()
	snapshot.Projection = workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "settled", Status: "satisfied", Terminal: "landed",
			TargetRepo: repository, TargetRef: "refs/heads/main", Candidate: head, LandingReceipt: "receipt"}},
		Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{
			"merge_head": head, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}},
	}
	views := func() []WorktreeView {
		return []WorktreeView{{Checkout: "whole-batch", Branch: "request/whole-batch", Head: head, State: "clean"}}
	}

	// The positive control: with room to finish, this checkout really is a
	// candidate and really is graded.
	whole := f.w.captureAround(f.ctx, views())
	defer whole.Close()
	eligible, table := f.w.AdviseCheckouts(whole, snapshot)
	if len(eligible) != 1 || eligible[0] != "whole-batch" {
		t.Fatalf("the fixture is not an eligible candidate: %v %+v", eligible, whole.Worktrees[0])
	}
	if !table.Complete || whole.Worktrees[0].Classification != "deletable" {
		t.Fatalf("the fixture did not answer completely: %+v %+v", table, whole.Worktrees[0])
	}

	// The cost of the classification alone, measured on its own capture.
	calibration := f.w.captureAround(f.ctx, views())
	defer calibration.Close()
	if positive := f.w.ClassifyCheckouts(calibration, snapshot.Projection); len(positive) != 1 {
		t.Fatalf("classification is not eligible before exhaustion: %v", positive)
	}
	spent := worktreeInspectionLimit - calibration.budget.remaining
	if spent <= 0 {
		t.Fatal("classification spent nothing, so the budget below would prove nothing")
	}

	// Exactly that much, and nothing touched between the two judgments.
	read := f.w.captureAround(f.ctx, views())
	defer read.Close()
	read.budget.remaining = spent
	deletable, associations := f.w.AdviseCheckouts(read, snapshot)
	if !read.Exhausted() {
		t.Fatalf("the read was not exhausted, so this proves nothing: %+v", associations)
	}
	if len(deletable) != 0 {
		t.Fatalf("a candidate was offered from a read that did not finish: %v", deletable)
	}
	if associations.Complete {
		t.Fatalf("an exhausted read published a complete table: %+v", associations)
	}
	view := read.Worktrees[0]
	if view.Classification != "unknown" || view.Grade != GradeUnknown || view.Governing != "" {
		t.Fatalf("an exhausted read left advice standing on a checkout: %+v", view)
	}
	if view.Row != "" || len(view.Rows) != 0 {
		t.Fatalf("an exhausted read published mapped rows: %+v", view)
	}
}

// A cancelled request is the same fact arriving from outside, and it must
// withhold the same way.
func TestCancellationWithholdsTheWholeResponse(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("cancelled")
	f.git("checkout", "-qb", "request/cancelled")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	snapshot := f.snapshot()
	views := []WorktreeView{{Checkout: "cancelled", Branch: "request/cancelled", Head: head, State: "clean"}}

	read := f.w.captureAround(f.ctx, views)
	defer read.Close()
	read.cancel()
	deletable, table := f.w.AdviseCheckouts(read, snapshot)
	if len(deletable) != 0 || table.Complete {
		t.Fatalf("a cancelled read published advice: %v %+v", deletable, table)
	}
	if read.Worktrees[0].Classification != "unknown" || read.Worktrees[0].Grade != GradeUnknown {
		t.Fatalf("a cancelled read left a checkout answered: %+v", read.Worktrees[0])
	}
}

// The measurement has to survive the paths it is given. A temporary directory
// with a space in its name broke the shim silently: it reported zero reads,
// which reads as "the code did nothing" rather than as "the test is broken",
// and a count that fails that way can only ever fail towards a false pass.
func TestTheSubprocessCountSurvivesPathsWithSpaces(t *testing.T) {
	// The base is made before any t.TempDir call in this test, because the
	// first such call fixes the directory every later one is created under.
	base, err := os.MkdirTemp("", "gitseq-spaced")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	spaced := filepath.Join(base, "temporary directory with spaces")
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	// Everything below is built under that directory: the shim, its log, and
	// the repository the capture reads.
	t.Setenv("TMPDIR", spaced)

	f := newAssociationFixture(t)
	if !strings.Contains(f.repo, " ") {
		t.Fatalf("the fixture is not under the spaced directory: %s", f.repo)
	}
	request, _ := f.assign("spaced")
	f.git("checkout", "-qb", "request/spaced")
	f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	snapshot := f.snapshot()

	argv := countGitSubprocesses(t)
	read, err := f.w.CaptureCheckouts(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	f.w.AdviseCheckouts(read, snapshot)
	if reads := countMatching(argv(), "for-each-ref"); reads != 1 {
		t.Fatalf("the shim counted %d inventory reads under a spaced path, want 1:\n%s",
			reads, strings.Join(argv(), "\n"))
	}
}

// eligibleCheckout is a checkout that really is a cleanup candidate: a settled
// commitment whose sealed receipt witnesses its exact tip. Every withholding
// proof below starts from one, so a test can only pass by withholding an
// answer that was genuinely there.
func (f *associationFixture) eligibleCheckout(t *testing.T, name string) (Snapshot, func() []WorktreeView) {
	t.Helper()
	request, _ := f.assign(name)
	f.git("checkout", "-qb", "request/"+name)
	head := f.commit("work" + restsOn(request.ID))
	f.git("update-ref", "refs/heads/main", head)
	f.git("checkout", "-q", "main")
	repository := "git:" + f.w.config.ObjectFormat + ":" + f.w.config.Genesis
	snapshot := f.snapshot()
	snapshot.Projection = workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "settled", Status: "satisfied", Terminal: "landed",
			TargetRepo: repository, TargetRef: "refs/heads/main", Candidate: head, LandingReceipt: "receipt"}},
		Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{
			"merge_head": head, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}},
	}
	return snapshot, func() []WorktreeView {
		return []WorktreeView{{Checkout: name, Branch: "request/" + name, Head: head, State: "clean"}}
	}
}

// requirePublished asserts the positive control: this checkout really is an
// eligible candidate and really is graded when the read finishes.
func (f *associationFixture) requirePublished(t *testing.T, snapshot Snapshot, views []WorktreeView, name string) {
	t.Helper()
	read := f.w.captureAround(f.ctx, views)
	defer read.Close()
	deletable, table := f.w.AdviseCheckouts(read, snapshot)
	if len(deletable) != 1 || deletable[0] != name {
		t.Fatalf("%s is not an eligible candidate before the bound: %v %+v", name, deletable, read.Worktrees[0])
	}
	if !table.Complete || read.Worktrees[0].Classification != "deletable" {
		t.Fatalf("%s did not answer completely before the bound: %+v %+v", name, table, read.Worktrees[0])
	}
}

// requireWithheld asserts that the whole response is withheld, with a reason.
func (f *associationFixture) requireWithheld(t *testing.T, snapshot Snapshot, views []WorktreeView, what string) AssociationTable {
	t.Helper()
	read := f.w.captureAround(f.ctx, views)
	defer read.Close()
	deletable, table := f.w.AdviseCheckouts(read, snapshot)
	if len(deletable) != 0 {
		t.Fatalf("%s: a candidate was offered from a read that did not finish: %v", what, deletable)
	}
	if table.Complete || table.Reason == "" {
		t.Fatalf("%s: the withheld table is not marked incomplete with a reason: %+v", what, table)
	}
	view := read.Worktrees[0]
	if view.Classification != "unknown" || view.Grade != GradeUnknown || view.Governing != "" {
		t.Fatalf("%s: advice stood on a checkout: %+v", what, view)
	}
	if view.ClassificationReason != table.Reason {
		t.Fatalf("%s: the classification reason %q does not name the bound %q", what, view.ClassificationReason, table.Reason)
	}
	if view.Row != "" || len(view.Rows) != 0 {
		t.Fatalf("%s: mapped rows were published: %+v", what, view)
	}
	return table
}

// A bound the association meets is the same fact as running out of steps: the
// read did not finish. The cleanup advice computed beside it is an answer from
// that same unfinished read, so it is withheld too. Each case below starts from
// a checkout that really is a candidate, and each drives one bound that leaves
// the shared budget and deadline untouched.
func TestAnIncompleteAssociationWithholdsTheCleanupAdviceToo(t *testing.T) {
	t.Run("tip limit", func(t *testing.T) {
		f := newAssociationFixture(t)
		snapshot, views := f.eligibleCheckout(t, "tips")
		f.requirePublished(t, snapshot, views(), "tips")
		for i := 0; len(f.branches()) <= gitstore.LineageTipLimit; i++ {
			f.git("update-ref", fmt.Sprintf("refs/heads/extra-%03d", i), "refs/heads/main")
			if i > 400 {
				t.Fatal("the tip limit was never passed")
			}
		}
		table := f.requireWithheld(t, snapshot, views(), "tip limit")
		if !strings.Contains(table.Reason, "tip limit") {
			t.Fatalf("the reason does not name the bound: %q", table.Reason)
		}
	})

	t.Run("per-tip depth limit", func(t *testing.T) {
		f := newAssociationFixture(t)
		snapshot, views := f.eligibleCheckout(t, "depth")
		f.requirePublished(t, snapshot, views(), "depth")
		// A chain one commit longer than the per-tip limit reads, on a branch
		// of its own, so the eligible checkout's own tip is untouched.
		f.git("checkout", "-qb", "deep")
		for i := 0; i <= gitstore.LineageCommitLimit; i++ {
			f.commit(fmt.Sprintf("filler %d", i))
		}
		f.git("checkout", "-q", "main")
		table := f.requireWithheld(t, snapshot, views(), "depth limit")
		if !strings.Contains(table.Reason, "per-tip limit") {
			t.Fatalf("the reason does not name the bound: %q", table.Reason)
		}
	})

	t.Run("unavailable tip", func(t *testing.T) {
		f := newAssociationFixture(t)
		snapshot, views := f.eligibleCheckout(t, "missing")
		f.requirePublished(t, snapshot, views(), "missing")
		// A second checkout whose head this repository does not hold. The
		// eligible checkout is still perfectly readable.
		withAbsent := append(views(), WorktreeView{
			Checkout: "absent", Head: strings.Repeat("f", 40), Detached: true, State: "clean"})
		read := f.w.captureAround(f.ctx, withAbsent)
		defer read.Close()
		deletable, table := f.w.AdviseCheckouts(read, snapshot)
		if len(deletable) != 0 || table.Complete {
			t.Fatalf("an unavailable tip published advice: %v %+v", deletable, table)
		}
		for _, view := range read.Worktrees {
			if view.Classification != "unknown" || view.Grade != GradeUnknown {
				t.Fatalf("an unavailable tip left checkout %q answered: %+v", view.Checkout, view)
			}
		}
		if !strings.Contains(table.Reason, "not an object this repository holds") {
			t.Fatalf("the reason does not name the bound: %q", table.Reason)
		}
	})
}
