package main

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestMergeResumeAppendsAHistoricalWiderReceiptWithoutReplanningOrRemerging(t *testing.T) {
	t.Parallel()
	f, candidate, approval, wider, nested := buildNestedCrossAuthorApproval(t)
	targetPreHead := testGit(t, f.repo, "rev-parse", "HEAD")
	before := f.snapshot(t)
	// Pin the shape approved at ca5b378f, rather than asking today's planner
	// to reconstruct it. Legacy receipts have neither left-live accounting nor
	// changed-path trailers; target and ratified-review checks still apply.
	sealed := mergeplan.Succession{
		Publish: []string{"docs", "feature.txt"},
		Retire:  map[string]string{wider: "docs", nested: "docs"},
	}
	if err := mergeplan.ValidateReach(before.Projection, sealed, approval,
		f.workspace.View().Actors["operator"].Fingerprint); err == nil || !strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("fresh reach must refuse the historical wider retirement: %v", err)
	}
	message, err := mergeReceiptMessage("Merge the approved nested guide.", approval, "", "", candidate,
		mergeTestTarget(f.workspace, targetPreHead), "", false, sealed)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, f.repo, "merge", "--no-ff", "-m", message, candidate)
	mergeHead := testGit(t, f.repo, "rev-parse", "HEAD")
	testGit(t, f.repo, "update-ref", mergeReceiptRef(approval), mergeHead, "")
	receipt, ok, err := readMergeReceipt(f.ctx, f.repo, mergeHead)
	if err != nil || !ok {
		t.Fatalf("read historical receipt: ok=%v err=%v", ok, err)
	}
	if receipt.LeftLivePresent || receipt.ChangedPathsPresent {
		t.Fatalf("historical receipt acquired prospective accounting: %+v", receipt)
	}
	var published []string
	var retired map[string]string
	if err := json.Unmarshal([]byte(receipt.Successors), &published); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(receipt.Retirements), &retired); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, []string{"docs", "feature.txt"}) || !maps.Equal(retired, map[string]string{wider: "docs", nested: "docs"}) {
		t.Fatalf("historical sealed shape changed: publish %v retire %v", published, retired)
	}
	args := []string{
		"--repo", f.repo, "--as", "operator", "--checkout", f.repo,
		"--candidate", candidate, "--approval", approval,
		"--text", "Resume the historical sealed suffix.",
	}
	if err := mergeCommand(f.ctx, args); err != nil {
		t.Fatalf("authorized historical resume refused: %v", err)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("resume moved Git HEAD: got %s want %s", got, mergeHead)
	}
	after := f.snapshot(t)
	wantDepth := before.Depth + 1 + len(published) + len(retired)
	if after.Depth != wantDepth {
		t.Fatalf("resume depth = %d, want %d for the exact sealed suffix", after.Depth, wantDepth)
	}
	receipts := 0
	for _, statement := range after.Projection.Statements {
		if statement.Kind == workroom.KindAssert && statement.Body["merge_approval"] == approval && statement.Body["merge_head"] == mergeHead {
			receipts++
			if statement.Body["merge_successors"] != receipt.Successors || statement.Body["merge_retirements"] != receipt.Retirements {
				t.Fatalf("durable receipt reinterpreted the sealed plan: %+v", statement.Body)
			}
			if _, present := statement.Body["merge_left_live"]; present {
				t.Fatal("durable legacy receipt invented left-live accounting")
			}
			if _, present := statement.Body["merge_changed_paths"]; present {
				t.Fatal("durable legacy receipt invented changed paths")
			}
		}
	}
	if receipts != 1 {
		t.Fatalf("durable receipt count = %d, want one", receipts)
	}
	for predecessor := range retired {
		if !artifactByEvent(t, after.Projection, predecessor).Retired {
			t.Fatalf("sealed predecessor %s stayed live", predecessor)
		}
	}
	for _, path := range published {
		live := 0
		for _, artifact := range after.Projection.Artifacts {
			if artifact.Path == path && artifact.Commit == mergeHead && !artifact.Retired && !artifact.DescribesSupersededWorld {
				live++
			}
		}
		if live != 1 {
			t.Fatalf("current successors at %s = %d, want one", path, live)
		}
	}
	if err := mergeCommand(f.ctx, args); err != nil {
		t.Fatalf("repeated historical resume refused: %v", err)
	}
	repeated := f.snapshot(t)
	if repeated.Head != after.Head || repeated.Depth != after.Depth {
		t.Fatalf("repeat changed durable head/depth: %s/%d to %s/%d", after.Head, after.Depth, repeated.Head, repeated.Depth)
	}
	if got := testGit(t, f.repo, "rev-parse", "HEAD"); got != mergeHead {
		t.Fatalf("repeat moved Git HEAD: got %s want %s", got, mergeHead)
	}
	if got := testGit(t, f.repo, "rev-parse", mergeReceiptRef(approval)); got != mergeHead {
		t.Fatalf("repeat moved sealed receipt ref: got %s want %s", got, mergeHead)
	}
}
