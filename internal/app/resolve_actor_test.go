package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An actor another process adds after this workspace was opened resolves by
// name without reopening the workroom: the in-memory view misses, one fresh
// re-read of the custody record finds the actor, and the fresh record is
// adopted so later hits cost nothing. Removing the re-read alone fails this
// test by name.
func TestResolveActorFindsActorAddedAfterOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := external.AddActor(ctx, "operator", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}
	got, err := workspace.ResolveActor("late")
	if err != nil {
		t.Fatalf("actor added after open did not resolve: %v", err)
	}
	if got.Fingerprint != actor.Fingerprint || got.KeyFile != actor.KeyFile {
		t.Fatalf("resolved %+v, want the added custody %+v", got, actor)
	}
	// Adoption: the second resolution is served from memory, so it must
	// succeed even with the metadata directory made unreadable afterwards.
	blocked := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := workspace.MetaDir
	workspace.MetaDir = blocked
	defer func() { workspace.MetaDir = saved }()
	again, err := workspace.ResolveActor("late")
	if err != nil || again.Fingerprint != actor.Fingerprint {
		t.Fatalf("adopted custody was not reused (%+v, %v)", again, err)
	}
}

// The two ways to miss stay apart: a name nobody knows after a successful
// fresh read says it addresses no known actor, while an unreadable custody
// record reports its own failure - so a caller can never mistake a live
// actor for an unknown one. Collapsing either branch into the other fails
// this test by name.
func TestResolveActorErrorCasesStayApart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = workspace.ResolveActor("nobody")
	if err == nil || !strings.Contains(err.Error(), "addresses no known actor") {
		t.Fatalf("unknown-actor error = %v, want the no-known-actor wording", err)
	}
	if strings.Contains(err.Error(), "re-read configuration") {
		t.Fatalf("unknown-actor error leaked the I/O wording: %v", err)
	}

	blocked := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace.MetaDir = blocked
	// A miss is what reaches the re-read, so the same unknown name under an
	// unreadable custody record takes the I/O branch, not the memory hit.
	_, err = workspace.ResolveActor("nobody")
	if err == nil || !strings.Contains(err.Error(), "re-read configuration custody") {
		t.Fatalf("unreadable-custody error = %v, want the re-read wording", err)
	}
	if strings.Contains(err.Error(), "addresses no known actor") {
		t.Fatalf("I/O error leaked the unknown-actor wording: %v", err)
	}
}

// The application edge that normalises request bodies answers through the
// same re-read discipline as the adapter's own identity path: every accepted
// form of a late-added address resolves, and the two ways to miss stay apart
// under errors.Is rather than message matching. Reverting the address path to
// the cached view alone fails this test by name.
func TestResolveActorAddressRereadsCustodyBeforeConcludingUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	external, err := Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := external.AddActor(ctx, "operator", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"@late", "late", added.Fingerprint} {
		got, err := workspace.ResolveActorAddress(address)
		if err != nil || got.Fingerprint != added.Fingerprint {
			t.Fatalf("address %q resolved to (%+v, %v), want the added custody", address, got, err)
		}
	}
	if _, err := workspace.ResolveActorAddress("@nobody"); !errors.Is(err, ErrUnknownActor) {
		t.Fatalf("unknown-address error = %v, want ErrUnknownActor", err)
	}
	blocked := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := workspace.MetaDir
	workspace.MetaDir = blocked
	defer func() { workspace.MetaDir = saved }()
	// A miss is what reaches the re-read, so the unreadable custody record
	// takes the I/O branch and must not classify as ErrUnknownActor.
	if _, err := workspace.ResolveActorAddress("@nobody"); err == nil || errors.Is(err, ErrUnknownActor) {
		t.Fatalf("unreadable-custody error = %v, want a distinct I/O failure", err)
	}
}
