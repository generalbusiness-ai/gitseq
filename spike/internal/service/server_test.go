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

	"gitseq/spike/internal/app"
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
