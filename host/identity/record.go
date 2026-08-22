package identity

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/generalbusiness-ai/gitseq/host"
)

// DeclareWitness records the public half of a deployment's witnessing key.
//
// It must be signed by the key that initialized the repository — the actor on
// the log's first record — because that is the one authority a host can check
// without an application roster, and it is the same authority the host binding
// answers to. A witness declaration signed by anything else is recorded like
// any other record and resolves to nothing.
//
// Declaring again replaces: the declaration in force is the last one signed by
// the initializing key, so rotating the witness key, or adding a provider to
// its schemes, is one more record rather than a migration. Anchors the old key
// signed keep the force they had at the position they occupy, which is what
// makes rotation safe to do.
//
// The private half never comes here. Where it lives is the deployment's
// business, under a host posture in which every process in the trusted
// boundary can use it.
func DeclareWitness(ctx context.Context, workspace *host.Workspace, initializer ed25519.PrivateKey, witness ed25519.PublicKey, schemes []string) (host.Record, error) {
	if len(witness) != ed25519.PublicKeySize {
		return host.Record{}, errors.New("witness must be an ed25519 public key")
	}
	genesis, err := genesisOf(ctx, workspace)
	if err != nil {
		return host.Record{}, err
	}
	payload, err := encodeBody(WitnessDeclaration{
		Genesis: genesis,
		Key:     hex.EncodeToString(witness),
		Schemes: schemes,
	})
	if err != nil {
		return host.Record{}, err
	}
	return workspace.Append(ctx, initializer, host.Act{Schema: WitnessSchema, Payload: payload})
}

// Endorse records one anchor, signed by endorser.
//
// The same act carries every rung, and which one it is comes from signatures
// that verify rather than from anything the caller writes:
//
//   - Signed by the declared witness key, it is a witnessed anchor, and it
//     must name the identity a provider reported. This is the GitHub login
//     path: the deployment checked a token, and now says so in the log.
//   - Carrying a valid [NostrProof] and signed by its subject Gitseq key, it is
//     self-signed and in-log. The proof's public key becomes the identity; the
//     payload may not state another one.
//   - Signed by anything else, it is a delegation, and it must name no
//     identity. The endorser's own identity is inherited by the subject, and
//     an endorser with no anchor endorses nothing, because nobody can hand on
//     what they do not have. A new device and an agent credential are the same
//     act, and an agent credential is exactly as strong as the anchor that
//     minted it, on both axes.
//
// The payload shape and any carried cryptographic proof are checked before
// writing. Standing is not: an endorsement with no authority is recorded
// exactly as signed and resolves to nothing, which is the same posture the
// rest of the host takes. Signatures say who acted and what they endorsed, and
// the fold says what that was worth.
func Endorse(ctx context.Context, workspace *host.Workspace, endorser ed25519.PrivateKey, anchor Anchor) (host.Record, error) {
	genesis, err := genesisOf(ctx, workspace)
	if err != nil {
		return host.Record{}, err
	}
	// The repository is not the caller's to choose. Filling it in rather than
	// taking it means an endorsement cannot be built for one log and appended
	// to another.
	anchor.Genesis = genesis
	payload, err := encodeBody(anchor)
	if err != nil {
		return host.Record{}, err
	}
	return workspace.Append(ctx, endorser, host.Act{Schema: AnchorSchema, Payload: payload, RestsOn: nil})
}

// Revoke withdraws an endorsement before its expiry.
//
// It must be signed by the same Ed25519 key that signed the anchor, because
// withdrawing an endorsement is the endorser's act: the deployment takes back
// what its witness said, and a person takes back a device or an agent key. A
// self-signed Nostr root may instead use [RevokeNostr]. A revocation with no
// authority resolves to nothing.
//
// The withdrawal takes effect at the revocation's own position, so records
// already folded keep the identity they were folded with. A revoked key is
// provable from the log alone, with no server left to ask.
func Revoke(ctx context.Context, workspace *host.Workspace, endorser ed25519.PrivateKey, anchorRecord string) (host.Record, error) {
	genesis, err := genesisOf(ctx, workspace)
	if err != nil {
		return host.Record{}, err
	}
	payload, err := encodeBody(Revocation{Genesis: genesis, Anchor: anchorRecord})
	if err != nil {
		return host.Record{}, err
	}
	return workspace.Append(ctx, endorser, host.Act{Schema: RevokeSchema, Payload: payload})
}

// RevokeNostr withdraws a self-signed Nostr anchor with its root key's proof.
// Submitter signs the containing Gitseq record and need not be the anchored
// session key: losing that session key is one reason the persistent root must
// be able to withdraw it. Resolve admits the withdrawal only when the proof's
// public key is the same root that signed the named anchor.
func RevokeNostr(ctx context.Context, workspace *host.Workspace, submitter ed25519.PrivateKey, anchorRecord string, proof NostrProof) (host.Record, error) {
	genesis, err := genesisOf(ctx, workspace)
	if err != nil {
		return host.Record{}, err
	}
	payload, err := encodeBody(Revocation{Genesis: genesis, Anchor: anchorRecord, Nostr: &proof})
	if err != nil {
		return host.Record{}, err
	}
	return workspace.Append(ctx, submitter, host.Act{Schema: RevokeSchema, Payload: payload})
}

// genesisOf asks the workspace which log it is, which also means every act
// here is written against a log that verified first.
func genesisOf(ctx context.Context, workspace *host.Workspace) (string, error) {
	if workspace == nil {
		return "", errors.New("workspace is required")
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		return "", err
	}
	return log.Genesis, nil
}

func decodeWitnessKey(encoded string) (ed25519.PublicKey, error) {
	if encoded == "" {
		return nil, errors.New("witness key is required")
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("witness key is not hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("witness key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
