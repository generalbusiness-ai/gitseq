package main

import (
	"context"
	"fmt"
	"os"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// promiseCommand claims one request: one basis, signed by the addressee, once.
// Every refusal here is one the fold or the work loop makes later and more
// expensively — a promise on somebody else's request is a dangling act nobody
// can close, and a second live promise splits one commitment across two
// closures. See docs/reference/gs/promise.md.
func promiseCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("promise", arguments)
	as := set.String("as", "", "actor accepting the request")
	text := set.String("text", "", "promise text; the default names the request")
	branch := set.String("branch", "", "advisory branch this work will run on, recorded as body.branch")
	serverFlag := set.String("server", "", "resident sequencer URL")
	stepUsage(set, "gs promise [flags] <request>")
	if err := set.Parse(arguments); err != nil {
		return parseRefusal(set, err)
	}
	if set.NArg() != 1 {
		return usageErrorf(set, "promise takes exactly one request event, after the flags: gs promise [flags] <request>")
	}
	session, err := openStep(ctx, set, *repo, *as, *serverFlag)
	if err != nil {
		return err
	}
	request := set.Arg(0)
	if err := session.resolve(&request); err != nil {
		return err
	}
	statement, found := session.statement(request)
	if !found {
		return session.usage(fmt.Errorf("%s names no statement in this workroom; gs promise takes the request event, which `gs work --next` prints for every request addressed to you", short(request)))
	}
	if statement.Kind != workroom.KindRequest {
		return session.usage(fmt.Errorf("%s is a %s, not a request; a promise rests on exactly one request. `gs inspect %s` names what this event is",
			short(request), statement.Kind, short(request)))
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
	// The key a claim lands under is a function of this actor, the request,
	// and the promise it follows, so it can be recomputed here: the claim that
	// follows the newest withdrawn promise carries that promise's identifier.
	// Recomputing it is what tells a lost answer being retried, which must
	// replay, from a second closure on one commitment, which the fold cannot
	// pay off.
	withdrawn := session.latestWithdrawnPromise(request)
	key := promiseKey(session.fingerprint, request, withdrawn)
	retry := false
	if held, holds := session.statement(commitment.Promise); holds && !held.Retired && commitment.Performer == session.fingerprint {
		if !session.retrying(key, commitment.Promise) {
			return fmt.Errorf("you already hold promise %s on request %s; one commitment takes one closure. Report against that promise with `gs artifact --head <sha> --promise %s <path…>`, or withdraw it with `gs supersede %s --text '<why>'` and claim it again",
				short(commitment.Promise), short(request), short(commitment.Promise), short(commitment.Promise))
		}
		retry = true
	}
	if commitment.AddressedTo != "" && commitment.AddressedTo != session.fingerprint {
		return fmt.Errorf("request %s is addressed to %s, not to you (%s); ask %s to move it with `gs reassign-if-unclaimed %s --to %s`, or promise a request addressed to you (`gs work --next`)",
			short(request), session.name(commitment.AddressedTo), session.actor, requester, short(request), session.actor)
	}
	// "stale" and "reneged" are both still claimable: a moved basis retires
	// nothing, and the fold admits a fresh promise after a withdrawal.
	if !retry && !claimableStatus[commitment.Status] {
		return fmt.Errorf("request %s is %s, not open, so there is nothing here to claim; `gs inspect %s` shows the commitment and `gs work --next` prints what you do owe",
			short(request), commitment.Status, short(request))
	}
	// Staleness is not a refusal: only the request's author can replace it, and
	// the promise is what keeps the board honest meanwhile.
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
		RestsOn: []string{request}, IdempotencyKey: key,
	})
	if err != nil {
		return err
	}
	fmt.Println(record.ID)
	return nil
}

// claimableStatus is every lifecycle word that leaves a request to be claimed:
// never promised, promised on bases that later moved, or promised and withdrawn.
var claimableStatus = map[string]bool{"open": true, "stale": true, "reneged": true}

// promiseKey is the deterministic retry key: this actor, this request, and the
// promise this claim follows. Two runs of the same claim are the same act and
// the second replays. Claiming again after reneging is a different act, so it
// names the promise it follows and neither replays the withdrawn one nor
// collides with its key.
func promiseKey(fingerprint, request, withdrawn string) string {
	key := "gs-promise/" + fingerprint + "/" + request
	if withdrawn != "" {
		key += "/after/" + withdrawn
	}
	return key
}
