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

	// An unenumerated scheme that still carries an authority, so the host check
	// is not what refuses it. Only an allowlist refuses this.
	setRemote("future+git://github.com/generalbusiness-ai/gitseq.git")
	if local, body := read(); local.Remote != "" || strings.Contains(body, `"remote"`) {
		t.Fatalf("an unenumerated scheme reached the browser: %s", body)
	}

	setRemote("https://x-access-token:s3cr3t-token@github.com/generalbusiness-ai/gitseq.git")
	if _, body := read(); strings.Contains(body, "s3cr3t-token") || strings.Contains(body, `"remote"`) {
		t.Fatalf("credential-bearing remote reached the browser: %s", body)
	}

	// A query carries credential material as readily as userinfo does, and is
	// refused by the same rule rather than passed through to the DOM.
	setRemote("https://github.com/generalbusiness-ai/gitseq.git?access_token=s3cr3t-token")
	if _, body := read(); strings.Contains(body, "s3cr3t-token") || strings.Contains(body, `"remote"`) {
		t.Fatalf("a query-bearing remote reached the browser: %s", body)
	}

	setRemote("https://github.com/generalbusiness-ai/gitseq.git#access_token=s3cr3t-token")
	if _, body := read(); strings.Contains(body, "s3cr3t-token") || strings.Contains(body, `"remote"`) {
		t.Fatalf("a fragment-bearing remote reached the browser: %s", body)
	}
}

// The end of the outer-scope config path: what the browser is actually handed.
// Without --local, `git config` merges command scope (GIT_CONFIG_COUNT and its
// key/value pairs) over the repository's own, and the outer value is emitted
// last, so it is the one that would have been serialised here.
func TestWorktreesResponseIgnoresOutOfRepositoryRemoteConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if _, _, err := app.Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "config", "remote.origin.url", "https://real.invalid/org/repo.git").CombinedOutput(); err != nil {
		t.Fatalf("set remote: %v: %s", err, output)
	}

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "remote.origin.url")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://attacker.invalid/evil.git")

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
	if strings.Contains(string(body), "attacker.invalid") {
		t.Fatalf("an out-of-repository remote reached the browser: %s", body)
	}
	var local worktreesResponse
	if err := json.Unmarshal(body, &local); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if local.Remote != "https://real.invalid/org/repo.git" {
		t.Fatalf("remote = %q, want the repository's own local origin", local.Remote)
	}
}
