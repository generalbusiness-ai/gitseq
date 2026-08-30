package docset

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/testgit"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The basis and flare gates both ask what the fold does with the anchoring the
// pages declare. Neither can ask the workroom this repository lives in: a
// page's own artifact is filed after the commit that contains the page, so a
// test that demanded to find it would be red at every commit it is meant to
// guard.
//
// So the graph is replayed instead. Each distinct act named in front matter
// gets a stand-in artifact, each page gets an artifact resting on the
// stand-ins for the acts it names, and the gates then read the same marks —
// unable_to_flare, stale — that the projection shows for the real record. What
// is under test is the declared graph and the fold's treatment of it, which is
// exactly what a reader relies on.
//
// The replay runs through workroom.Fold on records built in memory. Those
// marks are decided by the fold over the record sequence, not by the signing
// and storage underneath it, so a Git-backed workspace per test bought no
// coverage the fold cannot give — only signing, repository copies and reopen
// time. TestGateGitBackedWorkroomShowsTheSameMarks keeps one real signed
// workroom end to end, so the wiring from an appended act to these projection
// marks stays proven against the same storage the real record uses.

// modelActor signs every modelled record. One actor is enough: the fold lets
// an actor supersede its own statements, which is all retire needs.
const modelActor = "actor:operator"

type model struct {
	// records is the modelled log, in append order, ready for workroom.Fold.
	records []workroom.Record
	// standIn maps a declared act identifier to the record standing in for it.
	standIn map[string]string
	// artifact maps a page path to the record representing it.
	artifact map[string]string
	// seed is the modelled log's first record: the one basis every stand-in
	// rests on, and a resolvable event for the control case.
	seed string
}

func TestMain(m *testing.M) {
	code := testgit.Run(m)
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

// buildModel replays the declared graph as in-memory records. Building from
// scratch per test is cheap without signing, and gives every test its own
// records so parallel subtests never share a backing array.
func buildModel(t *testing.T, pages []Page) *model {
	t.Helper()
	built := &model{standIn: map[string]string{}, artifact: map[string]string{}, seed: "seed"}
	// The fold requires the first record to self-seed the operator roster, the
	// same founding record app.Init writes.
	built.append(t, built.seed, workroom.SchemaState, workroom.State{
		Kind: workroom.KindRoster, Text: "founding operator",
		Body: map[string]string{"actor": modelActor, "kind": "human", "name": "Operator", "role": "operator"},
	})
	for index, act := range declaredActs(pages) {
		id := "act-" + strconv.Itoa(index)
		built.append(t, id, workroom.SchemaState, workroom.State{
			Kind: workroom.KindArtifact,
			Text: "stands in for " + act,
			Body: map[string]string{"path": "governed/" + strconv.Itoa(index), "commit": fakeCommit(act)},
		}, built.seed)
		built.standIn[act] = id
	}
	for index, page := range pages {
		bases := make([]string, 0, len(page.RestsOn))
		for _, act := range page.RestsOn {
			bases = append(bases, built.standIn[act])
		}
		id := "page-" + strconv.Itoa(index)
		built.append(t, id, workroom.SchemaState, workroom.State{
			Kind: workroom.KindArtifact,
			Text: page.Path,
			Body: map[string]string{"path": page.Path, "commit": fakeCommit(page.Path)},
		}, bases...)
		built.artifact[page.Path] = id
	}
	return built
}

// append encodes one payload and adds it to the modelled log.
func (m *model) append(t *testing.T, id, schema string, payload any, restsOn ...string) {
	t.Helper()
	encoded, err := workroom.Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	m.records = append(m.records, workroom.Record{
		ID: id, Actor: modelActor, Schema: schema, RestsOn: restsOn, Payload: encoded,
	})
}

// artifacts folds the modelled log and returns the projection's artifacts by
// event. The fold reports a refused record as a decision rather than an error,
// so every modelled record's decision is checked here: a silently ineffective
// retirement would otherwise read as a page that never flares.
func (m *model) artifacts(t *testing.T) map[string]workroom.Artifact {
	t.Helper()
	projection := workroom.Fold(m.records)
	for _, record := range m.records {
		decision, ok := projection.Decision(record.ID)
		if !ok || decision.Verdict != workroom.Effective {
			t.Fatalf("modelled record %s is not effective: %s", record.ID, decision.Reason)
		}
	}
	found := make(map[string]workroom.Artifact, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
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
	m.append(t, "retire", workroom.SchemaSupersede, workroom.Supersede{
		Target: target, Text: "the world moved under " + act,
	}, target)
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

// The gates above trust that the marks workroom.Fold shows for in-memory
// records are the marks a real Git-backed workroom shows for its signed,
// appended record. This case proves that wiring end to end: one initialized
// repository, acts submitted through the application, the repository reopened
// from disk, and the same marks — stale on a page whose basis was retired,
// unable_to_flare on a page citing nothing — read back from its snapshot.
func TestGateGitBackedWorkroomShowsTheSameMarks(t *testing.T) {
	requireTool(t, "git")
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "room")
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
	file := func(key, text string, restsOn ...string) string {
		submission, err := workspace.Act(ctx, "operator", app.Act{
			Verb: app.VerbState, Kind: workroom.KindArtifact,
			Text:           text,
			Body:           map[string]string{"path": "docs/" + key + ".md", "commit": fakeCommit(key)},
			RestsOn:        restsOn,
			IdempotencyKey: key,
		})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return submission.Record.ID
	}
	basis := file("basis", "the behaviour a page describes", seed.ID)
	anchored := file("anchored", "a page resting on that behaviour", basis)
	unbridged := file("unbridged", "a page citing nothing")
	if _, err := workspace.Act(ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: basis, Text: "the world moved",
		IdempotencyKey: "retire",
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := app.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reopened.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make(map[string]workroom.Artifact, len(snapshot.Projection.Artifacts))
	for _, artifact := range snapshot.Projection.Artifacts {
		artifacts[artifact.Event] = artifact
	}
	if got := artifacts[anchored]; !got.Stale && !got.DescribesSupersededWorld {
		t.Errorf("retiring the basis did not flare the page resting on it: %+v", got)
	}
	if got := artifacts[anchored]; got.UnableToFlare {
		t.Errorf("a page citing a real basis is marked unable to flare: %+v", got)
	}
	if !artifacts[unbridged].UnableToFlare {
		t.Errorf("a page citing nothing is not marked unable to flare: %+v", artifacts[unbridged])
	}
	if !artifacts[basis].Retired {
		t.Errorf("the superseded basis is not marked retired: %+v", artifacts[basis])
	}
}
