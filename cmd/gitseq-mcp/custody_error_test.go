package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
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
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	server := newServer("nobody", workspace.Repo)
	room := &room{workspace: workspace}

	fingerprint, err := server.fingerprint(room)
	if err != nil || fingerprint != "" {
		t.Fatalf("unknown-actor resolution = (%q, %v), want the empty fingerprint with no error", fingerprint, err)
	}

	// A miss is what reaches the re-read, so an unreadable custody record
	// takes the I/O branch. Only config.json is made unreadable — as a
	// directory, so every other metadata file this path may touch keeps
	// working — which pins the failure to the custody read itself.
	blocked := filepath.Join(t.TempDir(), "meta")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(blocked, "config.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := room.workspace.MetaDir
	room.workspace.MetaDir = blocked

	fingerprint, err = server.fingerprint(room)
	room.workspace.MetaDir = saved
	if err == nil {
		t.Fatalf("custody I/O failure surfaced as the empty unknown-actor fingerprint %q", fingerprint)
	}
	if errors.Is(err, app.ErrUnknownActor) {
		t.Fatalf("custody I/O failure classified as unknown actor: %v", err)
	}
	if fingerprint != "" {
		t.Fatalf("custody I/O failure invented a fingerprint %q", fingerprint)
	}

	// Tool calls are stricter than the internal digest helper: even readable
	// custody must not let an unknown selected actor degrade into an empty
	// identity. Every tool refuses before attaching or falling back.
	dead := httptest.NewServer(nil)
	client := dead.Client()
	dead.Close()
	digesting := newServer("nobody", workspace.Repo)
	digesting.client = residentclient.NewWithHTTP(client, residentHTTPTimeout)
	for _, name := range []string{"status", "wait", "work"} {
		_, _, err := digesting.call(ctx, toolCall{Name: name})
		if err == nil {
			t.Fatalf("%s answered for an unknown selected actor", name)
		}
	}
}
