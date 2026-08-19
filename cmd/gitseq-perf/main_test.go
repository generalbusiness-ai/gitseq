package main

import (
	"context"
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
		if selected.Fanout > 1 {
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

func TestEnsureFixturesRejectsCachedActorCountMismatch(t *testing.T) {
	contract := testContract(t)
	digest, err := perflane.CorrectnessDigest(contract)
	if err != nil {
		t.Fatal(err)
	}
	selected := runCase{Scenario: "cold_status", Shape: "linear", Depth: 100, Tail: -1, ActorCount: 8, Fanout: 1}
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

func TestWorkerResultActorCountMustMatchCase(t *testing.T) {
	selected := runCase{Scenario: "cold_status", Shape: "linear", Depth: 100, Tail: -1, ActorCount: 8, Fanout: 1}
	if err := validateActorCount(selected, perfscenario.Result{ActorCount: 8}); err != nil {
		t.Fatal(err)
	}
	if err := validateActorCount(selected, perfscenario.Result{ActorCount: 1}); err == nil {
		t.Fatal("mismatched worker actor count was accepted")
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
