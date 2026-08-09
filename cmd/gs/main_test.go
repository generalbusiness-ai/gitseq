package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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

func TestValidateLoopbackServer(t *testing.T) {
	tests := map[string]bool{
		"http://127.0.0.1:7777":   true,
		"http://[::1]:7777":       true,
		"http://localhost:7777":   true,
		"https://127.0.0.1:7777":  false,
		"http://user@127.0.0.1:7": false,
		"http://192.0.2.1:7777":   false,
		"http://0.0.0.0:7777":     false,
		"http://127.0.0.1/x":      false,
		"http://127.0.0.1/?x=y":   false,
		"not-a-url":               false,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			if got := validateLoopbackServer(raw) == nil; got != want {
				t.Fatalf("validateLoopbackServer(%q) success = %v, want %v", raw, got, want)
			}
		})
	}
}

func statusSummaryFixture(t *testing.T) (*app.Workspace, service.SummaryStatus) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", repo)
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	frontier := service.Frontier{Genesis: snapshot.Genesis, Head: snapshot.Head, Depth: snapshot.Depth}
	return workspace, service.SummaryStatus{
		Durable: statusview.Build(snapshot.Genesis, snapshot.Head, snapshot.Depth, snapshot.Projection),
		Cursor:  service.Cursor{Frontier: []service.Frontier{frontier}},
	}
}

func TestFetchSummaryPinsGenesisHeadCursorAndResponseCap(t *testing.T) {
	if summaryResponseLimit != 64<<10 {
		t.Fatalf("summary response limit = %d, want 64 KiB", summaryResponseLimit)
	}
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	serve := func(handler http.HandlerFunc) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}
	valid := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(summary) })
	if got, err := fetchSummary(ctx, workspace, valid.URL); err != nil || got.Durable.Head != summary.Durable.Head {
		t.Fatalf("valid resident summary = %+v, %v", got, err)
	}

	wrongGenesis := summary
	wrongGenesis.Cursor.Frontier = append([]service.Frontier(nil), summary.Cursor.Frontier...)
	wrongGenesis.Durable.Genesis = "foreign"
	wrongGenesis.Cursor.Frontier[0].Genesis = "foreign"
	foreign := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(wrongGenesis) })
	if _, err := fetchSummary(ctx, workspace, foreign.URL); err == nil || !strings.Contains(err.Error(), "genesis") {
		t.Fatalf("foreign-genesis error = %v", err)
	}

	wrongHead := summary
	wrongHead.Durable.Head = strings.Repeat("0", len(summary.Durable.Head))
	stale := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(wrongHead) })
	if _, err := fetchSummary(ctx, workspace, stale.URL); err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("stale-head error = %v", err)
	}

	redirect := serve(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, valid.URL, http.StatusFound)
	})
	if _, err := fetchSummary(ctx, workspace, redirect.URL); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect error = %v", err)
	}

	oversized := serve(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(bytes.Repeat([]byte("x"), (64<<10)+1))
	})
	if _, err := fetchSummary(ctx, workspace, oversized.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestFetchSummaryRejectsSlowAndMovingResident(t *testing.T) {
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
			return
		case <-time.After(3 * time.Second):
			_ = json.NewEncoder(writer).Encode(summary)
		}
	}))
	defer slow.Close()
	started := time.Now()
	if _, err := fetchSummary(ctx, workspace, slow.URL); err == nil || time.Since(started) > 2500*time.Millisecond {
		t.Fatalf("slow resident error = %v after %s", err, time.Since(started))
	}

	moving := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		basis := workspace.EventID(workspace.Config.Genesis)
		if _, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance while status is in flight",
			RestsOn: []string{basis}, IdempotencyKey: "moving-status-head",
		}); err != nil {
			t.Errorf("advance head: %v", err)
			return
		}
		advanced, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Errorf("advanced snapshot: %v", err)
			return
		}
		response := service.SummaryStatus{
			Durable: statusview.Build(advanced.Genesis, advanced.Head, advanced.Depth, advanced.Projection),
			Cursor:  service.Cursor{Frontier: []service.Frontier{{Genesis: advanced.Genesis, Head: advanced.Head, Depth: advanced.Depth}}},
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer moving.Close()
	if _, err := fetchSummary(ctx, workspace, moving.URL); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("moving-head error = %v", err)
	}
	current := testGit(t, workspace.Repo, "rev-parse", kernel.Ref(workspace.Config.Genesis))
	if current == summary.Durable.Head {
		t.Fatal("moving-head fixture did not move the durable ref")
	}
}

func TestSlowLocalAuditReportsProgressWithoutChangingTheResult(t *testing.T) {
	want := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 7}
	var progress bytes.Buffer
	got, err := loadSnapshotWithProgress(context.Background(), &progress, func(context.Context) (app.Snapshot, error) {
		time.Sleep(1100 * time.Millisecond)
		return want, nil
	})
	if err != nil || got.Genesis != want.Genesis || got.Head != want.Head || got.Depth != want.Depth {
		t.Fatalf("slow load = %+v, %v", got, err)
	}
	if !strings.Contains(progress.String(), "verifying the durable log") {
		t.Fatalf("slow audit was silent: %q", progress.String())
	}
	progress.Reset()
	if _, err := loadSnapshotWithProgress(context.Background(), &progress, func(context.Context) (app.Snapshot, error) {
		return want, nil
	}); err != nil || progress.Len() != 0 {
		t.Fatalf("fast audit reported progress: %q, %v", progress.String(), err)
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

// chainBatch is the ordinary case: a request, then a promise resting on it by
// intra-batch label. The verb argument is the genesis event the request rests on.
const chainBatch = `[
  {"label": "req", "verb": "state", "kind": "request", "text": "do the thing",
   "body": {"to": "@worker", "conditions": "tests green"},
   "rests_on": [%q], "idempotency_key": "chain-request"},
  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will do the thing",
   "rests_on": ["$req"], "idempotency_key": "chain-promise"}
]`

func TestBatchLandsChainResolvingIntraBatchLabels(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	report, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if report.Landed != 2 || report.Replayed != 0 || report.Error != nil {
		t.Fatalf("batch report = %#v", report)
	}
	for position, want := range []string{"req", "promise"} {
		outcome := report.Acts[position]
		if outcome.Position != position || outcome.Label != want || outcome.Outcome != "landed" || outcome.Event == "" {
			t.Fatalf("act %d outcome = %#v", position, outcome)
		}
	}
	snapshot := fixture.snapshot()
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d", snapshot.Depth, before+2)
	}
	request, promise := report.Acts[0].Event, report.Acts[1].Event
	if !contains(snapshot.Projection.Provenance[promise], request) {
		t.Fatalf("promise provenance = %#v, want the minted request %s", snapshot.Projection.Provenance[promise], request)
	}
}

func TestBatchRetryLandsNothingNew(t *testing.T) {
	fixture := newBatchFixture(t)
	first, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	landed := fixture.snapshot()
	second, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if second.Landed != 0 || second.Replayed != 2 || second.Error != nil {
		t.Fatalf("retry report = %#v", second)
	}
	for position := range second.Acts {
		if second.Acts[position].Outcome != "replayed" || second.Acts[position].Event != first.Acts[position].Event {
			t.Fatalf("retry act %d = %#v, want the first run's event %s", position, second.Acts[position], first.Acts[position].Event)
		}
	}
	after := fixture.snapshot()
	if after.Head != landed.Head || after.Depth != landed.Depth {
		t.Fatalf("retry moved the log to %s depth %d, want %s depth %d", after.Head, after.Depth, landed.Head, landed.Depth)
	}
}

func TestBatchRefusesUndefinedLabelWithoutLanding(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot()
	report, err := fixture.run("operator", fmt.Sprintf(`[
	  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "undefined-first"},
	  {"verb": "state", "kind": "assert", "text": "second", "rests_on": ["$missing"], "idempotency_key": "undefined-second"}
	]`, fixture.genesis))
	var failure *batchError
	if !errors.As(err, &failure) || failure.Code != "reference" {
		t.Fatalf("undefined label error = %v", err)
	}
	if report.Landed != 0 || report.Replayed != 0 || report.Error == nil || report.Error.Code != "reference" {
		t.Fatalf("undefined label report = %#v", report)
	}
	if report.Acts[0].Outcome != "skipped" || report.Acts[0].Event != "" || report.Acts[1].Outcome != "failed" {
		t.Fatalf("undefined label acts = %#v", report.Acts)
	}
	after := fixture.snapshot()
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused batch moved the log to %s depth %d, want %s depth %d", after.Head, after.Depth, before.Head, before.Depth)
	}
}

// TestBatchRefusesTrailingInputWithoutLanding guards the parser boundary. A
// valid array followed by anything but whitespace must be refused before the
// first append. The stray-delimiter cases are the ones a decoder.More check
// lets through: More asks whether the value being parsed has another element,
// so a closing delimiter reads to it as a clean end of input and the acts
// before it land.
func TestBatchRefusesTrailingInputWithoutLanding(t *testing.T) {
	for _, testCase := range []struct{ name, trailing string }{
		{"stray array delimiter", "]"},
		{"stray object delimiter", "}"},
		{"delimiter on its own line", "\n]\n"},
		{"second value", `{"verb": "state", "kind": "assert", "text": "second"}`},
		{"unterminated value", "["},
		{"malformed bytes", "not json"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newBatchFixture(t)
			before := fixture.snapshot()
			printed, err := fixture.runFile("operator", fmt.Sprintf(`[
			  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "trailing-first"}
			]`, fixture.genesis)+testCase.trailing)
			var failure *batchError
			if !errors.As(err, &failure) || failure.Code != "input" {
				t.Fatalf("trailing %q error = %v, want a typed input failure", testCase.trailing, err)
			}
			if len(printed) != 0 {
				t.Fatalf("trailing %q printed a report before failing: %s", testCase.trailing, printed)
			}
			after := fixture.snapshot()
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("trailing %q moved the log to %s depth %d, want %s depth %d",
					testCase.trailing, after.Head, after.Depth, before.Head, before.Depth)
			}
		})
	}
	t.Run("trailing whitespace is not content", func(t *testing.T) {
		fixture := newBatchFixture(t)
		before := fixture.snapshot().Depth
		report, err := fixture.run("operator", fmt.Sprintf(`[
		  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "whitespace-first"}
		]`, fixture.genesis)+"\n\t \n")
		if err != nil {
			t.Fatal(err)
		}
		if report.Landed != 1 || report.Error != nil {
			t.Fatalf("whitespace-terminated report = %#v", report)
		}
		if depth := fixture.snapshot().Depth; depth != before+1 {
			t.Fatalf("depth = %d, want %d", depth, before+1)
		}
	})
}

// TestBatchRetryAfterPartialLandingReplaysPrefixAndLandsSuffix is the recovery
// case: the first run stops mid-chain, so only a prefix is durable. The second
// run of the same file replays that prefix under its idempotency key, resolves
// the label to the event already minted, and lands only the suffix.
func TestBatchRetryAfterPartialLandingReplaysPrefixAndLandsSuffix(t *testing.T) {
	fixture := newBatchFixture(t)
	// The second act is addressed to an actor the roster does not carry yet,
	// so it cannot be signed and the chain stops after the first act.
	acts := fmt.Sprintf(`[
	  {"label": "note", "verb": "state", "kind": "assert", "text": "the prefix is durable",
	   "rests_on": [%q], "idempotency_key": "partial-assert"},
	  {"verb": "state", "kind": "request", "text": "finish the chain",
	   "body": {"to": "@latecomer", "conditions": "the suffix lands once"},
	   "rests_on": ["$note"], "idempotency_key": "partial-request"}
	]`, fixture.genesis)

	before := fixture.snapshot()
	first, err := fixture.run("operator", acts)
	if err == nil {
		t.Fatal("batch naming an unknown performer succeeded, so no prefix was left partly landed")
	}
	if first.Landed != 1 || first.Replayed != 0 || first.Error == nil {
		t.Fatalf("partial run report = %#v", first)
	}
	if first.Acts[0].Outcome != "landed" || first.Acts[1].Outcome != "failed" {
		t.Fatalf("partial run acts = %#v", first.Acts)
	}
	partial := fixture.snapshot()
	if partial.Depth != before.Depth+1 {
		t.Fatalf("partial run depth = %d, want %d", partial.Depth, before.Depth+1)
	}

	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "latecomer", "agent"); err != nil {
		t.Fatal(err)
	}
	admitted := fixture.snapshot()

	second, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Landed != 1 || second.Replayed != 1 || second.Error != nil {
		t.Fatalf("retry report = %#v", second)
	}
	if second.Acts[0].Outcome != "replayed" || second.Acts[0].Event != first.Acts[0].Event {
		t.Fatalf("retry act 0 = %#v, want the first run's event %s replayed", second.Acts[0], first.Acts[0].Event)
	}
	if second.Acts[1].Outcome != "landed" || second.Acts[1].Event == "" {
		t.Fatalf("retry act 1 = %#v, want the suffix landed", second.Acts[1])
	}
	after := fixture.snapshot()
	if after.Depth != admitted.Depth+1 {
		t.Fatalf("retry depth = %d, want %d: only the suffix should have landed", after.Depth, admitted.Depth+1)
	}
	if !contains(after.Projection.Provenance[second.Acts[1].Event], first.Acts[0].Event) {
		t.Fatalf("suffix provenance = %#v, want the replayed prefix event %s",
			after.Projection.Provenance[second.Acts[1].Event], first.Acts[0].Event)
	}
	assertions := 0
	for _, statement := range after.Projection.Statements {
		if statement.Text == "the prefix is durable" {
			assertions++
		}
	}
	if assertions != 1 {
		t.Fatalf("the prefix act appears %d times in the projection, want once", assertions)
	}
}

func TestBatchMixesStateAndRatify(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	report, err := fixture.run("operator", fmt.Sprintf(`[
	  {"label": "note", "verb": "state", "kind": "assert", "text": "the log is verified once",
	   "rests_on": [%q], "idempotency_key": "mixed-assert"},
	  {"verb": "ratify", "target": "$note", "idempotency_key": "mixed-ratify"}
	]`, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if report.Landed != 2 || report.Error != nil {
		t.Fatalf("mixed report = %#v", report)
	}
	snapshot := fixture.snapshot()
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d", snapshot.Depth, before+2)
	}
	if statement := statementByEvent(t, snapshot.Projection, report.Acts[0].Event); !statement.Ratified {
		t.Fatalf("assert %s is not ratified", report.Acts[0].Event)
	}
	ratification := actByEvent(t, snapshot.Projection, report.Acts[1].Event)
	if ratification.Target != report.Acts[0].Event || ratification.Verdict != workroom.Effective {
		t.Fatalf("ratify act = %#v, want an effective ratification of %s", ratification, report.Acts[0].Event)
	}
}

type batchFixture struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	genesis   string
}

func newBatchFixture(t *testing.T) batchFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-b", "main", repo)
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	return batchFixture{t: t, ctx: ctx, repo: repo, workspace: workspace, genesis: workspace.EventID(workspace.Config.Genesis)}
}

func (f batchFixture) snapshot() app.Snapshot {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

// runFile writes the acts to a file, runs the batch command against it, and
// returns whatever the command printed together with its error. Input rejected
// before the first append prints nothing at all, so the raw bytes matter.
func (f batchFixture) runFile(actor, acts string) ([]byte, error) {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(acts), 0o600); err != nil {
		f.t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	batchErr := batchCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, path})
	os.Stdout = stdout
	writer.Close()
	printed, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	return printed, batchErr
}

// run is runFile for the cases that reach the report the command prints.
func (f batchFixture) run(actor, acts string) (batchReport, error) {
	f.t.Helper()
	printed, batchErr := f.runFile(actor, acts)
	var report batchReport
	if err := json.Unmarshal(printed, &report); err != nil {
		f.t.Fatalf("decode batch report %q: %v", printed, err)
	}
	return report, batchErr
}

func actByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Act {
	t.Helper()
	for _, act := range projection.Acts {
		if act.Event == event {
			return act
		}
	}
	t.Fatalf("act %s not found", event)
	return workroom.Act{}
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

// A client should not have to be told where the service is. Serving publishes
// the address actually bound, beside the workroom config in the repository, and
// takes the advertisement back when it stops.
func TestServePublishesWhereItListensAndWithdrawsOnExit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveCommand(ctx, []string{"--repo", repo, "--listen", "127.0.0.1:0"}) }()

	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := workspace.ResidentURL(); ok {
			url = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		stop()
		t.Fatal("serving never published its address")
	}
	response, err := http.Get(url + "/v0/presence")
	if err != nil {
		stop()
		t.Fatalf("the published address does not answer: %v", err)
	}
	response.Body.Close()

	stop()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serving failed: %v", err)
	}
	if published, ok := workspace.ResidentURL(); ok {
		t.Fatalf("a stopped service is still advertised at %q", published)
	}
}

// The test above cancels a context that the command line never handed the
// command, so on its own it proves only that the withdrawal works when someone
// asks for it. What a person does is press Ctrl-C, and the whole point of the
// advertisement is that it must not outlive the process it names. These tests
// therefore run the real binary and stop it the real way.
func TestServeWithdrawsWhenTheProcessIsInterrupted(t *testing.T) {
	binary := buildGS(t)
	repo, workspace := servableRepository(t)

	serving := startServing(t, binary, repo)
	url := awaitPublication(t, workspace, "")
	response, err := http.Get(url + "/v0/presence")
	if err != nil {
		t.Fatalf("the published address does not answer: %v", err)
	}
	response.Body.Close()

	interrupt(t, serving)
	if err := serving.Wait(); err != nil {
		t.Fatalf("stopping normally was reported as a failure: %v", err)
	}
	if published, ok := workspace.ResidentURL(); ok {
		t.Fatalf("an interrupted service is still advertised at %q", published)
	}
}

// Withdrawal is conditional for a reason: a service that took the repository
// over is still serving it, and erasing its record would send every client into
// degraded mode. Stopping the service it replaced must leave it alone.
func TestAnInterruptedServiceLeavesItsSuccessorAdvertised(t *testing.T) {
	binary := buildGS(t)
	repo, workspace := servableRepository(t)

	replaced := startServing(t, binary, repo)
	first := awaitPublication(t, workspace, "")
	successor := startServing(t, binary, repo)
	second := awaitPublication(t, workspace, first)

	interrupt(t, replaced)
	if err := replaced.Wait(); err != nil {
		t.Fatalf("stopping normally was reported as a failure: %v", err)
	}
	published, ok := workspace.ResidentURL()
	if !ok || published != second {
		t.Fatalf("stopping a replaced service took the successor's advertisement %q down to %q/%v", second, published, ok)
	}
	response, err := http.Get(published + "/v0/presence")
	if err != nil {
		t.Fatalf("the surviving advertisement does not answer: %v", err)
	}
	response.Body.Close()

	interrupt(t, successor)
	if err := successor.Wait(); err != nil {
		t.Fatalf("stopping normally was reported as a failure: %v", err)
	}
	if published, ok := workspace.ResidentURL(); ok {
		t.Fatalf("the last service left the repository advertised at %q", published)
	}
}

// buildGS compiles the command as it is actually installed. Calling
// serveCommand in process cannot see the defect these tests exist to catch,
// because the defect was in what main hands the command.
func buildGS(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gs")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("building gs: %v: %s", err, output)
	}
	return binary
}

func servableRepository(t *testing.T) (string, *app.Workspace) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return repo, workspace
}

func startServing(t *testing.T, binary, repo string) *exec.Cmd {
	t.Helper()
	serving := exec.Command(binary, "serve", "--repo", repo, "--listen", "127.0.0.1:0")
	serving.Stdout, serving.Stderr = os.Stderr, os.Stderr
	if err := serving.Start(); err != nil {
		t.Fatalf("starting gs serve: %v", err)
	}
	t.Cleanup(func() {
		if serving.ProcessState == nil {
			_ = serving.Process.Kill()
			_ = serving.Wait()
		}
	})
	return serving
}

// awaitPublication waits for an advertisement other than the one already
// there, so a successor's record is never mistaken for its predecessor's.
func awaitPublication(t *testing.T, workspace *app.Workspace, previous string) string {
	t.Helper()
	for attempt := 0; attempt < 600; attempt++ {
		if published, ok := workspace.ResidentURL(); ok && published != previous {
			return published
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serving never published its address")
	return ""
}

func interrupt(t *testing.T, serving *exec.Cmd) {
	t.Helper()
	if err := serving.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupting gs serve: %v", err)
	}
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
