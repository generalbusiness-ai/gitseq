package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// reviewRequestCommand writes the shape gs review judges later: the request
// rests on every live artifact standing at the head — not on the promise — and
// names the reporting artifact in body.artifact, because the verdict resolves
// its lane through that field. It refuses a mixed-head set, which gs review
// refuses only at signing, after the reviewer has read everything; and it
// refuses a second live review request, because refiling releases the
// reviewer's promise and a verdict filed against the retired request binds to
// nothing. See docs/reference/gs/review-request.md.
type reviewArtifact struct {
	event    string
	path     string
	sequence int
}

func reviewRequestCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("review-request", arguments)
	as := set.String("as", "", "actor asking for the review")
	head := set.String("head", "", "the exact full commit to be reviewed")
	to := set.String("to", "", "the reviewing actor: a roster name, @name, or fingerprint")
	text := set.String("text", "", "request text; the default lists the artifacts, the promise and the governing request")
	conditions := set.String("conditions", "", "conditions of satisfaction; the default asks for an exact-head review with the three conclusions")
	replace := set.Bool("replace", false, "supersede your live review request for this promise in the same run, resting the supersession on the new request")
	serverFlag := set.String("server", "", "resident sequencer URL")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return usageErrorf(set, "review-request takes no positional arguments")
	}
	if *head == "" || *to == "" {
		return usageErrorf(set, "review-request requires --head and --to: gs review-request --head <sha> --to <actor>")
	}
	if err := requireFullCommit("--head", *head); err != nil {
		return err
	}
	session, err := openStep(ctx, *repo, *as, *serverFlag)
	if err != nil {
		return err
	}
	reviewer, err := rosterFingerprint(session, *to)
	if err != nil {
		return err
	}
	if reviewer == session.fingerprint {
		return fmt.Errorf("--to names you (%s); a review is by a different actor, and gs review refuses a verdict signed by the artifact's own author", session.actor)
	}
	standing := session.artifactsAt(*head)
	if len(standing) == 0 {
		return fmt.Errorf("no live artifact of yours stands at %s; publish the head first with `gs artifact --head %s --promise <promise> <path…>`", short(*head), *head)
	}
	promise, err := session.singlePromiseUnder(standing)
	if err != nil {
		return err
	}
	if err := session.requireOneHead(promise, *head); err != nil {
		return err
	}
	reporting := session.reportingArtifact(standing, promise)
	request, commitment := "", workroom.Commitment{}
	if promise != "" {
		commitment, _ = session.commitmentByPromise(promise)
		request = commitment.Request
		if commitment.Stale {
			fmt.Fprintln(os.Stderr, "gs:", staleWarning("governing request", request, "", session.name(commitment.Requester)))
		}
	}
	existing := session.liveReviewRequestFor(promise)
	if existing != "" && !*replace {
		return fmt.Errorf("you already have a live review request %s for promise %s; refiling cancels review work already in flight, so ask for the verdict on that request, or pass --replace to supersede it in the same run",
			short(existing), short(promise))
	}
	branch := branchAtHead(ctx, *repo, *head)
	body := map[string]string{
		"to":              reviewer,
		"conditions":      defaultConditions(*conditions, *head, reporting, len(standing)),
		"no_git_artifact": "true",
		"head":            *head,
	}
	if branch != "" {
		body["branch"] = branch
	}
	body["artifact"] = reporting.event
	bases := make([]string, 0, len(standing))
	for _, artifact := range standing {
		bases = append(bases, artifact.event)
	}
	// The key names the head, and a replacement names the request it
	// replaces as well: asking twice for a review of one head is the same
	// act and replays, while a deliberate replacement is a different one.
	key := "gs-review-request/" + session.fingerprint + "/" + *head
	if existing != "" {
		key += "/after/" + existing
	}
	acts := []batchAct{{
		Label: "request", Verb: app.VerbState, Kind: workroom.KindRequest,
		Text:           defaultRequestText(*text, *head, branch, standing, reporting, promise, request),
		Body:           body,
		RestsOn:        bases,
		IdempotencyKey: key,
	}}
	if existing != "" {
		acts = append(acts, batchAct{
			Verb: app.VerbSupersede, Target: existing,
			Text:           fmt.Sprintf("Replaced by the review request for exact head %s; any review promised on this request is released, and a verdict filed against it would bind to nothing.", *head),
			RestsOn:        []string{"$request"},
			IdempotencyKey: "gs-review-request-replace/" + session.fingerprint + "/" + existing,
		})
	}
	_, private, err := session.workspace.Actor(session.actor)
	if err != nil {
		return err
	}
	discloseBases(session.resolver, chainCitations(acts))
	filed, err := runBatch(ctx, session.workspace, session.serverURL, session.actor, private, acts, false)
	for _, act := range filed.Acts {
		if act.Event != "" {
			fmt.Println(act.Event)
		}
	}
	noteBatchDeadRestsOn(ctx, session.workspace, acts, filed)
	return err
}

// artifactsAt is every live artifact this actor has standing at one head, in
// the order they were signed.
func (s *stepSession) artifactsAt(head string) []reviewArtifact {
	var standing []reviewArtifact
	for _, artifact := range s.projection().Artifacts {
		statement, found := s.statement(artifact.Event)
		if artifact.Commit != head || artifact.Retired || !found || statement.Actor != s.fingerprint {
			continue
		}
		standing = append(standing, reviewArtifact{event: artifact.Event, path: artifact.Path, sequence: statement.Sequence})
	}
	sort.Slice(standing, func(i, j int) bool { return standing[i].sequence < standing[j].sequence })
	return standing
}

// singlePromiseUnder reads the lane out of the artifacts' own bases. Two
// promises under one head are two commitments, and one verdict cannot close
// both. Self-initiated work rests on no promise and returns an empty lane.
func (s *stepSession) singlePromiseUnder(standing []reviewArtifact) (string, error) {
	found := map[string]bool{}
	for _, artifact := range standing {
		for _, basis := range s.projection().Provenance[artifact.event] {
			if statement, ok := s.statement(basis); ok && statement.Kind == workroom.KindPromise && statement.Actor == s.fingerprint {
				found[basis] = true
			}
		}
	}
	promises := make([]string, 0, len(found))
	for promise := range found {
		promises = append(promises, promise)
	}
	sort.Strings(promises)
	switch len(promises) {
	case 0:
		return "", nil
	case 1:
		return promises[0], nil
	default:
		named := make([]string, 0, len(promises))
		for _, promise := range promises {
			named = append(named, short(promise))
		}
		return "", fmt.Errorf("the artifacts at this head rest on %d of your promises (%s); one review request answers one commitment, so publish and request review for one lane at a time",
			len(promises), strings.Join(named, ", "))
	}
}

// reportingArtifact is the newest artifact *on the promise* at this head. The
// newest of everything standing there is another artifact whenever the head
// also carries a pointer filed for another reason, and naming that one hands
// the verdict a lane it does not report.
func (s *stepSession) reportingArtifact(standing []reviewArtifact, promise string) reviewArtifact {
	if promise == "" {
		return standing[len(standing)-1]
	}
	reporting := reviewArtifact{}
	for _, artifact := range standing {
		for _, basis := range s.projection().Provenance[artifact.event] {
			if basis == promise {
				reporting = artifact
			}
		}
	}
	if reporting.event == "" {
		return standing[len(standing)-1]
	}
	return reporting
}

// requireOneHead refuses a set that does not all stand at the reviewed head.
func (s *stepSession) requireOneHead(promise, head string) error {
	if promise == "" {
		return nil
	}
	var elsewhere []string
	for _, artifact := range s.projection().Artifacts {
		statement, found := s.statement(artifact.Event)
		if artifact.Retired || artifact.Commit == head || !found || statement.Actor != s.fingerprint {
			continue
		}
		for _, basis := range s.projection().Provenance[artifact.Event] {
			if basis == promise {
				elsewhere = append(elsewhere, fmt.Sprintf("%s at %s", artifact.Path, short(artifact.Commit)))
				break
			}
		}
	}
	if len(elsewhere) == 0 {
		return nil
	}
	sort.Strings(elsewhere)
	return fmt.Errorf("promise %s also carries live artifacts at another head (%s); gs review refuses a mixed-head set, so republish every path at %s with `gs artifact --head %s --promise %s <path…>`",
		short(promise), strings.Join(elsewhere, ", "), short(head), head, short(promise))
}

// liveReviewRequestFor finds the actor's own unclosed review request for this
// lane — a request resting on any artifact of that promise — which is what a
// second one would cancel.
func (s *stepSession) liveReviewRequestFor(promise string) string {
	if promise == "" {
		return ""
	}
	lane := map[string]bool{}
	for _, artifact := range s.projection().Artifacts {
		for _, basis := range s.projection().Provenance[artifact.Event] {
			if basis == promise {
				lane[artifact.Event] = true
			}
		}
	}
	for index := len(s.projection().Commitments) - 1; index >= 0; index-- {
		commitment := s.projection().Commitments[index]
		if commitment.Requester != s.fingerprint || !unclosedRequestStatus[commitment.Status] {
			continue
		}
		for _, basis := range s.projection().Provenance[commitment.Request] {
			if lane[basis] {
				return commitment.Request
			}
		}
	}
	return ""
}

// rosterFingerprint reads the addressee out of the durable roster, so a
// misspelled name refuses here rather than addressing a request to nobody.
func rosterFingerprint(session *stepSession, reference string) (string, error) {
	name := strings.TrimPrefix(reference, "@")
	if actor, ok := session.projection().Actors[name]; ok && !actor.Retired {
		return name, nil
	}
	var names []string
	for fingerprint, actor := range session.projection().Actors {
		if actor.Retired {
			continue
		}
		names = append(names, actor.Name)
		if actor.Name == name {
			return fingerprint, nil
		}
	}
	sort.Strings(names)
	return "", fmt.Errorf("--to %s names no live actor in the durable roster; the roster holds %s", reference, strings.Join(names, ", "))
}

func defaultConditions(given, head string, reporting reviewArtifact, count int) string {
	if given != "" {
		return given
	}
	return fmt.Sprintf("Independent exact-head review of %s with explicit Architecture, Security and Simplification conclusions, filed with gs review naming %s first and resting on all %d artifacts.",
		short(head), short(reporting.event), count)
}

func defaultRequestText(given, head, branch string, standing []reviewArtifact, reporting reviewArtifact, promise, request string) string {
	if given != "" {
		return given
	}
	var out strings.Builder
	where := head
	if branch != "" {
		where += " on " + branch
	}
	fmt.Fprintf(&out, "Review the exact head %s.\n\nArtifacts standing at that head:\n", where)
	for _, artifact := range standing {
		marker := ""
		if artifact.event == reporting.event {
			marker = "  (reporting artifact; name it first)"
		}
		fmt.Fprintf(&out, "- %s %s%s\n", artifact.path, artifact.event, marker)
	}
	switch {
	case promise != "" && request != "":
		fmt.Fprintf(&out, "\nUnder promise %s on request %s.\n", promise, request)
	case promise != "":
		fmt.Fprintf(&out, "\nUnder promise %s.\n", promise)
	default:
		out.WriteString("\nSelf-initiated: no promise stands under these artifacts.\n")
	}
	return out.String()
}
