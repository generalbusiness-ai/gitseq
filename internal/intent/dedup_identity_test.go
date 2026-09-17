package intent

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// The two spellings of the retry identity must agree exactly. One is used where
// the signing key is in hand; the other where only the roster fingerprint is.
// If they ever differ, a surface that asks "does this key already stand?" reads
// a different index from the one the kernel writes, and a retry that should
// replay is judged as a fresh act instead.
func TestDedupIdentityIsTheSameFromAKeyOrItsFingerprint(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct{ namespace, key string }{
		{"gs/alice", "retire-42"},
		{"", ""},
		{"ns with spaces", "key\x00with a separator"},
	} {
		fromKey := DedupIdentity("git:sha1:abc", public, probe.namespace, probe.key)
		fromFingerprint := DedupIdentityFor("git:sha1:abc", ActorFingerprint(public), probe.namespace, probe.key)
		if fromKey != fromFingerprint {
			t.Fatalf("identity from key = %q, from fingerprint = %q", fromKey, fromFingerprint)
		}
	}
}
