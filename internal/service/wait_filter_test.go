package service

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"testing"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// What an actionable poll costs while it is declining things, and what it must
// still notice afterwards. Both are about the same decision: a change the
// filter has judged is a change the poll has seen, so it moves its baseline
// past it — which is what stops the poll re-reading and re-judging the same
// thing four times a second, and what must not stop it noticing the next one.

// filterFixture is the head-watch fixture with a second actor, so one of them
// can file work the other is meant to be woken by.
func filterFixture(t *testing.T) (*Server, *app.Workspace, *refCounter, string) {
	t.Helper()
	server, workspace, _, counter := newHeadWatchFixture(t, 1)
	if _, _, err := workspace.AddActor(context.Background(), "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	resolved, err := workspace.ResolveActor("other")
	if err != nil {
		t.Fatal(err)
	}
	return server, workspace, counter, resolved.Fingerprint
}

// sessionFor opens a presence session bound to one workroom actor's own key, so
// the fingerprint the filter reads is the durable one.
func sessionFor(t *testing.T, server *Server, workspace *app.Workspace, name string) string {
	t.Helper()
	actor, private, err := workspace.Actor(name)
	if err != nil {
		t.Fatal(err)
	}
	credential, _, err := server.hub.OpenTrustedSession(name, private.Public().(ed25519.PublicKey),
		actor.Name+" ("+actor.Fingerprint[:12]+")", time.Minute, nexus.ActivityUpdate{})
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func appendUnrelated(t *testing.T, workspace *app.Workspace, key string) {
	t.Helper()
	if _, err := workspace.Act(context.Background(), "human", app.Act{Verb: app.VerbState,
		Kind: workroom.KindAssert, Text: "not yours: " + key,
		RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: key}); err != nil {
		t.Fatal(err)
	}
}

func appendRequestTo(t *testing.T, workspace *app.Workspace, fingerprint, key string) {
	t.Helper()
	if _, err := workspace.Act(context.Background(), "human", app.Act{Verb: app.VerbState,
		Kind: workroom.KindRequest, Text: "yours: " + key,
		Body:    map[string]string{"to": fingerprint, "conditions": "say so", "no_git_artifact": "true"},
		RestsOn: []string{workspace.EventID(workspace.View().Genesis)}, IdempotencyKey: key}); err != nil {
		t.Fatal(err)
	}
}

// A live change the filter declines must not be found again on every tick. It
// used to be: Observe reports changes since the cursor it is given and does not
// move it, so one presence announcement left liveMoved true for the rest of the
// poll, and each tick then re-read the verified snapshot — a Git process every
// 250 ms for a poll that was never going to answer. Every `gs wait` tripped
// this on its own announcement.
//
// The clock is slowed here on purpose. It reads the head four times a second
// for as long as any poll is open, by design and once for all waiters; what is
// measured here is what this one waiter spends on top of that.
func TestActionablePollBehindALiveChangeDoesNotReadOnEveryTick(t *testing.T) {
	server, workspace, counter, _ := filterFixture(t)
	ctx := context.Background()
	server.heads.interval = 2 * time.Second
	credential := sessionFor(t, server, workspace, "other")
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Somebody else arrives: one live change sitting after the cursor, which
	// `actionable` declines.
	stranger := sessionFor(t, server, workspace, "human")
	if stranger == "" {
		t.Fatal("no stranger session")
	}
	counter.refs.Store(0)
	counter.all.Store(0)
	_, _, changed, err := server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 3000, Session: credential},
		statusview.UntilActionable)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("an actionable poll woke on somebody else's arrival")
	}
	if spent := counter.refs.Load(); spent > 6 {
		t.Fatalf("an actionable poll behind one declined live change spent %d ref reads in 3s; it is re-reading per tick", spent)
	}
}

// A change the poll has judged is a change it has seen, on the durable side as
// well as the live one. Once the frontier has moved past the caller's cursor it
// stays moved for the rest of the poll, so a poll that did not carry its
// baseline forward re-ran the whole filter — the lanes, the signed set, the
// provenance walk — four times a second over a projection that had not changed.
// That has no outward sign at all, so it is counted.
func TestActionablePollJudgesADeclinedChangeOnce(t *testing.T) {
	server, workspace, _, _ := filterFixture(t)
	ctx := context.Background()
	credential := sessionFor(t, server, workspace, "other")
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	appendUnrelated(t, workspace, "judged-once")
	server.filtered.Store(0)
	_, _, changed, err := server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 3000, Session: credential},
		statusview.UntilActionable)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("an actionable poll woke on an act between two other people")
	}
	// One evaluation for the change, and room for a second if the clock's own
	// tick lands between the append and the first check. Never one per tick.
	if spent := server.filtered.Load(); spent > 2 {
		t.Fatalf("a 3s poll evaluated the filter %d times for one declined change", spent)
	}
}

// Declining is not deafness. The poll moves its baseline past what it has
// judged, and the very next thing that arrives must still be judged on its own.
func TestActionablePollWakesOnAnEventArrivingAfterADeclinedOne(t *testing.T) {
	server, workspace, _, fingerprint := filterFixture(t)
	ctx := context.Background()
	credential := sessionFor(t, server, workspace, "other")
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type answer struct {
		changed bool
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		_, _, changed, err := server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 8000, Session: credential},
			statusview.UntilActionable)
		done <- answer{changed: changed, err: err}
	}()
	// First something that is not this actor's, which the poll declines and
	// puts behind its baseline; then something that is.
	time.Sleep(500 * time.Millisecond)
	appendUnrelated(t, workspace, "declined-first")
	time.Sleep(1500 * time.Millisecond)
	appendRequestTo(t, workspace, fingerprint, "yours-second")
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !result.changed {
			t.Fatal("the poll declined the first event and then slept through the second")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("the poll never returned")
	}
}

// The same two events in the other order: a poll that has already declined must
// not report a change merely because something once moved.
func TestActionablePollStillDeclinesAfterSeveralUnrelatedEvents(t *testing.T) {
	server, workspace, _, _ := filterFixture(t)
	ctx := context.Background()
	credential := sessionFor(t, server, workspace, "other")
	initial, err := server.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(300 * time.Millisecond)
			appendUnrelated(t, workspace, fmt.Sprintf("declined-%d", i))
		}
	}()
	_, _, changed, err := server.wait(ctx, WaitRequest{Cursor: initial.Cursor, TimeoutMS: 2000, Session: credential},
		statusview.UntilActionable)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("an actionable poll woke on three events none of which were this actor's")
	}
}

// The whole-workroom route has no actor, so it cannot evaluate the filter and
// refuses it rather than accepting a field it would ignore.
func TestWaitRouteRefusesTheActionableFilter(t *testing.T) {
	server, _, _, _ := filterFixture(t)
	if _, err := statusview.ParseUntil("sometime"); err == nil {
		t.Error("ParseUntil accepted a value that is neither actionable nor any")
	}
	// The route check lives in handleWaitResponse; this asserts the value the
	// route is checked against is the one the poll understands.
	if _, _, _, err := server.wait(context.Background(),
		WaitRequest{Cursor: Cursor{}, TimeoutMS: 50}, statusview.UntilAny); err != nil {
		t.Fatalf("an unfiltered whole-workroom poll: %v", err)
	}
}
