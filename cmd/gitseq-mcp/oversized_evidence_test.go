package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
)

// MCP `state` carries evidence as inline strings, and used to sign it and send
// it to the resident before the kernel refused the result. The application now
// measures the act where it signs it, so the adapter refuses without signing
// and without sending. The resident below counts what actually reaches it, so
// the test can tell a refusal raised before the act travels from the same
// refusal raised at the far end.
func TestMCPStateEvidenceOverTheCeilingIsRefusedBeforeSubmission(t *testing.T) {
	parallelTest(t)
	m := newMCPAuthoring(t)
	submissions := countingResident(t, m.workspace)
	before := m.frontier()

	_, _, err := m.adapter.call(m.ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "oversized evidence", "rests_on": []any{m.seed},
		"evidence":        map[string]any{"observations.json": strings.Repeat("x", 1<<20)},
		"idempotency_key": "oversized-evidence",
	}})
	if err == nil {
		t.Fatal("MCP state submitted evidence over the ceiling")
	}
	for _, want := range []string{
		"event exceeds genesis ceiling: ",
		"against a ceiling of 1048576",
		`attachments 1048576 in 1 file, largest "observations.json" at 1048576 bytes`,
		"cite a repository path instead of attaching bytes",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not carry %q", err.Error(), want)
		}
	}
	if sent := submissions.Load(); sent != 0 {
		t.Fatalf("the refused act was sent to the resident %d time(s)", sent)
	}
	if after := m.frontier(); after != before {
		t.Fatalf("refused act moved the durable frontier: before=%s after=%s", before, after)
	}

	// No signature was produced, so the retry key was never minted into an act:
	// the same key, with evidence that fits, still lands as a fresh act.
	value, _, err := m.adapter.call(m.ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "oversized evidence", "rests_on": []any{m.seed},
		"evidence":        map[string]any{"observations.json": strings.Repeat("x", 64)},
		"idempotency_key": "oversized-evidence",
	}})
	if err != nil {
		t.Fatalf("the refused act's idempotency key was spent: %v", err)
	}
	if _, ok := submissionRecord(value); !ok {
		t.Fatalf("admitted act returned no durable record: %#v", value)
	}
	if sent := submissions.Load(); sent != 1 {
		t.Fatalf("admitted act reached the resident %d time(s), want 1", sent)
	}
}

// countingResident serves the real resident for this workspace, advertises it
// the way `gs serve` does, and counts the submissions that reach it. What is
// being measured is whether an act was sent at all, so the count is taken at
// the transport rather than inside the fold.
func countingResident(t *testing.T, workspace *app.Workspace) *atomic.Int64 {
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
	if _, err := workspace.PublishResident(resident.URL); err != nil {
		t.Fatal(err)
	}
	return &submissions
}
