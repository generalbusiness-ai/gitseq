package kernel

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
)

const (
	checkpointSchema   = "gitseq-checkpoint@0"
	checkpointFile     = "checkpoint"
	checkpointMarker   = "gitseq-checkpoint-v0"
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
	Signed      intent.Signed     `json:"signed"`
	Payload     []byte            `json:"payload"`
	Attachments map[string][]byte `json:"attachments,omitempty"`
}

func CheckpointRef(genesis string) string { return "refs/gitseq/checkpoints/" + genesis }

func loadCheckpoint(ctx context.Context, store gitstore.Store, genesis, head string, options CheckpointOptions) (scannedLog, bool, error) {
	if options.Profile == "" {
		return scannedLog{}, false, ErrNoUsableCheckpoint
	}
	commit, err := store.Head(ctx, CheckpointRef(genesis))
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint ref: %v", ErrNoUsableCheckpoint, err)
	}
	desc, err := Descriptor(ctx, store, genesis)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: descriptor: %v", ErrNoUsableCheckpoint, err)
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", desc.SequencerPublicKey); err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: signature: %v", ErrNoUsableCheckpoint, err)
	}
	parents, err := store.CommitParents(ctx, commit)
	if err != nil || len(parents) != 0 {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint commit must be parentless", ErrNoUsableCheckpoint)
	}
	message, err := store.CommitMessage(ctx, commit)
	if err != nil || strings.TrimSpace(message) != checkpointMarker {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint marker", ErrNoUsableCheckpoint)
	}
	files, err := store.ListFiles(ctx, commit, "")
	if err != nil || len(files) != 1 || files[0] != checkpointFile {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint tree shape", ErrNoUsableCheckpoint)
	}
	data, err := store.ReadFileLimit(ctx, commit, checkpointFile, maxCheckpointBytes)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: checkpoint payload: %v", ErrNoUsableCheckpoint, err)
	}
	stored, err := decodeCheckpoint(data)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return scannedLog{}, false, err
	}
	if stored.Schema != checkpointSchema || stored.ObjectFormat != format || stored.ObjectFormat != desc.ObjectFormat || stored.Genesis != genesis || stored.Profile != options.Profile {
		return scannedLog{}, false, fmt.Errorf("%w: identity or profile mismatch", ErrNoUsableCheckpoint)
	}
	log, err := validateCheckpoint(stored, desc)
	if err != nil {
		return scannedLog{}, false, fmt.Errorf("%w: %v", ErrNoUsableCheckpoint, err)
	}
	prefix := append([]Event(nil), log.Events...)
	advanced := stored.Head != head
	if advanced {
		extended, err := scanAfter(ctx, store, log, head, true)
		if err != nil {
			return scannedLog{}, false, fmt.Errorf("%w: checkpoint frontier: %v", ErrNoUsableCheckpoint, err)
		}
		log = extended
		log.Events = append(prefix, extended.Events...)
	}
	return log, advanced, nil
}

func validateCheckpoint(stored checkpoint, desc GenesisDescriptor) (scannedLog, error) {
	if stored.Depth < 0 || stored.Depth != len(stored.Events) {
		return scannedLog{}, errors.New("checkpoint depth does not match event count")
	}
	if stored.Depth == 0 {
		if stored.Head != stored.Genesis {
			return scannedLog{}, errors.New("empty checkpoint head is not genesis")
		}
	} else if stored.Events[len(stored.Events)-1].Commit != stored.Head {
		return scannedLog{}, errors.New("checkpoint head does not match final event")
	}
	log := scannedLog{
		Verification: Verification{Genesis: stored.Genesis, Head: stored.Head, Depth: stored.Depth, Events: stored.Depth},
		Events:       make([]Event, 0, stored.Depth),
		Dedup:        make(map[string]Event, stored.Depth),
	}
	commits := make(map[string]struct{}, stored.Depth)
	for index, cached := range stored.Events {
		if err := validateObjectID(stored.ObjectFormat, cached.Commit); err != nil {
			return scannedLog{}, fmt.Errorf("event %d commit: %w", index, err)
		}
		if _, exists := commits[cached.Commit]; exists {
			return scannedLog{}, fmt.Errorf("event %d repeats commit %s", index, cached.Commit)
		}
		commits[cached.Commit] = struct{}{}
		decoded, err := intent.Verify(cached.Signed)
		if err != nil {
			return scannedLog{}, fmt.Errorf("event %d actor signature: %w", index, err)
		}
		if decoded.Target != "git:"+stored.ObjectFormat+":"+stored.Genesis {
			return scannedLog{}, fmt.Errorf("event %d target does not name checkpoint genesis", index)
		}
		format, tree, err := gitstore.ParseTypedOID(decoded.PayloadTree)
		if err != nil || format != stored.ObjectFormat {
			return scannedLog{}, fmt.Errorf("event %d payload tree identity", index)
		}
		if err := payloadWithinCeiling(cached.Payload, cached.Attachments, desc.PayloadCeiling); err != nil {
			return scannedLog{}, fmt.Errorf("event %d: %w", index, err)
		}
		calculated, err := gitstore.HashPayloadTree(stored.ObjectFormat, cached.Payload, cached.Attachments)
		if err != nil || calculated != tree {
			return scannedLog{}, fmt.Errorf("event %d payload differs from actor-signed tree", index)
		}
		event := Event{
			Commit: cached.Commit, Intent: decoded, Signed: cloneSigned(cached.Signed), PayloadTree: decoded.PayloadTree,
			Payload: bytes.Clone(cached.Payload), Attachments: cloneByteMap(cached.Attachments),
		}
		key, err := event.Signed.DedupKey()
		if err != nil {
			return scannedLog{}, err
		}
		if prior, exists := log.Dedup[key]; exists {
			if !prior.Signed.Equal(event.Signed) {
				return scannedLog{}, fmt.Errorf("event %d: %w", index, ErrIdempotencyConflict)
			}
			return scannedLog{}, fmt.Errorf("event %d duplicates idempotent event %s", index, prior.Commit)
		}
		log.Dedup[key] = eventWithoutPayload(event)
		log.Events = append(log.Events, event)
	}
	return log, nil
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
			Commit: event.Commit, Signed: cloneSigned(event.Signed), Payload: bytes.Clone(event.Payload), Attachments: cloneByteMap(event.Attachments),
		})
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if len(data) > maxCheckpointBytes {
		return fmt.Errorf("checkpoint size %d exceeds limit %d", len(data), maxCheckpointBytes)
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
	ref := CheckpointRef(log.Verification.Genesis)
	old, _ := store.Head(ctx, ref)
	return store.UpdateRef(ctx, ref, commit, old)
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
