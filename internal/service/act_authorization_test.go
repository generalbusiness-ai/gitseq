package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// authorizationFixture is a resident serving one real repository: a checkout
// with a branch that can move, a workroom, and a session credential minted the
// way a browser mints one. Everything below drives the /v0/act endpoint the
// resident actually serves, so what is proved is the wiring of the running
// surface rather than the behaviour of the check in isolation.
type authorizationFixture struct {
	t          *testing.T
	ctx        context.Context
	server     *Server
	workspace  *app.Workspace
	repo       string
	ref        string
	credential string
}

func newAuthorizationFixture(t *testing.T) authorizationFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	fixture := authorizationFixture{t: t, ctx: ctx, repo: repo}
	fixture.git("commit", "--allow-empty", "-qm", "seed")
	// The default branch name is whoever ran the tests' business; the report
	// names the ref this checkout is actually on.
	fixture.ref = fixture.git("symbolic-ref", "HEAD")
	workspace, _, err := app.Init(ctx, repo, "reviewer", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	fixture.workspace, fixture.server = workspace, server
	fixture.credential, _ = announceCredential(t, server, presenceRequest{Actor: "reviewer"})
	return fixture
}

func (f authorizationFixture) git(arguments ...string) string {
	f.t.Helper()
	full := append([]string{"-C", f.repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, arguments...)
	output, err := exec.Command("git", full...).Output()
	if err != nil {
		f.t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

// post is the browser's own path to a durable act: JSON to /v0/act carrying the
// session credential. It returns the accepted event identifier, or the refusal
// the resident wrote.
func (f authorizationFixture) post(input actRequest) (string, string) {
	f.t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(response, request)
	var answer struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		f.t.Fatalf("decode /v0/act response: %v", err)
	}
	if response.Code == http.StatusOK && answer.Error == "" {
		return answer.ID, ""
	}
	if answer.Error == "" {
		f.t.Fatalf("/v0/act returned %d with no error text", response.Code)
	}
	return "", answer.Error
}

// request files the commitment the report below will close, so that each case
// files an admissible report and nothing is refused for want of a basis.
func (f authorizationFixture) request(key, text string) string {
	f.t.Helper()
	id, refusal := f.post(actRequest{Session: f.credential, Act: "state", Kind: "request", Text: text,
		Body: map[string]string{"to": "reviewer", "conditions": text}, IdempotencyKey: key})
	if refusal != "" {
		f.t.Fatalf("filing the request %q: %s", key, refusal)
	}
	return id
}

// report files an authorization report over HTTP under the caller's own words
// and idempotency key, so one key can be retried exactly or reused for a
// different act.
func (f authorizationFixture) report(request, key, text string, body map[string]string) (string, string) {
	f.t.Helper()
	fields := map[string]string{"authorizes_request": request}
	for field, value := range body {
		if value == "" {
			delete(fields, field)
			continue
		}
		fields[field] = value
	}
	return f.post(actRequest{Session: f.credential, Act: "state", Kind: "report", Text: text,
		Body: fields, RestsOn: []string{request}, IdempotencyKey: key})
}

func (f authorizationFixture) snapshot() app.Snapshot {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

// unmoved is the durable half of a refusal: nothing was appended, so the
// frontier the whole workroom reads is exactly where it was.
func (f authorizationFixture) unmoved(before app.Snapshot, what string) {
	f.t.Helper()
	after := f.snapshot()
	if after.Depth != before.Depth || after.Head != before.Head {
		f.t.Fatalf("%s changed the durable frontier: depth %d -> %d, head %s -> %s",
			what, before.Depth, after.Depth, before.Head, after.Head)
	}
}

// TestActEndpointReresolvesTheTargetOfAnAuthorizationReport is the resident's
// half of the filing-time re-resolution the CLI and the MCP adapter already
// apply. The act travels to the resident as JSON, and a precondition cannot
// travel in JSON, so a report filed through the browser would otherwise be
// signed against a ref that had already moved and refused later by whoever ran
// the merge. Each refusal here is red if handleAct stops wiring the
// precondition on.
func TestActEndpointReresolvesTheTargetOfAnAuthorizationReport(t *testing.T) {
	t.Parallel()
	fixture := newAuthorizationFixture(t)
	base := fixture.git("rev-parse", "HEAD")
	fixture.git("commit", "--allow-empty", "-qm", "advance the target before the report is filed")
	moved := fixture.git("rev-parse", "HEAD")
	// An orphan commit is in the repository and is on no path to the branch,
	// which is what a remeasurement from off the ref looks like.
	unrelated := fixture.git("commit-tree", base+"^{tree}", "-m", "off the ref")

	for _, test := range []struct {
		name string
		body map[string]string
		want string
	}{
		{
			name: "the ref moved under the measurement",
			body: map[string]string{"target_ref": fixture.ref, "target_pre_head": base},
			want: fixture.ref + " is at " + moved + ", but target_pre_head is " + base,
		},
		{
			name: "no measurement at all",
			body: map[string]string{"target_ref": fixture.ref},
			want: "names target_ref " + fixture.ref + " without target_pre_head",
		},
		{
			name: "a remeasurement from off the ref",
			body: map[string]string{"target_ref": fixture.ref, "target_pre_head": unrelated, "remeasure": "disjoint-paths"},
			want: "is not an ancestor of target_ref " + fixture.ref,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request("request-"+test.name, "release the hold: "+test.name)
			before := fixture.snapshot()
			id, refusal := fixture.report(request, "report-"+test.name, "release the hold", test.body)
			if id != "" || !strings.Contains(refusal, test.want) {
				t.Fatalf("filing %s: id %q, refusal %q, want a refusal containing %q", test.name, id, refusal, test.want)
			}
			fixture.unmoved(before, "the refused report ("+test.name+")")
		})
	}

	t.Run("the ref where the signer measured it", func(t *testing.T) {
		request := fixture.request("request-current", "release the hold now")
		id, refusal := fixture.report(request, "report-current", "release the hold",
			map[string]string{"target_ref": fixture.ref, "target_pre_head": moved})
		if id == "" {
			t.Fatalf("a report measured against the current ref was refused: %s", refusal)
		}
	})

	// This is an ancestry allowance and nothing more: the signer says the
	// commits between the measurement and the ref touch other paths, and the
	// check confirms only that the measurement is still on the ref's history.
	// It does not read a single path, so it is no proof that the paths are
	// disjoint; the merge's own re-resolution remains the later reading.
	t.Run("a stated disjoint-paths remeasurement over an ancestor", func(t *testing.T) {
		request := fixture.request("request-remeasured", "release the hold as remeasured")
		id, refusal := fixture.report(request, "report-remeasured", "release the hold",
			map[string]string{"target_ref": fixture.ref, "target_pre_head": base, "remeasure": "disjoint-paths"})
		if id == "" {
			t.Fatalf("a remeasured report over an ancestor was refused: %s", refusal)
		}
	})
}

// TestActEndpointReplaysAnAcceptedAuthorizationOverHTTPAfterTheTargetMoves is
// the other side of the guard on this surface. The ref moves; the accepted act
// does not. An exact retry is a browser recovering a lost response: it replays
// the event accepted while the world still agreed with its measurement and
// appends nothing. A fresh key over the same stale measurement is a new
// signature and is refused.
func TestActEndpointReplaysAnAcceptedAuthorizationOverHTTPAfterTheTargetMoves(t *testing.T) {
	t.Parallel()
	fixture := newAuthorizationFixture(t)
	measured := fixture.git("rev-parse", "HEAD")
	request := fixture.request("retry-request", "release the hold")
	body := map[string]string{"target_ref": fixture.ref, "target_pre_head": measured}

	first, refusal := fixture.report(request, "accepted-authorization", "release the hold", body)
	if first == "" {
		t.Fatalf("first submission: %s", refusal)
	}
	accepted := fixture.snapshot()

	fixture.git("commit", "--allow-empty", "-qm", "advance the target after the report was accepted")
	moved := fixture.git("rev-parse", "HEAD")

	id, refusal := fixture.report(request, "fresh-authorization", "release the hold", body)
	if id != "" || !strings.Contains(refusal, fixture.ref+" is at "+moved) {
		t.Fatalf("a fresh report measured against the moved target: id %q, refusal %q", id, refusal)
	}
	fixture.unmoved(accepted, "the refused fresh report")

	replayed, refusal := fixture.report(request, "accepted-authorization", "release the hold", body)
	if replayed != first {
		t.Fatalf("the exact retry returned %q (refusal %q), want the original event %q", replayed, refusal, first)
	}
	fixture.unmoved(accepted, "the exact retry")
}

// TestActEndpointLeavesOrdinaryActsAndCredentialCheckingAlone bounds the guard.
// It reads only a body that claims a measured landing target, so an ordinary
// durable act and a report that names no authorized request are untouched even
// where the branch has moved. And it is judged well after custody: a request
// carrying somebody else's identifier is still refused as a credential, never
// measured.
func TestActEndpointLeavesOrdinaryActsAndCredentialCheckingAlone(t *testing.T) {
	t.Parallel()
	fixture := newAuthorizationFixture(t)
	stale := fixture.git("rev-parse", "HEAD")
	fixture.git("commit", "--allow-empty", "-qm", "move the branch under everything below")

	id, refusal := fixture.post(actRequest{Session: fixture.credential, Act: "state", Kind: "assert",
		Text: "an ordinary durable act", IdempotencyKey: "ordinary-assert"})
	if id == "" {
		t.Fatalf("an ordinary assert was refused: %s", refusal)
	}

	request := fixture.request("ordinary-request", "do the ordinary work")
	id, refusal = fixture.report(request, "ordinary-report", "the work is done",
		map[string]string{"authorizes_request": "", "target_ref": fixture.ref, "target_pre_head": stale})
	if id == "" {
		t.Fatalf("a report that authorizes no request was refused: %s", refusal)
	}

	before := fixture.snapshot()
	id, refusal = fixture.post(actRequest{Session: "credential:not-the-minted-one", Act: "state", Kind: "report",
		Text: "release the hold", Body: map[string]string{"authorizes_request": request, "target_ref": fixture.ref, "target_pre_head": stale},
		RestsOn: []string{request}, IdempotencyKey: "forged-authorization"})
	if id != "" || !strings.Contains(refusal, "credential is not valid") {
		t.Fatalf("a forged credential: id %q, refusal %q", id, refusal)
	}
	fixture.unmoved(before, "the forged authorization")
}
