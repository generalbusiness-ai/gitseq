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

// The four ways a known-failing list can be wrong. The page-set case is the one
// that matters most and was missing: an event already on the list can spread to
// a page the list does not cover, and every other field stays identical — same
// key, same reason, same line count, one more broken page.
func TestCompareBaselineRefusesNewChangedAndFixedEntries(t *testing.T) {
	failing := map[string]BaselineEntry{
		"known":   {Reason: "is retired", Pages: []string{"docs/a.md"}},
		"altered": {Reason: "sits at the whole-repository path", Pages: []string{"docs/b.md"}},
		"spread":  {Reason: "is retired", Pages: []string{"docs/c.md", "docs/d.md"}},
		"fresh":   {Reason: "names a commit that is not a full canonical object ID", Pages: []string{"docs/e.md"}},
	}
	baseline := map[string]BaselineEntry{
		"known":    {Reason: "is retired", Pages: []string{"docs/a.md"}},
		"altered":  {Reason: "is retired", Pages: []string{"docs/b.md"}},
		"spread":   {Reason: "is retired", Pages: []string{"docs/c.md"}},
		"repaired": {Reason: "sits at the whole-repository path", Pages: []string{"docs/f.md"}},
	}
	newly, changed, fixed := CompareBaseline(failing, baseline)

	if len(newly) != 1 || newly[0].Citation != "fresh" {
		t.Errorf("newly = %+v, want exactly the unrecorded citation", newly)
	}
	if len(fixed) != 1 || fixed[0].Citation != "repaired" {
		t.Errorf("fixed = %+v, want the entry that has stopped failing", fixed)
	}
	reported := map[string]string{}
	for _, finding := range changed {
		reported[finding.Citation] = finding.Reason
	}
	if _, ok := reported["altered"]; !ok {
		t.Error("a citation whose defect changed shape must be reported")
	}
	if reason, ok := reported["spread"]; !ok {
		t.Error("a known citation that gained a dependent page must be reported; otherwise the baseline widens without a line changing")
	} else if !strings.Contains(reason, "docs/d.md") {
		t.Errorf("the report must name the page that appeared: %q", reason)
	}
	if _, quiet := reported["known"]; quiet {
		t.Error("a citation failing exactly as recorded, on exactly the recorded pages, must not be reported")
	}
}

// The parser refuses a repeated event rather than letting one row overwrite the
// other. Two rows for one citation means the file disagrees with itself about
// which pages are covered, and silently keeping either lets the rest through.
func TestParseBaselineRefusesDuplicateAndMalformedRows(t *testing.T) {
	if _, err := ParseBaseline("evt\tis retired\tdocs/a.md\nevt\tis retired\tdocs/b.md\n"); err == nil {
		t.Error("a repeated event ID must be an error, not a last-assignment-wins overwrite")
	}
	if _, err := ParseBaseline("evt is retired docs/a.md\n"); err == nil {
		t.Error("a row without tab-separated fields must be an error rather than a silent misparse")
	}
	entries, err := ParseBaseline("# comment\n\nevt\tis retired\tdocs/b.md,docs/a.md\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["evt"].Pages; len(got) != 2 || got[0] != "docs/a.md" {
		t.Errorf("pages = %v, want them sorted so comparison does not depend on file order", got)
	}
}

// An empty baseline against an empty failing set is the state this gate is
// working towards, and must be silent rather than an error.
func TestCompareBaselineIsSilentWhenNothingFails(t *testing.T) {
	newly, changed, fixed := CompareBaseline(map[string]BaselineEntry{}, map[string]BaselineEntry{})
	if len(newly)+len(changed)+len(fixed) != 0 {
		t.Errorf("a clean set reported %d findings; the destination state must be quiet", len(newly)+len(changed)+len(fixed))
	}
}
