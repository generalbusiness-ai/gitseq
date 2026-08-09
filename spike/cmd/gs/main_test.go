package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/workroom"
)

func TestValidateLoopbackListen(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1:7777": true,
		"[::1]:7777":     true,
		"localhost:7777": true,
		":7777":          false,
		"0.0.0.0:7777":   false,
		"192.0.2.1:7777": false,
		"not-an-address": false,
	}
	for address, want := range tests {
		t.Run(address, func(t *testing.T) {
			if got := validateLoopbackListen(address) == nil; got != want {
				t.Fatalf("validateLoopbackListen(%q) success = %v, want %v", address, got, want)
			}
		})
	}
}

func TestAttachAdvancesButRejectsRemoteRewind(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	auditor := filepath.Join(root, "auditor")

	testGit(t, "", "init", source)
	workspace, _, err := app.Init(ctx, source, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/seq/" + workspace.Config.Genesis
	first := testGit(t, source, "rev-parse", ref)
	testGit(t, "", "init", "--bare", remote)
	testGit(t, source, "remote", "add", "origin", remote)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")

	testGit(t, "", "clone", remote, auditor)
	testGit(t, auditor, "config", "--add", "remote.origin.fetch", forcedSequenceFetchRefspec)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != first {
		t.Fatalf("initial sequence head = %s, want %s", got, first)
	}
	fetchRules := strings.Fields(testGit(t, auditor, "config", "--get-all", "remote.origin.fetch"))
	if contains(fetchRules, forcedSequenceFetchRefspec) || !contains(fetchRules, sequenceFetchRefspec) {
		t.Fatalf("sequence fetch rules = %#v, want only non-forcing sequence rule", fetchRules)
	}

	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance",
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "advance",
	}); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, source, "rev-parse", ref)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("forward attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("forward sequence head = %s, want %s", got, second)
	}

	testGit(t, "", "--git-dir", remote, "update-ref", ref, first, second)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err == nil {
		t.Fatal("attach to rewound remote sequence succeeded")
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("rewind changed local sequence head to %s, want %s", got, second)
	}
	if output, err := exec.Command("git", "-C", auditor, "fetch", "origin").CombinedOutput(); err == nil {
		t.Fatalf("ordinary fetch accepted rewound sequence: %s", output)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("ordinary fetch rewound local sequence head to %s, want %s", got, second)
	}
}

func TestReviewGuardAcceptsExactCleanArtifactHead(t *testing.T) {
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	approval := fixture.review(t)
	after := fixture.snapshot(t)
	if after.Depth != before+1 {
		t.Fatalf("review depth = %d, want %d", after.Depth, before+1)
	}
	statement := statementByEvent(t, after.Projection, approval)
	if statement.Body["head"] != fixture.candidate || statement.Body["artifact"] != fixture.artifact || statement.Body["verdict"] != "approved" {
		t.Fatalf("review body = %#v", statement.Body)
	}
	if !contains(after.Projection.Provenance[approval], fixture.promise) || !contains(after.Projection.Provenance[approval], fixture.request) || !contains(after.Projection.Provenance[approval], fixture.artifact) {
		t.Fatalf("review provenance = %#v", after.Projection.Provenance[approval])
	}
}

func TestReviewGuardRefusesDirtyCheckoutBeforeVerdict(t *testing.T) {
	fixture := newWorkflowFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.feature, "feature.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "checkout is dirty") {
		t.Fatalf("dirty review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("dirty review signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesAdvancedCheckoutBeforeVerdict(t *testing.T) {
	fixture := newWorkflowFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.feature, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.feature, "add", "later.txt")
	testGit(t, fixture.feature, "commit", "-m", "advance feature")
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "does not equal artifact head") {
		t.Fatalf("advanced review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("advanced review signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesAnotherActorsPromise(t *testing.T) {
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "other-reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	foreignRequest, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review assigned to someone else",
		Body: map[string]string{
			"to": fixture.workspace.Config.Actors["other-reviewer"].Fingerprint, "conditions": "exact head",
		},
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "foreign-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignPromise, err := fixture.workspace.Act(fixture.ctx, "other-reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "another reviewer's promise",
		RestsOn: []string{foreignRequest.Record.ID}, IdempotencyKey: "foreign-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err = reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", foreignPromise.Record.ID,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "review actor did not make the named promise") {
		t.Fatalf("foreign promise review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("foreign promise signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesCheckoutFromAnotherRepository(t *testing.T) {
	fixture := newWorkflowFixture(t)
	foreign := filepath.Join(t.TempDir(), "foreign")
	testGit(t, "", "clone", fixture.repo, foreign)
	before := fixture.snapshot(t).Depth
	err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", foreign,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "checkout does not belong to the workroom repository") {
		t.Fatalf("foreign checkout review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("foreign checkout signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesBasisChangeBeforeSigning(t *testing.T) {
	fixture := newWorkflowFixture(t)
	calls := 0
	validate := func(ctx context.Context, workspace *app.Workspace, actorName, checkout, artifact, promise string) (string, string, error) {
		head, request, err := validateReview(ctx, workspace, actorName, checkout, artifact, promise)
		calls++
		if calls == 2 && err == nil {
			return head, request + "-changed", nil
		}
		return head, request, err
	}
	before := fixture.snapshot(t).Depth
	err := reviewCommandWithValidator(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	}, validate)
	if err == nil || !strings.Contains(err.Error(), "review basis changed while validating") {
		t.Fatalf("changed review basis error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("review validation calls = %d, want 2", calls)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("changed review basis signed a verdict: depth %d -> %d", before, after)
	}
}

func TestMergeGuardMergesOnlyRatifiedApprovedExactHead(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	}); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", fixture.repo, "merge-base", "--is-ancestor", fixture.candidate, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("approved candidate was not merged: %v: %s", err, output)
	}
}

func TestMergeGuardRefusesChangedCandidate(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", base, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "does not equal approved head") {
		t.Fatalf("changed candidate error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("refused merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeGuardRefusesStaleApproval(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	if _, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbSupersede, Target: fixture.promise, Text: "review promise withdrawn",
		IdempotencyKey: "retire-review-promise",
	}); err != nil {
		t.Fatal(err)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "stale or retired") {
		t.Fatalf("stale approval error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("stale approval merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeGuardRefusesUnratifiedApproval(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "not ratified") {
		t.Fatalf("unratified approval error = %v", err)
	}
}

func TestMergeGuardRefusesRatifiedChangesRequestedVerdict(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.reviewVerdict(t, "changes-requested")
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "review verdict is not approved") {
		t.Fatalf("changes-requested merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("changes-requested merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeGuardRefusesApprovalNotRestingOnNamedArtifact(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approvalSubmission, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "approval with an ungrounded artifact field",
		Body: map[string]string{
			"verdict": "approved", "head": fixture.candidate, "artifact": fixture.artifact,
		},
		RestsOn: []string{fixture.promise, fixture.request}, IdempotencyKey: "ungrounded-artifact-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := approvalSubmission.Record.ID
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "approval does not rest on its named artifact") {
		t.Fatalf("ungrounded artifact approval error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("ungrounded artifact merge moved HEAD to %s, want %s", got, base)
	}
}

type workflowFixture struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	feature   string
	workspace *app.Workspace
	candidate string
	artifact  string
	request   string
	promise   string
}

func newWorkflowFixture(t *testing.T) workflowFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	testGit(t, "", "init", "-b", "main", repo)
	testGit(t, repo, "config", "user.name", "Test")
	testGit(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", "base.txt")
	testGit(t, repo, "commit", "-m", "base")
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", "-b", "feature", feature)
	if err := os.WriteFile(filepath.Join(feature, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, feature, "add", "feature.txt")
	testGit(t, feature, "commit", "-m", "feature")
	candidate := testGit(t, feature, "rev-parse", "HEAD")
	artifactSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "feature artifact",
		Body:    map[string]string{"path": feature, "commit": candidate},
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	requestSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review feature",
		Body:    map[string]string{"to": workspace.Config.Actors["reviewer"].Fingerprint, "conditions": "exact head"},
		RestsOn: []string{artifactSubmission.Record.ID}, IdempotencyKey: "review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promiseSubmission, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review exact head",
		RestsOn: []string{requestSubmission.Record.ID}, IdempotencyKey: "review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workflowFixture{
		t: t, ctx: ctx, repo: repo, feature: feature, workspace: workspace,
		candidate: candidate, artifact: artifactSubmission.Record.ID,
		request: requestSubmission.Record.ID, promise: promiseSubmission.Record.ID,
	}
}

func (f workflowFixture) snapshot(t *testing.T) app.Snapshot {
	t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f workflowFixture) review(t *testing.T) string {
	return f.reviewVerdict(t, "approved")
}

func (f workflowFixture) reviewVerdict(t *testing.T, verdict string) string {
	t.Helper()
	before := f.snapshot(t).Depth
	if err := reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", f.artifact, "--promise", f.promise,
		"--verdict", verdict, "--text", strings.ToUpper(verdict) + " exact head",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("review depth = %d, want %d", snapshot.Depth, before+1)
	}
	return snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event
}

func (f workflowFixture) reviewError() error {
	return reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", f.artifact, "--promise", f.promise,
		"--verdict", "approved", "--text", "APPROVED exact head",
	})
}

func (f workflowFixture) ratify(t *testing.T, approval string) {
	t.Helper()
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-" + approval,
	}); err != nil {
		t.Fatal(err)
	}
}

func statementByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement
		}
	}
	t.Fatalf("statement %s not found", event)
	return workroom.Statement{}
}

func testGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	if repo != "" {
		arguments = append([]string{"-C", repo}, arguments...)
	}
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// The different-agent rule is a fingerprint test, applied where the verdict is
// signed rather than left to whoever remembers who did the work.
func TestReviewGuardRefusesVerdictOnTheReviewersOwnArtifact(t *testing.T) {
	fixture := newWorkflowFixture(t)
	own, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the reviewer's own implementation",
		Body:    map[string]string{"path": fixture.feature, "commit": fixture.candidate},
		RestsOn: []string{fixture.request}, IdempotencyKey: "self-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err = reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", own.Record.ID, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "an independent reviewer must sign the verdict") {
		t.Fatalf("self-review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("self-review signed a verdict: depth %d -> %d", before, after)
	}
}

// A verdict written around the guard still cannot be merged: the projection
// answers the independence question and merge reads the answer.
func TestMergeGuardRefusesApprovalSignedByTheImplementer(t *testing.T) {
	fixture := newWorkflowFixture(t)
	own, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the reviewer's own implementation",
		Body:    map[string]string{"path": fixture.feature, "commit": fixture.candidate},
		RestsOn: []string{fixture.request}, IdempotencyKey: "self-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "approving my own head",
		Body:    map[string]string{"verdict": "approved", "head": fixture.candidate, "artifact": own.Record.ID},
		RestsOn: []string{fixture.promise, fixture.request, own.Record.ID}, IdempotencyKey: "self-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.ratify(t, approval.Record.ID)
	review, found := fixture.snapshot(t).Projection.Review(approval.Record.ID)
	if !found || review.Independence != workroom.IndependenceSelfReview {
		t.Fatalf("projected review = %+v (found %v)", review, found)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval.Record.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "an independent review is required") {
		t.Fatalf("self-approved merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("refused merge moved HEAD to %s, want %s", got, base)
	}
}

// No command signs under a default name. The identity comes from the flag or
// from the environment that started the instance, and its absence is an error.
func TestSigningActorComesFromFlagThenEnvironmentAndOtherwiseFails(t *testing.T) {
	t.Setenv(actorEnvironment, "")
	if _, err := signingActor(""); err == nil || !strings.Contains(err.Error(), actorEnvironment) {
		t.Fatalf("missing identity error = %v", err)
	}
	if name, err := signingActor("explicit"); err != nil || name != "explicit" {
		t.Fatalf("flag identity = %q, %v", name, err)
	}
	t.Setenv(actorEnvironment, " claude.2 ")
	if name, err := signingActor(""); err != nil || name != "claude.2" {
		t.Fatalf("environment identity = %q, %v", name, err)
	}
	if name, err := signingActor("explicit"); err != nil || name != "explicit" {
		t.Fatalf("flag did not win over the environment: %q, %v", name, err)
	}
}

// The environment identity reaches a real durable act, not only the resolver.
func TestStateCommandSignsAsTheEnvironmentIdentity(t *testing.T) {
	fixture := newWorkflowFixture(t)
	t.Setenv(actorEnvironment, "reviewer")
	if err := stateCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--kind", "assert", "--text", "signed without --as",
		"--rests-on", fixture.artifact,
	}); err != nil {
		t.Fatal(err)
	}
	projection := fixture.snapshot(t).Projection
	last := projection.Statements[len(projection.Statements)-1]
	if last.Actor != fixture.workspace.Config.Actors["reviewer"].Fingerprint {
		t.Fatalf("environment identity signed as %s", last.Actor)
	}
}
