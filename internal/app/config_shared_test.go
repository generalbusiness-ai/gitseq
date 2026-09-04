package app

// config.json is one record shared by every process that opened the
// repository, not a private copy per Workspace. The tests here pin the
// judgements a workspace must make against that shared record rather than
// against its own memory. They open two Workspace values in this one process,
// which proves the routing and says nothing about processes; the
// cross-process facts — one transaction excluding another, and a reader
// excluding a writer — are proven by the subprocess tests in
// internal/apphost.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func storedFrontier(t *testing.T, metaDir string) apphost.VerifiedFrontier {
	t.Helper()
	config, err := apphost.LoadConfig(metaDir)
	if err != nil {
		t.Fatal(err)
	}
	if config.VerifiedFrontier == nil {
		t.Fatal("stored config has no verified frontier")
	}
	return *config.VerifiedFrontier
}

// A workspace whose own memory already records the verification's head must
// still judge that verification against the stored file: memory answering
// ahead of the transaction is the bypass this record exists to close. Here
// the stored frontier has moved past this workspace's memory, so
// re-remembering the position its memory holds must refuse as a rollback — a
// memory-first fast path would instead report success without ever reading
// the file, and the stored marker would be free to move backwards next time.
func TestStaleMemoryCannotAnswerForTheStoredFrontier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	founder, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	remembered := storedFrontier(t, founder.MetaDir)
	mover, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, mover, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "advance past stale memory",
		RestsOn: []string{seed.ID}, IdempotencyKey: "stale-memory-advance",
	})
	if _, err := mover.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	newest := storedFrontier(t, founder.MetaDir)
	if newest.Depth != remembered.Depth+1 {
		t.Fatalf("stored frontier depth = %d, want %d", newest.Depth, remembered.Depth+1)
	}
	err = stale.rememberVerifiedFrontier(ctx, kernel.Verification{Head: remembered.Head, Depth: remembered.Depth})
	if err == nil || !strings.Contains(err.Error(), "shorter than previously verified") {
		t.Fatalf("stale memory answered for the stored frontier: err = %v", err)
	}
	if after := storedFrontier(t, founder.MetaDir); after != newest {
		t.Fatalf("stored frontier moved to %+v, want %+v", after, newest)
	}
}

// Initialization stores the record exclusively, so an initializer that lost
// the race to a concurrent one is refused rather than overwriting what that
// one stored. The window is pinned rather than raced: the gate holds this
// init between observing no configuration and storing its own, and the test
// stores a different configuration in that window. An overwriting save would
// report success and replace a stored genesis with one nobody else can read.
func TestInitRefusesToOverwriteAConfigurationStoredInsideItsWindow(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	reached := make(chan struct{})
	release := make(chan struct{})
	previousGate := initAbsenceGate
	initAbsenceGate = func() {
		close(reached)
		<-release
	}
	defer func() { initAbsenceGate = previousGate }()

	done := make(chan error, 1)
	go func() {
		_, _, err := Init(ctx, repo, "human", 1<<20)
		done <- err
	}()
	<-reached

	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	metaDir := apphost.MetaDir(commonDir)
	winner := apphost.Config{Version: 0, Genesis: strings.Repeat("a", 40), ObjectFormat: "sha1", ReadOnly: true}
	if err := apphost.CreateConfig(metaDir, winner); err != nil {
		t.Fatal(err)
	}
	close(release)

	if err := <-done; !errors.Is(err, os.ErrExist) {
		t.Fatalf("init that lost the creation race error = %v, want it refused with os.ErrExist", err)
	}
	stored, err := apphost.LoadConfig(metaDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Genesis != winner.Genesis || !stored.ReadOnly {
		t.Fatalf("the refused init overwrote the stored configuration: %+v", stored)
	}
}
