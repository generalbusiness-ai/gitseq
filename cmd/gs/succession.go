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

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

type mergeChange = mergeplan.Change

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

func parseMergeChanges(raw string) ([]mergeChange, error) {
	shared, err := mergeplan.ParseChanges(raw)
	if err != nil {
		return nil, errMalformedDiff
	}
	return shared, nil
}

var errMalformedDiff = errors.New("malformed NUL-delimited merge diff")

// The command, Git receipt and shared evaluator use one lossless succession
// value. Aliases keep the historical command names readable without another
// authority-bearing representation or converter.
type successionPlan = mergeplan.Succession
type mergeLeftLive = mergeplan.LeftLive

// mergeChangedPaths seals the complete path set from the diff: both sides of
// a rename or copy, the old side of a deletion, and the new side of every
// addition or modification. A set makes repeated paths harmless; sorting makes
// the JSON stable across Git's output order and retries.
func mergeChangedPaths(changes []mergeChange) []string {
	return mergeplan.ChangedPaths(changes)
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
	if _, coherent := receipt.TargetPairPresent(); !coherent {
		return errors.New("merge receipt must carry Gitseq-Target-Repo and Gitseq-Target-Ref together, or neither for a legacy receipt")
	}
	if receipt.HoldWarning != "" && receipt.HoldWarning != "true" {
		return fmt.Errorf("merge receipt Gitseq-Hold-Warning is %q, want true or absence", receipt.HoldWarning)
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
		if err := json.Unmarshal([]byte(receipt.Retirements), &plan.Retire); err != nil {
			return fmt.Errorf("decode Git receipt retirements: %w", err)
		}
		if err := json.Unmarshal([]byte(receipt.Successors), &plan.Publish); err != nil {
			return fmt.Errorf("decode Git receipt successors: %w", err)
		}
		plan.LeftLive = gitLeftLive
		plan.ChangedPaths = gitChangedPaths
	} else {
		if receipt.LeftLivePresent != (plan.LeftLive != nil) || (receipt.LeftLivePresent && !maps.Equal(gitLeftLive, plan.LeftLive)) {
			return errors.New("recorded merge left-live accounting does not match the sealed Git receipt")
		}
		if receipt.ChangedPathsPresent != (plan.ChangedPaths != nil) || (receipt.ChangedPathsPresent && !slices.Equal(gitChangedPaths, plan.ChangedPaths)) {
			return errors.New("recorded merge changed paths do not match the sealed Git receipt")
		}
	}
	if err := mergeplan.ValidateSuccession(ctx, workspace, checkout, plan); err != nil {
		return err
	}
	acts := successionActs(receipt.Approval, receipt.Authorization, receipt.AuthorizationRatification, receipt.Candidate,
		mergeplan.Target{Repo: receipt.TargetRepo, Ref: receipt.TargetRef, PreHead: receipt.TargetPreHead},
		receipt.MergeHead, receipt.Staleness, receipt.HoldWarning == "true", plan)
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
		// The durable receipt and the Git trailers are two copies of one
		// binding, and only one of them can be edited after the fact. Reading
		// both and requiring them equal is what makes a rewritten trailer a
		// refusal rather than a quiet re-landing somewhere else.
		if statement.Body["merge_target_repo"] != receipt.TargetRepo || statement.Body["merge_target_ref"] != receipt.TargetRef {
			return successionPlan{}, false, errors.New("recorded merge target does not match the sealed Git receipt")
		}
		if statement.Body["merge_target_pre_head"] != receipt.TargetPreHead {
			return successionPlan{}, false, errors.New("recorded merge target pre-head does not match the sealed Git receipt")
		}
		if statement.Body["merge_hold_warning"] != receipt.HoldWarning {
			return successionPlan{}, false, errors.New("recorded merge hold warning does not match the sealed Git receipt")
		}
		var plan successionPlan
		if err := json.Unmarshal([]byte(statement.Body["merge_retirements"]), &plan.Retire); err != nil {
			return successionPlan{}, false, fmt.Errorf("decode recorded merge retirements: %w", err)
		}
		if err := json.Unmarshal([]byte(statement.Body["merge_successors"]), &plan.Publish); err != nil {
			return successionPlan{}, false, fmt.Errorf("decode recorded merge successors: %w", err)
		}
		if encoded, present := statement.Body["merge_left_live"]; present {
			if err := json.Unmarshal([]byte(encoded), &plan.LeftLive); err != nil {
				return successionPlan{}, false, fmt.Errorf("decode recorded merge left-live accounting: %w", err)
			}
			if plan.LeftLive == nil {
				return successionPlan{}, false, errors.New("decode recorded merge left-live accounting: expected a JSON object, got null")
			}
		}
		if encoded, present := statement.Body["merge_changed_paths"]; present {
			var err error
			plan.ChangedPaths, err = decodeChangedPaths(encoded, "recorded merge receipt")
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

func successionActs(approval, authorization, authorizationRatification, candidate string, target mergeplan.Target, mergeHead, staleness string, holdWarning bool, plan successionPlan) []batchAct {
	if (plan.LeftLive != nil) != (plan.ChangedPaths != nil) {
		return nil
	}
	shared := mergeplan.SuccessionActs(approval, authorization, authorizationRatification, candidate, target, mergeHead, staleness, holdWarning, plan)
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
	for target := range plan.Retire {
		artifact, err := mergeplan.StandingArtifact(snapshot.Projection, target)
		if err == nil || !strings.Contains(err.Error(), "retired") {
			return fmt.Errorf("merge succession did not retire predecessor %s (artifact %+v, error %v)", target, artifact, err)
		}
	}
	for _, path := range plan.Publish {
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
