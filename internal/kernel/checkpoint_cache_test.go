package kernel

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	mathrand "math/rand"
	"strconv"
	"strings"
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
	if cache.chunks.head != nil || cache.chunks.count != 0 || cache.tail != nil || cache.chunkBytes != 0 || cache.tailBytes != 0 || cache.scratchBytes != 0 {
		t.Fatalf("overflowed cache retained memory: chunks=%d tail=%d chunkBytes=%d tailBytes=%d scratchBytes=%d",
			cache.chunks.count, len(cache.tail), cache.chunkBytes, cache.tailBytes, cache.scratchBytes)
	}
}

func TestCheckpointCacheDefaultLimitIsMarshalLimit(t *testing.T) {
	t.Parallel()
	if got := (&checkpointEventCache{}).byteLimit(); got != maxCheckpointBytes {
		t.Fatalf("default cache byte limit = %d, want %d", got, maxCheckpointBytes)
	}
	if got := (&checkpointEventCache{limit: 5}).byteLimit(); got != 5 {
		t.Fatalf("test cache byte limit = %d, want 5", got)
	}
}

func TestCheckpointCacheBoundsTailAccumulation(t *testing.T) {
	t.Parallel()
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

// TestCheckpointCacheTailGrowthChargesOldArray forces a real tail capacity
// expansion at the edge of the budget. It fills the tail to len == cap and
// pins both sides of the boundary the next append must budget: one byte
// short must refuse and release, the exact budget must admit, and the
// tail's capacity must be seen to actually double. The whole expected
// budget is derived below from unsafe.Sizeof and the payload lengths this
// test controls — no production accounting helper or constant is called —
// so a mutation that moves the code's boundary, even one that changes the
// production charges coherently in every place at once, cannot move this
// expectation with it.
func TestCheckpointCacheTailGrowthChargesOldArray(t *testing.T) {
	t.Parallel()
	const entries = 8
	const payloadBytes = 16
	events := make([]Event, entries)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, payloadBytes, int64(index))}
	}
	// Each held entry is a clone at exactly its payload length (no
	// attachments) plus two independently measured slots of container
	// charge for the doubling backing array. The next append needs its
	// own such charge plus the full old backing array — entries slots —
	// that stays live beside the new one during the growth copy.
	slot := int(unsafe.Sizeof(checkpointEvent{}))
	charge := entries * (payloadBytes + 2*slot)
	next := Event{Payload: incompressiblePayload(t, payloadBytes, 99)}
	needed := charge + payloadBytes + 2*slot + entries*slot

	short := checkpointEventCache{limit: needed - 1}
	for _, event := range events {
		short.append(event)
	}
	if short.err != nil || len(short.tail) != entries || cap(short.tail) != entries {
		t.Fatalf("fixture did not reach len == cap: err=%v len=%d cap=%d", short.err, len(short.tail), cap(short.tail))
	}
	short.append(next)
	assertCacheReleased(t, &short)

	exact := checkpointEventCache{limit: needed}
	for _, event := range events {
		exact.append(event)
	}
	if exact.err != nil || cap(exact.tail) != entries {
		t.Fatalf("fixture cap = %d err = %v, want cap %d", cap(exact.tail), exact.err, entries)
	}
	exact.append(next)
	if exact.err != nil {
		t.Fatalf("append with the growth transient exactly budgeted failed: %v", exact.err)
	}
	if len(exact.tail) != entries+1 || cap(exact.tail) != 2*entries {
		t.Fatalf("tail did not cross a real growth boundary: len=%d cap=%d, want len %d cap %d",
			len(exact.tail), cap(exact.tail), entries+1, 2*entries)
	}
}

// TestCheckpointCacheOwnedTailFlushPeakBudget drives a full 4096-event owned
// tail of incompressible payloads through its flush and pins the peak-budget
// invariant: the growing output, its growth-copy headroom, and everything
// the cache still retains must fit the limit together at every instant. The
// encoded size is measured first; an honestly budgeted flush needs the
// container charges, the stored chunk's list node,
// plus twice the encoded output — content and equal headroom for the old
// array a growth copy briefly holds — so the two limits sit exactly at
// and one byte below that bound.
func TestCheckpointCacheOwnedTailFlushPeakBudget(t *testing.T) {
	t.Parallel()
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

	// With exactly the container charge, the chunk's list node, and twice the
	// encoded output available, the flush fits only because each entry's
	// content is credited back as its write completes. It must succeed,
	// release the tail and its backing array, and retain no more output
	// capacity than the content needs.
	cache := checkpointEventCache{limit: container + checkpointChunkNodeBytes + 2*encoded.Len()}
	for _, event := range events {
		cache.append(event)
	}
	if cache.err != nil {
		t.Fatalf("flush within an exact budget failed: %v", cache.err)
	}
	if cache.chunks.count != 1 || cache.count != checkpointChunkEvents {
		t.Fatalf("flush stored %d chunks for %d events", cache.chunks.count, cache.count)
	}
	if cache.tail != nil || cache.tailBytes != 0 {
		t.Fatalf("flushed tail still retained: tail=%d tailBytes=%d", len(cache.tail), cache.tailBytes)
	}
	if cache.chunkBytes != checkpointChunkNodeBytes+cap(cache.chunks.head.chunk) {
		t.Fatalf("chunkBytes = %d, want retained capacity %d plus the %d-byte chunk node",
			cache.chunkBytes, cap(cache.chunks.head.chunk), checkpointChunkNodeBytes)
	}
	if cache.chunkBytes != checkpointChunkNodeBytes+encoded.Len() {
		t.Fatalf("flush retained %d bytes while the tail's container charge held %d of the %d limit; the node plus %d was the most the budget admitted",
			cache.chunkBytes, container, cache.byteLimit(), encoded.Len())
	}

	// One byte short, the output and its growth headroom can never fit
	// alongside the container storage and chunk node that are still held,
	// so the flush must refuse and release rather than let the true peak
	// exceed the limit.
	short := checkpointEventCache{limit: container + checkpointChunkNodeBytes + 2*encoded.Len() - 1}
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
	t.Parallel()
	cache := checkpointEventCache{limit: 1 << 20}
	events := make([]Event, checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: incompressiblePayload(t, 48, int64(index))}
	}
	cache.appendEvents(events)
	if cache.err != nil || cache.chunks.count != 1 || cache.count != checkpointChunkEvents {
		t.Fatalf("borrowed chunk failed: err=%v chunks=%d count=%d", cache.err, cache.chunks.count, cache.count)
	}
	retained := cap(cache.chunks.head.chunk)
	if retained <= len(cache.chunks.head.chunk) {
		t.Fatalf("fixture kept capacity %d equal to content %d and proves nothing; grow the fixture", retained, len(cache.chunks.head.chunk))
	}
	if cache.chunkBytes != checkpointChunkNodeBytes+retained {
		t.Fatalf("chunkBytes = %d, want retained capacity %d plus the %d-byte chunk node (content is %d)",
			cache.chunkBytes, retained, checkpointChunkNodeBytes, len(cache.chunks.head.chunk))
	}
}

// TestCheckpointChunkWriterBoundsRetainedCapacity pins the cache writer's
// peak contract: content may fill only half the live budget, the other half
// is headroom for the old array a growth copy briefly holds, and the buffer
// never retains capacity past the content ceiling. The true peak — reserve,
// old array, new array — therefore never exceeds the limit.
func TestCheckpointChunkWriterBoundsRetainedCapacity(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	writer := &checkpointChunkWriter{output: &output, limit: 1000, reserve: func() int { return 0 }}
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
	live := &checkpointChunkWriter{output: &shared, limit: 100, reserve: func() int { return reserved }}
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
	t.Parallel()
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

// TestCheckpointCacheBoundsBorrowedChunkWhileBuilding overflows a borrowed
// chunk mid-build and requires the cache to fail released. The failure path
// it drives abandons the compressor without Close — that abandonment is
// belt-and-braces, not the enforced guard: even a cleanup that did write
// would be refused by the post-failure whole-limit reservation, which
// TestCheckpointCacheWriterRefusesAfterFail pins as the load-bearing guard.
func TestCheckpointCacheBoundsBorrowedChunkWhileBuilding(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	if submitter.cache.checkpointEvents.count != 0 || submitter.cache.checkpointEvents.tail != nil || submitter.cache.checkpointEvents.chunks.count != 0 {
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
	t.Parallel()
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
	if reader.logCache.checkpointEvents.count != 0 || reader.logCache.checkpointEvents.tail != nil || reader.logCache.checkpointEvents.chunks.count != 0 {
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
	t.Parallel()
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

	// The honest budget for this tail: its charges, the stored chunk's
	// node, plus the encoded output and equal growth headroom in place of
	// the drained content. The large entry's own encoding then fits beside
	// its still-charged clone.
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
	generous := checkpointEventCache{limit: charge + checkpointChunkNodeBytes + 2*encoded.Len()}
	for _, event := range events {
		generous.append(event)
	}
	if generous.err != nil {
		t.Fatalf("skewed flush within an honest budget failed: %v", generous.err)
	}
	if generous.chunks.count != 1 || generous.count != checkpointChunkEvents || generous.tail != nil {
		t.Fatalf("skewed flush stored %d chunks for %d events, tail=%d", generous.chunks.count, generous.count, len(generous.tail))
	}
}

// TestCheckpointCacheFlushKeepsInHandEntryCharged pins the hand-off itself:
// the entry being encoded stays charged until its write completes. With a
// 512 KiB incompressible entry and a budget whose slack is well below that
// entry's output, crediting the entry before its write would admit the
// flush; keeping it charged through the write must refuse and release.
func TestCheckpointCacheFlushKeepsInHandEntryCharged(t *testing.T) {
	t.Parallel()
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

// TestCheckpointCacheClonesAtExactSourceLength pins the fix for the
// preflight-then-clone gap: the cache clones into storage whose capacity is
// exactly the source length, so the length preflight in append bounds the
// clone allocation itself and the charge equals what was admitted. 300
// bytes sits off the allocator's size classes, so an append-based clone
// here would retain more capacity than length and fail this test.
func TestCheckpointCacheClonesAtExactSourceLength(t *testing.T) {
	t.Parallel()
	cache := checkpointEventCache{limit: 1 << 20}
	event := Event{
		Payload:     incompressiblePayload(t, 300, 1),
		Attachments: map[string][]byte{"name": incompressiblePayload(t, 300, 2)},
	}
	cache.append(event)
	if cache.err != nil || len(cache.tail) != 1 {
		t.Fatalf("append failed: err=%v tail=%d", cache.err, len(cache.tail))
	}
	entry := cache.tail[0]
	if cap(entry.Payload) != len(entry.Payload) {
		t.Fatalf("cloned payload retains %d bytes of capacity for %d of content", cap(entry.Payload), len(entry.Payload))
	}
	for name, content := range entry.Attachments {
		if cap(content) != len(content) {
			t.Fatalf("cloned attachment %q retains %d bytes of capacity for %d of content", name, cap(content), len(content))
		}
	}
	want := len(event.Payload) + len("name") + len(event.Attachments["name"]) +
		checkpointTailEntryOverhead + checkpointAttachmentOverhead
	if cache.tailBytes != want {
		t.Fatalf("tailBytes = %d, want the preflighted %d: charge and preflight must measure the same bytes", cache.tailBytes, want)
	}
}

// TestCheckpointCacheRefusesOversizedEventBeforeCloning measures the
// refusal path's allocations: an event the preflight refuses must never be
// cloned, or the clone transient would stand beside the retained bytes
// while the budget is already exceeded. A refusal allocates only its
// error; cloning first would add the payload, the attachment map, and
// every attachment body.
func TestCheckpointCacheRefusesOversizedEventBeforeCloning(t *testing.T) {
	attachments := make(map[string][]byte, 16)
	for index := 0; index < 16; index++ {
		attachments["attachment-"+strconv.Itoa(index)] = incompressiblePayload(t, 4096, int64(index))
	}
	event := Event{Payload: incompressiblePayload(t, 1<<20, 99), Attachments: attachments}
	allocs := testing.AllocsPerRun(10, func() {
		cache := checkpointEventCache{limit: 1024}
		cache.append(event)
		if !errors.Is(cache.err, errCheckpointTooLarge) {
			t.Fatalf("oversized append was admitted: %v", cache.err)
		}
	})
	if !raceEnabled && allocs > 8 {
		t.Fatalf("refused append allocated %.0f times; the event must not be cloned before the preflight admits it", allocs)
	}
}

// TestCheckpointCacheWriterRefusesAfterFail pins the load-bearing guard of
// the post-failure cleanup invariant: the write-boundary reservation. Two
// mechanisms defend the same invariant — the abandoned, never-Closed
// compressor on the failure paths, and this whole-limit reservation that
// refuses any write against a failed cache's budget. The reservation is the
// enforced one: it holds even if some cleanup does attempt to write, so the
// abandonment is defence in depth on top of it, kept because it avoids
// exercising this refusal at all.
//
// It also pins the failure-ordering
// invariant: fail releases the storage accounting, so nothing — including
// a compressor cleanup that runs after the failure — may build against
// that freed budget while the dropped material is still reachable. A
// failed cache reserves its entire budget.
func TestCheckpointCacheWriterRefusesAfterFail(t *testing.T) {
	t.Parallel()
	cache := checkpointEventCache{limit: 4096}
	var output bytes.Buffer
	writer := cache.boundedWriter(&output)
	if _, err := writer.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	cache.fail(errors.New("mid-build failure"))
	if _, err := writer.Write([]byte{0}); !errors.Is(err, errCheckpointTooLarge) {
		t.Fatalf("write against a failed cache's budget = %v, want %v", err, errCheckpointTooLarge)
	}
	if output.Len() != len("before") {
		t.Fatalf("failed cache's writer grew its output to %d bytes", output.Len())
	}
}

// TestCheckpointCacheChunkListChargesEachNode pins the metadata cost of the
// stored-chunk list. The list never reallocates — each stored chunk
// occupies exactly one node, so no backing-array growth boundary exists to
// charge, and the type itself guards against one reappearing: a slice
// representation cannot satisfy this test's node walk. What remains to
// verify is the accounting identity: after chunks land through both storing
// paths — borrowed chunks and a tail flush — chunkBytes must equal the
// retained chunk capacities plus one measured node per stored chunk.
// Dropping either node precharge, or shrinking the measured node size,
// breaks the equality.
func TestCheckpointCacheChunkListChargesEachNode(t *testing.T) {
	t.Parallel()
	cache := checkpointEventCache{limit: 4 << 20}
	borrowed := make([]Event, 2*checkpointChunkEvents)
	for index := range borrowed {
		borrowed[index] = Event{Payload: bytes.Repeat([]byte{byte(index)}, 32)}
	}
	cache.appendEvents(borrowed)
	for index := 0; index < checkpointChunkEvents; index++ {
		cache.append(Event{Payload: bytes.Repeat([]byte{byte(index)}, 24)})
	}
	if cache.err != nil || cache.chunks.count != 3 || cache.tail != nil {
		t.Fatalf("cache = err %v chunks %d tail %d, want 3 stored chunks and no tail",
			cache.err, cache.chunks.count, len(cache.tail))
	}
	nodes, retained := 0, 0
	for node := cache.chunks.head; node != nil; node = node.next {
		nodes++
		retained += cap(node.chunk)
	}
	if nodes != cache.chunks.count {
		t.Fatalf("list holds %d nodes but counts %d", nodes, cache.chunks.count)
	}
	// The node size is measured here, independently of the production
	// constant, so shrinking that constant cannot shrink this expectation.
	nodeBytes := int(unsafe.Sizeof(checkpointChunkNode{}))
	if want := retained + nodes*nodeBytes; cache.chunkBytes != want {
		t.Fatalf("chunkBytes = %d, want %d: capacities %d plus %d nodes at the measured %d bytes",
			cache.chunkBytes, want, retained, nodes, nodeBytes)
	}
}

// TestCheckpointMarshalUsesFullSizeLimit pins the published contract from
// docs/reference/limits.md: a serialized compact checkpoint may use its
// whole byte limit. A construction-memory policy leaking into the marshal
// path silently halves the largest writable checkpoint; marshalling at
// exactly the blob's own size proves the blob — necessarily larger than
// half that limit — is admitted, and one byte less refuses.
func TestCheckpointMarshalUsesFullSizeLimit(t *testing.T) {
	t.Parallel()
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1", Genesis: "genesis", Head: "head",
		Depth: 1, EventCount: 1,
		Events: []checkpointEvent{{Payload: incompressiblePayload(t, 64<<10, 5)}},
	}
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := marshalCheckpoint(stored, len(data))
	if err != nil {
		t.Fatalf("checkpoint filling its whole size limit was refused: %v", err)
	}
	if !bytes.Equal(exact, data) {
		t.Fatal("marshalling at the exact limit changed the blob")
	}
	if _, err := marshalCheckpoint(stored, len(data)-1); !errors.Is(err, errCheckpointTooLarge) {
		t.Fatalf("checkpoint one byte over the size limit = %v, want %v", err, errCheckpointTooLarge)
	}
}

// TestCheckpointMarshalRefusesChunkCountDivergence pins the marshal-path
// check that the chunk list's explicit count matches the payload chain the
// encoder traverses. The event-count identity trusts that count, so a
// divergent count would otherwise serialize a manifest promising events the
// chunks do not carry, or omitting events they do. Both divergence
// directions must refuse before anything is encoded, and the agreeing list
// must still marshal.
func TestCheckpointMarshalRefusesChunkCountDivergence(t *testing.T) {
	t.Parallel()
	events := make([]Event, checkpointChunkEvents)
	for index := range events {
		events[index] = Event{Payload: []byte{byte(index)}}
	}
	var cache checkpointEventCache
	cache.appendBorrowedChunk(events)
	if cache.err != nil || cache.chunks.count != 1 {
		t.Fatalf("fixture cache = err %v chunks %d, want one stored chunk", cache.err, cache.chunks.count)
	}
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: "sha1", Genesis: "genesis", Head: "head",
		Depth: checkpointChunkEvents, EventCount: checkpointChunkEvents,
		Cached: true, CachedChunks: cache.chunks,
	}
	if _, err := marshalCheckpoint(stored, maxCheckpointBytes); err != nil {
		t.Fatalf("agreeing chunk count was refused: %v", err)
	}
	for _, count := range []int{0, 2} {
		divergent := stored
		divergent.CachedChunks.count = count
		// Keep the event count consistent with the claimed chunk count, so
		// only the chain check can be the reason for refusal.
		divergent.EventCount = count * checkpointChunkEvents
		divergent.Depth = divergent.EventCount
		_, err := marshalCheckpoint(divergent, maxCheckpointBytes)
		if err == nil || !strings.Contains(err.Error(), "chunk count mismatch") {
			t.Fatalf("count %d over a one-node chain = %v, want chunk count mismatch", count, err)
		}
	}
}

// TestCheckpointCacheScratchPreflightCountsRetainedOutput pins the one
// blind spot the bounded writer cannot cover itself. The writer's
// invariant is that the reservation plus twice the output capacity fits
// the limit, but it can only check that on an underlying write, and the
// compressor buffers: a scratch-heavy event can raise the reservation
// while causing no underlying write at all. The scratch preflight must
// therefore hold the same invariant against the output capacity earlier
// writes have already grown. The regression replays the borrowed-chunk
// construction on the production components: ordinary no-scratch events
// grow the real output buffer first, then one all-attachment event
// raises scratch without reaching the buffer — the run asserts that
// non-reach directly. The limit is set from the capacity and reservation
// read out of the production objects, never from a parallel model: the
// exact budget — reservation, scratch, twice the retained capacity —
// must admit, and one byte short must refuse.
func TestCheckpointCacheScratchPreflightCountsRetainedOutput(t *testing.T) {
	t.Parallel()
	// Incompressible payloads force the compressor to emit blocks into
	// the bounded writer, growing the production output buffer before any
	// scratch exists.
	growth := make([]Event, 3)
	for index := range growth {
		growth[index] = Event{Payload: incompressiblePayload(t, 16384, int64(index+1))}
	}
	// Empty attachments with short repetitive names: the scratch charge
	// is real, but the few compressible encoded bytes stay inside the
	// compressor, so no underlying write can run the writer's own check.
	const attachmentCount = 512
	attachments := make(map[string][]byte, attachmentCount)
	for index := 0; index < attachmentCount; index++ {
		attachments["n-"+strconv.Itoa(index)] = nil
	}
	scratchEvent := Event{Attachments: attachments}

	// build replays appendBorrowedChunk's construction sequence up to the
	// scratch event and reports what the production objects held at the
	// moment its preflight ran, plus that preflight's outcome.
	type moment struct {
		capacity int
		length   int
		reserve  int
		err      error
	}
	build := func(limit int) moment {
		t.Helper()
		cache := checkpointEventCache{limit: limit}
		var output bytes.Buffer
		cache.chunkBytes += checkpointChunkNodeBytes
		bounded := cache.boundedWriter(&output)
		compressed := gzip.NewWriter(bounded)
		for _, event := range growth {
			if err := cache.writeEventReservingScratch(bounded, compressed, checkpointReference(event)); err != nil {
				t.Fatalf("growth event failed: %v", err)
			}
		}
		// Drain the compressor's pending block through the bounded writer.
		// These are real writes that grow the real buffer; afterwards the
		// compressor holds nothing, so the small scratch event below
		// cannot tip a pending block into an underlying write.
		if err := compressed.Flush(); err != nil {
			t.Fatalf("flush after growth failed: %v", err)
		}
		at := moment{
			capacity: output.Cap(),
			length:   output.Len(),
			reserve:  cache.chunkBytes + cache.tailBytes + cache.scratchBytes,
		}
		at.err = cache.writeEventReservingScratch(bounded, compressed, checkpointReference(scratchEvent))
		if output.Cap() != at.capacity || output.Len() != at.length {
			t.Fatalf("scratch event reached the underlying buffer (cap %d->%d, len %d->%d); shrink it so only the preflight can refuse",
				at.capacity, output.Cap(), at.length, output.Len())
		}
		return at
	}

	// Probe under a roomy limit to read the grown production capacity.
	probe := build(1 << 20)
	if probe.err != nil {
		t.Fatalf("probe scratch event failed: %v", probe.err)
	}
	if probe.length == 0 || probe.capacity == 0 {
		t.Fatalf("fixture proves nothing: the compressor never wrote, so no output capacity was retained")
	}
	if probe.capacity <= probe.length {
		t.Fatalf("fixture proves nothing about capacity: retained capacity %d does not exceed content %d, so length could impersonate it",
			probe.capacity, probe.length)
	}
	scratch := attachmentCount * int(unsafe.Sizeof(""))

	// The exact budget: what the cache still reserves, the new scratch,
	// and twice the capacity the production buffer actually retains.
	limit := probe.reserve + scratch + 2*probe.capacity
	exact := build(limit)
	if exact.capacity != probe.capacity || exact.reserve != probe.reserve {
		t.Fatalf("construction diverged under the exact limit: cap %d reserve %d, probe saw cap %d reserve %d",
			exact.capacity, exact.reserve, probe.capacity, probe.reserve)
	}
	if exact.err != nil {
		t.Fatalf("scratch %d within reserve %d plus twice retained capacity %d was refused: %v",
			scratch, exact.reserve, exact.capacity, exact.err)
	}

	short := build(limit - 1)
	if short.capacity != probe.capacity || short.reserve != probe.reserve {
		t.Fatalf("construction diverged one byte short: cap %d reserve %d, probe saw cap %d reserve %d",
			short.capacity, short.reserve, probe.capacity, probe.reserve)
	}
	if !errors.Is(short.err, errCheckpointTooLarge) {
		t.Fatalf("scratch one byte past the retained-output budget was admitted: %v", short.err)
	}
}

// TestCheckpointCacheScratchPreflightRefusesBeforeAllocation gives a
// borrowed chunk a budget one byte too small for its first event's
// sorting scratch alone, so the scratch preflight is what refuses. The
// refusal must release everything and must happen before the scratch is
// allocated: a preflight refusal allocates only the writer plumbing and
// its error — eight allocations measured — while checking after the fact
// would allocate the names slice first. The ceiling is the measured
// preflight count, so even that one extra allocation fails. That ceiling
// holds only without the race detector, whose per-call bookkeeping the
// count would otherwise include; the refusal itself is checked either way.
func TestCheckpointCacheScratchPreflightRefusesBeforeAllocation(t *testing.T) {
	const attachmentCount = 4096
	attachments := make(map[string][]byte, attachmentCount)
	for index := 0; index < attachmentCount; index++ {
		attachments["name-"+strconv.Itoa(index)] = nil
	}
	event := Event{Attachments: attachments}
	limit := int(unsafe.Sizeof(checkpointChunkNode{})) + attachmentCount*int(unsafe.Sizeof("")) - 1

	cache := checkpointEventCache{limit: limit}
	cache.appendBorrowedChunk([]Event{event})
	assertCacheReleased(t, &cache)

	allocs := testing.AllocsPerRun(10, func() {
		refused := checkpointEventCache{limit: limit}
		refused.appendBorrowedChunk([]Event{event})
		if !errors.Is(refused.err, errCheckpointTooLarge) {
			t.Fatalf("scratch past the budget was admitted: %v", refused.err)
		}
	})
	if !raceEnabled && allocs > 8 {
		t.Fatalf("preflight refusal allocated %.0f times; the names slice must not be allocated for scratch the budget refuses", allocs)
	}
}

// TestCompactCheckpointWriterRefusesAboveDecoderCeiling pins the
// encoder/decoder agreement on the attachment-count ceiling. The decoder's
// published ceiling is 1<<20, written here as a literal so a moved
// constant cannot move the expectation. An event one past it must be
// refused by the writer before a single byte is encoded — a checkpoint
// carrying it could never be read back — and before the names slice is
// allocated, so the refusal costs only its error. The decoder must refuse
// the same count and still admit the count one below it, so the two sides
// share one boundary rather than merely ordered ones.
func TestCompactCheckpointWriterRefusesAboveDecoderCeiling(t *testing.T) {
	const decoderCeiling = 1 << 20
	attachments := make(map[string][]byte, decoderCeiling+1)
	for index := 0; index <= decoderCeiling; index++ {
		attachments[strconv.Itoa(index)] = nil
	}
	event := checkpointEvent{Payload: []byte("payload"), Attachments: attachments}
	var output bytes.Buffer
	err := writeCompactCheckpointEvent(&output, event)
	if err == nil || !strings.Contains(err.Error(), "attachment count") {
		t.Fatalf("writer admitted %d attachments the decoder refuses: %v", len(attachments), err)
	}
	if output.Len() != 0 {
		t.Fatalf("writer emitted %d bytes before refusing an undecodable event", output.Len())
	}
	allocs := testing.AllocsPerRun(10, func() {
		if err := writeCompactCheckpointEvent(io.Discard, event); err == nil {
			t.Fatal("writer admitted an undecodable event")
		}
	})
	if !raceEnabled && allocs > 1 {
		t.Fatalf("refusal allocated %.0f times, want only the error: the names slice must not be allocated first", allocs)
	}

	// The decoder's side of the shared boundary: a stream declaring one
	// attachment past the ceiling refuses on the count itself, and one
	// declaring exactly the ceiling gets past the count check — it fails
	// later, on the truncated stream, not on the count.
	var over bytes.Buffer
	if err := writeCheckpointUint64(&over, 0); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpointUint32(&over, decoderCeiling+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readCompactCheckpointEvent(&over, ^uint64(0)); err == nil || !strings.Contains(err.Error(), "attachment count") {
		t.Fatalf("decoder ceiling moved without the writer: %v", err)
	}
	var at bytes.Buffer
	if err := writeCheckpointUint64(&at, 0); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpointUint32(&at, decoderCeiling); err != nil {
		t.Fatal(err)
	}
	if _, err := readCompactCheckpointEvent(&at, ^uint64(0)); err == nil || strings.Contains(err.Error(), "attachment count") {
		t.Fatalf("decoder refused the count at its own ceiling: %v", err)
	}
}
