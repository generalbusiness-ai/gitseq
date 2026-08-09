package kernel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/fxamacker/cbor/v2"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
)

const (
	genesisMarker  = "gitseq-genesis-v0"
	rotationMarker = "gitseq-rotation-v0"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key reused with different intent")
	ErrNotDescendant       = errors.New("head is not a descendant of verified frontier")
)

type GenesisDescriptor struct {
	_                  struct{} `cbor:",toarray"`
	Version            uint64
	ObjectFormat       string
	PayloadCeiling     uint64
	SequencerPublicKey string
	PredecessorGenesis string
	SealedHead         string
}

type rotationDescriptor struct {
	_                  struct{} `cbor:",toarray"`
	Version            uint64
	SequencerPublicKey string
}

type Request struct {
	Signed          intent.Signed     `json:"signed"`
	Payload         []byte            `json:"payload"`
	Attachments     map[string][]byte `json:"attachments,omitempty"`
	CapabilityProof []byte            `json:"capability_proof,omitempty"`
}

type Result struct {
	Commit     string `json:"commit"`
	Head       string `json:"head"`
	Replay     bool   `json:"replay"`
	CASRetries int    `json:"cas_retries"`
	BaseHead   string `json:"-"`
	Timestamp  int64  `json:"timestamp"`
}

type Options struct {
	SigningKey        string
	CheckpointProfile string
	Failpoint         func(string)
	MaxRetries        int
	PreAppend         func(context.Context, Admission) error
}

// Submitter is the resident sequencing path. It retains only a log state that
// scanHead has fully verified, and reuses it only while the target ref still
// names that exact head. A cold or non-descendant submit pays for a full scan;
// descendant external advances verify only their delta, and successive local
// appends update the verified dedup state after their compare-and-swap succeeds.
//
// Submit remains the stateless failover path. Keeping the cache here, rather
// than in an HTTP or application adapter, makes the trust boundary identical
// for every resident caller.
type Submitter struct {
	store   gitstore.Store
	options Options

	mu    sync.Mutex
	cache submitCache
}

type submitCache struct {
	target              string
	head                string
	log                 scannedLog
	fullScans           int
	deltaScans          int
	cacheHits           int
	checkpointLoads     int
	checkpointFallbacks int
	checkpointWrites    int
	checkpointFailures  int
	checkpointEvents    []Event
	checkpointAttempt   int
}

func NewSubmitter(store gitstore.Store, options Options) *Submitter {
	return &Submitter{store: store, options: options}
}

func (s *Submitter) Submit(ctx context.Context, request Request) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return submit(ctx, s.store, request, s.options, &s.cache)
}

// Admission deliberately has no payload field. A profile hook can inspect the
// signed envelope and presented capability material, but not application data.
type Admission struct {
	Intent          intent.Intent
	ActorKey        []byte
	CapabilityProof []byte
}

type Event struct {
	Commit      string
	Timestamp   int64
	Intent      intent.Intent
	Signed      intent.Signed
	PayloadTree string
	Payload     []byte
	Attachments map[string][]byte
}

func deterministicModes() (cbor.EncMode, cbor.DecMode) {
	enc, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	dec, err := (cbor.DecOptions{IndefLength: cbor.IndefLengthForbidden, TagsMd: cbor.TagsForbidden, MaxArrayElements: 32}).DecMode()
	if err != nil {
		panic(err)
	}
	return enc, dec
}

func encodeGenesis(desc GenesisDescriptor) ([]byte, error) {
	if err := validateGenesisDescriptor(desc); err != nil {
		return nil, err
	}
	enc, _ := deterministicModes()
	return enc.Marshal(desc)
}

func decodeGenesis(data []byte) (GenesisDescriptor, error) {
	enc, dec := deterministicModes()
	var desc GenesisDescriptor
	if err := dec.Unmarshal(data, &desc); err != nil {
		return GenesisDescriptor{}, err
	}
	reencoded, err := enc.Marshal(desc)
	if err != nil || !bytes.Equal(reencoded, data) {
		return GenesisDescriptor{}, errors.New("non-deterministic genesis descriptor")
	}
	if err := validateGenesisDescriptor(desc); err != nil {
		return GenesisDescriptor{}, err
	}
	return desc, nil
}

func validateGenesisDescriptor(desc GenesisDescriptor) error {
	if desc.Version != 0 || (desc.ObjectFormat != "sha1" && desc.ObjectFormat != "sha256") || desc.PayloadCeiling == 0 {
		return errors.New("invalid genesis descriptor")
	}
	if err := gitstore.ValidateSSHPublicKey(desc.SequencerPublicKey); err != nil {
		return fmt.Errorf("invalid genesis sequencer key: %w", err)
	}
	if (desc.PredecessorGenesis == "") != (desc.SealedHead == "") {
		return errors.New("continuation requires both predecessor genesis and sealed head")
	}
	return nil
}

func genesisMessage(desc GenesisDescriptor) (string, error) {
	encoded, err := encodeGenesis(desc)
	if err != nil {
		return "", err
	}
	return genesisMarker + "\nDescriptor: " + base64.RawURLEncoding.EncodeToString(encoded) + "\n", nil
}

func parseGenesisMessage(message string) (GenesisDescriptor, error) {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	if len(lines) != 2 || lines[0] != genesisMarker || !strings.HasPrefix(lines[1], "Descriptor: ") {
		return GenesisDescriptor{}, errors.New("malformed genesis envelope")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(lines[1], "Descriptor: "))
	if err != nil {
		return GenesisDescriptor{}, err
	}
	return decodeGenesis(encoded)
}

func rotationMessage(publicKey string) (string, error) {
	if err := gitstore.ValidateSSHPublicKey(publicKey); err != nil {
		return "", fmt.Errorf("invalid successor sequencer key: %w", err)
	}
	enc, _ := deterministicModes()
	encoded, err := enc.Marshal(rotationDescriptor{Version: 0, SequencerPublicKey: publicKey})
	if err != nil {
		return "", err
	}
	return rotationMarker + "\nDescriptor: " + base64.RawURLEncoding.EncodeToString(encoded) + "\n", nil
}

func parseRotationMessage(message string) (string, bool, error) {
	lines := strings.Split(strings.TrimSpace(message), "\n")
	if len(lines) == 0 || lines[0] != rotationMarker {
		return "", false, nil
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "Descriptor: ") {
		return "", true, errors.New("malformed rotation envelope")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(lines[1], "Descriptor: "))
	if err != nil {
		return "", true, fmt.Errorf("decode rotation descriptor: %w", err)
	}
	enc, dec := deterministicModes()
	var desc rotationDescriptor
	if err := dec.Unmarshal(encoded, &desc); err != nil {
		return "", true, fmt.Errorf("decode rotation descriptor: %w", err)
	}
	reencoded, err := enc.Marshal(desc)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return "", true, errors.New("non-deterministic rotation descriptor")
	}
	if desc.Version != 0 {
		return "", true, errors.New("unsupported rotation version")
	}
	if err := gitstore.ValidateSSHPublicKey(desc.SequencerPublicKey); err != nil {
		return "", true, fmt.Errorf("invalid successor sequencer key: %w", err)
	}
	return desc.SequencerPublicKey, true, nil
}

func Ref(genesis string) string { return "refs/seq/" + genesis }

func Descriptor(ctx context.Context, store gitstore.Store, genesis string) (GenesisDescriptor, error) {
	message, err := store.CommitMessage(ctx, genesis)
	if err != nil {
		return GenesisDescriptor{}, err
	}
	return parseGenesisMessage(message)
}

func Create(ctx context.Context, store gitstore.Store, desc GenesisDescriptor, signingKey string) (string, error) {
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return "", err
	}
	if format != desc.ObjectFormat {
		return "", fmt.Errorf("descriptor object format %s != repository %s", desc.ObjectFormat, format)
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		return "", err
	}
	message, err := genesisMessage(desc)
	if err != nil {
		return "", err
	}
	commit, err := store.SignedCommit(ctx, tree, "", message, signingKey, gitstore.CommitIdentity{
		AuthorName: "gitseq genesis", AuthorEmail: "genesis@gitseq.invalid",
		CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
	})
	if err != nil {
		return "", err
	}
	if err := store.VerifySSHCommit(ctx, commit, "sequencer", desc.SequencerPublicKey); err != nil {
		return "", fmt.Errorf("genesis sequencer signature: %w", err)
	}
	if err := store.UpdateRef(ctx, Ref(commit), commit, ""); err != nil {
		return "", err
	}
	return commit, nil
}

func capabilityMatches(hash, proof []byte) bool {
	if len(hash) == 0 {
		return len(proof) == 0
	}
	digest := sha256.Sum256(proof)
	return bytes.Equal(hash, digest[:])
}

func fail(options Options, name string) {
	if options.Failpoint != nil {
		options.Failpoint(name)
	}
}

func Submit(ctx context.Context, store gitstore.Store, request Request, options Options) (Result, error) {
	return submit(ctx, store, request, options, nil)
}

// Rotate appends the kernel's reserved key-rotation event. The rotation commit
// is signed by the current sequencer key and names the only key accepted for
// later commits. The private key named by options remains the current key;
// callers switch custody to the successor only after this append succeeds.
func Rotate(ctx context.Context, store gitstore.Store, genesis, successorPublicKey string, options Options) (Result, error) {
	message, err := rotationMessage(successorPublicKey)
	if err != nil {
		return Result{}, err
	}
	desc, err := Descriptor(ctx, store, genesis)
	if err != nil {
		return Result{}, err
	}
	if uint64(len(message)) > desc.PayloadCeiling {
		return Result{}, errors.New("rotation envelope exceeds genesis ceiling")
	}
	tree, err := store.EmptyTree(ctx)
	if err != nil {
		return Result{}, err
	}
	maxRetries := options.MaxRetries
	if maxRetries == 0 {
		maxRetries = 32
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		head, err := store.Head(ctx, Ref(genesis))
		if err != nil {
			return Result{}, err
		}
		log, err := scanHead(ctx, store, genesis, head, false)
		if err != nil {
			return Result{}, err
		}
		if log.sequencerPublicKey == successorPublicKey {
			return Result{}, errors.New("successor is already the current sequencer key")
		}
		commit, timestamp, err := store.SignedCommitWithTimestamp(ctx, tree, head, message, options.SigningKey, gitstore.CommitIdentity{
			AuthorName: "gitseq rotation", AuthorEmail: "rotation@gitseq.invalid",
			CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
		})
		if err != nil {
			return Result{}, err
		}
		if err := store.VerifySSHCommit(ctx, commit, "sequencer", log.sequencerPublicKey); err != nil {
			return Result{}, fmt.Errorf("rotation %s sequencer signature: %w", commit, err)
		}
		if err := store.UpdateRef(ctx, Ref(genesis), commit, head); err != nil {
			current, headErr := store.Head(ctx, Ref(genesis))
			if headErr == nil && current != head {
				continue
			}
			return Result{}, err
		}
		return Result{Commit: commit, Head: commit, CASRetries: attempt, BaseHead: head, Timestamp: timestamp}, nil
	}
	return Result{}, errors.New("CAS retry limit exceeded")
}

func submit(ctx context.Context, store gitstore.Store, request Request, options Options, cache *submitCache) (Result, error) {
	decoded, err := intent.Verify(request.Signed)
	if err != nil {
		return Result{}, err
	}
	if !capabilityMatches(decoded.CapabilityHash, request.CapabilityProof) {
		return Result{}, errors.New("capability proof does not match signed hash")
	}
	storeFormat, err := store.ObjectFormat(ctx)
	if err != nil {
		return Result{}, err
	}
	targetFormat, targetOID, err := gitstore.ParseTypedOID(decoded.Target)
	if err != nil {
		return Result{}, err
	}
	if targetFormat != storeFormat {
		return Result{}, errors.New("target object format differs from repository")
	}
	ref := Ref(targetOID)
	if _, err := store.Head(ctx, ref); err != nil {
		return Result{}, errors.New("unknown target log")
	}
	genesisMessage, err := store.CommitMessage(ctx, targetOID)
	if err != nil {
		return Result{}, err
	}
	desc, err := parseGenesisMessage(genesisMessage)
	if err != nil {
		return Result{}, err
	}
	message := intent.Envelope(request.Signed, decoded.RestsOn)
	eventSize := uint64(len(message))
	if eventSize > desc.PayloadCeiling || uint64(len(request.Payload)) > desc.PayloadCeiling-eventSize {
		return Result{}, errors.New("event exceeds genesis ceiling")
	}
	eventSize += uint64(len(request.Payload))
	for _, attachment := range request.Attachments {
		size := uint64(len(attachment))
		if eventSize > desc.PayloadCeiling || size > desc.PayloadCeiling-eventSize {
			return Result{}, errors.New("event exceeds genesis ceiling")
		}
		eventSize += size
	}
	if eventSize > desc.PayloadCeiling {
		return Result{}, errors.New("event exceeds genesis ceiling")
	}
	if options.PreAppend != nil {
		admission := Admission{Intent: decoded, ActorKey: bytes.Clone(request.Signed.ActorKey), CapabilityProof: bytes.Clone(request.CapabilityProof)}
		if err := options.PreAppend(ctx, admission); err != nil {
			return Result{}, fmt.Errorf("pre-append hook refused: %w", err)
		}
	}
	format, treeOID, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil {
		return Result{}, err
	}
	if format != storeFormat {
		return Result{}, errors.New("payload object format differs from repository")
	}
	writtenTree, err := store.WritePayloadTree(ctx, request.Payload, request.Attachments)
	if err != nil {
		return Result{}, err
	}
	fail(options, "after_objects_written")
	if writtenTree != treeOID {
		return Result{}, fmt.Errorf("payload tree mismatch: signed %s, reconstructed %s", treeOID, writtenTree)
	}
	maxRetries := options.MaxRetries
	if maxRetries == 0 {
		maxRetries = 32
	}
	key, err := request.Signed.DedupKey()
	if err != nil {
		return Result{}, err
	}
	checkpointEnabled := cache != nil && options.CheckpointProfile != ""
	checkpointWritable := checkpointEnabled && options.SigningKey != ""
	for attempt := 0; attempt < maxRetries; attempt++ {
		head, err := store.Head(ctx, ref)
		if err != nil {
			return Result{}, err
		}
		var log scannedLog
		if cache != nil && cache.target == targetOID && cache.head == head {
			log = cache.log
			cache.cacheHits++
		} else {
			fullScan := false
			checkpointReset := false
			checkpointCurrent := false
			checkpointOptions := CheckpointOptions{Profile: options.CheckpointProfile, SigningKey: options.SigningKey}
			if cache != nil && cache.target == targetOID && cache.head != "" {
				log, err = scanAfter(ctx, store, cache.log, head, checkpointEnabled)
				if err == nil {
					cache.deltaScans++
				} else if !errors.Is(err, ErrNotDescendant) {
					return Result{}, err
				}
			}
			if cache == nil || cache.target != targetOID || cache.head == "" || errors.Is(err, ErrNotDescendant) {
				checkpointReset = true
				loadedCheckpoint := false
				checkpointAdvanced := false
				if checkpointEnabled {
					log, checkpointAdvanced, err = loadCheckpoint(ctx, store, targetOID, head, checkpointOptions)
					if err == nil {
						loadedCheckpoint = true
						cache.checkpointLoads++
					} else {
						cache.checkpointFallbacks++
					}
				}
				if !loadedCheckpoint {
					loadPayload := checkpointEnabled
					log, err = scanHead(ctx, store, targetOID, head, loadPayload)
					fullScan = true
				}
				if err == nil && checkpointEnabled {
					checkpointCurrent = loadedCheckpoint && !checkpointAdvanced
					if !checkpointCurrent && checkpointWritable {
						if writeCheckpoint(ctx, store, log, checkpointOptions) == nil {
							cache.checkpointWrites++
							checkpointCurrent = true
						} else {
							cache.checkpointFailures++
						}
					}
				}
			}
			if err != nil {
				return Result{}, err
			}
			if cache != nil {
				if checkpointEnabled {
					if checkpointReset {
						cache.checkpointEvents = cloneEvents(log.Events)
						if checkpointCurrent {
							cache.checkpointAttempt = log.Verification.Depth
						} else {
							cache.checkpointAttempt = log.Verification.Depth - checkpointInterval
						}
					} else {
						cache.checkpointEvents = append(cache.checkpointEvents, cloneEvents(log.Events)...)
					}
				}
				// Submission needs the verified frontier and dedup projection, not
				// a second application-facing copy of the event stream. Checkpoint
				// events are retained separately only while checkpointing is enabled.
				log.Events = nil
				cache.target = targetOID
				cache.head = head
				cache.log = log
				if fullScan {
					cache.fullScans++
				}
			}
		}
		if prior, ok := log.Dedup[key]; ok {
			if !prior.Signed.Equal(request.Signed) {
				return Result{}, ErrIdempotencyConflict
			}
			return Result{Commit: prior.Commit, Head: prior.Commit, Replay: true, CASRetries: attempt, BaseHead: head, Timestamp: prior.Timestamp}, nil
		}
		actorID := intent.ActorFingerprint(request.Signed.ActorKey)
		commit, timestamp, err := store.SignedCommitWithTimestamp(ctx, writtenTree, head, message, options.SigningKey, gitstore.CommitIdentity{
			AuthorName: "actor " + actorID[:16], AuthorEmail: actorID[:16] + "@actor.gitseq.invalid",
			CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
		})
		if err != nil {
			return Result{}, err
		}
		fail(options, "after_commit_written")
		// Eager application is safe only if the commit is acceptable to the
		// chain's ordinary auditor. In particular, local custody may have been
		// rotated without changing the genesis descriptor; never advance the ref
		// with a commit signed by that unrelated key.
		if err := store.VerifySSHCommit(ctx, commit, "sequencer", log.sequencerPublicKey); err != nil {
			return Result{}, fmt.Errorf("commit %s sequencer signature: %w", commit, err)
		}
		fail(options, "before_ref_cas")
		if err := store.UpdateRef(ctx, ref, commit, head); err != nil {
			current, headErr := store.Head(ctx, ref)
			if headErr == nil && current != head {
				continue
			}
			return Result{}, err
		}
		if cache != nil {
			event := Event{
				Commit: commit, Timestamp: timestamp, Intent: decoded, Signed: cloneSigned(request.Signed), PayloadTree: decoded.PayloadTree,
				Payload: bytes.Clone(request.Payload), Attachments: cloneByteMap(request.Attachments),
			}
			cache.log.Dedup[key] = event
			cache.log.Verification.Head = commit
			cache.log.Verification.Depth++
			cache.log.Verification.Events++
			cache.head = commit
			if checkpointEnabled {
				cache.checkpointEvents = append(cache.checkpointEvents, cloneEvent(event))
				if checkpointWritable && checkpointDue(cache.log.Verification.Depth, cache.checkpointAttempt) {
					checkpointLog := cache.log
					checkpointLog.Events = cache.checkpointEvents
					if writeCheckpoint(ctx, store, checkpointLog, CheckpointOptions{Profile: options.CheckpointProfile, SigningKey: options.SigningKey}) == nil {
						cache.checkpointWrites++
						cache.checkpointAttempt = cache.log.Verification.Depth
					} else {
						cache.checkpointFailures++
					}
				}
			}
		}
		fail(options, "after_ref_cas")
		fail(options, "before_reply")
		return Result{Commit: commit, Head: commit, CASRetries: attempt, BaseHead: head, Timestamp: timestamp}, nil
	}
	return Result{}, errors.New("CAS retry limit exceeded")
}

func cloneSigned(signed intent.Signed) intent.Signed {
	return intent.Signed{
		Intent:    bytes.Clone(signed.Intent),
		ActorKey:  bytes.Clone(signed.ActorKey),
		Signature: bytes.Clone(signed.Signature),
	}
}

type Verification struct {
	Genesis string
	Head    string
	Depth   int
	Events  int
}

type scannedLog struct {
	Verification       Verification
	Events             []Event
	Dedup              map[string]Event
	sequencerPublicKey string
}

// LoadResult is a verified resident read. Full means Events contains the
// complete log. Otherwise Events contains only commits after BaseHead; an
// empty delta means the verified frontier was already current.
type LoadResult struct {
	Events       []Event
	Verification Verification
	BaseHead     string
	Full         bool
	Checkpoint   bool
}

// Reader retains a verified frontier and dedup index while leaving the full
// event stream to the application projection that consumes it. Descendant
// advances verify only their delta; cold and non-descendant reads remain full
// audits.
type Reader struct {
	store gitstore.Store

	mu                  sync.Mutex
	target              string
	head                string
	log                 scannedLog
	fullScans           int
	deltaScans          int
	cacheHits           int
	checkpoint          CheckpointOptions
	checkpointLoads     int
	checkpointFallbacks int
	checkpointWrites    int
	checkpointFailures  int
	checkpointEvents    []Event
	checkpointAttempt   int
}

func NewReader(store gitstore.Store, checkpoint ...CheckpointOptions) *Reader {
	reader := &Reader{store: store}
	if len(checkpoint) > 0 {
		reader.checkpoint = checkpoint[0]
	}
	return reader
}

func (r *Reader) Load(ctx context.Context, genesis string) (LoadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	head, err := r.store.Head(ctx, Ref(genesis))
	if err != nil {
		return LoadResult{}, err
	}
	if r.target == genesis && r.head == head {
		r.cacheHits++
		return LoadResult{Verification: r.log.Verification, BaseHead: head}, nil
	}
	if r.target == genesis && r.head != "" {
		base := r.head
		log, deltaErr := scanAfter(ctx, r.store, r.log, head, true)
		if deltaErr == nil {
			events := log.Events
			if r.checkpoint.Profile != "" && r.checkpoint.SigningKey != "" {
				r.checkpointEvents = append(r.checkpointEvents, cloneEvents(events)...)
				if checkpointDue(log.Verification.Depth, r.checkpointAttempt) {
					checkpointLog := log
					checkpointLog.Events = r.checkpointEvents
					if writeCheckpoint(ctx, r.store, checkpointLog, r.checkpoint) == nil {
						r.checkpointWrites++
						r.checkpointAttempt = log.Verification.Depth
					} else {
						r.checkpointFailures++
					}
				}
			}
			log.Events = nil
			r.head, r.log = head, log
			r.deltaScans++
			return LoadResult{Events: events, Verification: log.Verification, BaseHead: base}, nil
		}
		if !errors.Is(deltaErr, ErrNotDescendant) {
			return LoadResult{}, deltaErr
		}
	}
	var log scannedLog
	fromCheckpoint := false
	checkpointAdvanced := false
	if r.checkpoint.Profile != "" {
		log, checkpointAdvanced, err = loadCheckpoint(ctx, r.store, genesis, head, r.checkpoint)
		if err == nil {
			fromCheckpoint = true
			r.checkpointLoads++
		} else {
			r.checkpointFallbacks++
		}
	}
	if !fromCheckpoint {
		log, err = scanHead(ctx, r.store, genesis, head, true)
		if err != nil {
			return LoadResult{}, err
		}
		r.fullScans++
	}
	checkpointCurrent := fromCheckpoint && !checkpointAdvanced
	if r.checkpoint.Profile != "" && r.checkpoint.SigningKey != "" && !checkpointCurrent {
		if writeCheckpoint(ctx, r.store, log, r.checkpoint) == nil {
			r.checkpointWrites++
			checkpointCurrent = true
		} else {
			r.checkpointFailures++
		}
	}
	events := log.Events
	if r.checkpoint.Profile != "" && r.checkpoint.SigningKey != "" {
		r.checkpointEvents = cloneEvents(events)
		if checkpointCurrent {
			r.checkpointAttempt = log.Verification.Depth
		} else {
			r.checkpointAttempt = log.Verification.Depth - checkpointInterval
		}
	}
	log.Events = nil
	r.target, r.head, r.log = genesis, head, log
	return LoadResult{Events: events, Verification: log.Verification, Full: true, Checkpoint: fromCheckpoint}, nil
}

// Load verifies a log before returning its application records. Consumers do
// not get a convenient unverified read path by accident.
func Load(ctx context.Context, store gitstore.Store, genesis string) ([]Event, Verification, error) {
	head, err := store.Head(ctx, Ref(genesis))
	if err != nil {
		return nil, Verification{}, err
	}
	log, err := scanHead(ctx, store, genesis, head, true)
	if err != nil {
		return nil, Verification{}, err
	}
	return log.Events, log.Verification, nil
}

type ContinuationVerification struct {
	Predecessor Verification `json:"predecessor"`
	Successor   Verification `json:"successor"`
}

func VerifyContinuation(ctx context.Context, predecessorStore gitstore.Store, predecessorGenesis string, successorStore gitstore.Store, successorGenesis string) (ContinuationVerification, error) {
	predecessor, err := Verify(ctx, predecessorStore, predecessorGenesis)
	if err != nil {
		return ContinuationVerification{}, fmt.Errorf("predecessor: %w", err)
	}
	successor, err := Verify(ctx, successorStore, successorGenesis)
	if err != nil {
		return ContinuationVerification{}, fmt.Errorf("successor: %w", err)
	}
	descriptor, err := Descriptor(ctx, successorStore, successorGenesis)
	if err != nil {
		return ContinuationVerification{}, err
	}
	if descriptor.PredecessorGenesis != predecessor.Genesis || descriptor.SealedHead != predecessor.Head {
		return ContinuationVerification{}, errors.New("successor does not continue the verified predecessor frontier")
	}
	return ContinuationVerification{Predecessor: predecessor, Successor: successor}, nil
}

func Verify(ctx context.Context, store gitstore.Store, genesis string) (Verification, error) {
	head, err := store.Head(ctx, Ref(genesis))
	if err != nil {
		return Verification{}, err
	}
	log, err := scanHead(ctx, store, genesis, head, false)
	if err != nil {
		return Verification{}, err
	}
	return log.Verification, nil
}

// scanHead performs the explicit cold/non-descendant full audit. It verifies
// the immutable head and, in the same traversal, builds the event stream and
// actor-scoped dedup index. loadPayload controls only whether verified payload
// bytes are retained.
func scanHead(ctx context.Context, store gitstore.Store, genesis, head string, loadPayload bool) (scannedLog, error) {
	commits, err := store.RevList(ctx, head)
	if err != nil {
		return scannedLog{}, err
	}
	if len(commits) == 0 || commits[0] != genesis {
		return scannedLog{}, errors.New("chain does not begin at named genesis")
	}
	if commits[len(commits)-1] != head {
		return scannedLog{}, errors.New("history does not end at named head")
	}
	genesisMessage, err := store.CommitMessage(ctx, genesis)
	if err != nil {
		return scannedLog{}, err
	}
	desc, err := parseGenesisMessage(genesisMessage)
	if err != nil {
		return scannedLog{}, err
	}
	log := scannedLog{
		Verification:       Verification{Genesis: genesis, Head: head, Depth: len(commits) - 1},
		Events:             make([]Event, 0, len(commits)-1),
		Dedup:              make(map[string]Event, len(commits)-1),
		sequencerPublicKey: desc.SequencerPublicKey,
	}
	for index, commit := range commits {
		if err := store.VerifySSHCommit(ctx, commit, "sequencer", log.sequencerPublicKey); err != nil {
			return scannedLog{}, fmt.Errorf("commit %s sequencer signature: %w", commit, err)
		}
		parents, err := store.CommitParents(ctx, commit)
		if err != nil {
			return scannedLog{}, err
		}
		if index == 0 {
			if len(parents) != 0 {
				return scannedLog{}, errors.New("genesis has a parent")
			}
			continue
		}
		if len(parents) != 1 || parents[0] != commits[index-1] {
			return scannedLog{}, fmt.Errorf("commit %s is not single-parent chained", commit)
		}
		event, successor, rotation, err := loadCommit(ctx, store, desc, genesis, commit, loadPayload)
		if err != nil {
			return scannedLog{}, err
		}
		if rotation {
			if successor == log.sequencerPublicKey {
				return scannedLog{}, fmt.Errorf("commit %s rotates to the current sequencer key", commit)
			}
			log.sequencerPublicKey = successor
			continue
		}
		key, err := event.Signed.DedupKey()
		if err != nil {
			return scannedLog{}, err
		}
		if prior, exists := log.Dedup[key]; exists {
			if !prior.Signed.Equal(event.Signed) {
				return scannedLog{}, fmt.Errorf("commit %s: %w", commit, ErrIdempotencyConflict)
			}
			return scannedLog{}, fmt.Errorf("commit %s duplicates idempotent event %s", commit, prior.Commit)
		}
		log.Dedup[key] = eventWithoutPayload(event)
		log.Events = append(log.Events, event)
		log.Verification.Events++
	}
	return log, nil
}

// scanAfter extends a previously verified log only when head descends from its
// exact frontier on the first-parent chain. The base dedup map is not mutated
// until every new commit verifies, so a failed catch-up leaves trusted resident
// state intact.
func scanAfter(ctx context.Context, store gitstore.Store, base scannedLog, head string, loadPayload bool) (scannedLog, error) {
	if base.Verification.Genesis == "" || base.Verification.Head == "" {
		return scannedLog{}, ErrNotDescendant
	}
	commits, err := store.RevListAfter(ctx, base.Verification.Head, head)
	if err != nil {
		return scannedLog{}, err
	}
	return scanListedAfter(ctx, store, base, head, commits, loadPayload)
}

// scanListedAfter verifies an already enumerated first-parent suffix. Checkpoint
// recovery supplies the suffix from the same RevList that binds every cached
// event to the current sequence, avoiding a second, potentially drifting view
// of the ref while retaining the ordinary delta verifier's trust checks.
func scanListedAfter(ctx context.Context, store gitstore.Store, base scannedLog, head string, commits []string, loadPayload bool) (scannedLog, error) {
	genesis := base.Verification.Genesis
	if genesis == "" || base.Verification.Head == "" {
		return scannedLog{}, ErrNotDescendant
	}
	if len(commits) == 0 {
		if head != base.Verification.Head {
			return scannedLog{}, ErrNotDescendant
		}
		base.Events = nil
		return base, nil
	}
	desc, err := Descriptor(ctx, store, genesis)
	if err != nil {
		return scannedLog{}, err
	}
	expectedParent := base.Verification.Head
	events := make([]Event, 0, len(commits))
	additions := make(map[string]Event, len(commits))
	for _, commit := range commits {
		parents, parentErr := store.CommitParents(ctx, commit)
		if parentErr != nil {
			return scannedLog{}, parentErr
		}
		if len(parents) != 1 || parents[0] != expectedParent {
			return scannedLog{}, fmt.Errorf("%w: commit %s does not follow %s", ErrNotDescendant, commit, expectedParent)
		}
		if err := store.VerifySSHCommit(ctx, commit, "sequencer", base.sequencerPublicKey); err != nil {
			return scannedLog{}, fmt.Errorf("commit %s sequencer signature: %w", commit, err)
		}
		event, successor, rotation, err := loadCommit(ctx, store, desc, genesis, commit, loadPayload)
		if err != nil {
			return scannedLog{}, err
		}
		if rotation {
			if successor == base.sequencerPublicKey {
				return scannedLog{}, fmt.Errorf("commit %s rotates to the current sequencer key", commit)
			}
			base.sequencerPublicKey = successor
			expectedParent = commit
			continue
		}
		key, err := event.Signed.DedupKey()
		if err != nil {
			return scannedLog{}, err
		}
		if prior, exists := base.Dedup[key]; exists {
			if !prior.Signed.Equal(event.Signed) {
				return scannedLog{}, fmt.Errorf("commit %s: %w", commit, ErrIdempotencyConflict)
			}
			return scannedLog{}, fmt.Errorf("commit %s duplicates idempotent event %s", commit, prior.Commit)
		}
		if prior, exists := additions[key]; exists {
			if !prior.Signed.Equal(event.Signed) {
				return scannedLog{}, fmt.Errorf("commit %s: %w", commit, ErrIdempotencyConflict)
			}
			return scannedLog{}, fmt.Errorf("commit %s duplicates idempotent event %s", commit, prior.Commit)
		}
		additions[key] = eventWithoutPayload(event)
		events = append(events, event)
		expectedParent = commit
	}
	if expectedParent != head {
		return scannedLog{}, ErrNotDescendant
	}
	if base.Dedup == nil {
		base.Dedup = make(map[string]Event, len(additions))
	}
	for key, event := range additions {
		base.Dedup[key] = event
	}
	base.Verification.Head = head
	base.Verification.Depth += len(commits)
	base.Verification.Events += len(events)
	base.Events = events
	return base, nil
}

// loadEvent is the one decoder/verifier for an event commit after its position
// and sequencer signature have been established. Full and descendant scans
// deliberately share every envelope, actor signature, target, trailer, tree,
// and payload check.
func loadCommit(ctx context.Context, store gitstore.Store, desc GenesisDescriptor, genesis, commit string, loadPayload bool) (Event, string, bool, error) {
	message, timestamp, err := store.CommitMessageWithTimestamp(ctx, commit)
	if err != nil {
		return Event{}, "", false, err
	}
	if uint64(len(message)) > desc.PayloadCeiling {
		return Event{}, "", false, fmt.Errorf("commit %s envelope exceeds genesis ceiling", commit)
	}
	successor, rotation, err := parseRotationMessage(message)
	if err != nil {
		return Event{}, "", false, fmt.Errorf("commit %s: %w", commit, err)
	}
	if rotation {
		tree, err := store.CommitTree(ctx, commit)
		if err != nil {
			return Event{}, "", false, err
		}
		emptyTree, err := store.EmptyTree(ctx)
		if err != nil {
			return Event{}, "", false, err
		}
		if tree != emptyTree {
			return Event{}, "", false, fmt.Errorf("commit %s rotation tree is not empty", commit)
		}
		return Event{}, successor, true, nil
	}
	signed, trailers, err := intent.ParseEnvelope(message, desc.PayloadCeiling)
	if err != nil {
		return Event{}, "", false, err
	}
	decoded, err := intent.Verify(signed)
	if err != nil {
		return Event{}, "", false, err
	}
	if decoded.Target != "git:"+desc.ObjectFormat+":"+genesis {
		return Event{}, "", false, errors.New("intent target does not name chain genesis")
	}
	if !intent.EqualRefs(decoded.RestsOn, trailers) {
		return Event{}, "", false, errors.New("causal trailers differ from signed intent")
	}
	_, treeOID, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil {
		return Event{}, "", false, err
	}
	actualTree, err := store.CommitTree(ctx, commit)
	if err != nil {
		return Event{}, "", false, err
	}
	if actualTree != treeOID {
		return Event{}, "", false, errors.New("commit tree differs from signed intent")
	}
	remaining := desc.PayloadCeiling - uint64(len(message))
	if err := store.ValidatePayloadTree(ctx, actualTree, remaining); err != nil {
		return Event{}, "", false, fmt.Errorf("commit %s payload shape: %w", commit, err)
	}
	event := Event{Commit: commit, Timestamp: timestamp, Intent: decoded, Signed: signed, PayloadTree: decoded.PayloadTree}
	if !loadPayload {
		return event, "", false, nil
	}
	event.Payload, err = store.ReadFile(ctx, commit, "event")
	if err != nil {
		return Event{}, "", false, err
	}
	paths, err := store.ListFiles(ctx, commit, "attachments")
	if err != nil {
		return Event{}, "", false, err
	}
	if len(paths) > 0 {
		event.Attachments = make(map[string][]byte, len(paths))
	}
	for _, path := range paths {
		content, err := store.ReadFile(ctx, commit, path)
		if err != nil {
			return Event{}, "", false, err
		}
		event.Attachments[strings.TrimPrefix(path, "attachments/")] = content
	}
	return event, "", false, nil
}

func eventWithoutPayload(event Event) Event {
	event.Payload = nil
	event.Attachments = nil
	return event
}

// ExitFailpoint is used only by the one-shot spike CLI. It makes a selected
// boundary indistinguishable from process death to the next invocation.
func ExitFailpoint(selected string) func(string) {
	return func(name string) {
		if selected == name {
			os.Exit(97)
		}
	}
}
