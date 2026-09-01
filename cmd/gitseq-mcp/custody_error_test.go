package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

// The two ways a fingerprint resolution can miss stay apart on every adapter
// surface. An address that matches nobody after a successful fresh read is
// the empty fingerprint with no error — that is what "matches nothing" means
// here. A custody record that cannot be re-read is an I/O failure returned as
// itself: never the empty-fingerprint answer, and never classified as
// app.ErrUnknownActor, so a broken custody read can no more be reported as an
// unknown actor than a live actor can be hidden behind one. Collapsing either
// class into the other on the fingerprint, status, wait, or work path fails
// this test by name.
func TestDegradedCustodySurfacesIOFailureApartFromUnknownActor(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	for _, name := range []string{"status", "wait", "work"} {
		server := newServer("nobody", workspace.Repo)
		_, _, err := server.call(ctx, toolCall{Name: name})
		if !errors.Is(err, app.ErrUnknownActor) {
			t.Fatalf("%s unknown actor = %v, want app.ErrUnknownActor", name, err)
		}
	}

	// Replace only config.json with a directory. Repository discovery still
	// succeeds, but the custody record cannot be read.
	config := filepath.Join(workspace.MetaDir, "config.json")
	saved := config + ".saved"
	if err := os.Rename(config, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(config)
		_ = os.Rename(saved, config)
	})
	for _, name := range []string{"status", "wait", "work"} {
		server := newServer("nobody", workspace.Repo)
		_, _, err := server.call(ctx, toolCall{Name: name})
		if err == nil {
			t.Fatalf("%s swallowed the custody I/O failure", name)
		}
		if errors.Is(err, app.ErrUnknownActor) {
			t.Fatalf("%s classified custody I/O as unknown actor: %v", name, err)
		}
	}
}

// A configured actor whose key cannot be read is a custody I/O failure, not
// an unknown actor. Keep config.json readable while breaking only the key so
// the tool surface cannot pass this test by failing repository discovery
// first.
func TestToolSurfacePreservesUnreadableActorKeyError(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	keyFile := workspace.View().Actors["human"].KeyFile
	saved := keyFile + ".saved"
	if _, err := os.ReadFile(filepath.Join(workspace.MetaDir, "config.json")); err != nil {
		t.Fatalf("config.json is not readable before the key-only fault: %v", err)
	}
	if err := os.Rename(keyFile, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(keyFile, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(keyFile)
		_ = os.Rename(saved, keyFile)
	})

	server := newServer("human", workspace.Repo)
	_, _, err := server.call(ctx, toolCall{Name: "status"})
	if err == nil {
		t.Fatal("status swallowed the unreadable actor-key failure")
	}
	if errors.Is(err, app.ErrUnknownActor) {
		t.Fatalf("status classified an unreadable configured key as unknown actor: %v", err)
	}
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("status error = %T %v, want the actor key filesystem error", err, err)
	}
}
