// gitseq-perf runs Gitseq's opt-in, versioned performance evidence lane.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/perflane"
	"github.com/generalbusiness-ai/gitseq/internal/perfscenario"
)

const (
	defaultContract = "performance/contract-v2.json"
	evidenceSchema  = "gitseq.performance-evidence.v2"
	fanoutSchema    = "gitseq.performance-fanout-axis/v1"
)

type runCase struct {
	Scenario    string
	Shape       string
	Depth       int
	Tail        int
	Concurrency int
	ActorCount  int
	Fanout      int
}

type fixtureKey struct {
	shape      string
	actorCount int
}

func (c runCase) fixtureKey() fixtureKey {
	return fixtureKey{shape: c.Shape, actorCount: max(c.ActorCount, 1)}
}

func (c runCase) name() string {
	tail := ""
	if c.Scenario == "checkpoint_restart" {
		tail = fmt.Sprintf("/tail-%04d", c.Tail)
	}
	concurrency := ""
	if c.Concurrency > 0 {
		concurrency = fmt.Sprintf("/concurrency-%02d", c.Concurrency)
	}
	actors := ""
	if c.ActorCount > 1 {
		actors = fmt.Sprintf("/actors-%03d", c.ActorCount)
	}
	fanout := ""
	if c.Fanout > 0 {
		fanout = fmt.Sprintf("/fanout-%03d", c.Fanout)
	}
	return fmt.Sprintf("%s/shape-%s/depth-%06d%s%s%s%s", c.Scenario, c.Shape, c.Depth, tail, concurrency, actors, fanout)
}

type sampleEnvelope struct {
	Case     string                  `json:"case"`
	Revision perflane.Revision       `json:"revision"`
	Round    int                     `json:"round"`
	Position int                     `json:"position"`
	Result   perfscenario.Result     `json:"result"`
	Trace2   *perflane.Trace2Summary `json:"trace2,omitempty"`
	Error    string                  `json:"error,omitempty"`
}

type runEvidence struct {
	Schema          string                           `json:"schema"`
	ContractDigest  string                           `json:"contract_digest"`
	Contract        perflane.Contract                `json:"contract"`
	HarnessCommit   string                           `json:"harness_commit"`
	BaseCommit      string                           `json:"base_commit,omitempty"`
	CandidateCommit string                           `json:"candidate_commit"`
	Tier            string                           `json:"tier"`
	Environment     perflane.EnvironmentEvidence     `json:"environment"`
	StartedAt       string                           `json:"started_at"`
	Samples         []sampleEnvelope                 `json:"samples"`
	Latency         map[string]perflane.Distribution `json:"latency_ns"`
	FanoutAxis      map[string]fanoutAxisEvidence    `json:"fanout_axis,omitempty"`
	Outcome         string                           `json:"outcome"`
	Benchstat       perflane.Evidence[string]        `json:"benchstat"`
}

type fanoutAxisEvidence struct {
	Schema                  string              `json:"schema"`
	Depth                   int                 `json:"depth"`
	RecordedRepetitions     int                 `json:"recorded_repetitions"`
	OneBaseMedianNS         float64             `json:"one_base_median_ns"`
	RelativeLimit           float64             `json:"relative_limit"`
	PreviewMaxWidth         int                 `json:"preview_max_width"`
	FirstProductionMaxWidth int                 `json:"first_production_max_width"`
	PreviewVerdict          string              `json:"preview_verdict"`
	FirstProductionVerdict  string              `json:"first_production_verdict"`
	CaseOrder               []string            `json:"case_order"`
	Measurements            []fanoutMeasurement `json:"measurements"`
}

type fanoutMeasurement struct {
	Case                string  `json:"case"`
	Width               int     `json:"width"`
	MedianNS            float64 `json:"median_ns"`
	RelativeIncrement   float64 `json:"relative_increment"`
	AbsoluteIncrementMS float64 `json:"absolute_increment_ms"`
	WithinRelativeLimit bool    `json:"within_relative_limit"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gitseq-perf:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: gitseq-perf <validate|prepare|run|compare|overhead|worker> [options]")
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	switch arguments[0] {
	case "validate":
		flags := flag.NewFlagSet("validate", flag.ContinueOnError)
		contractPath := flags.String("contract", filepath.Join(root, defaultContract), "contract path")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		contract, err := perflane.LoadContract(*contractPath)
		if err != nil {
			return err
		}
		digest, err := perflane.CorrectnessDigest(contract)
		if err != nil {
			return err
		}
		fmt.Printf("valid %s %s\n", contract.SchemaVersion, digest)
		return nil
	case "prepare":
		return prepareCommand(ctx, root, arguments[1:])
	case "run":
		return laneCommand(ctx, root, false, false, arguments[1:])
	case "compare":
		return laneCommand(ctx, root, true, false, arguments[1:])
	case "overhead":
		return laneCommand(ctx, root, true, true, arguments[1:])
	case "worker":
		return workerCommand(ctx, arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func prepareCommand(ctx context.Context, root string, arguments []string) error {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	contractPath := flags.String("contract", filepath.Join(root, defaultContract), "contract path")
	output := flags.String("output", "", "new fixture directory")
	shape := flags.String("shape", "linear", "projection shape")
	depth := flags.Int("depth", 0, "maximum fixture depth")
	actors := flags.Int("actors", 1, "fixture actor count")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *output == "" || *depth < 1 {
		return errors.New("prepare requires --output and positive --depth")
	}
	contract, err := perflane.LoadContract(*contractPath)
	if err != nil {
		return err
	}
	if !contains(contract.ProjectionShapes, *shape) {
		return fmt.Errorf("shape %q is not in the contract", *shape)
	}
	if !containsInt(contract.ActorCounts, *actors) {
		return fmt.Errorf("actor count %d is not in the contract", *actors)
	}
	checkpointDepths := checkpointDepths(contract, *depth)
	manifest, err := perfscenario.Prepare(ctx, *output, perfscenario.FixturePlan{
		GeneratorVersion: contract.GeneratorVersion, Seed: contract.Seed, Depth: *depth,
		Shape: *shape, PayloadBuckets: contract.PayloadBuckets, CheckpointDepths: checkpointDepths, ActorCount: *actors,
	})
	if err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	fmt.Println(string(encoded))
	return nil
}

func workerCommand(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	scenario := flags.String("scenario", "", "scenario name")
	fixture := flags.String("fixture", "", "prepared fixture")
	scratch := flags.String("scratch", "", "fresh sample directory")
	depth := flags.Int("depth", 0, "sample depth")
	tail := flags.Int("tail", -1, "checkpoint tail; -1 means absent")
	soakOperations := flags.Int("soak-operations", 0, "bounded soak operations")
	soakSeconds := flags.Int("soak-seconds", 0, "bounded soak duration")
	concurrency := flags.Int("concurrency", 0, "concurrent operation count")
	fanout := flags.Int("fanout", 1, "dependency fan-out for the measured submission")
	trace2 := flags.String("trace2", "", "Git Trace2 event output")
	cpuProfile := flags.String("cpu-profile", "", "CPU profile output")
	heapProfile := flags.String("heap-profile", "", "heap profile output")
	withTelemetry := flags.Bool("telemetry", false, "enable the in-memory OpenTelemetry SDK")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *trace2 != "" {
		absolute, err := filepath.Abs(*trace2)
		if err != nil {
			return err
		}
		*trace2 = absolute
	}
	result, err := perfscenario.Run(ctx, perfscenario.RunOptions{
		Scenario: *scenario, Fixture: *fixture, Scratch: *scratch, Depth: *depth,
		Tail: *tail, Concurrency: *concurrency, Fanout: *fanout, SoakOperations: *soakOperations, SoakSeconds: *soakSeconds, Trace2Path: *trace2,
		CPUProfilePath: *cpuProfile, HeapProfilePath: *heapProfile,
		Telemetry: *withTelemetry,
	})
	if err != nil {
		return err
	}
	encoded, err := perfscenario.StableResult(result)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func laneCommand(ctx context.Context, root string, compare, overhead bool, arguments []string) error {
	flags := flag.NewFlagSet("lane", flag.ContinueOnError)
	contractPath := flags.String("contract", filepath.Join(root, defaultContract), "contract path")
	tier := flags.String("tier", "smoke", "smoke, standard, full, or fanout")
	output := flags.String("output", filepath.Join(root, "performance", "evidence"), "evidence directory")
	baseRef := flags.String("base", "main", "exact base ref")
	candidateRef := flags.String("candidate", "HEAD", "exact candidate ref")
	acceptBaseline := flags.Bool("accept-baseline", false, "explicitly write tracked baseline")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *acceptBaseline {
		return errors.New("baseline acceptance requires a reviewed evidence file; automatic acceptance is intentionally unsupported")
	}
	contract, err := perflane.LoadContract(*contractPath)
	if err != nil {
		return err
	}
	cases, err := casesForTier(contract, *tier)
	if err != nil {
		return err
	}
	contractDigest, err := perflane.CorrectnessDigest(contract)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	harnessCommit, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	environment := collectEnvironment(ctx, root)
	if err := environment.Validate(); err != nil {
		return err
	}
	evidence := runEvidence{
		Schema: evidenceSchema, ContractDigest: contractDigest, Contract: contract,
		HarnessCommit: harnessCommit, Tier: *tier, Environment: environment,
		StartedAt: time.Now().UTC().Format(time.RFC3339), Latency: make(map[string]perflane.Distribution),
		Outcome: "pass", Benchstat: perflane.Unavailable[string]("comparison not requested"),
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	binaries := map[perflane.Revision]string{perflane.CandidateRevision: executable}
	if compare {
		if dirty := environment.DirtyWorktree; dirty.Value == nil || *dirty.Value {
			return errors.New("compare refuses a dirty harness checkout")
		}
		base, err := resolveCommit(ctx, root, *baseRef)
		if err != nil {
			return err
		}
		candidate, err := resolveCommit(ctx, root, *candidateRef)
		if err != nil {
			return err
		}
		evidence.BaseCommit, evidence.CandidateCommit = base, candidate
		if overhead {
			if base != candidate {
				return errors.New("overhead comparison requires --base and --candidate to resolve to the same commit")
			}
			binaries[perflane.BaseRevision] = executable
		} else {
			cleanup, built, err := buildComparedWorkers(ctx, root, base, candidate)
			if err != nil {
				return err
			}
			defer cleanup()
			binaries = built
		}
		evidence.Benchstat = perflane.Unavailable[string]("benchstat has not run")
	} else {
		candidate, err := resolveCommit(ctx, root, "HEAD")
		if err != nil {
			return err
		}
		evidence.CandidateCommit = candidate
	}

	fixtures, err := ensureFixtures(ctx, root, contract, contractDigest, cases)
	if err != nil {
		return err
	}
	rawPath := filepath.Join(*output, "samples.jsonl")
	rawFile, err := os.OpenFile(rawPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer rawFile.Close()
	benchFiles := map[perflane.Revision]*os.File{}
	for revision := range binaries {
		file, err := os.OpenFile(filepath.Join(*output, string(revision)+".bench"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer file.Close()
		benchFiles[revision] = file
	}

	latencies := make(map[string][]float64)
	primarySamples := 0
	for _, selected := range cases {
		fixture := fixtures[selected.fixtureKey()]
		warmups, repetitions := tierCounts(contract, *tier, selected.Scenario)
		if compare && repetitions < 2 {
			repetitions = 2
		}
		for revision, binary := range binaries {
			for warmup := 0; warmup < warmups; warmup++ {
				if _, err := runWorker(ctx, binary, fixture, selected, contract, "", overhead && revision == perflane.CandidateRevision); err != nil {
					return fmt.Errorf("warmup %s %s: %w", revision, selected.name(), err)
				}
			}
		}
		var order []perflane.SampleRun
		if compare {
			order, err = perflane.AlternatingSampleOrder(repetitions)
			if err != nil {
				return err
			}
		} else {
			for round := 1; round <= repetitions; round++ {
				order = append(order, perflane.SampleRun{Round: round, Position: 1, Revision: perflane.CandidateRevision})
			}
		}
		for _, scheduled := range order {
			result, err := runWorker(ctx, binaries[scheduled.Revision], fixture, selected, contract, "", overhead && scheduled.Revision == perflane.CandidateRevision)
			envelope := sampleEnvelope{Case: selected.name(), Revision: scheduled.Revision, Round: scheduled.Round, Position: scheduled.Position, Result: result}
			if err != nil {
				envelope.Error = err.Error()
				evidence.Outcome = "error"
			}
			if err := appendJSON(rawFile, envelope); err != nil {
				return err
			}
			evidence.Samples = append(evidence.Samples, envelope)
			primarySamples++
			if err != nil {
				return fmt.Errorf("sample %s %s: %w", scheduled.Revision, selected.name(), err)
			}
			key := string(scheduled.Revision) + "/" + selected.name()
			latencies[key] = append(latencies[key], float64(result.LatencyNS))
			fmt.Fprintf(benchFiles[scheduled.Revision], "BenchmarkGitseq/%s 1 %d ns/op %d B/op %d allocs/op\n",
				selected.name(), result.LatencyNS, result.AllocatedBytes, result.Allocations)
		}
	}

	// Diagnostic reruns deliberately follow all primary case blocks. In
	// particular, they cannot split the consecutive fan-out denominator and
	// width distributions with profiling or Trace2 work.
	for _, selected := range cases {
		fixture := fixtures[selected.fixtureKey()]
		tracePath := filepath.Join(*output, sanitize(selected.name())+".trace2.json")
		profilePrefix := filepath.Join(*output, "profiles", sanitize(selected.name()))
		if err := os.MkdirAll(filepath.Dir(profilePrefix), 0o755); err != nil {
			return err
		}
		if err := os.Remove(tracePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		traceResult, traceErr := runWorkerDiagnostic(ctx, binaries[perflane.CandidateRevision], fixture, selected, contract, tracePath, profilePrefix, overhead)
		if traceErr != nil {
			return fmt.Errorf("Trace2 diagnostic %s: %w", selected.name(), traceErr)
		}
		traceFile, err := os.Open(tracePath)
		if err != nil {
			return err
		}
		traceSummary, err := perflane.ParseTrace2(traceFile)
		traceFile.Close()
		if err != nil {
			return fmt.Errorf("Trace2 diagnostic %s: %w", selected.name(), err)
		}
		if err := os.Remove(tracePath); err != nil {
			return err
		}
		if traceResult.GitProcessCount.Value == nil || *traceResult.GitProcessCount.Value != int64(traceSummary.ChildProcessCount) ||
			traceResult.GitDurationNS.Value == nil || *traceResult.GitDurationNS.Value != traceSummary.CumulativeDuration.Nanoseconds() {
			return fmt.Errorf("Trace2 diagnostic %s did not populate matching result metrics", selected.name())
		}
		traceEnvelope := sampleEnvelope{Case: selected.name(), Revision: perflane.CandidateRevision, Round: 0, Position: 0, Result: traceResult, Trace2: &traceSummary}
		if err := appendJSON(rawFile, traceEnvelope); err != nil {
			return err
		}
		evidence.Samples = append(evidence.Samples, traceEnvelope)
	}

	for name, values := range latencies {
		distribution, err := perflane.Summarize(values, contract.PercentileMinimums)
		if err != nil {
			return err
		}
		evidence.Latency[name] = distribution
	}
	evidence.FanoutAxis, err = summarizeFanoutAxis(contract, cases, evidence.Latency)
	if err != nil {
		return err
	}
	if compare {
		benchstat, err := runBenchstat(ctx, filepath.Join(*output, "base.bench"), filepath.Join(*output, "candidate.bench"))
		if err != nil {
			evidence.Benchstat = perflane.Unavailable[string](err.Error())
		} else {
			evidence.Benchstat = perflane.Available(benchstat)
			if err := os.WriteFile(filepath.Join(*output, "benchstat.txt"), []byte(benchstat), 0o644); err != nil {
				return err
			}
		}
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*output, "evidence.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: wrote %s (%d primary samples)\n", evidence.Outcome, *output, primarySamples)
	return nil
}

func runWorker(ctx context.Context, binary, fixture string, selected runCase, contract perflane.Contract, trace2 string, telemetry bool) (perfscenario.Result, error) {
	return runWorkerDiagnostic(ctx, binary, fixture, selected, contract, trace2, "", telemetry)
}

func runWorkerDiagnostic(ctx context.Context, binary, fixture string, selected runCase, contract perflane.Contract, trace2, profilePrefix string, telemetry bool) (perfscenario.Result, error) {
	scratch, err := os.MkdirTemp("", "gitseq-performance-sample.")
	if err != nil {
		return perfscenario.Result{}, err
	}
	if err := os.Remove(scratch); err != nil {
		return perfscenario.Result{}, err
	}
	defer os.RemoveAll(scratch)
	timeout := time.Duration(contract.TimeoutSeconds[selected.Scenario]) * time.Second
	workerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	arguments := []string{"worker", "--scenario", selected.Scenario, "--fixture", fixture, "--scratch", scratch,
		"--depth", strconv.Itoa(selected.Depth), "--tail", strconv.Itoa(selected.Tail),
		"--concurrency", strconv.Itoa(selected.Concurrency),
		"--fanout", strconv.Itoa(max(selected.Fanout, 1)),
		"--soak-operations", strconv.Itoa(contract.SoakOperations),
		"--soak-seconds", strconv.Itoa(contract.SoakSeconds)}
	if trace2 != "" {
		arguments = append(arguments, "--trace2", trace2)
	}
	if profilePrefix != "" {
		arguments = append(arguments, "--cpu-profile", profilePrefix+".cpu.pprof", "--heap-profile", profilePrefix+".heap.pprof")
	}
	if telemetry {
		arguments = append(arguments, "--telemetry")
	}
	command := exec.CommandContext(workerCtx, binary, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return perfscenario.Result{}, fmt.Errorf("worker: %w: %s", err, output)
	}
	var result perfscenario.Result
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return perfscenario.Result{}, fmt.Errorf("decode worker result: %w: %s", err, output)
	}
	if err := validateWorkerResult(selected, result); err != nil {
		return perfscenario.Result{}, err
	}
	return result, nil
}

func validateWorkerResult(selected runCase, result perfscenario.Result) error {
	wantActors := selected.fixtureKey().actorCount
	if result.ActorCount != wantActors {
		return fmt.Errorf("worker result %s actor_count = %d, want %d", selected.name(), result.ActorCount, wantActors)
	}
	wantFanout := max(selected.Fanout, 1)
	if result.Fanout != wantFanout {
		return fmt.Errorf("worker result %s dependency_fanout = %d, want %d", selected.name(), result.Fanout, wantFanout)
	}
	return nil
}

func casesForTier(contract perflane.Contract, tier string) ([]runCase, error) {
	if tier != "smoke" && tier != "standard" && tier != "full" && tier != "fanout" {
		return nil, errors.New("tier must be smoke, standard, full, or fanout")
	}
	benchmarkCases, err := perflane.BenchmarkCases(contract)
	if err != nil {
		return nil, err
	}
	allowedDepth := func(depth int) bool {
		switch tier {
		case "smoke":
			return depth <= 267
		case "standard":
			return depth <= 10_000
		default:
			return true
		}
	}
	var result []runCase
	for _, candidate := range benchmarkCases {
		if tier == "fanout" {
			if candidate.Fanout == 0 {
				continue
			}
		} else {
			if !allowedDepth(candidate.Depth) {
				continue
			}
			if tier == "smoke" && candidate.Scenario != "checkpoint_restart" && candidate.Depth != 100 {
				continue
			}
		}
		tail := -1
		if candidate.CheckpointTail != nil {
			tail = *candidate.CheckpointTail
		}
		result = append(result, runCase{Scenario: candidate.Scenario, Shape: "linear", Depth: candidate.Depth, Tail: tail, Concurrency: candidate.Concurrency, ActorCount: max(candidate.ActorCount, 1), Fanout: candidate.Fanout})
	}
	if tier != "fanout" {
		for _, shape := range contract.ProjectionShapes[1:] {
			result = append(result, runCase{Scenario: "cold_status", Shape: shape, Depth: 100, Tail: -1, ActorCount: 1})
		}
	}
	return result, nil
}

func tierCounts(contract perflane.Contract, tier, scenario string) (int, int) {
	switch tier {
	case "smoke":
		return 0, 1
	case "standard":
		warmups := min(contract.Warmups[scenario], 1)
		return warmups, min(contract.Repetitions[scenario], 5)
	case "fanout":
		return contract.Warmups[scenario], contract.Repetitions[scenario]
	default:
		return contract.Warmups[scenario], contract.Repetitions[scenario]
	}
}

func summarizeFanoutAxis(contract perflane.Contract, cases []runCase, distributions map[string]perflane.Distribution) (map[string]fanoutAxisEvidence, error) {
	var axisCases []runCase
	for _, selected := range cases {
		if selected.Fanout > 0 {
			axisCases = append(axisCases, selected)
		}
	}
	if len(axisCases) == 0 {
		return nil, nil
	}
	if len(axisCases) != len(contract.DependencyFanout.Widths) {
		return nil, fmt.Errorf("fan-out block has %d cases, want %d", len(axisCases), len(contract.DependencyFanout.Widths))
	}
	caseOrder := make([]string, len(axisCases))
	for index, selected := range axisCases {
		if selected.Scenario != "submit_ack" || selected.Depth != contract.DependencyFanout.Depth || selected.Fanout != contract.DependencyFanout.Widths[index] {
			return nil, fmt.Errorf("fan-out block case %d = %+v, want depth %d width %d", index, selected, contract.DependencyFanout.Depth, contract.DependencyFanout.Widths[index])
		}
		caseOrder[index] = selected.name()
	}

	summaries := make(map[string]fanoutAxisEvidence)
	for _, revision := range []perflane.Revision{perflane.BaseRevision, perflane.CandidateRevision} {
		baselineKey := string(revision) + "/" + axisCases[0].name()
		baseline, ok := distributions[baselineKey]
		if !ok {
			continue
		}
		if baseline.P50.Value == nil || *baseline.P50.Value <= 0 {
			return nil, fmt.Errorf("fan-out denominator %s has no positive median", baselineKey)
		}
		medianOne := *baseline.P50.Value
		summary := fanoutAxisEvidence{
			Schema: fanoutSchema, Depth: contract.DependencyFanout.Depth,
			RecordedRepetitions: baseline.Samples, OneBaseMedianNS: medianOne,
			RelativeLimit:           contract.DependencyFanout.RelativeLimit,
			PreviewMaxWidth:         contract.DependencyFanout.PreviewMaxWidth,
			FirstProductionMaxWidth: contract.DependencyFanout.FirstProductionMaxWidth,
			PreviewVerdict:          "pass", FirstProductionVerdict: "pass",
			CaseOrder: append([]string(nil), caseOrder...), Measurements: make([]fanoutMeasurement, 0, len(axisCases)),
		}
		for _, selected := range axisCases {
			key := string(revision) + "/" + selected.name()
			distribution, ok := distributions[key]
			if !ok || distribution.P50.Value == nil {
				return nil, fmt.Errorf("fan-out case %s has no median", key)
			}
			if distribution.Samples != baseline.Samples {
				return nil, fmt.Errorf("fan-out case %s has %d samples, denominator has %d", key, distribution.Samples, baseline.Samples)
			}
			median := *distribution.P50.Value
			relativeIncrement := median/medianOne - 1
			withinLimit := median <= medianOne*(1+contract.DependencyFanout.RelativeLimit)
			summary.Measurements = append(summary.Measurements, fanoutMeasurement{
				Case: selected.name(), Width: selected.Fanout, MedianNS: median,
				RelativeIncrement:   relativeIncrement,
				AbsoluteIncrementMS: (median - medianOne) / float64(time.Millisecond),
				WithinRelativeLimit: withinLimit,
			})
			if !withinLimit {
				if selected.Fanout <= contract.DependencyFanout.PreviewMaxWidth {
					summary.PreviewVerdict = "miss"
				}
				if selected.Fanout <= contract.DependencyFanout.FirstProductionMaxWidth {
					summary.FirstProductionVerdict = "miss"
				}
			}
		}
		summaries[string(revision)] = summary
	}
	if len(summaries) == 0 {
		return nil, errors.New("fan-out cases were selected but no revision distributions were recorded")
	}
	return summaries, nil
}

func ensureFixtures(ctx context.Context, root string, contract perflane.Contract, digest string, cases []runCase) (map[fixtureKey]string, error) {
	depths := make(map[fixtureKey]int)
	for _, selected := range cases {
		key := selected.fixtureKey()
		depths[key] = max(depths[key], selected.Depth)
	}
	fixtures := make(map[fixtureKey]string)
	for key, depth := range depths {
		directory := filepath.Join(root, "performance", "fixtures", digest[:16]+"-"+key.shape+"-actors-"+strconv.Itoa(key.actorCount)+"-"+strconv.Itoa(depth))
		if manifest, err := perfscenario.LoadManifest(directory); err == nil {
			if manifest.GeneratorVersion != contract.GeneratorVersion || manifest.Seed != contract.Seed || manifest.Shape != key.shape || manifest.Depth != depth || manifest.ActorCount != key.actorCount {
				return nil, fmt.Errorf("cached fixture %s does not match its key", directory)
			}
			fixtures[key] = directory
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		manifest, err := perfscenario.Prepare(ctx, directory, perfscenario.FixturePlan{
			GeneratorVersion: contract.GeneratorVersion, Seed: contract.Seed, Depth: depth,
			Shape: key.shape, PayloadBuckets: contract.PayloadBuckets, CheckpointDepths: checkpointDepths(contract, depth), ActorCount: key.actorCount,
		})
		if err != nil {
			return nil, err
		}
		if manifest.ExactDigest == "" {
			return nil, errors.New("prepared fixture has no exact digest")
		}
		fixtures[key] = directory
	}
	return fixtures, nil
}

func checkpointDepths(contract perflane.Contract, maxDepth int) []int {
	set := make(map[int]bool)
	for _, checkpoint := range contract.CheckpointCases {
		depth := checkpoint.Depth - checkpoint.Tail
		if checkpoint.Depth <= maxDepth {
			set[depth] = true
		}
	}
	values := make([]int, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Ints(values)
	return values
}

func buildComparedWorkers(ctx context.Context, root, base, candidate string) (func(), map[perflane.Revision]string, error) {
	temporary, err := os.MkdirTemp("", "gitseq-performance-compare.")
	if err != nil {
		return nil, nil, err
	}
	worktrees := make(map[perflane.Revision]string)
	binaries := make(map[perflane.Revision]string)
	cleanup := func() {
		for _, worktree := range worktrees {
			_ = exec.Command("git", "-C", root, "worktree", "remove", "--force", worktree).Run()
		}
		_ = os.RemoveAll(temporary)
	}
	for revision, commit := range map[perflane.Revision]string{perflane.BaseRevision: base, perflane.CandidateRevision: candidate} {
		worktree := filepath.Join(temporary, string(revision))
		command := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "--detach", worktree, commit)
		if output, err := command.CombinedOutput(); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("create %s worktree: %w: %s", revision, err, output)
		}
		worktrees[revision] = worktree
		for _, path := range []string{"cmd/gitseq-perf", "internal/perflane", "internal/perfscenario", defaultContract} {
			if err := overlay(filepath.Join(root, path), filepath.Join(worktree, path)); err != nil {
				cleanup()
				return nil, nil, err
			}
		}
		binary := filepath.Join(temporary, "gitseq-perf-"+string(revision))
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/gitseq-perf")
		build.Dir = worktree
		if output, err := build.CombinedOutput(); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("build %s worker: %w: %s", revision, err, output)
		}
		binaries[revision] = binary
	}
	return cleanup, binaries, nil
}

func overlay(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		content, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, content, info.Mode().Perm())
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("overlay refuses symlink %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode().Perm())
	})
}

func collectEnvironment(ctx context.Context, root string) perflane.EnvironmentEvidence {
	dirtyOutput, dirtyErr := gitOutput(ctx, root, "status", "--porcelain")
	dirty := dirtyErr == nil && dirtyOutput != ""
	gitVersion, gitErr := output(ctx, root, "git", "--version")
	cpuModel, cpuErr := firstOutput(ctx, root,
		[]string{"sysctl", "-n", "machdep.cpu.brand_string"},
		[]string{"sh", "-c", "LC_ALL=C lscpu | sed -n 's/^Model name:[[:space:]]*//p'"})
	filesystem, filesystemErr := firstOutput(ctx, root,
		[]string{"stat", "-f", "%T", root},
		[]string{"stat", "-f", "-c", "%T", root})
	memory, memoryErr := memoryBytes(ctx, root)
	containerCPU, containerCPUErr := containerCPULimit()
	containerMemory, containerMemoryErr := containerMemoryLimit()
	environment := perflane.EnvironmentEvidence{
		OS: perflane.Available(runtime.GOOS), Architecture: perflane.Available(runtime.GOARCH),
		GoVersion: perflane.Available(runtime.Version()), LogicalCPUs: perflane.Available(runtime.NumCPU()),
		DirtyWorktree: perflane.Available(dirty), PowerMode: perflane.Unavailable[string]("portable power-mode collector unavailable"),
	}
	environment.GitVersion = stringEvidence(gitVersion, gitErr)
	environment.CPUModel = stringEvidence(cpuModel, cpuErr)
	environment.Filesystem = stringEvidence(filesystem, filesystemErr)
	if memoryErr != nil {
		environment.MemoryBytes = perflane.Unavailable[uint64](memoryErr.Error())
	} else {
		environment.MemoryBytes = perflane.Available(memory)
	}
	environment.ContainerCPU = stringEvidence(containerCPU, containerCPUErr)
	if containerMemoryErr != nil {
		environment.ContainerMemory = perflane.Unavailable[uint64](containerMemoryErr.Error())
	} else {
		environment.ContainerMemory = perflane.Available(containerMemory)
	}
	return environment
}

func containerCPULimit() (string, error) {
	content, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return "", errors.New("cgroup v2 CPU limit unavailable")
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 || fields[0] == "max" {
		return "", errors.New("cgroup v2 CPU is unconstrained")
	}
	return fields[0] + "/" + fields[1], nil
}

func containerMemoryLimit() (uint64, error) {
	content, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0, errors.New("cgroup v2 memory limit unavailable")
	}
	value := strings.TrimSpace(string(content))
	if value == "max" {
		return 0, errors.New("cgroup v2 memory is unconstrained")
	}
	return strconv.ParseUint(value, 10, 64)
}

func memoryBytes(ctx context.Context, root string) (uint64, error) {
	if runtime.GOOS == "darwin" {
		value, err := output(ctx, root, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return 0, err
		}
		return strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	}
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
			return kilobytes * 1024, err
		}
	}
	return 0, errors.New("MemTotal is unavailable")
}

func stringEvidence(value string, err error) perflane.Evidence[string] {
	if err != nil || strings.TrimSpace(value) == "" {
		reason := "collector returned no value"
		if err != nil {
			reason = err.Error()
		}
		return perflane.Unavailable[string](reason)
	}
	return perflane.Available(strings.TrimSpace(value))
}

func resolveCommit(ctx context.Context, root, reference string) (string, error) {
	if reference == "" || strings.HasPrefix(reference, "-") || !regexp.MustCompile(`^[A-Za-z0-9._/@{}^~:+-]+$`).MatchString(reference) {
		return "", fmt.Errorf("unsafe Git ref %q", reference)
	}
	return gitOutput(ctx, root, "rev-parse", "--verify", reference+"^{commit}")
}

func runBenchstat(ctx context.Context, base, candidate string) (string, error) {
	path, err := exec.LookPath("benchstat")
	if err != nil {
		return "", errors.New("pinned benchstat is not installed")
	}
	command := exec.CommandContext(ctx, path, base, candidate)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("benchstat: %w: %s", err, output)
	}
	return string(output), nil
}

func moduleRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("go.mod not found")
		}
		directory = parent
	}
}

func gitOutput(ctx context.Context, root string, arguments ...string) (string, error) {
	return output(ctx, root, "git", arguments...)
}

func output(ctx context.Context, directory, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	content, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, content)
	}
	return strings.TrimSpace(string(content)), nil
}

func firstOutput(ctx context.Context, directory string, commands ...[]string) (string, error) {
	var failures []string
	for _, command := range commands {
		value, err := output(ctx, directory, command[0], command[1:]...)
		if err == nil && value != "" {
			return value, nil
		}
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	return "", errors.New(strings.Join(failures, "; "))
}

func appendJSON(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

func sanitize(value string) string {
	return strings.NewReplacer("/", "-", " ", "-").Replace(value)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
