package mergeplan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// approvedLane is one complete implementation lane in a scratch repository:
// the candidate commit, its ratified independent approval, and the custody the
// preflight needs to prove admission.
type approvedLane struct {
	workspace *app.Workspace
	repo      string
	candidate string
	approval  string
	merger    string
	signer    Signer
}

func buildApprovedLane(t *testing.T) approvedLane {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	runGit(t, "", "init", "-q", "-b", "main", repo)
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "base.txt")
	runGit(t, repo, "commit", "-qm", "base")

	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _, err := workspace.AddActor(ctx, "operator", "reviewer", "agent")
	if err != nil {
		t.Fatal(err)
	}
	base := runGit(t, repo, "rev-parse", "HEAD")
	ground, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "repository base",
		Body:    map[string]string{"path": "base.txt", "commit": base},
		RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: "ground",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "implement feature",
		Body:    map[string]string{"to": workspace.View().Actors["operator"].Fingerprint, "conditions": "publish the exact feature head", "target_ref": "refs/heads/main"},
		RestsOn: []string{ground.Record.ID}, IdempotencyKey: "implementation-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement feature",
		RestsOn: []string{request.Record.ID}, IdempotencyKey: "implementation-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "worktree", "add", "-qb", "candidate", feature)
	if err := os.WriteFile(filepath.Join(feature, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, feature, "add", "feature.txt")
	runGit(t, feature, "commit", "-qm", "feature")
	candidate := runGit(t, feature, "rev-parse", "HEAD")
	artifact, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "feature artifact",
		Body:    map[string]string{"path": "feature.txt", "commit": candidate},
		RestsOn: []string{promise.Record.ID, ground.Record.ID}, IdempotencyKey: "artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewRequest, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review feature",
		Body:    map[string]string{"to": reviewer.Fingerprint, "conditions": "exact head", "no_git_artifact": "true"},
		RestsOn: []string{artifact.Record.ID}, IdempotencyKey: "review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewPromise, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review feature",
		RestsOn: []string{reviewRequest.Record.ID}, IdempotencyKey: "review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func() (reviewguard.Basis, []reviewguard.News, workroom.Projection, error) {
		snapshot, readErr := workspace.Snapshot(ctx)
		if readErr != nil {
			return reviewguard.Basis{}, nil, workroom.Projection{}, readErr
		}
		basis, news, basisErr := reviewguard.ReviewBasis(reviewguard.Read{
			Projection: snapshot.Projection, ReviewerFingerprint: reviewer.Fingerprint,
			Checkout: feature, CommonDir: workspace.CommonDir, FrontierEvent: workspace.EventID(snapshot.Head),
		}, artifact.Record.ID, reviewPromise.Record.ID)
		return basis, news, snapshot.Projection, basisErr
	}
	body, restsOn, err := reviewguard.Confirm(read, []string{artifact.Record.ID}, nil, reviewguard.VerdictApproved, "approved")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "approved", Body: body,
		RestsOn: restsOn, GuardedReview: true, IdempotencyKey: "approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Act(ctx, "operator", app.Act{Verb: app.VerbRatify, Target: approval.Record.ID, IdempotencyKey: "ratify-approval"}); err != nil {
		t.Fatal(err)
	}
	operator, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	return approvedLane{
		workspace: workspace, repo: repo, candidate: candidate, approval: approval.Record.ID,
		merger: operator.Fingerprint, signer: Signer{Name: "operator", Private: private},
	}
}

// A candidate the target already contains plans an incorporation: one receipt
// act carrying merge_incorporation=prior and the four empty encodings, with no
// successor and no retirement beside it. Removing the incorporation branch
// from Build turns this back into the old containment refusal and the test
// goes red at the first assertion.
func TestBuildPlansAnIncorporationForAContainedCandidate(t *testing.T) {
	t.Parallel()
	lane := buildApprovedLane(t)
	ctx := context.Background()
	// The landing that happened out of band: an ordinary merge commit made by
	// plain Git, with no receipt of any kind.
	runGit(t, lane.repo, "merge", "--no-ff", "-q", "-m", "land the feature by hand", lane.candidate)
	targetPreHead := runGit(t, lane.repo, "rev-parse", "HEAD")

	result := Build(ctx, lane.workspace, lane.repo, lane.candidate, lane.approval, lane.merger, lane.signer)
	if result.Mode != ModeIncorporate || !result.Allowed {
		t.Fatalf("contained candidate plan mode = %q allowed = %v, want %q and allowed: %+v", result.Mode, result.Allowed, ModeIncorporate, result.Reasons)
	}
	incorporated := false
	for _, reason := range result.Reasons {
		if reason.Code == "candidate_incorporated" && reason.Check == "candidate" && reason.Allowed {
			incorporated = true
		}
		if reason.Code == "tentative_merge_allowed" {
			t.Errorf("an incorporation staged a tentative merge: %+v", reason)
		}
	}
	if !incorporated {
		t.Fatalf("plan gave no incorporation reason: %+v", result.Reasons)
	}
	if len(result.CandidateArtifacts) != 1 || result.CandidateArtifacts[0].Path != "feature.txt" || !result.CandidateArtifacts[0].Reviewed {
		t.Fatalf("incorporation plan candidate artifacts = %+v", result.CandidateArtifacts)
	}
	if len(result.Retirements) != 0 || len(result.Successors) != 0 || len(result.ChangedPaths) != 0 || len(result.CoveringArtifacts) != 0 {
		t.Fatalf("incorporation plan proposed succession: %+v", result)
	}
	plan, ok := result.ValidatedSuccession()
	if !ok || !plan.Incorporation {
		t.Fatalf("validated succession = %+v ok=%v, want an incorporation", plan, ok)
	}
	if plan.Retire == nil || plan.Publish == nil || plan.LeftLive == nil || plan.ChangedPaths == nil ||
		len(plan.Retire) != 0 || len(plan.Publish) != 0 || len(plan.LeftLive) != 0 || len(plan.ChangedPaths) != 0 {
		t.Fatalf("incorporation plan is not the empty succession: %+v", plan)
	}

	target := Target{Repo: WorkroomRepo(lane.workspace), Ref: "refs/heads/main", PreHead: targetPreHead}
	plan.Text = "Record the head main already carries."
	acts := SuccessionActs(lane.approval, "", "", lane.candidate, target, lane.candidate, "", false, plan)
	if len(acts) != 1 {
		t.Fatalf("incorporation encoded %d acts, want exactly the receipt", len(acts))
	}
	act := acts[0].Act
	if act.Kind != workroom.KindAssert || act.Text != plan.Text || act.IdempotencyKey != ReceiptKey(lane.approval) {
		t.Fatalf("incorporation receipt act = %+v", act)
	}
	want := map[string]string{
		"merge_approval": lane.approval, "merge_candidate": lane.candidate,
		"merge_head": lane.candidate, "merge_target_pre_head": targetPreHead,
		"merge_target_repo": target.Repo, "merge_target_ref": target.Ref,
		"merge_incorporation": IncorporationPrior,
		"merge_retirements":   "{}", "merge_successors": "[]",
		"merge_left_live": "{}", "merge_changed_paths": "[]",
	}
	for field, value := range want {
		if act.Body[field] != value {
			t.Errorf("receipt body[%q] = %q, want %q", field, act.Body[field], value)
		}
	}
	if len(act.Body) != len(want) {
		t.Errorf("receipt body = %+v, want exactly %d fields", act.Body, len(want))
	}
}

// The encoder is the first of the two doors that keep a prior incorporation's
// plan empty. Removing the emptiness check in SuccessionActs lets each of
// these encode a receipt that claims prior containment and succession
// authority at once, and this test goes red.
func TestSuccessionActsRefuseANonEmptyPriorIncorporation(t *testing.T) {
	t.Parallel()
	target := Target{Repo: "git:sha1:genesis", Ref: "refs/heads/main", PreHead: "target"}
	empty := func() Succession {
		return Succession{Publish: []string{}, Retire: map[string]string{}, LeftLive: map[string]LeftLive{},
			ChangedPaths: []string{}, Incorporation: true, Text: "record it"}
	}
	if acts := SuccessionActs("approval", "", "", "candidate", target, "candidate", "", false, empty()); len(acts) != 1 {
		t.Fatalf("the empty prior incorporation encoded %d acts, want one", len(acts))
	}
	for name, mutate := range map[string]func(*Succession){
		"publishes a successor": func(plan *Succession) { plan.Publish = []string{"spike"} },
		"retires a predecessor": func(plan *Succession) { plan.Retire = map[string]string{"victim": "spike"} },
		"declares a changed path": func(plan *Succession) {
			plan.ChangedPaths = []string{"spike"}
		},
		"claims left-live testimony": func(plan *Succession) {
			plan.LeftLive = map[string]LeftLive{"wide": {Class: LeftLiveCarried}}
		},
		"drops an empty encoding": func(plan *Succession) { plan.ChangedPaths = nil },
	} {
		plan := empty()
		mutate(&plan)
		if acts := SuccessionActs("approval", "", "", "candidate", target, "candidate", "", false, plan); acts != nil {
			t.Errorf("a prior incorporation that %s encoded %d acts, want none", name, len(acts))
		}
	}
}
