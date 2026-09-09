package perflane

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func validContract() Contract {
	checkpointCases := []CheckpointCase{
		{Depth: 257, Tail: 0},
		{Depth: 258, Tail: 1},
		{Depth: 267, Tail: 10},
		{Depth: 512, Tail: 255},
		{Depth: 513, Tail: 256},
		{Depth: 1_257, Tail: 1_000},
	}
	repetitions := make(map[string]int, len(requiredScenarios))
	warmups := make(map[string]int, len(requiredScenarios))
	timeouts := make(map[string]int, len(requiredScenarios))
	for _, scenario := range requiredScenarios {
		repetitions[scenario] = 100
		warmups[scenario] = 1
		timeouts[scenario] = 60
	}
	return Contract{
		SchemaVersion:    SchemaVersion,
		GeneratorVersion: "fixture-v1",
		Seed:             42,
		Depths:           RequiredDepths(),
		ActorCounts:      RequiredActorCounts(),
		DependencyFanout: FanoutAxis{
			Depth: 1_000, Widths: RequiredDependencyFanouts(), RelativeLimit: 0.10,
			PreviewMaxWidth: 64, FirstProductionMaxWidth: 256,
		},
		CheckpointTails:    RequiredCheckpointTails(),
		ProjectionShapes:   RequiredProjectionShapes(),
		PayloadBuckets:     []int{128, 1_024, 8_192},
		Concurrency:        []int{1, 4, 16},
		Scenarios:          RequiredScenarios(),
		Metrics:            RequiredMetrics(),
		Repetitions:        repetitions,
		Warmups:            warmups,
		TimeoutSeconds:     timeouts,
		SoakOperations:     10_000,
		SoakSeconds:        60,
		CheckpointCases:    checkpointCases,
		PercentileMinimums: PercentileMinimums{P95: 20, P99: 100},
	}
}

// envelopeContract is the smallest v3 contract: the v2 axes, the v3 metric
// list, and the two joint cells. Its PREVIEW cell sits at a checkpoint-case
// depth that the frozen depth axis does not carry.
func envelopeContract() Contract {
	contract := validContract()
	contract.SchemaVersion = SchemaVersionV3
	contract.Metrics = RequiredMetricsV3()
	contract.CheckpointCases = append(contract.CheckpointCases, CheckpointCase{Depth: 50_000, Tail: 255})
	contract.EnvelopeCases = []EnvelopeCase{{Depth: 50_000, Actors: 8}, {Depth: 500_000, Actors: 50}}
	return contract
}

func TestTrackedContractsBothLoad(t *testing.T) {
	for _, name := range []string{"contract-v2.json", "contract-v3.json"} {
		contract, err := LoadContract(filepath.Join("..", "..", "performance", name))
		if err != nil {
			t.Fatalf("LoadContract %s: %v", name, err)
		}
		switch name {
		case "contract-v2.json":
			if contract.SchemaVersion != SchemaVersion || len(contract.EnvelopeCases) != 0 {
				t.Fatalf("v2 contract = %s with %d envelope cells", contract.SchemaVersion, len(contract.EnvelopeCases))
			}
		case "contract-v3.json":
			want := []EnvelopeCase{{Depth: 50_000, Actors: 8}, {Depth: 500_000, Actors: 50}}
			if contract.SchemaVersion != SchemaVersionV3 || !reflect.DeepEqual(contract.EnvelopeCases, want) {
				t.Fatalf("v3 contract = %s with cells %+v", contract.SchemaVersion, contract.EnvelopeCases)
			}
			if !slices.Contains(contract.Metrics, "checkpoint_bytes") {
				t.Fatalf("v3 metrics omit checkpoint_bytes: %v", contract.Metrics)
			}
			// A warm_status sample pays a full cold status read in setup,
			// measured at about 134 s at 500,000 records, and the cold
			// scenarios pay it inside the measured window. v2's ceilings are
			// sized for the depths v2 reaches and would abort the tier.
			for scenario, floor := range map[string]int{
				"warm_status": 600, "cold_status": 900, "honest_fallback": 900, "checkpoint_restart": 900,
			} {
				if contract.TimeoutSeconds[scenario] < floor {
					t.Fatalf("v3 timeout_seconds[%q] = %d, want at least %d", scenario, contract.TimeoutSeconds[scenario], floor)
				}
			}
		}
	}
}

func TestEnvelopeContractValidation(t *testing.T) {
	if err := envelopeContract().Validate(); err != nil {
		t.Fatalf("valid v3 contract: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Contract)
		want   string
	}{
		{"metric missing", func(c *Contract) { c.Metrics = RequiredMetrics() }, "metrics"},
		{"metric on v2", func(c *Contract) {
			c.SchemaVersion = SchemaVersion
			c.EnvelopeCases = nil
		}, "metrics"},
		{"cells need v3", func(c *Contract) {
			c.SchemaVersion = SchemaVersion
			c.Metrics = RequiredMetrics()
		}, "envelope_cases requires schema_version"},
		{"depth unreached", func(c *Contract) { c.EnvelopeCases[0].Depth = 51_000 }, "already reaches"},
		{"depth is the actor axis", func(c *Contract) { c.EnvelopeCases[0].Depth = c.Depths[0] }, "must not be the smallest depth"},
		{"actors are the denominator", func(c *Contract) { c.EnvelopeCases[0].Actors = c.ActorCounts[0] }, "must exceed the smallest actor count"},
		{"actors off axis", func(c *Contract) { c.EnvelopeCases[0].Actors = 9 }, "actor_counts"},
		{"cells ordered", func(c *Contract) {
			c.EnvelopeCases[0], c.EnvelopeCases[1] = c.EnvelopeCases[1], c.EnvelopeCases[0]
		}, "ordered by depth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := envelopeContract()
			test.mutate(&contract)
			err := contract.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestParseAndLoadContract(t *testing.T) {
	want := validContract()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseContract(data)
	if err != nil {
		t.Fatalf("ParseContract: %v", err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.Seed != want.Seed {
		t.Fatalf("parsed contract = %#v", got)
	}

	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(path); err != nil {
		t.Fatalf("LoadContract: %v", err)
	}
}

func TestParseContractIsStrict(t *testing.T) {
	contract := validContract()
	data, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := append([]byte(nil), data[:len(data)-1]...)
	withUnknown = append(withUnknown, []byte(`,"surprise":true}`)...)
	if _, err := ParseContract(withUnknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := ParseContract(append(data, []byte(` {}`)...)); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("trailing value error = %v", err)
	}
}

func TestContractValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Contract)
		want   string
	}{
		{"schema", func(c *Contract) { c.SchemaVersion = "v2" }, "schema_version"},
		{"generator", func(c *Contract) { c.GeneratorVersion = "" }, "generator_version"},
		{"depths", func(c *Contract) { c.Depths[0] = 99 }, "depths"},
		{"actor counts", func(c *Contract) { c.ActorCounts[0] = 2 }, "actor_counts"},
		{"fanout depth absent", func(c *Contract) { c.DependencyFanout.Depth = 999 }, "dependency_fanout_axis.depth"},
		{"fanout depth too small", func(c *Contract) { c.DependencyFanout.Depth = 100 }, "at least its maximum width"},
		{"fanouts", func(c *Contract) { c.DependencyFanout.Widths[0] = 2 }, "dependency_fanout_axis.widths"},
		{"fanout limit", func(c *Contract) { c.DependencyFanout.RelativeLimit = 0 }, "relative_limit"},
		{"preview fanout", func(c *Contract) { c.DependencyFanout.PreviewMaxWidth = 8_192 }, "preview_max_width"},
		{"production fanout", func(c *Contract) { c.DependencyFanout.FirstProductionMaxWidth = 8_192 }, "first_production_max_width"},
		{"fanout envelope order", func(c *Contract) {
			c.DependencyFanout.PreviewMaxWidth = 256
			c.DependencyFanout.FirstProductionMaxWidth = 64
		}, "must not exceed"},
		{"tails", func(c *Contract) { c.CheckpointTails = c.CheckpointTails[:5] }, "checkpoint_tails"},
		{"shapes", func(c *Contract) { c.ProjectionShapes[0] = "other" }, "projection_shapes"},
		{"scenarios", func(c *Contract) { c.Scenarios[0] = "other" }, "scenarios"},
		{"metrics", func(c *Contract) { c.Metrics[0] = "other" }, "metrics"},
		{"payload order", func(c *Contract) { c.PayloadBuckets = []int{2, 1} }, "strictly increasing"},
		{"concurrency zero", func(c *Contract) { c.Concurrency = []int{0} }, "positive"},
		{"repetition missing", func(c *Contract) { delete(c.Repetitions, "startup") }, "exactly"},
		{"repetition zero", func(c *Contract) { c.Repetitions["startup"] = 0 }, "repetitions"},
		{"warmup negative", func(c *Contract) { c.Warmups["startup"] = -1 }, "warmups"},
		{"timeout zero", func(c *Contract) { c.TimeoutSeconds["startup"] = 0 }, "timeout_seconds"},
		{"soak operations", func(c *Contract) { c.SoakOperations = 0 }, "soak_operations"},
		{"soak seconds", func(c *Contract) { c.SoakSeconds = 0 }, "soak_seconds"},
		{"checkpoint empty", func(c *Contract) { c.CheckpointCases = nil }, "checkpoint_cases"},
		{"checkpoint invalid pair", func(c *Contract) { c.CheckpointCases[0] = CheckpointCase{Depth: 100, Tail: 1_000} }, "exceeds depth"},
		{"checkpoint order", func(c *Contract) {
			c.CheckpointCases[0], c.CheckpointCases[1] = c.CheckpointCases[1], c.CheckpointCases[0]
		}, "ordered"},
		{"checkpoint tail coverage", func(c *Contract) { c.CheckpointCases = c.CheckpointCases[:5] }, "cover tail 1000"},
		{"p95", func(c *Contract) { c.PercentileMinimums.P95 = 0 }, "p95"},
		{"p99", func(c *Contract) { c.PercentileMinimums.P99 = c.PercentileMinimums.P95 - 1 }, "p99"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := validContract()
			test.mutate(&contract)
			err := contract.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRequiredListsAreCopies(t *testing.T) {
	depths := RequiredDepths()
	depths[0] = 1
	if RequiredDepths()[0] != 100 {
		t.Fatal("RequiredDepths exposed mutable package state")
	}
	actors := RequiredActorCounts()
	actors[0] = 2
	if RequiredActorCounts()[0] != 1 {
		t.Fatal("RequiredActorCounts exposed mutable package state")
	}
	fanouts := RequiredDependencyFanouts()
	fanouts[0] = 2
	if RequiredDependencyFanouts()[0] != 1 {
		t.Fatal("RequiredDependencyFanouts exposed mutable package state")
	}
	scenarios := RequiredScenarios()
	scenarios[0] = "changed"
	if RequiredScenarios()[0] != "startup" {
		t.Fatal("RequiredScenarios exposed mutable package state")
	}
}
