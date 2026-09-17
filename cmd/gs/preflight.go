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

// noPreflightFlag names the one escape from the pre-signing fold check, for the
// author who means to file a shape the fold refuses. It changes nothing about
// what an admitted act contains.
const noPreflightFlag = "no-preflight"

// refuseIneffectiveAct asks the fold what it would decide about this act and
// refuses when the answer is not effective. It runs after every reference is
// resolved and before the act is built: no key, no log, no resident touched
// yet. The reason printed is the fold's, word for word; the fix line beneath it
// is all this boundary adds.
func refuseIneffectiveAct(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, skip bool, act app.Act) error {
	filer, ok := preflightFiler(ctx, workspace, serverURL, actorName, skip)
	if !ok {
		return nil
	}
	decision, judged := workspace.PreflightAct(ctx, filer, act)
	if !judged || decision.Verdict == workroom.Effective {
		return nil
	}
	return ineffectiveActError(ctx, workspace, act, decision)
}

// preflightFiler names who the act would be signed as, and reports whether the
// question may be put at all: not when the caller turned the check off, not when
// this checkout cannot name the actor, and not when the resident stands
// somewhere this checkout cannot see.
func preflightFiler(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, skip bool) (app.Filer, bool) {
	if skip {
		return app.Filer{}, false
	}
	actor, err := workspace.ResolveActor(actorName)
	if err != nil {
		return app.Filer{}, false
	}
	if !preflightWorldIsCurrent(ctx, workspace, serverURL) {
		return app.Filer{}, false
	}
	return app.Filer{Name: actorName, Fingerprint: actor.Fingerprint}, true
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
// world the act would join. A local act joins this checkout's log; an act
// submitted to a resident joins that resident's frontier, and a checkout behind
// it would refuse acts the fold admits. So the resident is asked where it
// stands, and anything but exact agreement leaves the judgement to the fold.
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

// preflightFixes maps a fold reason to the one line that says what would make
// that act land. Every key is a reason string internal/workroom writes, and a
// test holds it to that, because a reason the fold rewords would otherwise stop
// matching and take its advice with it. A value carrying %s takes one argument
// from fixArgument.
var preflightFixes = map[string]string{
	"actor may not supersede target":                                "only %s may retire it; ask one of them to file the supersede",
	"supersede target is unknown":                                   "name the target by its full event id, as a positional argument; gs work and gs inspect print the ids this workroom holds",
	"ratify target is unknown":                                      "name the target by its full event id, as a positional argument; gs work and gs inspect print the ids this workroom holds",
	"ratify target is not effective":                                "the fold refused that record, so nothing stands there to ratify; gs inspect %s says why",
	"retired statement cannot be ratified":                          "that record was retired; ratify its successor instead",
	"statement kind is not ratifiable":                              "that kind has no satisfier: an artifact is closed by an approved merge, a request by a promise, report or supersession",
	"only statements may be ratified":                               "ratify names a statement, not a ratification or a supersession",
	"ratify must rest on exactly its target":                        "gs ratify builds its own basis; pass the target as the positional argument and no --rests-on",
	"only the requester may declare satisfaction":                   "the originating requester ratifies this report; ask them to file it",
	"dangling promise has no request":                               "rest the promise on the request it claims: --rests-on <request-event>",
	"report has no promise or request":                              "rest the report on your promise, or on the request when you made no promise: --rests-on <event>",
	"report cites a request other than the one its promise answers": "cite only the request your promise rests on, or drop the request and cite the promise alone",
	"only the promisor may report completion":                       "the actor who promised reports on it; report on your own promise or against the request",
	"only the requested performer may report directly on a request": "only the addressee may report straight against a request; file a promise first, or ask the addressee to report",
	"promise actor is not the requested performer":                  "only the actor named in body.to may promise this request; ask the requester to reassign it",
	"requested performer is not in the live roster":                 "address the request to a live actor: --body to=<actor name>, as gs actors lists them",
	"participant roster state requires body.kind":                   "a participant roster state names what the actor is: --body kind=human or --body kind=agent",
}

// preflightFixPatterns are the reason fragments the table below matches by
// shape rather than by the whole string, because the fold builds those reasons
// around a field, a role or an event. The same test holds these to the fold
// source.
var preflightFixPatterns = []string{
	" state requires body.",
	"actor lacks ",
	"report rests on the request while promise ",
	"undefined kind ",
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
	if strings.HasPrefix(reason, "report rests on the request while promise ") {
		return "report on that promise instead, or supersede it first"
	}
	if strings.HasPrefix(reason, "undefined kind ") {
		return "gs status prints the kinds this workroom defines; a new one needs a ratified kind-def"
	}
	fix, known := preflightFixes[reason]
	if !known {
		return ""
	}
	if !strings.Contains(fix, "%s") {
		return fix
	}
	return fmt.Sprintf(fix, fixArgument(ctx, workspace, act, reason))
}

// fixArgument is the one value a fix line names: who may retire this target, or
// the target to go and read.
func fixArgument(ctx context.Context, workspace *app.Workspace, act app.Act, reason string) string {
	if reason == "actor may not supersede target" {
		return supersedeAuthority(ctx, workspace, act.Target)
	}
	return act.Target
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
// first one is appended: half a chain in the log is worse than none of it.
//
// Labels included. An act citing a `$label` names an act the chain has yet to
// mint, so each label is resolved to the identifier its act will be judged
// under and the chain is folded in order: act two is judged against the world
// act one would make. Skipping labeled acts left the commonest chain shape
// unchecked.
func refuseIneffectiveBatch(ctx context.Context, workspace *app.Workspace, serverURL, actorName string, skip bool, acts []batchAct) error {
	filer, ok := preflightFiler(ctx, workspace, serverURL, actorName, skip)
	if !ok {
		return nil
	}
	chain, ok := prospectiveChain(ctx, workspace, filer, acts)
	if !ok {
		return nil
	}
	position, decision, refused := workspace.PreflightChain(ctx, filer, chain)
	if !refused {
		return nil
	}
	return fmt.Errorf("act %d: %w", position, ineffectiveActError(ctx, workspace, chain[position].Act, decision))
}

// prospectiveChain is the chain as the fold would read it: every act built the
// way submission builds it, and every `$label` replaced by the identifier the
// act it names will be judged under.
//
// Which identifier that is depends on the act. An act whose idempotency key this
// actor already holds is one the log has, so its label names that real event and
// the acts after it are judged against it; anything else is judged under a
// prospective identifier no log can hold. A retry of a chain whose prefix landed
// is therefore judged as what it is: the prefix left to the sequencer, the new
// suffix judged against the world that prefix made.
//
// A label naming no act of this chain leaves an empty reference behind, and the
// chain is left to the fold; checkBatch has already refused that shape.
func prospectiveChain(ctx context.Context, workspace *app.Workspace, filer app.Filer, acts []batchAct) ([]app.ChainAct, bool) {
	chain := make([]app.ChainAct, len(acts))
	minted := make(map[string]string, len(acts))
	for position, entry := range acts {
		event, replayed := workspace.AcceptedUnderKey(ctx, filer, entry.IdempotencyKey)
		if !replayed {
			event = workspace.ProspectiveEventID(position)
		}
		chain[position] = app.ChainAct{Event: event, Replayed: replayed}
		if entry.Label != "" {
			minted[entry.Label] = event
		}
	}
	for position, entry := range acts {
		act := resolveBatchAct(entry, minted, false)
		if unresolvedReference(entry, act) {
			return nil, false
		}
		chain[position].Act = act
	}
	return chain, true
}

// unresolvedReference reports whether label resolution left one of this act's
// references empty, which is what a label no act of the chain defines leaves.
func unresolvedReference(entry batchAct, act app.Act) bool {
	for _, reference := range act.RestsOn {
		if reference == "" {
			return true
		}
	}
	return (entry.Target != "" && act.Target == "") || (entry.Retirement != "" && act.Retirement == "")
}
