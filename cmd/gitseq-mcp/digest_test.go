package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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
		workroom.Statement{Event: "request:available", Actor: theirs, Kind: workroom.KindRequest, Text: "available to me"},
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
		{Request: "request:available", Requester: theirs, AddressedTo: mine, Status: "open"},
		{Request: "request:for-me", Requester: theirs, Performer: mine, Status: "promised", WaitingOn: mine},
		{Request: "request:for-them", Requester: mine, Performer: theirs, Status: "promised", WaitingOn: theirs},
		{Request: "request:done", Requester: theirs, Performer: mine, Status: "satisfied"},
		{Request: "request:other", Requester: theirs, Performer: theirs, Status: "promised", WaitingOn: theirs},
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

	if len(digest.AvailableToYou) != 1 || digest.AvailableToYou[0].Request != "request:available" || digest.AvailableToYou[0].AddressedTo != "me" {
		t.Fatalf("available_to_you = %#v", digest.AvailableToYou)
	}
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
	if digest.PriorityChat.Available {
		t.Fatalf("degraded status invented an empty live inbox: %#v", digest.PriorityChat)
	}
}

func TestStatusAndWaitLeadWithBoundedPriorityEphemeralChat(t *testing.T) {
	status := sampleStatus(3)
	status.Inbox = &nexus.Inbox{Skipped: 2, Frames: []nexus.InboxFrame{{
		Actor: theirs, Text: strings.Repeat("review this ", 40), About: "event:1",
		Conversation: "eph:sha256:one", Sequence: 4, Recipients: []string{mine}, Thread: "eph:sha256:one:4",
	}}}
	digest := digestStatus(status, mine, "me", false)
	if !digest.PriorityChat.Available || len(digest.PriorityChat.Frames) != 1 || digest.PriorityChat.Frames[0].ActorName != "them" || digest.PriorityChat.Skipped != 2 {
		t.Fatalf("priority status = %+v", digest.PriorityChat)
	}
	if digest.PriorityChat.Frames[0].Text != status.Inbox.Frames[0].Text {
		t.Fatalf("priority chat lost signed inline text: %d of %d bytes", len(digest.PriorityChat.Frames[0].Text), len(status.Inbox.Frames[0].Text))
	}
	if summary := summarize("status", digest); !strings.HasPrefix(summary, "priority ephemeral chat: 1 unacknowledged, 2 additional pending") {
		t.Fatalf("priority summary is not first: %q", summary)
	}
	delta := digestWait(service.WaitResponse{Status: status}, status.Cursor, mine, "me", false)
	if len(delta.PriorityChat.Frames) != 1 || !strings.HasPrefix(summarize("wait", delta), "priority ephemeral chat: 1 unacknowledged") {
		t.Fatalf("priority wait = %+v; summary %q", delta.PriorityChat, summarize("wait", delta))
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
	if len(delta.CurrentWaitingOnYou) != 1 {
		t.Fatalf("delta should still say what waits on me: %#v", delta.CurrentWaitingOnYou)
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
		// The fold clears WaitingOn for reneged and cancelled commitments, so a
		// fixture that sets it would test a state that cannot occur — which is
		// how the previous version of this test passed while the code could
		// never surface a real reneging.
		workroom.Commitment{Request: "request:reneged", Requester: mine, Performer: theirs, Status: "reneged"},
		workroom.Commitment{Request: "request:cancelled", Requester: theirs, Performer: mine, Status: "cancelled"})

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
	if !seen["request:stale"] || !seen["request:reneged"] || !seen["request:cancelled"] {
		t.Fatalf("non-actionable commitments were dropped rather than separated: %#v", digest.NotActionable)
	}
	if !strings.Contains(summarize("status", digest), "not actionable") {
		t.Fatalf("summary hides them: %q", summarize("status", digest))
	}

	// The delta must apply the same rule, or wait would reintroduce the lie.
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: status.Durable.Depth}}}
	delta := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", false)
	for _, view := range delta.CurrentWaitingOnYou {
		if view.Status == "stale" {
			t.Fatalf("wait reintroduced a stale commitment as waiting: %#v", view)
		}
	}
	delivered := map[string]bool{}
	for _, view := range delta.CurrentNotActionable {
		delivered[view.Request] = true
	}
	if !delivered["request:stale"] || !delivered["request:reneged"] || !delivered["request:cancelled"] {
		t.Fatalf("wait dropped non-actionable commitments instead of carrying them: %#v", delta.CurrentNotActionable)
	}
}

// What the previous version of this file got wrong is worth stating, because
// the same shape of mistake is easy to repeat: it set the requested frontier
// to the current depth, appended commitments with no causal decisions, and
// then asserted that every current terminal row appeared — which a full
// current-state echo satisfies without delivering any transition at all. The
// assertion could not fail for the reason it was written.
//
// The honest property is the one below. Commitment lists in a delta are
// current state and are deliberately *not* cut at the cursor: the fold records
// no depth at which a commitment's status changed, so a commitment-level cut
// is not computable from a projection alone. Only Durable is a true delta.
// Pinning that here means a later change which quietly starts filtering — or
// quietly stops — has to come past this test and say so.
func TestWaitCommitmentListsAreCurrentStateAndOnlyDurableIsCut(t *testing.T) {
	status := sampleStatus(30)
	depth := status.Durable.Depth
	projection := &status.Durable.Projection
	projection.Commitments = append(projection.Commitments,
		workroom.Commitment{Request: "request:reneged", Requester: mine, Performer: theirs, Status: "reneged"})

	atFrontier := digestWait(service.WaitResponse{Status: status},
		service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: depth}}}, mine, "me", false)
	fromScratch := digestWait(service.WaitResponse{Status: status},
		service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: 0}}}, mine, "me", false)

	// Durable is genuinely cut: caught up sees nothing new, a fresh reader sees
	// history. If these were equal the cursor would not be doing anything.
	if len(atFrontier.Durable) != 0 {
		t.Fatalf("a caller at the frontier should receive no new events: %#v", atFrontier.Durable)
	}
	if len(fromScratch.Durable) == 0 {
		t.Fatalf("a caller from zero should receive history")
	}
	// The commitment lists are not cut, and must be identical across the two.
	if !sameCommitments(atFrontier.CurrentNotActionable, fromScratch.CurrentNotActionable) {
		t.Fatalf("commitment lists varied with the cursor: %#v vs %#v",
			atFrontier.CurrentNotActionable, fromScratch.CurrentNotActionable)
	}
	if !sameCommitments(atFrontier.CurrentWaitingOnYou, fromScratch.CurrentWaitingOnYou) {
		t.Fatalf("waiting lists varied with the cursor: %#v vs %#v",
			atFrontier.CurrentWaitingOnYou, fromScratch.CurrentWaitingOnYou)
	}
	if !sameCommitments(atFrontier.CurrentAvailableToYou, fromScratch.CurrentAvailableToYou) {
		t.Fatalf("available lists varied with the cursor: %#v vs %#v",
			atFrontier.CurrentAvailableToYou, fromScratch.CurrentAvailableToYou)
	}
	// And a caller sitting exactly at the frontier still learns its situation,
	// which is why they are carried at all.
	if len(atFrontier.CurrentNotActionable) == 0 {
		t.Fatal("a caught-up caller learned nothing about its terminal commitments")
	}
	// The JSON names must say "current", or a reader will take them for a cut.
	encoded, err := json.Marshal(atFrontier)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"current_available_to_you", "current_not_actionable", "current_waiting_on_you"} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("wire name %q missing, so the payload implies a cut it does not make: %s", field, encoded)
		}
	}
}

func sameCommitments(left, right []commitmentView) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// sampleTerminalStatus grows the dimension sampleStatus deliberately does not:
// commitment cardinality. Terminal commitments and an actor's own ineffective
// acts are the two things that accumulate and are never discharged, so they
// are the two an "is it bounded?" test has to scale. Padding decisions while
// holding commitments at four — which is what the earlier bounded test did —
// cannot detect an unbounded commitment list, and did not.
func sampleTerminalStatus(terminal int) service.Status {
	status := sampleStatus(4)
	projection := status.Durable.Projection
	for index := range terminal {
		request := fmt.Sprintf("request:stale-%d", index)
		failed := fmt.Sprintf("act:mine-failed-%d", index)
		projection.Statements = append(projection.Statements,
			workroom.Statement{Event: request, Actor: theirs, Kind: workroom.KindRequest, Text: "an old review that was withdrawn"},
			workroom.Statement{Event: failed, Actor: mine, Kind: workroom.KindPromise, Text: "another promise that did not take force"})
		projection.Decisions = append(projection.Decisions,
			workroom.Decision{Event: request, Verdict: workroom.Effective, Reason: "statement recorded"},
			workroom.Decision{Event: failed, Verdict: workroom.Ineffective, Reason: "dangling promise has no request"})
		// WaitingOn stays empty because the fold clears it for reneged
		// commitments; membership comes from requester/performer via involves.
		projection.Commitments = append(projection.Commitments,
			workroom.Commitment{Request: request, Requester: theirs, Performer: mine, Status: "reneged"})
	}
	status.Durable.Projection = projection
	status.Durable.Depth = len(projection.Decisions)
	status.Cursor.Frontier = []service.Frontier{{Genesis: "genesis", Head: "head", Depth: status.Durable.Depth}}
	return status
}

// The bound has to hold in the dimension the actor-oriented lists actually
// grow in. A workroom accrues terminal commitments and failed acts forever;
// without a cap, "what is my situation?" costs more every month while the
// answer stays the same size, which is the exact cost this view exists to
// remove.
func TestTerminalCommitmentsAndFailedActsAreBounded(t *testing.T) {
	// Under the cap, everything is listed and nothing is claimed skipped.
	small := digestStatus(sampleTerminalStatus(5), mine, "me", false)
	if len(small.NotActionable) != 5 || small.NotActionableSkipped != 0 {
		t.Fatalf("a short list must be complete: %d listed, %d skipped", len(small.NotActionable), small.NotActionableSkipped)
	}

	large := digestStatus(sampleTerminalStatus(500), mine, "me", false)
	if len(large.NotActionable) != listCap {
		t.Fatalf("terminal commitments are unbounded: %d listed", len(large.NotActionable))
	}
	if large.NotActionableSkipped != 500-listCap {
		t.Fatalf("skipped count = %d, want %d", large.NotActionableSkipped, 500-listCap)
	}
	// sampleStatus already contributes one ineffective act of mine.
	if len(large.YourAttention) != listCap || large.YourAttentionSkipped != 501-listCap {
		t.Fatalf("failed acts unbounded: %d listed, %d skipped", len(large.YourAttention), large.YourAttentionSkipped)
	}
	// Truncation that does not say so is worse than truncation. Both the wire
	// payload and the human-readable line must carry the omission.
	encoded, err := json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "not_actionable_skipped") {
		t.Fatalf("payload truncates silently: %s", encoded)
	}
	if line := summarize("status", large); !strings.Contains(line, fmt.Sprintf("%d of 500", listCap)) {
		t.Fatalf("summary states a truncated count as if it were the whole: %q", line)
	}

	// The size property, measured where it matters. Ten times the terminal
	// commitments must not mean ten times the answer: the digest may widen by
	// the digits its counters and request ids gained, and by nothing else.
	// The tolerance is generous on purpose — the discrimination that matters
	// is logarithmic growth against linear, and an uncapped list would add
	// hundreds of entries here, not tens of bytes.
	smallStatus, largeStatus := sampleTerminalStatus(100), sampleTerminalStatus(1000)
	hundred, err := json.Marshal(digestStatus(smallStatus, mine, "me", false))
	if err != nil {
		t.Fatal(err)
	}
	thousand, err := json.Marshal(digestStatus(largeStatus, mine, "me", false))
	if err != nil {
		t.Fatal(err)
	}
	if growth := len(thousand) - len(hundred); growth > 200 {
		t.Fatalf("digest grew %d bytes for ten times the terminal commitments: %d then %d", growth, len(hundred), len(thousand))
	}
	// …and the input has to have genuinely grown, or the assertion above is
	// measuring nothing. This is the guard the earlier bounded test lacked in
	// this dimension.
	smallFull, err := json.Marshal(smallStatus)
	if err != nil {
		t.Fatal(err)
	}
	largeFull, err := json.Marshal(largeStatus)
	if err != nil {
		t.Fatal(err)
	}
	if len(largeFull) < 5*len(smallFull) {
		t.Fatalf("the projection under test did not actually grow: %d then %d bytes", len(smallFull), len(largeFull))
	}

	// The delta carries the same lists, so it needs the same bound.
	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: 0}}}
	delta := digestWait(service.WaitResponse{Status: sampleTerminalStatus(500)}, requested, mine, "me", false)
	if len(delta.CurrentNotActionable) != listCap || delta.CurrentNotActionableSkipped != 500-listCap {
		t.Fatalf("wait is unbounded in terminal commitments: %d listed, %d skipped",
			len(delta.CurrentNotActionable), delta.CurrentNotActionableSkipped)
	}
}

func TestAvailableWorkIsBoundedInStatusAndWait(t *testing.T) {
	status := sampleStatus(4)
	projection := &status.Durable.Projection
	// sampleStatus contributes one available request of its own.
	for index := range 500 {
		request := fmt.Sprintf("request:available-%03d", index)
		projection.Statements = append(projection.Statements,
			workroom.Statement{Event: request, Actor: theirs, Kind: workroom.KindRequest, Text: "available work"})
		projection.Commitments = append(projection.Commitments,
			workroom.Commitment{Request: request, Requester: theirs, AddressedTo: mine, Status: "open"})
	}

	digest := digestStatus(status, mine, "me", false)
	if len(digest.AvailableToYou) != listCap || digest.AvailableToYouSkipped != 501-listCap {
		t.Fatalf("available work is unbounded or silently omitted: %d listed, %d skipped", len(digest.AvailableToYou), digest.AvailableToYouSkipped)
	}
	if summary := summarize("status", digest); !strings.Contains(summary, fmt.Sprintf("%d of 501 addressed to you", listCap)) {
		t.Fatalf("status summary hides the complete available count: %q", summary)
	}

	delta := digestWait(service.WaitResponse{Status: status}, service.Cursor{}, mine, "me", false)
	if len(delta.CurrentAvailableToYou) != listCap || delta.CurrentAvailableToSkipped != 501-listCap {
		t.Fatalf("wait available work is unbounded or silently omitted: %d listed, %d skipped", len(delta.CurrentAvailableToYou), delta.CurrentAvailableToSkipped)
	}
}

// A ratify or supersede arriving after the cursor must be recognisable. The
// status view was repaired for this; the delta was not, and a blank actor and
// kind is the same omission in the surface an agent polls most.
func TestWaitDeltaResolvesActsNotOnlyStatements(t *testing.T) {
	status := sampleStatus(4)
	projection := &status.Durable.Projection
	projection.Acts = append(projection.Acts, workroom.Act{
		Event: "act:late-ratify", Actor: mine, Type: "ratify", Target: "request:for-me",
		Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role", Text: "tried to ratify"})
	projection.Decisions = append(projection.Decisions, workroom.Decision{
		Event: "act:late-ratify", Verdict: workroom.Ineffective, Reason: "actor lacks ratifier role"})
	status.Durable.Depth = len(projection.Decisions)

	requested := service.Cursor{Frontier: []service.Frontier{{Genesis: "genesis", Depth: status.Durable.Depth - 1}}}
	delta := digestWait(service.WaitResponse{Status: status}, requested, mine, "me", false)
	if len(delta.Durable) != 1 {
		t.Fatalf("expected the one new event: %#v", delta.Durable)
	}
	view := delta.Durable[0]
	if view.Actor == "" || view.Kind != "ratify" || view.Target != "request:for-me" {
		t.Fatalf("a ratify arrived unrecognisable in the delta: %#v", view)
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

// Role names are not a closed vocabulary — a roster statement may confer any
// name — so an actor's role set grows with what the log has granted them and
// is bounded by nothing the substrate fixes. That makes it the same kind of
// list as the terminal commitments, and it was missed because the obvious
// reading of "roles" is the three or four the room's practice uses.
func TestYourOwnRolesAreBounded(t *testing.T) {
	status := sampleStatus(4)
	roles := make([]string, 0, 2001)
	for index := range 2001 {
		roles = append(roles, fmt.Sprintf("role-%04d", index))
	}
	sort.Strings(roles)
	actor := status.Durable.Projection.Actors[mine]
	actor.Roles = roles
	status.Durable.Projection.Actors[mine] = actor

	digest := digestStatus(status, mine, "me", false)
	if len(digest.You.Roles) != listCap {
		t.Fatalf("own roles are unbounded: %d listed", len(digest.You.Roles))
	}
	if digest.You.RolesSkipped != 2001-listCap {
		t.Fatalf("roles skipped = %d, want %d", digest.You.RolesSkipped, 2001-listCap)
	}
	if !containsRole(digest.You.Roles, roles[0]) {
		t.Fatalf("deterministic fill dropped the first role: %#v", digest.You.Roles)
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "roles_skipped") {
		t.Fatalf("truncated roles without saying so: %s", encoded)
	}
}

func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

// The previous cap kept the alphabetical head, on the stated grounds that the
// conventional roles sort early. They do not: any name a roster statement
// chooses can sort ahead of them, and the earlier fixture used role-NNNN,
// which happens to sort after "operator" and so masked the adversary
// completely. Role names are submitter-chosen, so the roles that carry
// authority have to be named rather than inferred from an ordering.
func TestRoleCapKeepsTheRolesThatCarryAuthority(t *testing.T) {
	roles := []string{}
	for index := range 20 {
		roles = append(roles, fmt.Sprintf("aaa-%04d", index))
	}
	roles = append(roles, "operator", "participant", "ratifier")
	sort.Strings(roles)

	status := sampleStatus(4)
	actor := status.Durable.Projection.Actors[mine]
	actor.Roles = roles
	status.Durable.Projection.Actors[mine] = actor
	digest := digestStatus(status, mine, "me", false)

	for _, role := range []string{"operator", "participant", "ratifier"} {
		if !containsRole(digest.You.Roles, role) {
			t.Fatalf("truncation dropped %q, which decides what the actor may do: %#v", role, digest.You.Roles)
		}
	}
	if len(digest.You.Roles) != listCap || digest.You.RolesSkipped != len(roles)-listCap {
		t.Fatalf("cap or count wrong: %d listed, %d skipped, %d total", len(digest.You.Roles), digest.You.RolesSkipped, len(roles))
	}
	// And the omission is stated in the line a human reads, not only on the wire.
	if line := summarize("status", digest); !strings.Contains(line, fmt.Sprintf("%d of %d roles", listCap, len(roles))) {
		t.Fatalf("summary hides the role truncation: %q", line)
	}
}
