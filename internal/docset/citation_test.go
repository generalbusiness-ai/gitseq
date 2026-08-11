package docset

import "testing"

// Every shape the log has actually accumulated, and the ordinary paths that
// must keep working. The table is the point: a path class that stops being
// rejected, or a real path that starts being rejected, fails here rather than
// in whichever page happens to cite one.
func TestUnmaintainablePathNamesEveryShapeSuccessionCannotMaintain(t *testing.T) {
	unmaintainable := []string{
		".",
		"/Users/someone/play/gitseq",
		"/Users/someone/play/gitseq-worktrees/a-branch",
		"AGENTS.md,SKILL.md",
		"internal/workroom,internal/service,SKILL.md",
		"request/docs-behavior-reanchor",
		"task/first-ontology",
		"security/service-boundary-bounds",
	}
	for _, path := range unmaintainable {
		if UnmaintainablePath(path) == "" {
			t.Errorf("UnmaintainablePath(%q) = %q, want a reason", path, "")
		}
	}
	maintainable := []string{
		"internal/workroom",
		"internal/workroom/fold.go",
		"cmd/gs",
		"cmd/gs/main.go",
		"ui",
		"ui/src/lib/store.ts",
		"docs",
		"docs/reference/gs/merge.md",
		"go.mod",
		"Makefile",
		".github/workflows/ci.yml",
		"public_surface_test.go",
	}
	for _, path := range maintainable {
		if why := UnmaintainablePath(path); why != "" {
			t.Errorf("UnmaintainablePath(%q) = %q, want no reason", path, why)
		}
	}
}

// The classification table, exhaustively. Written against primitives so the
// cases the live log does not currently contain are covered too: without this
// the retired and unresolvable branches would be tested only by whatever the
// workroom happens to hold on the day.
func TestClassifyCitationSeparatesFatalFromReported(t *testing.T) {
	for _, test := range []struct {
		name                                string
		found, isArtifact, retired, stale   bool
		path                                string
		wantFatal, wantReport               bool
	}{
		{name: "resolves to nothing", path: "internal/workroom", wantFatal: true},
		{name: "resolves to a request", found: true, path: "internal/workroom", wantFatal: true},
		{name: "retired artifact", found: true, isArtifact: true, retired: true, path: "internal/workroom", wantFatal: true},
		{name: "retired outranks path", found: true, isArtifact: true, retired: true, path: ".", wantFatal: true},
		{name: "whole-repository path", found: true, isArtifact: true, path: ".", wantFatal: true},
		{name: "comma-joined path", found: true, isArtifact: true, path: "a,b", wantFatal: true},
		{name: "absolute path", found: true, isArtifact: true, path: "/Users/x/repo", wantFatal: true},
		{name: "branch-name path", found: true, isArtifact: true, path: "request/x", wantFatal: true},
		{name: "stale but maintainable reports only", found: true, isArtifact: true, stale: true, path: "internal/workroom", wantReport: true},
		{name: "stale at an unmaintainable path still fails", found: true, isArtifact: true, stale: true, path: ".", wantFatal: true},
		{name: "live, maintainable, current", found: true, isArtifact: true, path: "internal/kernel"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyCitation(test.found, test.isArtifact, test.retired, test.stale, test.path)
			if got.Fatal != test.wantFatal || got.Report != test.wantReport {
				t.Fatalf("ClassifyCitation() = %+v, want fatal=%v report=%v", got, test.wantFatal, test.wantReport)
			}
			if (got.Fatal || got.Report) && got.Reason == "" {
				t.Error("a verdict that judges must say why")
			}
		})
	}
}

// Staleness must never be fatal on a maintainable path. This is its own test
// because it is the one classification a future tightening would most plausibly
// get wrong: every artifact at every core package is stale today, so promoting
// stale to fatal would redden the entire set for behaving as designed.
func TestStaleNeverFailsOnAMaintainablePath(t *testing.T) {
	for _, path := range []string{"internal/workroom", "cmd/gs", "ui", "internal/app"} {
		got := ClassifyCitation(true, true, false, true, path)
		if got.Fatal {
			t.Errorf("ClassifyCitation(stale at %q) is fatal; staleness is the set working, not failing", path)
		}
		if !got.Report {
			t.Errorf("ClassifyCitation(stale at %q) says nothing; a flare a reader cannot see is not a flare", path)
		}
	}
}
