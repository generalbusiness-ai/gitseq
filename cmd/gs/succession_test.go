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
	// The command reads the same three facts the fold does, so the fixture
	// carries them: the implementer's artifacts at the approved head, standing
	// before the verdict. `late` is the same implementer at the same head but
	// recorded afterwards, which reaches nothing.
	projection := workroom.Projection{
		Reviews: []workroom.Review{{Report: "approval", Head: "head1",
			Implementer: "implementer", Verdict: "approved"}},
		Statements: []workroom.Statement{
			{Event: "approved-artifact", Sequence: 1, Actor: "implementer"},
			{Event: "second-reviewed", Sequence: 2, Actor: "implementer"},
			{Event: "approval", Sequence: 3, Actor: "reviewer", Body: map[string]string{"verdict": "approved", "artifact": "approved-artifact"}},
			{Event: "late", Sequence: 4, Actor: "implementer"},
			{Event: "mine", Sequence: 5, Actor: "implementer"},
			{Event: "inside", Sequence: 6, Actor: "stranger"},
			{Event: "covering", Sequence: 7, Actor: "stranger"},
			{Event: "elsewhere", Sequence: 8, Actor: "stranger"},
		},
		Artifacts: []workroom.Artifact{
			{Event: "approved-artifact", Path: "cmd/gs", Commit: "head1"},
			{Event: "second-reviewed", Path: "internal/kernel", Commit: "head1"},
			{Event: "late", Path: "ui", Commit: "head1"},
			{Event: "mine", Path: "docs"},
			{Event: "inside", Path: "cmd/gs/main.go"},
			{Event: "covering", Path: "cmd"},
			{Event: "elsewhere", Path: "docs"},
			{Event: "in-second", Path: "internal/kernel/fold.go"},
			{Event: "in-late", Path: "ui/src"},
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
	// Not even for a ratifier, though the fold would let one retire anything.
	// Standing is live and can be withdrawn between here and the supersessions,
	// which the fold judges after the target has moved; a refusal that rests on
	// a role is a refusal that may arrive too late to be worth anything.
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "approval", "keeper"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
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
	// Every path the approval reviewed, not only the one its verdict names.
	// This is the case that stranded a head spanning four maintained trees.
	second := successionPlan{retire: map[string]string{"in-second": "internal/kernel"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, second, "approval", "implementer"); err != nil {
		t.Fatalf("retirement on a second reviewed path was refused: %v", err)
	}
	// A path the implementer claimed after the verdict was never reviewed.
	late := successionPlan{retire: map[string]string{"in-late": "ui"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, late, "approval", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("retirement on a path claimed after the verdict error = %v", err)
	}
	// An approval that reaches nothing authorizes nothing, rather than
	// everything.
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
