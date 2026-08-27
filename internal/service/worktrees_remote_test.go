package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// /v0/worktrees is the only path by which a remote reaches the browser, so the
// response is pinned here rather than inferred from the app-layer value. The
// no-remote case must serialise exactly as it did before the field existed: the
// key is absent, not present and empty.
func TestWorktreesResponseCarriesOnlyALinkableRemote(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if _, _, err := app.Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}

	// The eight-second worktree cache lives on the workspace, so each case gets
	// its own workspace and its own server rather than a re-read of the last.
	read := func() (worktreesResponse, string) {
		t.Helper()
		workspace, err := app.Open(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		server, err := New(workspace)
		if err != nil {
			t.Fatal(err)
		}
		httpServer := httptest.NewServer(server.Handler())
		defer httpServer.Close()
		response, err := http.Get(httpServer.URL + "/v0/worktrees")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var local worktreesResponse
		if err := json.Unmarshal(body, &local); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return local, string(body)
	}

	setRemote := func(value string) {
		t.Helper()
		if output, err := exec.Command("git", "-C", repo, "config", "remote.origin.url", value).CombinedOutput(); err != nil {
			t.Fatalf("set remote: %v: %s", err, output)
		}
	}

	local, body := read()
	if local.Remote != "" || strings.Contains(body, `"remote"`) {
		t.Fatalf("repository with no remote answered %s", body)
	}

	setRemote("https://github.com/generalbusiness-ai/gitseq.git")
	if local, body := read(); local.Remote != "https://github.com/generalbusiness-ai/gitseq.git" {
		t.Fatalf("https remote not carried to the browser: %s", body)
	}

	setRemote("git@github.com:generalbusiness-ai/gitseq.git")
	if local, body := read(); local.Remote != "" || strings.Contains(body, `"remote"`) {
		t.Fatalf("scp-style remote reached the browser: %s", body)
	}

	// git:// parses cleanly and carries no userinfo, so only the scheme
	// allowlist can refuse it: the case a permissive scheme check would pass.
	setRemote("git://github.com/generalbusiness-ai/gitseq.git")
	if local, body := read(); local.Remote != "" || strings.Contains(body, `"remote"`) {
		t.Fatalf("git:// remote reached the browser: %s", body)
	}

	setRemote("https://x-access-token:s3cr3t-token@github.com/generalbusiness-ai/gitseq.git")
	if _, body := read(); strings.Contains(body, "s3cr3t-token") || strings.Contains(body, `"remote"`) {
		t.Fatalf("credential-bearing remote reached the browser: %s", body)
	}
}
