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

// When one name moves differently on the live view and in the freshly loaded
// record across the pinned window, the reconciliation must refuse closed: the
// resolver reports an explicit refusal instead of picking a winner, and the
// adopted view stays exactly as the concurrent updateConfig left it — the
// retired name stays gone, the late actor the resolver was fetching is not
// slipped in, and the verified frontier does not move. Replacing the refusal
// with any arbitration that adopts either side fails this test by name.
func TestCustodyRereadRefusesDivergentChangeAndLeavesTheViewUntouched(t *testing.T) {
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
	held, _, err := external.AddActor(ctx, "operator", "contested", "agent")
	if err != nil {
		t.Fatal(err)
	}
	// Bring contested into this workspace's cached view first, so the
	// re-read's baseline is a view that held it.
	if got, err := workspace.ResolveActor("contested"); err != nil || got.Fingerprint != held.Fingerprint {
		t.Fatalf("contested did not enter the cached view (%+v, %v)", got, err)
	}
	replacement := apphost.Actor{Name: "contested", Fingerprint: strings.Repeat("f", 40), KeyFile: held.KeyFile + ".replaced"}
	if err := storeActorChange(workspace.MetaDir, func(actors map[string]apphost.Actor) {
		actors["contested"] = replacement
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := apphost.LoadConfig(workspace.MetaDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Actors["contested"] != replacement {
		t.Fatalf("the external replacement did not land on disk: %+v", stored.Actors["contested"])
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

	if _, err := workspace.RetireActor(ctx, "operator", "@contested"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := external.AddActor(ctx, "operator", "late", "agent"); err != nil {
		t.Fatal(err)
	}
	before := workspace.View()

	close(release)
	err = <-resolved
	if err == nil || !strings.Contains(err.Error(), "refuse divergent custody") || !strings.Contains(err.Error(), "contested") {
		t.Fatalf("divergent custody across the pinned window = %v, want an explicit refusal naming the actor", err)
	}

	viewed := workspace.View()
	if _, present := viewed.Actors["contested"]; present {
		t.Fatalf("a retired actor came back through the refused reconciliation: %+v", viewed.Actors["contested"])
	}
	if _, present := viewed.Actors["late"]; present {
		t.Fatalf("a refused reconciliation adopted the late actor the resolver was fetching: %+v", viewed.Actors["late"])
	}
	if len(viewed.Actors) != len(before.Actors) || viewed.Actors["operator"] != before.Actors["operator"] {
		t.Fatalf("the failed reconciliation moved the view %+v, want it untouched at %+v", viewed.Actors, before.Actors)
	}
	if viewed.VerifiedFrontier == nil || before.VerifiedFrontier == nil ||
		*viewed.VerifiedFrontier != *before.VerifiedFrontier {
		t.Fatalf("the failed reconciliation moved the frontier %+v -> %+v, want it unchanged", before.VerifiedFrontier, viewed.VerifiedFrontier)
	}
}

// The reconciled frontier follows mergeVerifiedFrontier rather than a
// depth-only replacement: a strictly deeper stored marker advances the live
// one on adoption, and an equal-depth marker naming a different head is the
// established conflict — refused, not resolved by order of arrival.
func TestCustodyRereadFrontierFollowsTheEstablishedMergeRule(t *testing.T) {
	t.Parallel()
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
// storeFrontier moves the stored frontier marker the way another process
// would: opening the configuration first, then updating it, because an update
// is refused unless the identity it opened matches the stored file.
func storeFrontier(metaDir string, frontier *apphost.VerifiedFrontier) error {
	opened, err := apphost.LoadConfig(metaDir)
	if err != nil {
		return err
	}
	_, err = apphost.UpdateConfig(metaDir, opened, func(c *apphost.Config) (bool, error) {
		c.VerifiedFrontier = frontier
		return true, nil
	})
	return err
}

// storeActorChange edits the actor map in the configuration file the way
// another process would, leaving every other stored field alone.
func storeActorChange(metaDir string, change func(actors map[string]apphost.Actor)) error {
	opened, err := apphost.LoadConfig(metaDir)
	if err != nil {
		return err
	}
	_, err = apphost.UpdateConfig(metaDir, opened, func(c *apphost.Config) (bool, error) {
		if c.Actors == nil {
			c.Actors = make(map[string]apphost.Actor)
		}
		change(c.Actors)
		return true, nil
	})
	return err
}

// A pre-window replacement of a name the live view already holds must be
// adopted when the view has not moved since the re-read started: live equal
// to baseline means this workspace learned nothing, so fresh stands —
// including a replacement of that name's custody. Copying held entries ahead
// of fresh, union-style, resurrects the stale copy and fails this test by
// name.
func TestCustodyRereadAdoptsFreshReplacementWhileLiveUnchanged(t *testing.T) {
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
	swapped, _, err := external.AddActor(ctx, "operator", "swapped", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := workspace.ResolveActor("swapped"); err != nil || got.Fingerprint != swapped.Fingerprint {
		t.Fatalf("swapped did not enter the cached view (%+v, %v)", got, err)
	}
	replacement := apphost.Actor{Name: "swapped", Fingerprint: strings.Repeat("f", 40), KeyFile: swapped.KeyFile + ".replaced"}
	if err := storeActorChange(workspace.MetaDir, func(actors map[string]apphost.Actor) {
		actors["swapped"] = replacement
	}); err != nil {
		t.Fatal(err)
	}
	late, _, err := external.AddActor(ctx, "operator", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}

	// The gate holds the resolver between its load — which sees the
	// replacement and the late actor — and its reconciliation, with this
	// workspace's live view still standing exactly at the baseline.
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
	close(release)

	if err := <-resolved; err != nil {
		t.Fatalf("the late actor did not resolve across the pinned replacement: %v", err)
	}
	viewed := workspace.View()
	if viewed.Actors["late"].Fingerprint != late.Fingerprint {
		t.Fatalf("the freshly stored actor was lost: %+v", viewed.Actors["late"])
	}
	if viewed.Actors["swapped"] != replacement {
		t.Fatalf("the view kept stale custody %+v, want the fresh replacement %+v", viewed.Actors["swapped"], replacement)
	}
}

// A pre-window deletion of a name the live view already holds must stand when
// the view has not moved since the re-read started: live equal to baseline
// means this workspace learned nothing, so fresh's removal is adopted.
// Union-merging the fresh record onto the live map resurrects the deleted
// name and fails this test by name.
func TestCustodyRereadKeepsFreshDeletionWhileLiveUnchanged(t *testing.T) {
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
	if got, err := workspace.ResolveActor("doomed"); err != nil || got.Fingerprint != doomed.Fingerprint {
		t.Fatalf("doomed did not enter the cached view (%+v, %v)", got, err)
	}
	if err := storeActorChange(workspace.MetaDir, func(actors map[string]apphost.Actor) {
		delete(actors, "doomed")
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := external.AddActor(ctx, "operator", "late", "agent"); err != nil {
		t.Fatal(err)
	}

	// The gate holds the resolver between its load — which no longer carries
	// the deleted actor — and its reconciliation, with this workspace's live
	// view still standing exactly at the baseline.
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
	close(release)

	if err := <-resolved; err != nil {
		t.Fatalf("the late actor did not resolve across the pinned deletion: %v", err)
	}
	viewed := workspace.View()
	if _, resurrected := viewed.Actors["doomed"]; resurrected {
		t.Fatalf("a freshly deleted actor was resurrected while live stood still: %+v", viewed.Actors)
	}
	if _, adopted := viewed.Actors["late"]; !adopted {
		t.Fatalf("the freshly added actor was lost by the same adoption: %+v", viewed.Actors)
	}
}

// When live and fresh both changed one name away from the baseline,
// differently, the reconciliation refuses closed: no verified-frontier
// arbitration picks a winner, the error names the actor, and nothing is
// adopted — including a replacement racing a deletion. An agreed change is
// taken once, and an agreed deletion preserves absence instead of inserting
// a zero Actor.
func TestReconcileCustodyResolvesDivergentNameByTheDocumentedRule(t *testing.T) {
	t.Parallel()
	baseline := &apphost.Config{Actors: map[string]apphost.Actor{
		"contested": {Name: "contested", Fingerprint: strings.Repeat("0", 40), KeyFile: "keys/contested-base"},
		"bystander": {Name: "bystander", Fingerprint: strings.Repeat("9", 40), KeyFile: "keys/bystander"},
	}}
	liveEntry := apphost.Actor{Name: "contested", Fingerprint: strings.Repeat("1", 40), KeyFile: "keys/contested-live"}
	freshEntry := apphost.Actor{Name: "contested", Fingerprint: strings.Repeat("2", 40), KeyFile: "keys/contested-fresh"}

	withContested := func(entry apphost.Actor, frontier *apphost.VerifiedFrontier) *apphost.Config {
		return &apphost.Config{
			Actors: map[string]apphost.Actor{
				"contested": entry,
				"bystander": baseline.Actors["bystander"],
			},
			VerifiedFrontier: frontier,
		}
	}
	withoutContested := func(frontier *apphost.VerifiedFrontier) *apphost.Config {
		return &apphost.Config{
			Actors:           map[string]apphost.Actor{"bystander": baseline.Actors["bystander"]},
			VerifiedFrontier: frontier,
		}
	}
	liveHeld := &apphost.VerifiedFrontier{Head: strings.Repeat("a", 40), Depth: 7}
	deeper := &apphost.VerifiedFrontier{Head: strings.Repeat("b", 40), Depth: 8}

	t.Run("a divergent change is refused closed", func(t *testing.T) {
		actors, frontier, err := reconcileCustody(baseline, withContested(liveEntry, liveHeld), withContested(freshEntry, deeper))
		if err == nil || !strings.Contains(err.Error(), "refuse") || !strings.Contains(err.Error(), "contested") {
			t.Fatalf("divergent custody = %v, want an explicit refusal naming the actor", err)
		}
		if actors != nil || frontier != nil {
			t.Fatalf("a refused conflict returned actors %+v, frontier %+v, want nothing adopted", actors, frontier)
		}
	})

	t.Run("a replacement racing a deletion is refused too", func(t *testing.T) {
		actors, frontier, err := reconcileCustody(baseline, withoutContested(liveHeld), withContested(freshEntry, deeper))
		if err == nil || !strings.Contains(err.Error(), "refuse") || !strings.Contains(err.Error(), "contested") {
			t.Fatalf("replacement racing deletion = %v, want an explicit refusal naming the actor", err)
		}
		if actors != nil || frontier != nil {
			t.Fatalf("a refused conflict returned actors %+v, frontier %+v, want nothing adopted", actors, frontier)
		}
	})

	t.Run("an agreed change is taken once", func(t *testing.T) {
		actors, _, err := reconcileCustody(baseline, withContested(freshEntry, liveHeld), withContested(freshEntry, liveHeld))
		if err != nil {
			t.Fatal(err)
		}
		if actors["contested"] != freshEntry || actors["bystander"] != baseline.Actors["bystander"] {
			t.Fatalf("agreed merge produced %+v, want the agreed entry and the bystander intact", actors)
		}
	})

	t.Run("an agreed deletion preserves absence", func(t *testing.T) {
		goneBaseline := &apphost.Config{Actors: map[string]apphost.Actor{
			"gone":      {Name: "gone", Fingerprint: strings.Repeat("5", 40), KeyFile: "keys/gone"},
			"bystander": baseline.Actors["bystander"],
		}}
		live := withoutContested(liveHeld)
		fresh := withoutContested(deeper)
		actors, _, err := reconcileCustody(goneBaseline, live, fresh)
		if err != nil {
			t.Fatal(err)
		}
		if _, held := actors["gone"]; held {
			t.Fatalf("an agreed deletion inserted %+#v instead of preserving absence", actors["gone"])
		}
		if actors["bystander"] != baseline.Actors["bystander"] {
			t.Fatalf("the bystander was disturbed by the agreed deletion: %+v", actors["bystander"])
		}
	})
}

// A fresh record carrying no verified frontier must neither panic nor move
// the local witness: the live frontier stays exactly as it was while the rest
// of the fresh record is still reconciled in. Dereferencing the absent
// marker, or dropping the live one, fails this test by name.
func TestCustodyRereadWithNilFreshFrontierKeepsLiveMonotonic(t *testing.T) {
	t.Parallel()
	held := &apphost.VerifiedFrontier{Head: strings.Repeat("c", 40), Depth: 11}

	got, changed, err := mergeVerifiedFrontier(held, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed || got == nil || got.Head != held.Head || got.Depth != held.Depth {
		t.Fatalf("merge against a nil fresh marker = (%+v, %v), want the live marker unchanged", got, changed)
	}

	baseline := &apphost.Config{}
	live := &apphost.Config{
		Actors:           map[string]apphost.Actor{"kept": {Name: "kept", Fingerprint: strings.Repeat("3", 40), KeyFile: "keys/kept"}},
		VerifiedFrontier: held,
	}
	fresh := &apphost.Config{Actors: map[string]apphost.Actor{
		"kept": live.Actors["kept"],
		"late": {Name: "late", Fingerprint: strings.Repeat("4", 40), KeyFile: "keys/late"},
	}}
	actors, frontier, err := reconcileCustody(baseline, live, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if frontier == nil || frontier.Head != held.Head || frontier.Depth != held.Depth {
		t.Fatalf("reconciliation moved the live frontier to %+v, want it kept at %+v", frontier, held)
	}
	if _, adopted := actors["late"]; !adopted {
		t.Fatalf("the fresh actor was lost because the fresh marker was absent: %+v", actors)
	}
}
