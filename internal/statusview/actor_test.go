package statusview

import (
	"bytes"
	"encoding/json"
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

func TestActorStatusCarriesStaleQualifierWithoutChangingLanes(t *testing.T) {
	digest := BuildActorStatus(actorQualifierSnapshot(), nexus.Snapshot{}, Cursor{}, me, "me", true)
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
	settled := findCommitmentView(digest.NotActionable, "request:satisfied-stale")
	if settled == nil || settled.Status != "satisfied" || !settled.Stale {
		t.Fatalf("a stale satisfied commitment was skipped or lost its outcome: %#v", digest.NotActionable)
	}
	if findCommitmentView(digest.NotActionable, "request:satisfied-clean") != nil {
		t.Fatalf("a settled commitment with nothing wrong was listed: %#v", digest.NotActionable)
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
	delta := BuildWait(actorQualifierSnapshot(), Cursor{}, nil, false, Cursor{}, me, "me", true)
	stale := findCommitmentView(delta.CurrentWaitingOnYou, "request:reported-stale")
	if stale == nil || stale.Status != "reported" || !stale.Stale {
		t.Fatalf("the delta dropped the qualifier or the lane: %#v", delta.CurrentWaitingOnYou)
	}
	settled := findCommitmentView(delta.CurrentNotActionable, "request:satisfied-stale")
	if settled == nil || settled.Status != "satisfied" || !settled.Stale {
		t.Fatalf("the delta skipped a stale satisfied commitment: %#v", delta.CurrentNotActionable)
	}
	if findCommitmentView(delta.CurrentNotActionable, "request:satisfied-clean") != nil {
		t.Fatalf("a settled commitment with nothing wrong was listed: %#v", delta.CurrentNotActionable)
	}
	if delta.Totals.StaleCommitments["satisfied"] != 1 {
		t.Fatalf("delta totals cannot identify stale commitments: %#v", delta.Totals)
	}
}
