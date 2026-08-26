package app

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// The window between a custody re-read loading the fresh record and that
// record being reconciled into the view is exactly where an updateConfig can
// land. This test pins that interleaving with the custodyRereadGate seam: the
// resolver is held after its load while this workspace's own AddActor — a
// real updateConfig, reloading the stored custody under the apphost lock and
// adopting the merged result under configMu — applies in the window. Both
// outcomes must survive: the newer adoption must not be discarded by the
// older load, and the open-time scalars must remain exactly as opened.
// Replacing reconciliation with a whole-field overwrite of the stale record
// fails this test by name.
func TestCustodyRereadAdoptsWithoutClobberingNewerUpdate(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	opened := workspace.View()

	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	late, _, err := external.AddActor(ctx, "operator", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	previousGate := custodyRereadGate
	custodyRereadGate = func() {
		close(reached)
		<-release
	}
	defer func() { custodyRereadGate = previousGate }()

	resolved := make(chan error, 1)
	go func() {
		_, err := workspace.ResolveActor("late")
		resolved <- err
	}()
	<-reached

	newer, _, err := workspace.AddActor(ctx, "operator", "newer", "agent")
	if err != nil {
		t.Fatal(err)
	}

	close(release)
	if err := <-resolved; err != nil {
		t.Fatalf("actor added after open did not resolve across the pinned window: %v", err)
	}

	got := workspace.View()
	if got.Actors["late"].Fingerprint != late.Fingerprint {
		t.Fatalf("re-read adoption lost the freshly stored actor: %+v", got.Actors)
	}
	if got.Actors["newer"] != newer {
		t.Fatalf("the stale re-read clobbered the concurrent updateConfig adoption: %+v", got.Actors["newer"])
	}
	if got.Version != opened.Version || got.Genesis != opened.Genesis || got.ObjectFormat != opened.ObjectFormat ||
		got.PayloadCeiling != opened.PayloadCeiling || got.IdempotencyNamespace != opened.IdempotencyNamespace ||
		got.SequencerKey != opened.SequencerKey || got.ReadOnly != opened.ReadOnly {
		t.Fatalf("the re-read overwrote open-time scalars: opened %+v, now %+v", opened, got)
	}
}

// A removal that lands inside the re-read window through a real updateConfig
// must hold: an actor the load still carries but the live view has already
// dropped stays dropped, because the record the re-read started from held it.
// Merging the fresh record in by union alone resurrects it and fails this
// test by name. The returned answer and the adopted view are one state: the
// late actor resolves to exactly the custody the reconciled view holds.
func TestCustodyRereadKeepsConcurrentRemovalRemoved(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	doomed, _, err := external.AddActor(ctx, "operator", "doomed", "agent")
	if err != nil {
		t.Fatal(err)
	}
	// Bring doomed into this workspace's cached view first, so the re-read's
	// baseline is a view that held it.
	if got, err := workspace.ResolveActor("doomed"); err != nil || got.Fingerprint != doomed.Fingerprint {
		t.Fatalf("doomed did not enter the cached view (%+v, %v)", got, err)
	}
	if _, _, err := external.AddActor(ctx, "operator", "late", "agent"); err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	previousGate := custodyRereadGate
	custodyRereadGate = func() {
		close(reached)
		<-release
	}
	defer func() { custodyRereadGate = previousGate }()

	resolved := make(chan error, 1)
	got := make(chan apphost.Actor, 1)
	go func() {
		actor, err := workspace.ResolveActor("late")
		resolved <- err
		got <- actor
	}()
	<-reached

	if _, err := workspace.RetireActor(ctx, "operator", "@doomed"); err != nil {
		t.Fatal(err)
	}

	close(release)
	if err := <-resolved; err != nil {
		t.Fatalf("the late actor did not resolve across the pinned removal: %v", err)
	}
	actor := <-got

	viewed := workspace.View()
	if viewed.Actors["late"] != actor {
		t.Fatalf("returned custody %+v is not the reconciled view's %+v", actor, viewed.Actors["late"])
	}
	if _, held := viewed.Actors["doomed"]; held {
		t.Fatalf("a concurrently removed actor was resurrected by the re-read: %+v", viewed.Actors)
	}
}

// The reconciled frontier follows mergeVerifiedFrontier rather than a
// depth-only replacement: a strictly deeper stored marker advances the live
// one on adoption, and an equal-depth marker naming a different head is the
// established conflict — refused, not resolved by order of arrival.
func TestCustodyRereadFrontierFollowsTheEstablishedMergeRule(t *testing.T) {
	ctx := context.Background()
	workspace, genesis, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := external.AddActor(ctx, "operator", "late", "agent"); err != nil {
		t.Fatal(err)
	}
	held := workspace.View().VerifiedFrontier
	if held == nil {
		t.Fatal("this workspace verified no frontier to reconcile against")
	}

	deeper := &apphost.VerifiedFrontier{Head: strings.Repeat("b", 40), Depth: held.Depth + 1}
	if err := storeFrontier(workspace.MetaDir, deeper); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ResolveActor("late"); err != nil {
		t.Fatalf("actor added after open did not resolve against a deeper stored frontier: %v", err)
	}
	viewed := workspace.View()
	if viewed.VerifiedFrontier == nil || *viewed.VerifiedFrontier != *deeper {
		t.Fatalf("frontier after adoption = %+v, want the deeper stored marker %+v", viewed.VerifiedFrontier, deeper)
	}

	conflicting := &apphost.VerifiedFrontier{Head: genesis.ID, Depth: deeper.Depth}
	if err := storeFrontier(workspace.MetaDir, conflicting); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ResolveActorAddress("@nobody"); err == nil || !strings.Contains(err.Error(), "refuse conflicting verified frontier") {
		t.Fatalf("equal-depth conflicting frontier = %v, want the established conflict refusal", err)
	}
	if kept := workspace.View().VerifiedFrontier; kept == nil || *kept != *deeper {
		t.Fatalf("a refused conflict moved the live frontier to %+v, want it unchanged at %+v", kept, deeper)
	}
}

// storeFrontier records one verified frontier in the configuration file the
// way another process would, leaving every other stored field alone.
func storeFrontier(metaDir string, frontier *apphost.VerifiedFrontier) error {
	_, err := apphost.UpdateConfig(metaDir, apphost.Config{}, func(c *apphost.Config) (bool, error) {
		c.VerifiedFrontier = frontier
		return true, nil
	})
	return err
}
