package statusview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
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
