package perfscenario

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
)

func TestGeneratedShapesCarryDistinctLogicalVocabulary(t *testing.T) {
	tests := []struct {
		shape string
		depth int
		want  string
	}{
		{"linear", 2, "assert"},
		{"assert_heavy", 2, "assert"},
		{"open_request_heavy", 2, "request"},
		{"terminal_history_heavy", 2, "request"},
		{"terminal_history_heavy", 3, "promise"},
		{"terminal_history_heavy", 4, "report"},
		{"terminal_history_heavy", 5, "ratify"},
		{"artifact_staleness_heavy", 2, "artifact"},
		{"artifact_staleness_heavy", 3, "supersede"},
	}
	for _, test := range tests {
		generated := generatedAct(test.shape, test.depth, 8, "actor", "seed", "request", "promise", "report", "artifact")
		if generated.label != test.want {
			t.Errorf("%s depth %d label = %q, want %q", test.shape, test.depth, generated.label, test.want)
		}
	}
}

func TestLoadManifestRejectsUnknownFieldsAndIncompleteRecords(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "performance-fixture.json")
	for _, content := range []string{
		`{"schema":"gitseq.performance-fixture-manifest.v1","unknown":true}`,
		`{"schema":"gitseq.performance-fixture-manifest.v1"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(directory); err == nil {
			t.Fatalf("manifest %s was accepted", content)
		}
	}
}

func TestSmallExactFixtureAndCompleteOperations(t *testing.T) {
	if os.Getenv("GITSEQ_PERF_INTEGRATION") == "" {
		t.Skip("set GITSEQ_PERF_INTEGRATION=1 for real signed fixture coverage")
	}
	ctx := context.Background()
	fixture := filepath.Join(t.TempDir(), "fixture")
	manifest, err := Prepare(ctx, fixture, FixturePlan{
		GeneratorVersion: "test.v1", Seed: 632, Depth: 8, Shape: "linear", PayloadBuckets: []int{8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.LogicalDigest == "" || manifest.ExactDigest == "" || len(manifest.Heads) != 8 {
		t.Fatalf("incomplete manifest: %+v", manifest)
	}
	for _, scenario := range []string{
		"startup", "cold_status", "warm_status", "submit_ack", "submit_wait",
		"honest_fallback", "quiet_long_poll", "concurrent_read_write", "bounded_soak",
	} {
		t.Run(scenario, func(t *testing.T) {
			concurrency := 0
			if scenario == "concurrent_read_write" {
				concurrency = 4
			}
			trace := ""
			if scenario == "cold_status" {
				trace = filepath.Join(t.TempDir(), "git.trace2")
			}
			result, err := Run(ctx, RunOptions{
				Scenario: scenario, Fixture: fixture, Scratch: filepath.Join(t.TempDir(), "sample"),
				Depth: 8, Tail: -1, Concurrency: concurrency, Fanout: 1, SoakOperations: 8, SoakSeconds: 10, Trace2Path: trace,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.LatencyNS <= 0 || result.SetupNS <= 0 || result.TrustedDigest == "" || result.CorrectnessDigest != result.TrustedDigest {
				t.Fatalf("bad result: %+v", result)
			}
			if scenario == "startup" && result.ResponseBytes == 0 {
				t.Fatal("startup recorded no readiness/status response")
			}
			if scenario == "cold_status" && (result.GitProcessCount.Value == nil || *result.GitProcessCount.Value < 1 || result.GitDurationNS.Value == nil || *result.GitDurationNS.Value <= 0) {
				t.Fatalf("Trace2 metrics were not populated: %+v %+v", result.GitProcessCount, result.GitDurationNS)
			}
			if scenario == "concurrent_read_write" && (result.Concurrency != 4 || result.Operations != 8) {
				t.Fatalf("concurrency result = %+v, want four reader/writer pairs", result)
			}
		})
	}
	sample := filepath.Join(t.TempDir(), "sample")
	_, cleanup, err := Materialize(ctx, fixture, sample, Sample{Depth: 8, Tail: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	pointer := filepath.Join(sample, ".git", "gitseq", "checkpoints", manifest.Genesis+".json")
	if _, err := os.Stat(pointer); !os.IsNotExist(err) {
		t.Fatalf("cold materialization retained checkpoint pointer: %v", err)
	}
	workspace, err := app.Open(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Config.VerifiedFrontier != nil {
		t.Fatalf("sample inherited source verified frontier: %+v", workspace.Config.VerifiedFrontier)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Depth != 8 || snapshot.Genesis != manifest.Genesis || strings.HasPrefix(snapshot.Genesis, "git:") {
		t.Fatalf("materialized snapshot = %+v", snapshot)
	}
}

func TestCheckpointSelectionAndColdMaterializationAreExact(t *testing.T) {
	if os.Getenv("GITSEQ_PERF_INTEGRATION") == "" {
		t.Skip("set GITSEQ_PERF_INTEGRATION=1 for real signed fixture coverage")
	}
	ctx := context.Background()
	fixture := filepath.Join(t.TempDir(), "fixture")
	manifest, err := Prepare(ctx, fixture, FixturePlan{
		GeneratorVersion: "test.v2", Seed: 632, Depth: 258, Shape: "linear", PayloadBuckets: []int{8}, CheckpointDepths: []int{257}, ActorCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Checkpoints["257"] == "" {
		t.Fatal("fixture did not retain its requested checkpoint")
	}

	coldPath := filepath.Join(t.TempDir(), "cold")
	_, coldCleanup, err := Materialize(ctx, fixture, coldPath, Sample{Depth: 100, Tail: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer coldCleanup()
	cold, err := app.Open(ctx, coldPath)
	if err != nil {
		t.Fatal(err)
	}
	if cold.Config.VerifiedFrontier != nil {
		t.Fatalf("cold sample inherited source verified frontier: %+v", cold.Config.VerifiedFrontier)
	}
	coldSnapshot, err := cold.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if coldSnapshot.Source != app.SnapshotSourceColdFullAudit || coldSnapshot.Snapshot.Depth != 100 {
		t.Fatalf("cold sample = source %s depth %d", coldSnapshot.Source, coldSnapshot.Snapshot.Depth)
	}

	warmPath := filepath.Join(t.TempDir(), "checkpoint")
	_, warmCleanup, err := Materialize(ctx, fixture, warmPath, Sample{Depth: 258, Tail: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer warmCleanup()
	warm, err := app.Open(ctx, warmPath)
	if err != nil {
		t.Fatal(err)
	}
	warmSnapshot, err := warm.SnapshotWithSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if warmSnapshot.Source != app.SnapshotSourceSignedCheckpointTail || warmSnapshot.Snapshot.Depth != 258 {
		t.Fatalf("checkpoint sample = source %s depth %d", warmSnapshot.Source, warmSnapshot.Snapshot.Depth)
	}
}

func TestActorCountFixtureAndFanoutBases(t *testing.T) {
	if os.Getenv("GITSEQ_PERF_INTEGRATION") == "" {
		t.Skip("set GITSEQ_PERF_INTEGRATION=1 for real signed fixture coverage")
	}
	ctx := context.Background()
	fixture := filepath.Join(t.TempDir(), "fixture")
	manifest, err := Prepare(ctx, fixture, FixturePlan{
		GeneratorVersion: "test.v2", Seed: 632, Depth: 8, Shape: "linear", PayloadBuckets: []int{8}, ActorCount: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ActorCount != 3 {
		t.Fatalf("actor count = %d, want 3", manifest.ActorCount)
	}
	actorResult, err := Run(ctx, RunOptions{
		Scenario: "cold_status", Fixture: fixture, Scratch: filepath.Join(t.TempDir(), "actor-sample"), Depth: 8, Tail: -1, Fanout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if actorResult.ActorCount != 3 {
		t.Fatalf("result actor count = %d, want 3", actorResult.ActorCount)
	}
	fanoutResult, err := Run(ctx, RunOptions{
		Scenario: "submit_ack", Fixture: fixture, Scratch: filepath.Join(t.TempDir(), "fanout-sample"), Depth: 8, Tail: -1, Fanout: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fanoutResult.Fanout != 4 {
		t.Fatalf("result dependency fan-out = %d, want 4", fanoutResult.Fanout)
	}
	sample := filepath.Join(t.TempDir(), "sample")
	_, cleanup, err := Materialize(ctx, fixture, sample, Sample{Depth: 8, Tail: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	workspace, err := app.Open(ctx, sample)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projection.Actors) != 3 {
		t.Fatalf("projected actors = %d, want 3", len(snapshot.Projection.Actors))
	}
	bases, err := fanoutBases(manifest, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 4 || bases[0] != "git:sha1:"+manifest.Genesis+"#git:sha1:"+manifest.Heads["8"] {
		t.Fatalf("fan-out bases = %v", bases)
	}
	if _, err := fanoutBases(manifest, 8, 9); err == nil {
		t.Fatal("fanoutBases accepted fan-out above sample depth")
	}
}

func TestEveryProjectionShapeMaterializes(t *testing.T) {
	if os.Getenv("GITSEQ_PERF_INTEGRATION") == "" {
		t.Skip("set GITSEQ_PERF_INTEGRATION=1 for real signed fixture coverage")
	}
	ctx := context.Background()
	for _, shape := range []string{"linear", "assert_heavy", "open_request_heavy", "terminal_history_heavy", "artifact_staleness_heavy"} {
		t.Run(shape, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "fixture")
			manifest, err := Prepare(ctx, directory, FixturePlan{
				GeneratorVersion: "test.v1", Seed: 632, Depth: 9, Shape: shape, PayloadBuckets: []int{8},
			})
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := app.Open(ctx, directory)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := workspace.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Depth != manifest.Depth || manifest.ExactDigest == "" || manifest.LogicalDigest == "" {
				t.Fatalf("incomplete %s fixture: manifest=%+v snapshot depth=%d", shape, manifest, snapshot.Depth)
			}
		})
	}
}
