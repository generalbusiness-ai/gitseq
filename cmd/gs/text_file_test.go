package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// report is the shape a real review has and a command line does not survive:
// headings, a table, a quoted finding and a fenced block, with the blank
// trailing line an editor leaves behind.
const report = `# Review

| conclusion | result |
|---|---|
| Architecture | preserved |

> "the head is the head"

` + "```sh\ngo test ./...\n```" + `

APPROVED at the exact head.

`

// writeText puts contents in a file outside any checkout, so nothing a test
// writes can make the reviewed working tree dirty.
func writeText(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "text.md")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The helper is the whole rule both commands share, so it is tested as one
// thing: one source, carried as written, trailing whitespace alone removed.
func TestResolveTextTakesExactlyOneSource(t *testing.T) {
	t.Parallel()
	file := writeText(t, report)
	blank := writeText(t, "\n \t\n\n")
	missing := filepath.Join(t.TempDir(), "absent.md")

	for _, test := range []struct {
		name     string
		text     string
		textFile string
		required bool
		want     string
		refusal  string
	}{
		{name: "typed", text: "a one-line verdict", required: true, want: "a one-line verdict"},
		{name: "from a file", textFile: file, required: true, want: strings.TrimRight(report, "\n")},
		{name: "absent where text is optional", want: ""},
		{name: "both sources", text: "typed", textFile: file, required: true, refusal: "--text and --text-file cannot both be given"},
		{name: "both sources where text is optional", text: "typed", textFile: file, refusal: "--text and --text-file cannot both be given"},
		{name: "neither where text is required", required: true, refusal: "--text or --text-file is required"},
		{name: "a blank file", textFile: blank, required: true, refusal: "is empty"},
		{name: "a path that cannot be read", textFile: missing, required: true, refusal: "--text-file " + missing + ":"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveText(test.text, test.textFile, test.required)
			if test.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.refusal) {
					t.Fatalf("resolveText(%q, %q, %v) error = %v, want one naming %q", test.text, test.textFile, test.required, err, test.refusal)
				}
				if got != "" {
					t.Fatalf("a refused resolveText still returned %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolveText = %q, want %q", got, test.want)
			}
		})
	}
}

// The verdict a reviewer writes in a file is the verdict the workroom holds.
func TestReviewFilesTheReportItReadsFromAFile(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	if err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise,
		"--verdict", "approved", "--text-file", writeText(t, report),
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("review depth = %d, want %d", snapshot.Depth, before+1)
	}
	statement := statementByEvent(t, snapshot.Projection, snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event)
	if statement.Text != strings.TrimRight(report, "\n") {
		t.Fatalf("filed verdict text = %q, want the file's contents %q", statement.Text, strings.TrimRight(report, "\n"))
	}
	if statement.Body["verdict"] != "approved" || statement.Body["head"] != fixture.candidate {
		t.Fatalf("verdict body = %#v", statement.Body)
	}
}

// Every way of naming the text badly is refused where the reviewer can still
// fix it, with nothing signed.
func TestReviewRefusesEveryBadTextSourceBeforeSigning(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		text    func(t *testing.T) []string
		refusal string
	}{
		{
			name:    "both sources",
			text:    func(t *testing.T) []string { return []string{"--text", "typed", "--text-file", writeText(t, report)} },
			refusal: "--text and --text-file cannot both be given",
		},
		{
			name:    "neither source",
			text:    func(t *testing.T) []string { return nil },
			refusal: "--text or --text-file is required",
		},
		{
			name:    "a blank file",
			text:    func(t *testing.T) []string { return []string{"--text-file", writeText(t, "\n\t \n")} },
			refusal: "is empty",
		},
		{
			name: "a path that cannot be read",
			text: func(t *testing.T) []string {
				return []string{"--text-file", filepath.Join(t.TempDir(), "absent.md")}
			},
			refusal: "--text-file ",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			before := fixture.snapshot(t).Depth
			arguments := append([]string{
				"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
				"--artifact", fixture.artifact, "--promise", fixture.promise,
				"--verdict", "approved",
			}, test.text(t)...)
			err := reviewCommand(fixture.ctx, arguments)
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("review error = %v, want one naming %q", err, test.refusal)
			}
			if after := fixture.snapshot(t).Depth; after != before {
				t.Fatalf("a refused review signed something: depth %d -> %d", before, after)
			}
		})
	}
}

// --prepare records no verdict, so it still needs no text at all.
func TestReviewPrepareStillNeedsNoText(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	if err := reviewCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "reviewer", "--checkout", fixture.feature,
		"--artifact", fixture.artifact, "--promise", fixture.promise, "--prepare",
	}); err != nil {
		t.Fatal(err)
	}
	if after := fixture.snapshot(t).Depth; after != before {
		t.Fatalf("--prepare signed something: depth %d -> %d", before, after)
	}
}

func TestStateFilesTheStatementItReadsFromAFile(t *testing.T) {
	t.Parallel()
	fixture := newWorkflowFixture(t)
	before := fixture.snapshot(t).Depth
	if err := stateCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--kind", string(workroom.KindAssert),
		"--text-file", writeText(t, report), "--rests-on", fixture.artifact,
		"--idempotency-key", "assert-from-a-file",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := fixture.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("state depth = %d, want %d", snapshot.Depth, before+1)
	}
	statement := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if statement.Text != strings.TrimRight(report, "\n") {
		t.Fatalf("filed statement text = %q, want the file's contents %q", statement.Text, strings.TrimRight(report, "\n"))
	}
}

func TestStateRefusesEveryBadTextSourceBeforeSigning(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		text    func(t *testing.T) []string
		refusal string
	}{
		{
			name:    "both sources",
			text:    func(t *testing.T) []string { return []string{"--text", "typed", "--text-file", writeText(t, report)} },
			refusal: "--text and --text-file cannot both be given",
		},
		{
			name:    "a blank file",
			text:    func(t *testing.T) []string { return []string{"--text-file", writeText(t, "   \n")} },
			refusal: "is empty",
		},
		{
			name: "a path that cannot be read",
			text: func(t *testing.T) []string {
				return []string{"--text-file", filepath.Join(t.TempDir(), "absent.md")}
			},
			refusal: "--text-file ",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkflowFixture(t)
			before := fixture.snapshot(t).Depth
			arguments := append([]string{
				"--repo", fixture.repo, "--as", "operator", "--kind", string(workroom.KindAssert),
				"--rests-on", fixture.artifact, "--idempotency-key", "refused-" + test.name,
			}, test.text(t)...)
			err := stateCommand(fixture.ctx, arguments)
			if err == nil || !strings.Contains(err.Error(), test.refusal) {
				t.Fatalf("state error = %v, want one naming %q", err, test.refusal)
			}
			if after := fixture.snapshot(t).Depth; after != before {
				t.Fatalf("a refused state signed something: depth %d -> %d", before, after)
			}
		})
	}
}
