package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

// Two attaches race to create one configuration while a reader opens it
// mid-flight. The interleaving is pinned, not stochastic: the first attach is
// held at the absence gate — it has seen no configuration and not yet created
// one — while the second attach runs to completion and an Open reads whatever
// is stored. Whoever wins, one invariant holds: every success observes the
// genesis the file finally stores, and a successful attach observes the
// genesis it asked for. A torn write would surface as a success observing a
// genesis nobody stored; a lost update as an attach that reported success for
// a genesis a later writer silently replaced.
func TestConcurrentAttachAndOpenAgreeOnStoredGenesis(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	metaDir := apphost.MetaDir(commonDir)

	// Attaching needs a genuine genesis envelope in the repository, so each
	// contender names a distinct real sequence; the differing payload ceiling
	// keeps the two commits distinct.
	store := gitstore.Store{Repo: commonDir}
	sequencerKey := filepath.Join(t.TempDir(), "sequencer")
	publicKey, err := gitstore.GenerateSSHKey(ctx, sequencerKey)
	if err != nil {
		t.Fatal(err)
	}
	geneses := make([]string, 2)
	for i := range geneses {
		genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{
			Version: 0, ObjectFormat: "sha1", PayloadCeiling: uint64(1<<20 + i), SequencerPublicKey: publicKey,
		}, sequencerKey)
		if err != nil {
			t.Fatal(err)
		}
		geneses[i] = genesis
	}

	gateReached := make(chan struct{})
	gateRelease := make(chan struct{})
	var once sync.Once
	previousGate := attachAbsenceGate
	attachAbsenceGate = func() {
		held := false
		once.Do(func() { held = true })
		if held {
			close(gateReached)
			<-gateRelease
		}
	}
	defer func() { attachAbsenceGate = previousGate }()

	type outcome struct {
		requested string
		observed  string
		err       error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		workspace, err := AttachConfig(ctx, repo, geneses[0], "sha1")
		result := outcome{requested: geneses[0], err: err}
		if err == nil {
			result.observed = workspace.config.Genesis
		}
		firstDone <- result
	}()
	<-gateReached

	second := outcome{requested: geneses[1]}
	if workspace, err := AttachConfig(ctx, repo, geneses[1], "sha1"); err != nil {
		second.err = err
	} else {
		second.observed = workspace.config.Genesis
	}
	during := outcome{}
	if workspace, err := Open(ctx, repo); err != nil {
		during.err = err
	} else {
		during.observed = workspace.config.Genesis
	}

	close(gateRelease)
	first := <-firstDone

	stored, err := apphost.LoadConfig(metaDir)
	if err != nil {
		t.Fatalf("no stored configuration survived the race: %v", err)
	}
	successes := 0
	for _, attach := range []outcome{first, second} {
		if attach.err != nil {
			continue
		}
		successes++
		if attach.observed != stored.Genesis {
			t.Errorf("successful attach of %s observed genesis %s, but the store finally holds %s", attach.requested, attach.observed, stored.Genesis)
		}
		if attach.requested != stored.Genesis {
			t.Errorf("attach of %s reported success, but the store finally holds %s: its update was lost", attach.requested, stored.Genesis)
		}
	}
	if successes == 0 {
		t.Errorf("no attach succeeded (first: %v; second: %v); one creator must win", first.err, second.err)
	}
	if during.err == nil && during.observed != stored.Genesis {
		t.Errorf("successful mid-flight open observed genesis %s, but the store finally holds %s", during.observed, stored.Genesis)
	}
}
