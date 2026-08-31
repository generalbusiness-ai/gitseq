package mergeplan

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestResultCeilingRefusesWithoutPartialPlan(t *testing.T) {
	result := Result{
		Mode: "fresh", Approval: "approval", ExactHead: "head", Allowed: true,
		ReviewedPaths: []string{string(make([]byte, OutputLimit))},
	}
	bounded := boundResult(result)
	if bounded.Allowed || len(bounded.Reasons) != 1 || bounded.Reasons[0].Code != "plan_output_too_large" {
		t.Fatalf("bounded result = %+v", bounded)
	}
	if len(bounded.ReviewedPaths) != 0 || len(bounded.CoveringArtifacts) != 0 || len(bounded.Retirements) != 0 {
		t.Fatalf("bounded refusal leaked a partial plan: %+v", bounded)
	}
	encoded, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > OutputLimit {
		t.Fatalf("bounded refusal encoded to %d bytes, ceiling %d", len(encoded), OutputLimit)
	}
}

func TestSuccessionActsAreDeterministic(t *testing.T) {
	plan := Succession{
		Publish: []string{"a", "b"}, ChangedPaths: []string{"a", "b"},
		Retire: map[string]string{"second": "b", "first": "a"},
		LeftLive: map[string]LeftLive{
			"z": {Class: "abandoned"},
			"y": {Class: "sibling", Commitment: "promise"},
		},
	}
	first, err := json.Marshal(SuccessionActs("approval", "candidate", "target", "merge", "", plan))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(SuccessionActs("approval", "candidate", "target", "merge", "", plan))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("succession encoding moved between calls\n%s\n%s", first, second)
	}
}

func TestReviewedCandidateAllowsSamePathCrossAuthorMainPredecessor(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-q", "-b", "main", repo)
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	path := filepath.Join(repo, "docs", "reference", "architecture.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "old architecture")
	base := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "checkout", "-qb", "candidate")
	if err := os.WriteFile(path, []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-qam", "current architecture")
	head := runGit(t, repo, "rev-parse", "HEAD")

	projection := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: "candidate", Sequence: 1, Actor: "implementer"},
			{Event: "predecessor", Sequence: 2, Actor: "other-author"},
			{Event: "approval", Sequence: 3, Actor: "reviewer", Kind: workroom.KindReport, Body: map[string]string{"artifact": "candidate", "head": head, "verdict": "approved"}, Ratified: true},
		},
		Decisions: []workroom.Decision{
			{Event: "candidate", Sequence: 1, Verdict: workroom.Effective},
			{Event: "predecessor", Sequence: 2, Verdict: workroom.Effective},
			{Event: "approval", Sequence: 3, Verdict: workroom.Effective},
		},
		Reviews: []workroom.Review{{Report: "approval", Head: head, Artifact: "candidate", Implementer: "implementer", Verdict: "approved", Independence: workroom.IndependenceIndependent, Ratified: true}},
		Artifacts: []workroom.Artifact{
			{Event: "candidate", Path: "docs/reference/architecture.md", Commit: head},
			{Event: "predecessor", Path: "docs/reference/architecture.md", Commit: base},
		},
		Provenance: map[string][]string{"approval": {"candidate"}},
	}
	reviewed, paths := ReviewedScope(projection, "approval")
	if !reviewed["candidate"] || reviewed["predecessor"] || len(paths) != 1 || paths[0] != "docs/reference/architecture.md" {
		t.Fatalf("reviewed scope = events %#v paths %#v", reviewed, paths)
	}
	changes := []Change{{Status: "M", New: "docs/reference/architecture.md"}}
	classified := Classify(context.Background(), repo, projection, changes, base, head, reviewed)
	if got := classified["candidate"].Class; got != ClassReviewedCandidate {
		t.Fatalf("candidate class = %q", got)
	}
	if got := classified["predecessor"].Class; got != ClassInTargetPredecessor {
		t.Fatalf("predecessor class = %q", got)
	}
	plan := PlanSuccession(projection, changes, classified)
	if plan.Retire["candidate"] != "docs/reference/architecture.md" || plan.Retire["predecessor"] != "docs/reference/architecture.md" {
		t.Fatalf("retirement plan = %#v", plan.Retire)
	}
	if err := ValidateReach(projection, plan, "approval", "implementer"); err != nil {
		t.Fatalf("same-path cross-author predecessor was refused: %v", err)
	}
}

func TestAllAndOnlyUnsettledCommitmentStatusesProtectCoveringArtifacts(t *testing.T) {
	statuses := []string{
		"open", "promised", "reported", "awaiting-merge", "stale",
		"superseded", "satisfied", "cancelled", "reneged", "withdrawn",
	}
	protectedStatuses := map[string]bool{
		"open": true, "promised": true, "reported": true,
		"awaiting-merge": true, "stale": true,
	}
	projection := workroom.Projection{Provenance: make(map[string][]string)}
	for _, status := range statuses {
		request := "request-" + status
		artifact := "artifact-" + status
		projection.Statements = append(projection.Statements,
			workroom.Statement{Event: request, Lifecycle: workroom.LifecycleRequest},
		)
		projection.Commitments = append(projection.Commitments,
			workroom.Commitment{Request: request, Status: status},
		)
		projection.Artifacts = append(projection.Artifacts,
			workroom.Artifact{Event: artifact, Path: "shared", Commit: ""},
		)
		projection.Provenance[artifact] = []string{request}
	}

	changes := []Change{{Status: "M", New: "shared/file.go"}}
	classified := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	plan := PlanSuccession(projection, changes, classified)
	for _, status := range statuses {
		request := "request-" + status
		artifact := "artifact-" + status
		got := classified[artifact]
		left := plan.LeftLive[artifact]
		if protectedStatuses[status] {
			if got.Class != ClassProtectedSibling || got.LeftLive.Class != "sibling" || got.LeftLive.Commitment != request {
				t.Errorf("status %q classified as %+v, want protected sibling under %s", status, got, request)
			}
			if left.Class != "sibling" || left.Commitment != request {
				t.Errorf("status %q covering artifact sealed as %+v, want protected sibling under %s", status, left, request)
			}
			continue
		}
		if got.Class != ClassAbandoned || got.LeftLive.Class != "abandoned" || got.LeftLive.Commitment != "" {
			t.Errorf("status %q classified as %+v, want abandoned", status, got)
		}
		if left.Class != "abandoned" || left.Commitment != "" {
			t.Errorf("status %q covering artifact sealed as %+v, want abandoned", status, left)
		}
	}
}

func TestIneffectiveProvenanceCannotProtectCoveredArtifact(t *testing.T) {
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{{Event: "candidate-artifact", Path: "shared"}},
		Statements: []workroom.Statement{
			{Event: "request", Lifecycle: workroom.LifecycleRequest},
			{Event: "promise", Lifecycle: workroom.LifecyclePromise},
		},
		Commitments: []workroom.Commitment{{Request: "request", Promise: "promise", Status: "promised"}},
		Provenance: map[string][]string{
			"candidate-artifact": {"ineffective-middle"},
			"ineffective-middle": {"promise"},
		},
		Decisions: []workroom.Decision{{Event: "ineffective-middle", Verdict: workroom.Ineffective}},
	}
	changes := []Change{{Status: "M", New: "shared/file.go"}}
	classified := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	got := classified["candidate-artifact"]
	if got.Class != ClassAbandoned || got.LeftLive.Class != "abandoned" || got.LeftLive.Commitment != "" {
		t.Fatalf("artifact behind ineffective provenance classified as %+v, want abandoned", got)
	}
	plan := PlanSuccession(projection, changes, classified)
	if left := plan.LeftLive["candidate-artifact"]; left.Class != "abandoned" || left.Commitment != "" {
		t.Fatalf("artifact behind ineffective provenance sealed as %+v, want abandoned", left)
	}
}

func TestUnrelatedPathCannotEnterCoveredClassificationOrReceipt(t *testing.T) {
	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{{Event: "unrelated-artifact", Path: "elsewhere"}},
		Statements: []workroom.Statement{
			{Event: "request", Lifecycle: workroom.LifecycleRequest},
			{Event: "promise", Lifecycle: workroom.LifecyclePromise},
		},
		Commitments: []workroom.Commitment{{Request: "request", Promise: "promise", Status: "promised"}},
		Provenance:  map[string][]string{"unrelated-artifact": {"promise"}},
	}
	changes := []Change{{Status: "M", New: "shared/file.go"}}
	if covered := CoveredArtifacts(projection, changes); len(covered) != 0 {
		t.Fatalf("unrelated artifact entered covered set: %#v", covered)
	}
	classified := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	if got, exists := classified["unrelated-artifact"]; exists {
		t.Fatalf("unrelated artifact entered classification as %+v", got)
	}
	plan := PlanSuccession(projection, changes, classified)
	if left, exists := plan.LeftLive["unrelated-artifact"]; exists {
		t.Fatalf("unrelated artifact entered sealed left-live accounting as %+v", left)
	}
}

func TestChangedPathsAreTheCanonicalOldAndNewDiffSet(t *testing.T) {
	changes := []Change{
		{Status: "M", New: "z.txt"},
		{Status: "R100", Old: "old/name.go", New: "new/name.go"},
		{Status: "D", Old: "gone.txt"},
		{Status: "C100", Old: "old/name.go", New: "copy/name.go"},
		{Status: "M", New: "z.txt"},
	}
	want := []string{"copy/name.go", "gone.txt", "new/name.go", "old/name.go", "z.txt"}
	if got := ChangedPaths(changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("changed paths = %#v, want %#v", got, want)
	}
	if got := ChangedPaths(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty changed paths = %#v, want a sealed empty array", got)
	}
}

func TestPlanSuccessionKeepsExactPathRules(t *testing.T) {
	t.Run("wider path wins", func(t *testing.T) {
		narrow := workroom.Artifact{Event: "narrow", Path: "internal/workroom", Commit: "old"}
		wide := workroom.Artifact{Event: "wide", Path: "internal", Commit: "old"}
		for _, order := range [][]workroom.Artifact{{narrow, wide}, {wide, narrow}} {
			candidates := map[string]Candidate{
				"narrow": {Class: ClassInTargetPredecessor},
				"wide":   {Class: ClassInTargetPredecessor},
			}
			plan := PlanSuccession(workroom.Projection{Artifacts: order},
				[]Change{{Status: "M", New: "internal/workroom/fold.go"}}, candidates)
			if !reflect.DeepEqual(plan.Publish, []string{"internal"}) {
				t.Fatalf("published paths = %#v for %s first", plan.Publish, order[0].Path)
			}
			if plan.Retire["wide"] != "internal" || plan.Retire["narrow"] != "internal" {
				t.Fatalf("retirements = %#v for %s first", plan.Retire, order[0].Path)
			}
		}
	})
	t.Run("first artifact", func(t *testing.T) {
		plan := PlanSuccession(workroom.Projection{Artifacts: []workroom.Artifact{
			{Event: "elsewhere", Path: "cmd/gs", Commit: "old"},
		}}, []Change{{Status: "A", New: "internal/workroom/fold.go"}}, nil)
		if !reflect.DeepEqual(plan.Publish, []string{"internal/workroom/fold.go"}) || len(plan.Retire) != 0 {
			t.Fatalf("uncovered plan = %+v", plan)
		}
	})
	t.Run("rename", func(t *testing.T) {
		projection := workroom.Projection{Artifacts: []workroom.Artifact{{Event: "old-file", Path: "old/name.go", Commit: "old"}}}
		plan := PlanSuccession(projection, []Change{{Status: "R100", Old: "old/name.go", New: "new/name.go"}},
			map[string]Candidate{"old-file": {Class: ClassInTargetPredecessor}})
		if !reflect.DeepEqual(plan.Publish, []string{"new/name.go"}) {
			t.Fatalf("published paths = %#v", plan.Publish)
		}
		if successor, exists := plan.Retire["old-file"]; !exists || successor != "" {
			t.Fatalf("old-path retirement = %q, exists %v", successor, exists)
		}
	})
	t.Run("delete", func(t *testing.T) {
		projection := workroom.Projection{Artifacts: []workroom.Artifact{{Event: "deleted", Path: "gone.go", Commit: "old"}}}
		plan := PlanSuccession(projection, []Change{{Status: "D", Old: "gone.go"}},
			map[string]Candidate{"deleted": {Class: ClassInTargetPredecessor}})
		if len(plan.Publish) != 0 {
			t.Fatalf("deletion published %#v", plan.Publish)
		}
		if successor, exists := plan.Retire["deleted"]; !exists || successor != "" {
			t.Fatalf("deleted-path retirement = %q, exists %v", successor, exists)
		}
	})
}

func runGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	args := []string{"--no-optional-locks", "--no-replace-objects"}
	if repo != "" {
		args = append(args, "-C", repo)
	}
	args = append(args, arguments...)
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(bytesTrimSpace(output))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
