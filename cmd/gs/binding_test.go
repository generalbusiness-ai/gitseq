package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// evidenceLane is the I2 control's shape: a state@3 request that owes no Git
// artifact, promised by its addressee, who then publishes a candidate artifact
// against that promise. The fold correctly leaves Commitment.Report empty.
type evidenceLane struct {
	request, promise, artifact, candidate, checkout, reviewPromise string
}

func (f workflowFixture) buildEvidenceLane(t *testing.T, name string) evidenceLane {
	t.Helper()
	request := f.stateV3(t, "reviewer", workroom.KindRequest, "show evidence for "+name,
		map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "publish evidence", "no_git_artifact": "true"}, f.ground)
	promise, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "evidence for " + name,
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
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " evidence",
		Body:    map[string]string{"path": name + ".txt", "commit": candidate},
		RestsOn: []string{promise.Record.ID, f.ground}, IdempotencyKey: name + "-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review " + name,
		Body:    map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"},
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
	return evidenceLane{request: request, promise: promise.Record.ID, artifact: artifact.Record.ID, candidate: candidate, checkout: checkout, reviewPromise: reviewPromise.Record.ID}
}

func (f workflowFixture) lastEvent(t *testing.T) string {
	t.Helper()
	snapshot := f.snapshot(t)
	return snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event
}

// The I2 control, replayed through ordinary review and merge: an evidence
// artifact cannot be signed as an assigned delivery, can be signed as
// evidence, and that approval lands nothing — the merge refuses before Git or
// the workroom moves, with or without --authorization.
func TestEvidenceOnlyReviewIsValidButNeverLands(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildEvidenceLane(t, "binding-evidence")
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", lane.checkout, "--artifact", lane.artifact, "--promise", lane.reviewPromise}
	err := reviewCommand(f.ctx, append(base, "--verdict", "approved", "--text", "APPROVED as delivery"))
	if err == nil || !strings.Contains(err.Error(), "owes no Git artifact; it cannot be a delivery primary") {
		t.Fatalf("assigned review of evidence: %v", err)
	}
	err = reviewCommand(f.ctx, append(base, "--self-initiated", lane.request, "--verdict", "approved", "--text", "APPROVED as own work"))
	if err == nil || !strings.Contains(err.Error(), "does not become self-initiated") {
		t.Fatalf("self-initiated relabel of evidence: %v", err)
	}
	if err := reviewCommand(f.ctx, append(base, "--evidence-only", "--verdict", "approved", "--text", "APPROVED evidence")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	snapshot := f.snapshot(t)
	verdict, _ := mergeplan.StandingStatement(snapshot.Projection, approval, workroom.KindReport)
	if verdict.Body[reviewguard.BodyBinding] != reviewguard.BindingEvidenceOnly {
		t.Fatalf("verdict body = %v", verdict.Body)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-evidence-approval"}); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "checkout", "-qb", "wrong-destination")
	before := testGit(t, f.repo, "rev-parse", "HEAD")
	depth := f.snapshot(t).Depth
	for _, extra := range [][]string{nil, {"--authorization", approval}} {
		err = mergeCommand(f.ctx, append([]string{
			"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
			"--candidate", lane.candidate, "--approval", approval, "--text", "land the evidence",
		}, extra...))
		if err == nil || !strings.Contains(err.Error(), "evidence-only review") {
			t.Fatalf("merge of evidence approval (extra %v): %v", extra, err)
		}
		if after := testGit(t, f.repo, "rev-parse", "HEAD"); after != before || f.snapshot(t).Depth != depth {
			t.Fatal("refusal mutated Git or the workroom")
		}
	}
	plan := mergeplan.Build(f.ctx, f.workspace, f.repo, lane.candidate, approval, f.fingerprint(t, "operator"), mergeplan.Signer{})
	if plan.Allowed || !strings.Contains(plan.Reasons[len(plan.Reasons)-1].Reason, "evidence-only") {
		t.Fatalf("merge plan admitted an evidence-only approval: %+v", plan.Reasons)
	}
}

// A verdict signed before bindings were recorded is reclassified from its
// actual primary: zero report matches is not grandfathered as self-initiation.
func TestLegacyZeroMatchApprovalDoesNotLandAsSelfInitiated(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildEvidenceLane(t, "binding-legacy")
	// A hand-shaped legacy approval carrying no binding fields, filed through
	// the generic path the fold accepted before the guard existed.
	snapshot := f.snapshot(t)
	basis, news, err := reviewguard.ReviewBasis(reviewguard.Read{Projection: snapshot.Projection, ReviewerFingerprint: f.fingerprint(t, "reviewer"), FrontierEvent: f.workspace.EventID(snapshot.Head), NoCheckout: true}, lane.artifact, lane.reviewPromise)
	if err != nil {
		t.Fatal(err)
	}
	body, restsOn, err := reviewguard.Build(basis, reviewguard.VerdictApproved, "APPROVED legacy", []string{lane.artifact}, news)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindReport, Text: "APPROVED legacy", Body: body, RestsOn: restsOn, GuardedReview: true, IdempotencyKey: "legacy-approval"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval.Record.ID, IdempotencyKey: "ratify-legacy-approval"}); err != nil {
		t.Fatal(err)
	}
	before := testGit(t, f.repo, "rev-parse", "HEAD")
	depth := f.snapshot(t).Depth
	err = mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", lane.candidate, "--approval", approval.Record.ID, "--text", "land legacy"})
	if err == nil || !strings.Contains(err.Error(), "cannot be a delivery primary") {
		t.Fatalf("legacy zero-match merge: %v", err)
	}
	if after := testGit(t, f.repo, "rev-parse", "HEAD"); after != before || f.snapshot(t).Depth != depth {
		t.Fatal("refusal mutated Git or the workroom")
	}
}

// Self-initiated work needs its positive witness: a ratified proposal the
// primary rests on directly. With it the review signs and the merge lands
// without a commitment to close; without it nothing is inferred.
func TestSelfInitiatedReviewNeedsTheAdoptedDecisionWitness(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	proposal, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPropose, Text: "adopt the tidy-up", RestsOn: []string{f.ground}, IdempotencyKey: "tidy-proposal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: proposal.Record.ID, IdempotencyKey: "ratify-tidy"}); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "tidy")
	testGit(t, f.repo, "worktree", "add", "-qb", "tidy", checkout)
	if err := os.WriteFile(filepath.Join(checkout, "tidy.txt"), []byte("tidy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, checkout, "add", "tidy.txt")
	testGit(t, checkout, "commit", "-qm", "tidy")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "tidy artifact", Body: map[string]string{"path": "tidy.txt", "commit": candidate}, RestsOn: []string{proposal.Record.ID}, IdempotencyKey: "tidy-artifact"})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review tidy", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifact.Record.ID}, IdempotencyKey: "tidy-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review tidy", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "tidy-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--artifact", artifact.Record.ID, "--promise", reviewPromise.Record.ID}
	err = reviewCommand(f.ctx, append(base, "--verdict", "approved", "--text", "APPROVED without witness"))
	if err == nil || !strings.Contains(err.Error(), "rests on no request or promise of its author") {
		t.Fatalf("zero-match review without a witness: %v", err)
	}
	if err := reviewCommand(f.ctx, append(base, "--self-initiated", proposal.Record.ID, "--verdict", "approved", "--text", "APPROVED own work")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-tidy-approval"}); err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", candidate, "--approval", approval, "--text", "land the tidy-up"}); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "merge-base", "--is-ancestor", candidate, testGit(t, f.repo, "rev-parse", "HEAD"))
}

// Two assigned implementations at one candidate keep both reports and every
// examined companion; the primary must be the first selected report.
func TestCombinedImplementationsKeepBothReportsAndRefuseAReorderedPrimary(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}
	err = reviewCommand(f.ctx, append(base, "--artifact", artifacts["second"], "--artifact", artifacts["first"], "--implementation", first, "--implementation", second, "--verdict", "approved", "--text", "APPROVED"))
	if err == nil || !strings.Contains(err.Error(), "differs from the first selected implementation's report") {
		t.Fatalf("reordered primary: %v", err)
	}
	err = reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--implementation", first, "--implementation", second, "--verdict", "approved", "--text", "APPROVED"))
	if err == nil || !strings.Contains(err.Error(), "not in the examined set") {
		t.Fatalf("unexamined report: %v", err)
	}
	if err := reviewCommand(f.ctx, append(base, "--artifact", artifacts["first"], "--artifact", artifacts["second"], "--verdict", "approved", "--text", "APPROVED both")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	verdict, _ := mergeplan.StandingStatement(f.snapshot(t).Projection, approval, workroom.KindReport)
	if verdict.Body[reviewguard.BodyImplementations] != `["`+promises["first"]+`","`+promises["second"]+`"]` {
		t.Fatalf("recorded implementations = %s", verdict.Body[reviewguard.BodyImplementations])
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", candidate, "--approval", approval, "--text", "land both"}); err != nil {
		t.Fatal(err)
	}
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if (commitment.Request == first || commitment.Request == second) && commitment.Status != "satisfied" {
			t.Fatalf("implementation %s not closed by the sealed receipt: %+v", commitment.Request, commitment)
		}
	}
}

// Preparation explains without signing: the wrong primary is named with the
// required report, and nothing is appended.
func TestPrepareExplainsTheWrongPrimaryWithoutSigning(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildLandingLane(t, "binding-prepare", map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")})
	checkout := filepath.Join(filepath.Dir(f.repo), "binding-prepare")
	var report, promise string
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == lane.request {
			report, promise = commitment.Report, commitment.Promise
		}
	}
	// The historical shape: a second artifact at the same head, filed on the
	// same promise, that the fold did not make the commitment's report.
	companion, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "companion", Body: map[string]string{"path": "binding-prepare.txt", "commit": lane.candidate}, RestsOn: []string{promise}, IdempotencyKey: "prepare-companion"})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review again", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{report}, IdempotencyKey: "prepare-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review again", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "prepare-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	depth := f.snapshot(t).Depth
	err = reviewCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--artifact", companion.Record.ID, "--artifact", report, "--promise", reviewPromise.Record.ID, "--prepare"})
	if err == nil || !strings.Contains(err.Error(), "reported by") || !strings.Contains(err.Error(), report) {
		t.Fatalf("prepare with the wrong primary: %v", err)
	}
	if err := reviewCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--artifact", report, "--artifact", companion.Record.ID, "--promise", reviewPromise.Record.ID, "--prepare"}); err != nil {
		t.Fatal(err)
	}
	if f.snapshot(t).Depth != depth {
		t.Fatal("preparation appended to the workroom")
	}
}

// The authorization consumer resolves the binding on its own, so a structured
// authorization offered for an evidence-only approval refuses here even when
// the landing guard in front of it is not consulted.
func TestMergeAuthorizationRefusesANonAssignedApprovalOnItsOwn(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildEvidenceLane(t, "binding-authorization")
	if err := reviewCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--checkout", lane.checkout, "--artifact", lane.artifact, "--promise", lane.reviewPromise, "--evidence-only", "--verdict", "approved", "--text", "APPROVED evidence"}); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-authorization-approval"}); err != nil {
		t.Fatal(err)
	}
	authorizationRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "authorize", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "authorize the landing", "no_git_artifact": "true"}, RestsOn: []string{lane.request}, IdempotencyKey: "authorization-request"})
	if err != nil {
		t.Fatal(err)
	}
	target := f.measuredTarget(t)
	authorization, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindReport, Text: "authorized", Body: map[string]string{
		"authorizes_candidate": lane.candidate, "authorizes_approval": approval, "authorizes_request": lane.request,
		"target_pre_head": target.PreHead, "target_repo": target.Repo, "target_ref": target.Ref,
	}, RestsOn: []string{authorizationRequest.Record.ID}, IdempotencyKey: "authorization-report"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: authorization.Record.ID, IdempotencyKey: "ratify-authorization"}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	_, err = validateMergeAuthorization(f.ctx, snapshot.Projection, f.repo, lane.candidate, approval, authorization.Record.ID, target, false, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "an authorization needs an assigned implementation lane") {
		t.Fatalf("authorization of an evidence-only approval: %v", err)
	}
}

// combinedDelivery builds two assigned implementations delivered at one
// candidate, with the second request carrying extra body fields. It returns
// the requests, the candidate, both artifacts and the reviewer's base args.
type combinedDelivery struct {
	first, second, candidate string
	promises, artifacts      map[string]string
	base                     []string
}

func buildCombinedDelivery(t *testing.T, f workflowFixture, secondExtra map[string]string) combinedDelivery {
	t.Helper()
	body := map[string]string{"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main", "target_head": testGit(t, f.repo, "rev-parse", "HEAD")}
	secondBody := map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it too", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}
	for key, value := range secondExtra {
		secondBody[key] = value
	}
	first := f.stateV3(t, "reviewer", workroom.KindRequest, "implement first", map[string]string{"to": f.fingerprint(t, "operator"), "conditions": "land it", "target_repo": body["target_repo"], "target_ref": body["target_ref"], "target_head": body["target_head"]}, f.ground)
	second := f.stateV3(t, "reviewer", workroom.KindRequest, "implement second", secondBody, f.ground)
	promises := map[string]string{}
	for name, request := range map[string]string{"first": first, "second": second} {
		promise, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement " + name, RestsOn: []string{request}, IdempotencyKey: name + "-promise"})
		if err != nil {
			t.Fatal(err)
		}
		promises[name] = promise.Record.ID
	}
	checkout := filepath.Join(filepath.Dir(f.repo), "combined")
	testGit(t, f.repo, "worktree", "add", "-qb", "combined", checkout)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(checkout, name+".txt"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", "combined")
	candidate := testGit(t, checkout, "rev-parse", "HEAD")
	artifacts := map[string]string{}
	for _, name := range []string{"first", "second"} {
		artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: name + " artifact", Body: map[string]string{"path": name + ".txt", "commit": candidate}, RestsOn: []string{promises[name]}, IdempotencyKey: name + "-artifact"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = artifact.Record.ID
	}
	reviewRequest, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review combined", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifacts["first"]}, IdempotencyKey: "combined-review-request"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review combined", RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "combined-review-promise"})
	if err != nil {
		t.Fatal(err)
	}
	return combinedDelivery{first: first, second: second, candidate: candidate, promises: promises, artifacts: artifacts, base: []string{"--repo", f.repo, "--as", "reviewer", "--checkout", checkout, "--promise", reviewPromise.Record.ID}}
}

// Review finding 12182bd2: selecting only the first implementation must not
// hide the examined held companion. The verdict records both requests and the
// merge refuses the held landing exactly as it does with no selector.
func TestSelectorCannotHideAnExaminedHeldCompanion(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := buildCombinedDelivery(t, f, map[string]string{"landing": "held"})
	if err := reviewCommand(f.ctx, append(lane.base, "--artifact", lane.artifacts["first"], "--artifact", lane.artifacts["second"], "--implementation", lane.first, "--verdict", "approved", "--text", "APPROVED first only")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	verdict, _ := mergeplan.StandingStatement(f.snapshot(t).Projection, approval, workroom.KindReport)
	if verdict.Body[reviewguard.BodyImplementations] != `["`+lane.promises["first"]+`","`+lane.promises["second"]+`"]` {
		t.Fatalf("recorded implementations = %s", verdict.Body[reviewguard.BodyImplementations])
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	before, head := f.snapshot(t).Depth, testGit(t, f.repo, "rev-parse", "HEAD")
	err := mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", lane.candidate, "--approval", approval, "--text", "land first without the held second's release"})
	if err == nil || !strings.Contains(err.Error(), "is held") {
		t.Fatalf("selector hid the held companion: %v", err)
	}
	if f.snapshot(t).Depth != before {
		t.Fatal("refused merge appended to the workroom")
	}
	if testGit(t, f.repo, "rev-parse", "HEAD") != head {
		t.Fatal("refused merge moved the target")
	}
}

// Review finding 12182bd2: selecting only the first implementation must not
// hide an examined companion owed to a different target. The review refuses
// before signing, exactly as it does with no selector.
func TestSelectorCannotHideAnExaminedCompanionOwedElsewhere(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := buildCombinedDelivery(t, f, map[string]string{"target_ref": "refs/heads/other"})
	depth := f.snapshot(t).Depth
	err := reviewCommand(f.ctx, append(lane.base, "--artifact", lane.artifacts["first"], "--artifact", lane.artifacts["second"], "--implementation", lane.first, "--verdict", "approved", "--text", "APPROVED first only"))
	if err == nil || !strings.Contains(err.Error(), "different targets") {
		t.Fatalf("selector hid the companion's target: %v", err)
	}
	if f.snapshot(t).Depth != depth {
		t.Fatal("refused review appended to the workroom")
	}
}

// Paired control for the held case: with no selector, examining both reports
// refuses the held companion at gs merge exactly as the selector case does.
func TestExaminedHeldCompanionRefusesWithoutASelector(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := buildCombinedDelivery(t, f, map[string]string{"landing": "held"})
	if err := reviewCommand(f.ctx, append(lane.base, "--artifact", lane.artifacts["first"], "--artifact", lane.artifacts["second"], "--verdict", "approved", "--text", "APPROVED both")); err != nil {
		t.Fatal(err)
	}
	approval := f.lastEvent(t)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-combined"}); err != nil {
		t.Fatal(err)
	}
	head := testGit(t, f.repo, "rev-parse", "HEAD")
	err := mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", lane.candidate, "--approval", approval, "--text", "land both without the held second's release"})
	if err == nil || !strings.Contains(err.Error(), "is held") {
		t.Fatalf("held companion without a selector: %v", err)
	}
	if testGit(t, f.repo, "rev-parse", "HEAD") != head {
		t.Fatal("refused merge moved the target")
	}
}

// Paired control for the target case: with no selector, examining both
// reports refuses the differently targeted companion at gs review.
func TestExaminedCompanionOwedElsewhereRefusesWithoutASelector(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := buildCombinedDelivery(t, f, map[string]string{"target_ref": "refs/heads/other"})
	err := reviewCommand(f.ctx, append(lane.base, "--artifact", lane.artifacts["first"], "--artifact", lane.artifacts["second"], "--verdict", "approved", "--text", "APPROVED both"))
	if err == nil || !strings.Contains(err.Error(), "different targets") {
		t.Fatalf("companion owed elsewhere without a selector: %v", err)
	}
}

// Review finding d850bca9: a request with a withdrawn first promise and a
// current second one is a valid history. Preparing with the exact current
// promise, filing, ratifying and landing that verdict must all name the same
// lifecycle; the sealed receipt closes the current lifecycle and leaves the
// withdrawn one as it was.
func TestExactPromiseAfterWithdrawalPreparesFilesRatifiesAndLands(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := buildCombinedDelivery(t, f, nil)
	var oldPromise string
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == lane.first {
			oldPromise = commitment.Promise
		}
	}
	if oldPromise == "" {
		t.Fatal("missing original promise")
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbSupersede, Target: oldPromise, Text: "withdraw first attempt", RestsOn: []string{oldPromise}, IdempotencyKey: "withdraw-first"}); err != nil {
		t.Fatal(err)
	}
	current, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "resume same request", RestsOn: []string{lane.first}, IdempotencyKey: "resume-first"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "current first report", Body: map[string]string{"path": "first.txt", "commit": lane.candidate}, RestsOn: []string{current.Record.ID}, IdempotencyKey: "current-first-report"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review resumed delivery", Body: map[string]string{"to": f.fingerprint(t, "reviewer"), "conditions": "exact head", "no_git_artifact": "true"}, RestsOn: []string{artifact.Record.ID}, IdempotencyKey: "review-resumed"})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review resumed", RestsOn: []string{request.Record.ID}, IdempotencyKey: "review-resumed-promise"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycles := 0
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == lane.first {
			lifecycles++
		}
	}
	if lifecycles != 2 {
		t.Fatalf("need two actual lifecycles, got %d", lifecycles)
	}
	args := append([]string{}, lane.base...)
	args[len(args)-1] = reviewPromise.Record.ID
	args = append(args, "--artifact", artifact.Record.ID, "--implementation", current.Record.ID)
	if err := reviewCommand(f.ctx, append(append([]string{}, args...), "--prepare")); err != nil {
		t.Fatalf("exact promise prepare refused: %v", err)
	}
	before := f.snapshot(t).Depth
	if err := reviewCommand(f.ctx, append(args, "--verdict", "approved", "--text", "APPROVED resumed delivery")); err != nil {
		t.Fatalf("filing lost the exact promise (depth %d -> %d): %v", before, f.snapshot(t).Depth, err)
	}
	approval := f.lastEvent(t)
	verdict, _ := mergeplan.StandingStatement(f.snapshot(t).Projection, approval, workroom.KindReport)
	if verdict.Body[reviewguard.BodyImplementations] != `["`+current.Record.ID+`"]` {
		t.Fatalf("recorded lifecycle = %s, want the current promise", verdict.Body[reviewguard.BodyImplementations])
	}
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-resumed"}); err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--checkout", f.repo, "--candidate", lane.candidate, "--approval", approval, "--text", "land the resumed delivery"}); err != nil {
		t.Fatalf("merge lost the exact promise: %v", err)
	}
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request != lane.first {
			continue
		}
		if commitment.Promise == current.Record.ID && commitment.Status != "satisfied" {
			t.Fatalf("current lifecycle not closed by the sealed receipt: %+v", commitment)
		}
		if commitment.Promise == oldPromise && commitment.Status == "satisfied" {
			t.Fatalf("withdrawn lifecycle was closed by the receipt: %+v", commitment)
		}
	}
	testGit(t, f.repo, "merge-base", "--is-ancestor", lane.candidate, testGit(t, f.repo, "rev-parse", "HEAD"))
}
