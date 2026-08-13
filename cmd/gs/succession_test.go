package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// Both covering artifacts sit above the changed file, so neither path is the
// fallback and the comparison is the only thing that can pick a winner. An
// earlier fixture put the narrow artifact at exactly the changed path, which
// left the fallback arm answering for it: replacing widerPath with `return
// false` changed no result in the whole package. Both orders run, because a
// comparison that is never reached still passes whichever order happens to be
// right.
func TestMergeWiderPathWinsOverNestedArtifact(t *testing.T) {
	narrow := workroom.Artifact{Event: "narrow", Path: "internal/workroom", Commit: "old"}
	wide := workroom.Artifact{Event: "wide", Path: "internal", Commit: "old"}
	for _, order := range [][]workroom.Artifact{{narrow, wide}, {wide, narrow}} {
		plan := planSuccession(workroom.Projection{Artifacts: order},
			[]mergeChange{{status: "M", new: "internal/workroom/fold.go"}}, nil)
		if !reflect.DeepEqual(plan.publish, []string{"internal"}) {
			t.Fatalf("published paths = %#v for %s first", plan.publish, order[0].Path)
		}
		if plan.retire["wide"] != "internal" || plan.retire["narrow"] != "internal" {
			t.Fatalf("retirements = %#v for %s first", plan.retire, order[0].Path)
		}
	}
}

// The fallback is only for a changed file no live artifact covers at all.
func TestMergePublishesAFirstArtifactAtAnUncoveredPath(t *testing.T) {
	plan := planSuccession(workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "elsewhere", Path: "cmd/gs", Commit: "old"},
	}}, []mergeChange{{status: "A", new: "internal/workroom/fold.go"}}, nil)
	if !reflect.DeepEqual(plan.publish, []string{"internal/workroom/fold.go"}) {
		t.Fatalf("published paths = %#v", plan.publish)
	}
	if len(plan.retire) != 0 {
		t.Fatalf("uncovered path retired %#v", plan.retire)
	}
}

func TestMergeRenameRetiresOldPathAndPublishesNewPath(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "old-file", Path: "old/name.go", Commit: "old"},
	}}
	plan := planSuccession(projection, []mergeChange{{status: "R100", old: "old/name.go", new: "new/name.go"}}, nil)
	if !reflect.DeepEqual(plan.publish, []string{"new/name.go"}) {
		t.Fatalf("published paths = %#v", plan.publish)
	}
	if successor, exists := plan.retire["old-file"]; !exists || successor != "" {
		t.Fatalf("old-path retirement = %q, exists %v", successor, exists)
	}
}

func TestMergeDeleteRetiresOldPathWithoutSuccessor(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "deleted", Path: "gone.go", Commit: "old"},
	}}
	plan := planSuccession(projection, []mergeChange{{status: "D", old: "gone.go"}}, nil)
	if len(plan.publish) != 0 {
		t.Fatalf("deletion published %#v", plan.publish)
	}
	if successor, exists := plan.retire["deleted"]; !exists || successor != "" {
		t.Fatalf("deleted-path retirement = %q, exists %v", successor, exists)
	}
}

// The command applies the fold's own bound before the merge commit exists, so a
// plan the fold will refuse never moves the target. The bound is the approval's
// artifact path, because the fold holds no repository: it cannot open the merge
// head or read a diff, so the reviewer's signed choice of artifact is the only
// fact about the merger that the merger did not write.
func TestMergePreflightRefusesACrossAuthorRetirementOutsideTheApprovedTree(t *testing.T) {
	projection := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: "approval", Actor: "reviewer", Body: map[string]string{"verdict": "approved", "artifact": "approved-artifact"}},
			{Event: "approved-artifact", Actor: "implementer"},
			{Event: "mine", Actor: "implementer"},
			{Event: "inside", Actor: "stranger"},
			{Event: "covering", Actor: "stranger"},
			{Event: "elsewhere", Actor: "stranger"},
		},
		Artifacts: []workroom.Artifact{
			{Event: "approved-artifact", Path: "cmd/gs"},
			{Event: "mine", Path: "docs"},
			{Event: "inside", Path: "cmd/gs/main.go"},
			{Event: "covering", Path: "cmd"},
			{Event: "elsewhere", Path: "docs"},
		},
		Actors: map[string]workroom.ActorState{
			"implementer": {Name: "implementer", Roles: []string{"participant"}},
			"keeper":      {Name: "keeper", Roles: []string{"participant", "ratifier"}},
		},
	}
	unrelated := successionPlan{retire: map[string]string{"elsewhere": "docs"}}
	err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "approval", "implementer")
	if err == nil || !strings.Contains(err.Error(), "outside the approved tree") {
		t.Fatalf("cross-author retirement outside the approved tree error = %v", err)
	}
	// Not even for a ratifier, though the fold would let one retire anything.
	// Standing is live and can be withdrawn between here and the supersessions,
	// which the fold judges after the target has moved; a refusal that rests on
	// a role is a refusal that may arrive too late to be worth anything.
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "approval", "keeper"); err == nil ||
		!strings.Contains(err.Error(), "outside the approved tree") {
		t.Fatalf("ratifier plan outside the approved tree error = %v, want a refusal", err)
	}
	// The merger's own pointer needs no merge authority at any path.
	own := successionPlan{retire: map[string]string{"mine": "docs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, own, "approval", "implementer"); err != nil {
		t.Fatalf("retiring the merger's own artifact was refused: %v", err)
	}
	// Both directions of one lineage: inside the approved tree, and the
	// directory covering it that the wider-path rule retires with it.
	lineage := successionPlan{retire: map[string]string{"inside": "cmd/gs", "covering": "cmd"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, lineage, "approval", "implementer"); err != nil {
		t.Fatalf("retirement on the approved lineage was refused: %v", err)
	}
	// An approval naming no artifact path bounds nothing, so it authorizes
	// nothing rather than everything.
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "missing", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "bounds no retirement") {
		t.Fatalf("approval naming no artifact error = %v", err)
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
