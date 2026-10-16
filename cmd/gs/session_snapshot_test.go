package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
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
	// One command reads one projection. A chain of acts asks this question
	// either side of every act, and the answer is tens of megabytes.
	dialed := hits.Load()
	again, err := loadSessionSnapshot(ctx, io.Discard, workspace, url, refusedLocalLoad(t))
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if again.Head != snapshot.Head {
		t.Fatalf("the second read answered head %s, want the first answer %s", again.Head, snapshot.Head)
	}
	if hits.Load() != dialed {
		t.Fatalf("the resident was read again: %d requests, want %d", hits.Load(), dialed)
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

// moveTheRef appends one act to this workroom, which is how a test makes the
// sequence ref move while an answer is being read.
func moveTheRef(t *testing.T, workspace *app.Workspace) {
	t.Helper()
	if _, err := workspace.Act(context.Background(), "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the world moved under the read",
		IdempotencyKey: "session-snapshot-moving-head",
	}); err != nil {
		t.Errorf("moving the ref: %v", err)
	}
}

// The refusals, each with one thing changed and every other check passing.
func TestSessionSnapshotFallsBackToTheLocalAudit(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(*testing.T, *app.Workspace, *service.Status)
		reason string
	}{
		{"head is not current", func(_ *testing.T, _ *app.Workspace, status *service.Status) {
			status.Durable.Head = strings.Repeat("0", len(status.Durable.Head))
		}, "head is not current"},
		{"genesis is another workroom", func(_ *testing.T, _ *app.Workspace, status *service.Status) {
			status.Durable.Genesis = strings.Repeat("1", len(status.Durable.Genesis))
		}, "genesis does not match"},
		{"profile is another interpreter", func(_ *testing.T, _ *app.Workspace, status *service.Status) {
			status.Profile = "profile:00000000000000000000000000000000"
		}, "fold profile"},
		{"projection is truncated", func(_ *testing.T, _ *app.Workspace, status *service.Status) {
			status.Durable.Projection.Decisions = status.Durable.Projection.Decisions[:len(status.Durable.Projection.Decisions)-1]
		}, "decisions at depth"},
		// The head the resident named was this checkout's when the request
		// went out and is not when the answer arrives, because the workroom
		// moved while it was being read. Only the second ref read catches
		// this: the answer itself is entirely truthful.
		{"head moved while the answer was read", func(t *testing.T, workspace *app.Workspace, _ *service.Status) {
			moveTheRef(t, workspace)
		}, "moved while resident status was read"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			workspace, _ := statusSummaryFixture(t)
			url, hits := residentStub(t, workspace, func(status *service.Status) { test.tamper(t, workspace, status) })
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

// The vocabulary an act is warned against comes from the same opener too. The
// resident's answer is the only place this workroom does not define `promise`,
// and the warning follows it: nothing here folds the log a second time after
// the act has landed.
func TestUndefinedKindWarningReadsTheResidentVocabulary(t *testing.T) {
	workspace, _ := statusSummaryFixture(t)
	url, hits := residentStub(t, workspace, func(status *service.Status) {
		kept := status.Durable.Vocabulary.Definitions[:0]
		for _, definition := range status.Durable.Vocabulary.Definitions {
			if definition.Name != workroom.KindPromise {
				kept = append(kept, definition)
			}
		}
		status.Durable.Vocabulary.Definitions = kept
	})
	_, _, stderr := runPiped(t, func() error {
		warnUndefinedKind(context.Background(), workspace, url, workroom.KindPromise)
		return nil
	})
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if !strings.Contains(stderr, "promise") || !strings.Contains(stderr, "warning") {
		t.Fatalf("the warning did not read the resident's vocabulary: %q", stderr)
	}
}

// A resident that could not answer the page has been dialed and named once.
// gs work --next needs the projection as well as the page, and asking the same
// refused listener a second time costs a second dial and tells the reader the
// same thing twice in different words.
func TestWorkNextDialsARefusedResidentOnce(t *testing.T) {
	fixture := newWorkflowFixture(t)
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "no", http.StatusInternalServerError)
	}))
	commandErr, _, stderr := runPiped(t, func() error {
		return workCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator", "--next", "--server", listener.URL})
	})
	if commandErr != nil {
		t.Fatalf("a refused resident must degrade to the local read, not fail: %v", commandErr)
	}
	if dialed := hits.Load(); dialed != 1 {
		t.Fatalf("the refused resident was dialed %d times, want 1", dialed)
	}
	if said := strings.Count(stderr, "unavailable"); said != 1 {
		t.Fatalf("the refusal was reported %d times: %q", said, stderr)
	}
}

// paddingToken marks the one string field paddedResidentStub grows.
const paddingToken = "padding-token-5d26227f"

// paddedResidentStub is residentStub with the complete status answer grown to
// exactly size bytes. tamper puts paddingToken into one string field, and the
// padding is written in its place as the answer streams, so a test that needs
// an answer of tens of megabytes holds it once, on the reading side, as the
// command does.
func paddedResidentStub(t *testing.T, workspace *app.Workspace, size int64, tamper func(*service.Status)) (string, *atomic.Int64) {
	t.Helper()
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int64
	listener := countingServer(t, &hits, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/status" {
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
		encoded, err := json.Marshal(status)
		if err != nil {
			t.Errorf("encoding the tampered status: %v", err)
			return
		}
		head, tail, found := bytes.Cut(encoded, []byte(paddingToken))
		padding := size - int64(len(head)+len(tail))
		if !found || padding < 0 {
			t.Errorf("cannot pad a %d-byte answer to %d bytes (token found %v)", len(encoded), size, found)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(head)
		chunk := bytes.Repeat([]byte("x"), 1<<20)
		for padding > 0 {
			step := min(padding, int64(len(chunk)))
			if _, err := writer.Write(chunk[:step]); err != nil {
				return
			}
			padding -= step
		}
		_, _ = writer.Write(tail)
	}))
	return listener.URL, &hits
}

// The complete answer grows with the log, so its ceiling grows with this
// checkout's own count of records. An answer past the 64 MiB that was once the
// whole ceiling is read like any other; an answer past the allowance for this
// depth is still refused, and the command says so and audits locally.
func TestSessionSnapshotCeilingGrowsWithTheLocalLog(t *testing.T) {
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	limit, err := fullResponseLimit(ctx, workspace, summary.Durable.Head)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(64<<20) + int64(summary.Durable.Depth+1)*(8<<10); limit != want {
		t.Fatalf("ceiling at depth %d = %d bytes, want %d", summary.Durable.Depth, limit, want)
	}
	cases := []struct {
		name      string
		size      int64
		fallsBack bool
	}{
		{"past the old fixed ceiling", 64<<20 + 1, false},
		{"exactly at this depth's ceiling", limit, false},
		{"past this depth's ceiling", limit + 1, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			url, hits := paddedResidentStub(t, workspace, test.size, func(status *service.Status) {
				status.TrustBoundary = paddingToken
			})
			var progress strings.Builder
			called := false
			snapshot, err := loadSessionSnapshot(ctx, &progress, workspace, url, sentinelLocalLoad(&called))
			if err != nil {
				t.Fatalf("session: %v", err)
			}
			if hits.Load() == 0 {
				t.Fatal("the resident was never dialed")
			}
			if called != test.fallsBack {
				t.Fatalf("local audit called = %v, want %v; stderr %q", called, test.fallsBack, progress.String())
			}
			if test.fallsBack && !strings.Contains(progress.String(), fmt.Sprintf("resident response exceeds %d bytes", limit)) {
				t.Fatalf("the fallback did not name the ceiling: %q", progress.String())
			}
			if !test.fallsBack && snapshot.Head != summary.Durable.Head {
				t.Fatalf("session head = %s, want the resident's %s", snapshot.Head, summary.Durable.Head)
			}
		})
	}
}

// And a step command reads such an answer end to end: the promise quotes text
// only the resident's projection holds, and nothing is audited locally.
func TestStepCommandReadsAResidentAnswerPastTheOldCeiling(t *testing.T) {
	fixture := newWorkflowFixture(t)
	lane := fixture.buildStepLane(t, "large-lane", "reviewer", "operator", "docs/lane.md")
	marker := "only the large resident answer says this"
	url, hits := paddedResidentStub(t, fixture.workspace, 64<<20+1, func(status *service.Status) {
		for index := range status.Durable.Projection.Statements {
			if status.Durable.Projection.Statements[index].Event == lane.request {
				status.Durable.Projection.Statements[index].Text = marker
			}
		}
		status.TrustBoundary = paddingToken
	})
	commandErr, _, stderr := runPiped(t, func() error {
		return promiseCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator",
			"--server", url, lane.request})
	})
	if commandErr != nil {
		t.Fatalf("promise against a large resident answer: %v", commandErr)
	}
	if hits.Load() == 0 {
		t.Fatal("the resident was never dialed")
	}
	if strings.Contains(stderr, "verifying the durable log locally") {
		t.Fatalf("the command fell back to the local audit: %q", stderr)
	}
	if promise := fixture.latestStatement(t); !strings.Contains(promise.Text, marker) {
		t.Fatalf("the promise text %q was not composed from the resident's projection", promise.Text)
	}
}

// gs status --all and --json, gs artifacts and gs reviews read the complete
// answer through residentStatus, which documents a head unmoved while the
// answer was read. Here the answer is truthful about the head the ref moved to
// during the read, so only the read taken before the request refuses it.
func TestResidentStatusRefusesAHeadThatMovedDuringTheRead(t *testing.T) {
	ctx := context.Background()
	workspace, _ := statusSummaryFixture(t)
	url, _ := residentStub(t, workspace, func(status *service.Status) {
		moveTheRef(t, workspace)
		moved, err := workspace.Store.Head(ctx, kernel.Ref(workspace.View().Genesis))
		if err != nil {
			t.Errorf("reading the moved ref: %v", err)
		}
		status.Durable.Head = moved
	})
	if _, err := residentStatus(ctx, workspace, url); err == nil || !strings.Contains(err.Error(), "moved while resident status was read") {
		t.Fatalf("an answer read across a moving ref was accepted or refused for another reason: %v", err)
	}
}
