package live_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/host/live"
)

func deterministicKey(index int) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(fmt.Sprintf("live-contract-key-%d", index)))
	return ed25519.NewKeyFromSeed(seed[:])
}

func openPublicSession(t *testing.T, runtime *live.Hub, name string, key ed25519.PrivateKey, update live.ActivityUpdate) string {
	t.Helper()
	credential, _, err := runtime.OpenSession(name, key.Public().(ed25519.PublicKey), name, time.Minute, update)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func submitPublicFrame(t *testing.T, runtime *live.Hub, credential, scope, conversation string, payload []byte, key ed25519.PrivateKey) live.Frame {
	t.Helper()
	public := key.Public().(ed25519.PublicKey)
	draft, err := runtime.PrepareFrameForSession(credential, scope, conversation, payload, public)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, err := live.ActorSigningBytes(draft)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(key, signingBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func submitPublicMessage(t *testing.T, runtime *live.Hub, credential, conversation string, message live.Message, key ed25519.PrivateKey) live.Frame {
	t.Helper()
	public := key.Public().(ed25519.PublicKey)
	draft, err := runtime.PrepareMessageForSession(credential, conversation, message, public)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, err := live.ActorSigningBytes(draft)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.SubmitMessageForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(key, signingBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestPublicLeaseLifecycleAndGenerationReset(t *testing.T) {
	_, issuer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	actor := deterministicKey(1)
	first, err := live.NewWithSigningKey(8, issuer)
	if err != nil {
		t.Fatal(err)
	}
	credential := openPublicSession(t, first, "actor", actor, live.ActivityUpdate{})
	handle := first.HandleFor(credential)
	if handle == "" || handle == credential {
		t.Fatalf("public handle %q did not remain separate from the credential", handle)
	}
	if _, err := first.RenewSession(credential, "actor", actor.Public().(ed25519.PublicKey), "actor", time.Minute, live.ActivityUpdate{}); err != nil {
		t.Fatal(err)
	}
	oldCursor := first.Snapshot().Cursor

	second, err := live.NewWithSigningKey(8, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation() == second.Generation() {
		t.Fatal("restart reused the live generation")
	}
	if _, _, err := second.ChangesSince(oldCursor); !errors.Is(err, live.ErrReset) {
		t.Fatalf("old cursor = %v, want ErrReset", err)
	}
	if _, err := second.RenewSession(credential, "actor", actor.Public().(ed25519.PublicKey), "actor", time.Minute, live.ActivityUpdate{}); err == nil {
		t.Fatal("credential crossed a runtime restart")
	}
	if _, err := first.RevokeSession(credential); err != nil {
		t.Fatal(err)
	}
	if _, err := first.RenewSession(credential, "actor", actor.Public().(ed25519.PublicKey), "actor", time.Minute, live.ActivityUpdate{}); err == nil {
		t.Fatal("revoked credential renewed")
	}

	expiring, _ := live.New(8)
	expiringCredential, _, err := expiring.OpenSession("actor", actor.Public().(ed25519.PublicKey), "actor", time.Nanosecond, live.ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := expiring.RenewSession(expiringCredential, "actor", actor.Public().(ed25519.PublicKey), "actor", time.Minute, live.ActivityUpdate{}); err == nil {
		t.Fatal("expired credential renewed")
	}
}

func TestPublicConversationScopesAreExactAndIsolated(t *testing.T) {
	runtime, _ := live.New(16)
	actor := deterministicKey(2)
	credential := openPublicSession(t, runtime, "actor", actor, live.ActivityUpdate{})
	first := submitPublicFrame(t, runtime, credential, "game:one", "", []byte("e4"), actor)
	if _, err := runtime.PrepareFrameForSession(credential, "game:two", first.Conversation, []byte("d4"), actor.Public().(ed25519.PublicKey)); err == nil {
		t.Fatal("conversation crossed its exact scope")
	}
	second := submitPublicFrame(t, runtime, credential, "game:two", "", []byte("d4"), actor)
	if first.Conversation == second.Conversation {
		t.Fatal("independent scopes shared a conversation")
	}
	continued := submitPublicFrame(t, runtime, credential, "game:one", first.Conversation, []byte("e5"), actor)
	if continued.Sequence != 1 || continued.Conversation != first.Conversation {
		t.Fatalf("same-scope continuation = %+v", continued)
	}
}

func TestPublicActivityAndAttentionBounds(t *testing.T) {
	status := live.ActivityBusy
	maxFocus := make([]string, live.MaxFocusEvents)
	for index := range maxFocus {
		maxFocus[index] = strings.Repeat("x", live.MaxFocusEventBytes-1) + string(rune('a'+index))
	}
	maxNote := strings.Repeat("n", live.MaxActivityNoteBytes)
	runtime, _ := live.New(64)
	key := deterministicKey(3)
	credential := openPublicSession(t, runtime, "bounded", key, live.ActivityUpdate{Status: &status, Focus: &maxFocus, Note: &maxNote})
	if _, err := runtime.RevokeSession(credential); err != nil {
		t.Fatal(err)
	}

	tooMany := append(append([]string(nil), maxFocus...), "overflow")
	tooLongFocus := []string{strings.Repeat("x", live.MaxFocusEventBytes+1)}
	tooLongNote := strings.Repeat("n", live.MaxActivityNoteBytes+1)
	for _, update := range []live.ActivityUpdate{{Focus: &tooMany}, {Focus: &tooLongFocus}, {Note: &tooLongNote}} {
		if _, _, err := runtime.OpenSession("invalid", key.Public().(ed25519.PublicKey), "invalid", time.Minute, update); err == nil {
			t.Fatal("activity above a public bound was accepted")
		}
	}

	focus := []string{"event"}
	for index := 0; index < live.MaxAttentionActors+3; index++ {
		actorKey := deterministicKey(100 + index)
		openPublicSession(t, runtime, fmt.Sprintf("actor-%d", index), actorKey, live.ActivityUpdate{Status: &status, Focus: &focus})
	}
	actors, omitted := runtime.FocusedOn("", focus)
	if len(actors) != live.MaxAttentionActors || omitted != 3 {
		t.Fatalf("attention bound = %d + %d omitted", len(actors), omitted)
	}
}

func TestPublicSessionBounds(t *testing.T) {
	t.Run("per actor", func(t *testing.T) {
		runtime, _ := live.New(live.MaxSessionsPerActor + 4)
		key := deterministicKey(4)
		for index := 0; index < live.MaxSessionsPerActor; index++ {
			openPublicSession(t, runtime, fmt.Sprintf("actor-%d", index), key, live.ActivityUpdate{})
		}
		if _, _, err := runtime.OpenSession("overflow", key.Public().(ed25519.PublicKey), "overflow", time.Minute, live.ActivityUpdate{}); err == nil {
			t.Fatal("per-actor session overflow was accepted")
		}
	})
	t.Run("global", func(t *testing.T) {
		runtime, _ := live.New(live.MaxLiveSessions + 4)
		for index := 0; index < live.MaxLiveSessions; index++ {
			openPublicSession(t, runtime, fmt.Sprintf("actor-%d", index), deterministicKey(1000+index), live.ActivityUpdate{})
		}
		overflow := deterministicKey(2000)
		if _, _, err := runtime.OpenSession("overflow", overflow.Public().(ed25519.PublicKey), "overflow", time.Minute, live.ActivityUpdate{}); err == nil {
			t.Fatal("global session overflow was accepted")
		}
	})
}

func TestPublicPayloadMessageAndHistoryBounds(t *testing.T) {
	runtime, _ := live.New(8)
	key := deterministicKey(5)
	credential := openPublicSession(t, runtime, "actor", key, live.ActivityUpdate{})
	public := key.Public().(ed25519.PublicKey)
	if _, err := runtime.PrepareFrameForSession(credential, "payload", "", make([]byte, live.MaxFramePayloadBytes), public); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PrepareFrameForSession(credential, "payload", "", make([]byte, live.MaxFramePayloadBytes+1), public); err == nil {
		t.Fatal("oversize frame payload was accepted")
	}

	maxID := strings.Repeat("i", live.MaxMessageIDBytes)
	maxText := strings.Repeat("t", live.MaxMessageTextBytes)
	recipients := make([]string, live.MaxMessageRecipients)
	for index := range recipients {
		fingerprint, _ := live.ActorFingerprint(deterministicKey(3000 + index).Public().(ed25519.PublicKey))
		recipients[index] = fingerprint
	}
	if _, err := runtime.PrepareMessageForSession(credential, "", live.Message{About: maxID, Text: maxText, Recipients: recipients}, public); err != nil {
		t.Fatal(err)
	}
	tooManyRecipients := append(append([]string(nil), recipients...), recipients[0])
	for _, message := range []live.Message{
		{About: strings.Repeat("i", live.MaxMessageIDBytes+1), Text: "x"},
		{About: "x", Text: strings.Repeat("t", live.MaxMessageTextBytes+1)},
		{About: "x", Text: "x", Recipients: tooManyRecipients},
	} {
		if _, err := runtime.PrepareMessageForSession(credential, "", message, public); err == nil {
			t.Fatal("message above a public bound was accepted")
		}
	}

	history, _ := live.New(2)
	baseline := history.Snapshot().Cursor
	for index := 0; index < 3; index++ {
		openPublicSession(t, history, fmt.Sprintf("history-%d", index), deterministicKey(4000+index), live.ActivityUpdate{})
	}
	if _, _, err := history.ChangesSince(baseline); !errors.Is(err, live.ErrReset) {
		t.Fatalf("evicted history cursor = %v, want ErrReset", err)
	}
}

func TestPublicRetainedFrameAndByteBounds(t *testing.T) {
	t.Run("frames", func(t *testing.T) {
		runtime, _ := live.New(2)
		key := deterministicKey(6)
		credential := openPublicSession(t, runtime, "actor", key, live.ActivityUpdate{})
		var conversation string
		for index := 0; index < live.MaxRetainedFrames; index++ {
			conversation = submitPublicFrame(t, runtime, credential, "frames", conversation, nil, key).Conversation
		}
		if _, err := runtime.PrepareFrameForSession(credential, "frames", conversation, nil, key.Public().(ed25519.PublicKey)); err == nil {
			t.Fatal("retained-frame overflow was accepted")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		runtime, _ := live.New(2)
		key := deterministicKey(7)
		credential := openPublicSession(t, runtime, "actor", key, live.ActivityUpdate{})
		var conversation string
		remaining := live.MaxRetainedBytes
		for remaining > 0 {
			size := live.MaxFramePayloadBytes
			if remaining < size {
				size = remaining
			}
			conversation = submitPublicFrame(t, runtime, credential, "bytes", conversation, make([]byte, size), key).Conversation
			remaining -= size
		}
		if _, err := runtime.PrepareFrameForSession(credential, "bytes", conversation, []byte{1}, key.Public().(ed25519.PublicKey)); err == nil {
			t.Fatal("retained-byte overflow was accepted")
		}
	})
}

func TestPublicInboxBounds(t *testing.T) {
	runtime, _ := live.New(4)
	senderKey := deterministicKey(8)
	recipientKey := deterministicKey(9)
	sender := openPublicSession(t, runtime, "sender", senderKey, live.ActivityUpdate{})
	recipient := openPublicSession(t, runtime, "recipient", recipientKey, live.ActivityUpdate{})
	if err := runtime.EnableInbox(recipient); err != nil {
		t.Fatal(err)
	}
	recipientFingerprint, _ := live.ActorFingerprint(recipientKey.Public().(ed25519.PublicKey))
	var conversation string
	for index := 0; index < live.MaxPendingInboxFrames; index++ {
		frame := submitPublicMessage(t, runtime, sender, conversation, live.Message{
			About: "inbox", Text: fmt.Sprintf("message-%d", index), Recipients: []string{recipientFingerprint},
		}, senderKey)
		conversation = frame.Conversation
	}
	_, inbox, err := runtime.SnapshotForSession(recipient)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Frames) != live.MaxInboxFrames || inbox.Skipped != live.MaxPendingInboxFrames-live.MaxInboxFrames {
		t.Fatalf("inbox page = %d frames, %d skipped", len(inbox.Frames), inbox.Skipped)
	}
	tooManyAcks := make([]string, live.MaxInboxFrames+1)
	if _, err := runtime.Acknowledge(recipient, tooManyAcks); err == nil {
		t.Fatal("oversize acknowledgement was accepted")
	}

	public := senderKey.Public().(ed25519.PublicKey)
	draft, err := runtime.PrepareMessageForSession(sender, conversation, live.Message{About: "inbox", Text: "overflow", Recipients: []string{recipientFingerprint}}, public)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, _ := live.ActorSigningBytes(draft)
	baseline := runtime.Snapshot().Cursor
	if _, err := runtime.SubmitMessageForSession(sender, live.Submission{Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(senderKey, signingBytes)}); err == nil {
		t.Fatal("pending-inbox overflow was accepted")
	}
	if after := runtime.Snapshot().Cursor; after != baseline {
		t.Fatalf("inbox overflow moved cursor from %+v to %+v", baseline, after)
	}
}
