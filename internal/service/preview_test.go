package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestPreviewRecordEvidenceAndExactSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	run := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--git-dir", workspace.Store.Repo}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	blob := run("immutable file\n", "hash-object", "-w", "--stdin")
	empty := run("", "hash-object", "-w", "--stdin")
	binary := run("a\x00b", "hash-object", "-w", "--stdin")
	lines := run(strings.Repeat("\n", 5000), "hash-object", "-w", "--stdin")
	tree := run("100644 blob "+blob+"\tsource.go\n100644 blob "+binary+"\tbinary.dat\n100644 blob "+lines+"\tlines.txt\n100644 blob "+empty+"\tempty.txt\n", "mktree")
	head := run("tree "+tree+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\nsource\n", "hash-object", "-t", "commit", "-w", "--stdin")
	record, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "Review source.go:1", Body: map[string]string{"head": head}, Attachments: map[string][]byte{"review.md": []byte("# Review\nSafe <script> text"), "evidence.json": []byte(`{"ok":true}`)}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	query := func(input previewRequest) previewResponse {
		t.Helper()
		body, _ := json.Marshal(input)
		req := httptest.NewRequest(http.MethodPost, "/v0/preview", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("preview %d: %s", response.Code, response.Body.String())
		}
		var got previewResponse
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := query(previewRequest{Event: record.Record.ID})
	if got.Status != "ready" || len(got.Attachments) != 2 || len(got.Heads) != 1 || got.Heads[0] != head {
		t.Fatalf("metadata: %+v", got)
	}
	got = query(previewRequest{Event: record.Record.ID, Attachment: "review.md"})
	if got.Content != "# Review\nSafe <script> text" || got.Commit == head {
		t.Fatalf("attachment: %+v", got)
	}
	got = query(previewRequest{Event: record.Record.ID, Path: "source.go"})
	if got.Content != "immutable file\n" || got.Commit != head {
		t.Fatalf("source: %+v", got)
	}

	got = query(previewRequest{Event: record.Record.ID, Path: "empty.txt"})
	if got.Status != "ready" || got.Content != "" {
		t.Fatalf("empty file: %+v", got)
	}
	secondHead := run("tree "+tree+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\nsecond source\n", "hash-object", "-t", "commit", "-w", "--stdin")
	var artifactIDs []string
	for _, commit := range []string{head, secondHead} {
		artifact, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "Fixture exact source", Body: map[string]string{"path": "source.go", "commit": commit}})
		if err != nil {
			t.Fatal(err)
		}
		artifactIDs = append(artifactIDs, artifact.Record.ID)
	}
	note, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "Two cited revisions", RestsOn: artifactIDs})
	if err != nil {
		t.Fatal(err)
	}
	got = query(previewRequest{Event: note.Record.ID, Path: "source.go"})
	if got.Status != "ambiguous" || len(got.Heads) != 2 || got.Content != "" {
		t.Fatalf("ambiguous source: %+v", got)
	}
	got = query(previewRequest{Event: note.Record.ID, Path: "source.go", Commit: secondHead})
	if got.Status != "ready" || got.Commit != secondHead {
		t.Fatalf("explicit choice: %+v", got)
	}
	unbound, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "No source head"})
	if err != nil {
		t.Fatal(err)
	}
	got = query(previewRequest{Event: unbound.Record.ID, Path: "source.go"})
	if got.Status != "unavailable" {
		t.Fatalf("invented current-main fallback: %+v", got)
	}
	for _, tc := range []struct {
		input  previewRequest
		status string
	}{
		{previewRequest{Event: record.Record.ID, Path: "absent.go"}, "missing"},
		{previewRequest{Event: record.Record.ID, Path: "binary.dat"}, "binary"},
		{previewRequest{Event: record.Record.ID, Path: "lines.txt"}, "oversize"},
		{previewRequest{Event: record.Record.ID, Path: "source.go", Commit: strings.Repeat("1", 40)}, "invalid"},
		{previewRequest{Event: record.Record.ID, Path: "../source.go"}, "invalid"},
		{previewRequest{Event: record.Record.ID, Attachment: "../review.md"}, "invalid"},
		{previewRequest{Event: record.Record.ID, Attachment: "review.md", Path: "source.go"}, "invalid"},
		{previewRequest{Event: strings.Replace(record.Record.ID, "git:sha1:", "git:sha256:", 1), Path: "source.go"}, "missing"},
	} {
		if got := query(tc.input); got.Status != tc.status || got.Content != "" {
			t.Errorf("%+v: %+v", tc.input, got)
		}
	}
	for _, body := range []string{`{"event":"x","extra":true}`, `{"event":"x"}{}`, `{"event":"` + strings.Repeat("x", 8192) + `"}`} {
		req := httptest.NewRequest(http.MethodPost, "/v0/preview", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != 400 {
			t.Errorf("invalid input returned %d", res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v0/preview", strings.NewReader(`{"event":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://untrusted.invalid")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != 400 {
		t.Fatalf("cross-origin: %d", res.Code)
	}
}

func TestPreviewRevisionSelectionIsDirectAndUnambiguous(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	projection := workroom.Projection{
		Artifacts:  []workroom.Artifact{{Event: "one", Commit: a}, {Event: "two", Commit: b}},
		Provenance: map[string][]string{"review": {"one", "two"}, "indirect": {"review"}},
	}
	if got := previewHeads(projection, "review", "sha1"); len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("multiple direct heads: %v", got)
	}
	if got := previewHeads(projection, "indirect", "sha1"); len(got) != 0 {
		t.Fatalf("walked indirect provenance: %v", got)
	}
	projection.Reviews = []workroom.Review{{Report: "review", Head: b}}
	if got := previewHeads(projection, "review", "sha1"); len(got) != 1 || got[0] != b {
		t.Fatalf("exact review head should win: %v", got)
	}
	if got := previewHeads(projection, "unknown", "sha1"); len(got) != 0 {
		t.Fatalf("invented source head: %v", got)
	}
}
