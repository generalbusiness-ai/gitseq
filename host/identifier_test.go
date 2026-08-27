package host_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/host"
)

func TestActorIdentifierContract(t *testing.T) {
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	digest := sha256.Sum256(key)
	want := hex.EncodeToString(digest[:])
	got, err := host.ActorFingerprint(key)
	if err != nil || got != want {
		t.Fatalf("ActorFingerprint(zero key) = %q, %v; want %q", got, err, want)
	}
	if host.ActorFingerprintLength != len(want) {
		t.Fatalf("ActorFingerprintLength = %d, want %d", host.ActorFingerprintLength, len(want))
	}

	for name, value := range map[string]string{
		"constructed":           got,
		"all lowercase digits":  strings.Repeat("0", 64),
		"all lowercase letters": strings.Repeat("a", 64),
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			if !host.ValidActorFingerprint(value) {
				t.Fatalf("ValidActorFingerprint(%q) = false", value)
			}
		})
	}
	for name, value := range map[string]string{
		"empty":         "",
		"short":         strings.Repeat("a", 63),
		"long":          strings.Repeat("a", 65),
		"uppercase":     strings.Repeat("A", 64),
		"non-hex":       strings.Repeat("g", 64),
		"hex prefix":    "0x" + strings.Repeat("a", 62),
		"leading space": " " + strings.Repeat("a", 63),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if host.ValidActorFingerprint(value) {
				t.Fatalf("ValidActorFingerprint(%q) = true", value)
			}
		})
	}
	for _, size := range []int{ed25519.PublicKeySize - 1, ed25519.PublicKeySize + 1} {
		if fingerprint, err := host.ActorFingerprint(make(ed25519.PublicKey, size)); err == nil || fingerprint != "" {
			t.Fatalf("ActorFingerprint(%d-byte key) = %q, %v; want refusal", size, fingerprint, err)
		}
	}
}

func TestEventIdentifierContract(t *testing.T) {
	sha1Genesis := strings.Repeat("a", 40)
	sha1Event := strings.Repeat("b", 40)
	sha1ID := "git:sha1:" + sha1Genesis + "#git:sha1:" + sha1Event
	if got := host.EventID("sha1", sha1Genesis, sha1Event); got != sha1ID {
		t.Fatalf("EventID(sha1) = %q, want %q", got, sha1ID)
	}
	sha256Genesis := strings.Repeat("c", 64)
	sha256Event := strings.Repeat("d", 64)
	sha256ID := "git:sha256:" + sha256Genesis + "#git:sha256:" + sha256Event
	if got := host.EventID("sha256", sha256Genesis, sha256Event); got != sha256ID {
		t.Fatalf("EventID(sha256) = %q, want %q", got, sha256ID)
	}

	for name, value := range map[string]string{
		"sha1":   sha1ID,
		"sha256": sha256ID,
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			if !host.ValidEventID(value) {
				t.Fatalf("ValidEventID(%q) = false", value)
			}
		})
	}
	for name, value := range map[string]string{
		"empty":                 "",
		"one object name":       "git:sha1:" + sha1Genesis,
		"extra part":            sha1ID + "#git:sha1:" + sha1Event,
		"wrong prefix":          "Git:sha1:" + sha1Genesis + "#git:sha1:" + sha1Event,
		"unsupported algorithm": "git:md5:" + strings.Repeat("a", 40) + "#git:md5:" + strings.Repeat("b", 40),
		"mismatched algorithms": "git:sha1:" + sha1Genesis + "#git:sha256:" + sha256Event,
		"short genesis":         "git:sha1:" + strings.Repeat("a", 39) + "#git:sha1:" + sha1Event,
		"long event":            "git:sha1:" + sha1Genesis + "#git:sha1:" + strings.Repeat("b", 41),
		"uppercase object id":   "git:sha1:" + strings.Repeat("A", 40) + "#git:sha1:" + sha1Event,
		"non-hex object id":     "git:sha1:" + strings.Repeat("g", 40) + "#git:sha1:" + sha1Event,
		"extra object segment":  "git:sha1:extra:" + sha1Genesis + "#git:sha1:" + sha1Event,
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			if host.ValidEventID(value) {
				t.Fatalf("ValidEventID(%q) = true", value)
			}
		})
	}
}
