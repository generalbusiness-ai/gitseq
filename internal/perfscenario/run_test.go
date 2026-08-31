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
