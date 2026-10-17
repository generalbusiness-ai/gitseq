package kernel

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/ed25519"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

// identityOf is the dedup identity Submit indexes an act under.
func identityOf(f fixtureState, private ed25519.PrivateKey, namespace, key string) string {
	return intent.DedupIdentity("git:"+f.format+":"+f.genesis, private.Public().(ed25519.PublicKey), namespace, key)
}

func submitAll(t *testing.T, f fixtureState, requests ...Request) {
	t.Helper()
	for _, request := range requests {
		if _, err := Submit(f.ctx, f.store, request, Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
}

func loadedReader(t *testing.T, f fixtureState, checkpoint ...CheckpointOptions) *Reader {
	t.Helper()
	reader := NewReader(f.store, checkpoint...)
	if _, err := reader.Load(f.ctx, f.genesis); err != nil {
		t.Fatal(err)
	}
	return reader
}

// A reader's own index answers a retry lookup exactly as a fresh scan of the
// same head does — key for key, payload for payload — and keeps the identity
// whole: the same key under another actor or another namespace is a different
// question. At any other head it answers nothing: it never reads the log to
// catch up.
func TestReaderPriorActsAtAnswersAsAFreshScanAtExactlyItsHead(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice, bob := actor(t), actor(t)
	target := "git:" + f.format + ":" + f.genesis
	submitAll(t, f,
		f.request(t, alice, "shared", []byte("alice's act"), nil),
		f.request(t, bob, "shared", []byte("bob's act"), nil),
		f.request(t, alice, "only-alice", []byte("alice again"), nil),
	)
	reader := loadedReader(t, f)
	head := mustHead(t, f.store, Ref(f.genesis))
	keys := []string{
		identityOf(f, alice, "test", "shared"),
		identityOf(f, bob, "test", "shared"),
		identityOf(f, alice, "test", "only-alice"),
		identityOf(f, bob, "test", "only-alice"),
		identityOf(f, alice, "other-namespace", "shared"),
		identityOf(f, alice, "test", "never-filed"),
	}
	fresh, err := PriorActs(f.ctx, f.store, target, keys)
	if err != nil {
		t.Fatal(err)
	}
	indexed, held, err := reader.PriorActsAt(f.ctx, f.genesis, head, keys)
	if err != nil || !held {
		t.Fatalf("reader at the head did not answer: held=%v err=%v", held, err)
	}
	if len(fresh) != 3 || !reflect.DeepEqual(indexed, fresh) {
		t.Fatalf("the reader's index answered %v, a fresh scan %v", indexed, fresh)
	}
	if string(indexed[keys[0]].Payload) != "alice's act" || string(indexed[keys[1]].Payload) != "bob's act" {
		t.Fatalf("one key under two actors answered %q and %q", indexed[keys[0]].Payload, indexed[keys[1]].Payload)
	}

	submitAll(t, f, f.request(t, alice, "later", []byte("after the read"), nil))
	moved := mustHead(t, f.store, Ref(f.genesis))
	for _, at := range []string{moved, f.genesis, ""} {
		if prior, held, err := reader.PriorActsAt(f.ctx, f.genesis, at, []string{identityOf(f, alice, "test", "later")}); held || prior != nil || err != nil {
			t.Fatalf("a reader at %s answered at %q: %v %v %v", short(head), at, prior, held, err)
		}
	}
	if _, held, _ := reader.PriorActsAt(f.ctx, strings.Repeat("0", len(f.genesis)), head, keys); held {
		t.Fatal("a reader answered for a log it never read")
	}
}

// An index entry whose payload tree is not the one its own signature names is
// refused, never read: the payload returned is always the one the actor
// signed.
func TestReaderPriorActsAtReadsOnlyThePayloadTheSignatureNames(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	submitAll(t, f,
		f.request(t, alice, "signed", []byte("the signed payload"), nil),
		f.request(t, alice, "decoy", []byte("some other payload"), nil),
	)
	reader := loadedReader(t, f)
	head := mustHead(t, f.store, Ref(f.genesis))
	signed, decoy := identityOf(f, alice, "test", "signed"), identityOf(f, alice, "test", "decoy")
	reader.mu.Lock()
	entry := reader.log.Dedup[signed]
	entry.PayloadTree = reader.log.Dedup[decoy].PayloadTree
	reader.log.Dedup[signed] = entry
	reader.mu.Unlock()
	prior, held, err := reader.PriorActsAt(f.ctx, f.genesis, head, []string{signed})
	if !held || err == nil || !strings.Contains(err.Error(), "differs from the one its signature names") {
		t.Fatalf("an index entry naming another tree answered %v held=%v err=%v", prior, held, err)
	}
}

// The payload is read through the audit reader, which hashes every object
// against the identifier asked for, so an object store that no longer holds
// the signed bytes under that identifier is refused rather than believed.
func TestReaderPriorActsAtRefusesAPayloadObjectThatNoLongerHashesToItsName(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	request := f.request(t, alice, "corrupted", []byte("the signed payload"), nil)
	submitAll(t, f, request)
	reader := loadedReader(t, f)
	head := mustHead(t, f.store, Ref(f.genesis))
	blob := looseBlobID(request.Payload)
	path := filepath.Join(f.store.Repo, "objects", blob[:2], blob[2:])
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("payload blob %s is not loose: %v", blob, err)
	}
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	forged := []byte("the forged payload")
	fmt.Fprintf(writer, "blob %d\x00%s", len(forged), forged)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	prior, held, err := reader.PriorActsAt(f.ctx, f.genesis, head, []string{identityOf(f, alice, "test", "corrupted")})
	if !held || err == nil {
		t.Fatalf("a payload object that no longer hashes to its name answered %q held=%v err=%v", prior[identityOf(f, alice, "test", "corrupted")].Payload, held, err)
	}
}

// looseBlobID is the identifier Git gives these bytes as a blob in a SHA-1
// repository.
func looseBlobID(content []byte) string {
	digest := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(content))), content...))
	return hex.EncodeToString(digest[:])
}

// The reader answers an exact retry as a replay and a different act under a
// held key as the conflict Submit would refuse; at any other head it does not
// answer.
func TestReaderReplayAtRefusesAConflictingRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	accepted := f.request(t, alice, "retry", []byte("first"), nil)
	submitAll(t, f, accepted)
	reader := loadedReader(t, f)
	head := mustHead(t, f.store, Ref(f.genesis))
	if replay, held, err := reader.ReplayAt(head, accepted); !held || !replay || err != nil {
		t.Fatalf("exact retry = replay %v held %v err %v", replay, held, err)
	}
	conflict := f.request(t, alice, "retry", []byte("different"), nil)
	if replay, held, err := reader.ReplayAt(head, conflict); !held || replay || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry = replay %v held %v err %v", replay, held, err)
	}
	fresh := f.request(t, alice, "fresh", []byte("new"), nil)
	if replay, held, err := reader.ReplayAt(head, fresh); !held || replay || err != nil {
		t.Fatalf("new act = replay %v held %v err %v", replay, held, err)
	}
	submitAll(t, f, fresh)
	if _, held, _ := reader.ReplayAt(mustHead(t, f.store, Ref(f.genesis)), fresh); held {
		t.Fatal("a reader answered at a head it never verified")
	}
}

// A consume-only reader starts from a signed checkpoint but changes nothing:
// it publishes none though it holds the signing key, and repairs neither the
// checkpoint ref nor the pointer that selected an older checkpoint.
func TestConsumeOnlyReaderChangesNoCheckpointSelector(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	submitAll(t, f, f.request(t, alice, "one", []byte("one"), nil))
	publisher := &testCheckpointPointer{}
	loadedReader(t, f, CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: publisher})
	older := mustHead(t, f.store, CheckpointRef(f.genesis))
	submitAll(t, f, f.request(t, alice, "two", []byte("two"), nil))
	// The pointer names the older checkpoint, and the ref is gone: an ordinary
	// reader would restore the ref and publish a checkpoint at the new head.
	if err := f.store.DeleteRef(f.ctx, CheckpointRef(f.genesis), older); err != nil {
		t.Fatal(err)
	}
	pointer := &testCheckpointPointer{commit: older}
	reader := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: pointer, ConsumeOnly: true})
	loaded, err := reader.Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || reader.checkpointLoads != 1 || loaded.Verification.Head != mustHead(t, f.store, Ref(f.genesis)) {
		t.Fatalf("consume-only reader did not start from the checkpoint: %+v loads=%d", loaded, reader.checkpointLoads)
	}
	if _, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err == nil {
		t.Fatal("a consume-only reader restored or published the checkpoint ref")
	}
	if pointer.writes != 0 || pointer.commit != older || reader.checkpointWrites != 0 {
		t.Fatalf("a consume-only reader moved the pointer: writes=%d commit=%s published=%d", pointer.writes, pointer.commit, reader.checkpointWrites)
	}
	// Now the ref names the checkpoint and the pointer names something that is
	// not one: an ordinary reader would point the pointer at what it selected.
	if err := f.store.UpdateRef(f.ctx, CheckpointRef(f.genesis), older, ""); err != nil {
		t.Fatal(err)
	}
	stale := &testCheckpointPointer{commit: f.genesis}
	reader = NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey, Pointer: stale, ConsumeOnly: true})
	if loaded, err := reader.Load(f.ctx, f.genesis); err != nil || !loaded.Checkpoint {
		t.Fatalf("consume-only reader did not start from the ref's checkpoint: %+v %v", loaded, err)
	}
	if stale.writes != 0 || stale.commit != f.genesis {
		t.Fatalf("a consume-only reader repaired the pointer: writes=%d commit=%s", stale.writes, stale.commit)
	}
	if ref := mustHead(t, f.store, CheckpointRef(f.genesis)); ref != older {
		t.Fatalf("a consume-only reader moved the checkpoint ref to %s", ref)
	}
}

// A submitter seeded from a reader keeps the identity the reader verified: it
// reuses the copy only at the exact head it was read at, verifies whatever
// was appended since before judging, runs post-dedup admission for every new
// act, and owns its index, so an append moves nothing in the reader.
func TestSeededSubmitterKeepsItsHeadIdentityAndAdmission(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	submitAll(t, f, f.request(t, alice, "before", []byte("before"), nil))
	reader := loadedReader(t, f)
	readHead := mustHead(t, f.store, Ref(f.genesis))
	// Another writer appends after the read and before the seed.
	external := f.request(t, alice, "external", []byte("external"), nil)
	submitAll(t, f, external)
	externalHead := mustHead(t, f.store, Ref(f.genesis))
	admitted := 0
	submitter := NewSubmitterFrom(f.store, Options{
		SigningKey: f.signingKey,
		PostDedup: func(_ context.Context, application Application) error {
			admitted++
			if string(application.Payload) == "refused" {
				return errors.New("refused by the application")
			}
			return nil
		},
	}, reader)
	if submitter.cache.head != readHead {
		t.Fatalf("seeded at %s, want the head the reader verified %s", submitter.cache.head, readHead)
	}
	result, err := submitter.Submit(f.ctx, external)
	if err != nil || !result.Replay {
		t.Fatalf("the act appended after the read did not replay: %+v %v", result, err)
	}
	if _, err := submitter.Submit(f.ctx, f.request(t, alice, "external", []byte("reused key"), nil)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("a reused key after the read = %v, want the conflict", err)
	}
	if _, err := submitter.Submit(f.ctx, f.request(t, alice, "refusal", []byte("refused"), nil)); err == nil || !strings.Contains(err.Error(), "refused by the application") {
		t.Fatalf("a seeded submitter skipped post-dedup admission: %v", err)
	}
	landed, err := submitter.Submit(f.ctx, f.request(t, alice, "new", []byte("new"), nil))
	if err != nil || landed.Replay || landed.BaseHead != externalHead {
		t.Fatalf("new act = %+v %v, want it chained on %s", landed, err, externalHead)
	}
	if admitted != 2 {
		t.Fatalf("post-dedup admission ran %d times, want 2: once per new act and never for a replay or a conflict", admitted)
	}
	reader.mu.Lock()
	_, leaked := reader.log.Dedup[identityOf(f, alice, "test", "new")]
	readerHead := reader.logCache.head
	reader.mu.Unlock()
	if leaked || readerHead != readHead {
		t.Fatalf("the submitter's appends reached the reader: leaked=%v head=%s", leaked, readerHead)
	}
	if _, err := VerifyAt(f.ctx, f.store, f.genesis, mustHead(t, f.store, Ref(f.genesis))); err != nil {
		t.Fatalf("the seeded submitter left a log that does not verify: %v", err)
	}
}

// A seeded submitter keeps the sequence-ref compare-and-swap: a log that
// moves between its head read and its ref update makes it verify the new
// commit and chain on top of it, never over it.
func TestSeededSubmitterRetriesAfterCASLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	submitAll(t, f, f.request(t, alice, "before", []byte("before"), nil))
	reader := loadedReader(t, f)
	arrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	submitter := NewSubmitterFrom(f.store, Options{SigningKey: f.signingKey, Failpoint: func(name string) {
		if name == "before_ref_cas" {
			once.Do(func() {
				close(arrived)
				<-release
			})
		}
	}}, reader)
	type outcome struct {
		result Result
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, err := submitter.Submit(f.ctx, f.request(t, alice, "seeded", []byte("seeded"), nil))
		completed <- outcome{result: result, err: err}
	}()
	<-arrived
	submitAll(t, f, f.request(t, alice, "external", []byte("external"), nil))
	close(release)
	out := <-completed
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.result.CASRetries != 1 || submitter.cache.fullScans != 0 || submitter.cache.deltaScans != 1 {
		t.Fatalf("CAS loss = %d retries, %d full scans, %d delta scans; want 1, 0 and 1",
			out.result.CASRetries, submitter.cache.fullScans, submitter.cache.deltaScans)
	}
	verified, err := Verify(f.ctx, f.store, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Events != 3 || verified.Head != out.result.Head {
		t.Fatalf("verification = %+v result = %+v, want all three acts with the seeded one last", verified, out.result)
	}
}

// A seeded submitter holds none of the checkpoint material for the log it
// started from, so it can publish no checkpoint from it, and it collects none
// for what it verifies or appends afterwards either: material it could never
// publish would only be held until its next full read.
func TestSeededSubmitterCollectsNoCheckpointMaterial(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	alice := actor(t)
	submitAll(t, f, f.request(t, alice, "before", []byte("before"), nil))
	reader := loadedReader(t, f, CheckpointOptions{Enabled: true, SigningKey: f.signingKey})
	submitter := NewSubmitterFrom(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true}, reader)
	// One act appended elsewhere, so the submitter verifies a delta first.
	submitAll(t, f, f.request(t, alice, "external", []byte("external"), nil))
	if _, err := submitter.Submit(f.ctx, f.request(t, alice, "seeded", []byte("seeded"), nil)); err != nil {
		t.Fatal(err)
	}
	if cache := submitter.cache.checkpointEvents; cache.count != 0 || len(cache.tail) != 0 || cache.chunks.count != 0 {
		t.Fatalf("a seeded submitter collected checkpoint material for %d events", cache.count)
	}
	if submitter.cache.deltaScans != 1 || submitter.cache.fullScans != 0 || !submitter.cache.seeded {
		t.Fatalf("the fixture did not verify a delta on a seeded cache: delta=%d full=%d seeded=%v",
			submitter.cache.deltaScans, submitter.cache.fullScans, submitter.cache.seeded)
	}
}

// A reader of another repository, or one that has verified nothing, seeds
// nothing.
func TestSubmitterSeedsOnlyFromAVerifiedReaderOfTheSameRepository(t *testing.T) {
	t.Parallel()
	f := newFixture(t, "sha1")
	if seeded := NewSubmitterFrom(f.store, Options{SigningKey: f.signingKey}, NewReader(f.store)); seeded.cache.head != "" {
		t.Fatalf("seeded from a reader that verified nothing: %s", seeded.cache.head)
	}
	other := newFixture(t, "sha1")
	if seeded := NewSubmitterFrom(f.store, Options{SigningKey: f.signingKey}, loadedReader(t, other)); seeded.cache.head != "" {
		t.Fatalf("seeded from a reader of another repository: %s", seeded.cache.head)
	}
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// The verified dedup index owns its signed bytes. A caller that edits what a
// read handed it — the events Load returns, or the events a lookup returns —
// into another valid intent under the same key changes nothing the index
// judges: the edited intent is still the conflict it was, for the reader and
// for a submitter seeded from it.
func TestTheVerifiedIndexOwnsItsSignedBytes(t *testing.T) {
	t.Parallel()
	edit := func(t *testing.T, lent intent.Signed, into Request) {
		t.Helper()
		if len(into.Signed.Intent) != len(lent.Intent) || len(into.Signed.Signature) != len(lent.Signature) {
			t.Fatal("the edit would not fit in place")
		}
		copy(lent.Intent, into.Signed.Intent)
		copy(lent.Signature, into.Signed.Signature)
	}
	t.Run("events a load returns", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "sha1")
		alice := actor(t)
		submitAll(t, f, f.request(t, alice, "held", []byte("first"), nil))
		reader := NewReader(f.store)
		loaded, err := reader.Load(f.ctx, f.genesis)
		if err != nil || len(loaded.Events) != 1 {
			t.Fatalf("load = %d events, %v", len(loaded.Events), err)
		}
		changed := f.request(t, alice, "held", []byte("other"), nil)
		edit(t, loaded.Events[0].Signed, changed)
		if replay, held, err := reader.ReplayAt(loaded.Verification.Head, changed); replay || !held || !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("after editing a loaded event the reader answered replay=%v held=%v err=%v; want the conflict", replay, held, err)
		}
		submitter := NewSubmitterFrom(f.store, Options{SigningKey: f.signingKey}, reader)
		if result, err := submitter.Submit(f.ctx, changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("after editing a loaded event the seeded submitter answered %+v %v; want the conflict", result, err)
		}
	})
	t.Run("events a lookup returns", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, "sha1")
		alice := actor(t)
		accepted := f.request(t, alice, "held", []byte("first"), []string{"opaque-basis"})
		submitAll(t, f, accepted)
		reader := loadedReader(t, f)
		head := mustHead(t, f.store, Ref(f.genesis))
		key := identityOf(f, alice, "test", "held")
		prior, held, err := reader.PriorActsAt(f.ctx, f.genesis, head, []string{key})
		if err != nil || !held {
			t.Fatal(held, err)
		}
		changed := f.request(t, alice, "held", []byte("other"), []string{"opaque-basis"})
		lent := prior[key]
		edit(t, lent.Signed, changed)
		lent.Intent.RestsOn[0] = "edited-basis"
		if replay, held, err := reader.ReplayAt(head, changed); replay || !held || !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("after editing a looked-up event the reader answered replay=%v held=%v err=%v; want the conflict", replay, held, err)
		}
		again, _, err := reader.PriorActsAt(f.ctx, f.genesis, head, []string{key})
		if err != nil || !again[key].Signed.Equal(accepted.Signed) || again[key].Intent.RestsOn[0] != "opaque-basis" {
			t.Fatalf("a second lookup returned the edited event: %v", err)
		}
	})
}
