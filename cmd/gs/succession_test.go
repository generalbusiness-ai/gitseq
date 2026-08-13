package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestMergeWiderPathWinsOverNestedArtifact(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		// Narrow first is deliberate: replacing widerPath with false must make
		// this test choose the wrong winner rather than pass by list order.
		{Event: "narrow", Path: "internal/workroom/fold.go", Commit: "old"},
		{Event: "wide", Path: "internal/workroom", Commit: "old"},
	}}
	plan := planSuccession(projection, []mergeChange{{status: "M", new: "internal/workroom/fold.go"}}, "merged", nil)
	if !reflect.DeepEqual(plan.publish, []string{"internal/workroom"}) {
		t.Fatalf("published paths = %#v", plan.publish)
	}
	if plan.retire["wide"] != "internal/workroom" || plan.retire["narrow"] != "internal/workroom" {
		t.Fatalf("retirements = %#v", plan.retire)
	}
}

func TestMergeRenameRetiresOldPathAndPublishesNewPath(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "old-file", Path: "old/name.go", Commit: "old"},
	}}
	plan := planSuccession(projection, []mergeChange{{status: "R100", old: "old/name.go", new: "new/name.go"}}, "merged", nil)
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
	plan := planSuccession(projection, []mergeChange{{status: "D", old: "gone.go"}}, "merged", nil)
	if len(plan.publish) != 0 {
		t.Fatalf("deletion published %#v", plan.publish)
	}
	if successor, exists := plan.retire["deleted"]; !exists || successor != "" {
		t.Fatalf("deleted-path retirement = %q, exists %v", successor, exists)
	}
}

func TestMergeRetryRecognizesItsAlreadyPublishedSuccessor(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{
		{Event: "old", Path: "internal", Commit: "old"},
		{Event: "successor", Path: "internal", Commit: "merged"},
	}}
	plan := planSuccession(projection, []mergeChange{{status: "M", new: "internal/x.go"}}, "merged", nil)
	if !reflect.DeepEqual(plan.publish, []string{"internal"}) || plan.retire["old"] != "internal" {
		t.Fatalf("retry plan = %#v", plan)
	}
	if _, retiresSuccessor := plan.retire["successor"]; retiresSuccessor {
		t.Fatalf("retry attempted to retire its own successor: %#v", plan.retire)
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
