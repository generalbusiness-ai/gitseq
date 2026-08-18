package perfscenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/perflane"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/telemetry"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// RunOptions selects one isolated worker operation.
type RunOptions struct {
	Scenario        string
	Fixture         string
	Scratch         string
	Depth           int
	Tail            int
	Concurrency     int
	Fanout          int
	SoakOperations  int
	SoakSeconds     int
	Trace2Path      string
	CPUProfilePath  string
	HeapProfilePath string
	Telemetry       bool
}

// OptionalMetric distinguishes an unavailable platform or scenario counter
// from a real zero.
type OptionalMetric struct {
	Value  *int64 `json:"value"`
	Reason string `json:"reason,omitempty"`
}

// Result is one raw sample. Aggregates are deliberately computed later so the
// evidence always retains the observations they summarize.
type Result struct {
	Schema            string                    `json:"schema"`
	Scenario          string                    `json:"scenario"`
	Depth             int                       `json:"depth"`
	CheckpointTail    *int                      `json:"checkpoint_tail,omitempty"`
	Concurrency       int                       `json:"concurrency,omitempty"`
	ActorCount        int                       `json:"actor_count"`
	Fanout            int                       `json:"dependency_fanout"`
	SetupNS           int64                     `json:"setup_ns"`
	LatencyNS         int64                     `json:"latency_ns"`
	Operations        int                       `json:"operations"`
	Throughput        float64                   `json:"throughput_ops_per_second"`
	CPUTimeNS         OptionalMetric            `json:"cpu_ns"`
	Allocations       uint64                    `json:"allocations"`
	AllocatedBytes    uint64                    `json:"allocated_bytes"`
	PeakRSSBytes      OptionalMetric            `json:"peak_rss_bytes"`
	SteadyMemoryBytes OptionalMetric            `json:"steady_memory_bytes"`
	HeapSystemBytes   uint64                    `json:"heap_system_bytes"`
	GCCount           uint32                    `json:"gc_count"`
	GCPauseNS         uint64                    `json:"gc_pause_ns"`
	ResponseBytes     int                       `json:"response_bytes"`
	ResponseBuildNS   OptionalMetric            `json:"response_build_ns"`
	ResponseEncodeNS  OptionalMetric            `json:"response_encode_ns"`
	QueueWaitNS       OptionalMetric            `json:"queue_wait_ns"`
	CheckpointNS      OptionalMetric            `json:"checkpoint_ns"`
	CASRetries        OptionalMetric            `json:"cas_retries"`
	GitProcessCount   OptionalMetric            `json:"git_process_count"`
	GitDurationNS     OptionalMetric            `json:"git_duration_ns"`
	FilesystemRead    OptionalMetric            `json:"filesystem_read_bytes"`
	FilesystemWrite   OptionalMetric            `json:"filesystem_write_bytes"`
	CorrectnessDigest string                    `json:"correctness_digest"`
	TrustedDigest     string                    `json:"trusted_digest"`
	SnapshotSource    app.SnapshotSource        `json:"snapshot_source,omitempty"`
	Fixture           FixtureEvidence           `json:"fixture"`
	Available         map[string]OptionalMetric `json:"platform_metrics,omitempty"`
}

type operationResult struct {
	response  []byte
	snapshot  *app.Snapshot
	workspace *app.Workspace
	source    app.SnapshotSource
	queueNS   *int64
	cas       *int64
}

// FixtureEvidence identifies the exact immutable fixture state used by one
// sample without repeating its depth-sized head map or retaining local paths.
type FixtureEvidence struct {
	GeneratorVersion string `json:"generator_version"`
	Seed             uint64 `json:"seed"`
	Shape            string `json:"shape"`
	Depth            int    `json:"depth"`
	ActorCount       int    `json:"actor_count"`
	Head             string `json:"head"`
	Checkpoint       string `json:"checkpoint,omitempty"`
	LogicalDigest    string `json:"logical_digest"`
	ExactDigest      string `json:"exact_digest"`
}

type preparedOperation func() (operationResult, int, error)

// Run materializes fresh writable state, performs one complete operation, and
// compares accelerated results with a cold full snapshot of that exact sample.
func Run(ctx context.Context, options RunOptions) (Result, error) {
	if options.Scenario == "" || options.Fixture == "" || options.Scratch == "" || options.Depth < 1 {
		return Result{}, errors.New("scenario, fixture, scratch, and positive depth are required")
	}
	setupStarted := time.Now()
	manifest, cleanup, err := Materialize(ctx, options.Fixture, options.Scratch, Sample{Depth: options.Depth, Tail: options.Tail})
	if err != nil {
		return Result{}, err
	}
	defer cleanup()
	var telemetryRuntime *telemetry.Runtime
	if options.Telemetry {
		telemetryRuntime, err = telemetry.NewInMemory()
		if err != nil {
			return Result{}, err
		}
		defer telemetryRuntime.Shutdown(context.Background())
	}
	operation, err := prepareOperation(ctx, options, telemetryRuntime)
	if err != nil {
		return Result{}, err
	}
	setupNS := time.Since(setupStarted).Nanoseconds()

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	usageBefore, usageBeforeErr := readProcessUsage()
	restoreTrace, err := enableTrace2(options.Trace2Path)
	if err != nil {
		return Result{}, err
	}
	stopProfile, err := startCPUProfile(options.CPUProfilePath)
	if err != nil {
		_ = restoreTrace()
		return Result{}, err
	}
	started := time.Now()
	observed, operations, err := operation()
	latency := time.Since(started)
	profileErr := stopProfile()
	restoreErr := restoreTrace()
	if err != nil {
		return Result{}, err
	}
	if restoreErr != nil {
		return Result{}, restoreErr
	}
	if profileErr != nil {
		return Result{}, profileErr
	}
	if observed.snapshot == nil && observed.workspace != nil {
		snapshot, snapshotErr := observed.workspace.Snapshot(ctx)
		if snapshotErr != nil {
			return Result{}, snapshotErr
		}
		observed.snapshot = &snapshot
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	usageAfter, usageAfterErr := readProcessUsage()
	runtime.GC()
	var steady runtime.MemStats
	runtime.ReadMemStats(&steady)
	if err := writeHeapProfile(options.HeapProfilePath); err != nil {
		return Result{}, err
	}

	result := Result{
		Schema: "gitseq.performance-sample.v1", Scenario: options.Scenario, Depth: options.Depth,
		Concurrency: options.Concurrency, ActorCount: manifest.ActorCount, Fanout: max(options.Fanout, 1),
		SetupNS: setupNS, LatencyNS: latency.Nanoseconds(), Operations: operations,
		Allocations: after.Mallocs - before.Mallocs, AllocatedBytes: after.TotalAlloc - before.TotalAlloc,
		SteadyMemoryBytes: available(int64(steady.HeapAlloc)), HeapSystemBytes: after.HeapSys,
		GCCount: after.NumGC - before.NumGC, GCPauseNS: pauseDelta(before, after),
		ResponseBytes: len(observed.response), SnapshotSource: observed.source, Fixture: fixtureEvidence(manifest, options.Depth, options.Tail),
		ResponseBuildNS:  unavailable("combined with complete operation"),
		ResponseEncodeNS: unavailable("combined with HTTP response encoding"),
		FilesystemRead:   unavailable("portable byte counter unavailable"),
		FilesystemWrite:  unavailable("portable byte counter unavailable"),
		QueueWaitNS:      unavailable("not applicable to scenario"),
		CheckpointNS:     unavailable("not applicable to scenario"),
		CASRetries:       unavailable("not exposed by scenario"),
		GitProcessCount:  unavailable("Git Trace2 diagnostic not requested"),
		GitDurationNS:    unavailable("Git Trace2 diagnostic not requested"),
	}
	if options.Trace2Path != "" {
		trace, openErr := os.Open(options.Trace2Path)
		if openErr != nil {
			return Result{}, fmt.Errorf("open Git Trace2 diagnostic: %w", openErr)
		}
		summary, parseErr := perflane.ParseTrace2(trace)
		closeErr := trace.Close()
		if parseErr != nil {
			return Result{}, fmt.Errorf("parse Git Trace2 diagnostic: %w", parseErr)
		}
		if closeErr != nil {
			return Result{}, fmt.Errorf("close Git Trace2 diagnostic: %w", closeErr)
		}
		result.GitProcessCount = available(int64(summary.ChildProcessCount))
		result.GitDurationNS = available(summary.CumulativeDuration.Nanoseconds())
	}
	if usageBeforeErr == nil && usageAfterErr == nil {
		result.CPUTimeNS = available(usageAfter.cpuNS - usageBefore.cpuNS)
		result.PeakRSSBytes = available(usageAfter.peakRSSBytes)
	} else {
		reason := "process usage unavailable"
		if usageAfterErr != nil {
			reason = usageAfterErr.Error()
		} else if usageBeforeErr != nil {
			reason = usageBeforeErr.Error()
		}
		result.CPUTimeNS = unavailable(reason)
		result.PeakRSSBytes = unavailable(reason)
	}
	if operations > 0 && latency > 0 {
		result.Throughput = float64(operations) / latency.Seconds()
	}
	if observed.queueNS != nil {
		result.QueueWaitNS = available(*observed.queueNS)
	}
	if observed.cas != nil {
		result.CASRetries = available(*observed.cas)
	}
	if options.Scenario == "checkpoint_restart" {
		tail := options.Tail
		result.CheckpointTail = &tail
		result.CheckpointNS = available(latency.Nanoseconds())
	}

	if observed.snapshot != nil {
		buildStarted := time.Now()
		summary := statusview.Build(observed.snapshot.Genesis, observed.snapshot.Head, observed.snapshot.Depth, observed.snapshot.Projection)
		result.ResponseBuildNS = available(time.Since(buildStarted).Nanoseconds())
		encodeStarted := time.Now()
		if _, encodeErr := json.Marshal(summary); encodeErr != nil {
			return Result{}, fmt.Errorf("encode status summary diagnostic: %w", encodeErr)
		}
		result.ResponseEncodeNS = available(time.Since(encodeStarted).Nanoseconds())
		result.CorrectnessDigest, err = snapshotDigest(*observed.snapshot)
		if err != nil {
			return Result{}, err
		}
	}
	trustedWorkspace, err := app.Open(ctx, options.Scratch)
	if err != nil {
		return Result{}, err
	}
	trusted, err := trustedWorkspace.Snapshot(ctx)
	if err != nil {
		return Result{}, err
	}
	result.TrustedDigest, err = snapshotDigest(trusted)
	if err != nil {
		return Result{}, err
	}
	if result.CorrectnessDigest == "" {
		result.CorrectnessDigest = result.TrustedDigest
	}
	if result.CorrectnessDigest != result.TrustedDigest {
		return Result{}, fmt.Errorf("scenario digest %s differs from trusted cold digest %s", result.CorrectnessDigest, result.TrustedDigest)
	}
	return result, nil
}

func fixtureEvidence(manifest Manifest, depth, tail int) FixtureEvidence {
	evidence := FixtureEvidence{
		GeneratorVersion: manifest.GeneratorVersion, Seed: manifest.Seed, Shape: manifest.Shape, Depth: depth,
		ActorCount: manifest.ActorCount,
		Head:       manifest.Heads[strconv.Itoa(depth)], LogicalDigest: manifest.LogicalDigest, ExactDigest: manifest.ExactDigest,
	}
	if tail >= 0 {
		evidence.Checkpoint = manifest.Checkpoints[strconv.Itoa(depth-tail)]
	}
	return evidence
}

func startCPUProfile(path string) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create CPU profile: %w", err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("start CPU profile: %w", err)
	}
	return func() error {
		pprof.StopCPUProfile()
		if err := file.Close(); err != nil {
			return fmt.Errorf("close CPU profile: %w", err)
		}
		return nil
	}, nil
}

func writeHeapProfile(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create heap profile: %w", err)
	}
	if err := pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("write heap profile: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close heap profile: %w", err)
	}
	return nil
}

func enableTrace2(path string) (func() error, error) {
	if path == "" {
		return func() error { return nil }, nil
	}
	previous, existed := os.LookupEnv("GIT_TRACE2_EVENT")
	if err := os.Setenv("GIT_TRACE2_EVENT", path); err != nil {
		return nil, fmt.Errorf("enable Git Trace2: %w", err)
	}
	return func() error {
		if existed {
			if err := os.Setenv("GIT_TRACE2_EVENT", previous); err != nil {
				return fmt.Errorf("restore Git Trace2: %w", err)
			}
			return nil
		}
		if err := os.Unsetenv("GIT_TRACE2_EVENT"); err != nil {
			return fmt.Errorf("restore Git Trace2: %w", err)
		}
		return nil
	}, nil
}

func prepareOperation(ctx context.Context, options RunOptions, telemetryRuntime *telemetry.Runtime) (preparedOperation, error) {
	observer := telemetryRuntime.Observer()
	handler := func(server *service.Server) http.Handler {
		if telemetryRuntime == nil {
			return server.Handler()
		}
		return telemetryRuntime.Handler(server.Handler())
	}
	switch options.Scenario {
	case "startup":
		return func() (operationResult, int, error) {
			workspace, err := app.OpenObserved(ctx, options.Scratch, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			server, err := service.NewObserved(workspace, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			body, err := request(handler(server), http.MethodGet, "/v0/status-summary", nil)
			if err != nil {
				return operationResult{}, 0, err
			}
			return operationResult{response: body, workspace: workspace}, 1, nil
		}, nil
	case "cold_status":
		return func() (operationResult, int, error) {
			workspace, err := app.OpenObserved(ctx, options.Scratch, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			server, err := service.NewObserved(workspace, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			body, err := request(handler(server), http.MethodGet, "/v0/status-summary", nil)
			if err != nil {
				return operationResult{}, 0, err
			}
			return operationResult{response: body, workspace: workspace}, 1, nil
		}, nil
	case "warm_status":
		workspace, server, err := openServerObserved(ctx, options.Scratch, observer)
		if err != nil {
			return nil, err
		}
		if _, err := request(handler(server), http.MethodGet, "/v0/status-summary", nil); err != nil {
			return nil, err
		}
		return func() (operationResult, int, error) {
			body, err := request(handler(server), http.MethodGet, "/v0/status-summary", nil)
			if err != nil {
				return operationResult{}, 0, err
			}
			return operationResult{response: body, workspace: workspace}, 1, nil
		}, nil
	case "submit_ack":
		workspace, err := app.OpenObserved(ctx, options.Scratch, observer)
		if err != nil {
			return nil, err
		}
		manifest, err := LoadManifest(options.Fixture)
		if err != nil {
			return nil, err
		}
		rests, err := fanoutBases(manifest, options.Depth, options.Fanout)
		if err != nil {
			return nil, err
		}
		return func() (operationResult, int, error) {
			submission, err := workspace.Act(ctx, "operator", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "performance acknowledgement",
				RestsOn: rests, IdempotencyKey: "performance-submit-ack",
			})
			if err != nil {
				return operationResult{}, 0, err
			}
			body, _ := json.Marshal(submission.Record)
			cas := int64(submission.Result.CASRetries)
			return operationResult{response: body, workspace: workspace, cas: &cas}, 1, nil
		}, nil
	case "submit_wait":
		return prepareSubmitWait(ctx, options, telemetryRuntime)
	case "checkpoint_restart":
		return func() (operationResult, int, error) {
			workspace, err := app.OpenObserved(ctx, options.Scratch, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			loaded, err := workspace.SnapshotWithSource(ctx)
			if err != nil {
				return operationResult{}, 0, err
			}
			if loaded.Source != app.SnapshotSourceSignedCheckpointTail {
				return operationResult{}, 0, fmt.Errorf("checkpoint source = %s, want %s", loaded.Source, app.SnapshotSourceSignedCheckpointTail)
			}
			body, _ := json.Marshal(loaded.Snapshot)
			return operationResult{response: body, snapshot: &loaded.Snapshot, source: loaded.Source}, 1, nil
		}, nil
	case "honest_fallback":
		return func() (operationResult, int, error) {
			workspace, err := app.OpenObserved(ctx, options.Scratch, observer)
			if err != nil {
				return operationResult{}, 0, err
			}
			loaded, err := workspace.SnapshotWithSource(ctx)
			if err != nil {
				return operationResult{}, 0, err
			}
			if loaded.Source != app.SnapshotSourceColdFullAudit {
				return operationResult{}, 0, fmt.Errorf("fallback source = %s, want %s", loaded.Source, app.SnapshotSourceColdFullAudit)
			}
			body, _ := json.Marshal(loaded.Snapshot)
			return operationResult{response: body, snapshot: &loaded.Snapshot, source: loaded.Source}, 1, nil
		}, nil
	case "quiet_long_poll":
		workspace, server, err := openServerObserved(ctx, options.Scratch, observer)
		if err != nil {
			return nil, err
		}
		statusBody, err := request(handler(server), http.MethodGet, "/v0/status", nil)
		if err != nil {
			return nil, err
		}
		var status service.Status
		if err := json.Unmarshal(statusBody, &status); err != nil {
			return nil, err
		}
		waitBody, err := json.Marshal(service.WaitRequest{Cursor: status.Cursor, TimeoutMS: 20})
		if err != nil {
			return nil, err
		}
		return func() (operationResult, int, error) {
			waitStarted := time.Now()
			body, err := request(handler(server), http.MethodPost, "/v0/wait", waitBody)
			waitNS := time.Since(waitStarted).Nanoseconds()
			if err != nil {
				return operationResult{}, 0, err
			}
			return operationResult{response: body, workspace: workspace, queueNS: &waitNS}, 1, nil
		}, nil
	case "concurrent_read_write":
		return prepareConcurrentReadWrite(ctx, options, telemetryRuntime)
	case "bounded_soak":
		return prepareSoak(ctx, options, telemetryRuntime)
	default:
		return nil, fmt.Errorf("unknown performance scenario %q", options.Scenario)
	}
}

func fanoutBases(manifest Manifest, depth, fanout int) ([]string, error) {
	if fanout == 0 {
		fanout = 1
	}
	if fanout < 1 || fanout > depth {
		return nil, fmt.Errorf("dependency fan-out %d must be between 1 and sample depth %d", fanout, depth)
	}
	bases := make([]string, 0, fanout)
	genesisPrefix, _, ok := strings.Cut(manifest.SeedEvent, "#")
	if !ok {
		return nil, errors.New("fixture seed event has no repository prefix")
	}
	_, format, ok := strings.Cut(genesisPrefix, ":")
	if !ok {
		return nil, errors.New("fixture seed event has no object format")
	}
	format, _, ok = strings.Cut(format, ":")
	if !ok || (format != "sha1" && format != "sha256") {
		return nil, errors.New("fixture seed event has an invalid object format")
	}
	for position := depth; position > depth-fanout; position-- {
		commit := manifest.Heads[strconv.Itoa(position)]
		if commit == "" {
			return nil, fmt.Errorf("fixture has no head at depth %d for dependency fan-out", position)
		}
		bases = append(bases, genesisPrefix+"#git:"+format+":"+commit)
	}
	return bases, nil
}

func prepareSubmitWait(ctx context.Context, options RunOptions, telemetryRuntime *telemetry.Runtime) (preparedOperation, error) {
	observer := telemetryRuntime.Observer()
	workspace, server, err := openServerObserved(ctx, options.Scratch, observer)
	if err != nil {
		return nil, err
	}
	handler := server.Handler()
	if telemetryRuntime != nil {
		handler = telemetryRuntime.Handler(handler)
	}
	if _, err := request(handler, http.MethodPost, "/v0/presence", mustJSON(map[string]any{
		"actor": "operator", "session": "performance-session", "ttl_ms": 30000,
	})); err != nil {
		return nil, err
	}
	statusBody, err := request(handler, http.MethodGet, "/v0/status", nil)
	if err != nil {
		return nil, err
	}
	var status service.Status
	if err := json.Unmarshal(statusBody, &status); err != nil {
		return nil, err
	}
	waitInput := mustJSON(service.WaitRequest{Cursor: status.Cursor, TimeoutMS: 3000})
	manifest, err := LoadManifest(options.Fixture)
	if err != nil {
		return nil, err
	}
	act := mustJSON(map[string]any{
		"session": "performance-session", "act": "state", "kind": "assert",
		"text": "performance waiter notification", "rests_on": []string{manifest.SeedEvent},
		"idempotency_key": "performance-submit-wait",
	})
	return func() (operationResult, int, error) {
		type waitResult struct {
			body []byte
			err  error
		}
		waitDone := make(chan waitResult, 1)
		go func() {
			body, waitErr := request(handler, http.MethodPost, "/v0/wait", waitInput)
			waitDone <- waitResult{body: body, err: waitErr}
		}()
		waitStarted := time.Now()
		if _, err := request(handler, http.MethodPost, "/v0/act", act); err != nil {
			return operationResult{}, 0, err
		}
		select {
		case completed := <-waitDone:
			if completed.err != nil {
				return operationResult{}, 0, completed.err
			}
			queueNS := time.Since(waitStarted).Nanoseconds()
			snapshot, err := workspace.Snapshot(ctx)
			return operationResult{response: completed.body, snapshot: &snapshot, queueNS: &queueNS}, 1, err
		case <-ctx.Done():
			return operationResult{}, 0, ctx.Err()
		}
	}, nil
}

func prepareConcurrentReadWrite(ctx context.Context, options RunOptions, telemetryRuntime *telemetry.Runtime) (preparedOperation, error) {
	if options.Concurrency < 1 {
		return nil, errors.New("concurrent_read_write requires positive concurrency")
	}
	observer := telemetryRuntime.Observer()
	workspace, server, err := openServerObserved(ctx, options.Scratch, observer)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(options.Fixture)
	if err != nil {
		return nil, err
	}
	handler := server.Handler()
	if telemetryRuntime != nil {
		handler = telemetryRuntime.Handler(handler)
	}
	if _, err := request(handler, http.MethodPost, "/v0/presence", mustJSON(map[string]any{
		"actor": "operator", "session": "performance-session", "ttl_ms": 30000,
	})); err != nil {
		return nil, err
	}
	return func() (operationResult, int, error) {
		type response struct {
			body []byte
			err  error
		}
		start := make(chan struct{})
		operationCount := options.Concurrency * 2
		results := make(chan response, operationCount)
		var group sync.WaitGroup
		operations := make([]func() ([]byte, error), 0, operationCount)
		for index := 0; index < options.Concurrency; index++ {
			operations = append(operations, func() ([]byte, error) {
				return request(handler, http.MethodGet, "/v0/status-summary", nil)
			})
			act := mustJSON(map[string]any{
				"session": "performance-session", "act": "state", "kind": "assert",
				"text":            fmt.Sprintf("concurrent operation %d", index),
				"rests_on":        []string{manifest.SeedEvent},
				"idempotency_key": fmt.Sprintf("performance-concurrent-%d", index),
			})
			operations = append(operations, func() ([]byte, error) {
				return request(handler, http.MethodPost, "/v0/act", act)
			})
		}
		for _, operation := range operations {
			group.Add(1)
			go func(run func() ([]byte, error)) {
				defer group.Done()
				<-start
				body, runErr := run()
				results <- response{body: body, err: runErr}
			}(operation)
		}
		close(start)
		group.Wait()
		close(results)
		var combined []byte
		for result := range results {
			if result.err != nil {
				return operationResult{}, 0, result.err
			}
			combined = append(combined, result.body...)
		}
		return operationResult{response: combined, workspace: workspace}, operationCount, nil
	}, nil
}

func prepareSoak(ctx context.Context, options RunOptions, telemetryRuntime *telemetry.Runtime) (preparedOperation, error) {
	observer := telemetryRuntime.Observer()
	workspace, server, err := openServerObserved(ctx, options.Scratch, observer)
	if err != nil {
		return nil, err
	}
	operations := options.SoakOperations
	if operations < 1 {
		operations = 32
	}
	seconds := options.SoakSeconds
	if seconds < 1 {
		seconds = 60
	}
	manifest, err := LoadManifest(options.Fixture)
	if err != nil {
		return nil, err
	}
	return func() (operationResult, int, error) {
		var last []byte
		completed := 0
		deadline := time.Now().Add(time.Duration(seconds) * time.Second)
		for index := 0; index < operations; index++ {
			if index > 0 && time.Now().After(deadline) {
				break
			}
			if index%4 == 0 {
				handler := server.Handler()
				if telemetryRuntime != nil {
					handler = telemetryRuntime.Handler(handler)
				}
				last, err = request(handler, http.MethodGet, "/v0/status-summary", nil)
			} else {
				_, err = workspace.Act(ctx, "operator", app.Act{
					Verb: app.VerbState, Kind: workroom.KindAssert, Text: "bounded soak",
					RestsOn: []string{manifest.SeedEvent}, IdempotencyKey: fmt.Sprintf("performance-soak-%d", index),
				})
			}
			if err != nil {
				return operationResult{}, 0, err
			}
			completed++
		}
		return operationResult{response: last, workspace: workspace}, completed, nil
	}, nil
}

func openServer(ctx context.Context, repository string) (*app.Workspace, *service.Server, error) {
	return openServerObserved(ctx, repository, nil)
}

func openServerObserved(ctx context.Context, repository string, observer observe.Observer) (*app.Workspace, *service.Server, error) {
	workspace, err := app.OpenObserved(ctx, repository, observer)
	if err != nil {
		return nil, nil, err
	}
	server, err := service.NewObserved(workspace, observer)
	return workspace, server, err
}

func request(handler http.Handler, method, path string, body []byte) ([]byte, error) {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 200 || response.Code >= 300 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, response.Code, response.Body.String())
	}
	return append([]byte(nil), response.Body.Bytes()...), nil
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func mustManifest(directory string) Manifest {
	manifest, err := LoadManifest(directory)
	if err != nil {
		panic(err)
	}
	return manifest
}

func snapshotDigest(snapshot app.Snapshot) (string, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func pauseDelta(before, after runtime.MemStats) uint64 {
	if after.PauseTotalNs < before.PauseTotalNs {
		return 0
	}
	return after.PauseTotalNs - before.PauseTotalNs
}

func available(value int64) OptionalMetric { return OptionalMetric{Value: &value} }

func unavailable(reason string) OptionalMetric { return OptionalMetric{Reason: reason} }

// StableResult sorts map keys through encoding/json and additionally orders
// optional platform names before callers write newline JSON collections.
func StableResult(result Result) ([]byte, error) {
	if result.Available != nil {
		keys := make([]string, 0, len(result.Available))
		for key := range result.Available {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(map[string]OptionalMetric, len(keys))
		for _, key := range keys {
			ordered[key] = result.Available[key]
		}
		result.Available = ordered
	}
	return json.Marshal(result)
}

// InboxFrame is referenced here so compile-time coverage keeps the performance
// worker aligned with the addressed-wait contract it measures.
var _ = nexus.InboxFrame{}
