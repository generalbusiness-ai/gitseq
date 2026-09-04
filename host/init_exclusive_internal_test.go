package host

// This file is in package host rather than host_test because the seam it uses
// is unexported: the gate is a test hook, not part of the public boundary.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// Initialization stores the configuration exclusively, so an initializer that
// lost the race to a concurrent one is refused rather than overwriting what
// that one stored. The window is pinned rather than raced: the gate holds this
// Init between observing no configuration and storing its own, and the test
// stores a different configuration in that window. An overwriting save would
// report success and replace a stored genesis with one nobody else can read.
func TestInitRefusesToOverwriteAConfigurationStoredInsideItsWindow(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	_, initializer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	previousGate := initAbsenceGate
	initAbsenceGate = func() {
		close(reached)
		<-release
	}
	defer func() { initAbsenceGate = previousGate }()

	done := make(chan error, 1)
	go func() {
		_, err := Init(ctx, repo, Application{
			Name: "gitseq-host-exclusive-init", FoldVersion: "gitseq-host-exclusive-init@0",
			SourceURL: "https://example.invalid/app.git",
		}, initializer, Options{})
		done <- err
	}()
	<-reached

	_, commonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	metaDir := apphost.MetaDir(commonDir)
	winner := apphost.Config{Version: 0, Genesis: strings.Repeat("a", 40), ObjectFormat: "sha1", ReadOnly: true}
	if err := apphost.CreateConfig(metaDir, winner); err != nil {
		t.Fatal(err)
	}
	close(release)

	if err := <-done; !errors.Is(err, os.ErrExist) {
		t.Fatalf("init that lost the creation race error = %v, want it refused with os.ErrExist", err)
	}
	stored, err := apphost.LoadConfig(metaDir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Genesis != winner.Genesis || !stored.ReadOnly {
		t.Fatalf("the refused init overwrote the stored configuration: %+v", stored)
	}
}
