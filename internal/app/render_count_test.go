package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// renderCounter counts whole-projection renders by the world they rendered.
type renderCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func countRenders(workspace *Workspace) *renderCounter {
	counter := &renderCounter{counts: make(map[string]int)}
	workspace.renderTestHook = func(world string) {
		counter.mu.Lock()
		defer counter.mu.Unlock()
		counter.counts[world]++
	}
	return counter
}

func (c *renderCounter) of(world string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[world]
}

func encodedSnapshot(t *testing.T, snapshot Snapshot) []byte {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A chain of acts this workspace sequences, with no reader between them,
// renders no projection for any of them. The first reader renders it once, and
// what it renders is byte for byte what a cold audit of the same log renders.
// A merge succession is exactly such a chain, and rendering after every act was
// most of what each act cost at depth.
func TestSequencedActsRenderTheSnapshotOnlyWhenRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	renders := countRenders(workspace)
	const acts = 4
	for index := range acts {
		actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: fmt.Sprintf("act %d", index),
			RestsOn: []string{seed.ID}, IdempotencyKey: fmt.Sprintf("unread-%d", index),
		})
	}
	if got := renders.of("snapshot"); got != 0 {
		t.Fatalf("%d sequenced acts rendered the snapshot %d times with no reader, want 0", acts, got)
	}
	read, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := renders.of("snapshot"); got != 1 {
		t.Fatalf("the first read rendered the snapshot %d times, want 1", got)
	}
	if read.Source != SnapshotSourceIncrementalTail {
		t.Fatalf("snapshot source = %q, want the incremental tail the acts left", read.Source)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if got := renders.of("snapshot"); got != 1 {
		t.Fatalf("a second read at the same head rendered again: %d renders, want 1", got)
	}
	cold, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	audited, err := cold.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if audited.Depth != read.Snapshot.Depth || audited.Head != read.Snapshot.Head {
		t.Fatalf("read frontier %s at %d, cold audit %s at %d", read.Snapshot.Head, read.Snapshot.Depth, audited.Head, audited.Depth)
	}
	if !bytes.Equal(encodedSnapshot(t, read.Snapshot), encodedSnapshot(t, audited)) {
		t.Fatal("the snapshot rendered on first read differs from a cold audit of the same log")
	}
}

// Admission renders its private world once per head. Judging several acts
// against an unmoved head, as a batch preflight does, reads the one rendering;
// the next event moves the head and the world is rendered again, holding it.
func TestAdmissionRendersItsWorldOncePerHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	renders := countRenders(workspace)
	head, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := workspace.admissionWorld(ctx, head); err != nil {
			t.Fatal(err)
		}
	}
	if got := renders.of("admission"); got != 1 {
		t.Fatalf("three admissions at one head rendered %d times, want 1", got)
	}
	landed := actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "moves the head",
		RestsOn: []string{seed.ID}, IdempotencyKey: "admission-moves",
	})
	moved, err := workspace.Store.Head(ctx, kernel.Ref(workspace.config.Genesis))
	if err != nil {
		t.Fatal(err)
	}
	before := renders.of("admission")
	world, err := workspace.admissionWorld(ctx, moved)
	if err != nil {
		t.Fatal(err)
	}
	if got := renders.of("admission"); got != before+1 {
		t.Fatalf("admission at a moved head rendered %d times, want 1", got-before)
	}
	held := false
	for _, decision := range world.Projection.Decisions {
		held = held || decision.Event == landed.ID
	}
	if world.Head != moved || !held {
		t.Fatalf("admission world at %s does not hold the event that moved the head to %s", world.Head, moved)
	}
	audited, err := workspace.ReadOnlySnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encodedSnapshot(t, world), encodedSnapshot(t, audited)) {
		t.Fatal("the admission world at the moved head differs from a read-only audit of the same log")
	}
}
