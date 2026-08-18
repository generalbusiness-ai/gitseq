// Package perflane defines the deterministic, opt-in performance evidence
// contract. It has no dependency on production instrumentation.
package perflane

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

const SchemaVersion = "gitseq.performance/v1"

const (
	maxAxisValues     = 1_000
	maxRepetitions    = 10_000
	maxWarmups        = 10_000
	maxTimeoutSeconds = 24 * 60 * 60
	maxSoakOperations = 1_000_000_000
	maxSoakSeconds    = 7 * 24 * 60 * 60
)

var (
	requiredDepths           = []int{100, 1_000, 10_000, 100_000, 500_000}
	requiredActorCounts      = []int{1, 8, 50}
	requiredFanouts          = []int{1, 8, 16, 64, 256}
	requiredTails            = []int{0, 1, 10, 255, 256, 1_000}
	requiredProjectionShapes = []string{
		"linear",
		"assert_heavy",
		"open_request_heavy",
		"terminal_history_heavy",
		"artifact_staleness_heavy",
	}
	requiredScenarios = []string{
		"startup",
		"cold_status",
		"warm_status",
		"submit_ack",
		"submit_wait",
		"checkpoint_restart",
		"honest_fallback",
		"quiet_long_poll",
		"concurrent_read_write",
		"bounded_soak",
	}
	requiredMetrics = []string{
		"latency_ns",
		"throughput_ops_per_second",
		"cpu_ns",
		"allocations",
		"allocated_bytes",
		"peak_rss_bytes",
		"steady_memory_bytes",
		"gc_count",
		"gc_pause_ns",
		"git_process_count",
		"git_duration_ns",
		"filesystem_read_bytes",
		"filesystem_write_bytes",
		"queue_wait_ns",
		"checkpoint_ns",
		"response_build_ns",
		"response_encode_ns",
		"response_bytes",
		"correctness_digest",
	}
)

// RequiredDepths returns the contract's required fixture depths.
func RequiredDepths() []int { return slices.Clone(requiredDepths) }

// RequiredActorCounts returns the required roster-size axis.
func RequiredActorCounts() []int { return slices.Clone(requiredActorCounts) }

// RequiredDependencyFanouts returns the required causal fan-out axis.
func RequiredDependencyFanouts() []int { return slices.Clone(requiredFanouts) }

// RequiredCheckpointTails returns the contract's required checkpoint tails.
func RequiredCheckpointTails() []int { return slices.Clone(requiredTails) }

// RequiredProjectionShapes returns the required fixture shapes in stable order.
func RequiredProjectionShapes() []string { return slices.Clone(requiredProjectionShapes) }

// RequiredScenarios returns the required scenarios in stable order.
func RequiredScenarios() []string { return slices.Clone(requiredScenarios) }

// RequiredMetrics returns the required metrics in stable order.
func RequiredMetrics() []string { return slices.Clone(requiredMetrics) }

// Contract is the complete versioned input to an evidence run. Slices use
// contract order, while scenario maps must contain exactly one value for each
// required scenario.
type Contract struct {
	SchemaVersion      string             `json:"schema_version"`
	GeneratorVersion   string             `json:"generator_version"`
	Seed               uint64             `json:"seed"`
	Depths             []int              `json:"depths"`
	ActorCounts        []int              `json:"actor_counts"`
	DependencyFanouts  []int              `json:"dependency_fanouts"`
	CheckpointTails    []int              `json:"checkpoint_tails"`
	ProjectionShapes   []string           `json:"projection_shapes"`
	PayloadBuckets     []int              `json:"payload_buckets"`
	Concurrency        []int              `json:"concurrency"`
	Scenarios          []string           `json:"scenarios"`
	Metrics            []string           `json:"metrics"`
	Repetitions        map[string]int     `json:"repetitions"`
	Warmups            map[string]int     `json:"warmups"`
	TimeoutSeconds     map[string]int     `json:"timeout_seconds"`
	SoakOperations     int                `json:"soak_operations"`
	SoakSeconds        int                `json:"soak_seconds"`
	CheckpointCases    []CheckpointCase   `json:"checkpoint_cases"`
	PercentileMinimums PercentileMinimums `json:"percentile_minimums"`
}

type CheckpointCase struct {
	Depth int `json:"depth"`
	Tail  int `json:"tail"`
}

type PercentileMinimums struct {
	P95 int `json:"p95"`
	P99 int `json:"p99"`
}

// LoadContract loads a contract while rejecting unknown fields and trailing
// JSON. Validate is called before the contract is returned.
func LoadContract(path string) (Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Contract{}, err
	}
	return ParseContract(data)
}

// ParseContract decodes strict JSON and validates the versioned contract.
func ParseContract(data []byte) (Contract, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contract Contract
	if err := decoder.Decode(&contract); err != nil {
		return Contract{}, fmt.Errorf("decode performance contract: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return Contract{}, fmt.Errorf("decode performance contract: %w", err)
	}
	if err := contract.Validate(); err != nil {
		return Contract{}, err
	}
	return contract, nil
}

// Validate rejects incomplete contracts and unsafe run bounds.
func (c Contract) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if c.GeneratorVersion == "" {
		return errors.New("generator_version must not be empty")
	}
	if !slices.Equal(c.Depths, requiredDepths) {
		return fmt.Errorf("depths must be %v in that order", requiredDepths)
	}
	if !slices.Equal(c.ActorCounts, requiredActorCounts) {
		return fmt.Errorf("actor_counts must be %v in that order", requiredActorCounts)
	}
	if !slices.Equal(c.DependencyFanouts, requiredFanouts) {
		return fmt.Errorf("dependency_fanouts must be %v in that order", requiredFanouts)
	}
	if !slices.Equal(c.CheckpointTails, requiredTails) {
		return fmt.Errorf("checkpoint_tails must be %v in that order", requiredTails)
	}
	if !slices.Equal(c.ProjectionShapes, requiredProjectionShapes) {
		return fmt.Errorf("projection_shapes must be %v in that order", requiredProjectionShapes)
	}
	if !slices.Equal(c.Scenarios, requiredScenarios) {
		return fmt.Errorf("scenarios must be %v in that order", requiredScenarios)
	}
	if !slices.Equal(c.Metrics, requiredMetrics) {
		return fmt.Errorf("metrics must be %v in that order", requiredMetrics)
	}
	if err := validateIncreasingPositive("payload_buckets", c.PayloadBuckets); err != nil {
		return err
	}
	if err := validateIncreasingPositive("concurrency", c.Concurrency); err != nil {
		return err
	}
	if err := validateScenarioMap("repetitions", c.Repetitions, 1, maxRepetitions); err != nil {
		return err
	}
	if err := validateScenarioMap("warmups", c.Warmups, 0, maxWarmups); err != nil {
		return err
	}
	if err := validateScenarioMap("timeout_seconds", c.TimeoutSeconds, 1, maxTimeoutSeconds); err != nil {
		return err
	}
	if c.SoakOperations < 1 || c.SoakOperations > maxSoakOperations {
		return fmt.Errorf("soak_operations must be between 1 and %d", maxSoakOperations)
	}
	if c.SoakSeconds < 1 || c.SoakSeconds > maxSoakSeconds {
		return fmt.Errorf("soak_seconds must be between 1 and %d", maxSoakSeconds)
	}
	if err := validateCheckpointCases(c.CheckpointCases); err != nil {
		return err
	}
	if c.PercentileMinimums.P95 < 1 || c.PercentileMinimums.P95 > maxRepetitions {
		return fmt.Errorf("percentile_minimums.p95 must be between 1 and %d", maxRepetitions)
	}
	if c.PercentileMinimums.P99 < c.PercentileMinimums.P95 || c.PercentileMinimums.P99 > maxRepetitions {
		return fmt.Errorf("percentile_minimums.p99 must be between p95 and %d", maxRepetitions)
	}
	return nil
}

func validateIncreasingPositive(name string, values []int) error {
	if len(values) == 0 || len(values) > maxAxisValues {
		return fmt.Errorf("%s must contain between 1 and %d values", name, maxAxisValues)
	}
	for index, value := range values {
		if value < 1 {
			return fmt.Errorf("%s[%d] must be positive", name, index)
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be strictly increasing", name)
		}
	}
	return nil
}

func validateScenarioMap(name string, values map[string]int, minimum, maximum int) error {
	if len(values) != len(requiredScenarios) {
		return fmt.Errorf("%s must contain exactly the required scenarios", name)
	}
	for _, scenario := range requiredScenarios {
		value, ok := values[scenario]
		if !ok {
			return fmt.Errorf("%s is missing scenario %q", name, scenario)
		}
		if value < minimum || value > maximum {
			return fmt.Errorf("%s[%q] must be between %d and %d", name, scenario, minimum, maximum)
		}
	}
	return nil
}

func validateCheckpointCases(cases []CheckpointCase) error {
	if len(cases) == 0 || len(cases) > maxAxisValues {
		return fmt.Errorf("checkpoint_cases must contain between 1 and %d pairs", maxAxisValues)
	}
	seenTails := make(map[int]bool, len(requiredTails))
	for index, checkpoint := range cases {
		if checkpoint.Depth < 1 || checkpoint.Depth > requiredDepths[len(requiredDepths)-1] {
			return fmt.Errorf("checkpoint_cases[%d] depth must be between 1 and %d", index, requiredDepths[len(requiredDepths)-1])
		}
		if !slices.Contains(requiredTails, checkpoint.Tail) {
			return fmt.Errorf("checkpoint_cases[%d] has unsupported tail %d", index, checkpoint.Tail)
		}
		if checkpoint.Tail > checkpoint.Depth {
			return fmt.Errorf("checkpoint_cases[%d] tail %d exceeds depth %d", index, checkpoint.Tail, checkpoint.Depth)
		}
		if index > 0 {
			previous := cases[index-1]
			if previous.Depth > checkpoint.Depth || (previous.Depth == checkpoint.Depth && previous.Tail >= checkpoint.Tail) {
				return fmt.Errorf("checkpoint_cases must be unique and ordered by depth, then tail")
			}
		}
		seenTails[checkpoint.Tail] = true
	}
	for _, tail := range requiredTails {
		if !seenTails[tail] {
			return fmt.Errorf("checkpoint_cases must cover tail %d", tail)
		}
	}
	return nil
}
