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
	hub.Announce("alice", "ready")
	snapshot := hub.Snapshot()
	hub.Announce("bob", "ready")
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
	if _, _, err := before.OpenConversation(); err != nil {
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
	after.Announce("a", "1")
	after.Announce("b", "2")
	after.Announce("c", "3")
	if _, _, err := after.ChangesSince(initial); !errors.Is(err, ErrReset) {
		t.Fatalf("expired cursor should reset, got %v", err)
	}
}

func TestRetainedFramesVerifyWithoutHub(t *testing.T) {
	hub := newHub(t, 16)
	hub.Announce("session", "actor")
	conversationID, _, err := hub.OpenConversation()
	if err != nil {
		t.Fatal(err)
	}
	_, actorPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := hub.Publish(conversationID, []byte("offer"), actorPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Publish(conversationID, []byte("accept"), actorPrivateKey)
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
	attacker.Announce("session", "attacker")
	conversationID, _, err := attacker.OpenConversation()
	if err != nil {
		t.Fatal(err)
	}
	_, actorKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := attacker.Publish(conversationID, []byte("forged"), actorKey)
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
	hub.Announce("alice", "online")
	if _, _, err := hub.OpenConversation(); err != nil {
		t.Fatal(err)
	}
	if after := gitState(); before != after {
		t.Fatalf("ephemeral nexus changed Git state\nbefore: %s\nafter: %s", before, after)
	}
}

func TestPresenceLeaseExpiresAndForgetsConversations(t *testing.T) {
	hub := newHub(t, 16)
	hub.AnnounceFor("session", "actor", time.Hour)
	conversation, _, err := hub.OpenConversation()
	if err != nil {
		t.Fatal(err)
	}
	hub.Expire(time.Now().Add(2 * time.Hour))
	if snapshot := hub.Snapshot(); len(snapshot.Presence) != 0 || len(snapshot.Conversations) != 0 {
		t.Fatalf("expired live state retained: %+v", snapshot)
	}
	if _, err := hub.Frames(conversation); err == nil {
		t.Fatal("expired conversation frames remained available")
	}
}
