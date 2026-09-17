package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// prints it the way `gs work --next` prints it.
//
// It exists because the CLI had no wait at all. `gs status` is a snapshot and
// `gs work --next` is one shot, so an agent following a workroom from a shell
// built its own wait out of a sleep loop around one of them, and every
// iteration paid for a whole verification or a whole status fetch to discover
// that nothing had happened. This is the same long poll the MCP `wait` tool
// uses — one resident round trip that returns when there is news — with the
// loop, the presence lease and the cursor kept here rather than reinvented in
// bash.
//
// Three things make one invocation one wake. The resident caps a single poll
// at 30 seconds, so the loop is client-side and bounded by --timeout rather
// than by the resident's cap. The `until` filter is applied at the resident,
// so a quiet answer never crosses the socket at all. And the cursor is
// persisted per actor, so the next invocation resumes where this one stopped
// instead of replaying the log it already read.
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
	waitTimeoutExit    = 3
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
	out           io.Writer
	progress      io.Writer
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
		watchInterval: localWatchInterval, out: os.Stdout, progress: os.Stderr,
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
	genesis := workspace.View().Genesis
	cursor := readWaitCursor(options.cursorFile, genesis)
	if options.serverURL == "" {
		return watchLocally(ctx, workspace, options, cursor)
	}
	return pollResident(ctx, workspace, options, cursor)
}

// pollResident is the long poll, looped under this command's own deadline. The
// presence session is opened before the first poll because /v0/actor-wait
// refuses without a credential, and it is closed on every exit path including
// an interrupt, so a departed session does not sit in the room's presence list
// until its lease expires.
func pollResident(ctx context.Context, workspace *app.Workspace, options waitOptions, cursor service.Cursor) error {
	deadline := time.Now().Add(options.timeout)
	client := residentclient.New(options.pollCap + 10*time.Second)
	credential, err := announcePresence(ctx, client, options, "")
	if err != nil {
		return fmt.Errorf("open a presence session at %s: %w", options.serverURL, err)
	}
	defer departPresence(client, options, credential)
	registerInbox(ctx, client, options, credential)
	renewed := time.Now()
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errWaitTimeout
		}
		if time.Since(renewed) >= options.renewEvery {
			if _, err := announcePresence(ctx, client, options, credential); err != nil {
				return fmt.Errorf("renew the presence session: %w", err)
			}
			renewed = time.Now()
		}
		poll, askable := pollWindow(remaining, options.pollCap)
		if !askable {
			return errWaitTimeout
		}
		var delta statusview.WaitDelta
		request := service.WaitRequest{Cursor: cursor, TimeoutMS: int(poll.Milliseconds()), Session: credential}
		if options.until == statusview.UntilActionable {
			// `any` is sent as an absent field, not as the word. The resident
			// decodes this request strictly, so a resident built before the
			// filter existed refuses any spelling of it — and the default
			// behaviour it already implements is exactly `any`.
			request.Until = string(options.until)
		}
		if err := client.PostJSON(ctx, options.serverURL, "/v0/actor-wait", request, waitResponseLimit, &delta); err != nil {
			return unfilteredResidentError(options, err)
		}
		woken := woke(options.until, request.Cursor, delta)
		cursor = delta.Cursor
		// A wake is printed before its cursor is kept. If the rendering fails,
		// the cursor stays where it was and the next call sees the same news
		// again; advancing first would lose it for good. A poll that woke
		// nobody has nothing to lose, and keeping its cursor is what stops the
		// filter reconsidering the same declined events every time round.
		if woken {
			if err := renderWake(ctx, workspace, options, delta); err != nil {
				return err
			}
		}
		if err := writeWaitCursor(options.cursorFile, workspace.View().Genesis, options.actorName, cursor); err != nil {
			return err
		}
		if woken {
			return nil
		}
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

// woke decides whether one answer is a wake. A resident that knows the
// `changed` field says so outright, and under `--until actionable` that is the
// only honest source: the durable list holds every event after the cursor
// whether or not any of them was this actor's, so counting it would wake on
// everything.
//
// A resident built before the field leaves it absent, and such a resident is
// also one that refuses `until` outright — so the only case left to decide is
// an unfiltered poll, where a wake is exactly what the delta itself shows: a
// frontier that moved, an event after the cursor, a live change, a reset or a
// pending frame. Without this an older resident answered every poll with an
// absent field and the command waited out its whole deadline in silence.
func woke(until statusview.Until, requested service.Cursor, delta statusview.WaitDelta) bool {
	if delta.Changed || until == statusview.UntilActionable {
		return delta.Changed
	}
	return frontierMoved(requested, delta.Cursor) || len(delta.Durable) > 0 || delta.Skipped > 0 ||
		delta.Reset || len(delta.Live) > 0 || len(delta.PriorityChat.Frames) > 0
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

// watchLocally is the wait with no resident to ask. It watches the one fact
// that can change underneath it — the sequence ref — and pays for a verified
// fold only when that ref moves. The filter is the same function the resident
// applies, so `--until actionable` means the same thing here as there.
func watchLocally(ctx context.Context, workspace *app.Workspace, options waitOptions, cursor service.Cursor) error {
	fmt.Fprintf(options.progress, "gs: no resident named; watching the local sequence ref every %s and verifying the durable log on every move\n",
		options.watchInterval)
	deadline := time.Now().Add(options.timeout)
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
			if err := writeWaitCursor(options.cursorFile, workspace.View().Genesis, options.actorName, cursor); err != nil {
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
	delta.Changed = service.DurableChanged(requested.Frontier, snapshot) &&
		(options.until != statusview.UntilActionable ||
			statusview.ActionableWait(snapshot.Projection, delta, options.fingerprint))
	return delta, nil
}

func cursorHead(cursor service.Cursor) string {
	if len(cursor.Frontier) != 1 {
		return ""
	}
	return cursor.Frontier[0].Head
}

// renderWake prints what woke this call: the durable events after the cursor,
// the priority chat that has not been acknowledged, and then the actor's own
// work in the exact shape `gs work --next` prints it, commands and all. The
// point of reusing that renderer rather than writing a second one is that an
// agent reading a wake does not have to learn a second output.
func renderWake(ctx context.Context, workspace *app.Workspace, options waitOptions, delta statusview.WaitDelta) error {
	var out strings.Builder
	// The live count is named as well as the durable one, because under
	// `--until any` a wake with nothing durable behind it is an ordinary
	// answer — presence or conversation moved — and a header that reported
	// only durable events would leave the reader asking why it printed.
	fmt.Fprintf(&out, "# gs wait woke at depth %d: %d durable events after your cursor, %d live changes, %d unacknowledged priority chat frames.\n",
		delta.Totals.Depth, len(delta.Durable), len(delta.Live), len(delta.PriorityChat.Frames))
	if delta.Skipped > 0 {
		fmt.Fprintf(&out, "# %d older durable events were omitted by the delta cap.\n", delta.Skipped)
	}
	for _, event := range delta.Durable {
		fmt.Fprintf(&out, "# %s %s by %s %s", verdictOrKind(event), event.Kind, event.Actor, event.Event)
		if event.Text != "" {
			fmt.Fprintf(&out, " — %s", oneLine(event.Text, 90))
		}
		out.WriteString("\n")
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

type presenceAnswer struct {
	Credential string          `json:"credential"`
	Change     json.RawMessage `json:"change"`
}

// announcePresence opens this command's session, or renews the one it holds.
// It is the same request the MCP adapter sends, and the resident opens the
// actor's key from this checkout, which is why `gs wait` needs `--as` and the
// local key just as the adapter does. The credential stays in this process:
// it is never printed, logged or written to the cursor file.
func announcePresence(ctx context.Context, client *residentclient.Client, options waitOptions, credential string) (string, error) {
	request := map[string]any{"actor": options.actorName, "ttl_ms": options.leaseTTL.Milliseconds()}
	if credential != "" {
		request["credential"] = credential
	}
	var answer presenceAnswer
	if err := client.PostJSON(ctx, options.serverURL, "/v0/presence", request, waitResponseLimit, &answer); err != nil {
		return "", err
	}
	if credential != "" {
		return credential, nil
	}
	if answer.Credential == "" {
		return "", errors.New("resident did not mint a credential")
	}
	return answer.Credential, nil
}

// registerInbox asks for the addressed-chat half of this session. Priority
// chat is per session and is delivered only to a session that asked for it, so
// without this a wait would be blind to the one live thing it must not miss. A
// resident too old to know the protocol simply has no inbox, and saying so is
// better than waiting silently for frames that can never arrive.
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
// command is interrupted, and an interrupt is exactly when the session most
// needs removing.
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
func readWaitCursor(path, genesis string) service.Cursor {
	data, err := os.ReadFile(path)
	if err != nil {
		return service.Cursor{}
	}
	var held waitCursorFile
	if err := json.Unmarshal(data, &held); err != nil || held.Genesis != genesis {
		return service.Cursor{}
	}
	return held.Cursor
}

// writeWaitCursor replaces the file atomically. A half-written cursor read by
// the next call would be indistinguishable from a corrupt one, and the repair
// for both is the same replay, but a rename costs nothing and avoids it.
func writeWaitCursor(path, genesis, actor string, cursor service.Cursor) error {
	data, err := json.Marshal(waitCursorFile{Genesis: genesis, Actor: actor, Cursor: cursor})
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
