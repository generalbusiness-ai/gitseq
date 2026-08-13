package kernel

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

const (
	checkpointSchema   = "gitseq-checkpoint@1"
	checkpointFile     = "checkpoint"
	checkpointMarker   = "gitseq-checkpoint-v1"
	checkpointInterval = 256
	maxCheckpointBytes = 256 << 20
)

var ErrNoUsableCheckpoint = errors.New("no usable checkpoint")

// CheckpointOptions enables the optional Git-backed restart cache. Profile is
// the application fold contract; changing it makes old derived state
// ineligible. SigningKey is optional: readers without sequencer custody may
// consume a valid checkpoint but never publish one.
type CheckpointOptions struct {
	Profile    string
	SigningKey string
	// Pointer is an optional host-owned, process-independent selector for a
	// signed Git checkpoint object. The kernel assigns it no authority: every
	// selected object still passes the complete checkpoint verification path.
	Pointer CheckpointPointer
}

// CheckpointPointer is the host seam for local checkpoint selection. It keeps
// filesystem policy outside the kernel; implementations store only an opaque
// object ID and must not treat it as verified.
type CheckpointPointer interface {
	Load() (string, error)
	Store(string) error
}

type checkpoint struct {
	Schema       string            `json:"schema"`
	ObjectFormat string            `json:"object_format"`
	Genesis      string            `json:"genesis"`
	Head         string            `json:"head"`
	Depth        int               `json:"depth"`
	Profile      string            `json:"profile"`
	Events       []checkpointEvent `json:"events"`
}

type checkpointEvent struct {
	Commit      string            `json:"commit"`
	Timestamp   int64             `json:"timestamp"`
	Signed      intent.Signed     `json:"signed"`
	Payload     []byte            `json:"payload"`
	Attachments map[string][]byte `json:"attachments,omitempty"`
}

func CheckpointRef(genesis string) string { return "refs/gitseq/checkpoints/" + genesis }

func loadCheckpoint(ctx context.Context, store gitstore.Store, genesis, head string, options CheckpointOptions) (scannedLog, bool, error) {
	if options.Profile == "" {
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
	type candidate struct {
		commit string
		stored checkpoint
	}
	parsed := make([]candidate, 0, len(candidates))
	var lastErr error
	for _, commit := range candidates {
		stored, err := readCheckpointCandidate(ctx, store, desc, format, genesis, commit, options.Profile)
		if err != nil {
			lastErr = err
			continue
		}
		parsed = append(parsed, candidate{commit: commit, stored: stored})
	}
	sort.SliceStable(parsed, func(i, j int) bool { return parsed[i].stored.Depth > parsed[j].stored.Depth })
	for _, candidate := range parsed {
		log, err := authenticateCheckpointCandidate(ctx, store, candidate.commit, candidate.stored, desc, commits)
		if err != nil {
			lastErr = err
			continue
		}
		advanced := candidate.stored.Head != head
		if advanced {
			prefix := append([]Event(nil), log.Events...)
			extended, err := scanListedAfter(ctx, store, log, head, commits[candidate.stored.Depth+1:], true)
			if err != nil {
				lastErr = fmt.Errorf("%w: checkpoint frontier: %v", ErrNoUsableCheckpoint, err)
				continue
			}
			log = extended
			log.Events = append(prefix, extended.Events...)
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

func readCheckpointCandidate(ctx context.Context, store gitstore.Store, desc GenesisDescriptor, format, genesis, commit, profile string) (checkpoint, error) {
	// A local pointer is mutable input. Validate it before it reaches any Git
	// command so it can select only an object ID, never a command-line option.
	if err := validateObjectID(format, commit); err != nil {
		return checkpoint{}, fmt.Errorf("%w: checkpoint commit: %v", ErrNoUsableCheckpoint, err)
	}
	parents, err := store.CommitParents(ctx, commit)
	if err != nil || len(parents) != 0 {
		return checkpoint{}, fmt.Errorf("%w: checkpoint commit must be parentless", ErrNoUsableCheckpoint)
	}
	message, err := store.CommitMessage(ctx, commit)
	if err != nil || strings.TrimSpace(message) != checkpointMarker {
		return checkpoint{}, fmt.Errorf("%w: checkpoint marker", ErrNoUsableCheckpoint)
	}
	files, err := store.ListFiles(ctx, commit, "")
	if err != nil || len(files) != 1 || files[0] != checkpointFile {
		return checkpoint{}, fmt.Errorf("%w: checkpoint tree shape", ErrNoUsableCheckpoint)
	}
	data, err := store.ReadFileLimit(ctx, commit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		return checkpoint{}, fmt.Errorf("%w: checkpoint payload: %v", ErrNoUsableCheckpoint, err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		return checkpoint{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	if stored.Schema != checkpointSchema || stored.ObjectFormat != format || stored.ObjectFormat != desc.ObjectFormat || stored.Genesis != genesis || stored.Profile != profile {
		return checkpoint{}, fmt.Errorf("%w: identity or profile mismatch", ErrNoUsableCheckpoint)
	}
	return stored, nil
}

func authenticateCheckpointCandidate(ctx context.Context, store gitstore.Store, commit string, stored checkpoint, desc GenesisDescriptor, commits []gitstore.CommitMetadata) (scannedLog, error) {
	log, err := validateCheckpoint(stored, desc, commits)
	if err != nil {
		return scannedLog{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	sequencerPublicKey, err := verifyCheckpointRotations(ctx, store, stored, desc, commits)
	if err != nil {
		return scannedLog{}, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", sequencerPublicKey); err != nil {
		return scannedLog{}, fmt.Errorf("%w: signature: %v", ErrNoUsableCheckpoint, err)
	}
	log.sequencerPublicKey = sequencerPublicKey
	return log, nil
}

func validateCheckpoint(stored checkpoint, desc GenesisDescriptor, sequence []gitstore.CommitMetadata) (scannedLog, error) {
	if stored.Depth < 0 || stored.Depth < len(stored.Events) {
		return scannedLog{}, errors.New("checkpoint depth is smaller than event count")
	}
	if len(sequence) < stored.Depth+1 || sequence[0].OID != stored.Genesis {
		return scannedLog{}, errors.New("checkpoint does not begin the named sequence")
	}
	if err := validateChainParents(0, sequence[0].Parents, ""); err != nil {
		return scannedLog{}, errors.New("checkpoint genesis has a parent")
	}
	for index := 1; index <= stored.Depth; index++ {
		if err := validateChainParents(index, sequence[index].Parents, sequence[index-1].OID); err != nil {
			return scannedLog{}, fmt.Errorf("checkpoint event %d is not single-parent chained", index-1)
		}
	}
	if stored.Depth == 0 {
		if stored.Head != stored.Genesis {
			return scannedLog{}, errors.New("empty checkpoint head is not genesis")
		}
	}
	if sequence[stored.Depth].OID != stored.Head {
		return scannedLog{}, errors.New("checkpoint head is not the claimed sequence frontier")
	}
	eventPositions := make([]gitstore.CommitMetadata, 0, len(stored.Events))
	for index := 1; index <= stored.Depth; index++ {
		position := sequence[index]
		if uint64(len(position.Message)) > desc.PayloadCeiling {
			return scannedLog{}, fmt.Errorf("commit %s envelope exceeds genesis ceiling", position.OID)
		}
		_, rotation, err := parseRotationMessage(position.Message)
		if err != nil {
			return scannedLog{}, fmt.Errorf("commit %s: %w", position.OID, err)
		}
		if !rotation {
			eventPositions = append(eventPositions, position)
		}
	}
	if len(eventPositions) != len(stored.Events) {
		return scannedLog{}, errors.New("checkpoint event count does not match sequence prefix")
	}
	log := scannedLog{
		Verification:       Verification{Genesis: stored.Genesis, Head: stored.Head, Depth: stored.Depth, Events: len(stored.Events)},
		Events:             make([]Event, 0, len(stored.Events)),
		Dedup:              make(map[string]Event, len(stored.Events)),
		sequencerPublicKey: desc.SequencerPublicKey,
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
		decoded, targetMatches, err := verifySignedTarget(cached.Signed, "git:"+stored.ObjectFormat+":"+stored.Genesis)
		if err != nil {
			return scannedLog{}, fmt.Errorf("event %d actor signature: %w", index, err)
		}
		if !targetMatches {
			return scannedLog{}, fmt.Errorf("event %d target does not name checkpoint genesis", index)
		}
		actualSigned, trailers, err := intent.ParseEnvelope(position.Message, desc.PayloadCeiling)
		if err != nil {
			return scannedLog{}, fmt.Errorf("event %d commit envelope: %w", index, err)
		}
		if !actualSigned.Equal(cached.Signed) {
			return scannedLog{}, fmt.Errorf("event %d cached intent differs from sequence commit", index)
		}
		if !intent.EqualRefs(decoded.RestsOn, trailers) {
			return scannedLog{}, fmt.Errorf("event %d causal trailers differ from signed intent", index)
		}
		format, tree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
		if err != nil || format != stored.ObjectFormat {
			return scannedLog{}, fmt.Errorf("event %d payload tree identity", index)
		}
		if position.Tree != tree {
			return scannedLog{}, fmt.Errorf("event %d commit tree differs from signed intent", index)
		}
		remaining := desc.PayloadCeiling - uint64(len(position.Message))
		if err := payloadWithinCeiling(cached.Payload, cached.Attachments, remaining); err != nil {
			return scannedLog{}, fmt.Errorf("event %d: %w", index, err)
		}
		calculated, err := gitstore.HashPayloadTree(stored.ObjectFormat, cached.Payload, cached.Attachments)
		if err != nil || calculated != tree {
			return scannedLog{}, fmt.Errorf("event %d payload differs from actor-signed tree", index)
		}
		event := Event{
			Commit: cached.Commit, Timestamp: cached.Timestamp, Intent: decoded, Signed: cloneSigned(cached.Signed), PayloadTree: decoded.PayloadTree,
			Payload: bytes.Clone(cached.Payload), Attachments: cloneByteMap(cached.Attachments),
		}
		key, err := event.Signed.DedupKey()
		if err != nil {
			return scannedLog{}, err
		}
		prior, duplicate, dedupErr := dedupPrior(log.Dedup, key, event.Signed)
		if dedupErr != nil {
			return scannedLog{}, fmt.Errorf("event %d: %w", index, dedupErr)
		}
		if duplicate {
			return scannedLog{}, fmt.Errorf("event %d duplicates idempotent event %s", index, prior.Commit)
		}
		log.Dedup[key] = eventWithoutPayload(event)
		log.Events = append(log.Events, event)
	}
	return log, nil
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
	if options.Profile == "" || options.SigningKey == "" || len(log.Events) != log.Verification.Events {
		return ErrNoUsableCheckpoint
	}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return err
	}
	stored := checkpoint{
		Schema: checkpointSchema, ObjectFormat: format, Genesis: log.Verification.Genesis,
		Head: log.Verification.Head, Depth: log.Verification.Depth, Profile: options.Profile,
		Events: make([]checkpointEvent, 0, len(log.Events)),
	}
	for _, event := range log.Events {
		stored.Events = append(stored.Events, checkpointEvent{
			Commit: event.Commit, Timestamp: event.Timestamp, Signed: cloneSigned(event.Signed), Payload: bytes.Clone(event.Payload), Attachments: cloneByteMap(event.Attachments),
		})
	}
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
	data, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("checkpoint size %d exceeds limit %d", len(data), limit)
	}
	return data, nil
}

func decodeCheckpoint(data []byte) (checkpoint, error) {
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

func cloneEvent(event Event) Event {
	event.Intent.RestsOn = append([]string(nil), event.Intent.RestsOn...)
	event.Intent.CapabilityHash = bytes.Clone(event.Intent.CapabilityHash)
	event.Signed = cloneSigned(event.Signed)
	event.Payload = bytes.Clone(event.Payload)
	event.Attachments = cloneByteMap(event.Attachments)
	return event
}

func cloneEvents(events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]Event, len(events))
	for index, event := range events {
		cloned[index] = cloneEvent(event)
	}
	return cloned
}

func checkpointDue(depth, lastAttempt int) bool {
	return depth-lastAttempt >= checkpointInterval
}
