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
	"sync"
	"testing"
	"time"

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

// The witness refuses a sequence ref that moved backwards, and nothing else.
// A verification shorter than the stored witness whose ref still stands on
// the witnessed head is a read that finished after an appender moved the
// world on, so it is admitted and the witness is left where the appender put
// it. Refusing it, as this path once did, made every slow reader verify the
// whole sequence again for nothing.
func TestStaleVerificationIsAdmittedWhileTheRefStillContinuesTheWitness(t *testing.T) {
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
	newest := advancePastMemory(t, ctx, founder, seed, remembered, "stale-read-advance")

	if err := stale.rememberVerifiedFrontier(ctx, kernel.Verification{Head: remembered.Head, Depth: remembered.Depth}); err != nil {
		t.Fatalf("stale read refused while the ref still continued the witness: %v", err)
	}
	if after := storedFrontier(t, founder.MetaDir); after != newest {
		t.Fatalf("admitted stale read moved the stored witness to %+v, want %+v", after, newest)
	}
	// The transaction hands the newest stored witness back, so this
	// workspace's memory follows the file rather than its own older read.
	if adopted := stale.View().VerifiedFrontier; adopted == nil || *adopted != newest {
		t.Fatalf("admitted stale read left memory at %+v, want %+v", adopted, newest)
	}
}

// A workspace whose own memory already records the verification's head must
// still judge that verification against the stored file: memory answering
// ahead of the transaction is the bypass this record exists to close. Here
// the stored witness has moved past this workspace's memory and the ref has
// been rolled back to what that memory holds, so re-remembering it must
// refuse — a memory-first fast path would instead report success without ever
// reading the file, and the stored marker would be free to move backwards
// next time.
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
	newest := advancePastMemory(t, ctx, founder, seed, remembered, "stale-memory-advance")

	// Roll the ref itself back to the shorter head. That, and not the
	// shortness of the read, is what the witness refuses.
	if err := founder.Store.UpdateRef(ctx, kernel.Ref(founder.View().Genesis), remembered.Head, newest.Head); err != nil {
		t.Fatal(err)
	}
	err = stale.rememberVerifiedFrontier(ctx, kernel.Verification{Head: remembered.Head, Depth: remembered.Depth})
	if err == nil || !strings.Contains(err.Error(), "shorter than previously verified") {
		t.Fatalf("stale memory answered for the stored frontier: err = %v", err)
	}
	if after := storedFrontier(t, founder.MetaDir); after != newest {
		t.Fatalf("stored frontier moved to %+v, want %+v", after, newest)
	}
}

// A shorter verification standing on a history the witnessed head never
// carried is a rollback whatever the ref does, so it is refused even though
// the ref still continues the witness.
func TestVerificationOffTheWitnessedHistoryIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	founder, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	genesis := founder.View().Genesis
	ref := kernel.Ref(genesis)
	first := actRecord(t, ctx, founder, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "shared base",
		RestsOn: []string{seed.ID}, IdempotencyKey: "off-history-base",
	})
	abandoned := actRecord(t, ctx, founder, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "abandoned line",
		RestsOn: []string{seed.ID}, IdempotencyKey: "off-history-abandoned",
	})
	base := eventCommit(t, founder.View().ObjectFormat, first.ID)
	abandonedHead := eventCommit(t, founder.View().ObjectFormat, abandoned.ID)

	// Rewind to the shared base and grow a different line from it, so the
	// abandoned head is reachable in the object store but on no history the
	// witness will name.
	if err := founder.Store.UpdateRef(ctx, ref, base, abandonedHead); err != nil {
		t.Fatal(err)
	}
	sibling, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, sibling, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "sibling line",
		RestsOn: []string{seed.ID}, IdempotencyKey: "off-history-sibling",
	})
	actRecord(t, ctx, sibling, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "sibling tip",
		RestsOn: []string{seed.ID}, IdempotencyKey: "off-history-tip",
	})
	auditor, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	witnessed, err := auditor.Verify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stored := storedFrontier(t, founder.MetaDir)
	if stored.Head != witnessed.Head || stored.Depth != witnessed.Depth {
		t.Fatalf("witness = %+v, want the sibling tip %+v", stored, witnessed)
	}

	// The abandoned head is one shorter than the witness and the ref stands
	// exactly on the witnessed head, so only the ancestry test can refuse it.
	err = auditor.rememberVerifiedFrontier(ctx, kernel.Verification{Head: abandonedHead, Depth: witnessed.Depth - 1})
	if err == nil || !strings.Contains(err.Error(), "shorter than previously verified") {
		t.Fatalf("verification off the witnessed history was admitted: err = %v", err)
	}
	if after := storedFrontier(t, founder.MetaDir); after != stored {
		t.Fatalf("refused verification moved the witness to %+v, want %+v", after, stored)
	}
}

// The refusal this rule removes was a race, not a state: a command verified a
// deep sequence for minutes while the resident on the same checkout appended,
// and the finished read was judged against a witness that had moved. The gate
// holds a cold audit open, a second workspace appends and audits inside that
// window, and the held read must still complete — returning the world it
// actually verified, and leaving the deeper witness alone.
func TestSnapshotInFlightSurvivesAConcurrentAppendAndWitnessAdvance(t *testing.T) {
	ctx := context.Background()
	founder, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		actRecord(t, ctx, founder, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: text,
			RestsOn: []string{seed.ID}, IdempotencyKey: "in-flight-" + text,
		})
	}
	// The gate pauses a commit-by-commit audit, so drop the checkpoint the
	// appends published; otherwise the read is answered from it and never
	// occupies the window this test is about.
	if err := founder.InvalidateCheckpoint(ctx); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	reader.SetRebuildTestGate(func(kernel.Progress) {
		once.Do(func() {
			close(entered)
			<-release
		})
	})

	type read struct {
		snapshot Snapshot
		err      error
	}
	done := make(chan read, 1)
	go func() {
		snapshot, err := reader.Snapshot(ctx)
		done <- read{snapshot, err}
	}()
	select {
	case <-entered:
	case result := <-done:
		t.Fatalf("the cold audit never reached the gate: %+v, %v", result.snapshot, result.err)
	case <-time.After(60 * time.Second):
		t.Fatal("the cold audit never reached the gate")
	}

	mover, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, mover, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "appended under the reader",
		RestsOn: []string{seed.ID}, IdempotencyKey: "in-flight-concurrent-append",
	})
	if _, err := mover.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	newest := storedFrontier(t, founder.MetaDir)
	close(release)

	var result read
	select {
	case result = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the held read never completed")
	}
	if result.err != nil {
		t.Fatalf("a read overtaken by an appender was refused: %v", result.err)
	}
	if result.snapshot.Depth != newest.Depth-1 {
		t.Fatalf("held read returned depth %d, want the world it verified, %d", result.snapshot.Depth, newest.Depth-1)
	}
	if after := storedFrontier(t, founder.MetaDir); after != newest {
		t.Fatalf("the stale read moved the witness to %+v, want %+v", after, newest)
	}
}

// advancePastMemory appends one event through a second workspace and audits
// it there, so the stored witness sits one commit ahead of what the caller
// remembered. It returns the witness the file then holds.
func advancePastMemory(t *testing.T, ctx context.Context, founder *Workspace, seed workroom.Record, remembered apphost.VerifiedFrontier, key string) apphost.VerifiedFrontier {
	t.Helper()
	mover, err := Open(ctx, founder.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actRecord(t, ctx, mover, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "advance past stale memory",
		RestsOn: []string{seed.ID}, IdempotencyKey: key,
	})
	if _, err := mover.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	newest := storedFrontier(t, founder.MetaDir)
	if newest.Depth != remembered.Depth+1 {
		t.Fatalf("stored frontier depth = %d, want %d", newest.Depth, remembered.Depth+1)
	}
	return newest
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
