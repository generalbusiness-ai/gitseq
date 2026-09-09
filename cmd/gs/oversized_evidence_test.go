package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
)

// `gs state --evidence` reads any file off disk and used to sign it and send it
// to the sequencer before the kernel refused it. The application now measures
// the act where it signs it, so this command refuses without signing and
// without sending. The resident below counts what actually reaches it, so the
// test can tell a refusal raised before the act travels from the same refusal
// raised at the far end.
func TestStateEvidenceOverTheCeilingIsRefusedBeforeSubmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 2<<10)
	if err != nil {
		t.Fatal(err)
	}
	resident, submissions := countingResident(t, workspace)

	oversized := filepath.Join(t.TempDir(), "observations.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), 4<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = stateCommand(ctx, []string{
		"--repo", repo, "--as", "operator", "--server", resident, "--kind", "assert",
		"--text", "oversized evidence", "--rests-on", seed.ID,
		"--evidence", "observations.json=" + oversized, "--idempotency-key", "oversized-evidence",
	})
	if err == nil {
		t.Fatal("gs state submitted evidence over the ceiling")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 2048",
		`attachments 4096 in 1 file, largest "observations.json" at 4096 bytes`,
		"cite a repository path instead of attaching bytes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not carry %q", err.Error(), want)
		}
	}
	if sent := submissions.Load(); sent != 0 {
		t.Fatalf("the refused act was sent to the sequencer %d time(s)", sent)
	}

	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused act changed the durable log: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}

	// No signature was produced, so the retry key was never minted into an act:
	// the same key, with evidence that fits, still lands as a fresh act.
	small := filepath.Join(t.TempDir(), "observations.json")
	if err := os.WriteFile(small, bytes.Repeat([]byte("x"), 64), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stateCommand(ctx, []string{
		"--repo", repo, "--as", "operator", "--server", resident, "--kind", "assert",
		"--text", "oversized evidence", "--rests-on", seed.ID,
		"--evidence", "observations.json=" + small, "--idempotency-key", "oversized-evidence",
	}); err != nil {
		t.Fatalf("the refused act's idempotency key was spent: %v", err)
	}
	if sent := submissions.Load(); sent != 1 {
		t.Fatalf("admitted act reached the sequencer %d time(s), want 1", sent)
	}
	landed, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if landed.Depth != before.Depth+1 {
		t.Fatalf("admitted evidence depth = %d, want %d", landed.Depth, before.Depth+1)
	}
}

// countingResident serves the real resident for this workspace and counts the
// submissions that reach it. What is being measured is whether an act was sent
// at all, so the count is taken at the transport rather than inside the fold.
func countingResident(t *testing.T, workspace *app.Workspace) (string, *atomic.Int64) {
	t.Helper()
	server, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	var submissions atomic.Int64
	resident := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v0/submit" {
			submissions.Add(1)
		}
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(resident.Close)
	return resident.URL, &submissions
}
