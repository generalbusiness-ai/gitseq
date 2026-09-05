package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// This integration proof overlays I4 consumer 2938646b9 on immutable I2
// producer 28ff5c60f. It calls the real merge, reads both actual receipts, and
// exercises the consumer without changing the producer's merge machinery.
func TestI4ConsumesActualHeldAndReleasedReceipts(t *testing.T) {
	for _, released := range []bool{false, true} {
		name := "held"
		if released {
			name = "released"
		}
		t.Run(name, func(t *testing.T) {
			f := newWorkflowFixture(t)
			lane := f.buildLandingLane(t, "i4-"+name, map[string]string{
				"target_repo": mergeplan.WorkroomRepo(f.workspace), "target_ref": "refs/heads/main",
				"target_head": testGit(t, f.repo, "rev-parse", "HEAD"), "landing": "held",
			})
			release := ""
			if released {
				release = f.release(t, lane, "reviewer")
			}
			if err := f.mergeLane(t, lane); err != nil {
				t.Fatal(err)
			}
			snapshot := f.snapshot(t)
			receipt := mergeReceiptStatement(t, f, lane.approval)
			source, ok, err := readMergeReceipt(f.ctx, f.repo, testGit(t, f.repo, "rev-parse", "HEAD"))
			if err != nil || !ok {
				t.Fatalf("source receipt unavailable: %v", err)
			}
			inspection, err := statusview.BuildItemInspection(snapshot, lane.request, false)
			if err != nil {
				t.Fatal(err)
			}
			if inspection.Commitment == nil || inspection.Commitment.Status != "satisfied" || inspection.Commitment.Terminal != "landed" {
				t.Fatalf("not an actual delivered commitment: %+v", inspection.Commitment)
			}
			row := inspection.Landing
			if row == nil || row.LandingReceipt != receipt.Event || row.MergeHead != receipt.Body["merge_head"] || row.MergeHoldWarning == released || row.Release != release || row.ReceiptLegacy {
				t.Fatalf("receipt consumer mismatch: %+v", row)
			}
			if (source.HoldWarning == "true") != row.MergeHoldWarning || source.Authorization != release {
				t.Fatalf("source and projected receipts differ: %+v", source)
			}
			f.workspace.MeasureLandingDetails(f.ctx, inspection.LandingRows())
			if row.Git == nil || row.Git.State != "incorporated" || row.Git.RefIncorporated == nil || !*row.Git.RefIncorporated {
				t.Fatalf("actual target not incorporated: %+v", row.Git)
			}
			page, err := statusview.BuildWorkPage(snapshot, statusview.WorkQuery{Actor: f.fingerprint(t, "operator"), Statuses: []string{"satisfied"}, TargetRef: "refs/heads/main"}, false)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range page.Items {
				if item.Request == lane.request {
					found = true
					if item.LandingReceipt != row.LandingReceipt || item.MergeHoldWarning != row.MergeHoldWarning {
						t.Fatalf("work and inspect differ: %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("delivered row missing from bounded work query")
			}
			summary := statusview.Build(snapshot.Genesis, snapshot.Head, snapshot.Depth, snapshot.Projection)
			found = false
			for _, target := range summary.LandingTargets {
				if target.LandingReceipt == row.LandingReceipt {
					found = true
					if target.MergeHoldWarning != row.MergeHoldWarning {
						t.Fatal("summary warning differs")
					}
				}
			}
			if !found {
				t.Fatal("receipt missing from target observations")
			}
			data, err := json.MarshalIndent(struct {
				Producer, Consumer string
				Receipt            workroom.Statement
				Inspection         statusview.ItemInspection
			}{"28ff5c60f", "2938646b9", receipt, inspection}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("/tmp/i4-receipt-"+name+".json", data, 0o644); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("git", "-C", f.repo, "bundle", "create", "/tmp/i4-receipt-"+name+".bundle", "--all").CombinedOutput(); err != nil {
				t.Fatalf("public proof bundle: %v %s", err, output)
			}
			t.Logf("%s: actual receipt %s, merge %s, hold_warning=%v, release=%s; current target incorporated", name, receipt.Event, row.MergeHead, row.MergeHoldWarning, row.Release)
		})
	}
}
