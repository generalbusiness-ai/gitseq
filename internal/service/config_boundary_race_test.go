package service

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
)

// This package holds one long-lived Workspace and serves many goroutines from
// it, which is exactly the consumer the configuration boundary exists for.
// The witness lives outside internal/app and uses only the exported API, the
// way a real consumer does. One goroutine reads the workspace through View
// continuously while the test drives the real custody mutations a resident
// performs: AddActor writes the actor map and saves, Verify advances and
// persists the verified frontier, RetireActor deletes from the map and saves.
// If View and those mutations were not serialized by one configuration lock,
// the race detector would flag the clone against the live writes — cloning
// the actor map iterates it, so an unguarded concurrent AddActor is also a
// fatal concurrent map iteration and map write. The earlier witness here
// raced two goroutines over a detached clone, which proved the clone was
// detached and nothing about the workspace under real mutation; this one
// mutates the workspace itself.
func TestConfigViewUnderRealConcurrentMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			view := workspace.View()
			_ = view.Actors["human"]
			if view.VerifiedFrontier != nil {
				_ = view.VerifiedFrontier.Depth
			}
		}
	}()

	const agents = 6
	for i := 0; i < agents; i++ {
		if _, _, err := workspace.AddActor(ctx, "human", fmt.Sprintf("agent-%02d", i), "agent"); err != nil {
			t.Fatalf("AddActor agent-%02d: %v", i, err)
		}
		if _, err := workspace.Verify(ctx); err != nil {
			t.Fatalf("Verify after agent-%02d: %v", i, err)
		}
	}
	if _, err := workspace.RetireActor(ctx, "human", "agent-00"); err != nil {
		t.Fatalf("RetireActor: %v", err)
	}
	close(stop)
	wg.Wait()

	final := workspace.View()
	if len(final.Actors) != agents {
		t.Errorf("after adding %d agents and retiring one, custody holds %d actors, want %d", agents, len(final.Actors), agents)
	}
	if final.VerifiedFrontier == nil {
		t.Fatal("workspace forgot its verified frontier under mutation")
	}

	// The view must also remain the caller's alone: mutating it must not
	// hand the workspace custody it never granted or move its frontier.
	depth := final.VerifiedFrontier.Depth
	final.Actors["intruder"] = apphost.Actor{Name: "intruder"}
	final.VerifiedFrontier.Depth++
	again := workspace.View()
	if _, held := again.Actors["intruder"]; held {
		t.Error("mutating a view gave the workspace custody of an actor it never held")
	}
	if again.VerifiedFrontier == nil || again.VerifiedFrontier.Depth != depth {
		t.Errorf("mutating a view moved the workspace's verified frontier: got %+v, want depth %d", again.VerifiedFrontier, depth)
	}
}
