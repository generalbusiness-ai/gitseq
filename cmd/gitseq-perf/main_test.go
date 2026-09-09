package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/perflane"
	"github.com/generalbusiness-ai/gitseq/internal/perfscenario"
)

func testContract(t *testing.T) perflane.Contract {
	t.Helper()
	contract, err := perflane.LoadContract(filepath.Join("..", "..", defaultContract))
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestCasesForTierRemainBoundedAndDeterministic(t *testing.T) {
	contract := testContract(t)
	first, err := casesForTier(contract, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	second, err := casesForTier(contract, "smoke")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || !reflect.DeepEqual(first, second) {
		t.Fatalf("smoke cases are not stable: %#v / %#v", first, second)
	}
	if len(first) != 20 {
		t.Fatalf("smoke case count = %d, want 20", len(first))
	}
	var concurrency []int
	for _, selected := range first {
		if selected.Depth > 267 {
			t.Fatalf("smoke case escaped its depth bound: %+v", selected)
		}
		if selected.Scenario == "concurrent_read_write" {
			concurrency = append(concurrency, selected.Concurrency)
		}
	}
	if !reflect.DeepEqual(concurrency, []int{1, 4, 16}) {
		t.Fatalf("smoke concurrency = %v, want [1 4 16]", concurrency)
	}
	var actors, fanouts []int
	for _, selected := range first {
		if selected.ActorCount > 1 {
			actors = append(actors, selected.ActorCount)
		}
		if selected.Fanout > 0 {
			fanouts = append(fanouts, selected.Fanout)
		}
	}
	if !reflect.DeepEqual(actors, []int{8, 50}) || len(fanouts) != 0 {
		t.Fatalf("smoke scale axes = actors %v fanouts %v", actors, fanouts)
	}
	if _, err := casesForTier(contract, "unbounded"); err == nil {
		t.Fatal("unknown tier was accepted")
	}
}

func TestFanoutTierIsOneConsecutiveVersionedBlock(t *testing.T) {
	contract := testContract(t)
	cases, err := casesForTier(contract, "fanout")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"submit_ack/shape-linear/depth-001000/fanout-001",
		"submit_ack/shape-linear/depth-001000/fanout-008",
		"submit_ack/shape-linear/depth-001000/fanout-016",
		"submit_ack/shape-linear/depth-001000/fanout-064",
		"submit_ack/shape-linear/depth-001000/fanout-256",
	}
	var got []string
	for _, selected := range cases {
		got = append(got, selected.name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fan-out tier = %v, want %v", got, want)
	}
	if warmups, repetitions := tierCounts(contract, "fanout", "submit_ack"); warmups != 5 || repetitions != 100 {
		t.Fatalf("fan-out counts = %d warmups, %d repetitions", warmups, repetitions)
	}
}

func TestFanoutSummaryKeepsSignedRatioAndMillisecondIncrements(t *testing.T) {
	contract := testContract(t)
	cases, err := casesForTier(contract, "fanout")
	if err != nil {
		t.Fatal(err)
	}
	medians := []float64{100_000_000, 110_000_000, 90_000_000, 120_000_000, 105_000_000}
	distributions := make(map[string]perflane.Distribution)
	for index, selected := range cases {
		distributions["candidate/"+selected.name()] = perflane.Distribution{
			Samples: 100,
			P50:     perflane.Available(medians[index]),
		}
	}
	summaries, err := summarizeFanoutAxis(contract, cases, distributions)
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries["candidate"]
	if summary.Schema != fanoutSchema || summary.Depth != 1_000 || summary.RecordedRepetitions != 100 || summary.OneBaseMedianNS != medians[0] || summary.RelativeLimit != 0.10 {
		t.Fatalf("summary identity = %#v", summary)
	}
	if summary.PreviewVerdict != "miss" || summary.FirstProductionVerdict != "miss" {
		t.Fatalf("fan-out verdicts = %q / %q", summary.PreviewVerdict, summary.FirstProductionVerdict)
	}
	if got := summary.Measurements[2]; got.Width != 16 || math.Abs(got.RelativeIncrement-(-0.1)) > 1e-12 || got.AbsoluteIncrementMS != -10 || !got.WithinRelativeLimit {
		t.Fatalf("negative increment = %#v", got)
	}
	if got := summary.Measurements[3]; got.Width != 64 || math.Abs(got.RelativeIncrement-0.2) > 1e-12 || got.AbsoluteIncrementMS != 20 || got.WithinRelativeLimit {
		t.Fatalf("positive increment = %#v", got)
	}
}

func TestFanoutSummarySeparatesPreviewFromFirstProduction(t *testing.T) {
	contract := testContract(t)
	cases, err := casesForTier(contract, "fanout")
	if err != nil {
		t.Fatal(err)
	}
	medians := []float64{100_000_000, 105_000_000, 105_000_000, 105_000_000, 120_000_000}
	distributions := make(map[string]perflane.Distribution)
	for index, selected := range cases {
		distributions["candidate/"+selected.name()] = perflane.Distribution{
			Samples: 100,
			P50:     perflane.Available(medians[index]),
		}
	}

	summaries, err := summarizeFanoutAxis(contract, cases, distributions)
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries["candidate"]
	if summary.PreviewVerdict != "pass" || summary.FirstProductionVerdict != "miss" {
		t.Fatalf("fan-out verdicts = %q / %q, want pass / miss", summary.PreviewVerdict, summary.FirstProductionVerdict)
	}
	last := summary.Measurements[len(summary.Measurements)-1]
	if last.Width != 256 || last.WithinRelativeLimit {
		t.Fatalf("first-production-only miss = %#v", last)
	}
}

func TestCheckpointDepthsAreUniqueAndSorted(t *testing.T) {
	tests := []struct {
		maximum int
		want    []int
	}{
		{600, []int{257}},
		{50_000, []int{257, 49_745}},
		{500_000, []int{257, 49_745, 499_745}},
	}
	for _, test := range tests {
		got := checkpointDepths(testContract(t), test.maximum)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("checkpoint depths through %d = %v, want %v", test.maximum, got, test.want)
		}
	}
}

func TestFullTierIncludesNearHeadCheckpointCases(t *testing.T) {
	cases, err := casesForTier(testContract(t), "full")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"checkpoint_restart/shape-linear/depth-050000/tail-0255": false,
		"checkpoint_restart/shape-linear/depth-500000/tail-0255": false,
	}
	for _, selected := range cases {
		if _, ok := want[selected.name()]; ok {
			want[selected.name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("full tier is missing %s", name)
		}
	}
}

func TestMemoryTierIsOnlyTheLinearColdDepthAxis(t *testing.T) {
	contract := testContract(t)
	cases, err := casesForTier(contract, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(contract.Depths) {
		t.Fatalf("memory cases = %d, want %d", len(cases), len(contract.Depths))
	}
	for index, selected := range cases {
		if selected.Scenario != "cold_status" || selected.Shape != "linear" || selected.Depth != contract.Depths[index] || selected.Tail != -1 || selected.ActorCount != 1 || selected.Fanout != 0 || selected.Concurrency != 0 {
			t.Fatalf("memory case %d = %+v", index, selected)
		}
	}
	if warmups, repetitions := tierCounts(contract, "memory", "cold_status"); warmups != 0 || repetitions != 2 {
		t.Fatalf("memory population = %d warmups / %d repetitions, want 0 / 2", warmups, repetitions)
	}
}

func TestEnvelopeTierIsTheBoundedTwoEnvelopeBlock(t *testing.T) {
	contract, err := perflane.LoadContract(filepath.Join("..", "..", "performance", "contract-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := casesForTier(contract, "envelope")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cold_status/shape-linear/depth-500000",
		"warm_status/shape-linear/depth-500000",
		"checkpoint_restart/shape-linear/depth-050000/tail-0255",
		"checkpoint_restart/shape-linear/depth-500000/tail-0255",
		"honest_fallback/shape-linear/depth-500000",
		"cold_status/shape-linear/depth-050000",
		"cold_status/shape-linear/depth-050000/actors-008",
		"warm_status/shape-linear/depth-050000",
		"honest_fallback/shape-linear/depth-050000",
		"cold_status/shape-linear/depth-500000/actors-050",
	}
	var got []string
	for _, selected := range cases {
		got = append(got, selected.name())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("envelope tier = %v, want %v", got, want)
	}
	for _, scenario := range []string{"checkpoint_restart", "honest_fallback", "warm_status", "cold_status"} {
		if warmups, repetitions := tierCounts(contract, "envelope", scenario); warmups != 0 || repetitions != 2 {
			t.Fatalf("envelope %s population = %d warmups / %d repetitions, want 0 / 2", scenario, warmups, repetitions)
		}
	}
	// Each envelope depth must carry both cold reads the actor-count cost is
	// measured from, and at 50,000 they must be adjacent.
	for _, envelope := range contract.EnvelopeCases {
		var reads []int
		for _, selected := range cases {
			if selected.Scenario == "cold_status" && selected.Depth == envelope.Depth {
				reads = append(reads, selected.ActorCount)
			}
		}
		if !reflect.DeepEqual(reads, []int{1, envelope.Actors}) {
			t.Fatalf("cold reads at depth %d = %v, want [1 %d]", envelope.Depth, reads, envelope.Actors)
		}
	}
	if got[5] != "cold_status/shape-linear/depth-050000" || got[6] != "cold_status/shape-linear/depth-050000/actors-008" {
		t.Fatalf("the 50,000 cold reads are not adjacent: %q then %q", got[5], got[6])
	}

	// The tier must be selection only: the v2 contract names no cells, so it
	// selects nothing rather than falling back to a depth-bounded matrix.
	empty, err := casesForTier(testContract(t), "envelope")
	if err != nil || len(empty) != 0 {
		t.Fatalf("envelope tier on contract v2 = %d cases, %v", len(empty), err)
	}
}

// TestEnvelopeTierTakesOnlyTheNearHeadCheckpointTail pins the tier against a
// contract that names a second checkpoint case at an envelope depth: the
// envelope claims a near-head restart, not every tail the contract carries.
func TestEnvelopeTierTakesOnlyTheNearHeadCheckpointTail(t *testing.T) {
	contract, err := perflane.LoadContract(filepath.Join("..", "..", "performance", "contract-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(contract.CheckpointCases, func(c perflane.CheckpointCase) bool {
		return c.Depth == 50_000 && c.Tail == envelopeCheckpointTail
	})
	if index < 0 {
		t.Fatal("contract v3 has no tail-255 checkpoint case at 50,000")
	}
	contract.CheckpointCases = slices.Insert(contract.CheckpointCases, index+1, perflane.CheckpointCase{Depth: 50_000, Tail: 1_000})
	cases, err := casesForTier(contract, "envelope")
	if err != nil {
		t.Fatal(err)
	}
	var tails []int
	for _, selected := range cases {
		if selected.Scenario == "checkpoint_restart" && selected.Depth == 50_000 {
			tails = append(tails, selected.Tail)
		}
	}
	if !reflect.DeepEqual(tails, []int{envelopeCheckpointTail}) {
		t.Fatalf("checkpoint tails at 50,000 = %v, want [%d]", tails, envelopeCheckpointTail)
	}
}

// TestFailedSampleStillPublishesEvidence pins the recovery a lost sample used
// to destroy: the run exits non-zero, but the contract, environment and the
// samples that did complete survive in evidence.json.
func TestFailedSampleStillPublishesEvidence(t *testing.T) {
	output := t.TempDir()
	evidence := runEvidence{
		Schema: evidenceSchema, ContractDigest: "digest", HarnessCommit: "head", Tier: "envelope",
		Outcome: "pass",
		Samples: []sampleEnvelope{{
			Case: "cold_status/shape-linear/depth-500000", Revision: perflane.CandidateRevision,
			Round: 1, Position: 1, Error: "worker: signal: killed",
		}},
	}
	cause := errors.New("sample candidate cold_status/shape-linear/depth-500000: worker: signal: killed")
	returned := failRun(output, evidence, cause)
	if !errors.Is(returned, cause) {
		t.Fatalf("failRun returned %v, want the original cause", returned)
	}
	content, err := os.ReadFile(filepath.Join(output, "evidence.json"))
	if err != nil {
		t.Fatalf("failed run published no evidence: %v", err)
	}
	var published runEvidence
	if err := json.Unmarshal(content, &published); err != nil {
		t.Fatal(err)
	}
	if published.Outcome != "error" {
		t.Fatalf("published outcome = %q, want error", published.Outcome)
	}
	if published.ContractDigest != "digest" || published.HarnessCommit != "head" || len(published.Samples) != 1 {
		t.Fatalf("published evidence lost the run: %+v", published)
	}
	if published.Samples[0].Error != "worker: signal: killed" {
		t.Fatalf("published sample error = %q", published.Samples[0].Error)
	}
	if evidence.Outcome != "pass" {
		t.Fatal("failRun mutated its caller's evidence")
	}
}

// TestRecordCheckpointBytesReadsTheRestoredObject pins the metric the lane
// parent adds. It reads the fixture's own checkpoint blob, so the figure is
// the stored object's size and a compared base worker needs no accessor.
func TestRecordCheckpointBytesReadsTheRestoredObject(t *testing.T) {
	ctx := context.Background()
	fixture := filepath.Join(t.TempDir(), "fixture")
	manifest, err := perfscenario.Prepare(ctx, fixture, perfscenario.FixturePlan{
		GeneratorVersion: "checkpoint-bytes-test.v1", Seed: 632, Depth: 267, Shape: "linear",
		PayloadBuckets: []int{8}, CheckpointDepths: []int{257}, ActorCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := manifest.Checkpoints["257"]
	// Ask Git for the size directly, so the expectation does not travel
	// through the code under test.
	sized := exec.CommandContext(ctx, "git", "cat-file", "-s", checkpoint+":checkpoint")
	sized.Dir = fixture
	sizeOutput, err := sized.CombinedOutput()
	if err != nil {
		t.Fatalf("git cat-file -s: %v: %s", err, sizeOutput)
	}
	want, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if err != nil || want <= 0 {
		t.Fatalf("checkpoint blob size = %q: %v", sizeOutput, err)
	}

	restored := perfscenario.Result{Fixture: perfscenario.FixtureEvidence{Checkpoint: checkpoint}}
	restart := runCase{Scenario: "checkpoint_restart", Shape: "linear", Depth: 267, Tail: 10, ActorCount: 1}
	if err := recordCheckpointBytes(ctx, fixture, restart, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.CheckpointBytes != want {
		t.Fatalf("checkpoint_bytes = %d, want the restored object's %d", restored.CheckpointBytes, want)
	}

	verify := perfscenario.Result{}
	fallback := runCase{Scenario: "honest_fallback", Shape: "linear", Depth: 267, Tail: -1, ActorCount: 1}
	if err := recordCheckpointBytes(ctx, fixture, fallback, &verify); err != nil {
		t.Fatal(err)
	}
	if verify.CheckpointBytes != 0 {
		t.Fatalf("cold verify recorded %d checkpoint bytes with no checkpoint", verify.CheckpointBytes)
	}
	// A checkpoint identifier is concatenated into a revision expression and
	// handed to Git, so anything that is not a bare object name must be
	// refused here, by name, before Git runs.
	for _, hostile := range []string{"", "--output=/tmp/escape", "-c", checkpoint + ":extra", "HEAD"} {
		hostileResult := perfscenario.Result{Fixture: perfscenario.FixtureEvidence{Checkpoint: hostile}}
		err := recordCheckpointBytes(ctx, fixture, restart, &hostileResult)
		if err == nil || !strings.Contains(err.Error(), "checkpoint_restart/shape-linear/depth-000267/tail-0010 recorded no usable checkpoint object") {
			t.Fatalf("checkpoint object %q error = %v", hostile, err)
		}
	}
}

func TestEnsureFixturesRejectsCachedActorCountMismatch(t *testing.T) {
	contract := testContract(t)
	digest, err := perflane.CorrectnessDigest(contract)
	if err != nil {
		t.Fatal(err)
	}
	selected := runCase{Scenario: "cold_status", Shape: "linear", Depth: 100, Tail: -1, ActorCount: 8}
	key := selected.fixtureKey()
	root := t.TempDir()
	directory := filepath.Join(root, "performance", "fixtures", digest[:16]+"-"+key.shape+"-actors-8-100")
	if _, err := perfscenario.Prepare(context.Background(), directory, perfscenario.FixturePlan{
		GeneratorVersion: contract.GeneratorVersion,
		Seed:             contract.Seed,
		Depth:            selected.Depth,
		Shape:            selected.Shape,
		PayloadBuckets:   contract.PayloadBuckets,
		ActorCount:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureFixtures(context.Background(), root, contract, digest, []runCase{selected}); err == nil || !strings.Contains(err.Error(), "does not match its key") {
		t.Fatalf("ensureFixtures mismatch error = %v", err)
	}
}

func TestWorkerResultAxesMustMatchCase(t *testing.T) {
	selected := runCase{Scenario: "cold_status", Shape: "linear", Depth: 100, Tail: -1, ActorCount: 8}
	if err := validateWorkerResult(selected, perfscenario.Result{ActorCount: 8, Fanout: 1}); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerResult(selected, perfscenario.Result{ActorCount: 1, Fanout: 1}); err == nil {
		t.Fatal("mismatched worker actor count was accepted")
	}
	if err := validateWorkerResult(selected, perfscenario.Result{ActorCount: 8, Fanout: 2}); err == nil {
		t.Fatal("mismatched worker fan-out was accepted")
	}
}

func TestResolveCommitRejectsUnsafeRefsBeforeGit(t *testing.T) {
	for _, reference := range []string{"", "--help", "main;touch-pwned", "main name"} {
		if _, err := resolveCommit(context.Background(), t.TempDir(), reference); err == nil {
			t.Fatalf("unsafe ref %q was accepted", reference)
		}
	}
}

func TestOverlayCopiesAFileTreeAndRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "value"), []byte("stable\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := overlay(source, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "nested", "value"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "stable\n" {
		t.Fatalf("copied content = %q", content)
	}
	if err := os.Symlink("nested/value", filepath.Join(source, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := overlay(source, filepath.Join(t.TempDir(), "copy")); err == nil {
		t.Fatal("overlay followed a symlink")
	}
}

// TestEmptySelectionIsRefusedBeforeAnyEvidence covers the lane entry
// boundary. A contract that names no cases for the requested tier must be
// refused by name, and nothing may be written: a zero-sample directory whose
// outcome is pass reads as a successful campaign that never ran.
func TestEmptySelectionIsRefusedBeforeAnyEvidence(t *testing.T) {
	root := filepath.Join("..", "..")
	contractPath := filepath.Join(root, defaultContract)
	cases, err := casesForTier(testContract(t), "envelope")
	if err != nil || len(cases) != 0 {
		t.Fatalf("envelope selection under %s = %d cases, %v; this test needs the empty selection", defaultContract, len(cases), err)
	}
	output := filepath.Join(t.TempDir(), "evidence")
	err = laneCommand(context.Background(), root, false, false, []string{"--tier", "envelope", "--contract", contractPath, "--output", output})
	if err == nil {
		t.Fatal("laneCommand accepted a tier that selects no cases")
	}
	for _, want := range []string{"envelope", defaultContract, "no cases"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("refused campaign created %s (%v)", output, statErr)
	}
}
