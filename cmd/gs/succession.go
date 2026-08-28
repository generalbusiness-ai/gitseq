package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type mergeChange struct {
	status string
	old    string
	new    string
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
	fields := strings.Split(raw, "\x00")
	if len(fields) != 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	var changes []mergeChange
	for position := 0; position < len(fields); {
		status := fields[position]
		position++
		if status == "" || position >= len(fields) {
			return nil, errMalformedDiff
		}
		change := mergeChange{status: status, new: fields[position]}
		position++
		if status[0] == 'R' || status[0] == 'C' {
			change.old = change.new
			if position >= len(fields) {
				return nil, errMalformedDiff
			}
			change.new = fields[position]
			position++
		} else if status[0] == 'D' {
			change.old, change.new = change.new, ""
		}
		changes = append(changes, change)
	}
	return changes, nil
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
	var live []workroom.Artifact
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == "." || strings.Contains(artifact.Path, ",") {
			continue
		}
		live = append(live, artifact)
	}
	published := map[string]bool{}
	retire := map[string]string{}
	var leftLive map[string]mergeLeftLive
	if candidates != nil {
		leftLive = make(map[string]mergeLeftLive)
	}

	covering := func(path string, includeExact bool) []workroom.Artifact {
		var found []workroom.Artifact
		for _, artifact := range live {
			if artifact.Path == path {
				if includeExact {
					found = append(found, artifact)
				}
				continue
			}
			if strings.HasPrefix(path, strings.TrimSuffix(artifact.Path, "/")+"/") {
				found = append(found, artifact)
			}
		}
		return found
	}
	// The sentinel is emptiness, never the fallback string. An earlier form
	// seeded the winner with the changed path, so a narrow artifact sitting at
	// exactly that path left `winner == fallback` true and the next artifact
	// won by that arm whatever the comparison said — the wider-path rule was
	// unreachable, and disabling it changed no result anywhere.
	widest := func(artifacts []workroom.Artifact, fallback string) string {
		winner := ""
		for _, artifact := range artifacts {
			if winner == "" || widerPath(artifact.Path, winner) {
				winner = artifact.Path
			}
		}
		if winner == "" {
			return fallback
		}
		return winner
	}
	assign := func(artifact workroom.Artifact, successor string) {
		if candidate, classified := candidates[artifact.Event]; classified && !candidate.predecessor {
			leftLive[artifact.Event] = candidate.leftLive
			return
		}
		current, exists := retire[artifact.Event]
		if !exists || current == "" || (successor != "" && widerPath(successor, current)) {
			retire[artifact.Event] = successor
		}
	}
	landed := func(path string) {
		covers := covering(path, true)
		winner := widest(covers, path)
		published[winner] = true
		for _, artifact := range covers {
			assign(artifact, winner)
		}
	}
	removed := func(path string) {
		// An artifact at the removed file is gone. A directory which covered it
		// survives with changed contents and therefore receives a successor.
		for _, artifact := range covering(path, true) {
			if artifact.Path == path {
				assign(artifact, "")
			}
		}
		covers := covering(path, false)
		if len(covers) == 0 {
			return
		}
		winner := widest(covers, path)
		published[winner] = true
		for _, artifact := range covers {
			assign(artifact, winner)
		}
	}

	for _, change := range changes {
		switch change.status[0] {
		case 'D':
			removed(change.old)
		case 'R':
			removed(change.old)
			landed(change.new)
		default:
			landed(change.new)
		}
	}
	paths := make([]string, 0, len(published))
	for path := range published {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return successionPlan{publish: paths, retire: retire, changedPaths: mergeChangedPaths(changes), leftLive: leftLive}
}

// mergeChangedPaths seals the complete path set from the diff: both sides of
// a rename or copy, the old side of a deletion, and the new side of every
// addition or modification. A set makes repeated paths harmless; sorting makes
// the JSON stable across Git's output order and retries.
func mergeChangedPaths(changes []mergeChange) []string {
	set := make(map[string]bool)
	for _, change := range changes {
		if change.old != "" {
			set[change.old] = true
		}
		if change.new != "" {
			set[change.new] = true
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// successionPredecessors classifies every live artifact rather than silently
// filtering non-ancestors out of the plan. A shipped artifact is a predecessor
// when its commit is in the target's world; the exact reviewed candidate is a
// predecessor because that world is what is landing. Every other candidate is
// left live and the sealed receipt says whether an unsettled durable
// commitment protects it or it is abandoned.
func successionPredecessors(ctx context.Context, checkout string, projection workroom.Projection, changes []mergeChange, targetPreHead, candidate string) map[string]successionCandidate {
	classified := make(map[string]successionCandidate)
	covered := coveredArtifacts(projection, changes)
	protected := protectionIndex(projection, covered)
	byCommit := make(map[string]bool)
	for _, artifact := range covered {
		if artifact.Commit == candidate {
			classified[artifact.Event] = successionCandidate{predecessor: true}
			continue
		}
		ancestor := false
		if artifact.Commit != "" {
			var checked bool
			ancestor, checked = byCommit[artifact.Commit]
			if !checked {
				_, err := git(ctx, checkout, "merge-base", "--is-ancestor", artifact.Commit, targetPreHead)
				ancestor = err == nil
				byCommit[artifact.Commit] = ancestor
			}
		}
		if ancestor {
			classified[artifact.Event] = successionCandidate{predecessor: true}
			continue
		}
		if commitment := protected[artifact.Event]; commitment != "" {
			classified[artifact.Event] = successionCandidate{leftLive: mergeLeftLive{Class: leftLiveSibling, Commitment: commitment}}
		} else {
			classified[artifact.Event] = successionCandidate{leftLive: mergeLeftLive{Class: leftLiveAbandoned}}
		}
	}
	return classified
}

func coveredArtifacts(projection workroom.Projection, changes []mergeChange) []workroom.Artifact {
	var covered []workroom.Artifact
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == "." || strings.Contains(artifact.Path, ",") {
			continue
		}
		for _, change := range changes {
			if artifactCoversPath(artifact.Path, change.old) || artifactCoversPath(artifact.Path, change.new) {
				covered = append(covered, artifact)
				break
			}
		}
	}
	return covered
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
	published := make(map[string]bool, len(plan.publish))
	for _, path := range plan.publish {
		if path == "." || strings.Contains(path, ",") {
			return fmt.Errorf("merge would publish an invalid artifact path %q", path)
		}
		published[path] = true
	}
	targets := make([]string, 0, len(plan.retire))
	for target := range plan.retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		successor := plan.retire[target]
		if successor == "" {
			if err := workspace.RefuseCitedRetirementInCheckout(ctx, checkout, target, false); err != nil {
				return err
			}
			continue
		}
		// A named successor only preserves the citation if this merge really
		// publishes it. A plan claiming one it does not publish would retire
		// the predecessor and leave the pages pointing at nothing.
		if !published[successor] {
			return fmt.Errorf("merge would retire %s naming a successor at %q that it does not publish", target, successor)
		}
	}
	return nil
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
	if actor == "" {
		return errors.New("merge succession needs the merging actor's fingerprint")
	}
	reviewed := reviewedPaths(projection, approval)
	if len(reviewed) == 0 {
		return fmt.Errorf("approval %s puts no artifact path within reach, so it bounds no retirement", approval)
	}
	authors := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	targets := make([]string, 0, len(plan.retire))
	for target := range plan.retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		path := artifactPath(projection, target)
		if authors[target] == actor || withinReviewedPaths(reviewed, path) {
			continue
		}
		return fmt.Errorf("merge would retire %s at %q, which belongs to another actor and lies outside the reviewed paths %s:\nhave the approval cover it, or ask its author or an actor holding ratifier to retire it. See docs/reference/gs/merge.md#approval-scope-and-receipt",
			target, path, strings.Join(reviewed, ", "))
	}
	return nil
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
	review, found := projection.Review(approval)
	if !found || review.Implementer == "" || review.Head == "" {
		return nil
	}
	authors := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	standing := make(map[string]workroom.Artifact, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		standing[artifact.Event] = artifact
	}
	// The same temporal rule the merge guard applies to the approval's primary
	// artifact, read from the same fold-published date. A pointer the reviewer
	// signed still bounds what their approval reaches when the world moved after
	// they signed it; one that already described a superseded world when they
	// looked does not, and an undated one fails closed.
	verdict := verdictSequence(projection, approval)
	var paths []string
	seen := make(map[string]bool)
	for _, basis := range projection.Provenance[approval] {
		artifact, isArtifact := standing[basis]
		if !isArtifact || artifact.Retired || artifact.Path == "" {
			continue
		}
		if artifact.DescribesSupersededWorld && !worldMovedAfterVerdict(artifact, verdict) {
			continue
		}
		if artifact.Commit != review.Head || authors[basis] != review.Implementer {
			continue
		}
		if seen[artifact.Path] {
			continue
		}
		seen[artifact.Path] = true
		paths = append(paths, artifact.Path)
	}
	sort.Strings(paths)
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
	acts := successionActs(receipt.Approval, receipt.Authorization, receipt.AuthorizationRatification, receipt.Candidate, receipt.TargetPreHead, receipt.MergeHead, receipt.Staleness, plan)
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
		if statement.Body["merge_authorization"] != receipt.Authorization {
			return successionPlan{}, false, errors.New("recorded merge authorization does not match the sealed Git receipt")
		}
		if statement.Body["merge_authorization_ratification"] != receipt.AuthorizationRatification {
			return successionPlan{}, false, errors.New("recorded merge authorization ratification does not match the sealed Git receipt")
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

func successionKey(approval, class, value string) string {
	sum := sha256.Sum256([]byte(approval + "\x00" + class + "\x00" + value))
	return "merge-succession-" + hex.EncodeToString(sum[:])
}

func successionActs(approval, authorization, authorizationRatification, candidate, targetPreHead, mergeHead, staleness string, plan successionPlan) []batchAct {
	if (plan.leftLive != nil) != (plan.changedPaths != nil) {
		return nil
	}
	retirements, err := json.Marshal(plan.retire)
	if err != nil {
		return nil
	}
	successors, err := json.Marshal(plan.publish)
	if err != nil {
		return nil
	}
	receiptBody := map[string]string{
		"merge_approval": approval, "merge_candidate": candidate,
		"merge_target_pre_head": targetPreHead, "merge_head": mergeHead,
		"merge_retirements": string(retirements), "merge_successors": string(successors),
	}
	if authorization != "" {
		receiptBody["merge_authorization"] = authorization
		receiptBody["merge_authorization_ratification"] = authorizationRatification
	}
	if plan.leftLive != nil {
		leftLive, err := json.Marshal(plan.leftLive)
		if err != nil {
			return nil
		}
		receiptBody["merge_left_live"] = string(leftLive)
	}
	if plan.changedPaths != nil {
		changedPaths, err := json.Marshal(plan.changedPaths)
		if err != nil {
			return nil
		}
		receiptBody["merge_changed_paths"] = string(changedPaths)
	}
	if staleness != "" {
		receiptBody["stale"] = "true"
		receiptBody["staleness"] = staleness
	}
	// The merge machinery rests on the approval it was granted, whatever had
	// moved underneath it while it was reviewed; validateMerge has already
	// judged that movement and the receipt records it. Dead bases here are
	// expected, so every act asks for the recorded escape.
	receiptBases := []string{approval}
	if authorization != "" {
		receiptBases = append(receiptBases, authorization, authorizationRatification)
	}
	acts := []batchAct{{
		Label: "merge", Verb: app.VerbState, Kind: workroom.KindAssert,
		Text:    "approved candidate merged",
		Body:    receiptBody,
		RestsOn: receiptBases, IdempotencyKey: mergeReceiptKey(approval),
		AllowDeadBasis: true,
	}}
	labels := make(map[string]string, len(plan.publish))
	for index, path := range plan.publish {
		label := fmt.Sprintf("successor-%d", index)
		labels[path] = label
		acts = append(acts, batchAct{
			Label: label, Verb: app.VerbState, Kind: workroom.KindArtifact,
			Text:    "Merge published the current artifact at " + path,
			Body:    map[string]string{"path": path, "commit": mergeHead},
			RestsOn: []string{"$merge"}, IdempotencyKey: successionKey(approval, "publish", path),
			AllowDeadBasis: true,
		})
	}
	targets := make([]string, 0, len(plan.retire))
	for target := range plan.retire {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		successor := plan.retire[target]
		rests := []string{"$merge"}
		text := "Merge retired a covered predecessor with no successor at its old path."
		if successor != "" {
			rests = append(rests, "$"+labels[successor])
			text = "Merge retired a covered predecessor; the successor is at " + successor + "."
		}
		acts = append(acts, batchAct{
			Verb: app.VerbSupersede, Target: target, Text: text, RestsOn: rests,
			IdempotencyKey: successionKey(approval, "retire", target),
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
