package kernel

import (
	"bytes"
	"compress/gzip"
	"errors"
	mathrand "math/rand"
	"strconv"
	"testing"
	"unsafe"
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
	cache := checkpointEventCache{limit: 2048}
	for index := 0; index < 3; index++ {
		cache.append(Event{Payload: incompressiblePayload(t, 300, int64(index))})
	}
	// The charge covers what the cloned entries actually retain: their
	// capacities plus the flat container charges, never less than the
	// source lengths.
	charged := 0
	for _, entry := range cache.tail {
		charged += checkpointEventRetainedBytes(entry)
	}
	if minimum := 3 * (300 + checkpointTailEntryOverhead); charged < minimum {
		t.Fatalf("charge %d is below the source-length floor %d", charged, minimum)
	}
	if cache.err != nil || cache.count != 3 || cache.tailBytes != charged {
		t.Fatalf("cache under limit failed: err=%v count=%d tailBytes=%d want %d", cache.err, cache.count, cache.tailBytes, charged)
	}
	cache.append(Event{Payload: incompressiblePayload(t, 300, 99)})
	assertCacheReleased(t, &cache)
	cache.append(Event{Payload: []byte("after")})
	if cache.count != 3 || cache.tail != nil {
		t.Fatalf("failed cache accepted an append: count=%d tail=%d", cache.count, len(cache.tail))
	}
}

func TestCheckpointTailEntryOverheadCoversEntrySlot(t *testing.T) {
	if slot := int(unsafe.Sizeof(checkpointEvent{})); 2*slot > checkpointTailEntryOverhead {
		t.Fatalf("tail entry slot is %d bytes; the %d-byte container charge no longer covers doubled growth", slot, checkpointTailEntryOverhead)
	}
}

// TestCheckpointCacheOwnedTailFlushPeakBudget drives a full 4096-event owned
// tail of incompressible payloads through its flush and pins the peak-budget
// invariant: the growing output, its growth-copy headroom, and everything
// the cache still retains must fit the limit together at every instant. The
// encoded size is measured first; an honestly budgeted flush needs the
// container charges plus twice the encoded output — content and equal
// headroom for the old array a growth copy briefly holds — so the two
// limits sit exactly at and one byte below that bound.
func TestCheckpointCacheOwnedTailFlushPeakBudget(t *testing.T) {
	events := make([]Event, checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, 48, int64(index))}
	}
	var encoded bytes.Buffer
	compressed := gzip.NewWriter(&encoded)
	for _, event := range events {
		if err := writeCompactCheckpointEvent(compressed, checkpointReference(event)); err != nil {
			t.Fatal(err)
		}
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	container := checkpointChunkEvents * checkpointTailEntryOverhead

	// With exactly the container charge plus twice the encoded output
	// available, the flush fits only because each entry's content is
	// credited back as its write completes. It must succeed, release the
	// tail and its backing array, and retain no more output capacity than
	// the content needs.
	cache := checkpointEventCache{limit: container + 2*encoded.Len()}
	for _, event := range events {
		cache.append(event)
	}
	if cache.err != nil {
		t.Fatalf("flush within an exact budget failed: %v", cache.err)
	}
	if len(cache.chunks) != 1 || cache.count != checkpointChunkEvents {
		t.Fatalf("flush stored %d chunks for %d events", len(cache.chunks), cache.count)
	}
	if cache.tail != nil || cache.tailBytes != 0 {
		t.Fatalf("flushed tail still retained: tail=%d tailBytes=%d", len(cache.tail), cache.tailBytes)
	}
	if cache.chunkBytes != cap(cache.chunks[0]) {
		t.Fatalf("chunkBytes = %d, want retained capacity %d", cache.chunkBytes, cap(cache.chunks[0]))
	}
	if cache.chunkBytes != encoded.Len() {
		t.Fatalf("flush retained %d bytes of capacity while the tail's container charge held %d of the %d limit; %d was the most the budget admitted",
			cache.chunkBytes, container, cache.byteLimit(), encoded.Len())
	}

	// One byte short, the output and its growth headroom can never fit
	// alongside the container storage that is still held, so the flush must
	// refuse and release rather than let the true peak exceed the limit.
	short := checkpointEventCache{limit: container + 2*encoded.Len() - 1}
	for _, event := range events {
		short.append(event)
	}
	assertCacheReleased(t, &short)
}

// TestCheckpointCacheCountsChunkCapacityNotLength gives a borrowed chunk a
// budget with room beyond its encoded size: the writer's doubling then
// legitimately retains more capacity than content, and the accounting must
// charge what is retained, not what is used.
func TestCheckpointCacheCountsChunkCapacityNotLength(t *testing.T) {
	cache := checkpointEventCache{limit: 1 << 20}
	events := make([]Event, checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, 48, int64(index))}
	}
	cache.appendEvents(events)
	if cache.err != nil || len(cache.chunks) != 1 || cache.count != checkpointChunkEvents {
		t.Fatalf("borrowed chunk failed: err=%v chunks=%d count=%d", cache.err, len(cache.chunks), cache.count)
	}
	retained := cap(cache.chunks[0])
	if retained <= len(cache.chunks[0]) {
		t.Fatalf("fixture kept capacity %d equal to content %d and proves nothing; grow the fixture", retained, len(cache.chunks[0]))
	}
	if cache.chunkBytes != retained {
		t.Fatalf("chunkBytes = %d, want retained capacity %d (content is %d)", cache.chunkBytes, retained, len(cache.chunks[0]))
	}
}

// TestCheckpointLimitWriterBoundsRetainedCapacity pins the writer's peak
// contract: content may fill only half the live budget, the other half is
// headroom for the old array a growth copy briefly holds, and the buffer
// never retains capacity past the content ceiling. The true peak — reserve,
// old array, new array — therefore never exceeds the limit.
func TestCheckpointLimitWriterBoundsRetainedCapacity(t *testing.T) {
	var output bytes.Buffer
	writer := &checkpointLimitWriter{output: &output, limit: 1000}
	for index := 0; index < 500; index++ {
		if _, err := writer.Write([]byte{byte(index)}); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 500 || output.Cap() > 500 {
		t.Fatalf("writer retained %d bytes of capacity for %d bytes of content against limit 1000", output.Cap(), output.Len())
	}
	// The 501st byte would leave no headroom for its own growth copy: the
	// content ceiling is half the budget, and the writer must refuse there
	// rather than let the copy's transient exceed the limit.
	if _, err := writer.Write([]byte{0}); !errors.Is(err, errCheckpointTooLarge) {
		t.Fatalf("write past the content ceiling = %v, want %v", err, errCheckpointTooLarge)
	}

	// The reservation is read live: bytes freed elsewhere against the same
	// budget admit writes that were refused a moment earlier.
	reserved := 60
	var shared bytes.Buffer
	live := &checkpointLimitWriter{output: &shared, limit: 100, reserve: func() int { return reserved }}
	if _, err := live.Write(make([]byte, 20)); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Write([]byte{0}); !errors.Is(err, errCheckpointTooLarge) {
		t.Fatalf("write past the reserved budget = %v, want %v", err, errCheckpointTooLarge)
	}
	reserved = 30
	if _, err := live.Write(make([]byte, 15)); err != nil {
		t.Fatalf("write after the reservation drained = %v, want success", err)
	}
	if shared.Cap() > 35 {
		t.Fatalf("writer retained %d bytes of capacity against a 100-byte limit with 30 still reserved", shared.Cap())
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
	// Roomy enough for the first chunk's content and growth headroom
	// (~312 KB encoded against a 400 KB content ceiling), far too small
	// once that chunk's retained capacity is charged against the second.
	cache := checkpointEventCache{limit: 800_000}
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

// TestCheckpointCacheOwnedTailFlushRefusesSkewedTailBeyondBudget drives an
// adversarial owned tail: 4096 entries whose first is a single 512 KiB
// incompressible payload, with only 128 KiB of budget beyond the charged
// tail. While that entry is encoded its cloned content and its
// incompressible output are both live, so an honest flush must refuse and
// release rather than stand ~512 KiB of output next to the still-charged
// source. The same tail must still flush once the budget honestly covers
// the encoded output and its growth headroom on top of the charges.
func TestCheckpointCacheOwnedTailFlushRefusesSkewedTailBeyondBudget(t *testing.T) {
	events := make([]Event, checkpointChunkEvents)
	events[0] = Event{Payload: incompressiblePayload(t, 512<<10, 424242)}
	for index := 1; index < len(events); index++ {
		events[index] = Event{Payload: incompressiblePayload(t, 16, int64(index))}
	}
	charge := 0
	for _, event := range events {
		charge += checkpointEventRetainedBytes(checkpointMaterial(event))
	}

	cache := checkpointEventCache{limit: charge + 128<<10}
	for _, event := range events {
		cache.append(event)
	}
	assertCacheReleased(t, &cache)

	// The honest budget for this tail: its charges, plus the encoded output
	// and equal growth headroom in place of the drained content. The large
	// entry's own encoding then fits beside its still-charged clone.
	var encoded bytes.Buffer
	compressed := gzip.NewWriter(&encoded)
	for _, event := range events {
		if err := writeCompactCheckpointEvent(compressed, checkpointReference(event)); err != nil {
			t.Fatal(err)
		}
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	generous := checkpointEventCache{limit: charge + 2*encoded.Len()}
	for _, event := range events {
		generous.append(event)
	}
	if generous.err != nil {
		t.Fatalf("skewed flush within an honest budget failed: %v", generous.err)
	}
	if len(generous.chunks) != 1 || generous.count != checkpointChunkEvents || generous.tail != nil {
		t.Fatalf("skewed flush stored %d chunks for %d events, tail=%d", len(generous.chunks), generous.count, len(generous.tail))
	}
}

// TestCheckpointCacheFlushKeepsInHandEntryCharged pins the hand-off itself:
// the entry being encoded stays charged until its write completes. With a
// 512 KiB incompressible entry and a budget whose slack is well below that
// entry's output, crediting the entry before its write would admit the
// flush; keeping it charged through the write must refuse and release.
func TestCheckpointCacheFlushKeepsInHandEntryCharged(t *testing.T) {
	events := []Event{
		{Payload: incompressiblePayload(t, 512<<10, 7)},
		{Payload: incompressiblePayload(t, 16, 8)},
		{Payload: incompressiblePayload(t, 16, 9)},
	}
	charge := 0
	for _, event := range events {
		charge += checkpointEventRetainedBytes(checkpointMaterial(event))
	}
	cache := checkpointEventCache{limit: charge + 600<<10}
	for _, event := range events {
		cache.append(event)
	}
	if cache.err != nil {
		t.Fatalf("appends within the charge failed: %v", cache.err)
	}
	cache.flushTail()
	assertCacheReleased(t, &cache)
}

// TestCheckpointCacheChargesClonedCapacityNotSourceLength appends an event
// whose cloned slices keep more capacity than their source length, and
// requires the charge to cover the capacities the cache actually retains,
// with the flat container charges on top.
func TestCheckpointCacheChargesClonedCapacityNotSourceLength(t *testing.T) {
	cache := checkpointEventCache{limit: 1 << 20}
	cache.append(Event{
		Payload:     incompressiblePayload(t, 300, 1),
		Attachments: map[string][]byte{"name": incompressiblePayload(t, 300, 2)},
	})
	if cache.err != nil || len(cache.tail) != 1 {
		t.Fatalf("append failed: err=%v tail=%d", cache.err, len(cache.tail))
	}
	entry := cache.tail[0]
	if cap(entry.Payload) == len(entry.Payload) && cap(entry.Attachments["name"]) == len(entry.Attachments["name"]) {
		t.Fatalf("fixture cloned at exact capacity and proves nothing; pick a length off the allocator's size classes")
	}
	want := cap(entry.Payload) + checkpointTailEntryOverhead + checkpointAttachmentOverhead
	for name, content := range entry.Attachments {
		want += len(name) + cap(content)
	}
	if cache.tailBytes != want {
		t.Fatalf("tailBytes = %d, want %d: the charge must cover cloned capacity, not source length", cache.tailBytes, want)
	}
}
