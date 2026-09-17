package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
)

// `gs wait` blocks until something this actor can act on has changed, then
// prints it the way `gs work --next` prints it. What it does and why is
// docs/reference/gs/wait.md; what is here is how, and the reasons the code
// could not be read off the page.
const (
	// residentPollCap is the longest single poll worth asking for: the
	// resident's own bound is 30 seconds and it shortens anything longer to
	// 25, so asking for more only hides the loop's own clock.
	residentPollCap = 25 * time.Second
	// waitResponseLimit bounds one actor-wait answer. The delta is capped by
	// statusview on the resident side, so this is a transport backstop.
	waitResponseLimit = 8 << 20
	// waitLeaseTTL is the presence lease this command asks for. The resident
	// refuses anything above two minutes and treats a missing value as thirty
	// seconds; a minute leaves room for a renewal to be late without the
	// session lapsing mid-poll.
	waitLeaseTTL = time.Minute
	// waitRenewEvery is how often the lease is renewed between polls. It is
	// well inside the TTL because a renewal can only happen between polls, and
	// a poll may run for the resident's whole cap.
	waitRenewEvery = 20 * time.Second
	// localWatchInterval is how often the local fallback reads the sequence
	// ref. Reading a ref is one cheap Git call; folding the log is not, and
	// only a ref that moved buys one.
	localWatchInterval = 2 * time.Second
	// residentBackoff is the pause before asking again after a refusal the
	// resident invited: its wait budget is full, or the socket did not answer.
	// Short, because the caller is already inside its own deadline.
	residentBackoff = 250 * time.Millisecond
	// residentTransportRetries is how many unanswered polls to absorb before
	// deciding the resident is gone and watching the sequence ref instead. A
	// resident restart is the ordinary cause and takes longer than one retry.
	residentTransportRetries = 2
	// maxCredentialLapses bounds re-announcing. One lapse is a restart; a
	// stream of them is a resident that will not keep a session, and looping
	// on it would look like a wait that never returns.
	maxCredentialLapses = 3
	waitTimeoutExit     = 3
)

// errWaitTimeout is the deadline passing with nothing new. It is not a
// failure: main turns it into exit status 3 so a shell loop can tell "nothing
// happened" from "the command broke" without parsing anything.
var errWaitTimeout = errors.New("nothing new before the deadline")

// waitCursorFile is what one actor's cursor file holds. The genesis is stored
// beside the cursor because a cursor means nothing in another workroom, and a
// checkout that has been re-initialised must resume from nothing rather than
// from a frontier that never existed here.
type waitCursorFile struct {
	Genesis string         `json:"genesis"`
	Actor   string         `json:"actor"`
	Cursor  service.Cursor `json:"cursor"`
	// Handle is the public presence handle of the session this invocation
	// opened. The next invocation reads it so that, under `--until any`, it
	// does not wake on its predecessor's departure — which is the one live
	// change a quiet room is guaranteed to produce.
	Handle string `json:"handle,omitempty"`
}

type waitOptions struct {
	actorName   string
	fingerprint string
	serverURL   string
	until       statusview.Until
	timeout     time.Duration
	cursorFile  string
	// The clocks below are fields rather than constants so a test can drive a
	// renewal, a poll and a local watch in less time than a person would wait.
	pollCap       time.Duration
	leaseTTL      time.Duration
	renewEvery    time.Duration
	watchInterval time.Duration
	// backoff is how long to wait before asking a resident again after a
	// refusal it invited: a full wait budget, or a socket that did not answer.
	backoff time.Duration
	// transportRetries is how many unanswered polls to absorb before giving up
	// on the resident and watching the sequence ref instead.
	transportRetries int
	out              io.Writer
	progress         io.Writer
}

func waitCommand(ctx context.Context, arguments []string) error {
	set, repo := flags("wait", arguments)
	as := set.String("as", "", "actor whose lanes are followed")
	serverFlag := set.String("server", "", "resident sequencer URL")
	timeout := set.Duration("timeout", 10*time.Minute, "how long to wait before giving up")
	until := set.String("until", string(statusview.UntilActionable), "wake on: actionable, or any")
	cursorFile := set.String("cursor-file", "", "where this actor's cursor is kept between calls")
	if err := set.Parse(arguments); err != nil {
		return parseRefusal(set, err)
	}
	if set.NArg() != 0 {
		return usageErrorf(set, "wait takes no positional arguments")
	}
	if *timeout <= 0 {
		return usageErrorf(set, "--timeout must be positive")
	}
	filter, err := statusview.ParseUntil(*until)
	if err != nil {
		return usageErrorf(set, "%v", err)
	}
	workspace, err := app.Open(ctx, *repo)
	if err != nil {
		return err
	}
	actorName, err := signingActorFrom("--as", *as)
	if err != nil {
		return usageReferenceError(set, workIdentityRefusal(ctx, workspace, err))
	}
	fingerprint := workspace.View().Actors[actorName].Fingerprint
	if fingerprint == "" {
		return usageReferenceError(set, workIdentityRefusal(ctx, workspace,
			fmt.Errorf("actor %q is not provisioned in this checkout", actorName)))
	}
	serverURL, err := resolveServerURL(workspace, *serverFlag)
	if err != nil {
		return err
	}
	path := *cursorFile
	if path == "" {
		if path, err = defaultCursorFile(ctx, workspace, fingerprint); err != nil {
			return err
		}
	}
	return runWait(ctx, workspace, waitOptions{
		actorName: actorName, fingerprint: fingerprint, serverURL: serverURL,
		until: filter, timeout: *timeout, cursorFile: path,
		pollCap: residentPollCap, leaseTTL: waitLeaseTTL, renewEvery: waitRenewEvery,
		watchInterval: localWatchInterval, backoff: residentBackoff, transportRetries: residentTransportRetries,
		out: os.Stdout, progress: os.Stderr,
	})
}

// defaultCursorFile puts one file per actor beside the rest of this
// repository's gitseq state, in the common directory, so every worktree of the
// same checkout resumes the same cursor.
func defaultCursorFile(ctx context.Context, workspace *app.Workspace, fingerprint string) (string, error) {
	_, commonDir, err := apphost.ResolveGitDirs(ctx, workspace.Repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(apphost.MetaDir(commonDir), "wait-cursor-"+fingerprint+".json"), nil
}

func runWait(ctx context.Context, workspace *app.Workspace, options waitOptions) error {
	held := readWaitCursor(options.cursorFile, workspace.View().Genesis)
	// One deadline for the whole invocation, computed before anything is
	// dialled. Both paths take it, so falling back from the resident to the
	// local watch spends what is left rather than starting the clock again.
	deadline := time.Now().Add(options.timeout)
	if options.serverURL == "" {
		return watchLocally(ctx, workspace, options, deadline, held)
	}
	return pollResident(ctx, workspace, options, deadline, held)
}

// pollResident is the long poll, looped under this command's deadline. The
// session is opened first because /v0/actor-wait answers only a session, and
// closed by the deferred departure on every exit path, interrupt included.
func pollResident(ctx context.Context, workspace *app.Workspace, options waitOptions, deadline time.Time, held waitCursorFile) error {
	client := residentclient.New(options.pollCap + 10*time.Second)
	session, err := openSession(ctx, client, options)
	if err != nil {
		switch classifyResident(ctx, err) {
		case abandoned:
			return ctx.Err()
		case recoverLocally:
			return fallBackLocally(ctx, workspace, options, deadline, held, err)
		}
		return fmt.Errorf("open a presence session at %s: %w", options.serverURL, err)
	}
	// The departure reads the session this loop holds now, not the one it held
	// when the defer was written: a reopened session must be the one closed.
	defer func() { departPresence(client, options, session.credential) }()
	cursor, ours := held.Cursor, []string{held.Handle, session.handle}
	renewed := time.Now()
	unanswered, lapses := 0, 0
	// reopen is the repair shared by a refused poll and a refused renewal: the
	// resident restarted, so the session it minted is gone and another one is
	// what this command needs to carry on.
	reopen := func(cause error) error {
		lapses++
		if lapses > maxCredentialLapses {
			return fmt.Errorf("the resident at %s would not keep a presence session: %w", options.serverURL, cause)
		}
		fmt.Fprintf(options.progress, "gs: the presence session lapsed (%v); opening another\n", cause)
		opened, err := openSession(ctx, client, options)
		if err != nil {
			return err
		}
		session, renewed = opened, time.Now()
		ours = append(ours, session.handle)
		return nil
	}
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errWaitTimeout
		}
		if time.Since(renewed) >= options.renewEvery {
			_, err := announce(ctx, client, options, session.credential)
			switch {
			case err == nil:
				renewed = time.Now()
			default:
				switch classifyResident(ctx, err) {
				case abandoned:
					return ctx.Err()
				case reopenSession:
					if err := reopen(err); err != nil {
						return err
					}
				case recoverLocally:
					return fallBackLocally(ctx, workspace, options, deadline, held, err)
				default:
					return fmt.Errorf("renew the presence session: %w", err)
				}
			}
			continue
		}
		poll, askable := pollWindow(remaining, options.pollCap)
		if !askable {
			return errWaitTimeout
		}
		var delta statusview.WaitDelta
		request := service.WaitRequest{Cursor: cursor, TimeoutMS: int(poll.Milliseconds()), Session: session.credential}
		if options.until == statusview.UntilActionable {
			request.Until = string(options.until)
		}
		polled := client.PostJSON(ctx, options.serverURL, "/v0/actor-wait", request, waitResponseLimit, &delta)
		if polled != nil {
			switch classifyResident(ctx, polled) {
			case abandoned:
				return ctx.Err()
			case retryShortly:
				if err := pause(ctx, deadline, options.backoff); err != nil {
					return err
				}
			case reopenSession:
				if err := reopen(polled); err != nil {
					return err
				}
			case recoverLocally:
				unanswered++
				if unanswered > options.transportRetries {
					return fallBackLocally(ctx, workspace, options, deadline, held, polled)
				}
				if err := pause(ctx, deadline, options.backoff); err != nil {
					return err
				}
			default:
				return unfilteredResidentError(options, polled)
			}
			continue
		}
		unanswered, lapses = 0, 0
		woken := woke(options.until, request.Cursor, delta, ours)
		cursor = delta.Cursor
		// A wake is printed before its cursor is kept: if the rendering fails,
		// the cursor stays where it was and the next call sees the same news
		// again rather than losing it.
		if woken {
			if err := renderWake(ctx, workspace, options, delta); err != nil {
				return err
			}
		}
		if err := writeWaitCursor(options.cursorFile, workspace.View().Genesis, options.actorName, cursor, session.handle); err != nil {
			return err
		}
		if woken {
			return nil
		}
	}
}

// residentRecovery is what one failed request asks its caller to do. Each has a
// repair this command can perform inside its own deadline, and the same answers
// reach it from a poll and from a lease renewal alike.
type residentRecovery int

const (
	// reportIt is a refusal with no repair here; the caller is told.
	reportIt residentRecovery = iota
	// retryShortly is a full wait budget: the resident asks to be tried again.
	retryShortly
	// reopenSession is a session the resident no longer holds, which is what a
	// restart looks like from here.
	reopenSession
	// recoverLocally is a resident that did not answer at all.
	recoverLocally
	// abandoned is this command being interrupted while the request was in
	// flight. It arrives looking exactly like a resident that stopped
	// answering, and recovering from it as one would print a line sending the
	// reader after the wrong thing.
	abandoned
)

// classifyResident reads one failed request. The cancellation is checked first
// and in this one place, because it is the one cause that is not the resident's
// and every caller would otherwise have to rule it out for itself.
func classifyResident(ctx context.Context, err error) residentRecovery {
	if ctx.Err() != nil {
		return abandoned
	}
	var refusal *residentclient.HTTPError
	switch {
	case errors.As(err, &refusal) && refusal.StatusCode == http.StatusTooManyRequests:
		return retryShortly
	case errors.As(err, &refusal) && strings.Contains(refusal.Message, "credential is not valid"):
		return reopenSession
	case residentclient.IsTransportError(err):
		return recoverLocally
	default:
		return reportIt
	}
}

// fallBackLocally gives up on a resident that is not answering and spends what
// is left of the deadline watching the sequence ref, which is what the MCP
// adapter does with the same failure. A resident restart is the ordinary cause,
// and it is exactly when an agent most needs the wait to keep working.
func fallBackLocally(ctx context.Context, workspace *app.Workspace, options waitOptions, deadline time.Time, held waitCursorFile, cause error) error {
	fmt.Fprintf(options.progress, "gs: the resident at %s is not answering (%v); watching the local sequence ref instead\n",
		options.serverURL, cause)
	local := options
	local.serverURL = ""
	return watchLocally(ctx, workspace, local, deadline, held)
}

// budgetFull recognises the resident's own refusal when every long-poll slot is
// taken. It is a 429 by design: the resource it protects is the goroutine
// holding a poll open, and queueing behind it would consume the same thing.
func budgetFull(err error) bool {
	var refusal *residentclient.HTTPError
	return errors.As(err, &refusal) && refusal.StatusCode == http.StatusTooManyRequests
}

// lapsedCredential recognises a session the resident no longer holds. It is the
// ordinary consequence of a resident restart, not a mistake by the caller.
func lapsedCredential(err error) bool {
	var refusal *residentclient.HTTPError
	return errors.As(err, &refusal) && strings.Contains(refusal.Message, "credential is not valid")
}

// pause waits out a short backoff without overrunning this command's deadline.
func pause(ctx context.Context, deadline time.Time, backoff time.Duration) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return errWaitTimeout
	}
	timer := time.NewTimer(min(remaining, backoff))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// pollWindow is how long one poll may ask for, and whether it is worth asking
// at all. A poll of zero milliseconds is not a short poll: the resident reads a
// non-positive timeout as "use my own", so asking for one would block for
// twenty-five seconds past this command's own deadline.
func pollWindow(remaining, capped time.Duration) (time.Duration, bool) {
	window := min(remaining, capped)
	if window < time.Millisecond {
		return 0, false
	}
	return window, true
}

// woke decides whether one answer is a wake.
//
// Under `--until actionable` the resident's verdict is the only honest source:
// the durable list holds every event after the cursor whether or not any of
// them was this actor's, so counting it would wake on everything.
//
// Under `--until any` the resident wakes on every live change, including the
// ones this command itself causes — its own announcement, and the departure of
// the invocation before it. In a quiet room those are the only live changes
// there are, so taking the resident's verdict made every call after the first
// return at once and a loop over `any` spun instead of waiting. The decision is
// therefore made here, over what the delta carries with this command's own
// sessions removed.
//
// A live reset is not a wake either. It says presence and conversation started
// again, which this command does not report and which leaves the durable
// frontier untouched; the cursor is rewritten to the new generation and the
// next poll blocks properly.
//
// Rollout window: `changed` being absent, and `any` travelling as an absent
// field, are both for residents built before this filter. When every resident
// this command meets sets `changed`, this function, frontierMoved and that
// spelling can all go, and the field can be read directly.
func woke(until statusview.Until, requested service.Cursor, delta statusview.WaitDelta, ours []string) bool {
	if until == statusview.UntilActionable {
		return delta.Changed
	}
	return frontierMoved(requested, delta.Cursor) || len(delta.Durable) > 0 || delta.Skipped > 0 ||
		len(delta.PriorityChat.Frames) > 0 || len(othersLive(delta.Live, ours)) > 0
}

// othersLive drops the presence changes this command is responsible for: the
// session it opened, and the sessions earlier invocations of it opened, whose
// departures are still in the room's history.
func othersLive(changes []nexus.Change, ours []string) []nexus.Change {
	if len(ours) == 0 {
		return changes
	}
	kept := make([]nexus.Change, 0, len(changes))
	for _, change := range changes {
		mine := false
		for _, handle := range ours {
			if handle != "" && change.ID == handle {
				mine = true
				break
			}
		}
		if !mine {
			kept = append(kept, change)
		}
	}
	return kept
}

// frontierMoved compares the frontier asked about with the frontier answered.
// Anything that is not one frontier on each side is treated as movement: a
// caller that has seen nothing, or an answer shaped in a way this command
// cannot compare, is better woken once than left waiting.
func frontierMoved(requested, answered service.Cursor) bool {
	if len(requested.Frontier) != 1 || len(answered.Frontier) != 1 {
		return true
	}
	return requested.Frontier[0] != answered.Frontier[0]
}

// unfilteredResidentError names the one refusal a reader cannot act on as it
// stands. A resident built before `until` existed rejects the field by name,
// and "unknown field" says nothing about which side is behind or what to do
// about it. Both repairs are named: restart the resident on this build, or ask
// for the behaviour that resident already has.
func unfilteredResidentError(options waitOptions, err error) error {
	var refusal *residentclient.HTTPError
	if errors.As(err, &refusal) && strings.Contains(refusal.Message, `unknown field "until"`) {
		return fmt.Errorf("the resident at %s was built before --until existed (%w); restart it on this build, or pass --until any",
			options.serverURL, err)
	}
	return err
}

// watchLocally is the wait with no resident to ask: it reads the sequence ref,
// and buys a verified fold only when that ref has moved.
func watchLocally(ctx context.Context, workspace *app.Workspace, options waitOptions, deadline time.Time, held waitCursorFile) error {
	cursor := held.Cursor
	fmt.Fprintf(options.progress, "gs: watching the local sequence ref every %s and verifying the durable log on every move\n",
		options.watchInterval)
	ref := kernel.Ref(workspace.View().Genesis)
	seen := cursorHead(cursor)
	for {
		head, err := workspace.Store.Head(ctx, ref)
		if err != nil {
			return err
		}
		if head != seen {
			seen = head
			delta, err := localDelta(ctx, workspace, options, cursor)
			if err != nil {
				return err
			}
			cursor = delta.Cursor
			if delta.Changed {
				if err := renderWake(ctx, workspace, options, delta); err != nil {
					return err
				}
			}
			if err := writeWaitCursor(options.cursorFile, workspace.View().Genesis, options.actorName, cursor, held.Handle); err != nil {
				return err
			}
			if delta.Changed {
				return nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errWaitTimeout
		}
		timer := time.NewTimer(min(remaining, options.watchInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// localDelta folds the log once and builds the same answer the resident would
// have built, with the live half declared degraded because there is no live
// half without a resident.
func localDelta(ctx context.Context, workspace *app.Workspace, options waitOptions, requested service.Cursor) (statusview.WaitDelta, error) {
	snapshot, err := sessionSnapshot(ctx, workspace, "")
	if err != nil {
		return statusview.WaitDelta{}, err
	}
	frontier := service.Cursor{Frontier: []service.Frontier{{Genesis: snapshot.Genesis, Head: snapshot.Head, Depth: snapshot.Depth}},
		Live: nexus.Cursor{Generation: "degraded"}}
	delta := statusview.BuildWait(snapshot, frontier, nil, false, requested, nil, options.fingerprint, options.actorName, true)
	if options.until != statusview.UntilActionable {
		delta.Changed = service.DurableChanged(requested.Frontier, snapshot)
		return delta, nil
	}
	// The filter runs over every event after the cursor, not over the capped
	// list the delta prints, and what it accepted is what the wake reports.
	accepted, omitted, actionable := statusview.FilterWait(snapshot, requested, delta, options.fingerprint)
	delta.Accepted, delta.AcceptedSkipped, delta.Changed = accepted, omitted, actionable
	return delta, nil
}

func cursorHead(cursor service.Cursor) string {
	if len(cursor.Frontier) != 1 {
		return ""
	}
	return cursor.Frontier[0].Head
}

// renderWake prints the reason this call woke — the accepted events under
// `actionable`, the durable delta under `any` — then the unacknowledged
// priority chat, then this actor's work through the `gs work --next`
// renderer, so a wake needs no second output to learn.
func renderWake(ctx context.Context, workspace *app.Workspace, options waitOptions, delta statusview.WaitDelta) error {
	var out strings.Builder
	events, omitted := delta.Accepted, delta.AcceptedSkipped
	if options.until != statusview.UntilActionable {
		events, omitted = delta.Durable, delta.Skipped
	}
	for _, event := range events {
		fmt.Fprintf(&out, "# %s %s by %s %s", verdictOrKind(event), event.Kind, event.Actor, event.Event)
		if event.Text != "" {
			fmt.Fprintf(&out, " — %s", oneLine(event.Text, 90))
		}
		out.WriteString("\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&out, "# and %d more events after your cursor, not shown.\n", omitted)
	}
	for _, frame := range delta.PriorityChat.Frames {
		fmt.Fprintf(&out, "# priority chat from %s in thread %s: %s\n", frame.ActorName, frame.Thread, oneLine(frame.Text, 90))
	}
	if _, err := io.WriteString(options.out, out.String()); err != nil {
		return err
	}
	lines, err := waitWorkNext(ctx, workspace, options)
	if err != nil {
		return err
	}
	_, err = io.WriteString(options.out, lines)
	return err
}

func verdictOrKind(event statusview.EventView) string {
	if event.Verdict == "" {
		return "event"
	}
	return event.Verdict
}

// waitWorkNext runs the default `gs work` selection for this actor and renders
// it through the same formatter `gs work --next` uses, so one wake answers the
// question a wake raises: what do I now owe, and what exactly do I type?
func waitWorkNext(ctx context.Context, workspace *app.Workspace, options waitOptions) (string, error) {
	query := statusview.WorkQuery{Actor: options.fingerprint}
	var page statusview.WorkPage
	serverURL := options.serverURL
	answered := false
	if serverURL != "" {
		answered = askResident(ctx, workspace, serverURL, "/v0/work-query", query, &page, func() statusview.Frontier { return page.Frontier })
	}
	if !answered {
		// A resident that could not answer the page has already been dialed
		// and named on standard error; asking it again for the projection
		// would say the same thing twice in different words.
		serverURL = ""
		snapshot, err := snapshotWithProgress(ctx, workspace)
		if err != nil {
			return "", err
		}
		page, err = statusview.BuildWorkPage(snapshot, query, options.serverURL != "")
		if err != nil {
			return "", err
		}
		workspace.MeasureLandingDetails(ctx, page.LandingRows())
	}
	return workNext(ctx, workspace, serverURL, page, options.fingerprint)
}

// waitSession is what one announcement returns: the private credential every
// later request carries, and the public handle the room sees this session by.
type waitSession struct {
	credential string
	handle     string
}

type presenceAnswer struct {
	Credential string       `json:"credential"`
	Change     nexus.Change `json:"change"`
}

// openSession announces this command into the room and registers its inbox.
func openSession(ctx context.Context, client *residentclient.Client, options waitOptions) (waitSession, error) {
	answer, err := announce(ctx, client, options, "")
	if err != nil {
		return waitSession{}, err
	}
	if answer.Credential == "" {
		return waitSession{}, errors.New("resident did not mint a credential")
	}
	session := waitSession{credential: answer.Credential, handle: answer.Change.ID}
	registerInbox(ctx, client, options, session.credential)
	return session, nil
}

// announce opens this command's session, or renews the one it holds. The
// credential stays in this process: it is never printed, logged, or written to
// the cursor file, which keeps only the public handle.
func announce(ctx context.Context, client *residentclient.Client, options waitOptions, credential string) (presenceAnswer, error) {
	// `waiting` is what this session is actually doing, and presence is where
	// the workroom says so. It is advisory attention and nothing more: it
	// claims no work, reports nothing and authorizes nothing.
	request := map[string]any{"actor": options.actorName, "ttl_ms": options.leaseTTL.Milliseconds(), "status": "waiting"}
	if credential != "" {
		request["credential"] = credential
	}
	var answer presenceAnswer
	err := client.PostJSON(ctx, options.serverURL, "/v0/presence", request, waitResponseLimit, &answer)
	return answer, err
}

// registerInbox asks for the addressed-chat half of this session, which is
// delivered only to a session that asked for it. A resident too old to know
// the protocol has no inbox, and saying so beats waiting for frames that
// cannot arrive.
func registerInbox(ctx context.Context, client *residentclient.Client, options waitOptions, credential string) {
	var answer struct {
		Version string `json:"version"`
	}
	if err := client.PostJSON(ctx, options.serverURL, "/v0/inbox/register",
		map[string]any{"credential": credential, "version": service.InboxProtocolVersion}, waitResponseLimit, &answer); err != nil {
		fmt.Fprintf(options.progress, "gs: this resident serves no priority chat inbox (%v); waiting on durable lanes only\n", err)
	}
}

// departPresence closes the session on the way out. It deliberately does not
// inherit the command's context: that context is already cancelled when the
// command is interrupted, which is when the session most needs removing.
func departPresence(client *residentclient.Client, options waitOptions, credential string) {
	if credential == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.PostValue(ctx, options.serverURL, "/v0/presence/depart",
		map[string]any{"credential": credential}, waitResponseLimit); err != nil {
		fmt.Fprintf(options.progress, "gs: the presence session could not be closed (%v); its lease expires on its own\n", err)
	}
}

// readWaitCursor resumes where the last call stopped. Anything unreadable,
// unparseable or from another workroom resumes from nothing, which costs one
// replay of the current lanes and never a wrong answer.
func readWaitCursor(path, genesis string) waitCursorFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return waitCursorFile{}
	}
	var held waitCursorFile
	if err := json.Unmarshal(data, &held); err != nil || held.Genesis != genesis {
		return waitCursorFile{}
	}
	return held
}

// writeWaitCursor replaces the file atomically. A half-written cursor read by
// the next call would be indistinguishable from a corrupt one, and the repair
// for both is the same replay, but a rename costs nothing and avoids it.
func writeWaitCursor(path, genesis, actor string, cursor service.Cursor, handle string) error {
	data, err := json.Marshal(waitCursorFile{Genesis: genesis, Actor: actor, Cursor: cursor, Handle: handle})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		os.Remove(name)
		return err
	}
	if err := temporary.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
