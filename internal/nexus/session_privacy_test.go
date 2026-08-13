package nexus

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func privateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func hub(t *testing.T) *Hub {
	t.Helper()
	h, err := New(64)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A session identifier authorizes speech and durable acts, so nothing an
// observer can read may contain one.
func TestSessionIdentifiersAreNeverPublished(t *testing.T) {
	h := hub(t)
	const secret = "mcp:0123456789abcdef-secret"
	if _, err := h.AnnounceSession(secret, "actor:me", "me (fingerprint)", time.Minute); err != nil {
		t.Fatal(err)
	}
	snapshot := h.Snapshot()
	for key, value := range snapshot.Presence {
		if strings.Contains(key, secret) || strings.Contains(value, secret) {
			t.Fatalf("snapshot discloses the session identifier: %q -> %q", key, value)
		}
	}
	if _, ok := snapshot.Presence[h.HandleFor(secret)]; !ok {
		t.Fatalf("presence is not keyed by the minted handle: %#v", snapshot.Presence)
	}
	changes, _, err := h.ChangesSince(Cursor{Generation: h.Generation()})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("expected a presence change")
	}
	for _, change := range changes {
		if strings.Contains(change.ID, secret) || strings.Contains(change.Value, secret) {
			t.Fatalf("change stream discloses the session identifier: %+v", change)
		}
	}
	h.Depart(secret)
	departures, _, err := h.ChangesSince(Cursor{Generation: h.Generation()})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range departures {
		if strings.Contains(change.ID, secret) {
			t.Fatalf("departure discloses the session identifier: %+v", change)
		}
	}
}

// The handle must not be derivable from the identifier. A derived handle is an
// oracle: an observer guesses a candidate identifier, computes the handle, and
// a match confirms the guess — after which the identifier can simply be used.
// The service does not constrain how much entropy a client puts in its session,
// so the derivation would only ever be as strong as the weakest client.
func TestHandleIsNotDerivableFromTheSessionIdentifier(t *testing.T) {
	h := hub(t)
	const guessable = "alice"
	if _, err := h.AnnounceSession(guessable, "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	handle := h.HandleFor(guessable)
	if handle == "" {
		t.Fatal("no handle was minted")
	}
	// Any construction an attacker could try offline must fail to reproduce it.
	for name, candidate := range map[string]string{
		"plain sha256":  "session:" + hex.EncodeToString(sum(guessable)[:8]),
		"full sha256":   "session:" + hex.EncodeToString(sum(guessable)),
		"domain-tagged": "session:" + hex.EncodeToString(sum("gitseq.nexus.session-handle.v0\x00" + guessable)[:8]),
	} {
		if handle == candidate {
			t.Fatalf("handle is reproducible offline via %s; it is an oracle for guessing the identifier", name)
		}
	}
	// Two sessions with the same identifier text in different hubs must differ,
	// which a derivation could never provide.
	other := hub(t)
	if _, err := other.AnnounceSession(guessable, "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	if other.HandleFor(guessable) == handle {
		t.Fatal("the same identifier produced the same handle twice; it is derived, not minted")
	}
}

// A handle stays put for the life of a lease, or observers could not follow a
// renewal.
func TestHandleIsStableAcrossRenewals(t *testing.T) {
	h := hub(t)
	if _, err := h.AnnounceSession("mcp:one", "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	first := h.HandleFor("mcp:one")
	if _, err := h.AnnounceSession("mcp:one", "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	if h.HandleFor("mcp:one") != first {
		t.Fatal("the handle moved across a renewal")
	}
}

// Knowing a handle must not be enough to act anywhere.
func TestAHandleCannotBeUsedWhereAnIdentifierIsRequired(t *testing.T) {
	h := hub(t)
	const session = "mcp:private"
	if _, err := h.AnnounceSession(session, "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	handle := h.HandleFor(session)
	if _, ok := h.SessionActor(handle); ok {
		t.Fatal("a handle resolved to an actor; it would authorize speech and durable acts")
	}
	if _, err := h.PublishForSession(handle, "about", "", []byte(`{"text":"x"}`), privateKey(t)); err == nil {
		t.Fatal("a handle was accepted as a session and produced a signed frame")
	}
	if actor, ok := h.SessionActor(session); !ok || actor != "actor:me" {
		t.Fatalf("owner lost access to its own session: %q %v", actor, ok)
	}
}

func TestOneSessionCannotEvictOrSpeakForAnother(t *testing.T) {
	h := hub(t)
	if _, err := h.AnnounceSession("mcp:mine", "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnnounceSession("mcp:theirs", "actor:them", "them", time.Minute); err != nil {
		t.Fatal(err)
	}
	theirs := h.HandleFor("mcp:theirs")
	h.Depart(theirs)
	if actor, ok := h.SessionActor("mcp:theirs"); !ok || actor != "actor:them" {
		t.Fatal("a handle was sufficient to evict another session's lease")
	}
	if _, err := h.PublishForSession(theirs, "about", "", []byte(`{"text":"x"}`), privateKey(t)); err == nil {
		t.Fatal("a handle was sufficient to speak in another session's name")
	}
}

func TestALiveSessionCannotBeReboundToAnotherActor(t *testing.T) {
	h := hub(t)
	if _, err := h.AnnounceSession("mcp:one", "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnnounceSession("mcp:one", "actor:them", "them", time.Minute); err == nil {
		t.Fatal("a live session was rebound to a different actor")
	}
	if actor, _ := h.SessionActor("mcp:one"); actor != "actor:me" {
		t.Fatalf("binding moved to %q", actor)
	}
}

func TestLiveSessionCountUsesTheFullFingerprint(t *testing.T) {
	h := hub(t)
	sharedPrefix := strings.Repeat("a", 12)
	if _, err := h.AnnounceSessionIdentity("mcp:one", "alias-one", sharedPrefix+"1", "one", time.Minute, ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnnounceSessionIdentity("mcp:two", "alias-two", sharedPrefix+"2", "two", time.Minute, ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	if got := h.LiveSessionsForActor(sharedPrefix + "1"); got != 1 {
		t.Fatalf("sessions for the first full fingerprint = %d, want 1", got)
	}
	if got := h.LiveSessionsForActor(sharedPrefix); got != 0 {
		t.Fatalf("a fingerprint prefix matched %d sessions", got)
	}
}

func sum(text string) []byte {
	digest := sha256.Sum256([]byte(text))
	return digest[:]
}
