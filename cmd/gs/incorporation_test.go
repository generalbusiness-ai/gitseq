package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// gitState is everything a merge would have moved, read straight out of the
// checkout. An incorporation must leave every one of these exactly as it found
// them, so they are compared as a whole rather than one assertion at a time.
type gitState struct {
	head, targetRef, status, index string
}

func readGitState(t *testing.T, repo, targetRef string) gitState {
	t.Helper()
	return gitState{
		head:      testGit(t, repo, "rev-parse", "HEAD"),
		targetRef: testGit(t, repo, "rev-parse", targetRef),
		status:    testGitRaw(t, repo, "status", "--porcelain=v1", "-uall"),
		index:     testGitRaw(t, repo, "diff-index", "--cached", "--name-status", "HEAD"),
	}
}

func laneCommitment(t *testing.T, f workflowFixture, request string) workroom.Commitment {
	t.Helper()
	var found []workroom.Commitment
	for _, commitment := range f.snapshot(t).Projection.Commitments {
		if commitment.Request == request {
			found = append(found, commitment)
		}
	}
	if len(found) != 1 {
		t.Fatalf("request %s has %d commitment rows, want one: %+v", request, len(found), found)
	}
	return found[0]
}

func mergePlanJSON(t *testing.T, f workflowFixture, lane landingLane) mergeplan.Result {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	commandErr := mergePlanCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", lane.candidate, "--approval", lane.approval,
	})
	os.Stdout = stdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	printed, readErr := io.ReadAll(reader)
	reader.Close()
	if commandErr != nil || readErr != nil {
		t.Fatalf("merge-plan: command=%v read=%v", commandErr, readErr)
	}
	var result mergeplan.Result
	if err := json.Unmarshal(printed, &result); err != nil {
		t.Fatalf("merge-plan output %q: %v", printed, err)
	}
	return result
}

// The whole incorporation path, driven through the real command against a
// landing lane whose approved head reached the target without a receipt. Both
// shapes of out-of-band landing are covered, because a fast-forward and a
// merge commit are the two ways a head arrives with nobody signing for it.
//
// Not parallel: it reads process-wide standard output and standard error.
func TestMergeRecordsAnIncorporationForAHeadTheTargetAlreadyHas(t *testing.T) {
	for _, test := range []struct {
		name string
		land func(t *testing.T, repo, candidate string)
	}{
		{name: "merge commit", land: func(t *testing.T, repo, candidate string) {
			testGit(t, repo, "merge", "--no-ff", "-q", "-m", "land it by hand", candidate)
		}},
		{name: "fast-forward", land: func(t *testing.T, repo, candidate string) {
			testGit(t, repo, "merge", "--ff-only", "-q", candidate)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			lane := f.buildLandingLane(t, "incorporated", map[string]string{
				"target_repo": mergeplan.WorkroomRepo(f.workspace),
				"target_ref":  "refs/heads/main",
				"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
			})
			if row := laneCommitment(t, f, lane.request); row.Status != "awaiting-landing" || !row.ApprovedNotLanded {
				t.Fatalf("before the out-of-band landing the row is %+v, want awaiting-landing and approved_not_landed", row)
			}
			test.land(t, f.repo, lane.candidate)
			targetPreHead := testGit(t, f.repo, "rev-parse", "refs/heads/main")
			before := readGitState(t, f.repo, "refs/heads/main")

			if plan := mergePlanJSON(t, f, lane); plan.Mode != mergeplan.ModeIncorporate || !plan.Allowed {
				t.Fatalf("merge-plan mode = %q allowed = %v, want an allowed incorporation: %+v", plan.Mode, plan.Allowed, plan.Reasons)
			}

			const text = "Record that main already carries this approved head."
			notice, err := captureStderr(t, func() error {
				return f.mergeLane(t, lane, "--text", text)
			})
			if err != nil {
				t.Fatalf("incorporation merge: %v", err)
			}
			if !strings.Contains(notice, "records an incorporation rather than a merge") {
				t.Fatalf("incorporation stderr = %q, want the incorporation notice", notice)
			}

			// Condition 1: one receipt, the exact body, and Git untouched.
			receipt := mergeReceiptStatement(t, f, lane.approval)
			if receipt.Text != text {
				t.Errorf("receipt text = %q, want the merge --text %q", receipt.Text, text)
			}
			targetRepo, targetRef := f.laneTarget(t, lane.request)
			want := map[string]string{
				"merge_approval": lane.approval, "merge_candidate": lane.candidate,
				"merge_head": lane.candidate, "merge_target_pre_head": targetPreHead,
				"merge_target_repo": targetRepo, "merge_target_ref": targetRef,
				"merge_incorporation": mergeplan.IncorporationPrior,
				"merge_retirements":   "{}", "merge_successors": "[]",
				"merge_left_live": "{}", "merge_changed_paths": "[]",
			}
			for field, value := range want {
				if receipt.Body[field] != value {
					t.Errorf("receipt body[%q] = %q, want %q", field, receipt.Body[field], value)
				}
			}
			if len(receipt.Body) != len(want) {
				t.Errorf("receipt body = %+v, want exactly %d fields", receipt.Body, len(want))
			}
			if after := readGitState(t, f.repo, "refs/heads/main"); after != before {
				t.Fatalf("the incorporation moved Git\nbefore: %+v\nafter:  %+v", before, after)
			}
			if _, err := git(f.ctx, f.repo, "show-ref", "--verify", mergeReceiptRef(lane.approval)); err == nil {
				t.Error("the incorporation created a Git receipt ref")
			}

			// The read-back the command makes before it reports success: the
			// receipt the fold admitted, not the one the batch submitted.
			if err := verifyIncorporation(f.ctx, f.workspace, lane.approval, lane.candidate); err != nil {
				t.Errorf("verifying the recorded incorporation: %v", err)
			}
			if err := verifyIncorporation(f.ctx, f.workspace, lane.approval, strings.Repeat("e", 40)); err == nil ||
				!strings.Contains(err.Error(), "does not name candidate") {
				t.Errorf("verification of a receipt for another candidate = %v", err)
			}
			if err := verifyIncorporation(f.ctx, f.workspace, f.artifact, lane.candidate); err == nil ||
				!strings.Contains(err.Error(), "no durable incorporation receipt") {
				t.Errorf("verification of an approval with no receipt at all = %v", err)
			}

			// Condition 2: the commitment closes under the ordinary rules.
			row := laneCommitment(t, f, lane.request)
			if row.Status != "satisfied" || row.Terminal != "landed" || row.ApprovedNotLanded || row.LandingReceipt != receipt.Event {
				t.Fatalf("incorporated commitment row = %+v, want satisfied/landed on receipt %s", row, receipt.Event)
			}

			// Condition 3: the approval is spent, and says so by name.
			second := f.mergeLane(t, lane, "--text", text)
			if second == nil || !strings.Contains(second.Error(), "approval already has durable merge receipt "+receipt.Event) {
				t.Fatalf("second incorporation error = %v, want a refusal naming %s", second, receipt.Event)
			}
			receipts := 0
			for _, statement := range f.snapshot(t).Projection.Statements {
				if statement.Body["merge_approval"] == lane.approval {
					receipts++
				}
			}
			if receipts != 1 {
				t.Fatalf("the approval has %d durable receipts, want exactly one", receipts)
			}
			if after := readGitState(t, f.repo, "refs/heads/main"); after != before {
				t.Fatalf("the refused second incorporation moved Git\nbefore: %+v\nafter:  %+v", before, after)
			}
		})
	}
}

// The recorded-plan reader is the other side of the incorporation encoding:
// it is what recovery compares a durable receipt against. It must round-trip
// the field, refuse an unknown spelling of it, and refuse a recorded prior
// incorporation whose plan is not empty, so that neither door admits a receipt
// claiming prior containment and succession authority at once.
func TestRecordedSuccessionPlanBoundsAPriorIncorporation(t *testing.T) {
	t.Parallel()
	receipt := mergeReceipt{Approval: "approval", Candidate: "candidate", MergeHead: "head"}
	recorded := func(mutate func(map[string]string)) workroom.Projection {
		body := map[string]string{
			"merge_approval": "approval", "merge_head": "head", "merge_candidate": "candidate",
			"merge_incorporation": mergeplan.IncorporationPrior,
			"merge_retirements":   "{}", "merge_successors": "[]",
			"merge_left_live": "{}", "merge_changed_paths": "[]",
		}
		if mutate != nil {
			mutate(body)
		}
		return workroom.Projection{Statements: []workroom.Statement{
			{Event: "receipt", Kind: workroom.KindAssert, Text: "Record what the target already has", Body: body},
		}}
	}
	plan, found, err := recordedSuccessionPlan(recorded(nil), receipt)
	if err != nil || !found || !plan.Incorporation || plan.Text != "Record what the target already has" {
		t.Fatalf("recorded prior incorporation = %+v found=%v err=%v", plan, found, err)
	}
	for name, test := range map[string]struct {
		mutate func(map[string]string)
		want   string
	}{
		"unknown value": {
			mutate: func(body map[string]string) { body["merge_incorporation"] = "later" },
			want:   "unknown merge_incorporation",
		},
		"retires a predecessor": {
			mutate: func(body map[string]string) { body["merge_retirements"] = `{"victim":"spike"}` },
			want:   "must carry an empty succession plan",
		},
		"publishes a successor": {
			mutate: func(body map[string]string) { body["merge_successors"] = `["spike"]` },
			want:   "must carry an empty succession plan",
		},
		"declares a changed path": {
			mutate: func(body map[string]string) { body["merge_changed_paths"] = `["spike"]` },
			want:   "must carry an empty succession plan",
		},
		"claims left-live testimony": {
			mutate: func(body map[string]string) { body["merge_left_live"] = `{"wide":{"class":"carried"}}` },
			want:   "must carry an empty succession plan",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, found, err := recordedSuccessionPlan(recorded(test.mutate), receipt)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("recorded prior incorporation that %s = %+v found=%v err=%v, want %q", name, plan, found, err, test.want)
			}
		})
	}
}

// The frontier remeasure. An incorporation appends its receipt against the
// world it planned in; a workroom that moved in between is planned against a
// frontier that no longer exists, so the append is refused and nothing is
// recorded. Not parallel: it replaces the package-level plan builder.
func TestIncorporationRefusesAMovedWorkroomFrontier(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildLandingLane(t, "moved-frontier", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
	})
	testGit(t, f.repo, "merge", "--ff-only", "-q", lane.candidate)
	before := readGitState(t, f.repo, "refs/heads/main")

	previous := buildMergePlan
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		result := previous(ctx, workspace, checkout, candidate, approval, merger, signer)
		if result.Mode == mergeplan.ModeIncorporate && result.Allowed {
			if _, err := workspace.Act(ctx, "reviewer", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "unrelated traffic",
				RestsOn: []string{f.ground}, IdempotencyKey: "frontier-mover",
			}); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	t.Cleanup(func() { buildMergePlan = previous })

	err := f.mergeLane(t, lane, "--text", "Record a head the target already has.")
	if err == nil || !strings.Contains(err.Error(), "workroom frontier moved after planning") {
		t.Fatalf("incorporation against a moved frontier = %v", err)
	}
	for _, statement := range f.snapshot(t).Projection.Statements {
		if statement.Body["merge_approval"] == lane.approval {
			t.Fatalf("the refused incorporation appended receipt %s", statement.Event)
		}
	}
	if after := readGitState(t, f.repo, "refs/heads/main"); after != before {
		t.Fatalf("the refused incorporation moved Git\nbefore: %+v\nafter:  %+v", before, after)
	}
}

// An ordinary landing is not an incorporation, and the read-back says so. This
// is the other half of the verification: without it a receipt sealed by a real
// merge would satisfy a command that promised to write no Git at all.
func TestVerifyIncorporationRefusesAnOrdinaryReceipt(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildLandingLane(t, "ordinary", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
	})
	if err := f.mergeLane(t, lane); err != nil {
		t.Fatalf("ordinary merge: %v", err)
	}
	err := verifyIncorporation(f.ctx, f.workspace, lane.approval, lane.candidate)
	if err == nil || !strings.Contains(err.Error(), "is not an incorporation") {
		t.Fatalf("verification of an ordinary merge receipt = %v", err)
	}
}
