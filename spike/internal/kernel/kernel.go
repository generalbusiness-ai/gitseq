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

	"github.com/fxamacker/cbor/v2"

	"gitseq/spike/internal/gitstore"
	"gitseq/spike/internal/intent"
)

const genesisMarker = "gitseq-genesis-v0"

var ErrIdempotencyConflict = errors.New("idempotency key reused with different intent")

type GenesisDescriptor struct {
	_                  struct{} `cbor:",toarray"`
	Version            uint64
	ObjectFormat       string
	PayloadCeiling     uint64
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
}

type Options struct {
	SigningKey string
	Failpoint  func(string)
	MaxRetries int
	PreAppend  func(context.Context, Admission) error
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
	Intent      intent.Intent
	Signed      intent.Signed
	PayloadTree string
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
	if desc.Version != 0 || (desc.ObjectFormat != "sha1" && desc.ObjectFormat != "sha256") || desc.PayloadCeiling == 0 || desc.SequencerPublicKey == "" {
		return nil, errors.New("invalid genesis descriptor")
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
	return desc, nil
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

func Ref(genesis string) string { return "refs/seq/" + genesis }

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
	inlineSize := uint64(len(request.Payload))
	for _, attachment := range request.Attachments {
		size := uint64(len(attachment))
		if inlineSize > desc.PayloadCeiling || size > desc.PayloadCeiling-inlineSize {
			return Result{}, errors.New("payload exceeds genesis ceiling")
		}
		inlineSize += size
	}
	if inlineSize > desc.PayloadCeiling {
		return Result{}, errors.New("payload exceeds genesis ceiling")
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
	for attempt := 0; attempt < maxRetries; attempt++ {
		index, err := rebuildDedup(ctx, store, ref)
		if err != nil {
			return Result{}, err
		}
		key, err := request.Signed.DedupKey()
		if err != nil {
			return Result{}, err
		}
		if prior, ok := index[key]; ok {
			if !prior.Signed.Equal(request.Signed) {
				return Result{}, ErrIdempotencyConflict
			}
			return Result{Commit: prior.Commit, Head: prior.Commit, Replay: true, CASRetries: attempt}, nil
		}
		head, err := store.Head(ctx, ref)
		if err != nil {
			return Result{}, err
		}
		message := intent.Envelope(request.Signed, decoded.RestsOn)
		actorID := intent.ActorFingerprint(request.Signed.ActorKey)
		commit, err := store.SignedCommit(ctx, writtenTree, head, message, options.SigningKey, gitstore.CommitIdentity{
			AuthorName: "actor " + actorID[:16], AuthorEmail: actorID[:16] + "@actor.gitseq.invalid",
			CommitterName: "gitseq sequencer", CommitterEmail: "sequencer@gitseq.invalid",
		})
		if err != nil {
			return Result{}, err
		}
		fail(options, "after_commit_written")
		fail(options, "before_ref_cas")
		if err := store.UpdateRef(ctx, ref, commit, head); err != nil {
			current, headErr := store.Head(ctx, ref)
			if headErr == nil && current != head {
				continue
			}
			return Result{}, err
		}
		fail(options, "after_ref_cas")
		fail(options, "before_reply")
		return Result{Commit: commit, Head: commit, CASRetries: attempt}, nil
	}
	return Result{}, errors.New("CAS retry limit exceeded")
}

// rebuildDedup re-reads and re-verifies the whole chain on every submit. That
// O(history) cost is the one-shot cold-failover model, not the production
// shape: a resident sequencer keeps this index warm and pays it once.
func rebuildDedup(ctx context.Context, store gitstore.Store, ref string) (map[string]Event, error) {
	commits, err := store.RevList(ctx, ref)
	if err != nil {
		return nil, err
	}
	index := make(map[string]Event)
	for _, commit := range commits[1:] {
		message, err := store.CommitMessage(ctx, commit)
		if err != nil {
			return nil, err
		}
		signed, _, err := intent.ParseEnvelope(message)
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", commit, err)
		}
		decoded, err := intent.Verify(signed)
		if err != nil {
			return nil, err
		}
		key, err := signed.DedupKey()
		if err != nil {
			return nil, err
		}
		index[key] = Event{Commit: commit, Intent: decoded, Signed: signed, PayloadTree: decoded.PayloadTree}
	}
	return index, nil
}

type Verification struct {
	Genesis string
	Head    string
	Depth   int
	Events  int
}

func Verify(ctx context.Context, store gitstore.Store, genesis string) (Verification, error) {
	ref := Ref(genesis)
	commits, err := store.RevList(ctx, ref)
	if err != nil {
		return Verification{}, err
	}
	if len(commits) == 0 || commits[0] != genesis {
		return Verification{}, errors.New("chain does not begin at named genesis")
	}
	genesisMessage, err := store.CommitMessage(ctx, genesis)
	if err != nil {
		return Verification{}, err
	}
	desc, err := parseGenesisMessage(genesisMessage)
	if err != nil {
		return Verification{}, err
	}
	for index, commit := range commits {
		if err := store.VerifySSHCommit(ctx, commit, "sequencer", desc.SequencerPublicKey); err != nil {
			return Verification{}, fmt.Errorf("commit %s sequencer signature: %w", commit, err)
		}
		parents, err := store.CommitParents(ctx, commit)
		if err != nil {
			return Verification{}, err
		}
		if index == 0 {
			if len(parents) != 0 {
				return Verification{}, errors.New("genesis has a parent")
			}
			continue
		}
		if len(parents) != 1 || parents[0] != commits[index-1] {
			return Verification{}, fmt.Errorf("commit %s is not single-parent chained", commit)
		}
		message, err := store.CommitMessage(ctx, commit)
		if err != nil {
			return Verification{}, err
		}
		signed, trailers, err := intent.ParseEnvelope(message)
		if err != nil {
			return Verification{}, err
		}
		decoded, err := intent.Verify(signed)
		if err != nil {
			return Verification{}, err
		}
		if decoded.Target != "git:"+desc.ObjectFormat+":"+genesis {
			return Verification{}, errors.New("intent target does not name chain genesis")
		}
		if !intent.EqualRefs(decoded.RestsOn, trailers) {
			return Verification{}, errors.New("causal trailers differ from signed intent")
		}
		_, treeOID, err := gitstore.ParseTypedOID(decoded.PayloadTree)
		if err != nil {
			return Verification{}, err
		}
		actualTree, err := store.CommitTree(ctx, commit)
		if err != nil {
			return Verification{}, err
		}
		if actualTree != treeOID {
			return Verification{}, errors.New("commit tree differs from signed intent")
		}
		if err := store.ValidatePayloadTree(ctx, actualTree, desc.PayloadCeiling); err != nil {
			return Verification{}, fmt.Errorf("commit %s payload shape: %w", commit, err)
		}
	}
	return Verification{Genesis: genesis, Head: commits[len(commits)-1], Depth: len(commits) - 1, Events: len(commits) - 1}, nil
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
