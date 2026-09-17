package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
)

// landCommand finishes an approved lane: ratify when that is this actor's act,
// preview, merge, push, and — only once Git says the head is in the target —
// remove the worktree and branch. It adds no authority; the merge is gs merge's
// own locked transaction. What it adds is the order, stated in
// docs/reference/gs/land.md. Two invariants govern this file: nothing durable
// is appended before the read-only refusals, and no deletion follows from a
// durable act having succeeded.
func landCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("land", arguments)
	as := set.String("as", "", "actor recording the merge receipt")
	approval := set.String("approval", "", "the ratified approval report this landing rests on")
	checkout := set.String("checkout", "", "the target checkout: the working tree standing on the request's target ref")
	text := set.String("text", "", "plain-language merge description and impact")
	cleanup := set.Bool("cleanup", false, "after the candidate is provably in the target, remove its worktree and delete its branch here and on origin")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return usageErrorf(set, "land takes no positional arguments")
	}
	if *approval == "" || *checkout == "" || strings.TrimSpace(*text) == "" {
		return usageErrorf(set, "land requires --approval, --checkout and --text: gs land --approval <event> --checkout <target checkout> --text '<what landed and why it matters>'")
	}
	session, err := openStep(ctx, *repo, *as, *serverFlag)
	if err != nil {
		return err
	}
	if err := session.resolve(approval); err != nil {
		return err
	}
	candidate, err := approvedCandidate(session, *approval)
	if err != nil {
		return err
	}
	// Every read-only refusal runs before the ratification. A ratification is
	// a durable act, and a run that is going to refuse over the checkout must
	// leave the log exactly as long as it found it; the merge preview below
	// is what actually needs the approval ratified.
	targetRef := landingTargetRef(session, *approval)
	if err := requireTargetCheckout(ctx, *checkout, targetRef); err != nil {
		return err
	}
	if err := requireCleanCheckout(ctx, *checkout); err != nil {
		return err
	}
	if err := ratifyApprovalIfOurs(session, *approval); err != nil {
		return err
	}
	if err := previewMerge(ctx, session, *checkout, candidate, *approval); err != nil {
		return err
	}
	if err := runLandingMerge(ctx, session, *checkout, candidate, *approval, *text); err != nil {
		return err
	}
	if targetRef == "" {
		targetRef = currentBranchRef(ctx, *checkout)
	}
	landed := ""
	if targetRef != "" {
		if head, err := git(ctx, *checkout, "rev-parse", "--verify", "--end-of-options", targetRef+"^{commit}"); err == nil {
			landed = strings.TrimSpace(head)
		}
	}
	if err := pushTarget(ctx, *checkout, targetRef); err != nil {
		return err
	}
	if !*cleanup {
		return nil
	}
	return cleanupCandidate(ctx, *checkout, candidate, targetRef, landed)
}

// approvedCandidate reads the exact head the verdict approved, rather than
// asking the caller to retype an immutable hash.
func approvedCandidate(session *stepSession, approval string) (string, error) {
	for _, review := range session.projection().Reviews {
		if review.Report != approval {
			continue
		}
		if review.Verdict != "approved" {
			return "", fmt.Errorf("%s is a %s verdict, so it authorizes no landing; correct the head, publish a fresh artifact with `gs artifact`, and ask for review again",
				short(approval), review.Verdict)
		}
		if review.Retired {
			return "", fmt.Errorf("approval %s is retired; a withdrawn verdict authorizes nothing, so ask for review of the current head again", short(approval))
		}
		if review.Head == "" {
			return "", fmt.Errorf("approval %s names no head, so there is nothing to land; file the verdict with gs review, which records body.head", short(approval))
		}
		return review.Head, nil
	}
	return "", fmt.Errorf("%s is not a review verdict in this workroom; gs land takes the approval report gs review filed (`gs reviews` lists them)", short(approval))
}

// ratifyApprovalIfOurs closes the one gap the merger can close themselves. The
// fold admits a ratification only from the review requester, so this offers it
// only to that actor and judges nothing itself.
func ratifyApprovalIfOurs(session *stepSession, approval string) error {
	statement, found := session.statement(approval)
	if !found {
		return fmt.Errorf("approval %s is not a statement in this workroom", short(approval))
	}
	if statement.Ratified {
		return nil
	}
	commitment, ok := session.commitmentByReport(approval)
	if !ok || commitment.Requester != session.fingerprint {
		requester := "the review requester"
		if ok {
			requester = session.name(commitment.Requester)
		}
		return fmt.Errorf("approval %s is not ratified, and only its review requester may ratify it; ask %s to run `gs ratify %s`",
			short(approval), requester, short(approval))
	}
	record, err := session.submit(app.Act{
		Verb: app.VerbRatify, Target: approval, IdempotencyKey: "gs-land-ratify/" + session.fingerprint + "/" + approval,
	})
	if err != nil {
		return fmt.Errorf("ratify approval %s: %w", short(approval), err)
	}
	fmt.Fprintf(os.Stderr, "gs: ratified approval %s as its review requester (%s)\n", short(approval), record.ID)
	return nil
}

// landingTargetRef is where this approval's implementation is owed. A
// self-initiated or evidence-only approval has no lane, which is not an error:
// the merge plan judges the destination either way.
func landingTargetRef(session *stepSession, approval string) string {
	commitment, err := approvalImplementationCommitment(session.projection(), approval)
	if err != nil {
		return ""
	}
	return commitment.TargetRef
}

func requireTargetCheckout(ctx context.Context, checkout, targetRef string) error {
	if targetRef == "" {
		return nil
	}
	standing := currentBranchRef(ctx, checkout)
	if standing == targetRef {
		return nil
	}
	where := "a detached HEAD"
	if standing != "" {
		where = standing
	}
	return fmt.Errorf("--checkout %s stands on %s, but this landing is owed to %s; --checkout is the target checkout — the working tree standing on %s — not the candidate's worktree",
		checkout, where, targetRef, targetRef)
}

func currentBranchRef(ctx context.Context, checkout string) string {
	standing, err := git(ctx, checkout, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(standing)
}

// requireCleanCheckout names the files it found, rather than sending a reader
// back to `git status`.
func requireCleanCheckout(ctx context.Context, checkout string) error {
	status, err := git(ctx, checkout, "status", "--porcelain=v1", "-uall")
	if err != nil {
		return err
	}
	var dirty []string
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			dirty = append(dirty, strings.TrimSpace(line))
		}
	}
	if len(dirty) == 0 {
		return nil
	}
	shown, omitted := dirty, 0
	if len(shown) > 10 {
		shown, omitted = shown[:10], len(dirty)-10
	}
	more := ""
	if omitted > 0 {
		more = fmt.Sprintf("\n  …and %d more", omitted)
	}
	return fmt.Errorf("--checkout %s is not clean, and a merge refuses a dirty checkout including untracked files:\n  %s%s\nCommit, stash or remove them, then run gs land again",
		checkout, strings.Join(shown, "\n  "), more)
}

// previewMerge runs the read-only plan first, so a refusal arrives before the
// approval is reserved and before anything is staged.
func previewMerge(ctx context.Context, session *stepSession, checkout, candidate, approval string) error {
	_, private, err := session.workspace.Actor(session.actor)
	if err != nil {
		return err
	}
	plan := buildMergePlan(ctx, session.workspace, checkout, candidate, approval, session.fingerprint, mergeplan.Signer{
		Name: session.actor, Private: private, ResidentCeiling: residentSubmissionCeiling(session.serverURL),
	})
	if plan.Allowed {
		return nil
	}
	if len(plan.Reasons) == 0 {
		return errors.New("the merge plan refused this landing without a reason; run `gs merge-plan` for the whole preview")
	}
	var refusals []string
	for _, reason := range plan.Reasons {
		refusals = append(refusals, fmt.Sprintf("%s: %s", reason.Check, reason.Reason))
	}
	return fmt.Errorf("the merge plan refuses this landing, and nothing was appended or staged:\n  %s", strings.Join(refusals, "\n  "))
}

// runLandingMerge is gs merge's own locked transaction. A frontier that moved
// between planning and landing is retried exactly once: that refusal leaves
// nothing behind, so the only honest recovery is to plan again.
func runLandingMerge(ctx context.Context, session *stepSession, checkout, candidate, approval, text string) error {
	authorization := ""
	merge := func() error {
		_, err := apphost.WithMetaLock(session.workspace.MetaDir, mergeLockFile, func() (struct{}, error) {
			return struct{}{}, mergeLocked(ctx, session.workspace, &session.actor, &checkout, &candidate, &approval, &authorization, &text, session.serverURL)
		})
		return err
	}
	err := merge()
	if err != nil && strings.Contains(err.Error(), "workroom frontier moved after planning") {
		fmt.Fprintf(os.Stderr, "gs: %v; planning again against the current frontier\n", err)
		err = merge()
	}
	return err
}

// pushTarget publishes what landed. No origin is an ordinary arrangement and
// says so; a push that fails with an origin present is an error, because the
// next step would delete the only other copy of those commits.
func pushTarget(ctx context.Context, checkout, targetRef string) error {
	if targetRef == "" {
		fmt.Fprintln(os.Stderr, "gs: this landing states no target ref, so nothing was pushed")
		return nil
	}
	if _, err := git(ctx, checkout, "remote", "get-url", "origin"); err != nil {
		fmt.Fprintf(os.Stderr, "gs: %s has no origin remote, so %s was not pushed\n", checkout, targetRef)
		return nil
	}
	if _, err := git(ctx, checkout, "push", "origin", targetRef); err != nil {
		return fmt.Errorf("the merge landed, but pushing %s to origin failed: %w; push it yourself, then rerun gs land to finish", targetRef, err)
	}
	fmt.Fprintf(os.Stderr, "gs: pushed %s to origin\n", targetRef)
	return nil
}

// cleanupCandidate removes the worktree and branch the work was done on, and
// only after Git has said the candidate is in the target. Everything here is
// irreversible, so each step is gated on a measurement taken immediately
// before it rather than on the previous command appearing to have worked, and
// each deletion carries the tip it expects so a ref somebody advanced in the
// meantime keeps its commits.
func cleanupCandidate(ctx context.Context, checkout, candidate, targetRef, landed string) error {
	contained, err := isAncestor(ctx, checkout, candidate, targetRef)
	if err != nil {
		return fmt.Errorf("could not measure whether %s is in %s, so nothing was deleted: %w", short(candidate), targetRef, err)
	}
	if !contained {
		return fmt.Errorf("%s is not in %s even though the merge reported success, so nothing was deleted; check %s before cleaning up by hand",
			short(candidate), targetRef, landed)
	}
	branch, err := candidateBranch(ctx, checkout, candidate, targetRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gs: %v; delete the worktree and branch by hand\n", err)
		return nil
	}
	if worktree := worktreeOfBranch(ctx, checkout, branch); worktree != "" {
		if _, err := git(ctx, checkout, "worktree", "remove", worktree); err != nil {
			return fmt.Errorf("the landing is complete, but the worktree at %s could not be removed: %w; remove it, then delete branch %s", worktree, err, branch)
		}
		fmt.Fprintf(os.Stderr, "gs: removed worktree %s\n", worktree)
	}
	if err := deleteLandedBranch(ctx, checkout, branch, candidate); err != nil {
		return err
	}
	deleteLandedRemoteBranch(ctx, checkout, branch, candidate)
	return nil
}

// deleteLandedBranch removes the local branch, and only while it still points
// at the head that landed. `git update-ref -d` takes the value it expects, so
// this is one compare-and-swap rather than a measurement followed by a forced
// delete: a branch somebody advanced in between keeps its commits, and says so.
func deleteLandedBranch(ctx context.Context, checkout, branch, candidate string) error {
	ref := "refs/heads/" + branch
	tip, err := git(ctx, checkout, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gs: branch %s is already gone here\n", branch)
		return nil
	}
	if strings.TrimSpace(tip) != candidate {
		return fmt.Errorf("the landing is complete, but branch %s is at %s, not the head that landed (%s), so it carries commits this landing did not include; nothing was deleted. Review them, then delete the branch yourself",
			branch, short(strings.TrimSpace(tip)), short(candidate))
	}
	if _, err := git(ctx, checkout, "update-ref", "-d", ref, candidate); err != nil {
		return fmt.Errorf("the landing is complete, but branch %s moved off %s while it was being deleted, so it was left alone: %w", branch, short(candidate), err)
	}
	fmt.Fprintf(os.Stderr, "gs: deleted branch %s\n", branch)
	return nil
}

// deleteLandedRemoteBranch is the same rule one network hop away, and it never
// fails the command: the landing is already complete, and what is left is a ref
// on a remote that may hold work nobody here has seen. A remote tip that is not
// the head that landed is reported and kept — deleting it would drop commits
// this repository does not have — and the deletion itself carries a lease, so a
// tip that moves between the reading and the push is rejected by the remote
// rather than overwritten.
func deleteLandedRemoteBranch(ctx context.Context, checkout, branch, candidate string) {
	ref := "refs/heads/" + branch
	tip, err := remoteBranchHead(ctx, checkout, "origin", ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gs: origin's %s could not be read (%v), so it was left alone; it may never have been pushed\n", branch, err)
		return
	}
	if tip != candidate {
		fmt.Fprintf(os.Stderr, "gs: origin's %s is at %s, not the head that landed (%s), so it carries work this landing did not include and was left alone; fetch and review it before deleting it\n",
			branch, short(tip), short(candidate))
		return
	}
	if _, err := git(ctx, checkout, "push", "origin", "--force-with-lease="+ref+":"+candidate, "--delete", ref); err != nil {
		fmt.Fprintf(os.Stderr, "gs: origin's %s was not deleted (%v); it was left as it stands\n", branch, err)
		return
	}
	fmt.Fprintf(os.Stderr, "gs: deleted branch %s on origin\n", branch)
}

// candidateBranch is the one local branch still pointing at the approved head.
// None, or more than one, is reported rather than guessed: this answer decides
// what gets deleted.
func candidateBranch(ctx context.Context, checkout, candidate, targetRef string) (string, error) {
	output, err := git(ctx, checkout, "for-each-ref", "--points-at", candidate, "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return "", err
	}
	var branches []string
	for _, name := range strings.Fields(output) {
		if "refs/heads/"+name == targetRef {
			continue
		}
		branches = append(branches, name)
	}
	if len(branches) != 1 {
		return "", fmt.Errorf("%d local branches point at %s, so which one the work was done on is not decidable", len(branches), short(candidate))
	}
	return branches[0], nil
}

// worktreeOfBranch reads `git worktree list --porcelain`, which reports one
// stanza per worktree with its branch on its own line.
func worktreeOfBranch(ctx context.Context, checkout, branch string) string {
	output, err := git(ctx, checkout, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	path := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			return path
		}
	}
	return ""
}

// isAncestor branches on the exit status rather than on failure. A non-zero
// status from `git merge-base --is-ancestor` means either "not an ancestor" or
// "the check never ran", and reading the second as the first would delete a
// branch on a measurement nobody made.
func isAncestor(ctx context.Context, checkout, commit, ref string) (bool, error) {
	if _, err := git(ctx, checkout, "merge-base", "--is-ancestor", commit, ref); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
