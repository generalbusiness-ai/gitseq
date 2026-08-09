package docset

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The basis and flare gates both ask what the fold does with the anchoring the
// pages declare. Neither can ask the workroom this repository lives in: a
// page's own artifact is filed after the commit that contains the page, so a
// test that demanded to find it would be red at every commit it is meant to
// guard.
//
// So the graph is replayed instead. Each distinct act named in front matter
// gets a stand-in artifact in a scratch workroom, each page gets an artifact
// resting on the stand-ins for the acts it names, and the gates then read the
// same marks — unable_to_flare, stale — that the projection shows for the real
// record. What is under test is the declared graph and the fold's treatment of
// it, which is exactly what a reader relies on.

type model struct {
	workspace *app.Workspace
	// standIn maps a declared act identifier to the scratch event standing in
	// for it.
	standIn map[string]string
	// artifact maps a page path to the scratch artifact representing it.
	artifact map[string]string
	// seed is the workroom's first record: the one basis every stand-in rests
	// on, and a resolvable event for the control case.
	seed string
	// dir is the repository the model lives in.
	dir string
}

// Signing every event is the expensive part, and the flare gate needs one
// workroom per act it retires. So the graph is built once and copied: event
// identifiers are derived from content, so a copy has the same identifiers as
// the original and the maps carry over unchanged.
var (
	templateOnce sync.Once
	templateDir  string
	template     *model
	templateErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, directory := range []string{templateDir, buildDir} {
		if directory != "" {
			_ = os.RemoveAll(directory)
		}
	}
	os.Exit(code)
}

// modelCopy returns a private copy of the modelled set, ready to be superseded
// in without disturbing any other test.
func modelCopy(t *testing.T, pages []Page) *model {
	t.Helper()
	requireTool(t, "git")
	templateOnce.Do(func() {
		directory, err := os.MkdirTemp("", "docset-model")
		if err != nil {
			templateErr = err
			return
		}
		templateDir = directory
		template = buildModelIn(t, filepath.Join(directory, "room"), pages)
	})
	if templateErr != nil {
		t.Fatal(templateErr)
	}
	destination := filepath.Join(t.TempDir(), "room")
	if err := os.CopyFS(destination, os.DirFS(template.dir)); err != nil {
		t.Fatal(err)
	}
	workspace, err := app.Open(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	return &model{workspace: workspace, standIn: template.standIn, artifact: template.artifact, seed: template.seed, dir: destination}
}

func buildModel(t *testing.T, pages []Page) *model {
	t.Helper()
	requireTool(t, "git")
	return buildModelIn(t, filepath.Join(t.TempDir(), "room"), pages)
}

func buildModelIn(t *testing.T, directory string, pages []Page) *model {
	t.Helper()
	ctx := context.Background()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "-q", directory).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, directory, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	built := &model{workspace: workspace, standIn: map[string]string{}, artifact: map[string]string{}, seed: seed.ID, dir: directory}
	for index, act := range declaredActs(pages) {
		submission, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindArtifact,
			Text:           "stands in for " + act,
			Body:           map[string]string{"path": "governed/" + itoa(index), "commit": fakeCommit(act)},
			RestsOn:        []string{seed.ID},
			IdempotencyKey: "act-" + itoa(index),
		})
		if err != nil {
			t.Fatalf("stand-in for %s: %v", act, err)
		}
		built.standIn[act] = submission.Record.ID
	}
	for index, page := range pages {
		bases := make([]string, 0, len(page.RestsOn))
		for _, act := range page.RestsOn {
			bases = append(bases, built.standIn[act])
		}
		submission, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindArtifact,
			Text:           page.Path,
			Body:           map[string]string{"path": page.Path, "commit": fakeCommit(page.Path)},
			RestsOn:        bases,
			IdempotencyKey: "page-" + itoa(index),
		})
		if err != nil {
			t.Fatalf("page artifact for %s: %v", page.Path, err)
		}
		built.artifact[page.Path] = submission.Record.ID
	}
	return built
}

// artifacts returns the current projection's artifacts by event.
func (m *model) artifacts(t *testing.T) map[string]workroom.Artifact {
	t.Helper()
	snapshot, err := m.workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]workroom.Artifact, len(snapshot.Projection.Artifacts))
	for _, artifact := range snapshot.Projection.Artifacts {
		found[artifact.Event] = artifact
	}
	return found
}

// retire supersedes one stand-in, which is what a later act changing that
// behaviour does to the act a page named.
func (m *model) retire(t *testing.T, act string) {
	t.Helper()
	target, ok := m.standIn[act]
	if !ok {
		t.Fatalf("no stand-in for %s", act)
	}
	if _, err := m.workspace.Act(context.Background(), "operator", app.Act{
		Verb: app.VerbSupersede, Target: target, Text: "the world moved under " + act,
		IdempotencyKey: "retire",
	}); err != nil {
		t.Fatal(err)
	}
}

// declaredActs lists every distinct act named across the set, in stable order.
func declaredActs(pages []Page) []string {
	seen := map[string]bool{}
	var acts []string
	for _, page := range pages {
		for _, act := range page.RestsOn {
			if !seen[act] {
				seen[act] = true
				acts = append(acts, act)
			}
		}
	}
	sort.Strings(acts)
	return acts
}

// dependents lists the pages naming one act, in stable order.
func dependents(pages []Page, act string) []string {
	var found []string
	for _, page := range pages {
		for _, named := range page.RestsOn {
			if named == act {
				found = append(found, page.Path)
				break
			}
		}
	}
	sort.Strings(found)
	return found
}

func fakeCommit(seed string) string {
	sum := sha1.Sum([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
