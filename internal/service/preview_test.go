package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
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
	if got.Status != "ready" || got.Content != "" || got.Window == nil || got.Window.Total != 0 || got.Window.Partial {
		t.Fatalf("empty file: %+v", got)
	}
	// Five thousand empty lines no longer refuse: they arrive as the first
	// window, saying so.
	got = query(previewRequest{Event: record.Record.ID, Path: "lines.txt"})
	if got.Status != "ready" || got.Window == nil || !got.Window.Partial || got.Window.Start != 1 || got.Window.End != PreviewWindowLines || got.Window.Total != 5000 || got.Window.Next != PreviewWindowLines+1 {
		t.Fatalf("first window of a long file: %+v", got.Window)
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

// TestPreviewWindowsBoundCitedSource is the producer/consumer proof for
// bounded windows: the observed 5,611-line file opens at the window holding
// its cited line, a file above the old 512 KiB ceiling is readable in windows,
// one above the read budget is refused, and neighbouring content is always
// the exact cited revision's.
func TestPreviewWindowsBoundCitedSource(t *testing.T) {
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
	numbered := func(n int, tag string) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, "%s line %d\n", tag, i)
		}
		return b.String()
	}
	long := numbered(5611, "first")
	longBlob := run(long, "hash-object", "-w", "--stdin")
	otherBlob := run(numbered(5611, "second"), "hash-object", "-w", "--stdin")
	wide := strings.Repeat("é", PreviewLineBytes) + "\nshort\n" // 2 bytes per rune: the cut lands inside a rune
	wideBlob := run(wide, "hash-object", "-w", "--stdin")
	big := numbered(40000, "big") // about 640 KiB: above the old ceiling, inside the budget
	bigBlob := run(big, "hash-object", "-w", "--stdin")
	// Literal boundary sizes, independent of the constant: exactly 4 MiB
	// reads, one byte more is refused, over HTTP.
	atBudget := run(strings.Repeat("y", 4194303)+"\n", "hash-object", "-w", "--stdin")
	overBudget := run(strings.Repeat("x", 4194304)+"\n", "hash-object", "-w", "--stdin")
	invalid := run("ok\n\xff\xfe\n", "hash-object", "-w", "--stdin")
	tree := run("100644 blob "+longBlob+"\tmain_test.go\n100644 blob "+wideBlob+"\twide.txt\n100644 blob "+bigBlob+"\tbig.txt\n100644 blob "+atBudget+"\tatbudget.txt\n100644 blob "+overBudget+"\tover.txt\n100644 blob "+invalid+"\tinvalid.txt\n", "mktree")
	otherTree := run("100644 blob "+otherBlob+"\tmain_test.go\n", "mktree")
	commit := func(tree, message string) string {
		return run("tree "+tree+"\nauthor Test <test@example.invalid> 1 +0000\ncommitter Test <test@example.invalid> 1 +0000\n\n"+message+"\n", "hash-object", "-t", "commit", "-w", "--stdin")
	}
	head := commit(tree, "first")
	otherHead := commit(otherTree, "second")
	var artifacts []string
	for _, c := range []string{head, otherHead} {
		artifact, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "exact source", Body: map[string]string{"path": "main_test.go", "commit": c}})
		if err != nil {
			t.Fatal(err)
		}
		artifacts = append(artifacts, artifact.Record.ID)
	}
	review, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "Review cites main_test.go:726", RestsOn: artifacts, Attachments: map[string][]byte{"evidence.txt": []byte(long)}})
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
	event := review.Record.ID
	// The cited line arrives inside its window, under the exact revision
	// asked for, with real line numbers: the first line of the window is
	// the file's line Start, not line one.
	got := query(previewRequest{Event: event, Path: "main_test.go", Commit: head, Line: 726})
	w := got.Window
	if got.Status != "ready" || w == nil || !w.Partial || w.Start > 726 || w.End < 726 || w.Total != 5611 || got.Size != len(long) || got.Commit != head {
		t.Fatalf("cited-line window: %+v %+v", got.Status, w)
	}
	lines := strings.Split(got.Content, "\n")
	if len(lines) != w.End-w.Start+1 || lines[0] != fmt.Sprintf("first line %d", w.Start) || lines[726-w.Start] != "first line 726" {
		t.Fatalf("window content is not the numbered lines: %q…", lines[0])
	}
	if w.Previous != w.Start-PreviewWindowLines || w.Next != w.End+1 {
		t.Fatalf("navigation: %+v", w)
	}
	// The same line under the other cited revision shows that revision's
	// text, never the neighbour's.
	got = query(previewRequest{Event: event, Path: "main_test.go", Commit: otherHead, Line: 726})
	if got.Commit != otherHead || !strings.Contains(got.Content, "second line 726\n") || strings.Contains(got.Content, "first line") {
		t.Fatalf("wrong revision content: %s %q", got.Commit, got.Content[:40])
	}
	// Following Next reaches the final window, which has no Next and ends at
	// the last line; a start beyond the file is an honest empty window.
	got = query(previewRequest{Event: event, Path: "main_test.go", Commit: head, Start: 5601})
	if w = got.Window; got.Status != "ready" || w.Start != 5601 || w.End != 5611 || w.Next != 0 || w.Previous != 5201 || !strings.HasSuffix(got.Content, "first line 5611") {
		t.Fatalf("final window: %+v", w)
	}
	// Beyond the file, Previous and the cited-line fallback both name the
	// last window of the partition, which for short lines starts at 5601.
	got = query(previewRequest{Event: event, Path: "main_test.go", Commit: head, Start: 9000})
	if w = got.Window; got.Status != "ready" || got.Content != "" || w.End < w.Start-1 || w.Previous != 5601 || !strings.Contains(got.Message, "outside this file") {
		t.Fatalf("out-of-range start: %+v %q", w, got.Message)
	}
	got = query(previewRequest{Event: event, Path: "main_test.go", Commit: head, Line: 9000})
	if w = got.Window; w.Start != 5601 || w.End != 5611 || w.Next != 0 || !strings.Contains(got.Message, "Cited line 9000 is outside") {
		t.Fatalf("out-of-range line: %+v %q", w, got.Message)
	}
	// Evidence attachments use the same bounded surface.
	got = query(previewRequest{Event: event, Attachment: "evidence.txt", Line: 5000})
	if w = got.Window; got.Status != "ready" || w.Start != 4801 || w.End != 5200 || got.Commit == head {
		t.Fatalf("attachment window: %+v", w)
	}
	// Above the old 512 KiB ceiling but inside the read budget: readable in
	// windows, and a byte-bounded window closes early rather than exceed
	// its budget. Above the budget: refused, honestly, with no content.
	got = query(previewRequest{Event: event, Path: "big.txt", Commit: head, Line: 39999})
	if w = got.Window; got.Status != "ready" || got.Size != len(big) || w.Start > 39999 || w.End < 39999 || w.Next != 0 {
		t.Fatalf("large file window: %s %+v", got.Status, w)
	}
	if gitstore.PreviewReadBudget != 4194304 {
		t.Fatalf("read budget is %d, not the documented 4 MiB", gitstore.PreviewReadBudget)
	}
	got = query(previewRequest{Event: event, Path: "atbudget.txt", Commit: head})
	if got.Status != "ready" || got.Size != 4194304 || got.Limit != 4194304 || got.Window == nil || got.Window.Total != 1 {
		t.Fatalf("exactly 4 MiB: %s size %d limit %d %+v", got.Status, got.Size, got.Limit, got.Window)
	}
	got = query(previewRequest{Event: event, Path: "over.txt", Commit: head})
	if got.Status != "oversize" || got.Content != "" || got.Size != 0 || !strings.Contains(got.Message, "4 MiB") {
		t.Fatalf("over budget (4194305 bytes): %+v", got)
	}
	// A pathological line is cut at a rune boundary and named as truncated.
	got = query(previewRequest{Event: event, Path: "wide.txt", Commit: head})
	if w = got.Window; got.Status != "ready" || !w.Partial || len(w.Truncated) != 1 || w.Truncated[0] != 1 || !utf8.ValidString(got.Content) || len(strings.Split(got.Content, "\n")[0]) > PreviewLineBytes || !strings.HasSuffix(got.Content, "\nshort") {
		t.Fatalf("truncated line: %+v %d", w, len(got.Content))
	}
	// Invalid UTF-8 is not text, whatever window is asked for.
	if got = query(previewRequest{Event: event, Path: "invalid.txt", Commit: head, Line: 1}); got.Status != "binary" {
		t.Fatalf("invalid utf-8: %+v", got.Status)
	}
	for _, input := range []previewRequest{
		{Event: event, Line: 3},
		{Event: event, Path: "main_test.go", Commit: head, Line: -1},
		{Event: event, Path: "main_test.go", Commit: head, Start: previewLineCeiling + 1},
	} {
		if got := query(input); got.Status != "invalid" {
			t.Errorf("%+v accepted: %s", input, got.Status)
		}
	}
}

func TestPreviewWindowSelection(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	content := []byte(b.String())
	for _, tc := range []struct {
		line, start, wantStart, wantEnd, previous, next int
	}{
		{0, 0, 1, 400, 0, 401},
		{1, 0, 1, 400, 0, 401},
		{400, 0, 1, 400, 0, 401},
		{401, 0, 401, 800, 1, 801},
		{999, 0, 801, 1000, 401, 0},
		{0, 801, 801, 1000, 401, 0},
		{5, 801, 801, 1000, 401, 0}, // an explicit start wins over the line
	} {
		text, w, _ := windowFor(content, tc.line, tc.start)
		if w.Start != tc.wantStart || w.End != tc.wantEnd || w.Previous != tc.previous || w.Next != tc.next || !w.Partial || w.Total != 1000 {
			t.Errorf("line %d start %d: %+v", tc.line, tc.start, w)
		}
		if got := strings.Split(text, "\n"); got[0] != fmt.Sprint(w.Start) || got[len(got)-1] != fmt.Sprint(w.End) {
			t.Errorf("line %d start %d: content %q…%q", tc.line, tc.start, got[0], got[len(got)-1])
		}
	}
	// A small file is whole and byte-identical, trailing newline included.
	small := []byte("a\nb\n")
	if text, w, msg := windowFor(small, 2, 0); text != "a\nb\n" || w.Partial || w.Start != 1 || w.End != 2 || w.Total != 2 || msg != "" {
		t.Fatalf("small: %q %+v %q", text, w, msg)
	}
	if _, _, msg := windowFor(small, 3, 0); !strings.Contains(msg, "outside this file") {
		t.Fatalf("small out of range: %q", msg)
	}
	// The byte bound closes a window early and Next resumes where it ended.
	wide := []byte(strings.Repeat(strings.Repeat("w", 1000)+"\n", 200))
	if _, w, _ := windowFor(wide, 0, 0); w.End >= 200 || w.End < 60 || w.Next != w.End+1 || len(w.Truncated) != 0 {
		t.Fatalf("byte-bounded window: %+v", w)
	}
}

// TestPreviewWindowsUnderTheByteBound pins the findings of review 318bec67:
// with long lines the byte bound closes windows early, and the cited line,
// Previous, an out-of-range start on a small file and the truncation list
// must all stay honest.
func TestPreviewWindowsUnderTheByteBound(t *testing.T) {
	t.Parallel()
	wide := []byte(strings.Repeat(strings.Repeat("w", 1000)+"\n", 1000))
	// The aligned window for line 726 starts at 401 but the byte bound
	// closes it near 465; the window must still contain the cited line.
	text, w, msg := windowFor(wide, 726, 0)
	if w.Start > 726 || w.End < 726 || msg != "" {
		t.Fatalf("cited line dropped by the byte bound: %+v %q", w, msg)
	}
	if got := strings.Split(text, "\n"); len(got) != w.End-w.Start+1 {
		t.Fatalf("content lines %d, window %+v", len(got), w)
	}
	// Next then Previous return to the adjacent window, never skipping one.
	_, first, _ := windowFor(wide, 0, 0)
	_, second, _ := windowFor(wide, 0, first.Next)
	_, third, _ := windowFor(wide, 0, second.Next)
	_, back, _ := windowFor(wide, 0, third.Previous)
	if back.End != third.Start-1 || back.Start != second.Start {
		t.Fatalf("previous skipped a window: first %+v second %+v third %+v back %+v", first, second, third, back)
	}
	if first.Previous != 0 || second.Previous != 1 {
		t.Fatalf("previous at the start: %+v %+v", first, second)
	}
	// A small file still answers an out-of-range start honestly.
	text, w, msg = windowFor([]byte("a\nb\n"), 0, 9000)
	if text != "" || w.End >= w.Start || w.Total != 2 || !strings.Contains(msg, "outside this file") {
		t.Fatalf("small file ignored start 9000: %q %+v %q", text, w, msg)
	}
	if text, w, _ = windowFor([]byte("a\nb\n"), 0, 2); text != "a\nb\n" || w.Partial {
		t.Fatalf("small file with an in-range start: %q %+v", text, w)
	}
	// The truncated list names only lines the window carries.
	long := []byte(strings.Repeat(strings.Repeat("x", 5000)+"\n", 100))
	_, w, _ = windowFor(long, 0, 0)
	if len(w.Truncated) != w.End-w.Start+1 {
		t.Fatalf("truncated list %d entries for window %+v", len(w.Truncated), w)
	}
	for _, n := range w.Truncated {
		if n < w.Start || n > w.End {
			t.Fatalf("truncated names unreturned line %d: %+v", n, w)
		}
	}
	// A cited line beyond the file lands on the last byte-bounded window.
	_, w, msg = windowFor(wide, 5000, 0)
	if w.End != 1000 || w.Start < 1000-PreviewWindowLines || !strings.Contains(msg, "outside this file") {
		t.Fatalf("out-of-range line on a byte-bounded file: %+v %q", w, msg)
	}
}

// TestPreviewWindowsArePartitionOfTheFile pins the second round of review
// 318bec67: windows are a fixed partition, so the window Previous names
// ends exactly where the current one starts, whatever the line lengths.
func TestPreviewWindowsArePartitionOfTheFile(t *testing.T) {
	t.Parallel()
	mixed := []byte(strings.Repeat(strings.Repeat("w", 4000)+"\n", 19) + strings.Repeat("a\n", 81))
	_, current, _ := windowFor(mixed, 20, 0)
	if current.Start > 20 || current.End < 20 || current.Previous == 0 {
		t.Fatalf("cited line 20: %+v", current)
	}
	_, previous, _ := windowFor(mixed, 0, current.Previous)
	if previous.End != current.Start-1 || previous.Next != current.Start {
		t.Fatalf("previous is not adjacent: current %+v previous %+v", current, previous)
	}
	// Every line of the file belongs to exactly one window, and walking Next
	// from the first window visits them all in order without gaps or overlap.
	wide := []byte(strings.Repeat(strings.Repeat("w", 1000)+"\n", 1000))
	_, w, _ := windowFor(wide, 0, 1)
	seen := 0
	for {
		if w.Start != seen+1 {
			t.Fatalf("gap or overlap at %d: %+v", seen+1, w)
		}
		seen = w.End
		if w.Next == 0 {
			break
		}
		_, w, _ = windowFor(wide, 0, w.Next)
	}
	if seen != 1000 {
		t.Fatalf("walk ended at %d", seen)
	}
	// A start inside a window names that window, so a copied link opens the
	// same window whichever of its lines it named.
	_, a, _ := windowFor(wide, 0, 70)
	_, b, _ := windowFor(wide, 0, 100)
	if a.Start != b.Start || a.Start > 70 || a.End < 100 {
		t.Fatalf("starts inside one window differ: %+v %+v", a, b)
	}
}
