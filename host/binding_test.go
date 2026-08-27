package host

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

func testRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func TestBindingReplacementRefusesIfAnotherReplacementWinsTheCAS(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	_, initializer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	current := Application{Name: "race-test", FoldVersion: "race-fold@0"}
	workspace, err := Init(ctx, repo, current, initializer, Options{})
	if err != nil {
		t.Fatal(err)
	}

	arrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	type outcome struct {
		err error
	}
	completed := make(chan outcome, 1)
	go func() {
		_, err := replaceBinding(ctx, repo, Application{Name: current.Name, FoldVersion: "race-fold@1"}, initializer, func(name string) {
			if name == "before_ref_cas" {
				once.Do(func() {
					close(arrived)
					<-release
				})
			}
		})
		completed <- outcome{err: err}
	}()
	<-arrived

	winner := apphost.Binding{Application: current.Name, FoldVersion: "race-fold@2"}
	payload, err := winner.Payload()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.append(ctx, initializer, apphost.BindingSchema, payload, nil, "winning-binding"); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-completed
	if result.err == nil || !strings.Contains(result.err.Error(), "binding in force changed") {
		t.Fatalf("racing replacement = %v, want the outgoing-binding mismatch refused", result.err)
	}
	if _, err := Open(ctx, repo, Application{Name: current.Name, FoldVersion: winner.FoldVersion}); err != nil {
		t.Fatalf("winning replacement did not remain in force: %v", err)
	}
}
