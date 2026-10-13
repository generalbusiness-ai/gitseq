package main

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
)

// snapshotPaths records how each workspace snapshot was answered. Anything
// other than the cache is a fold of the log.
type snapshotPaths struct {
	sync.Mutex
	paths []observe.Path
}

func (s *snapshotPaths) Record(_ context.Context, measurement observe.Measurement) {
	if measurement.Operation != observe.OperationSnapshot {
		return
	}
	s.Lock()
	defer s.Unlock()
	s.paths = append(s.paths, measurement.Path)
}

func (s *snapshotPaths) seen() []observe.Path {
	s.Lock()
	defer s.Unlock()
	return append([]observe.Path(nil), s.paths...)
}

// A landing plans twice and reads the snapshot around both plans. At an
// unmoved frontier that is one fold: the first plan pays it, and every later
// read is the cache. The read-only plan is the control: it must leave the
// cache as cold as it found it, which is what gs merge-plan promises.
func TestALandingPlanSharesTheWorkspaceFold(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	actor, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	signer := mergeplan.Signer{Name: "operator", Private: private}
	open := func() (*app.Workspace, *snapshotPaths) {
		paths := &snapshotPaths{}
		workspace, err := app.OpenObserved(fixture.ctx, fixture.repo, paths)
		if err != nil {
			t.Fatal(err)
		}
		return workspace, paths
	}

	landing, landingPaths := open()
	preview := mergeplan.BuildForLanding(fixture.ctx, landing, fixture.repo, fixture.candidate, approval, actor.Fingerprint, signer)
	if !preview.Allowed {
		t.Fatalf("landing plan refused: %+v", preview.Reasons)
	}
	if _, err := landing.Snapshot(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	again := mergeplan.BuildForLanding(fixture.ctx, landing, fixture.repo, fixture.candidate, approval, actor.Fingerprint, signer)
	seen := landingPaths.seen()
	if len(seen) != 3 || seen[0] == observe.PathCache || seen[1] != observe.PathCache || seen[2] != observe.PathCache {
		t.Fatalf("landing snapshot paths = %v, want one fold then the cache twice", seen)
	}
	if !reflect.DeepEqual(preview, again) {
		t.Fatalf("the second landing plan differs from the first\nfirst:  %+v\nsecond: %+v", preview, again)
	}

	diagnostic, diagnosticPaths := open()
	readOnly := mergeplan.Build(fixture.ctx, diagnostic, fixture.repo, fixture.candidate, approval, actor.Fingerprint, signer)
	if seen := diagnosticPaths.seen(); len(seen) != 0 {
		t.Fatalf("the read-only plan went through the workspace snapshot: %v", seen)
	}
	if !reflect.DeepEqual(preview, readOnly) {
		t.Fatalf("the landing plan differs from the read-only plan at the same frontier\nlanding:   %+v\nread-only: %+v", preview, readOnly)
	}
	if _, err := diagnostic.Snapshot(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if seen := diagnosticPaths.seen(); len(seen) != 1 || seen[0] == observe.PathCache {
		t.Fatalf("the read-only plan warmed the workspace cache: %v", seen)
	}
}

// countLandingPlans counts the plans built through the landing seam. Not
// parallel: it replaces the package's plan builder.
func countLandingPlans(t *testing.T) *int {
	t.Helper()
	if reflect.ValueOf(buildMergePlan).Pointer() != reflect.ValueOf(mergeplan.BuildForLanding).Pointer() {
		t.Fatal("the landing plan builder is not mergeplan.BuildForLanding, so each plan would fold the whole log again")
	}
	previous := buildMergePlan
	plans := 0
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		plans++
		return previous(ctx, workspace, checkout, candidate, approval, merger, signer)
	}
	t.Cleanup(func() { buildMergePlan = previous })
	return &plans
}

// gs merge builds its one plan through the landing seam, so it shares the
// fold its own validation already paid for.
func TestMergeBuildsItsPlanThroughTheLandingSeam(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	plans := countLandingPlans(t)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land the approved head on one fold.",
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if *plans != 1 {
		t.Fatalf("gs merge built %d plans through the landing seam, want 1", *plans)
	}
}

// gs land builds two plans, its preview and the merge's own, and both go
// through the landing seam.
func TestLandBuildsBothPlansThroughTheLandingSeam(t *testing.T) {
	f := newWorkflowFixture(t)
	_, _, approval := f.approvedLane(t, "one-fold", true)
	plans := countLandingPlans(t)
	if _, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error {
			return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
				"--checkout", f.repo, "--text", "Land the approved head on one fold."})
		})
		return runErr
	}); err != nil {
		t.Fatalf("land: %v", err)
	}
	if *plans != 2 {
		t.Fatalf("gs land built %d plans through the landing seam, want 2", *plans)
	}
}
