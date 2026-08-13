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
	"sync/atomic"
	"time"
	"unicode"

	"github.com/fxamacker/cbor/v2"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

const (
	genesisMarker  = "gitseq-genesis-v0"
	rotationMarker = "gitseq-rotation-v0"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key reused with different intent")
	ErrNotDescendant       = errors.New("head is not a descendant of verified frontier")
	// ErrBackPressure is the sequencer's overload refusal for submissions. It is
	// returned before any chaining work when the submitter is at capacity, and it
	// wraps a submission's exhausted retry limit, so a caller can tell overload
	// apart from a malformed or unauthorized submission with errors.Is. Rotation
	// keeps its own anonymous exhaustion result: it is a rare operator action, not
	// a load-bearing path, and proving its contention would need a production seam
	// that exists only for a test.
	ErrBackPressure = errors.New("sequencer at capacity")
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
	CheckpointPointer CheckpointPointer
	Failpoint         func(string)
	MaxRetries        int
	PreAppend         func(context.Context, Admission) error
	// MaxQueueDepth bounds how many submissions may be inside Submitter.Submit
	// at once, counting the one holding the lock. Zero leaves the queue
	// unbounded, which is the behaviour every existing deployment has.
	MaxQueueDepth int
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
	cache logCache

	// inFlight counts submissions between entering Submit and leaving it. It is
	// read before the lock so a refusal costs one atomic add rather than a wait.
	inFlight atomic.Int64
}

// logCache is the one verified-history cache shared by resident readers and
// submitters. It owns the exact/delta/checkpoint/cold transition ladder and
// publishes new trusted state only after the selected path fully verifies.
type logCache struct {
	target              string
	head                string
	log                 scannedLog
	checkpoint          CheckpointOptions
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
	return &Submitter{
		store: store, options: options,
		cache: logCache{checkpoint: CheckpointOptions{Profile: options.CheckpointProfile, SigningKey: options.SigningKey, Pointer: options.CheckpointPointer}},
	}
}

// Submit refuses at capacity before it chains anything. The check is one atomic
// add taken before the lock, so a refused submission never waits behind the
// queue it was refused for, and never writes an object it will not use.
func (s *Submitter) Submit(ctx context.Context, request Request) (Result, error) {
	if limit := s.options.MaxQueueDepth; limit > 0 {
		depth := s.inFlight.Add(1)
		if depth > int64(limit) {
			s.inFlight.Add(-1)
			return Result{}, fmt.Errorf("%w: %d submissions in flight, limit %d", ErrBackPressure, depth-1, limit)
		}
		defer s.inFlight.Add(-1)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return submit(ctx, s.store, request, s.options, &s.cache)
}

// QueueDepth reports the bound this submitter was built with. It exists so a
// deployment can assert its own posture: zero means unbounded, and a resident
// that means to be bounded should be able to prove it rather than trust that
// the option was passed.
func (s *Submitter) QueueDepth() int { return s.options.MaxQueueDepth }

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
		log, err := scanHead(ctx, store, genesis, head, false, nil)
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

func submit(ctx context.Context, store gitstore.Store, request Request, options Options, cache *logCache) (Result, error) {
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
	for attempt := 0; attempt < maxRetries; attempt++ {
		var (
			head string
			log  scannedLog
		)
		if cache == nil {
			head, err = store.Head(ctx, ref)
			if err == nil {
				log, err = scanHead(ctx, store, targetOID, head, false, nil)
			}
		} else {
			advance, advanceErr := cache.advance(ctx, store, targetOID, cache.checkpoint.Profile != "", false, nil)
			err = advanceErr
			head = advance.Verification.Head
			log = cache.log
		}
		if err != nil {
			return Result{}, err
		}
		prior, replay, dedupErr := dedupPrior(log.Dedup, key, request.Signed)
		if dedupErr != nil {
			return Result{}, dedupErr
		}
		if replay {
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
			cache.append(ctx, store, key, event)
		}
		fail(options, "after_ref_cas")
		fail(options, "before_reply")
		return Result{Commit: commit, Head: commit, CASRetries: attempt, BaseHead: head, Timestamp: timestamp}, nil
	}
	return Result{}, fmt.Errorf("%w: retry limit of %d exceeded while chaining", ErrBackPressure, maxRetries)
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
// Progress is how far a cold audit has got. It exists because the audit is the
// slowest thing this package does and the least able to say so: Load holds the
// reader mutex for its whole duration, and callers hold their own on top of
// that, so by the time anyone can ask, there is nothing left to ask about.
//
// Started becomes true only when checkpoint lookup has fallen back to a full
// scan. Verified counts commits whose complete kernel verification has
// succeeded. Total is how many there are to check.
type Progress struct {
	Started  bool
	Verified int
	Total    int
}

// AuditProgress is written by the verification loop and read by anyone,
// without either mutex. A fresh tracker belongs to one LoadWithProgress call;
// its final values remain available while the caller finishes downstream work.
//
// It is reporting only. Nothing in the kernel reads it back, no verification
// decision depends on it, and omitting it changes no accepted result.
type AuditProgress struct {
	started  atomic.Bool
	verified atomic.Int64
	total    atomic.Int64
}

func (p *AuditProgress) begin() {
	p.started.Store(true)
	p.verified.Store(0)
	p.total.Store(0)
}

func (p *AuditProgress) setTotal(total int) {
	p.total.Store(int64(total))
}

func (p *AuditProgress) advance(verified int) {
	p.verified.Store(int64(verified))
}

// Snapshot returns a coherent-enough monotonic observation for progress UI.
// The fields are bounded counters, not verification inputs.
func (p *AuditProgress) Snapshot() Progress {
	return Progress{Started: p.started.Load(), Verified: int(p.verified.Load()), Total: int(p.total.Load())}
}

type Reader struct {
	store gitstore.Store

	mu sync.Mutex
	logCache
}

func NewReader(store gitstore.Store, checkpoint ...CheckpointOptions) *Reader {
	reader := &Reader{store: store}
	if len(checkpoint) > 0 {
		reader.logCache.checkpoint = checkpoint[0]
	}
	return reader
}

func (r *Reader) Load(ctx context.Context, genesis string) (LoadResult, error) {
	return r.load(ctx, genesis, nil)
}

// LoadWithProgress is Load with an optional, semantic-free cold-audit tracker.
// A checkpoint hit or incremental read leaves the fresh tracker unstarted.
func (r *Reader) LoadWithProgress(ctx context.Context, genesis string, report *AuditProgress) (LoadResult, error) {
	return r.load(ctx, genesis, report)
}

func (r *Reader) load(ctx context.Context, genesis string, report *AuditProgress) (LoadResult, error) {
	started := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	advance, err := r.logCache.advance(ctx, r.store, genesis, true, true, report)
	if err != nil {
		recordVerify(ctx, observe.PathOther, started, 0, err)
		return LoadResult{}, err
	}
	path := observe.PathIncremental
	switch {
	case advance.Checkpoint:
		path = observe.PathCheckpoint
	case advance.Full:
		path = observe.PathCold
	case len(advance.Events) == 0:
		path = observe.PathCache
	}
	recordVerify(ctx, path, started, len(advance.Events), nil)
	return LoadResult{
		Events: advance.Events, Verification: advance.Verification, BaseHead: advance.BaseHead,
		Full: advance.Full, Checkpoint: advance.Checkpoint,
	}, nil
}

func recordVerify(ctx context.Context, path observe.Path, started time.Time, items int, err error) {
	if observer := observe.FromContext(ctx); observer != nil {
		observer.Record(ctx, observe.Measurement{
			Operation: observe.OperationVerify, Path: path, Outcome: observe.Classify(ctx, err),
			Duration: time.Since(started), Items: int64(items),
		})
	}
}

type cacheAdvance struct {
	BaseHead     string
	Events       []Event
	Verification Verification
	Full         bool
	Checkpoint   bool
}

func (c *logCache) advance(ctx context.Context, store gitstore.Store, target string, loadPayload, writeCheckpointOnAdvance bool, report *AuditProgress) (cacheAdvance, error) {
	head, err := store.Head(ctx, Ref(target))
	if err != nil {
		return cacheAdvance{}, err
	}
	if c.target == target && c.head == head {
		c.cacheHits++
		return cacheAdvance{BaseHead: head, Verification: c.log.Verification}, nil
	}

	if c.target == target && c.head != "" {
		baseHead := c.head
		log, deltaErr := scanAfter(ctx, store, c.log, head, loadPayload)
		if deltaErr == nil {
			events := log.Events
			if c.checkpointWritable() {
				c.checkpointEvents = append(c.checkpointEvents, cloneEvents(events)...)
			}
			if writeCheckpointOnAdvance {
				c.maybeWriteCheckpoint(ctx, store, log)
			}
			log.Events = nil
			c.target, c.head, c.log = target, head, log
			c.deltaScans++
			return cacheAdvance{BaseHead: baseHead, Events: events, Verification: log.Verification}, nil
		}
		if !errors.Is(deltaErr, ErrNotDescendant) {
			return cacheAdvance{}, deltaErr
		}
	}

	var log scannedLog
	fromCheckpoint := false
	checkpointAdvanced := false
	if c.checkpoint.Profile != "" {
		log, checkpointAdvanced, err = loadCheckpoint(ctx, store, target, head, c.checkpoint)
		if err == nil {
			fromCheckpoint = true
			c.checkpointLoads++
		} else {
			c.checkpointFallbacks++
		}
	}
	if !fromCheckpoint {
		if report != nil {
			report.begin()
		}
		log, err = scanHead(ctx, store, target, head, loadPayload, report)
		if err != nil {
			return cacheAdvance{}, err
		}
	}

	checkpointCurrent := fromCheckpoint && !checkpointAdvanced
	if c.checkpoint.Profile != "" && c.checkpoint.SigningKey != "" && !checkpointCurrent {
		if writeCheckpoint(ctx, store, log, c.checkpoint) == nil {
			c.checkpointWrites++
			checkpointCurrent = true
		} else {
			c.checkpointFailures++
		}
	}
	events := log.Events
	if c.checkpointWritable() {
		c.checkpointEvents = cloneEvents(events)
		if checkpointCurrent {
			c.checkpointAttempt = log.Verification.Depth
		} else {
			c.checkpointAttempt = log.Verification.Depth - checkpointInterval
		}
	} else {
		c.checkpointEvents = nil
		c.checkpointAttempt = 0
	}
	log.Events = nil
	c.target, c.head, c.log = target, head, log
	if !fromCheckpoint {
		c.fullScans++
	}
	return cacheAdvance{
		Events: events, Verification: log.Verification,
		Full: true, Checkpoint: fromCheckpoint,
	}, nil
}

func (c *logCache) checkpointWritable() bool {
	return c.checkpoint.Profile != "" && c.checkpoint.SigningKey != ""
}

func (c *logCache) maybeWriteCheckpoint(ctx context.Context, store gitstore.Store, log scannedLog) {
	if !c.checkpointWritable() || !checkpointDue(log.Verification.Depth, c.checkpointAttempt) {
		return
	}
	checkpointLog := log
	checkpointLog.Events = c.checkpointEvents
	if writeCheckpoint(ctx, store, checkpointLog, c.checkpoint) == nil {
		c.checkpointWrites++
		c.checkpointAttempt = log.Verification.Depth
	} else {
		c.checkpointFailures++
	}
}

func (c *logCache) append(ctx context.Context, store gitstore.Store, key string, event Event) {
	c.log.Dedup[key] = eventWithoutPayload(event)
	c.log.Verification.Head = event.Commit
	c.log.Verification.Depth++
	c.log.Verification.Events++
	c.head = event.Commit
	if !c.checkpointWritable() {
		return
	}
	c.checkpointEvents = append(c.checkpointEvents, cloneEvent(event))
	c.maybeWriteCheckpoint(ctx, store, c.log)
}

// Load verifies a log before returning its application records. Consumers do
// not get a convenient unverified read path by accident.
func Load(ctx context.Context, store gitstore.Store, genesis string) ([]Event, Verification, error) {
	head, err := store.Head(ctx, Ref(genesis))
	if err != nil {
		return nil, Verification{}, err
	}
	log, err := scanHead(ctx, store, genesis, head, true, nil)
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
	log, err := scanHead(ctx, store, genesis, head, false, nil)
	if err != nil {
		return Verification{}, err
	}
	return log.Verification, nil
}

// scanHead performs the explicit cold/non-descendant full audit. It verifies
// the immutable head and, in the same traversal, builds the event stream and
// actor-scoped dedup index. loadPayload controls only whether verified payload
// bytes are retained.
func scanHead(ctx context.Context, store gitstore.Store, genesis, head string, loadPayload bool, report *AuditProgress) (scannedLog, error) {
	sequence, err := store.RevList(ctx, head)
	if err != nil {
		return scannedLog{}, err
	}
	if err := validateSequenceBounds(sequence, genesis, head); err != nil {
		return scannedLog{}, err
	}
	if report != nil {
		report.setTotal(len(sequence))
	}
	var (
		desc  GenesisDescriptor
		log   scannedLog
		index int
	)
	err = store.WalkRevListMetadata(ctx, head, func(commit gitstore.CommitMetadata) error {
		if index >= len(sequence) || commit.OID != sequence[index] {
			return errors.New("history metadata differs from sequence enumeration")
		}
		if index == 0 {
			desc, err = parseGenesisMessage(normalizeGenesisMessage(commit.Message))
			if err != nil {
				return err
			}
			log = scannedLog{
				Verification:       Verification{Genesis: genesis, Head: head, Depth: len(sequence) - 1},
				Events:             make([]Event, 0, len(sequence)-1),
				Dedup:              make(map[string]Event, len(sequence)-1),
				sequencerPublicKey: desc.SequencerPublicKey,
			}
		}
		if err := store.VerifySSHCommit(ctx, commit.OID, "sequencer", log.sequencerPublicKey); err != nil {
			return fmt.Errorf("commit %s sequencer signature: %w", commit.OID, err)
		}
		if index == 0 {
			if err := validateChainParents(index, commit.Parents, ""); err != nil {
				return err
			}
			index++
			if report != nil {
				report.advance(index)
			}
			return nil
		}
		if err := validateChainParents(index, commit.Parents, sequence[index-1]); err != nil {
			return fmt.Errorf("commit %s: %w", commit.OID, err)
		}
		event, successor, rotation, err := loadCommit(ctx, store, desc, genesis, commit, loadPayload)
		if err != nil {
			return err
		}
		if rotation {
			if successor == log.sequencerPublicKey {
				return fmt.Errorf("commit %s rotates to the current sequencer key", commit.OID)
			}
			log.sequencerPublicKey = successor
			index++
			if report != nil {
				report.advance(index)
			}
			return nil
		}
		key, err := event.Signed.DedupKey()
		if err != nil {
			return err
		}
		prior, duplicate, dedupErr := dedupPrior(log.Dedup, key, event.Signed)
		if dedupErr != nil {
			return fmt.Errorf("commit %s: %w", commit.OID, dedupErr)
		}
		if duplicate {
			return fmt.Errorf("commit %s duplicates idempotent event %s", commit.OID, prior.Commit)
		}
		log.Dedup[key] = eventWithoutPayload(event)
		log.Events = append(log.Events, event)
		log.Verification.Events++
		index++
		if report != nil {
			report.advance(index)
		}
		return nil
	})
	if err != nil {
		return scannedLog{}, err
	}
	if index != len(sequence) {
		return scannedLog{}, errors.New("history metadata differs from sequence enumeration")
	}
	return log, nil
}

func validateSequenceBounds(commits []string, genesis, head string) error {
	if len(commits) == 0 || commits[0] != genesis {
		return errors.New("chain does not begin at named genesis")
	}
	if commits[len(commits)-1] != head {
		return errors.New("history does not end at named head")
	}
	return nil
}

// Git's show helpers historically passed commit messages through Store.run,
// which trims the command's outer whitespace. Metadata enumeration preserves
// the raw NUL-framed message instead, so scans normalize it at this boundary
// to keep the established genesis and event byte semantics unchanged.
func normalizeGenesisMessage(message string) string {
	return string(bytes.TrimSpace([]byte(message))) + "\n"
}

func normalizeEventMessage(message string) string {
	return string(bytes.TrimRightFunc([]byte(message), unicode.IsSpace)) + "\n"
}

func validateChainParents(index int, parents []string, prior string) error {
	if index == 0 {
		if len(parents) != 0 {
			return errors.New("genesis has a parent")
		}
		return nil
	}
	if len(parents) != 1 || parents[0] != prior {
		return errors.New("is not single-parent chained")
	}
	return nil
}

// scanAfter extends a previously verified log only when head descends from its
// exact frontier on the first-parent chain. The base dedup map is not mutated
// until every new commit verifies, so a failed catch-up leaves trusted resident
// state intact.
func scanAfter(ctx context.Context, store gitstore.Store, base scannedLog, head string, loadPayload bool) (scannedLog, error) {
	if base.Verification.Genesis == "" || base.Verification.Head == "" {
		return scannedLog{}, ErrNotDescendant
	}
	scan := newDeltaScan(ctx, store, base, head, loadPayload, 0)
	err := store.WalkRevListMetadataAfter(ctx, base.Verification.Head, head, scan.accept)
	if err != nil {
		return scannedLog{}, err
	}
	return scan.finish()
}

// scanListedAfter verifies an already enumerated first-parent suffix. Checkpoint
// recovery supplies the suffix from the same RevList that binds every cached
// event to the current sequence, avoiding a second, potentially drifting view
// of the ref while retaining the ordinary delta verifier's trust checks.
func scanListedAfter(ctx context.Context, store gitstore.Store, base scannedLog, head string, commits []gitstore.CommitMetadata, loadPayload bool) (scannedLog, error) {
	if base.Verification.Genesis == "" || base.Verification.Head == "" {
		return scannedLog{}, ErrNotDescendant
	}
	scan := newDeltaScan(ctx, store, base, head, loadPayload, len(commits))
	for _, commit := range commits {
		if err := scan.accept(commit); err != nil {
			return scannedLog{}, err
		}
	}
	return scan.finish()
}

type deltaScan struct {
	ctx            context.Context
	store          gitstore.Store
	base           scannedLog
	head           string
	loadPayload    bool
	desc           GenesisDescriptor
	descLoaded     bool
	expectedParent string
	events         []Event
	additions      map[string]Event
	positions      int
}

func newDeltaScan(ctx context.Context, store gitstore.Store, base scannedLog, head string, loadPayload bool, capacity int) *deltaScan {
	return &deltaScan{
		ctx: ctx, store: store, base: base, head: head, loadPayload: loadPayload,
		expectedParent: base.Verification.Head,
		events:         make([]Event, 0, capacity),
		additions:      make(map[string]Event, capacity),
	}
}

func (s *deltaScan) accept(commit gitstore.CommitMetadata) error {
	if !s.descLoaded {
		desc, err := Descriptor(s.ctx, s.store, s.base.Verification.Genesis)
		if err != nil {
			return err
		}
		s.desc = desc
		s.descLoaded = true
	}
	if err := validateChainParents(1, commit.Parents, s.expectedParent); err != nil {
		return fmt.Errorf("%w: commit %s does not follow %s: %v", ErrNotDescendant, commit.OID, s.expectedParent, err)
	}
	if err := s.store.VerifySSHCommit(s.ctx, commit.OID, "sequencer", s.base.sequencerPublicKey); err != nil {
		return fmt.Errorf("commit %s sequencer signature: %w", commit.OID, err)
	}
	event, successor, rotation, err := loadCommit(s.ctx, s.store, s.desc, s.base.Verification.Genesis, commit, s.loadPayload)
	if err != nil {
		return err
	}
	if rotation {
		if successor == s.base.sequencerPublicKey {
			return fmt.Errorf("commit %s rotates to the current sequencer key", commit.OID)
		}
		s.base.sequencerPublicKey = successor
		s.expectedParent = commit.OID
		s.positions++
		return nil
	}
	key, err := event.Signed.DedupKey()
	if err != nil {
		return err
	}
	prior, duplicate, dedupErr := dedupPrior(s.base.Dedup, key, event.Signed)
	if dedupErr != nil {
		return fmt.Errorf("commit %s: %w", commit.OID, dedupErr)
	}
	if duplicate {
		return fmt.Errorf("commit %s duplicates idempotent event %s", commit.OID, prior.Commit)
	}
	prior, duplicate, dedupErr = dedupPrior(s.additions, key, event.Signed)
	if dedupErr != nil {
		return fmt.Errorf("commit %s: %w", commit.OID, dedupErr)
	}
	if duplicate {
		return fmt.Errorf("commit %s duplicates idempotent event %s", commit.OID, prior.Commit)
	}
	s.additions[key] = eventWithoutPayload(event)
	s.events = append(s.events, event)
	s.expectedParent = commit.OID
	s.positions++
	return nil
}

func (s *deltaScan) finish() (scannedLog, error) {
	if s.positions == 0 {
		if s.head != s.base.Verification.Head {
			return scannedLog{}, ErrNotDescendant
		}
		s.base.Events = nil
		return s.base, nil
	}
	if s.expectedParent != s.head {
		return scannedLog{}, ErrNotDescendant
	}
	if s.base.Dedup == nil {
		s.base.Dedup = make(map[string]Event, len(s.additions))
	}
	for key, event := range s.additions {
		s.base.Dedup[key] = event
	}
	// Additions stay separate until finish, so a failed streamed delta cannot
	// mutate the resident dedup map. Key rotation likewise changes only this
	// copied scannedLog until the whole suffix succeeds.
	s.base.Verification.Head = s.head
	s.base.Verification.Depth += s.positions
	s.base.Verification.Events += len(s.events)
	s.base.Events = s.events
	return s.base, nil
}

// loadEvent is the one decoder/verifier for an event commit after its position
// and sequencer signature have been established. Full and descendant scans
// deliberately share every envelope, actor signature, target, trailer, tree,
// and payload check.
func loadCommit(ctx context.Context, store gitstore.Store, desc GenesisDescriptor, genesis string, commit gitstore.CommitMetadata, loadPayload bool) (Event, string, bool, error) {
	message := normalizeEventMessage(commit.Message)
	if uint64(len(message)) > desc.PayloadCeiling {
		return Event{}, "", false, fmt.Errorf("commit %s envelope exceeds genesis ceiling", commit.OID)
	}
	successor, rotation, err := parseRotationMessage(message)
	if err != nil {
		return Event{}, "", false, fmt.Errorf("commit %s: %w", commit.OID, err)
	}
	if rotation {
		emptyTree, err := store.EmptyTree(ctx)
		if err != nil {
			return Event{}, "", false, err
		}
		if commit.Tree != emptyTree {
			return Event{}, "", false, fmt.Errorf("commit %s rotation tree is not empty", commit.OID)
		}
		return Event{}, successor, true, nil
	}
	signed, trailers, err := intent.ParseEnvelope(message, desc.PayloadCeiling)
	if err != nil {
		return Event{}, "", false, err
	}
	decoded, targetMatches, err := verifySignedTarget(signed, "git:"+desc.ObjectFormat+":"+genesis)
	if err != nil {
		return Event{}, "", false, err
	}
	if !targetMatches {
		return Event{}, "", false, errors.New("intent target does not name chain genesis")
	}
	if !intent.EqualRefs(decoded.RestsOn, trailers) {
		return Event{}, "", false, errors.New("causal trailers differ from signed intent")
	}
	_, treeOID, err := gitstore.ParseTypedOID(decoded.PayloadTree)
	if err != nil {
		return Event{}, "", false, err
	}
	if commit.Tree != treeOID {
		return Event{}, "", false, errors.New("commit tree differs from signed intent")
	}
	remaining := desc.PayloadCeiling - uint64(len(message))
	if err := store.ValidatePayloadTree(ctx, commit.Tree, remaining); err != nil {
		return Event{}, "", false, fmt.Errorf("commit %s payload shape: %w", commit.OID, err)
	}
	event := Event{Commit: commit.OID, Timestamp: commit.Timestamp, Intent: decoded, Signed: signed, PayloadTree: decoded.PayloadTree}
	if !loadPayload {
		return event, "", false, nil
	}
	event.Payload, err = store.ReadFile(ctx, commit.OID, "event")
	if err != nil {
		return Event{}, "", false, err
	}
	paths, err := store.ListFiles(ctx, commit.OID, "attachments")
	if err != nil {
		return Event{}, "", false, err
	}
	if len(paths) > 0 {
		event.Attachments = make(map[string][]byte, len(paths))
	}
	for _, path := range paths {
		content, err := store.ReadFile(ctx, commit.OID, path)
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

func dedupPrior(index map[string]Event, key string, signed intent.Signed) (Event, bool, error) {
	prior, exists := index[key]
	if !exists {
		return Event{}, false, nil
	}
	if !prior.Signed.Equal(signed) {
		return Event{}, false, ErrIdempotencyConflict
	}
	return prior, true, nil
}

func verifySignedTarget(signed intent.Signed, target string) (intent.Intent, bool, error) {
	decoded, err := intent.Verify(signed)
	if err != nil {
		return intent.Intent{}, false, err
	}
	return decoded, decoded.Target == target, nil
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
