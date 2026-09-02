package statusview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	me   = "fingerprint:me"
	them = "fingerprint:them"
)

// actorQualifierSnapshot puts one of each interesting pair in front of me: a
// report I owe a ratification on, and a finished commitment whose basis moved.
func actorQualifierSnapshot() app.Snapshot {
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{me: {Name: "me"}, them: {Name: "them"}},
		Statements: []workroom.Statement{
			{Event: "request:reported-stale", Actor: me, Kind: workroom.KindRequest, Text: "moved under the report"},
			{Event: "request:reported-clean", Actor: me, Kind: workroom.KindRequest, Text: "still standing"},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:reported-stale", Requester: me, Performer: them, Status: "reported", Stale: true, WaitingOn: me},
			{Request: "request:reported-clean", Requester: me, Performer: them, Status: "reported", WaitingOn: me},
			{Request: "request:satisfied-stale", Requester: me, Performer: them, Status: "satisfied", Stale: true},
			{Request: "request:satisfied-clean", Requester: me, Performer: them, Status: "satisfied"},
		},
	}
	return app.Snapshot{Genesis: "genesis", Head: "head", Depth: 1, Projection: projection}
}

func findCommitmentView(items []CommitmentView, request string) *CommitmentView {
	for index := range items {
		if items[index].Request == request {
			return &items[index]
		}
	}
	return nil
}

func ratificationSnapshot() app.Snapshot {
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{
			me:   {Name: "me", Roles: []string{"participant", "ratifier"}},
			them: {Name: "them", Roles: []string{"participant"}},
		},
		Statements: []workroom.Statement{
			{Event: "proposal:open", Sequence: 1, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier", Text: "adopt open"},
			{Event: "proposal:stale", Sequence: 2, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier", Text: "adopt stale", Stale: true},
			{Event: "proposal:ratified", Sequence: 3, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier", Ratified: true},
			{Event: "proposal:retired", Sequence: 4, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier", Retired: true},
			{Event: "proposal:other-role", Sequence: 5, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:steward"},
			{Event: "proposal:refused", Sequence: 6, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier"},
			{Event: "proposal:dissented", Sequence: 7, Actor: them, Kind: workroom.KindPropose, Satisfier: "role:ratifier"},
			{Event: "dissent", Sequence: 8, Actor: me, Kind: workroom.KindDissent, Satisfier: workroom.SatisfierNone},
		},
		Provenance: map[string][]string{"dissent": {"proposal:dissented"}},
	}
	for _, statement := range projection.Statements {
		verdict := workroom.Effective
		if statement.Event == "proposal:refused" {
			verdict = workroom.Ineffective
		}
		projection.Decisions = append(projection.Decisions, workroom.Decision{Event: statement.Event, Sequence: statement.Sequence, Verdict: verdict})
	}
	return app.Snapshot{Genesis: "genesis", Head: "head", Depth: len(projection.Decisions), Projection: projection}
}

func TestActorStatusWaitAndWorkExposeOnlyStandingRoleRatifications(t *testing.T) {
	snapshot := ratificationSnapshot()
	digest := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", true)
	if len(digest.AwaitingRatification) != 2 || digest.AwaitingRatification[0].Event != "proposal:open" ||
		digest.AwaitingRatification[1].Event != "proposal:stale" || !digest.AwaitingRatification[1].Stale {
		t.Fatalf("awaiting ratification = %#v", digest.AwaitingRatification)
	}
	if summary := Summarize("status", digest); !strings.Contains(summary, "2 awaiting your ratification") {
		t.Fatalf("status summary hides ratification duty: %q", summary)
	}

	delta := BuildWait(snapshot, Cursor{}, nil, false, Cursor{}, nil, me, "me", true)
	if len(delta.CurrentAwaitingRatification) != 2 || delta.CurrentAwaitingRatification[1].Event != "proposal:stale" {
		t.Fatalf("wait ratification lane = %#v", delta.CurrentAwaitingRatification)
	}
	if summary := Summarize("wait", delta); !strings.Contains(summary, "2 awaiting your ratification") {
		t.Fatalf("wait summary hides ratification duty: %q", summary)
	}

	page, err := BuildWorkPage(snapshot, WorkQuery{Actor: me, Lanes: []WorkLane{LaneAwaitingRatification}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Event != "proposal:stale" || page.Items[0].Request != "" ||
		page.Items[0].Lane != LaneAwaitingRatification || page.Items[0].Status != "awaiting-ratification" || page.Items[0].Author == nil || page.Items[0].Author.Fingerprint != them {
		t.Fatalf("work ratification lane invented commitment fields or lost proposal fields: %#v", page.Items)
	}
	fresh, err := BuildWorkPage(snapshot, WorkQuery{Actor: me, Lanes: []WorkLane{LaneAwaitingRatification},
		Statuses: []string{"awaiting-ratification"}, Stale: StaleExclude, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Items) != 1 || fresh.Items[0].Event != "proposal:open" {
		t.Fatalf("stale exclusion did not preserve only the current proposal: %#v", fresh.Items)
	}
	moved, err := BuildWorkPage(snapshot, WorkQuery{Actor: me, Lanes: []WorkLane{LaneAwaitingRatification},
		Stale: StaleOnly, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Items) != 1 || moved.Items[0].Event != "proposal:stale" || !moved.Items[0].Stale {
		t.Fatalf("stale-only ratification selection = %#v", moved.Items)
	}

	current, err := BuildWorkPage(snapshot, WorkQuery{Actor: them, Lanes: []WorkLane{LaneAwaitingRatification}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if current.MatchingTotal != 0 {
		t.Fatalf("actor without satisfier role received ratification work: %#v", current.Items)
	}
}

func TestActorStatusAndWaitExposeOpenAddressedWorkWithoutInventingAPromise(t *testing.T) {
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{me: {Name: "me"}, them: {Name: "them"}},
		Statements: []workroom.Statement{
			{Event: "request:mine", Actor: them, Kind: workroom.KindRequest, Text: "available to me"},
			{Event: "request:theirs", Actor: me, Kind: workroom.KindRequest, Text: "available to them"},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:mine", Requester: them, AddressedTo: me, Status: "open"},
			{Request: "request:theirs", Requester: me, AddressedTo: them, Status: "open"},
		},
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 2, Projection: projection}

	digest := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", true)
	if len(digest.AvailableToYou) != 1 {
		t.Fatalf("available_to_you = %#v", digest.AvailableToYou)
	}
	available := digest.AvailableToYou[0]
	if available.Request != "request:mine" || available.Status != "open" || available.AddressedTo != "me" || available.Performer != "" || available.Promise != "" || available.Text != "available to me" {
		t.Fatalf("available request invented or lost responsibility: %#v", available)
	}
	if len(digest.WaitingOnYou) != 0 {
		t.Fatalf("an unclaimed request was presented as promised work: %#v", digest.WaitingOnYou)
	}
	if summary := Summarize("status", digest); !strings.Contains(summary, "1 addressed to you") {
		t.Fatalf("status summary hides available work: %q", summary)
	}

	delta := BuildWait(snapshot, Cursor{}, nil, false, Cursor{}, nil, me, "me", true)
	if len(delta.CurrentAvailableToYou) != 1 || delta.CurrentAvailableToYou[0] != available {
		t.Fatalf("wait does not preserve the current available lane: %#v", delta.CurrentAvailableToYou)
	}
	if summary := Summarize("wait", delta); !strings.Contains(summary, "1 addressed to you") {
		t.Fatalf("wait summary hides available work: %q", summary)
	}
}

// An artifact completion waits on its performer, who signs the merge of the
// approved head. Both fold shapes are covered: a direct completion with no
// promise, and a promised one. The row lands in the performer's waiting_on_you
// lane and the requester's you_are_waiting_on lane, identically in gs status
// and gs work, and names the performer as the waiting party.
func TestArtifactCompletionWaitsOnItsPerformer(t *testing.T) {
	for _, shape := range []struct {
		name    string
		promise string
	}{{name: "direct completion"}, {name: "promised", promise: "promise:implementation"}} {
		t.Run(shape.name, func(t *testing.T) {
			projection := workroom.Projection{
				Actors: map[string]workroom.ActorState{me: {Name: "me"}, them: {Name: "them"}},
				Statements: []workroom.Statement{
					{Event: "request:implementation", Actor: me, Kind: workroom.KindRequest, Text: "implement it"},
					{Event: "promise:implementation", Actor: them, Kind: workroom.KindPromise, Text: "I will"},
					{Event: "artifact:implementation", Actor: them, Kind: workroom.KindArtifact, Text: "exact head"},
				},
				Commitments: []workroom.Commitment{{
					Request: "request:implementation", Requester: me, Performer: them, Promise: shape.promise,
					Report: "artifact:implementation", Status: "awaiting-merge", WaitingOn: them,
				}},
			}
			snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 3, Projection: projection}

			performer := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, them, "them", true)
			if row := findCommitmentView(performer.WaitingOnYou, "request:implementation"); row == nil || row.Status != "awaiting-merge" {
				t.Fatalf("artifact completion is not in the performer's queue: %#v", performer.WaitingOnYou)
			}
			requester := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", true)
			if len(requester.WaitingOnYou) != 0 {
				t.Fatalf("artifact completion was assigned to the requester: %#v", requester.WaitingOnYou)
			}
			if row := findCommitmentView(requester.YouAreWaiting, "request:implementation"); row == nil || row.Status != "awaiting-merge" || row.Performer != "them" {
				t.Fatalf("requester is not shown waiting on the performer: %#v", requester.YouAreWaiting)
			}

			for actor, want := range map[string]WorkLane{them: LaneWaitingOnYou, me: LaneYouAreWaitingOn} {
				page, err := BuildWorkPage(snapshot, WorkQuery{Actor: actor, Statuses: []string{"awaiting-merge"}, Limit: 10}, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != 1 || page.Items[0].Lane != want || page.Items[0].WaitingOn == nil || page.Items[0].WaitingOn.Fingerprint != them {
					t.Fatalf("work lane for %s = %#v, want %s waiting on the performer", actor, page.Items, want)
				}
			}
		})
	}
}

func TestActorStatusAndWaitExposeStaleUnclaimedAddressedWork(t *testing.T) {
	conditions := strings.Repeat("still required ", 40)
	claimedConditions := strings.Repeat("already accepted ", 40)
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{me: {Name: "me"}, them: {Name: "them"}},
		Statements: []workroom.Statement{
			{Event: "request:unclaimed", Actor: them, Kind: workroom.KindRequest, Text: "still owed", Body: map[string]string{"conditions": conditions}},
			{Event: "request:claimed", Actor: them, Kind: workroom.KindRequest, Text: "already claimed", Body: map[string]string{"conditions": claimedConditions}},
			{Event: "request:closed", Actor: them, Kind: workroom.KindRequest, Text: "already done"},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:unclaimed", Requester: them, AddressedTo: me, Status: "stale", Stale: true},
			{Request: "request:claimed", Requester: them, AddressedTo: me, Performer: me, Promise: "promise:claimed", Status: "stale", Stale: true},
			{Request: "request:closed", Requester: them, AddressedTo: me, Performer: me, Status: "satisfied", Stale: true},
		},
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 3, Projection: projection}

	digest := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", true)
	if len(digest.AvailableToYou) != 1 {
		t.Fatalf("available_to_you = %#v", digest.AvailableToYou)
	}
	available := digest.AvailableToYou[0]
	if available.Request != "request:unclaimed" || available.Status != "stale" || !available.Stale ||
		available.AddressedTo != "me" || available.Performer != "" || available.Promise != "" ||
		available.Conditions != conditions || len(available.Conditions) <= TextCap {
		t.Fatalf("stale unclaimed request lost its responsibility or qualifier: %#v", available)
	}
	claimed := findCommitmentView(digest.NotActionable, "request:claimed")
	if claimed == nil {
		t.Fatalf("claimed stale request changed lanes: %#v", digest.NotActionable)
	}
	if claimed.Conditions != "" {
		t.Fatalf("claimed stale request was enriched as unclaimed: %#v", *claimed)
	}
	if findCommitmentView(digest.NotActionable, "request:closed") != nil {
		t.Fatalf("closed stale request returned to a live lane: %#v", digest.NotActionable)
	}

	delta := BuildWait(snapshot, Cursor{}, nil, false, Cursor{}, nil, me, "me", true)
	if len(delta.CurrentAvailableToYou) != 1 || delta.CurrentAvailableToYou[0] != available {
		t.Fatalf("wait does not preserve the stale available lane: %#v", delta.CurrentAvailableToYou)
	}
	if findCommitmentView(delta.CurrentNotActionable, "request:claimed") == nil {
		t.Fatalf("wait changed the claimed stale request's lane: %#v", delta.CurrentNotActionable)
	}
	if findCommitmentView(delta.CurrentNotActionable, "request:closed") != nil {
		t.Fatalf("wait returned closed stale work to a live lane: %#v", delta.CurrentNotActionable)
	}

	page, err := BuildWorkPage(snapshot, WorkQuery{Actor: me, Lanes: []WorkLane{LaneAvailable}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Request != "request:unclaimed" || page.Items[0].Lane != LaneAvailable ||
		page.Items[0].Status != "stale" || !page.Items[0].Stale || page.Items[0].Conditions != conditions {
		t.Fatalf("work disagrees with status and wait about stale unclaimed intake: %#v", page.Items)
	}
	claimedPage, err := BuildWorkPage(snapshot, WorkQuery{Actor: me, Lanes: []WorkLane{LaneNotActionable}, Statuses: []string{"stale"}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedPage.Items) != 1 || claimedPage.Items[0].Request != "request:claimed" || claimedPage.Items[0].Conditions != "" {
		t.Fatalf("work changed or enriched claimed stale intake: %#v", claimedPage.Items)
	}
}

func TestActorStatusCarriesStaleQualifierWithoutChangingLanes(t *testing.T) {
	digest := BuildActorStatus(actorQualifierSnapshot(), nexus.Snapshot{}, Cursor{}, nil, me, "me", true)
	stale := findCommitmentView(digest.WaitingOnYou, "request:reported-stale")
	if stale == nil {
		t.Fatalf("a stale report stopped waiting on me: %#v", digest.WaitingOnYou)
	}
	if stale.Status != "reported" || !stale.Stale {
		t.Fatalf("stale report lost its status or its qualifier: %#v", *stale)
	}
	if clean := findCommitmentView(digest.WaitingOnYou, "request:reported-clean"); clean == nil || clean.Stale {
		t.Fatalf("an unqualified report was marked stale: %#v", clean)
	}
	// Ordinary staleness under a finished commitment is history: it blocks
	// nothing, and a lane full of it hid the rows that are still owed. The
	// totals below keep the fact.
	for _, request := range []string{"request:satisfied-stale", "request:satisfied-clean"} {
		if findCommitmentView(digest.NotActionable, request) != nil {
			t.Fatalf("a finished commitment was listed in a lane: %s in %#v", request, digest.NotActionable)
		}
	}
	if digest.Totals.StaleCommitments["reported"] != 1 || digest.Totals.StaleCommitments["satisfied"] != 1 {
		t.Fatalf("actor totals cannot identify stale commitments: %#v", digest.Totals)
	}
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"stale":true`)) {
		t.Fatalf("the actor JSON does not carry the qualifier at all:\n%s", encoded)
	}
}

// wait must apply the same rule, or following the room would quietly undo what
// status just said.
func TestWaitDeltaCarriesStaleQualifierWithoutChangingLanes(t *testing.T) {
	delta := BuildWait(actorQualifierSnapshot(), Cursor{}, nil, false, Cursor{}, nil, me, "me", true)
	stale := findCommitmentView(delta.CurrentWaitingOnYou, "request:reported-stale")
	if stale == nil || stale.Status != "reported" || !stale.Stale {
		t.Fatalf("the delta dropped the qualifier or the lane: %#v", delta.CurrentWaitingOnYou)
	}
	for _, request := range []string{"request:satisfied-stale", "request:satisfied-clean"} {
		if findCommitmentView(delta.CurrentNotActionable, request) != nil {
			t.Fatalf("the delta listed a finished commitment: %s in %#v", request, delta.CurrentNotActionable)
		}
	}
	if delta.Totals.StaleCommitments["satisfied"] != 1 {
		t.Fatalf("delta totals cannot identify stale commitments: %#v", delta.Totals)
	}
}

func BenchmarkActorStatusAtDepth500000(b *testing.B) {
	const depth = 500_000
	projection := workroom.Projection{
		Decisions:  make([]workroom.Decision, depth),
		Statements: make([]workroom.Statement, depth),
		Actors:     map[string]workroom.ActorState{me: {Name: "me", Roles: []string{"participant"}}},
	}
	for index := range depth {
		event := fmt.Sprintf("event:%06d", index)
		projection.Decisions[index] = workroom.Decision{Event: event, Sequence: index + 1, Verdict: workroom.Effective}
		projection.Statements[index] = workroom.Statement{Event: event, Sequence: index + 1, Actor: me, Kind: workroom.KindAssert, Text: "linear history"}
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: depth, Projection: projection}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		status := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", false)
		body, err := json.Marshal(status)
		if err != nil {
			b.Fatal(err)
		}
		if len(body) > 1<<20 {
			b.Fatalf("bounded actor status = %d bytes, want at most 1 MiB", len(body))
		}
		b.ReportMetric(float64(len(body)), "response_bytes")
	}
}

// TestActorStatusKeepsYourOwnUnclaimedRequestOnTheBoard pins the lane-drop
// repair. A request you filed and addressed to someone else, which nobody has
// promised yet, carries an actionable status and an empty WaitingOn because the
// fold sets Performer and WaitingOn only once a promise takes force. Before the
// repair it matched no branch and was appended to no lane at all — not even
// not_actionable — so its author could not see it on their own board.
func TestActorStatusKeepsYourOwnUnclaimedRequestOnTheBoard(t *testing.T) {
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{me: {Name: "me"}, them: {Name: "them"}},
		Statements: []workroom.Statement{
			{Event: "request:unclaimed-by-them", Actor: me, Kind: workroom.KindRequest, Text: "filed by me, nobody has claimed it"},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:unclaimed-by-them", Requester: me, AddressedTo: them, Status: "open"},
		},
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 1, Projection: projection}

	digest := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, me, "me", true)

	total := len(digest.AvailableToYou) + len(digest.WaitingOnYou) + len(digest.YouAreWaiting) + len(digest.NotActionable)
	if total != 1 {
		t.Fatalf("the row reached %d lanes, want exactly one: available=%#v waitingOnYou=%#v youAreWaiting=%#v notActionable=%#v",
			total, digest.AvailableToYou, digest.WaitingOnYou, digest.YouAreWaiting, digest.NotActionable)
	}
	if len(digest.YouAreWaiting) != 1 {
		t.Fatalf("your own unclaimed request is not in the waiting lane: %#v", digest.YouAreWaiting)
	}
	waiting := digest.YouAreWaiting[0]
	if waiting.Request != "request:unclaimed-by-them" || waiting.Status != "open" {
		t.Fatalf("row lost its identity or status: %#v", waiting)
	}
	if waiting.Performer != "" || waiting.Promise != "" {
		t.Fatalf("classification invented a commitment the fold has not recorded: %#v", waiting)
	}
	if len(digest.AvailableToYou) != 0 {
		t.Fatalf("a request addressed to someone else was offered to its author to claim: %#v", digest.AvailableToYou)
	}
}
