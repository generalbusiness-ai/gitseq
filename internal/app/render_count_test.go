package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
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

// snapshotTimings keeps the duration of every snapshot measurement by path.
type snapshotTimings struct {
	mu       sync.Mutex
	recorded map[observe.Path][]time.Duration
}

func (o *snapshotTimings) Record(_ context.Context, measurement observe.Measurement) {
	if measurement.Operation != observe.OperationSnapshot {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.recorded[measurement.Path] = append(o.recorded[measurement.Path], measurement.Duration)
}

// A read that renders the projection a submission deferred is timed with the
// render inside it. Recording first would report a whole-log render as a
// near-instant cache hit.
func TestADeferredRenderIsTimedByTheReadThatPaysForIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	initialised, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	timings := &snapshotTimings{recorded: make(map[observe.Path][]time.Duration)}
	workspace, err := OpenObserved(ctx, initialised.Repo, timings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "deferred",
		RestsOn: []string{seed.ID}, IdempotencyKey: "deferred-render",
	})
	const render = 200 * time.Millisecond
	workspace.renderTestHook = func(world string) {
		if world == "snapshot" {
			time.Sleep(render)
		}
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	timings.mu.Lock()
	defer timings.mu.Unlock()
	cached := timings.recorded[observe.PathCache]
	if len(cached) == 0 {
		t.Fatal("the read that rendered the deferred projection recorded no cache measurement")
	}
	if got := cached[len(cached)-1]; got < render {
		t.Fatalf("the read that rendered the deferred projection was recorded as %v, less than the %v render inside it", got, render)
	}
}
