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
// container charges, the stored chunk's slot in the growing chunk list,
// plus twice the encoded output — content and equal headroom for the old
// array a growth copy briefly holds — so the two limits sit exactly at
// and one byte below that bound.
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

	// With exactly the container charge, the chunk slot, and twice the
	// encoded output available, the flush fits only because each entry's
	// content is credited back as its write completes. It must succeed,
	// release the tail and its backing array, and retain no more output
	// capacity than the content needs.
	cache := checkpointEventCache{limit: container + checkpointChunkSlotOverhead + 2*encoded.Len()}
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
	if cache.chunkBytes != checkpointChunkSlotOverhead+cap(cache.chunks[0]) {
		t.Fatalf("chunkBytes = %d, want retained capacity %d plus the %d chunk slot",
			cache.chunkBytes, cap(cache.chunks[0]), checkpointChunkSlotOverhead)
	}
	if cache.chunkBytes != checkpointChunkSlotOverhead+encoded.Len() {
		t.Fatalf("flush retained %d bytes while the tail's container charge held %d of the %d limit; slot plus %d was the most the budget admitted",
			cache.chunkBytes, container, cache.byteLimit(), encoded.Len())
	}

	// One byte short, the output and its growth headroom can never fit
	// alongside the container storage and chunk slot that are still held,
	// so the flush must refuse and release rather than let the true peak
	// exceed the limit.
	short := checkpointEventCache{limit: container + checkpointChunkSlotOverhead + 2*encoded.Len() - 1}
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
	if cache.chunkBytes != checkpointChunkSlotOverhead+retained {
		t.Fatalf("chunkBytes = %d, want retained capacity %d plus the %d chunk slot (content is %d)",
			cache.chunkBytes, retained, checkpointChunkSlotOverhead, len(cache.chunks[0]))
	}
}

// TestCheckpointChunkWriterBoundsRetainedCapacity pins the cache writer's
// peak contract: content may fill only half the live budget, the other half
// is headroom for the old array a growth copy briefly holds, and the buffer
// never retains capacity past the content ceiling. The true peak — reserve,
// old array, new array — therefore never exceeds the limit.
func TestCheckpointChunkWriterBoundsRetainedCapacity(t *testing.T) {
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

	// The honest budget for this tail: its charges, the stored chunk's
	// slot, plus the encoded output and equal growth headroom in place of
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
	generous := checkpointEventCache{limit: charge + checkpointChunkSlotOverhead + 2*encoded.Len()}
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

// TestCheckpointCacheClonesAtExactSourceLength pins the fix for the
// preflight-then-clone gap: the cache clones into storage whose capacity is
// exactly the source length, so the length preflight in append bounds the
// clone allocation itself and the charge equals what was admitted. 300
// bytes sits off the allocator's size classes, so an append-based clone
// here would retain more capacity than length and fail this test.
func TestCheckpointCacheClonesAtExactSourceLength(t *testing.T) {
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
	if allocs > 8 {
		t.Fatalf("refused append allocated %.0f times; the event must not be cloned before the preflight admits it", allocs)
	}
}

// TestCheckpointCacheWriterRefusesAfterFail pins the failure-ordering
// invariant: fail releases the storage accounting, so nothing — including
// a compressor cleanup that runs after the failure — may build against
// that freed budget while the dropped material is still reachable. A
// failed cache reserves its entire budget.
func TestCheckpointCacheWriterRefusesAfterFail(t *testing.T) {
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

func TestCheckpointChunkSlotOverheadCoversSliceHeader(t *testing.T) {
	if header := int(unsafe.Sizeof([]byte(nil))); 2*header > checkpointChunkSlotOverhead {
		t.Fatalf("chunk slice header is %d bytes; the %d-byte slot charge no longer covers doubled growth", header, checkpointChunkSlotOverhead)
	}
}

// TestCheckpointMarshalUsesFullSizeLimit pins the published contract from
// docs/reference/limits.md: a serialized compact checkpoint may use its
// whole byte limit. A construction-memory policy leaking into the marshal
// path silently halves the largest writable checkpoint; marshalling at
// exactly the blob's own size proves the blob — necessarily larger than
// half that limit — is admitted, and one byte less refuses.
func TestCheckpointMarshalUsesFullSizeLimit(t *testing.T) {
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
