package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/eventref"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Guarded creation, proved against a real Git binary in a temporary
// repository. Every refusal is measured by what the filesystem holds
// afterwards rather than by an exit status, because a refusal that wrote
// something first is not a refusal.

// forgetCheckouts defeats the eight-second checkout-listing cache. These
// fixtures add and remove checkouts faster than that, and a creation reading
// a stale listing would answer about a repository that has moved.
func (f *associationFixture) forgetCheckouts() {
	f.w.worktreesMu.Lock()
	f.w.worktreesCachedAt = f.w.worktreesCachedAt.Add(-time.Hour)
	f.w.worktreesMu.Unlock()
}

// checkoutIntent is one ordinary intent: the fixture's agent, a destination
// beside the repository, and the fixture's own verified snapshot.
func checkoutIntent(f *associationFixture, governing, name, branch string) CheckoutIntent {
	return CheckoutIntent{
		Governing: governing,
		Actor:     "agent",
		Path:      filepath.Join(filepath.Dir(f.repo), name),
		Branch:    branch,
		Start:     "HEAD",
		Snapshot:  f.snapshot(),
	}
}

func (f *associationFixture) create(t *testing.T, intent CheckoutIntent) CheckoutResult {
	t.Helper()
	f.forgetCheckouts()
	result, err := f.w.CreateCheckout(f.ctx, intent)
	if err != nil {
		t.Fatalf("creation refused: %v\n%s", err, strings.Join(result.Report(), "\n"))
	}
	f.forgetCheckouts()
	return result
}

// advance moves the served checkout's branch on, so that the next creation
// starts somewhere no existing checkout is sitting. Two checkouts at one head
// are a separate fact with a separate proof, and a fixture that tripped it by
// accident would be testing the duplicate warning instead of what it says.
func (f *associationFixture) advance(message string) string {
	f.t.Helper()
	head := f.commit(message)
	f.forgetCheckouts()
	return head
}

// check finds one named check in a result.
func check(t *testing.T, result CheckoutResult, name string) CheckoutCheck {
	t.Helper()
	for _, candidate := range result.Checks {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("no %q check in %v", name, result.Report())
	return CheckoutCheck{}
}

// ---------------------------------------------------------------------------
// The positive control

// One creation makes exactly five things, and each of them says the same
// governing record. Nothing here is a proof about refusals: it is the proof
// that the guards are not simply refusing everything.
func TestCreatingACheckoutStampsItAndAllocatesOneAttempt(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("stamp")
	head := f.git("rev-parse", "HEAD")

	result := f.create(t, checkoutIntent(f, request.ID, "stamped", "request/stamped"))
	for _, candidate := range result.Checks {
		if candidate.Name == CheckCheckoutRoot {
			// Nothing is configured in this fixture, so there is no boundary
			// to be outside of. That is neither a pass nor a refusal.
			if candidate.Outcome != CheckNotEstablished || !strings.Contains(candidate.Detail, checkoutRootKey) {
				t.Fatalf("checkout root = %+v", candidate)
			}
			continue
		}
		if candidate.Outcome != CheckPassed {
			t.Fatalf("an ordinary creation did not pass every check: %v", result.Report())
		}
	}
	if !result.Created || result.Head != head {
		t.Fatalf("creation result = %+v", result)
	}
	if branch := f.git("rev-parse", "refs/heads/request/stamped"); branch != head {
		t.Fatalf("the branch is at %s, want %s", branch, head)
	}
	if info, err := os.Stat(filepath.Join(result.Path, ".git")); err != nil || info.IsDir() {
		t.Fatalf("the destination is not a linked checkout: %v", err)
	}

	// The record: this workroom's genesis, the full canonical identifier, and
	// the attempt it was allocated. It is read back from disk rather than
	// taken from the result.
	stored, err := readCheckoutRecordAt(result.RecordPath, f.w.config.Genesis, f.w.config.ObjectFormat)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Governing != request.ID || stored.Genesis != f.w.config.Genesis || stored.Branch != "request/stamped" {
		t.Fatalf("record = %+v", stored)
	}
	if stored.AttemptRef != result.Attempt.Ref || stored.Attempt != 1 {
		t.Fatalf("record names attempt %d at %q, result says %+v", stored.Attempt, stored.AttemptRef, result.Attempt)
	}
	// The record's home is the checkout's own private Git directory, not the
	// repository's common one and not the invoking checkout's.
	if want := filepath.Join(f.w.CommonDir, "worktrees", "stamped", "gitseq", "checkout.json"); result.RecordPath != want {
		t.Fatalf("record path = %s, want %s", result.RecordPath, want)
	}

	// The template: an empty subject, a blank line, and the exact trailer.
	// Appending these bytes to a message is what makes the line a trailer
	// rather than part of a paragraph.
	template, err := os.ReadFile(result.TemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\n\nRests-On: " + request.ID + "\n"; string(template) != want {
		t.Fatalf("template = %q, want %q", template, want)
	}
	if result.Trailer != "Rests-On: "+request.ID {
		t.Fatalf("trailer = %q", result.Trailer)
	}

	// The attempt ref, at the head the checkout actually holds.
	if value := f.git("rev-parse", result.Attempt.Ref); value != head {
		t.Fatalf("%s points at %s, want %s", result.Attempt.Ref, value, head)
	}
	if !strings.HasPrefix(result.Attempt.Ref, "refs/gitseq/lanes/") || !strings.HasSuffix(result.Attempt.Ref, "/1") {
		t.Fatalf("attempt ref = %q", result.Attempt.Ref)
	}
	// The trailer assistance is the template and nothing else. No hook is
	// installed, and this stage installs none: a linked checkout resolves its
	// hooks to the shared common directory, so a hook is repository-wide
	// however narrowly it is asked for, and that contract is not settled.
	if _, err := os.Lstat(filepath.Join(f.w.CommonDir, "hooks", "prepare-commit-msg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a commit hook exists after a creation: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Copies and foreign records

// A record is scoped to one workroom by its genesis, and that is what makes a
// copy detectable. Deleting the genesis comparison in the reader would make
// the copied file below read as this repository's own record.
func TestARecordCopiedFromAnotherWorkroomIsRefusedAndAForeignSelectorWritesNothing(t *testing.T) {
	first := newAssociationFixture(t)
	second := newAssociationFixture(t)
	firstRequest, _ := first.assign("origin")
	secondRequest, _ := second.assign("destination")

	origin := first.create(t, checkoutIntent(first, firstRequest.ID, "origin", "request/origin"))
	destination := second.create(t, checkoutIntent(second, secondRequest.ID, "destination", "request/destination"))

	// The positive control first: a record written here reads here.
	if _, err := second.w.readCheckoutRecord(second.ctx, destination.Path); err != nil {
		t.Fatalf("this workroom's own record did not read back: %v", err)
	}

	copied, err := os.ReadFile(origin.RecordPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination.RecordPath, copied, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = second.w.readCheckoutRecord(second.ctx, destination.Path)
	if err == nil {
		t.Fatal("a record copied from another repository read as this one's own")
	}
	if !strings.Contains(err.Error(), "copied from another repository") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}

	// A canonical identifier of the other workroom is a determinate negative
	// with no projection reading at all, and it is refused before anything is
	// written.
	foreign := "git:sha1:" + first.w.config.Genesis + "#git:sha1:" + strings.Repeat("c", 40)
	second.forgetCheckouts()
	second.w.LocalWorktrees(second.ctx)
	intent := checkoutIntent(second, foreign, "foreign", "request/foreign")
	before := treeDigest(t, second.repo)
	result, err := second.w.CreateCheckout(second.ctx, intent)
	if err == nil {
		t.Fatal("a foreign governing record created a checkout")
	}
	if got := check(t, result, CheckGoverningRecord); got.Outcome != CheckRefused || !strings.Contains(got.Detail, "another workroom") {
		t.Fatalf("the foreign identifier was graded %+v", got)
	}
	if after := treeDigest(t, second.repo); after != before {
		t.Fatal("a refused creation changed the repository")
	}
	if _, err := os.Lstat(intent.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused creation made its destination: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Destination and containment

// The four destinations creation refuses on their own terms, and the two it
// accepts. No configured checkout root exists in this source, so nothing here
// refuses a path for being outside one; what is refused is a path that is
// unsafe as a place to put a working tree.
func TestUnsafeDestinationsRefuseBeforeAnyWrite(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("destinations")
	root := filepath.Dir(f.repo)

	occupied := filepath.Join(root, "occupied")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	existing := f.create(t, checkoutIntent(f, request.ID, "existing", "request/existing"))
	f.advance("move on so the next destination is not a duplicate head")

	f.w.LocalWorktrees(f.ctx)
	for _, test := range []struct {
		name    string
		path    string
		check   string
		because string
	}{
		{"an existing non-empty directory", occupied, CheckDestination, "already exists"},
		{"a symbolic link", link, CheckDestination, "symbolic link"},
		{"inside the repository's Git directory", filepath.Join(f.w.CommonDir, "inside"), CheckRepository, "Git directory"},
		{"inside an existing checkout", filepath.Join(existing.Path, "nested"), CheckRepository, "existing checkout"},
		{"nothing at all", "", CheckDestination, "no destination"},
	} {
		t.Run(test.name, func(t *testing.T) {
			intent := checkoutIntent(f, request.ID, "unused", "request/"+strings.ReplaceAll(test.name, " ", "-"))
			intent.Path = test.path
			before := treeDigest(t, f.repo)
			beforeRoot := treeDigest(t, root)
			result, err := f.w.CreateCheckout(f.ctx, intent)
			if err == nil {
				t.Fatalf("%s was accepted as a destination", test.name)
			}
			if got := check(t, result, test.check); got.Outcome != CheckRefused || !strings.Contains(got.Detail, test.because) {
				t.Fatalf("%s: %s check = %+v", test.name, test.check, got)
			}
			if after := treeDigest(t, f.repo); after != before {
				t.Fatalf("%s: the refusal changed the repository", test.name)
			}
			if after := treeDigest(t, root); after != beforeRoot {
				t.Fatalf("%s: the refusal changed the destination's neighbourhood", test.name)
			}
		})
	}

	// The positive control: an ordinary sibling of the repository creates, so
	// the guards above are not simply refusing everything.
	f.create(t, checkoutIntent(f, request.ID, "ordinary", "request/ordinary"))
}

// ---------------------------------------------------------------------------
// Selectors

// A selector is resolved at the command boundary, by the one resolver every
// `gs` command uses, and creation is handed the identifier it produced. An
// ambiguous or unresolvable selector therefore never reaches creation at all
// — which is the point: the refusal a person sees is "this selector could not
// be resolved", the same refusal every other command gives on the same input,
// and no new rule was added to the adopted refusal list to produce it.
func TestAnAmbiguousSelectorRefusesAtTheResolverAndCreatesNothing(t *testing.T) {
	f := newAssociationFixture(t)
	for i := 0; i < 6; i++ {
		f.assign(fmt.Sprintf("filler-%d", i))
	}
	snapshot := f.snapshot()
	room := eventref.Room{Genesis: f.w.config.Genesis, ObjectFormat: f.w.config.ObjectFormat}
	resolver := func() *eventref.Resolver {
		return eventref.New(room, func() (eventref.Set, error) {
			return eventref.FromProjection(room, snapshot.Projection), nil
		})
	}

	fragment := ""
	for _, character := range "0123456789abcdef" {
		matched := 0
		for _, decision := range snapshot.Projection.Decisions {
			hash := decision.Event[strings.LastIndex(decision.Event, ":")+1:]
			if strings.HasPrefix(hash, string(character)) || strings.HasSuffix(hash, string(character)) {
				matched++
			}
		}
		if matched > 1 {
			fragment = string(character)
			break
		}
	}
	if fragment == "" {
		t.Skip("no one-character fragment matched two event hashes in this run")
	}

	before := treeDigest(t, f.repo)
	_, err := resolver().One(fragment)
	if err == nil {
		t.Fatal("an ambiguous fragment resolved to one record")
	}
	var refusal *eventref.Refusal
	if !errors.As(err, &refusal) || len(refusal.Candidates) < 2 {
		t.Fatalf("the ambiguity refusal did not carry its candidates: %v", err)
	}
	if after := treeDigest(t, f.repo); after != before {
		t.Fatal("a refused resolution changed the repository")
	}

	// The positive control: a full hash resolves, and the identifier it
	// produces creates.
	full := snapshot.Projection.Decisions[1].Event
	hash := full[strings.LastIndex(full, ":")+1:]
	resolved, err := resolver().One(hash)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != full {
		t.Fatalf("an unambiguous fragment resolved to %s, want %s", resolved, full)
	}

	// A canonical identifier of this workroom naming no record here is a
	// determinate negative creation itself makes, because a canonical
	// selector reaches creation without the log being read.
	missing := "git:sha1:" + f.w.config.Genesis + "#git:sha1:" + strings.Repeat("d", 40)
	result, err := f.w.CreateCheckout(f.ctx, checkoutIntent(f, missing, "missing", "request/missing"))
	if err == nil {
		t.Fatal("an identifier naming no record here created a checkout")
	}
	if got := check(t, result, CheckRecordKind); got.Outcome != CheckRefused || !strings.Contains(got.Detail, "no durable record") {
		t.Fatalf("record kind = %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Record kind and addressee

// The two limbs a governing record may arrive by, and the two determinate
// negatives beside them. Dropping the addressee comparison would let the
// third actor's request through; dropping the adopted-decision limb would
// forbid all self-initiated work, which a test asking only "does it refuse?"
// would never notice.
func TestOnlyARequestAddressedToTheActorOrAnAdoptedDecisionGoverns(t *testing.T) {
	f := newAssociationFixture(t)
	other, _, err := f.w.AddActor(f.ctx, "human", "other", "agent")
	if err != nil {
		t.Fatal(err)
	}
	mine, _ := f.assign("mine")
	theirs := f.act("human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "for somebody else",
		Body:    map[string]string{"to": other.Fingerprint, "conditions": "done", "no_git_artifact": "true"},
		RestsOn: []string{f.seed.ID}, IdempotencyKey: "theirs"})
	head := f.commit("a commit an artifact can name")
	artifact := f.artifact("an-artifact", "internal/thing", head, f.seed.ID)

	proposal := f.act("human", Act{Verb: VerbState, Kind: workroom.KindPropose, Text: "adopt the approach",
		RestsOn: []string{f.seed.ID}, IdempotencyKey: "proposal"})
	f.act("human", Act{Verb: VerbRatify, Target: proposal.ID, IdempotencyKey: "adopt"})

	// A satisfied request addressed to somebody else is an adopted decision
	// by the review guard's own rule, and self-initiated work names it.
	closed := f.act("human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "already done",
		Body:    map[string]string{"to": other.Fingerprint, "conditions": "done", "no_git_artifact": "true"},
		RestsOn: []string{f.seed.ID}, IdempotencyKey: "closed"})
	closedPromise := f.act("other", Act{Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
		RestsOn: []string{closed.ID}, IdempotencyKey: "closed-promise"})
	closedReport := f.act("other", Act{Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{closedPromise.ID}, IdempotencyKey: "closed-report"})
	f.act("human", Act{Verb: VerbRatify, Target: closedReport.ID, IdempotencyKey: "closed-satisfy"})

	snapshot := f.snapshot()
	if commitment, ok := commitmentIn(snapshot.Projection, closed.ID); !ok || commitment.Status != "satisfied" {
		t.Fatalf("the fixture did not settle its request: %+v", snapshot.Projection.Commitments)
	}

	accepts := func(name, governing, branch string) CheckoutResult {
		t.Helper()
		f.advance("move on before " + name)
		intent := checkoutIntent(f, governing, name, branch)
		intent.Snapshot = snapshot
		return f.create(t, intent)
	}
	refuses := func(name, governing, branch, checkName, because string) {
		t.Helper()
		intent := checkoutIntent(f, governing, name, branch)
		intent.Snapshot = snapshot
		result, err := f.w.CreateCheckout(f.ctx, intent)
		if err == nil {
			t.Fatalf("%s created a checkout", name)
		}
		if got := check(t, result, checkName); got.Outcome != CheckRefused || !strings.Contains(got.Detail, because) {
			t.Fatalf("%s: %s = %+v", name, checkName, got)
		}
	}

	accepts("mine", mine.ID, "request/mine")
	accepts("proposal", proposal.ID, "request/proposal")
	accepts("closed", closed.ID, "request/closed")
	refuses("artifact", artifact.ID, "request/artifact", CheckRecordKind, "neither a request nor an adopted decision")
	refuses("theirs", theirs.ID, "request/theirs", CheckAddressee, "is addressed to")

	// The record kind of a request addressed to somebody else is still a
	// request. One refusal per fact: the addressee is what refused, and
	// saying the kind was wrong too would send a reader to fix the wrong
	// thing.
	intent := checkoutIntent(f, theirs.ID, "theirs", "request/theirs-again")
	intent.Snapshot = snapshot
	result, _ := f.w.CreateCheckout(f.ctx, intent)
	if got := check(t, result, CheckRecordKind); got.Outcome != CheckPassed {
		t.Fatalf("a request addressed elsewhere was graded %+v as a kind", got)
	}
}

func commitmentIn(projection workroom.Projection, request string) (workroom.Commitment, bool) {
	for _, commitment := range projection.Commitments {
		if commitment.Request == request {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

// ---------------------------------------------------------------------------
// Settled, stale, and the confirmation that never reaches the fold

// Settlement is the commitment's Status. Staleness is a separate qualifier
// beside it and is not a settlement: a stale request is one whose reasoning
// moved, and its work is still owed. Putting "stale" on the settled side
// would make every stale lane demand a flag.
func TestASettledCommitmentWarnsAndAMerelyStaleOneDoesNot(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("settlement")

	withStatus := func(status string, stale bool) Snapshot {
		snapshot := f.snapshot()
		for i := range snapshot.Projection.Commitments {
			if snapshot.Projection.Commitments[i].Request == request.ID {
				snapshot.Projection.Commitments[i].Status = status
				snapshot.Projection.Commitments[i].Stale = stale
				snapshot.Projection.Commitments[i].Report = "the-closing-report"
			}
		}
		return snapshot
	}

	// Merely stale: creates, with no confirmation flag and no settlement
	// warning, and the staleness is still recorded.
	intent := checkoutIntent(f, request.ID, "stale", "request/stale")
	intent.Snapshot = withStatus("stale", true)
	result := f.create(t, intent)
	if got := check(t, result, CheckSettlement); got.Outcome != CheckPassed || !strings.Contains(got.Detail, "not settlement") {
		t.Fatalf("a stale record was graded %+v", got)
	}

	// Settled: refused without the flag, and the refusal names the settling
	// event rather than only the word.
	f.advance("move on")
	intent = checkoutIntent(f, request.ID, "settled", "request/settled")
	intent.Snapshot = withStatus("satisfied", false)
	refused, err := f.w.CreateCheckout(f.ctx, intent)
	if err == nil {
		t.Fatal("a settled commitment created without confirmation")
	}
	got := check(t, refused, CheckSettlement)
	if got.Outcome != CheckRefused || !strings.Contains(got.Detail, "satisfied") {
		t.Fatalf("a settled commitment was graded %+v", got)
	}
	if !strings.Contains(got.Detail, "the-closing-report") {
		t.Fatalf("the warning names no settling event: %q", got.Detail)
	}

	// Settled, with the flag: creates, and the check still does not read as
	// consent from the record.
	intent.SettledOK = true
	confirmed := f.create(t, intent)
	got = check(t, confirmed, CheckSettlement)
	if got.Outcome != CheckWarned || !strings.Contains(got.Detail, "--settled-ok") {
		t.Fatalf("a confirmed settled commitment was graded %+v", got)
	}
	if strings.Contains(strings.Join(confirmed.Report(), "\n"), "settlement: passed") {
		t.Fatalf("a settled commitment read as passed: %v", confirmed.Report())
	}

	// A status word this vocabulary has never heard of counts as settled and
	// therefore warns. The consequence is a warning and a flag, not a refusal
	// to protect something, which is why the default runs this way here and
	// the other way for deletion.
	f.advance("move on again")
	intent = checkoutIntent(f, request.ID, "unknown", "request/unknown")
	intent.Snapshot = withStatus("some-future-status", false)
	if _, err := f.w.CreateCheckout(f.ctx, intent); err == nil {
		t.Fatal("an unrecognised status created without confirmation")
	}
	intent.SettledOK = true
	f.create(t, intent)
}

// The pair the adopted design names. With the confirmation flag present local
// creation succeeds, and the fold refuses the durable act exactly as it does
// today. Passing only the first half would prove nothing about the boundary
// the flag must never cross.
func TestTheConfirmationFlagNeverReachesDurableAdmission(t *testing.T) {
	f := newAssociationFixture(t)
	request := f.act("human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "finish it",
		Body:    map[string]string{"to": f.agent, "conditions": "done", "no_git_artifact": "true"},
		RestsOn: []string{f.seed.ID}, IdempotencyKey: "closing"})
	promise := f.act("agent", Act{Verb: VerbState, Kind: workroom.KindPromise, Text: "I will",
		RestsOn: []string{request.ID}, IdempotencyKey: "closing-promise"})
	report := f.act("agent", Act{Verb: VerbState, Kind: workroom.KindReport, Text: "done",
		RestsOn: []string{promise.ID}, IdempotencyKey: "closing-report"})
	f.act("human", Act{Verb: VerbRatify, Target: report.ID, IdempotencyKey: "closing-satisfy"})

	snapshot := f.snapshot()
	commitment, ok := commitmentIn(snapshot.Projection, request.ID)
	if !ok || commitment.Status != "satisfied" {
		t.Fatalf("the fixture did not settle its request: %+v", snapshot.Projection.Commitments)
	}
	// One act the fold does not admit, asked before the confirmed creation
	// and again after it. An uninvited reporter is refused for a reason that
	// has nothing to do with a settled lane, which is exactly why it measures
	// the flag: whatever the answer is, it must be the same answer.
	uninvited := func(key string) string {
		t.Helper()
		if _, _, err := f.w.AddActor(f.ctx, "human", "bystander", "agent"); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatal(err)
		}
		submission, err := f.w.Act(f.ctx, "bystander", Act{Verb: VerbState, Kind: workroom.KindReport,
			Text: "nobody asked me", RestsOn: []string{promise.ID}, IdempotencyKey: key})
		if err != nil {
			return "refused at the boundary"
		}
		decision, found := f.snapshot().Projection.Decision(submission.Record.ID)
		if found && decision.Verdict != workroom.Effective {
			return "recorded " + string(decision.Verdict)
		}
		return ""
	}
	before := uninvited("uninvited-before")

	intent := checkoutIntent(f, request.ID, "confirmed", "request/confirmed")
	intent.Snapshot, intent.SettledOK = snapshot, true
	// The frontier is read either side of creation and of nothing else, so
	// what it measures is creation.
	frontier := f.snapshot().Head
	result := f.create(t, intent)
	moved := f.snapshot().Head
	after := uninvited("uninvited-after")

	// Half one: local creation succeeded behind the flag.
	if !result.Created {
		t.Fatalf("the confirmed creation made nothing: %v", result.Report())
	}
	// Half two: the durable act the fold refuses is refused exactly as it was
	// a moment ago, with the flag in between. The flag is not a body field,
	// not a basis and not an input to admission; there is nowhere for it to
	// have reached. The act is one nobody was asked to file, which is a
	// refusal that has nothing to do with settlement — the point being that
	// the flag did not change admission at all, in either direction.
	if before != "" && before != after {
		t.Fatalf("the same durable act was answered %q before the confirmed creation and %q after it", before, after)
	}
	if after == "" {
		t.Fatal("the fold admitted an uninvited report; the pair proves nothing if the act was admissible anyway")
	}
	if moved != frontier {
		t.Fatalf("creation moved the durable frontier from %s to %s", frontier, moved)
	}
	if commitment, _ := commitmentIn(f.snapshot().Projection, request.ID); commitment.Status != "satisfied" {
		t.Fatalf("the settled commitment reads %q after a confirmed creation", commitment.Status)
	}
	// Nothing the flag touched is stored. A reader of the record cannot tell
	// whether a confirmation was given, because a confirmation is not a fact
	// about the work — so the record's fields are enumerated rather than
	// searched for words, and a field added for a flag would appear here.
	stored, err := os.ReadFile(result.RecordPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(stored, &fields); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"attempt", "attempt_ref", "branch", "genesis", "governing", "object_format"}
	if !slices.Equal(keys, want) {
		t.Fatalf("the record carries %v, want exactly %v", keys, want)
	}
}

// ---------------------------------------------------------------------------
// Duplicates

// A second checkout at one head is ordinary, so it is warned and confirmed
// rather than refused outright — and it takes a fresh attempt. Reusing the
// first attempt would make one ref name two lanes, which is exactly what the
// attempt suffix exists to prevent.
func TestASecondCheckoutAtOneHeadIsWarnedAndTakesAFreshAttempt(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("duplicate")
	first := f.create(t, checkoutIntent(f, request.ID, "first", "request/first"))

	second := checkoutIntent(f, request.ID, "second", "request/second")
	refused, err := f.w.CreateCheckout(f.ctx, second)
	if err == nil {
		t.Fatal("a duplicate head created without confirmation")
	}
	if got := check(t, refused, CheckDuplicateHead); got.Outcome != CheckRefused || !strings.Contains(got.Detail, "first") {
		t.Fatalf("duplicate head = %+v", got)
	}

	second.DuplicateOK = true
	created := f.create(t, second)
	if got := check(t, created, CheckDuplicateHead); got.Outcome != CheckWarned {
		t.Fatalf("a confirmed duplicate was graded %+v", got)
	}
	if created.Attempt.Number != 2 {
		t.Fatalf("the second checkout took attempt %d, want 2", created.Attempt.Number)
	}
	// The first attempt still names its own tip, and both refs are live.
	if value := f.git("rev-parse", first.Attempt.Ref); value != first.Attempt.Tip {
		t.Fatalf("%s moved to %s", first.Attempt.Ref, value)
	}
	if f.git("rev-parse", created.Attempt.Ref) != created.Attempt.Tip {
		t.Fatal("the second attempt does not name its tip")
	}
	// The confirmation flag is not visible to allocation. With the flag and
	// without it, a fresh checkout for this record takes the next number.
	third := checkoutIntent(f, request.ID, "third", "request/third")
	third.DuplicateOK = true
	if got := f.create(t, third); got.Attempt.Number != 3 {
		t.Fatalf("a third checkout took attempt %d, want 3", got.Attempt.Number)
	}
}

// ---------------------------------------------------------------------------
// Unreadable projection

// Unknown is not a negative and is not a consent. A projection that could not
// be read leaves the three record checks unestablished, and creation proceeds
// on a canonical identifier: what it makes grants nothing, and durable
// admission reads the durable record itself.
func TestAnUnreadableProjectionIsNeitherPassedNorFailed(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("unreadable")

	intent := checkoutIntent(f, request.ID, "unreadable", "request/unreadable")
	intent.Snapshot, intent.Unreadable = Snapshot{}, errors.New("the durable log could not be verified")
	result := f.create(t, intent)

	for _, name := range []string{CheckRecordKind, CheckAddressee, CheckSettlement} {
		got := check(t, result, name)
		if got.Outcome != CheckNotEstablished {
			t.Fatalf("%s = %+v, want not established", name, got)
		}
		if strings.Contains(got.String(), CheckPassed) {
			t.Fatalf("%s reads as passed: %q", name, got.String())
		}
		if strings.Contains(got.String(), CheckRefused) {
			t.Fatalf("%s reads as refused: %q", name, got.String())
		}
		if !strings.Contains(got.Detail, "could not read the durable record set") {
			t.Fatalf("%s does not say why it could not look: %q", name, got.Detail)
		}
	}
	// The determinate guards still fire. Four of them need no projection at
	// all, which is what makes proceeding safe.
	for _, name := range []string{CheckGoverningRecord, CheckDestination, CheckRepository, CheckBranch} {
		if got := check(t, result, name); got.Outcome != CheckPassed {
			t.Fatalf("%s did not run without a projection: %+v", name, got)
		}
	}
	if !result.Created || result.Record.Governing != request.ID {
		t.Fatalf("creation on a canonical identifier did not proceed: %+v", result)
	}

	// And the determinate refusals still refuse with no projection.
	blocked := checkoutIntent(f, request.ID, "unreadable", "request/blocked")
	blocked.Snapshot, blocked.Unreadable = Snapshot{}, errors.New("the durable log could not be verified")
	if _, err := f.w.CreateCheckout(f.ctx, blocked); err == nil {
		t.Fatal("an unreadable projection disabled the destination guard")
	}
}

// A `#N` or a hash fragment against an unreadable projection has no
// identifier to stamp, and the resolver every command already shares refuses
// it for the reason it has always refused: the durable event set could not be
// read. Nothing new was added to the refusal list to produce that.
func TestAShortSelectorAgainstAnUnreadableProjectionRefusesAtTheResolver(t *testing.T) {
	f := newAssociationFixture(t)
	room := eventref.Room{Genesis: f.w.config.Genesis, ObjectFormat: f.w.config.ObjectFormat}
	unreadable := errors.New("the durable log could not be verified")
	resolver := eventref.New(room, func() (eventref.Set, error) { return eventref.Set{}, unreadable })

	if _, err := resolver.One("#3"); err == nil || !errors.Is(err, unreadable) {
		t.Fatalf("a record number against an unreadable log answered %v", err)
	}
	if _, err := resolver.One("abcdef"); err == nil || !errors.Is(err, unreadable) {
		t.Fatalf("a hash fragment against an unreadable log answered %v", err)
	}
	// A canonical identifier is classified without reading anything, so it
	// comes back unchanged and creation can proceed on it.
	canonical := "git:sha1:" + f.w.config.Genesis + "#git:sha1:" + strings.Repeat("e", 40)
	if resolved, err := resolver.One(canonical); err != nil || resolved != canonical {
		t.Fatalf("a canonical identifier needed the log: %q %v", resolved, err)
	}
}

// ---------------------------------------------------------------------------
// Nothing is removed

// Removal is a separate deliberate act everywhere in this design, and
// creation is not an exception to it. A cleanup-on-failure path would be the
// one place in the whole layer that deletes a person's directory on an error
// nobody chose.
func TestCreationRemovesNothingEvenWhenItFailsPartWay(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("no-removal")
	created := f.create(t, checkoutIntent(f, request.ID, "kept", "request/kept"))

	// A creation that fails after the checkout exists leaves the checkout and
	// the branch, and says so. The failure here is attempt allocation with
	// every attempt under this record's hash already taken, which is the one
	// failure after the directory exists that a fixture can arrange without
	// a seam.
	hash := request.ID[strings.LastIndex(request.ID, ":")+1:]
	for attempt := 1; attempt <= checkoutAttemptLimit; attempt++ {
		f.git("update-ref", fmt.Sprintf("%s%s/%d", checkoutAttemptNamespace, hash, attempt), f.git("rev-parse", "HEAD"))
	}
	intent := checkoutIntent(f, request.ID, "half", "request/half")
	intent.DuplicateOK = true
	f.forgetCheckouts()
	partial, err := f.w.CreateCheckout(f.ctx, intent)
	if err == nil {
		t.Fatal("attempt allocation did not refuse with every attempt taken")
	}
	f.forgetCheckouts()
	if !partial.Created {
		t.Fatalf("the checkout was not made before the failure: %+v", partial)
	}
	if _, statErr := os.Stat(partial.Path); statErr != nil {
		t.Fatalf("a failed creation removed the checkout it had already made: %v", statErr)
	}
	if f.git("rev-parse", "refs/heads/"+partial.Branch) == "" {
		t.Fatal("a failed creation removed the branch it had already made")
	}
	for attempt := 1; attempt <= checkoutAttemptLimit; attempt++ {
		ref := fmt.Sprintf("%s%s/%d", checkoutAttemptNamespace, hash, attempt)
		if f.git("rev-parse", ref) == "" {
			t.Fatalf("a failed allocation removed %s", ref)
		}
	}

	// And the first checkout is untouched throughout.
	if _, err := os.Stat(created.Path); err != nil {
		t.Fatalf("an unrelated checkout was removed: %v", err)
	}
	if value := f.git("rev-parse", created.Attempt.Ref); value != created.Attempt.Tip {
		t.Fatalf("an unrelated attempt ref moved to %s", value)
	}
}

// ---------------------------------------------------------------------------
// Linked checkouts

// Two linked checkouts of one repository keep their own records, because the
// record's home is resolved against the created checkout rather than reused
// from the invoking one. Reusing the invoking checkout's private Git
// directory would make the second creation overwrite the first's record.
func TestTwoLinkedCheckoutsKeepTheirOwnRecordsAndAttempts(t *testing.T) {
	f := newAssociationFixture(t)
	first, _ := f.assign("linked-one")
	second, _ := f.assign("linked-two")

	one := f.create(t, checkoutIntent(f, first.ID, "one", "request/one"))
	twoIntent := checkoutIntent(f, second.ID, "two", "request/two")
	twoIntent.DuplicateOK = true
	two := f.create(t, twoIntent)

	if one.RecordPath == two.RecordPath {
		t.Fatalf("both checkouts wrote to %s", one.RecordPath)
	}
	oneRecord, err := f.w.readCheckoutRecord(f.ctx, one.Path)
	if err != nil {
		t.Fatal(err)
	}
	twoRecord, err := f.w.readCheckoutRecord(f.ctx, two.Path)
	if err != nil {
		t.Fatal(err)
	}
	if oneRecord.Governing != first.ID || twoRecord.Governing != second.ID {
		t.Fatalf("the records name %s and %s", oneRecord.Governing, twoRecord.Governing)
	}
	// Two governing records, so each gets its own attempt 1 under its own
	// hash; both are live.
	if one.Attempt.Ref == two.Attempt.Ref {
		t.Fatalf("two governing records share one attempt ref %s", one.Attempt.Ref)
	}
	for _, attempt := range []CheckoutAttempt{one.Attempt, two.Attempt} {
		if attempt.Number != 1 || f.git("rev-parse", attempt.Ref) != attempt.Tip {
			t.Fatalf("attempt %+v is not live at its tip", attempt)
		}
	}
	// The current checkout is not confused with either of them.
	f.forgetCheckouts()
	views := f.inventory()
	if current := f.view(views, filepath.Base(f.repo)); !current.Current {
		t.Fatalf("the served checkout is not current: %+v", current)
	}
	if f.view(views, "one").Current || f.view(views, "two").Current {
		t.Fatal("a created checkout reads as the current one")
	}
}

// ---------------------------------------------------------------------------
// The trailer template

// A trailer takes the full canonical identifier only. A commit message is not
// a boundary anything resolves at, so a template naming `#N` would produce a
// trailer no reader could follow.
func TestTheTemplateNamesTheFullCanonicalIdentifierWhateverWasTyped(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("typed")
	snapshot := f.snapshot()
	room := eventref.Room{Genesis: f.w.config.Genesis, ObjectFormat: f.w.config.ObjectFormat}
	resolver := eventref.New(room, func() (eventref.Set, error) {
		return eventref.FromProjection(room, snapshot.Projection), nil
	})

	number := ""
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == request.ID {
			number = fmt.Sprintf("#%d", statement.Sequence)
		}
	}
	if number == "" {
		t.Fatal("the fixture request has no record number")
	}
	governing, err := resolver.One(number)
	if err != nil {
		t.Fatal(err)
	}
	if governing != request.ID {
		t.Fatalf("%s resolved to %s, want %s", number, governing, request.ID)
	}

	result := f.create(t, checkoutIntent(f, governing, "typed", "request/typed"))
	template, err := os.ReadFile(result.TemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), request.ID) {
		t.Fatalf("the template does not name the full identifier: %q", template)
	}
	if strings.Contains(string(template), number) {
		t.Fatalf("the template carries the selector as typed: %q", template)
	}
	if result.Record.Governing != request.ID {
		t.Fatalf("the record carries %q", result.Record.Governing)
	}
}

// ---------------------------------------------------------------------------
// The configured checkout root

// One root, configured once, answering both questions: which destinations
// creation may use, and which existing checkouts containment protects. With
// nothing configured there is no boundary for a destination to be outside of,
// and the derived fallback that protects is not treated as though somebody had
// chosen it.
func TestTheConfiguredCheckoutRootBoundsCreationAndProtection(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("root")
	outer := filepath.Dir(f.repo)
	root := filepath.Join(outer, "roots", "a")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory whose name begins with the root's as a string and which is
	// not inside it as a directory. A prefix comparison would admit it.
	adjacent := filepath.Join(outer, "roots", "ab")
	if err := os.MkdirAll(adjacent, 0o755); err != nil {
		t.Fatal(err)
	}

	// Unconfigured: the check is not established, and it names the key that
	// would establish it. Creation proceeds.
	unconfigured := checkoutIntent(f, request.ID, "before", "request/before")
	result := f.create(t, unconfigured)
	if got := check(t, result, CheckCheckoutRoot); got.Outcome != CheckNotEstablished || !strings.Contains(got.Detail, checkoutRootKey) {
		t.Fatalf("with nothing configured, checkout root = %+v", got)
	}

	f.git("config", "--local", checkoutRootKey, root)
	f.forgetCheckouts()
	f.advance("move on")

	inside := checkoutIntent(f, request.ID, "unused", "request/inside")
	inside.Path = filepath.Join(root, "inside")
	created := f.create(t, inside)
	if got := check(t, created, CheckCheckoutRoot); got.Outcome != CheckPassed {
		t.Fatalf("a destination inside the configured root = %+v", got)
	}

	for _, destination := range []string{filepath.Join(adjacent, "x"), filepath.Join(outer, "elsewhere")} {
		intent := checkoutIntent(f, request.ID, "unused", "request/outside-"+filepath.Base(filepath.Dir(destination)))
		intent.Path = destination
		before := treeDigest(t, f.repo)
		refused, err := f.w.CreateCheckout(f.ctx, intent)
		if err == nil {
			t.Fatalf("%s was accepted outside the configured root", destination)
		}
		if got := check(t, refused, CheckCheckoutRoot); got.Outcome != CheckRefused || !strings.Contains(got.Detail, root) {
			t.Fatalf("%s: checkout root = %+v", destination, got)
		}
		if after := treeDigest(t, f.repo); after != before {
			t.Fatalf("%s: the refusal changed the repository", destination)
		}
	}

	// The same value governs protection. The checkout created before the key
	// was set is outside the configured root now, and layer 1 says so from
	// the same one root rather than from a second derivation of its own.
	f.forgetCheckouts()
	views := f.inventory()
	if before := f.view(views, "before"); !before.OutsideRoot {
		t.Fatalf("a checkout outside the configured root was not reported: %+v", before)
	}
	if within := f.view(views, "inside"); within.OutsideRoot {
		t.Fatalf("a checkout inside the configured root was reported outside it: %+v", within)
	}
}

// ---------------------------------------------------------------------------
// Reading a lane back

// The result the whole of layer 3 is for: find the lane again after its
// checkout is gone. The attempt ref is read back through the same captured
// observation and the same grader everything else is graded by, and what comes
// back is a claim. A ref can be written by anyone with repository write access
// and can point anywhere, so reading one back promotes nothing.
func TestAnAttemptRefIsReadBackAfterItsCheckoutAndBranchAreGone(t *testing.T) {
	f := newAssociationFixture(t)
	request, promise := f.assign("readback")
	created := f.create(t, checkoutIntent(f, request.ID, "gone", "request/gone"))

	// Corroboration first, while the checkout is still here: a signed
	// artifact naming this exact tip promotes the claim by the ordinary rule.
	f.artifact("readback-artifact", "internal/thing", created.Head, promise.ID)
	// The served checkout moves on, so the only checkout at the lane tip is
	// the one this test is about to take away.
	f.advance("move the served checkout off the lane tip")
	f.forgetCheckouts()
	table := f.associate(f.inventory())
	attempt := attemptRow(t, table, created.Attempt.Ref)
	if attempt.Governing != request.ID || attempt.Grade != GradeCorroborated {
		t.Fatalf("a signed artifact did not promote the attempt's claim: %+v", attempt)
	}
	if len(attempt.Checkouts) == 0 {
		t.Fatalf("the attempt names no checkout while its checkout is here: %+v", attempt)
	}
	record := recordRow(t, table, "gone")
	if record.Governing != request.ID || record.Grade != GradeCorroborated {
		t.Fatalf("the checkout record read back as %+v", record)
	}

	// Now take the checkout and the branch away.
	f.git("worktree", "remove", "--force", created.Path)
	f.git("branch", "-D", created.Branch)
	f.forgetCheckouts()

	table = f.associate(f.inventory())
	attempt = attemptRow(t, table, created.Attempt.Ref)
	if attempt.Governing != request.ID {
		t.Fatalf("the lane could not be found after its checkout was removed: %+v", attempt)
	}
	if attempt.Tip != created.Head || attempt.Attempt != 1 {
		t.Fatalf("the attempt lost its tip or its number: %+v", attempt)
	}
	if len(attempt.Checkouts) != 0 {
		t.Fatalf("a removed checkout is still named: %+v", attempt)
	}
	// The record went with the checkout, so nothing claims it from there any
	// more. The ref is what survived, which is its whole point.
	for _, row := range table.Records {
		if row.Checkout == "gone" {
			t.Fatalf("a removed checkout still has a record row: %+v", row)
		}
	}
	if attempt.Grade != GradeCorroborated {
		t.Fatalf("the surviving attempt graded %+v; the artifact still names its tip", attempt)
	}
}

// A ref this version does not recognise, and a hash naming no record here.
// Neither is guessed at.
func TestAnUnreadableAttemptRefIsReportedRatherThanGuessed(t *testing.T) {
	f := newAssociationFixture(t)
	head := f.git("rev-parse", "HEAD")
	missing := strings.Repeat("d", 40)
	f.git("update-ref", checkoutAttemptNamespace+missing+"/1", head)
	f.git("update-ref", checkoutAttemptNamespace+"not-a-hash/1", head)

	f.forgetCheckouts()
	table := f.associate(f.inventory())
	if row := attemptRow(t, table, checkoutAttemptNamespace+missing+"/1"); row.Grade != GradeUnresolved || row.Governing != "" {
		t.Fatalf("a hash naming no record here was resolved: %+v", row)
	}
	row := attemptRow(t, table, checkoutAttemptNamespace+"not-a-hash/1")
	if row.Grade != GradeUnresolved || row.Attempt != 0 {
		t.Fatalf("a ref name this version cannot read was parsed anyway: %+v", row)
	}
}

// A record copied from another workroom is carried as typed and never
// resolved locally, which is what every foreign citation in this repository
// gets. Reading it as this room's own is the failure the genesis exists to
// prevent.
func TestAForeignCheckoutRecordIsCarriedAsTypedByTheAssociation(t *testing.T) {
	first := newAssociationFixture(t)
	second := newAssociationFixture(t)
	firstRequest, _ := first.assign("foreign-origin")
	secondRequest, _ := second.assign("foreign-destination")
	origin := first.create(t, checkoutIntent(first, firstRequest.ID, "origin", "request/origin"))
	destination := second.create(t, checkoutIntent(second, secondRequest.ID, "destination", "request/destination"))

	// The positive control: before the copy, the record reads as this room's.
	second.forgetCheckouts()
	if row := recordRow(t, second.associate(second.inventory()), "destination"); row.Grade != GradeClaimed || row.Governing != secondRequest.ID {
		t.Fatalf("this room's own record graded %+v", row)
	}

	copied, err := os.ReadFile(origin.RecordPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination.RecordPath, copied, 0o600); err != nil {
		t.Fatal(err)
	}
	second.forgetCheckouts()
	row := recordRow(t, second.associate(second.inventory()), "destination")
	if row.Grade != GradeForeign || row.Governing != "" {
		t.Fatalf("a copied record resolved against this room: %+v", row)
	}
	if !strings.Contains(row.Reason, first.w.config.Genesis) {
		t.Fatalf("the foreign record's own workroom is not named: %+v", row)
	}
}

func attemptRow(t *testing.T, table AssociationTable, ref string) AssociationAttempt {
	t.Helper()
	if !table.Complete {
		t.Fatalf("the association did not finish: %s", table.Reason)
	}
	for _, attempt := range table.Attempts {
		if attempt.Ref == ref {
			return attempt
		}
	}
	t.Fatalf("no attempt row for %s in %+v", ref, table.Attempts)
	return AssociationAttempt{}
}

func recordRow(t *testing.T, table AssociationTable, checkout string) AssociationRecord {
	t.Helper()
	if !table.Complete {
		t.Fatalf("the association did not finish: %s", table.Reason)
	}
	for _, record := range table.Records {
		if record.Checkout == checkout {
			return record
		}
	}
	t.Fatalf("no record row for %s in %+v", checkout, table.Records)
	return AssociationRecord{}
}

// The record is an input the table is derived from, so it is an input to the
// cache key. A record changed while no ref and no listing entry moves would
// otherwise leave the cached answer wrong rather than stale.
func TestTheAssociationCacheIsKeyedOnTheCapturedRecordsToo(t *testing.T) {
	f := newAssociationFixture(t)
	first, _ := f.assign("cached-one")
	second, _ := f.assign("cached-two")
	created := f.create(t, checkoutIntent(f, first.ID, "cached", "request/cached"))

	f.forgetCheckouts()
	views := f.inventory()
	if row := recordRow(t, f.associate(views), "cached"); row.Governing != first.ID {
		t.Fatalf("the first reading did not see the record: %+v", row)
	}

	// Restamp the checkout for the other record, moving nothing else.
	record := created.Record
	record.Governing = second.ID
	if _, _, err := f.w.writeCheckoutRecord(f.ctx, created.Path, record); err != nil {
		t.Fatal(err)
	}
	f.forgetCheckouts()
	if row := recordRow(t, f.associate(f.inventory()), "cached"); row.Governing != second.ID {
		t.Fatalf("the cached answer survived a changed record: %+v", row)
	}
}

// An attempt ref reaches its tip, so a head one names is not a head only its
// checkout can reach. This is one protection fewer for such a checkout, and it
// is the truth about the head: removing the checkout does not take the commit
// with it.
func TestAHeadAnAttemptRefNamesIsNotARefless(t *testing.T) {
	f := newAssociationFixture(t)
	f.git("checkout", "-qb", "request/orphan")
	orphan := f.commit("work no branch will keep")
	f.git("checkout", "-q", "main")
	f.git("branch", "-D", "request/orphan")

	views := []WorktreeView{{Checkout: "orphaned", Head: orphan, State: "clean"}}
	snapshot := f.snapshot()
	if deletable := f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views); len(deletable) != 0 {
		t.Fatalf("a refless head was offered for deletion: %v", deletable)
	}
	if !views[0].ReflessHead {
		t.Fatalf("a head no ref points at was not reported: %+v", views[0])
	}

	if _, err := f.w.ClaimCheckoutAttempt(f.ctx, f.seed.ID, orphan); err != nil {
		t.Fatal(err)
	}
	views = []WorktreeView{{Checkout: "orphaned", Head: orphan, State: "clean"}}
	f.w.ClassifyWorktrees(f.ctx, snapshot.Projection, views)
	if views[0].ReflessHead {
		t.Fatalf("a head an attempt ref names still reads as refless: %+v", views[0])
	}
}

// Creation makes a branch. Attaching a new checkout to a branch somebody is
// already working on is a different act, and this command does not offer it,
// so a name already in use refuses before anything is written.
func TestAnExistingBranchRefusesAndAFreshOneCreates(t *testing.T) {
	f := newAssociationFixture(t)
	request, _ := f.assign("branches")
	f.git("branch", "request/taken", "HEAD")

	intent := checkoutIntent(f, request.ID, "taken", "request/taken")
	before := treeDigest(t, f.repo)
	result, err := f.w.CreateCheckout(f.ctx, intent)
	if err == nil {
		t.Fatal("a branch already in use was taken over")
	}
	if got := check(t, result, CheckBranch); got.Outcome != CheckRefused || !strings.Contains(got.Detail, "already exists") {
		t.Fatalf("branch = %+v", got)
	}
	if after := treeDigest(t, f.repo); after != before {
		t.Fatal("a refused creation changed the repository")
	}
	if _, statErr := os.Lstat(intent.Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a refused creation made its destination: %v", statErr)
	}

	// A name Git will not accept refuses too, and says so as a name rather
	// than as a collision.
	malformed := checkoutIntent(f, request.ID, "malformed", "request/..bad")
	if _, err := f.w.CreateCheckout(f.ctx, malformed); err == nil {
		t.Fatal("a malformed branch name was accepted")
	}

	// The positive control.
	f.create(t, checkoutIntent(f, request.ID, "fresh", "request/fresh"))
}
