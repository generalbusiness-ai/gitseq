package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The landing obligation at the merge boundary. Every test here drives the
// real mergeCommand against a temporary repository, because the guards being
// proved are the ones that decide whether Git moves.

// stateV3 files one workroom/state@3 record. No filer writes that schema yet —
// I5 owns that — so these tests sign the payload the way any other surface
// would and submit it through the kernel. Nothing else about the act differs:
// it is admitted, sequenced and folded by exactly the rules a filer would meet.
func (f workflowFixture) stateV3(t *testing.T, actor string, kind workroom.Kind, text string, body map[string]string, restsOn ...string) string {
	t.Helper()
	_, private, err := f.workspace.Actor(actor)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{Kind: kind, Text: text, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := f.workspace.Store.WritePayloadTree(f.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := f.workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaStateV3, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        restsOn,
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: text,
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Submit(f.ctx, f.workspace.Store, kernel.Request{Signed: signed, Payload: payload},
		kernel.Options{SigningKey: view.SequencerKey}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	last := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if decision := decisionByEvent(t, snapshot.Projection, last.Event); decision.Verdict != workroom.Effective {
		t.Fatalf("state@3 %s record is %s: %s", kind, decision.Verdict, decision.Reason)
	}
	return last.Event
}

// testGitRaw is testGit without the trimming, for the one assertion that is
// about exact bytes: the merge commit message a receipt is parsed back out of.
func testGitRaw(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repo}, arguments...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func (f workflowFixture) fingerprint(t *testing.T, actor string) string {
	t.Helper()
	resolved, err := f.workspace.ResolveActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	return resolved.Fingerprint
}

// landingLane is one complete implementation lane whose request states its
// result under state@3: request, promise, an independent candidate commit, its
// reporting artifact, an independent review, and the ratified approval that
// merge consumes.
type landingLane struct {
	request   string
	candidate string
	approval  string
}

// buildLandingLane stages that lane on top of an existing workflow fixture.
// name keys every idempotency key and Git ref, so several lanes can coexist in
// one repository.
func (f workflowFixture) buildLandingLane(t *testing.T, name string, requestBody map[string]string, bases ...string) landingLane {
	t.Helper()
	if len(bases) == 0 {
		bases = []string{f.ground}
	}
	body := map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "publish the exact head"}
	for field, value := range requestBody {
		body[field] = value
	}
	request := f.stateV3(t, "reviewer", workroom.KindRequest, "implement "+name, body, bases...)
	promise, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name,
		RestsOn: []string{request}, IdempotencyKey: name + "-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(filepath.Dir(f.repo), name)
	testGit(t, f.repo, "worktree", "add", "-qb", name, checkout)
	if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, checkout, "add", name+".txt")
	testGit(t, checkout, "commit", "-qm", name)
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact",
		Body:    map[string]string{"path": name + ".txt", "commit": candidate},
		RestsOn: []string{promise.Record.ID, f.ground}, IdempotencyKey: name + "-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review " + name,
		Body:    map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head"},
		RestsOn: []string{artifact.Record.ID}, IdempotencyKey: name + "-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review " + name,
		RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: name + "-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", checkout,
		"--artifact", artifact.Record.ID, "--promise", reviewPromise.Record.ID,
		"--verdict", "approved", "--text", "APPROVED " + name,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	approval := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbRatify, Target: approval, IdempotencyKey: name + "-ratify-approval",
	}); err != nil {
		t.Fatal(err)
	}
	return landingLane{request: request, candidate: candidate, approval: approval}
}

// laneTarget is the destination the fold resolved for one request. The release
// states it, and the merge compares what it states against both this and the
// checkout, so the helper reads the same fact rather than restating a literal.
func (f workflowFixture) laneTarget(t *testing.T, request string) (repo, ref string) {
	t.Helper()
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == request {
			return commitment.TargetRepo, commitment.TargetRef
		}
	}
	t.Fatalf("no commitment lane for request %s", request)
	return "", ""
}

// release files the authorization request the performer owes and the hold
// owner's report answering it, then the performer's ratification: the exact
// durable shape section 3 recognises as lifting a hold.
func (f workflowFixture) release(t *testing.T, lane landingLane, owner string) string {
	return f.releaseWith(t, lane, owner, nil)
}

// releaseWith is release with the report body handed to mutate first, for the
// tests that need a release the fold admits and the merge must judge.
func (f workflowFixture) releaseWith(t *testing.T, lane landingLane, owner string, mutate func(map[string]string)) string {
	t.Helper()
	request := f.stateV3(t, "operator", workroom.KindRequest, "release the landing hold on "+lane.candidate,
		map[string]string{"to": f.fingerprint(t, owner), "no_git_artifact": "true", "conditions": "lift the hold for this candidate"},
		lane.request)
	targetRepo, targetRef := f.laneTarget(t, lane.request)
	body := map[string]string{
		"authorizes_candidate": lane.candidate,
		"authorizes_approval":  lane.approval,
		"authorizes_request":   lane.request,
		"target_pre_head":      testGit(t, f.repo, "rev-parse", targetRef),
		"target_repo":          targetRepo,
		"target_ref":           targetRef,
	}
	if mutate != nil {
		mutate(body)
	}
	report, err := f.workspace.Act(f.ctx, owner, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "release the hold for this exact candidate",
		Body: body, RestsOn: []string{request}, IdempotencyKey: "release-report-" + lane.candidate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbRatify, Target: report.Record.ID, IdempotencyKey: "ratify-release-" + lane.candidate,
	}); err != nil {
		t.Fatal(err)
	}
	return report.Record.ID
}

func (f workflowFixture) mergeLane(t *testing.T, lane landingLane, extra ...string) error {
	t.Helper()
	arguments := append([]string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", lane.candidate, "--approval", lane.approval,
		"--text", "Merge the approved lane and make it available.",
	}, extra...)
	return mergeCommand(f.ctx, arguments)
}

// captureStderr runs one merge with standard error redirected, so a warning
// that only exists as a side effect can be asserted on.
func captureStderr(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = writer
	runErr := run()
	os.Stderr = stderr
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(written), runErr
}

func mergeReceiptStatement(t *testing.T, f workflowFixture, approval string) workroom.Statement {
	t.Helper()
	for _, statement := range f.snapshot(t).Projection.Statements {
		if statement.Body["merge_approval"] == approval && statement.Body["merge_head"] != "" {
			return statement
		}
	}
	t.Fatalf("no durable merge receipt for approval %s", approval)
	return workroom.Statement{}
}

// TestMergeSealsTheTargetBindingInBothReceipts is the positive case the rest of
// this file measures against: an ordinary merge names its destination in the
// Git trailers and in the durable receipt, and the two agree.
func TestMergeSealsTheTargetBindingInBothReceipts(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{
		Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
		PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}
	// The commit is built with commit-tree rather than committed through HEAD,
	// so the message bytes are asserted rather than assumed: a receipt is read
	// back out of them, and the plan they carry is the one the merge saw.
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, target.PreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	plan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, target.PreHead, fixture.candidate, nil))
	want, err := mergeReceiptMessage("Merge the approved feature and seal where it landed.",
		approval, "", "", fixture.candidate, target, "", false, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the approved feature and seal where it landed.",
	}); err != nil {
		t.Fatal(err)
	}
	head := testGit(t, fixture.repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if got := testGitRaw(t, fixture.repo, "log", "-1", "--format=%B", head); got != want+"\n" {
		t.Fatalf("merge commit message =\n%q\nwant\n%q", got, want+"\n")
	}
	wantRepo := mergeplan.WorkroomRepo(fixture.workspace)
	if receipt.TargetRepo != wantRepo || receipt.TargetRef != "refs/heads/main" {
		t.Fatalf("sealed Git target = %s %s, want %s refs/heads/main", receipt.TargetRepo, receipt.TargetRef, wantRepo)
	}
	if receipt.HoldWarning != "" {
		t.Fatalf("unheld legacy merge recorded a hold warning %q", receipt.HoldWarning)
	}
	statement := mergeReceiptStatement(t, fixture, approval)
	if statement.Body["merge_target_repo"] != wantRepo || statement.Body["merge_target_ref"] != "refs/heads/main" {
		t.Fatalf("durable target = %s %s, want %s refs/heads/main",
			statement.Body["merge_target_repo"], statement.Body["merge_target_ref"], wantRepo)
	}
	if _, recorded := statement.Body["merge_hold_warning"]; recorded {
		t.Fatal("durable receipt recorded a hold warning for an unheld merge")
	}
}

// TestMergeRefusesADetachedCheckout proves the first section-6 refusal. A
// detached checkout has no branch to land into, so there is nothing a receipt
// could truthfully name.
func TestMergeRefusesADetachedCheckout(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	testGit(t, fixture.repo, "checkout", "--detach", "HEAD")
	beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a merge into a detached checkout.",
	})
	if err == nil || !strings.Contains(err.Error(), "checkout is detached") {
		t.Fatalf("detached merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("detached refusal moved HEAD %s -> %s", beforeHead, got)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("detached refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// TestMergeRefusesACheckoutOnAnotherBranch proves the second. A request filed
// before state@3 owes its landing to refs/heads/main of this workroom, so any
// other branch is somewhere the request never named.
func TestMergeRefusesACheckoutOnAnotherBranch(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	testGit(t, fixture.repo, "switch", "-qc", "release-2")
	beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a merge into a branch the request never named.",
	})
	if err == nil || !strings.Contains(err.Error(), "checkout is on refs/heads/release-2") ||
		!strings.Contains(err.Error(), "owes its landing to refs/heads/main") {
		t.Fatalf("wrong-branch merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("wrong-branch refusal moved HEAD %s -> %s", beforeHead, got)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("wrong-branch refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// TestMergeStatedTargetLandsOnItsOwnRefAndNowhereElse covers a request that
// states its triple by value: the named branch lands, and the branch beside it
// refuses even though both are clean checkouts of the same repository.
func TestMergeStatedTargetLandsOnItsOwnRefAndNowhereElse(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	testGit(t, fixture.repo, "branch", "release-2")
	lane := fixture.buildLandingLane(t, "stated", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/release-2",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
	})
	if err := fixture.mergeLane(t, lane); err == nil ||
		!strings.Contains(err.Error(), "owes its landing to refs/heads/release-2") {
		t.Fatalf("merge on refs/heads/main error = %v", err)
	}
	testGit(t, fixture.repo, "switch", "-q", "release-2")
	if err := fixture.mergeLane(t, lane); err != nil {
		t.Fatalf("merge on the stated target: %v", err)
	}
	head := testGit(t, fixture.repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.TargetRef != "refs/heads/release-2" {
		t.Fatalf("sealed target ref = %q, want refs/heads/release-2", receipt.TargetRef)
	}
}

// TestMergeInheritedTargetLandsOnTheParentsRef proves the merge reads I1's
// resolved fact rather than the child request's own body: the child states
// target=inherit and nothing else, and the destination is still enforced.
func TestMergeInheritedTargetLandsOnTheParentsRef(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	testGit(t, fixture.repo, "branch", "release-2")
	parent := fixture.stateV3(t, "reviewer", workroom.KindRequest, "parent owing release-2", map[string]string{
		"to":          fixture.fingerprint(t, "operator"),
		"conditions":  "the parent obligation",
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/release-2",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}, fixture.ground)
	lane := fixture.buildLandingLane(t, "inherited", map[string]string{"target": "inherit"}, parent)
	if err := fixture.mergeLane(t, lane); err == nil ||
		!strings.Contains(err.Error(), "owes its landing to refs/heads/release-2") {
		t.Fatalf("inherited-target merge on main error = %v", err)
	}
	testGit(t, fixture.repo, "switch", "-q", "release-2")
	if err := fixture.mergeLane(t, lane); err != nil {
		t.Fatalf("merge on the inherited target: %v", err)
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, testGit(t, fixture.repo, "rev-parse", "HEAD"))
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.TargetRef != "refs/heads/release-2" {
		t.Fatalf("sealed target ref = %q, want the inherited refs/heads/release-2", receipt.TargetRef)
	}
}

// TestMergeRefusesUnsolicitedAuthorization is section 3's rule. An unheld
// state@3 request asked for no release, so a merge that presents one is
// claiming an authority nobody granted.
func TestMergeRefusesUnsolicitedAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	lane := fixture.buildLandingLane(t, "unheld", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
	})
	authorization := fixture.release(t, lane, "reviewer")
	before := fixture.snapshot(t)
	err := fixture.mergeLane(t, lane, "--authorization", authorization)
	if err == nil || !strings.Contains(err.Error(), "is not held; drop --authorization") {
		t.Fatalf("unsolicited authorization error = %v", err)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("unsolicited authorization refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
	if err := fixture.mergeLane(t, lane); err != nil {
		t.Fatalf("the same merge without --authorization: %v", err)
	}
}

// TestMergeWarnsAndRecordsAHeldLandingWithoutARelease is the compatibility
// window of section 9. For one release the missing release is said out loud
// and written into both receipts instead of refusing the merge.
func TestMergeWarnsAndRecordsAHeldLandingWithoutARelease(t *testing.T) {
	// Not parallel: it reads the process-wide standard error, and only the
	// other sequential tests that do the same could take it away.
	fixture := newWorkflowFixture(t)
	lane := fixture.buildLandingLane(t, "held", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
		"landing":     "held",
	})
	warning, err := captureStderr(t, func() error { return fixture.mergeLane(t, lane) })
	if err != nil {
		t.Fatalf("held merge under the compatibility window: %v", err)
	}
	if !strings.Contains(warning, "landing is held and no effective release") {
		t.Fatalf("held merge stderr = %q, want the compatibility-window warning", warning)
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, testGit(t, fixture.repo, "rev-parse", "HEAD"))
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.HoldWarning != "true" {
		t.Fatalf("sealed Gitseq-Hold-Warning = %q, want true", receipt.HoldWarning)
	}
	if got := mergeReceiptStatement(t, fixture, lane.approval).Body["merge_hold_warning"]; got != "true" {
		t.Fatalf("durable merge_hold_warning = %q, want true", got)
	}
}

// TestMergeOfAReleasedHoldRecordsNoWarning is the other half of the same rule:
// with the release in force the window is not used, and neither receipt says
// it was.
func TestMergeOfAReleasedHoldRecordsNoWarning(t *testing.T) {
	t.Parallel()
	// The proof here is what the two receipts record, not what standard error
	// happened to contain: an absence cannot be asserted on a stream every
	// other merge in this package also writes to.
	fixture := newWorkflowFixture(t)
	lane := fixture.buildLandingLane(t, "released", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
		"landing":     "held",
	})
	release := fixture.release(t, lane, "reviewer")
	if got := commitmentRelease(t, fixture, lane.request); got != release {
		t.Fatalf("the fold's release for the held landing request = %q, want %s", got, release)
	}
	if err := fixture.mergeLane(t, lane); err != nil {
		t.Fatalf("merge of a released hold: %v", err)
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, testGit(t, fixture.repo, "rev-parse", "HEAD"))
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.HoldWarning != "" {
		t.Fatalf("released hold recorded Gitseq-Hold-Warning %q", receipt.HoldWarning)
	}
	statement := mergeReceiptStatement(t, fixture, lane.approval)
	if _, recorded := statement.Body["merge_hold_warning"]; recorded {
		t.Fatal("released hold recorded merge_hold_warning in the durable receipt")
	}
	// A landing of a released hold seals exactly that release, whether or not
	// the caller named it: the receipt is where this merge's authority is
	// written down.
	if receipt.Authorization != release || receipt.AuthorizationRatification == "" {
		t.Fatalf("sealed Git authorization = %q/%q, want the release %s and its witness",
			receipt.Authorization, receipt.AuthorizationRatification, release)
	}
	if statement.Body["merge_authorization"] != release {
		t.Fatalf("durable merge_authorization = %q, want the release %s", statement.Body["merge_authorization"], release)
	}
	if statement.Body["merge_authorization_ratification"] != receipt.AuthorizationRatification {
		t.Fatalf("durable merge_authorization_ratification = %q, want %q",
			statement.Body["merge_authorization_ratification"], receipt.AuthorizationRatification)
	}
}

// TestMergeRefusesAnAuthorizationThatIsNotTheReleaseInForce keeps the sealing
// exact. A held request is released by one report; a merge that names some
// other report is not landing under the release the hold owner signed.
func TestMergeRefusesAnAuthorizationThatIsNotTheReleaseInForce(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	lane := fixture.buildLandingLane(t, "misnamed", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(fixture.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, fixture.repo, "rev-parse", "HEAD"),
		"landing":     "held",
	})
	release := fixture.release(t, lane, "reviewer")
	other := fixture.authorize(t, lane.approval, true, nil)
	before := fixture.snapshot(t)
	err := fixture.mergeLane(t, lane, "--authorization", other)
	if err == nil || !strings.Contains(err.Error(), "is released by "+release) {
		t.Fatalf("misnamed authorization error = %v", err)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("misnamed authorization refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

func commitmentRelease(t *testing.T, f workflowFixture, request string) string {
	t.Helper()
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == request && commitment.Release != "" {
			return commitment.Release
		}
	}
	return ""
}

// TestMergeRefusesWhenTheTargetRefMovesAfterPlanning proves the re-resolution
// covers the ref and not only the head. The plan was computed for one branch
// and the checkout is on another by the time the merge would reserve.
func TestMergeRefusesWhenTheTargetRefMovesAfterPlanning(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	before := fixture.snapshot(t)
	previous := buildMergePlan
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		result := previous(ctx, workspace, checkout, candidate, approval, merger, signer)
		if result.Allowed {
			testGit(t, checkout, "switch", "-qc", "moved-after-planning")
		}
		return result
	}
	t.Cleanup(func() { buildMergePlan = previous })

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a plan whose target branch moved before reservation.",
	})
	if err == nil || !strings.Contains(err.Error(), "merge target ref moved after planning") {
		t.Fatalf("moving-ref merge error = %v", err)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("moving-ref refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("moving-ref refusal left a receipt reservation")
	}
}

// TestMergeRefusesWhenTheTargetMovesBeforeTheLanding is the compare-and-swap.
// The tentative merge is staged and the reservation taken when the target
// branch is advanced underneath it; the ref is not at its sealed pre-head any
// more, so it is not advanced at all and the concurrent move stands.
func TestMergeRefusesWhenTheTargetMovesBeforeTheLanding(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
	previous := readStagedMergeChanges
	readStagedMergeChanges = func(ctx context.Context, checkout string) ([]mergeChange, error) {
		changes, err := previous(ctx, checkout)
		testGit(t, fixture.repo, "update-ref", "refs/heads/main", fixture.candidate)
		return changes, err
	}
	t.Cleanup(func() { readStagedMergeChanges = previous })

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a target that moved while the merge was staged.",
	})
	if err == nil || !strings.Contains(err.Error(), "advance refs/heads/main from the sealed pre-head "+beforeHead) {
		t.Fatalf("late-movement merge error = %v", err)
	}
	// The concurrent move keeps the ref. A landing that could not take the
	// branch from where it sealed it must not take it from anywhere else.
	if got := testGit(t, fixture.repo, "rev-parse", "refs/heads/main"); got != fixture.candidate {
		t.Fatalf("refused landing left refs/heads/main at %s, want the concurrent move %s", got, fixture.candidate)
	}
	if _, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, "refs/heads/main"); ok || err != nil {
		t.Fatalf("a merge receipt is reachable from the target ref after a refused landing: ok=%v err=%v", ok, err)
	}
	testGit(t, fixture.repo, "update-ref", "refs/heads/main", beforeHead)
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("late-movement refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// TestMergeLandsOnTheSealedRefWhenAHookRetargetsHead is planner's blocker,
// reproduced. A repository hook that points HEAD at another branch used to
// move the whole merge there while the receipt went on naming the branch the
// command had measured. The landing is a compare-and-swap on the sealed ref
// now, so HEAD has no say in where it goes.
func TestMergeLandsOnTheSealedRefWhenAHookRetargetsHead(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "branch", "raced-branch")
	hooks := t.TempDir()
	testGit(t, fixture.repo, "config", "core.hooksPath", hooks)
	hook := "#!/bin/sh\ngit symbolic-ref HEAD refs/heads/raced-branch\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Verify the destination across the final commit boundary.",
	}); err != nil {
		t.Fatal(err)
	}
	landed := testGit(t, fixture.repo, "rev-parse", "refs/heads/main")
	if landed == before {
		t.Fatal("the merge did not advance the sealed target ref")
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, landed)
	if err != nil || !ok {
		t.Fatalf("read the landed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.TargetRef != "refs/heads/main" {
		t.Fatalf("sealed target ref = %q, want refs/heads/main", receipt.TargetRef)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "refs/heads/raced-branch"); got != before {
		t.Fatalf("the merge landed on raced-branch at %s, want it left at %s", got, before)
	}
	// The hook still ran nothing here — the landing writes no commit through
	// HEAD — so this checkout is exactly where the merge put it.
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != landed {
		t.Fatalf("checkout HEAD = %s, want the landed merge %s", got, landed)
	}
}

// sealReceipt writes one merge commit carrying exactly the receipt these
// recovery tests need, without going through the merge command.
func sealReceipt(t *testing.T, fixture workflowFixture, approval string, target mergeplan.Target, holdWarning bool) string {
	t.Helper()
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, target.PreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	plan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, target.PreHead, fixture.candidate, nil))
	message, err := mergeReceiptMessage("Seal a receipt for recovery.", approval, "", "", fixture.candidate, target, "", holdWarning, plan)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
	head := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), head, "")
	return head
}

// TestMergeResumeRefusesAReceiptSealedForAnotherDestination is the recovery
// side of the binding. Both halves are proved: a ref that names another
// branch, and a repository half that names another workroom.
func TestMergeResumeRefusesAReceiptSealedForAnotherDestination(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		target mergeplan.Target
		want   string
	}{
		"another branch": {
			target: mergeplan.Target{Ref: "refs/heads/release-2"},
			want:   "merge receipt landed on refs/heads/release-2",
		},
		"another repository": {
			target: mergeplan.Target{Repo: "git:sha1:" + strings.Repeat("b", 40)},
			want:   "merge receipt landed on refs/heads/main of git:sha1:" + strings.Repeat("b", 40),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			target := mergeplan.Target{
				Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
				PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
			}
			if test.target.Ref != "" {
				target.Ref = test.target.Ref
			}
			if test.target.Repo != "" {
				target.Repo = test.target.Repo
			}
			sealReceipt(t, fixture, approval, target, false)
			before := fixture.snapshot(t)
			err := mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", approval,
				"--text", "Resume a receipt sealed somewhere else.",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("foreign-destination resume error = %v, want %q", err, test.want)
			}
			if after := fixture.snapshot(t); after.Depth != before.Depth {
				t.Fatalf("foreign-destination refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
			}
		})
	}
}

// TestMergeResumeRefusesAHalfSealedTargetBinding separates a legacy receipt,
// which names no destination at all, from a malformed one that names half of
// it and proves nothing.
func TestMergeResumeRefusesAHalfSealedTargetBinding(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{
		Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
		PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}
	head := sealReceipt(t, fixture, approval, target, false)
	message := testGit(t, fixture.repo, "show", "-s", "--format=%B", head)
	testGit(t, fixture.repo, "commit", "--amend", "-m",
		strings.Replace(message, "\n"+mergeplan.TargetRefTrailer+target.Ref, "", 1))
	amended := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), amended, head)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume a half-sealed receipt.",
	})
	if err == nil || !strings.Contains(err.Error(), "Gitseq-Target-Repo and Gitseq-Target-Ref together") {
		t.Fatalf("half-sealed resume error = %v", err)
	}
}

// TestMergeResumeRefusesADurableReceiptDisagreeingWithTheTrailers is the
// tamper check, once per sealed field. The destination lives in two places — a
// signed durable receipt and a Git commit message — and recovery reads both
// and requires them equal before appending anything further.
func TestMergeResumeRefusesADurableReceiptDisagreeingWithTheTrailers(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		sealedHoldWarning bool
		forge             func(body map[string]string)
		want              string
	}{
		"target ref": {
			forge: func(body map[string]string) { body["merge_target_ref"] = "refs/heads/release-2" },
			want:  "recorded merge target does not match the sealed Git receipt",
		},
		"target repository": {
			forge: func(body map[string]string) { body["merge_target_repo"] = "git:sha1:" + strings.Repeat("b", 40) },
			want:  "recorded merge target does not match the sealed Git receipt",
		},
		"target pre-head": {
			forge: func(body map[string]string) { body["merge_target_pre_head"] = strings.Repeat("c", 40) },
			want:  "recorded merge target pre-head does not match the sealed Git receipt",
		},
		"hold warning": {
			sealedHoldWarning: true,
			forge:             func(body map[string]string) { delete(body, "merge_hold_warning") },
			want:              "recorded merge hold warning does not match the sealed Git receipt",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			target := mergeplan.Target{
				Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
				PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
			}
			head := sealReceipt(t, fixture, approval, target, test.sealedHoldWarning)
			sealed, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
			if err != nil || !ok {
				t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
			}
			body := map[string]string{
				"merge_approval": approval, "merge_candidate": fixture.candidate,
				"merge_target_pre_head": target.PreHead, "merge_head": head,
				"merge_retirements": sealed.Retirements, "merge_successors": sealed.Successors,
				"merge_target_repo": target.Repo, "merge_target_ref": target.Ref,
			}
			if test.sealedHoldWarning {
				body["merge_hold_warning"] = "true"
			}
			test.forge(body)
			if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "approved candidate merged",
				Body: body, RestsOn: []string{approval}, IdempotencyKey: "disagreeing-receipt", AllowDeadBasis: true,
			}); err != nil {
				t.Fatal(err)
			}
			err = mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", approval,
				"--text", "Resume a receipt whose two halves disagree.",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("disagreeing receipt resume error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestMergeResumeRefusesACheckoutFromAnotherRepository binds recovery to this
// workroom's repository. A clone carries the sealed merge commit, so the
// receipt is found and its destination matches; only the checkout's own
// identity separates it from the repository this workroom governs.
func TestMergeResumeRefusesACheckoutFromAnotherRepository(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{
		Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
		PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}
	sealReceipt(t, fixture, approval, target, false)
	clone := filepath.Join(filepath.Dir(fixture.repo), "clone")
	testGit(t, fixture.repo, "clone", "-q", "--", fixture.repo, clone)
	before := fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", clone,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume a sealed receipt from a clone.",
	})
	if err == nil || !strings.Contains(err.Error(), "checkout does not belong to the workroom repository") {
		t.Fatalf("foreign-checkout resume error = %v", err)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("foreign-checkout refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// TestMergeResumesAnUntamperedReceiptCarryingTheNewFields is the same recovery
// path with nothing rewritten: the durable suffix lands, and the receipt keeps
// the destination it sealed.
func TestMergeResumesAnUntamperedReceiptCarryingTheNewFields(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{
		Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
		PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}
	head := sealReceipt(t, fixture, approval, target, false)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume the sealed receipt.",
	}); err != nil {
		t.Fatalf("resume an untampered sealed receipt: %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("resume moved HEAD %s -> %s", head, got)
	}
	statement := mergeReceiptStatement(t, fixture, approval)
	if statement.Body["merge_target_repo"] != target.Repo || statement.Body["merge_target_ref"] != target.Ref {
		t.Fatalf("resumed durable target = %s %s, want %s %s",
			statement.Body["merge_target_repo"], statement.Body["merge_target_ref"], target.Repo, target.Ref)
	}
}

// TestMergeResumesALegacyReceiptWithNoTargetFields is section 9's receipt
// reading. A receipt sealed before the destination was part of one names none,
// reads as refs/heads/main of this workroom, and resumes without acquiring
// fields its author never signed.
func TestMergeResumesALegacyReceiptWithNoTargetFields(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD")}
	head := sealReceipt(t, fixture, approval, target, false)
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
	if err != nil || !ok || !receipt.Legacy() {
		t.Fatalf("legacy receipt: ok=%v legacy=%v err=%v", ok, receipt.Legacy(), err)
	}
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume a legacy receipt.",
	}); err != nil {
		t.Fatalf("resume a legacy receipt: %v", err)
	}
	statement := mergeReceiptStatement(t, fixture, approval)
	if _, present := statement.Body["merge_target_repo"]; present {
		t.Fatalf("legacy resume invented a target repository: %v", statement.Body)
	}
	if _, present := statement.Body["merge_target_ref"]; present {
		t.Fatalf("legacy resume invented a target ref: %v", statement.Body)
	}
}

// TestMergeResumeRefusesALegacyReceiptOnAnotherBranch completes that reading.
// A legacy receipt landed on refs/heads/main here, so recovery of one from a
// checkout standing somewhere else is refused rather than silently accepted.
func TestMergeResumeRefusesALegacyReceiptOnAnotherBranch(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD")}
	head := sealReceipt(t, fixture, approval, target, false)
	testGit(t, fixture.repo, "switch", "-qc", "release-2")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume a legacy receipt from another branch.",
	})
	if err == nil || !strings.Contains(err.Error(), "merge receipt landed on refs/heads/main") {
		t.Fatalf("legacy resume from another branch error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("refused legacy resume moved HEAD %s -> %s", head, got)
	}
}

// TestMergeResumeRefusesAnUnknownHoldWarningValue keeps the window marker a
// fact rather than free text. Recovery reads it back out of a commit message,
// so anything but the exact value it writes is refused.
func TestMergeResumeRefusesAnUnknownHoldWarningValue(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := mergeplan.Target{
		Repo: mergeplan.WorkroomRepo(fixture.workspace), Ref: "refs/heads/main",
		PreHead: testGit(t, fixture.repo, "rev-parse", "HEAD"),
	}
	head := sealReceipt(t, fixture, approval, target, true)
	message := testGit(t, fixture.repo, "show", "-s", "--format=%B", head)
	testGit(t, fixture.repo, "commit", "--amend", "-m",
		strings.Replace(message, mergeplan.HoldWarningTrailer+"true", mergeplan.HoldWarningTrailer+"maybe", 1))
	amended := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), amended, head)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume a receipt whose window marker is not a fact.",
	})
	if err == nil || !strings.Contains(err.Error(), "Gitseq-Hold-Warning is \"maybe\"") {
		t.Fatalf("unknown hold-warning value resume error = %v", err)
	}
}

// firstMainUpdateHook installs a reference-transaction hook that runs body
// once, after the first committed update of refs/heads/main, inside the
// checkout. That is the instant just after the landing's compare-and-swap,
// where anything the checkout cleanup writes would collide with a concurrent
// actor; body is that actor. It leaves a marker file so the test can prove it
// fired.
func firstMainUpdateHook(t *testing.T, repo, body string) string {
	t.Helper()
	hooks := t.TempDir()
	testGit(t, repo, "config", "core.hooksPath", hooks)
	fired := filepath.Join(hooks, "fired")
	script := "#!/bin/sh\n[ \"$1\" = committed ] || exit 0\nwhile read old new ref; do\n" +
		"  [ \"$ref\" = refs/heads/main ] || continue\n" +
		"  [ ! -e '" + fired + "' ] || continue\n" +
		"  touch '" + fired + "'\n" +
		body + "\ndone\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return fired
}

// assertMergeStateForgotten checks the checkout finished its merge without
// writing a ref: no MERGE_HEAD remains, and the index and working tree hold
// exactly the landed tree.
func assertMergeStateForgotten(t *testing.T, repo, landed string) {
	t.Helper()
	if _, err := exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", "MERGE_HEAD").Output(); err == nil {
		t.Fatal("MERGE_HEAD still present after the merge landed")
	}
	testGit(t, repo, "diff", "--quiet", "--cached", landed)
	testGit(t, repo, "diff", "--quiet", landed)
}

func TestMergeKeepsAFastForwardLandedAfterTheSwap(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	hooks := t.TempDir()
	advanced := filepath.Join(hooks, "advanced")
	fired := firstMainUpdateHook(t, fixture.repo,
		"  next=$(git commit-tree \"$new^{tree}\" -p \"$new\" -m 'Concurrent advance after the swap') || exit 1\n"+
			"  printf '%s' \"$next\" > '"+advanced+"'\n"+
			"  git update-ref refs/heads/main \"$next\" \"$new\" || exit 1")
	stderr, err := captureStderr(t, func() error {
		return mergeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
			"--candidate", fixture.candidate, "--approval", approval,
			"--text", "Keep a concurrent advance landed after the swap.",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fired); err != nil {
		t.Fatal("the concurrent advance never ran; the test proves nothing")
	}
	child, err := os.ReadFile(advanced)
	if err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "refs/heads/main"); got != string(child) {
		t.Fatalf("refs/heads/main = %s, want the concurrent advance %s kept", got, child)
	}
	landed := testGit(t, fixture.repo, "rev-parse", string(child)+"^")
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, landed)
	if err != nil || !ok || receipt.Approval != approval {
		t.Fatalf("the advance's parent %s is not the landed merge: ok=%v err=%v", landed, ok, err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", mergeReceiptRef(approval)); got != landed {
		t.Fatalf("receipt ref = %s, want the landed merge %s", got, landed)
	}
	assertMergeStateForgotten(t, fixture.repo, landed)
	if !strings.Contains(stderr, "moved on to "+string(child)) {
		t.Fatalf("stderr does not report the target moving on:\n%s", stderr)
	}
}

func TestMergeLeavesASwitchedHeadsBranchAloneAfterTheSwap(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "branch", "raced-branch")
	fired := firstMainUpdateHook(t, fixture.repo, "  git symbolic-ref HEAD refs/heads/raced-branch || exit 1")
	stderr, err := captureStderr(t, func() error {
		return mergeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
			"--candidate", fixture.candidate, "--approval", approval,
			"--text", "Leave a branch HEAD switched to after the swap alone.",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fired); err != nil {
		t.Fatal("the HEAD switch never ran; the test proves nothing")
	}
	landed := testGit(t, fixture.repo, "rev-parse", "refs/heads/main")
	if landed == before {
		t.Fatal("the merge did not advance the sealed target ref")
	}
	if got := testGit(t, fixture.repo, "rev-parse", "refs/heads/raced-branch"); got != before {
		t.Fatalf("raced-branch = %s, want it left at %s", got, before)
	}
	if got := testGit(t, fixture.repo, "symbolic-ref", "HEAD"); got != "refs/heads/raced-branch" {
		t.Fatalf("HEAD = %s, want the switch to raced-branch left standing", got)
	}
	assertMergeStateForgotten(t, fixture.repo, landed)
	if !strings.Contains(stderr, "no longer stands on it") {
		t.Fatalf("stderr does not report the switched HEAD:\n%s", stderr)
	}
}

// addActor puts one more actor on the roster, with the roles named, and
// returns its fingerprint. The hold-owner rule turns on who signs, so these
// tests need actors that differ from the fixture's requester and performer.
func (f workflowFixture) addActor(t *testing.T, name string, roles ...string) string {
	t.Helper()
	if _, _, err := f.workspace.AddActor(f.ctx, "operator", name, "agent"); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if _, err := f.workspace.GrantRole(f.ctx, "operator", name, role); err != nil {
			t.Fatal(err)
		}
	}
	return f.fingerprint(t, name)
}

// heldLane stages a landing lane held on behalf of one named owner.
func (f workflowFixture) heldLane(t *testing.T, name, owner string) landingLane {
	t.Helper()
	return f.buildLandingLane(t, name, map[string]string{
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
		"landing":     "held",
		"hold_owner":  f.fingerprint(t, owner),
	})
}

// TestMergeAdmitsAHoldOwnerOutsideThePhaseOneSignerList is the signing rule the
// landing obligation changes. A held request delegates its release to one actor
// by name, and that name is the whole authority: the owner here is neither the
// implementation requester, nor the actor called planner, nor a ratifier, and
// the phase-one list would have refused the release the request itself asked
// for.
func TestMergeAdmitsAHoldOwnerOutsideThePhaseOneSignerList(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	owner := fixture.addActor(t, "custodian")
	lane := fixture.heldLane(t, "delegated", "custodian")
	if got := fixture.snapshot(t).Projection.Actors[owner]; slices.Contains(got.Roles, "ratifier") || got.Name == "planner" {
		t.Fatalf("the hold owner carries phase-one authority already: %+v", got)
	}
	release := fixture.release(t, lane, "custodian")
	if got := commitmentRelease(t, fixture, lane.request); got != release {
		t.Fatalf("the fold's release = %q, want %s", got, release)
	}
	if err := fixture.mergeLane(t, lane); err != nil {
		t.Fatalf("merge released by its delegated hold owner: %v", err)
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, testGit(t, fixture.repo, "rev-parse", "HEAD"))
	if err != nil || !ok {
		t.Fatalf("read sealed receipt: ok=%v err=%v", ok, err)
	}
	if receipt.Authorization != release {
		t.Fatalf("sealed authorization = %q, want the release %s", receipt.Authorization, release)
	}
}

// TestMergeRefusesAReleaseFromARetiredHoldOwner is the other half of the same
// rule. The fold admitted the release while its owner was on the roster and
// that decision is immutable, so the merge is where a delegation whose holder
// has since been retired stops: an authority read out of a retired fingerprint
// belongs to nobody.
func TestMergeRefusesAReleaseFromARetiredHoldOwner(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	fixture.addActor(t, "custodian")
	lane := fixture.heldLane(t, "retired-owner", "custodian")
	release := fixture.release(t, lane, "custodian")
	if got := commitmentRelease(t, fixture, lane.request); got != release {
		t.Fatalf("the fold's release = %q, want %s", got, release)
	}
	if _, err := fixture.workspace.RetireActor(fixture.ctx, "operator", "custodian"); err != nil {
		t.Fatal(err)
	}
	beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
	err := fixture.mergeLane(t, lane)
	if err == nil || !strings.Contains(err.Error(), "is not a live roster actor") {
		t.Fatalf("retired hold owner error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("retired-owner refusal moved HEAD %s -> %s", beforeHead, got)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("retired-owner refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// TestMergeRefusesAHeldLandingAuthorizedByThePhaseOneList closes the
// compatibility window's hole. The window exists so a held lane with no release
// can still land, and it did that by warning; it must not also let some other
// actor's authorization be sealed as the authority for the landing. Each signer
// here is one of the three the phase-one list admits, and none of them owns
// this hold.
func TestMergeRefusesAHeldLandingAuthorizedByThePhaseOneList(t *testing.T) {
	t.Parallel()
	for _, signer := range []string{"reviewer", "planner", "governor"} {
		t.Run(signer, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			fixture.addActor(t, "custodian")
			switch signer {
			case "planner":
				fixture.addActor(t, "planner")
			case "governor":
				fixture.addActor(t, "governor", "ratifier")
			}
			lane := fixture.heldLane(t, "phase-one-"+signer, "custodian")
			authorization := fixture.releaseWith(t, lane, signer, nil)
			if got := commitmentRelease(t, fixture, lane.request); got != "" {
				t.Fatalf("a non-owner lifted the hold: release = %s", got)
			}
			before := fixture.snapshot(t)
			err := fixture.mergeLane(t, lane, "--authorization", authorization)
			if err == nil || !strings.Contains(err.Error(), "--authorization must be that release, signed by the hold owner") {
				t.Fatalf("phase-one authorization on a held lane error = %v", err)
			}
			if after := fixture.snapshot(t); after.Depth != before.Depth {
				t.Fatalf("refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
			}
			// The window itself is untouched: the same held lane with no
			// authorization named still warns and lands.
			if _, err := captureStderr(t, func() error { return fixture.mergeLane(t, lane) }); err != nil {
				t.Fatalf("the compatibility window no longer lands a held lane: %v", err)
			}
		})
	}
}

// TestMergeRequiresTheAuthorizationToNameItsLandingTarget binds a release to a
// destination. A report that names only a pre-head says nothing about which
// branch of which repository held it, so one signed for a landing here would
// read as authority for the same commit landing anywhere.
func TestMergeRequiresTheAuthorizationToNameItsLandingTarget(t *testing.T) {
	t.Parallel()
	t.Run("held state@3 release must state both", func(t *testing.T) {
		t.Parallel()
		fixture := newWorkflowFixture(t)
		lane := fixture.heldLane(t, "unbound", "reviewer")
		release := fixture.releaseWith(t, lane, "reviewer", func(body map[string]string) {
			delete(body, "target_repo")
			delete(body, "target_ref")
		})
		// The fold reads the two fields as optional, so this release is in
		// force and the merge is the boundary that requires them.
		if got := commitmentRelease(t, fixture, lane.request); got != release {
			t.Fatalf("the fold's release = %q, want %s", got, release)
		}
		before := fixture.snapshot(t)
		err := fixture.mergeLane(t, lane)
		if err == nil || !strings.Contains(err.Error(), "authorization must state target_repo and target_ref") {
			t.Fatalf("unbound release error = %v", err)
		}
		if after := fixture.snapshot(t); after.Depth != before.Depth {
			t.Fatalf("unbound-release refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
		}
	})
	// A pre-state@3 lane may state the destination or not, and a stated value
	// that disagrees is a disagreement whatever schema admitted the request.
	for name, test := range map[string]struct {
		mutate func(map[string]string)
		want   string
	}{
		"legacy ref elsewhere": {
			mutate: func(body map[string]string) { body["target_ref"] = "refs/heads/release-2" },
			want:   "owes its landing to refs/heads/main",
		},
		"legacy repository elsewhere": {
			mutate: func(body map[string]string) { body["target_repo"] = "git:sha1:" + strings.Repeat("b", 40) },
			want:   "owes its landing to repository",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			target := fixture.measuredTarget(t)
			authorization := fixture.authorize(t, approval, true, func(body map[string]string) {
				body["target_repo"], body["target_ref"] = target.Repo, target.Ref
				test.mutate(body)
			})
			before := fixture.snapshot(t)
			err := mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
				"--text", "An authorization naming another destination must not move this one.",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s error = %v, want %q", name, err, test.want)
			}
			if after := fixture.snapshot(t); after.Depth != before.Depth {
				t.Fatalf("%s refusal changed workroom depth %d -> %d", name, before.Depth, after.Depth)
			}
		})
	}
}

// TestMergeAcceptsALegacyAuthorizationStatingItsDestination is the positive
// case beside them: the two fields are optional on a pre-state@3 lane, and
// stating them correctly changes nothing about the merge.
func TestMergeAcceptsALegacyAuthorizationStatingItsDestination(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	target := fixture.measuredTarget(t)
	authorization := fixture.authorize(t, approval, true, func(body map[string]string) {
		body["target_repo"], body["target_ref"] = target.Repo, target.Ref
	})
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Merge under an authorization that names where it lands.",
	}); err != nil {
		t.Fatalf("legacy authorization stating its destination: %v", err)
	}
}

// TestMergeAuthorizationRefusesADestinationTheCheckoutIsNotOn is the third
// comparison, which recovery needs. The fresh merge path proves the checkout
// equals the request's target before it ever reaches this guard, so this is
// the resume path's protection: a receipt sealed elsewhere revalidates its
// authorization against the destination it sealed, and an authorization
// agreeing only with the request would pass unexamined.
func TestMergeAuthorizationRefusesADestinationTheCheckoutIsNotOn(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, func(body map[string]string) {
		body["target_repo"] = mergeplan.WorkroomRepo(fixture.workspace)
		body["target_ref"] = "refs/heads/release-2"
	})
	snapshot := fixture.snapshot(t)
	for index := range snapshot.Projection.Commitments {
		if snapshot.Projection.Commitments[index].Request == fixture.implementationRequest {
			snapshot.Projection.Commitments[index].TargetRef = "refs/heads/release-2"
		}
	}
	_, err := validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, fixture.candidate, approval,
		authorization, fixture.measuredTarget(t), true, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "but the checkout is on refs/heads/main") {
		t.Fatalf("checkout-destination error = %v", err)
	}
}

// TestMergeAuthorizationRefusesASignerWhoIsNotTheHoldOwner states the rule
// itself, at the merge boundary. The fold already refuses a release signed by
// anyone but the owner, so no merge command can reach this comparison with a
// real record; the merge is nonetheless the last reader of the delegation
// before Git moves, and it must not stand on the fold alone. The hold is moved
// to another actor in the projection this validation reads, which is the only
// way to put a real release in front of a hold it does not own.
func TestMergeAuthorizationRefusesASignerWhoIsNotTheHoldOwner(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	stranger := fixture.addActor(t, "custodian")
	lane := fixture.heldLane(t, "reassigned-hold", "reviewer")
	release := fixture.release(t, lane, "reviewer")
	snapshot := fixture.snapshot(t)
	for index := range snapshot.Projection.Commitments {
		if snapshot.Projection.Commitments[index].Request == lane.request {
			snapshot.Projection.Commitments[index].HoldOwner = stranger
		}
	}
	_, err := validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, lane.candidate, lane.approval,
		release, fixture.measuredTarget(t), true, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "is not the hold owner "+stranger) {
		t.Fatalf("non-owner signer error = %v", err)
	}
}

// TestMergeRefusesAReleaseMeasuredAgainstAMovedTarget is the merge-time half of
// the two re-resolutions. The release measured the target ref; between its
// ratification and the merge the ref moved, so what the hold owner looked at is
// no longer what the landing would join.
func TestMergeRefusesAReleaseMeasuredAgainstAMovedTarget(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	lane := fixture.heldLane(t, "moved", "reviewer")
	release := fixture.release(t, lane, "reviewer")
	measured := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if got := statementByEvent(t, fixture.snapshot(t).Projection, release).Body["target_pre_head"]; got != measured {
		t.Fatalf("release target_pre_head = %s, want the measured head %s", got, measured)
	}
	if err := os.WriteFile(filepath.Join(fixture.repo, "moved.txt"), []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "moved.txt")
	testGit(t, fixture.repo, "commit", "-qm", "advance the target after the release")
	before := fixture.snapshot(t)
	err := fixture.mergeLane(t, lane)
	if err == nil || !strings.Contains(err.Error(), "remeasure is not disjoint-paths") {
		t.Fatalf("moved-target release error = %v", err)
	}
	if after := fixture.snapshot(t); after.Depth != before.Depth {
		t.Fatalf("moved-target refusal changed workroom depth %d -> %d", before.Depth, after.Depth)
	}
}

// ownRequest files one request addressed to the reviewer, so a report can close
// a commitment of its own rather than intruding on the implementation lane.
func (f workflowFixture) ownRequest(t *testing.T, key, text string) string {
	t.Helper()
	request, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: text,
		Body:    map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": text},
		RestsOn: []string{f.artifact}, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return request.Record.ID
}

// fileAuthorizationReport files one authorization-shaped report through the
// real gs state command, which is where the filing-time target check lives.
func fileAuthorizationReport(t *testing.T, fixture workflowFixture, request, key string, body map[string]string) error {
	t.Helper()
	arguments := []string{"--repo", fixture.repo, "--as", "reviewer", "--kind", "report",
		"--text", "release the hold", "--rests-on", request, "--idempotency-key", key}
	for field, value := range body {
		if value == "" {
			continue
		}
		arguments = append(arguments, "--body", field+"="+value)
	}
	return stateCommand(fixture.ctx, arguments)
}

// TestStateReresolvesTheTargetOfAnAuthorizationReport is the filing-time half
// of the two re-resolutions. A force-push between the measurement and the
// signature is refused where it happened, rather than being signed, ratified,
// and then refused by whoever runs the merge.
func TestStateReresolvesTheTargetOfAnAuthorizationReport(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(fixture.repo, "moved.txt"), []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "moved.txt")
	testGit(t, fixture.repo, "commit", "-qm", "advance the target before the report is filed")
	moved := testGit(t, fixture.repo, "rev-parse", "HEAD")
	unrelated := testGit(t, fixture.repo, "rev-parse", fixture.candidate)

	body := func(overrides map[string]string) map[string]string {
		fields := map[string]string{
			"authorizes_request": fixture.implementationRequest,
			"target_ref":         "refs/heads/main",
			"target_pre_head":    base,
		}
		for field, value := range overrides {
			fields[field] = value
		}
		return fields
	}
	for name, test := range map[string]struct {
		body map[string]string
		want string
	}{
		"the ref moved": {
			body: body(nil),
			want: "refs/heads/main is at " + moved + ", but target_pre_head is " + base,
		},
		"no measurement at all": {
			body: body(map[string]string{"target_pre_head": ""}),
			want: "names target_ref refs/heads/main without target_pre_head",
		},
		"an abbreviated measurement": {
			body: body(map[string]string{"target_pre_head": base[:12]}),
			want: "target_pre_head: candidate must be a full lowercase commit object ID",
		},
		"a remeasurement from off the ref": {
			body: body(map[string]string{"target_pre_head": unrelated, "remeasure": "disjoint-paths"}),
			want: "is not an ancestor of target_ref refs/heads/main",
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := fixture.ownRequest(t, "authorization-request-"+name, "release the hold "+name)
			before := fixture.snapshot(t)
			err := fileAuthorizationReport(t, fixture, request, name, test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s error = %v, want %q", name, err, test.want)
			}
			if after := fixture.snapshot(t); after.Depth != before.Depth {
				t.Fatalf("%s refusal appended to the log: depth %d -> %d", name, before.Depth, after.Depth)
			}
		})
	}
	t.Run("the ref where the signer measured it", func(t *testing.T) {
		request := fixture.ownRequest(t, "authorization-request-current", "release the hold now")
		if err := fileAuthorizationReport(t, fixture, request, "current",
			body(map[string]string{"target_pre_head": moved})); err != nil {
			t.Fatalf("filing a report measured against the current ref: %v", err)
		}
	})
	t.Run("a stated disjoint-paths remeasurement", func(t *testing.T) {
		request := fixture.ownRequest(t, "authorization-request-remeasured", "release the hold as remeasured")
		if err := fileAuthorizationReport(t, fixture, request, "remeasured",
			body(map[string]string{"remeasure": "disjoint-paths"})); err != nil {
			t.Fatalf("filing a remeasured report: %v", err)
		}
	})
}

// TestMergeRefusesEveryNonApprovalReportAsAuthority is blocker 3. Merge
// authority is a ratified approval naming the reviewed artifact at its exact
// head, and nothing else: not an ordinary report, not the resolution report the
// landing obligation admits as nonterminal evidence, and not the release that
// lifts a hold. Each is a live ratified report on a commitment of its own,
// carrying the artifact and head an approval carries, and each is fed to
// --approval in the position an approval occupies.
func TestMergeRefusesEveryNonApprovalReportAsAuthority(t *testing.T) {
	t.Parallel()
	for name, extra := range map[string]func(fixture workflowFixture, approval string) map[string]string{
		"an ordinary report": func(workflowFixture, string) map[string]string { return nil },
		"a resolution report": func(workflowFixture, string) map[string]string {
			return map[string]string{"resolution": "carried", "reason": "the work stands where it was"}
		},
		"a release": func(fixture workflowFixture, approval string) map[string]string {
			return map[string]string{
				"authorizes_candidate": fixture.candidate,
				"authorizes_approval":  approval,
				"authorizes_request":   fixture.implementationRequest,
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			body := map[string]string{"artifact": fixture.artifact, "head": fixture.candidate}
			for field, value := range extra(fixture, approval) {
				body[field] = value
			}
			request := fixture.ownRequest(t, "non-approval-request-"+name, "say where "+name+" stands")
			report, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
				Verb: app.VerbState, Kind: workroom.KindReport, Text: "not an approval",
				Body: body, RestsOn: []string{request, fixture.artifact},
				IdempotencyKey: "non-approval-" + name,
			})
			if err != nil {
				t.Fatal(err)
			}
			fixture.ratify(t, report.Record.ID)
			if statement := statementByEvent(t, fixture.snapshot(t).Projection, report.Record.ID); !statement.Ratified {
				t.Fatalf("%s is not a live ratified report; the test would prove nothing", name)
			}
			beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
			err = mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", report.Record.ID,
				"--text", "Only a ratified approval is merge authority.",
			})
			if err == nil || !strings.Contains(err.Error(), "review verdict is not approved") {
				t.Fatalf("%s as --approval error = %v", name, err)
			}
			if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != beforeHead {
				t.Fatalf("%s moved HEAD %s -> %s", name, beforeHead, got)
			}
			if after := fixture.snapshot(t); after.Depth != before.Depth {
				t.Fatalf("%s changed workroom depth %d -> %d", name, before.Depth, after.Depth)
			}
		})
	}
}
