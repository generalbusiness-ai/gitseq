package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/testgit"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// mergeTestTarget names the destination a hand-sealed test receipt landed on:
// the fixture repository's own workroom, on the branch every fixture checks
// out. A receipt built without it would read as legacy, which is a different
// case these tests are not exercising.
func mergeTestTarget(workspace *app.Workspace, preHead string) mergeplan.Target {
	return mergeplan.Target{Repo: mergeplan.WorkroomRepo(workspace), Ref: "refs/heads/main", PreHead: preHead}
}

func mustClassify(t testing.TB, ctx context.Context, checkout string, projection workroom.Projection, changes []mergeplan.Change, targetPreHead, candidate string, reviewed map[string]bool) map[string]mergeplan.Candidate {
	t.Helper()
	classified, err := mergeplan.Classify(ctx, checkout, projection, changes, targetPreHead, candidate, reviewed)
	if err != nil {
		t.Fatal(err)
	}
	return classified
}

type mergePlanReadOnlyState struct {
	GitHead          string
	WorkroomHead     string
	Depth            int
	VerifiedFrontier string
	CheckpointRef    string
	CheckpointFile   string
	Config           string
}

func captureMergePlanReadOnlyState(t *testing.T, workspace *app.Workspace) mergePlanReadOnlyState {
	t.Helper()
	snapshot, err := workspace.ReadOnlySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := json.Marshal(workspace.View().VerifiedFrontier)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workspace.Store.Head(context.Background(), kernel.CheckpointRef(workspace.View().Genesis))
	if err != nil {
		checkpoint = "absent: " + err.Error()
	}
	pointer, err := os.ReadFile(filepath.Join(workspace.MetaDir, "checkpoints", workspace.View().Genesis+".json"))
	if err != nil {
		pointer = []byte("absent: " + err.Error())
	}
	config, err := os.ReadFile(filepath.Join(workspace.MetaDir, apphost.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	return mergePlanReadOnlyState{
		GitHead: testGit(t, workspace.Repo, "rev-parse", "HEAD"), WorkroomHead: snapshot.Head, Depth: snapshot.Depth,
		VerifiedFrontier: string(frontier), CheckpointRef: checkpoint, CheckpointFile: string(pointer), Config: string(config),
	}
}

func TestValidateLoopbackListen(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"127.0.0.1:7777": true,
		"[::1]:7777":     true,
		"localhost:7777": true,
		":7777":          false,
		"0.0.0.0:7777":   false,
		"192.0.2.1:7777": false,
		"not-an-address": false,
	}
	for address, want := range tests {
		t.Run(address, func(t *testing.T) {
			if got := validateLoopbackListen(address) == nil; got != want {
				t.Fatalf("validateLoopbackListen(%q) success = %v, want %v", address, got, want)
			}
		})
	}
}

func TestValidateLoopbackListenRejectsMixedResolution(t *testing.T) {
	previous := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		if host != "mixed.example" {
			t.Fatalf("lookup host = %q, want mixed.example", host)
		}
		return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.0.2.1")}, nil
	}
	t.Cleanup(func() { lookupIP = previous })

	if err := validateLoopbackListen("mixed.example:7777"); err == nil {
		t.Fatal("listener accepted a hostname with a non-loopback resolution")
	}
}

func TestResidentHTTPServerBoundsSlowRequestBodies(t *testing.T) {
	t.Parallel()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.ReadAll(request.Body); err != nil {
			http.Error(writer, "request body deadline exceeded", http.StatusRequestTimeout)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	server := residentHTTPServer(handler)
	if server.ReadHeaderTimeout != residentReadHeaderTimeout || server.ReadTimeout != residentReadTimeout ||
		server.WriteTimeout != residentWriteTimeout || server.IdleTimeout != residentIdleTimeout ||
		server.MaxHeaderBytes != residentMaxHeaderBytes {
		t.Fatalf("resident HTTP limits = %+v", server)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.ReadTimeout = 50 * time.Millisecond
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-serveDone
	})

	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := fmt.Fprintf(connection, "POST / HTTP/1.1\r\nHost: %s\r\nContent-Length: 100\r\n\r\n{", listener.Addr()); err != nil {
		t.Fatal(err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("slow request did not terminate predictably: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("slow request returned %d", response.StatusCode)
	}
}

func TestResidentHTTPServerLetsColdStatusOutliveWriteTimeout(t *testing.T) {
	t.Parallel()
	const writeTimeout = 50 * time.Millisecond
	handler := residentHTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(3 * writeTimeout)
		writer.WriteHeader(http.StatusNoContent)
	}))
	server := residentHTTPServer(handler)
	server.WriteTimeout = writeTimeout

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-serveDone
	})

	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	response, err := client.Get("http://" + listener.Addr().String() + "/v0/status")
	if err != nil {
		t.Fatalf("slow status was cut by the ordinary write deadline: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("slow status returned %d", response.StatusCode)
	}

	response, err = client.Get("http://" + listener.Addr().String() + "/ordinary")
	if err == nil {
		response.Body.Close()
		t.Fatalf("ordinary response survived the %s write deadline with status %d", writeTimeout, response.StatusCode)
	}
}

// The listener check is not only a check, it is an ordering: a start that
// refuses must leave the repository exactly as it found it, so no client is
// ever sent to an address nothing is listening on. The test above exercises
// the validator alone; this one drives the command, where the ordering lives.
func TestAServeRefusedForANonLoopbackListenAdvertisesNothing(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// The refusal returns at once. The deadline is only so that a build which
	// serves instead of refusing fails here rather than hanging the package.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = serveCommand(ctx, []string{"--repo", repo, "--listen", "0.0.0.0:0"})
	if err == nil {
		t.Fatal("serve accepted a non-loopback listen address")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("serve refused a non-loopback listen address for another reason: %v", err)
	}
	if published, ok := advertised(workspace); ok {
		t.Fatalf("a refused serve advertised %q", published)
	}
}

func TestProfilerIsDisabledByDefaultAndLoopbackOnly(t *testing.T) {
	t.Parallel()
	stop, err := serveProfiler(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := serveProfiler(context.Background(), "0.0.0.0:0"); err == nil {
		t.Fatal("profiler accepted a non-loopback listener")
	}
}

func statusSummaryFixture(t *testing.T) (*app.Workspace, service.SummaryStatus) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", repo)
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	frontier := service.Frontier{Genesis: snapshot.Genesis, Head: snapshot.Head, Depth: snapshot.Depth}
	return workspace, service.SummaryStatus{
		Durable: statusview.Build(snapshot.Genesis, snapshot.Head, snapshot.Depth, snapshot.Projection),
		Cursor:  service.Cursor{Frontier: []service.Frontier{frontier}},
	}
}

func TestFetchSummaryPinsGenesisHeadCursorAndResponseCap(t *testing.T) {
	t.Parallel()
	if summaryResponseLimit != 64<<10 {
		t.Fatalf("summary response limit = %d, want 64 KiB", summaryResponseLimit)
	}
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	serve := func(handler http.HandlerFunc) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}
	valid := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(summary) })
	if got, err := fetchSummary(ctx, workspace, valid.URL); err != nil || got.Durable.Head != summary.Durable.Head {
		t.Fatalf("valid resident summary = %+v, %v", got, err)
	}

	wrongGenesis := summary
	wrongGenesis.Cursor.Frontier = append([]service.Frontier(nil), summary.Cursor.Frontier...)
	wrongGenesis.Durable.Genesis = "foreign"
	wrongGenesis.Cursor.Frontier[0].Genesis = "foreign"
	foreign := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(wrongGenesis) })
	if _, err := fetchSummary(ctx, workspace, foreign.URL); err == nil || !strings.Contains(err.Error(), "genesis") {
		t.Fatalf("foreign-genesis error = %v", err)
	}

	wrongHead := summary
	wrongHead.Durable.Head = strings.Repeat("0", len(summary.Durable.Head))
	stale := serve(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(wrongHead) })
	if _, err := fetchSummary(ctx, workspace, stale.URL); err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("stale-head error = %v", err)
	}

	redirect := serve(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, valid.URL, http.StatusFound)
	})
	if _, err := fetchSummary(ctx, workspace, redirect.URL); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect error = %v", err)
	}

	oversized := serve(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(bytes.Repeat([]byte("x"), (64<<10)+1))
	})
	if _, err := fetchSummary(ctx, workspace, oversized.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestFetchSummaryRejectsSlowAndMovingResident(t *testing.T) {
	ctx := context.Background()
	workspace, summary := statusSummaryFixture(t)
	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
			return
		case <-time.After(3 * time.Second):
			_ = json.NewEncoder(writer).Encode(summary)
		}
	}))
	defer slow.Close()
	started := time.Now()
	if _, err := fetchSummary(ctx, workspace, slow.URL); err == nil || time.Since(started) > 2500*time.Millisecond {
		t.Fatalf("slow resident error = %v after %s", err, time.Since(started))
	}

	moving := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		basis := workspace.EventID(workspace.View().Genesis)
		if _, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance while status is in flight",
			RestsOn: []string{basis}, IdempotencyKey: "moving-status-head",
		}); err != nil {
			t.Errorf("advance head: %v", err)
			return
		}
		advanced, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Errorf("advanced snapshot: %v", err)
			return
		}
		response := service.SummaryStatus{
			Durable: statusview.Build(advanced.Genesis, advanced.Head, advanced.Depth, advanced.Projection),
			Cursor:  service.Cursor{Frontier: []service.Frontier{{Genesis: advanced.Genesis, Head: advanced.Head, Depth: advanced.Depth}}},
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer moving.Close()
	if _, err := fetchSummary(ctx, workspace, moving.URL); err == nil || !strings.Contains(err.Error(), "moved") {
		t.Fatalf("moving-head error = %v", err)
	}
	current := testGit(t, workspace.Repo, "rev-parse", kernel.Ref(workspace.View().Genesis))
	if current == summary.Durable.Head {
		t.Fatal("moving-head fixture did not move the durable ref")
	}
}

func TestSlowLocalAuditReportsProgressWithoutChangingTheResult(t *testing.T) {
	t.Parallel()
	want := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 7}
	var progress bytes.Buffer
	got, err := loadSnapshotWithProgress(context.Background(), &progress, func(context.Context) (app.Snapshot, error) {
		time.Sleep(1100 * time.Millisecond)
		return want, nil
	})
	if err != nil || got.Genesis != want.Genesis || got.Head != want.Head || got.Depth != want.Depth {
		t.Fatalf("slow load = %+v, %v", got, err)
	}
	if !strings.Contains(progress.String(), "verifying the durable log") {
		t.Fatalf("slow audit was silent: %q", progress.String())
	}
	progress.Reset()
	if _, err := loadSnapshotWithProgress(context.Background(), &progress, func(context.Context) (app.Snapshot, error) {
		return want, nil
	}); err != nil || progress.Len() != 0 {
		t.Fatalf("fast audit reported progress: %q, %v", progress.String(), err)
	}
}

func TestCheckpointClearRemovesBothPersistentSelectors(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", repo)
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(workspace.MetaDir, "checkpoints", workspace.View().Genesis+".json")
	if _, err := os.Stat(pointer); err != nil {
		t.Fatalf("checkpoint pointer was not created: %v", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	commandErr := checkpointClearCommand(ctx, []string{"--repo", repo})
	os.Stdout = stdout
	writer.Close()
	printed, readErr := io.ReadAll(reader)
	reader.Close()
	if commandErr != nil || readErr != nil {
		t.Fatalf("checkpoint-clear: command=%v read=%v", commandErr, readErr)
	}
	if _, err := os.Stat(pointer); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint pointer remains: %v", err)
	}
	if head := testGit(t, repo, "rev-parse", kernel.CheckpointRef(workspace.View().Genesis)); head != workspace.View().Genesis {
		t.Fatalf("checkpoint ref = %s, want genesis %s", head, workspace.View().Genesis)
	}
	var result map[string]string
	if err := json.Unmarshal(printed, &result); err != nil || result["checkpoint"] != "cleared" || result["genesis"] != workspace.View().Genesis {
		t.Fatalf("checkpoint-clear output = %q, result=%#v err=%v", printed, result, err)
	}
	fresh, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fresh.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Source != app.SnapshotSourceColdFullAudit {
		t.Fatalf("post-clear source = %q, want cold full audit", loaded.Source)
	}
}

func TestAttachAdvancesButRejectsRemoteRewind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	auditor := filepath.Join(root, "auditor")

	testGit(t, "", "init", source)
	workspace, _, err := app.Init(ctx, source, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/seq/" + workspace.View().Genesis
	first := testGit(t, source, "rev-parse", ref)
	testGit(t, "", "init", "--bare", remote)
	testGit(t, source, "remote", "add", "origin", remote)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")

	testGit(t, "", "clone", remote, auditor)
	testGit(t, auditor, "config", "--add", "remote.origin.fetch", forcedSequenceFetchRefspec)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != first {
		t.Fatalf("initial sequence head = %s, want %s", got, first)
	}
	attached, err := app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.View().VerifiedFrontier == nil || attached.View().VerifiedFrontier.Head != first {
		t.Fatalf("initial verified frontier = %+v, want head %s", attached.View().VerifiedFrontier, first)
	}
	fetchRules := strings.Fields(testGit(t, auditor, "config", "--get-all", "remote.origin.fetch"))
	if contains(fetchRules, forcedSequenceFetchRefspec) || !contains(fetchRules, sequenceFetchRefspec) {
		t.Fatalf("sequence fetch rules = %#v, want only non-forcing sequence rule", fetchRules)
	}

	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance",
		RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: "advance",
	}); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, source, "rev-parse", ref)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err != nil {
		t.Fatalf("forward attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("forward sequence head = %s, want %s", got, second)
	}
	attached, err = app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.View().VerifiedFrontier == nil || attached.View().VerifiedFrontier.Head != second {
		t.Fatalf("forward verified frontier = %+v, want head %s", attached.View().VerifiedFrontier, second)
	}

	testGit(t, "", "--git-dir", remote, "update-ref", ref, first, second)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err == nil {
		t.Fatal("attach to rewound remote sequence succeeded")
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("rewind changed local sequence head to %s, want %s", got, second)
	}
	if output, err := exec.Command("git", "-C", auditor, "fetch", "origin").CombinedOutput(); err == nil {
		t.Fatalf("ordinary fetch accepted rewound sequence: %s", output)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("ordinary fetch rewound local sequence head to %s, want %s", got, second)
	}
}

func TestAttachRejectsHostileRemoteBeforeConfigOrTransport(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:file")

	for _, test := range []struct {
		name   string
		remote func(*testing.T, string) string
	}{
		{name: "option", remote: func(*testing.T, string) string { return "--no-recurse-submodules" }},
		{name: "ext transport", remote: func(t *testing.T, marker string) string {
			helper := marker + "-helper"
			if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > \""+marker+"\"\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			return "ext::" + helper
		}},
		{name: "file transport", remote: func(*testing.T, string) string { return "file:///tmp/untrusted.git" }},
		{name: "relative path", remote: func(*testing.T, string) string { return filepath.Join("..", "untrusted.git") }},
		{name: "unknown name", remote: func(*testing.T, string) string { return "unconfigured" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := t.TempDir()
			testGit(t, "", "init", repo)
			configPath := filepath.Join(repo, ".git", "config")
			marker := filepath.Join(t.TempDir(), "remote-helper-ran")
			remote := test.remote(t, marker)
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := attachCommand(ctx, []string{"--repo", repo, "--remote", remote, "--genesis", strings.Repeat("0", 40)}); err == nil || !strings.Contains(err.Error(), "configured Git remote") {
				t.Errorf("attach --remote %q = %v, want configured-remote refusal", remote, err)
			}
			after, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("attach --remote %q changed repository config", remote)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("attach --remote %q reached transport helper: %v", remote, err)
			}
		})
	}
}

func TestAttachRejectsSpentIdempotencyReplayAfterLocalFrontierLoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	remote := filepath.Join(root, "remote.git")
	auditor := filepath.Join(root, "auditor")
	freshAuditor := filepath.Join(root, "fresh-auditor")

	testGit(t, "", "init", source)
	workspace, seed, err := app.Init(ctx, source, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := "refs/seq/" + workspace.View().Genesis
	base := testGit(t, source, "rev-parse", ref)
	_, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := workspace.BuildActRequest(ctx, private, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "different event after truncation",
		RestsOn: []string{seed.ID}, IdempotencyKey: "spent-before-truncation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "accepted event",
		RestsOn: []string{seed.ID}, IdempotencyKey: "spent-before-truncation",
	}); err != nil {
		t.Fatal(err)
	}
	trusted := testGit(t, source, "rev-parse", ref)

	testGit(t, "", "init", "--bare", remote)
	testGit(t, source, "remote", "add", "origin", remote)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")
	testGit(t, "", "clone", remote, auditor)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}

	if err := workspace.Store.UpdateRef(ctx, ref, base, trusted); err != nil {
		t.Fatal(err)
	}
	attack, err := kernel.Submit(ctx, workspace.Store, replayed, kernel.Options{SigningKey: workspace.View().SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, source, "push", "origin", "+"+ref+":"+ref)
	// A first-time auditor has no earlier frontier to compare. The truncated
	// branch is internally signed and must remain attachable; detecting that it
	// omitted prior history needs a witness or trusted checkpoint.
	testGit(t, "", "clone", remote, freshAuditor)
	if err := attachCommand(ctx, []string{"--repo", freshAuditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err != nil {
		t.Fatalf("fresh auditor rejected internally valid truncated branch: %v", err)
	}
	fresh, err := app.Open(ctx, freshAuditor)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.View().VerifiedFrontier == nil || fresh.View().VerifiedFrontier.Head != attack.Head {
		t.Fatalf("fresh auditor frontier = %+v, want attack head %s", fresh.View().VerifiedFrontier, attack.Head)
	}
	// Losing the tracking ref defeats Git's non-force comparison, but it must
	// not erase the separately persisted verified frontier.
	testGit(t, auditor, "update-ref", "-d", ref)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.View().Genesis}); err == nil || !strings.Contains(err.Error(), "non-descendant verified frontier") {
		t.Fatalf("attach accepted replay branch %s after losing its ref: %v", attack.Head, err)
	}
	attached, err := app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.View().VerifiedFrontier == nil || attached.View().VerifiedFrontier.Head != trusted {
		t.Fatalf("rejected replay replaced trusted frontier: %+v, want %s", attached.View().VerifiedFrontier, trusted)
	}
	if verification, err := kernel.Verify(ctx, attached.Store, workspace.View().Genesis); err != nil || verification.Head != attack.Head {
		t.Fatalf("attack branch was not independently valid, verification=%+v err=%v", verification, err)
	}
}

func TestReviewGuardAcceptsExactCleanArtifactHead(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	approval := fixture.review(t)
	after := fixture.snapshot(t)
	if after.Depth != before+1 {
		t.Fatalf("review depth = %d, want %d", after.Depth, before+1)
	}
	statement := statementByEvent(t, after.Projection, approval)
	if statement.Body["head"] != fixture.candidate || statement.Body["artifact"] != fixture.artifact || statement.Body["verdict"] != "approved" {
		t.Fatalf("review body = %#v", statement.Body)
	}
	if !contains(after.Projection.Provenance[approval], fixture.promise) || !contains(after.Projection.Provenance[approval], fixture.request) || !contains(after.Projection.Provenance[approval], fixture.artifact) {
		t.Fatalf("review provenance = %#v", after.Projection.Provenance[approval])
	}
}

// A commit does not stop being the commit it is because something upstream was
// superseded, and whether that movement matters to this head is the reviewer's
// question to answer. The gate must let it be asked.
func TestReviewGuardReviewsAMerelyStaleArtifactAndSaysSoInTheVerdict(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	fixture.moveTheWorld(t)
	approval := fixture.review(t)
	statement := statementByEvent(t, fixture.snapshot(t).Projection, approval)
	if statement.Body["head"] != fixture.candidate || statement.Body["verdict"] != "approved" {
		t.Fatalf("verdict over a moved world = %#v", statement.Body)
	}
	if statement.Body["stale"] != "true" {
		t.Fatalf("verdict did not record that the world had moved: %#v", statement.Body)
	}
	note := statement.Body["staleness"]
	for _, want := range []string{"artifact", "promise", "request", "describes a superseded world", fixture.ground} {
		if !strings.Contains(note, want) {
			t.Fatalf("staleness note %q does not name %q", note, want)
		}
	}
}

// Retirement is the other fact, and it still refuses: a withdrawn pointer
// names nothing left to review.
func TestReviewGuardRefusesARetiredArtifact(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: fixture.artifact, Text: "that head is withdrawn",
		IdempotencyKey: "retire-artifact",
	}); err != nil {
		t.Fatal(err)
	}
	if artifact := artifactByEvent(t, fixture.snapshot(t).Projection, fixture.artifact); !artifact.Retired {
		t.Fatal("the supersession was ineffective, so this case is untested")
	}
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "artifact is retired") {
		t.Fatalf("retired artifact review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("retired artifact signed a verdict: depth %d -> %d", before, after)
	}
}

// A reviewer who has withdrawn the promise is no longer undertaking to review,
// whatever the artifact says.
func TestReviewGuardRefusesARetiredPromise(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbSupersede, Target: fixture.promise, Text: "I cannot review this after all",
		IdempotencyKey: "retire-promise",
	}); err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "statement is retired") {
		t.Fatalf("retired promise review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("retired promise signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesDirtyCheckoutBeforeVerdict(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.feature, "feature.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "checkout is dirty") {
		t.Fatalf("dirty review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("dirty review signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesAdvancedCheckoutBeforeVerdict(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.feature, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.feature, "add", "later.txt")
	testGit(t, fixture.feature, "commit", "-m", "advance feature")
	before := fixture.snapshot(t).Depth
	err := fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), "does not equal artifact head") {
		t.Fatalf("advanced review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("advanced review signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesAnotherActorsPromise(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "other-reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	foreignRequest, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review assigned to someone else",
		Body: map[string]string{
			"to": fixture.workspace.View().Actors["other-reviewer"].Fingerprint, "conditions": "exact head",
		},
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "foreign-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	foreignPromise, err := fixture.workspace.Act(fixture.ctx, "other-reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "another reviewer's promise",
		RestsOn: []string{foreignRequest.Record.ID}, IdempotencyKey: "foreign-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err = reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", foreignPromise.Record.ID,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "review actor did not make the named promise") {
		t.Fatalf("foreign promise review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("foreign promise signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesCheckoutFromAnotherRepository(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	foreign := filepath.Join(t.TempDir(), "foreign")
	testGit(t, "", "clone", fixture.repo, foreign)
	before := fixture.snapshot(t).Depth
	err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", foreign,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "checkout does not belong to the workroom repository") {
		t.Fatalf("foreign checkout review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("foreign checkout signed a verdict: depth %d -> %d", before, after)
	}
}

func TestReviewGuardRefusesBasisChangeBeforeSigning(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	calls := 0
	read := func() (reviewguard.Basis, []reviewguard.News, workroom.Projection, error) {
		basis, news, projection, err := reviewRead(fixture.ctx, fixture.workspace, "reviewer", fixture.feature, fixture.artifact, fixture.promise)()
		calls++
		if calls == 2 && err == nil {
			basis.Request += "-changed"
		}
		return basis, news, projection, err
	}
	before := fixture.snapshot(t).Depth
	err := reviewCommandWithValidator(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	}, read)
	if err == nil || !strings.Contains(err.Error(), "review basis changed while validating") {
		t.Fatalf("changed review basis error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("review validation calls = %d, want 2", calls)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("changed review basis signed a verdict: depth %d -> %d", before, after)
	}
}

// A statement sequenced after the review request that names the reviewed head
// is head news: the verdict refuses until the reviewer has acknowledged it.
func TestReviewGuardRefusesUnacknowledgedHeadNews(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	news, err := fixture.fileHeadNews(t, "the implementer pushed again")
	if err != nil {
		t.Fatal(err)
	}
	err = fixture.reviewError()
	if err == nil || !strings.Contains(err.Error(), news) || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("unacknowledged head news error = %v, want a refusal naming %s", err, news)
	}
	if after := fixture.snapshot(t).Depth; after != before+1 {
		t.Fatalf("head news refusal changed depth %d -> %d", before, after)
	}
}

// Acknowledging exactly the news lets the verdict through, and the verdict
// then rests on what it acknowledged and records the canonical set.
func TestReviewGuardAcceptsExactHeadNewsAcknowledgments(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	news, err := fixture.fileHeadNews(t, "a stranger noticed this head")
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "APPROVED with news seen",
		"--ack-head-news", news,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want news plus verdict", snapshot.Depth)
	}
	statement := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if !contains(snapshot.Projection.Provenance[statement.Event], news) {
		t.Fatalf("verdict provenance %v does not rest on acknowledged news %s", snapshot.Projection.Provenance[statement.Event], news)
	}
	want, _ := json.Marshal([]string{fixture.promise, news})
	// The promise matches through its direct basis and is cited by the verdict;
	// the acknowledged array records every matched event in sequence order.
	if statement.Body["head_news_acknowledged"] != string(want) {
		t.Fatalf("head_news_acknowledged = %q, want %s", statement.Body["head_news_acknowledged"], want)
	}
	if statement.Body["review_frontier"] == "" {
		t.Fatal("verdict recorded no review frontier")
	}
	if statement.Body["review_path"] != "reviewguard@1" {
		t.Fatalf("review_path = %q", statement.Body["review_path"])
	}
}

func TestReviewGuardRejectsWrongAcknowledgmentSets(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	first, err := fixture.fileHeadNews(t, "first piece of news")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.fileHeadNews(t, "second piece of news")
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	for _, test := range []struct {
		name string
		acks []string
		want string
	}{
		{name: "missing", acks: []string{first}, want: "missing"},
		{name: "extraneous", acks: []string{first, second, fixture.artifact}, want: "extraneous"},
		{name: "duplicate", acks: []string{first, first, second}, want: "twice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			arguments := []string{
				"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
				"--artifact", fixture.artifact, "--promise", fixture.promise,
				"--verdict", "approved", "--text", "must not be signed",
			}
			for _, ack := range test.acks {
				arguments = append(arguments, "--ack-head-news", ack)
			}
			err := reviewCommand(fixture.ctx, arguments)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s acknowledgment error = %v", test.name, err)
			}
			if after := fixture.snapshot(t).Depth; after != before {
				t.Fatalf("wrong acknowledgment set changed depth %d -> %d", before, after)
			}
		})
	}
}

// News arriving between confirmation and sequencing reruns preflight instead
// of chaining the verdict onto a world it never saw. The confirming read sees
// what landed meanwhile, so the command refuses before anything is signed.
func TestReviewGuardExposesLateArrivingNewsAtSequencingTime(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	calls := 0
	read := func() (reviewguard.Basis, []reviewguard.News, workroom.Projection, error) {
		basis, news, projection, err := reviewRead(fixture.ctx, fixture.workspace, "reviewer", fixture.feature, fixture.artifact, fixture.promise)()
		calls++
		if calls == 2 && err == nil {
			if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "late news",
				Body:    map[string]string{"head": basis.Head},
				RestsOn: []string{fixture.artifact}, IdempotencyKey: "late-arrival",
			}); err != nil {
				return basis, news, projection, err
			}
		}
		return basis, news, projection, err
	}
	err := reviewCommandWithValidator(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	}, read)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("late arrival error = %v, want a basis-changed refusal", err)
	}
	if after := fixture.snapshot(t).Depth; after != before+1 {
		t.Fatalf("late arrival changed depth %d -> %d, want only the news itself", before, after)
	}
}

func TestMergeGuardMergesOnlyRatifiedApprovedExactHead(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the approved feature and make it available on main.",
	}); err != nil {
		t.Fatal(err)
	}
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if output, err := exec.Command("git", "-C", fixture.repo, "merge-base", "--is-ancestor", fixture.candidate, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("approved candidate was not merged: %v: %s", err, output)
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("merge head is not a receipt commit: ok=%v err=%v", ok, err)
	}
	if receipt.Approval != approval || receipt.Candidate != fixture.candidate || receipt.TargetPreHead != targetPreHead || receipt.MergeHead != mergeHead {
		t.Fatalf("merge receipt = %+v", receipt)
	}
	if receipt.LeftLive != "{}" {
		t.Fatalf("merge receipt left-live accounting = %q, want an explicit empty map", receipt.LeftLive)
	}
	if receipt.ChangedPaths != `["feature.txt"]` {
		t.Fatalf("merge receipt changed paths = %q, want feature.txt", receipt.ChangedPaths)
	}
	if got := testGit(t, fixture.repo, "rev-parse", mergeReceiptRef(approval)); got != mergeHead {
		t.Fatalf("receipt ref = %s, want %s", got, mergeHead)
	}
	var durable workroom.Statement
	for _, statement := range fixture.snapshot(t).Projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	if durable.Event == "" || durable.Body["merge_candidate"] != fixture.candidate ||
		durable.Body["merge_target_pre_head"] != targetPreHead || durable.Body["merge_head"] != mergeHead ||
		durable.Body["merge_left_live"] != receipt.LeftLive || durable.Body["merge_changed_paths"] != receipt.ChangedPaths {
		t.Fatalf("durable merge receipt = %+v", durable)
	}
	projection := fixture.snapshot(t).Projection
	if predecessor := artifactByEvent(t, projection, fixture.artifact); !predecessor.Retired {
		t.Fatal("merge left the covered predecessor live")
	}
	live := 0
	for _, artifact := range projection.Artifacts {
		if artifact.Path == "feature.txt" && !artifact.Retired {
			live++
			if artifact.Commit != mergeHead {
				t.Fatalf("live successor commit = %s, want merge head %s", artifact.Commit, mergeHead)
			}
		}
	}
	if live != 1 {
		t.Fatalf("live artifacts at feature.txt = %d, want exactly one", live)
	}
}

func TestMergePhaseOneWarnsWhenAuthorizationIsAbsent(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = writer
	mergeErr := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge under the explicit phase-one compatibility path.",
	})
	os.Stderr = stderr
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	warning, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if mergeErr != nil {
		t.Fatal(mergeErr)
	}
	const want = "warning: merge has no structured authorization; phase-one compatibility permits this merge"
	if !strings.Contains(string(warning), want) {
		t.Fatalf("phase-one stderr = %q, want %q", warning, want)
	}
}

func TestMergeGuardRecordsStructuredAuthorization(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Merge the specifically authorized feature and make it available on main.",
	}); err != nil {
		t.Fatal(err)
	}
	head := testGit(t, fixture.repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
	if err != nil || !ok {
		t.Fatalf("read authorized receipt: ok=%v err=%v", ok, err)
	}
	if receipt.Authorization != authorization {
		t.Fatalf("Git receipt authorization = %q, want %q", receipt.Authorization, authorization)
	}
	authorizationStatement := statementByEvent(t, fixture.snapshot(t).Projection, authorization)
	if receipt.AuthorizationRatification != authorizationStatement.RatifiedBy {
		t.Fatalf("Git receipt authorization ratification = %q, want %q", receipt.AuthorizationRatification, authorizationStatement.RatifiedBy)
	}
	var durable workroom.Statement
	for _, statement := range fixture.snapshot(t).Projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	if durable.Body["merge_authorization"] != authorization {
		t.Fatalf("durable receipt authorization = %q, want %q", durable.Body["merge_authorization"], authorization)
	}
	if durable.Body["merge_authorization_ratification"] != authorizationStatement.RatifiedBy {
		t.Fatalf("durable receipt authorization ratification = %q, want %q", durable.Body["merge_authorization_ratification"], authorizationStatement.RatifiedBy)
	}
	if !slices.Contains(fixture.snapshot(t).Projection.Provenance[durable.Event], authorization) {
		t.Fatal("durable receipt does not rest on its merge authorization")
	}
	if !slices.Contains(fixture.snapshot(t).Projection.Provenance[durable.Event], authorizationStatement.RatifiedBy) {
		t.Fatal("durable receipt does not rest on its sealed authorization ratification")
	}
}

func TestMergeAuthorizationRefusesOrdinaryParticipantsSelfAuthorizing(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "intruder", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "accomplice", "agent"); err != nil {
		t.Fatal(err)
	}
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorizeAs(t, approval, "intruder", "accomplice", true, nil)
	beforeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	before := fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Ordinary participants must not authorize this merge for themselves.",
	})
	if err == nil || !strings.Contains(err.Error(), "is not the implementation requester and is not a live actor named planner or carrying ratifier") {
		t.Fatalf("ordinary participant authorization error = %v", err)
	}
	if afterHead := testGit(t, fixture.repo, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("refused authorization moved HEAD from %s to %s", beforeHead, afterHead)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused authorization changed durable log from %s/%d to %s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestMergeAuthorizationAcceptsLivePlanner(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "planner", "agent"); err != nil {
		t.Fatal(err)
	}
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorizeAs(t, approval, "operator", "planner", true, nil)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Merge the exact head authorized by the live Planner actor.",
	}); err != nil {
		t.Fatalf("Planner authorization refused: %v", err)
	}
}

func TestMergeAuthorizationAcceptsLiveRatifier(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "governor", "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.workspace.GrantRole(fixture.ctx, "operator", "governor", "ratifier"); err != nil {
		t.Fatal(err)
	}
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorizeAs(t, approval, "operator", "governor", true, nil)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Merge the exact head authorized by a live ratifier.",
	}); err != nil {
		t.Fatalf("ratifier authorization refused: %v", err)
	}
}

func TestMergeAuthorizationRefusesEveryWrongBinding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{name: "candidate missing", mutate: func(body map[string]string) { delete(body, "authorizes_candidate") }, want: "authorizes_candidate"},
		{name: "candidate wrong", mutate: func(body map[string]string) { body["authorizes_candidate"] = strings.Repeat("a", 40) }, want: "authorizes_candidate"},
		{name: "approval missing", mutate: func(body map[string]string) { delete(body, "authorizes_approval") }, want: "authorizes_approval"},
		{name: "approval wrong", mutate: func(body map[string]string) { body["authorizes_approval"] = "wrong" }, want: "authorizes_approval"},
		{name: "request missing", mutate: func(body map[string]string) { delete(body, "authorizes_request") }, want: "authorizes_request"},
		{name: "request wrong", mutate: func(body map[string]string) { body["authorizes_request"] = "wrong" }, want: "authorizes_request"},
		{name: "target missing", mutate: func(body map[string]string) { delete(body, "target_pre_head") }, want: "target_pre_head is missing"},
		{name: "remeasure wrong", mutate: func(body map[string]string) { body["remeasure"] = "trust-me" }, want: "want disjoint-paths"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			authorization := fixture.authorize(t, approval, true, test.mutate)
			err := mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
				"--text", "This malformed authorization must not move the target.",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("authorization error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMergeAuthorizationRequiresRatificationAndPreReceiptOrdering(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, false, nil)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "An unratified authorization must not move the target.",
	})
	if err == nil || !strings.Contains(err.Error(), "not ratified") {
		t.Fatalf("unratified authorization error = %v", err)
	}
	if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbRatify, Target: authorization, IdempotencyKey: "late-authorization-ratification",
	}); err != nil {
		t.Fatal(err)
	}
	projection := fixture.snapshot(t).Projection
	ratified := statementByEvent(t, projection, authorization)
	ratificationSequence := eventSequence(projection, ratified.RatifiedBy)
	_, err = validateMergeAuthorization(fixture.ctx, projection, fixture.repo, fixture.candidate, approval, authorization,
		testGit(t, fixture.repo, "rev-parse", "HEAD"), true, ratificationSequence, "")
	if err == nil || !strings.Contains(err.Error(), "is not before merge receipt") {
		t.Fatalf("late ratification ordering error = %v", err)
	}
}

func TestMergeAuthorizationRequiresExactlyOneAuthorizationLane(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		lanes int
	}{{"zero", 0}, {"double", 2}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			authorization := fixture.authorize(t, approval, true, nil)
			snapshot := fixture.snapshot(t)
			kept := snapshot.Projection.Commitments[:0]
			for _, lane := range snapshot.Projection.Commitments {
				if lane.Report != authorization {
					kept = append(kept, lane)
				}
			}
			if test.lanes == 2 {
				kept = append(kept, workroom.Commitment{Report: authorization}, workroom.Commitment{Report: authorization})
			}
			snapshot.Projection.Commitments = kept
			_, err := validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, fixture.candidate, approval, authorization, testGit(t, fixture.repo, "rev-parse", "HEAD"), true, snapshot.Depth+1, "")
			if err == nil || !strings.Contains(err.Error(), "authorization report belongs to") {
				t.Fatalf("%s authorization lanes error = %v", test.name, err)
			}
		})
	}
}

func TestMergeAuthorizationRefusesIneffectiveRatification(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	snapshot := fixture.snapshot(t)
	r := statementByEvent(t, snapshot.Projection, authorization).RatifiedBy
	for index := range snapshot.Projection.Decisions {
		if snapshot.Projection.Decisions[index].Event == r {
			snapshot.Projection.Decisions[index].Verdict = "ineffective"
		}
	}
	_, err := validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, fixture.candidate, approval, authorization, testGit(t, fixture.repo, "rev-parse", "HEAD"), true, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "ratification is not an effective sequenced event") {
		t.Fatalf("ineffective ratification error = %v", err)
	}
}

func TestMergeAuthorizationRequiredModeRefusesAbsence(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	_, err := validateMergeAuthorization(fixture.ctx, fixture.snapshot(t).Projection, fixture.repo, fixture.candidate, approval, "",
		testGit(t, fixture.repo, "rev-parse", "HEAD"), true, fixture.snapshot(t).Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "--authorization is required") {
		t.Fatalf("required authorization error = %v", err)
	}
}

func TestMergeAuthorizationRefusesBlockingStaleness(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	snapshot := fixture.snapshot(t)
	for index := range snapshot.Projection.Statements {
		if snapshot.Projection.Statements[index].Event == authorization {
			snapshot.Projection.Statements[index].DescribesSupersededWorld = true
			snapshot.Projection.Statements[index].WorldSupersededAt = snapshot.Projection.Statements[index].Sequence
		}
	}
	_, err := validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, fixture.candidate, approval, authorization,
		testGit(t, fixture.repo, "rev-parse", "HEAD"), true, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "describes a superseded world") {
		t.Fatalf("world-stale authorization error = %v", err)
	}
	for index := range snapshot.Projection.Statements {
		if snapshot.Projection.Statements[index].Event == authorization {
			snapshot.Projection.Statements[index].DescribesSupersededWorld = false
			snapshot.Projection.Statements[index].Retired = true
		}
	}
	_, err = validateMergeAuthorization(fixture.ctx, snapshot.Projection, fixture.repo, fixture.candidate, approval, authorization,
		testGit(t, fixture.repo, "rev-parse", "HEAD"), true, snapshot.Depth+1, "")
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("retired authorization error = %v", err)
	}
}

func TestMergeAuthorizationRemeasuresDisjointTargetMovement(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, func(body map[string]string) {
		body["remeasure"] = "disjoint-paths"
	})
	if err := os.WriteFile(filepath.Join(fixture.repo, "main-only.txt"), []byte("main moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "main-only.txt")
	testGit(t, fixture.repo, "commit", "-m", "move main on a disjoint path")
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Merge the authorized feature after a disjoint main change.",
	}); err != nil {
		t.Fatalf("disjoint remeasurement refused: %v", err)
	}
}

func TestMergeAuthorizationRefusesOverlappingTargetMovement(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, func(body map[string]string) {
		body["remeasure"] = "disjoint-paths"
	})
	if err := os.WriteFile(filepath.Join(fixture.repo, "feature.txt"), []byte("main changed the same path\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "feature.txt")
	testGit(t, fixture.repo, "commit", "-m", "move main on the candidate path")
	beforeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	before := fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Do not merge when target movement overlaps the candidate.",
	})
	if err == nil || !strings.Contains(err.Error(), `disjoint-paths remeasurement failed: "feature.txt" changed in both candidate and target`) {
		t.Fatalf("overlapping remeasurement error = %v", err)
	}
	if afterHead := testGit(t, fixture.repo, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("overlap refusal moved HEAD from %s to %s", beforeHead, afterHead)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("overlap refusal changed durable log from %s/%d to %s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestMergeAuthorizationRefusesNonAncestorRemeasure(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, func(body map[string]string) { body["remeasure"] = "disjoint-paths" })
	// The unrelated history has to arrive on the target branch itself. A
	// checkout left on another branch is refused for that reason alone under
	// the landing obligation, which would never reach the remeasurement.
	testGit(t, fixture.repo, "checkout", "--orphan", "unrelated")
	testGit(t, fixture.repo, "commit", "-m", "unrelated root")
	if err := os.WriteFile(filepath.Join(fixture.repo, "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "unrelated.txt")
	testGit(t, fixture.repo, "commit", "-m", "unrelated target")
	unrelated := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "checkout", "-f", "main")
	testGit(t, fixture.repo, "reset", "--hard", unrelated)
	beforeHead, before := testGit(t, fixture.repo, "rev-parse", "HEAD"), fixture.snapshot(t)
	err := mergeCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo, "--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization, "--text", "Do not remeasure across unrelated history."})
	if err == nil || !strings.Contains(err.Error(), "is not an ancestor of current target") {
		t.Fatalf("non-ancestor remeasurement error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("refusal moved HEAD from %s to %s", beforeHead, got)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refusal changed durable log")
	}
}

func TestResumeRefusesSealedUnratifiedAuthorizationBeforeDurableSuffix(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, false, nil)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	snapshot := fixture.snapshot(t)
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	plan := sharedPlan
	message, err := mergeReceiptMessage("Seal a receipt whose authorization was never ratified.", approval,
		authorization, approval, fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, plan)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "merge", "--no-ff", "--no-commit", "--", fixture.candidate)
	testGit(t, fixture.repo, "commit", "-m", message)
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	before := fixture.snapshot(t)
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Do not resume a receipt without pre-Git authorization force.",
	})
	if err == nil || !strings.Contains(err.Error(), "sealed receipt authorization: report is not ratified by its requester") {
		t.Fatalf("sealed unratified authorization error = %v", err)
	}
	if afterHead := testGit(t, fixture.repo, "rev-parse", "HEAD"); afterHead != mergeHead {
		t.Fatalf("resume refusal moved HEAD from sealed merge %s to %s", mergeHead, afterHead)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("resume refusal appended durable suffix: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestResumeRefusesAuthorizationWithoutRatificationWitness(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	authorizationStatement := statementByEvent(t, fixture.snapshot(t).Projection, authorization)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	snapshot := fixture.snapshot(t)
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	plan := sharedPlan
	message, err := mergeReceiptMessage("Seal an incomplete authorization receipt.", approval, authorization,
		authorizationStatement.RatifiedBy, fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, plan)
	if err != nil {
		t.Fatal(err)
	}
	message = strings.Replace(message, "\n"+mergeplan.AuthorizationRatificationTrailer+authorizationStatement.RatifiedBy, "", 1)
	testGit(t, fixture.repo, "merge", "--no-ff", "--no-commit", "--", fixture.candidate)
	testGit(t, fixture.repo, "commit", "-m", message)
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	before := fixture.snapshot(t)
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Do not resume authorization without its temporal witness.",
	})
	if err == nil || !strings.Contains(err.Error(), "must carry Gitseq-Authorization and Gitseq-Authorization-Ratification together") {
		t.Fatalf("missing authorization witness error = %v", err)
	}
	if afterHead := testGit(t, fixture.repo, "rev-parse", "HEAD"); afterHead != mergeHead {
		t.Fatalf("missing-witness refusal moved HEAD from %s to %s", mergeHead, afterHead)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("missing-witness refusal appended durable suffix: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestResumeRefusesSealedAuthorizationRatificationMismatch(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	sealed := statementByEvent(t, fixture.snapshot(t).Projection, authorization).RatifiedBy
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	snapshot := fixture.snapshot(t)
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	plan := sharedPlan
	message, err := mergeReceiptMessage("Seal the first authorization witness.", approval, authorization, sealed, fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, plan)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "merge", "--no-ff", "--no-commit", "--", fixture.candidate)
	testGit(t, fixture.repo, "commit", "-m", message)
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if _, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{Verb: app.VerbRatify, Target: authorization, IdempotencyKey: "reratify-sealed-authorization"}); err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t)
	err = mergeCommand(fixture.ctx, []string{"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo, "--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization, "--text", "Do not resume with a different authorization witness."})
	if err == nil || !strings.Contains(err.Error(), "want sealed authorization ratification") {
		t.Fatalf("sealed witness mismatch error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("refusal moved sealed HEAD from %s to %s", mergeHead, got)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refusal appended durable suffix")
	}
}

func TestMergeAuthorizationRefusesMovedTargetWithoutRemeasure(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	authorization := fixture.authorize(t, approval, true, nil)
	if err := os.WriteFile(filepath.Join(fixture.repo, "main-only.txt"), []byte("main moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "main-only.txt")
	testGit(t, fixture.repo, "commit", "-m", "move main after authorization")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "A pinned authorization must not float onto a newer target.",
	})
	if err == nil || !strings.Contains(err.Error(), "remeasure is not disjoint-paths") {
		t.Fatalf("moved target error = %v", err)
	}
}

func TestLegacyReceiptCannotBeRetrospectivelyAuthorized(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land a phase-one legacy receipt without structured authorization.",
	}); err != nil {
		t.Fatal(err)
	}
	authorization := fixture.authorize(t, approval, true, nil)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval, "--authorization", authorization,
		"--text", "Do not rewrite the ordering of the earlier merge.",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot retroactively order") {
		t.Fatalf("retrospective authorization error = %v", err)
	}
}

func TestMergeRefusesUnrecordableReceiptBeforeMovingHead(t *testing.T) {
	t.Parallel()
	const ceiling = 8 << 10
	root := t.TempDir()
	template := &workflowTemplate{}
	if err := template.buildWorkflow(root, false, workflowNoCitation, ceiling, 180); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	workspace, err := app.Open(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", feature, "feature")
	fixture := workflowFixture{
		t: t, ctx: context.Background(), repo: repo, feature: feature, workspace: workspace,
		candidate: template.candidate, artifact: template.artifact, ground: template.ground,
		request: template.request, promise: template.promise,
	}
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	beforeHead := testGit(t, repo, "rev-parse", "HEAD")
	before := fixture.snapshot(t)
	changes, err := mergeChangesBetween(fixture.ctx, repo, beforeHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	sharedPlan := mergeplan.PlanSuccession(before.Projection, changes,
		mustClassify(t, fixture.ctx, repo, before.Projection, changes, beforeHead, fixture.candidate, nil))
	plan := sharedPlan
	changedPaths, err := json.Marshal(plan.ChangedPaths)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangedPaths) != 181 || len(plan.Publish) != 181 || len(plan.Retire) != 1 || len(plan.LeftLive) != 1 {
		t.Fatalf("oversized frontier plan = changed %d publish %d retire %d left-live %d", len(plan.ChangedPaths), len(plan.Publish), len(plan.Retire), len(plan.LeftLive))
	}
	var extraArtifact string
	for _, artifact := range before.Projection.Artifacts {
		if artifact.Path == "extra" {
			extraArtifact = artifact.Event
			break
		}
	}
	if extraArtifact == "" || !maps.Equal(plan.LeftLive,
		map[string]mergeLeftLive{extraArtifact: {Class: mergeplan.LeftLiveCarried}}) {
		t.Fatalf("oversized frontier left-live accounting = %#v, want carried target tree", plan.LeftLive)
	}
	if len(changedPaths) <= ceiling {
		t.Fatalf("changed-path seal is %d bytes, want more than %d-byte ceiling", len(changedPaths), ceiling)
	}
	err = mergeCommand(fixture.ctx, []string{
		"--repo", repo, "--as", "operator", "--checkout", repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Attempt a merge whose complete receipt cannot be recorded.",
	})
	if err == nil || !strings.Contains(err.Error(), "event exceeds genesis ceiling") {
		t.Fatalf("oversized receipt error = %v", err)
	}
	if afterHead := testGit(t, repo, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("oversized receipt moved HEAD: before=%s after=%s", beforeHead, afterHead)
	}
	after := fixture.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("oversized receipt changed durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
	if status := testGit(t, repo, "status", "--porcelain"); status != "" {
		t.Fatalf("oversized receipt left checkout dirty: %q", status)
	}
	if _, err := gitCommand(repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("oversized receipt left approval reservation behind")
	}
}

func TestSuccessionAdmissionPreflightsEveryActWithoutAppending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 2<<10)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	largePath := strings.Repeat("p", 1400)
	head := testGit(t, repo, "rev-parse", "HEAD")
	acts := []batchAct{
		{Label: "merge", Verb: app.VerbState, Kind: workroom.KindAssert, Text: "small merge receipt", RestsOn: []string{seed.ID}, IdempotencyKey: "preflight-receipt"},
		{Label: "successor", Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "Merge published the current artifact at " + largePath, Body: map[string]string{"path": largePath, "commit": head}, RestsOn: []string{"$merge"}, IdempotencyKey: "preflight-successor"},
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := preflightBatchAdmission(ctx, workspace, "", "operator", private, acts[:1], true); err != nil {
		t.Fatalf("receipt act alone should fit: %v", err)
	}
	err = preflightBatchAdmission(ctx, workspace, "", "operator", private, acts, true)
	if err == nil || !strings.Contains(err.Error(), "act 1:") || !strings.Contains(err.Error(), "event exceeds genesis ceiling") {
		t.Fatalf("later oversized act error = %v", err)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("batch preflight changed durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestSuccessionAdmissionAppliesResidentRequestCapWithoutAppending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	acts := []batchAct{{
		Label: "merge", Verb: app.VerbState, Kind: workroom.KindAssert, Text: "transport-bound receipt",
		Body:    map[string]string{"merge_changed_paths": strings.Repeat("x", int(service.SubmissionRequestLimit*3/4))},
		RestsOn: []string{seed.ID}, IdempotencyKey: "resident-cap-receipt",
	}}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := preflightBatchAdmission(ctx, workspace, "", "operator", private, acts, true); err != nil {
		t.Fatalf("kernel ceiling should admit request: %v", err)
	}
	err = preflightBatchAdmission(ctx, workspace, "http://127.0.0.1:7777", "operator", private, acts, true)
	if err == nil || !strings.Contains(err.Error(), "resident submission request exceeds") {
		t.Fatalf("resident-cap preflight error = %v", err)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("resident-cap preflight changed durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestMergeReceiptLeftLiveRoundTripAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	t.Run("new receipt", func(t *testing.T) {
		fixture := newWorkflowFixture(t)
		targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
		plan := successionPlan{
			Publish:      []string{"feature.txt"},
			Retire:       map[string]string{"predecessor": "feature.txt"},
			ChangedPaths: []string{"feature.txt"},
			LeftLive: map[string]mergeLeftLive{
				"z-artifact": {Class: mergeplan.LeftLiveAbandoned},
				"a-artifact": {Class: mergeplan.LeftLiveSibling, Commitment: "promise"},
			},
		}
		message, err := mergeReceiptMessage("Merge with complete accounting.", "approval", "", "", fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, plan)
		if err != nil {
			t.Fatal(err)
		}
		wantLeftLive := `{"a-artifact":{"class":"sibling","commitment":"promise"},"z-artifact":{"class":"abandoned"}}`
		if !strings.Contains(message, mergeplan.LeftLiveTrailer+wantLeftLive) {
			t.Fatalf("merge message did not carry deterministic left-live JSON:\n%s", message)
		}
		testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
		head := testGit(t, fixture.repo, "rev-parse", "HEAD")
		receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
		if err != nil || !ok {
			t.Fatalf("read new receipt: ok=%v err=%v", ok, err)
		}
		if receipt.LeftLive != wantLeftLive {
			t.Fatalf("left-live receipt = %q, want %q", receipt.LeftLive, wantLeftLive)
		}
		if !receipt.LeftLivePresent || !receipt.ChangedPathsPresent || receipt.ChangedPaths != `["feature.txt"]` {
			t.Fatalf("prospective receipt fields = %+v", receipt)
		}
	})

	t.Run("old receipt", func(t *testing.T) {
		fixture := newWorkflowFixture(t)
		targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
		message := fmt.Sprintf("Historical merge.\n\n%s%s\n%s%s\n%s%s\n%s{}\n%s[]",
			mergeplan.ApprovalTrailer, "old-approval", mergeplan.CandidateTrailer, fixture.candidate,
			mergeplan.TargetTrailer, targetPreHead, mergeplan.RetirementsTrailer, mergeplan.SuccessorsTrailer)
		testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
		head := testGit(t, fixture.repo, "rev-parse", "HEAD")
		receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, head)
		if err != nil || !ok {
			t.Fatalf("read historical receipt: ok=%v err=%v", ok, err)
		}
		if receipt.LeftLive != "" {
			t.Fatalf("historical receipt invented left-live accounting %q", receipt.LeftLive)
		}
		if receipt.LeftLivePresent || receipt.ChangedPathsPresent || receipt.ChangedPaths != "" {
			t.Fatalf("historical receipt became prospective: %+v", receipt)
		}
	})
}

func TestMergeRetryRejectsMalformedOrForgedProspectiveAccounting(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"null left-live": {
			mutate: func(message string) string {
				return strings.Replace(message, mergeplan.LeftLiveTrailer+"{}", mergeplan.LeftLiveTrailer+"null", 1)
			},
			want: "expected a JSON object, got null",
		},
		"empty left-live": {
			mutate: func(message string) string {
				return strings.Replace(message, mergeplan.LeftLiveTrailer+"{}", mergeplan.LeftLiveTrailer, 1)
			},
			want: "unexpected end of JSON input",
		},
		"left-live only": {
			mutate: func(message string) string {
				return strings.Replace(message, "\n"+mergeplan.ChangedPathsTrailer+`["feature.txt"]`, "", 1)
			},
			want: "must carry Gitseq-Left-Live and Gitseq-Changed-Paths together",
		},
		"changed-paths only": {
			mutate: func(message string) string {
				return strings.Replace(message, "\n"+mergeplan.LeftLiveTrailer+"{}", "", 1)
			},
			want: "must carry Gitseq-Left-Live and Gitseq-Changed-Paths together",
		},
		"noncanonical changed paths": {
			mutate: func(message string) string {
				return strings.Replace(message, mergeplan.ChangedPathsTrailer+`["feature.txt"]`, mergeplan.ChangedPathsTrailer+`["feature.txt","feature.txt"]`, 1)
			},
			want: "paths must be sorted and unique",
		},
		"forged changed paths": {
			mutate: func(message string) string {
				return strings.Replace(message, mergeplan.ChangedPathsTrailer+`["feature.txt"]`, mergeplan.ChangedPathsTrailer+`["other.txt"]`, 1)
			},
			want: "do not equal merge first-parent diff paths",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newWorkflowFixture(t)
			approval := fixture.review(t)
			fixture.ratify(t, approval)
			targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
			changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := fixture.snapshot(t)
			sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
				mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
			plan := sharedPlan
			message, err := mergeReceiptMessage("Merge a deliberately malformed receipt.", approval, "", "", fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, plan)
			if err != nil {
				t.Fatal(err)
			}
			message = test.mutate(message)
			testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
			mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
			testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")

			err = mergeCommand(fixture.ctx, []string{
				"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
				"--candidate", fixture.candidate, "--approval", approval,
				"--text", "Retry the malformed receipt.",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("retry error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMergeGuardIgnoresReplacementForApprovedCandidate(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)

	replacementCheckout := filepath.Join(filepath.Dir(fixture.repo), "replacement-candidate")
	testGit(t, fixture.repo, "worktree", "add", "-b", "replacement-candidate", replacementCheckout, fixture.candidate+"^")
	if err := os.WriteFile(filepath.Join(replacementCheckout, "feature.txt"), []byte("replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, replacementCheckout, "add", "feature.txt")
	testGit(t, replacementCheckout, "commit", "-m", "unreviewed replacement content")
	replacement := testGit(t, replacementCheckout, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "replace", fixture.candidate, replacement)
	if got := testGit(t, fixture.repo, "show", fixture.candidate+":feature.txt"); got != "replacement" {
		t.Fatalf("replacement fixture is inactive: show = %q", got)
	}
	if got := testGit(t, fixture.repo, "--no-replace-objects", "show", fixture.candidate+":feature.txt"); got != "feature" {
		t.Fatalf("approved object content = %q, want feature", got)
	}

	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the approved object while ignoring repository-local replacement refs.",
	}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(fixture.repo, "feature.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "feature\n" {
		t.Fatalf("merge landed replacement content %q instead of the approved object", content)
	}
}

func TestMergeRefusesANonUTF8DiffPathBeforeCreatingTheMergeCommit(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	invalid := string([]byte{'b', 'a', 'd', 0xff})
	blob := testGit(t, fixture.repo, "rev-parse", fixture.candidate+":feature.txt")
	sawRawPath := false
	previous := readStagedMergeChanges
	readStagedMergeChanges = func(ctx context.Context, checkout string) ([]mergeChange, error) {
		// APFS refuses a non-UTF-8 pathname at open(2), so put the raw byte name
		// directly into Git's real index. The production reader below must return
		// that exact byte from `git diff --cached -z`; no synthetic mergeChange is
		// substituted for the Git boundary this test exists to prove.
		if _, err := git(ctx, checkout, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+invalid); err != nil {
			return nil, err
		}
		changes, err := stagedMergeChanges(ctx, checkout)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			if change.Old == invalid || change.New == invalid {
				sawRawPath = true
			}
		}
		return changes, nil
	}
	t.Cleanup(func() { readStagedMergeChanges = previous })

	beforeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	beforeDepth := fixture.snapshot(t).Depth
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Attempt to merge a path the receipt cannot encode exactly.",
	})
	if err == nil || !strings.Contains(err.Error(), "merge diff path is not valid UTF-8") {
		t.Fatalf("merge error = %v, want non-UTF-8 path refusal", err)
	}
	if !sawRawPath {
		t.Fatal("Git's NUL-delimited staged diff did not return the raw 0xff filename")
	}
	if afterHead := testGit(t, fixture.repo, "rev-parse", "HEAD"); afterHead != beforeHead {
		t.Fatalf("refused merge moved HEAD from %s to %s", beforeHead, afterHead)
	}
	if afterDepth := fixture.snapshot(t).Depth; afterDepth != beforeDepth {
		t.Fatalf("refused merge recorded durable acts: depth %d -> %d", beforeDepth, afterDepth)
	}
	if status := testGit(t, fixture.repo, "status", "--porcelain"); status != "" {
		t.Fatalf("refused merge left the checkout dirty: %q", status)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused merge left its receipt reservation behind")
	}
}

func TestMergeRefusesWhenTargetMovesAfterReadOnlyPlanning(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	beforeDepth := fixture.snapshot(t).Depth
	previous := buildMergePlan
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		result := previous(ctx, workspace, checkout, candidate, approval, merger, signer)
		if result.Allowed {
			testGit(t, checkout, "commit", "--allow-empty", "-m", "move target after planning")
		}
		return result
	}
	t.Cleanup(func() { buildMergePlan = previous })

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a plan whose target moved before reservation.",
	})
	if err == nil || !strings.Contains(err.Error(), "merge target moved after planning") {
		t.Fatalf("moving-target merge error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != beforeDepth {
		t.Fatalf("moving-target refusal changed workroom depth %d -> %d", beforeDepth, after)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("moving-target refusal left a receipt reservation")
	}
}

func TestMergeRefusesWhenStagedPathsDifferFromReadOnlyPlan(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	beforeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	beforeDepth := fixture.snapshot(t).Depth
	previous := readStagedMergeChanges
	readStagedMergeChanges = func(ctx context.Context, checkout string) ([]mergeChange, error) {
		changes, err := previous(ctx, checkout)
		return append(changes, mergeChange{Status: "A", New: "unexpected.txt"}), err
	}
	t.Cleanup(func() { readStagedMergeChanges = previous })

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse staged paths that differ from the read-only plan.",
	})
	if err == nil || !strings.Contains(err.Error(), "do not equal the read-only merge plan") {
		t.Fatalf("changed-path coherence error = %v", err)
	}
	if after := testGit(t, fixture.repo, "rev-parse", "HEAD"); after != beforeHead {
		t.Fatalf("changed-path refusal moved HEAD %s -> %s", beforeHead, after)
	}
	if after := fixture.snapshot(t).Depth; after != beforeDepth {
		t.Fatalf("changed-path refusal changed workroom depth %d -> %d", beforeDepth, after)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("changed-path refusal left a receipt reservation")
	}
}

func TestMergeRefusesWhenWorkroomFrontierMovesAfterPlanning(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	beforeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	previous := buildMergePlan
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		result := previous(ctx, workspace, checkout, candidate, approval, merger, signer)
		if result.Allowed {
			if _, err := workspace.Act(ctx, "operator", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "durable event after planning",
				RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: "move-frontier-after-planning",
			}); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	t.Cleanup(func() { buildMergePlan = previous })

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Refuse a plan whose verified workroom frontier moved.",
	})
	if err == nil || !strings.Contains(err.Error(), "workroom frontier moved after planning") {
		t.Fatalf("moving-frontier merge error = %v", err)
	}
	if after := testGit(t, fixture.repo, "rev-parse", "HEAD"); after != beforeHead {
		t.Fatalf("moving-frontier refusal moved HEAD %s -> %s", beforeHead, after)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("moving-frontier refusal left a receipt reservation")
	}
}

func TestMergeLeavesAnUnrelatedCandidateArtifactLive(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	otherCheckout := filepath.Join(filepath.Dir(fixture.repo), "other-feature")
	testGit(t, fixture.repo, "worktree", "add", "-b", "other-feature", otherCheckout)
	if err := os.WriteFile(filepath.Join(otherCheckout, "feature.txt"), []byte("unrelated candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, otherCheckout, "add", "feature.txt")
	testGit(t, otherCheckout, "commit", "-m", "unrelated candidate at the same artifact path")
	otherHead := testGit(t, otherCheckout, "rev-parse", "HEAD")
	unrelated, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "unrelated candidate still under review",
		Body:    map[string]string{"path": "feature.txt", "commit": otherHead},
		RestsOn: []string{fixture.workspace.EventID(fixture.workspace.View().Genesis)}, IdempotencyKey: "unrelated-candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge one candidate without withdrawing another proposed world.",
	}); err != nil {
		t.Fatal(err)
	}
	projection := fixture.snapshot(t).Projection
	if artifactByEvent(t, projection, unrelated.Record.ID).Retired {
		t.Fatal("merge retired an unrelated unmerged candidate artifact")
	}
	if !artifactByEvent(t, projection, fixture.artifact).Retired {
		t.Fatal("merge did not retire the exact reviewed candidate artifact")
	}
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("read receipt with abandoned candidate: ok=%v err=%v", ok, err)
	}
	want, err := json.Marshal(map[string]mergeLeftLive{unrelated.Record.ID: {Class: mergeplan.LeftLiveAbandoned}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.LeftLive != string(want) {
		t.Fatalf("left-live accounting = %s, want %s", receipt.LeftLive, want)
	}
	var durable workroom.Statement
	for _, statement := range projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	if durable.Body["merge_left_live"] != string(want) {
		t.Fatalf("durable left-live accounting = %q, want %s", durable.Body["merge_left_live"], want)
	}
}

// The deadlock this repository actually hit. Documentation names the exact
// artifacts a merge must retire — that is what `rests_on` is for — so refusing
// every cited retirement refused every documented-area merge, and the advice to
// repoint the pages first was unsatisfiable because the successor does not
// exist until the merge lands. A retirement this same merge succeeds must
// therefore go through, and the pages that named it flare rather than break.
func TestMergeLandsWhenTheCitedPredecessorGetsASuccessor(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	writeCitingPage(t, fixture.repo, "docs/reference/feature.md", fixture.artifact)
	testGit(t, fixture.repo, "commit", "-m", "cite the live feature artifact")
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")

	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land the approved feature while a page still cites its predecessor.",
	}); err != nil {
		t.Fatalf("cited predecessor with a successor was refused: %v", err)
	}
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if mergeHead == before {
		t.Fatal("merge reported success without moving the target")
	}
	projection := fixture.snapshot(t).Projection
	if !artifactByEvent(t, projection, fixture.artifact).Retired {
		t.Fatal("merge left the cited predecessor live")
	}
	// The citation is preserved because the retirement says where the behaviour
	// went: a successor at the same path, named by the supersession itself.
	successor := ""
	for _, artifact := range projection.Artifacts {
		if artifact.Path == "feature.txt" && artifact.Commit == mergeHead && !artifact.Retired {
			successor = artifact.Event
		}
	}
	if successor == "" {
		t.Fatal("merge retired the cited predecessor without publishing a successor at its path")
	}
	retirement := ""
	for _, act := range projection.Acts {
		if act.Type == "supersede" && act.Target == fixture.artifact && act.Verdict == workroom.Effective {
			retirement = act.Event
		}
	}
	if retirement == "" {
		t.Fatal("no effective supersession retired the cited predecessor")
	}
	if !slices.Contains(projection.Provenance[retirement], successor) {
		t.Fatalf("retirement %s does not name the successor %s, so a page citing the predecessor has nowhere to go",
			retirement, successor)
	}
}

// A ratified approval is public, so anyone can read one and call `gs merge`
// with it. The fold refuses the receipt that comes out — but it only ever sees
// the receipt, which is written after Git has committed. So the wrong signer
// moved the target, spent the single-use approval, and stranded the succession,
// and every check that reported the error had already let it happen. The
// signer is now part of what is validated before the merge begins.
func TestMergeRefusesASignerWhoDidNotDoTheApprovedWork(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	beforeDepth := fixture.snapshot(t).Depth

	// The reviewer is a bare participant: independent enough to approve this
	// head, and with no part in making it.
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "A participant who did not do the work must not consume this approval.",
	})
	if err == nil || !strings.Contains(err.Error(), "approved work is landing") {
		t.Fatalf("merge signed by a non-implementer error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("refused merge moved the target to %s, want %s", got, before)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused merge left a receipt reservation, so the approval is spent")
	}
	if after := fixture.snapshot(t); after.Depth != beforeDepth {
		t.Fatalf("refused merge appended %d durable record(s)", after.Depth-beforeDepth)
	}
	// The approval is untouched, so the actor whose work it is can still land it.
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land the approved feature and make it available on main.",
	}); err != nil {
		t.Fatalf("the implementer could not merge after the refusal: %v", err)
	}
}

// The refusal this repair narrows, proved end to end on the property that
// matters: a plan reaching outside what the approval reviewed is refused with
// the target, the receipt reservation and the durable log all where they were.
// The checkpoint case reached this refusal for three of the four trees its head
// actually changed; what must never change is that reaching it costs nothing.
func TestMergeUnreachableRetirementLeavesEverythingUnchanged(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	// A pointer belonging to another actor, at a path this head never reviewed.
	stranger, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "another actor's pointer elsewhere",
		Body:    map[string]string{"path": "elsewhere.txt", "commit": testGit(t, fixture.repo, "rev-parse", "HEAD")},
		RestsOn: []string{fixture.workspace.EventID(fixture.workspace.View().Genesis)}, IdempotencyKey: "stranger-elsewhere",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	beforeDepth := fixture.snapshot(t).Depth
	snapshot := fixture.snapshot(t)
	plan := successionPlan{Publish: []string{"elsewhere.txt"},
		Retire: map[string]string{stranger.Record.ID: "elsewhere.txt"}}
	if err := mergeplan.ValidateReach(snapshot.Projection, plan, approval,
		fixture.workspace.View().Actors["operator"].Fingerprint); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("unreachable retirement error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("refused plan moved the target to %s, want %s", got, before)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused plan left a receipt reservation")
	}
	if after := fixture.snapshot(t); after.Depth != beforeDepth {
		t.Fatalf("refused plan appended %d durable record(s)", after.Depth-beforeDepth)
	}
	// And the approval is unspent, so the reachable part still lands.
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land the approved feature and make it available on main.",
	}); err != nil {
		t.Fatalf("the reviewed part of the merge was refused: %v", err)
	}
	if artifactByEvent(t, fixture.snapshot(t).Projection, stranger.Record.ID).Retired {
		t.Fatal("the merge retired a pointer outside the reviewed paths")
	}
}

// The rule itself, across the cases a single fixture cannot reach. One
// fingerprint admits a merge: the author of the approved artifact. A role does
// not, however senior — standing is live and can be revoked between this check
// and the acts it would authorize, and the fold judges those acts after `HEAD`
// has moved, so a role here is authority that may not survive the merge it
// allowed. The author of a record that already happened cannot be revoked.
func TestMergeAuthoritySignerIsExactlyTheApprovedImplementer(t *testing.T) {
	t.Parallel()
	projection := workroom.Projection{
		Reviews: []workroom.Review{{Report: "approval", Implementer: "implementer",
			Independence: workroom.IndependenceIndependent}},
		Actors: map[string]workroom.ActorState{
			"implementer": {Name: "implementer", Roles: []string{"participant"}},
			"stranger":    {Name: "stranger", Roles: []string{"participant"}},
			"keeper":      {Name: "keeper", Roles: []string{"participant", "ratifier"}},
		},
	}
	if err := requireApprovedImplementer(projection, "approval", "implementer"); err != nil {
		t.Errorf("merge signed by the approved implementer was refused: %v", err)
	}
	for _, merger := range []string{"stranger", "keeper"} {
		if err := requireApprovedImplementer(projection, "approval", merger); err == nil ||
			!strings.Contains(err.Error(), "approved work is landing") {
			t.Errorf("merge signed by %s error = %v, want a refusal", merger, err)
		}
	}
	if err := requireApprovedImplementer(projection, "approval", ""); err == nil {
		t.Error("merge with no signing fingerprint was allowed")
	}
	if err := requireApprovedImplementer(projection, "unresolved", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "cannot say who implemented") {
		t.Errorf("merge on an approval with no projected review error = %v", err)
	}
}

// The other half of the same rule, and the reason it is not a bypass. A
// deletion retires a pointer and puts nothing in its place, so a page naming it
// really is left pointing at a hole. That refusal still runs before `HEAD`
// moves and still leaves no reservation behind.
func TestMergeBareRetirementOfACitedPredecessorLeavesTargetUnchanged(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixtureWithCitation(t, workflowCandidateAddsCitation)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	page := "docs/reference/base.md"
	if _, err := os.Stat(filepath.Join(fixture.repo, page)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target citation exists before planning: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.feature, page)); err != nil {
		t.Fatalf("candidate citation is absent: %v", err)
	}
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	actor, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	prospective := mergeplan.Build(fixture.ctx, fixture.workspace, fixture.repo, fixture.candidate, approval,
		actor.Fingerprint, mergeplan.Signer{Name: "operator", Private: private})
	if prospective.Allowed || len(prospective.Reasons) == 0 ||
		prospective.Reasons[len(prospective.Reasons)-1].Code != "succession_refused" ||
		!strings.Contains(prospective.Reasons[len(prospective.Reasons)-1].Reason, page) {
		t.Fatalf("read-only cited-retirement plan = %+v, want refusal naming the staged page", prospective)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("read-only cited-retirement plan reserved the approval")
	}

	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "This merge must be refused before it changes the target.",
	})
	if err == nil || !strings.Contains(err.Error(), page) {
		t.Fatalf("cited bare retirement merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("refused merge moved target to %s, want %s", got, before)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused merge left a receipt reservation")
	}
}

func TestMergePlanAllowsCandidateThatDeletesTheSoleCitation(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixtureWithCitation(t, workflowCandidateDeletesCitation)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	page := "docs/reference/base.md"
	if _, err := os.Stat(filepath.Join(fixture.repo, page)); err != nil {
		t.Fatalf("target citation is absent: %v", err)
	}
	if _, err := git(fixture.ctx, fixture.feature, "cat-file", "-e", fixture.candidate+":"+page); err == nil {
		t.Fatal("candidate kept the sole citing page")
	}
	actor, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	prospective := mergeplan.Build(fixture.ctx, fixture.workspace, fixture.repo, fixture.candidate, approval,
		actor.Fingerprint, mergeplan.Signer{Name: "operator", Private: private})
	if !prospective.Allowed {
		t.Fatalf("read-only plan refused after the candidate deleted the sole citation: %+v", prospective)
	}
	if !contains(prospective.ChangedPaths, page) {
		t.Fatalf("changed paths %v do not include deleted citation %s", prospective.ChangedPaths, page)
	}
}

// The preflight replayed on its own, the way the review that found the deadlock
// replayed it: the real citing checkout, and the three plan shapes that decide
// the outcome.
func TestMergePreflightSeparatesSucceededRetirementFromOrphaning(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	writeCitingPage(t, fixture.repo, "docs/reference/feature.md", fixture.artifact)
	testGit(t, fixture.repo, "commit", "-m", "cite the live feature artifact")

	succeeded := successionPlan{Publish: []string{"feature.txt"},
		Retire: map[string]string{fixture.artifact: "feature.txt"}}
	if err := mergeplan.ValidateSuccession(fixture.ctx, fixture.workspace, fixture.repo, succeeded); err != nil {
		t.Fatalf("succeeded retirement of a cited predecessor was refused: %v", err)
	}
	wider := successionPlan{Publish: []string{"docs"},
		Retire: map[string]string{fixture.artifact: "docs"}}
	if err := mergeplan.ValidateSuccession(fixture.ctx, fixture.workspace, fixture.repo, wider); err != nil {
		t.Fatalf("succession to a covering directory was refused: %v", err)
	}
	bare := successionPlan{Retire: map[string]string{fixture.artifact: ""}}
	if err := mergeplan.ValidateSuccession(fixture.ctx, fixture.workspace, fixture.repo, bare); err == nil ||
		!strings.Contains(err.Error(), "docs/reference/feature.md") {
		t.Fatalf("bare retirement of a cited predecessor error = %v", err)
	}
	unpublished := successionPlan{Publish: []string{"docs"},
		Retire: map[string]string{fixture.artifact: "feature.txt"}}
	if err := mergeplan.ValidateSuccession(fixture.ctx, fixture.workspace, fixture.repo, unpublished); err == nil ||
		!strings.Contains(err.Error(), "does not publish") {
		t.Fatalf("successor this merge never publishes error = %v", err)
	}
}

func TestMergeRetryResumesPartlyLandedSuccessionWithoutRemerging(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	message, err := mergeReceiptMessage("Merge the approved feature.", approval, "", "", fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, sharedPlan)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")

	changes, err = mergeChanges(fixture.ctx, fixture.repo, mergeHead)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = fixture.snapshot(t)
	sharedPlan = mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	plan := sharedPlan
	acts := successionActs(approval, "", "", fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), mergeHead, "", false, plan)
	if len(acts) < 3 {
		t.Fatalf("succession acts = %d, want receipt, successor, and retirement", len(acts))
	}
	_, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	partial, err := runBatch(fixture.ctx, fixture.workspace, "", "operator", private, acts[:2], false)
	if err != nil || partial.Landed != 2 {
		t.Fatalf("partial succession = %+v, %v", partial, err)
	}
	beforeRetry := fixture.snapshot(t).Depth
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume the already-landed merge succession.",
	}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("retry re-merged or moved HEAD to %s, want %s", got, mergeHead)
	}
	after := fixture.snapshot(t)
	if after.Depth != beforeRetry+1 {
		t.Fatalf("retry depth = %d, want %d: only the missing retirement should land", after.Depth, beforeRetry+1)
	}
	if !artifactByEvent(t, after.Projection, fixture.artifact).Retired {
		t.Fatal("retry did not finish predecessor retirement")
	}
}

// A merge commit is the first irreversible boundary. If the process stops
// before its durable receipt lands, retry must recover the exact succession
// plan sealed into that commit rather than planning again from a later
// projection. Otherwise an artifact filed after the merge could be retired by
// a plan nobody reviewed before HEAD moved.
func TestMergeRetryBeforeDurableReceiptUsesTheSealedGitPlan(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	outside := testGit(t, fixture.repo, "commit-tree", testGit(t, fixture.repo, "rev-parse", "HEAD^{tree}"), "-m", "unlanded sibling")
	leftBehind, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "unlanded candidate present when the merge was sealed",
		Body:    map[string]string{"path": "feature.txt", "commit": outside},
		RestsOn: []string{fixture.workspace.EventID(fixture.workspace.View().Genesis)}, IdempotencyKey: "pre-merge-left-live-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, fixture.ctx, fixture.repo, snapshot.Projection, changes, targetPreHead, fixture.candidate, nil))
	sealed := sharedPlan
	message, err := mergeReceiptMessage("Merge the approved feature.", approval, "", "", fixture.candidate, mergeTestTarget(fixture.workspace, targetPreHead), "", false, sealed)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "merge", "--no-ff", "-m", message, fixture.candidate)
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")

	// This statement did not exist when the merge plan was checked and sealed.
	// Replanning now would incorrectly add it to the retirement set.
	later, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "artifact filed after the Git merge",
		Body:    map[string]string{"path": "feature.txt", "commit": mergeHead},
		RestsOn: []string{fixture.artifact}, IdempotencyKey: "post-merge-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Resume after Git committed but before the durable receipt.",
	}); err != nil {
		t.Fatal(err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("retry re-merged or moved HEAD to %s, want %s", got, mergeHead)
	}
	snapshot = fixture.snapshot(t)
	if !artifactByEvent(t, snapshot.Projection, fixture.artifact).Retired {
		t.Fatal("pre-merge predecessor from the sealed plan remains live")
	}
	if artifactByEvent(t, snapshot.Projection, later.Record.ID).Retired {
		t.Fatal("post-merge artifact was retroactively added to the sealed retirement plan")
	}
	if artifactByEvent(t, snapshot.Projection, leftBehind.Record.ID).Retired {
		t.Fatal("sealed left-live candidate was retired during retry")
	}
	wantLeftLive, err := json.Marshal(map[string]mergeLeftLive{leftBehind.Record.ID: {Class: mergeplan.LeftLiveAbandoned}})
	if err != nil {
		t.Fatal(err)
	}
	var durable workroom.Statement
	for _, statement := range snapshot.Projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	if durable.Body["merge_left_live"] != string(wantLeftLive) {
		t.Fatalf("retried durable receipt left-live accounting = %q, want %s", durable.Body["merge_left_live"], wantLeftLive)
	}
	if durable.Body["merge_changed_paths"] != `["feature.txt"]` {
		t.Fatalf("retried durable receipt changed paths = %q, want feature.txt", durable.Body["merge_changed_paths"])
	}
	if !artifactByEvent(t, snapshot.Projection, later.Record.ID).Stale {
		t.Fatal("post-merge descendant did not flare when its predecessor retired")
	}
}

// buildNestedCrossAuthorApproval creates the shape the directional reach rule
// decides. The approved head adds `docs/how-to/x.md` beside the reviewed
// `feature.txt`, and another actor holds a pointer at bare `docs`, so planning
// the merge retires a cross-author pointer above the reviewed path: reach that
// the fold's symmetric lineage authorizes and the command's prospective
// direction refuses.
func buildNestedCrossAuthorApproval(t *testing.T) (workflowFixture, string, string, string, string) {
	t.Helper()
	f := newWorkflowFixture(t)
	if err := os.MkdirAll(filepath.Join(f.feature, "docs", "how-to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.feature, "docs", "how-to", "x.md"), []byte("nested page\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.feature, "add", ".")
	testGit(t, f.feature, "commit", "-m", "add a nested page")
	candidate := testGit(t, f.feature, "rev-parse", "HEAD")
	stranger, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "another actor's covering pointer",
		Body:    map[string]string{"path": "docs", "commit": testGit(t, f.repo, "rev-parse", "HEAD")},
		RestsOn: []string{f.workspace.EventID(f.workspace.View().Genesis)}, IdempotencyKey: "stranger-covering-docs",
	})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "nested implementation under docs",
		Body:    map[string]string{"path": "docs/how-to/x.md", "commit": candidate},
		RestsOn: []string{f.ground}, IdempotencyKey: "nested-reviewed-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review the nested head",
		Body:    map[string]string{"to": f.workspace.View().Actors["reviewer"].Fingerprint, "conditions": "exact head"},
		RestsOn: []string{nested.Record.ID}, IdempotencyKey: "nested-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "I will review the exact nested head",
		RestsOn: []string{request.Record.ID}, IdempotencyKey: "nested-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", nested.Record.ID, "--promise", promise.Record.ID,
		"--verdict", "approved", "--text", "APPROVED exact head",
	}); err != nil {
		t.Fatal(err)
	}
	statements := f.snapshot(t).Projection.Statements
	approval := statements[len(statements)-1].Event
	f.ratify(t, approval)
	return f, candidate, approval, stranger.Record.ID, nested.Record.ID
}

func buildRemovedNestedCrossAuthorApproval(t *testing.T) (workflowFixture, string, string) {
	t.Helper()
	f := newWorkflowFixture(t)
	removedPath := filepath.Join("docs", "how-to", "x.md")
	if err := os.MkdirAll(filepath.Join(f.repo, "docs", "how-to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, removedPath), []byte("remove me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "add", removedPath)
	testGit(t, f.repo, "commit", "-m", "add the page that the candidate removes")
	targetPreHead := testGit(t, f.repo, "rev-parse", "HEAD")
	testGit(t, f.feature, "merge", "--no-edit", "main")
	if err := os.Remove(filepath.Join(f.feature, removedPath)); err != nil {
		t.Fatal(err)
	}
	testGit(t, f.feature, "add", "-u", "--", removedPath)
	testGit(t, f.feature, "commit", "-m", "remove the nested page")
	candidate := testGit(t, f.feature, "rev-parse", "HEAD")

	if _, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "another actor's covering pointer",
		Body:    map[string]string{"path": "docs", "commit": targetPreHead},
		RestsOn: []string{f.workspace.EventID(f.workspace.View().Genesis)}, IdempotencyKey: "stranger-covering-removed-docs",
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "implementation removes the nested page",
		Body:    map[string]string{"path": removedPath, "commit": candidate},
		RestsOn: []string{f.ground}, IdempotencyKey: "removed-nested-reviewed-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review the nested deletion",
		Body:    map[string]string{"to": f.workspace.View().Actors["reviewer"].Fingerprint, "conditions": "exact head"},
		RestsOn: []string{removed.Record.ID}, IdempotencyKey: "removed-nested-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "I will review the exact nested deletion head",
		RestsOn: []string{request.Record.ID}, IdempotencyKey: "removed-nested-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", removed.Record.ID, "--promise", promise.Record.ID,
		"--verdict", "approved", "--text", "APPROVED exact deletion head",
	}); err != nil {
		t.Fatal(err)
	}
	statements := f.snapshot(t).Projection.Statements
	approval := statements[len(statements)-1].Event
	f.ratify(t, approval)
	return f, candidate, approval
}

// The regression this repair fixes, end to end in the direction the reviewer
// filed it. A receipt sealed while reach read both directions — here, one
// whose plan naturally retires another actor's pointer at bare `docs` above
// the reviewed `docs/how-to/x.md` — must resume by appending its immutable
// succession suffix. Re-applying today's prospective guard to that historical
// plan would strand it before the durable suffix completes; replanning or
// re-merging would reinterpret what was sealed instead of resuming it.
func TestMergeResumeAppendsASealedSymmetricReceiptWithoutReplanningOrRemerging(t *testing.T) {
	t.Parallel()
	f, candidate, approval, _, nested := buildNestedCrossAuthorApproval(t)
	targetPreHead := testGit(t, f.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(f.ctx, f.repo, targetPreHead, candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	sharedPlan := mergeplan.PlanSuccession(snapshot.Projection, changes,
		mustClassify(t, f.ctx, f.repo, snapshot.Projection, changes, targetPreHead, candidate, nil))
	sealed := sharedPlan
	// The exact shape the receipt owes: one successor at each published path
	// and a supersession only for the exact-path predecessor. The wider pointer
	// above the reviewed leaf is carried rather than retired.
	if !slices.Equal(sealed.Publish, []string{"docs/how-to/x.md", "feature.txt"}) {
		t.Fatalf("sealed publish paths = %v, want [docs/how-to/x.md feature.txt]", sealed.Publish)
	}
	wantRetire := map[string]string{nested: "docs/how-to/x.md"}
	if !maps.Equal(sealed.Retire, wantRetire) {
		t.Fatalf("sealed retirements = %v, want %v", sealed.Retire, wantRetire)
	}
	message, err := mergeReceiptMessage("Merge the approved nested guide.", approval, "", "", candidate, mergeTestTarget(f.workspace, targetPreHead), "", false, sealed)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "merge", "--no-ff", "-m", message, candidate)
	mergeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	testGit(t, f.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")

	// What was sealed really does sit outside today's prospective reach, so
	// only the fold's unchanged authority can carry it.
	if err := mergeplan.ValidateReach(snapshot.Projection, sealed, approval,
		f.workspace.View().Actors["operator"].Fingerprint); err != nil {
		t.Fatalf("sealed exact-path plan against the current guard: %v", err)
	}

	before := f.snapshot(t).Depth
	if err := mergeCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", candidate, "--approval", approval,
		"--text", "Resume the sealed receipt without replanning or re-merging.",
	}); err != nil {
		t.Fatalf("resuming a receipt the unchanged fold authorized was refused: %v", err)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("resume re-merged or moved HEAD to %s, want %s", got, mergeHead)
	}
	after := f.snapshot(t)
	// One receipt assertion plus one durable act per publish and per
	// retirement in the sealed suffix.
	wantDepth := before + 1 + len(sealed.Publish) + len(sealed.Retire)
	if after.Depth != wantDepth {
		t.Fatalf("resume depth = %d, want %d: the sealed receipt plus its publish and retirement acts", after.Depth, wantDepth)
	}
	for target := range sealed.Retire {
		if !artifactByEvent(t, after.Projection, target).Retired {
			t.Fatalf("resume did not append the sealed retirement of %s", target)
		}
	}
	for _, path := range sealed.Publish {
		live := 0
		for _, artifact := range after.Projection.Artifacts {
			if artifact.Path == path && artifact.Commit == mergeHead && !artifact.Retired {
				live++
			}
		}
		if live != 1 {
			t.Fatalf("resume left %d live successors at %s, want one", live, path)
		}
	}
}

// A wider pointer covering only a landed destination stays live and is sealed
// in the receipt. It is not a retirement, so the directional guard has no
// reason to refuse the merge.
func TestMergeFreshPreflightAllowsAWiderPointerAtALandedDestination(t *testing.T) {
	t.Parallel()
	f, candidate, approval, wider, _ := buildNestedCrossAuthorApproval(t)
	beforeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	before := f.snapshot(t)

	err := mergeCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", candidate, "--approval", approval,
		"--text", "Merge the reviewed leaf without retiring its wider directory.",
	})
	if err != nil {
		t.Fatalf("fresh exact-path merge: %v", err)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got == beforeHead {
		t.Fatal("exact-path merge did not move the target")
	}
	if _, err := git(f.ctx, f.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err != nil {
		t.Fatalf("merged exact-path receipt was not sealed: %v", err)
	}
	mergeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	receipt, ok, err := readMergeReceipt(f.ctx, f.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("read carried receipt: ok=%v err=%v", ok, err)
	}
	var gitLeftLive map[string]mergeLeftLive
	if err := json.Unmarshal([]byte(receipt.LeftLive), &gitLeftLive); err != nil {
		t.Fatal(err)
	}
	if got := gitLeftLive[wider]; got.Class != mergeplan.LeftLiveCarried || got.Commitment != "" {
		t.Fatalf("Git receipt carried accounting for %s = %+v", wider, got)
	}
	after := f.snapshot(t)
	if after.Depth <= before.Depth {
		t.Fatalf("merged exact-path receipt did not extend durable log: depth %d/%d", after.Depth, before.Depth)
	}
	var durable workroom.Statement
	for _, statement := range after.Projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	var durableLeftLive map[string]mergeLeftLive
	if err := json.Unmarshal([]byte(durable.Body["merge_left_live"]), &durableLeftLive); err != nil {
		t.Fatal(err)
	}
	if got := durableLeftLive[wider]; got.Class != mergeplan.LeftLiveCarried || got.Commitment != "" {
		t.Fatalf("durable receipt carried accounting for %s = %+v", wider, got)
	}
}

func TestMergePlanLeavesStructuredAuthorizationToMerge(t *testing.T) {
	err := mergePlanCommand(context.Background(), []string{"--authorization", "report"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("merge-plan authorization flag error = %v", err)
	}
}

func TestMergePlanAllowedResultLeavesGitAndWorkroomAccelerationStateUnchanged(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	before := captureMergePlanReadOnlyState(t, fixture.workspace)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	commandErr := mergePlanCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	os.Stdout = stdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	printed, readErr := io.ReadAll(reader)
	reader.Close()
	if commandErr != nil || readErr != nil {
		t.Fatalf("allowed merge-plan: command=%v read=%v", commandErr, readErr)
	}
	var result mergeplan.Result
	if err := json.Unmarshal(printed, &result); err != nil || !result.Allowed {
		t.Fatalf("allowed merge-plan output = %q, result=%+v, err=%v", printed, result, err)
	}
	after := captureMergePlanReadOnlyState(t, fixture.workspace)
	if after != before {
		t.Fatalf("allowed merge-plan mutated governed or acceleration state\nbefore: %+v\nafter:  %+v", before, after)
	}
}

// Removing a file changes its covering directory, so the plan retires and
// republishes the widest covering pointer. Another actor's directory above the
// reviewed path remains outside the directional authority bound.
func TestMergeFreshPreflightRefusesACrossAuthorPointerAboveARemovedSource(t *testing.T) {
	t.Parallel()
	f, candidate, approval := buildRemovedNestedCrossAuthorApproval(t)
	beforeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	before := f.snapshot(t)
	if _, err := git(f.ctx, f.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("receipt reservation existed before the merge attempt")
	}

	err := mergeCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", candidate, "--approval", approval,
		"--text", "Attempt to merge a reviewed deletion under another actor's directory.",
	})
	if err == nil || !strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("fresh deletion merge error = %v, want reviewed-path refusal", err)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("refused deletion merge moved HEAD to %s, want %s", got, beforeHead)
	}
	after := f.snapshot(t)
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused deletion merge changed workroom: before=%s/%d after=%s/%d",
			before.Head, before.Depth, after.Head, after.Depth)
	}
	if _, err := git(f.ctx, f.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused deletion merge left the receipt reservation behind")
	}
}

func TestMergeGuardConsumesApprovalOnceAcrossTargets(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the approved feature and make it available on main.",
	}); err != nil {
		t.Fatal(err)
	}
	firstMerge := testGit(t, fixture.repo, "rev-parse", "HEAD")
	secondTarget := filepath.Join(filepath.Dir(fixture.repo), "second-target")
	testGit(t, fixture.repo, "worktree", "add", "-b", "second-target", secondTarget, targetPreHead)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", secondTarget,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Attempt to merge the feature into a second target.",
	})
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("approval replay on another target error = %v", err)
	}
	if got := testGit(t, secondTarget, "rev-parse", "HEAD"); got != targetPreHead {
		t.Fatalf("refused replay moved second target to %s, want %s", got, targetPreHead)
	}
	if got := testGit(t, fixture.repo, "rev-parse", mergeReceiptRef(approval)); got != firstMerge {
		t.Fatalf("replay changed receipt ref to %s, want %s", got, firstMerge)
	}
	// The signed workroom receipt keeps the approval consumed even if a local
	// receipt ref and the branch carrying the merge commit are both lost before
	// another checkout tries to use it.
	testGit(t, fixture.repo, "update-ref", "-d", mergeReceiptRef(approval), firstMerge)
	testGit(t, fixture.repo, "update-ref", "refs/heads/main", targetPreHead, firstMerge)
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", secondTarget,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Attempt to merge the feature after losing local receipts.",
	})
	if err == nil || !strings.Contains(err.Error(), "durable merge receipt") {
		t.Fatalf("approval replay after receipt-ref loss error = %v", err)
	}
}

func TestMergeGuardRequiresPlainLanguageMergeText(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "merge requires --text") {
		t.Fatalf("missing merge text error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != targetPreHead {
		t.Fatalf("missing merge text moved target to %s, want %s", got, targetPreHead)
	}
}

func TestMergeGuardSerializesConcurrentApprovalUse(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	// Model another merge process holding the repository-wide reservation.
	testGit(t, fixture.repo, "update-ref", mergeReceiptRef(approval), targetPreHead, "")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Attempt a concurrent merge of the approved feature.",
	})
	if err == nil || !strings.Contains(err.Error(), "reserved or used") {
		t.Fatalf("concurrent approval use error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != targetPreHead {
		t.Fatalf("reservation refusal moved target to %s, want %s", got, targetPreHead)
	}
}

func TestMergeGuardRefusesChangedCandidate(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", base, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "does not equal approved head") {
		t.Fatalf("changed candidate error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("refused merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeGuardRecordsAndMergesAReasoningStaleApproval(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	if _, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbSupersede, Target: fixture.promise, Text: "review promise withdrawn",
		IdempotencyKey: "retire-review-promise",
	}); err != nil {
		t.Fatal(err)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the exact approved head and record the moved review argument.",
	})
	if err != nil {
		t.Fatalf("reasoning-stale approval was refused: %v", err)
	}
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if mergeHead == base {
		t.Fatal("reasoning-stale approval merge did not move HEAD")
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("read reasoning-stale merge receipt: ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"approval stale", fixture.promise} {
		if !strings.Contains(receipt.Staleness, want) {
			t.Fatalf("merge receipt staleness %q does not name %q", receipt.Staleness, want)
		}
	}
	var durable workroom.Statement
	for _, statement := range fixture.snapshot(t).Projection.Statements {
		if statement.Body["merge_approval"] == approval {
			durable = statement
		}
	}
	if durable.Body["stale"] != "true" || durable.Body["staleness"] != receipt.Staleness {
		t.Fatalf("durable merge receipt did not preserve staleness: %+v", durable)
	}
}

// A world that moves after the verdict is news the reviewer had no chance to
// see. The head they approved is immutable and the artifact still points at it,
// so the merge records what moved rather than refusing a judgement that was
// sound when it was made.
func TestMergeGuardRecordsAWorldThatMovedAfterTheVerdict(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	fixture.moveTheWorld(t)
	artifact := artifactByEvent(t, fixture.snapshot(t).Projection, fixture.artifact)
	if !artifact.DescribesSupersededWorld || artifact.WorldSupersededAt == 0 {
		t.Fatalf("artifact world=%v at=%d: this test cannot tell the dated rule from no rule at all", artifact.DescribesSupersededWorld, artifact.WorldSupersededAt)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Merge the exact approved head and record the world that moved after the verdict.",
	}); err != nil {
		t.Fatalf("a retirement after the verdict blocked the merge: %v", err)
	}
	mergeHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	if mergeHead == base {
		t.Fatal("the merge did not move HEAD")
	}
	receipt, ok, err := readMergeReceipt(fixture.ctx, fixture.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("read merge receipt: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(receipt.Staleness, "describes a superseded world") {
		t.Fatalf("merge receipt staleness %q does not record the moved world", receipt.Staleness)
	}
}

// The consumer half of the ordering defect. A date older than the verdict
// refuses whatever produced it, and a later one does not, so the guard cannot
// pass by refusing everything.
func TestReviewWorldMovedAfterDoesNotHideOlderNestedRetirement(t *testing.T) {
	t.Parallel()
	projection := workroom.Projection{
		Decisions: []workroom.Decision{
			{Event: "candidate", Sequence: 40, Verdict: workroom.Effective},
			{Event: "approval", Sequence: 50, Verdict: workroom.Effective},
		},
		Artifacts: []workroom.Artifact{
			{Event: "candidate", Path: "candidate", Commit: "head", Stale: true,
				DescribesSupersededWorld: true, WorldSupersededAt: 10},
		},
		Statements: []workroom.Statement{
			{Event: "candidate", Kind: workroom.KindArtifact, Actor: "implementer", Stale: true,
				DescribesSupersededWorld: true, WorldSupersededAt: 10},
		},
	}
	if _, err := mergeplan.LiveArtifactAsOf(projection, "candidate", 50); err == nil {
		t.Fatal("a cause older than the verdict was admitted")
	}
	projection.Artifacts[0].WorldSupersededAt = 60
	if _, err := mergeplan.LiveArtifactAsOf(projection, "candidate", 50); err != nil {
		t.Fatalf("a cause later than the verdict refused the merge: %v", err)
	}
}

// An undated superseded world is not permission. The fold reports zero when no
// active cause accounts for it, and the guard must read that as refuse.
func TestReviewRefusesAnUndatedSupersededWorld(t *testing.T) {
	t.Parallel()
	projection := workroom.Projection{
		Decisions:  []workroom.Decision{{Event: "candidate", Sequence: 40, Verdict: workroom.Effective}},
		Artifacts:  []workroom.Artifact{{Event: "candidate", Path: "candidate", Commit: "head", Stale: true, DescribesSupersededWorld: true}},
		Statements: []workroom.Statement{{Event: "candidate", Kind: workroom.KindArtifact, Actor: "implementer", Stale: true, DescribesSupersededWorld: true}},
	}
	if _, err := mergeplan.LiveArtifactAsOf(projection, "candidate", math.MaxInt); err == nil {
		t.Fatal("an undated superseded world was admitted")
	}
}

// World staleness is not repaired by repeating reasoning: the implementation
// pointer itself follows behaviour that was replaced and must be re-anchored.
func TestMergeGuardStillRefusesAMovedWorldAfterAnApprovedReview(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	fixture.moveTheWorld(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "describes a superseded world") {
		t.Fatalf("merge over a moved world error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("refused merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeLivenessSeparatesReasoningStaleFromSupersededWorld(t *testing.T) {
	t.Parallel()
	projection := workroom.Projection{
		Decisions: []workroom.Decision{
			{Event: "ordinary-artifact", Verdict: workroom.Effective},
			{Event: "world-artifact", Verdict: workroom.Effective},
			{Event: "ordinary-report", Verdict: workroom.Effective},
			{Event: "world-report", Verdict: workroom.Effective},
		},
		Artifacts: []workroom.Artifact{
			{Event: "ordinary-artifact", Path: "ordinary", Commit: "head", Stale: true},
			{Event: "world-artifact", Path: "world", Commit: "head", Stale: true, DescribesSupersededWorld: true},
		},
		Statements: []workroom.Statement{
			{Event: "ordinary-artifact", Kind: workroom.KindArtifact, Actor: "implementer", Stale: true},
			{Event: "world-artifact", Kind: workroom.KindArtifact, Actor: "implementer", Stale: true, DescribesSupersededWorld: true},
			{Event: "ordinary-report", Kind: workroom.KindReport, Stale: true},
			{Event: "world-report", Kind: workroom.KindReport, Stale: true, DescribesSupersededWorld: true},
		},
		Reviews:    []workroom.Review{{Report: "approval", Implementer: "implementer", Head: "head"}},
		Provenance: map[string][]string{"approval": {"ordinary-artifact", "world-artifact"}},
	}
	if _, err := mergeplan.LiveArtifactAsOf(projection, "ordinary-artifact", math.MaxInt); err != nil {
		t.Fatalf("ordinary reasoning-stale artifact was refused: %v", err)
	}
	if _, err := mergeplan.LiveStatementAsOf(projection, "ordinary-report", workroom.KindReport, math.MaxInt); err != nil {
		t.Fatalf("ordinary reasoning-stale report was refused: %v", err)
	}
	if _, err := mergeplan.LiveArtifactAsOf(projection, "world-artifact", math.MaxInt); err == nil || !strings.Contains(err.Error(), "superseded world") {
		t.Fatalf("world-stale artifact error = %v", err)
	}
	if _, err := mergeplan.LiveStatementAsOf(projection, "world-report", workroom.KindReport, math.MaxInt); err == nil || !strings.Contains(err.Error(), "superseded world") {
		t.Fatalf("world-stale report error = %v", err)
	}
	if _, paths := mergeplan.ReviewedScope(projection, "approval"); !slices.Equal(paths, []string{"ordinary"}) {
		t.Fatalf("reviewed paths = %v, want only the ordinary-stale artifact", paths)
	}
}

func TestMergeGuardRefusesUnratifiedApproval(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "not ratified") {
		t.Fatalf("unratified approval error = %v", err)
	}
}

func TestMergeGuardRefusesRatifiedChangesRequestedVerdict(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	approval := fixture.reviewVerdict(t, "changes-requested")
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "review verdict is not approved") {
		t.Fatalf("changes-requested merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("changes-requested merge moved HEAD to %s, want %s", got, base)
	}
}

func TestMergeGuardRefusesApprovalNotRestingOnNamedArtifact(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	// Build below the application write boundary to preserve the merge gate's
	// defense-in-depth coverage. Ordinary state surfaces now refuse this shape
	// before signing it; an older or independently signed record can still be
	// present in an attached sequence.
	_, private, err := fixture.workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{
		Kind: workroom.KindReport, Text: "approval with an ungrounded artifact field",
		Body: map[string]string{
			"verdict": "approved", "head": fixture.candidate, "artifact": fixture.artifact,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fixture.workspace.Store.WritePayloadTree(fixture.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := fixture.workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        []string{fixture.promise, fixture.request},
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "ungrounded-artifact-approval",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Submit(fixture.ctx, fixture.workspace.Store, kernel.Request{Signed: signed, Payload: payload}, kernel.Options{SigningKey: view.SequencerKey}); err != nil {
		t.Fatal(err)
	}
	snapshotAfter := fixture.snapshot(t).Projection
	approval := snapshotAfter.Statements[len(snapshotAfter.Statements)-1].Event
	fixture.ratify(t, approval)
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
	})
	if err == nil || !strings.Contains(err.Error(), "approval does not rest on its named artifact") {
		t.Fatalf("ungrounded artifact approval error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("ungrounded artifact merge moved HEAD to %s, want %s", got, base)
	}
}

// chainBatch is the ordinary case: a request, then a promise resting on it by
// intra-batch label. The verb argument is the genesis event the request rests on.
const chainBatch = `[
  {"label": "req", "verb": "state", "kind": "request", "text": "do the thing",
   "body": {"to": "@worker", "conditions": "tests green"},
   "rests_on": [%q], "idempotency_key": "chain-request"},
  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will do the thing",
   "rests_on": ["$req"], "idempotency_key": "chain-promise"}
]`

func TestBatchLandsChainResolvingIntraBatchLabels(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	report, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if report.Landed != 2 || report.Replayed != 0 || report.Error != nil {
		t.Fatalf("batch report = %#v", report)
	}
	for position, want := range []string{"req", "promise"} {
		outcome := report.Acts[position]
		if outcome.Position != position || outcome.Label != want || outcome.Outcome != "landed" || outcome.Event == "" {
			t.Fatalf("act %d outcome = %#v", position, outcome)
		}
	}
	snapshot := fixture.snapshot()
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d", snapshot.Depth, before+2)
	}
	request, promise := report.Acts[0].Event, report.Acts[1].Event
	if !contains(snapshot.Projection.Provenance[promise], request) {
		t.Fatalf("promise provenance = %#v, want the minted request %s", snapshot.Projection.Provenance[promise], request)
	}
}

func TestBatchArtifactFilingRequiresFullCanonicalCommit(t *testing.T) {
	fixture := newBatchFixture(t)
	testGit(t, fixture.repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "artifact head")
	head := testGit(t, fixture.repo, "rev-parse", "HEAD")
	before := fixture.snapshot()
	acts := fmt.Sprintf(`[
	  {"verb":"state","kind":"artifact","text":"short commit must not land",
	   "body":{"path":"short.txt","commit":%q},
	   "rests_on":[%q],"idempotency_key":"short-artifact-commit"}
	]`, head[:12], fixture.genesis)
	report, err := fixture.run("operator", acts)
	if err == nil {
		t.Fatalf("short artifact commit landed: %#v", report)
	}
	for _, want := range []string{"commit must be the full canonical object ID", head[:12], head} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("batch artifact refusal omits %q: %v", want, err)
		}
	}
	if len(report.Acts) != 1 || report.Acts[0].Outcome != "failed" || report.Error == nil || report.Error.Code != "admission" {
		t.Fatalf("refused batch report = %#v", report)
	}
	after := fixture.snapshot()
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused batch changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

func TestBatchRetryLandsNothingNew(t *testing.T) {
	fixture := newBatchFixture(t)
	first, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	landed := fixture.snapshot()
	second, err := fixture.run("operator", fmt.Sprintf(chainBatch, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if second.Landed != 0 || second.Replayed != 2 || second.Error != nil {
		t.Fatalf("retry report = %#v", second)
	}
	for position := range second.Acts {
		if second.Acts[position].Outcome != "replayed" || second.Acts[position].Event != first.Acts[position].Event {
			t.Fatalf("retry act %d = %#v, want the first run's event %s", position, second.Acts[position], first.Acts[position].Event)
		}
	}
	after := fixture.snapshot()
	if after.Head != landed.Head || after.Depth != landed.Depth {
		t.Fatalf("retry moved the log to %s depth %d, want %s depth %d", after.Head, after.Depth, landed.Head, landed.Depth)
	}
}

func TestBatchRefusesUndefinedLabelWithoutLanding(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot()
	report, err := fixture.run("operator", fmt.Sprintf(`[
	  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "undefined-first"},
	  {"verb": "state", "kind": "assert", "text": "second", "rests_on": ["$missing"], "idempotency_key": "undefined-second"}
	]`, fixture.genesis))
	var failure *batchError
	if !errors.As(err, &failure) || failure.Code != "reference" {
		t.Fatalf("undefined label error = %v", err)
	}
	if report.Landed != 0 || report.Replayed != 0 || report.Error == nil || report.Error.Code != "reference" {
		t.Fatalf("undefined label report = %#v", report)
	}
	if report.Acts[0].Outcome != "skipped" || report.Acts[0].Event != "" || report.Acts[1].Outcome != "failed" {
		t.Fatalf("undefined label acts = %#v", report.Acts)
	}
	after := fixture.snapshot()
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused batch moved the log to %s depth %d, want %s depth %d", after.Head, after.Depth, before.Head, before.Depth)
	}
}

// TestBatchRefusesTrailingInputWithoutLanding guards the parser boundary. A
// valid array followed by anything but whitespace must be refused before the
// first append. The stray-delimiter cases are the ones a decoder.More check
// lets through: More asks whether the value being parsed has another element,
// so a closing delimiter reads to it as a clean end of input and the acts
// before it land.
func TestBatchRefusesTrailingInputWithoutLanding(t *testing.T) {
	for _, testCase := range []struct{ name, trailing string }{
		{"stray array delimiter", "]"},
		{"stray object delimiter", "}"},
		{"delimiter on its own line", "\n]\n"},
		{"second value", `{"verb": "state", "kind": "assert", "text": "second"}`},
		{"unterminated value", "["},
		{"malformed bytes", "not json"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newBatchFixture(t)
			before := fixture.snapshot()
			printed, err := fixture.runFile("operator", fmt.Sprintf(`[
			  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "trailing-first"}
			]`, fixture.genesis)+testCase.trailing)
			var failure *batchError
			if !errors.As(err, &failure) || failure.Code != "input" {
				t.Fatalf("trailing %q error = %v, want a typed input failure", testCase.trailing, err)
			}
			if len(printed) != 0 {
				t.Fatalf("trailing %q printed a report before failing: %s", testCase.trailing, printed)
			}
			after := fixture.snapshot()
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("trailing %q moved the log to %s depth %d, want %s depth %d",
					testCase.trailing, after.Head, after.Depth, before.Head, before.Depth)
			}
		})
	}
	t.Run("trailing whitespace is not content", func(t *testing.T) {
		fixture := newBatchFixture(t)
		before := fixture.snapshot().Depth
		report, err := fixture.run("operator", fmt.Sprintf(`[
		  {"verb": "state", "kind": "assert", "text": "first", "rests_on": [%q], "idempotency_key": "whitespace-first"}
		]`, fixture.genesis)+"\n\t \n")
		if err != nil {
			t.Fatal(err)
		}
		if report.Landed != 1 || report.Error != nil {
			t.Fatalf("whitespace-terminated report = %#v", report)
		}
		if depth := fixture.snapshot().Depth; depth != before+1 {
			t.Fatalf("depth = %d, want %d", depth, before+1)
		}
	})
}

// TestBatchRetryAfterPartialLandingReplaysPrefixAndLandsSuffix is the recovery
// case: the first run stops mid-chain, so only a prefix is durable. The second
// run of the same file replays that prefix under its idempotency key, resolves
// the label to the event already minted, and lands only the suffix.
func TestBatchRetryAfterPartialLandingReplaysPrefixAndLandsSuffix(t *testing.T) {
	fixture := newBatchFixture(t)
	// An act the application boundary cannot build now stops the whole batch
	// before the first append, so the prefix is durable only once the chain
	// can land cleanly; the idempotency keys keep every later rerun cheap.
	acts := fmt.Sprintf(`[
	  {"label": "note", "verb": "state", "kind": "assert", "text": "the prefix is durable",
	   "rests_on": [%q], "idempotency_key": "partial-assert"},
	  {"verb": "state", "kind": "request", "text": "finish the chain",
	   "body": {"to": "@latecomer", "conditions": "the suffix lands once"},
	   "rests_on": ["$note"], "idempotency_key": "partial-request"}
	]`, fixture.genesis)

	before := fixture.snapshot()
	first, err := fixture.run("operator", acts)
	if err == nil {
		t.Fatal("batch naming an unknown performer succeeded")
	}
	if first.Landed != 0 || first.Replayed != 0 || first.Error == nil {
		t.Fatalf("preflighted run report = %#v", first)
	}
	if fixture.snapshot().Depth != before.Depth {
		t.Fatalf("a batch whose suffix cannot build changed depth to %d", fixture.snapshot().Depth)
	}

	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "latecomer", "agent"); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Landed != 2 || second.Replayed != 0 || second.Error != nil {
		t.Fatalf("repair run report = %#v", second)
	}
	if second.Acts[0].Outcome != "landed" || second.Acts[1].Outcome != "landed" {
		t.Fatalf("repair acts = %#v", second.Acts)
	}
	landed := fixture.snapshot()
	if !contains(landed.Projection.Provenance[second.Acts[1].Event], second.Acts[0].Event) {
		t.Fatalf("suffix provenance = %#v, want the prefix event %s",
			landed.Projection.Provenance[second.Acts[1].Event], second.Acts[0].Event)
	}

	third, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatal(err)
	}
	if third.Landed != 0 || third.Replayed != 2 || third.Error != nil {
		t.Fatalf("retry report = %#v", third)
	}
	for position := range third.Acts {
		if third.Acts[position].Outcome != "replayed" || third.Acts[position].Event != second.Acts[position].Event {
			t.Fatalf("retry act %d = %#v, want the earlier event %s", position, third.Acts[position], second.Acts[position].Event)
		}
	}
	after := fixture.snapshot()
	if after.Head != landed.Head || after.Depth != landed.Depth {
		t.Fatalf("retry moved the log to %s depth %d, want %s depth %d", after.Head, after.Depth, landed.Head, landed.Depth)
	}
	assertions := 0
	for _, statement := range after.Projection.Statements {
		if statement.Text == "the prefix is durable" {
			assertions++
		}
	}
	if assertions != 1 {
		t.Fatalf("the prefix act appears %d times in the projection, want once", assertions)
	}
}

func TestBatchMixesStateAndRatify(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	report, err := fixture.run("operator", fmt.Sprintf(`[
	  {"label": "note", "verb": "state", "kind": "assert", "text": "the log is verified once",
	   "rests_on": [%q], "idempotency_key": "mixed-assert"},
	  {"verb": "ratify", "target": "$note", "idempotency_key": "mixed-ratify"}
	]`, fixture.genesis))
	if err != nil {
		t.Fatal(err)
	}
	if report.Landed != 2 || report.Error != nil {
		t.Fatalf("mixed report = %#v", report)
	}
	snapshot := fixture.snapshot()
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d", snapshot.Depth, before+2)
	}
	if statement := statementByEvent(t, snapshot.Projection, report.Acts[0].Event); !statement.Ratified {
		t.Fatalf("assert %s is not ratified", report.Acts[0].Event)
	}
	ratification := actByEvent(t, snapshot.Projection, report.Acts[1].Event)
	if ratification.Target != report.Acts[0].Event || ratification.Verdict != workroom.Effective {
		t.Fatalf("ratify act = %#v, want an effective ratification of %s", ratification, report.Acts[0].Event)
	}
}

// A kind this workroom does not define is refused before anything is signed.
// The refusal names the live vocabulary and the ratified kind-def that would
// establish the kind, and a defined kind still lands quietly.
func TestStateRefusesUndefinedKindsAndListsTheVocabulary(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	_, _, err := fixture.state("operator", "commit", "I will re-review task/x at exact head y", "undefined-kind-refused")
	if err == nil {
		t.Fatal("state with an undefined kind was signed")
	}
	for _, want := range []string{`"commit"`, "no override exists", "kinds defined here:", "ratified kind-def"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
	for _, definition := range fixture.snapshot().Vocabulary.Definitions {
		if !strings.Contains(err.Error(), string(definition.Name)) {
			t.Fatalf("refusal %q does not list the defined kind %q", err, definition.Name)
		}
	}
	if got := fixture.snapshot().Depth; got != before {
		t.Fatalf("refused undefined kind changed depth %d -> %d", before, got)
	}
	printed, warned, err := fixture.state("operator", "assert", "an ordinary claim", "defined-kind-is-quiet")
	if err != nil {
		t.Fatalf("state with a defined kind failed: %v", err)
	}
	if !strings.Contains(printed, "#git:") || strings.TrimSpace(warned) != "" {
		t.Fatalf("a defined kind landed noisily: stdout=%q stderr=%q", printed, warned)
	}
}

// deadWorld lands one live claim, retires it with a supersession, and files
// another claim on the retired one, which inherits staleness while staying
// live. It returns the three identifiers a later act can then cite to rest on
// ground that died in each of the three ways.
func deadWorld(t *testing.T, f batchFixture) (ground, stale, supersedeEvent string) {
	t.Helper()
	printed, _, err := f.state("operator", "assert", "the ground truth", "dead-basis-ground")
	if err != nil {
		t.Fatal(err)
	}
	ground = strings.TrimSpace(printed)
	retire := `[{"verb":"supersede","target":"` + ground + `","text":"withdrawn","idempotency_key":"dead-basis-retire"}]`
	if _, err := f.run("operator", retire); err != nil {
		t.Fatal(err)
	}

	snapshot := f.snapshot()
	if statement := statementByEvent(t, snapshot.Projection, ground); !statement.Retired || statement.Stale {
		t.Fatalf("ground after retirement: retired=%v stale=%v, want retired only", statement.Retired, statement.Stale)
	}
	for _, act := range snapshot.Projection.Acts {
		if act.Type == "supersede" && act.Target == ground && act.Verdict == workroom.Effective {
			supersedeEvent = act.Event
		}
	}
	if supersedeEvent == "" {
		t.Fatal("the retirement did not project as an effective supersede act")
	}

	// The stale claim is itself the escape the dead-basis rule exists for: it
	// rests on withdrawn ground deliberately, signing the override.
	stalePrinted, _, err := f.stateWith("operator", "assert", "resting on withdrawn ground",
		"dead-basis-stale", []string{"--allow-dead-basis"}, ground)
	if err != nil {
		t.Fatal(err)
	}
	stale = strings.TrimSpace(stalePrinted)
	if statement := statementByEvent(t, f.snapshot().Projection, stale); statement.Retired || !statement.Stale {
		t.Fatalf("claim after its basis retired: retired=%v stale=%v, want stale and live", statement.Retired, statement.Stale)
	}
	return ground, stale, supersedeEvent
}

// Resting on a retired basis refuses by default now, naming the retired basis
// and the escape. A merely stale basis is not part of that refusal: it is
// admitted and recorded. Asking for the escape signs the override, the act
// lands, and each of the three deaths still gets its own advisory line, named
// by event id; a citation that is alive says nothing.
func TestStateRefusesADeadRestOnBasisUntilTheOverrideSignsIt(t *testing.T) {
	f := newBatchFixture(t)
	ground, stale, supersedeEvent := deadWorld(t, f)

	before := f.snapshot().Depth
	_, _, err := f.state("operator", "assert", "citing every dead thing at once", "dead-basis-refused",
		ground, stale, supersedeEvent, f.genesis)
	if err == nil {
		t.Fatal("a state resting on retired ground was signed without the escape")
	}
	if !strings.Contains(err.Error(), "--allow-dead-basis") {
		t.Errorf("refusal %q does not name the escape", err)
	}
	for _, dead := range []struct {
		id     string
		reason string
		named  bool
	}{
		{id: ground, reason: "retired", named: true},
		{id: stale, reason: "stale", named: false},
		{id: supersedeEvent, reason: "supersede", named: false},
	} {
		want := dead.id + " (" + dead.reason + ")"
		if named := strings.Contains(err.Error(), want); named != dead.named {
			t.Errorf("refusal %q names %q = %v, want %v", err, want, named, dead.named)
		}
	}
	if got := f.snapshot().Depth; got != before {
		t.Fatalf("refused dead-basis state changed depth %d -> %d", before, got)
	}

	stdout, stderr, err := f.stateWith("operator", "assert", "citing every dead thing at once",
		"dead-basis-override", []string{"--allow-dead-basis"}, ground, stale, supersedeEvent, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "#git:") {
		t.Fatalf("stdout = %q, want exactly one event id line", stdout)
	}
	stamped := statementByEvent(t, f.snapshot().Projection, strings.TrimSpace(stdout))
	if stamped.Body["dead_basis_override"] != "true" {
		t.Fatalf("override act body = %#v, want dead_basis_override=true", stamped.Body)
	}
	// The stale basis needed no escape, and the boundary wrote what had moved
	// under it onto the act instead of refusing it.
	if !strings.Contains(stamped.Body["stale_bases"], stale) {
		t.Fatalf("override act body = %#v, want stale_bases naming %s", stamped.Body, stale)
	}
	for id, reason := range map[string]string{
		ground:         "retired",
		stale:          "stale",
		supersedeEvent: "supersede",
	} {
		want := "note: rests-on " + id + " is already dead (" + reason + ")"
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not say %q", stderr, want)
		}
	}
	if strings.Contains(stderr, f.genesis) {
		t.Errorf("stderr %q warns about the living genesis basis", stderr)
	}

	_, quiet, err := f.state("operator", "assert", "an act on living ground", "dead-basis-quiet")
	if err != nil {
		t.Fatal(err)
	}
	if quiet != "" {
		t.Fatalf("a clean act warned anyway: %q", quiet)
	}
}

// The batch preflight refuses a chain resting on a retired basis before
// anything lands; with the per-act escape the chain lands, the act that needed
// the escape carries dead_basis_override=true, the act resting on merely stale
// ground carries the recorded staleness instead, and the advisory notes still
// resolve intra-batch citations to their durable names.
func TestBatchRefusesADeadRestOnBasisUntilTheOverrideSignsIt(t *testing.T) {
	f := newBatchFixture(t)
	ground, _, supersedeEvent := deadWorld(t, f)

	blocked := `[
	  {"label":"stale","verb":"state","kind":"assert","text":"on withdrawn ground","rests_on":["` + ground + `"]},
	  {"label":"final","verb":"state","kind":"assert","text":"citing the chain","rests_on":["$stale","` + supersedeEvent + `"]}
	]`
	before := f.snapshot().Depth
	stdout, stderr, batchErr := f.runFileStreams("operator", blocked)
	if batchErr == nil {
		t.Fatal("a batch resting on dead bases was admitted")
	}
	var refused batchReport
	if err := json.Unmarshal(stdout, &refused); err != nil {
		t.Fatalf("decode batch report %q: %v (stderr %q)", stdout, err, stderr)
	}
	if refused.Landed != 0 || refused.Error == nil {
		t.Fatalf("refused report = %#v", refused)
	}
	if f.snapshot().Depth != before {
		t.Fatalf("refused batch changed depth %d -> %d", before, f.snapshot().Depth)
	}

	escaped := `[
	  {"label":"stale","verb":"state","kind":"assert","text":"on withdrawn ground","rests_on":["` + ground + `"],"allow_dead_basis":true},
	  {"label":"final","verb":"state","kind":"assert","text":"citing the chain","rests_on":["$stale","` + supersedeEvent + `"],"allow_dead_basis":true}
	]`
	stdout, stderr, batchErr = f.runFileStreams("operator", escaped)
	if batchErr != nil {
		t.Fatal(batchErr)
	}
	var report batchReport
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("decode batch report %q: %v", stdout, err)
	}
	if report.Landed != 2 || report.Acts[0].Outcome != "landed" || report.Acts[1].Outcome != "landed" {
		t.Fatalf("batch report = %#v, want two landed acts", report)
	}
	projection := f.snapshot().Projection
	// The first act rests on retired ground, so it records the escape it
	// needed. The second rests on ground that is merely stale, which needs no
	// escape: the boundary admits it and records what had moved.
	first := statementByEvent(t, projection, report.Acts[0].Event)
	if first.Body["dead_basis_override"] != "true" {
		t.Fatalf("act %s body = %#v, want dead_basis_override=true", first.Event, first.Body)
	}
	second := statementByEvent(t, projection, report.Acts[1].Event)
	if second.Body["dead_basis_override"] != "" {
		t.Fatalf("act %s signed an escape it did not need: %#v", second.Event, second.Body)
	}
	if !strings.Contains(second.Body["stale_bases"], first.Event) {
		t.Fatalf("act %s body = %#v, want stale_bases naming %s", second.Event, second.Body, first.Event)
	}
	resolvedStale := report.Acts[0].Event
	for _, want := range []string{
		"note: rests-on " + ground + " is already dead (retired)",
		"note: rests-on " + resolvedStale + " is already dead (stale)",
		"note: rests-on " + supersedeEvent + " is already dead (supersede)",
	} {
		if !strings.Contains(string(stderr), want) {
			t.Errorf("stderr %q does not say %q", stderr, want)
		}
	}
}

// A fixture template is one finished fixture repository, built once for the
// whole package run. Building a workroom costs dozens of git subprocesses,
// so every test that needs one copies the finished template instead of
// rebuilding it from scratch. TestMain removes the template roots when the
// package finishes.
type fixtureTemplate struct {
	build func(root string) error
	once  sync.Once
	root  string
	err   error
}

// repo builds the template on first use and returns its repository path.
func (template *fixtureTemplate) repo(t *testing.T) string {
	t.Helper()
	template.once.Do(func() {
		template.root, template.err = os.MkdirTemp("", "gitseq-gs-template-")
		if template.err == nil {
			template.err = template.build(template.root)
		}
	})
	if template.err != nil {
		t.Fatal(template.err)
	}
	return filepath.Join(template.root, "repo")
}

// copyRepo copies the template repository to destination and points the
// absolute key paths its configuration holds (all under .git/gitseq) at the
// new location.
func (template *fixtureTemplate) copyRepo(t *testing.T, destination string) {
	t.Helper()
	source := template.repo(t)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cp", "-R", source+"/.", destination).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture template: %v: %s", err, output)
	}
	configPath := filepath.Join(destination, ".git", "gitseq", "config.json")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = bytes.ReplaceAll(config, []byte(source), []byte(destination))
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
}

var batchTemplate = fixtureTemplate{build: buildBatchTemplate}

func buildBatchTemplate(root string) error {
	ctx := context.Background()
	repo := filepath.Join(root, "repo")
	if _, err := gitCommand("", "init", "-b", "main", repo); err != nil {
		return err
	}
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		return err
	}
	_, _, err = workspace.AddActor(ctx, "operator", "worker", "agent")
	return err
}

// TestMain removes the shared fixture templates once every test is done with
// them.
func TestMain(m *testing.M) {
	code := testgit.Run(m)
	templates := []*fixtureTemplate{&batchTemplate}
	for _, workflow := range workflowTemplates {
		templates = append(templates, &workflow.fixtureTemplate)
	}
	for _, template := range templates {
		if template.root != "" {
			os.RemoveAll(template.root)
		}
	}
	os.Exit(code)
}

type batchFixture struct {
	t         *testing.T
	ctx       context.Context
	repo      string
	workspace *app.Workspace
	genesis   string
}

func newBatchFixture(t *testing.T) batchFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	batchTemplate.copyRepo(t, repo)
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	return batchFixture{t: t, ctx: ctx, repo: repo, workspace: workspace, genesis: workspace.EventID(workspace.View().Genesis)}
}

func (f batchFixture) snapshot() app.Snapshot {
	f.t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return snapshot
}

// runFile writes the acts to a file, runs the batch command against it, and
// returns whatever the command printed together with its error. Input rejected
// before the first append prints nothing at all, so the raw bytes matter.
func (f batchFixture) runFile(actor, acts string) ([]byte, error) {
	printed, _, err := f.runFileStreams(actor, acts)
	return printed, err
}

// runFileStreams is runFile for the cases where what lands on standard error
// while the batch runs is the point.
func (f batchFixture) runFileStreams(actor, acts string) ([]byte, []byte, error) {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(acts), 0o600); err != nil {
		f.t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, errWriter
	batchErr := batchCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, path})
	os.Stdout, os.Stderr = stdout, stderr
	writer.Close()
	errWriter.Close()
	printed, err := io.ReadAll(reader)
	if err != nil {
		f.t.Fatal(err)
	}
	warned, err := io.ReadAll(errReader)
	if err != nil {
		f.t.Fatal(err)
	}
	reader.Close()
	errReader.Close()
	return printed, warned, batchErr
}

// run is runFile for the cases that reach the report the command prints.
func (f batchFixture) run(actor, acts string) (batchReport, error) {
	f.t.Helper()
	printed, batchErr := f.runFile(actor, acts)
	var report batchReport
	if err := json.Unmarshal(printed, &report); err != nil {
		f.t.Fatalf("decode batch report %q: %v", printed, err)
	}
	return report, batchErr
}

func TestBatchReassignIfUnclaimedCarriesTheGuardedPairAndReplaysIt(t *testing.T) {
	fixture := newBatchFixture(t)
	old := actRecordCLI(t, fixture, app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "do it",
		Body:    map[string]string{"to": "@worker", "conditions": "finish"},
		RestsOn: []string{fixture.genesis}, IdempotencyKey: "reassign-old",
	})
	acts := `[
  {"label":"retirement","verb":"retire-if-unclaimed","target":"` + old.ID + `","text":"retire before reassignment","idempotency_key":"reassign-retirement"},
  {"verb":"state","kind":"assert","text":"unrelated traffic","rests_on":["` + fixture.genesis + `"],"idempotency_key":"reassign-unrelated"},
  {"label":"replacement","verb":"reassign-if-unclaimed","target":"` + old.ID + `","retirement":"$retirement","text":"ask again","body":{"to":"@worker","conditions":"finish on current bases"},"idempotency_key":"reassign-replacement"}
]`
	first, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Landed != 3 || first.Replayed != 0 {
		t.Fatalf("first batch = %+v", first)
	}
	if _, err := fixture.workspace.RetireActor(fixture.ctx, "operator", "@worker"); err != nil {
		t.Fatal(err)
	}
	second, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if second.Landed != 0 || second.Replayed != 3 {
		t.Fatalf("retry batch = %+v", second)
	}
}

func TestReassignIfUnclaimedCommandOwnsTwoActRetry(t *testing.T) {
	fixture := newBatchFixture(t)
	old := actRecordCLI(t, fixture, app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "old request",
		Body:    map[string]string{"to": "@worker", "conditions": "finish"},
		RestsOn: []string{fixture.genesis}, IdempotencyKey: "command-old",
	})
	arguments := []string{
		"--repo", fixture.repo, "--as", "operator", "--server", localFold,
		"--to", "@worker", "--text", "replacement request", "--conditions", "finish on current bases",
		"--idempotency-key", "command-reassign", old.ID,
	}
	first := runReassignCommand(t, fixture.ctx, arguments)
	if _, err := fixture.workspace.RetireActor(fixture.ctx, "operator", "@worker"); err != nil {
		t.Fatal(err)
	}
	actRecordCLI(t, fixture, app.Act{
		Verb: app.VerbSupersede, Target: first.Retirement,
		Text: "restore the original request after the completed reassignment", IdempotencyKey: "restore-command-old",
	})
	page := filepath.Join(fixture.repo, "docs", "reference", "late-command-citation.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+old.ID+"\n---\n\nlate citation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, fixture.repo, "add", "docs/reference/late-command-citation.md")
	second := runReassignCommand(t, fixture.ctx, arguments)
	if first != second || first.Retirement == "" || first.Request == "" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestReassignIfUnclaimedChecksTheCallingWorktreeBeforeResidentSubmission(t *testing.T) {
	t.Parallel()
	fixture := newBatchFixture(t)
	old := actRecordCLI(t, fixture, app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "old request",
		Body:    map[string]string{"to": "@worker", "conditions": "finish"},
		RestsOn: []string{fixture.genesis}, IdempotencyKey: "linked-command-old",
	})
	linked := filepath.Join(filepath.Dir(fixture.repo), "linked")
	testGit(t, fixture.repo, "worktree", "add", "-qb", "linked-reassign", linked)
	page := filepath.Join(linked, "docs", "reference", "linked-request.md")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("---\nrests_on:\n  - "+old.ID+"\n---\n\nlinked candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, linked, "add", "docs/reference/linked-request.md")

	resident, err := service.New(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(resident.Handler())
	defer httpServer.Close()
	before := fixture.snapshot().Depth
	err = reassignIfUnclaimedCommand(fixture.ctx, []string{
		"--repo", linked, "--as", "operator", "--server", httpServer.URL,
		"--to", "@worker", "--text", "replacement", "--conditions", "finish",
		"--idempotency-key", "linked-reassign", old.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "docs/reference/linked-request.md") {
		t.Fatalf("linked worktree citation error = %v", err)
	}
	if after := fixture.snapshot().Depth; after != before {
		t.Fatalf("resident received a refused guarded retirement: depth %d -> %d", before, after)
	}
}

func actRecordCLI(t *testing.T, fixture batchFixture, act app.Act) workroom.Record {
	t.Helper()
	submission, err := fixture.workspace.Act(fixture.ctx, "operator", act)
	if err != nil {
		t.Fatal(err)
	}
	return submission.Record
}

func runReassignCommand(t *testing.T, ctx context.Context, arguments []string) reassignIfUnclaimedResult {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	err = reassignIfUnclaimedCommand(ctx, arguments)
	os.Stdout = stdout
	writer.Close()
	printed, readErr := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result reassignIfUnclaimedResult
	if err := json.Unmarshal(printed, &result); err != nil {
		t.Fatalf("decode %q: %v", printed, err)
	}
	return result
}

// state runs the state command the way a person does and returns what each
// stream received, because which stream carried the warning is the point.
// Rests-on bases default to the genesis event; a test citing other ground
// names it and every id gets its own repeat of the flag, as a person types.
func (f batchFixture) state(actor, kind, text, key string, rests ...string) (string, string, error) {
	return f.stateWith(actor, kind, text, key, nil, rests...)
}

// stateWith is state with extra command-line flags, for tests that exercise
// the explicit escapes.
func (f batchFixture) stateWith(actor, kind, text, key string, extraFlags []string, rests ...string) (string, string, error) {
	f.t.Helper()
	if len(rests) == 0 {
		rests = []string{f.genesis}
	}
	outReader, outWriter, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	arguments := []string{"--repo", f.repo, "--as", actor, "--kind", kind, "--text", text}
	for _, id := range rests {
		arguments = append(arguments, "--rests-on", id)
	}
	arguments = append(arguments, extraFlags...)
	stateErr := stateCommand(f.ctx, append(arguments, "--idempotency-key", key))
	os.Stdout, os.Stderr = stdout, stderr
	outWriter.Close()
	errWriter.Close()
	printed, err := io.ReadAll(outReader)
	if err != nil {
		f.t.Fatal(err)
	}
	warned, err := io.ReadAll(errReader)
	if err != nil {
		f.t.Fatal(err)
	}
	outReader.Close()
	errReader.Close()
	return string(printed), string(warned), stateErr
}

func decisionByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Decision {
	t.Helper()
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return decision
		}
	}
	t.Fatalf("decision for %s not found", event)
	return workroom.Decision{}
}

func TestStateRefusesMalformedRequestBeforeAppend(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body []string
		want string
	}{
		{name: "missing conditions", body: []string{"--body", "to=@worker"}, want: "request state requires body.conditions"},
		{name: "unknown performer", body: []string{"--body", "to=@nobody", "--body", "conditions=tests pass"}, want: "request body.to"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newBatchFixture(t)
			before := f.snapshot()
			arguments := []string{
				"--repo", f.repo, "--as", "operator", "--kind", "request",
				"--text", "malformed request", "--rests-on", f.genesis,
				"--idempotency-key", "state-malformed-" + strings.ReplaceAll(test.name, " ", "-"),
			}
			arguments = append(arguments, test.body...)
			err := stateCommand(f.ctx, arguments)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			after := f.snapshot()
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused state request changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

func TestBatchRefusesMalformedRequestsBeforeTheirAppend(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing conditions", body: `{"to":"@worker"}`, want: "request state requires body.conditions"},
		{name: "unknown performer", body: `{"to":"@nobody","conditions":"tests pass"}`, want: "request body.to"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newBatchFixture(t)
			before := f.snapshot()
			acts := `[{"verb":"state","kind":"request","text":"malformed request","body":` + test.body + `,"rests_on":["` + f.genesis + `"]}]`
			report, err := f.run("operator", acts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(report.Acts) != 1 || report.Acts[0].Outcome != "failed" || report.Error == nil {
				t.Fatalf("refused batch report = %#v", report)
			}
			after := f.snapshot()
			if after.Head != before.Head || after.Depth != before.Depth {
				t.Fatalf("refused batch changed workroom: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
			}
		})
	}
}

func actByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Act {
	t.Helper()
	for _, act := range projection.Acts {
		if act.Event == event {
			return act
		}
	}
	t.Fatalf("act %s not found", event)
	return workroom.Act{}
}

type workflowFixture struct {
	t                     *testing.T
	ctx                   context.Context
	repo                  string
	feature               string
	workspace             *app.Workspace
	candidate             string
	artifact              string
	ground                string
	request               string
	promise               string
	implementationRequest string
}

func newWorkflowFixture(t *testing.T) workflowFixture {
	return newWorkflowFixtureRemoving(t, false)
}

// newWorkflowFixtureRemoving optionally makes the candidate delete the base
// file as well as adding the feature file. A deletion is the one change that
// produces a retirement with no successor, which is the case merge succession
// still refuses to force through a citation.
func newWorkflowFixtureRemoving(t *testing.T, removeBase bool) workflowFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	template := workflowTemplateRemoving(removeBase)
	template.copyRepo(t, repo)
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", feature, "feature")
	return workflowFixture{
		t: t, ctx: ctx, repo: repo, feature: feature, workspace: workspace,
		candidate: template.candidate, artifact: template.artifact, ground: template.ground,
		request: template.request, promise: template.promise, implementationRequest: template.implementationRequest,
	}
}

type workflowCitation int

const (
	workflowNoCitation workflowCitation = iota
	workflowCandidateAddsCitation
	workflowCandidateDeletesCitation
)

func newWorkflowFixtureWithCitation(t *testing.T, citation workflowCitation) workflowFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	template := workflowTemplateWithCitation(citation)
	template.copyRepo(t, repo)
	workspace, err := app.Open(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", feature, "feature")
	return workflowFixture{
		t: t, ctx: ctx, repo: repo, feature: feature, workspace: workspace,
		candidate: template.candidate, artifact: template.artifact, ground: template.ground,
		request: template.request, promise: template.promise, implementationRequest: template.implementationRequest,
	}
}

// A workflow template also remembers the durable event ids its workroom
// holds. Every copy shares the template's log byte for byte, so the ids are
// the same in every copy.
type workflowTemplate struct {
	fixtureTemplate
	candidate             string
	ground                string
	artifact              string
	request               string
	promise               string
	implementationRequest string
}

var workflowTemplates = [4]*workflowTemplate{
	newWorkflowTemplate(false, workflowNoCitation),
	newWorkflowTemplate(true, workflowNoCitation),
	newWorkflowTemplate(true, workflowCandidateAddsCitation),
	newWorkflowTemplate(true, workflowCandidateDeletesCitation),
}

func newWorkflowTemplate(removeBase bool, citation workflowCitation) *workflowTemplate {
	template := &workflowTemplate{}
	template.build = func(root string) error { return template.buildWorkflow(root, removeBase, citation, 1<<20, 0) }
	return template
}

func workflowTemplateRemoving(removeBase bool) *workflowTemplate {
	if removeBase {
		return workflowTemplates[1]
	}
	return workflowTemplates[0]
}

func workflowTemplateWithCitation(citation workflowCitation) *workflowTemplate {
	switch citation {
	case workflowCandidateAddsCitation:
		return workflowTemplates[2]
	case workflowCandidateDeletesCitation:
		return workflowTemplates[3]
	default:
		return workflowTemplates[0]
	}
}

func (template *workflowTemplate) buildWorkflow(root string, removeBase bool, citation workflowCitation, ceiling uint64, extraPaths int) error {
	ctx := context.Background()
	repo := filepath.Join(root, "repo")
	feature := filepath.Join(root, "feature")
	if _, err := gitCommand("", "init", "-b", "main", repo); err != nil {
		return err
	}
	if _, err := gitCommand(repo, "config", "user.name", "Test"); err != nil {
		return err
	}
	if _, err := gitCommand(repo, "config", "user.email", "test@example.invalid"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		return err
	}
	if _, err := gitCommand(repo, "add", "base.txt"); err != nil {
		return err
	}
	if _, err := gitCommand(repo, "commit", "-m", "base"); err != nil {
		return err
	}
	workspace, _, err := app.Init(ctx, repo, "operator", ceiling)
	if err != nil {
		return err
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "reviewer", "agent"); err != nil {
		return err
	}
	base, err := gitCommand(repo, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	// The feature stands on the base of the repository, exactly as ordinary
	// work stands on whatever main was when it started. Retiring this is how a
	// test moves the world without touching the feature commit.
	groundSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "repository base",
		Body:    map[string]string{"path": "base.txt", "commit": base},
		RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: "ground",
	})
	if err != nil {
		return err
	}
	implementationRequest, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "implement feature",
		Body: map[string]string{
			"to":         workspace.View().Actors["operator"].Fingerprint,
			"conditions": "publish the exact feature head; do not merge without authorization",
		},
		RestsOn: []string{groundSubmission.Record.ID}, IdempotencyKey: "implementation-request",
	})
	if err != nil {
		return err
	}
	implementationPromise, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "implement exact feature head",
		RestsOn: []string{implementationRequest.Record.ID}, IdempotencyKey: "implementation-promise",
	})
	if err != nil {
		return err
	}
	citationPath := filepath.Join("docs", "reference", "base.md")
	if citation == workflowCandidateDeletesCitation {
		full := filepath.Join(repo, citationPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte("---\nbasis:\n  - "+groundSubmission.Record.ID+"\n---\n\nprose\n"), 0o600); err != nil {
			return err
		}
		if _, err := gitCommand(repo, "add", citationPath); err != nil {
			return err
		}
		if _, err := gitCommand(repo, "commit", "-m", "cite the live base artifact"); err != nil {
			return err
		}
	}
	if _, err := gitCommand(repo, "worktree", "add", "-b", "feature", feature); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(feature, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		return err
	}
	if extraPaths > 0 {
		if err := os.MkdirAll(filepath.Join(feature, "extra"), 0o755); err != nil {
			return err
		}
		for index := 0; index < extraPaths; index++ {
			name := fmt.Sprintf("%04d-%s.txt", index, strings.Repeat("x", 48))
			if err := os.WriteFile(filepath.Join(feature, "extra", name), []byte("x\n"), 0o644); err != nil {
				return err
			}
		}
	}
	if citation == workflowCandidateAddsCitation {
		full := filepath.Join(feature, citationPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte("---\nbasis:\n  - "+groundSubmission.Record.ID+"\n---\n\nprose\n"), 0o600); err != nil {
			return err
		}
	}
	if _, err := gitCommand(feature, "add", "."); err != nil {
		return err
	}
	if removeBase {
		if _, err := gitCommand(feature, "rm", "-q", "base.txt"); err != nil {
			return err
		}
	}
	if citation == workflowCandidateDeletesCitation {
		if _, err := gitCommand(feature, "rm", "-q", citationPath); err != nil {
			return err
		}
	}
	if _, err := gitCommand(feature, "commit", "-m", "feature"); err != nil {
		return err
	}
	candidate, err := gitCommand(feature, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	artifactSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "feature artifact",
		Body:    map[string]string{"path": "feature.txt", "commit": candidate},
		RestsOn: []string{implementationPromise.Record.ID, groundSubmission.Record.ID}, IdempotencyKey: "artifact",
	})
	if err != nil {
		return err
	}
	if extraPaths > 0 {
		if _, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "extra candidate tree",
			Body:    map[string]string{"path": "extra", "commit": candidate},
			RestsOn: []string{groundSubmission.Record.ID}, IdempotencyKey: "extra-artifact",
		}); err != nil {
			return err
		}
	}
	requestSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review feature",
		Body:    map[string]string{"to": workspace.View().Actors["reviewer"].Fingerprint, "conditions": "exact head"},
		RestsOn: []string{artifactSubmission.Record.ID}, IdempotencyKey: "review-request",
	})
	if err != nil {
		return err
	}
	promiseSubmission, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review exact head",
		RestsOn: []string{requestSubmission.Record.ID}, IdempotencyKey: "review-promise",
	})
	if err != nil {
		return err
	}
	template.candidate = candidate
	template.ground = groundSubmission.Record.ID
	template.artifact = artifactSubmission.Record.ID
	template.request = requestSubmission.Record.ID
	template.promise = promiseSubmission.Record.ID
	template.implementationRequest = implementationRequest.Record.ID
	_, err = gitCommand(repo, "worktree", "remove", feature)
	return err
}

// moveTheWorld retires the base everything under review rests on, leaving the
// reviewed commit exactly what it was. Nothing here is retired except the
// base: the artifact, the request and the promise become stale.
func (f workflowFixture) moveTheWorld(t *testing.T) {
	t.Helper()
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: f.ground, Text: "the base moved on",
		IdempotencyKey: "retire-ground",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	artifact := artifactByEvent(t, snapshot.Projection, f.artifact)
	if artifact.Retired || !artifact.Stale {
		t.Fatalf("feature artifact after the base moved: retired=%v stale=%v, want a stale live artifact", artifact.Retired, artifact.Stale)
	}
	if promise := statementByEvent(t, snapshot.Projection, f.promise); promise.Retired || !promise.Stale {
		t.Fatalf("review promise after the base moved: retired=%v stale=%v, want stale and live", promise.Retired, promise.Stale)
	}
}

// fileHeadNews files a durable statement sequenced after the review request
// that names the reviewed head in a structured body field, and returns the
// event id the guard must see as head news.
func (f workflowFixture) fileHeadNews(t *testing.T, text string) (string, error) {
	t.Helper()
	submission, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: text,
		Body:    map[string]string{"head": f.candidate},
		RestsOn: []string{f.artifact}, IdempotencyKey: "head-news-" + text,
	})
	if err != nil {
		return "", err
	}
	return submission.Record.ID, nil
}

func (f workflowFixture) snapshot(t *testing.T) app.Snapshot {
	t.Helper()
	snapshot, err := f.workspace.Snapshot(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f workflowFixture) review(t *testing.T) string {
	return f.reviewVerdict(t, "approved")
}

func (f workflowFixture) reviewVerdict(t *testing.T, verdict string) string {
	t.Helper()
	before := f.snapshot(t).Depth
	if err := reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", f.artifact, "--promise", f.promise,
		"--verdict", verdict, "--text", strings.ToUpper(verdict) + " exact head",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("review depth = %d, want %d", snapshot.Depth, before+1)
	}
	return snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event
}

func (f workflowFixture) reviewError() error {
	return reviewCommand(f.ctx, []string{
		"--repo", f.repo, "--as", "reviewer", "--checkout", f.feature,
		"--artifact", f.artifact, "--promise", f.promise,
		"--verdict", "approved", "--text", "APPROVED exact head",
	})
}

func (f workflowFixture) ratify(t *testing.T, approval string) {
	t.Helper()
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbRatify, Target: approval, IdempotencyKey: "ratify-" + approval,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f workflowFixture) authorize(t *testing.T, approval string, ratified bool, mutate func(map[string]string)) string {
	return f.authorizeAs(t, approval, "operator", "reviewer", ratified, mutate)
}

func (f workflowFixture) authorizeAs(t *testing.T, approval, requester, reporter string, ratified bool, mutate func(map[string]string)) string {
	t.Helper()
	targetPreHead := testGit(t, f.repo, "rev-parse", "HEAD")
	request, err := f.workspace.Act(f.ctx, requester, app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "authorize the exact merge",
		Body: map[string]string{
			"to":         f.workspace.View().Actors[reporter].Fingerprint,
			"conditions": "lift do-not-merge only for the structured bindings in the report",
		},
		RestsOn:        []string{approval, f.implementationRequest},
		IdempotencyKey: "authorization-request-" + approval + "-" + requester + "-" + reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{
		"authorizes_candidate": f.candidate,
		"authorizes_approval":  approval,
		"authorizes_request":   f.implementationRequest,
		"target_pre_head":      targetPreHead,
	}
	if mutate != nil {
		mutate(body)
	}
	report, err := f.workspace.Act(f.ctx, reporter, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "authorize only the bound merge",
		Body: body, RestsOn: []string{request.Record.ID},
		IdempotencyKey: "authorization-report-" + approval + "-" + requester + "-" + reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ratified {
		if _, err := f.workspace.Act(f.ctx, requester, app.Act{
			Verb: app.VerbRatify, Target: report.Record.ID,
			IdempotencyKey: "ratify-authorization-" + approval + "-" + requester + "-" + reporter,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return report.Record.ID
}

func artifactByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Artifact {
	t.Helper()
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			return artifact
		}
	}
	t.Fatalf("artifact %s not found", event)
	return workroom.Artifact{}
}

func statementByEvent(t *testing.T, projection workroom.Projection, event string) workroom.Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement
		}
	}
	t.Fatalf("statement %s not found", event)
	return workroom.Statement{}
}

func testGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	output, err := gitCommand(repo, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

// gitCommand is testGit for callers that must report failure rather than end
// the test, such as the template builders that run once for many tests.
func gitCommand(repo string, arguments ...string) (string, error) {
	if repo != "" {
		arguments = append([]string{"-C", repo}, arguments...)
	}
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func TestStatusVerifyAndStateShareWorkroomAcrossLinkedCheckouts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", "-qb", "linked", linked)

	if err := stateCommand(ctx, []string{
		"--repo", repo, "--as", "operator", "--kind", "assert", "--text", "written from main",
		"--rests-on", seed.ID, "--idempotency-key", "main-write",
	}); err != nil {
		t.Fatalf("state from main checkout: %v", err)
	}
	if err := stateCommand(ctx, []string{
		"--repo", linked, "--as", "operator", "--kind", "assert", "--text", "written from linked checkout",
		"--rests-on", seed.ID, "--idempotency-key", "linked-write",
	}); err != nil {
		t.Fatalf("state from linked checkout: %v", err)
	}
	for name, checkout := range map[string]string{"main": repo, "linked": linked} {
		t.Run(name, func(t *testing.T) {
			if err := statusCommand(ctx, []string{"--repo", checkout, "--json"}); err != nil {
				t.Fatalf("status: %v", err)
			}
			if err := verifyCommand(ctx, []string{"--repo", checkout}); err != nil {
				t.Fatalf("verify: %v", err)
			}
		})
	}

	fromLinked, err := app.Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if fromLinked.GitDir == workspace.GitDir || fromLinked.CommonDir != workspace.CommonDir {
		t.Fatalf("checkout and repository scopes were conflated: main git=%q common=%q linked git=%q common=%q", workspace.GitDir, workspace.CommonDir, fromLinked.GitDir, fromLinked.CommonDir)
	}
	mainSnapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	linkedSnapshot, err := fromLinked.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mainSnapshot.Depth != 3 || linkedSnapshot.Depth != 3 || mainSnapshot.Head != linkedSnapshot.Head {
		t.Fatalf("commands did not share one durable sequence: main=%s/%d linked=%s/%d", mainSnapshot.Head, mainSnapshot.Depth, linkedSnapshot.Head, linkedSnapshot.Depth)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// A client should not have to be told where the service is. Serving publishes
// the address actually bound, beside the workroom config in the repository, and
// takes the advertisement back when it stops.
func TestServePublishesWhereItListensAndWithdrawsOnExit(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveCommand(ctx, []string{"--repo", repo, "--listen", "127.0.0.1:0"})
	}()

	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := advertised(workspace); ok {
			url = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		stop()
		t.Fatal("serving never published its address")
	}
	response, err := http.Get(url + "/v0/presence")
	if err != nil {
		stop()
		t.Fatalf("the published address does not answer: %v", err)
	}
	response.Body.Close()

	stop()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serving failed: %v", err)
	}
	if published, ok := advertised(workspace); ok {
		t.Fatalf("a stopped service is still advertised at %q", published)
	}
}

// The test above cancels a context that the command line never handed the
// command, so on its own it proves only that the withdrawal works when someone
// asks for it. What a person does is press Ctrl-C, and the whole point of the
// advertisement is that it must not outlive the process it names. These tests
// therefore run the real binary and stop it the real way.
func TestServeWithdrawsItsClaimOnSignalsAndRestartsCleanly(t *testing.T) {
	binary := buildGS(t)
	for name, signal := range map[string]os.Signal{
		"SIGINT":  os.Interrupt,
		"SIGTERM": syscall.SIGTERM,
	} {
		t.Run(name, func(t *testing.T) {
			repo, workspace := servableRepository(t)
			ref := app.ResidentRef(workspace.View().Genesis)

			serving := startServing(t, binary, repo)
			url := awaitPublication(t, workspace, "")
			response, err := http.Get(url + "/v0/presence")
			if err != nil {
				t.Fatalf("the published address does not answer: %v", err)
			}
			response.Body.Close()

			signalServing(t, serving, signal)
			if err := serving.Wait(); err != nil {
				t.Fatalf("stopping normally was reported as a failure: %v", err)
			}
			if published, ok := advertised(workspace); ok {
				t.Fatalf("a stopped service is still advertised at %q", published)
			}
			if _, present, err := workspace.Store.RefValue(context.Background(), ref); err != nil || present {
				t.Fatalf("a stopped service left its claim behind: present=%v err=%v", present, err)
			}

			// No manual repair lies between shutdown and restart.
			restarted := startServing(t, binary, repo)
			next := awaitPublication(t, workspace, url)
			if next == "" {
				t.Fatal("the repository did not restart after clean shutdown")
			}
			signalServing(t, restarted, signal)
			if err := restarted.Wait(); err != nil {
				t.Fatalf("stopping the restarted service: %v", err)
			}
		})
	}
}

// Two services on one repository is what the ownership claim prevents, and
// this is that at the executable level: the second real process refuses, says
// which address holds the repository, exits non-zero, and leaves the
// incumbent's claim and advertisement exactly as they were.
//
// This replaces a test that asserted the opposite — that a second service took
// the advertisement over, and that stopping the service it replaced left the
// successor advertised. That was an honest description of a world with no
// lock, and it is no longer reachable: there is no way to have two services
// running, so there is no replaced service to stop. The withdrawal guard that
// test protected is covered where it now lives, on the claim, in
// TestATakerRacingACleanShutdownYieldsOneOwnerInEitherOrder.
func TestASecondServeProcessRefusesAndLeavesTheIncumbentUntouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	binary := buildGS(t)
	repo, workspace := servableRepository(t)
	ref := app.ResidentRef(workspace.View().Genesis)

	serving := startServing(t, binary, repo)
	url := awaitPublication(t, workspace, "")
	held, present, err := workspace.Store.RefValue(ctx, ref)
	if err != nil || !present {
		t.Fatalf("a serving process holds no claim: present=%v err=%v", present, err)
	}

	// Ask for the incumbent's exact port. The ownership preflight must run
	// before bind, or this would lose the precise service-is-answering refusal
	// behind an opaque address-in-use error.
	output, err := exec.Command(binary, "serve", "--repo", repo, "--listen", strings.TrimPrefix(url, "http://")).CombinedOutput()
	if err == nil {
		t.Fatalf("a second gs serve started beside the one holding the repository: %s", output)
	}
	if !strings.Contains(string(output), url) {
		t.Fatalf("the refusal does not name the incumbent %q: %s", url, output)
	}
	if !strings.Contains(string(output), "pid ") {
		t.Fatalf("the refusal does not name the answering process: %s", output)
	}
	if strings.Contains(string(output), "update-ref -d") || strings.Contains(string(output), "remove the claim") {
		t.Fatalf("the live-service refusal offered claim deletion: %s", output)
	}
	if value, _, err := workspace.Store.RefValue(ctx, ref); err != nil || value != held {
		t.Fatalf("the refused process disturbed the claim: %q (was %q) err=%v", value, held, err)
	}
	if published, ok := advertised(workspace); !ok || published != url {
		t.Fatalf("the refused process took the advertisement: %q ok=%v", published, ok)
	}
	response, err := http.Get(url + "/v0/presence")
	if err != nil {
		t.Fatalf("the incumbent stopped answering: %v", err)
	}
	response.Body.Close()

	interrupt(t, serving)
	if err := serving.Wait(); err != nil {
		t.Fatalf("stopping normally was reported as a failure: %v", err)
	}
	if _, present, err := workspace.Store.RefValue(ctx, ref); err != nil || present {
		t.Fatalf("an interrupted service left its claim behind: present=%v err=%v", present, err)
	}

	// The repository is free again, so an ordinary start serves it.
	startServing(t, binary, repo)
	if next := awaitPublication(t, workspace, url); next == "" {
		t.Fatal("the repository could not be served after its owner stopped")
	}
}

// buildGS compiles the command as it is actually installed. Calling
// serveCommand in process cannot see the defect these tests exist to catch,
// because the defect was in what main hands the command.
func buildGS(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gs")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("building gs: %v: %s", err, output)
	}
	return binary
}

func TestInstalledHelpIsUsefulAndHasStableExitCodes(t *testing.T) {
	t.Parallel()
	binary := buildGS(t)
	tests := []struct {
		name     string
		args     []string
		exitCode int
		contains []string
	}{
		{name: "top help", args: []string{"--help"}, contains: []string{"usage: gs <command> [flags]", "docs/how-to/end-to-end.md", "docs/reference/gs/"}},
		{name: "help command", args: []string{"help"}, contains: []string{"usage: gs <command> [flags]", "docs/how-to/end-to-end.md"}},
		{name: "subcommand help", args: []string{"help", "work"}, contains: []string{"usage: gs work [flags]", "-as string", "-repo string", "-stale string"}},
		{name: "subcommand flag help", args: []string{"work", "--help"}, contains: []string{"usage: gs work [flags]", "-lane value", "-json"}},
		{name: "unknown flag", args: []string{"work", "--not-a-flag"}, exitCode: 1, contains: []string{"flag provided but not defined: -not-a-flag", "usage: gs work [flags]", "-as string"}},
		{name: "missing command", exitCode: 2, contains: []string{"usage: gs <command> [flags]", "docs/how-to/end-to-end.md"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := exec.Command(binary, test.args...).CombinedOutput()
			if test.exitCode == 0 && err != nil {
				t.Fatalf("help exited with %v: %s", err, output)
			}
			if test.exitCode != 0 {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != test.exitCode {
					t.Fatalf("exit = %v, want %d: %s", err, test.exitCode, output)
				}
			}
			for _, want := range test.contains {
				if !bytes.Contains(output, []byte(want)) {
					t.Errorf("output omits %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestLifecycleRefusalsTellTheActorWhatToDo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		message string
		want    string
	}{
		{message: "dangling promise has no request", want: "Add exactly one live request event with --rests-on"},
		{message: "report cites a request other than the one its promise answers", want: "File against the one live promise you made"},
		{message: "report rests on the request while promise event is live; report on the promise", want: "use the request directly only when you made no promise"},
	}
	for _, test := range tests {
		got := explainLifecycleRefusal(errors.New(test.message)).Error()
		if !strings.Contains(got, test.message) || !strings.Contains(got, test.want) || !strings.Contains(got, "docs/reference/gs/state.md#citing") {
			t.Errorf("guidance for %q = %q", test.message, got)
		}
	}
}

// These checks run the installed command rather than calling batchCommand in
// process. A returned error is not enough at this boundary: scripts only see
// the process status, and the positional file is passed to main before the
// command can decide where to read its acts.
func TestBatchProcessReadsItsFileAndReportsFailures(t *testing.T) {
	t.Parallel()
	binary := buildGS(t)

	t.Run("positional file", func(t *testing.T) {
		repo, workspace := servableRepository(t)
		seed := workspace.EventID(workspace.View().Genesis)
		path := filepath.Join(t.TempDir(), "batch.json")
		acts := fmt.Sprintf(`[{"verb":"state","kind":"assert","text":"the positional file was read","rests_on":[%q],"idempotency_key":"positional-file"}]`, seed)
		if err := os.WriteFile(path, []byte(acts), 0o600); err != nil {
			t.Fatal(err)
		}

		output, err := exec.Command(binary, "batch", "--repo", repo, "--as", "human", path).CombinedOutput()
		if err != nil {
			t.Fatalf("gs batch file: %v: %s", err, output)
		}
		var report batchReport
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatalf("decode batch report %q: %v", output, err)
		}
		if report.Landed != 1 || report.Error != nil {
			t.Fatalf("batch report = %#v, want one landed act and no error", report)
		}
	})

	t.Run("bad batch input exits nonzero", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "batch.json")
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(binary, "batch", "--repo", t.TempDir(), "--as", "human", path).CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Fatalf("gs batch bad input error = %v, output = %q; want non-zero exit", err, output)
		}
		if !bytes.Contains(output, []byte("read batch acts")) {
			t.Fatalf("gs batch bad input output = %q, want parse error", output)
		}
	})

	t.Run("missing flag value exits nonzero", func(t *testing.T) {
		output, err := exec.Command(binary, "batch", "--server").CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Fatalf("gs batch missing flag value error = %v, output = %q; want non-zero exit", err, output)
		}
		if !bytes.Contains(output, []byte("flag needs an argument")) {
			t.Fatalf("gs batch missing flag value output = %q, want flag error", output)
		}
	})
}

func servableRepository(t *testing.T) (string, *app.Workspace) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return repo, workspace
}

func startServing(t *testing.T, binary, repo string) *exec.Cmd {
	t.Helper()
	serving := exec.Command(binary, "serve", "--repo", repo, "--listen", "127.0.0.1:0")
	serving.Stdout, serving.Stderr = os.Stderr, os.Stderr
	if err := serving.Start(); err != nil {
		t.Fatalf("starting gs serve: %v", err)
	}
	t.Cleanup(func() {
		if serving.ProcessState == nil {
			_ = serving.Process.Kill()
			_ = serving.Wait()
		}
	})
	return serving
}

// awaitPublication waits for an advertisement other than the one already
// there, so a successor's record is never mistaken for its predecessor's.
func awaitPublication(t *testing.T, workspace *app.Workspace, previous string) string {
	t.Helper()
	for attempt := 0; attempt < 600; attempt++ {
		if published, ok := advertised(workspace); ok && published != previous {
			return published
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serving never published its address")
	return ""
}

func interrupt(t *testing.T, serving *exec.Cmd) {
	t.Helper()
	signalServing(t, serving, os.Interrupt)
}

func signalServing(t *testing.T, serving *exec.Cmd, signal os.Signal) {
	t.Helper()
	if err := serving.Process.Signal(signal); err != nil {
		t.Fatalf("signalling gs serve with %v: %v", signal, err)
	}
}

// The different-agent rule is a fingerprint test, applied where the verdict is
// signed rather than left to whoever remembers who did the work.
func TestReviewGuardRefusesVerdictOnTheReviewersOwnArtifact(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	own, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the reviewer's own implementation",
		Body:    map[string]string{"path": "feature.txt", "commit": fixture.candidate},
		RestsOn: []string{fixture.request}, IdempotencyKey: "self-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := fixture.snapshot(t).Depth
	err = reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", own.Record.ID, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	})
	if err == nil || !strings.Contains(err.Error(), "an independent reviewer must sign the verdict") {
		t.Fatalf("self-review error = %v", err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("self-review signed a verdict: depth %d -> %d", before, after)
	}
}

// A verdict written around the guard still cannot be merged: the projection
// answers the independence question and merge reads the answer.
func TestMergeGuardRefusesApprovalSignedByTheImplementer(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	own, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the reviewer's own implementation",
		Body:    map[string]string{"path": "feature.txt", "commit": fixture.candidate},
		RestsOn: []string{fixture.request}, IdempotencyKey: "self-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Build below the application write boundary: ordinary state surfaces now
	// refuse a verdict shape before signing it, and this test exists to prove
	// the merge gate still refuses a self-review on its own facts.
	_, private, err := fixture.workspace.Actor("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{
		Kind: workroom.KindReport, Text: "approving my own head",
		Body: map[string]string{"verdict": "approved", "head": fixture.candidate, "artifact": own.Record.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fixture.workspace.Store.WritePayloadTree(fixture.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := fixture.workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        []string{fixture.promise, fixture.request, own.Record.ID},
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "self-approval",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Submit(fixture.ctx, fixture.workspace.Store, kernel.Request{Signed: signed, Payload: payload}, kernel.Options{SigningKey: view.SequencerKey}); err != nil {
		t.Fatal(err)
	}
	selfFiled := fixture.snapshot(t).Projection.Statements[len(fixture.snapshot(t).Projection.Statements)-1].Event
	fixture.ratify(t, selfFiled)
	review, found := fixture.snapshot(t).Projection.Review(selfFiled)
	if !found || review.Independence != workroom.IndependenceSelfReview {
		t.Fatalf("projected review = %+v (found %v)", review, found)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", selfFiled,
	})
	if err == nil || !strings.Contains(err.Error(), "an independent review is required") {
		t.Fatalf("self-approved merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != base {
		t.Fatalf("refused merge moved HEAD to %s, want %s", got, base)
	}
}

// No command signs under a default name. The identity comes from the flag or
// from the environment that started the instance, and its absence is an error.
func TestSigningActorComesFromFlagThenEnvironmentAndOtherwiseFails(t *testing.T) {
	t.Setenv(actorEnvironment, "")
	if _, err := signingActor(""); err == nil || !strings.Contains(err.Error(), actorEnvironment) {
		t.Fatalf("missing identity error = %v", err)
	}
	if name, err := signingActor("explicit"); err != nil || name != "explicit" {
		t.Fatalf("flag identity = %q, %v", name, err)
	}
	t.Setenv(actorEnvironment, " claude.2 ")
	if name, err := signingActor(""); err != nil || name != "claude.2" {
		t.Fatalf("environment identity = %q, %v", name, err)
	}
	if name, err := signingActor("explicit"); err != nil || name != "explicit" {
		t.Fatalf("flag did not win over the environment: %q, %v", name, err)
	}
}

// The environment identity reaches a real durable act, not only the resolver.
func TestStateCommandSignsAsTheEnvironmentIdentity(t *testing.T) {
	fixture := newWorkflowFixture(t)
	t.Setenv(actorEnvironment, "reviewer")
	if err := stateCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--kind", "assert", "--text", "signed without --as",
		"--rests-on", fixture.artifact,
	}); err != nil {
		t.Fatal(err)
	}
	projection := fixture.snapshot(t).Projection
	last := projection.Statements[len(projection.Statements)-1]
	if last.Actor != fixture.workspace.View().Actors["reviewer"].Fingerprint {
		t.Fatalf("environment identity signed as %s", last.Actor)
	}
}

// gs init is where "there is no default identity" is easiest to break, because
// it is the one command with nobody to sign as yet. Seeding an operator named
// by nothing but a flag default puts an identity nobody chose at the root of
// the log.
func TestInitRefusesToSeedAnOperatorNobodyChose(t *testing.T) {
	t.Setenv(actorEnvironment, "")
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-b", "main", repo)
	err := initCommand(context.Background(), []string{"--repo", repo})
	if err == nil || !strings.Contains(err.Error(), actorEnvironment) {
		t.Fatalf("init without an identity = %v", err)
	}
	if !strings.Contains(err.Error(), "--operator") {
		t.Fatalf("the refusal does not name the flag that carries the identity: %v", err)
	}
	if _, err := app.Open(context.Background(), repo); err == nil {
		t.Fatal("a refused init still made a workroom")
	}

	// The environment identity is a choice, so it seeds; and it seeds under
	// that name, not under a default.
	t.Setenv(actorEnvironment, "alice")
	if err := initCommand(context.Background(), []string{"--repo", repo}); err != nil {
		t.Fatal(err)
	}
	workspace, err := app.Open(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := workspace.View().Actors["alice"]; !exists {
		t.Fatalf("the operator was not seeded as alice: %v", workspace.View().Actors)
	}
	if _, exists := workspace.View().Actors["operator"]; exists {
		t.Fatal("init seeded a default operator beside the chosen one")
	}
}

// Retiring an artifact that documentation still names leaves those pages
// resting on a withdrawn pointer, which the documentation gate refuses: the
// repository goes red, and the act that did it is already in an append-only
// log. Twice in one session a retirement did exactly that, both times through
// gs batch rather than gs supersede, so both paths are covered here.
func writeCitingPage(t *testing.T, repo, page, event string) {
	t.Helper()
	full := filepath.Join(repo, page)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("---\nbasis:\n  - "+event+"\n---\n\nprose\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", page)
}

func TestSupersedeRefusesWhenDocumentationCitesTheTarget(t *testing.T) {
	t.Parallel()
	f := newBatchFixture(t)
	target := f.genesis
	writeCitingPage(t, f.repo, "docs/reference/thing.md", target)

	err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--text", "retire it", target})
	if err == nil {
		t.Fatal("supersede was allowed while a page still cited the target")
	}
	if !strings.Contains(err.Error(), "docs/reference/thing.md") {
		t.Errorf("the refusal must name the page to repoint, got %v", err)
	}

	// The escape exists for migrations that retire first and re-anchor after,
	// and it must be asked for rather than assumed.
	if err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--text", "retire it", "--cited-ok", target}); err != nil {
		t.Errorf("--cited-ok must allow the retirement, got %v", err)
	}
}

func TestBatchRefusesRetirementWhenDocumentationCitesTheTarget(t *testing.T) {
	f := newBatchFixture(t)
	target := f.genesis
	writeCitingPage(t, f.repo, "docs/concepts/other.md", target)

	acts := `[{"verb":"supersede","target":"` + target + `","text":"retire it"}]`
	printed, err := f.runFile("operator", acts)
	if err == nil {
		t.Fatal("batch was allowed while a page still cited a target")
	}
	if !strings.Contains(err.Error(), "docs/concepts/other.md") {
		t.Errorf("the refusal must name the page, got %v", err)
	}
	// Refused before the first append, so nothing is reported and nothing
	// landed: a batch that cannot land cleanly lands nothing.
	if strings.TrimSpace(string(printed)) != "" {
		t.Errorf("a batch refused before appending must print nothing, got %q", printed)
	}
}

// An untracked page is not what the gate reads, so it must not block anyone.
func TestRetirementIgnoresUntrackedPages(t *testing.T) {
	t.Parallel()
	f := newBatchFixture(t)
	target := f.genesis
	full := filepath.Join(f.repo, "scratch.md")
	if err := os.WriteFile(full, []byte(target), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--text", "retire it", target}); err != nil {
		t.Errorf("an untracked page must not block a retirement, got %v", err)
	}
}

// Two services on one repository is the case this refuses. The durable log
// stays correct either way — appends are compare-and-swap on a Git ref — but
// presence and ephemeral conversation are per-process, so a second resident
// would form a second room whose participants never see the first and are never
// told. The second start therefore refuses, names the incumbent, leaves the
// claim and the advertisement alone, and serves nothing.
func TestASecondServeRefusesWhileTheFirstHoldsTheRepository(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := app.ResidentRef(workspace.View().Genesis)

	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveCommand(ctx, []string{"--repo", repo, "--listen", "127.0.0.1:0"})
	}()

	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := advertised(workspace); ok {
			url = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		stop()
		t.Fatal("serving never published its address")
	}
	held, present, err := workspace.Store.RefValue(ctx, ref)
	if err != nil || !present {
		stop()
		t.Fatalf("a serving process holds no claim: present=%v err=%v", present, err)
	}

	second := serveCommand(context.Background(), []string{"--repo", repo, "--listen", strings.TrimPrefix(url, "http://")})
	var refusal *app.ResidentHeldError
	if !errors.As(second, &refusal) {
		stop()
		t.Fatalf("a second serve on a held repository returned %v", second)
	}
	if refusal.URL != url {
		stop()
		t.Fatalf("the refusal named %q, not the incumbent %q", refusal.URL, url)
	}
	if value, _, err := workspace.Store.RefValue(ctx, ref); err != nil || value != held {
		stop()
		t.Fatalf("the refused start disturbed the claim: %q (was %q) err=%v", value, held, err)
	}
	if published, ok := advertised(workspace); !ok || published != url {
		stop()
		t.Fatalf("the refused start advertised itself: %q ok=%v", published, ok)
	}

	stop()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serving failed: %v", err)
	}
	// Stopping releases the claim, so the next start is an ordinary one rather
	// than a recovery.
	if _, present, err := workspace.Store.RefValue(context.Background(), ref); err != nil || present {
		t.Fatalf("a stopped service left its claim behind: present=%v err=%v", present, err)
	}

	next, stopNext := context.WithCancel(context.Background())
	defer stopNext()
	after := make(chan error, 1)
	go func() {
		after <- serveCommand(next, []string{"--repo", repo, "--listen", "127.0.0.1:0"})
	}()
	var successor string
	for attempt := 0; attempt < 300 && successor == ""; attempt++ {
		if published, ok := advertised(workspace); ok && published != url {
			successor = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if successor == "" {
		t.Fatal("the repository could not be served again after its owner stopped")
	}
	stopNext()
	if err := <-after; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("the successor failed: %v", err)
	}
}

// A claim left behind by a process that died without releasing must not wedge
// the repository. The next start probes the address, finds nothing listening,
// and takes the claim over in one compare-and-swap.
func TestServeRecoversAClaimLeftByADeadOwner(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := app.ResidentRef(workspace.View().Genesis)

	// An address nothing is listening on, which is what a crashed owner leaves.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := dead.Addr().String()
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	abandoned, err := json.Marshal(app.ResidentClaim{
		Genesis: workspace.View().Genesis,
		URL:     "http://" + address,
		Nonce:   "00000000000000000000000000000000",
		PID:     os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := workspace.Store.WriteBlob(ctx, abandoned)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Store.UpdateRef(ctx, ref, blob, ""); err != nil {
		t.Fatal(err)
	}

	errReader, errWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStderr := os.Stderr
	os.Stderr = errWriter
	defer func() {
		os.Stderr = previousStderr
		_ = errWriter.Close()
		_ = errReader.Close()
	}()

	serving, stop := context.WithCancel(ctx)
	defer stop()
	served := make(chan error, 1)
	started := time.Now()
	go func() {
		// Restart on the exact stale port. The post-bind acquisition must consume
		// the pre-bind proof rather than probing its own unserved listener.
		served <- serveCommand(serving, []string{"--repo", repo, "--listen", address})
	}()
	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := advertised(workspace); ok {
			url = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		t.Fatal("a claim left by a dead owner wedged the repository")
	}
	if elapsed := time.Since(started); elapsed >= residentProbeTimeout {
		t.Fatalf("connection-refused recovery took %s; it should not wait for the probe timeout", elapsed)
	}
	if value, present, err := workspace.Store.RefValue(ctx, ref); err != nil || !present || value == blob {
		t.Fatalf("the abandoned claim was not taken over: %q present=%v err=%v", value, present, err)
	}
	stop()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serving failed: %v", err)
	}
	os.Stderr = previousStderr
	if err := errWriter.Close(); err != nil {
		t.Fatal(err)
	}
	warned, err := io.ReadAll(errReader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(warned, []byte("reclaimed stale resident claim after http://"+address+" refused the liveness probe")) {
		t.Fatalf("automatic recovery was not logged: %s", warned)
	}
}
