package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/nexus"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
)

const (
	mine   = "fingerprint:mine"
	theirs = "fingerprint:theirs"
)

// sampleStatus builds a projection with more history than any one actor cares
// about, so the digest has something to leave out.
func sampleStatus(events int) service.Status {
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{
			mine:   {Name: "me", Roles: []string{"participant"}},
			theirs: {Name: "them", Roles: []string{"operator", "ratifier"}},
		},
	}
	for index := range events {
		id := fmt.Sprintf("event:%d", index)
		projection.Decisions = append(projection.Decisions, workroom.Decision{
			Event: id, Verdict: workroom.Effective, Reason: "statement recorded",
		})
		projection.Statements = append(projection.Statements, workroom.Statement{
			Event: id, Actor: theirs, Kind: workroom.KindAssert,
			Text: strings.Repeat("padding so the full projection is genuinely large ", 8),
		})
	}
	projection.Statements = append(projection.Statements,
		workroom.Statement{Event: "request:for-me", Actor: theirs, Kind: workroom.KindRequest, Text: "please do the thing"},
		workroom.Statement{Event: "request:for-them", Actor: mine, Kind: workroom.KindRequest, Text: "please do the other thing"},
		workroom.Statement{Event: "act:mine-failed", Actor: mine, Kind: workroom.KindPromise, Text: "a promise that did not take"},
	)
	projection.Decisions = append(projection.Decisions,
		workroom.Decision{Event: "act:mine-failed", Verdict: workroom.Ineffective, Reason: "dangling promise has no request"},
		workroom.Decision{Event: "act:theirs-failed", Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"},
	)
	projection.Statements = append(projection.Statements,
		workroom.Statement{Event: "act:theirs-failed", Actor: theirs, Kind: workroom.KindPropose, Text: "not my business"})
	projection.Commitments = []workroom.Commitment{
		{Request: "request:for-me", Requester: theirs, Performer: mine, Status: "requested", WaitingOn: mine},
		{Request: "request:for-them", Requester: mine, Performer: theirs, Status: "promised", WaitingOn: theirs},
		{Request: "request:done", Requester: theirs, Performer: mine, Status: "satisfied"},
		{Request: "request:other", Requester: theirs, Performer: theirs, Status: "requested", WaitingOn: theirs},
	}
	projection.Artifacts = []workroom.Artifact{
		{Event: "artifact:a", Path: ".", Commit: "abc", Stale: false},
		{Event: "artifact:b", Path: "x", Commit: "def", Stale: true},
	}
	depth := len(projection.Decisions)
	return service.Status{
		Durable: app.Snapshot{Genesis: "genesis", Head: "head", Depth: depth, Projection: projection},
		Live: nexus.Snapshot{
			Cursor:   nexus.Cursor{Generation: "generation:x", Position: 9},
			Presence: map[string]string{"session:1": "me (finger)", "session:2": "me (finger)", "session:3": "them (finger)"},
		},
		Cursor: service.Cursor{
			Frontier: []service.Frontier{{Genesis: "genesis", Head: "head", Depth: depth}},
			Live:     nexus.Cursor{Generation: "generation:x", Position: 9},
		},
	}
}

func TestStatusDigestIsActorOrientedAndBounded(t *testing.T) {
	status := sampleStatus(400)
	digest := digestStatus(status, mine, "me", false)

	if len(digest.WaitingOnYou) != 1 || digest.WaitingOnYou[0].Request != "request:for-me" {
		t.Fatalf("waiting_on_you = %#v", digest.WaitingOnYou)
	}
	if digest.WaitingOnYou[0].Requester != "them" {
		t.Fatalf("requester should render as a name: %#v", digest.WaitingOnYou[0])
	}
	if len(digest.YouAreWaiting) != 1 || digest.YouAreWaiting[0].Request != "request:for-them" {
		t.Fatalf("you_are_waiting_on = %#v", digest.YouAreWaiting)
	}
	// Only my own ineffective act, not everyone's.
	if len(digest.YourAttention) != 1 || digest.YourAttention[0].Event != "act:mine-failed" {
		t.Fatalf("needs_your_attention = %#v", digest.YourAttention)
	}
	// Nothing is hidden: totals still describe the whole projection.
	if digest.Totals.Depth != status.Durable.Depth || digest.Totals.Statements != len(status.Durable.Projection.Statements) {
		t.Fatalf("totals do not describe the whole projection: %#v", digest.Totals)
	}
	if digest.Totals.IneffectiveActs != 2 || digest.Totals.StaleArtifacts != 1 || digest.Totals.Artifacts != 2 {
		t.Fatalf("totals = %#v", digest.Totals)
	}
	// One actor with two sessions is one person present.
	if len(digest.Live.Present) != 2 {
		t.Fatalf("presence should collapse sessions: %#v", digest.Live.Present)
	}
	// The point of the exercise: the answer must not grow with the log.
	full, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	bounded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded)*10 > len(full) {
		t.Fatalf("digest %d bytes is not materially smaller than the projection %d bytes", len(bounded), len(full))
	}
	// Ten times the history must not mean ten times the answer. The digest is
	// allowed to grow by the width of its own counters — depth 401 becomes
	// depth 4001 — and by nothing else, so the growth is logarithmic in the
	// log's size while the projection it summarises grows linearly.
	tenfold := sampleStatus(4000)
	grown, err := json.Marshal(digestStatus(tenfold, mine, "me", false))
	if err != nil {
		t.Fatal(err)
	}
	fullTenfold, err := json.Marshal(tenfold)
	if err != nil {
		t.Fatal(err)
	}
	if growth := len(grown) - len(bounded); growth > 32 {
		t.Fatalf("digest grew %d bytes for ten times the history: %d at 400 events, %d at 4000", growth, len(bounded), len(grown))
	}
	if len(fullTenfold) < 5*len(full) {
		t.Fatalf("the projection under test did not actually grow: %d then %d bytes", len(full), len(fullTenfold))
	}
}

func TestStatusDigestReportsDegradedWithoutInventingLiveState(t *testing.T) {
	digest := digestStatus(sampleStatus(3), mine, "me", true)
	if !digest.Live.Degraded {
		t.Fatal("degraded status did not say so")
	}
	if digest.Live.Generation != "" || len(digest.Live.Present) != 0 {
		t.Fatalf("degraded status invented live state: %#v", digest.Live)
	}
	if len(digest.WaitingOnYou) != 1 {
		t.Fatalf("durable answers must survive degradation: %#v", digest.WaitingOnYou)
	}
}

func TestWaitDeltaCarriesOnlyWhatIsNewAfterTheCursor(t *testing.T) {
	status := sampleStatus(20)
	depth := status.Durable.Depth
	// The caller has read everything except the last three events.
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: depth - 3}}}
	delta := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", false)
	if len(delta.Durable) != 3 {
		t.Fatalf("expected three new events, got %d", len(delta.Durable))
	}
	if delta.Skipped != 0 {
		t.Fatalf("nothing should have been skipped: %d", delta.Skipped)
	}
	last := status.Durable.Projection.Decisions[depth-1].Event
	if delta.Durable[len(delta.Durable)-1].Event != last {
		t.Fatalf("delta does not end at the frontier: %#v", delta.Durable)
	}
	if len(delta.WaitingOnYou) != 1 {
		t.Fatalf("delta should still say what waits on me: %#v", delta.WaitingOnYou)
	}
}

func TestWaitDeltaBoundsAFarBehindCursorAndSaysWhatItSkipped(t *testing.T) {
	status := sampleStatus(500)
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: 0}}}
	delta := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", false)
	if len(delta.Durable) != deltaCap {
		t.Fatalf("delta not capped: %d events", len(delta.Durable))
	}
	if delta.Skipped != status.Durable.Depth-deltaCap {
		t.Fatalf("skipped count = %d, want %d", delta.Skipped, status.Durable.Depth-deltaCap)
	}
	// The omission must be visible in the human-readable line too.
	if !strings.Contains(summarize("wait", delta), "omitted") {
		t.Fatalf("summary hides the omission: %q", summarize("wait", delta))
	}
}

func TestWaitDeltaPreservesResetAndDegraded(t *testing.T) {
	status := sampleStatus(5)
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: status.Durable.Depth}}}
	delta := digestWait(service.WaitResponse{Status: status, Reset: true}, requested, mine, "me", false)
	if !delta.Reset {
		t.Fatal("reset was dropped")
	}
	degraded := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", true)
	if degraded.Cursor.Live.Generation != "degraded" {
		t.Fatalf("degraded wait did not mark the live cursor: %#v", degraded.Cursor.Live)
	}
}

// The text block used to be a second copy of the structured payload, which
// doubled every response. It must stay a summary.
func TestSummaryDoesNotRestateTheStructuredPayload(t *testing.T) {
	digest := digestStatus(sampleStatus(200), mine, "me", false)
	text := summarize("status", digest)
	structured, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(text)*4 > len(structured) {
		t.Fatalf("summary %d bytes is too close to the payload %d bytes", len(text), len(structured))
	}
	for _, want := range []string{"depth", "waiting on you"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary %q lacks %q", text, want)
		}
	}
}

// An act that failed to take force must surface whatever kind it was. Ratify
// and supersede are not statements, and joining only through statements hid
// exactly the authority failures an agent most needs to see.
func TestAttentionCoversRatifyAndSupersedeNotJustStatements(t *testing.T) {
	status := sampleStatus(5)
	projection := &status.Durable.Projection
	projection.Acts = append(projection.Acts,
		workroom.Act{Event: "act:my-ratify", Actor: mine, Type: "ratify", Target: "something",
			Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"},
		workroom.Act{Event: "act:their-ratify", Actor: theirs, Type: "ratify", Target: "other",
			Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"})
	projection.Decisions = append(projection.Decisions,
		workroom.Decision{Event: "act:my-ratify", Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"},
		workroom.Decision{Event: "act:their-ratify", Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"})

	digest := digestStatus(status, mine, "me", false)
	var found *eventView
	for index, view := range digest.YourAttention {
		if view.Event == "act:my-ratify" {
			found = &digest.YourAttention[index]
		}
		if view.Event == "act:their-ratify" {
			t.Fatal("another actor's failed act appeared in my attention")
		}
	}
	if found == nil {
		t.Fatalf("my failed ratify is invisible: %#v", digest.YourAttention)
	}
	if found.Kind != "ratify" || found.Target != "something" {
		t.Fatalf("failed act does not say what it was or what it aimed at: %#v", *found)
	}
}

// A commitment nobody can discharge is not work waiting on someone. Reporting
// it as such is the projection lying in the quiet direction.
func TestNonActionableCommitmentsAreSeparatedFromWaiting(t *testing.T) {
	status := sampleStatus(5)
	projection := &status.Durable.Projection
	projection.Commitments = append(projection.Commitments,
		workroom.Commitment{Request: "request:stale", Requester: theirs, Performer: mine, Status: "stale", WaitingOn: mine},
		workroom.Commitment{Request: "request:reneged", Requester: mine, Performer: theirs, Status: "reneged", WaitingOn: theirs})

	digest := digestStatus(status, mine, "me", false)
	for _, view := range digest.WaitingOnYou {
		if view.Status == "stale" || view.Status == "reneged" {
			t.Fatalf("a non-actionable commitment is presented as waiting on me: %#v", view)
		}
	}
	for _, view := range digest.YouAreWaiting {
		if view.Status == "stale" || view.Status == "reneged" {
			t.Fatalf("a non-actionable commitment is presented as awaited: %#v", view)
		}
	}
	// Separated, not discarded: both must still be visible.
	seen := map[string]bool{}
	for _, view := range digest.NotActionable {
		seen[view.Request] = true
	}
	if !seen["request:stale"] || !seen["request:reneged"] {
		t.Fatalf("non-actionable commitments were dropped rather than separated: %#v", digest.NotActionable)
	}
	if !strings.Contains(summarize("status", digest), "not actionable") {
		t.Fatalf("summary hides them: %q", summarize("status", digest))
	}

	// The delta must apply the same rule, or wait would reintroduce the lie.
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: status.Durable.Depth}}}
	delta := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", false)
	for _, view := range delta.WaitingOnYou {
		if view.Status == "stale" {
			t.Fatalf("wait reintroduced a stale commitment as waiting: %#v", view)
		}
	}
}

// Ineffective and disputed are distinct verdicts; the totals must not collapse
// a distinction the fold keeps deliberately.
func TestTotalsKeepIneffectiveAndDisputedApart(t *testing.T) {
	status := sampleStatus(3)
	projection := &status.Durable.Projection
	projection.Decisions = append(projection.Decisions,
		workroom.Decision{Event: "d:disputed", Verdict: workroom.Disputed, Reason: "duplicate event id"})
	digest := digestStatus(status, mine, "me", false)
	if digest.Totals.DisputedActs != 1 {
		t.Fatalf("disputed acts = %d, want 1", digest.Totals.DisputedActs)
	}
	// The sample carries two ineffective decisions of its own; the disputed one
	// added here must not be counted among them.
	if digest.Totals.IneffectiveActs != 2 {
		t.Fatalf("ineffective acts = %d, want 2; disputed appears to have been folded in", digest.Totals.IneffectiveActs)
	}
}
