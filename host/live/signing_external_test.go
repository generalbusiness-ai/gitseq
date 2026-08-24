package live_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/host/live"
)

const testSeed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func openProvedSession(t *testing.T, runtime *live.Hub, actor string, private ed25519.PrivateKey) string {
	t.Helper()
	challenge, err := runtime.PrepareSession(actor, private.Public().(ed25519.PublicKey), actor, time.Minute, live.ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	toSign, err := live.SessionSigningBytes(challenge)
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := runtime.OpenSession(challenge, ed25519.Sign(private, toSign))
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func TestBrowserProvesPossessionBeforePresenceBecomesVisible(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	fingerprint, _ := live.ActorFingerprint(public)
	before := runtime.Snapshot()
	challenge, err := runtime.PrepareSession("browser", public, "Browser", time.Minute, live.ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	if afterPrepare := runtime.Snapshot(); afterPrepare.Cursor != before.Cursor || len(afterPrepare.Presence) != 0 || runtime.LiveSessionsForActor(fingerprint) != 0 {
		t.Fatalf("unproved challenge published or consumed quota: before=%+v after=%+v", before, afterPrepare)
	}
	toSign, err := live.SessionSigningBytes(challenge)
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := runtime.OpenSession(challenge, ed25519.Sign(private, toSign))
	if err != nil {
		t.Fatal(err)
	}
	if credential == "" || runtime.LiveSessionsForActor(fingerprint) != 1 || len(runtime.Snapshot().Presence) != 1 {
		t.Fatal("proved session did not become the one visible lease")
	}
	if _, _, err := runtime.OpenSession(challenge, ed25519.Sign(private, toSign)); err == nil {
		t.Fatal("single-use session challenge replayed")
	}
}

func TestBadSessionProofConsumesNoQuotaAndCannotBeRetried(t *testing.T) {
	runtime, _ := live.New(32)
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	challenge, err := runtime.PrepareSession("browser", public, "Browser", time.Minute, live.ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	_, wrong, _ := ed25519.GenerateKey(nil)
	toSign, _ := live.SessionSigningBytes(challenge)
	if _, _, err := runtime.OpenSession(challenge, ed25519.Sign(wrong, toSign)); err == nil {
		t.Fatal("wrong key opened a lease")
	}
	if _, _, err := runtime.OpenSession(challenge, ed25519.Sign(private, toSign)); err == nil {
		t.Fatal("failed proof left a reusable challenge")
	}
	fingerprint, _ := live.ActorFingerprint(public)
	if runtime.LiveSessionsForActor(fingerprint) != 0 || len(runtime.Snapshot().Presence) != 0 {
		t.Fatal("failed proof consumed quota or published presence")
	}
}

func TestSessionSigningBytesPinnedForBrowserParity(t *testing.T) {
	private := testKey(t)
	challenge := live.SessionChallenge{
		Version:    0,
		Generation: "generation:000102030405060708090a0b0c0d0e0f",
		Nonce:      []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
		ActorKey:   private.Public().(ed25519.PublicKey),
	}
	got, err := live.SessionSigningBytes(challenge)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("6769747365712e6c6976652e73657373696f6e2d70726f6f662e7630008400782b67656e65726174696f6e3a30303031303230333034303530363037303830393061306230633064306530665820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f5820d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if !bytes.Equal(got, want) {
		t.Fatalf("session signing bytes = %x, want %x", got, want)
	}
}

func TestActorSigningBytesPinnedForBrowserParity(t *testing.T) {
	draft := live.Draft{
		Generation: "g", Conversation: "c", Sequence: 2,
		PreviousHash: []byte{1, 2}, Payload: []byte{0xa0},
	}
	got, err := live.ActorSigningBytes(draft)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("6769747365712e6e657875732e6163746f722d6672616d652e7630008600616761630242010241a0")
	if !bytes.Equal(got, want) {
		t.Fatalf("signing bytes = %x, want %x", got, want)
	}
	signature := ed25519.Sign(testKey(t), got)
	wantSignature, _ := hex.DecodeString("ccc5b9eff23a38de8abbe344eebdd43c025f54ef34c7b47b3127ff080abf083901e3bdd10394ae5b6d8d249a750005547248d4ec09f059634f174b428ebe070f")
	if !bytes.Equal(signature, wantSignature) {
		t.Fatalf("signature = %x, want %x", signature, wantSignature)
	}
}

func TestBrowserHeldKeyPreparesSignsAndSubmitsWithoutRuntimeCustody(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	credential := openProvedSession(t, runtime, "browser", private)

	before := runtime.Snapshot()
	draft, err := runtime.PrepareFrameForSession(credential, "game:42", "", []byte(`{"move":"e4"}`), public)
	if err != nil {
		t.Fatal(err)
	}
	afterPrepare := runtime.Snapshot()
	if afterPrepare.Cursor != before.Cursor || len(afterPrepare.Conversations) != 0 {
		t.Fatalf("preparation mutated the runtime: before=%+v after=%+v", before, afterPrepare)
	}
	signingBytes, err := live.ActorSigningBytes(draft)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(private, signingBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := live.VerifyFrame(frame, runtime.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(private, signingBytes),
	}); !errors.Is(err, live.ErrStaleDraft) {
		t.Fatalf("replayed draft = %v, want ErrStaleDraft", err)
	}

	// Returned frames must not alias the retained transcript.
	frame.Payload[0] ^= 0xff
	frame.ActorSignature[0] ^= 0xff
	retained, err := runtime.Frames(draft.Conversation)
	if err != nil || len(retained) != 1 || string(retained[0].Payload) != `{"move":"e4"}` {
		t.Fatalf("caller mutation reached retained frame: frames=%+v err=%v", retained, err)
	}
	if err := live.VerifyFrame(retained[0], runtime.PublicKey()); err != nil {
		t.Fatalf("retained frame no longer verifies: %v", err)
	}
}

func TestExternallySignedSubmissionRejectsKeyScopeAndPayloadTamperingAtomically(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	credential := openProvedSession(t, runtime, "browser", private)
	draft, err := runtime.PrepareFrameForSession(credential, "game:42", "", []byte("e4"), public)
	if err != nil {
		t.Fatal(err)
	}
	bytesToSign, _ := live.ActorSigningBytes(draft)
	signature := ed25519.Sign(private, bytesToSign)
	baseline := runtime.Snapshot()

	tamperedPayload := draft
	tamperedPayload.Payload = []byte("d4")
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{Draft: tamperedPayload, ActorKey: public, ActorSignature: signature}); err == nil {
		t.Fatal("tampered payload was accepted")
	}
	tamperedScope := draft
	tamperedScope.Scope = "game:other"
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{Draft: tamperedScope, ActorKey: public, ActorSignature: signature}); err == nil {
		t.Fatal("tampered scope was accepted")
	}
	_, otherPrivate, _ := ed25519.GenerateKey(nil)
	otherPublic := otherPrivate.Public().(ed25519.PublicKey)
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{Draft: draft, ActorKey: otherPublic, ActorSignature: ed25519.Sign(otherPrivate, bytesToSign)}); err == nil {
		t.Fatal("a key outside the lease binding was accepted")
	}
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{Draft: draft, ActorKey: public, ActorSignature: signature[:len(signature)-1]}); err == nil {
		t.Fatal("a malformed signature was accepted")
	}
	after := runtime.Snapshot()
	if after.Cursor != baseline.Cursor || len(after.Conversations) != 0 {
		t.Fatalf("rejected submissions mutated the runtime: before=%+v after=%+v", baseline, after)
	}
}

func TestSignedMessageRejectsNoncanonicalJSONBeforePublishing(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	credential := openProvedSession(t, runtime, "browser", private)
	// This decodes to a valid Message, but carries an ignored field and a
	// different member order from the one canonical representation delivered
	// by the runtime.
	draft, err := runtime.PrepareFrameForSession(credential, "topic", "", []byte(`{"text":"hello","about":"topic","ignored":true}`), public)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, _ := live.ActorSigningBytes(draft)
	baseline := runtime.Snapshot()
	if _, err := runtime.SubmitMessageForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(private, signingBytes),
	}); err == nil {
		t.Fatal("noncanonical signed message JSON was accepted")
	}
	after := runtime.Snapshot()
	if after.Cursor != baseline.Cursor || len(after.Conversations) != 0 {
		t.Fatalf("rejected message mutated the runtime: before=%+v after=%+v", baseline, after)
	}
}

func TestTypedMessageAuthenticationPrecedesConversationAndPayloadInspection(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	credential := openProvedSession(t, runtime, "browser", private)
	draft, err := runtime.PrepareMessageForSession(credential, "", live.Message{About: "known", Text: "hello"}, public)
	if err != nil {
		t.Fatal(err)
	}
	signingBytes, _ := live.ActorSigningBytes(draft)
	frame, err := runtime.SubmitMessageForSession(credential, live.Submission{
		Draft: draft, ActorKey: public, ActorSignature: ed25519.Sign(private, signingBytes),
	})
	if err != nil {
		t.Fatal(err)
	}

	const refusal = "credential is not valid"
	for _, testCase := range []struct {
		name         string
		conversation string
		message      live.Message
	}{
		{name: "known conversation and reply", conversation: frame.Conversation, message: live.Message{About: "known", Text: "reply", Re: frame.Conversation + ":0"}},
		{name: "unknown conversation", conversation: "eph:sha256:" + string(bytes.Repeat([]byte{'0'}, 64)), message: live.Message{About: "unknown", Text: "hello"}},
		{name: "malformed message", conversation: frame.Conversation, message: live.Message{About: "known"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runtime.PrepareMessageForSession("credential:"+string(bytes.Repeat([]byte{'0'}, 64)), testCase.conversation, testCase.message, public)
			if err == nil || err.Error() != refusal {
				t.Fatalf("prepare refusal = %v, want %q", err, refusal)
			}
		})
	}

	_, err = runtime.SubmitMessageForSession("credential:"+string(bytes.Repeat([]byte{'0'}, 64)), live.Submission{
		Draft:    live.Draft{Scope: "known", Conversation: frame.Conversation, Payload: []byte("not-json")},
		ActorKey: public, ActorSignature: bytes.Repeat([]byte{1}, ed25519.SignatureSize),
	})
	if err == nil || err.Error() != refusal {
		t.Fatalf("submit refusal = %v, want %q", err, refusal)
	}

	_, otherPrivate, _ := ed25519.GenerateKey(nil)
	_, err = runtime.PrepareMessageForSession(credential, frame.Conversation, live.Message{About: "known", Text: "hello"}, otherPrivate.Public().(ed25519.PublicKey))
	if err == nil || err.Error() != refusal {
		t.Fatalf("mismatched-key refusal = %v, want %q", err, refusal)
	}
	if _, err := runtime.RenewSession(credential, "browser", ed25519.PublicKey{1}, "Browser", time.Minute, live.ActivityUpdate{}); err == nil || err.Error() != refusal {
		t.Fatalf("malformed renewal-key refusal = %v, want %q", err, refusal)
	}
}

func TestConversationGenesisBoundsAndCanonicalEncodingRejectAtomically(t *testing.T) {
	runtime, err := live.New(32)
	if err != nil {
		t.Fatal(err)
	}
	private := testKey(t)
	public := private.Public().(ed25519.PublicKey)
	credential := openProvedSession(t, runtime, "browser", private)
	prepared, err := runtime.PrepareFrameForSession(credential, "scope", "", []byte("payload"), public)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.ConversationGenesis) < 2 || prepared.ConversationGenesis[0] != 0x85 || prepared.ConversationGenesis[1] != 0x01 {
		t.Fatalf("unexpected canonical genesis prefix: %x", prepared.ConversationGenesis)
	}
	baseline := runtime.Snapshot()

	oversized := prepared
	oversized.ConversationGenesis = bytes.Repeat([]byte{0}, live.MaxConversationGenesisBytes+1)
	oversizedBytes, _ := live.ActorSigningBytes(oversized)
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: oversized, ActorKey: public, ActorSignature: ed25519.Sign(private, oversizedBytes),
	}); err == nil {
		t.Fatal("oversized conversation genesis was accepted")
	}

	// Encode the same version integer in a longer legal CBOR form. Decoding it
	// would produce the same value, but it is not the deterministic wire form.
	noncanonical := prepared
	noncanonical.ConversationGenesis = append([]byte{0x85, 0x18, 0x01}, prepared.ConversationGenesis[2:]...)
	digest := sha256.Sum256(noncanonical.ConversationGenesis)
	noncanonical.Conversation = "eph:sha256:" + hex.EncodeToString(digest[:])
	noncanonicalBytes, _ := live.ActorSigningBytes(noncanonical)
	if _, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: noncanonical, ActorKey: public, ActorSignature: ed25519.Sign(private, noncanonicalBytes),
	}); err == nil {
		t.Fatal("noncanonical conversation genesis was accepted")
	}
	after := runtime.Snapshot()
	if after.Cursor != baseline.Cursor || len(after.Conversations) != 0 {
		t.Fatalf("rejected genesis mutated the runtime: before=%+v after=%+v", baseline, after)
	}
	validBytes, _ := live.ActorSigningBytes(prepared)
	frame, err := runtime.SubmitFrameForSession(credential, live.Submission{
		Draft: prepared, ActorKey: public, ActorSignature: ed25519.Sign(private, validBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*live.Frame){
		"genesis": func(frame *live.Frame) {
			frame.ConversationGenesis = bytes.Repeat([]byte{0}, live.MaxConversationGenesisBytes+1)
		},
		"payload":         func(frame *live.Frame) { frame.Payload = bytes.Repeat([]byte{0}, live.MaxFramePayloadBytes+1) },
		"actor signature": func(frame *live.Frame) { frame.ActorSignature = nil },
		"nexus signature": func(frame *live.Frame) { frame.NexusSignature = nil },
		"previous hash":   func(frame *live.Frame) { frame.PreviousHash = []byte{1} },
	} {
		t.Run("verify bounds "+name, func(t *testing.T) {
			tampered := frame
			mutate(&tampered)
			if err := live.VerifyFrame(tampered, runtime.PublicKey()); err == nil {
				t.Fatal("out-of-bounds frame verified")
			}
		})
	}
}
