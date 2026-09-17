package statusview

import (
	"fmt"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The filter, at the level it is written: a projection, a cursor, and one
// actor. Everything above it — the resident's poll, the MCP tool, `gs wait` —
// asks this the same question, so the rules are pinned once here rather than
// three times through three transports.

// The two actors these rules are about. actor_test.go already has names of
// its own for the same idea; these are this file's, so the two sets cannot be
// confused for one another.
const (
	filterMe   = "fingerprint:filter-me"
	filterThem = "fingerprint:filter-them"
)

// filterWorld builds a projection by hand: it is the only way to say exactly
// which event rests on which and who signed it, which is what these rules are
// about.
type filterWorld struct {
	decisions  []workroom.Decision
	statements []workroom.Statement
	acts       []workroom.Act
	provenance map[string][]string
	comm       []workroom.Commitment
}

func newFilterWorld() *filterWorld {
	return &filterWorld{provenance: map[string][]string{}}
}

func (w *filterWorld) say(event, actor string, kind workroom.Kind, retired bool, bases ...string) *filterWorld {
	w.decisions = append(w.decisions, workroom.Decision{Event: event, Verdict: workroom.Effective})
	w.statements = append(w.statements, workroom.Statement{Event: event, Actor: actor, Kind: kind,
		Retired: retired, Sequence: len(w.decisions)})
	if len(bases) > 0 {
		w.provenance[event] = bases
	}
	return w
}

func (w *filterWorld) commit(request, promise, report, status, addressedTo, waitingOn, performer string) *filterWorld {
	w.comm = append(w.comm, workroom.Commitment{Request: request, Promise: promise, Report: report,
		Status: status, AddressedTo: addressedTo, WaitingOn: waitingOn, Performer: performer, Requester: filterThem})
	return w
}

func (w *filterWorld) snapshot() app.Snapshot {
	return app.Snapshot{Genesis: "g", Head: "h", Depth: len(w.decisions),
		Projection: workroom.Projection{Decisions: w.decisions, Statements: w.statements,
			Acts: w.acts, Provenance: w.provenance, Commitments: w.comm, Actors: map[string]workroom.ActorState{
				filterMe: {Name: "me", Roles: []string{"participant"}}, filterThem: {Name: "them", Roles: []string{"participant"}}}}}
}

// from returns the cursor standing at depth n, which is "everything after the
// first n records is new to me".
func from(n int) Cursor {
	return Cursor{Frontier: []Frontier{{Genesis: "g", Head: "h", Depth: n}}}
}

func acceptedEvents(t *testing.T, world *filterWorld, at Cursor) []string {
	t.Helper()
	views, _ := AcceptedWaitEvents(world.snapshot(), at, filterMe)
	events := make([]string, 0, len(views))
	for _, view := range views {
		events = append(events, view.Event)
	}
	return events
}

func TestParseUntilTakesTheTwoValuesAndRefusesTheRest(t *testing.T) {
	for _, value := range []string{"", "any"} {
		if until, err := ParseUntil(value); err != nil || until != UntilAny {
			t.Errorf("ParseUntil(%q) = %q, %v; want any", value, until, err)
		}
	}
	if until, err := ParseUntil("actionable"); err != nil || until != UntilActionable {
		t.Errorf("ParseUntil(actionable) = %q, %v", until, err)
	}
	for _, value := range []string{"soon", "ACTIONABLE", "none", " any"} {
		until, err := ParseUntil(value)
		if err == nil {
			t.Errorf("ParseUntil(%q) = %q; want a refusal", value, until)
			continue
		}
		if got := err.Error(); !contains(got, "actionable, any") {
			t.Errorf("ParseUntil(%q) refusal %q does not name the admissible values", value, got)
		}
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestAcceptedWaitEventsTakesTheActorsOwnLanesAndNothingElse(t *testing.T) {
	world := newFilterWorld().
		say("seed", filterThem, workroom.KindAssert, false).
		say("request-mine", filterThem, workroom.KindRequest, false, "seed").
		say("request-theirs", filterThem, workroom.KindRequest, false, "seed").
		say("chatter", filterThem, workroom.KindAssert, false, "seed")
	world.commit("request-mine", "", "", "open", filterMe, "", "")
	world.commit("request-theirs", "", "", "open", filterThem, "", "")

	got := acceptedEvents(t, world, from(1))
	if len(got) != 1 || got[0] != "request-mine" {
		t.Fatalf("accepted %v; want only the request addressed to this actor", got)
	}
}

// The lanes are read from the projection, not from a response's twenty rows.
// A request arriving behind a full lane is still this actor's to claim.
func TestAcceptedWaitEventsReadsLanesPastTheResponseCap(t *testing.T) {
	world := newFilterWorld().say("seed", filterThem, workroom.KindAssert, false)
	for index := 0; index < ListCap+5; index++ {
		event := "old-" + string(rune('a'+index))
		world.say(event, filterThem, workroom.KindRequest, false, "seed")
		world.commit(event, "", "", "open", filterMe, "", "")
	}
	depth := len(world.decisions)
	world.say("newest", filterThem, workroom.KindRequest, false, "seed")
	world.commit("newest", "", "", "open", filterMe, "", "")

	got := acceptedEvents(t, world, from(depth))
	if len(got) != 1 || got[0] != "newest" {
		t.Fatalf("accepted %v; want the newest request even though the lane is over its cap", got)
	}
}

// Rule three: what rests on a live event this actor signed.
func TestAcceptedWaitEventsTakesWhatRestsOnALiveEventOfYours(t *testing.T) {
	world := newFilterWorld().
		say("seed", filterThem, workroom.KindAssert, false).
		say("mine", filterMe, workroom.KindReport, false, "seed")
	depth := len(world.decisions)
	world.say("answer", filterThem, workroom.KindAssert, false, "mine")

	if got := acceptedEvents(t, world, from(depth)); len(got) != 1 || got[0] != "answer" {
		t.Fatalf("accepted %v; want the record answering this actor's own", got)
	}
}

// ...and only a live one. A record resting on something of this actor's that
// has since been retired is news about the retirement, not work for them.
func TestAcceptedWaitEventsIgnoresWhatRestsOnARetiredEventOfYours(t *testing.T) {
	world := newFilterWorld().
		say("seed", filterThem, workroom.KindAssert, false).
		say("mine", filterMe, workroom.KindReport, true, "seed")
	depth := len(world.decisions)
	world.say("answer", filterThem, workroom.KindAssert, false, "mine")

	if got := acceptedEvents(t, world, from(depth)); len(got) != 0 {
		t.Fatalf("accepted %v; a retired basis of this actor's is not work for them", got)
	}
}

// An actor's own act is none of the rules, whichever one it would match. Their
// promise on a request addressed to them is inside their own lane row, and
// waking on it makes every act they file return their own next wait.
func TestAcceptedWaitEventsIgnoresTheActorsOwnActs(t *testing.T) {
	world := newFilterWorld().
		say("seed", filterThem, workroom.KindAssert, false).
		say("request", filterThem, workroom.KindRequest, false, "seed")
	depth := len(world.decisions)
	world.say("my-promise", filterMe, workroom.KindPromise, false, "request").
		say("my-assert", filterMe, workroom.KindAssert, false, "my-promise")
	world.commit("request", "my-promise", "", "promised", "", filterMe, filterMe)

	if got := acceptedEvents(t, world, from(depth)); len(got) != 0 {
		t.Fatalf("accepted %v; an actor's own acts are not news to them", got)
	}
}

// The decision covers every event after the cursor; only the rendering is
// capped, and what it left out is counted rather than dropped in silence.
func TestAcceptedWaitEventsDecidesPastTheDeltaCap(t *testing.T) {
	world := newFilterWorld().say("seed", filterThem, workroom.KindAssert, false)
	depth := len(world.decisions)
	for index := 0; index < DeltaCap+7; index++ {
		event := fmt.Sprint("mine-", index)
		world.say(event, filterThem, workroom.KindRequest, false, "seed")
		world.commit(event, "", "", "open", filterMe, "", "")
	}
	views, skipped := AcceptedWaitEvents(world.snapshot(), from(depth), filterMe)
	if len(views) != DeltaCap || skipped != 7 {
		t.Fatalf("rendered %d with %d omitted; want %d and 7", len(views), skipped, DeltaCap)
	}
}

// Chat is the one rule that is not about the log at all, so FilterWait carries
// it while AcceptedWaitEvents does not.
func TestFilterWaitIsActionableForChatAloneAndQuietOtherwise(t *testing.T) {
	world := newFilterWorld().say("seed", filterThem, workroom.KindAssert, false)
	snapshot := world.snapshot()
	quiet := WaitDelta{}
	if _, _, actionable := FilterWait(snapshot, from(1), quiet, filterMe); actionable {
		t.Error("an empty answer is actionable")
	}
	spoken := WaitDelta{PriorityChat: InboxView{Available: true, Frames: []AddressedFrame{{Text: "hello"}}}}
	accepted, _, actionable := FilterWait(snapshot, from(1), spoken, filterMe)
	if !actionable {
		t.Error("an unacknowledged priority frame is not actionable")
	}
	if len(accepted) != 0 {
		t.Errorf("chat produced %d accepted events; it is not one", len(accepted))
	}
}
