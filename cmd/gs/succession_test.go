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

func TestMergePreflightRefusesInvalidGeneratedArtifactPaths(t *testing.T) {
	for _, path := range []string{".", "cmd/gs,internal/app"} {
		err := preflightSuccession(context.Background(), nil, "", successionPlan{publish: []string{path}})
		if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
			t.Errorf("preflight path %q error = %v", path, err)
		}
	}
}
