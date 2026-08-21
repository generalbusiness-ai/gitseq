package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestValidateLoopbackListen(t *testing.T) {
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

func TestProfilerIsDisabledByDefaultAndLoopbackOnly(t *testing.T) {
	stop, err := serveProfiler(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := serveProfiler(context.Background(), "0.0.0.0:0"); err == nil {
		t.Fatal("profiler accepted a non-loopback listener")
	}
}

func TestValidateLoopbackServer(t *testing.T) {
	tests := map[string]bool{
		"http://127.0.0.1:7777":   true,
		"http://[::1]:7777":       true,
		"http://localhost:7777":   true,
		"https://127.0.0.1:7777":  false,
		"http://user@127.0.0.1:7": false,
		"http://192.0.2.1:7777":   false,
		"http://0.0.0.0:7777":     false,
		"http://127.0.0.1/x":      false,
		"http://127.0.0.1/?x=y":   false,
		"not-a-url":               false,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			if got := validateLoopbackServer(raw) == nil; got != want {
				t.Fatalf("validateLoopbackServer(%q) success = %v, want %v", raw, got, want)
			}
		})
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
		basis := workspace.EventID(workspace.Config.Genesis)
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
	current := testGit(t, workspace.Repo, "rev-parse", kernel.Ref(workspace.Config.Genesis))
	if current == summary.Durable.Head {
		t.Fatal("moving-head fixture did not move the durable ref")
	}
}

func TestSlowLocalAuditReportsProgressWithoutChangingTheResult(t *testing.T) {
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
	pointer := filepath.Join(workspace.MetaDir, "checkpoints", workspace.Config.Genesis+".json")
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
	if head := testGit(t, repo, "rev-parse", kernel.CheckpointRef(workspace.Config.Genesis)); head != workspace.Config.Genesis {
		t.Fatalf("checkpoint ref = %s, want genesis %s", head, workspace.Config.Genesis)
	}
	var result map[string]string
	if err := json.Unmarshal(printed, &result); err != nil || result["checkpoint"] != "cleared" || result["genesis"] != workspace.Config.Genesis {
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
	ref := "refs/seq/" + workspace.Config.Genesis
	first := testGit(t, source, "rev-parse", ref)
	testGit(t, "", "init", "--bare", remote)
	testGit(t, source, "remote", "add", "origin", remote)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")

	testGit(t, "", "clone", remote, auditor)
	testGit(t, auditor, "config", "--add", "remote.origin.fetch", forcedSequenceFetchRefspec)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != first {
		t.Fatalf("initial sequence head = %s, want %s", got, first)
	}
	attached, err := app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Config.VerifiedFrontier == nil || attached.Config.VerifiedFrontier.Head != first {
		t.Fatalf("initial verified frontier = %+v, want head %s", attached.Config.VerifiedFrontier, first)
	}
	fetchRules := strings.Fields(testGit(t, auditor, "config", "--get-all", "remote.origin.fetch"))
	if contains(fetchRules, forcedSequenceFetchRefspec) || !contains(fetchRules, sequenceFetchRefspec) {
		t.Fatalf("sequence fetch rules = %#v, want only non-forcing sequence rule", fetchRules)
	}

	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "advance",
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "advance",
	}); err != nil {
		t.Fatal(err)
	}
	second := testGit(t, source, "rev-parse", ref)
	testGit(t, source, "push", "origin", "refs/seq/*:refs/seq/*")
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("forward attach: %v", err)
	}
	if got := testGit(t, auditor, "rev-parse", ref); got != second {
		t.Fatalf("forward sequence head = %s, want %s", got, second)
	}
	attached, err = app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Config.VerifiedFrontier == nil || attached.Config.VerifiedFrontier.Head != second {
		t.Fatalf("forward verified frontier = %+v, want head %s", attached.Config.VerifiedFrontier, second)
	}

	testGit(t, "", "--git-dir", remote, "update-ref", ref, first, second)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err == nil {
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
	ref := "refs/seq/" + workspace.Config.Genesis
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
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("initial attach: %v", err)
	}

	if err := workspace.Store.UpdateRef(ctx, ref, base, trusted); err != nil {
		t.Fatal(err)
	}
	attack, err := kernel.Submit(ctx, workspace.Store, replayed, kernel.Options{SigningKey: workspace.Config.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, source, "push", "origin", "+"+ref+":"+ref)
	// A first-time auditor has no earlier frontier to compare. The truncated
	// branch is internally signed and must remain attachable; detecting that it
	// omitted prior history needs a witness or trusted checkpoint.
	testGit(t, "", "clone", remote, freshAuditor)
	if err := attachCommand(ctx, []string{"--repo", freshAuditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err != nil {
		t.Fatalf("fresh auditor rejected internally valid truncated branch: %v", err)
	}
	fresh, err := app.Open(ctx, freshAuditor)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Config.VerifiedFrontier == nil || fresh.Config.VerifiedFrontier.Head != attack.Head {
		t.Fatalf("fresh auditor frontier = %+v, want attack head %s", fresh.Config.VerifiedFrontier, attack.Head)
	}
	// Losing the tracking ref defeats Git's non-force comparison, but it must
	// not erase the separately persisted verified frontier.
	testGit(t, auditor, "update-ref", "-d", ref)
	if err := attachCommand(ctx, []string{"--repo", auditor, "--remote", "origin", "--genesis", workspace.Config.Genesis}); err == nil || !strings.Contains(err.Error(), "non-descendant verified frontier") {
		t.Fatalf("attach accepted replay branch %s after losing its ref: %v", attack.Head, err)
	}
	attached, err := app.Open(ctx, auditor)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Config.VerifiedFrontier == nil || attached.Config.VerifiedFrontier.Head != trusted {
		t.Fatalf("rejected replay replaced trusted frontier: %+v, want %s", attached.Config.VerifiedFrontier, trusted)
	}
	if verification, err := kernel.Verify(ctx, attached.Store, workspace.Config.Genesis); err != nil || verification.Head != attack.Head {
		t.Fatalf("attack branch was not independently valid, verification=%+v err=%v", verification, err)
	}
}

func TestReviewGuardAcceptsExactCleanArtifactHead(t *testing.T) {
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
	fixture := newWorkflowFixture(t)
	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "other-reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	foreignRequest, err := fixture.workspace.Act(fixture.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review assigned to someone else",
		Body: map[string]string{
			"to": fixture.workspace.Config.Actors["other-reviewer"].Fingerprint, "conditions": "exact head",
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
	fixture := newWorkflowFixture(t)
	calls := 0
	validate := func(ctx context.Context, workspace *app.Workspace, actorName, checkout, artifact, promise string) (reviewBasis, error) {
		basis, err := validateReview(ctx, workspace, actorName, checkout, artifact, promise)
		calls++
		if calls == 2 && err == nil {
			basis.Request += "-changed"
		}
		return basis, err
	}
	before := fixture.snapshot(t).Depth
	err := reviewCommandWithValidator(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text", "must not be signed",
	}, validate)
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

func TestMergeGuardMergesOnlyRatifiedApprovedExactHead(t *testing.T) {
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
		durable.Body["merge_target_pre_head"] != targetPreHead || durable.Body["merge_head"] != mergeHead {
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

func TestMergeGuardIgnoresReplacementForApprovedCandidate(t *testing.T) {
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

func TestMergeLeavesAnUnrelatedCandidateArtifactLive(t *testing.T) {
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
		RestsOn: []string{fixture.workspace.EventID(fixture.workspace.Config.Genesis)}, IdempotencyKey: "unrelated-candidate",
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
}

// The deadlock this repository actually hit. Documentation names the exact
// artifacts a merge must retire — that is what `rests_on` is for — so refusing
// every cited retirement refused every documented-area merge, and the advice to
// repoint the pages first was unsatisfiable because the successor does not
// exist until the merge lands. A retirement this same merge succeeds must
// therefore go through, and the pages that named it flare rather than break.
func TestMergeLandsWhenTheCitedPredecessorGetsASuccessor(t *testing.T) {
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
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	// A pointer belonging to another actor, at a path this head never reviewed.
	stranger, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "another actor's pointer elsewhere",
		Body:    map[string]string{"path": "elsewhere.txt", "commit": testGit(t, fixture.repo, "rev-parse", "HEAD")},
		RestsOn: []string{fixture.workspace.EventID(fixture.workspace.Config.Genesis)}, IdempotencyKey: "stranger-elsewhere",
	})
	if err != nil {
		t.Fatal(err)
	}
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")
	beforeDepth := fixture.snapshot(t).Depth
	snapshot := fixture.snapshot(t)
	plan := successionPlan{publish: []string{"elsewhere.txt"},
		retire: map[string]string{stranger.Record.ID: "elsewhere.txt"}}
	if err := refuseUnreachableCrossAuthorRetirements(snapshot.Projection, plan, approval,
		fixture.workspace.Config.Actors["operator"].Fingerprint); err == nil ||
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
	fixture := newWorkflowFixtureRemoving(t, true)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	writeCitingPage(t, fixture.repo, "docs/reference/base.md", fixture.ground)
	testGit(t, fixture.repo, "commit", "-m", "cite the live base artifact")
	before := testGit(t, fixture.repo, "rev-parse", "HEAD")

	err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "This merge must be refused before it changes the target.",
	})
	if err == nil || !strings.Contains(err.Error(), "docs/reference/base.md") {
		t.Fatalf("cited bare retirement merge error = %v", err)
	}
	if got := testGit(t, fixture.repo, "rev-parse", "HEAD"); got != before {
		t.Fatalf("refused merge moved target to %s, want %s", got, before)
	}
	if _, err := git(fixture.ctx, fixture.repo, "show-ref", "--verify", mergeReceiptRef(approval)); err == nil {
		t.Fatal("refused merge left a receipt reservation")
	}
}

// The preflight replayed on its own, the way the review that found the deadlock
// replayed it: the real citing checkout, and the three plan shapes that decide
// the outcome.
func TestMergePreflightSeparatesSucceededRetirementFromOrphaning(t *testing.T) {
	fixture := newWorkflowFixture(t)
	writeCitingPage(t, fixture.repo, "docs/reference/feature.md", fixture.artifact)
	testGit(t, fixture.repo, "commit", "-m", "cite the live feature artifact")

	succeeded := successionPlan{publish: []string{"feature.txt"},
		retire: map[string]string{fixture.artifact: "feature.txt"}}
	if err := preflightSuccession(fixture.ctx, fixture.workspace, fixture.repo, succeeded); err != nil {
		t.Fatalf("succeeded retirement of a cited predecessor was refused: %v", err)
	}
	wider := successionPlan{publish: []string{"docs"},
		retire: map[string]string{fixture.artifact: "docs"}}
	if err := preflightSuccession(fixture.ctx, fixture.workspace, fixture.repo, wider); err != nil {
		t.Fatalf("succession to a covering directory was refused: %v", err)
	}
	bare := successionPlan{retire: map[string]string{fixture.artifact: ""}}
	if err := preflightSuccession(fixture.ctx, fixture.workspace, fixture.repo, bare); err == nil ||
		!strings.Contains(err.Error(), "docs/reference/feature.md") {
		t.Fatalf("bare retirement of a cited predecessor error = %v", err)
	}
	unpublished := successionPlan{publish: []string{"docs"},
		retire: map[string]string{fixture.artifact: "feature.txt"}}
	if err := preflightSuccession(fixture.ctx, fixture.workspace, fixture.repo, unpublished); err == nil ||
		!strings.Contains(err.Error(), "does not publish") {
		t.Fatalf("successor this merge never publishes error = %v", err)
	}
}

func TestMergeRetryResumesPartlyLandedSuccessionWithoutRemerging(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	predecessors := successionPredecessors(fixture.ctx, fixture.repo, snapshot.Projection, targetPreHead, fixture.candidate)
	message, err := mergeReceiptMessage("Merge the approved feature.", approval, fixture.candidate, targetPreHead, "", planSuccession(snapshot.Projection, changes, predecessors))
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
	predecessors = successionPredecessors(fixture.ctx, fixture.repo, snapshot.Projection, targetPreHead, fixture.candidate)
	plan := planSuccession(snapshot.Projection, changes, predecessors)
	acts := successionActs(approval, fixture.candidate, targetPreHead, mergeHead, "", plan)
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
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	targetPreHead := testGit(t, fixture.repo, "rev-parse", "HEAD")
	changes, err := mergeChangesBetween(fixture.ctx, fixture.repo, targetPreHead, fixture.candidate)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	predecessors := successionPredecessors(fixture.ctx, fixture.repo, snapshot.Projection, targetPreHead, fixture.candidate)
	sealed := planSuccession(snapshot.Projection, changes, predecessors)
	message, err := mergeReceiptMessage("Merge the approved feature.", approval, fixture.candidate, targetPreHead, "", sealed)
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
	if !artifactByEvent(t, snapshot.Projection, later.Record.ID).Stale {
		t.Fatal("post-merge descendant did not flare when its predecessor retired")
	}
}

func TestMergeGuardConsumesApprovalOnceAcrossTargets(t *testing.T) {
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

// World staleness is not repaired by repeating reasoning: the implementation
// pointer itself follows behaviour that was replaced and must be re-anchored.
func TestMergeGuardStillRefusesAMovedWorldAfterAnApprovedReview(t *testing.T) {
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
	if _, err := liveArtifact(projection, "ordinary-artifact"); err != nil {
		t.Fatalf("ordinary reasoning-stale artifact was refused: %v", err)
	}
	if _, err := liveStatement(projection, "ordinary-report", workroom.KindReport); err != nil {
		t.Fatalf("ordinary reasoning-stale report was refused: %v", err)
	}
	if _, err := liveArtifact(projection, "world-artifact"); err == nil || !strings.Contains(err.Error(), "superseded world") {
		t.Fatalf("world-stale artifact error = %v", err)
	}
	if _, err := liveStatement(projection, "world-report", workroom.KindReport); err == nil || !strings.Contains(err.Error(), "superseded world") {
		t.Fatalf("world-stale report error = %v", err)
	}
	if paths := reviewedPaths(projection, "approval"); !slices.Equal(paths, []string{"ordinary"}) {
		t.Fatalf("reviewed paths = %v, want only the ordinary-stale artifact", paths)
	}
}

func TestMergeGuardRefusesUnratifiedApproval(t *testing.T) {
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
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + fixture.workspace.Config.ObjectFormat + ":" + fixture.workspace.Config.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + fixture.workspace.Config.ObjectFormat + ":" + tree,
		RestsOn:        []string{fixture.promise, fixture.request},
		IdempotencyNS:  fixture.workspace.Config.IdempotencyNamespace,
		IdempotencyKey: "ungrounded-artifact-approval",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	approvalSubmission, err := fixture.workspace.AcceptSubmission(fixture.ctx, kernel.Request{Signed: signed, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	approval := approvalSubmission.Record.ID
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
	// The second act is addressed to an actor the roster does not carry yet,
	// so it cannot be signed and the chain stops after the first act.
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
		t.Fatal("batch naming an unknown performer succeeded, so no prefix was left partly landed")
	}
	if first.Landed != 1 || first.Replayed != 0 || first.Error == nil {
		t.Fatalf("partial run report = %#v", first)
	}
	if first.Acts[0].Outcome != "landed" || first.Acts[1].Outcome != "failed" {
		t.Fatalf("partial run acts = %#v", first.Acts)
	}
	partial := fixture.snapshot()
	if partial.Depth != before.Depth+1 {
		t.Fatalf("partial run depth = %d, want %d", partial.Depth, before.Depth+1)
	}

	if _, _, err := fixture.workspace.AddActor(fixture.ctx, "operator", "latecomer", "agent"); err != nil {
		t.Fatal(err)
	}
	admitted := fixture.snapshot()

	second, err := fixture.run("operator", acts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Landed != 1 || second.Replayed != 1 || second.Error != nil {
		t.Fatalf("retry report = %#v", second)
	}
	if second.Acts[0].Outcome != "replayed" || second.Acts[0].Event != first.Acts[0].Event {
		t.Fatalf("retry act 0 = %#v, want the first run's event %s replayed", second.Acts[0], first.Acts[0].Event)
	}
	if second.Acts[1].Outcome != "landed" || second.Acts[1].Event == "" {
		t.Fatalf("retry act 1 = %#v, want the suffix landed", second.Acts[1])
	}
	after := fixture.snapshot()
	if after.Depth != admitted.Depth+1 {
		t.Fatalf("retry depth = %d, want %d: only the suffix should have landed", after.Depth, admitted.Depth+1)
	}
	if !contains(after.Projection.Provenance[second.Acts[1].Event], first.Acts[0].Event) {
		t.Fatalf("suffix provenance = %#v, want the replayed prefix event %s",
			after.Projection.Provenance[second.Acts[1].Event], first.Acts[0].Event)
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

// A kind this workroom does not define is admitted and stays visible, and the
// fold still calls it undefined-kind. Nothing about that changes: the record
// was right about the two review promises filed as kind "commit", and hiding
// the attempt would be worse than keeping it.
func TestStateWithAnUndefinedKindStillLandsAndProjectsUndefinedKind(t *testing.T) {
	fixture := newBatchFixture(t)
	before := fixture.snapshot().Depth
	printed, _, err := fixture.state("operator", "commit", "I will re-review task/x at exact head y", "undefined-kind-lands")
	if err != nil {
		t.Fatalf("state with an undefined kind failed: %v", err)
	}
	event := strings.TrimSpace(printed)
	if !strings.Contains(event, "#git:") {
		t.Fatalf("state printed no event id: %q", printed)
	}
	snapshot := fixture.snapshot()
	if snapshot.Depth != before+1 {
		t.Fatalf("depth = %d, want %d", snapshot.Depth, before+1)
	}
	decision := decisionByEvent(t, snapshot.Projection, event)
	if decision.Verdict != workroom.UndefinedKind {
		t.Fatalf("verdict = %q (%s), want %q", decision.Verdict, decision.Reason, workroom.UndefinedKind)
	}
}

// What changes is that the author finds out at once. The act carried what
// reads in English as a promise; it formed no promise, and only the writing
// path is in a position to say so before the author acts on the belief.
func TestStateWithAnUndefinedKindWarnsTheAuthorOnStandardError(t *testing.T) {
	fixture := newBatchFixture(t)
	_, warned, err := fixture.state("operator", "commit", "I will re-review task/x at exact head y", "undefined-kind-warns")
	if err != nil {
		t.Fatalf("state with an undefined kind failed: %v", err)
	}
	for _, want := range []string{`"commit"`, "no rule reads it", "undefined-kind", "does not form", "kinds defined here:"} {
		if !strings.Contains(warned, want) {
			t.Fatalf("warning %q does not say %q", warned, want)
		}
	}
	for _, definition := range fixture.snapshot().Vocabulary.Definitions {
		if !strings.Contains(warned, string(definition.Name)) {
			t.Fatalf("warning %q does not list the defined kind %q", warned, definition.Name)
		}
	}
	// A defined kind is ordinary work and says nothing, so the warning cannot
	// pass by being printed every time.
	_, quiet, err := fixture.state("operator", "assert", "an ordinary claim", "defined-kind-is-quiet")
	if err != nil {
		t.Fatalf("state with a defined kind failed: %v", err)
	}
	if strings.TrimSpace(quiet) != "" {
		t.Fatalf("a defined kind warned anyway: %q", quiet)
	}
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
	testGit(t, "", "init", "-b", "main", repo)
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	return batchFixture{t: t, ctx: ctx, repo: repo, workspace: workspace, genesis: workspace.EventID(workspace.Config.Genesis)}
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
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), "batch.json")
	if err := os.WriteFile(path, []byte(acts), 0o600); err != nil {
		f.t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		f.t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	batchErr := batchCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, path})
	os.Stdout = stdout
	writer.Close()
	printed, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		f.t.Fatal(err)
	}
	return printed, batchErr
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

// state runs the state command the way a person does and returns what each
// stream received, because which stream carried the warning is the point.
func (f batchFixture) state(actor, kind, text, key string) (string, string, error) {
	f.t.Helper()
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
	stateErr := stateCommand(f.ctx, []string{
		"--repo", f.repo, "--as", actor, "--kind", kind, "--text", text,
		"--rests-on", f.genesis, "--idempotency-key", key,
	})
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
	t         *testing.T
	ctx       context.Context
	repo      string
	feature   string
	workspace *app.Workspace
	candidate string
	artifact  string
	ground    string
	request   string
	promise   string
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
	testGit(t, "", "init", "-b", "main", repo)
	testGit(t, repo, "config", "user.name", "Test")
	testGit(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", "base.txt")
	testGit(t, repo, "commit", "-m", "base")
	workspace, _, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "operator", "reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "worktree", "add", "-b", "feature", feature)
	if err := os.WriteFile(filepath.Join(feature, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, feature, "add", "feature.txt")
	if removeBase {
		testGit(t, feature, "rm", "-q", "base.txt")
	}
	testGit(t, feature, "commit", "-m", "feature")
	candidate := testGit(t, feature, "rev-parse", "HEAD")
	// The feature stands on the base of the repository, exactly as ordinary
	// work stands on whatever main was when it started. Retiring this is how a
	// test moves the world without touching the feature commit.
	groundSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "repository base",
		Body:    map[string]string{"path": "base.txt", "commit": testGit(t, repo, "rev-parse", "HEAD")},
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "ground",
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "feature artifact",
		Body:    map[string]string{"path": "feature.txt", "commit": candidate},
		RestsOn: []string{groundSubmission.Record.ID}, IdempotencyKey: "artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	requestSubmission, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review feature",
		Body:    map[string]string{"to": workspace.Config.Actors["reviewer"].Fingerprint, "conditions": "exact head"},
		RestsOn: []string{artifactSubmission.Record.ID}, IdempotencyKey: "review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promiseSubmission, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review exact head",
		RestsOn: []string{requestSubmission.Record.ID}, IdempotencyKey: "review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workflowFixture{
		t: t, ctx: ctx, repo: repo, feature: feature, workspace: workspace,
		candidate: candidate, artifact: artifactSubmission.Record.ID, ground: groundSubmission.Record.ID,
		request: requestSubmission.Record.ID, promise: promiseSubmission.Record.ID,
	}
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
	if repo != "" {
		arguments = append([]string{"-C", repo}, arguments...)
	}
	output, err := exec.Command("git", arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestStatusVerifyAndStateShareWorkroomAcrossLinkedCheckouts(t *testing.T) {
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
		if published, ok := workspace.ResidentURL(); ok {
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
	if published, ok := workspace.ResidentURL(); ok {
		t.Fatalf("a stopped service is still advertised at %q", published)
	}
}

// The test above cancels a context that the command line never handed the
// command, so on its own it proves only that the withdrawal works when someone
// asks for it. What a person does is press Ctrl-C, and the whole point of the
// advertisement is that it must not outlive the process it names. These tests
// therefore run the real binary and stop it the real way.
func TestServeWithdrawsWhenTheProcessIsInterrupted(t *testing.T) {
	binary := buildGS(t)
	repo, workspace := servableRepository(t)

	serving := startServing(t, binary, repo)
	url := awaitPublication(t, workspace, "")
	response, err := http.Get(url + "/v0/presence")
	if err != nil {
		t.Fatalf("the published address does not answer: %v", err)
	}
	response.Body.Close()

	interrupt(t, serving)
	if err := serving.Wait(); err != nil {
		t.Fatalf("stopping normally was reported as a failure: %v", err)
	}
	if published, ok := workspace.ResidentURL(); ok {
		t.Fatalf("an interrupted service is still advertised at %q", published)
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
	ctx := context.Background()
	binary := buildGS(t)
	repo, workspace := servableRepository(t)
	ref := app.ResidentRef(workspace.Config.Genesis)

	serving := startServing(t, binary, repo)
	url := awaitPublication(t, workspace, "")
	held, present, err := workspace.Store.RefValue(ctx, ref)
	if err != nil || !present {
		t.Fatalf("a serving process holds no claim: present=%v err=%v", present, err)
	}

	output, err := exec.Command(binary, "serve", "--repo", repo, "--listen", "127.0.0.1:0").CombinedOutput()
	if err == nil {
		t.Fatalf("a second gs serve started beside the one holding the repository: %s", output)
	}
	if !strings.Contains(string(output), url) {
		t.Fatalf("the refusal does not name the incumbent %q: %s", url, output)
	}
	if value, _, err := workspace.Store.RefValue(ctx, ref); err != nil || value != held {
		t.Fatalf("the refused process disturbed the claim: %q (was %q) err=%v", value, held, err)
	}
	if published, ok := workspace.ResidentURL(); !ok || published != url {
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

// These checks run the installed command rather than calling batchCommand in
// process. A returned error is not enough at this boundary: scripts only see
// the process status, and the positional file is passed to main before the
// command can decide where to read its acts.
func TestBatchProcessReadsItsFileAndReportsFailures(t *testing.T) {
	binary := buildGS(t)

	t.Run("positional file", func(t *testing.T) {
		repo, workspace := servableRepository(t)
		seed := workspace.EventID(workspace.Config.Genesis)
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
		if published, ok := workspace.ResidentURL(); ok && published != previous {
			return published
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("serving never published its address")
	return ""
}

func interrupt(t *testing.T, serving *exec.Cmd) {
	t.Helper()
	if err := serving.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupting gs serve: %v", err)
	}
}

// The different-agent rule is a fingerprint test, applied where the verdict is
// signed rather than left to whoever remembers who did the work.
func TestReviewGuardRefusesVerdictOnTheReviewersOwnArtifact(t *testing.T) {
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
	fixture := newWorkflowFixture(t)
	own, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "the reviewer's own implementation",
		Body:    map[string]string{"path": "feature.txt", "commit": fixture.candidate},
		RestsOn: []string{fixture.request}, IdempotencyKey: "self-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := fixture.workspace.Act(fixture.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "approving my own head",
		Body:    map[string]string{"verdict": "approved", "head": fixture.candidate, "artifact": own.Record.ID},
		RestsOn: []string{fixture.promise, fixture.request, own.Record.ID}, IdempotencyKey: "self-approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.ratify(t, approval.Record.ID)
	review, found := fixture.snapshot(t).Projection.Review(approval.Record.ID)
	if !found || review.Independence != workroom.IndependenceSelfReview {
		t.Fatalf("projected review = %+v (found %v)", review, found)
	}
	base := testGit(t, fixture.repo, "rev-parse", "HEAD")
	err = mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval.Record.ID,
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
	if last.Actor != fixture.workspace.Config.Actors["reviewer"].Fingerprint {
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
	if _, exists := workspace.Config.Actors["alice"]; !exists {
		t.Fatalf("the operator was not seeded as alice: %v", workspace.Config.Actors)
	}
	if _, exists := workspace.Config.Actors["operator"]; exists {
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
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref := app.ResidentRef(workspace.Config.Genesis)

	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- serveCommand(ctx, []string{"--repo", repo, "--listen", "127.0.0.1:0"})
	}()

	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := workspace.ResidentURL(); ok {
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

	second := serveCommand(context.Background(), []string{"--repo", repo, "--listen", "127.0.0.1:0"})
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
	if published, ok := workspace.ResidentURL(); !ok || published != url {
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
		if published, ok := workspace.ResidentURL(); ok && published != url {
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
	ref := app.ResidentRef(workspace.Config.Genesis)

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
		Genesis: workspace.Config.Genesis,
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

	serving, stop := context.WithCancel(ctx)
	defer stop()
	served := make(chan error, 1)
	go func() {
		served <- serveCommand(serving, []string{"--repo", repo, "--listen", "127.0.0.1:0"})
	}()
	var url string
	for attempt := 0; attempt < 300 && url == ""; attempt++ {
		if published, ok := workspace.ResidentURL(); ok {
			url = published
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if url == "" {
		t.Fatal("a claim left by a dead owner wedged the repository")
	}
	if value, present, err := workspace.Store.RefValue(ctx, ref); err != nil || !present || value == blob {
		t.Fatalf("the abandoned claim was not taken over: %q present=%v err=%v", value, present, err)
	}
	stop()
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serving failed: %v", err)
	}
}
