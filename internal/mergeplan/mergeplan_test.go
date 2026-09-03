package mergeplan

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

func TestValidatedSuccessionIsAnInternalLosslessClone(t *testing.T) {
	result := Result{validatedSuccession: &Succession{
		Publish:      []string{"shared/file"},
		Retire:       map[string]string{"old": "shared/file"},
		ChangedPaths: []string{"shared/file"},
		LeftLive:     map[string]LeftLive{"wide": {Class: LeftLiveCarried}},
	}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "validatedSuccession") || strings.Contains(string(encoded), "LeftLive") {
		t.Fatalf("internal validated succession leaked into JSON: %s", encoded)
	}
	first, ok := result.ValidatedSuccession()
	if !ok {
		t.Fatal("validated succession was not available")
	}
	first.Publish[0] = "changed"
	first.Retire["old"] = "changed"
	first.ChangedPaths[0] = "changed"
	first.LeftLive["wide"] = LeftLive{Class: LeftLiveAbandoned}
	second, ok := result.ValidatedSuccession()
	if !ok || second.Publish[0] != "shared/file" || second.Retire["old"] != "shared/file" ||
		second.ChangedPaths[0] != "shared/file" || second.LeftLive["wide"].Class != "carried" {
		t.Fatalf("validated succession was mutable through its clone: %+v", second)
	}
	empty, ok := (Result{validatedSuccession: &Succession{
		Publish: []string{}, Retire: map[string]string{}, ChangedPaths: []string{}, LeftLive: map[string]LeftLive{},
	}}).ValidatedSuccession()
	if !ok || empty.Publish == nil || empty.Retire == nil || empty.ChangedPaths == nil || empty.LeftLive == nil {
		t.Fatalf("validated succession lost non-nil empty receipt fields: %+v", empty)
	}
}

func TestSuccessionActsAreDeterministic(t *testing.T) {
	plan := Succession{
		Publish: []string{"a", "b"}, ChangedPaths: []string{"a", "b"},
		Retire: map[string]string{"second": "b", "first": "a"},
		LeftLive: map[string]LeftLive{
			"z": {Class: LeftLiveAbandoned},
			"y": {Class: LeftLiveSibling, Commitment: "promise"},
		},
	}
	first, err := json.Marshal(SuccessionActs("approval", "", "", "candidate", "target", "merge", "", plan))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(SuccessionActs("approval", "", "", "candidate", "target", "merge", "", plan))
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
	classified, err := Classify(context.Background(), repo, projection, changes, base, head, reviewed)
	if err != nil {
		t.Fatal(err)
	}
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

func TestValidateReachPreservesDirectionalCrossAuthorProofs(t *testing.T) {
	projection := workroom.Projection{
		Reviews: []workroom.Review{{Report: "approval", Head: "head1", Implementer: "implementer", Verdict: "approved"}},
		Provenance: map[string][]string{
			"approval": {"approved-artifact", "second-reviewed"},
		},
		Statements: []workroom.Statement{
			{Event: "approved-artifact", Sequence: 1, Actor: "implementer"},
			{Event: "second-reviewed", Sequence: 2, Actor: "implementer"},
			{Event: "approval", Sequence: 3, Actor: "reviewer"},
			{Event: "uncited", Sequence: 4, Actor: "implementer"},
			{Event: "covering", Sequence: 5, Actor: "stranger"},
			{Event: "elsewhere", Sequence: 6, Actor: "stranger"},
			{Event: "in-second", Sequence: 7, Actor: "stranger"},
			{Event: "in-uncited", Sequence: 8, Actor: "stranger"},
		},
		Artifacts: []workroom.Artifact{
			{Event: "approved-artifact", Path: "cmd/gs", Commit: "head1"},
			{Event: "second-reviewed", Path: "internal/kernel", Commit: "head1"},
			{Event: "uncited", Path: "ui", Commit: "head1"},
			{Event: "covering", Path: "cmd"},
			{Event: "elsewhere", Path: "docs"},
			{Event: "in-second", Path: "internal/kernel/fold.go"},
			{Event: "in-uncited", Path: "ui/src"},
		},
		Actors: map[string]workroom.ActorState{
			"implementer": {Name: "implementer", Roles: []string{"participant"}},
			"keeper":      {Name: "keeper", Roles: []string{"participant", "ratifier"}},
		},
	}
	tests := []struct {
		name     string
		approval string
		actor    string
		retire   map[string]string
		want     string
	}{
		{name: "ratifier remains bounded", approval: "approval", actor: "keeper", retire: map[string]string{"elsewhere": "docs"}, want: "outside the reviewed paths"},
		{name: "uncited candidate path", approval: "approval", actor: "implementer", retire: map[string]string{"in-uncited": "ui"}, want: "outside the reviewed paths"},
		{name: "above reviewed path", approval: "approval", actor: "implementer", retire: map[string]string{"covering": "cmd"}, want: "outside the reviewed paths"},
		{name: "approval bounds no retirement", approval: "missing", actor: "implementer", retire: map[string]string{}, want: "bounds no retirement"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateReach(projection, Succession{Retire: test.retire}, test.approval, test.actor)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateReach error = %v, want %q", err, test.want)
			}
		})
	}
	t.Run("second reviewed path", func(t *testing.T) {
		plan := Succession{Retire: map[string]string{"in-second": "internal/kernel"}}
		if err := ValidateReach(projection, plan, "approval", "implementer"); err != nil {
			t.Fatalf("second reviewed path was refused: %v", err)
		}
	})
	t.Run("merger may retire own pointer outside reviewed paths", func(t *testing.T) {
		plan := Succession{Retire: map[string]string{"uncited": "ui"}}
		if err := ValidateReach(projection, plan, "approval", "implementer"); err != nil {
			t.Fatalf("merger's own pointer was refused: %v", err)
		}
	})
}

func TestClassifyRefusesAnUnreadableCommitInsteadOfCallingItOutsideTarget(t *testing.T) {
	projection := workroom.Projection{Artifacts: []workroom.Artifact{{
		Event: "unreadable", Path: "shared/file", Commit: strings.Repeat("f", 40),
	}}}
	_, err := Classify(context.Background(), t.TempDir(), projection,
		[]Change{{Status: "M", New: "shared/file"}}, strings.Repeat("a", 40), strings.Repeat("b", 40), nil)
	if err == nil || !strings.Contains(err.Error(), "classify artifact unreadable") {
		t.Fatalf("unreadable commit classification error = %v", err)
	}
}

func TestValidateSuccessionRefusesInvalidGeneratedArtifactPaths(t *testing.T) {
	for _, path := range []string{".", "cmd/gs,internal/app"} {
		err := ValidateSuccession(context.Background(), nil, "", Succession{Publish: []string{path}})
		if err == nil || !strings.Contains(err.Error(), "invalid artifact path") {
			t.Errorf("path %q error = %v", path, err)
		}
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
	classified, err := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	if err != nil {
		t.Fatal(err)
	}
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
	classified, err := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	if err != nil {
		t.Fatal(err)
	}
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
	classified, err := Classify(context.Background(), "", projection, changes, "target", "candidate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, exists := classified["unrelated-artifact"]; exists {
		t.Fatalf("unrelated artifact entered classification as %+v", got)
	}
	plan := PlanSuccession(projection, changes, classified)
	if left, exists := plan.LeftLive["unrelated-artifact"]; exists {
		t.Fatalf("unrelated artifact entered sealed left-live accounting as %+v", left)
	}
}

func TestClassifyAndPlanPreserveExactPathCarriedAccounting(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-q", "-b", "main", repo)
	write := func(name, content, message string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", name)
		runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", message)
		return runGit(t, repo, "rev-parse", "HEAD")
	}
	base := write("base", "base\n", "base")
	runGit(t, repo, "checkout", "-qb", "candidate")
	candidate := write("candidate", "candidate\n", "candidate")
	runGit(t, repo, "checkout", "-qb", "other", base)
	sibling := write("sibling", "sibling\n", "protected sibling")
	abandoned := write("abandoned", "abandoned\n", "abandoned candidate")

	projection := workroom.Projection{
		Artifacts: []workroom.Artifact{
			{Event: "target-exact", Path: "shared/file", Commit: base},
			{Event: "candidate-exact", Path: "shared/file", Commit: candidate},
			{Event: "target-wide", Path: "shared", Commit: base},
			{Event: "candidate-wide", Path: "shared", Commit: candidate},
			{Event: "sibling", Path: "shared/file", Commit: sibling},
			{Event: "sibling-wide", Path: "shared", Commit: sibling},
			{Event: "abandoned", Path: "shared/file", Commit: abandoned},
			{Event: "ineffective", Path: "shared/file", Commit: abandoned},
			{Event: "outside", Path: "elsewhere", Commit: "not-a-git-object"},
		},
		Statements: []workroom.Statement{
			{Event: "request", Lifecycle: workroom.LifecycleRequest},
			{Event: "promise", Lifecycle: workroom.LifecyclePromise},
			{Event: "ineffective-promise", Lifecycle: workroom.LifecyclePromise},
		},
		Commitments: []workroom.Commitment{
			{Request: "request", Promise: "promise", Status: "promised"},
			{Request: "request", Promise: "ineffective-promise", Status: "promised"},
		},
		Provenance: map[string][]string{
			"sibling":            {"promise"},
			"sibling-wide":       {"promise"},
			"ineffective":        {"ineffective-middle"},
			"ineffective-middle": {"ineffective-promise"},
		},
		Decisions: []workroom.Decision{{Event: "ineffective-middle", Verdict: workroom.Ineffective}},
	}
	changes := []Change{{Status: "M", New: "shared/file"}}
	classified, err := Classify(context.Background(), repo, projection, changes, base, candidate, map[string]bool{"candidate-exact": true, "candidate-wide": true})
	if err != nil {
		t.Fatal(err)
	}
	if classified["target-exact"].Class != ClassInTargetPredecessor || classified["candidate-exact"].Class != ClassReviewedCandidate {
		t.Fatalf("exact in-target classifications = %#v", classified)
	}
	for _, event := range []string{"target-wide", "candidate-wide"} {
		if got := classified[event]; got.Class != ClassCarried || got.LeftLive.Class != "carried" {
			t.Fatalf("%s classification = %+v, want carried", event, got)
		}
	}
	for _, event := range []string{"sibling", "sibling-wide"} {
		if got := classified[event]; got.Class != ClassProtectedSibling || got.LeftLive.Commitment != "promise" {
			t.Fatalf("%s classification = %+v, want protected sibling", event, got)
		}
	}
	if got := classified["ineffective"]; got.Class != ClassAbandoned || got.LeftLive.Commitment != "" {
		t.Fatalf("ineffective classification = %+v, want abandoned", got)
	}
	if _, exists := classified["outside"]; exists {
		t.Fatal("artifact outside the diff entered classification")
	}
	plan := PlanSuccession(projection, changes, classified)
	if !reflect.DeepEqual(plan.Publish, []string{"shared/file"}) || plan.Retire["target-exact"] != "shared/file" || plan.Retire["candidate-exact"] != "shared/file" {
		t.Fatalf("exact-path succession = %+v", plan)
	}
	wantLeftLive := map[string]LeftLive{
		"target-wide":    {Class: LeftLiveCarried},
		"candidate-wide": {Class: LeftLiveCarried},
		"sibling":        {Class: LeftLiveSibling, Commitment: "promise"},
		"sibling-wide":   {Class: LeftLiveSibling, Commitment: "promise"},
		"abandoned":      {Class: LeftLiveAbandoned},
		"ineffective":    {Class: LeftLiveAbandoned},
	}
	if !reflect.DeepEqual(plan.LeftLive, wantLeftLive) {
		t.Fatalf("left-live accounting = %#v, want %#v", plan.LeftLive, wantLeftLive)
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
	t.Run("wider paths stay carried", func(t *testing.T) {
		narrow := workroom.Artifact{Event: "narrow", Path: "internal/workroom", Commit: "old"}
		wide := workroom.Artifact{Event: "wide", Path: "internal", Commit: "old"}
		for _, order := range [][]workroom.Artifact{{narrow, wide}, {wide, narrow}} {
			candidates := map[string]Candidate{
				"narrow": {Class: ClassCarried, LeftLive: LeftLive{Class: LeftLiveCarried}},
				"wide":   {Class: ClassCarried, LeftLive: LeftLive{Class: LeftLiveCarried}},
			}
			plan := PlanSuccession(workroom.Projection{Artifacts: order},
				[]Change{{Status: "M", New: "internal/workroom/fold.go"}}, candidates)
			if !reflect.DeepEqual(plan.Publish, []string{"internal/workroom/fold.go"}) {
				t.Fatalf("published paths = %#v for %s first", plan.Publish, order[0].Path)
			}
			if len(plan.Retire) != 0 || plan.LeftLive["wide"].Class != "carried" || plan.LeftLive["narrow"].Class != "carried" {
				t.Fatalf("plan = %+v for %s first", plan, order[0].Path)
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
	t.Run("nil candidates retire exact path", func(t *testing.T) {
		exact := workroom.Artifact{Event: "exact", Path: "internal/workroom/fold.go", Commit: "old"}
		narrow := workroom.Artifact{Event: "narrow", Path: "internal/workroom", Commit: "old"}
		wide := workroom.Artifact{Event: "wide", Path: "internal", Commit: "old"}
		for _, order := range [][]workroom.Artifact{{exact, narrow, wide}, {wide, narrow, exact}} {
			plan := PlanSuccession(workroom.Projection{Artifacts: order}, []Change{{Status: "M", New: "internal/workroom/fold.go"}}, nil)
			wantRetire := map[string]string{"exact": "internal/workroom/fold.go"}
			if !reflect.DeepEqual(plan.Publish, []string{"internal/workroom/fold.go"}) || !reflect.DeepEqual(plan.Retire, wantRetire) || plan.LeftLive != nil {
				t.Fatalf("nil-candidate exact/narrow/wide plan = %+v for %s first", plan, order[0].Path)
			}
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
