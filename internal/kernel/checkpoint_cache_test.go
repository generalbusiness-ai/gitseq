package kernel

import (
	"errors"
	mathrand "math/rand"
	"strconv"
	"testing"
)

// incompressiblePayload returns deterministic pseudo-random bytes that gzip
// cannot shrink, so compressed-chunk sizes track their inputs.
func incompressiblePayload(t *testing.T, size int, seed int64) []byte {
	t.Helper()
	payload := make([]byte, size)
	if _, err := mathrand.New(mathrand.NewSource(seed)).Read(payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertCacheReleased(t *testing.T, cache *checkpointEventCache) {
	t.Helper()
	if !errors.Is(cache.err, errCheckpointTooLarge) {
		t.Fatalf("cache error = %v, want %v", cache.err, errCheckpointTooLarge)
	}
	if cache.chunks != nil || cache.tail != nil || cache.chunkBytes != 0 || cache.tailBytes != 0 {
		t.Fatalf("overflowed cache retained memory: chunks=%d tail=%d chunkBytes=%d tailBytes=%d",
			len(cache.chunks), len(cache.tail), cache.chunkBytes, cache.tailBytes)
	}
}

func TestCheckpointCacheDefaultLimitIsMarshalLimit(t *testing.T) {
	if got := (&checkpointEventCache{}).byteLimit(); got != maxCheckpointBytes {
		t.Fatalf("default cache byte limit = %d, want %d", got, maxCheckpointBytes)
	}
	if got := (&checkpointEventCache{limit: 5}).byteLimit(); got != 5 {
		t.Fatalf("test cache byte limit = %d, want 5", got)
	}
}

func TestCheckpointCacheBoundsTailAccumulation(t *testing.T) {
	cache := checkpointEventCache{limit: 1024}
	for index := 0; index < 3; index++ {
		cache.append(Event{Payload: incompressiblePayload(t, 300, int64(index))})
	}
	if cache.err != nil || cache.count != 3 || cache.tailBytes != 900 {
		t.Fatalf("cache under limit failed: err=%v count=%d tailBytes=%d", cache.err, cache.count, cache.tailBytes)
	}
	cache.append(Event{Payload: incompressiblePayload(t, 300, 99)})
	assertCacheReleased(t, &cache)
	cache.append(Event{Payload: []byte("after")})
	if cache.count != 3 || cache.tail != nil {
		t.Fatalf("failed cache accepted an append: count=%d tail=%d", cache.count, len(cache.tail))
	}
}

func TestCheckpointCacheBoundsSingleOversizedEvent(t *testing.T) {
	cache := checkpointEventCache{limit: 1024}
	cache.append(Event{
		Payload:     incompressiblePayload(t, 500, 1),
		Attachments: map[string][]byte{"a": incompressiblePayload(t, 600, 2)},
	})
	assertCacheReleased(t, &cache)
	if cache.count != 0 {
		t.Fatalf("oversized event was counted: count=%d", cache.count)
	}
}

func TestCheckpointCacheBoundsBorrowedChunkWhileBuilding(t *testing.T) {
	cache := checkpointEventCache{limit: 4096}
	events := make([]Event, checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, 16, int64(index))}
	}
	cache.appendEvents(events)
	assertCacheReleased(t, &cache)
	if cache.count != 0 {
		t.Fatalf("failed chunk was counted: count=%d", cache.count)
	}
}

func TestCheckpointCacheBoundsCumulativeChunks(t *testing.T) {
	cache := checkpointEventCache{limit: 500_000}
	events := make([]Event, 2*checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, 64, int64(index))}
	}
	cache.appendEvents(events)
	assertCacheReleased(t, &cache)
	if cache.count != checkpointChunkEvents {
		t.Fatalf("count = %d, want the one completed chunk %d", cache.count, checkpointChunkEvents)
	}
}

func TestCheckpointCacheOverflowIsTerminalUntilVerifiedFullRebuild(t *testing.T) {
	f := newFixture(t, "sha1")
	private := actor(t)
	submitter := NewSubmitter(f.store, Options{SigningKey: f.signingKey, CheckpointEnabled: true})
	first, err := submitter.Submit(f.ctx, f.request(t, private, "before", []byte("before"), nil))
	if err != nil {
		t.Fatal(err)
	}
	submitter.cache.checkpointEvents.limit = 1024

	// The append that overflows the cache must still land: the bound fails the
	// checkpoint, never the verified write.
	oversized := incompressiblePayload(t, 2048, 7)
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "oversized", oversized, nil)); err != nil {
		t.Fatal(err)
	}
	assertCacheReleased(t, &submitter.cache.checkpointEvents)
	if submitter.cache.checkpointOversized {
		t.Fatal("oversized latched before any checkpoint write was due")
	}

	// The next due checkpoint write surfaces the overflow into the same
	// terminal oversized latch a marshal-time overflow uses.
	writes, failures := submitter.cache.checkpointWrites, submitter.cache.checkpointFailures
	submitter.cache.checkpointAttempt = submitter.cache.log.Verification.Depth - checkpointInterval
	if _, err := submitter.Submit(f.ctx, f.request(t, private, "latch", []byte("latch"), nil)); err != nil {
		t.Fatal(err)
	}
	if !submitter.cache.checkpointOversized || submitter.cache.checkpointFailures != failures+1 || submitter.cache.checkpointWrites != writes {
		t.Fatalf("overflow did not latch terminal oversized state: %+v", submitter.cache)
	}
	if submitter.cache.checkpointEvents.count != 0 || submitter.cache.checkpointEvents.tail != nil || submitter.cache.checkpointEvents.chunks != nil {
		t.Fatalf("terminal checkpoint retained write material: %+v", submitter.cache.checkpointEvents)
	}
	for index := 0; index < 2; index++ {
		if _, err := submitter.Submit(f.ctx, f.request(t, private, "after-latch-"+strconv.Itoa(index), []byte("after"), nil)); err != nil {
			t.Fatal(err)
		}
	}
	if submitter.cache.checkpointFailures != failures+1 || submitter.cache.checkpointWrites != writes {
		t.Fatal("terminal checkpoint failure retried before a full rebuild")
	}
	events, _, err := Load(f.ctx, f.store, f.genesis)
	if err != nil || len(events) != 5 {
		t.Fatalf("verified read after overflow: events=%d err=%v", len(events), err)
	}

	// A verified full rebuild without the oversized history is the reset
	// boundary: the latch clears and one fresh publication succeeds.
	checkpointCommit := mustHead(t, f.store, CheckpointRef(f.genesis))
	if err := f.store.UpdateRef(f.ctx, CheckpointRef(f.genesis), f.genesis, checkpointCommit); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateRef(f.ctx, Ref(f.genesis), first.Head, mustHead(t, f.store, Ref(f.genesis))); err != nil {
		t.Fatal(err)
	}
	result, err := submitter.Submit(f.ctx, f.request(t, private, "after-rebuild", []byte("after-rebuild"), nil))
	if err != nil {
		t.Fatal(err)
	}
	if submitter.cache.checkpointOversized {
		t.Fatal("full verified rebuild did not clear the oversized latch")
	}
	if submitter.cache.checkpointWrites != writes+1 {
		t.Fatalf("rebuild checkpoint writes = %d, want %d", submitter.cache.checkpointWrites, writes+1)
	}
	loaded, err := NewReader(f.store, CheckpointOptions{Enabled: true}).Load(f.ctx, f.genesis)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Checkpoint || loaded.Verification.Head != result.Head {
		t.Fatalf("rebuilt checkpoint load = %+v, want head %s", loaded.Verification, result.Head)
	}
}

func TestStreamedFullRebuildCheckpointCacheIsBounded(t *testing.T) {
	f := newFixture(t, "sha1")
	private := actor(t)
	for index := 0; index < 4; index++ {
		payload := incompressiblePayload(t, 1024, int64(index))
		if _, err := Submit(f.ctx, f.store, f.request(t, private, "streamed-"+strconv.Itoa(index), payload, nil), Options{SigningKey: f.signingKey}); err != nil {
			t.Fatal(err)
		}
	}
	reader := NewReader(f.store, CheckpointOptions{Enabled: true, SigningKey: f.signingKey})
	reader.logCache.checkpointEvents.limit = 2048

	delivered := 0
	result, err := reader.LoadWithProgressStream(f.ctx, f.genesis, nil, func(Event) error {
		delivered++
		return nil
	})
	if err != nil {
		t.Fatalf("verified streamed read failed on cache overflow: %v", err)
	}
	if !result.Full || delivered != result.Verification.Events || delivered != 4 {
		t.Fatalf("streamed read delivered %d events, verification %+v", delivered, result.Verification)
	}
	if !reader.logCache.checkpointOversized || reader.logCache.checkpointFailures != 1 || reader.logCache.checkpointWrites != 0 {
		t.Fatalf("streamed overflow did not fail the checkpoint closed: %+v", reader.logCache)
	}
	if reader.logCache.checkpointEvents.count != 0 || reader.logCache.checkpointEvents.tail != nil || reader.logCache.checkpointEvents.chunks != nil {
		t.Fatalf("streamed overflow retained cache memory: %+v", reader.logCache.checkpointEvents)
	}
	if _, err := f.store.Head(f.ctx, CheckpointRef(f.genesis)); err == nil {
		t.Fatal("overflowed streamed rebuild still published a checkpoint")
	}
}
