package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"unicode"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The host layer sits between the kernel and the application profiles. It
// answers one question before any record is folded: which application gives
// this repository's records their meaning. Reading the binding first is what
// keeps a reader from interpreting a log with the wrong application and
// repairing the projection afterwards. See docs/reference/architecture.md,
// "Application host binding".

const (
	// bindingSchema is the fixed binding family every host recognizes.
	// Application profiles cannot rename or extend it.
	bindingSchema = "gitseq/app-binding@0"

	// defaultApplication is what an absent binding names, permanently. The
	// compatibility rule is fixed: no binding means workroom at the version
	// the reader ships.
	defaultApplication = "workroom"
)

// binding is a repository's declaration of the application that interprets it.
// Source commit is format-qualified (`git:sha1:<commit>`), never a bare hash.
// Source URL is provenance and never authority: nothing here fetches it.
type binding struct {
	Application  string `json:"application"`
	SourceCommit string `json:"source_commit,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	FoldVersion  string `json:"fold_version"`
}

// host is one application interpreter this build holds. newFolder is the
// selection's whole point: the host layer chooses the fold, once, at open.
type host struct {
	application string
	foldVersion string
	newFolder   func([]workroom.Record) *workroom.Folder
}

// workroomHost is the interpreter this build holds, and the one an absent
// binding names. A build holds the applications it was compiled with, and
// nothing installs one at runtime.
var workroomHost = host{
	application: defaultApplication,
	foldVersion: workroom.ProfileVersion,
	newFolder:   workroom.NewFolder,
}

// heldHost answers whether this build can interpret one named application.
// There is exactly one, so this is the whole registry. A map would be an index
// over a single entry, and a misleading one: newFolder is spelled in Workroom's
// own types, so no second application could be registered through it anyway.
// The second application arrives with the interpreter type that can hold it.
func heldHost(application string) (host, bool) {
	if application == workroomHost.application {
		return workroomHost, true
	}
	return host{}, false
}

// selection is one resolved answer to "which interpreter". A refusal is part
// of the answer rather than a failure to answer: a repository bound to an
// application this build does not hold stays kernel-verifiable, and only its
// application meaning is unavailable.
type selection struct {
	host host
	err  error
}

// selectHost reads the binding and selects the interpreter. The returned error
// means the log could not be read at all, which is not an answer and must not
// be cached; a refusal to interpret is carried in the selection instead.
func (w *Workspace) selectHost(ctx context.Context) (selection, error) {
	recorded, err := w.readBinding(ctx)
	if err != nil {
		return selection{}, err
	}
	if recorded == nil {
		return selection{host: workroomHost}, nil
	}
	held, exists := heldHost(recorded.Application)
	if !exists {
		return selection{err: fmt.Errorf("repository is verifiable but uninterpretable: it is bound to application %q, which this build does not hold", recorded.Application)}, nil
	}
	if held.foldVersion != recorded.FoldVersion {
		return selection{err: fmt.Errorf("repository is verifiable but uninterpretable: it is bound to %s fold %q, and this build holds %q", recorded.Application, recorded.FoldVersion, held.foldVersion)}, nil
	}
	return selection{host: held}, nil
}

// interpreter returns the selected fold, selecting it first if opening the
// workspace could not. Concurrent callers may both select; they read the same
// log position and reach the same answer.
func (w *Workspace) interpreter(ctx context.Context) (host, error) {
	if resolved := w.selected.Load(); resolved != nil {
		return resolved.host, resolved.err
	}
	resolved, err := w.selectHost(ctx)
	if err != nil {
		return host{}, err
	}
	w.selected.Store(&resolved)
	return resolved.host, resolved.err
}

// foldProfile names the fold that produced local checkpoints, so a checkpoint
// can never be reused across interpreters. Before selection resolves it names
// the default; that is a cache key, and a wrong one costs a cold audit rather
// than a wrong projection, because folding always selects first.
func (w *Workspace) foldProfile() string {
	if resolved := w.selected.Load(); resolved != nil && resolved.err == nil {
		return resolved.host.foldVersion
	}
	return workroomHost.foldVersion
}

// errFirstRecordUnauthenticated ends the binding scan when the log's first
// record does not authenticate. It never leaves readBinding.
var errFirstRecordUnauthenticated = errors.New("the log's first record does not authenticate")

// readBinding returns the binding in force, or nil when the log declares none.
// A log that cannot be read yet — a workroom attached before its objects
// arrive — is not an absent binding, so it reports an error and leaves the
// question open.
//
// The binding in force is the last binding record signed by the key that
// initialized the repository: the actor key on the log's first record. The
// bootstrap binding and a later replacement are therefore one rule, not two —
// scan the log in order and keep the last record that qualifies. Binding
// authority sits below application roles, because another application has no
// roster to consult.
//
// This is a bounded pre-audit read, not a verification. It authenticates
// exactly what binding authority rests on: the initializing actor's signature
// over an intent that names this genesis and matches the tree the commit
// carries. It does not re-verify the sequencer chain, because nothing is
// folded on its authority — the kernel audit runs before any record is folded,
// and a chain that does not verify loses its projection there rather than to a
// mis-selected interpreter.
//
// Anything that does not qualify is passed over rather than raised. An
// unauthorized, unparseable, or malformed binding-shaped record has no force
// and leaves the previous answer standing, so nobody able to append can make a
// repository unreadable by recording one.
func (w *Workspace) readBinding(ctx context.Context) (*binding, error) {
	desc, err := kernel.Descriptor(ctx, w.Store, w.Config.Genesis)
	if err != nil {
		return nil, err
	}
	target := "git:" + desc.ObjectFormat + ":" + w.Config.Genesis
	var (
		initializing []byte
		established  bool
		inForce      *binding
	)
	err = w.Store.WalkRevListMetadata(ctx, kernel.Ref(w.Config.Genesis), func(commit gitstore.CommitMetadata) error {
		// Genesis and sequencer rotations carry no event envelope.
		signed, _, err := intent.ParseEnvelope(normalizeEnvelope(commit.Message), desc.PayloadCeiling)
		if err != nil {
			return nil
		}
		if !established {
			if _, err := intent.Verify(signed); err != nil {
				return errFirstRecordUnauthenticated
			}
			initializing = signed.ActorKey
			established = true
		}
		declared, err := intent.Decode(signed.Intent)
		if err != nil || declared.Schema != bindingSchema {
			return nil
		}
		if !bytes.Equal(signed.ActorKey, initializing) {
			return nil
		}
		if _, err := intent.Verify(signed); err != nil {
			return nil
		}
		// The intent must name this log, and the commit must carry the tree
		// the actor signed, or the payload read below is not what was signed.
		if declared.Target != target {
			return nil
		}
		_, tree, err := gitstore.ParseTypedOID(declared.PayloadTree)
		if err != nil || tree != commit.Tree {
			return nil
		}
		payload, err := w.Store.ReadFileLimit(ctx, commit.OID, "event", int64(desc.PayloadCeiling))
		if err != nil {
			return nil
		}
		decoded, err := decodeBinding(payload)
		if err != nil {
			return nil
		}
		inForce = &decoded
		return nil
	})
	if errors.Is(err, errFirstRecordUnauthenticated) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return inForce, nil
}

// normalizeEnvelope matches how the kernel presents a commit message to the
// envelope parser, so this read admits exactly the envelopes the audit does.
func normalizeEnvelope(message string) string {
	return strings.TrimRightFunc(message, unicode.IsSpace) + "\n"
}

func decodeBinding(payload []byte) (binding, error) {
	var decoded binding
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return binding{}, err
	}
	if decoder.More() {
		return binding{}, errors.New("multiple JSON values")
	}
	if err := decoded.validate(); err != nil {
		return binding{}, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return binding{}, err
	}
	if !bytes.Equal(canonical, payload) {
		return binding{}, errors.New("binding payload is not canonical JSON")
	}
	return decoded, nil
}

func (b binding) validate() error {
	if b.Application == "" {
		return errors.New("binding application is required")
	}
	if b.FoldVersion == "" {
		return errors.New("binding fold version is required")
	}
	if b.SourceCommit != "" {
		if _, _, err := gitstore.ParseTypedOID(b.SourceCommit); err != nil {
			return fmt.Errorf("binding source commit: %w", err)
		}
	}
	return nil
}

// selfBinding is what this build says it is: the host it runs, the fold that
// host holds, and the commit Go stamped in at build time. An unstamped build
// records no commit rather than a guess, and no build knows the URL it was
// cloned from, so provenance stays empty until something can state it truly.
func selfBinding(running host) binding {
	return binding{Application: running.application, SourceCommit: sourceCommit(), FoldVersion: running.foldVersion}
}

func sourceCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var system, revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs":
			system = setting.Value
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	// A build from a dirty tree is not the commit it was built near, and a
	// binding that named it would be a claim the binary cannot support.
	if system != "git" || modified == "true" {
		return ""
	}
	switch len(revision) {
	case 40:
		return "git:sha1:" + revision
	case 64:
		return "git:sha256:" + revision
	}
	return ""
}

// buildBindingRequest signs the repository's binding with the initializing
// key. Recording a binding runs no application code and fetches nothing.
func (w *Workspace) buildBindingRequest(ctx context.Context, private ed25519.PrivateKey, operatorName string, recorded binding) (kernel.Request, error) {
	if err := recorded.validate(); err != nil {
		return kernel.Request{}, err
	}
	payload, err := json.Marshal(recorded)
	if err != nil {
		return kernel.Request{}, err
	}
	// Recording a binding is an act at a position in the log, so that is what
	// the idempotency key names. Retrying one submission is the same act and
	// appends once; binding again later is a new act whatever it names. A
	// fixed key would let a repository record only its first binding ever, and
	// a key over the binding's own bytes would silently swallow the rollback
	// that re-records an earlier binding — reporting success while leaving the
	// binding it replaced in force.
	head, err := w.Store.Head(ctx, kernel.Ref(w.Config.Genesis))
	if err != nil {
		return kernel.Request{}, err
	}
	return w.signRequest(ctx, private, operatorName, bindingSchema, payload, nil, nil, "binding/"+head)
}
