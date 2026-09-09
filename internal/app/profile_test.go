package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// A new binary that reuses the signed kernel checkpoint re-interprets the
// projection without a cold audit, so while that interpretation is pending
// nothing is "running". The profile is what changes, and it changes at once:
// a reader comparing profiles learns the retained projection came from
// another contract even though no rebuild progress is reported. Ported from
// codex's review evidence 6d4c1e50.
func TestProfileNamesTheNewInterpreterWhileCheckpointReinterpretationIsPending(t *testing.T) {
	ctx := context.Background()
	w, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	before, err := w.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := w.Profile()
	if old == "" {
		t.Fatal("an opened workspace names no profile")
	}
	w.selected = selection{host: host{application: apphost.DefaultApplication, foldVersion: workroom.ProfileVersion + "-probe-change", newFolder: workroom.NewFolder}}
	if w.Profile() == old {
		t.Fatal("a changed fold version did not change the profile")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	w.SetProjectionRebuildTestGate(func(int) { close(entered); <-release })
	result := make(chan error, 1)
	go func() { _, err := w.Snapshot(ctx); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("profile re-interpretation did not reach its publication gate")
	}
	// Nothing is running: the checkpoint was reused, so there is no cold
	// audit to report. The profile alone carries the change.
	if _, running := w.RebuildProgress(); running {
		t.Fatal("a checkpoint-backed re-interpretation reported a running cold audit")
	}
	if w.Profile() == old {
		t.Fatal("the profile reverted while re-interpretation was pending")
	}
	select {
	case err := <-result:
		t.Fatalf("snapshot returned before publication: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("snapshot did not finish after release")
	}
	after, err := w.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head {
		t.Fatalf("re-interpretation moved the frontier: %s then %s", before.Head, after.Head)
	}
	// Binding: the published snapshot was folded under exactly the identity
	// Profile derives from, so a status carrying both is self-consistent.
	w.snapshotMu.Lock()
	published := w.snapshotProfile
	w.snapshotMu.Unlock()
	if published != w.selected.host.projectionProfile() {
		t.Fatalf("snapshot published under %q, selected identity %q", published, w.selected.host.projectionProfile())
	}
}

// The identifier follows the cache-invalidating identity exactly: same
// application and fold version, same profile; either differing, different.
func TestProfileFollowsTheProjectionProfileIdentity(t *testing.T) {
	ctx := context.Background()
	w, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	base := w.selected.host
	same := w.Profile()
	w.selected = selection{host: host{application: base.application, foldVersion: base.foldVersion, newFolder: base.newFolder}}
	if w.Profile() != same {
		t.Fatal("an equal identity produced a different profile")
	}
	w.selected = selection{host: host{application: base.application + "-other", foldVersion: base.foldVersion, newFolder: base.newFolder}}
	if w.Profile() == same {
		t.Fatal("a different application produced the same profile")
	}
	w.selected = selection{host: host{application: base.application, foldVersion: base.foldVersion + "-other", newFolder: base.newFolder}}
	if w.Profile() == same {
		t.Fatal("a different fold version produced the same profile")
	}
}
