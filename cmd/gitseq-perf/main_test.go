package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
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
