package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/service"
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
func TestRecordedSuccessionPlanRefusesAnIncorporation(t *testing.T) {
	t.Parallel()
	receipt := mergeReceipt{Approval: "approval", Candidate: "candidate", MergeHead: "head"}
	for name, value := range map[string]string{"prior": mergeplan.IncorporationPrior, "unknown value": "later"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			projection := workroom.Projection{Statements: []workroom.Statement{{
				Event: "receipt", Kind: workroom.KindAssert, Text: "Record what the target already has",
				Body: map[string]string{
					"merge_approval": "approval", "merge_head": "head", "merge_candidate": "candidate",
					"merge_incorporation": value,
					"merge_retirements":   "{}", "merge_successors": "[]",
					"merge_left_live": "{}", "merge_changed_paths": "[]",
				},
			}}}
			plan, found, err := recordedSuccessionPlan(projection, receipt)
			if err == nil || !strings.Contains(err.Error(), "never an incorporation") {
				t.Fatalf("recorded %s incorporation resumed as %+v found=%v err=%v", name, plan, found, err)
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

// incorporationAttempt is one independently planned incorporation: its own
// workspace, its own preflight, its own measured destination. Two of these
// stand in for two processes that each planned before either appended.
type incorporationAttempt struct {
	workspace  *app.Workspace
	private    ed25519.PrivateKey
	plan       mergeplan.Result
	validation mergeValidation
	text       string
}

func planIncorporation(t *testing.T, f workflowFixture, serverURL string, lane landingLane, text string) incorporationAttempt {
	t.Helper()
	// A workspace of its own, opened on the same repository: separate caches
	// and a separate frontier reading, which is what makes the two plans
	// independent rather than two views of one snapshot.
	workspace, err := app.Open(f.ctx, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	actor, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	signer := mergeplan.Signer{Name: "operator", Private: private, ResidentCeiling: residentSubmissionCeiling(serverURL)}
	plan := buildMergePlan(f.ctx, workspace, f.repo, lane.candidate, lane.approval, actor.Fingerprint, signer)
	if plan.Mode != mergeplan.ModeIncorporate || !plan.Allowed {
		t.Fatalf("attempt planned mode = %q allowed = %v, want an allowed incorporation: %+v", plan.Mode, plan.Allowed, plan.Reasons)
	}
	validation, err := validateMerge(f.ctx, workspace, f.repo, lane.candidate, lane.approval, "", false)
	if err != nil {
		t.Fatalf("attempt validation: %v", err)
	}
	return incorporationAttempt{workspace: workspace, private: private, plan: plan, validation: validation, text: text}
}

func (a incorporationAttempt) submit(f workflowFixture, serverURL string, lane landingLane) error {
	return recordMergeIncorporation(f.ctx, a.workspace, f.repo, serverURL, "operator", a.private,
		lane.approval, lane.candidate, a.text, a.validation.Target, a.validation, a.plan)
}

// Two independently planned incorporations submitted through one resident,
// held at the submission boundary until both have passed the no-receipt check
// and their frontier remeasure. The merge lock serialises two `gs merge`
// processes on one machine, so this is the arrangement that actually reaches
// the deterministic receipt key: it proves the key, the kernel's dedup, and
// what the command does with each outcome. It does not prove anything about
// two machines' clocks, two residents, or a partitioned log, none of which
// this design claims.
//
// Not parallel: it replaces the package-level submission seam.
func TestConcurrentIncorporationsLeaveExactlyOneReceipt(t *testing.T) {
	const shared = "Record that main already carries this approved head."
	for _, test := range []struct {
		name         string
		secondText   string
		advanceFirst bool
		wantAccepted int
	}{
		// Identical observations rebuild the identical act, so the loser of
		// the race replays the winner's receipt and both calls succeed.
		{name: "identical acts", secondText: shared, wantAccepted: 2},
		// Everything else is the same approval spending its one key on a
		// different claim, and is refused.
		{name: "different text", secondText: "A different description of the same recording.", wantAccepted: 1},
		{name: "different observed pre-head", secondText: shared, advanceFirst: true, wantAccepted: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			resident, err := service.NewObserved(f.workspace, nopObserver{})
			if err != nil {
				t.Fatal(err)
			}
			var hits atomic.Int64
			listener := countingServer(t, &hits, resident.Handler())
			lane := f.buildLandingLane(t, "raced", map[string]string{
				"target_repo": mergeplan.WorkroomRepo(f.workspace),
				"target_ref":  "refs/heads/main",
				"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
			})
			testGit(t, f.repo, "merge", "--ff-only", "-q", lane.candidate)

			first := planIncorporation(t, f, listener.URL, lane, shared)
			if test.advanceFirst {
				// Unrelated traffic on the target between the two plans. The
				// candidate is still contained, so the second attempt plans a
				// valid incorporation against a different pre-head.
				testGit(t, f.repo, "commit", "-q", "--allow-empty", "-m", "unrelated traffic on the target")
			}
			second := planIncorporation(t, f, listener.URL, lane, test.secondText)
			if sameHead := first.validation.Target.PreHead == second.validation.Target.PreHead; sameHead == test.advanceFirst {
				t.Fatalf("attempts observed pre-heads %s and %s, advanceFirst=%v",
					first.validation.Target.PreHead, second.validation.Target.PreHead, test.advanceFirst)
			}
			before := readGitState(t, f.repo, "refs/heads/main")

			// The barrier. Both attempts reach the submission boundary — past
			// the durable no-receipt check and past the frontier remeasure —
			// before either is allowed to append.
			arrived, release := make(chan struct{}, 2), make(chan struct{})
			previous := recordIncorporationBatch
			recordIncorporationBatch = func(ctx context.Context, workspace *app.Workspace, serverURL, actorName string,
				private ed25519.PrivateKey, acts []batchAct, citedOK bool) (batchReport, error) {
				arrived <- struct{}{}
				<-release
				return previous(ctx, workspace, serverURL, actorName, private, acts, citedOK)
			}
			t.Cleanup(func() { recordIncorporationBatch = previous })

			results := make([]error, 2)
			var running sync.WaitGroup
			for index, attempt := range []incorporationAttempt{first, second} {
				running.Add(1)
				go func() {
					defer running.Done()
					results[index] = attempt.submit(f, listener.URL, lane)
				}()
			}
			// An attempt that fails before the seam never arrives, and
			// waiting for it forever would report as a package timeout rather
			// than as this test.
			for waiting := 2; waiting > 0; waiting-- {
				select {
				case <-arrived:
				case <-time.After(time.Minute):
					close(release)
					running.Wait()
					t.Fatalf("only %d of 2 incorporations reached the submission boundary: %v", 2-waiting, results)
				}
			}
			close(release)
			running.Wait()

			accepted, refusals := 0, []error{}
			for _, err := range results {
				if err == nil {
					accepted++
					continue
				}
				refusals = append(refusals, err)
			}
			if accepted != test.wantAccepted {
				t.Fatalf("%d of 2 racing incorporations were accepted, want %d; refusals %v", accepted, test.wantAccepted, refusals)
			}
			for _, err := range refusals {
				// Both doors — the client's own accepted-act classifier and
				// the kernel's dedup index — refuse with the kernel's words,
				// so the assertion does not depend on which one won.
				if !strings.Contains(err.Error(), "idempotency key reused with different intent") {
					t.Fatalf("racing refusal = %v, want an idempotency conflict", err)
				}
			}
			if hits.Load() == 0 {
				t.Fatal("the racing incorporations never reached the resident")
			}

			// The invariant, whatever the race decided.
			receipts := []string{}
			for _, statement := range f.snapshot(t).Projection.Statements {
				if statement.Body["merge_approval"] == lane.approval {
					receipts = append(receipts, statement.Event)
				}
			}
			if len(receipts) != 1 {
				t.Fatalf("racing incorporations left %d durable receipts: %v", len(receipts), receipts)
			}
			if after := readGitState(t, f.repo, "refs/heads/main"); after != before {
				t.Fatalf("the racing incorporations moved Git\nbefore: %+v\nafter:  %+v", before, after)
			}
			if _, err := git(f.ctx, f.repo, "show-ref", "--verify", mergeReceiptRef(lane.approval)); err == nil {
				t.Error("the racing incorporations created a Git receipt ref")
			}
			row := laneCommitment(t, f, lane.request)
			if row.Status != "satisfied" || row.Terminal != "landed" || row.ApprovedNotLanded || row.LandingReceipt != receipts[0] {
				t.Fatalf("raced commitment row = %+v, want satisfied/landed on receipt %s", row, receipts[0])
			}
		})
	}
}
