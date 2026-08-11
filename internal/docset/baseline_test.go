package docset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const baselineFile = "citation_baseline.txt"

// The baseline is written by the same code path the gate reads, so the two
// cannot disagree about which citations are failing or why. Regenerate with
// DOCSET_WRITE_BASELINE=1 go test ./internal/docset -run RegenerateCitationBaseline
// and review the diff: every line added is a debt taken on, every line removed
// is a page repaired.
func TestRegenerateCitationBaseline(t *testing.T) {
	if os.Getenv("DOCSET_WRITE_BASELINE") == "" {
		t.Skip("set DOCSET_WRITE_BASELINE=1 to rewrite the baseline from the live workroom")
	}
	failing, _, err := currentCitations(t)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(failing))
	for act := range failing {
		keys = append(keys, act)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString(baselineHeader)
	for _, act := range keys {
		fmt.Fprintf(&out, "%s %s\n", act, failing[act])
	}
	if err := os.WriteFile(filepath.Join("..", "..", "internal", "docset", baselineFile), []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d baseline entries", len(keys))
}

const baselineHeader = `# Known-failing documentation citations.
#
# Each line is a citation the gate would otherwise refuse, kept so that new
# violations fail immediately while these are repaired by separate semantic
# re-anchoring work. The list may only shrink: the gate fails an entry whose
# classification changed, and fails an entry that has stopped failing, so the
# head that repairs a page must delete its line in the same commit.
#
# <event id> <reason>
`

// currentCitations classifies every citation the documentation set names,
// against the real workroom. It returns the fatal ones with their reasons, and
// the count of citations examined.
func currentCitations(t *testing.T) (map[string]string, int, error) {
	t.Helper()
	root := mustRoot(t)
	pages := mustPages(t, root)
	acts := declaredActs(pages)
	workspace, err := app.Open(context.Background(), root)
	if err != nil {
		return nil, 0, fmt.Errorf("no workroom in this checkout: %w", err)
	}
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		return nil, 0, err
	}
	kinds := make(map[string]workroom.Kind, len(snapshot.Projection.Statements))
	for _, statement := range snapshot.Projection.Statements {
		kinds[statement.Event] = statement.Kind
	}
	artifacts := make(map[string]workroom.Artifact, len(snapshot.Projection.Artifacts))
	for _, artifact := range snapshot.Projection.Artifacts {
		artifacts[artifact.Event] = artifact
	}
	failing := make(map[string]string)
	for _, act := range acts {
		kind, found := kinds[act]
		artifact := artifacts[act]
		verdict := ClassifyCitation(found, kind == workroom.KindArtifact, artifact.Retired, artifact.Stale, artifact.Path, artifact.Commit)
		if verdict.Fatal {
			failing[act] = verdict.Reason
		}
	}
	return failing, len(acts), nil
}

// LoadBaseline reads the checked-in known-failing list.
func loadBaseline(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "docset", baselineFile))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, reason, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("baseline line has no reason: %q", line)
		}
		entries[id] = reason
	}
	return entries
}
