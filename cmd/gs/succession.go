package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type mergeChange struct {
	status string
	old    string
	new    string
}

func sharedChanges(changes []mergeChange) []mergeplan.Change {
	shared := make([]mergeplan.Change, len(changes))
	for index, change := range changes {
		shared[index] = mergeplan.Change{Status: change.status, Old: change.old, New: change.new}
	}
	return shared
}

func localChanges(changes []mergeplan.Change) []mergeChange {
	local := make([]mergeChange, len(changes))
	for index, change := range changes {
		local[index] = mergeChange{status: change.Status, old: change.Old, new: change.New}
	}
	return local
}

// mergeChanges reads the merge commit rather than the candidate branch. That
// distinction matters when Git's merge result includes a conflict resolution:
// succession follows what actually landed on the first parent.
func mergeChanges(ctx context.Context, checkout, head string) ([]mergeChange, error) {
	return mergeChangesBetween(ctx, checkout, head+"^1", head)
}

func mergeChangesBetween(ctx context.Context, checkout, before, after string) ([]mergeChange, error) {
	raw, err := git(ctx, checkout, "diff", "--name-status", "-z", "--find-renames", before, after)
	if err != nil {
		return nil, err
	}
	return parseMergeChanges(raw)
}

func stagedMergeChanges(ctx context.Context, checkout string) ([]mergeChange, error) {
	raw, err := git(ctx, checkout, "diff", "--cached", "--name-status", "-z", "--find-renames")
	if err != nil {
		return nil, err
	}
	return parseMergeChanges(raw)
}

var readStagedMergeChanges = stagedMergeChanges

func validateMergeChangePaths(changes []mergeChange) error {
	for _, change := range changes {
		for _, path := range []string{change.old, change.new} {
			if path != "" && !utf8.ValidString(path) {
				return fmt.Errorf("merge diff path is not valid UTF-8: %q", path)
			}
		}
	}
	return nil
}

func parseMergeChanges(raw string) ([]mergeChange, error) {
	shared, err := mergeplan.ParseChanges(raw)
	if err != nil {
		return nil, errMalformedDiff
	}
	return localChanges(shared), nil
}

var errMalformedDiff = errors.New("malformed NUL-delimited merge diff")

type successionPlan struct {
	publish      []string
	retire       map[string]string // predecessor event -> successor path, empty when gone
	changedPaths []string
	// leftLive accounts for covered live artifacts that are not in the target
	// world and therefore are not within this merge's retirement authority.
	// A nil map marks a historical receipt which predates this accounting.
	leftLive map[string]mergeLeftLive
}

type mergeLeftLive struct {
	Class      string `json:"class"`
	Commitment string `json:"commitment,omitempty"`
}

type successionCandidate struct {
	predecessor bool
	leftLive    mergeLeftLive
}

const (
	leftLiveSibling   = "sibling"
	leftLiveAbandoned = "abandoned"
)

// planSuccession is deterministic over the merge diff and the fold snapshot.
// Live includes stale artifacts: stale says a basis moved, not that the pointer
// was withdrawn. Historical unmaintainable paths are ignored; state@1 and
// later prevent any more from entering the effective set.
func planSuccession(projection workroom.Projection, changes []mergeChange, candidates map[string]successionCandidate) successionPlan {
	sharedCandidates := make(map[string]mergeplan.Candidate, len(candidates))
	for event, candidate := range candidates {
		if candidate.predecessor {
			sharedCandidates[event] = mergeplan.Candidate{Class: mergeplan.ClassInTargetPredecessor}
		} else {
			class := mergeplan.ClassAbandoned
			if candidate.leftLive.Class == leftLiveSibling {
				class = mergeplan.ClassProtectedSibling
			}
			sharedCandidates[event] = mergeplan.Candidate{Class: class, LeftLive: mergeplan.LeftLive{Class: candidate.leftLive.Class, Commitment: candidate.leftLive.Commitment}}
		}
	}
	shared := mergeplan.PlanSuccession(projection, sharedChanges(changes), sharedCandidates)
	leftLive := make(map[string]mergeLeftLive, len(shared.LeftLive))
	for event, left := range shared.LeftLive {
		leftLive[event] = mergeLeftLive{Class: left.Class, Commitment: left.Commitment}
	}
	if candidates == nil {
		leftLive = nil
	}
	return successionPlan{publish: shared.Publish, retire: shared.Retire, changedPaths: shared.ChangedPaths, leftLive: leftLive}
}

// mergeChangedPaths seals the complete path set from the diff: both sides of
// a rename or copy, the old side of a deletion, and the new side of every
// addition or modification. A set makes repeated paths harmless; sorting makes
// the JSON stable across Git's output order and retries.
func mergeChangedPaths(changes []mergeChange) []string {
	return mergeplan.ChangedPaths(sharedChanges(changes))
}

// successionPredecessors classifies every live artifact rather than silently
// filtering non-ancestors out of the plan. A shipped artifact is a predecessor
// when its commit is in the target's world; the exact reviewed candidate is a
// predecessor because that world is what is landing. Every other candidate is
// left live and the sealed receipt says whether an unsettled durable
// commitment protects it or it is abandoned.
func successionPredecessors(ctx context.Context, checkout string, projection workroom.Projection, changes []mergeChange, targetPreHead, candidate string) map[string]successionCandidate {
	shared := mergeplan.Classify(ctx, checkout, projection, sharedChanges(changes), targetPreHead, candidate, nil)
	classified := make(map[string]successionCandidate, len(shared))
	for event, candidate := range shared {
		if candidate.Class == mergeplan.ClassInTargetPredecessor || candidate.Class == mergeplan.ClassReviewedCandidate {
			classified[event] = successionCandidate{predecessor: true}
		} else {
			classified[event] = successionCandidate{leftLive: mergeLeftLive{Class: candidate.LeftLive.Class, Commitment: candidate.LeftLive.Commitment}}
		}
	}
	return classified
}

func coveredArtifacts(projection workroom.Projection, changes []mergeChange) []workroom.Artifact {
	return mergeplan.CoveredArtifacts(projection, sharedChanges(changes))
}

func artifactCoversPath(artifact, changed string) bool {
	return artifact != "" && changed != "" && (artifact == changed || strings.HasPrefix(changed, strings.TrimSuffix(artifact, "/")+"/"))
}

// protectionIndex computes the checkable witness once for every covered
// candidate. Only projected durable commitment rows count; leased presence and
// conversation never enter this projection. Both provenance directions matter:
// review work reaches the artifact, while an implementation artifact serving
// as a report rests on its promise. Traversal stops at ineffective records,
// matching foldState.effectiveProvenanceReaches.
func protectionIndex(projection workroom.Projection, artifacts []workroom.Artifact) map[string]string {
	statements := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements[statement.Event] = statement
	}
	effective := make(map[string]bool, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		effective[decision.Event] = decision.Verdict == workroom.Effective
	}
	active := make(map[string]bool)
	for _, commitment := range projection.Commitments {
		if !unsettledCommitment(commitment.Status) {
			continue
		}
		for _, event := range []string{commitment.Request, commitment.Promise, commitment.Report} {
			statement, found := statements[event]
			if !found || (statement.Lifecycle != workroom.LifecycleRequest && statement.Lifecycle != workroom.LifecyclePromise && statement.Lifecycle != workroom.LifecycleReport) {
				continue
			}
			active[event] = true
		}
	}
	artifactByEvent := make(map[string]workroom.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		artifactByEvent[artifact.Event] = artifact
	}
	protected := make(map[string]string)
	consider := func(artifact, commitment string) {
		if current := protected[artifact]; current == "" || commitment < current {
			protected[artifact] = commitment
		}
	}
	byCommit := make(map[string]string)
	for event := range active {
		statement := statements[event]
		for _, commit := range []string{statement.Body["head"], statement.Body["commit"]} {
			if commit != "" && (byCommit[commit] == "" || event < byCommit[commit]) {
				byCommit[commit] = event
			}
		}
		for reached := range provenanceClosure(projection.Provenance, effective, event) {
			if _, ok := artifactByEvent[reached]; ok {
				consider(reached, event)
			}
		}
	}
	for _, artifact := range artifacts {
		if event := byCommit[artifact.Commit]; artifact.Commit != "" && event != "" {
			consider(artifact.Event, event)
		}
		for reached := range provenanceClosure(projection.Provenance, effective, artifact.Event) {
			if active[reached] {
				consider(artifact.Event, reached)
			}
		}
	}
	return protected
}

func unsettledCommitment(status string) bool {
	switch status {
	case "open", "promised", "reported", "awaiting-merge", "stale":
		return true
	default:
		return false
	}
}

func provenanceClosure(provenance map[string][]string, effective map[string]bool, from string) map[string]bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		event := queue[0]
		queue = queue[1:]
		// A synthetic projection may omit Decisions, but a projected decision
		// that is present and not effective is a real break in provenance. This
		// matches foldState.effectiveProvenanceReaches, so the CLI cannot seal
		// testimony which the fold must reject.
		if decision, projected := effective[event]; projected && !decision {
			continue
		}
		for _, basis := range provenance[event] {
			if !seen[basis] {
				seen[basis] = true
				queue = append(queue, basis)
			}
		}
	}
	return seen
}

// preflightSuccession refuses the two retirements that cannot be repaired
// afterwards, and only those.
//
// The citation guard exists because a page resting on a withdrawn pointer has
// nowhere to go. A merge retirement that this same plan gives a successor is a
// different act: the supersession names the successor artifact, so the log
// carries the reader from the old pointer to the current one at a path that
// still covers the behaviour, and the documentation gate follows that link and
// flares rather than failing. Refusing it was a deadlock with no legal exit —
// the successor cannot exist until the merge lands, and the merge could not
// land until the pages were repointed at the successor. Passing every
// retirement through as allowed would be the other half of the same mistake,
// so a bare retirement, which orphans whatever cites it, is still refused here
// and still needs a deliberate `gs supersede --cited-ok` after the pages move.
func preflightSuccession(ctx context.Context, workspace *app.Workspace, checkout string, plan successionPlan) error {
	return mergeplan.ValidateSuccession(ctx, workspace, checkout, mergeplan.Succession{
		Publish: plan.publish, Retire: plan.retire, ChangedPaths: plan.changedPaths,
	})
}

// refuseUnreachableCrossAuthorRetirements states the command's own reach bound
// before the merge commit exists, so a plan outside the prospective policy is
// refused while the target is still unchanged rather than discovered after
// `HEAD` has moved and half the succession has landed. The bound is narrower
// than the fold's symmetric lineage rule, which is unchanged and goes on
// judging what has already been sealed.
//
// The bound is the approval's: a receipt reaches another actor's pointer only
// on the path lineage of the artifact that approval names. The fold holds no
// repository and cannot check a merge head or a diff, so the reviewer's signed
// choice of artifact is the only fact bounding the merger that the merger did
// not write.
//
// Live standing is deliberately not consulted, though the fold does grant a
// ratifier free-standing authority to retire anything. Standing can be revoked
// between this check and the acts it would authorize, and the fold judges each
// supersession after `HEAD` has moved, so admitting a plan on a role is
// admitting it on a fact that may not survive the merge. Refusing here is
// recoverable and costs a caller nothing; discovering it afterwards leaves the
// target moved and the succession half-done.
func refuseUnreachableCrossAuthorRetirements(projection workroom.Projection, plan successionPlan, approval, actor string) error {
	return mergeplan.ValidateReach(projection, mergeplan.Succession{Publish: plan.publish, Retire: plan.retire, ChangedPaths: plan.changedPaths}, approval, actor)
}

// reviewedPaths reads which artifacts an approval puts within reach the way
// the fold reads it: the artifacts the reviewer cited as bases of the verdict,
// each standing at the exact head approved and owned by the implementer it
// binds. That selection has to agree with the fold, or the command refuses
// merges the fold would allow — which is what stranded a head spanning four
// maintained trees — or admits ones the fold refuses, which strands a
// succession after the target has moved.
//
// The reach of those paths does not agree, and deliberately so. The fold's own
// lineage test still reads both directions and is unchanged; this command-side
// guard narrows its reading to one direction, prospectively, and runs once, in
// fresh-merge preflight while the target is still where it was. Succession
// recording never re-applies it: a receipt sealed under the symmetric reading
// keeps the authority it was sealed with.
//
// The command applies one check the fold cannot: whether a candidate describes
// a superseded world, which is known only after the whole log is folded.
// Ordinary reasoning staleness remains review evidence and does not erase the
// reviewer's signed reach over this exact head.
func reviewedPaths(projection workroom.Projection, approval string) []string {
	_, paths := mergeplan.ReviewedScope(projection, approval)
	return paths
}

// withinReviewedPaths reports whether an artifact standing at path lies inside
// what the approval reviewed. Reach runs one way only: a reviewed path bounds
// retirement at itself and beneath it, never above it. This is narrower than
// the fold, whose lineage test still reads both directions; the difference is
// deliberate, prospective, and never re-applied to a sealed receipt. Reviewing
// one page is not authority over the tree that contains it, so a head that
// reviews `docs/how-to/x.md` does not take another actor's pointer at bare
// `docs` with it.
func withinReviewedPaths(reviewed []string, path string) bool {
	if path == "" {
		return false
	}
	for _, within := range reviewed {
		if within != "" && (within == path || widerPath(within, path)) {
			return true
		}
	}
	return false
}

func artifactPath(projection workroom.Projection, event string) string {
	for _, artifact := range projection.Artifacts {
		if artifact.Event == event {
			return artifact.Path
		}
	}
	return ""
}

// recordMergeSuccession appends the durable succession suffix a sealed Git
// receipt owes: the receipt assertion, one successor artifact per published
// path, and one supersession per retired predecessor. The acts are idempotent,
// so the same call both completes a fresh merge and resumes an interrupted
// one.
//
// The prospective directional reviewed-path guard does not run here, for
// either entry: reach is judged once, in fresh-merge preflight while the
// target is still where it was. Re-judging here could refuse a plan after
// `HEAD` has moved — a fresh merge overtaken by concurrent admissions, or a
// receipt sealed under the older symmetric reading of reach — and strand it
// before its durable suffix completes. No authority moves because of this:
// every act below still crosses the fold's own unchanged rule at admission,
// signature checks, citation preflight, and admission bounds all still run.
func recordMergeSuccession(ctx context.Context, workspace *app.Workspace, checkout, serverURL, actor string, private ed25519.PrivateKey, receipt mergeReceipt) error {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	// Also on the resume path, which reaches here without validateMerge: a
	// receipt the fold will refuse should never be appended at all.
	if err := requireApprovedImplementer(snapshot.Projection, receipt.Approval,
		workspace.View().Actors[actor].Fingerprint); err != nil {
		return err
	}
	if receipt.LeftLivePresent != receipt.ChangedPathsPresent {
		return errors.New("merge receipt must carry Gitseq-Left-Live and Gitseq-Changed-Paths together, or neither for a legacy receipt")
	}
	var gitLeftLive map[string]mergeLeftLive
	if receipt.LeftLivePresent {
		if err := json.Unmarshal([]byte(receipt.LeftLive), &gitLeftLive); err != nil {
			return fmt.Errorf("decode Git receipt left-live accounting: %w", err)
		}
		if gitLeftLive == nil {
			return errors.New("decode Git receipt left-live accounting: expected a JSON object, got null")
		}
	}
	var gitChangedPaths []string
	if receipt.ChangedPathsPresent {
		gitChangedPaths, err = decodeChangedPaths(receipt.ChangedPaths, "Git receipt")
		if err != nil {
			return err
		}
		changes, err := mergeChanges(ctx, checkout, receipt.MergeHead)
		if err != nil {
			return fmt.Errorf("verify Git receipt changed paths: %w", err)
		}
		actual := mergeChangedPaths(changes)
		if !slices.Equal(gitChangedPaths, actual) {
			return fmt.Errorf("Git receipt changed paths %q do not equal merge first-parent diff paths %q", gitChangedPaths, actual)
		}
	}
	plan, found, err := recordedSuccessionPlan(snapshot.Projection, receipt)
	if err != nil {
		return err
	}
	if !found {
		if receipt.Retirements == "" || receipt.Successors == "" {
			return errors.New("merge receipt has no sealed succession plan")
		}
		if err := json.Unmarshal([]byte(receipt.Retirements), &plan.retire); err != nil {
			return fmt.Errorf("decode Git receipt retirements: %w", err)
		}
		if err := json.Unmarshal([]byte(receipt.Successors), &plan.publish); err != nil {
			return fmt.Errorf("decode Git receipt successors: %w", err)
		}
		plan.leftLive = gitLeftLive
		plan.changedPaths = gitChangedPaths
	} else {
		if receipt.LeftLivePresent != (plan.leftLive != nil) || (receipt.LeftLivePresent && !maps.Equal(gitLeftLive, plan.leftLive)) {
			return errors.New("recorded merge left-live accounting does not match the sealed Git receipt")
		}
		if receipt.ChangedPathsPresent != (plan.changedPaths != nil) || (receipt.ChangedPathsPresent && !slices.Equal(gitChangedPaths, plan.changedPaths)) {
			return errors.New("recorded merge changed paths do not match the sealed Git receipt")
		}
	}
	if err := preflightSuccession(ctx, workspace, checkout, plan); err != nil {
		return err
	}
	acts := successionActs(receipt.Approval, receipt.Candidate, receipt.TargetPreHead, receipt.MergeHead, receipt.Staleness, plan)
	if err := preflightBatchAdmission(ctx, workspace, serverURL, actor, private, acts, true); err != nil {
		return fmt.Errorf("merge succession admission preflight: %w", err)
	}
	// The exact checkout preflight above is the deliberate authorization for
	// bypassing Workspace's repository-default citation guard here.
	if _, err := runBatch(ctx, workspace, serverURL, actor, private, acts, true); err != nil {
		return fmt.Errorf("record merge succession: %w", err)
	}
	return verifySuccession(ctx, workspace, receipt, plan)
}

func recordedSuccessionPlan(projection workroom.Projection, receipt mergeReceipt) (successionPlan, bool, error) {
	for _, statement := range projection.Statements {
		if statement.Kind != workroom.KindAssert || statement.Body["merge_approval"] != receipt.Approval || statement.Body["merge_head"] != receipt.MergeHead {
			continue
		}
		var plan successionPlan
		if err := json.Unmarshal([]byte(statement.Body["merge_retirements"]), &plan.retire); err != nil {
			return successionPlan{}, false, fmt.Errorf("decode recorded merge retirements: %w", err)
		}
		if err := json.Unmarshal([]byte(statement.Body["merge_successors"]), &plan.publish); err != nil {
			return successionPlan{}, false, fmt.Errorf("decode recorded merge successors: %w", err)
		}
		if encoded, present := statement.Body["merge_left_live"]; present {
			if err := json.Unmarshal([]byte(encoded), &plan.leftLive); err != nil {
				return successionPlan{}, false, fmt.Errorf("decode recorded merge left-live accounting: %w", err)
			}
			if plan.leftLive == nil {
				return successionPlan{}, false, errors.New("decode recorded merge left-live accounting: expected a JSON object, got null")
			}
		}
		if encoded, present := statement.Body["merge_changed_paths"]; present {
			var err error
			plan.changedPaths, err = decodeChangedPaths(encoded, "recorded merge receipt")
			if err != nil {
				return successionPlan{}, false, err
			}
		}
		return plan, true, nil
	}
	return successionPlan{}, false, nil
}

func decodeChangedPaths(raw, source string) ([]string, error) {
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, fmt.Errorf("decode %s changed paths: %w", source, err)
	}
	if paths == nil {
		return nil, fmt.Errorf("decode %s changed paths: expected a JSON array, got null", source)
	}
	for index, path := range paths {
		if path == "" {
			return nil, fmt.Errorf("decode %s changed paths: path %d is empty", source, index)
		}
		if index > 0 && paths[index-1] >= path {
			return nil, fmt.Errorf("decode %s changed paths: paths must be sorted and unique", source)
		}
	}
	canonical, err := json.Marshal(paths)
	if err != nil {
		return nil, fmt.Errorf("encode %s changed paths: %w", source, err)
	}
	if string(canonical) != raw {
		return nil, fmt.Errorf("decode %s changed paths: JSON is not canonical", source)
	}
	return paths, nil
}

func widerPath(candidate, current string) bool {
	return candidate != current && strings.HasPrefix(current, strings.TrimSuffix(candidate, "/")+"/")
}

func successionActs(approval, candidate, targetPreHead, mergeHead, staleness string, plan successionPlan) []batchAct {
	if (plan.leftLive != nil) != (plan.changedPaths != nil) {
		return nil
	}
	leftLive := make(map[string]mergeplan.LeftLive, len(plan.leftLive))
	for event, left := range plan.leftLive {
		leftLive[event] = mergeplan.LeftLive{Class: left.Class, Commitment: left.Commitment}
	}
	shared := mergeplan.SuccessionActs(approval, candidate, targetPreHead, mergeHead, staleness, mergeplan.Succession{
		Publish: plan.publish, Retire: plan.retire, ChangedPaths: plan.changedPaths, LeftLive: leftLive,
	})
	acts := make([]batchAct, 0, len(shared))
	for _, entry := range shared {
		act := entry.Act
		acts = append(acts, batchAct{
			Label: entry.Label, Verb: act.Verb, Kind: act.Kind, Text: act.Text, Body: act.Body,
			Target: act.Target, RestsOn: act.RestsOn, IdempotencyKey: act.IdempotencyKey,
			AllowDeadBasis: act.AllowDeadBasis,
		})
	}
	return acts
}

func verifySuccession(ctx context.Context, workspace *app.Workspace, receipt mergeReceipt, plan successionPlan) error {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
	}
	for target := range plan.retire {
		artifact, err := standingArtifact(snapshot.Projection, target)
		if err == nil || !strings.Contains(err.Error(), "retired") {
			return fmt.Errorf("merge succession did not retire predecessor %s (artifact %+v, error %v)", target, artifact, err)
		}
	}
	for _, path := range plan.publish {
		live := 0
		for _, artifact := range snapshot.Projection.Artifacts {
			if artifact.Path == path && artifact.Commit == receipt.MergeHead && !artifact.Retired && !artifact.DescribesSupersededWorld {
				live++
			}
		}
		if live != 1 {
			return fmt.Errorf("merge succession left %d current successors at %s, want one", live, path)
		}
	}
	return nil
}

// repeatedFlag collects a flag given more than once, in order.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatedFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value may not be blank")
	}
	*r = append(*r, value)
	return nil
}
