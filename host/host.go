// Package host runs an application built outside this module on the gitseq
// kernel.
//
// Gitseq proves two things about a repository: who signed each record, and in
// what order the records were accepted. It assigns them no meaning. Meaning
// comes from an application — its vocabulary and its deterministic fold — and
// an application is an ordinary Go module that imports this package. Nothing
// here decodes an application payload, so nothing here can disagree with the
// application about what its records mean.
//
// The whole surface is eight acts:
//
//	ws, err := host.Init(ctx, dir, app, initializer, host.Options{})
//	ws, err := host.Open(ctx, dir, app)
//	ws, err := host.OpenAttached(ctx, clone, app, host.Attachment{Genesis: genesis, SequencerKey: key})
//	replacement, err := host.ReplaceBinding(ctx, dir, nextApp, initializer)
//	rec, err := ws.Append(ctx, signer, host.Act{Schema: "chess/move@0", Payload: encoded})
//	draft, err := ws.Prepare(host.Act{Schema: "chess/move@0", Payload: encoded})
//	rec, err := ws.AppendSigned(ctx, host.SignedAct{Prepared: draft, ActorKey: public, ActorSignature: signature})
//	log, err := ws.Records(ctx)
//
// Init records the first binding and makes its signer the binding authority.
// Open refuses to hand back a repository bound to an application or fold this
// build does not hold. OpenAttached opens an already-fetched sequence with
// caller-supplied sequencer custody; unlike Init, it never creates a sequence.
// ReplaceBinding lets that initializing signer record an ordered replacement
// after establishing the migration is sound. Append signs one application act
// with the caller's key and gives it a position. Prepare and AppendSigned split
// that operation so the actor can sign outside this process. Records returns
// the verified ordered records for the application to fold. There is no
// projection here, because the projection is the application's.
//
// # Binding
//
// A repository declares its first application in its opening records. The key
// that signed the first record may later replace that binding; no other key or
// application-level role may do so. Bindings are read in order and the newest
// qualifying replacement wins. An unauthorized, unparseable, or malformed
// binding-shaped record is skipped and leaves the previous valid binding in
// force. Open checks that binding against what the running binary says it is
// and refuses with [ErrUninterpretable] when they differ — the repository is
// still kernel-verifiable, and only its meaning is unavailable. A repository
// that declares no binding is a Workroom repository by the fixed compatibility
// rule, so an application other than Workroom refuses it too. Reading or
// recording a binding fetches, builds, and runs nothing.
//
// # Who may append
//
// Every actor is a key. This package keeps no roster and no allowlist: any
// syntactically valid act carrying a good signature over a well-formed intent
// is admitted, and the application's fold decides what force it has. That is
// the deliberate posture for an application whose submit path is open to
// whoever holds a key, and it costs nothing in record authority, because the
// signature — not the transport, and not admission — is what says who acted.
// An act the fold judges illegal is still recorded, exactly as it was signed,
// and remains ineffective forever.
//
// The kernel's own bounds still apply: intent field sizes, causal-reference
// counts, the payload ceiling fixed at Init, idempotency, and a bounded
// sequencer queue. Volume abuse is answered there and by whatever rate limit
// the application's transport imposes, never by pretending an unsigned caller
// is unauthenticated.
//
// # Custody and concurrency
//
// Init generates the sequence's sequencer key inside the repository, so the
// process holding that file is the one that can append. One process must hold
// it at a time; two writers racing on one repository is a deployment error
// this package does not detect. A [Workspace] is safe for concurrent use.
package host

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

// queueDepth bounds how many submissions may be inside the sequencer at once,
// counting the one holding the lock. The kernel treats zero as unbounded,
// which is not a posture an open submit path can take, so this package always
// sets a bound and does not offer the opt-out.
const queueDepth = 32

// defaultPayloadCeiling is the largest application payload a repository
// admits when Init is not told otherwise. It is fixed at genesis and cannot be
// raised later, which is why it is stated here rather than left implicit.
const defaultPayloadCeiling = 1 << 20

// ErrUninterpretable reports a repository that verifies as a sequence but is
// bound to an application, or a fold of that application, which the running
// binary does not embody. It is not a damaged repository and not a failed
// signature check: the log's authority is intact and readable, and only the
// meaning of its records is unavailable here. Recovering the meaning takes
// either the application at the bound version or an authorized binding
// replacement whose migration evidence establishes how the incoming fold
// judges the existing log.
var ErrUninterpretable = errors.New("repository is verifiable but uninterpretable")

// Application is what a binary says it is. Name and FoldVersion are the
// binding's two load-bearing fields: Name says whose vocabulary the records
// are written in, and FoldVersion says which exact deterministic meaning that
// vocabulary had. Change the fold in a way that changes any judgement and the
// version must change with it, or two builds will read one log differently and
// both will believe they agree.
//
// The source commit is not stated here. Go stamps it into the binary at build
// time and Init records what it finds, so the binding says which build wrote
// it rather than what that build claimed about itself.
type Application struct {
	// Name identifies the application, for example "chess".
	Name string
	// FoldVersion identifies the exact fold, for example "chess-fold@1".
	FoldVersion string
	// SourceURL is where the application can be obtained. It is provenance
	// and never authority: nothing here fetches it, and a reader who follows
	// it is making a deliberate choice outside gitseq. It may be empty.
	SourceURL string
}

func (a Application) validate() error {
	if a.Name == "" {
		return errors.New("application name is required")
	}
	if a.FoldVersion == "" {
		return errors.New("application fold version is required")
	}
	return nil
}

// Options are the choices Init fixes permanently. Open takes none: everything
// it needs is already in the repository.
type Options struct {
	// PayloadCeiling is the largest record the sequence will ever admit, in
	// bytes. It bounds the whole event — the signed envelope as well as the
	// payload — so the room left for an application payload is a little
	// smaller than the number set here. It is written into the genesis record
	// and cannot be changed afterwards, so a deployment that wants a tighter
	// bound than the 1 MiB default must say so at Init. Zero means the
	// default.
	PayloadCeiling uint64
}

// Attachment identifies one already-fetched sequence and the local sequencer
// custody that may extend it. OpenAttached derives the object format and every
// other kernel fact from the verified repository. SequencerKey is a path to an
// OpenSSH Ed25519 private key; the key itself is never copied into this value.
type Attachment struct {
	Genesis      string
	SequencerKey string
}

// Act is one thing an actor does. Gitseq carries Schema and Payload without
// reading them; the application owns both.
type Act struct {
	// Schema names the payload's shape, for example "chess/move@0". It is an
	// opaque string to gitseq and belongs to the application's own family.
	Schema string
	// Payload is the encoded act. The application chooses the encoding and is
	// the only thing that decodes it.
	Payload []byte
	// RestsOn carries the record identifiers this act points at — the causal
	// chain the application relies on, such as the move before this one. The
	// strings are signed with the act and carried unread.
	RestsOn []string
	// IdempotencyKey makes a retry the same act rather than a second one.
	// Submitting the same key with the same intent twice appends once and
	// returns the record that already exists. An empty key means this act is
	// unique and generates one, which is right for a fresh act and wrong for
	// a retry.
	IdempotencyKey string
}

// PreparedAct is an application act bound to this sequence and ready for its
// actor to sign. Intent is canonical kernel intent bytes. Payload is carried
// beside it because its Git tree identifier is already inside Intent.
//
// Preparation writes nothing. Keep the whole value for a retry: when the
// caller omitted Act.IdempotencyKey, preparation chose one and signed it into
// Intent.
type PreparedAct struct {
	Intent  []byte `json:"intent"`
	Payload []byte `json:"payload"`
}

// SignedAct carries an actor signature made outside the host. The host never
// receives the corresponding private key.
type SignedAct struct {
	Prepared       PreparedAct       `json:"prepared"`
	ActorKey       ed25519.PublicKey `json:"actor_key"`
	ActorSignature []byte            `json:"actor_signature"`
}

// Record is one accepted act, as the application's fold will see it.
type Record struct {
	// ID identifies this record globally: it embeds the log's genesis as well
	// as the record's own commit, so an identifier from one repository is
	// never mistaken for one from another.
	ID string
	// Actor is the fingerprint of the key that signed this act. It is the
	// application's identity for a player, an agent, or anything else that
	// holds a key.
	Actor string
	// ActorKey is that key itself. An application that verifies an
	// endorsement of a signing key — linking a session key to a persistent
	// identity, say — needs the key and not only its fingerprint.
	ActorKey ed25519.PublicKey
	// Schema and Payload are what the actor signed, unchanged.
	Schema  string
	Payload []byte
	// RestsOn is the causal chain the actor signed.
	RestsOn []string
	// Timestamp is the sequencer's signed time for this position, in Unix
	// seconds. A deterministic fold judges expiry against this and never
	// against the reader's clock.
	Timestamp int64
}

// Log is the verified ordered record stream at one moment, with the sequence
// position it was read at.
//
// Records is borrowed, not copied: it and the payloads inside it belong to the
// Workspace and must not be modified. Fold it, or copy what you keep.
type Log struct {
	// Genesis is the repository's sequence identity.
	Genesis string
	// Head is the commit at the verified frontier, and Depth is how many
	// records stand at or before it.
	Head  string
	Depth int
	// Records are every accepted record, oldest first. Replaying them through
	// the application's fold reproduces its state exactly, on any machine,
	// with no network and no clock.
	Records []Record
}

// BindingReplacement reports one ordered transition between fold versions.
// The replacement record itself carries the same genesis and outgoing fold,
// so this result is a convenient rendering rather than the only evidence.
type BindingReplacement struct {
	Genesis             string `json:"genesis"`
	Application         string `json:"application"`
	OutgoingFoldVersion string `json:"outgoing_fold_version"`
	IncomingFoldVersion string `json:"incoming_fold_version"`
	Record              Record `json:"record"`
}

// Workspace is one opened repository, bound to one application. The binding is
// read when the workspace is made and never again, so every operation on one
// workspace means the same thing however the log moves underneath it.
type Workspace struct {
	genesis      string
	objectFormat string
	namespace    string
	sequencerKey string
	store        gitstore.Store

	mu        sync.Mutex
	reader    *kernel.Reader
	submitter *kernel.Submitter
	records   []Record
	head      string
	depth     int
}

// Init creates a sequence in an existing Git repository and records its first
// application binding. The binding is the log's first record, signed by
// initializer, and that key is the binding authority from then on: only it can
// record a replacement, and no application-level role can grant or revoke
// that. Keep it, or the repository can never be rebound.
//
// The repository must already exist as a Git repository and must not already
// hold a gitseq sequence.
//
// Init that fails partway leaves a sequence that no application can open: the
// configuration is written before the binding, so a later Open refuses it as
// uninterpretable and a later Init refuses it as already initialized. That is
// deliberate. Writing the configuration last would instead let a retry create
// a second sequence beside the first without saying so, and a loud refusal
// whose repair is to discard an empty repository is the better failure.
func Init(ctx context.Context, repo string, application Application, initializer ed25519.PrivateKey, options Options) (*Workspace, error) {
	if err := application.validate(); err != nil {
		return nil, err
	}
	if len(initializer) != ed25519.PrivateKeySize {
		return nil, errors.New("initializer must be an ed25519 private key")
	}
	ceiling := options.PayloadCeiling
	if ceiling == 0 {
		ceiling = defaultPayloadCeiling
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	metaDir := apphost.MetaDir(commonDir)
	if _, err := os.Stat(filepath.Join(metaDir, apphost.ConfigFile)); err == nil {
		return nil, errors.New("repository already holds a gitseq sequence")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		return nil, err
	}
	store := gitstore.Store{Repo: commonDir}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return nil, err
	}
	sequencerKey := filepath.Join(metaDir, "sequencer")
	sequencerPublic, err := gitstore.GenerateSSHKey(ctx, sequencerKey)
	if err != nil {
		return nil, err
	}
	genesis, err := kernel.Create(ctx, store, kernel.GenesisDescriptor{
		Version: 0, ObjectFormat: format, PayloadCeiling: ceiling, SequencerPublicKey: sequencerPublic,
	}, sequencerKey)
	if err != nil {
		return nil, err
	}
	config := apphost.Config{
		Version: 0, Genesis: genesis, ObjectFormat: format, PayloadCeiling: ceiling,
		IdempotencyNamespace: application.Name, SequencerKey: sequencerKey,
	}
	if err := apphost.SaveConfig(metaDir, config); err != nil {
		return nil, err
	}
	workspace := newWorkspace(config, store)
	// The binding goes in as the log's first record, which is what makes the
	// key that signed it the initializing key this repository answers to. A
	// repository that recorded anything else first would hand that authority
	// to whoever signed that record instead.
	recorded := apphost.Binding{
		Application: application.Name, FoldVersion: application.FoldVersion,
		SourceCommit: apphost.SourceCommit(), SourceURL: application.SourceURL,
	}
	payload, err := recorded.Payload()
	if err != nil {
		return nil, err
	}
	if _, err := workspace.append(ctx, initializer, apphost.BindingSchema, payload, nil, "binding"); err != nil {
		return nil, err
	}
	return workspace, nil
}

// Open reads an existing repository and hands it back only to the application
// it is bound to.
//
// The order is fixed and the reason is the whole point of it: the kernel
// verifies the sequence first, and only a verified sequence is told what it is
// bound to. A refusal from here is therefore a claim about a repository whose
// signatures and order already check out, so no history an appender controls
// can present an unverifiable chain as a missing interpreter instead.
//
// The binding is read at the exact frontier the kernel just verified, not at
// whatever the ref points at afterwards. Asking the ref a second time would
// leave a gap between the two questions: an appender racing the open could
// advance the ref in between, and the workspace would come back bound by a
// frontier it never verified.
func Open(ctx context.Context, repo string, application Application) (*Workspace, error) {
	if err := application.validate(); err != nil {
		return nil, err
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(commonDir))
	if err != nil {
		return nil, err
	}
	workspace := newWorkspace(config, gitstore.Store{Repo: commonDir})
	return openBound(ctx, workspace, application)
}

// OpenAttached opens an existing sequence whose refs have already been
// fetched into repo, using caller-supplied sequencer custody to make the
// returned workspace writable. It never creates a sequence or writes Gitseq
// configuration, which keeps attaching distinct from initialization and lets
// an outside application use this boundary without importing internal/apphost.
//
// Genesis must name the fetched sequence. SequencerKey is checked by the
// kernel on the first append against the sequence's verified current key; a
// missing, malformed, stale, or unrelated key cannot advance the sequence.
func OpenAttached(ctx context.Context, repo string, application Application, attachment Attachment) (*Workspace, error) {
	if err := application.validate(); err != nil {
		return nil, err
	}
	if attachment.SequencerKey == "" {
		return nil, errors.New("attachment sequencer key is required")
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return nil, err
	}
	store := gitstore.Store{Repo: commonDir}
	format, err := store.ObjectFormat(ctx)
	if err != nil {
		return nil, err
	}
	if err := apphost.ValidateGenesis(format, attachment.Genesis); err != nil {
		return nil, fmt.Errorf("invalid attachment genesis: %w", err)
	}
	workspace := newWorkspace(apphost.Config{
		Version: 0, Genesis: attachment.Genesis, ObjectFormat: format,
		IdempotencyNamespace: application.Name, SequencerKey: attachment.SequencerKey,
	}, store)
	return openBound(ctx, workspace, application)
}

func openBound(ctx context.Context, workspace *Workspace, application Application) (*Workspace, error) {
	// The kernel speaks first, and the frontier it verified is what the
	// binding is then read out of.
	verified, err := workspace.Records(ctx)
	if err != nil {
		return nil, err
	}
	recorded, err := apphost.BindingInForce(ctx, workspace.store, workspace.genesis, verified.Head)
	if err != nil {
		return nil, err
	}
	// No binding is not an open question. The compatibility rule is fixed: a
	// repository that declares none is a Workroom repository, so anything
	// else opening it is opening someone else's log.
	bound := apphost.Binding{Application: apphost.DefaultApplication}
	if recorded != nil {
		bound = *recorded
	}
	if bound.Application != application.Name {
		return nil, fmt.Errorf("%w: it is bound to application %q, and this build is %q", ErrUninterpretable, bound.Application, application.Name)
	}
	if bound.FoldVersion != application.FoldVersion {
		return nil, fmt.Errorf("%w: it is bound to %s fold %q, and this build holds %q", ErrUninterpretable, bound.Application, bound.FoldVersion, application.FoldVersion)
	}
	return workspace, nil
}

// ReplaceBinding records a new application binding after verifying the whole
// sequence. The caller must hold the private key that signed the log's first
// record; having a sequencer key or an application-level role is insufficient.
//
// This operation authorizes application to interpret the existing log. It
// does not prove that application's fold preserves earlier judgments: the
// operator must establish that separately before calling it. The replacement
// records the exact genesis and outgoing fold version, and is admitted only if
// that same binding remains in force at the head it will extend. A concurrent
// replacement therefore causes a refusal instead of being overwritten.
//
// A repository with no explicit binding cannot be replaced through this
// surface because its compatibility binding names no exact outgoing fold in
// the log. A repository already carrying the exact requested binding is also
// refused rather than gaining a redundant transition.
func ReplaceBinding(ctx context.Context, repo string, application Application, initializer ed25519.PrivateKey) (BindingReplacement, error) {
	return replaceBinding(ctx, repo, application, initializer, nil)
}

func replaceBinding(ctx context.Context, repo string, application Application, initializer ed25519.PrivateKey, failpoint func(string)) (BindingReplacement, error) {
	if err := application.validate(); err != nil {
		return BindingReplacement{}, err
	}
	if len(initializer) != ed25519.PrivateKeySize {
		return BindingReplacement{}, errors.New("initializer must be an ed25519 private key")
	}
	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		return BindingReplacement{}, err
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(commonDir))
	if err != nil {
		return BindingReplacement{}, err
	}
	if config.ReadOnly || config.SequencerKey == "" {
		return BindingReplacement{}, errors.New("repository is attached read-only: it holds no sequencer key to replace its binding")
	}
	workspace := newWorkspace(config, gitstore.Store{Repo: commonDir})
	verified, err := workspace.Records(ctx)
	if err != nil {
		return BindingReplacement{}, err
	}
	if len(verified.Records) == 0 {
		return BindingReplacement{}, errors.New("repository has no initializing actor record")
	}
	initializingKey := verified.Records[0].ActorKey
	if !bytes.Equal(initializer.Public().(ed25519.PublicKey), initializingKey) {
		return BindingReplacement{}, errors.New("binding replacement requires the initializing actor's key")
	}
	outgoing, err := apphost.BindingInForce(ctx, workspace.store, config.Genesis, verified.Head)
	if err != nil {
		return BindingReplacement{}, err
	}
	if outgoing == nil {
		return BindingReplacement{}, errors.New("repository has no explicit binding with an exact outgoing fold version")
	}
	incoming := apphost.Binding{
		Application: application.Name, SourceCommit: apphost.SourceCommit(),
		SourceURL: application.SourceURL, FoldVersion: application.FoldVersion,
	}
	if sameBindingIdentity(*outgoing, incoming) {
		return BindingReplacement{}, errors.New("requested binding is already in force")
	}
	recorded := incoming
	recorded.Genesis = "git:" + config.ObjectFormat + ":" + config.Genesis
	recorded.PreviousFoldVersion = outgoing.FoldVersion
	payload, err := recorded.Payload()
	if err != nil {
		return BindingReplacement{}, err
	}
	request, err := workspace.bindingRequest(ctx, initializer, payload, verified.Head)
	if err != nil {
		return BindingReplacement{}, err
	}
	expected := *outgoing
	submitter := kernel.NewSubmitter(workspace.store, kernel.Options{
		SigningKey: config.SequencerKey, MaxQueueDepth: queueDepth, Failpoint: failpoint,
		PostDedup: func(ctx context.Context, act kernel.Application) error {
			if !bytes.Equal(act.ActorKey, initializingKey) {
				return errors.New("binding replacement requires the initializing actor's key")
			}
			current, err := apphost.BindingInForce(ctx, workspace.store, config.Genesis, act.Head)
			if err != nil {
				return err
			}
			if current == nil || *current != expected {
				return errors.New("binding in force changed while its replacement was being recorded")
			}
			return nil
		},
	})
	result, err := submitter.Submit(ctx, request)
	if err != nil {
		return BindingReplacement{}, err
	}
	inForce, err := apphost.BindingInForce(ctx, workspace.store, config.Genesis, result.Head)
	if err != nil {
		return BindingReplacement{}, err
	}
	if inForce == nil || *inForce != recorded {
		return BindingReplacement{}, errors.New("recorded binding replacement did not take force")
	}
	return BindingReplacement{
		Genesis: config.Genesis, Application: incoming.Application,
		OutgoingFoldVersion: outgoing.FoldVersion, IncomingFoldVersion: incoming.FoldVersion,
		Record: Record{
			ID: workspace.eventID(result.Commit), Actor: actorFingerprint(request.Signed.ActorKey),
			ActorKey: ed25519.PublicKey(request.Signed.ActorKey), Schema: apphost.BindingSchema,
			Payload: payload, Timestamp: result.Timestamp,
		},
	}, nil
}

func sameBindingIdentity(left, right apphost.Binding) bool {
	return left.Application == right.Application && left.SourceCommit == right.SourceCommit &&
		left.SourceURL == right.SourceURL && left.FoldVersion == right.FoldVersion
}

func newWorkspace(config apphost.Config, store gitstore.Store) *Workspace {
	return &Workspace{
		genesis: config.Genesis, objectFormat: config.ObjectFormat,
		namespace: config.IdempotencyNamespace, sequencerKey: config.SequencerKey,
		store: store, reader: kernel.NewReader(store),
	}
}

// Append signs one act with the caller's key and gives it a position in the
// log. The returned record is what every reader of this repository will fold,
// including this one.
//
// Appending does not judge the act. A move out of turn, a join of a taken
// seat, an act whose schema means nothing at all: each is recorded exactly as
// signed, and the application's fold decides what force it has. Only the
// kernel's bounds and the signature can refuse an act here.
func (w *Workspace) Append(ctx context.Context, signer ed25519.PrivateKey, act Act) (Record, error) {
	if len(signer) != ed25519.PrivateKeySize {
		return Record{}, errors.New("signer must be an ed25519 private key")
	}
	if err := w.validateAct(act.Schema); err != nil {
		return Record{}, err
	}
	return w.append(ctx, signer, act.Schema, act.Payload, act.RestsOn, act.IdempotencyKey)
}

// Prepare binds an application act to this sequence and returns the exact
// canonical intent an actor must sign. It retains no draft and writes no Git
// object. When IdempotencyKey is empty it chooses one here, so a caller must
// retry with the returned PreparedAct rather than prepare the act again.
func (w *Workspace) Prepare(act Act) (PreparedAct, error) {
	if err := w.validateAct(act.Schema); err != nil {
		return PreparedAct{}, err
	}
	tree, err := gitstore.HashPayloadTree(w.objectFormat, act.Payload, nil)
	if err != nil {
		return PreparedAct{}, err
	}
	key := act.IdempotencyKey
	if key == "" {
		key, err = randomKey()
		if err != nil {
			return PreparedAct{}, err
		}
	}
	encoded, err := intent.Encode(intent.Intent{
		Version: intent.Version, Target: "git:" + w.objectFormat + ":" + w.genesis,
		Schema: act.Schema, PayloadTree: "git:" + w.objectFormat + ":" + tree,
		RestsOn: act.RestsOn, IdempotencyNS: w.namespace, IdempotencyKey: key,
	})
	if err != nil {
		return PreparedAct{}, err
	}
	return PreparedAct{Intent: encoded, Payload: bytes.Clone(act.Payload)}, nil
}

// ActorSigningBytes returns the exact domain-separated bytes an actor signs
// for Prepared. It accepts only a canonical kernel intent; malformed or
// non-canonical input is refused before any signature can be mistaken for a
// Gitseq act.
func ActorSigningBytes(prepared PreparedAct) ([]byte, error) {
	return intent.SigningBytes(prepared.Intent)
}

// AppendSigned verifies and sequences an act signed outside the host. It
// accepts only the sequence, namespace, schema and payload tree Prepare would
// have produced for this workspace. Rejected signatures and tampered drafts
// reach no Git write.
func (w *Workspace) AppendSigned(ctx context.Context, submission SignedAct) (Record, error) {
	signed := intent.Signed{
		Intent: bytes.Clone(submission.Prepared.Intent), ActorKey: bytes.Clone(submission.ActorKey),
		Signature: bytes.Clone(submission.ActorSignature),
	}
	decoded, err := intent.Verify(signed)
	if err != nil {
		return Record{}, err
	}
	if err := w.validatePrepared(decoded, submission.Prepared.Payload); err != nil {
		return Record{}, err
	}
	return w.submitSigned(ctx, signed, decoded, submission.Prepared.Payload)
}

func (w *Workspace) validateAct(schema string) error {
	if schema == "" {
		return errors.New("act schema is required")
	}
	if w.sequencerKey == "" {
		// A repository attached for reading holds no sequencer key. Saying so
		// here is better than letting the kernel refuse an unsigned position
		// and leaving the caller to work out which key was missing.
		return errors.New("repository is attached read-only: it holds no sequencer key to append with")
	}
	if schema == apphost.BindingSchema {
		// The binding is host vocabulary, not application vocabulary. Letting
		// an application write one through this path would put a record with
		// binding authority's schema in the log without the checks Init makes
		// about who signs it and where it stands.
		return fmt.Errorf("schema %q is reserved for the host binding", schema)
	}
	return nil
}

func (w *Workspace) validatePrepared(decoded intent.Intent, payload []byte) error {
	if err := w.validateAct(decoded.Schema); err != nil {
		return err
	}
	if decoded.Target != "git:"+w.objectFormat+":"+w.genesis {
		return errors.New("signed act targets a different sequence")
	}
	if decoded.IdempotencyNS != w.namespace {
		return errors.New("signed act uses a different idempotency namespace")
	}
	if decoded.EnvelopeVersion != 0 || len(decoded.CapabilityHash) != 0 {
		return errors.New("signed act uses host-unsupported envelope fields")
	}
	tree, err := gitstore.HashPayloadTree(w.objectFormat, payload, nil)
	if err != nil {
		return err
	}
	if decoded.PayloadTree != "git:"+w.objectFormat+":"+tree {
		return errors.New("signed act payload does not match its intent")
	}
	return nil
}

func (w *Workspace) append(ctx context.Context, signer ed25519.PrivateKey, schema string, payload []byte, restsOn []string, idempotencyKey string) (Record, error) {
	// The signed intent names the payload tree, so the tree's identity has to
	// be known before signing — but knowing it is not a reason to write it.
	// Computing the identity here and leaving the single write to the kernel
	// keeps the submit path's order intact: an act refused for its size, its
	// field bounds, or its signature leaves no unreachable objects behind, so
	// an open submit path cannot be turned into a way of filling a disk with
	// content nothing will ever reference.
	tree, err := gitstore.HashPayloadTree(w.objectFormat, payload, nil)
	if err != nil {
		return Record{}, err
	}
	if idempotencyKey == "" {
		idempotencyKey, err = randomKey()
		if err != nil {
			return Record{}, err
		}
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.objectFormat + ":" + w.genesis,
		Schema: schema, PayloadTree: "git:" + w.objectFormat + ":" + tree,
		RestsOn: restsOn, IdempotencyNS: w.namespace, IdempotencyKey: idempotencyKey,
	}, signer)
	if err != nil {
		return Record{}, err
	}
	return w.submitSigned(ctx, signed, intent.Intent{
		Version: intent.Version, Target: "git:" + w.objectFormat + ":" + w.genesis,
		Schema: schema, PayloadTree: "git:" + w.objectFormat + ":" + tree,
		RestsOn: restsOn, IdempotencyNS: w.namespace, IdempotencyKey: idempotencyKey,
	}, payload)
}

func (w *Workspace) submitSigned(ctx context.Context, signed intent.Signed, decoded intent.Intent, payload []byte) (Record, error) {
	w.mu.Lock()
	if w.submitter == nil {
		w.submitter = kernel.NewSubmitter(w.store, kernel.Options{SigningKey: w.sequencerKey, MaxQueueDepth: queueDepth})
	}
	submitter := w.submitter
	w.mu.Unlock()
	result, err := submitter.Submit(ctx, kernel.Request{Signed: signed, Payload: payload})
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID: w.eventID(result.Commit), Actor: actorFingerprint(signed.ActorKey),
		ActorKey: ed25519.PublicKey(signed.ActorKey), Schema: decoded.Schema, Payload: payload,
		RestsOn: decoded.RestsOn, Timestamp: result.Timestamp,
	}, nil
}

func (w *Workspace) bindingRequest(ctx context.Context, signer ed25519.PrivateKey, payload []byte, head string) (kernel.Request, error) {
	tree, err := gitstore.HashPayloadTree(w.objectFormat, payload, nil)
	if err != nil {
		return kernel.Request{}, err
	}
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version, Target: "git:" + w.objectFormat + ":" + w.genesis,
		Schema: apphost.BindingSchema, PayloadTree: "git:" + w.objectFormat + ":" + tree,
		IdempotencyNS: w.namespace, IdempotencyKey: "binding/" + head,
	}, signer)
	if err != nil {
		return kernel.Request{}, err
	}
	return kernel.Request{Signed: signed, Payload: payload}, nil
}

// Records verifies the sequence and returns every accepted record in order.
// Repeated calls verify only what has arrived since the last one.
//
// Verification is not optional and not cached across processes: the first call
// in a process reads and checks the whole log. A long log therefore costs a
// real audit at startup, which is the price of never presenting an unverified
// record as a fact.
func (w *Workspace) Records(ctx context.Context) (Log, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	loaded, err := w.reader.Load(ctx, w.genesis)
	if err != nil {
		return Log{}, err
	}
	switch {
	case loaded.Full:
		w.records = w.convert(loaded.Events)
	case w.records != nil && loaded.BaseHead == w.head:
		w.records = append(w.records, w.convert(loaded.Events)...)
	default:
		// The reader's frontier and the records held here disagree, so the
		// tail cannot be joined to anything. Replace the reader and read the
		// log again from the beginning rather than splice a gap.
		w.reader = kernel.NewReader(w.store)
		if loaded, err = w.reader.Load(ctx, w.genesis); err != nil {
			return Log{}, err
		}
		w.records = w.convert(loaded.Events)
	}
	w.head, w.depth = loaded.Verification.Head, loaded.Verification.Depth
	return Log{Genesis: w.genesis, Head: w.head, Depth: w.depth, Records: w.records}, nil
}

func (w *Workspace) convert(events []kernel.Event) []Record {
	records := make([]Record, 0, len(events))
	for _, event := range events {
		records = append(records, Record{
			ID: w.eventID(event.Commit), Actor: actorFingerprint(event.Signed.ActorKey),
			ActorKey: ed25519.PublicKey(event.Signed.ActorKey), Schema: event.Intent.Schema,
			Payload: event.Payload, RestsOn: event.Intent.RestsOn, Timestamp: event.Timestamp,
		})
	}
	return records
}

func (w *Workspace) eventID(commit string) string {
	return EventID(w.objectFormat, w.genesis, commit)
}

func randomKey() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
