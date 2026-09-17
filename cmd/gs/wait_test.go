package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// What `gs wait` has to get right is which changes wake it and which do not,
// and what it leaves behind when it stops. Every test here runs against a real
// in-process resident over a real workroom — the same service.Server `gs serve`
// runs — because a fake resident could only replay the answers this command was
// written to expect, and the filter under test lives inside the resident's own
// poll.

type waitFixture struct {
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	url       string
	handler   http.Handler
	seed      string
	scratch   string
}

func newWaitFixture(t *testing.T) *waitFixture {
	t.Helper()
	ctx := context.Background()
	scratch := t.TempDir()
	repo := filepath.Join(scratch, "repo")
	testGit(t, "", "init", "-q", repo)
	workspace, _, err := app.Init(ctx, repo, "alice", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bot", "carol"} {
		if _, _, err := workspace.AddActor(ctx, "alice", name, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	server, err := service.NewObserved(workspace, nopObserver{})
	if err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewServer(server.Handler())
	t.Cleanup(listener.Close)
	return &waitFixture{ctx: ctx, repo: repo, workspace: workspace, url: listener.URL, handler: server.Handler(),
		seed: workspace.EventID(workspace.View().Genesis), scratch: scratch}
}

// appendState is the goroutine-safe half: it reports rather than fails, so a
// test that moves the log while a wait is running never calls t.Fatal off the
// test's own goroutine.
func (f *waitFixture) appendState(actor string, kind workroom.Kind, text string, body map[string]string, restsOn ...string) (string, error) {
	submission, err := f.workspace.Act(f.ctx, actor, app.Act{Verb: app.VerbState, Kind: kind,
		Text: text, Body: body, RestsOn: restsOn, IdempotencyKey: text})
	if err != nil {
		return "", err
	}
	return submission.Record.ID, nil
}

func (f *waitFixture) act(t *testing.T, actor string, kind workroom.Kind, text string, body map[string]string, restsOn ...string) string {
	t.Helper()
	event, err := f.appendState(actor, kind, text, body, restsOn...)
	if err != nil {
		t.Fatalf("append %s %q: %v", kind, text, err)
	}
	return event
}

func (f *waitFixture) fingerprintOf(t *testing.T, actor string) string {
	t.Helper()
	resolved, err := f.workspace.ResolveActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	return resolved.Fingerprint
}

func (f *waitFixture) frontier(t *testing.T) service.Cursor {
	t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return service.Cursor{Frontier: []service.Frontier{{Genesis: snapshot.Genesis, Head: snapshot.Head, Depth: snapshot.Depth}}}
}

// seedCursor writes the cursor a previous call would have left, which is what
// lets a test say "from here on" rather than "from the beginning of the log".
func (f *waitFixture) seedCursor(t *testing.T, name string, cursor service.Cursor) string {
	t.Helper()
	path := filepath.Join(f.scratch, name)
	if err := writeWaitCursor(path, f.workspace.View().Genesis, "bot", cursor); err != nil {
		t.Fatal(err)
	}
	return path
}

// options is one wait with every clock wound down, so a test spends
// milliseconds where a person would spend minutes.
func (f *waitFixture) options(t *testing.T, until statusview.Until, timeout time.Duration, cursorFile string, out io.Writer) waitOptions {
	t.Helper()
	return waitOptions{actorName: "bot", fingerprint: f.fingerprintOf(t, "bot"), serverURL: f.url,
		until: until, timeout: timeout, cursorFile: cursorFile,
		pollCap: 400 * time.Millisecond, leaseTTL: 30 * time.Second, renewEvery: 10 * time.Second,
		watchInterval: 50 * time.Millisecond, out: out, progress: io.Discard}
}

// request is the ordinary intake record: a state@3 request addressed to one
// actor, stating what it owes so the fold admits it.
func (f *waitFixture) request(t *testing.T, from, to, text string) string {
	t.Helper()
	return f.act(t, from, workroom.KindRequest, text, map[string]string{
		"to": f.fingerprintOf(t, to), "conditions": "the result is in the report", "no_git_artifact": "true",
	}, f.seed)
}

func TestWaitUnderActionableIgnoresAnUnrelatedEventAndUnderAnyReturnsIt(t *testing.T) {
	fixture := newWaitFixture(t)
	before := fixture.frontier(t)
	filtered := fixture.seedCursor(t, "filtered.json", before)
	unfiltered := fixture.seedCursor(t, "unfiltered.json", before)

	// Work between two other actors. It moves the frontier, so every wait in
	// the room sees the head change; it is nobody's business but theirs.
	fixture.request(t, "alice", "carol", "sweep the floor")

	var actionable strings.Builder
	err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 1200*time.Millisecond, filtered, &actionable))
	if err != errWaitTimeout {
		t.Fatalf("actionable wait on somebody else's request = %v, %q; want the deadline to pass", err, actionable.String())
	}

	var any strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilAny, 10*time.Second, unfiltered, &any)); err != nil {
		t.Fatalf("any wait on the same event: %v", err)
	}
	if !strings.Contains(any.String(), "sweep the floor") {
		t.Fatalf("any wait did not report the unrelated event:\n%s", any.String())
	}
}

func TestWaitReturnsARequestAddressedToTheActor(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	request := fixture.request(t, "alice", "bot", "write the changelog")

	var out strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &out)); err != nil {
		t.Fatalf("wait on a request addressed to bot: %v", err)
	}
	printed := out.String()
	if !strings.Contains(printed, request) {
		t.Errorf("the wake does not name the request %s:\n%s", request, printed)
	}
	// The exact command the row owes is the whole point of the renderer being
	// shared with `gs work --next`; a wake that named the event and left the
	// reader to work out the act would be a notification, not an answer.
	if !strings.Contains(printed, "gs promise --as bot "+request) {
		t.Errorf("the wake does not print the act the row owes:\n%s", printed)
	}
}

func TestWaitReturnsAVerdictRestingOnTheActorsArtifact(t *testing.T) {
	fixture := newWaitFixture(t)
	request := fixture.request(t, "alice", "bot", "publish the page")
	promise := fixture.act(t, "bot", workroom.KindPromise, "I will publish it", nil, request)
	artifact := fixture.act(t, "bot", workroom.KindReport, "published", nil, promise)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))

	// Alice answers bot's own record. It creates no lane row of its own: the
	// only thing connecting it to bot is that it rests on something bot signed.
	verdict := fixture.act(t, "alice", workroom.KindAssert, "the page is wrong in two places", nil, artifact)

	var out strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &out)); err != nil {
		t.Fatalf("wait on a record resting on bot's artifact: %v", err)
	}
	if !strings.Contains(out.String(), verdict) {
		t.Errorf("the wake does not name the answering record %s:\n%s", verdict, out.String())
	}
}

func TestWaitReturnsUnacknowledgedPriorityChat(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	// Alice addresses bot by name from her own session, once bot's wait has
	// opened its own. The frame is live, not durable: nothing in the log moves,
	// so only the inbox half of the wait can possibly see it, and an inbox is
	// per session — a frame sent before bot had one would be addressed to
	// nobody who is listening.
	alice := openTestSession(t, fixture.url, "alice")
	spoken := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		spoken <- say(fixture.url, alice, fixture.seed, "@bot the build is red")
	}()

	var out strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &out)); err != nil {
		t.Fatalf("wait on addressed priority chat: %v", err)
	}
	if err := <-spoken; err != nil {
		t.Fatalf("say: %v", err)
	}
	if !strings.Contains(out.String(), "the build is red") {
		t.Errorf("the wake does not carry the addressed frame:\n%s", out.String())
	}
}

func TestWaitTimesOutWithNothingNew(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	var out strings.Builder
	err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 900*time.Millisecond, cursor, &out))
	if err != errWaitTimeout {
		t.Fatalf("quiet wait = %v; want errWaitTimeout", err)
	}
	if out.String() != "" {
		t.Errorf("a wait that timed out printed something:\n%s", out.String())
	}
	if waitTimeoutExit != 3 {
		t.Errorf("the timeout exit status is %d; a shell loop branches on 3", waitTimeoutExit)
	}
}

func TestWaitPersistsItsCursorAndResumesFromIt(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	fixture.request(t, "alice", "bot", "first task")

	var first strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &first)); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	if !strings.Contains(first.String(), "first task") {
		t.Fatalf("first wait did not report the request:\n%s", first.String())
	}
	held := readWaitCursor(cursor, fixture.workspace.View().Genesis)
	if len(held.Frontier) != 1 || held.Frontier[0].Head != fixture.frontier(t).Frontier[0].Head {
		t.Fatalf("the persisted cursor is %+v; want this workroom's current frontier", held)
	}

	// The second call reads that file. The same request is no longer new, so
	// the only honest answer is the deadline.
	var second strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 900*time.Millisecond, cursor, &second)); err != errWaitTimeout {
		t.Fatalf("second wait = %v, %q; want the persisted cursor to have been resumed", err, second.String())
	}
}

func TestWaitRenewsItsLeaseAcrossTheTTLAndDepartsOnExit(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	options := fixture.options(t, statusview.UntilActionable, 1500*time.Millisecond, cursor, io.Discard)
	// A lease shorter than the wait, renewed more often than it expires. If
	// the renewal stopped, the session would lapse before the wait ended.
	options.leaseTTL = 400 * time.Millisecond
	options.renewEvery = 100 * time.Millisecond
	options.pollCap = 150 * time.Millisecond

	present := make(chan int, 1)
	go func() {
		time.Sleep(time.Second)
		present <- livePresenceCount(t, fixture.url, "bot")
	}()
	if err := runWait(fixture.ctx, fixture.workspace, options); err != errWaitTimeout {
		t.Fatalf("leased wait = %v; want the deadline to pass", err)
	}
	if held := <-present; held != 1 {
		t.Errorf("live sessions for bot a second into the wait = %d, want 1: the lease was not renewed across its TTL", held)
	}
	if after := livePresenceCount(t, fixture.url, "bot"); after != 0 {
		t.Errorf("live sessions for bot after the wait returned = %d, want 0: the session did not depart", after)
	}
}

func TestWaitWithoutAResidentWatchesTheSequenceRef(t *testing.T) {
	fixture := newWaitFixture(t)
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	options := fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &strings.Builder{})
	options.serverURL = ""
	var out strings.Builder
	options.out = &out
	var progress strings.Builder
	options.progress = &progress

	type appended struct {
		event string
		err   error
	}
	moved := make(chan appended, 1)
	body := map[string]string{"to": fixture.fingerprintOf(t, "bot"),
		"conditions": "the result is in the report", "no_git_artifact": "true"}
	go func() {
		time.Sleep(150 * time.Millisecond)
		event, err := fixture.appendState("alice", workroom.KindRequest, "watch the ref", body, fixture.seed)
		moved <- appended{event: event, err: err}
	}()
	if err := runWait(fixture.ctx, fixture.workspace, options); err != nil {
		t.Fatalf("local wait: %v", err)
	}
	request := <-moved
	if request.err != nil {
		t.Fatalf("append the request: %v", request.err)
	}
	if !strings.Contains(out.String(), request.event) {
		t.Errorf("the local wake does not name the request %s:\n%s", request.event, out.String())
	}
	if !strings.Contains(progress.String(), "watching the local sequence ref") {
		t.Errorf("the local fallback did not say on standard error that it is polling locally: %q", progress.String())
	}
}

func TestWaitReturnsAProposalThisActorMayRatify(t *testing.T) {
	fixture := newWaitFixture(t)
	if _, err := fixture.workspace.GrantRole(fixture.ctx, "alice", "bot", "ratifier"); err != nil {
		t.Fatal(err)
	}
	cursor := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	// A proposal whose captured satisfier names a role bot holds. It is not a
	// commitment and creates no request row, but it stands in bot's
	// ratification lane, which is bot's to move.
	proposal := fixture.act(t, "alice", workroom.KindPropose, "adopt the new cadence",
		map[string]string{"satisfier": "role:ratifier"}, fixture.seed)

	var out strings.Builder
	if err := runWait(fixture.ctx, fixture.workspace, fixture.options(t, statusview.UntilActionable, 10*time.Second, cursor, &out)); err != nil {
		t.Fatalf("wait on a proposal bot may ratify: %v", err)
	}
	printed := out.String()
	if !strings.Contains(printed, proposal) {
		t.Errorf("the wake does not name the proposal %s:\n%s", proposal, printed)
	}
	if !strings.Contains(printed, "gs ratify --as bot "+proposal) {
		t.Errorf("the wake does not print the act the proposal owes:\n%s", printed)
	}
}

func TestWaitNeverAsksForAZeroLengthPoll(t *testing.T) {
	// The resident reads a non-positive timeout as "use my own", so a window
	// that rounds to zero milliseconds must end the loop rather than be sent.
	for _, sample := range []struct {
		remaining, capped, window time.Duration
		askable                   bool
	}{
		{remaining: 9 * time.Second, capped: 400 * time.Millisecond, window: 400 * time.Millisecond, askable: true},
		{remaining: 300 * time.Millisecond, capped: 25 * time.Second, window: 300 * time.Millisecond, askable: true},
		{remaining: time.Millisecond, capped: 25 * time.Second, window: time.Millisecond, askable: true},
		{remaining: 999 * time.Microsecond, capped: 25 * time.Second},
		{remaining: 0, capped: 25 * time.Second},
		{remaining: -time.Second, capped: 25 * time.Second},
	} {
		window, askable := pollWindow(sample.remaining, sample.capped)
		if askable != sample.askable || (askable && window != sample.window) {
			t.Errorf("pollWindow(%s, %s) = %s, %v; want %s, %v",
				sample.remaining, sample.capped, window, askable, sample.window, sample.askable)
		}
	}
}

func TestWaitRefusesAMalformedInvocationWithUsageAndAnExample(t *testing.T) {
	fixture := newWaitFixture(t)
	// The usage and the example go to the flag set's own output, which is the
	// shared process handle; this asserts on what the command hands back and on
	// the example itself rather than swapping a global out from under the other
	// tests in this package.
	example, published := commandExamples["wait"]
	if !published {
		t.Fatal("gs wait has no worked example; a caller who mistyped it gets a flag list and nothing to copy")
	}
	if !strings.HasPrefix(example, "gs wait ") || !strings.Contains(example, "--as") {
		t.Errorf("the worked example %q is not a complete gs wait call", example)
	}
	if !strings.Contains(commandNames, "wait") {
		t.Error("gs prints no `wait` in its command list")
	}
	for _, malformed := range [][]string{
		{"--repo", fixture.repo, "--as", "bot", "--until", "soon"},
		{"--repo", fixture.repo, "--as", "bot", "--timeout", "0"},
		{"--repo", fixture.repo, "--as", "bot", "extra"},
	} {
		err := waitCommand(fixture.ctx, malformed)
		if err == nil {
			t.Errorf("gs wait %s was accepted", strings.Join(malformed, " "))
		}
	}
	err := waitCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "bot", "--until", "soon"})
	if err == nil || !strings.Contains(err.Error(), "actionable, any") {
		t.Errorf("refusal %v does not name the admissible values", err)
	}
}

func TestWaitCursorFileIgnoresAnotherWorkroomsCursor(t *testing.T) {
	fixture := newWaitFixture(t)
	path := fixture.seedCursor(t, "cursor.json", fixture.frontier(t))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var held waitCursorFile
	if err := json.Unmarshal(data, &held); err != nil {
		t.Fatal(err)
	}
	held.Genesis = "another-workroom"
	if err := os.WriteFile(path, mustJSON(t, held), 0o644); err != nil {
		t.Fatal(err)
	}
	if resumed := readWaitCursor(path, fixture.workspace.View().Genesis); len(resumed.Frontier) != 0 {
		t.Errorf("a cursor from another workroom was resumed: %+v", resumed)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// openTestSession opens one presence session with its inbox enabled, which is
// what a speaker needs before the resident will accept a frame from it.
func openTestSession(t *testing.T, url, actor string) string {
	t.Helper()
	client := residentclient.New(5 * time.Second)
	var answer presenceAnswer
	if err := client.PostJSON(context.Background(), url, "/v0/presence",
		map[string]any{"actor": actor, "ttl_ms": 60000}, waitResponseLimit, &answer); err != nil {
		t.Fatal(err)
	}
	var registered struct {
		Version string `json:"version"`
	}
	if err := client.PostJSON(context.Background(), url, "/v0/inbox/register",
		map[string]any{"credential": answer.Credential, "version": service.InboxProtocolVersion}, waitResponseLimit, &registered); err != nil {
		t.Fatal(err)
	}
	return answer.Credential
}

func say(url, credential, about, text string) error {
	_, err := residentclient.New(5*time.Second).PostValue(context.Background(), url, "/v0/say",
		map[string]any{"credential": credential, "about": about, "text": text,
			"inbox_version": service.InboxProtocolVersion}, waitResponseLimit)
	return err
}

func livePresenceCount(t *testing.T, url, actor string) int {
	t.Helper()
	var counted struct {
		Count int `json:"count"`
	}
	if err := residentclient.New(5*time.Second).GetJSON(context.Background(), url,
		"/v0/presence-count?actor="+actor, waitResponseLimit, &counted); err != nil {
		t.Fatal(err)
	}
	return counted.Count
}

func TestWaitNamesTheRepairWhenTheResidentPredatesTheFilter(t *testing.T) {
	fixture := newWaitFixture(t)
	// A resident built before `until` existed decodes this request strictly
	// and rejects the field by name. "unknown field" says nothing about which
	// side is behind, so the command says it.
	older := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if request.URL.Path != "/v0/actor-wait" {
			fixture.handler.ServeHTTP(writer, request)
			return
		}
		if strings.Contains(string(body), `"until"`) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(writer).Encode(map[string]string{"error": `json: unknown field "until"`})
			return
		}
		// The same resident also predates the `changed` field, so its answer
		// carries no verdict at all. Stripping it here is what makes the
		// unfiltered case below a real test of the derivation rather than of
		// the field.
		recorded := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorded, request)
		var answered map[string]any
		if err := json.Unmarshal(recorded.Body.Bytes(), &answered); err != nil {
			t.Errorf("actor-wait answer was not JSON: %v", err)
			return
		}
		delete(answered, "changed")
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(answered)
	}))
	t.Cleanup(older.Close)

	options := fixture.options(t, statusview.UntilActionable, 5*time.Second, fixture.seedCursor(t, "cursor.json", fixture.frontier(t)), io.Discard)
	options.serverURL = older.URL
	err := runWait(fixture.ctx, fixture.workspace, options)
	if err == nil || !strings.Contains(err.Error(), "restart it on this build") || !strings.Contains(err.Error(), "--until any") {
		t.Fatalf("refusal = %v; want both repairs named", err)
	}

	// `any` is sent as an absent field, so the same older resident answers it —
	// and although its answer carries no verdict, the delta it returns shows
	// the change. Without the derivation this call waited out its whole
	// deadline in silence against every resident that predates the field.
	fixture.request(t, "alice", "bot", "answered by an older resident")
	var out strings.Builder
	unfiltered := fixture.options(t, statusview.UntilAny, 5*time.Second,
		fixture.seedCursor(t, "any.json", service.Cursor{}), &out)
	unfiltered.serverURL = older.URL
	if err := runWait(fixture.ctx, fixture.workspace, unfiltered); err != nil {
		t.Fatalf("unfiltered wait against the older resident = %v; want a wake", err)
	}
	if !strings.Contains(out.String(), "answered by an older resident") {
		t.Errorf("the wake does not report the request:\n%s", out.String())
	}
}

func TestActorWaitRefusesAnActionableFilterOnTheWorkroomRoute(t *testing.T) {
	fixture := newWaitFixture(t)
	err := residentclient.New(5*time.Second).PostJSON(context.Background(), fixture.url, "/v0/wait",
		service.WaitRequest{TimeoutMS: 100, Until: string(statusview.UntilActionable)}, waitResponseLimit, &struct{}{})
	var refusal *residentclient.HTTPError
	if err == nil {
		t.Fatal("/v0/wait accepted until=actionable, which it has no actor to evaluate")
	}
	if !errors.As(err, &refusal) || refusal.StatusCode != http.StatusBadRequest {
		t.Fatalf("/v0/wait refusal = %v; want a 400", err)
	}
	if !strings.Contains(refusal.Message, "actor-wait") {
		t.Errorf("the refusal %q does not name the route that can evaluate the filter", refusal.Message)
	}
}
