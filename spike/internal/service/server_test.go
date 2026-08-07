package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/nexus"
)

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
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(repo)) {
		t.Fatalf("local projection exposed absolute repository path: %s", encoded)
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

	for _, session := range []string{"speaker", "bystander"} {
		body, _ := json.Marshal(presenceRequest{Actor: "human", Session: session})
		response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
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
	if _, present := live.Presence["bystander"]; !present {
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

func TestSayPreservesReplyTarget(t *testing.T) {
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
	say, _ := json.Marshal(sayRequest{Session: "speaker", About: genesis.ID, Text: "reply", Re: "conversation:7"})
	request = httptest.NewRequest(http.MethodPost, "/v0/say", bytes.NewReader(say))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("say returned %d: %s", response.Code, response.Body.String())
	}
	var frame nexus.Frame
	if err := json.NewDecoder(response.Body).Decode(&frame); err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["re"] != "conversation:7" {
		t.Fatalf("reply target = %q", payload["re"])
	}
}
