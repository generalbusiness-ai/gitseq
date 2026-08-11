package docset

import (
	"strings"
	"testing"
)

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
		path, commit                        string
		wantFatal, wantReport               bool
	}{
		{name: "resolves to nothing", path: "internal/workroom", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "resolves to a request", found: true, path: "internal/workroom", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "retired artifact", found: true, isArtifact: true, retired: true, path: "internal/workroom", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "retired outranks path", found: true, isArtifact: true, retired: true, path: ".", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "whole-repository path", found: true, isArtifact: true, path: ".", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "comma-joined path", found: true, isArtifact: true, path: "a,b", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "absolute path", found: true, isArtifact: true, path: "/Users/x/repo", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "branch-name path", found: true, isArtifact: true, path: "request/x", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "stale but maintainable reports only", found: true, isArtifact: true, stale: true, path: "internal/workroom", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantReport: true},
		{name: "stale at an unmaintainable path still fails", found: true, isArtifact: true, stale: true, path: ".", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "live, maintainable, current", found: true, isArtifact: true, path: "internal/kernel", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a4"},
		// The fifth class. An abbreviated commit is not untidiness: gs merge
		// refuses anything but a full canonical object ID, and a review resolves
		// to its artifact by matching this field as an exact string, so such an
		// artifact can take part in neither. This is the shape that actually
		// occurred — an eight-character pointer published by mistake.
		{name: "abbreviated commit", found: true, isArtifact: true, path: "internal/nexus", commit: "c0c70300", wantFatal: true},
		{name: "empty commit", found: true, isArtifact: true, path: "internal/nexus", commit: "", wantFatal: true},
		{name: "non-hex commit", found: true, isArtifact: true, path: "internal/nexus", commit: "zzzz257884bb5d39b445d0411c89338f1dc892a4", wantFatal: true},
		{name: "sha256 commit is canonical", found: true, isArtifact: true, path: "internal/nexus", commit: "0f9b257884bb5d39b445d0411c89338f1dc892a40f9b257884bb5d39b445d041"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyCitation(test.found, test.isArtifact, test.retired, test.stale, test.path, test.commit)
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
		got := ClassifyCitation(true, true, false, true, path, "0f9b257884bb5d39b445d0411c89338f1dc892a4")
		if got.Fatal {
			t.Errorf("ClassifyCitation(stale at %q) is fatal; staleness is the set working, not failing", path)
		}
		if !got.Report {
			t.Errorf("ClassifyCitation(stale at %q) says nothing; a flare a reader cannot see is not a flare", path)
		}
	}
}

// The three ways a known-failing list can be wrong. Each is an error rather
// than a warning, and the third is the one that matters most: without it the
// list keeps entries after their repair and quietly becomes an exceptions file,
// which is the failure mode a baseline is supposed to avoid rather than cause.
func TestCompareBaselineRefusesNewChangedAndFixedEntries(t *testing.T) {
	failing := map[string]string{
		"known":   "is retired",
		"altered": "sits at the whole-repository path",
		"fresh":   "names a commit that is not a full canonical object ID",
	}
	baseline := map[string]string{
		"known":   "is retired",
		"altered": "is retired",
		"repaired": "sits at the whole-repository path",
	}
	newly, changed, fixed := CompareBaseline(failing, baseline)

	if len(newly) != 1 || newly[0].Citation != "fresh" {
		t.Errorf("newly = %+v, want exactly the unrecorded citation", newly)
	}
	if len(changed) != 1 || changed[0].Citation != "altered" {
		t.Errorf("changed = %+v, want the citation whose defect changed shape", changed)
	}
	if len(fixed) != 1 || fixed[0].Citation != "repaired" {
		t.Errorf("fixed = %+v, want the entry that has stopped failing", fixed)
	}
	// A citation failing for exactly the reason recorded is the only quiet case.
	for _, finding := range append(append(newly, changed...), fixed...) {
		if finding.Citation == "known" {
			t.Error("a citation failing exactly as recorded must not be reported")
		}
	}
	if finding := changed[0]; !strings.Contains(finding.Reason, "recorded as") || !strings.Contains(finding.Reason, "now") {
		t.Errorf("a changed entry must say both what was recorded and what is true now: %q", finding.Reason)
	}
}

// An empty baseline against an empty failing set is the state this gate is
// working towards, and must be silent rather than an error.
func TestCompareBaselineIsSilentWhenNothingFails(t *testing.T) {
	newly, changed, fixed := CompareBaseline(map[string]string{}, map[string]string{})
	if len(newly)+len(changed)+len(fixed) != 0 {
		t.Errorf("a clean set reported %d findings; the destination state must be quiet", len(newly)+len(changed)+len(fixed))
	}
}
