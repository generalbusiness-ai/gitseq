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
	server.mu.Lock()
	bound := server.sessions["reused"]
	server.mu.Unlock()
	if bound != "other" {
		t.Fatalf("session remained bound to %q", bound)
	}
}
