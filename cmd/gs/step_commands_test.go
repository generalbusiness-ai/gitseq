package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/docset"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The step commands are preflights over paths that already existed, so what
// these tests measure is the preflight: an accepted run appends exactly the
// acts the working cycle calls for, in the order it calls for them, and every
// refusal appends nothing at all. Depth before and after is asserted on every
// refusal, because a command that refuses after signing has already done the
// damage the refusal exists to prevent.

// stepLane is one implementation lane in the shape these commands expect: a
// state@3 request that states its target, a branch and worktree of its own,
// and one head that changes a known set of paths.
type stepLane struct {
	request  string
	branch   string
	checkout string
	head     string
	paths    []string
}

func (f workflowFixture) buildStepLane(t *testing.T, name, requester, performer string, paths ...string) stepLane {
	t.Helper()
	// The whole target triple, because this helper signs the record directly
	// rather than through the filing boundary that would fill it in.
	request := f.stateV3(t, requester, workroom.KindRequest, "implement "+name, map[string]string{
		"to":          f.fingerprint(t, performer),
		"conditions":  "publish the exact head for " + name,
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/main",
		"target_head": testGit(t, f.repo, "rev-parse", "refs/heads/main"),
	}, f.ground)
	checkout := filepath.Join(filepath.Dir(f.repo), name)
	testGit(t, f.repo, "worktree", "add", "-qb", name, checkout)
	for _, path := range paths {
		full := filepath.Join(checkout, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(name+" "+path+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, checkout, "add", ".")
	testGit(t, checkout, "commit", "-qm", name)
	return stepLane{request: request, branch: name, checkout: checkout,
		head: testGit(t, checkout, "rev-parse", "HEAD"), paths: paths}
}

// promiseLane claims the lane through the command under test, because every
// later step reads the promise it files.
func (f workflowFixture) promiseLane(t *testing.T, lane stepLane, actor string) string {
	t.Helper()
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, "--branch", lane.branch, lane.request}); err != nil {
		t.Fatalf("promise %s: %v", lane.branch, err)
	}
	return f.latestStatement(t).Event
}

func (f workflowFixture) latestStatement(t *testing.T) workroom.Statement {
	t.Helper()
	statements := f.snapshot(t).Projection.Statements
	if len(statements) == 0 {
		t.Fatal("the workroom holds no statements")
	}
	return statements[len(statements)-1]
}

// refuses runs one command that must refuse, and proves the workroom is
// exactly as long afterwards as it was before.
func (f workflowFixture) refuses(t *testing.T, want string, run func() error) error {
	t.Helper()
	before := f.snapshot(t).Depth
	err := run()
	if err == nil {
		t.Fatalf("command was accepted; want a refusal naming %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal %q does not name %q", err, want)
	}
	if after := f.snapshot(t).Depth; after != before {
		t.Fatalf("depth moved from %d to %d on a refusal; nothing may be appended", before, after)
	}
	return err
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = writer
	runErr := run()
	os.Stdout = stdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	written, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(written), runErr
}

func basesOf(t *testing.T, snapshot app.Snapshot, event string) []string {
	t.Helper()
	return snapshot.Projection.Provenance[event]
}

// Not parallel: it reads process-wide standard output.
func TestPromiseClaimsOneRequestAddressedToTheActor(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "claimable", "reviewer", "operator", "claimable.txt")

	before := f.snapshot(t).Depth
	printed, err := captureStdout(t, func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--branch", lane.branch, lane.request})
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("depth = %d, want %d: one promise", snapshot.Depth, before+1)
	}
	promise := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if strings.TrimSpace(printed) != promise.Event {
		t.Fatalf("printed %q, want the promise event %s", printed, promise.Event)
	}
	if promise.Kind != workroom.KindPromise || promise.Actor != f.fingerprint(t, "operator") {
		t.Fatalf("filed a %s by %s, want a promise by operator", promise.Kind, promise.Actor)
	}
	if bases := basesOf(t, snapshot, promise.Event); len(bases) != 1 || bases[0] != lane.request {
		t.Fatalf("promise rests on %v, want exactly the request %s", bases, lane.request)
	}
	if promise.Body["branch"] != lane.branch {
		t.Fatalf("body.branch = %q, want %q", promise.Body["branch"], lane.branch)
	}
	if !strings.Contains(promise.Text, "implement claimable") {
		t.Fatalf("default text %q does not name the request", promise.Text)
	}
	commitment, ok := commitmentForRequest(snapshot.Projection, lane.request)
	if !ok || commitment.Status != "promised" || commitment.Promise != promise.Event {
		t.Fatalf("commitment %+v, want promised by this promise", commitment)
	}

	// A retry of the same claim is the same act. The deterministic key makes
	// it replay the promise already recorded, which is what a caller who lost
	// the answer needs: the same event id, and nothing appended.
	repeated, err := captureStdout(t, func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--branch", lane.branch, lane.request})
	})
	if err != nil {
		t.Fatalf("repeating the claim: %v", err)
	}
	if strings.TrimSpace(repeated) != promise.Event {
		t.Fatalf("the retry printed %q, want the promise already recorded %s", repeated, promise.Event)
	}
	if after := f.snapshot(t).Depth; after != before+1 {
		t.Fatalf("depth = %d after a repeated claim, want %d", after, before+1)
	}
}

// A promise filed some other way — by hand, by the MCP adapter, by an earlier
// version of this command — is not this run's act, so claiming again is the
// second closure on one commitment that the fold cannot pay off.
func TestPromiseRefusesASecondClaimItDidNotFile(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "held-elsewhere", "reviewer", "operator", "held-elsewhere.txt")
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "claimed by hand",
		RestsOn: []string{lane.request}, IdempotencyKey: "by-hand",
	}); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "you already hold promise", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", lane.request})
	})
}

// A claim filed after a withdrawal has its own key, and repeating that exact
// command must replay it: only the first claim's key being recognised meant an
// actor who reneged, claimed again, and repeated the command was told they
// already held a promise they had just filed.
func TestPromiseReplaysARetryOfAClaimFiledAfterReneging(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "retry-after-renege", "reviewer", "operator", "retry-after-renege.txt")
	first := f.promiseLane(t, lane, "operator")
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: first, Text: "withdrawn", IdempotencyKey: "renege-retry-after",
	}); err != nil {
		t.Fatal(err)
	}
	second := f.promiseLane(t, lane, "operator")
	if second == first {
		t.Fatal("the second claim replayed the withdrawn promise")
	}
	before := f.snapshot(t).Depth
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--branch", lane.branch, lane.request}); err != nil {
		t.Fatalf("repeating the claim filed after reneging: %v", err)
	}
	if after := f.snapshot(t).Depth; after != before {
		t.Fatalf("depth moved from %d to %d; the retry filed a second promise", before, after)
	}
	if latest := f.latestStatement(t).Event; latest != second {
		t.Fatalf("the newest statement is %s, want the promise the retry replayed %s", latest, second)
	}
}

// Reneging is visible forever, and it is not a locked door: the fold admits a
// fresh promise on the request, and the new claim carries the promise it
// follows in its key so it neither replays nor collides with the withdrawn one.
func TestPromiseCanClaimARequestAgainAfterReneging(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "reclaimed", "reviewer", "operator", "reclaimed.txt")
	first := f.promiseLane(t, lane, "operator")
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: first, Text: "cannot keep this", IdempotencyKey: "renege-reclaimed",
	}); err != nil {
		t.Fatal(err)
	}
	before := f.snapshot(t).Depth
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", lane.request}); err != nil {
		t.Fatalf("claiming again after reneging: %v", err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("depth = %d, want %d: one fresh promise", snapshot.Depth, before+1)
	}
	second := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if second.Event == first || second.Kind != workroom.KindPromise {
		t.Fatalf("the second claim filed %s (%s), want a new promise", second.Event, second.Kind)
	}
	// The reneged row stays in history and the fresh claim opens its own,
	// which is what "visible forever" means: both rows are readable.
	claimed := false
	for _, commitment := range snapshot.Projection.Commitments {
		if commitment.Request == lane.request && commitment.Promise == second.Event {
			claimed = true
		}
	}
	if !claimed {
		t.Fatalf("no commitment row on request %s carries the fresh promise %s", lane.request, second.Event)
	}
}

func commitmentForRequest(projection workroom.Projection, request string) (workroom.Commitment, bool) {
	for _, commitment := range projection.Commitments {
		if commitment.Request == request {
			return commitment, true
		}
	}
	return workroom.Commitment{}, false
}

func TestPromiseRefusesWhatTheFoldWouldNotClose(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	theirs := f.buildStepLane(t, "theirs", "operator", "reviewer", "theirs.txt")

	f.refuses(t, "is addressed to reviewer", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", theirs.request})
	})
	f.refuses(t, "not a request", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", f.ground})
	})
	f.refuses(t, "takes exactly one request event", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator"})
	})
}

func TestPromiseRefusesARetiredRequest(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	withdrawn := f.buildStepLane(t, "withdrawn", "reviewer", "operator", "withdrawn.txt")
	if _, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbSupersede, Target: withdrawn.request, Text: "no longer wanted", IdempotencyKey: "retire-withdrawn",
	}); err != nil {
		t.Fatal(err)
	}
	err := f.refuses(t, "is retired", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", withdrawn.request})
	})
	if !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("refusal %q does not name whose request it was", err)
	}
}

func TestReviewRequestRefusesTwoPromisesUnderOneHead(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	// One head, two lanes: each request is promised separately and each
	// publishes its own path at the same commit.
	first := f.buildStepLane(t, "both-one", "reviewer", "operator", "both-one.txt")
	firstPromise := f.promiseLane(t, first, "operator")
	second := f.buildStepLane(t, "both-two", "reviewer", "operator", "both-two.txt")
	secondPromise := f.promiseLane(t, second, "operator")
	testGit(t, first.checkout, "merge", "--no-ff", "-q", "-m", "carry both lanes", second.head)
	head := testGit(t, first.checkout, "rev-parse", "HEAD")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", head, "--promise", firstPromise, "both-one.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", head, "--promise", secondPromise, "both-two.txt"}); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "one review request answers one commitment", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", head, "--to", "reviewer"})
	})
}

func TestPromiseRefusesARequestThatIsNotOpenAndWarnsOnAStaleOne(t *testing.T) {
	f := newWorkflowFixture(t)
	// Reported straight against the request, with no promise in between: the
	// commitment is closed and there is nothing left to claim.
	closed := f.stateV3(t, "reviewer", workroom.KindRequest, "answer directly", map[string]string{
		"to": f.fingerprint(t, "operator"), "conditions": "answer in the report", "no_git_artifact": "true",
	}, f.ground)
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: "answered directly",
		RestsOn: []string{closed}, IdempotencyKey: "direct-report",
	}); err != nil {
		t.Fatal(err)
	}
	err := f.refuses(t, "not open", func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", closed})
	})
	if !strings.Contains(err.Error(), "gs work --next") {
		t.Fatalf("refusal %q does not name where to look next", err)
	}

	// Staleness is a warning, never a refusal: the reasoning moved, the
	// conditions did not, and only the author can replace the request.
	stale := f.buildStepLane(t, "stale-lane", "reviewer", "operator", "stale.txt")
	f.moveTheWorld(t)
	before := f.snapshot(t).Depth
	notice, err := captureStderr(t, func() error {
		return promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", stale.request})
	})
	if err != nil {
		t.Fatalf("promise on a stale request: %v", err)
	}
	if !strings.Contains(notice, "is stale") || !strings.Contains(notice, "refile") {
		t.Fatalf("stderr %q does not warn about staleness and name the repair", notice)
	}
	if after := f.snapshot(t).Depth; after != before+1 {
		t.Fatalf("depth = %d, want %d: the promise is still filed", after, before+1)
	}
}

// Not parallel: it reads process-wide standard output.
func TestArtifactPublishesEveryPathWithTheReportLast(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "published", "reviewer", "operator", "published.txt", "docs/published.md")
	promise := f.promiseLane(t, lane, "operator")

	before := f.snapshot(t).Depth
	printed, err := captureStdout(t, func() error {
		return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", lane.head, "--promise", promise, "--report", "published.txt",
			"--text", "Tests: go test ./...", "--rests-on", f.ground,
			"docs/published.md", "published.txt"})
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d: one artifact per path", snapshot.Depth, before+2)
	}
	events := strings.Fields(printed)
	if len(events) != 2 {
		t.Fatalf("printed %q, want one event per path", printed)
	}
	statements := snapshot.Projection.Statements
	first := statements[len(statements)-2]
	report := statements[len(statements)-1]
	if first.Body["path"] != "docs/published.md" || report.Body["path"] != "published.txt" {
		t.Fatalf("published %q then %q, want the reporting artifact last", first.Body["path"], report.Body["path"])
	}
	if report.Event != events[1] {
		t.Fatalf("the last printed event %s is not the last artifact %s", events[1], report.Event)
	}
	for _, statement := range []workroom.Statement{first, report} {
		if statement.Kind != workroom.KindArtifact || statement.Body["commit"] != lane.head {
			t.Fatalf("artifact %+v does not stand at %s", statement.Body, lane.head)
		}
		bases := basesOf(t, snapshot, statement.Event)
		if len(bases) != 2 || bases[0] != promise || bases[1] != f.ground {
			t.Fatalf("artifact at %s rests on %v, want the promise then the extra basis", statement.Body["path"], bases)
		}
		if !strings.Contains(statement.Text, "at exact "+lane.head) || !strings.Contains(statement.Text, "on "+lane.branch) {
			t.Fatalf("artifact text %q does not name the exact head and branch", statement.Text)
		}
	}
	if !strings.Contains(report.Text, "Tests: go test ./...") {
		t.Fatalf("reporting artifact text %q does not carry --text", report.Text)
	}
	if strings.Contains(first.Text, "Tests: go test ./...") {
		t.Fatal("--text was added to an artifact that is not the report")
	}

	// A rerun is the same publication: the keys are derived from the actor,
	// the head and the path, so nothing is published twice.
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "--report", "published.txt",
		"--text", "Tests: go test ./...", "--rests-on", f.ground,
		"docs/published.md", "published.txt"}); err != nil {
		t.Fatal(err)
	}
	if after := f.snapshot(t).Depth; after != before+2 {
		t.Fatalf("depth = %d after a rerun, want %d", after, before+2)
	}
}

func TestArtifactRefusesAHeadOrPathThatCannotCarryTheReport(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "guarded", "reviewer", "operator", "guarded.txt")
	promise := f.promiseLane(t, lane, "operator")
	foreign := f.buildStepLane(t, "foreign", "operator", "reviewer", "foreign.txt")
	foreignPromise := f.promiseLane(t, foreign, "reviewer")

	tests := map[string]struct {
		want      string
		arguments []string
	}{
		"abbreviated head": {want: "not a full canonical object ID",
			arguments: []string{"--head", lane.head[:12], "--promise", promise, "guarded.txt"}},
		"head this clone does not have": {want: "does not resolve to a commit",
			arguments: []string{"--head", strings.Repeat("a", 40), "--promise", promise, "guarded.txt"}},
		"promise of another actor": {want: "an artifact must rest on your own promise",
			arguments: []string{"--head", lane.head, "--promise", foreignPromise, "guarded.txt"}},
		"promise that is not a promise": {want: "not a promise",
			arguments: []string{"--head", lane.head, "--promise", lane.request, "guarded.txt"}},
		"path the head did not change": {want: "is not changed by",
			arguments: []string{"--head", lane.head, "--promise", promise, "untouched.txt"}},
		"path named twice": {want: "is named twice",
			arguments: []string{"--head", lane.head, "--promise", promise, "guarded.txt", "guarded.txt"}},
		"report outside the set": {want: "is not one of the paths given",
			arguments: []string{"--head", lane.head, "--promise", promise, "--report", "other.txt", "guarded.txt"}},
		"no path at all": {want: "at least one path",
			arguments: []string{"--head", lane.head, "--promise", promise}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			f.refuses(t, test.want, func() error {
				return artifactCommand(f.ctx, append([]string{"--repo", f.repo, "--as", "operator"}, test.arguments...))
			})
		})
	}
}

func TestArtifactWarnsAboutAChangedPathNothingNames(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "partial", "reviewer", "operator", "partial.txt", "partial-two.txt")
	promise := f.promiseLane(t, lane, "operator")
	notice, err := captureStderr(t, func() error {
		return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", lane.head, "--promise", promise, "partial.txt"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "partial-two.txt") || !strings.Contains(notice, "which no artifact here names") {
		t.Fatalf("stderr %q does not warn about the unnamed changed path", notice)
	}
}

func TestReviewRequestRestsOnEveryArtifactAndNamesTheReport(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "reviewable", "reviewer", "operator", "reviewable.txt", "docs/reviewable.md")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "docs/reviewable.md", "reviewable.txt"}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	statements := snapshot.Projection.Statements
	other, reporting := statements[len(statements)-2].Event, statements[len(statements)-1].Event

	before := f.snapshot(t).Depth
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	snapshot = f.snapshot(t)
	if snapshot.Depth != before+1 {
		t.Fatalf("depth = %d, want %d: one review request", snapshot.Depth, before+1)
	}
	request := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if request.Kind != workroom.KindRequest {
		t.Fatalf("filed a %s, want a request", request.Kind)
	}
	if request.Body["artifact"] != reporting {
		t.Fatalf("body.artifact = %s, want the newest artifact on the promise %s", request.Body["artifact"], reporting)
	}
	if request.Body["head"] != lane.head || request.Body["no_git_artifact"] != "true" {
		t.Fatalf("body %v does not state the head and that the review owes no Git artifact", request.Body)
	}
	if request.Body["to"] != f.fingerprint(t, "reviewer") {
		t.Fatalf("body.to = %s, want the reviewer", request.Body["to"])
	}
	for _, phrase := range []string{"Architecture, Security and Simplification", "resting on all 2 artifacts"} {
		if !strings.Contains(request.Body["conditions"], phrase) {
			t.Fatalf("conditions %q do not say %q", request.Body["conditions"], phrase)
		}
	}
	bases := basesOf(t, snapshot, request.Event)
	if len(bases) != 2 || !containsString(bases, other) || !containsString(bases, reporting) {
		t.Fatalf("review request rests on %v, want both artifacts", bases)
	}
	if !strings.Contains(request.Text, "reporting artifact") || !strings.Contains(request.Text, promise) {
		t.Fatalf("default text %q does not list the artifacts and the promise", request.Text)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestReviewRequestRefusesWhatAVerdictCouldNotAnswer(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "asking", "reviewer", "operator", "asking.txt")
	promise := f.promiseLane(t, lane, "operator")

	f.refuses(t, "no live artifact of yours stands at", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", lane.head, "--to", "reviewer"})
	})
	f.refuses(t, "not a full canonical object ID", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", lane.head[:8], "--to", "reviewer"})
	})
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "asking.txt"}); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "--to names you", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", lane.head, "--to", "operator"})
	})
	f.refuses(t, "names no live actor in the durable roster", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", lane.head, "--to", "nobody"})
	})

	// A second head on the same promise is the set gs review refuses at
	// signing, after the reviewer has done the reading.
	if err := os.WriteFile(filepath.Join(lane.checkout, "asking.txt"), []byte("again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, lane.checkout, "commit", "-qam", "asking again")
	second := testGit(t, lane.checkout, "rev-parse", "HEAD")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", second, "--promise", promise, "asking.txt"}); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "also carries live artifacts at another head", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--head", second, "--to", "reviewer"})
	})
}

func TestReviewRequestRefusesASecondRequestUnlessItReplacesTheFirst(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "replacing", "reviewer", "operator", "replacing.txt")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "replacing.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	first := f.latestStatement(t).Event

	f.refuses(t, "refiling cancels review work already in flight", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", lane.head, "--to", "reviewer", "--text", "please look again"})
	})

	before := f.snapshot(t).Depth
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer", "--text", "please look again", "--replace"}); err != nil {
		t.Fatal(err)
	}
	snapshot := f.snapshot(t)
	if snapshot.Depth != before+2 {
		t.Fatalf("depth = %d, want %d: the replacement and its supersession", snapshot.Depth, before+2)
	}
	if statement, ok := statementFor(snapshot.Projection, first); !ok || !statement.Retired {
		t.Fatalf("the first review request %s was not retired", first)
	}
	var retirement workroom.Act
	for _, act := range snapshot.Projection.Acts {
		if act.Target == first && act.Type == "supersede" {
			retirement = act
		}
	}
	if retirement.Event == "" {
		t.Fatal("no supersession of the first review request")
	}
	replacement := ""
	for _, statement := range snapshot.Projection.Statements {
		if statement.Kind == workroom.KindRequest && statement.Body["head"] == lane.head && statement.Event != first {
			replacement = statement.Event
		}
	}
	if bases := basesOf(t, snapshot, retirement.Event); !containsString(bases, replacement) {
		t.Fatalf("the supersession rests on %v, want the replacement request %s", bases, replacement)
	}
}

func statementFor(projection workroom.Projection, event string) (workroom.Statement, bool) {
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement, true
		}
	}
	return workroom.Statement{}, false
}

// Staleness retires nothing. The reviewer's promise is still live on a review
// request whose bases moved, so a second request would still cancel work in
// flight and is still refused without --replace.
func TestReviewRequestRefusesASecondRequestWhenTheFirstWentStale(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "stale-review", "reviewer", "operator", "stale-review.txt")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "stale-review.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	first := f.latestStatement(t).Event
	f.moveTheWorld(t)
	if commitment, ok := commitmentForRequest(f.snapshot(t).Projection, first); !ok || !commitment.Stale {
		t.Fatalf("the review request %+v is not stale; the fixture no longer moves the world under it", commitment)
	}
	f.refuses(t, "refiling cancels review work already in flight", func() error {
		return reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", lane.head, "--to", "reviewer", "--text", "again"})
	})
}

// --rests-on carries the behaviour a page describes, not another commitment:
// an artifact resting on two promises closes neither.
func TestArtifactRefusesASecondPromiseAsAnExtraBasis(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	first := f.buildStepLane(t, "basis-one", "reviewer", "operator", "basis-one.txt")
	firstPromise := f.promiseLane(t, first, "operator")
	second := f.buildStepLane(t, "basis-two", "reviewer", "operator", "basis-two.txt")
	secondPromise := f.promiseLane(t, second, "operator")
	f.refuses(t, "an artifact resting on two promises closes neither", func() error {
		return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", first.head, "--promise", firstPromise, "--rests-on", secondPromise, "basis-one.txt"})
	})
	f.refuses(t, "is named once, by --promise", func() error {
		return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", first.head, "--promise", firstPromise, "--rests-on", firstPromise, "basis-one.txt"})
	})
}

// The reporting artifact is the newest artifact *on the promise*. A newer
// pointer filed at the same head for another reason is not the report, and
// naming it would hand the verdict a lane it does not report.
func TestReviewRequestNamesTheNewestArtifactOnThePromise(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "reporting", "reviewer", "operator", "reporting.txt", "docs/reporting.md")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "reporting.txt"}); err != nil {
		t.Fatal(err)
	}
	reporting := f.latestStatement(t).Event
	// A later live artifact of the same actor at the same head, resting on
	// something else entirely.
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "a pointer filed for another reason",
		Body:    map[string]string{"path": "docs/reporting.md", "commit": lane.head},
		RestsOn: []string{f.ground}, IdempotencyKey: "sideways-artifact",
	}); err != nil {
		t.Fatal(err)
	}
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	request := f.latestStatement(t)
	if request.Body["artifact"] != reporting {
		t.Fatalf("body.artifact = %s, want the newest artifact on the promise %s", request.Body["artifact"], reporting)
	}
}

// The change set cannot be measured without the target, and a note saying so
// while the page says the check happened is the worst of both.
func TestArtifactRefusesATargetThisCloneDoesNotHave(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	request := f.stateV3(t, "reviewer", workroom.KindRequest, "implement elsewhere", map[string]string{
		"to": f.fingerprint(t, "operator"), "conditions": "publish the exact head",
		"target_repo": mergeplan.WorkroomRepo(f.workspace),
		"target_ref":  "refs/heads/absent", "target_head": testGit(t, f.repo, "rev-parse", "refs/heads/main"),
	}, f.ground)
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", request}); err != nil {
		t.Fatal(err)
	}
	promise := f.latestStatement(t).Event
	err := f.refuses(t, "refs/heads/absent", func() error {
		return artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
			"--head", f.candidate, "--promise", promise, "feature.txt"})
	})
	if !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("refusal %q does not name the fix", err)
	}
}

// landedLane carries a lane all the way to a ratified approval, through the
// commands under test, so the landing tests below start where an actor does.
func (f workflowFixture) approvedLane(t *testing.T, name string, ratify bool) (stepLane, string, string) {
	t.Helper()
	lane := f.buildStepLane(t, name, "reviewer", "operator", name+".txt")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, name + ".txt"}); err != nil {
		t.Fatal(err)
	}
	artifact := f.latestStatement(t).Event
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	reviewRequest := f.latestStatement(t).Event
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", reviewRequest}); err != nil {
		t.Fatal(err)
	}
	reviewPromise := f.latestStatement(t).Event
	if err := reviewCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--checkout", lane.checkout,
		"--artifact", artifact, "--promise", reviewPromise, "--verdict", "approved", "--text", "APPROVED at the exact head"}); err != nil {
		t.Fatal(err)
	}
	approval := f.latestStatement(t).Event
	if ratify {
		f.ratify(t, approval)
	}
	return lane, artifact, approval
}

// The whole landing, driven through the command: an unratified approval this
// actor asked for is ratified, the merge runs, the target is pushed, and only
// then are the branch and worktree removed.
//
// Not parallel: it reads process-wide standard output.
func TestLandRatifiesMergesPushesAndCleansUp(t *testing.T) {
	f := newWorkflowFixture(t)
	lane, _, approval := f.approvedLane(t, "landing", false)
	origin := filepath.Join(filepath.Dir(f.repo), "origin.git")
	testGit(t, "", "init", "-q", "--bare", origin)
	testGit(t, f.repo, "remote", "add", "origin", origin)
	testGit(t, f.repo, "push", "-q", "origin", "main")
	testGit(t, f.repo, "push", "-q", "origin", lane.branch)

	printed, err := captureStdout(t, func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", f.repo, "--text", "Land the approved lane so the work is available.", "--cleanup"})
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	head := testGit(t, f.repo, "rev-parse", "refs/heads/main")
	if !strings.Contains(printed, head) {
		t.Fatalf("stdout %q does not print the landed head %s", printed, head)
	}
	if statement, ok := statementFor(f.snapshot(t).Projection, approval); !ok || !statement.Ratified {
		t.Fatal("the approval was not ratified by the review requester")
	}
	if contained := testGit(t, f.repo, "branch", "--contains", lane.head, "--format=%(refname:short)"); !strings.Contains(contained, "main") {
		t.Fatalf("main does not contain the candidate: %q", contained)
	}
	if pushed := testGit(t, origin, "rev-parse", "refs/heads/main"); pushed != head {
		t.Fatalf("origin main is %s, want the landed head %s", pushed, head)
	}
	if _, err := os.Stat(lane.checkout); !os.IsNotExist(err) {
		t.Fatalf("the candidate's worktree is still at %s", lane.checkout)
	}
	if branches := testGit(t, f.repo, "branch", "--format=%(refname:short)"); strings.Contains(branches, lane.branch) {
		t.Fatalf("branch %s still exists locally: %q", lane.branch, branches)
	}
	if refs := testGit(t, origin, "for-each-ref", "--format=%(refname:short)", "refs/heads"); strings.Contains(refs, lane.branch) {
		t.Fatalf("branch %s still exists on origin: %q", lane.branch, refs)
	}
	commitment, _ := commitmentForRequest(f.snapshot(t).Projection, lane.request)
	if commitment.Status != "satisfied" {
		t.Fatalf("the implementation commitment is %s, want satisfied by the sealed receipt", commitment.Status)
	}
}

// The cleanup gate, measured on its own: a candidate that is not in the target
// must leave the worktree and the branch exactly where they are, whatever the
// merge reported.
func TestLandCleanupRefusesACandidateTheTargetDoesNotHave(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "unlanded", "reviewer", "operator", "unlanded.txt")
	err := cleanupCandidate(f.ctx, f.repo, lane.head, "refs/heads/main", "")
	if err == nil || !strings.Contains(err.Error(), "is not in refs/heads/main") {
		t.Fatalf("cleanup error = %v, want a refusal naming the missing containment", err)
	}
	if _, statErr := os.Stat(lane.checkout); statErr != nil {
		t.Fatalf("the worktree was removed anyway: %v", statErr)
	}
	if branches := testGit(t, f.repo, "branch", "--format=%(refname:short)"); !strings.Contains(branches, lane.branch) {
		t.Fatalf("branch %s was deleted anyway: %q", lane.branch, branches)
	}
}

// A remote branch may hold work this repository has never seen. Landing the
// commit it was built on says nothing about the commits on top of it, so the
// remote ref is kept and reported rather than deleted.
func TestLandCleanupKeepsARemoteBranchThatMovedAhead(t *testing.T) {
	f := newWorkflowFixture(t)
	lane, _, approval := f.approvedLane(t, "ahead-remote", true)
	origin := filepath.Join(filepath.Dir(f.repo), "origin.git")
	testGit(t, "", "init", "-q", "--bare", origin)
	testGit(t, f.repo, "remote", "add", "origin", origin)
	testGit(t, f.repo, "push", "-q", "origin", "main")
	// A commit only origin has: built here, pushed, and never merged.
	tree := testGit(t, f.repo, "rev-parse", lane.head+"^{tree}")
	newer := testGit(t, f.repo, "commit-tree", tree, "-p", lane.head, "-m", "remote work nobody merged")
	testGit(t, f.repo, "push", "-q", "origin", newer+":refs/heads/"+lane.branch)

	notice, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error {
			return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
				"--checkout", f.repo, "--text", "Land the approved head and leave the remote alone.", "--cleanup"})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if tip := testGit(t, origin, "rev-parse", "refs/heads/"+lane.branch); tip != newer {
		t.Fatalf("origin's %s is at %s, want the unmerged tip %s kept", lane.branch, tip, newer)
	}
	if !strings.Contains(notice, "carries work this landing did not include") {
		t.Fatalf("stderr %q does not report the remote it left alone", notice)
	}
	// The local branch pointed at the landed head, so that one goes.
	if branches := testGit(t, f.repo, "branch", "--format=%(refname:short)"); strings.Contains(branches, lane.branch) {
		t.Fatalf("the local branch was kept although it held nothing new: %q", branches)
	}
}

// The local deletion is a compare-and-swap on the tip it measured, so a branch
// somebody advanced in between keeps its commits and says so.
func TestLandCleanupRefusesABranchThatMovedAfterMeasurement(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "moved-branch", "reviewer", "operator", "moved-branch.txt")
	if err := os.WriteFile(filepath.Join(lane.checkout, "moved-branch.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, lane.checkout, "commit", "-qam", "work the landing did not include")
	advanced := testGit(t, lane.checkout, "rev-parse", "HEAD")

	err := deleteLandedBranch(f.ctx, f.repo, lane.branch, lane.head)
	if err == nil || !strings.Contains(err.Error(), "not the head that landed") {
		t.Fatalf("deleteLandedBranch error = %v, want a refusal naming the moved tip", err)
	}
	if tip := testGit(t, f.repo, "rev-parse", "refs/heads/"+lane.branch); tip != advanced {
		t.Fatalf("branch %s is at %s, want its own tip %s untouched", lane.branch, tip, advanced)
	}
}

func TestLandRefusesBeforeItTouchesGitOrTheLog(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane, artifact, approval := f.approvedLane(t, "refusing", true)

	f.refuses(t, "is not a review verdict", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", f.ground,
			"--checkout", f.repo, "--text", "no"})
	})
	f.refuses(t, "--checkout is the target checkout", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", lane.checkout, "--text", "no"})
	})
	f.refuses(t, "requires --approval, --checkout and --text", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval, "--checkout", f.repo})
	})

	// A dirty target checkout stops a merge after the approval is reserved,
	// so it is refused here, with the files named.
	if err := os.WriteFile(filepath.Join(f.repo, "untracked.txt"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.refuses(t, "is not clean", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", f.repo, "--text", "no"})
	})
	if !strings.Contains(err.Error(), "untracked.txt") {
		t.Fatalf("refusal %q does not name the file in the way", err)
	}
	if err := os.Remove(filepath.Join(f.repo, "untracked.txt")); err != nil {
		t.Fatal(err)
	}

	// The read-only plan runs before anything is staged or reserved: a
	// retired reporting artifact is refused by the plan, not by the merge.
	if _, err := f.workspace.Act(f.ctx, "operator", app.Act{
		Verb: app.VerbSupersede, Target: artifact, Text: "withdrawn", IdempotencyKey: "retire-refusing-artifact",
	}); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "the merge plan refuses this landing", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", f.repo, "--text", "no"})
	})
}

// The ratification is a durable act, so it must not happen on a run that is
// going to refuse. Both checkout refusals are proved against an approval this
// actor could have ratified.
func TestLandRatifiesNothingOnARunThatRefuses(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane, _, approval := f.approvedLane(t, "unratified-refusal", false)

	f.refuses(t, "--checkout is the target checkout", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", lane.checkout, "--text", "no"})
	})
	if statement, ok := statementFor(f.snapshot(t).Projection, approval); !ok || statement.Ratified {
		t.Fatal("the approval was ratified by a run that refused over the checkout")
	}

	if err := os.WriteFile(filepath.Join(f.repo, "untracked-refusal.txt"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.refuses(t, "is not clean", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", f.repo, "--text", "no"})
	})
	if statement, ok := statementFor(f.snapshot(t).Projection, approval); !ok || statement.Ratified {
		t.Fatal("the approval was ratified by a run that refused over a dirty checkout")
	}
	if err := os.Remove(filepath.Join(f.repo, "untracked-refusal.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestLandRefusesAnUnratifiedApprovalItCannotRatify(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	// The reviewer is not the review requester here, so the ratification is
	// somebody else's act and the command says whose.
	_, _, approval := f.approvedLane(t, "unratified", false)
	err := f.refuses(t, "only its review requester may ratify it", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--approval", approval,
			"--checkout", f.repo, "--text", "no"})
	})
	if !strings.Contains(err.Error(), "operator") {
		t.Fatalf("refusal %q does not name the actor who must ratify", err)
	}
}

func TestLandRefusesAChangesRequestedVerdict(t *testing.T) {
	t.Parallel()
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "rejected", "reviewer", "operator", "rejected.txt")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "rejected.txt"}); err != nil {
		t.Fatal(err)
	}
	artifact := f.latestStatement(t).Event
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	reviewRequest := f.latestStatement(t).Event
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", reviewRequest}); err != nil {
		t.Fatal(err)
	}
	reviewPromise := f.latestStatement(t).Event
	if err := reviewCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", "--checkout", lane.checkout,
		"--artifact", artifact, "--promise", reviewPromise, "--verdict", "changes-requested",
		"--text", "CHANGES-REQUESTED at the exact head"}); err != nil {
		t.Fatal(err)
	}
	verdict := f.latestStatement(t).Event
	f.refuses(t, "is a changes-requested verdict", func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", verdict,
			"--checkout", f.repo, "--text", "no"})
	})
}

// gs land without an origin is an ordinary arrangement: the merge lands, the
// push is reported as not taken, and nothing fails.
func TestLandReportsAMissingOriginWithoutFailing(t *testing.T) {
	f := newWorkflowFixture(t)
	_, _, approval := f.approvedLane(t, "originless", true)
	notice, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error {
			return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
				"--checkout", f.repo, "--text", "Land it in a repository with no origin."})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !strings.Contains(notice, "has no origin remote") {
		t.Fatalf("stderr %q does not report the missing origin", notice)
	}
}

// A frontier that moved between planning and landing leaves nothing behind, so
// the only honest recovery is to plan again. Not parallel: it replaces the
// package's plan builder and reads standard error.
func TestLandPlansAgainWhenTheFrontierMovesUnderTheMerge(t *testing.T) {
	f := newWorkflowFixture(t)
	lane, _, approval := f.approvedLane(t, "moving", true)
	previous := buildMergePlan
	plans := 0
	buildMergePlan = func(ctx context.Context, workspace *app.Workspace, checkout, candidate, approval, merger string, signer mergeplan.Signer) mergeplan.Result {
		result := previous(ctx, workspace, checkout, candidate, approval, merger, signer)
		// The first call is gs land's read-only preview; the second is the
		// merge's own. Moving the frontier after that one is what the merge
		// refuses and this command retries.
		plans++
		if plans == 2 {
			if _, err := workspace.Act(ctx, "reviewer", app.Act{
				Verb: app.VerbState, Kind: workroom.KindAssert, Text: "something else landed meanwhile",
				RestsOn: []string{f.ground}, IdempotencyKey: "frontier-mover",
			}); err != nil {
				t.Error(err)
			}
		}
		return result
	}
	t.Cleanup(func() { buildMergePlan = previous })

	notice, err := captureStderr(t, func() error {
		_, runErr := captureStdout(t, func() error {
			return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
				"--checkout", f.repo, "--text", "Land it although the workroom moved under the plan."})
		})
		return runErr
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !strings.Contains(notice, "planning again against the current frontier") {
		t.Fatalf("stderr %q does not report the retry", notice)
	}
	if contained := testGit(t, f.repo, "branch", "--contains", lane.head, "--format=%(refname:short)"); !strings.Contains(contained, "main") {
		t.Fatalf("the retry did not land the candidate: %q", contained)
	}
}

// A push that fails with an origin present is an error, not a note: the next
// step would delete the only other copy of those commits.
func TestLandFailsWhenTheTargetCannotBePushed(t *testing.T) {
	f := newWorkflowFixture(t)
	lane, _, approval := f.approvedLane(t, "unpushable", true)
	testGit(t, f.repo, "remote", "add", "origin", filepath.Join(filepath.Dir(f.repo), "no-such-origin.git"))

	_, err := captureStdout(t, func() error {
		return landCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--approval", approval,
			"--checkout", f.repo, "--text", "Land it with an origin that is not there.", "--cleanup"})
	})
	if err == nil || !strings.Contains(err.Error(), "pushing refs/heads/main to origin failed") {
		t.Fatalf("land error = %v, want the push failure named", err)
	}
	if !strings.Contains(err.Error(), "rerun gs land") {
		t.Fatalf("refusal %q does not name the repair", err)
	}
	// The merge landed, and nothing was deleted on the way out.
	if contained := testGit(t, f.repo, "branch", "--contains", lane.head, "--format=%(refname:short)"); !strings.Contains(contained, "main") {
		t.Fatalf("the merge did not land: %q", contained)
	}
	if _, statErr := os.Stat(lane.checkout); statErr != nil {
		t.Fatalf("the worktree was removed although the push failed: %v", statErr)
	}
	if branches := testGit(t, f.repo, "branch", "--format=%(refname:short)"); !strings.Contains(branches, lane.branch) {
		t.Fatalf("branch %s was deleted although the push failed: %q", lane.branch, branches)
	}
}

// gs work --next is a formatter over the rows gs work already returns, so what
// this measures is the mapping: one exact command line per owed act, and a
// row that owes nothing saying why.
func TestWorkNextPrintsOneCommandPerOwedAct(t *testing.T) {
	f := newWorkflowFixture(t)
	unclaimed := f.buildStepLane(t, "unclaimed", "reviewer", "operator", "unclaimed.txt")
	claimed := f.buildStepLane(t, "claimed", "reviewer", "operator", "claimed.txt")
	promise := f.promiseLane(t, claimed, "operator")

	next := func(actor string) string {
		t.Helper()
		printed, err := captureStdout(t, func() error {
			return workCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, "--next", "--server", "-"})
		})
		if err != nil {
			t.Fatal(err)
		}
		return printed
	}
	printed := next("operator")
	for _, want := range []string{
		"gs promise --as operator " + unclaimed.request,
		"gs artifact --as operator --head <head> --promise " + promise,
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("gs work --next printed\n%s\nwant a line %q", printed, want)
		}
	}

	// An assert against a promise is not a commitment and never appears as a
	// work row, so --next surfaces it or nobody sees it.
	blockage, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "The claimed lane needs a decision first.",
		RestsOn: []string{promise}, IdempotencyKey: "blockage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if printed := next("operator"); !strings.Contains(printed, "gs inspect "+blockage.Record.ID) ||
		!strings.Contains(printed, "needs a decision first") {
		t.Fatalf("gs work --next printed\n%s\nwant the assert on the promise", printed)
	}

	// Once the head is published and reviewed, the same page says ratify and
	// then land, and says who is waiting when nothing is owed.
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", claimed.head, "--promise", promise, "claimed.txt"}); err != nil {
		t.Fatal(err)
	}
	if printed := next("operator"); !strings.Contains(printed, "gs review-request --as operator --head "+claimed.head) {
		t.Fatalf("gs work --next printed\n%s\nwant the review request line", printed)
	}
	if err := reviewRequestCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", claimed.head, "--to", "reviewer"}); err != nil {
		t.Fatal(err)
	}
	reviewRequest := f.latestStatement(t).Event
	if printed := next("operator"); !strings.Contains(printed, "nothing for you: a review request is live") {
		t.Fatalf("gs work --next printed\n%s\nwant the waiting row to say why", printed)
	}
	if printed := next("reviewer"); !strings.Contains(printed, "gs promise --as reviewer "+reviewRequest) {
		t.Fatalf("gs work --next for the reviewer printed\n%s\nwant the promise line", printed)
	}
	if err := promiseCommand(f.ctx, []string{"--repo", f.repo, "--as", "reviewer", reviewRequest}); err != nil {
		t.Fatal(err)
	}
	printed = next("reviewer")
	if !strings.Contains(printed, "gs review --as reviewer --checkout") {
		t.Fatalf("gs work --next for the reviewer printed\n%s\nwant the review skeleton", printed)
	}
	if !strings.Contains(printed, "--verdict <approved|changes-requested>") {
		t.Fatalf("gs work --next for the reviewer printed\n%s\nwant a verdict placeholder, not a pre-filled one", printed)
	}
}

// A commitment has a performer and a requester, and their acts are not
// interchangeable. Printing the performer's acts on the requester's page
// prints commands the preflight refuses, which is worse than printing nothing.
func TestWorkNextNeverPrintsTheOtherPartysActs(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "sided", "reviewer", "operator", "sided.txt")
	promise := f.promiseLane(t, lane, "operator")
	next := func(actor string) string {
		t.Helper()
		printed, err := captureStdout(t, func() error {
			return workCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, "--next", "--server", "-"})
		})
		if err != nil {
			t.Fatal(err)
		}
		return printed
	}

	// Promised: the performer owes the artifacts; the requester owes nothing.
	requesterPage := next("reviewer")
	for _, forbidden := range []string{"gs artifact", "gs review-request", "gs land"} {
		if strings.Contains(requesterPage, forbidden) {
			t.Fatalf("the requester's page printed %q, an act only the performer can file:\n%s", forbidden, requesterPage)
		}
	}
	if !strings.Contains(requesterPage, "nothing for you: waiting on operator") {
		t.Fatalf("the requester's page does not say who it waits on:\n%s", requesterPage)
	}
	if !strings.Contains(next("operator"), "gs artifact --as operator") {
		t.Fatalf("the performer's page does not print the act they owe:\n%s", next("operator"))
	}

	// Awaiting review: still the performer's move, and still not the
	// requester's, even though the row is theirs to watch.
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "sided.txt"}); err != nil {
		t.Fatal(err)
	}
	if page := next("reviewer"); strings.Contains(page, "gs review-request") || strings.Contains(page, "gs artifact") {
		t.Fatalf("the requester's page printed a performer act for an awaiting-review row:\n%s", page)
	}
	if page := next("operator"); !strings.Contains(page, "gs review-request --as operator --head "+lane.head) {
		t.Fatalf("the performer's page does not ask for the review:\n%s", page)
	}
}

// A basis under a claimed lane can move before anything is published. The fold
// writes "stale" over "promised" then, and the row still owes its artifacts.
func TestWorkNextStillOwesArtifactsOnAStalePromisedRow(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "stale-promised", "reviewer", "operator", "stale-promised.txt")
	promise := f.promiseLane(t, lane, "operator")
	f.moveTheWorld(t)
	printed, err := captureStdout(t, func() error {
		return workCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--next", "--server", "-", "--stale", "include"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed, "gs artifact --as operator --head <head> --promise "+promise) {
		t.Fatalf("gs work --next printed\n%s\nwant the artifacts a stale promised row still owes", printed)
	}
	if strings.Contains(printed, "nothing for you: this row is stale") {
		t.Fatalf("gs work --next called a stale promised row done:\n%s", printed)
	}
}

// Every line gs work --next prints is meant to be pasted, so every flag on it
// has to be a flag that command defines. The comparison is against the
// implementation's own flag surface, read out of the source, not against a list
// kept beside it here.
func TestWorkNextPrintsOnlyFlagsTheCommandsDefine(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "parsed", "reviewer", "operator", "parsed.txt")
	promise := f.promiseLane(t, lane, "operator")
	if err := artifactCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator",
		"--head", lane.head, "--promise", promise, "parsed.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.workspace.Act(f.ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindAssert, Text: "a breakdown to read",
		RestsOn: []string{promise}, IdempotencyKey: "parsed-assert",
	}); err != nil {
		t.Fatal(err)
	}
	root, err := docset.Root()
	if err != nil {
		t.Skipf("no repository root above this package: %v", err)
	}
	surface, err := docset.CLISurface(root)
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]map[string]bool{}
	for _, command := range surface {
		defined[command.Name] = map[string]bool{}
		for _, flag := range command.Flags {
			defined[command.Name]["--"+flag] = true
		}
	}
	for _, actor := range []string{"operator", "reviewer"} {
		printed, err := captureStdout(t, func() error {
			return workCommand(f.ctx, []string{"--repo", f.repo, "--as", actor, "--next", "--server", "-"})
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range commandLines(t, printed) {
			words := shellWords(t, line)
			if len(words) < 2 || words[0] != "gs" {
				t.Fatalf("line %q is not a gs invocation", line)
			}
			flags, ok := defined[words[1]]
			if !ok {
				t.Fatalf("line %q names a subcommand gs does not have", line)
			}
			for _, word := range words[2:] {
				if strings.HasPrefix(word, "--") && !flags[word] {
					t.Fatalf("line %q passes %s, which gs %s does not define", line, word, words[1])
				}
			}
		}
	}
	// The one command a reader is sent to that signs nothing must carry no
	// identity, and the real parser is what says so.
	line := (nextWorld{actor: "operator"}).command("inspect", "%s", datum("some-event"))
	if err := inspectCommand(f.ctx, append([]string{"--repo", f.repo}, shellWords(t, line)[2:]...)); err != nil &&
		strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("generated line %q does not parse: %v", line, err)
	}
}

// An actor name may contain a space or a shell metacharacter, and a copyable
// line that splits one name into two arguments is not the act it claims to be.
func TestWorkNextQuotesDataForTheShell(t *testing.T) {
	t.Parallel()
	for _, actor := range []string{"build bot", "bot;$(rm -rf /)", "o'brien", "#bot"} {
		line := (nextWorld{actor: actor}).command("promise", "%s", datum("git:sha1:abc#git:sha1:def"))
		words := shellWords(t, line)
		if len(words) != 5 || words[3] != actor {
			t.Fatalf("line %q split into %q; want --as to carry %q as one argument", line, words, actor)
		}
		if words[4] != "git:sha1:abc#git:sha1:def" {
			t.Fatalf("line %q lost the event argument: %q", line, words)
		}
	}
	// A hole is left unquoted on purpose: it is not runnable, and it must not
	// look as though it were.
	line := (nextWorld{actor: "bot"}).command("review", "--verdict %s", hole("approved|changes-requested"))
	if !strings.Contains(line, "<approved|changes-requested>") {
		t.Fatalf("line %q no longer shows the hole a reader must fill", line)
	}
}

// commandLines returns the copyable lines of a --next page: everything that is
// not a comment.
func commandLines(t *testing.T, page string) []string {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(page, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Fatalf("the page carries no command lines:\n%s", page)
	}
	return lines
}

var holePattern = regexp.MustCompile(`<[^<>]+>`)

// shellWords splits a generated line the way the shell it is meant for would,
// which is the only reading that says whether a quoted argument survived. It
// understands the one quoting this output produces: POSIX single quotes.
// shellWords splits a generated line the way the shell it is meant for does:
// a real sh runs it with gs replaced by a function that prints each argument
// on its own line, so quoting, comments and word splitting are the shell's,
// not a test's imitation of them.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	// A hole such as <reviewer> is deliberately not runnable; the shell would
	// read it as a redirection, so it is replaced by a plain word first.
	line = holePattern.ReplaceAllString(line, "HOLE")
	script := "gs() { for a in \"$@\"; do printf '%s\\n' \"$a\"; done; }; " + line
	output, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("sh could not run %q: %v\n%s", line, err, output)
	}
	text := strings.TrimSuffix(string(output), "\n")
	if text == "" {
		return []string{"gs"}
	}
	return append([]string{"gs"}, strings.Split(text, "\n")...)
}

func TestWorkNextNotesAStaleRowAndItsRepair(t *testing.T) {
	f := newWorkflowFixture(t)
	lane := f.buildStepLane(t, "stale-next", "reviewer", "operator", "stale-next.txt")
	f.moveTheWorld(t)
	printed, err := captureStdout(t, func() error {
		return workCommand(f.ctx, []string{"--repo", f.repo, "--as", "operator", "--next", "--server", "-", "--stale", "include"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed, "stale: re-anchor on") || !strings.Contains(printed, "ask reviewer to refile") {
		t.Fatalf("gs work --next printed\n%s\nwant the stale note and its repair", printed)
	}
	if !strings.Contains(printed, "gs promise --as operator "+lane.request) {
		t.Fatalf("gs work --next printed\n%s\nwant the act the stale row still owes", printed)
	}
}
