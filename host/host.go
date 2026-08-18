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
// The whole surface is four acts:
//
//	ws, err := host.Init(ctx, dir, app, initializer, host.Options{})
//	ws, err := host.Open(ctx, dir, app)
//	rec, err := ws.Append(ctx, signer, host.Act{Schema: "chess/move@0", Payload: encoded})
//	log, err := ws.Records(ctx)
//
// Init binds the repository to one application for life and Open refuses to
// hand back a repository bound to a different one. Append signs one act with
// the caller's key and gives it a position. Records returns the verified
// ordered records for the application to fold. There is no projection here,
// because the projection is the application's.
//
// # Binding
//
// A repository declares its application once, in its opening records, and that
// choice is permanent. Open reads the binding in force, checks it against what
// the running binary says it is, and refuses with [ErrUninterpretable] when
// they differ — the repository is still kernel-verifiable, and only its
// meaning is unavailable. A repository that declares no binding is a Workroom
// repository by the fixed compatibility rule, so an application other than
// Workroom refuses it too. Reading or recording a binding fetches, builds, and
// runs nothing.
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
// meaning of its records is unavailable here. Recovering the meaning takes the
// application at the bound version, not a repair.
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

// Init creates a sequence in an existing Git repository and binds it to one
// application, permanently. The binding is the log's first record, signed by
// initializer, and that key is the binding authority from then on: only it can
// record a replacement, and no application-level role can grant or revoke
// that. Keep it, or the repository can never be rebound.
//
// The repository must already exist as a Git repository and must not already
// hold a gitseq sequence.
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
	// The kernel speaks first.
	if _, err := workspace.Records(ctx); err != nil {
		return nil, err
	}
	recorded, err := apphost.BindingInForce(ctx, workspace.store, config.Genesis)
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
	if act.Schema == "" {
		return Record{}, errors.New("act schema is required")
	}
	if act.Schema == apphost.BindingSchema {
		// The binding is host vocabulary, not application vocabulary. Letting
		// an application write one through this path would put a record with
		// binding authority's schema in the log without the checks Init makes
		// about who signs it and where it stands.
		return Record{}, fmt.Errorf("schema %q is reserved for the host binding", act.Schema)
	}
	return w.append(ctx, signer, act.Schema, act.Payload, act.RestsOn, act.IdempotencyKey)
}

func (w *Workspace) append(ctx context.Context, signer ed25519.PrivateKey, schema string, payload []byte, restsOn []string, idempotencyKey string) (Record, error) {
	tree, err := w.store.WritePayloadTree(ctx, payload, nil)
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
	public := append(ed25519.PublicKey(nil), signed.ActorKey...)
	return Record{
		ID: w.eventID(result.Commit), Actor: intent.ActorFingerprint(signed.ActorKey), ActorKey: public,
		Schema: schema, Payload: payload, RestsOn: restsOn, Timestamp: result.Timestamp,
	}, nil
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
			ID: w.eventID(event.Commit), Actor: intent.ActorFingerprint(event.Signed.ActorKey),
			ActorKey: ed25519.PublicKey(event.Signed.ActorKey), Schema: event.Intent.Schema,
			Payload: event.Payload, RestsOn: event.Intent.RestsOn, Timestamp: event.Timestamp,
		})
	}
	return records
}

func (w *Workspace) eventID(commit string) string {
	return "git:" + w.objectFormat + ":" + w.genesis + "#git:" + w.objectFormat + ":" + commit
}

func randomKey() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
