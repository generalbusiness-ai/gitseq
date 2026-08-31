package mergeplan

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
