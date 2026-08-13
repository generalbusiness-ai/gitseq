package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

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
			return nil, errorsNewMalformedDiff()
		}
		change := mergeChange{status: status, new: fields[position]}
		position++
		if status[0] == 'R' || status[0] == 'C' {
			change.old = change.new
			if position >= len(fields) {
				return nil, errorsNewMalformedDiff()
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

func errorsNewMalformedDiff() error { return fmt.Errorf("malformed NUL-delimited merge diff") }

type successionPlan struct {
	publish []string
	retire  map[string]string // predecessor event -> successor path, empty when gone
}

// planSuccession is deterministic over the merge diff and the fold snapshot.
// Live includes stale artifacts: stale says a basis moved, not that the pointer
// was withdrawn. Historical unmaintainable paths are ignored; state@1 prevents
// any more from entering the effective set.
func planSuccession(projection workroom.Projection, changes []mergeChange, mergeHead string, predecessors map[string]bool) successionPlan {
	type liveArtifact struct {
		workroom.Artifact
		publishedByThisMerge bool
	}
	var live []liveArtifact
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Path == "." || strings.Contains(artifact.Path, ",") {
			continue
		}
		if predecessors != nil && !predecessors[artifact.Event] && artifact.Commit != mergeHead {
			continue
		}
		live = append(live, liveArtifact{Artifact: artifact, publishedByThisMerge: artifact.Commit == mergeHead})
	}
	published := map[string]bool{}
	retire := map[string]string{}

	covering := func(path string, includeExact bool) []liveArtifact {
		var found []liveArtifact
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
	widest := func(artifacts []liveArtifact, fallback string) string {
		winner := fallback
		for _, artifact := range artifacts {
			if winner == fallback || widerPath(artifact.Path, winner) {
				winner = artifact.Path
			}
		}
		return winner
	}
	assign := func(artifact liveArtifact, successor string) {
		if artifact.publishedByThisMerge {
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
				if _, exists := retire[artifact.Event]; !exists {
					retire[artifact.Event] = ""
				}
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
	return successionPlan{publish: paths, retire: retire}
}

// successionPredecessors separates pointers to the world this merge changes
// from pointers to other proposed worlds. A shipped artifact is eligible when
// its commit is in the target's first-parent world; an artifact for the exact
// reviewed candidate is eligible because that world is what is landing. An
// unrelated live candidate at the same path remains live and reviewable.
func successionPredecessors(ctx context.Context, checkout string, projection workroom.Projection, targetPreHead, candidate string) map[string]bool {
	eligible := make(map[string]bool)
	byCommit := make(map[string]bool)
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Commit == "" {
			continue
		}
		if artifact.Commit == candidate {
			eligible[artifact.Event] = true
			continue
		}
		ancestor, checked := byCommit[artifact.Commit]
		if !checked {
			_, err := git(ctx, checkout, "merge-base", "--is-ancestor", artifact.Commit, targetPreHead)
			ancestor = err == nil
			byCommit[artifact.Commit] = ancestor
		}
		if ancestor {
			eligible[artifact.Event] = true
		}
	}
	return eligible
}

func preflightSuccession(ctx context.Context, workspace *app.Workspace, checkout string, plan successionPlan) error {
	for _, path := range plan.publish {
		if path == "." || strings.Contains(path, ",") {
			return fmt.Errorf("merge would publish an invalid artifact path %q", path)
		}
	}
	for target := range plan.retire {
		if err := workspace.RefuseCitedRetirementInCheckout(ctx, checkout, target, false); err != nil {
			return err
		}
	}
	return nil
}

func recordMergeSuccession(ctx context.Context, workspace *app.Workspace, checkout, serverURL, actor string, private ed25519.PrivateKey, receipt mergeReceipt) error {
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return err
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
	}
	if err := preflightSuccession(ctx, workspace, checkout, plan); err != nil {
		return err
	}
	acts := successionActs(receipt.Approval, receipt.Candidate, receipt.TargetPreHead, receipt.MergeHead, plan)
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
		return plan, true, nil
	}
	return successionPlan{}, false, nil
}

func widerPath(candidate, current string) bool {
	return candidate != current && strings.HasPrefix(current, strings.TrimSuffix(candidate, "/")+"/")
}

func successionKey(approval, class, value string) string {
	sum := sha256.Sum256([]byte(approval + "\x00" + class + "\x00" + value))
	return "merge-succession-" + hex.EncodeToString(sum[:])
}

func successionActs(approval, candidate, targetPreHead, mergeHead string, plan successionPlan) []batchAct {
	retirements, err := json.Marshal(plan.retire)
	if err != nil {
		return nil
	}
	successors, err := json.Marshal(plan.publish)
	if err != nil {
		return nil
	}
	acts := []batchAct{{
		Label: "merge", Verb: app.VerbState, Kind: workroom.KindAssert,
		Text: "approved candidate merged",
		Body: map[string]string{
			"merge_approval": approval, "merge_candidate": candidate,
			"merge_target_pre_head": targetPreHead, "merge_head": mergeHead,
			"merge_retirements": string(retirements), "merge_successors": string(successors),
		},
		RestsOn: []string{approval}, IdempotencyKey: mergeReceiptKey(approval),
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
			if artifact.Path == path && artifact.Commit == receipt.MergeHead && !artifact.Retired && !artifact.Stale {
				live++
			}
		}
		if live != 1 {
			return fmt.Errorf("merge succession left %d current successors at %s, want one", live, path)
		}
	}
	return nil
}
