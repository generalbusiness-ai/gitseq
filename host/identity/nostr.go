package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// NostrProof is a self-signed Nostr authorization carried by an anchor or
// withdrawal.
// PublicKey is the 32-byte x-only secp256k1 key used by Nostr, and Signature
// is a 64-byte BIP-340 signature. Both use lowercase hexadecimal encoding.
//
// The proof signs SHA-256([NostrDelegation]) rather than a Nostr event. Its
// domain-separated shape follows NIP-26, while its conditions are Gitseq's own
// repository, scope and expiry contract; it carries no relay or event meaning.
type NostrProof struct {
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

func (p NostrProof) validate() error {
	publicKey, err := lowerHex("nostr public key", p.PublicKey, 32)
	if err != nil {
		return err
	}
	if _, err := schnorr.ParsePubKey(publicKey); err != nil {
		return errors.New("nostr public key is not a valid secp256k1 key")
	}
	if _, err := lowerHex("nostr signature", p.Signature, schnorr.SignatureSize); err != nil {
		return err
	}
	return nil
}

// NostrDelegation returns the exact string a Nostr root key signs to endorse
// an Anchor. The caller signs its SHA-256 digest with BIP-340 Schnorr.
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
	return validNostrSignature(message, *anchor.Nostr)
}

// NostrWithdrawal returns the exact string a Nostr root key signs to withdraw
// one of its anchors. The caller signs its SHA-256 digest with BIP-340 Schnorr.
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
	return validNostrSignature(message, *revocation.Nostr)
}

func validNostrSignature(message string, proof NostrProof) bool {
	publicKey, err := hex.DecodeString(proof.PublicKey)
	if err != nil {
		return false
	}
	key, err := schnorr.ParsePubKey(publicKey)
	if err != nil {
		return false
	}
	rawSignature, err := hex.DecodeString(proof.Signature)
	if err != nil {
		return false
	}
	signature, err := schnorr.ParseSignature(rawSignature)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(message))
	return signature.Verify(digest[:], key)
}

func lowerHex(what, value string, size int) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != size || hex.EncodeToString(decoded) != value {
		return nil, errors.New(what + " must be " + strconv.Itoa(size) + " bytes of lowercase hex")
	}
	return decoded, nil
}
