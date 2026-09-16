package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// promiseCommand claims one request. A promise is the board's only sign that
// work is in flight, and its durable shape is fixed: exactly one request as
// its basis, signed by the actor that request is addressed to, once.
//
// Everything it refuses, the fold or the work loop refuses later and more
// expensively: a promise on somebody else's request is a dangling act nobody
// can close, a second promise on a request you already hold splits one
// commitment across two closures, and a promise on a closed request claims
// work that is not owed.
//
// The idempotency key is derived from the actor and the request, so a retry
// after a lost answer replays the promise already recorded instead of filing
// a second one.
func promiseCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("promise", arguments)
	as := set.String("as", "", "actor accepting the request")
	text := set.String("text", "", "promise text; the default names the request")
	branch := set.String("branch", "", "advisory branch this work will run on, recorded as body.branch")
	serverFlag := set.String("server", "", "resident sequencer URL")
	stepUsage(set, "gs promise [flags] <request>")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("promise takes exactly one request event, after the flags: gs promise [flags] <request>")
	}
	session, err := openStep(ctx, *repo, *as, *serverFlag)
	if err != nil {
		return err
	}
	request := set.Arg(0)
	if err := session.resolve(&request); err != nil {
		return err
	}
	statement, found := session.statement(request)
	if !found {
		return fmt.Errorf("%s names no statement in this workroom; gs promise takes the request event, which `gs work --next` prints for every request addressed to you", short(request))
	}
	if statement.Kind != workroom.KindRequest {
		return fmt.Errorf("%s is a %s, not a request; a promise rests on exactly one request. `gs inspect %s` names what this event is",
			short(request), statement.Kind, short(request))
	}
	if statement.Retired {
		return fmt.Errorf("request %s is retired, so nothing can be promised on it; ask %s for a fresh request, or run `gs work --next`",
			short(request), session.name(statement.Actor))
	}
	commitment, live := session.commitmentByRequest(request)
	if !live {
		return fmt.Errorf("request %s has no commitment row, so the fold did not admit it as a request; `gs inspect %s` shows its decision, and %s must refile it",
			short(request), short(request), session.name(statement.Actor))
	}
	requester := session.name(commitment.Requester)
	// A promise this actor withdrew still names them as the performer, so the
	// row is read for a *live* promise: reneging is visible forever, but it
	// is not a promise anybody is still holding.
	if held, ok := session.statement(commitment.Promise); ok && !held.Retired && commitment.Performer == session.fingerprint {
		return fmt.Errorf("you already hold promise %s on request %s; one commitment takes one closure. Report against that promise with `gs artifact --head <sha> --promise %s <path…>`, or withdraw it with `gs supersede %s --text '<why>'`",
			short(commitment.Promise), short(request), short(commitment.Promise), short(commitment.Promise))
	}
	if commitment.AddressedTo != "" && commitment.AddressedTo != session.fingerprint {
		return fmt.Errorf("request %s is addressed to %s, not to you (%s); ask %s to move it with `gs reassign-if-unclaimed %s --to %s`, or promise a request addressed to you (`gs work --next`)",
			short(request), session.name(commitment.AddressedTo), session.actor, requester, short(request), session.actor)
	}
	// "stale" is the lifecycle word an unclaimed request wears when a basis
	// under it was retired. It is still unclaimed, and still yours to answer;
	// the warning below is the answer AGENTS.md step 1 asks for.
	if commitment.Status != "open" && commitment.Status != "stale" {
		return fmt.Errorf("request %s is %s, not open, so there is nothing here to claim; `gs inspect %s` shows the commitment and `gs work --next` prints what you do owe",
			short(request), commitment.Status, short(request))
	}
	// Staleness is not a refusal. The reasoning under the request moved; the
	// conditions did not, and only its author can replace it. Say so and file
	// the promise, which is what keeps the board honest in the meantime.
	if commitment.Stale || statement.Stale {
		fmt.Fprintln(os.Stderr, "gs:", staleWarning("request", request, statement.StaleBecause, requester))
	}
	promiseText := *text
	if promiseText == "" {
		promiseText = fmt.Sprintf("Accept request %s: %s", short(request), oneLine(statement.Text, 160))
	}
	body := map[string]string{}
	if *branch != "" {
		body["branch"] = *branch
	}
	record, err := session.submit(app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: promiseText, Body: body,
		RestsOn: []string{request}, IdempotencyKey: promiseKey(session.fingerprint, request),
	})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

// promiseKey is the deterministic retry key. Two runs of the same claim by the
// same actor are the same act, so the second replays rather than filing a
// second promise on one request.
func promiseKey(fingerprint, request string) string {
	return "gs-promise/" + fingerprint + "/" + request
}
