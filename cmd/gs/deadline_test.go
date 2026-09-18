package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The deadline belongs to one invocation, so these tests read it the way a
// command does: through the flags that invocation was given.
func deadlineFor(t *testing.T, arguments ...string) (time.Duration, error) {
	t.Helper()
	set, _ := actFlags("state", arguments)
	set.SetOutput(&strings.Builder{})
	if err := set.Parse(arguments); err != nil {
		return 0, err
	}
	ctx, err := withSubmitDeadline(context.Background(), set)
	if err != nil {
		return 0, err
	}
	return submitDeadline(ctx), nil
}

func TestSubmitDeadlineComesFromTheFlagThenTheEnvironment(t *testing.T) {
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "")
	if deadline, err := deadlineFor(t); err != nil || deadline != residentclient.DefaultSubmitDeadline {
		t.Fatalf("default deadline = %s, %v, want %s", deadline, err, residentclient.DefaultSubmitDeadline)
	}
	if deadline, err := deadlineFor(t, "--deadline", "45s"); err != nil || deadline != 45*time.Second {
		t.Fatalf("flag deadline = %s, %v", deadline, err)
	}
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "3m")
	if deadline, err := deadlineFor(t); err != nil || deadline != 3*time.Minute {
		t.Fatalf("environment deadline = %s, %v", deadline, err)
	}
	if deadline, err := deadlineFor(t, "--deadline", "20s"); err != nil || deadline != 20*time.Second {
		t.Fatalf("flag did not win over the environment: %s, %v", deadline, err)
	}
	if _, err := deadlineFor(t, "--deadline", "soon"); err == nil || !strings.Contains(err.Error(), "30s") {
		t.Fatalf("unreadable deadline error = %v", err)
	}
}

// A context that was never given a deadline answers with the one every caller
// had before the deadline could be set at all, so a helper called directly
// submits exactly as it always did.
func TestSubmitDeadlineFallsBackToTheDefault(t *testing.T) {
	if deadline := submitDeadline(context.Background()); deadline != residentclient.DefaultSubmitDeadline {
		t.Fatalf("deadline without a context value = %s, want %s", deadline, residentclient.DefaultSubmitDeadline)
	}
	stale := context.WithValue(context.Background(), submitDeadlineKey{}, time.Duration(0))
	if deadline := submitDeadline(stale); deadline != residentclient.DefaultSubmitDeadline {
		t.Fatalf("deadline from a zero value = %s, want the default", deadline)
	}
}

// The deadline on the context has to reach the dial, not merely be carried.
// A resident that never answers is the only way to see which deadline was used:
// the submission stops when this invocation's deadline expires and not at some
// other time. submitRequest is asked directly, because a whole command would
// also spend its own read timeouts on the same stalled server and those are not
// what this pins.
func TestSubmitRequestWaitsForThisInvocationsDeadline(t *testing.T) {
	fixture := newWorkflowFixture(t)
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	// Handlers are released before Close, which otherwise waits on a connection
	// the client has already given up on.
	t.Cleanup(func() { close(release); stalled.Close() })

	_, private, err := fixture.workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	built, err := fixture.workspace.BuildActRequest(fixture.ctx, private, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "waits as long as it was told to",
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "deadline-probe",
	})
	if err != nil {
		t.Fatal(err)
	}

	waited := func(t *testing.T, deadline time.Duration) time.Duration {
		t.Helper()
		ctx := context.WithValue(fixture.ctx, submitDeadlineKey{}, deadline)
		started := time.Now()
		if _, err := submitRequest(ctx, fixture.workspace, stalled.URL, built); err == nil {
			t.Fatal("a resident that never answered was read as a successful append")
		} else if !residentclient.TimedOut(err) {
			t.Fatalf("stalled resident error = %v, want an expired deadline", err)
		}
		return time.Since(started)
	}

	if quick := waited(t, 200*time.Millisecond); quick > 3*time.Second {
		t.Fatalf("waited %s under a 200ms deadline: the deadline did not reach the dial", quick)
	}
	if patient := waited(t, 1500*time.Millisecond); patient < time.Second {
		t.Fatalf("waited only %s under a 1500ms deadline: the deadline did not reach the dial", patient)
	}
}

// A command that appends accepts the flag and refuses a value it cannot read
// before anything is signed. This is the link between the flag and the context:
// a command that forgot to bind it would refuse the flag as undefined, and one
// that bound it without resolving it would accept nonsense.
func TestSubmittingCommandsRefuseAnUnreadableDeadline(t *testing.T) {
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "")
	fixture := newWorkflowFixture(t)
	err := stateCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--kind", "assert",
		"--text", "never signed", "--rests-on", fixture.artifact, "--deadline", "soon",
	})
	if err == nil || !strings.Contains(err.Error(), "--deadline") || !strings.Contains(err.Error(), "30s") {
		t.Fatalf("state with an unreadable deadline = %v", err)
	}
	before := fixture.snapshot(t).Depth
	if err := stateCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--kind", "assert",
		"--text", "never signed either", "--rests-on", fixture.artifact, "--deadline", "0s",
	}); err == nil {
		t.Fatal("a deadline of zero was accepted")
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("a refused deadline still appended: depth %d -> %d", before, after)
	}
}

// What a caller is told after a batch act times out is the whole of the
// recovery: the act may have landed, and whether it can be replayed depends on
// whether it carries a key of its own.
func TestBatchSubmitRefusalNamesTheKeyToReplayUnder(t *testing.T) {
	expired := &residentclient.TransportError{Err: context.DeadlineExceeded}
	keyed := batchSubmitRefusal(expired, 2, batchAct{IdempotencyKey: "publish-report"}, 30*time.Second)
	for _, want := range []string{"may already have landed", "position 2", "publish-report", "30s", "--deadline"} {
		if !strings.Contains(keyed.Error(), want) {
			t.Fatalf("timed-out refusal does not say %q: %v", want, keyed)
		}
	}
	if !errors.Is(keyed, context.DeadlineExceeded) {
		t.Fatalf("timed-out refusal dropped the cause: %v", keyed)
	}

	unkeyed := batchSubmitRefusal(expired, 0, batchAct{}, 30*time.Second)
	for _, want := range []string{"no idempotency_key", "second copy", "find that act first"} {
		if !strings.Contains(unkeyed.Error(), want) {
			t.Fatalf("refusal for an act with no key does not say %q: %v", want, unkeyed)
		}
	}

	// A refusal is definite, so it keeps its own words: telling an author their
	// act may have landed when the resident said it did not is worse than
	// saying nothing.
	refused := errors.New("rests on a retired basis")
	if got := batchSubmitRefusal(refused, 1, batchAct{IdempotencyKey: "k"}, 30*time.Second); got.Error() != refused.Error() {
		t.Fatalf("refusal was rewritten as a lost answer: %v", got)
	}
}

// The batch has to say it, not merely be able to: a run that stops on an
// expired deadline reports the key of the act whose fate is unknown.
func TestBatchReportsTheReplayKeyWhenAnActsDeadlineExpires(t *testing.T) {
	fixture := newWorkflowFixture(t)
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); stalled.Close() })

	_, private, err := fixture.workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	acts := []batchAct{{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the first act of the file",
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "batch-deadline-probe",
	}}
	ctx := context.WithValue(fixture.ctx, submitDeadlineKey{}, 200*time.Millisecond)
	report, err := runBatch(ctx, fixture.workspace, stalled.URL, "reviewer", private, acts, false)
	if err == nil {
		t.Fatal("a batch whose act never got an answer reported success")
	}
	for _, want := range []string{"may already have landed", "batch-deadline-probe", "200ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("batch refusal does not say %q: %v", want, err)
		}
	}
	if report.Acts[0].Outcome != "failed" || report.Landed != 0 {
		t.Fatalf("batch report = %+v", report)
	}
}
