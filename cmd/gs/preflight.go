package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// noPreflightFlag names the one escape from the pre-signing fold check. An act
// the fold would refuse is sometimes exactly the act an author means to file —
// replaying a shape the log already holds under a key, or recording an attempt
// deliberately — and the check is a courtesy, not a rule. It is one flag, it
// changes nothing about what an admitted act contains, and the fold decides
// either way.
const noPreflightFlag = "no-preflight"

// refuseIneffectiveAct asks the fold what it would decide about this act and
// refuses before a signing key is read when the answer is not effective. It is
// called after every reference is resolved and before the act is built, which
// is the last moment at which nothing has been touched: no key, no log, no
// resident.
//
// It never invents a refusal of its own. The reason printed is the fold's, word
// for word, and the fix line beneath it is this boundary's only addition. When
// the question cannot be put — an unfoldable world, a resident standing
// somewhere this checkout cannot see, an act shape this boundary does not build
// — it says nothing and the act goes to the fold as before.
func refuseIneffectiveAct(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, skip bool, act app.Act) error {
	if skip {
		return nil
	}
	actor, err := workspace.ResolveActor(actorName)
	if err != nil {
		return nil
	}
	if !preflightWorldIsCurrent(ctx, workspace, serverURL) {
		return nil
	}
	decision, judged := workspace.PreflightAct(ctx, actor.Fingerprint, act)
	if !judged || decision.Verdict == workroom.Effective {
		return nil
	}
	return ineffectiveActError(ctx, workspace, act, decision)
}

// ineffectiveActError renders one refusal: the fold's verdict and reason, the
// one line that says what to do about it, and the flag that files the act
// anyway.
func ineffectiveActError(ctx context.Context, workspace *app.Workspace, act app.Act, decision workroom.Decision) error {
	message := fmt.Sprintf("the fold would rule this act %s: %s", decision.Verdict, decision.Reason)
	if fix := preflightFix(ctx, workspace, act, decision.Reason); fix != "" {
		message += "\nfix: " + fix
	}
	return fmt.Errorf("%s\nfile it as written with --%s", message, noPreflightFlag)
}

// preflightWorldIsCurrent reports whether the world this process folded is the
// world the act would join. A local act always joins the log this checkout
// holds. An act submitted to a resident joins the resident's frontier, and a
// checkout that has not caught up with it would refuse acts the fold admits —
// the one failure this whole mechanism must not introduce. So the resident is
// asked where it stands, cheaply, and anything but exact agreement leaves the
// judgement to the fold.
func preflightWorldIsCurrent(ctx context.Context, workspace *app.Workspace, serverURL string) bool {
	if serverURL == "" {
		return true
	}
	var summary service.SummaryStatus
	if err := residentclient.New(2*time.Second).GetJSON(ctx, serverURL, "/v0/status-summary", summaryResponseLimit, &summary); err != nil {
		return false
	}
	return validateRemoteFrontier(ctx, workspace, summary.Durable.Genesis, summary.Durable.Head) == nil
}

// preflightFix is one line saying what would make this act land. The mapping is
// from the fold's own reason, so a reason nobody has written a line for prints
// alone rather than guessing.
func preflightFix(ctx context.Context, workspace *app.Workspace, act app.Act, reason string) string {
	if field, ok := missingBodyField(reason); ok {
		switch field {
		case "path":
			return "an artifact names one file: --body path=<file>, at the exact string the merge will publish"
		case "commit":
			return "an artifact names the exact head: --body commit=<full sha>"
		case "conditions":
			return "a request states what satisfies it: --body conditions=<conditions of satisfaction>"
		case "to":
			return "a request names its addressee: --body to=<actor name>"
		default:
			return "supply it: --body " + field + "=<value>"
		}
	}
	if role, ok := strings.CutPrefix(reason, "actor lacks "); ok {
		role = strings.TrimSuffix(role, " role")
		return "ask an actor holding " + role + " to file this act"
	}
	switch reason {
	case "actor may not supersede target":
		return "only " + supersedeAuthority(ctx, workspace, act.Target) + " may retire it; ask one of them to file the supersede"
	case "supersede target is unknown", "ratify target is unknown":
		return "name the target by its full event id, as a positional argument; gs work and gs inspect print the ids this workroom holds"
	case "ratify target is not effective":
		return "the fold refused that record, so nothing stands there to ratify; gs inspect " + act.Target + " says why"
	case "retired statement cannot be ratified":
		return "that record was retired; ratify its successor instead"
	case "statement kind is not ratifiable":
		return "that kind has no satisfier: an artifact is closed by an approved merge, a request by a promise, report or supersession"
	case "only statements may be ratified":
		return "ratify names a statement, not a ratification or a supersession"
	case "ratify must rest on exactly its target":
		return "gs ratify builds its own basis; pass the target as the positional argument and no --rests-on"
	case "only the requester may declare satisfaction":
		return "the originating requester ratifies this report; ask them to file it"
	case "dangling promise has no request":
		return "rest the promise on the request it claims: --rests-on <request-event>"
	case "report has no promise or request":
		return "rest the report on your promise, or on the request when you made no promise: --rests-on <event>"
	case "report cites a request other than the one its promise answers":
		return "cite only the request your promise rests on, or drop the request and cite the promise alone"
	case "only the promisor may report completion":
		return "the actor who promised reports on it; report on your own promise or against the request"
	case "only the requested performer may report directly on a request":
		return "only the addressee may report straight against a request; file a promise first, or ask the addressee to report"
	case "promise actor is not the requested performer":
		return "only the actor named in body.to may promise this request; ask the requester to reassign it"
	case "requested performer is not in the live roster":
		return "address the request to a live actor: --body to=<actor name>, as gs actors lists them"
	case "participant roster state requires body.kind":
		return "a participant roster state names what the actor is: --body kind=human or --body kind=agent"
	}
	if strings.HasPrefix(reason, "report rests on the request while promise ") {
		return "report on that promise instead, or supersede it first"
	}
	if strings.HasPrefix(reason, "undefined kind ") {
		return "gs status prints the kinds this workroom defines; a new one needs a ratified kind-def"
	}
	return ""
}

// missingBodyField reads the field out of the fold's missing-field reason,
// which is written as "<kind> state requires body.<field>".
func missingBodyField(reason string) (string, bool) {
	_, field, found := strings.Cut(reason, " state requires body.")
	if !found || field == "" || strings.Contains(field, " ") {
		return "", false
	}
	return field, true
}

// supersedeAuthority names who may retire this target here: its author, and any
// actor holding ratifier. The author is named from the projection when this
// checkout can put a name to the fingerprint that signed it.
func supersedeAuthority(ctx context.Context, workspace *app.Workspace, target string) string {
	author := "its author"
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		return author + " or an actor holding ratifier"
	}
	name := func(fingerprint string) {
		if state, known := snapshot.Projection.Actors[fingerprint]; known && state.Name != "" {
			author = state.Name
		}
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == target {
			name(statement.Actor)
			return author + " or an actor holding ratifier"
		}
	}
	for _, act := range snapshot.Projection.Acts {
		if act.Event == target {
			name(act.Actor)
			break
		}
	}
	return author + " or an actor holding ratifier"
}

// refuseIneffectiveBatch asks the fold about every act of a chain before the
// first one is appended, for the same reason the chain is read and resolved
// whole: a batch that cannot land cleanly should land nothing, and half a chain
// in the log is worse than none of it.
//
// An act that cites a label names an act this batch has yet to mint. The fold
// cannot be asked about a world that does not exist yet, so those acts are left
// to it, exactly as they were before. Every other act is judged against the
// world as it stands now, which is the world the first act of the chain will
// join.
func refuseIneffectiveBatch(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, skip bool, acts []batchAct) error {
	if skip {
		return nil
	}
	actor, err := workspace.ResolveActor(actorName)
	if err != nil {
		return nil
	}
	if !preflightWorldIsCurrent(ctx, workspace, serverURL) {
		return nil
	}
	for position, entry := range acts {
		if citesUnmintedAct(entry) {
			continue
		}
		act := resolveBatchAct(entry, nil, false)
		decision, judged := workspace.PreflightAct(ctx, actor.Fingerprint, act)
		if !judged || decision.Verdict == workroom.Effective {
			continue
		}
		return fmt.Errorf("act %d: %w", position, ineffectiveActError(ctx, workspace, act, decision))
	}
	return nil
}

// citesUnmintedAct reports whether this act names another act of the same
// chain, which has no identifier until the chain runs.
func citesUnmintedAct(entry batchAct) bool {
	if isBatchLabel(entry.Target) || isBatchLabel(entry.Retirement) {
		return true
	}
	for _, basis := range entry.RestsOn {
		if isBatchLabel(basis) {
			return true
		}
	}
	return false
}
