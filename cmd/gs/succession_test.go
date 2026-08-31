package main

import (
	"context"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestMergePreflightRefusesACrossAuthorRetirementOutsideTheApprovedTree(t *testing.T) {
	projection := workroom.Projection{
		Reviews: []workroom.Review{{Report: "approval", Head: "head1",
			Implementer: "implementer", Verdict: "approved"}},
		Provenance: map[string][]string{
			"approval": {"promise", "approved-artifact", "second-reviewed"},
		},
		Statements: []workroom.Statement{
			{Event: "approved-artifact", Sequence: 1, Actor: "implementer"},
			{Event: "second-reviewed", Sequence: 2, Actor: "implementer"},
			{Event: "approval", Sequence: 3, Actor: "reviewer", Body: map[string]string{"verdict": "approved", "artifact": "approved-artifact"}},
			{Event: "uncited", Sequence: 4, Actor: "implementer"},
			{Event: "mine", Sequence: 5, Actor: "implementer"},
			{Event: "inside", Sequence: 6, Actor: "stranger"},
			{Event: "covering", Sequence: 7, Actor: "stranger"},
			{Event: "elsewhere", Sequence: 8, Actor: "stranger"},
			{Event: "at-reviewed", Sequence: 9, Actor: "stranger"},
		},
		Artifacts: []workroom.Artifact{
			{Event: "approved-artifact", Path: "cmd/gs", Commit: "head1"},
			{Event: "second-reviewed", Path: "internal/kernel", Commit: "head1"},
			{Event: "uncited", Path: "ui", Commit: "head1"},
			{Event: "mine", Path: "docs"},
			{Event: "inside", Path: "cmd/gs/main.go"},
			{Event: "covering", Path: "cmd"},
			{Event: "at-reviewed", Path: "cmd/gs"},
			{Event: "elsewhere", Path: "docs"},
			{Event: "in-second", Path: "internal/kernel/fold.go"},
			{Event: "in-uncited", Path: "ui/src"},
		},
		Actors: map[string]workroom.ActorState{
			"implementer": {Name: "implementer", Roles: []string{"participant"}},
			"keeper":      {Name: "keeper", Roles: []string{"participant", "ratifier"}},
		},
	}
	unrelated := successionPlan{retire: map[string]string{"elsewhere": "docs"}}
	err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "approval", "implementer")
	if err == nil || !strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("cross-author retirement outside the approved tree error = %v", err)
	}
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "approval", "keeper"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("ratifier plan outside the approved tree error = %v, want a refusal", err)
	}
	own := successionPlan{retire: map[string]string{"mine": "docs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, own, "approval", "implementer"); err != nil {
		t.Fatalf("retiring the merger's own artifact was refused: %v", err)
	}
	beneath := successionPlan{retire: map[string]string{"inside": "cmd/gs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, beneath, "approval", "implementer"); err != nil {
		t.Fatalf("retirement beneath the reviewed path was refused: %v", err)
	}
	exact := successionPlan{retire: map[string]string{"at-reviewed": "cmd/gs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, exact, "approval", "implementer"); err != nil {
		t.Fatalf("retirement at the reviewed path itself was refused: %v", err)
	}
	above := successionPlan{retire: map[string]string{"covering": "cmd"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, above, "approval", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("retirement above the reviewed path error = %v, want a refusal", err)
	}
	second := successionPlan{retire: map[string]string{"in-second": "internal/kernel"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, second, "approval", "implementer"); err != nil {
		t.Fatalf("retirement on a second reviewed path was refused: %v", err)
	}
	uncited := successionPlan{retire: map[string]string{"in-uncited": "ui"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, uncited, "approval", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("retirement on an uncited path error = %v", err)
	}
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "missing", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "bounds no retirement") {
		t.Fatalf("approval reaching no path error = %v", err)
	}
}

func TestMergePreflightRefusesInvalidGeneratedArtifactPaths(t *testing.T) {
	for _, path := range []string{".", "cmd/gs,internal/app"} {
		err := preflightSuccession(context.Background(), nil, "", successionPlan{publish: []string{path}})
		if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
			t.Errorf("preflight path %q error = %v", path, err)
		}
	}
}
