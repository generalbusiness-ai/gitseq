package main

import (
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)

// A request holding an approved head can be retired only by carrying the head
// or declaring it abandoned. The declaration needs a writer: --abandon.
func TestSupersedeAbandonRetiresARequestHoldingAnApprovedHead(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildLandingLane(t, "abandoned", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
	})
	if row := laneCommitment(t, f, lane.request); row.Status != "awaiting-landing" {
		t.Fatalf("before retirement the row is %+v, want awaiting-landing", row)
	}

	err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--text", "dropping it", lane.request})
	if err == nil || !strings.Contains(err.Error(), "declare abandoned") || !strings.Contains(err.Error(), "gs supersede --abandon") {
		t.Fatalf("plain retirement of an approved head = %v, want the carry-or-abandon refusal naming --abandon", err)
	}
	if row := laneCommitment(t, f, lane.request); row.Status != "awaiting-landing" {
		t.Fatalf("a refused retirement changed the row to %+v", row)
	}

	if err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--abandon", "--text", "the approach was wrong", lane.request}); err != nil {
		t.Fatalf("--abandon: %v", err)
	}
	if row := laneCommitment(t, f, lane.request); row.Status != "abandoned" || row.Terminal != "abandoned" {
		t.Fatalf("after --abandon the row is %+v, want abandoned", row)
	}
}

// --abandon on a request with no approved head is refused before signing: the
// fold would drop the declaration, so the author would believe it was kept.
func TestSupersedeAbandonRefusesATargetWithNoApprovedHead(t *testing.T) {
	f := newWorkflowFixture(t)
	target := f.request
	err := supersedeCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--abandon", "--text", "nothing to abandon", target})
	if err == nil || !strings.Contains(err.Error(), "approved head") {
		t.Fatalf("--abandon without an approved head = %v, want a refusal naming the approved head", err)
	}
}
