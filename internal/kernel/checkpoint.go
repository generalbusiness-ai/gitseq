package kernel

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

const (
	checkpointSchema                = "gitseq-checkpoint@3"
	profiledCompactCheckpointSchema = "gitseq-checkpoint@2"
	legacyCheckpointSchema          = "gitseq-checkpoint@1"
	checkpointFile                  = "checkpoint"
	checkpointMarker                = "gitseq-checkpoint-v3"
	profiledCompactCheckpointMarker = "gitseq-checkpoint-v2"
	legacyCheckpointMarker          = "gitseq-checkpoint-v1"
	checkpointContainer             = "gitseq-checkpoint-container-v2\x00"
	checkpointInterval              = 256
	checkpointChunkEvents           = 4096
	maxCheckpointBytes              = 256 << 20
	maxCheckpointManifest           = 1 << 20
)

var (
	ErrNoUsableCheckpoint = errors.New("no usable checkpoint")
	errCheckpointTooLarge = errors.New("checkpoint exceeds byte limit")
)

// checkpointStreamError marks a failure after a valid checkpoint has begun
// transferring provisional events. The caller must not try another candidate
// or fall back to a cold scan with the same sink, because that would deliver a
// duplicate prefix to application state that is waiting to be discarded.
type checkpointStreamError struct{ err error }

func (e *checkpointStreamError) Error() string { return e.err.Error() }
func (e *checkpointStreamError) Unwrap() error { return e.err }

// CheckpointOptions enables the optional Git-backed restart cache. The cache
// contains only kernel-verified event material and is reusable across
// application folds. SigningKey is optional: readers without sequencer
// custody may consume a valid checkpoint but never publish one.
type CheckpointOptions struct {
	Enabled    bool
	SigningKey string
	// Pointer is an optional host-owned, process-independent selector for a
	// signed Git checkpoint object. The kernel assigns it no authority: every
	// selected object still passes the complete checkpoint verification path.
	Pointer CheckpointPointer
}

func (o CheckpointOptions) enabled() bool { return o.Enabled }

// CheckpointPointer is the host seam for local checkpoint selection. It keeps
// filesystem policy outside the kernel; implementations store only an opaque
// object ID and must not treat it as verified.
type CheckpointPointer interface {
	Load() (string, error)
	Store(string) error
}

type checkpoint struct {
	Schema       string `json:"schema"`
	ObjectFormat string `json:"object_format"`
	Genesis      string `json:"genesis"`
	Head         string `json:"head"`
	Depth        int    `json:"depth"`
	// Profile exists only to decode checkpoint@1 and checkpoint@2. Current
	// checkpoints never write it; projection selectors belong above the kernel.
	Profile      string            `json:"profile,omitempty"`
	Events       []checkpointEvent `json:"events"`
	EventCount   int               `json:"-"`
	Cached       bool              `json:"-"`
	CachedChunks [][]byte          `json:"-"`
	CachedTail   []checkpointEvent `json:"-"`
}

type checkpointEvent struct {
	Commit      string            `json:"commit"`
	Timestamp   int64             `json:"timestamp"`
	Signed      intent.Signed     `json:"signed"`
	Payload     []byte            `json:"payload"`
	Attachments map[string][]byte `json:"attachments,omitempty"`
}

// checkpointEventCache retains only the material a future compact checkpoint
// writes. Full chunks are compressed; the raw tail is bounded independently
// of sequence depth.
type checkpointEventCache struct {
	chunks [][]byte
	tail   []checkpointEvent
	count  int
	err    error
}

func (c *checkpointEventCache) reset(events []Event) {
	*c = checkpointEventCache{}
	c.appendEvents(events)
}

func (c *checkpointEventCache) appendEvents(events []Event) {
	for _, event := range events {
		c.append(event)
	}
}

func (c *checkpointEventCache) append(event Event) {
	if c.err != nil {
		return
	}
	c.tail = append(c.tail, checkpointMaterial(event))
	c.count++
	if len(c.tail) < checkpointChunkEvents {
		return
	}
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	for _, material := range c.tail {
		if err := writeCompactCheckpointEvent(compressed, material); err != nil {
			c.err = err
			_ = compressed.Close()
			return
		}
	}
	if err := compressed.Close(); err != nil {
		c.err = err
		return
	}
	c.chunks = append(c.chunks, output.Bytes())
	clear(c.tail)
	c.tail = c.tail[:0]
}

type compactCheckpointManifest struct {
	Schema       string `json:"schema"`
	ObjectFormat string `json:"object_format"`
	Genesis      string `json:"genesis"`
	Head         string `json:"head"`
	Depth        int    `json:"depth"`
	Profile      string `json:"profile,omitempty"`
	Events       int    `json:"events"`
}

type checkpointCandidate struct {
	commit  string
	stored  checkpoint
	payload []byte
}

func CheckpointRef(genesis string) string { return "refs/gitseq/checkpoints/" + genesis }

func loadCheckpoint(ctx context.Context, store gitstore.Store, genesis, head string, options CheckpointOptions) (scannedLog, bool, error) {
	return loadCheckpointInto(ctx, store, genesis, head, options, nil)
}

func loadCheckpointInto(ctx context.Context, store gitstore.Store, genesis, head string, options CheckpointOptions, accept func(Event) error) (scannedLog, bool, error) {
	if !options.enabled() {
		return scannedLog{}, false, ErrNoUsableCheckpoint
	}
	var candidates []string
	pointerCommit := ""
	refCommit := ""
	if options.Pointer != nil {
		if commit, err := options.Pointer.Load(); err == nil {
			pointerCommit = commit
			candidates = append(candidates, commit)
		}
	}
	if commit, err := store.Head(ctx, CheckpointRef(genesis)); err == nil {
		refCommit = commit
		if len(candidates) == 0 || candidates[0] != commit {
			candidates = append(candidates, commit)
		}
	}
	if len(candidates) == 0 {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint pointer and ref are unavailable", ErrNoUsableCheckpoint)
	}
	desc, err := Descriptor(ctx, store, genesis)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: descriptor: %v", ErrNoUsableCheckpoint, err)
	}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return scannedLog{}, false, err
	}
	commits, err := store.RevListMetadata(ctx, head)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: sequence: %v", ErrNoUsableCheckpoint, err)
	}
	if err := validateNamedSequence(head, commits); err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	parsed := make([]checkpointCandidate, 0, len(candidates))
	var lastErr error
	for _, commit := range candidates {
		candidate, err := readCheckpointCandidate(ctx, store, desc, format, genesis, commit)
		if err != nil {
			lastErr = err
			continue
		}
		parsed = append(parsed, candidate)
	}
	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].stored.Depth > parsed[j].stored.Depth })
	for _, candidate := range parsed {
		log, err := authenticateCheckpointCandidateInto(ctx, store, candidate, desc, commits, accept == nil)
		if err != nil {
			lastErr = err
			continue
		}
		advanced := candidate.stored.Head != head
		checkpointBase := log
		var suffix []gitstore.CommitMetadata
		if advanced {
			suffix = commits[candidate.stored.Depth+1:]
			prefix := append([]Event(nil), log.Events...)
			extended, err := scanListedAfterMode(ctx, store, log, head, suffix, true, accept == nil, nil)
			if err != nil {
				lastErr = fmt.Errorf("%w: checkpoint frontier: %v", ErrNoUsableCheckpoint, err)
				continue
			}
			log = extended
			if accept == nil {
				log.Events = append(prefix, extended.Events...)
			}
		}
		if accept != nil {
			if err := streamCheckpointCandidate(candidate, desc, commits, accept); err != nil {
				return scannedLog{}, false, &checkpointStreamError{err: err}
			}
			if len(suffix) > 0 {
				replayBase := checkpointBase
				replayBase.Dedup = nil
				replayBase.Events = nil
				if _, err := scanListedAfterMode(ctx, store, replayBase, head, suffix, true, false, accept); err != nil {
					return scannedLog{}, false, &checkpointStreamError{err: fmt.Errorf("replay checkpoint frontier: %w", err)}
				}
			}
			log.Events = nil
		}
		// Repair the Git reachability anchor before the host selector. Each is
		// only a hint, so a repair failure cannot invalidate an authenticated
		// checkpoint or block the other repair.
		if refCommit != candidate.commit {
			_ = store.UpdateRef(ctx, CheckpointRef(genesis), candidate.commit, refCommit)
		}
		if options.Pointer != nil && pointerCommit != candidate.commit {
			_ = options.Pointer.Store(candidate.commit)
		}
		return log, advanced, nil
	}
	return scannedLog{}, false, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, lastErr)
}

func readCheckpointCandidate(ctx context.Context, store gitstore.Store, desc GenesisDescriptor, format, genesis, commit string) (checkpointCandidate, error) {
	// A local pointer is mutable input. Validate it before it reaches any Git
	// command so it can select only an object ID, never a command-line option.
	if err := validateObjectID(format, commit); err != nil {
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint commit: %v", ErrNoUsableCheckpoint, err)
	}
	parents, err := store.CommitParents(ctx, commit)
	if err != nil || len(parents) != 0 {
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint commit must be parentless", ErrNoUsableCheckpoint)
	}
	message, err := store.CommitMessage(ctx, commit)
	marker := strings.TrimSpace(message)
	if err != nil || (marker != checkpointMarker && marker != profiledCompactCheckpointMarker && marker != legacyCheckpointMarker) {
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint marker", ErrNoUsableCheckpoint)
	}
	files, err := store.ListFiles(ctx, commit, "")
	if err != nil || len(files) != 1 || files[0] != checkpointFile {
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint tree shape", ErrNoUsableCheckpoint)
	}
	data, err := store.ReadFileLimit(ctx, commit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint payload: %v", ErrNoUsableCheckpoint, err)
	}
	var candidate checkpointCandidate
	candidate.commit = commit
	if marker == legacyCheckpointMarker {
		candidate.stored, err = decodeLegacyCheckpoint(data)
	} else {
		candidate.stored, candidate.payload, err = decodeCompactCheckpointManifest(data)
	}
	if err != nil {
		return checkpointCandidate{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	stored := candidate.stored
	switch {
	case marker == checkpointMarker && stored.Schema == checkpointSchema:
		if stored.Profile != "" {
			return checkpointCandidate{}, fmt.Errorf("%w: application profile in kernel checkpoint", ErrNoUsableCheckpoint)
		}
	case marker == profiledCompactCheckpointMarker && stored.Schema == profiledCompactCheckpointSchema:
		if stored.Profile == "" {
			return checkpointCandidate{}, fmt.Errorf("%w: profiled compact checkpoint profile is missing", ErrNoUsableCheckpoint)
		}
	case marker == legacyCheckpointMarker && stored.Schema == legacyCheckpointSchema:
		if stored.Profile == "" {
			return checkpointCandidate{}, fmt.Errorf("%w: legacy checkpoint profile is missing", ErrNoUsableCheckpoint)
		}
	default:
		return checkpointCandidate{}, fmt.Errorf("%w: checkpoint marker/schema mismatch", ErrNoUsableCheckpoint)
	}
	if stored.ObjectFormat != format || stored.ObjectFormat != desc.ObjectFormat || stored.Genesis != genesis {
		return checkpointCandidate{}, fmt.Errorf("%w: kernel identity mismatch", ErrNoUsableCheckpoint)
	}
	return candidate, nil
}

func authenticateCheckpointCandidate(ctx context.Context, store gitstore.Store, candidate checkpointCandidate, desc GenesisDescriptor, commits []gitstore.CommitMetadata) (scannedLog, error) {
	return authenticateCheckpointCandidateInto(ctx, store, candidate, desc, commits, true)
}

func authenticateCheckpointCandidateInto(ctx context.Context, store gitstore.Store, candidate checkpointCandidate, desc GenesisDescriptor, commits []gitstore.CommitMetadata, retainEvents bool) (scannedLog, error) {
	stored := candidate.stored
	eventPositions, err := checkpointEventPositions(stored, checkpointEventCount(stored), desc, commits)
	if err != nil {
		return scannedLog{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	sequencerPublicKey, err := verifyCheckpointRotations(ctx, store, stored, desc, commits)
	if err != nil {
		return scannedLog{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	if err := store.VerifySSHCommit(ctx, candidate.commit, "sequencer", sequencerPublicKey); err != nil {
		return scannedLog{}, fmt.Errorf("%w: signature: %v", ErrNoUsableCheckpoint, err)
	}
	var log scannedLog
	if stored.Schema == checkpointSchema || stored.Schema == profiledCompactCheckpointSchema {
		log, err = validateCompactCheckpointInto(stored, candidate.payload, desc, eventPositions, retainEvents)
	} else {
		log, err = validateCheckpointEventsInto(stored, desc, eventPositions, retainEvents)
	}
	if err != nil {
		return scannedLog{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	log.sequencerPublicKey = sequencerPublicKey
	return log, nil
}

// streamCheckpointCandidate replays a candidate only after its complete
// checkpoint and any current-head suffix have already verified. This second
// bounded pass lets application state consume records without retaining the
// depth-sized Event slice and without exposing callbacks from a rejected
// checkpoint candidate.
func streamCheckpointCandidate(candidate checkpointCandidate, desc GenesisDescriptor, commits []gitstore.CommitMetadata, accept func(Event) error) error {
	stored := candidate.stored
	positions, err := checkpointEventPositions(stored, checkpointEventCount(stored), desc, commits)
	if err != nil {
		return err
	}
	if stored.Schema == checkpointSchema || stored.Schema == profiledCompactCheckpointSchema {
		reader, source, err := openCompactCheckpointPayload(candidate.payload)
		if err != nil {
			return err
		}
		for index, position := range positions {
			remaining := desc.PayloadCeiling - uint64(len(position.Message))
			cached, err := readCompactCheckpointEvent(reader, remaining)
			if err != nil {
				return fmt.Errorf("checkpoint event %d: %w", index, err)
			}
			event, err := checkpointEventFromPayload(index, position, cached.Payload, cached.Attachments, stored, desc)
			if err != nil {
				return err
			}
			if err := accept(event); err != nil {
				return err
			}
		}
		return finishCompactCheckpointPayload(reader, source)
	}
	for index, cached := range stored.Events {
		event, err := checkpointEventFromPayload(index, positions[index], cached.Payload, cached.Attachments, stored, desc)
		if err != nil {
			return err
		}
		if err := accept(event); err != nil {
			return err
		}
	}
	return nil
}

func validateCheckpoint(stored checkpoint, desc GenesisDescriptor, sequence []gitstore.CommitMetadata) (scannedLog, error) {
	eventPositions, err := checkpointEventPositions(stored, len(stored.Events), desc, sequence)
	if err != nil {
		return scannedLog{}, err
	}
	return validateCheckpointEvents(stored, desc, eventPositions)
}

func validateCheckpointEvents(stored checkpoint, desc GenesisDescriptor, eventPositions []gitstore.CommitMetadata) (scannedLog, error) {
	return validateCheckpointEventsInto(stored, desc, eventPositions, true)
}

func validateCheckpointEventsInto(stored checkpoint, desc GenesisDescriptor, eventPositions []gitstore.CommitMetadata, retainEvents bool) (scannedLog, error) {
	log := scannedLog{
		Verification:       Verification{Genesis: stored.Genesis, Head: stored.Head, Depth: stored.Depth, Events: len(stored.Events)},
		Dedup:              make(map[string]Event, len(stored.Events)),
		Positions:          newPositions(stored.Genesis, len(stored.Events)+1),
		sequencerPublicKey: desc.SequencerPublicKey,
	}
	if retainEvents {
		log.Events = make([]Event, 0, len(stored.Events))
	}
	seenCommits := make(map[string]struct{}, len(stored.Events))
	for index, cached := range stored.Events {
		if err := validateObjectID(stored.ObjectFormat, cached.Commit); err != nil {
			return scannedLog{}, fmt.Errorf("event %d commit: %w", index, err)
		}
		if _, exists := seenCommits[cached.Commit]; exists {
			return scannedLog{}, fmt.Errorf("event %d repeats commit %s", index, cached.Commit)
		}
		position := eventPositions[index]
		if cached.Commit != position.OID {
			return scannedLog{}, fmt.Errorf("event %d commit does not match sequence", index)
		}
		if cached.Timestamp != position.Timestamp {
			return scannedLog{}, fmt.Errorf("event %d cached timestamp differs from sequence commit", index)
		}
		seenCommits[cached.Commit] = struct{}{}
		actualSigned, _, err := intent.ParseEnvelope(position.Message, desc.PayloadCeiling)
		if err != nil {
			return scannedLog{}, fmt.Errorf("event %d commit envelope: %w", index, err)
		}
		if !actualSigned.Equal(cached.Signed) {
			return scannedLog{}, fmt.Errorf("event %d cached intent differs from sequence commit", index)
		}
		event, err := checkpointEventFromPayload(index, position, cached.Payload, cached.Attachments, stored, desc)
		if err != nil {
			return scannedLog{}, err
		}
		if err := appendCheckpointEventInto(&log, index, event, retainEvents); err != nil {
			return scannedLog{}, err
		}
	}
	return log, nil
}

func checkpointEventCount(stored checkpoint) int {
	if stored.Schema == checkpointSchema || stored.Schema == profiledCompactCheckpointSchema {
		return stored.EventCount
	}
	return len(stored.Events)
}

func checkpointEventPositions(stored checkpoint, eventCount int, desc GenesisDescriptor, sequence []gitstore.CommitMetadata) ([]gitstore.CommitMetadata, error) {
	if stored.Depth < 0 || eventCount < 0 || stored.Depth < eventCount {
		return nil, errors.New("checkpoint depth is smaller than event count")
	}
	if stored.Depth >= len(sequence) || sequence[0].OID != stored.Genesis {
		return nil, errors.New("checkpoint does not begin the named sequence")
	}
	if err := validateChainParents(0, sequence[0].Parents, ""); err != nil {
		return nil, errors.New("checkpoint genesis has a parent")
	}
	for index := 1; index <= stored.Depth; index++ {
		if err := validateChainParents(index, sequence[index].Parents, sequence[index-1].OID); err != nil {
			return nil, fmt.Errorf("checkpoint event %d is not single-parent chained", index-1)
		}
	}
	if stored.Depth == 0 {
		if stored.Head != stored.Genesis {
			return nil, errors.New("empty checkpoint head is not genesis")
		}
	}
	if sequence[stored.Depth].OID != stored.Head {
		return nil, errors.New("checkpoint head is not the claimed sequence frontier")
	}
	eventPositions := make([]gitstore.CommitMetadata, 0, eventCount)
	for index := 1; index <= stored.Depth; index++ {
		position := sequence[index]
		if uint64(len(position.Message)) > desc.PayloadCeiling {
			return nil, fmt.Errorf("commit %s envelope exceeds genesis ceiling", position.OID)
		}
		_, rotation, err := parseRotationMessage(position.Message)
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", position.OID, err)
		}
		if !rotation {
			eventPositions = append(eventPositions, position)
		}
	}
	if len(eventPositions) != eventCount {
		return nil, errors.New("checkpoint event count does not match sequence prefix")
	}
	return eventPositions, nil
}

func checkpointEventFromPayload(index int, position gitstore.CommitMetadata, payload []byte, attachments map[string][]byte, stored checkpoint, desc GenesisDescriptor) (Event, error) {
	actualSigned, trailers, err := intent.ParseEnvelope(position.Message, desc.PayloadCeiling)
	if err != nil {
		return Event{}, fmt.Errorf("event %d commit envelope: %w", index, err)
	}
	decoded, targetMatches, err := verifySignedTarget(actualSigned, "git:"+stored.ObjectFormat+":"+stored.Genesis)
	if err != nil {
		return Event{}, fmt.Errorf("event %d actor signature: %w", index, err)
	}
	if !targetMatches {
		return Event{}, fmt.Errorf("event %d target does not name checkpoint genesis", index)
	}
	if !intent.EqualRefs(decoded.RestsOn, trailers) {
		return Event{}, fmt.Errorf("event %d causal trailers differ from signed intent", index)
	}
	format, tree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil || format != stored.ObjectFormat {
		return Event{}, fmt.Errorf("event %d payload tree identity", index)
	}
	if position.Tree != tree {
		return Event{}, fmt.Errorf("event %d commit tree differs from signed intent", index)
	}
	remaining := desc.PayloadCeiling - uint64(len(position.Message))
	if err := payloadWithinCeiling(payload, attachments, remaining); err != nil {
		return Event{}, fmt.Errorf("event %d: %w", index, err)
	}
	calculated, err := gitstore.HashPayloadTree(stored.ObjectFormat, payload, attachments)
	if err != nil || calculated != tree {
		return Event{}, fmt.Errorf("event %d payload differs from actor-signed tree", index)
	}
	return Event{
		Commit: position.OID, Timestamp: position.Timestamp, Intent: decoded, Signed: actualSigned, PayloadTree: decoded.PayloadTree,
		Payload: payload, Attachments: attachments,
	}, nil
}

func appendCheckpointEventInto(log *scannedLog, index int, event Event, retainEvent bool) error {
	key, err := event.Signed.DedupKey()
	if err != nil {
		return err
	}
	prior, duplicate, dedupErr := dedupPrior(log.Dedup, key, event.Signed)
	if dedupErr != nil {
		return fmt.Errorf("event %d: %w", index, dedupErr)
	}
	if duplicate {
		return fmt.Errorf("event %d duplicates idempotent event %s", index, prior.Commit)
	}
	log.Dedup[key] = eventWithoutPayload(event)
	log.Positions[event.Commit] = struct{}{}
	if retainEvent {
		log.Events = append(log.Events, event)
	}
	return nil
}

// verifyCheckpointRotations derives the key current at the cached frontier
// only from rotation commits in the exact named sequence. Each transition is
// authenticated by the key that was current immediately before it; the
// derived frontier key can then authenticate the checkpoint commit itself.
func verifyCheckpointRotations(ctx context.Context, store gitstore.Store, stored checkpoint, desc GenesisDescriptor, sequence []gitstore.CommitMetadata) (string, error) {
	current := desc.SequencerPublicKey
	emptyTree := ""
	for index := 1; index <= stored.Depth; index++ {
		position := sequence[index]
		successor, rotation, err := parseRotationMessage(position.Message)
		if err != nil {
			return "", fmt.Errorf("commit %s: %w", position.OID, err)
		}
		if !rotation {
			continue
		}
		if err := store.VerifySSHCommit(ctx, position.OID, "sequencer", current); err != nil {
			return "", fmt.Errorf("rotation %s sequencer signature: %w", position.OID, err)
		}
		if emptyTree == "" {
			emptyTree, err = store.EmptyTree(ctx)
			if err != nil {
				return "", err
			}
		}
		if position.Tree != emptyTree {
			return "", fmt.Errorf("commit %s rotation tree is not empty", position.OID)
		}
		if successor == current {
			return "", fmt.Errorf("commit %s rotates to the current sequencer key", position.OID)
		}
		current = successor
	}
	return current, nil
}

func writeCheckpoint(ctx context.Context, store gitstore.Store, log scannedLog, options CheckpointOptions) error {
	if !options.enabled() || options.SigningKey == "" || len(log.Events) != log.Verification.Events {
		return ErrNoUsableCheckpoint
	}
	events := make([]checkpointEvent, 0, len(log.Events))
	for _, event := range log.Events {
		events = append(events, checkpointEvent{Payload: event.Payload, Attachments: event.Attachments})
	}
	return writeCheckpointEvents(ctx, store, log, events, options)
}

func writeCheckpointEvents(ctx context.Context, store gitstore.Store, log scannedLog, events []checkpointEvent, options CheckpointOptions) error {
	if !options.enabled() || options.SigningKey == "" || len(events) != log.Verification.Events {
		return ErrNoUsableCheckpoint
	}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return err
	}
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: format, Genesis: log.Verification.Genesis,
		Head: log.Verification.Head, Depth: log.Verification.Depth,
		EventCount: len(events), Events: events,
	}
	return publishCheckpoint(ctx, store, log, stored, options)
}

func writeCheckpointCache(ctx context.Context, store gitstore.Store, log scannedLog, cache checkpointEventCache, options CheckpointOptions) error {
	if !options.enabled() || options.SigningKey == "" || cache.err != nil || cache.count != log.Verification.Events {
		return ErrNoUsableCheckpoint
	}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return err
	}
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: format, Genesis: log.Verification.Genesis,
		Head: log.Verification.Head, Depth: log.Verification.Depth,
		EventCount: cache.count, Cached: true, CachedChunks: cache.chunks, CachedTail: cache.tail,
	}
	return publishCheckpoint(ctx, store, log, stored, options)
}

func publishCheckpoint(ctx context.Context, store gitstore.Store, log scannedLog, stored checkpoint, options CheckpointOptions) error {
	data, err := marshalCheckpoint(stored, maxCheckpointBytes)
	if err != nil {
		return err
	}
	tree, err := store.WriteSingleFileTree(ctx, checkpointFile, data)
	if err != nil {
		return err
	}
	commit, err := store.SignedCommit(ctx, tree, "", checkpointMarker+"\n", options.SigningKey, gitstore.CommitIdentity{
		AuthorName: "gitseq checkpoint", AuthorEmail: "checkpoint@gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		return err
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", log.sequencerPublicKey); err != nil {
		return fmt.Errorf("checkpoint signature does not match current sequencer key: %w", err)
	}
	ref := CheckpointRef(log.Verification.Genesis)
	old, _ := store.Head(ctx, ref)
	if err := store.UpdateRef(ctx, ref, commit, old); err != nil {
		return err
	}
	// The ref is the reachability and recovery anchor. A host persistence
	// failure must never prevent it from advancing.
	if options.Pointer != nil {
		_ = options.Pointer.Store(commit)
	}
	return nil
}

func validateNamedSequence(head string, commits []gitstore.CommitMetadata) error {
	if len(commits) == 0 || commits[len(commits)-1].OID != head {
		return errors.New("sequence does not end at named head")
	}
	return nil
}

func marshalCheckpoint(stored checkpoint, limit int) ([]byte, error) {
	if stored.Schema == checkpointSchema || stored.Schema == profiledCompactCheckpointSchema {
		return marshalCompactCheckpoint(stored, limit)
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%w: size %d exceeds limit %d", errCheckpointTooLarge, len(data), limit)
	}
	return data, nil
}

func decodeCheckpoint(data []byte) (checkpoint, error) {
	if bytes.HasPrefix(data, []byte(checkpointContainer)) {
		return decodeCompactCheckpoint(data)
	}
	return decodeLegacyCheckpoint(data)
}

func decodeLegacyCheckpoint(data []byte) (checkpoint, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored checkpoint
	if err := decoder.Decode(&stored); err != nil {
		return checkpoint{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return checkpoint{}, errors.New("checkpoint has trailing JSON")
	}
	canonical, err := json.Marshal(stored)
	if err != nil || !bytes.Equal(canonical, data) {
		return checkpoint{}, errors.New("checkpoint is not canonical JSON")
	}
	return stored, nil
}

func marshalCompactCheckpoint(stored checkpoint, limit int) ([]byte, error) {
	if stored.EventCount < 0 ||
		(!stored.Cached && stored.EventCount != len(stored.Events)) ||
		(stored.Cached && stored.EventCount != len(stored.CachedChunks)*checkpointChunkEvents+len(stored.CachedTail)) {
		return nil, errors.New("compact checkpoint event count mismatch")
	}
	manifest := compactCheckpointManifest{
		Schema: stored.Schema, ObjectFormat: stored.ObjectFormat, Genesis: stored.Genesis,
		Head: stored.Head, Depth: stored.Depth, Profile: stored.Profile, Events: stored.EventCount,
	}
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(encodedManifest) > maxCheckpointManifest {
		return nil, fmt.Errorf("%w: manifest exceeds limit", errCheckpointTooLarge)
	}
	var output bytes.Buffer
	output.WriteString(checkpointContainer)
	if err := binary.Write(&output, binary.BigEndian, uint32(len(encodedManifest))); err != nil {
		return nil, err
	}
	output.Write(encodedManifest)
	if output.Len() > limit {
		return nil, fmt.Errorf("%w: size %d exceeds limit %d", errCheckpointTooLarge, output.Len(), limit)
	}
	bounded := &checkpointLimitWriter{output: &output, limit: limit}
	compressed, err := gzip.NewWriterLevel(bounded, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	compressed.Header.ModTime = time.Time{}
	compressed.Header.OS = 255
	if stored.Cached {
		for _, chunk := range stored.CachedChunks {
			reader, source, err := openCompactCheckpointPayload(chunk)
			if err != nil {
				_ = compressed.Close()
				return nil, err
			}
			if _, err := io.Copy(compressed, reader); err != nil {
				_ = reader.Close()
				_ = compressed.Close()
				return nil, err
			}
			if err := finishCompactCheckpointPayload(reader, source); err != nil {
				_ = compressed.Close()
				return nil, err
			}
		}
		for _, event := range stored.CachedTail {
			if err := writeCompactCheckpointEvent(compressed, event); err != nil {
				_ = compressed.Close()
				return nil, err
			}
		}
	} else {
		for _, event := range stored.Events {
			if err := writeCompactCheckpointEvent(compressed, event); err != nil {
				_ = compressed.Close()
				return nil, err
			}
		}
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	if output.Len() > limit {
		return nil, fmt.Errorf("%w: size %d exceeds limit %d", errCheckpointTooLarge, output.Len(), limit)
	}
	return output.Bytes(), nil
}

type checkpointLimitWriter struct {
	output *bytes.Buffer
	limit  int
}

func (w *checkpointLimitWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.output.Len() {
		return 0, fmt.Errorf("%w: limit %d", errCheckpointTooLarge, w.limit)
	}
	return w.output.Write(data)
}

func writeCompactCheckpointEvent(writer io.Writer, event checkpointEvent) error {
	if err := writeCheckpointUint64(writer, uint64(len(event.Payload))); err != nil {
		return err
	}
	if _, err := writer.Write(event.Payload); err != nil {
		return err
	}
	if uint64(len(event.Attachments)) > uint64(^uint32(0)) {
		return errors.New("too many checkpoint attachments")
	}
	if err := writeCheckpointUint32(writer, uint32(len(event.Attachments))); err != nil {
		return err
	}
	names := make([]string, 0, len(event.Attachments))
	for name := range event.Attachments {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(name) > int(^uint16(0)) {
			return errors.New("checkpoint attachment name is too long")
		}
		if err := writeCheckpointUint16(writer, uint16(len(name))); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, name); err != nil {
			return err
		}
		content := event.Attachments[name]
		if err := writeCheckpointUint64(writer, uint64(len(content))); err != nil {
			return err
		}
		if _, err := writer.Write(content); err != nil {
			return err
		}
	}
	return nil
}

func writeCheckpointUint64(writer io.Writer, value uint64) error {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, err := writer.Write(encoded[:])
	return err
}

func writeCheckpointUint32(writer io.Writer, value uint32) error {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, err := writer.Write(encoded[:])
	return err
}

func writeCheckpointUint16(writer io.Writer, value uint16) error {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], value)
	_, err := writer.Write(encoded[:])
	return err
}

func decodeCompactCheckpointManifest(data []byte) (checkpoint, []byte, error) {
	if !bytes.HasPrefix(data, []byte(checkpointContainer)) {
		return checkpoint{}, nil, errors.New("checkpoint container marker")
	}
	rest := data[len(checkpointContainer):]
	if len(rest) < 4 {
		return checkpoint{}, nil, errors.New("checkpoint manifest length")
	}
	manifestSize := uint64(binary.BigEndian.Uint32(rest[:4]))
	rest = rest[4:]
	if manifestSize == 0 || manifestSize > maxCheckpointManifest || manifestSize > uint64(len(rest)) {
		return checkpoint{}, nil, errors.New("checkpoint manifest exceeds container")
	}
	manifestEnd := int(manifestSize)
	encoded := rest[:manifestEnd]
	payload := rest[manifestEnd:]
	var manifest compactCheckpointManifest
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return checkpoint{}, nil, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return checkpoint{}, nil, errors.New("checkpoint manifest is not canonical JSON")
	}
	if len(payload) == 0 {
		return checkpoint{}, nil, errors.New("checkpoint payload is empty")
	}
	return checkpoint{
		Schema: manifest.Schema, ObjectFormat: manifest.ObjectFormat, Genesis: manifest.Genesis,
		Head: manifest.Head, Depth: manifest.Depth, Profile: manifest.Profile, EventCount: manifest.Events,
	}, payload, nil
}

func decodeCompactCheckpoint(data []byte) (checkpoint, error) {
	stored, payload, err := decodeCompactCheckpointManifest(data)
	if err != nil {
		return checkpoint{}, err
	}
	if stored.EventCount < 0 {
		return checkpoint{}, errors.New("checkpoint event count is negative")
	}
	reader, source, err := openCompactCheckpointPayload(payload)
	if err != nil {
		return checkpoint{}, err
	}
	stored.Events = make([]checkpointEvent, 0, stored.EventCount)
	for index := 0; index < stored.EventCount; index++ {
		event, err := readCompactCheckpointEvent(reader, ^uint64(0))
		if err != nil {
			return checkpoint{}, fmt.Errorf("checkpoint event %d: %w", index, err)
		}
		stored.Events = append(stored.Events, event)
	}
	if err := finishCompactCheckpointPayload(reader, source); err != nil {
		return checkpoint{}, err
	}
	return stored, nil
}

func validateCompactCheckpointInto(stored checkpoint, payload []byte, desc GenesisDescriptor, positions []gitstore.CommitMetadata, retainEvents bool) (scannedLog, error) {
	reader, source, err := openCompactCheckpointPayload(payload)
	if err != nil {
		return scannedLog{}, err
	}
	log := scannedLog{
		Verification:       Verification{Genesis: stored.Genesis, Head: stored.Head, Depth: stored.Depth, Events: stored.EventCount},
		Dedup:              make(map[string]Event, stored.EventCount),
		Positions:          newPositions(stored.Genesis, stored.EventCount+1),
		sequencerPublicKey: desc.SequencerPublicKey,
	}
	if retainEvents {
		log.Events = make([]Event, 0, stored.EventCount)
	}
	for index, position := range positions {
		remaining := desc.PayloadCeiling - uint64(len(position.Message))
		cached, err := readCompactCheckpointEvent(reader, remaining)
		if err != nil {
			return scannedLog{}, fmt.Errorf("checkpoint event %d: %w", index, err)
		}
		event, err := checkpointEventFromPayload(index, position, cached.Payload, cached.Attachments, stored, desc)
		if err != nil {
			return scannedLog{}, err
		}
		if err := appendCheckpointEventInto(&log, index, event, retainEvents); err != nil {
			return scannedLog{}, err
		}
	}
	if err := finishCompactCheckpointPayload(reader, source); err != nil {
		return scannedLog{}, err
	}
	return log, nil
}

func openCompactCheckpointPayload(payload []byte) (*gzip.Reader, *bytes.Reader, error) {
	source := bytes.NewReader(payload)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, nil, err
	}
	reader.Multistream(false)
	return reader, source, nil
}

func finishCompactCheckpointPayload(reader *gzip.Reader, source *bytes.Reader) error {
	var trailing [1]byte
	if _, err := reader.Read(trailing[:]); !errors.Is(err, io.EOF) {
		return errors.New("checkpoint payload has trailing decoded bytes")
	}
	if err := reader.Close(); err != nil {
		return err
	}
	if source.Len() != 0 {
		return errors.New("checkpoint payload has trailing compressed bytes")
	}
	return nil
}

func readCompactCheckpointEvent(reader io.Reader, ceiling uint64) (checkpointEvent, error) {
	payloadSize, err := readCheckpointUint64(reader)
	if err != nil {
		return checkpointEvent{}, err
	}
	if payloadSize > ceiling || payloadSize > uint64(maxIntValue()) {
		return checkpointEvent{}, errors.New("payload exceeds genesis ceiling")
	}
	payload := make([]byte, int(payloadSize))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return checkpointEvent{}, err
	}
	attachmentCount, err := readCheckpointUint32(reader)
	if err != nil {
		return checkpointEvent{}, err
	}
	if attachmentCount > 1<<20 {
		return checkpointEvent{}, errors.New("checkpoint attachment count exceeds limit")
	}
	var attachments map[string][]byte
	if attachmentCount > 0 {
		attachments = make(map[string][]byte, int(attachmentCount))
	}
	total := payloadSize
	for index := uint32(0); index < attachmentCount; index++ {
		nameSize, err := readCheckpointUint16(reader)
		if err != nil {
			return checkpointEvent{}, err
		}
		if nameSize == 0 || nameSize > 128 {
			return checkpointEvent{}, errors.New("invalid checkpoint attachment name length")
		}
		nameBytes := make([]byte, int(nameSize))
		if _, err := io.ReadFull(reader, nameBytes); err != nil {
			return checkpointEvent{}, err
		}
		name := string(nameBytes)
		if _, duplicate := attachments[name]; duplicate {
			return checkpointEvent{}, errors.New("duplicate checkpoint attachment")
		}
		contentSize, err := readCheckpointUint64(reader)
		if err != nil {
			return checkpointEvent{}, err
		}
		if total > ceiling || contentSize > ceiling-total || contentSize > uint64(maxIntValue()) {
			return checkpointEvent{}, errors.New("payload exceeds genesis ceiling")
		}
		content := make([]byte, int(contentSize))
		if _, err := io.ReadFull(reader, content); err != nil {
			return checkpointEvent{}, err
		}
		attachments[name] = content
		total += contentSize
	}
	return checkpointEvent{Payload: payload, Attachments: attachments}, nil
}

func readCheckpointUint64(reader io.Reader) (uint64, error) {
	var encoded [8]byte
	if _, err := io.ReadFull(reader, encoded[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(encoded[:]), nil
}

func readCheckpointUint32(reader io.Reader) (uint32, error) {
	var encoded [4]byte
	if _, err := io.ReadFull(reader, encoded[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(encoded[:]), nil
}

func readCheckpointUint16(reader io.Reader) (uint16, error) {
	var encoded [2]byte
	if _, err := io.ReadFull(reader, encoded[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(encoded[:]), nil
}

func maxIntValue() int { return int(^uint(0) >> 1) }

func validateObjectID(format, oid string) error {
	want := 40
	if format == "sha256" {
		want = 64
	}
	if len(oid) != want {
		return errors.New("wrong object id length")
	}
	if _, err := hex.DecodeString(oid); err != nil {
		return errors.New("object id is not hexadecimal")
	}
	return nil
}

func payloadWithinCeiling(payload []byte, attachments map[string][]byte, ceiling uint64) error {
	total := uint64(len(payload))
	if total > ceiling {
		return errors.New("payload exceeds genesis ceiling")
	}
	for _, content := range attachments {
		size := uint64(len(content))
		if size > ceiling-total {
			return errors.New("payload exceeds genesis ceiling")
		}
		total += size
	}
	return nil
}

func cloneByteMap(input map[string][]byte) map[string][]byte {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string][]byte, len(input))
	for name, content := range input {
		output[name] = bytes.Clone(content)
	}
	return output
}

func checkpointMaterial(event Event) checkpointEvent {
	return checkpointEvent{
		Payload: bytes.Clone(event.Payload), Attachments: cloneByteMap(event.Attachments),
	}
}

func checkpointDue(depth, lastAttempt int) bool {
	return depth-lastAttempt >= checkpointInterval
}
