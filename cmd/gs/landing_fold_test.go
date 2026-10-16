package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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

// fullLogScans puts a shim ahead of git on PATH and returns a counter of the
// full-log reads that reached it: `rev-list --first-parent --reverse` of a
// whole history, not of a range. Each one is a cold verified audit. Not
// parallel: it changes PATH.
func fullLogScans(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH to shim")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
	shim := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + quote(log) + "\nexec " + quote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		content, _ := os.ReadFile(log)
		scans := 0
		for _, line := range strings.Split(string(content), "\n") {
			if _, rest, found := strings.Cut(line, "rev-list --first-parent --reverse "); found && !strings.Contains(rest, "..") {
				scans++
			}
		}
		if err := os.WriteFile(log, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return scans
	}
}

// keyedChain is a workroom holding the first two acts of a six-act labelled,
// keyed chain, and that whole chain: a retry whose prefix landed, which is the
// shape that makes every preflight ask the log about every key.
func keyedChain(t *testing.T) (context.Context, string, *app.Workspace, ed25519.PrivateKey, []batchAct) {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	testGit(t, "", "init", "-q", "-b", "main", repo)
	testGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "seed")
	workspace, seed, err := app.Init(ctx, repo, "operator", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	var acts []batchAct
	for index := range 6 {
		basis := seed.ID
		if index > 0 {
			basis = fmt.Sprintf("$act%d", index-1)
		}
		acts = append(acts, batchAct{
			Label: fmt.Sprintf("act%d", index), Verb: app.VerbState, Kind: workroom.KindAssert,
			Text: fmt.Sprintf("act %d", index), RestsOn: []string{basis}, IdempotencyKey: fmt.Sprintf("scan-once-%d", index),
		})
	}
	if _, err := runBatch(ctx, workspace, "", "operator", private, acts[:2], false); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, repo, workspace, private, acts
}

// One preflight of a batch answers every label's key from one verified read
// of the log. It used to read the whole log once per labelled act, which on a
// large landing was most of its wall time.
func TestABatchPreflightReadsTheLogOnce(t *testing.T) {
	ctx, _, workspace, private, acts := keyedChain(t)
	scans := fullLogScans(t)
	if position, failure := preflightAdmission(ctx, workspace, "", "operator", private, acts, false); failure != nil {
		t.Fatalf("act %d: %v", position, failure)
	}
	if got := scans(); got != 1 {
		t.Fatalf("a preflight of %d labelled acts read the whole log %d times, want once", len(acts), got)
	}
	report, err := runBatch(ctx, workspace, "", "operator", private, acts, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Replayed != 2 || report.Landed != 4 {
		t.Fatalf("the batch replayed %d and landed %d, want 2 and 4", report.Replayed, report.Landed)
	}
}

// gs batch runs two preflights before it appends: the fold's judgement of the
// whole chain, then admission. Each asks the log about every key, and each
// does it in one verified read, so the whole command reads the log three
// times — the command's own cold load of the workroom, then one read per
// preflight — however many keyed acts the chain carries. A lookup per act in
// either preflight would add five reads for this chain.
func TestBatchCommandReadsTheLogOncePerPreflight(t *testing.T) {
	_, repo, workspace, _, acts := keyedChain(t)
	before, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(acts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "chain.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	scans := fullLogScans(t)
	output, err := quiet(t, func() error {
		return batchCommand(context.Background(), []string{"--repo", repo, "--as", "operator", path})
	})
	if err != nil {
		t.Fatalf("gs batch: %v\n%s", err, output)
	}
	if got := scans(); got != 3 {
		t.Fatalf("gs batch of %d keyed acts read the whole log %d times, want 3: one load and one per preflight", len(acts), got)
	}
	if !strings.Contains(output, `"replayed": 2`) || !strings.Contains(output, `"landed": 4`) {
		t.Fatalf("the batch did not replay its prefix and land the rest: %s", output)
	}
	after, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Depth != before.Depth+4 {
		t.Fatalf("depth %d -> %d, want four new acts", before.Depth, after.Depth)
	}
}

// gs artifact asks whether this head already published each path, and whether
// that pointer has since been retired, before it builds the chain. The answer
// for every path comes from one verified read of the log, as does each of the
// batch's two preflights, so with the command's own cold load the log is read
// four times. A lookup per path in any of those checks would add three reads
// for four paths.
func TestArtifactReadsTheLogOncePerCheck(t *testing.T) {
	f := newWorkflowFixture(t)
	paths := []string{"scan-a.txt", "scan-b.txt", "scan-c.txt", "scan-d.txt"}
	lane := f.buildStepLane(t, "scan-once", "reviewer", "operator", paths...)
	promise := f.promiseLane(t, lane, "operator")
	f.snapshot(t)
	scans := fullLogScans(t)
	if _, err := quiet(t, func() error {
		return artifactCommand(f.ctx, append([]string{"--repo", f.repo, "--as", "operator",
			"--head", lane.head, "--branch", lane.branch, "--promise", promise}, paths...))
	}); err != nil {
		t.Fatalf("gs artifact: %v", err)
	}
	if got := scans(); got != 4 {
		t.Fatalf("gs artifact of %d paths read the whole log %d times, want 4: one load and one per check", len(paths), got)
	}
}

// A merge preflights its succession before it moves the target, because a
// receipt it could not record would leave the merge unfinishable, and once more
// when it records that succession after the move. It used to run that second
// preflight twice: once on its own and once inside the batch.
func TestMergePreflightsItsSuccessionOnceAfterTheTargetMoves(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	preHead := testGit(t, fixture.repo, "rev-parse", "main")
	recorded := countAdmissionPreflights(t, fixture.repo, 0)
	if err := mergeCommand(fixture.ctx, []string{
		"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
		"--candidate", fixture.candidate, "--approval", approval,
		"--text", "Land the approved head with one succession preflight.",
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if heads := *recorded; len(heads) != 2 || heads[0] != preHead || heads[1] == preHead {
		t.Fatalf("merge preflighted its succession at %v (pre-head %s), want once before the move and once after", *recorded, preHead)
	}
}

// countAdmissionPreflights replaces the batch preflight seam with one that
// records the target's head at each call and refuses the call numbered
// refuse, counting from one, at the batch's last act; zero refuses nothing.
// Not parallel: it replaces a package variable.
func countAdmissionPreflights(t *testing.T, repo string, refuse int) *[]string {
	t.Helper()
	if reflect.ValueOf(admissionPreflight).Pointer() != reflect.ValueOf(preflightAdmission).Pointer() {
		t.Fatal("the batch preflight seam is not preflightAdmission")
	}
	previous := admissionPreflight
	heads := &[]string{}
	admissionPreflight = func(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, private ed25519.PrivateKey, acts []batchAct, citedOK bool) (int, *batchError) {
		*heads = append(*heads, testGit(t, repo, "rev-parse", "main"))
		if len(*heads) == refuse {
			return len(acts) - 1, batchFail("admission", "refused by the test")
		}
		return previous(ctx, workspace, serverURL, actorName, private, acts, citedOK)
	}
	t.Cleanup(func() { admissionPreflight = previous })
	return heads
}

// An incorporation writes nothing to Git before its one receipt, so the
// batch's own preflight is the only one it runs.
func TestIncorporationPreflightsItsReceiptOnce(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildLandingLane(t, "incorporated-once", map[string]string{
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "HEAD"),
	})
	testGit(t, f.repo, "merge", "--ff-only", "-q", lane.candidate)
	heads := countAdmissionPreflights(t, f.repo, 0)
	if _, err := quiet(t, func() error { return f.mergeLane(t, lane, "--text", "Record the head main already has.") }); err != nil {
		t.Fatalf("incorporation: %v", err)
	}
	if len(*heads) != 1 {
		t.Fatalf("the incorporation preflighted its receipt %d times, want once", len(*heads))
	}
}

// A succession refused after the target moved names the act it stopped at,
// as the refusal before the move does.
func TestAPostMoveSuccessionRefusalNamesTheAct(t *testing.T) {
	fixture := newWorkflowFixture(t)
	approval := fixture.review(t)
	fixture.ratify(t, approval)
	heads := countAdmissionPreflights(t, fixture.repo, 2)
	_, err := quiet(t, func() error {
		return mergeCommand(fixture.ctx, []string{
			"--repo", fixture.repo, "--as", "operator", "--checkout", fixture.repo,
			"--candidate", fixture.candidate, "--approval", approval,
			"--text", "Land the approved head and refuse its succession.",
		})
	})
	if len(*heads) != 2 {
		t.Fatalf("merge preflighted %d times, want the refusal on the second", len(*heads))
	}
	want := regexp.MustCompile(`record merge succession: act [1-9][0-9]*: batch admission: refused by the test`)
	if err == nil || !want.MatchString(err.Error()) {
		t.Fatalf("post-move refusal = %v, want it to name the refused act: %s", err, want)
	}
}
