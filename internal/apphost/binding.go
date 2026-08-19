// Package apphost holds the application host layer: the repository state and
// the binding vocabulary that sit between the semantic-free kernel and any
// application profile. It answers one question before any record is folded —
// which application gives this repository's records their meaning — and it
// answers it without knowing what the applications are. Nothing here imports
// an application profile, which is what lets a program that has never heard of
// Workroom read a binding written by one. See docs/reference/architecture.md,
// "Application host binding".
package apphost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"strings"
	"unicode"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
)

const (
	// BindingSchema is the fixed binding family every host recognizes.
	// Application profiles cannot rename or extend it.
	BindingSchema = "gitseq/app-binding@0"

	// DefaultApplication is what an absent binding names, permanently. The
	// compatibility rule is fixed: no binding means workroom at the version
	// the reader ships.
	DefaultApplication = "workroom"
)

// Binding is a repository's declaration of the application that interprets it.
// SourceCommit is format-qualified (`git:sha1:<commit>`), never a bare hash.
// SourceURL is provenance and never authority: nothing here fetches it.
type Binding struct {
	Application  string `json:"application"`
	SourceCommit string `json:"source_commit,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	FoldVersion  string `json:"fold_version"`
}

// Validate reports whether a binding says enough to select an interpreter.
func (b Binding) Validate() error {
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

// Payload encodes a binding as the canonical bytes that are recorded and
// signed. DecodeBinding accepts exactly what this produces and nothing else.
func (b Binding) Payload() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

// DecodeBinding reads a recorded binding payload. It admits only the canonical
// encoding, so two different byte strings can never mean the same binding.
func DecodeBinding(payload []byte) (Binding, error) {
	var decoded Binding
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Binding{}, err
	}
	if decoder.More() {
		return Binding{}, errors.New("multiple JSON values")
	}
	if err := decoded.Validate(); err != nil {
		return Binding{}, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return Binding{}, err
	}
	if !bytes.Equal(canonical, payload) {
		return Binding{}, errors.New("binding payload is not canonical JSON")
	}
	return decoded, nil
}

// errFirstRecordUnauthenticated ends the binding scan when the log's first
// record does not authenticate. It never leaves BindingInForce.
var errFirstRecordUnauthenticated = errors.New("the log's first record does not authenticate")

// BindingInForce returns the binding in force at one exact revision, or nil
// when the history ending there declares none. A log that cannot be read yet —
// a repository attached before its objects arrive — is not an absent binding,
// so it reports an error and leaves the question open.
//
// The revision is the caller's, and naming it is what keeps the answer honest.
// A caller that has just verified a frontier passes that commit, so the binding
// it selects comes out of the history it verified rather than out of whatever
// the ref points at by the time this runs; a concurrent appender cannot slip a
// binding into the answer through a frontier nobody checked. A caller with no
// verified frontier passes the ref, and gets the binding as of whenever it
// looked.
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
func BindingInForce(ctx context.Context, store gitstore.Store, genesis, revision string) (*Binding, error) {
	if revision == "" {
		return nil, errors.New("binding read needs the revision to scan")
	}
	desc, err := kernel.Descriptor(ctx, store, genesis)
	if err != nil {
		return nil, err
	}
	target := "git:" + desc.ObjectFormat + ":" + genesis
	var (
		initializing []byte
		established  bool
		inForce      *Binding
	)
	err = store.WalkRevListMetadata(ctx, revision, func(commit gitstore.CommitMetadata) error {
		// Genesis and sequencer rotations carry no event envelope.
		signed, _, err := intent.ParseEnvelope(NormalizeEnvelope(commit.Message), desc.PayloadCeiling)
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
		if err != nil || declared.Schema != BindingSchema {
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
		payload, err := store.ReadFileLimit(ctx, commit.OID, "event", readLimit(desc.PayloadCeiling))
		if err != nil {
			return nil
		}
		decoded, err := DecodeBinding(payload)
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

// readLimit carries a genesis payload ceiling into the signed limit
// [gitstore.Store.ReadFileLimit] takes. The ceiling is unsigned and the limit
// is not, and a straight conversion of a ceiling above MaxInt64 goes negative:
// every binding record would then be passed over as unreadable, and a
// repository that initialized without complaint would open as bound to
// nothing. Clamping instead loses nothing, because no blob Git can store
// reaches MaxInt64 bytes, so the clamped limit refuses exactly what the
// ceiling refuses.
func readLimit(ceiling uint64) int64 {
	if ceiling > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(ceiling)
}

// NormalizeEnvelope matches how the kernel presents a commit message to the
// envelope parser, so a binding read admits exactly the envelopes the audit
// does.
func NormalizeEnvelope(message string) string {
	return strings.TrimRightFunc(message, unicode.IsSpace) + "\n"
}

// SourceCommit is the commit Go stamped into the running binary at build time,
// format-qualified. An unstamped build records no commit rather than a guess.
//
// A build from a dirty tree is not the commit it was built near, and a binding
// that named it would be a claim the binary cannot support, so that reports
// nothing too. The answer describes the main module of the running program,
// which is the application binary rather than this module.
func SourceCommit() string {
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
