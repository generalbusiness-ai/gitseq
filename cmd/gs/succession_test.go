package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestMergeClassifiesEveryCoveredLiveArtifact(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	write := func(name, content, message string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		testGit(t, repo, "add", name)
		testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", message)
		return testGit(t, repo, "rev-parse", "HEAD")
	}
	base := write("base", "base\n", "base")
	testGit(t, repo, "checkout", "-qb", "candidate")
	candidate := write("candidate", "candidate\n", "candidate")
	testGit(t, repo, "checkout", "-qb", "other", base)
	sibling := write("sibling", "sibling\n", "protected sibling")
	abandoned := write("abandoned", "abandoned\n", "abandoned candidate")

	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "target-artifact", Path: "shared/file", Commit: base},
			{Event: "candidate-artifact", Path: "shared/file", Commit: candidate},
			{Event: "wider-target-artifact", Path: "shared", Commit: base},
			{Event: "wider-candidate-artifact", Path: "shared", Commit: candidate},
			{Event: "sibling-artifact", Path: "shared/file", Commit: sibling},
			{Event: "abandoned-artifact", Path: "shared/file", Commit: abandoned},
			{Event: "wider-sibling-artifact", Path: "shared", Commit: sibling},
			{Event: "wider-abandoned-artifact", Path: "shared", Commit: abandoned},
			{Event: "empty-commit-artifact", Path: "shared/file", Commit: ""},
			{Event: "ineffective-chain-artifact", Path: "shared/file", Commit: abandoned},
			{Event: "outside-diff-artifact", Path: "elsewhere", Commit: "not-a-git-object"},
		},
		Statements: []workroom.Statement{
			{Event: "request", Lifecycle: workroom.LifecycleRequest},
			{Event: "protect", Lifecycle: workroom.LifecyclePromise},
			{Event: "settled-request", Lifecycle: workroom.LifecycleRequest},
			{Event: "settled-report", Lifecycle: workroom.LifecycleReport, Body: map[string]string{"head": abandoned}},
			{Event: "ineffective-request", Lifecycle: workroom.LifecycleRequest},
			{Event: "ineffective-promise", Lifecycle: workroom.LifecyclePromise},
		},
		Commitments: []workroom.Commitment{
			{Request: "request", Promise: "protect", Status: "promised"},
			{Request: "settled-request", Report: "settled-report", Status: "satisfied"},
			{Request: "ineffective-request", Promise: "ineffective-promise", Status: "promised"},
		},
		Provenance: map[string][]string{
			"protect":                    {"request"},
			"sibling-artifact":           {"middle"},
			"wider-sibling-artifact":     {"middle"},
			"middle":                     {"protect"},
			"ineffective-chain-artifact": {"ineffective-middle"},
			"ineffective-middle":         {"ineffective-promise"},
		},
		Decisions: []workroom.Decision{{Event: "ineffective-middle", Verdict: workroom.Ineffective}},
	}
	changes := []mergeChange{{status: "M", new: "shared/file"}}
	classified := successionPredecessors(context.Background(), repo, projection, changes, base, candidate)
	for _, event := range []string{"target-artifact", "candidate-artifact"} {
		if !classified[event].predecessor {
			t.Errorf("%s was not classified as an in-target predecessor", event)
		}
	}
	for _, event := range []string{"wider-target-artifact", "wider-candidate-artifact"} {
		if got := classified[event]; got.predecessor || got.leftLive != (mergeLeftLive{Class: leftLiveCarried}) {
			t.Errorf("%s = %+v, want carried but not predecessor", event, got)
		}
	}
	if got := classified["sibling-artifact"].leftLive; got.Class != leftLiveSibling || got.Commitment != "protect" {
		t.Errorf("protected candidate = %+v, want sibling under protect", got)
	}
	if got := classified["abandoned-artifact"].leftLive; got.Class != leftLiveAbandoned || got.Commitment != "" {
		t.Errorf("unprotected candidate = %+v, want abandoned", got)
	}
	if got := classified["wider-sibling-artifact"].leftLive; got.Class != leftLiveSibling || got.Commitment != "protect" {
		t.Errorf("wider protected candidate = %+v, want sibling under protect", got)
	}
	if got := classified["wider-abandoned-artifact"].leftLive; got.Class != leftLiveAbandoned || got.Commitment != "" {
		t.Errorf("wider unprotected candidate = %+v, want abandoned", got)
	}
	if got := classified["empty-commit-artifact"].leftLive; got.Class != leftLiveAbandoned || got.Commitment != "" {
		t.Errorf("empty-commit candidate = %+v, want abandoned", got)
	}
	if got := classified["ineffective-chain-artifact"].leftLive; got.Class != leftLiveAbandoned || got.Commitment != "" {
		t.Errorf("candidate behind ineffective provenance = %+v, want abandoned", got)
	}
	if _, exists := classified["outside-diff-artifact"]; exists {
		t.Fatal("classifier queried or classified an artifact outside the diff")
	}

	plan := planSuccession(projection, changes, classified)
	if len(plan.retire) != 2 || plan.retire["target-artifact"] != "shared/file" || plan.retire["candidate-artifact"] != "shared/file" {
		t.Fatalf("in-target retirements = %#v", plan.retire)
	}
	if !reflect.DeepEqual(plan.leftLive, map[string]mergeLeftLive{
		"wider-target-artifact":      {Class: leftLiveCarried},
		"wider-candidate-artifact":   {Class: leftLiveCarried},
		"sibling-artifact":           {Class: leftLiveSibling, Commitment: "protect"},
		"abandoned-artifact":         {Class: leftLiveAbandoned},
		"wider-sibling-artifact":     {Class: leftLiveSibling, Commitment: "protect"},
		"wider-abandoned-artifact":   {Class: leftLiveAbandoned},
		"empty-commit-artifact":      {Class: leftLiveAbandoned},
		"ineffective-chain-artifact": {Class: leftLiveAbandoned},
	}) {
		t.Fatalf("left-live accounting = %#v", plan.leftLive)
	}
}

func TestMergeChangedPathsAreTheCanonicalOldAndNewDiffSet(t *testing.T) {
	t.Parallel()
	changes := []mergeChange{
		{status: "M", new: "z.txt"},
		{status: "R100", old: "old/name.go", new: "new/name.go"},
		{status: "D", old: "gone.txt"},
		{status: "C100", old: "old/name.go", new: "copy/name.go"},
		{status: "M", new: "z.txt"},
	}
	want := []string{"copy/name.go", "gone.txt", "new/name.go", "old/name.go", "z.txt"}
	if got := mergeChangedPaths(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("changed paths = %#v, want %#v", got, want)
	}
	if got := mergeChangedPaths(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty changed paths = %#v, want a sealed empty array", got)
	}
}

func TestMergeDoesNotProtectThroughIneffectiveProvenance(t *testing.T) {
	t.Parallel()
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{{Event: "candidate-artifact", Path: "shared", Commit: "candidate-head"}},
		Statements: []workroom.Statement{
			{Event: "request", Lifecycle: workroom.LifecycleRequest},
			{Event: "promise", Lifecycle: workroom.LifecyclePromise},
		},
		Commitments: []workroom.Commitment{{Request: "request", Promise: "promise", Status: "promised"}},
		Provenance: map[string][]string{
			"candidate-artifact": {"ineffective-middle"},
			"ineffective-middle": {"promise"},
		},
		Decisions: []workroom.Decision{
			{Event: "candidate-artifact", Verdict: workroom.Effective},
			{Event: "ineffective-middle", Verdict: workroom.Ineffective},
			{Event: "promise", Verdict: workroom.Effective},
		},
	}
	if got := protectionIndex(projection, projection.Artifacts)["candidate-artifact"]; got != "" {
		t.Fatalf("ineffective provenance protected candidate under %q", got)
	}
}

// A covering directory does not choose the path of a changed file's successor.
// Both orders prove that projection order cannot choose a different result.
func TestMergePublishesTheChangedPathInsteadOfACoveringDirectory(t *testing.T) {
	t.Parallel()
	exact := workroom.Artifact{Event: "exact", Path: "internal/workroom/fold.go", Commit: "old"}
	secondExact := workroom.Artifact{Event: "second-exact", Path: "internal/workroom/fold.go", Commit: "other"}
	narrow := workroom.Artifact{Event: "narrow", Path: "internal/workroom", Commit: "old"}
	wide := workroom.Artifact{Event: "wide", Path: "internal", Commit: "old"}
	for _, order := range [][]workroom.Artifact{{exact, secondExact, narrow, wide}, {wide, narrow, secondExact, exact}} {
		plan := planSuccession(workroom.Projection{Artifacts: order},
			[]mergeChange{{status: "M", new: "internal/workroom/fold.go"}}, nil)
		if !reflect.DeepEqual(plan.publish, []string{"internal/workroom/fold.go"}) {
			t.Fatalf("published paths = %#v for %s first", plan.publish, order[0].Path)
		}
		want := map[string]string{"exact": "internal/workroom/fold.go", "second-exact": "internal/workroom/fold.go"}
		if !reflect.DeepEqual(plan.retire, want) {
			t.Fatalf("retirements = %#v for %s first", plan.retire, order[0].Path)
		}
	}
}

// The fallback is only for a changed file no live artifact covers at all.
func TestMergePublishesAFirstArtifactAtAnUncoveredPath(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// The command reads the same three facts the fold does, so the fixture
	// carries them: the implementer's artifacts at the approved head, standing
	// before the verdict. `late` is the same implementer at the same head but
	// recorded afterwards, which reaches nothing.
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
	if err == nil || !strings.Contains(err.Error(), "outside the reviewed paths") ||
		!strings.Contains(err.Error(), "have the approval cover it") ||
		!strings.Contains(err.Error(), "docs/reference/gs/merge.md#approval-scope-and-receipt") {
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
	// A reviewed path reaches at itself and beneath it. `approved-artifact`
	// stands at `cmd/gs`, so a stranger's pointer inside that tree is within
	// reach, and so is one at the reviewed path exactly.
	beneath := successionPlan{retire: map[string]string{"inside": "cmd/gs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, beneath, "approval", "implementer"); err != nil {
		t.Fatalf("retirement beneath the reviewed path was refused: %v", err)
	}
	exact := successionPlan{retire: map[string]string{"at-reviewed": "cmd/gs"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, exact, "approval", "implementer"); err != nil {
		t.Fatalf("retirement at the reviewed path itself was refused: %v", err)
	}
	// And nowhere above it. Reviewing `cmd/gs` says nothing about `cmd`, which
	// covers trees this head never put in front of the reviewer, so a
	// stranger's pointer there is out of reach. The refusal is the command's
	// own: this guard reads a projection and no repository, so it answers
	// before the merge commit exists and the target is still where it was.
	above := successionPlan{retire: map[string]string{"covering": "cmd"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, above, "approval", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("retirement above the reviewed path error = %v, want a refusal", err)
	}
	// Every path the approval reviewed, not only the one its verdict names.
	// This is the case that stranded a head spanning four maintained trees.
	second := successionPlan{retire: map[string]string{"in-second": "internal/kernel"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, second, "approval", "implementer"); err != nil {
		t.Fatalf("retirement on a second reviewed path was refused: %v", err)
	}
	// The implementer's own artifact, at the approved head, that the reviewer
	// never cited. Seeding one before asking for review must reach nothing.
	uncited := successionPlan{retire: map[string]string{"in-uncited": "ui"}}
	if err := refuseUnreachableCrossAuthorRetirements(projection, uncited, "approval", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "outside the reviewed paths") {
		t.Fatalf("retirement on an uncited path error = %v", err)
	}
	// An approval that reaches nothing authorizes nothing, rather than
	// everything.
	if err := refuseUnreachableCrossAuthorRetirements(projection, unrelated, "missing", "implementer"); err == nil ||
		!strings.Contains(err.Error(), "bounds no retirement") {
		t.Fatalf("approval reaching no path error = %v", err)
	}
}

func TestMergePreflightRefusesInvalidGeneratedArtifactPaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{".", "cmd/gs,internal/app"} {
		err := preflightSuccession(context.Background(), nil, "", successionPlan{publish: []string{path}})
		if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
			t.Errorf("preflight path %q error = %v", path, err)
		}
	}
}
