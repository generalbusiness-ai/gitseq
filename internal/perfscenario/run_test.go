package perfscenario

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

func TestSessionBoundScenarioSetupUsesResidentCredential(t *testing.T) {
	tests := []struct {
		scenario    string
		concurrency int
		operations  int
	}{
		{scenario: "submit_wait", operations: 1},
		{scenario: "concurrent_read_write", concurrency: 2, operations: 4},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			ctx := context.Background()
			repository := filepath.Join(t.TempDir(), "repo")
			if err := os.MkdirAll(repository, 0o755); err != nil {
				t.Fatal(err)
			}
			if output, err := command(ctx, repository, "git", "init", "-q", "."); err != nil {
				t.Fatalf("git init: %v: %s", err, output)
			}
			workspace, seed, err := app.Init(ctx, repository, "operator", 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{
				Schema: manifestSchema, GeneratorVersion: "credential-contract-test.v1", Shape: "linear", Depth: 1,
				Repository: repository, Genesis: workspace.View().Genesis, SeedEvent: seed.ID, Actor: "operator", ActorCount: 1,
				Heads: map[string]string{"1": eventCommit(seed.ID)}, LogicalDigest: "test", ExactDigest: "test",
			}
			encoded, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repository, "performance-fixture.json"), encoded, 0o644); err != nil {
				t.Fatal(err)
			}

			operation, err := prepareOperation(ctx, RunOptions{
				Scenario: test.scenario, Fixture: repository, Scratch: repository, Depth: 1,
				Tail: -1, Concurrency: test.concurrency, Fanout: 1,
			}, nil)
			if err != nil {
				t.Fatalf("setup did not cross the strict resident contract: %v", err)
			}
			_, operations, err := operation()
			if err != nil {
				t.Fatalf("operation did not reuse the resident-minted credential: %v", err)
			}
			if operations != test.operations {
				t.Fatalf("operations = %d, want %d", operations, test.operations)
			}
		})
	}
}

// TestWorkerLeavesCheckpointBytesToTheLaneParent pins the division the
// compare path depends on. The worker never computes the serialized
// checkpoint size, so a base worker built from a tree that predates the
// metric still builds and still runs; the lane parent, which is always the
// candidate build, fills the field in from the fixture afterwards. The sample
// schema names v2 because the row now carries that field.
func TestWorkerLeavesCheckpointBytesToTheLaneParent(t *testing.T) {
	if os.Getenv("GITSEQ_PERF_INTEGRATION") == "" {
		t.Skip("set GITSEQ_PERF_INTEGRATION=1 for real signed fixture coverage")
	}
	ctx := context.Background()
	fixture := filepath.Join(t.TempDir(), "fixture")
	if _, err := Prepare(ctx, fixture, FixturePlan{
		GeneratorVersion: "checkpoint-bytes-test.v1", Seed: 632, Depth: 267, Shape: "linear",
		PayloadBuckets: []int{8}, CheckpointDepths: []int{257}, ActorCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	restart, err := Run(ctx, RunOptions{
		Scenario: "checkpoint_restart", Fixture: fixture, Scratch: filepath.Join(t.TempDir(), "restart"),
		Depth: 267, Tail: 10, Fanout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restart.SnapshotSource != app.SnapshotSourceSignedCheckpointTail {
		t.Fatalf("restart source = %s", restart.SnapshotSource)
	}
	if restart.Schema != "gitseq.performance-sample.v2" {
		t.Fatalf("sample schema = %q, want gitseq.performance-sample.v2", restart.Schema)
	}
	if restart.CheckpointBytes != 0 {
		t.Fatalf("the worker computed checkpoint_bytes = %d; that is the lane parent's job", restart.CheckpointBytes)
	}
	if restart.Fixture.Checkpoint == "" {
		t.Fatal("the sample recorded no checkpoint object for the parent to size")
	}
}
