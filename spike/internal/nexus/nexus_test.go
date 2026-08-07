package nexus

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gitseq/spike/internal/gitstore"
)

func newHub(t *testing.T, historyCap int) *Hub {
	t.Helper()
	hub, err := New(historyCap)
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func TestSnapshotWatchBarrierCannotMissTransition(t *testing.T) {
	hub := newHub(t, 16)
	if _, err := hub.AnnounceSession("alice", "alice", "ready", time.Hour); err != nil {
		t.Fatal(err)
	}
	snapshot := hub.Snapshot()
	if _, err := hub.AnnounceSession("bob", "bob", "ready", time.Hour); err != nil {
		t.Fatal(err)
	}
	changes, current, err := hub.ChangesSince(snapshot.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Presence["alice"] != "ready" || snapshot.Presence["bob"] != "" {
		t.Fatalf("bad snapshot: %#v", snapshot.Presence)
	}
	if len(changes) != 1 || changes[0].ID != "bob" || current.Position != snapshot.Cursor.Position+1 {
		t.Fatalf("transition was missed: %#v, current %#v", changes, current)
	}
}

func TestCrashChangesGenerationAndOldCursorResets(t *testing.T) {
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	before, err := NewWithSigningKey(2, signingKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.AnnounceSession("session", "actor", "actor", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := before.PublishForSession("session", "topic", "", []byte("live"), actorKey); err != nil {
		t.Fatal(err)
	}
	cursor := before.Snapshot().Cursor
	after, err := NewWithSigningKey(2, signingKey)
	if err != nil {
		t.Fatal(err)
	}
	if before.Generation() == after.Generation() {
		t.Fatal("new process generation unexpectedly reused")
	}
	if string(before.PublicKey()) != string(after.PublicKey()) {
		t.Fatal("process restart changed the durably anchored issuer identity")
	}
	if conversations := after.Snapshot().Conversations; len(conversations) != 0 {
		t.Fatalf("new process retained live conversations: %#v", conversations)
	}
	if _, _, err := after.ChangesSince(cursor); !errors.Is(err, ErrReset) {
		t.Fatalf("old generation should reset, got %v", err)
	}

	initial := after.Snapshot().Cursor
	for _, id := range []string{"a", "b", "c"} {
		if _, err := after.AnnounceSession(id, id, id, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := after.ChangesSince(initial); !errors.Is(err, ErrReset) {
		t.Fatalf("expired cursor should reset, got %v", err)
	}
}

func TestRetainedFramesVerifyWithoutHub(t *testing.T) {
	hub := newHub(t, 16)
	if _, err := hub.AnnounceSession("session", "actor", "actor", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, actorPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hub.PublishForSession("session", "topic", "", []byte("offer"), actorPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.PublishForSession("session", "topic", first.Conversation, []byte("accept"), actorPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	retained := []Frame{first, second}
	trustedNexusKey := hub.PublicKey()
	hub = nil // the retained transcript is independently verifiable after loss.
	if err := VerifyChain(retained, trustedNexusKey); err != nil {
		t.Fatal(err)
	}
	retained[1].Payload[0] ^= 1
	if err := VerifyChain(retained, trustedNexusKey); err == nil {
		t.Fatal("tampered retained frame verified")
	}
}

func TestSelfAssertedNexusKeyIsNotTrust(t *testing.T) {
	trusted := newHub(t, 4)
	attacker := newHub(t, 4)
	if _, err := attacker.AnnounceSession("session", "attacker", "attacker", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, actorKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := attacker.PublishForSession("session", "topic", "", []byte("forged"), actorKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFrame(frame, trusted.PublicKey()); err == nil {
		t.Fatal("a frame carrying its own untrusted nexus key verified")
	}
}

func TestNexusDoesNotTouchGit(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir() + "/repo.git"
	if _, err := gitstore.InitBare(ctx, repo, "sha1"); err != nil {
		t.Fatal(err)
	}
	gitState := func() string {
		commands := [][]string{
			{"--git-dir", repo, "count-objects", "-v"},
			{"--git-dir", repo, "for-each-ref", "--format=%(refname) %(objectname)"},
		}
		var state strings.Builder
		for _, args := range commands {
			output, err := exec.Command("git", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("git state: %v: %s", err, output)
			}
			state.Write(output)
		}
		return state.String()
	}
	before := gitState()
	hub := newHub(t, 8)
	if _, err := hub.AnnounceSession("alice", "alice", "online", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := hub.PublishForSession("alice", "topic", "", []byte("live"), actorKey); err != nil {
		t.Fatal(err)
	}
	if after := gitState(); before != after {
		t.Fatalf("ephemeral nexus changed Git state\nbefore: %s\nafter: %s", before, after)
	}
}

func TestPresenceLeaseExpiresAndForgetsConversations(t *testing.T) {
	hub := newHub(t, 16)
	if _, err := hub.AnnounceSession("session", "actor", "actor", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	frame, err := hub.PublishForSession("session", "topic", "", []byte("live"), actorKey)
	if err != nil {
		t.Fatal(err)
	}
	conversation := frame.Conversation
	hub.Expire(time.Now().Add(2 * time.Hour))
	if snapshot := hub.Snapshot(); len(snapshot.Presence) != 0 || len(snapshot.Conversations) != 0 {
		t.Fatalf("expired live state retained: %+v", snapshot)
	}
	if _, err := hub.Frames(conversation); err == nil {
		t.Fatal("expired conversation frames remained available")
	}
}

func TestSessionBindingAndConversationRetentionHaveOneOwner(t *testing.T) {
	hub := newHub(t, 32)
	if _, err := hub.AnnounceSession("one", "alice", "Alice", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.AnnounceSession("one", "mallory", "Mallory", time.Hour); err == nil {
		t.Fatal("live session rebound to another actor")
	}
	if _, err := hub.AnnounceSession("two", "bob", "Bob", time.Hour); err != nil {
		t.Fatal(err)
	}
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	first, err := hub.PublishForSession("one", "topic", "", []byte("one"), aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.PublishForSession("two", "topic", "", []byte("two"), bobKey)
	if err != nil {
		t.Fatal(err)
	}
	if first.Conversation != second.Conversation {
		t.Fatal("about anchor opened two conversations")
	}
	hub.Depart("one")
	if frames, err := hub.Frames(first.Conversation); err != nil || len(frames) != 2 {
		t.Fatalf("conversation forgot while a participant remained: frames=%d err=%v", len(frames), err)
	}
	hub.Depart("two")
	if _, err := hub.Frames(first.Conversation); err == nil {
		t.Fatal("conversation survived its last participant")
	}
}
