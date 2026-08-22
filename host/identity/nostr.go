package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// NostrProof is the NIP-01 event returned by a NIP-07 signEvent call for a
// self-signed anchor or withdrawal. ID, PubKey and Sig use Nostr's lowercase
// hexadecimal encoding. Kind and Tags are deliberately fixed: Gitseq needs a
// portable signing envelope, not relay or application metadata.
type NostrProof struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int64      `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
	Sig       string     `json:"sig"`
}

// NostrProofKind is an ephemeral NIP-01 kind. Proofs live in the Gitseq log;
// they are not intended to be published to relays.
const NostrProofKind int64 = 20000

func (p NostrProof) validate() error {
	if _, err := lowerHex("nostr event id", p.ID, sha256.Size); err != nil {
		return err
	}
	publicKey, err := lowerHex("nostr public key", p.PubKey, 32)
	if err != nil {
		return err
	}
	if _, err := schnorr.ParsePubKey(publicKey); err != nil {
		return errors.New("nostr public key is not a valid secp256k1 key")
	}
	if _, err := lowerHex("nostr signature", p.Sig, schnorr.SignatureSize); err != nil {
		return err
	}
	if p.CreatedAt < 0 {
		return errors.New("nostr event time cannot be negative")
	}
	if p.Kind != NostrProofKind {
		return errors.New("nostr proof has the wrong event kind")
	}
	if p.Tags == nil || len(p.Tags) != 0 {
		return errors.New("nostr proof event tags must be an empty array")
	}
	return nil
}

// NostrDelegation returns the exact content of the NIP-01 event a Nostr root
// key signs to endorse an Anchor. A NIP-07 client passes that content, an empty
// tags array, [NostrProofKind] and any honest created_at to signEvent.
//
// The message is deterministic and binds every authority-bearing field. Query
// escaping prevents a scope chosen by an application from changing field
// boundaries. Nostr and Identity proof fields are deliberately excluded: they
// carry the proof and its derived answer rather than a condition of the grant.
func NostrDelegation(anchor Anchor) (string, error) {
	if anchor.Genesis == "" {
		return "", errors.New("anchor genesis is required")
	}
	if anchor.Subject == "" {
		return "", errors.New("anchor subject is required")
	}
	if err := boundedText("anchor subject", anchor.Subject, maxSubjectLen); err != nil {
		return "", err
	}
	if err := boundedText("anchor scope", anchor.Scope, maxScopeLen); err != nil {
		return "", err
	}
	if anchor.NotAfter < 0 {
		return "", errors.New("anchor expiry cannot be negative")
	}
	conditions := url.Values{
		"genesis":   []string{anchor.Genesis},
		"not_after": []string{strconv.FormatInt(anchor.NotAfter, 10)},
		"scope":     []string{anchor.Scope},
	}.Encode()
	return "nostr:delegation:" + anchor.Subject + ":" + conditions, nil
}

func validNostrProof(anchor Anchor) bool {
	message, err := NostrDelegation(anchor)
	if err != nil || anchor.Nostr == nil {
		return false
	}
	return validNostrEvent(message, *anchor.Nostr)
}

// NostrWithdrawal returns the exact content of the NIP-01 event a Nostr root
// key signs to withdraw one of its anchors.
func NostrWithdrawal(revocation Revocation) (string, error) {
	if revocation.Genesis == "" {
		return "", errors.New("revocation genesis is required")
	}
	if revocation.Anchor == "" {
		return "", errors.New("revocation names no anchor")
	}
	if err := boundedText("revoked anchor", revocation.Anchor, maxAnchorRefLen); err != nil {
		return "", err
	}
	conditions := url.Values{
		"genesis":   []string{revocation.Genesis},
		"operation": []string{"revoke"},
	}.Encode()
	return "nostr:delegation:" + revocation.Anchor + ":" + conditions, nil
}

func validNostrWithdrawal(revocation Revocation) bool {
	message, err := NostrWithdrawal(revocation)
	if err != nil || revocation.Nostr == nil {
		return false
	}
	return validNostrEvent(message, *revocation.Nostr)
}

func validNostrEvent(content string, proof NostrProof) bool {
	if proof.Content != content || proof.Kind != NostrProofKind || proof.Tags == nil || len(proof.Tags) != 0 {
		return false
	}
	id, err := nostrEventID(proof)
	if err != nil {
		return false
	}
	if hex.EncodeToString(id[:]) != proof.ID {
		return false
	}
	publicKey, err := hex.DecodeString(proof.PubKey)
	if err != nil {
		return false
	}
	key, err := schnorr.ParsePubKey(publicKey)
	if err != nil {
		return false
	}
	rawSignature, err := hex.DecodeString(proof.Sig)
	if err != nil {
		return false
	}
	signature, err := schnorr.ParseSignature(rawSignature)
	if err != nil {
		return false
	}
	return signature.Verify(id[:], key)
}

func nostrEventID(proof NostrProof) ([sha256.Size]byte, error) {
	var serialized bytes.Buffer
	encoder := json.NewEncoder(&serialized)
	// NIP-01 uses the JSON.stringify representation. Our constrained event is
	// ASCII, but its content contains '&'; disabling HTML escaping matches the
	// browser representation a NIP-07 extension hashes.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]any{0, proof.PubKey, proof.CreatedAt, proof.Kind, proof.Tags, proof.Content}); err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(bytes.TrimSuffix(serialized.Bytes(), []byte{'\n'})), nil
}

func lowerHex(what, value string, size int) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != size || hex.EncodeToString(decoded) != value {
		return nil, errors.New(what + " must be " + strconv.Itoa(size) + " bytes of lowercase hex")
	}
	return decoded, nil
}
