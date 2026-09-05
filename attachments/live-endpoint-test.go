package service

import (
	"context"
	"encoding/json"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestPlannerLiveWorktreeEndpoint(t *testing.T) {
	repo := os.Getenv("GITSEQ_WORKTREE_MEASURE_REPO")
	if repo == "" {
		t.Skip("local endpoint diagnostic")
	}
	workspace, err := app.Open(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	start := time.Now()
	response, err := http.Get(httpServer.URL + "/v0/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result worktreesResponse
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	coldElapsed := time.Since(start)
	response.Body.Close()
	start = time.Now()
	response, err = http.Get(httpServer.URL + "/v0/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	counts := map[string]int{}
	for _, v := range result.Worktrees {
		counts[v.Classification]++
	}
	evidence := map[string]any{"http_status": response.StatusCode, "elapsed_seconds": elapsed.Seconds(), "cold_elapsed_seconds": coldElapsed.Seconds(), "counts": counts, "response": result, "method": "real HTTP route in httptest.NewServer; app.Open on live repository; existing resident untouched"}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile("/tmp/w1-planner-live-endpoint.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%d cold=%s warm=%s counts=%v deletable=%v", response.StatusCode, coldElapsed, elapsed, counts, result.Deletable)
	if response.StatusCode != 200 || len(result.Worktrees) < 35 || counts["unknown"] != 0 {
		t.Fatal("real room remains incomplete")
	}
}
