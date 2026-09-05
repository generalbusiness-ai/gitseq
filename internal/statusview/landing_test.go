package statusview

import (
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestApprovedLandingLanePreservesDebtAndLegacyHistory(t *testing.T) {
	snapshot := workSnapshot(0)
	snapshot.Projection.Commitments = []workroom.Commitment{
		{Request: "legacy", Requester: otherActor, Performer: queryActor, Status: "satisfied", Stale: true, Legacy: true, TargetRepo: "repo", TargetRef: "refs/heads/release", Approval: "approval-legacy", Candidate: "candidate-legacy", ApprovedNotLanded: true},
		{Request: "held", Requester: otherActor, Performer: otherActor, HoldOwner: queryActor, WaitingOn: queryActor, Status: "awaiting-authorization", TargetRepo: "repo", TargetRef: "refs/heads/release", Approval: "approval-held", Candidate: "candidate-held", ApprovedNotLanded: true},
		{Request: "other-target", Performer: queryActor, Status: "awaiting-landing", WaitingOn: queryActor, TargetRepo: "repo", TargetRef: "refs/heads/main", ApprovedNotLanded: true},
		{Request: "not-owner", Requester: queryActor, Performer: otherActor, Status: "awaiting-landing", TargetRef: "refs/heads/release", ApprovedNotLanded: true},
		{Request: "landed", Performer: queryActor, Status: "satisfied", TargetRef: "refs/heads/release", Terminal: "landed"},
	}
	query := WorkQuery{Actor: queryActor, Lanes: []WorkLane{LaneApprovedNotLanded}, TargetRef: "refs/heads/release", Limit: 1}
	page, err := BuildWorkPage(snapshot, query, false)
	if err != nil || page.MatchingTotal != 2 || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("bounded target selection: %+v, %v", page, err)
	}
	held := page.Items[0]
	if held.Request != "held" || held.Status != "awaiting-authorization" || held.WaitingOn == nil || held.WaitingOn.Fingerprint != queryActor || held.HoldOwner != queryActor || !held.ApprovedNotLanded {
		t.Fatalf("lane selection changed waiting party or lost target facts: %+v", held)
	}
	query.Cursor = page.NextCursor
	page, err = BuildWorkPage(snapshot, query, false)
	if err != nil || len(page.Items) != 1 || page.Items[0].Request != "legacy" || !page.Items[0].Legacy || page.Items[0].Status != "satisfied" || page.Items[0].Approval != "approval-legacy" {
		t.Fatalf("legacy closed row disappeared from the delivery debt: %+v, %v", page, err)
	}
	query.TargetRef = "refs/heads/main"
	if _, err := BuildWorkPage(snapshot, query, false); err == nil {
		t.Fatal("cursor accepted a changed target filter")
	}
	query.TargetRef = "refs/heads/release"
	no := false
	query.ApprovedNotLanded = &no
	if _, err := BuildWorkPage(snapshot, query, false); err == nil {
		t.Fatal("cursor accepted a changed approval filter")
	}
	query.Cursor = ""
	query.Lanes = []WorkLane{LaneNotActionable}
	query.Statuses = []string{"satisfied"}
	page, err = BuildWorkPage(snapshot, query, false)
	if err != nil || page.MatchingTotal != 1 || page.Items[0].Request != "landed" {
		t.Fatalf("explicit false approval filter became absent: %+v, %v", page, err)
	}
	if got := Build(snapshot.Genesis, snapshot.Head, snapshot.Depth, snapshot.Projection).Totals.ApprovedNotLanded; got != 4 {
		t.Fatalf("global approved-not-landed count = %d", got)
	}
	if got := actorTotals(snapshot.Projection, snapshot.Depth).ApprovedNotLanded; got != 4 {
		t.Fatalf("actor status total differs from global total: %d", got)
	}
}
