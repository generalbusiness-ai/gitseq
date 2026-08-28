package identity_test

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/host/identity"
)

// These tests exercise the external-signing endorsement path exactly as an
// application outside this module would: they prepare an endorsement through
// the public surface, sign the actor bytes with a key the host never sees, and
// append the result. They stand alongside identity_test.go, which walks the
// private-key Endorse path, and prove the two share one validation site.

// preparedWorkspace initializes a repository through the public host surface
// and returns both the workspace and the on-disk repository, so a test can
// watch the Git object store across a refused preparation.
func preparedWorkspace(t *testing.T, ctx context.Context) (*host.Workspace, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_, initializer := testKey(t)
	workspace, err := host.Init(ctx, repo, host.Application{Name: testName, FoldVersion: testFold}, initializer, host.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, repo
}

// looseObjects counts the repository's loose Git objects. A fresh test
// repository packs nothing, so this is the whole object store, and a change in
// it is a write that happened.
func looseObjects(t *testing.T, repo string) int {
	t.Helper()
	output, err := exec.Command("git", "-C", repo, "count-objects", "-v").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if rest, ok := strings.CutPrefix(line, "count:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("git count-objects gave no count:\n%s", output)
	return 0
}

func actorFingerprint(t *testing.T, key ed25519.PublicKey) string {
	t.Helper()
	value, err := host.ActorFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// TestPreparedNostrEndorsementSignedOutsideHostResolves is the positive path:
// a valid self-signed Nostr endorsement is prepared without any actor private
// key, the subject session key signs the actor bytes outside the host, and the
// appended anchor resolves exactly as the private-key Endorse path would leave
// it. It is the positive control for the refusal test below.
func TestPreparedNostrEndorsementSignedOutsideHostResolves(t *testing.T) {
	ctx := context.Background()
	workspace, _ := preparedWorkspace(t, ctx)
	sessionPublic, session := testKey(t)
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subject := actorFingerprint(t, sessionPublic)
	root := nostrKey(t)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: log.Genesis, Subject: subject, Scope: "play",
	})

	prepared, err := identity.PrepareEndorsement(ctx, workspace, anchor, "external-endorse")
	if err != nil {
		t.Fatalf("PrepareEndorsement refused a valid endorsement: %v", err)
	}
	signingBytes, err := host.ActorSigningBytes(prepared)
	if err != nil {
		t.Fatal(err)
	}
	endorsed, err := workspace.AppendSigned(ctx, host.SignedAct{
		Prepared: prepared, ActorKey: sessionPublic,
		ActorSignature: ed25519.Sign(session, signingBytes),
	})
	if err != nil {
		t.Fatalf("AppendSigned refused the externally signed endorsement: %v", err)
	}

	resolved := resolveWorkspace(t, ctx, workspace).LookupAt(endorsed.ID)
	if !resolved.Anchored {
		t.Fatal("an externally signed valid Nostr endorsement resolved as unanchored")
	}
	if resolved.Vouching != identity.SelfSigned || resolved.Verification != identity.InLog {
		t.Errorf("axes = %v/%v, want self-signed/in-log", resolved.Vouching, resolved.Verification)
	}
	want := identity.Identity{
		Scheme:  identity.NostrScheme,
		Subject: hex.EncodeToString(schnorr.SerializePubKey(root.PubKey())),
	}
	if resolved.Identity != want {
		t.Errorf("identity = %+v, want the Nostr root %+v", resolved.Identity, want)
	}
	if resolved.Record != endorsed.ID {
		t.Errorf("answering record = %q, want %q", resolved.Record, endorsed.ID)
	}
}

// TestPreparedEndorsementRefusesInvalidNostrProofWithNoWrite is the mutation
// witness. The named refusal assertion below fails if the shared Nostr guard in
// Anchor.validate (host/identity/identity.go, "nostr anchor signature is
// invalid") is bypassed: preparation would then return no error, no write
// happens either way because Prepare writes nothing, and the frontier stays
// put. The positive control at the end keeps the test from passing vacuously.
func TestPreparedEndorsementRefusesInvalidNostrProofWithNoWrite(t *testing.T) {
	ctx := context.Background()
	workspace, repo := preparedWorkspace(t, ctx)
	sessionPublic, session := testKey(t)
	log, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subject := actorFingerprint(t, sessionPublic)
	root := nostrKey(t)
	anchor := signNostrAnchor(t, root, identity.Anchor{
		Genesis: log.Genesis, Subject: subject, Scope: "play",
	})
	// Changing an authority-bearing field after the root signed invalidates the
	// proof: the delegation the root signed no longer matches this anchor.
	tampered := anchor
	tampered.Scope = "changed-after-signing"

	beforeObjects := looseObjects(t, repo)
	beforeHead, beforeDepth := log.Head, log.Depth

	// The named assertion. It fails when the shared validation guard is
	// bypassed, because preparation then returns no error.
	if _, err := identity.PrepareEndorsement(ctx, workspace, tampered, "external-endorse"); err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("PrepareEndorsement error = %v, want invalid-signature refusal before any preparation", err)
	}

	after, err := workspace.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := looseObjects(t, repo); got != beforeObjects {
		t.Errorf("a refused preparation changed the loose-object count: %d -> %d", beforeObjects, got)
	}
	if after.Head != beforeHead || after.Depth != beforeDepth {
		t.Errorf("a refused preparation moved the frontier: head %s->%s depth %d->%d",
			beforeHead, after.Head, beforeDepth, after.Depth)
	}

	// Positive control: the same anchor, untampered, prepares and appends, so
	// the refusal above is the guard doing its work rather than everything
	// being refused.
	prepared, err := identity.PrepareEndorsement(ctx, workspace, anchor, "external-endorse")
	if err != nil {
		t.Fatalf("PrepareEndorsement refused the valid control endorsement: %v", err)
	}
	signingBytes, err := host.ActorSigningBytes(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.AppendSigned(ctx, host.SignedAct{
		Prepared: prepared, ActorKey: sessionPublic,
		ActorSignature: ed25519.Sign(session, signingBytes),
	}); err != nil {
		t.Fatalf("AppendSigned refused the valid control endorsement: %v", err)
	}
}
