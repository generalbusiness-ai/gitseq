package nexus

import (
	"crypto/ed25519"
	"crypto/rand"
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

// A session identifier authorizes speech, so nothing an observer can read may
// contain one. This is the property, checked wherever presence is observable.
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
	if _, ok := snapshot.Presence[SessionHandle(secret)]; !ok {
		t.Fatalf("presence is not keyed by handle: %#v", snapshot.Presence)
	}

	// The change stream is the other way an observer sees presence.
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

	// Departure and expiry are announced too, and must be equally quiet.
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

// The handle must identify a session consistently, so observers can still
// follow renewals and departures, while telling them nothing they could use.
func TestSessionHandleIsStableDistinctAndOneWay(t *testing.T) {
	first, second := "mcp:aaa", "mcp:bbb"
	if SessionHandle(first) != SessionHandle(first) {
		t.Fatal("handle is not stable")
	}
	if SessionHandle(first) == SessionHandle(second) {
		t.Fatal("distinct sessions share a handle")
	}
	if strings.Contains(SessionHandle(first), first) {
		t.Fatal("handle contains the identifier it is meant to hide")
	}
	if SessionHandle("") != "" {
		t.Fatal("an absent session should yield an absent handle")
	}
}

// Knowing a handle must not be enough to act. Handles are published; if one
// could be presented where an identifier is expected, publishing them would
// simply move the disclosure rather than remove it.
func TestAHandleCannotBeUsedWhereAnIdentifierIsRequired(t *testing.T) {
	h := hub(t)
	const session = "mcp:private"
	if _, err := h.AnnounceSession(session, "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	handle := SessionHandle(session)

	if _, ok := h.SessionActor(handle); ok {
		t.Fatal("a handle resolved to an actor; it would authorize speech")
	}
	if _, err := h.PublishForSession(handle, "about", "", []byte(`{"text":"x"}`), privateKey(t)); err == nil {
		t.Fatal("a handle was accepted as a session and produced a signed frame")
	}
	// The real identifier still works for its own owner.
	if actor, ok := h.SessionActor(session); !ok || actor != "actor:me" {
		t.Fatalf("owner lost access to its own session: %q %v", actor, ok)
	}
}

// One session must not be able to end another's lease or speak in its name.
// Departing by handle is the shape an observer could attempt.
func TestOneSessionCannotEvictOrSpeakForAnother(t *testing.T) {
	h := hub(t)
	if _, err := h.AnnounceSession("mcp:mine", "actor:me", "me", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AnnounceSession("mcp:theirs", "actor:them", "them", time.Minute); err != nil {
		t.Fatal(err)
	}
	// What an observer of presence actually holds is a handle.
	h.Depart(SessionHandle("mcp:theirs"))
	if actor, ok := h.SessionActor("mcp:theirs"); !ok || actor != "actor:them" {
		t.Fatal("a handle was sufficient to evict another session's lease")
	}
	if _, err := h.PublishForSession(SessionHandle("mcp:theirs"), "about", "", []byte(`{"text":"x"}`), privateKey(t)); err == nil {
		t.Fatal("a handle was sufficient to speak in another session's name")
	}
}

// A live session belongs to one actor. Re-announcing it under another name
// must be refused, or an identifier that did leak could be repurposed.
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
