package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
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

// Every command that appends has to bind the flag, not merely register it: one
// that parsed --deadline and then submitted under the old wait would look
// configured and behave exactly as before. The refusal is the observable end of
// that binding, and it is asked of each of the twelve in turn, because the
// binding is a line per command and a missing line is silent.
func TestEveryAppendingCommandBindsItsDeadline(t *testing.T) {
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "")
	fixture := newWorkflowFixture(t)
	for _, command := range []struct {
		name string
		run  func(context.Context, []string) error
		args []string
	}{
		{name: "state", run: stateCommand, args: []string{"--kind", "assert", "--text", "never signed", "--rests-on", fixture.artifact}},
		{name: "promise", run: promiseCommand, args: []string{fixture.request}},
		{name: "artifact", run: artifactCommand, args: []string{"--head", fixture.candidate, "--promise", fixture.promise, "spike"}},
		{name: "review", run: reviewCommand, args: []string{"--artifact", fixture.artifact, "--promise", fixture.promise, "--verdict", "approved"}},
		{name: "review-request", run: reviewRequestCommand, args: []string{"--head", fixture.candidate, "--to", "reviewer"}},
		{name: "ratify", run: ratifyCommand, args: []string{fixture.artifact}},
		{name: "supersede", run: supersedeCommand, args: []string{fixture.artifact, "--text", "withdrawn"}},
		{name: "reassign-if-unclaimed", run: reassignIfUnclaimedCommand, args: []string{fixture.request, "--to", "reviewer"}},
		{name: "batch", run: batchCommand, args: []string{"-"}},
		{name: "merge", run: mergeCommand, args: []string{"--approval", fixture.artifact}},
		{name: "land", run: landCommand, args: []string{"--approval", fixture.artifact, "--checkout", fixture.repo}},
		{name: "publish", run: publishCommand, args: []string{}},
	} {
		t.Run(command.name, func(t *testing.T) {
			arguments := append([]string{"--repo", fixture.repo, "--as", "reviewer", "--deadline", "soon"}, command.args...)
			err := command.run(fixture.ctx, arguments)
			if err == nil {
				t.Fatalf("gs %s accepted an unreadable deadline", command.name)
			}
			if !strings.Contains(err.Error(), "--deadline") || !strings.Contains(err.Error(), "30s") {
				t.Fatalf("gs %s did not refuse on the deadline: %v", command.name, err)
			}
		})
	}
}

// A refused deadline stops the command before anything is signed, and the
// refusal is the command's own usage error rather than a failure later on.
func TestSubmittingCommandsRefuseAnUnreadableDeadline(t *testing.T) {
	t.Setenv(residentclient.SubmitDeadlineEnvironment, "")
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	for _, value := range []string{"soon", "0s", "-1m"} {
		err := stateCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "reviewer", "--kind", "assert",
			"--text", "never signed", "--rests-on", fixture.artifact, "--deadline", value,
		})
		if err == nil {
			t.Fatalf("state accepted %q as a deadline", value)
		}
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("a refused deadline still appended: depth %d -> %d", before, after)
	}
}

// What a caller is told after a batch act times out is the whole of the
// recovery, and whether rerunning the file is safe depends on the acts before
// the failure as much as on the one that failed.
func TestBatchSubmitRefusalWeighsTheWholePrefix(t *testing.T) {
	expired := &residentclient.TransportError{Err: context.DeadlineExceeded}
	keyed := func(key string) batchAct { return batchAct{IdempotencyKey: key} }

	// Every act up to the failure carries a key, so the file can be rerun.
	safe := batchSubmitRefusal(expired, 2, []batchAct{keyed("a"), keyed("b"), keyed("publish-report")}, 30*time.Second)
	for _, want := range []string{"may already have landed", "position 2", "30s", "Run the same file again", "--deadline"} {
		if !strings.Contains(safe.Error(), want) {
			t.Fatalf("refusal for a fully keyed prefix does not say %q: %v", want, safe)
		}
	}
	if !errors.Is(safe, context.DeadlineExceeded) {
		t.Fatalf("refusal dropped the cause: %v", safe)
	}

	// The failing act carries a key, but an earlier one does not: rerunning the
	// file would append that earlier act a second time, so the advice may not be
	// given. This is the case that made the previous wording unsafe.
	mixed := batchSubmitRefusal(expired, 2, []batchAct{keyed("a"), {}, keyed("publish-report")}, 30*time.Second)
	for _, want := range []string{"Do not rerun the file", "position 1", "publish-report"} {
		if !strings.Contains(mixed.Error(), want) {
			t.Fatalf("refusal with a keyless act before the failure does not say %q: %v", want, mixed)
		}
	}
	if strings.Contains(mixed.Error(), "Run the same file again") {
		t.Fatalf("a file that would duplicate an earlier act was called safe to rerun: %v", mixed)
	}

	// The failing act itself carries no key.
	unkeyed := batchSubmitRefusal(expired, 1, []batchAct{keyed("a"), {}}, 30*time.Second)
	for _, want := range []string{"no idempotency_key", "second copy", "position 1", "find them first"} {
		if !strings.Contains(unkeyed.Error(), want) {
			t.Fatalf("refusal for a keyless act does not say %q: %v", want, unkeyed)
		}
	}

	// A refusal is definite, so it keeps its own words: telling an author their
	// act may have landed when the resident said it did not is worse than
	// saying nothing.
	refused := errors.New("rests on a retired basis")
	if got := batchSubmitRefusal(refused, 0, []batchAct{keyed("k")}, 30*time.Second); got.Error() != refused.Error() {
		t.Fatalf("refusal was rewritten as a lost answer: %v", got)
	}
}

// The batch has to say it, not merely be able to — and it has to say it about
// the file it was running. The first act lands against a real resident and the
// second never gets an answer, so the refusal is about an act at position 1
// whose recovery depends on the keyless act at position 0 that already landed.
// A refusal built from the failing act alone would call this file safe to rerun.
func TestBatchReportsTheReplayKeyWhenAnActsDeadlineExpires(t *testing.T) {
	fixture := newWorkflowFixture(t)
	resident, err := service.New(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var answered atomic.Int64
	// The first submission is served; every one after it is left hanging.
	gate := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v0/submit" || answered.Add(1) == 1 {
			resident.Handler().ServeHTTP(writer, request)
			return
		}
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); gate.Close() })

	_, private, err := fixture.workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	acts := []batchAct{
		{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the act that lands", RestsOn: []string{fixture.artifact}},
		{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "the act whose answer is lost",
			RestsOn: []string{fixture.artifact}, IdempotencyKey: "batch-deadline-probe"},
	}
	ctx := context.WithValue(fixture.ctx, submitDeadlineKey{}, 400*time.Millisecond)
	report, err := runBatch(ctx, fixture.workspace, gate.URL, "reviewer", private, acts, false)
	if err == nil {
		t.Fatal("a batch whose act never got an answer reported success")
	}
	for _, want := range []string{"may already have landed", "position 1", "400ms",
		"Do not rerun the file", "position 0", "batch-deadline-probe"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("batch refusal does not say %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Run the same file again") {
		t.Fatalf("a file whose first act carries no key was called safe to rerun: %v", err)
	}
	if report.Landed != 1 || report.Acts[0].Outcome != "landed" || report.Acts[1].Outcome != "failed" {
		t.Fatalf("batch report = %+v", report)
	}
}

// gs publish reads the resident's verdict on each queued act, and it advertises
// --deadline like every other appending command. A read left on its own literal
// would ignore the deadline the author set for the command they ran.
func TestPublicationDecisionReadsUnderThisInvocationsDeadline(t *testing.T) {
	fixture := newWorkflowFixture(t)
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); stalled.Close() })

	ctx := context.WithValue(fixture.ctx, submitDeadlineKey{}, 200*time.Millisecond)
	started := time.Now()
	_, _, err := publicationDecision(ctx, fixture.workspace, stalled.URL, fixture.artifact)
	if err == nil {
		t.Fatal("a resident that never answered was read as a decision")
	}
	if !residentclient.TimedOut(err) {
		t.Fatalf("stalled resident error = %v, want an expired deadline", err)
	}
	if waited := time.Since(started); waited > 3*time.Second {
		t.Fatalf("the read waited %s under a 200ms deadline: it kept its own literal", waited)
	}
}
