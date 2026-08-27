package host

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/intent"
)

// ActorFingerprintLength is the exact number of lowercase hexadecimal characters
// in an actor fingerprint.
const ActorFingerprintLength = sha256.Size * 2

// ActorFingerprint returns the full lowercase fingerprint of an Ed25519
// actor key.
func ActorFingerprint(key ed25519.PublicKey) (string, error) {
	if len(key) != ed25519.PublicKeySize {
		return "", errors.New("invalid actor public key")
	}
	return actorFingerprint(key), nil
}

func actorFingerprint(key []byte) string { return intent.ActorFingerprint(key) }

// ValidActorFingerprint reports whether value is one full lowercase actor
// fingerprint.
func ValidActorFingerprint(value string) bool {
	if len(value) != ActorFingerprintLength {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

// EventID constructs the canonical identifier for one event in a sequence.
// The object format and raw object identifiers come from the verified Git
// repository; use ValidEventID at an untrusted string boundary.
func EventID(objectFormat, genesis, event string) string {
	return "git:" + objectFormat + ":" + genesis + "#git:" + objectFormat + ":" + event
}

// ValidEventID reports whether value contains exactly two canonical Git object
// names, for the same supported object format, separated by one hash mark.
func ValidEventID(value string) bool {
	parts := strings.Split(value, "#")
	if len(parts) != 2 {
		return false
	}
	genesisFormat, genesisOK := validEventObjectName(parts[0])
	eventFormat, eventOK := validEventObjectName(parts[1])
	return genesisOK && eventOK && genesisFormat == eventFormat
}

func validEventObjectName(value string) (string, bool) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "git" {
		return "", false
	}
	want := 0
	switch parts[1] {
	case "sha1":
		want = 40
	case "sha256":
		want = 64
	default:
		return "", false
	}
	if len(parts[2]) != want {
		return "", false
	}
	for _, character := range parts[2] {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", false
		}
	}
	return parts[1], true
}
