package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

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

func TestStatusPresenceAndResettableLiveLayer(t *testing.T) {
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
	announce, _ := json.Marshal(presenceRequest{Actor: "human", Session: "session-1"})
	response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(announce))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	response, err = http.Get(httpServer.URL + "/v0/status")
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
	if len(status.Durable.Vocabulary.Definitions) != 13 || status.Durable.Vocabulary.Binding.Status != "unbound" {
		t.Fatalf("status did not serve the room vocabulary and binding state: %+v", status.Durable.Vocabulary)
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
		summary.Durable.Depth != status.Durable.Depth || len(summary.Cursor.Frontier) != 1 || summary.Cursor.Frontier[0].Head != status.Durable.Head {
		t.Fatalf("summary frontier differs from full status: summary=%+v full=%+v", summary, status)
	}
	fingerprint := workspace.Config.Actors["human"].Fingerprint
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

	actor := workspace.Config.Actors["human"]
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
	response := post(presenceRequest{Actor: "human", Session: "private"})
	var announced nexus.Change
	if err := json.NewDecoder(response.Body).Decode(&announced); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response, err = http.Get(httpServer.URL + "/v0/status")
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
	response = post(presenceRequest{Actor: "human", Session: "private", Status: &blocked, Focus: &focus, Note: &note})
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
	response = post(presenceRequest{Actor: "other", Session: "private", Status: &blocked})
	if response.StatusCode < 400 {
		t.Fatal("another actor updated the live session")
	}
	response.Body.Close()
	response = post(presenceRequest{Actor: "human", Session: announced.ID, Status: &blocked})
	var separate nexus.Change
	if err := json.NewDecoder(response.Body).Decode(&separate); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if separate.ID == announced.ID || server.hub.HandleFor("private") != announced.ID {
		t.Fatal("a separately announced lease mutated the original private session")
	}

	unknown := []string{"git:sha1:elsewhere#git:sha1:deadbeef"}
	response = post(presenceRequest{Actor: "human", Session: "private", Focus: &unknown})
	if response.StatusCode < 400 {
		t.Fatal("cross-room focus was accepted")
	}
	response.Body.Close()
}

func TestGraphEndpointDisclosesItsNewestEightyWindow(t *testing.T) {
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
	for _, session := range []string{"speaker", "bystander"} {
		body, _ := json.Marshal(presenceRequest{Actor: "human", Session: session})
		response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var announced nexus.Change
		if err := json.NewDecoder(response.Body).Decode(&announced); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		handles[session] = announced.ID
	}
	say, _ := json.Marshal(sayRequest{Session: "speaker", About: genesis.ID, Text: "ephemeral only"})
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

	request, _ := http.NewRequest(http.MethodDelete, httpServer.URL+"/v0/presence/speaker", nil)
	response, err = http.DefaultClient.Do(request)
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

func TestExpiredSessionBindingDoesNotBlockRebind(t *testing.T) {
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

	announce := func(actor string, ttl int) int {
		t.Helper()
		body, _ := json.Marshal(presenceRequest{Actor: actor, Session: "reused", TTLMS: ttl})
		response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if status := announce("human", 1); status != http.StatusOK {
		t.Fatalf("initial announce status = %d", status)
	}
	time.Sleep(10 * time.Millisecond)
	if status := announce("other", 30000); status != http.StatusOK {
		t.Fatalf("expired session could not be rebound: status = %d", status)
	}
	bound, present := server.hub.SessionActor("reused")
	if !present {
		t.Fatal("rebound session is not present")
	}
	if bound != "other" {
		t.Fatalf("session remained bound to %q", bound)
	}
}

func TestWatchSurfaceIsRemoved(t *testing.T) {
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
	announce, _ := json.Marshal(presenceRequest{Actor: "Ada Lovelace", Session: "browser"})
	presence := httptest.NewRequest(http.MethodPost, "/v0/presence", bytes.NewReader(announce))
	presence.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(httptest.NewRecorder(), presence)

	body, _ := json.Marshal(actRequest{Session: "browser", Act: "state", Kind: "propose", Text: "One proposal", IdempotencyKey: "proposal-retry"})
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
	announce, _ := json.Marshal(presenceRequest{Actor: "human", Session: "speaker"})
	request := httptest.NewRequest(http.MethodPost, "/v0/presence", bytes.NewReader(announce))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(httptest.NewRecorder(), request)
	say, _ := json.Marshal(sayRequest{Session: "speaker", About: genesis.ID, Text: "first"})
	request = httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(say))
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
	say, _ = json.Marshal(sayRequest{Session: "speaker", About: genesis.ID, Conversation: first.Conversation, Text: "reply", Re: re})
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
	bad, _ := json.Marshal(sayRequest{Session: "speaker", About: genesis.ID, Text: "bad", Re: first.Conversation + ":99"})
	request = httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(bad))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("missing reply target was accepted")
	}
}

func TestAddressedSayAppearsInPrivateStatusAndWaitUntilAcknowledged(t *testing.T) {
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
	post("/v0/presence", presenceRequest{Actor: "human", Session: "human-session"}, nil)
	post("/v0/presence", presenceRequest{Actor: "other", Session: "other-session"}, nil)
	post("/v0/presence", presenceRequest{Actor: "other", Session: "other-legacy-session"}, nil)
	post("/v0/inbox/register", inboxRegisterRequest{Session: "other-session", Version: InboxProtocolVersion}, nil)
	var beforePublication Status
	post("/v0/status", sessionStatusRequest{Session: "other-session"}, &beforePublication)
	beforeInvalid := server.hub.Snapshot()
	invalidSay, _ := json.Marshal(sayRequest{
		Session: "human-session", About: genesis.ID, Text: "@other must fail closed", InboxVersion: "unknown-inbox-version",
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
		Session: "human-session", About: genesis.ID, Text: `please review @other and @"unknown person"`, InboxVersion: InboxProtocolVersion,
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
	post("/v0/status", sessionStatusRequest{Session: "other-session"}, &addressed)
	if addressed.Inbox == nil || len(addressed.Inbox.Frames) != 1 {
		t.Fatalf("addressed status inbox = %+v", addressed.Inbox)
	}
	thread := published.Conversation + ":" + strconv.FormatUint(published.Sequence, 10)
	if addressed.Inbox.Frames[0].Thread != thread || addressed.Inbox.Frames[0].Actor == "" {
		t.Fatalf("addressed frame = %+v", addressed.Inbox.Frames[0])
	}
	var legacySession Status
	post("/v0/status", sessionStatusRequest{Session: "other-legacy-session"}, &legacySession)
	if legacySession.Inbox == nil || len(legacySession.Inbox.Frames) != 0 {
		t.Fatalf("unregistered legacy session was enqueued: %+v", legacySession.Inbox)
	}
	var inline WaitResponse
	post("/v0/wait", WaitRequest{Cursor: beforePublication.Cursor, TimeoutMS: 20, Session: "other-session"}, &inline)
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
	post("/v0/wait", WaitRequest{Cursor: addressed.Cursor, TimeoutMS: 20, Session: "other-session"}, &repeated)
	if repeated.Status.Inbox == nil || len(repeated.Status.Inbox.Frames) != 1 {
		t.Fatalf("unacknowledged wait did not repeat inbox: %+v", repeated.Status.Inbox)
	}
	var acked map[string]int
	post("/v0/inbox/ack", inboxAckRequest{Session: "other-session", Threads: []string{thread, thread}}, &acked)
	if acked["acknowledged"] != 1 {
		t.Fatalf("acknowledged count = %d, want one actual removal", acked["acknowledged"])
	}
	post("/v0/inbox/ack", inboxAckRequest{Session: "other-session", Threads: []string{thread}}, &acked)
	if acked["acknowledged"] != 0 {
		t.Fatalf("repeat acknowledgement removed %d frames", acked["acknowledged"])
	}
	var acknowledged WaitResponse
	post("/v0/wait", WaitRequest{Cursor: repeated.Status.Cursor, TimeoutMS: 20, Session: "other-session"}, &acknowledged)
	if acknowledged.Status.Inbox == nil || len(acknowledged.Status.Inbox.Frames) != 0 {
		t.Fatalf("acknowledged wait retained inbox: %+v", acknowledged.Status.Inbox)
	}
}

func TestMentionResolutionUsesOnlyUniqueEffectiveParticipantNames(t *testing.T) {
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
	announce, _ := json.Marshal(presenceRequest{Actor: "Ada Lovelace", Session: "private-session"})
	request := httptest.NewRequest(http.MethodPost, "/v0/presence", bytes.NewReader(announce))
	request.Header.Set("Content-Type", "application/json")
	announced := httptest.NewRecorder()
	server.Handler().ServeHTTP(announced, request)
	if announced.Code != http.StatusOK {
		t.Fatalf("announce returned %d: %s", announced.Code, announced.Body.String())
	}
	var change nexus.Change
	if err := json.Unmarshal(announced.Body.Bytes(), &change); err != nil {
		t.Fatal(err)
	}
	handle := change.ID
	if handle == "" || handle == "private-session" {
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
	owned, _ := json.Marshal(actRequest{Session: "private-session", Act: "state", Kind: "assert",
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
	announce, _ := json.Marshal(presenceRequest{Actor: "human", Session: "private-session"})
	presence := httptest.NewRequest(http.MethodPost, "/v0/presence", bytes.NewReader(announce))
	presence.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(httptest.NewRecorder(), presence)

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
	if strings.Contains(response.Body.String(), "private-session") {
		t.Fatalf("presence count leaked the private session: %s", response.Body.String())
	}
}

// A fold-profile bump must run one resident rebuild, not one audit per browser
// request. The first reader may leave; another reader still joins the same
// verification and receives the exact projection only after it is ready.
func TestProfileMismatchRebuildIsSingleFlightAndPublishesAtomically(t *testing.T) {
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

	// Leave the repository with an exact signed checkpoint under the previous
	// application profile. A fresh current-profile Workspace must reject it and
	// audit from genesis.
	oldReader := kernel.NewReader(workspace.Store, kernel.CheckpointOptions{
		Profile: "deliberately-old-test-profile", SigningKey: workspace.Config.SequencerKey,
	})
	if _, err := oldReader.Load(ctx, workspace.Config.Genesis); err != nil {
		t.Fatal(err)
	}
	cold, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
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

	deadline := time.Now().Add(20 * time.Second)
	var observed rebuildReport
	for time.Now().Before(deadline) {
		if err := getJSON(httpServer.URL+"/v0/rebuild", &observed); err != nil {
			t.Fatal(err)
		}
		if observed.Running && observed.Total > 0 && observed.Verified > 0 && observed.Verified < observed.Total/2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !observed.Running || observed.Total == 0 || observed.Verified == 0 || observed.Verified >= observed.Total/2 {
		cancelFirst()
		t.Fatalf("no moving cold-rebuild report was observed: %+v", observed)
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

	// A third reader remains pending while verification is known to be active;
	// no stale or partial HTTP response escapes merely because it asked early.
	thirdCtx, cancelThird := context.WithCancel(ctx)
	thirdRequest, err := http.NewRequestWithContext(thirdCtx, http.MethodGet, httpServer.URL+"/v0/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	thirdDone := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(thirdRequest)
		if response != nil {
			response.Body.Close()
		}
		thirdDone <- err
	}()
	select {
	case err := <-thirdDone:
		t.Fatalf("status returned before the known mid-scan rebuild finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	var stillRunning rebuildReport
	if err := getJSON(httpServer.URL+"/v0/rebuild", &stillRunning); err != nil {
		t.Fatal(err)
	}
	if !stillRunning.Running || stillRunning.Verified >= stillRunning.Total {
		t.Fatalf("the rebuild did not remain active while the third status request stayed pending: %+v", stillRunning)
	}
	cancelThird()
	select {
	case err := <-thirdDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the cancelled third reader returned %v, want context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the third status reader did not stop after cancellation")
	}

	lastVerified, fixedTotal := observed.Verified, observed.Total
	var final Status
	for time.Now().Before(deadline) {
		var progress rebuildReport
		if err := getJSON(httpServer.URL+"/v0/rebuild", &progress); err != nil {
			t.Fatal(err)
		}
		if !progress.Running {
			select {
			case result := <-secondDone:
				if result.err != nil {
					t.Fatal(result.err)
				}
				final = result.status
			case <-time.After(2 * time.Second):
				t.Fatal("rebuild became quiet before the joined status reader received the projection")
			}
			break
		}
		if progress.Total > 0 && progress.Total != fixedTotal {
			t.Fatalf("one rebuild changed total from %d to %d", fixedTotal, progress.Total)
		}
		if progress.Verified < lastVerified {
			t.Fatalf("progress restarted from %d at %d after the first reader cancelled", lastVerified, progress.Verified)
		}
		lastVerified = progress.Verified
		time.Sleep(time.Millisecond)
	}
	if final.Durable.Head == "" {
		t.Fatal("the joined status reader never received a final projection")
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
	announce, err := json.Marshal(presenceRequest{Actor: "human", Session: "watcher", Status: &busy, Focus: &focus})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(announce))
	if err != nil {
		t.Fatal(err)
	}
	announced, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("announce failed %d: %s", response.StatusCode, announced)
	}

	report = ask(attentionRequest{Session: "someone-else", Events: []string{realEvent}})
	if len(report.Actors) != 1 || report.Actors[0].Name != "human" {
		t.Fatalf("a focused actor was not reported to another session: %+v", report.Actors)
	}
	if report.Actors[0].ActivityChangedAt.IsZero() {
		t.Fatalf("the actor row carries no activity clock: %+v", report.Actors[0])
	}
	if report := ask(attentionRequest{Session: "watcher", Events: []string{realEvent}}); len(report.Actors) != 0 {
		t.Fatalf("a session was told about its own focus: %+v", report.Actors)
	}
	// An event nobody named matches nobody.
	if report := ask(attentionRequest{Session: "someone-else", Events: []string{"event:unwatched"}}); len(report.Actors) != 0 {
		t.Fatalf("an unrelated event matched: %+v", report.Actors)
	}

	// One actor in two windows is one row. Aggregation happens on the durable
	// fingerprint the resident resolved, not on the session, so a person
	// working from two sessions does not read as two people watching.
	second, err := json.Marshal(presenceRequest{Actor: "human", Session: "watcher-2", Status: &busy, Focus: &focus})
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(second))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	report = ask(attentionRequest{Session: "someone-else", Events: []string{realEvent}})
	if len(report.Actors) != 1 {
		t.Fatalf("two sessions of one actor produced %d rows: %+v", len(report.Actors), report.Actors)
	}
	if report.Actors[0].Sessions != 2 {
		t.Fatalf("aggregated row reports %d sessions, want 2: %+v", report.Actors[0].Sessions, report.Actors[0])
	}
	// The caller filter still applies per session: asking as one of that
	// actor's own sessions leaves only the other one visible.
	if report := ask(attentionRequest{Session: "watcher", Events: []string{realEvent}}); len(report.Actors) != 1 || report.Actors[0].Sessions != 1 {
		t.Fatalf("asking as one of the actor's own sessions reported %+v", report.Actors)
	}
}
