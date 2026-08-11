package nexus

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/gitstore"
)

func newHub(t *testing.T, historyCap int) *Hub {
	t.Helper()
	hub, err := New(historyCap)
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func announceIdentity(t *testing.T, hub *Hub, session, name string, key ed25519.PrivateKey) string {
	t.Helper()
	fingerprint := actorFingerprint(key.Public().(ed25519.PublicKey))
	if _, err := hub.AnnounceSessionIdentity(session, name, fingerprint, name, time.Hour, ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	if err := hub.EnableInbox(session); err != nil {
		t.Fatal(err)
	}
	return fingerprint
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
	// Presence is published by handle; the identifiers stay private.
	if snapshot.Presence[hub.HandleFor("alice")] != "ready" || snapshot.Presence[hub.HandleFor("bob")] != "" {
		t.Fatalf("bad snapshot: %#v", snapshot.Presence)
	}
	if len(changes) != 1 || changes[0].ID != hub.HandleFor("bob") || current.Position != snapshot.Cursor.Position+1 {
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

func TestLeasedActivityIsBoundedOwnedAndPropagatesThroughTheCursor(t *testing.T) {
	hub := newHub(t, 32)
	announced, err := hub.AnnounceSession("mine", "actor:me", "me", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	baseline := announced.Cursor
	status := ActivityBlocked
	focus := []string{"event:z", "event:a", "event:a"}
	note := "  waiting on review  "
	changed, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{
		Status: &status, Focus: &focus, Note: &note,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Kind != "activity" || changed.Cursor.Position != baseline.Position+1 || changed.Activity == nil {
		t.Fatalf("activity transition = %+v", changed)
	}
	handle := hub.HandleFor("mine")
	wantFocus := []string{"event:a", "event:z"}
	got := hub.Snapshot().Activity[handle]
	if got.Status != ActivityBlocked || got.Note != "waiting on review" || !activityEqual(got, Activity{Status: ActivityBlocked, Focus: wantFocus, Note: "waiting on review"}) {
		t.Fatalf("normalized activity = %+v", got)
	}
	got.Focus[0] = "event:mutated-by-snapshot-caller"
	fresh := hub.Snapshot().Activity[handle]
	if !activityEqual(fresh, Activity{Status: ActivityBlocked, Focus: wantFocus, Note: "waiting on review"}) {
		t.Fatalf("snapshot caller mutated hub activity: %+v", fresh)
	}
	got = fresh
	changes, _, err := hub.ChangesSince(baseline)
	if err != nil || len(changes) != 1 || changes[0].Activity == nil || changes[0].Activity.Status != ActivityBlocked {
		t.Fatalf("activity was not carried by wait: changes=%+v err=%v", changes, err)
	}

	// A heartbeat with no activity fields preserves the state and does not
	// advance the live cursor.
	renewed, err := hub.AnnounceSession("mine", "actor:me", "me", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Kind != "renewal" || renewed.Cursor != changed.Cursor || renewed.Activity == nil || !activityEqual(*renewed.Activity, got) || !activityEqual(hub.Snapshot().Activity[handle], got) {
		t.Fatalf("renewal reset or republished activity: %+v", renewed)
	}

	// The public handle is not a session credential, and a live private
	// session cannot be rebound to update another actor's activity.
	busy := ActivityBusy
	separate, err := hub.AnnounceSessionActivity(handle, "actor:me", "me", time.Hour, ActivityUpdate{Status: &busy})
	if err != nil {
		t.Fatal(err)
	}
	if separate.ID == handle || hub.HandleFor("mine") != handle || hub.Snapshot().Activity[handle].Status != ActivityBlocked {
		t.Fatal("a separately announced lease mutated the original private session")
	}
	if _, err := hub.AnnounceSessionActivity("mine", "actor:them", "them", time.Hour, ActivityUpdate{Status: &busy}); err == nil {
		t.Fatal("another actor rebound a live session to update activity")
	}

	beforeExpiry := hub.Snapshot().Cursor
	hub.Expire(time.Now().Add(2 * time.Hour))
	snapshot := hub.Snapshot()
	if len(snapshot.Presence) != 0 || len(snapshot.Activity) != 0 {
		t.Fatalf("expired lease retained presence or focus: %+v", snapshot)
	}
	expired, _, err := hub.ChangesSince(beforeExpiry)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range expired {
		found = found || change.Kind == "expiration" && change.ID == handle
	}
	if !found {
		t.Fatalf("expiry did not propagate through the live cursor: %+v", expired)
	}
}

func TestLeasedActivityRejectsUnboundedOrInvalidInput(t *testing.T) {
	if MaxFocusEvents != 8 {
		t.Fatalf("MaxFocusEvents = %d, want 8", MaxFocusEvents)
	}
	if MaxActivityNoteBytes != 160 {
		t.Fatalf("MaxActivityNoteBytes = %d, want 160", MaxActivityNoteBytes)
	}
	hub := newHub(t, 8)
	if _, err := hub.AnnounceSession("mine", "actor:me", "me", time.Hour); err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, MaxFocusEvents+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("event:%d", index)
	}
	if _, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{Focus: &tooMany}); err == nil {
		t.Fatal("unbounded focus was accepted")
	}
	invalid := ActivityStatus("finished")
	if _, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{Status: &invalid}); err == nil {
		t.Fatal("unknown status was accepted")
	}
	note := strings.Repeat("x", MaxActivityNoteBytes+1)
	if _, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{Note: &note}); err == nil {
		t.Fatal("unbounded note was accepted")
	}
	oversizedEvent := []string{strings.Repeat("x", 257)}
	if _, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{Focus: &oversizedEvent}); err == nil {
		t.Fatal("257-byte focus event was accepted")
	}
	invalidUTF8 := []string{string([]byte{0xff})}
	if _, err := hub.AnnounceSessionActivity("mine", "actor:me", "me", time.Hour, ActivityUpdate{Focus: &invalidUTF8}); err == nil {
		t.Fatal("invalid UTF-8 focus event was accepted")
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

func TestAddressedMessagesAreSignedDeliveredRepeatedAndAcknowledgedPerSession(t *testing.T) {
	hub := newHub(t, 64)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	alice := announceIdentity(t, hub, "alice-web", "Alice", aliceKey)
	announceIdentity(t, hub, "alice-cli", "Alice", aliceKey)
	bob := announceIdentity(t, hub, "bob-one", "Bob", bobKey)
	announceIdentity(t, hub, "bob-two", "Bob", bobKey)
	baseline := hub.Snapshot().Cursor

	delivery, err := hub.PublishMessageForSession("alice-web", "", Message{
		About: "event:one", Text: "please review", Recipients: []string{bob, bob},
	}, aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := hub.Frames(delivery.Conversation)
	if err != nil || len(frames) != 1 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	var signed Message
	if err := json.Unmarshal(frames[0].Payload, &signed); err != nil || len(signed.Recipients) != 1 || signed.Recipients[0] != bob {
		t.Fatalf("signed payload = %+v err=%v", signed, err)
	}
	thread := delivery.Conversation + ":" + strconv.FormatUint(delivery.Sequence, 10)
	for _, session := range []string{"bob-one", "bob-two"} {
		_, inbox, err := hub.SnapshotForSession(session)
		if err != nil || len(inbox.Frames) != 1 || inbox.Frames[0].Thread != thread || inbox.Frames[0].Actor != alice {
			t.Fatalf("%s inbox = %+v err=%v", session, inbox, err)
		}
		changes, _, err := hub.ChangesSinceForSession(baseline, session)
		var addressed *InboxFrame
		for _, change := range changes {
			if change.Frame != nil {
				addressed = change.Frame
			}
		}
		if err != nil || addressed == nil || addressed.Thread != thread {
			t.Fatalf("%s addressed delta = %+v err=%v", session, changes, err)
		}
	}
	if _, inbox, err := hub.SnapshotForSession("alice-web"); err != nil || len(inbox.Frames) != 0 {
		t.Fatalf("publisher received its own session delivery: %+v err=%v", inbox, err)
	}
	if _, inbox, err := hub.SnapshotForSession("alice-cli"); err != nil || len(inbox.Frames) != 0 {
		t.Fatalf("unaddressed sibling received a delivery: %+v err=%v", inbox, err)
	}
	hub.Depart("alice-web")
	if frames, err := hub.Frames(delivery.Conversation); err != nil || len(frames) != 1 {
		t.Fatalf("sender departure discarded a recipient-retained conversation: frames=%d err=%v", len(frames), err)
	}
	if removed, err := hub.Acknowledge("bob-one", []string{thread, thread}); err != nil || removed != 1 {
		t.Fatalf("ack removed %d frames: %v", removed, err)
	}
	if _, inbox, _ := hub.SnapshotForSession("bob-one"); len(inbox.Frames) != 0 {
		t.Fatalf("acknowledged inbox = %+v", inbox)
	}
	if _, inbox, _ := hub.SnapshotForSession("bob-two"); len(inbox.Frames) != 1 {
		t.Fatalf("one session's ack changed another: %+v", inbox)
	}
}

func TestAddressedConversationSurvivesForLiveRecipientWithoutInboxCapability(t *testing.T) {
	hub := newHub(t, 16)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	announceIdentity(t, hub, "alice", "Alice", aliceKey)
	bob := actorFingerprint(bobKey.Public().(ed25519.PublicKey))
	if _, err := hub.AnnounceSessionIdentity("bob-browser", "Bob", bob, "Bob", time.Hour, ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}

	frame, err := hub.PublishMessageForSession("alice", "", Message{
		About: "event:browser-retention", Text: "please review", Recipients: []string{bob},
	}, aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	hub.Depart("alice")
	if frames, err := hub.Frames(frame.Conversation); err != nil || len(frames) != 1 {
		t.Fatalf("sender departure discarded a live legacy recipient's conversation: frames=%d err=%v", len(frames), err)
	}
	hub.Depart("bob-browser")
	if _, err := hub.Frames(frame.Conversation); err == nil {
		t.Fatal("conversation survived its final live recipient")
	}
}

func TestSelfAddressDeliversOnlyToSiblingSessionAndOpaqueFramesDoNotAddress(t *testing.T) {
	hub := newHub(t, 16)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	alice := announceIdentity(t, hub, "alice-one", "Alice", aliceKey)
	announceIdentity(t, hub, "alice-two", "Alice", aliceKey)
	frame, err := hub.PublishMessageForSession("alice-one", "", Message{About: "topic", Text: "note to self", Recipients: []string{alice}}, aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	thread := frame.Conversation + ":" + strconv.FormatUint(frame.Sequence, 10)
	if _, inbox, _ := hub.SnapshotForSession("alice-one"); len(inbox.Frames) != 0 {
		t.Fatalf("publishing session received itself: %+v", inbox)
	}
	if _, inbox, _ := hub.SnapshotForSession("alice-two"); len(inbox.Frames) != 1 || inbox.Frames[0].Thread != thread {
		t.Fatalf("sibling session missed self-address: %+v", inbox)
	}
	if _, err := hub.PublishForSession("alice-one", "opaque", "", []byte(`{"recipients":["`+alice+`"]}`), aliceKey); err != nil {
		t.Fatal(err)
	}
	if _, inbox, _ := hub.SnapshotForSession("alice-two"); len(inbox.Frames) != 1 {
		t.Fatalf("opaque frame invented a delivery: %+v", inbox)
	}
}

func TestPresenceWithoutInboxCapabilityIsNeverEnqueued(t *testing.T) {
	hub := newHub(t, 16)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, browserKey, _ := ed25519.GenerateKey(rand.Reader)
	announceIdentity(t, hub, "alice", "Alice", aliceKey)
	browserFingerprint := actorFingerprint(browserKey.Public().(ed25519.PublicKey))
	if _, err := hub.AnnounceSessionIdentity("browser", "Browser", browserFingerprint, "Browser", time.Hour, ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.PublishMessageForSession("alice", "", Message{About: "topic", Text: "first", Recipients: []string{browserFingerprint}}, aliceKey); err != nil {
		t.Fatal(err)
	}
	if _, inbox, err := hub.SnapshotForSession("browser"); err != nil || len(inbox.Frames) != 0 {
		t.Fatalf("presence-only session was enqueued: %+v err=%v", inbox, err)
	}
	if err := hub.EnableInbox("browser"); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.PublishMessageForSession("alice", "", Message{About: "topic", Text: "second", Recipients: []string{browserFingerprint}}, aliceKey); err != nil {
		t.Fatal(err)
	}
	if _, inbox, err := hub.SnapshotForSession("browser"); err != nil || len(inbox.Frames) != 1 || inbox.Frames[0].Text != "second" {
		t.Fatalf("registered session inbox = %+v err=%v", inbox, err)
	}
}

func TestReplyAddsExactParentAuthorAndRejectsWrongThread(t *testing.T) {
	hub := newHub(t, 32)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	alice := announceIdentity(t, hub, "alice", "Alice", aliceKey)
	announceIdentity(t, hub, "bob", "Bob", bobKey)
	first, err := hub.PublishMessageForSession("alice", "", Message{About: "topic", Text: "question"}, aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	firstThread := first.Conversation + ":" + strconv.FormatUint(first.Sequence, 10)
	reply, err := hub.PublishMessageForSession("bob", first.Conversation, Message{About: "topic", Text: "answer", Re: firstThread}, bobKey)
	if err != nil {
		t.Fatal(err)
	}
	var replyMessage Message
	if err := json.Unmarshal(reply.Payload, &replyMessage); err != nil || len(replyMessage.Recipients) != 1 || replyMessage.Recipients[0] != alice {
		t.Fatalf("reply payload = %+v err=%v", replyMessage, err)
	}
	replyThread := reply.Conversation + ":" + strconv.FormatUint(reply.Sequence, 10)
	if _, inbox, _ := hub.SnapshotForSession("alice"); len(inbox.Frames) != 1 || inbox.Frames[0].Thread != replyThread {
		t.Fatalf("reply did not reach parent author: %+v", inbox)
	}
	if _, err := hub.PublishMessageForSession("bob", first.Conversation, Message{About: "topic", Text: "bad", Re: first.Conversation + ":99"}, bobKey); err == nil {
		t.Fatal("missing reply target was accepted")
	}
	if _, err := hub.PublishMessageForSession("bob", first.Conversation, Message{About: "other", Text: "bad", Re: firstThread}, bobKey); err == nil {
		t.Fatal("reply crossed an about anchor")
	}
}

func TestReplyToCompatibilityFrameUsesItsSignedActorKey(t *testing.T) {
	hub := newHub(t, 16)
	_, leasedKey, _ := ed25519.GenerateKey(rand.Reader)
	_, actualKey, _ := ed25519.GenerateKey(rand.Reader)
	_, replierKey, _ := ed25519.GenerateKey(rand.Reader)
	announceIdentity(t, hub, "lease", "Leased", leasedKey)
	announceIdentity(t, hub, "replier", "Replier", replierKey)
	parent, err := hub.PublishForSession("lease", "topic", "", []byte("opaque"), actualKey)
	if err != nil {
		t.Fatal(err)
	}
	thread := parent.Conversation + ":" + strconv.FormatUint(parent.Sequence, 10)
	reply, err := hub.PublishMessageForSession("replier", parent.Conversation, Message{About: "topic", Text: "reply", Re: thread}, replierKey)
	if err != nil {
		t.Fatal(err)
	}
	var signed Message
	if err := json.Unmarshal(reply.Payload, &signed); err != nil {
		t.Fatal(err)
	}
	want := actorFingerprint(actualKey.Public().(ed25519.PublicKey))
	if !reflect.DeepEqual(signed.Recipients, []string{want}) {
		t.Fatalf("reply recipients = %#v, want signed parent actor %s", signed.Recipients, want)
	}
}

func TestAddressedInboxIsBoundedAndExpiresWithItsLease(t *testing.T) {
	hub := newHub(t, 128)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	bob := announceIdentity(t, hub, "bob", "Bob", bobKey)
	announceIdentity(t, hub, "alice", "Alice", aliceKey)
	for index := 0; index < MaxInboxFrames+3; index++ {
		if _, err := hub.PublishMessageForSession("alice", "", Message{
			About: "topic", Text: fmt.Sprintf("message %d", index), Recipients: []string{bob},
		}, aliceKey); err != nil {
			t.Fatal(err)
		}
	}
	_, inbox, err := hub.SnapshotForSession("bob")
	if err != nil || len(inbox.Frames) != MaxInboxFrames || inbox.Skipped != 3 {
		t.Fatalf("bounded inbox = %+v err=%v", inbox, err)
	}
	handles := make([]string, 0, len(inbox.Frames))
	for _, frame := range inbox.Frames {
		handles = append(handles, frame.Thread)
	}
	if removed, err := hub.Acknowledge("bob", handles); err != nil || removed != MaxInboxFrames {
		t.Fatalf("page ack removed %d: %v", removed, err)
	}
	if _, next, err := hub.SnapshotForSession("bob"); err != nil || len(next.Frames) != 3 || next.Skipped != 0 {
		t.Fatalf("ack did not reveal hidden pending frames: %+v err=%v", next, err)
	}
	hub.Expire(time.Now().Add(2 * time.Hour))
	if _, _, err := hub.SnapshotForSession("bob"); err == nil {
		t.Fatal("expired session retained an addressable inbox")
	}
	announceIdentity(t, hub, "bob", "Bob", bobKey)
	if _, fresh, err := hub.SnapshotForSession("bob"); err != nil || len(fresh.Frames) != 0 || fresh.Skipped != 0 {
		t.Fatalf("reused session retained old delivery state: %+v err=%v", fresh, err)
	}
}

func TestObserveResetStillReturnsCurrentPrivateInbox(t *testing.T) {
	hub := newHub(t, 4)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	bob := announceIdentity(t, hub, "bob", "Bob", bobKey)
	announceIdentity(t, hub, "alice", "Alice", aliceKey)
	if _, err := hub.PublishMessageForSession("alice", "", Message{About: "topic", Text: "hello", Recipients: []string{bob}}, aliceKey); err != nil {
		t.Fatal(err)
	}
	old := Cursor{Generation: "generation:gone", Position: 99}
	observed, err := hub.Observe("bob", &old)
	if err != nil || !observed.Reset || len(observed.Inbox.Frames) != 1 || observed.Snapshot.Cursor.Generation != hub.Generation() {
		t.Fatalf("reset observation = %+v err=%v", observed, err)
	}
}

func TestPublishBoundsFailBeforeOpeningOrIndexingConversation(t *testing.T) {
	hub := newHub(t, 8)
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	announceIdentity(t, hub, "actor", "Actor", actorKey)
	hub.retainedFrames = MaxRetainedFrames
	before := hub.Snapshot()
	if _, err := hub.PublishMessageForSession("actor", "", Message{About: "new-topic", Text: "blocked"}, actorKey); err == nil {
		t.Fatal("full room accepted a typed message")
	}
	if _, err := hub.PublishForSession("actor", "raw-topic", "", []byte("blocked"), actorKey); err == nil {
		t.Fatal("full room accepted an opaque message")
	}
	after := hub.Snapshot()
	if !reflect.DeepEqual(before, after) || len(hub.about) != 0 || len(hub.convs) != 0 {
		t.Fatalf("failed publication mutated live state: before=%+v after=%+v about=%d convs=%d", before, after, len(hub.about), len(hub.convs))
	}
}

func TestMessageAndFrameBoundsFailClosed(t *testing.T) {
	fingerprint := strings.Repeat("a", sha256.Size*2)
	recipients := make([]string, MaxMessageRecipients+1)
	for index := range recipients {
		recipients[index] = fingerprint
	}
	for _, testCase := range []struct {
		name    string
		message Message
	}{
		{name: "empty text", message: Message{About: "topic"}},
		{name: "oversize text", message: Message{About: "topic", Text: strings.Repeat("x", MaxMessageTextBytes+1)}},
		{name: "invalid text", message: Message{About: "topic", Text: string([]byte{0xff})}},
		{name: "oversize about", message: Message{About: strings.Repeat("x", MaxMessageIDBytes+1), Text: "hello"}},
		{name: "oversize reply", message: Message{About: "topic", Text: "hello", Re: strings.Repeat("x", MaxMessageIDBytes+1)}},
		{name: "too many recipients", message: Message{About: "topic", Text: "hello", Recipients: recipients}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateMessage(testCase.message); err == nil {
				t.Fatal("invalid message was accepted")
			}
		})
	}

	hub := newHub(t, 8)
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	announceIdentity(t, hub, "actor", "Actor", actorKey)
	before := hub.Snapshot()
	if _, err := hub.PublishForSession("actor", "topic", "", make([]byte, MaxFramePayloadBytes+1), actorKey); err == nil {
		t.Fatal("oversize opaque frame was accepted")
	}
	if after := hub.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("oversize frame mutated the room: before=%+v after=%+v", before, after)
	}
}

func TestLiveSessionCountsAreBoundedPerActor(t *testing.T) {
	hub := newHub(t, MaxSessionsPerActor+2)
	_, actorKey, _ := ed25519.GenerateKey(rand.Reader)
	fingerprint := actorFingerprint(actorKey.Public().(ed25519.PublicKey))
	for index := 0; index < MaxSessionsPerActor; index++ {
		if _, err := hub.AnnounceSessionIdentity(fmt.Sprintf("session-%d", index), "Actor", fingerprint, "Actor", time.Hour, ActivityUpdate{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := hub.AnnounceSessionIdentity("one-too-many", "Actor", fingerprint, "Actor", time.Hour, ActivityUpdate{}); err == nil {
		t.Fatal("per-actor live session cap was not enforced")
	}
}

func TestGlobalLiveSessionLimitIsEnforced(t *testing.T) {
	hub := newHub(t, MaxLiveSessions+2)
	for index := 0; index < MaxLiveSessions; index++ {
		name := fmt.Sprintf("actor-%d", index)
		if _, err := hub.AnnounceSessionIdentity(name, name, name, name, time.Hour, ActivityUpdate{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := hub.AnnounceSessionIdentity("overflow", "overflow", "overflow", "overflow", time.Hour, ActivityUpdate{}); err == nil {
		t.Fatal("global live session cap was not enforced")
	}
}

func TestRecipientInboxCapacityRefusesWithoutPublishing(t *testing.T) {
	hub := newHub(t, MaxPendingInboxFrames+8)
	_, aliceKey, _ := ed25519.GenerateKey(rand.Reader)
	_, bobKey, _ := ed25519.GenerateKey(rand.Reader)
	bob := announceIdentity(t, hub, "bob", "Bob", bobKey)
	announceIdentity(t, hub, "alice", "Alice", aliceKey)
	for index := 0; index < MaxPendingInboxFrames; index++ {
		if _, err := hub.PublishMessageForSession("alice", "", Message{About: "topic", Text: fmt.Sprintf("message %d", index), Recipients: []string{bob}}, aliceKey); err != nil {
			t.Fatal(err)
		}
	}
	before := hub.Snapshot()
	if _, err := hub.PublishMessageForSession("alice", "", Message{About: "new-topic", Text: "overflow", Recipients: []string{bob}}, aliceKey); err == nil {
		t.Fatal("full recipient inbox accepted another frame")
	}
	after := hub.Snapshot()
	if !reflect.DeepEqual(before, after) || len(hub.convs) != 1 {
		t.Fatalf("refused inbox publication mutated the room: before=%+v after=%+v convs=%d", before, after, len(hub.convs))
	}
}
