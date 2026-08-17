package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
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

	// openingRecords bounds the prefix of the log a binding may occupy. The
	// bootstrap position is the opening of the log, not a position anywhere in
	// it: a host that binds first records at index 0, and this one records the
	// operator's opening roster statement first because the workroom fold's
	// bootstrap grant is positional. Anything binding-shaped later has no
	// force, and is never read.
	openingRecords = 2
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

// hosts is the registry of interpreters this build holds, with workroom
// registered as the default. It is fixed for the life of the process: a build
// holds the applications it was compiled with, and nothing installs one at
// runtime.
var hosts = map[string]host{
	defaultApplication: {
		application: defaultApplication,
		foldVersion: workroom.ProfileVersion,
		newFolder:   workroom.NewFolder,
	},
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
		return selection{host: hosts[defaultApplication]}, nil
	}
	held, exists := hosts[recorded.Application]
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
	return hosts[defaultApplication].foldVersion
}

// readBinding returns the repository's binding, or nil when its log declares
// none. A log that cannot be read yet — a workroom attached before its objects
// arrive — is not an absent binding, so it reports an error and leaves the
// question open.
func (w *Workspace) readBinding(ctx context.Context) (*binding, error) {
	opening, err := kernel.ReadOpening(ctx, w.Store, w.Config.Genesis, openingRecords)
	if err != nil {
		return nil, err
	}
	var initializing []byte
	for index, event := range opening {
		if index == 0 {
			initializing = event.Signed.ActorKey
		}
		if event.Intent.Schema != bindingSchema {
			continue
		}
		// Binding authority is the key that initialized the repository: the
		// signer of the log's first record. It sits below application roles,
		// because another application has no roster to consult. A record that
		// merely resembles a binding has no force, so an unauthorized one is
		// passed over rather than raised: anyone able to append could
		// otherwise make a repository unreadable by naming it wrongly.
		if !bytes.Equal(event.Signed.ActorKey, initializing) {
			continue
		}
		decoded, err := decodeBinding(event.Payload)
		if err != nil {
			return nil, fmt.Errorf("binding record %s: %w", event.Commit, err)
		}
		return &decoded, nil
	}
	return nil, nil
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
	return w.signRequest(ctx, private, operatorName, bindingSchema, payload, nil, nil, "binding")
}
