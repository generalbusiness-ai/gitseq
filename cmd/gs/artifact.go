package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// artifactCommand publishes one artifact per changed path at one head, each
// resting on exactly one promise — the actor's own — with the reporting
// artifact last, because the lane reads its report off the newest artifact on
// the promise. Publish order is therefore load-bearing, and the keys are
// derived from actor, head and path so an interrupted run resumes rather than
// publishing twice.
//
// Publishing a new head also retires the ones this promise carried at an
// earlier head, in the same signed batch. Every repair round and every recut
// onto a moved target produces that mixed set, and leaving it live meant the
// review request refused and named a repair that did not clear it, while the
// merge later sealed the remainder as a sibling or abandoned and owed the
// author a cleanup it could not do itself. See docs/reference/gs/artifact.md.
func artifactCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("artifact", arguments)
	as := set.String("as", "", "actor publishing the artifacts")
	head := set.String("head", "", "the exact full commit every artifact stands at")
	promise := set.String("promise", "", "the actor's own live promise these artifacts report")
	branch := set.String("branch", "", "branch the head is on; the default is a branch that points at it")
	report := set.String("report", "", "which path carries the reporting artifact; the default is the first path")
	text := set.String("text", "", "extra text for the reporting artifact")
	var extra values
	set.Var(&extra, "rests-on", "an extra basis for every artifact, such as the behaviour a documentation page describes (repeatable)")
	serverFlag := set.String("server", "", "resident sequencer URL")
	stepUsage(set, "gs artifact [flags] <path…>")
	if err := set.Parse(arguments); err != nil {
		return parseRefusal(set, err)
	}
	paths := set.Args()
	if *head == "" || *promise == "" || len(paths) == 0 {
		return usageErrorf(set, "artifact requires --head, --promise and at least one path, with the paths after the flags: gs artifact --head <sha> --promise <promise> <path…>")
	}
	if err := requireFullCommit("--head", *head); err != nil {
		return usageReferenceError(set, err)
	}
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			return fmt.Errorf("path %q is named twice; publish one artifact per exact path", path)
		}
		seen[path] = true
	}
	reportPath := paths[0]
	if *report != "" {
		if !seen[*report] {
			return usageErrorf(set, "--report %s is not one of the paths given; the reporting artifact is one of the artifacts this head publishes", *report)
		}
		reportPath = *report
	}
	session, err := openStep(ctx, set, *repo, *as, *serverFlag)
	if err != nil {
		return err
	}
	bases := []string(extra)
	if err := resolveRefs(session.resolver, []*string{promise}, &bases); err != nil {
		return session.usage(err)
	}
	showResolved(session.resolver)
	if err := validateArtifactCommit(ctx, *repo, *head); err != nil {
		return fmt.Errorf("--head %s: %w; fetch the commit into %s, or name the head this work actually produced", *head, err, *repo)
	}
	commitment, err := requireOwnLivePromise(session, *promise)
	if err != nil {
		return err
	}
	if err := refuseSecondPromise(session, *promise, bases); err != nil {
		return err
	}
	if *branch == "" {
		*branch = branchAtHead(ctx, *repo, *head)
	}
	if err := requireChangedPaths(ctx, *repo, *head, commitment, paths); err != nil {
		return err
	}
	if commitment.Stale {
		fmt.Fprintf(os.Stderr, "note: promise %s is stale; the artifacts are admitted and record their stale bases, and ordinary staleness is not a reason to replace a promise\n", short(*promise))
	}
	acts := artifactActs(session.fingerprint, *head, *branch, *promise, commitment.Request, reportPath, *text, paths, bases)
	retiring := session.keepUncited(retirements(session.laneArtifactsElsewhere(*promise, *head), paths))
	acts = append(acts, retirementActs(session.fingerprint, *head, retiring)...)
	_, private, err := session.workspace.Actor(session.actor)
	if err != nil {
		return err
	}
	discloseBases(session.resolver, chainCitations(acts))
	published, err := runBatch(ctx, session.workspace, session.serverURL, session.actor, private, acts, false)
	// The artifacts are printed as bare identifiers, the retirements as the
	// lines below, so the last identifier on its own line is still the
	// reporting artifact whether or not this run withdrew anything.
	for _, act := range published.Acts[:len(acts)-len(retiring)] {
		if act.Event != "" {
			fmt.Println(act.Event)
		}
	}
	printRetirements(retiring, published, len(acts)-len(retiring))
	noteBatchDeadRestsOn(ctx, session.workspace, acts, published)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gs: reporting artifact at %s is the newest artifact on promise %s; name it first when you ask for review\n", reportPath, short(*promise))
	return nil
}

// requireOwnLivePromise is the basis rule, checked before anything is signed:
// one promise, the signer's own, live. Anything else closes nothing.
func requireOwnLivePromise(session *stepSession, promise string) (workroom.Commitment, error) {
	statement, found := session.statement(promise)
	if !found {
		return workroom.Commitment{}, session.usage(fmt.Errorf("--promise %s names no statement in this workroom; claim the request with `gs promise <request>` first", short(promise)))
	}
	if statement.Kind != workroom.KindPromise {
		return workroom.Commitment{}, session.usage(fmt.Errorf("--promise %s is a %s, not a promise; an artifact reports exactly one promise. `gs work --next` prints the promise for each row you owe",
			short(promise), statement.Kind))
	}
	if statement.Actor != session.fingerprint {
		return workroom.Commitment{}, fmt.Errorf("--promise %s was signed by %s, not by you (%s); an artifact must rest on your own promise, so file one with `gs promise <request>`",
			short(promise), session.name(statement.Actor), session.actor)
	}
	if statement.Retired {
		return workroom.Commitment{}, fmt.Errorf("--promise %s is retired, so it closes nothing; file a fresh promise with `gs promise <request>`", short(promise))
	}
	commitment, live := session.commitmentByPromise(promise)
	if !live {
		return workroom.Commitment{}, fmt.Errorf("--promise %s rests on no request, so it projects dangling and nobody can close it; promise the request itself with `gs promise <request>`", short(promise))
	}
	return commitment, nil
}

// refuseSecondPromise closes the one way a second promise could still get in.
// --rests-on carries the behaviour a page describes or the decision the work
// adopts, not another commitment.
func refuseSecondPromise(session *stepSession, promise string, extra []string) error {
	for _, basis := range extra {
		if basis == promise {
			return fmt.Errorf("--rests-on %s is the promise these artifacts already report; it is named once, by --promise", short(basis))
		}
		statement, found := session.statement(basis)
		if found && statement.Kind == workroom.KindPromise {
			return fmt.Errorf("--rests-on %s is a promise, and an artifact resting on two promises closes neither; publish this head under one promise at a time",
				short(basis))
		}
	}
	return nil
}

// requireChangedPaths compares the paths named against what this head changes
// against the request's target. A path the head did not change is refused, and
// a changed path no artifact names is a warning: a first artifact elsewhere is
// legitimate and only the author knows which. A directory path covers the files
// under it; everything else matches as an exact string, as the fold does.
func requireChangedPaths(ctx context.Context, repo, head string, commitment workroom.Commitment, paths []string) error {
	if commitment.TargetRef == "" {
		fmt.Fprintf(os.Stderr, "note: request %s states no target ref, so the paths this head changes cannot be measured; check them yourself\n", short(commitment.Request))
		return nil
	}
	if _, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", commitment.TargetRef+"^{commit}"); err != nil {
		return fmt.Errorf("%s, where request %s says this work is owed, is not in %s, so the paths this head changes cannot be measured; fetch it (`git -C %s fetch origin %s`) and run this again",
			commitment.TargetRef, short(commitment.Request), repo, repo, strings.TrimPrefix(commitment.TargetRef, "refs/heads/"))
	}
	base, err := git(ctx, repo, "merge-base", "--end-of-options", commitment.TargetRef, head)
	if err != nil {
		return fmt.Errorf("%s and %s share no common ancestor in %s, so what this head changes against the target cannot be measured; rebase or recut the work onto %s",
			commitment.TargetRef, short(head), repo, commitment.TargetRef)
	}
	changes, err := mergeChangesBetween(ctx, repo, strings.TrimSpace(base), head)
	if err != nil {
		return err
	}
	changed := mergeChangedPaths(changes)
	for _, path := range paths {
		if !coversAny(path, changed) {
			return fmt.Errorf("path %q is not changed by %s against %s; staleness travels along paths, so an artifact there could never flare. Name a path this head changes (`git diff --name-only %s %s`), or fix --head",
				path, short(head), commitment.TargetRef, strings.TrimSpace(base), head)
		}
	}
	var unnamed []string
	for _, path := range changed {
		if !coveredBy(path, paths) {
			unnamed = append(unnamed, path)
		}
	}
	if len(unnamed) > 0 {
		shown, omitted := unnamed, 0
		if len(shown) > 8 {
			shown, omitted = shown[:8], len(unnamed)-8
		}
		more := ""
		if omitted > 0 {
			more = fmt.Sprintf(" and %d more", omitted)
		}
		fmt.Fprintf(os.Stderr, "warning: %s also changes %s%s, which no artifact here names; publish each changed path, or be able to say why not\n",
			short(head), strings.Join(shown, ", "), more)
	}
	return nil
}

// coversAny reports whether a named path is one of the changed paths, or the
// directory holding one of them.
func coversAny(path string, changed []string) bool {
	for _, candidate := range changed {
		if candidate == path || strings.HasPrefix(candidate, path+"/") {
			return true
		}
	}
	return false
}

func coveredBy(changed string, paths []string) bool {
	for _, path := range paths {
		if changed == path || strings.HasPrefix(changed, path+"/") {
			return true
		}
	}
	return false
}

// artifactActs orders the chain: every other path first, the reporting
// artifact last, because that one is what a review request names. Each act
// carries the label its path's retirement cites as the successor, so a batch
// label is enough to say where a withdrawn pointer went and no second run is
// needed to learn the new artifact's identifier.
func artifactActs(fingerprint, head, branch, promise, request, reportPath, reportText string, paths, extra []string) []batchAct {
	acts := make([]batchAct, 0, len(paths))
	add := func(path string) {
		text := artifactText(path, head, branch, promise, request)
		if path == reportPath && reportText != "" {
			text += "\n\n" + reportText
		}
		bases := append([]string{promise}, extra...)
		acts = append(acts, batchAct{
			Label: pathLabel(path, paths), Verb: app.VerbState, Kind: workroom.KindArtifact, Text: text,
			Body:           map[string]string{"path": path, "commit": head},
			RestsOn:        bases,
			IdempotencyKey: artifactKey(fingerprint, head, path),
		})
	}
	for _, path := range paths {
		if path != reportPath {
			add(path)
		}
	}
	add(reportPath)
	return acts
}

// pathLabel names one published path inside this batch. The paths are already
// proved unique, and the index is stable whatever order the acts are emitted
// in, so no label can collide with another.
func pathLabel(path string, paths []string) string {
	for index, candidate := range paths {
		if candidate == path {
			return fmt.Sprintf("artifact-%d", index)
		}
	}
	return ""
}

// retirement is one earlier-head pointer this publication withdraws, together
// with where its behaviour went: the batch label of the new artifact covering
// the same path, or nothing at all when this head no longer changes it.
//
// A bare retirement is the honest answer there. It condemns what rested on the
// pointer, which is what an author wants to hear about a path the head under
// review does not touch; carrying it to an unrelated artifact would say the
// behaviour moved when it did not.
type retirement struct {
	target    string
	path      string
	commit    string
	successor string
}

// retirements pairs each earlier-head artifact of this lane with the new
// artifact that succeeds it. A path published again succeeds itself; a path
// now covered by a published directory succeeds through that directory,
// exactly as the fold reads succession.
func retirements(elsewhere []workroom.Artifact, paths []string) []retirement {
	if len(elsewhere) == 0 {
		return nil
	}
	withdrawn := make([]retirement, 0, len(elsewhere))
	for _, artifact := range elsewhere {
		entry := retirement{target: artifact.Event, path: artifact.Path, commit: artifact.Commit}
		for _, path := range paths {
			if path == artifact.Path || strings.HasPrefix(artifact.Path, path+"/") {
				entry.successor = pathLabel(path, paths)
				break
			}
		}
		withdrawn = append(withdrawn, entry)
	}
	return withdrawn
}

// keepUncited drops the retirements the citation guard refuses, and says which
// and why. Letting one of them refuse the whole publication would be a trap:
// the guard asks the author to repoint the page at the successor, and the
// successor is the artifact this very run would publish, so there is nothing
// yet to repoint at. Skipping leaves the lane mixed, which gs review-request
// refuses and names the repair for once the successor exists.
//
// Skipping is also the safe direction when the lookup itself did not run: the
// old pointer simply stays live, and nothing is retired on an unanswered
// question.
func (s *stepSession) keepUncited(withdrawn []retirement) []retirement {
	kept := make([]retirement, 0, len(withdrawn))
	for _, entry := range withdrawn {
		if err := s.workspace.RefuseCitedRetirement(s.ctx, entry.target, false); err != nil {
			fmt.Fprintf(os.Stderr, "warning: the artifact for %s at %s stays live, so this promise still carries two heads: %v\n",
				entry.path, short(entry.commit), err)
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// retirementActs is the suffix that makes one head the only live one on this
// promise. Every act here retires this actor's own artifact, which the fold
// admits on its own standing, so this adds no rule and claims no authority
// over anybody else's pointer.
//
// The key names the head being published, so a run interrupted after the
// artifacts landed retires on the next attempt rather than leaving the lane
// mixed, and a rerun of a completed publication finds nothing left to retire.
func retirementActs(fingerprint, head string, withdrawn []retirement) []batchAct {
	acts := make([]batchAct, 0, len(withdrawn))
	for _, entry := range withdrawn {
		var rests []string
		text := fmt.Sprintf("Republished at %s, which no longer changes %s: the pointer is withdrawn with no successor.", head, entry.path)
		if entry.successor != "" {
			rests = []string{"$" + entry.successor}
			text = fmt.Sprintf("Republished at %s; the successor stands at %s.", head, entry.path)
		}
		acts = append(acts, batchAct{
			Verb: app.VerbSupersede, Target: entry.target, Text: text, RestsOn: rests,
			IdempotencyKey: retirementKey(fingerprint, head, entry.target),
		})
	}
	return acts
}

// printRetirements says what the publication withdrew, reading the successor's
// identifier out of what the batch actually minted rather than out of the plan:
// an act that did not land is not reported as one that did.
func printRetirements(withdrawn []retirement, published batchReport, offset int) {
	minted := make(map[string]string, len(published.Acts))
	for _, act := range published.Acts {
		if act.Label != "" && act.Event != "" {
			minted[act.Label] = act.Event
		}
	}
	for index, entry := range withdrawn {
		position := offset + index
		if position >= len(published.Acts) || published.Acts[position].Event == "" {
			continue
		}
		successor := "bare"
		if event := minted[entry.successor]; event != "" {
			successor = short(event)
		}
		fmt.Printf("retired %s at %s -> %s\n", entry.path, short(entry.commit), successor)
	}
}

func artifactText(path, head, branch, promise, request string) string {
	where := "at exact " + head
	if branch != "" {
		where += " on " + branch
	}
	return fmt.Sprintf("%s %s, under promise %s on request %s", path, where, short(promise), short(request))
}

func artifactKey(fingerprint, head, path string) string {
	return "gs-artifact/" + fingerprint + "/" + head + "/" + path
}

func retirementKey(fingerprint, head, target string) string {
	return "gs-artifact-retire/" + fingerprint + "/" + head + "/" + target
}

// branchAtHead names the branch a head sits on when exactly one does. Nothing
// depends on the answer, so an ambiguous one is simply omitted.
func branchAtHead(ctx context.Context, repo, head string) string {
	output, err := git(ctx, repo, "for-each-ref", "--points-at", head, "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return ""
	}
	names := strings.Fields(output)
	if len(names) != 1 {
		return ""
	}
	return names[0]
}
