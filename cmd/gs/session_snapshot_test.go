package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Where a step command's projection comes from. The resident already holds the
// verified fold, so a command that names one must read it from there and fold
// nothing; and it must fall back, saying so, to anything it cannot confirm is
// this workroom, at this head, under these rules. Each test removes exactly one
// of those guards by name.

// residentStub serves this workspace's own resident with one lie told about
// the complete status answer, which is the only answer sessionSnapshot reads.
// Everything else is the real resident, so a test changing one field proves
// exactly the check that field belongs to.
func residentStub(t *testing.T, workspace *app.Workspace, tamper func(*service.Status)) (string, *atomic.Int64) {
	t.Helper()
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/status" || tamper == nil {
			server.Handler().ServeHTTP(writer, request)
			return
		}
		recorded := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorded, request)
		var status service.Status
		if err := json.Unmarshal(recorded.Body.Bytes(), &status); err != nil {
			t.Errorf("resident status was not JSON: %v", err)
			return
		}
		tamper(&status)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(status)
	}))
	return listener.URL, &hits
}

// refusedLocalLoad is the local audit as a test can see it: a load that must
// not happen. A resident-sourced session that folds the log anyway fails here
// rather than merely running slowly, which is the whole point of the change.
func refusedLocalLoad(t *testing.T) func(context.Context) (app.Snapshot, error) {
	t.Helper()
	return func(context.Context) (app.Snapshot, error) {
		t.Error("the durable log was verified locally while the resident was current")
		return app.Snapshot{}, nil
	}
}

// sentinelLocalLoad is the local audit standing in for itself: it answers with
// a head no resident served, so a test can tell which source answered.
func sentinelLocalLoad(called *bool) func(context.Context) (app.Snapshot, error) {
	return func(context.Context) (app.Snapshot, error) {
		*called = true
		return app.Snapshot{Head: "local-audit"}, nil
	}
}

func TestSessionSnapshotTakesTheResidentProjectionWithoutFoldingLocally(t *testing.T) {
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	url, hits := residentStub(t, workspace, nil)

	snapshot, err := loadSessionSnapshot(ctx, io.Discard, workspace, url, refusedLocalLoad(t))
	if err != nil {
		t.Fatalf("resident-sourced session: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if snapshot.Head != summary.Durable.Head {
		t.Fatalf("session head = %s, want the resident and checkout head %s", snapshot.Head, summary.Durable.Head)
	}
	if len(snapshot.Projection.Decisions) != snapshot.Depth {
		t.Fatalf("the resident answer carried %d decisions at depth %d", len(snapshot.Projection.Decisions), snapshot.Depth)
	}
}

// Without --server nothing about this path exists: the local audit answers and
// no resident is dialed, whatever one is advertised.
func TestSessionSnapshotWithoutServerReadsTheLocalLog(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	called := false
	snapshot, err := loadSessionSnapshot(ctx, io.Discard, workspace, "", sentinelLocalLoad(&called))
	if err != nil || !called || snapshot.Head != "local-audit" {
		t.Fatalf("local session head = %s, called %v, err %v", snapshot.Head, called, err)
	}
}

// The four refusals, each with one field changed and every other check passing.
func TestSessionSnapshotFallsBackToTheLocalAudit(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*service.Status)
		reason string
	}{
		{"head is not current", func(status *service.Status) {
			status.Durable.Head = strings.Repeat("0", len(status.Durable.Head))
		}, "head is not current"},
		{"genesis is another workroom", func(status *service.Status) {
			status.Durable.Genesis = strings.Repeat("1", len(status.Durable.Genesis))
		}, "genesis does not match"},
		{"profile is another interpreter", func(status *service.Status) {
			status.Profile = "profile:00000000000000000000000000000000"
		}, "fold profile"},
		{"projection is truncated", func(status *service.Status) {
			status.Durable.Projection.Decisions = status.Durable.Projection.Decisions[:len(status.Durable.Projection.Decisions)-1]
		}, "decisions at depth"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			workspace, _ := statusSummaryFixture(t)
			url, hits := residentStub(t, workspace, test.tamper)
			var progress strings.Builder
			called := false
			snapshot, err := loadSessionSnapshot(ctx, &progress, workspace, url, sentinelLocalLoad(&called))
			if err != nil {
				t.Fatalf("a resident this checkout cannot accept must degrade, not fail: %v", err)
			}
			if hits.Load() == 0 {
				t.Fatal("the resident was never dialed")
			}
			if !called || snapshot.Head != "local-audit" {
				t.Fatalf("the refused resident answer was used: head %s at depth %d", snapshot.Head, snapshot.Depth)
			}
			if !strings.Contains(progress.String(), "verifying the durable log locally") {
				t.Fatalf("the fallback was not stated on standard error: %q", progress.String())
			}
			if !strings.Contains(progress.String(), test.reason) {
				t.Fatalf("the stated reason %q does not name %q", progress.String(), test.reason)
			}
		})
	}
}

// An unreachable resident is the ordinary case of the same rule.
func TestSessionSnapshotFallsBackWhenTheResidentDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	var progress strings.Builder
	called := false
	snapshot, err := loadSessionSnapshot(ctx, &progress, workspace, "http://127.0.0.1:1/", sentinelLocalLoad(&called))
	if err != nil || !called || snapshot.Head != "local-audit" {
		t.Fatalf("unreachable resident head = %s, called %v, err %v", snapshot.Head, called, err)
	}
	if !strings.Contains(progress.String(), "verifying the durable log locally") {
		t.Fatalf("the fallback was not stated: %q", progress.String())
	}
}

// The act a step command submits is built from the projection it judged. The
// resident's answer carries one request whose text this checkout's own fold
// does not hold, and the promise gs promise signs quotes it: the world that
// admitted the act and the world that composed it are the same one.
func TestStepCommandSubmitsTheActItJudgedAgainstTheResidentProjection(t *testing.T) {
	fixture := newWorkflowFixture(t)
	lane := fixture.buildStepLane(t, "resident-lane", "reviewer", "operator", "docs/lane.md")
	marker := "only the resident says this"
	url, hits := residentStub(t, fixture.workspace, func(status *service.Status) {
		for index := range status.Durable.Projection.Statements {
			if status.Durable.Projection.Statements[index].Event == lane.request {
				status.Durable.Projection.Statements[index].Text = marker
			}
		}
	})

	if err := promiseCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator",
		"--server", url, lane.request}); err != nil {
		t.Fatalf("promise against the resident projection: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	promise := fixture.latestStatement(t)
	if promise.Kind != workroom.KindPromise {
		t.Fatalf("the newest statement is a %s, not a promise", promise.Kind)
	}
	if !strings.Contains(promise.Text, marker) {
		t.Fatalf("the promise text %q was not composed from the resident's projection", promise.Text)
	}
}

// And the same command falls back, saying so, to a resident it cannot accept —
// with the act still filed, from the local fold's own reading of the request.
func TestStepCommandFallsBackToTheLocalAuditAndSaysSo(t *testing.T) {
	fixture := newWorkflowFixture(t)
	lane := fixture.buildStepLane(t, "fallback-lane", "reviewer", "operator", "docs/lane.md")
	url, _ := residentStub(t, fixture.workspace, func(status *service.Status) {
		status.Durable.Head = strings.Repeat("0", len(status.Durable.Head))
	})
	commandErr, _, stderr := runPiped(t, func() error {
		return promiseCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator",
			"--server", url, lane.request})
	})
	if commandErr != nil {
		t.Fatalf("a refused resident must degrade to the local read, not fail: %v", commandErr)
	}
	if !strings.Contains(stderr, "verifying the durable log locally") {
		t.Fatalf("the fallback was not stated on standard error: %q", stderr)
	}
	if promise := fixture.latestStatement(t); promise.Kind != workroom.KindPromise {
		t.Fatalf("the newest statement is a %s, not a promise", promise.Kind)
	}
}

// gs work --next reads the projection itself, for the facts its bounded page
// does not carry. It reads it through the same opener, so a resident standing
// here answers it: the assert this run prints exists only in the resident's
// answer.
func TestWorkNextReadsTheResidentProjection(t *testing.T) {
	fixture := newWorkflowFixture(t)
	blocker := fixture.stateV3(t, "operator", workroom.KindAssert, "the lane is blocked", nil, fixture.promise)
	marker := "only the resident says this"
	url, hits := residentStub(t, fixture.workspace, func(status *service.Status) {
		for index := range status.Durable.Projection.Statements {
			if status.Durable.Projection.Statements[index].Event == blocker {
				status.Durable.Projection.Statements[index].Text = marker
			}
		}
	})
	workspace, err := app.Open(fixture.ctx, fixture.repo)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := workNext(fixture.ctx, workspace, url, statusview.WorkPage{Actor: statusview.ActorRef{Name: "reviewer"}},
		fixture.fingerprint(t, "reviewer"))
	if err != nil {
		t.Fatalf("work --next against the resident projection: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if !strings.Contains(lines, marker) {
		t.Fatalf("work --next did not read the resident's projection:\n%s", lines)
	}
}

// The preflighting commands resolve their references against the same opener,
// so the world a citation is read in is the resident's too. The resident's
// answer knows this event under another identifier, and gs state refuses the
// abbreviation it would otherwise have resolved — with nothing appended.
func TestStateResolvesReferencesInTheResidentProjection(t *testing.T) {
	fixture := newWorkflowFixture(t)
	hidden := fixture.ground
	url, hits := residentStub(t, fixture.workspace, func(status *service.Status) {
		for index := range status.Durable.Projection.Decisions {
			if status.Durable.Projection.Decisions[index].Event == hidden {
				status.Durable.Projection.Decisions[index].Event = strings.Repeat("f", len(hidden))
			}
		}
	})
	before := fixture.snapshot(t).Depth
	abbreviation := hidden[len(hidden)-12:]
	err := stateCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator", "--server", url,
		"--kind", "assert", "--text", "a note on the ground", "--rests-on", abbreviation})
	if err == nil {
		t.Fatal("the abbreviation resolved against a projection that does not hold it")
	}
	if !strings.Contains(err.Error(), abbreviation) {
		t.Fatalf("the refusal does not name the reference: %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("depth moved from %d to %d on a refusal", before, after)
	}
}
