package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func (f *associationFixture) inventory() []WorktreeView {
	f.t.Helper()
	local, err := f.w.LocalWorktrees(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	// The listing is cached for eight seconds and these fixtures change the
	// world faster than that.
	f.w.worktreesMu.Lock()
	f.w.worktreesCachedAt = f.w.worktreesCachedAt.Add(-time.Hour)
	f.w.worktreesMu.Unlock()
	return local.Worktrees
}

func (f *associationFixture) view(views []WorktreeView, checkout string) WorktreeView {
	f.t.Helper()
	for _, view := range views {
		if view.Checkout == checkout {
			return view
		}
	}
	f.t.Fatalf("no checkout %q in %+v", checkout, views)
	return WorktreeView{}
}

// Linked checkouts of one repository are the ordinary shape of this workroom,
// and two of them at one head is ordinary too: a second checkout at the target
// ref's own head is normal, and this repository has one. So it is reported and
// nothing is refused for it.
func TestInventoryReportsLinkedCheckoutsAndDuplicateHeads(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("linked")
	f.git("checkout", "-qb", "request/linked")
	head := f.commit("work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")

	root := filepath.Dir(f.repo)
	f.git("worktree", "add", "-q", filepath.Join(root, "one"), "request/linked")
	f.git("worktree", "add", "-q", "--detach", filepath.Join(root, "two"), head)

	views := f.inventory()
	if len(views) != 3 {
		t.Fatalf("linked checkouts not listed: %+v", views)
	}
	one, two := f.view(views, "one"), f.view(views, "two")
	if one.Head != head || two.Head != head {
		t.Fatalf("linked checkouts are not at the same head: %+v %+v", one, two)
	}
	if !one.DuplicateHead || !two.DuplicateHead {
		t.Fatalf("two checkouts at one head were not reported as duplicates: %+v %+v", one, two)
	}
	if f.view(views, "repo").DuplicateHead {
		t.Fatalf("the only checkout at its head was reported as a duplicate: %+v", f.view(views, "repo"))
	}
	if !two.Detached || one.Detached {
		t.Fatalf("detachment misreported: %+v %+v", one, two)
	}

	// Both linked checkouts join the same association row, and the detached
	// one gets its own because no branch covers it.
	table := f.associate(views)
	branchRow := f.row(table, "request/linked")
	if len(branchRow.Checkouts) != 1 || branchRow.Checkouts[0] != "one" {
		t.Fatalf("the branch row does not name its checkout: %+v", branchRow)
	}
	if branchRow.Governing != request.ID {
		t.Fatalf("the linked checkout's branch is not associated: %+v", branchRow)
	}
	if f.view(views, "two").Governing != request.ID {
		t.Fatalf("a detached checkout got no row of its own: %+v", f.view(views, "two"))
	}
}

// Five checkout states that must all be reported and none of which may lead to
// a write: dirty, locked, symlinked, outside the checkout root, and a head no
// ref in this repository points at. Every one of them protects.
func TestCheckoutReportingCoversDirtyLockedSymlinkedAndOutOfScopePaths(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("states")
	f.git("checkout", "-qb", "request/states")
	head := f.commit("work" + restsOn(request.ID))
	orphan := f.commit("a commit no ref will point at" + restsOn(request.ID))
	f.git("update-ref", "refs/heads/request/states", head)
	f.git("checkout", "-q", "main")

	root := filepath.Dir(f.repo)
	elsewhere := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	f.git("worktree", "add", "-q", filepath.Join(root, "dirty"), "request/states")
	if err := os.WriteFile(filepath.Join(root, "dirty", "scratch"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git("worktree", "add", "-q", "--detach", filepath.Join(root, "locked"), head)
	f.git("worktree", "lock", filepath.Join(root, "locked"))
	f.git("worktree", "add", "-q", "--detach", filepath.Join(real, "target"), head)
	if err := os.Symlink(filepath.Join(real, "target"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	// Not detached, so containment is what protects it rather than detachment.
	f.git("branch", "request/outside", head)
	f.git("worktree", "add", "-q", filepath.Join(elsewhere, "outside"), "request/outside")
	f.git("worktree", "add", "-q", "--detach", filepath.Join(root, "orphaned"), orphan)

	before := treeDigest(t, f.repo)
	views := f.inventory()
	snapshot := f.snapshot()
	deletable := f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views)
	f.w.Associations(f.ctx, snapshot, views)

	if len(deletable) != 0 {
		t.Fatalf("a checkout in one of these states was offered for deletion: %v", deletable)
	}
	if state := f.view(views, "dirty").State; state != "dirty" {
		t.Fatalf("the dirty checkout reads as %q", state)
	}
	if state := f.view(views, "locked").State; state != "locked" {
		t.Fatalf("the locked checkout reads as %q", state)
	}
	if outside := f.view(views, "outside"); !outside.OutsideRoot {
		t.Fatalf("a checkout outside the checkout root was not reported: %+v", outside)
	} else if outside.ClassificationReason != "checkout path is outside the checkout root or reached through a symbolic link" {
		t.Fatalf("containment did not protect: %+v", outside)
	}
	if target := f.view(views, "target"); target.OutsideRoot || target.SymlinkedPath {
		t.Fatalf("the real directory behind the link was misreported: %+v", target)
	}
	if orphaned := f.view(views, "orphaned"); !orphaned.ReflessHead {
		t.Fatalf("a head no ref points at was not reported: %+v", orphaned)
	}
	// The refless protection, reached without detachment: a clean checkout on
	// no branch, at a commit nothing points at.
	refless := []WorktreeView{{Checkout: "refless", Head: orphan, State: "clean"}}
	if deletable := f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, refless); len(deletable) != 0 {
		t.Fatalf("a refless head was offered for deletion: %v", deletable)
	}
	if refless[0].ClassificationReason != "no ref in this repository points at this head" {
		t.Fatalf("the refless head did not protect: %+v", refless[0])
	}
	for _, view := range views {
		if view.Classification == "deletable" {
			t.Fatalf("checkout %q was classified deletable: %+v", view.Checkout, view)
		}
	}
	if after := treeDigest(t, f.repo); after == before {
		// git status refreshes its index, so the digest is expected to move.
		// What matters is that nothing under refs changed.
		t.Log("the repository was byte-identical after the inventory")
	}
	refsBefore := f.git("for-each-ref", "--format=%(refname) %(objectname)")
	f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views)
	if refsAfter := f.git("for-each-ref", "--format=%(refname) %(objectname)"); refsAfter != refsBefore {
		t.Fatal("classification moved a ref")
	}

	// The linked entry itself: git records the path it was given, so a
	// checkout added through the symbolic link is the one reported as
	// symlinked. It is added last so the fixture above stays readable.
	f.git("worktree", "add", "-q", "--detach", filepath.Join(root, "linked", "inner"), head)
	inner := f.view(f.inventory(), "inner")
	if !inner.SymlinkedPath && !inner.OutsideRoot {
		t.Skipf("this host resolved the symbolic link before git recorded it: %+v", inner)
	}
}

// A symbolic link at the checkout entry is a name and a directory that are two
// different things. It is asked about directly rather than by comparing
// resolved paths, because on some hosts the temporary directory of every
// process is itself reached through a link.
func TestASymbolicLinkAtTheCheckoutEntryIsReported(t *testing.T) {
	f := newAssociationFixture(t)
	root := filepath.Dir(f.repo)
	real := filepath.Join(root, "real-checkout")
	f.git("worktree", "add", "-q", "--detach", real, "main")
	link := filepath.Join(root, "linked-checkout")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if isSymbolicLink(real) {
		t.Fatal("a real directory reported as a symbolic link")
	}
	if !isSymbolicLink(link) {
		t.Fatal("a symbolic link at the checkout entry was not reported")
	}
	if isSymbolicLink(filepath.Join(root, "absent")) {
		t.Fatal("a path this process cannot stat was reported as a symbolic link")
	}
	if withinRoot(root, filepath.Join(root+"-sibling", "x")) {
		t.Fatal("containment compared string prefixes rather than path elements")
	}
	if !withinRoot(root, filepath.Join(root, "x")) {
		t.Fatal("a path under the root was reported outside it")
	}
}

// Staleness is not settlement. A stale request is one whose reasoning moved,
// and its work is still owed; treating the two as one word is how a checkout
// holding unfinished work becomes a cleanup candidate.
func TestAStaleGoverningRecordProtectsAndASettledOneDoesNot(t *testing.T) {
	f := newAssociationFixture(t)
	f.git("checkout", "-qb", "request/stale")
	head := f.commit("work")
	f.git("update-ref", "refs/heads/main", head)
	repository := "git:" + f.w.config.ObjectFormat + ":" + f.w.config.Genesis

	classify := func(status string, approvedNotLanded bool) (string, WorktreeView) {
		p := workroom.Projection{
			Commitments: []workroom.Commitment{{Request: "request", Promise: "promise", Status: status,
				Stale: true, ApprovedNotLanded: approvedNotLanded, TargetRepo: repository, TargetRef: "refs/heads/main",
				Candidate: head, Approval: "approval", Terminal: "landed", LandingReceipt: "receipt"}},
			Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{
				"merge_head": head, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}},
		}
		views := []WorktreeView{{Checkout: "checkout", Branch: "request/stale", Head: head, State: "clean"}}
		deletable := f.w.ClassifyWorktrees(f.ctx, p, views)
		return strings.Join(deletable, ","), views[0]
	}

	if deletable, view := classify("stale", false); deletable != "" || view.Classification != "protected" {
		t.Fatalf("a stale governing record was offered for deletion: %q %+v", deletable, view)
	}
	if deletable, view := classify("satisfied", true); deletable != "" || view.Classification != "protected" {
		t.Fatalf("an approved-but-unlanded head was offered for deletion: %q %+v", deletable, view)
	}
	if deletable, _ := classify("satisfied", false); deletable != "checkout" {
		t.Fatalf("a settled landed record was not offered as a candidate: %q", deletable)
	}
	// A word this client has never heard of is a word it cannot settle.
	if deletable, view := classify("some-future-status", false); deletable != "" || view.Classification != "protected" {
		t.Fatalf("an unknown status settled a commitment: %q %+v", deletable, view)
	}
	if workroom.SettledCommitment("stale") {
		t.Fatal("staleness is on the settled word list")
	}
}

// The two endings a commitment has, and the one thing neither of them does.
// A merge receipt for the exact tip settles it and makes the checkout a
// candidate; a report that closed the commitment without landing anything
// leaves the tip unproved, so the checkout stays protected. Neither ending
// deletes anything and neither moves a ref.
func TestMergeAndNonMergeEndingsChangeAdviceWithoutDeletingAnything(t *testing.T) {
	f := newAssociationFixture(t)
	f.git("checkout", "-qb", "request/ending")
	head := f.commit("work")
	f.git("checkout", "-q", "main")
	repository := "git:" + f.w.config.ObjectFormat + ":" + f.w.config.Genesis

	views := func() []WorktreeView {
		return []WorktreeView{{Checkout: "ending", Branch: "request/ending", Head: head, State: "clean"}}
	}
	landed := workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "request", Status: "satisfied", Terminal: "landed",
			TargetRepo: repository, TargetRef: "refs/heads/main", Candidate: head, LandingReceipt: "receipt"}},
		Statements: []workroom.Statement{{Event: "receipt", Body: map[string]string{
			"merge_head": head, "merge_target_repo": repository, "merge_target_ref": "refs/heads/main"}}},
	}
	reported := workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "request", Status: "satisfied", Terminal: "reported", Candidate: head}},
	}
	abandoned := workroom.Projection{
		Commitments: []workroom.Commitment{{Request: "request", Status: "abandoned", Terminal: "abandoned", Candidate: head}},
	}

	before := f.git("for-each-ref", "--format=%(refname) %(objectname)")
	// Nothing landed yet, so the receipt names no incorporated head.
	if deletable := f.w.ClassifyWorktrees(f.ctx, landed, views()); len(deletable) != 0 {
		t.Fatalf("an unlanded tip was offered as a candidate: %v", deletable)
	}
	f.git("update-ref", "refs/heads/main", head)
	if deletable := f.w.ClassifyWorktrees(f.ctx, landed, views()); len(deletable) != 1 {
		t.Fatalf("a merged ending did not produce a candidate: %v", deletable)
	}
	f.git("update-ref", "refs/heads/main", f.git("rev-parse", "main^"))
	if deletable := f.w.ClassifyWorktrees(f.ctx, reported, views()); len(deletable) != 0 {
		t.Fatalf("a commitment reported closed without landing produced a candidate: %v", deletable)
	}
	if deletable := f.w.ClassifyWorktrees(f.ctx, abandoned, views()); len(deletable) != 1 {
		t.Fatalf("an explicitly abandoned exact tip did not produce a candidate: %v", deletable)
	}
	if after := f.git("for-each-ref", "--format=%(refname) %(objectname)"); after != before {
		t.Fatalf("classification changed the refs:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := os.Stat(f.repo); err != nil {
		t.Fatalf("the checkout was removed by advice: %v", err)
	}
}

// The disclosure this exists for. A note artifact was retired and its branch
// and checkout deleted while a proposal rested directly on it and the parent
// request still held a live promise; the work survived only because the commit
// outlived the deletion.
//
// The positive control is that checkout, protected. The negative control is a
// checkout in the same repository, in the same pass, with no live proposal and
// a settled commitment naming its exact tip: it must still be offered as a
// candidate, or the protection would be indistinguishable from protecting
// everything.
func TestAPendingDecisionProtectsItsCandidateAndNothingElse(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("pending")
	f.git("checkout", "-qb", "request/pending")
	cited := f.commit("the work a decision is about" + restsOn(request.ID))
	artifact := f.artifact("pending-artifact", "notes/pending.md", cited, promise.ID)
	proposal := f.act("human", Act{Verb: VerbState, Kind: workroom.KindPropose, Text: "adopt the note",
		RestsOn: []string{artifact.ID}, IdempotencyKey: "pending-proposal"})
	// Retirement withdraws the pointer. It does not decide the proposal that
	// cites it, and the parent request is still open.
	f.act("agent", Act{Verb: VerbSupersede, Target: artifact.ID, Text: "withdrawn", IdempotencyKey: "retire-pending"})

	// The negative control, in the same repository and the same pass.
	f.git("checkout", "-q", "main")
	f.git("checkout", "-qb", "request/finished")
	finished := f.commit("work nobody is deciding about")
	f.git("checkout", "-q", "main")

	snapshot := f.snapshot()
	if !workroomHolds(snapshot.Projection, request.ID, "promised") {
		t.Fatalf("the parent request is not unsettled: %+v", snapshot.Projection.Commitments)
	}
	p := snapshot.Projection
	p.Commitments = append(append([]workroom.Commitment(nil), p.Commitments...),
		workroom.Commitment{Request: "finished", Status: "abandoned", Terminal: "abandoned", Candidate: finished})

	views := []WorktreeView{
		{Checkout: "pending", Branch: "request/pending", Head: cited, State: "clean"},
		{Checkout: "finished", Branch: "request/finished", Head: finished, State: "clean"},
	}
	deletable := f.w.ClassifyWorktrees(f.ctx, p, views)

	if views[0].PendingDecision != proposal.ID {
		t.Fatalf("the pending decision did not protect its candidate: %+v", views[0])
	}
	if views[0].Classification != "protected" || views[0].ClassificationReason != "a live proposal cites work this checkout holds" {
		t.Fatalf("the protection did not reach the classification: %+v", views[0])
	}
	if len(deletable) != 1 || deletable[0] != "finished" {
		t.Fatalf("the negative control was not offered as a candidate: %v", deletable)
	}
	if views[1].PendingDecision != "" {
		t.Fatalf("work no proposal cites was protected anyway: %+v", views[1])
	}

	// A later commit on the same branch still holds the cited work, so the
	// protection follows the branch rather than one exact object.
	f.git("checkout", "-q", "request/pending")
	beyond := f.commit("more work" + restsOn(request.ID))
	f.git("checkout", "-q", "main")
	beyondViews := []WorktreeView{{Checkout: "pending", Branch: "request/pending", Head: beyond, State: "clean"}}
	f.w.ClassifyWorktrees(f.ctx, p, beyondViews)
	if beyondViews[0].PendingDecision != proposal.ID {
		t.Fatalf("a descendant of the cited commit lost the protection: %+v", beyondViews[0])
	}

	// Adopting the decision ends the pending state. Decision adoption and
	// checkout removal stay two facts, so this releases the protection and
	// removes nothing.
	f.act("human", Act{Verb: VerbRatify, Target: proposal.ID, IdempotencyKey: "adopt-pending"})
	adopted := f.snapshot().Projection
	settledViews := []WorktreeView{{Checkout: "pending", Branch: "request/pending", Head: cited, State: "clean"}}
	f.w.ClassifyWorktrees(f.ctx, adopted, settledViews)
	if settledViews[0].PendingDecision != "" {
		t.Fatalf("an adopted proposal is still pending: %+v", settledViews[0])
	}
}

func workroomHolds(p workroom.Projection, request, status string) bool {
	for _, commitment := range p.Commitments {
		if commitment.Request == request {
			return commitment.Status == status
		}
	}
	return false
}

// The inventory cap is a cap on the answer, not on the repository. Above it
// the projection refuses rather than reporting a truncated checkout list that
// a reader would take for the whole of it.
func TestTheCheckoutInventoryCapRefusesRatherThanTruncating(t *testing.T) {
	f := newAssociationFixture(t)
	root := filepath.Dir(f.repo)
	for i := 0; len(f.mustList()) <= 128; i++ {
		f.git("worktree", "add", "-q", "--detach", "--no-checkout", filepath.Join(root, fmt.Sprintf("wt-%03d", i)), "main")
		if i > 200 {
			t.Fatal("the cap was never reached")
		}
	}
	f.w.worktreesMu.Lock()
	f.w.worktreesCachedAt = f.w.worktreesCachedAt.Add(-time.Hour)
	f.w.worktreesMu.Unlock()
	if _, err := f.w.LocalWorktrees(f.ctx); err == nil {
		t.Fatal("a repository above the checkout cap was projected anyway")
	} else if !strings.Contains(err.Error(), "limit is 128") {
		t.Fatalf("the refusal does not name the cap: %v", err)
	}
}

// mustList counts checkouts through git directly, so the cap under test is not
// also the thing counting.
func (f *associationFixture) mustList() []string {
	f.t.Helper()
	var paths []string
	for _, line := range strings.Split(f.git("worktree", "list", "--porcelain"), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths
}
