package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestValidateSubmissionRequestSizeMatchesResidentJSONCap(t *testing.T) {
	t.Parallel()
	if err := ValidateSubmissionRequestSize(kernel.Request{Payload: []byte("small")}); err != nil {
		t.Fatalf("small resident request rejected: %v", err)
	}
	oversized := kernel.Request{Payload: make([]byte, SubmissionRequestLimit)}
	if err := ValidateSubmissionRequestSize(oversized); err == nil || !strings.Contains(err.Error(), "resident submission request exceeds") {
		t.Fatalf("oversized resident request error = %v", err)
	}
}

func announceCredential(t *testing.T, server *Server, input presenceRequest) (string, nexus.Change) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v0/presence", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("announce returned %d: %s", response.Code, response.Body.String())
	}
	var announced presenceResponse
	if err := json.NewDecoder(response.Body).Decode(&announced); err != nil {
		t.Fatal(err)
	}
	if announced.Credential == "" {
		t.Fatal("resident did not return its minted credential")
	}
	return announced.Credential, announced.Change
}

// The legacy demo page is gone, and this is what stops it coming back. It was
// a second renderer of the same projection, reachable by nobody: registered at
// one route, linked from no page, named in no document, and covered by one test
// that asserted its wording. Review found it still abbreviating event
// identifiers after the browser had stopped, which is the cost of a surface
// that no reader visits and so no reader corrects — it drifts, and the drift is
// invisible until someone audits it.
//
// Deleting it was the alternative review offered to threading numbers through
// it, and it is the better one: a surface with no readers earns no maintenance.
func TestTheUnreachableDemoSurfaceIsGone(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"demoHTML", "handleDemo", `"GET /legacy"`} {
		if bytes.Contains(source, []byte(gone)) {
			t.Errorf("%s is back; the legacy page was removed rather than kept in step with the fold", gone)
		}
	}
}

func TestResidentCredentialNeverAppearsOutsideCreationResponse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	newServer := func(name string) (*Server, *app.Workspace) {
		repo := filepath.Join(t.TempDir(), name)
		if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, output)
		}
		workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(workspace)
		if err != nil {
			t.Fatal(err)
		}
		return server, workspace
	}
	first, _ := newServer("first")
	second, _ := newServer("second")
	credential, change := announceCredential(t, first, presenceRequest{Actor: "human"})
	if !strings.HasPrefix(credential, "credential:") || len(credential) < len("credential:")+32 {
		t.Fatalf("credential does not carry the required entropy shape: %q", credential)
	}

	for _, path := range []string{"/v0/presence", "/v0/status", "/v0/status-summary"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		first.Handler().ServeHTTP(response, request)
		if strings.Contains(response.Body.String(), credential) {
			t.Fatalf("%s disclosed the credential: %s", path, response.Body.String())
		}
	}
	if change.ID == credential {
		t.Fatal("public handle equals private credential")
	}

	owned, _ := json.Marshal(actRequest{Session: credential, Act: "state", Kind: "assert", Text: "durable text", IdempotencyKey: "credential-disclosure-guard"})
	request := httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(owned))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	first.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("credential owner act returned %d: %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	first.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v0/status", nil))
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("durable projection disclosed the credential: %s", response.Body.String())
	}

	// The same actor name in another repository does not broaden the token.
	body, _ := json.Marshal(actRequest{Session: credential, Act: "state", Kind: "assert", Text: "cross-repository replay"})
	request = httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), credential) {
		t.Fatalf("cross-repository replay response = %d %s", response.Code, response.Body.String())
	}

	for _, attempted := range []string{"credential:not-hex", "credential:" + strings.Repeat("0", 64), change.ID} {
		guessed, _ := json.Marshal(actRequest{Session: attempted, Act: "state", Kind: "assert", Text: "credential guess"})
		request = httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(guessed))
		request.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		first.Handler().ServeHTTP(response, request)
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), attempted) || !strings.Contains(response.Body.String(), "credential is not valid") {
			t.Fatalf("malformed or guessed credential response = %d %s", response.Code, response.Body.String())
		}
	}

	depart, _ := json.Marshal(sessionStatusRequest{Session: credential})
	request = httptest.NewRequest(http.MethodPost, "/v0/presence/depart", bytes.NewReader(depart))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	first.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("departure returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	first.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK || strings.Contains(response.Body.String(), credential) {
		t.Fatalf("revoked credential replay response = %d %s", response.Code, response.Body.String())
	}
}

func TestTrustedHostAndOriginGuardsRunBeforeRouting(t *testing.T) {
	t.Parallel()
	var called atomic.Int64
	handler := TrustedHostHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			if err := guardMutation(request); err != nil {
				write(writer, nil, err)
				return
			}
		}
		called.Add(1)
		write(writer, map[string]bool{"ok": true}, nil)
	}))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "http://workroom.example/v0/status", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK || called.Load() != 0 {
			t.Fatalf("non-loopback Host reached a %s route", method)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "http://workroom.example/v0/act", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK || called.Load() != 0 {
		t.Fatal("non-loopback Host reached a mutation")
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7777/v0/act", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://127.0.0.1:7777")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK || called.Load() != 0 {
		t.Fatal("cross-scheme browser origin reached a mutation")
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7777/v0/status", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || called.Load() != 1 {
		t.Fatalf("loopback read = %d, called=%d", response.Code, called.Load())
	}

	request = httptest.NewRequest(http.MethodPost, "http://localhost:7777/v0/act", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:7777")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || called.Load() != 2 {
		t.Fatalf("same-origin loopback mutation = %d, called=%d", response.Code, called.Load())
	}
}

func TestTrustedHostAllowsLoopbackReadAndMutationClients(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"127.0.0.1:7777",
		"[::1]:7777",
		"localhost:7777",
		"LOCALHOST.:7777",
	} {
		t.Run(host, func(t *testing.T) {
			var called atomic.Int64
			handler := TrustedHostHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					if err := guardMutation(request); err != nil {
						write(writer, nil, err)
						return
					}
				}
				called.Add(1)
				write(writer, map[string]bool{"ok": true}, nil)
			}))

			request := httptest.NewRequest(http.MethodGet, "http://"+host+"/v0/status", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || called.Load() != 1 {
				t.Fatalf("loopback read = %d, called=%d", response.Code, called.Load())
			}

			request = httptest.NewRequest(http.MethodPost, "http://"+host+"/v0/act", strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://"+host)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || called.Load() != 2 {
				t.Fatalf("loopback mutation = %d, called=%d", response.Code, called.Load())
			}
		})
	}
}

func TestLoopbackRequestHostRefusesDNSNameWithoutResolver(t *testing.T) {
	t.Parallel()
	resolverCalls := 0
	resolveToLoopback := func(host string) ([]net.IP, error) {
		resolverCalls++
		if host != "rebinding.example" {
			t.Fatalf("resolver called with %q", host)
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	if loopbackRequestHostWithResolver("rebinding.example:7777", resolveToLoopback) {
		t.Fatal("ordinary DNS name resolving to loopback was admitted")
	}
	if resolverCalls != 0 {
		t.Fatalf("request Host triggered %d resolver calls", resolverCalls)
	}
}

func TestLoopbackRequestHostRefusesMalformedAndPortlessValues(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"",
		"localhost",
		"127.0.0.1",
		"::1",
		"localhost:",
		"localhost:http",
		"localhost:0",
		"localhost:65536",
		"localhost..:7777",
		"workroom.example:7777",
	} {
		t.Run(host, func(t *testing.T) {
			if loopbackRequestHost(host) {
				t.Fatalf("malformed, portless, or non-local Host %q was admitted", host)
			}
		})
	}
}

func TestStatusPresenceAndResettableLiveLayer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})
	response, err := http.Get(httpServer.URL + "/v0/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var status Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Durable.Depth != 1 || len(status.Live.Presence) != 1 || status.Cursor.Frontier[0].Depth != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.TrustBoundary != TrustedProcessPosture {
		t.Fatalf("status trust boundary = %q", status.TrustBoundary)
	}
	if len(status.Durable.Vocabulary.Definitions) != 12 || status.Durable.Vocabulary.Binding.Status != "unbound" {
		t.Fatalf("status did not serve the room vocabulary and binding state: %+v", status.Durable.Vocabulary)
	}
	actorInput, _ := json.Marshal(sessionStatusRequest{Session: credential})
	response, err = http.Post(httpServer.URL+"/v0/actor-status", "application/json", bytes.NewReader(actorInput))
	if err != nil {
		t.Fatal(err)
	}
	actorBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(actorBody, []byte(`"projection"`)) || bytes.Contains(actorBody, []byte(`"durable"`)) {
		t.Fatalf("bounded actor status encoded the complete projection: %s", actorBody)
	}
	var actorStatus ActorStatus
	if err := json.Unmarshal(actorBody, &actorStatus); err != nil {
		t.Fatal(err)
	}
	if actorStatus.You.Fingerprint != workspace.View().Actors["human"].Fingerprint || actorStatus.Totals.Depth != status.Durable.Depth ||
		actorStatus.Totals.FullProjectionAt == "" {
		t.Fatalf("bounded actor status lost identity, frontier, or full-projection route: %+v", actorStatus)
	}

	response, err = http.Get(httpServer.URL + "/v0/status-summary")
	if err != nil {
		t.Fatal(err)
	}
	var summary SummaryStatus
	if err := json.NewDecoder(response.Body).Decode(&summary); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if summary.Durable.Genesis != status.Durable.Genesis || summary.Durable.Head != status.Durable.Head ||
		summary.Durable.Depth != status.Durable.Depth || len(summary.Cursor.Frontier) != 1 || summary.Cursor.Frontier[0].Head != status.Durable.Head ||
		summary.TrustBoundary != TrustedProcessPosture {
		t.Fatalf("summary frontier differs from full status: summary=%+v full=%+v", summary, status)
	}
	fingerprint := workspace.View().Actors["human"].Fingerprint
	response, err = http.Get(httpServer.URL + "/v0/orientation/" + fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	var orientation Orientation
	if err := json.NewDecoder(response.Body).Decode(&orientation); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if orientation.ProjectionVersion != OrientationProjectionVersion || orientation.You.Fingerprint != fingerprint ||
		orientation.Frontier.Head != status.Durable.Head || orientation.Frontier.Depth != status.Durable.Depth ||
		orientation.You.Kind != "human" || orientation.You.MembershipEvent == "" || len(orientation.You.Roles) == 0 {
		t.Fatalf("orientation differs from effective status projection: %+v", orientation)
	}
	response, err = http.Get(httpServer.URL + "/v0/orientation/unknown")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound {
		response.Body.Close()
		t.Fatalf("unknown actor orientation status = %d", response.StatusCode)
	}
	response.Body.Close()
	response, err = http.Get(httpServer.URL + "/v0/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var local worktreesResponse
	if err := json.NewDecoder(response.Body).Decode(&local); err != nil {
		t.Fatal(err)
	}
	if len(local.Worktrees) != 1 || !local.Worktrees[0].Current || local.Worktrees[0].Checkout != "repo" {
		t.Fatalf("unexpected local worktree projection: %+v", local)
	}
	// The served checkout names itself — a reader has to know which repository
	// this is — while the per-checkout views stay basenames.
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if local.Repo != resolvedRepo {
		t.Fatalf("served repository path = %q want %q", local.Repo, resolvedRepo)
	}
	encoded, err := json.Marshal(local.Worktrees)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(repo)) {
		t.Fatalf("checkout views exposed an absolute path: %s", encoded)
	}
}

func TestSelectiveWorkAndInspectionEndpoints(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	actor := workspace.View().Actors["human"]
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	membershipEvent := snapshot.Projection.Actors[actor.Fingerprint].MembershipEvent
	query, _ := json.Marshal(WorkQuery{Actor: actor.Fingerprint})
	response, err := http.Post(httpServer.URL+"/v0/work-query", "application/json", bytes.NewReader(query))
	if err != nil {
		t.Fatal(err)
	}
	var page WorkPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || page.Actor.Fingerprint != actor.Fingerprint || page.Frontier.Head == "" || page.MatchingTotal != 0 {
		t.Fatalf("unexpected selective work page: status=%d page=%+v", response.StatusCode, page)
	}

	// Artifact state and provenance closure are CLI-only selectors. The HTTP
	// request contract remains the original exact live paths, and its strict
	// decoder refuses either field instead of silently honouring it.
	for _, body := range []string{
		`{"paths":["ui"],"state":"all"}`,
		`{"paths":["ui"],"reaches":"."}`,
	} {
		response, err = http.Post(httpServer.URL+"/v0/artifact-query", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("CLI-only artifact selector was honoured over HTTP: body=%s status=%d", body, response.StatusCode)
		}
	}

	inspect, _ := json.Marshal(InspectRequest{Event: membershipEvent})
	response, err = http.Post(httpServer.URL+"/v0/inspect", "application/json", bytes.NewReader(inspect))
	if err != nil {
		t.Fatal(err)
	}
	var item ItemInspection
	if err := json.NewDecoder(response.Body).Decode(&item); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || item.Event != membershipEvent || item.Statement == nil || item.Decision == nil {
		t.Fatalf("unexpected exact inspection: status=%d item=%+v", response.StatusCode, item)
	}

	response, err = http.Post(httpServer.URL+"/v0/work-query", "application/json", bytes.NewBufferString(`{"actor":"`+actor.Fingerprint+`","expression":".commitments[]"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("free-form query field was accepted: %d", response.StatusCode)
	}
}

func TestPresenceActivityUsesTheLeaseAndCompositeWait(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	post := func(input presenceRequest) *http.Response {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	credential, announced := announceCredential(t, server, presenceRequest{Actor: "human"})

	response, err := http.Get(httpServer.URL + "/v0/status")
	if err != nil {
		t.Fatal(err)
	}
	var before Status
	if err := json.NewDecoder(response.Body).Decode(&before); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	event := before.Durable.Projection.Decisions[0].Event
	blocked := nexus.ActivityBlocked
	focus := []string{event, event}
	note := "reviewing"
	response = post(presenceRequest{Actor: "human", Session: credential, Status: &blocked, Focus: &focus, Note: &note})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("activity update returned %d", response.StatusCode)
	}
	response.Body.Close()

	waitBody, _ := json.Marshal(WaitRequest{Cursor: before.Cursor, TimeoutMS: 100})
	response, err = http.Post(httpServer.URL+"/v0/wait", "application/json", bytes.NewReader(waitBody))
	if err != nil {
		t.Fatal(err)
	}
	var waited WaitResponse
	if err := json.NewDecoder(response.Body).Decode(&waited); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	activity := waited.Status.Live.Activity[announced.ID]
	if len(waited.LiveChanges) != 1 || waited.LiveChanges[0].Kind != "activity" || activity.Status != nexus.ActivityBlocked || len(activity.Focus) != 1 || activity.Focus[0] != event {
		t.Fatalf("composite wait lost activity: %+v", waited)
	}
	if waited.Status.Durable.Head != before.Durable.Head {
		t.Fatalf("activity update moved durable head from %s to %s", before.Durable.Head, waited.Status.Durable.Head)
	}

	// The private session remains bound to its first actor, and the opaque
	// public handle cannot be used as a substitute credential.
	response = post(presenceRequest{Actor: "other", Session: credential, Status: &blocked})
	if response.StatusCode < 400 {
		t.Fatal("another actor updated the live session")
	}
	response.Body.Close()
	response = post(presenceRequest{Actor: "human", Session: announced.ID, Status: &blocked})
	if response.StatusCode < 400 {
		t.Fatal("a public handle was accepted as a credential")
	}
	response.Body.Close()
	if server.hub.HandleFor(credential) != announced.ID {
		t.Fatal("a rejected public handle changed the original lease")
	}

	unknown := []string{"git:sha1:elsewhere#git:sha1:deadbeef"}
	response = post(presenceRequest{Actor: "human", Session: credential, Focus: &unknown})
	if response.StatusCode < 400 {
		t.Fatal("cross-room focus was accepted")
	}
	response.Body.Close()
}

func TestGraphEndpointDisclosesItsNewestEightyWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for index := 0; index < 81; index++ {
		if output, err := exec.Command("git", "-C", repo,
			"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
			"commit", "--allow-empty", "-qm", "ordinary-"+strconv.Itoa(index)).CombinedOutput(); err != nil {
			t.Fatalf("ordinary commit %d: %v: %s", index, err, output)
		}
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/v0/graph")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var graph graphResponse
	if err := json.NewDecoder(response.Body).Decode(&graph); err != nil {
		t.Fatal(err)
	}
	if !graph.Truncated || len(graph.Commits) != 80 {
		t.Fatalf("graph window = %d commits, truncated=%v", len(graph.Commits), graph.Truncated)
	}
}

func TestConversationIsForgottenWhenItsLastParticipantDeparts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, genesis, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	// The handle is minted by the service and returned on announce; a client
	// cannot compute it from the session it chose.
	handles := map[string]string{}
	credentials := map[string]string{}
	for _, name := range []string{"speaker", "bystander"} {
		credential, announced := announceCredential(t, server, presenceRequest{Actor: "human"})
		credentials[name] = credential
		handles[name] = announced.ID
	}
	say, _ := json.Marshal(sayRequest{Session: credentials["speaker"], About: genesis.ID, Text: "ephemeral only"})
	response, err := http.Post(httpServer.URL+"/v0/say", "application/json", bytes.NewReader(say))
	if err != nil {
		t.Fatal(err)
	}
	var frame nexus.Frame
	if err := json.NewDecoder(response.Body).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if frame.Conversation == "" {
		t.Fatal("say did not create a conversation")
	}

	depart, _ := json.Marshal(sessionStatusRequest{Session: credentials["speaker"]})
	response, err = http.Post(httpServer.URL+"/v0/presence/depart", "application/json", bytes.NewReader(depart))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = http.Get(httpServer.URL + "/v0/conversations/" + frame.Conversation + "/frames")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatal("conversation survived after its last participant departed")
	}
	response, err = http.Get(httpServer.URL + "/v0/presence")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var live nexus.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&live); err != nil {
		t.Fatal(err)
	}
	if _, present := live.Presence[handles["bystander"]]; !present {
		t.Fatal("unrelated presence was removed with the conversation")
	}
}

func TestExpiredCredentialCannotRenewAndDoesNotBlockNewLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human", TTLMS: 1})
	time.Sleep(10 * time.Millisecond)
	body, _ := json.Marshal(presenceRequest{Actor: "human", Session: credential, TTLMS: 30000})
	response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("expired credential renewed")
	}
	otherCredential, _ := announceCredential(t, server, presenceRequest{Actor: "other", TTLMS: 30000})
	bound, present := server.hub.SessionActor(otherCredential)
	if !present || bound != "other" {
		t.Fatalf("new lease binding = %q, present=%v", bound, present)
	}
}

func TestWatchSurfaceIsRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v0/watch?after_depth=0", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("removed watch surface returned %d", response.Code)
	}
}

func TestMutationGuardRejectsBrowserCrossOriginAndSafelistedContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		origin      string
		site        string
	}{
		{name: "safelisted content", contentType: "text/plain"},
		{name: "foreign origin", contentType: "application/json", origin: "https://elsewhere.example"},
		{name: "cross site", contentType: "application/json", site: "cross-site"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://workroom.example/v0/act", bytes.NewReader([]byte(`{}`)))
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Sec-Fetch-Site", test.site)
			if err := guardMutation(request); err == nil {
				t.Fatal("mutation guard accepted hostile browser request")
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "http://workroom.example/v0/act", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://workroom.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := guardMutation(request); err != nil {
		t.Fatalf("same-origin JSON mutation rejected: %v", err)
	}
}

func TestActEndpointUsesSessionCustodyAndReplaysSameIdempotencyKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "Ada Lovelace", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	actorsResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(actorsResponse, httptest.NewRequest(http.MethodGet, "/v0/actors", nil))
	var actors []app.ActorView
	if err := json.NewDecoder(actorsResponse.Body).Decode(&actors); err != nil {
		t.Fatal(err)
	}
	if len(actors) != 1 || actors[0].Name != "Ada Lovelace" || actors[0].Kind != "human" || !actors[0].Custody {
		t.Fatalf("actor views = %+v", actors)
	}
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "Ada Lovelace"})

	body, _ := json.Marshal(actRequest{Session: credential, Act: "state", Kind: "propose", Text: "One proposal", IdempotencyKey: "proposal-retry"})
	var firstID string
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d returned %d: %s", attempt, response.Code, response.Body.String())
		}
		var record struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			firstID = record.ID
		} else if record.ID != firstID {
			t.Fatalf("retry appended %q after %q", record.ID, firstID)
		}
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Depth != 2 {
		t.Fatalf("retry depth = %d, want genesis + one act", snapshot.Depth)
	}
}

func TestSayValidatesAndPreservesExactReplyTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, genesis, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})
	say, _ := json.Marshal(sayRequest{Session: credential, About: genesis.ID, Text: "first"})
	request := httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(say))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("say returned %d: %s", response.Code, response.Body.String())
	}
	var first nexus.Frame
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	re := first.Conversation + ":" + strconv.FormatUint(first.Sequence, 10)
	say, _ = json.Marshal(sayRequest{Session: credential, About: genesis.ID, Conversation: first.Conversation, Text: "reply", Re: re})
	request = httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(say))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reply returned %d: %s", response.Code, response.Body.String())
	}
	var reply nexus.Frame
	if err := json.NewDecoder(response.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	var payload nexus.Message
	if err := json.Unmarshal(reply.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Re != re {
		t.Fatalf("reply target = %q", payload.Re)
	}
	bad, _ := json.Marshal(sayRequest{Session: credential, About: genesis.ID, Text: "bad", Re: first.Conversation + ":99"})
	request = httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(bad))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("missing reply target was accepted")
	}
}

func TestAddressedSayAppearsInPrivateStatusAndWaitUntilAcknowledged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, genesis, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := workspace.AddActor(ctx, "human", "other", "agent")
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, value any, target any) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("POST %s returned %d: %s", path, response.Code, response.Body.String())
		}
		if target != nil {
			if err := json.NewDecoder(response.Body).Decode(target); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	humanCredential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})
	otherCredential, _ := announceCredential(t, server, presenceRequest{Actor: "other"})
	legacyCredential, _ := announceCredential(t, server, presenceRequest{Actor: "other"})
	post("/v0/inbox/register", inboxRegisterRequest{Session: otherCredential, Version: InboxProtocolVersion}, nil)
	var beforePublication Status
	post("/v0/status", sessionStatusRequest{Session: otherCredential}, &beforePublication)
	beforeInvalid := server.hub.Snapshot()
	invalidSay, _ := json.Marshal(sayRequest{
		Session: humanCredential, About: genesis.ID, Text: "@other must fail closed", InboxVersion: "unknown-inbox-version",
	})
	invalidRequest := httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(invalidSay))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown addressed inbox version returned %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	afterInvalid := server.hub.Snapshot()
	if !reflect.DeepEqual(afterInvalid.Conversations, beforeInvalid.Conversations) || afterInvalid.Cursor != beforeInvalid.Cursor {
		t.Fatalf("unknown addressed inbox version mutated live state: before=%+v after=%+v", beforeInvalid, afterInvalid)
	}

	var published nexus.Frame
	post("/v0/say", sayRequest{
		Session: humanCredential, About: genesis.ID, Text: `please review @other and @"unknown person"`, InboxVersion: InboxProtocolVersion,
	}, &published)
	var signed nexus.Message
	if err := json.Unmarshal(published.Payload, &signed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(signed.Recipients, []string{other.Fingerprint}) {
		t.Fatalf("signed recipients = %#v, want other only", signed.Recipients)
	}

	global := httptest.NewRecorder()
	server.Handler().ServeHTTP(global, httptest.NewRequest(http.MethodGet, "/v0/status", nil))
	if bytes.Contains(global.Body.Bytes(), []byte(`"inbox"`)) {
		t.Fatalf("sessionless status leaked a private inbox: %s", global.Body.String())
	}
	var addressed Status
	post("/v0/status", sessionStatusRequest{Session: otherCredential}, &addressed)
	if addressed.Inbox == nil || len(addressed.Inbox.Frames) != 1 {
		t.Fatalf("addressed status inbox = %+v", addressed.Inbox)
	}
	thread := published.Conversation + ":" + strconv.FormatUint(published.Sequence, 10)
	if addressed.Inbox.Frames[0].Thread != thread || addressed.Inbox.Frames[0].Actor == "" {
		t.Fatalf("addressed frame = %+v", addressed.Inbox.Frames[0])
	}
	var legacySession Status
	post("/v0/status", sessionStatusRequest{Session: legacyCredential}, &legacySession)
	if legacySession.Inbox == nil || len(legacySession.Inbox.Frames) != 0 {
		t.Fatalf("unregistered legacy session was enqueued: %+v", legacySession.Inbox)
	}
	var inline WaitResponse
	post("/v0/wait", WaitRequest{Cursor: beforePublication.Cursor, TimeoutMS: 20, Session: otherCredential}, &inline)
	var inlineFrame *nexus.InboxFrame
	for _, change := range inline.LiveChanges {
		if change.Frame != nil {
			inlineFrame = change.Frame
		}
	}
	if inlineFrame == nil || inlineFrame.Thread != thread || inlineFrame.Text != `please review @other and @"unknown person"` {
		t.Fatalf("pre-publication wait omitted addressed frame: %+v", inline.LiveChanges)
	}

	var repeated WaitResponse
	post("/v0/wait", WaitRequest{Cursor: addressed.Cursor, TimeoutMS: 20, Session: otherCredential}, &repeated)
	if repeated.Status.Inbox == nil || len(repeated.Status.Inbox.Frames) != 1 {
		t.Fatalf("unacknowledged wait did not repeat inbox: %+v", repeated.Status.Inbox)
	}
	var acked map[string]int
	post("/v0/inbox/ack", inboxAckRequest{Session: otherCredential, Threads: []string{thread, thread}}, &acked)
	if acked["acknowledged"] != 1 {
		t.Fatalf("acknowledged count = %d, want one actual removal", acked["acknowledged"])
	}
	post("/v0/inbox/ack", inboxAckRequest{Session: otherCredential, Threads: []string{thread}}, &acked)
	if acked["acknowledged"] != 0 {
		t.Fatalf("repeat acknowledgement removed %d frames", acked["acknowledged"])
	}
	var acknowledged WaitResponse
	post("/v0/wait", WaitRequest{Cursor: repeated.Status.Cursor, TimeoutMS: 20, Session: otherCredential}, &acknowledged)
	if acknowledged.Status.Inbox == nil || len(acknowledged.Status.Inbox.Frames) != 0 {
		t.Fatalf("acknowledged wait retained inbox: %+v", acknowledged.Status.Inbox)
	}
}

func TestMentionResolutionUsesOnlyUniqueEffectiveParticipantNames(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		text string
		want bool
	}{
		{text: `please ask @alice`, want: true},
		{text: `please ask @"Review Agent"`, want: true},
		{text: `email@alice`, want: false},
		{text: `docs/@alice/file`, want: false},
		{text: `@alice/path`, want: false},
		{text: `@"Review Agent"suffix`, want: false},
	} {
		if got := HasMentionToken(testCase.text); got != testCase.want {
			t.Errorf("HasMentionToken(%q) = %t, want %t", testCase.text, got, testCase.want)
		}
	}

	snapshot := app.Snapshot{Projection: workroom.Projection{Actors: map[string]workroom.ActorState{
		"fp-alice":       {Name: "Alice", Roles: []string{"participant"}},
		"fp-quoted":      {Name: "Review Agent", Roles: []string{"participant"}},
		"fp-duplicate-1": {Name: "same", Roles: []string{"participant"}},
		"fp-duplicate-2": {Name: "SAME", Roles: []string{"participant"}},
		"fp-retired":     {Name: "gone", Roles: []string{}, Retired: true},
		"fp-authority":   {Name: "ratifier-only", Roles: []string{"ratifier"}},
	}}}
	got := addressedRecipients(`@alice @ALICE (@"Review Agent"), @same @gone @ratifier-only @unknown email@alice foo@alice @alice/path @"Review Agent"suffix`, snapshot)
	want := []string{"fp-alice", "fp-quoted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved recipients = %#v, want %#v", got, want)
	}
}

// A published handle must not authorize anything, and /v0/act is the case that
// matters most: it appends a durable, signed event under the session's actor
// key. Testing this only at the nexus level would have covered speech and
// departure while leaving the durable path unexamined.
func TestPublishedHandleCannotAuthorizeDurableActs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "Ada Lovelace", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	credential, change := announceCredential(t, server, presenceRequest{Actor: "Ada Lovelace"})
	handle := change.ID
	if handle == "" || handle == credential {
		t.Fatalf("presence published %q for the session; it must publish a distinct handle", handle)
	}

	// This is exactly what an observer of /v0/presence holds.
	body, _ := json.Marshal(actRequest{Session: handle, Act: "state", Kind: "assert",
		Text: "forged with a published handle", IdempotencyKey: "handle-forgery"})
	forged := httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(body))
	forged.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, forged)
	if response.Code == http.StatusOK {
		t.Fatalf("a published handle authorized a durable act: %s", response.Body.String())
	}

	// The owner of the identifier is unaffected.
	owned, _ := json.Marshal(actRequest{Session: credential, Act: "state", Kind: "assert",
		Text: "the owner can still act", IdempotencyKey: "owner-act"})
	ownRequest := httptest.NewRequest(http.MethodPost, "/v0/act", bytes.NewReader(owned))
	ownRequest.Header.Set("Content-Type", "application/json")
	ownResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(ownResponse, ownRequest)
	if ownResponse.Code != http.StatusOK {
		t.Fatalf("the session's owner was refused: %d %s", ownResponse.Code, ownResponse.Body.String())
	}
}

func TestPresenceCountReturnsOnlyTheActorAggregate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := announceCredential(t, server, presenceRequest{Actor: "human"})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v0/presence-count?actor=human", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("presence count returned %d: %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result["count"] != float64(1) {
		t.Fatalf("presence count exposed more than the aggregate: %#v", result)
	}
	if strings.Contains(response.Body.String(), credential) {
		t.Fatalf("presence count leaked the private session: %s", response.Body.String())
	}
}

// A checkpoint-backed projection rebuild runs once, not once per browser
// request. The first reader may leave; another reader still joins the same
// fold and receives the exact projection only after it is ready.
func TestCheckpointProjectionRebuildIsSingleFlightAndPublishesAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	const records = 30
	for index := range records {
		text := "scaled rebuild record " + strconv.Itoa(index)
		if _, err := workspace.Act(ctx, "human", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: text,
			IdempotencyKey: "rebuild-profile-mismatch-" + strconv.Itoa(index),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Leave the repository with an exact signed kernel checkpoint. A fresh
	// workspace reuses it but must still build and publish its own projection.
	if err := workspace.InvalidateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	checkpointWriter := kernel.NewReader(workspace.Store, kernel.CheckpointOptions{
		Enabled: true, SigningKey: workspace.View().SequencerKey,
	})
	if _, err := checkpointWriter.Load(ctx, workspace.View().Genesis); err != nil {
		t.Fatal(err)
	}
	cold, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	rebuildHeld := make(chan struct{})
	releaseRebuild := make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRebuild) }) }
	t.Cleanup(release)
	var rebuilds atomic.Int64
	cold.SetProjectionRebuildTestGate(func(total int) {
		if total != records+1 {
			t.Errorf("projection rebuild events = %d, want %d", total, records+1)
		}
		rebuilds.Add(1)
		heldOnce.Do(func() { close(rebuildHeld) })
		<-releaseRebuild
	})
	server, err := New(cold)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	firstCtx, cancelFirst := context.WithCancel(ctx)
	firstRequest, err := http.NewRequestWithContext(firstCtx, http.MethodGet, httpServer.URL+"/v0/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(firstRequest)
		if response != nil {
			response.Body.Close()
		}
		firstDone <- err
	}()

	select {
	case <-rebuildHeld:
	case <-time.After(20 * time.Second):
		cancelFirst()
		t.Fatal("checkpoint projection rebuild did not reach its deterministic gate")
	}
	var observed rebuildReport
	if err := getJSON(httpServer.URL+"/v0/rebuild", &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Running || observed.Total != 0 || observed.Verified != 0 {
		cancelFirst()
		t.Fatalf("projection-only rebuild was reported as a cold kernel audit: %+v", observed)
	}

	type statusResult struct {
		status Status
		err    error
	}
	secondDone := make(chan statusResult, 1)
	go func() {
		var status Status
		err := getJSON(httpServer.URL+"/v0/status", &status)
		secondDone <- statusResult{status: status, err: err}
	}()

	cancelFirst()
	select {
	case err := <-firstDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the cancelled reader returned %v, want context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first status reader did not stop waiting after cancellation")
	}

	// The joined reader remains pending while authenticated events are held
	// before the fold. No stale or partial projection may escape.
	select {
	case result := <-secondDone:
		t.Fatalf("status returned before projection publication: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	release()

	var final Status
	select {
	case result := <-secondDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		final = result.status
	case <-time.After(5 * time.Second):
		t.Fatal("the joined status reader did not receive the published projection")
	}
	if rebuilds.Load() != 1 {
		t.Fatalf("projection rebuilds = %d, want one", rebuilds.Load())
	}
	expectedWorkspace, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := expectedWorkspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(final.Durable, expected) {
		t.Fatalf("published durable snapshot differs from an independent verified fold\npublished: %#v\nexpected:  %#v", final.Durable, expected)
	}

	// Once the rebuild is quiet, the exact projection is already available.
	immediateCtx, cancelImmediate := context.WithTimeout(ctx, time.Second)
	defer cancelImmediate()
	immediateRequest, err := http.NewRequestWithContext(immediateCtx, http.MethodGet, httpServer.URL+"/v0/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(immediateRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var immediate Status
	if err := json.NewDecoder(response.Body).Decode(&immediate); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(immediate.Durable, final.Durable) {
		t.Fatal("quiet status did not return the complete projection that was atomically published")
	}
}

// A repository with no usable checkpoint exposes one bounded, moving kernel
// audit through /v0/rebuild. Concurrent status readers join that audit and no
// reader receives a projection until the verified fold is complete.
func TestColdAuditProgressIsSingleFlightAndPublishesAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	const records = 30
	for index := range records {
		if _, err := workspace.Act(ctx, "human", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert,
			Text:           "cold rebuild record " + strconv.Itoa(index),
			IdempotencyKey: "cold-rebuild-progress-" + strconv.Itoa(index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := workspace.InvalidateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}

	cold, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	rebuildHeld := make(chan struct{})
	releaseRebuild := make(chan struct{})
	var heldOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRebuild) }) }
	t.Cleanup(release)
	cold.SetRebuildTestGate(func(progress kernel.Progress) {
		if progress.Verified != 2 {
			return
		}
		heldOnce.Do(func() { close(rebuildHeld) })
		<-releaseRebuild
	})
	server, err := New(cold)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer func() {
		release()
		httpServer.Close()
	}()

	type statusResult struct {
		status Status
		err    error
	}
	requestStatus := func() <-chan statusResult {
		done := make(chan statusResult, 1)
		go func() {
			var status Status
			done <- statusResult{status: status, err: getJSON(httpServer.URL+"/v0/status", &status)}
		}()
		return done
	}
	firstDone := requestStatus()
	select {
	case <-rebuildHeld:
	case <-time.After(20 * time.Second):
		t.Fatal("cold audit did not reach its deterministic progress gate")
	}

	var observed rebuildReport
	if err := getJSON(httpServer.URL+"/v0/rebuild", &observed); err != nil {
		t.Fatal(err)
	}
	// The kernel sequence contains the two initialization records in addition
	// to the application records appended above.
	if !observed.Running || observed.Verified != 2 || observed.Total != records+2 {
		t.Fatalf("moving cold-audit report = %+v, want 2/%d", observed, records+2)
	}
	var bounded map[string]any
	if err := getJSON(httpServer.URL+"/v0/rebuild", &bounded); err != nil {
		t.Fatal(err)
	}
	for key := range bounded {
		if key != "running" && key != "verified" && key != "total" {
			t.Fatalf("rebuild endpoint exposed unbounded field %q", key)
		}
	}

	secondDone := requestStatus()
	select {
	case result := <-secondDone:
		t.Fatalf("joined status returned during the held audit: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	release()

	var first, second Status
	for destination, done := range map[*Status]<-chan statusResult{&first: firstDone, &second: secondDone} {
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatal(result.err)
			}
			*destination = result.status
		case <-time.After(5 * time.Second):
			t.Fatal("joined status did not receive the completed projection")
		}
	}
	if first.Durable.Head == "" || !reflect.DeepEqual(first.Durable, second.Durable) {
		t.Fatal("joined readers did not receive the same complete projection")
	}
	var quiet rebuildReport
	if err := getJSON(httpServer.URL+"/v0/rebuild", &quiet); err != nil {
		t.Fatal(err)
	}
	if quiet.Running || quiet.Verified != 0 || quiet.Total != 0 {
		t.Fatalf("completed cold audit remained visible: %+v", quiet)
	}
}

func getJSON(url string, into any) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return json.NewDecoder(response.Body).Decode(into)
}

// The attention read is an adjunct, so its failure modes matter more than its
// happy path. A session the hub has never seen must still get an answer about
// other people's leases, because refusing the whole read when half of it is
// unavailable would turn an advisory extra into a precondition.
func TestAttentionAnswersAndDegradesWithoutRefusing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Focus is constrained to real identifiers from this workroom, so the test
	// uses the founding record rather than a convenient string.
	realEvent := seed.ID
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ask := func(body attentionRequest) AttentionReport {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(httpServer.URL+"/v0/attention", "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("attention returned %d for %+v; an advisory read must not refuse", response.StatusCode, body)
		}
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var report AttentionReport
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return report
	}

	// An unknown session: no inbox to report, but the read still succeeds.
	report := ask(attentionRequest{Session: "never-announced", Events: []string{realEvent}})
	if !report.Available {
		t.Fatalf("an unknown session made attention unavailable: %+v", report)
	}
	if report.Pending != 0 || len(report.Frames) != 0 || len(report.Actors) != 0 {
		t.Fatalf("an unknown session reported content: %+v", report)
	}

	// An empty request is legal and says nothing, rather than erroring.
	if report := ask(attentionRequest{}); !report.Available {
		t.Fatalf("an empty attention request was refused: %+v", report)
	}

	// A present session focused on an event is visible to another caller, and
	// never to itself.
	busy := nexus.ActivityBusy
	focus := []string{realEvent}
	watcherCredential, _ := announceCredential(t, server, presenceRequest{Actor: "human", Status: &busy, Focus: &focus})

	report = ask(attentionRequest{Session: "someone-else", Events: []string{realEvent}})
	if len(report.Actors) != 1 || report.Actors[0].Name != "human" {
		t.Fatalf("a focused actor was not reported to another session: %+v", report.Actors)
	}
	if report.Actors[0].ActivityChangedAt.IsZero() {
		t.Fatalf("the actor row carries no activity clock: %+v", report.Actors[0])
	}
	if report := ask(attentionRequest{Session: watcherCredential, Events: []string{realEvent}}); len(report.Actors) != 0 {
		t.Fatalf("a session was told about its own focus: %+v", report.Actors)
	}
	// An event nobody named matches nobody.
	if report := ask(attentionRequest{Session: "someone-else", Events: []string{"event:unwatched"}}); len(report.Actors) != 0 {
		t.Fatalf("an unrelated event matched: %+v", report.Actors)
	}

	// One actor in two windows is one row. Aggregation happens on the durable
	// fingerprint the resident resolved, not on the session, so a person
	// working from two sessions does not read as two people watching.
	_, _ = announceCredential(t, server, presenceRequest{Actor: "human", Status: &busy, Focus: &focus})

	report = ask(attentionRequest{Session: "someone-else", Events: []string{realEvent}})
	if len(report.Actors) != 1 {
		t.Fatalf("two sessions of one actor produced %d rows: %+v", len(report.Actors), report.Actors)
	}
	if report.Actors[0].Sessions != 2 {
		t.Fatalf("aggregated row reports %d sessions, want 2: %+v", report.Actors[0].Sessions, report.Actors[0])
	}
	// The caller filter still applies per session: asking as one of that
	// actor's own sessions leaves only the other one visible.
	if report := ask(attentionRequest{Session: watcherCredential, Events: []string{realEvent}}); len(report.Actors) != 1 || report.Actors[0].Sessions != 1 {
		t.Fatalf("asking as one of the actor's own sessions reported %+v", report.Actors)
	}
}

// The identity endpoint exists so a starting resident can tell a live
// incumbent from a claim left by a dead one. Everything about it is shaped by
// that job: it says which workroom and process answer here, it needs no
// credential, it changes nothing, and it grants no cross-origin authority.
func TestIdentitySaysWhichWorkroomAnswersAndNothingElse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/v0/identity")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("identity answered %s", response.Status)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("identity granted cross-origin authority: %q", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("identity is cacheable: %q", got)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		t.Fatal(err)
	}
	var identity map[string]any
	if err := json.Unmarshal(body, &identity); err != nil {
		t.Fatalf("identity body %q: %v", body, err)
	}
	if len(identity) != 2 || identity["genesis"] != workspace.View().Genesis || identity["pid"] != float64(os.Getpid()) {
		t.Fatalf("identity answered %v; it must carry only the genesis and answering process id", identity)
	}

	// Nothing here accepts a write, so a probe cannot be turned into one.
	posted, err := http.Post(httpServer.URL+"/v0/identity", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer posted.Body.Close()
	if posted.StatusCode == http.StatusOK {
		t.Fatal("identity accepted a POST")
	}
}

// The merge station is asked of git at render time. What matters at this
// boundary is that a browser cannot name the ref, that a commit which is not
// on the mainline is reported absent rather than landed, and that the answer
// carries the branch it is about.
func TestLandedEndpointAnswersFromTheMainlineItResolves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)
		output, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	if output, err := exec.Command("git", "init", "-q", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	git("commit", "--allow-empty", "-qm", "root")
	git("checkout", "-qb", "side")
	git("commit", "--allow-empty", "-qm", "side work")
	side := git("rev-parse", "HEAD")
	git("checkout", "-q", "main")

	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	ask := func(commits ...string) landedResponse {
		t.Helper()
		body, _ := json.Marshal(landedRequest{Commits: commits})
		response, err := http.Post(httpServer.URL+"/v0/landed", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var answer landedResponse
		if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}

	head := git("rev-parse", "main")
	answer := ask(head, side)
	if answer.Branch != "main" {
		t.Fatalf("the answer names the ref it is about: %#v", answer)
	}
	if len(answer.Commits) != 2 {
		t.Fatalf("one answer per commit: %#v", answer.Commits)
	}
	if answer.Commits[0].Status != gitstore.LandingLanded {
		t.Fatalf("main's own head is on main: %#v", answer.Commits[0])
	}
	if answer.Commits[1].Status != gitstore.LandingAbsent {
		t.Fatalf("an unmerged side head is absent: %#v", answer.Commits[1])
	}
	// A ref name arriving as a commit is refused before git runs: only the
	// server chooses the branch.
	if refused := ask("main").Commits[0]; refused.Status != gitstore.LandingUnknown {
		t.Fatalf("a ref name is not a commit: %#v", refused)
	}
}

// The bound is a property of the endpoint. An earlier head enforced it only on
// the path that finds a mainline, so a repository without one would iterate and
// echo back every untrusted commit a caller sent. A cap that holds only when
// the happy path runs is not a cap, and the failure path is the easier one to
// reach.
func TestLandedEndpointBoundsTheBatchWithNoMainline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	// Deliberately neither main nor master, so no mainline ref resolves.
	if output, err := exec.Command("git", "init", "-q", "-b", "trunk", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo,
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", "root").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	commits := make([]string, gitstore.LandingLimit+5)
	for index := range commits {
		commits[index] = strings.Repeat("a", 40)
	}
	body, _ := json.Marshal(landedRequest{Commits: commits})
	response, err := http.Post(httpServer.URL+"/v0/landed", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var answer landedResponse
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Commits) != gitstore.LandingLimit {
		t.Fatalf("no-mainline path returned %d answers, want the %d bound", len(answer.Commits), gitstore.LandingLimit)
	}
	if answer.Branch != "" {
		t.Fatalf("no mainline resolved, so no branch is named: %q", answer.Branch)
	}
	for _, landing := range answer.Commits {
		// Absent would be a lie: with nothing to compare against, the honest
		// answer is that it could not be determined.
		if landing.Status != gitstore.LandingUnknown || landing.Reason == "" {
			t.Fatalf("missing mainline must answer unknown with a reason: %#v", landing)
		}
	}
}
