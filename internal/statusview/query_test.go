package statusview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	queryActor = "fingerprint:query-actor"
	otherActor = "fingerprint:other-actor"
)

func workSnapshot(count int) app.Snapshot {
	projection := workroom.Projection{Actors: map[string]workroom.ActorState{
		queryActor: {Name: "Codex", Roles: []string{"participant"}},
		otherActor: {Name: "Claude", Roles: []string{"participant"}},
	}}
	for index := 0; index < count; index++ {
		request := fmt.Sprintf("git:sha1:genesis#git:sha1:%040d", index)
		projection.Statements = append(projection.Statements, workroom.Statement{
			Event: request, Actor: otherActor, Kind: workroom.KindRequest,
			Text: strings.Repeat("bounded actor work ", 30),
		})
		projection.Decisions = append(projection.Decisions, workroom.Decision{Event: request, Verdict: workroom.Effective})
		projection.Commitments = append(projection.Commitments, workroom.Commitment{
			Request: request, Requester: otherActor, AddressedTo: queryActor, Status: "open",
		})
	}
	projection.Statements = append(projection.Statements,
		workroom.Statement{Event: "request:waiting", Actor: otherActor, Kind: workroom.KindRequest, Text: "waiting on me"},
		workroom.Statement{Event: "request:stale", Actor: queryActor, Kind: workroom.KindRequest, Text: "stale result"},
		workroom.Statement{Event: "request:finished", Actor: queryActor, Kind: workroom.KindRequest, Text: "finished result"},
		workroom.Statement{Event: "request:unrelated", Actor: otherActor, Kind: workroom.KindRequest, Text: "not my work"},
	)
	projection.Commitments = append(projection.Commitments,
		workroom.Commitment{Request: "request:waiting", Requester: otherActor, Performer: queryActor, Promise: "promise:waiting", WaitingOn: queryActor, Status: "promised"},
		workroom.Commitment{Request: "request:stale", Requester: queryActor, Performer: otherActor, Promise: "promise:stale", Report: "report:stale", Status: "satisfied", Stale: true},
		workroom.Commitment{Request: "request:finished", Requester: queryActor, Performer: otherActor, Promise: "promise:finished", Report: "report:finished", Status: "satisfied"},
		workroom.Commitment{Request: "request:unrelated", Requester: otherActor, AddressedTo: otherActor, Status: "open"},
	)
	return app.Snapshot{Genesis: "genesis", Head: "head-one", Depth: len(projection.Decisions), Projection: projection}
}

func TestWorkQueryDefaultsIncludeAddressedAndStaleWorkWithoutWaitingDebt(t *testing.T) {
	page, err := BuildWorkPage(workSnapshot(2), WorkQuery{Actor: queryActor, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if page.MatchingTotal != 4 || page.Returned != 4 || page.Remaining != 0 {
		t.Fatalf("unexpected counts: %+v", page)
	}
	lanes := make(map[string]WorkItem)
	for _, item := range page.Items {
		lanes[item.Request] = item
	}
	available := lanes["git:sha1:genesis#git:sha1:0000000000000000000000000000000000000001"]
	if available.Lane != LaneAvailable || available.Status != "open" || available.Performer != nil || available.WaitingOn != nil || available.AddressedTo == nil || available.AddressedTo.Fingerprint != queryActor {
		t.Fatalf("available work invented or lost responsibility: %+v", available)
	}
	if waiting := lanes["request:waiting"]; waiting.Lane != LaneWaitingOnYou || waiting.WaitingOn == nil || waiting.WaitingOn.Fingerprint != queryActor {
		t.Fatalf("promised work lost its waiting lane: %+v", waiting)
	}
	if stale := lanes["request:stale"]; stale.Lane != LaneNotActionable || !stale.Stale || stale.Status != "satisfied" {
		t.Fatalf("default query hid stale closed work: %+v", stale)
	}
	if _, included := lanes["request:unrelated"]; included {
		t.Fatal("default query included another actor's work")
	}
	if _, included := lanes["request:finished"]; included {
		t.Fatal("default query buried current work under settled non-stale history")
	}
	history, err := BuildWorkPage(workSnapshot(0), WorkQuery{Actor: queryActor, Statuses: []string{"satisfied"}, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	if history.MatchingTotal != 2 {
		t.Fatalf("explicit lifecycle filter did not recover settled history: %+v", history)
	}
}

func TestWorkQueryFiltersAndHeadBoundPaginationAreStable(t *testing.T) {
	snapshot := workSnapshot(125)
	query := WorkQuery{Actor: queryActor, Lanes: []WorkLane{LaneAvailable}, Statuses: []string{"open"}, Stale: StaleExclude, Limit: 25}
	first, err := BuildWorkPage(snapshot, query, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.MatchingTotal != 125 || first.Returned != 25 || first.Before != 0 || first.Remaining != 100 || first.NextCursor == "" {
		t.Fatalf("first page does not disclose its complete bounds: %+v", first)
	}
	query.Cursor = first.NextCursor
	second, err := BuildWorkPage(snapshot, query, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Before != 25 || second.Returned != 25 || second.Remaining != 75 || second.Items[0].Request == first.Items[0].Request {
		t.Fatalf("continuation did not advance stably: %+v", second)
	}

	moved := snapshot
	moved.Head = "head-two"
	if _, err := BuildWorkPage(moved, query, false); err == nil || !strings.Contains(err.Error(), "current head") {
		t.Fatalf("cursor silently crossed a frontier: %v", err)
	}
	changed := query
	changed.Lanes = []WorkLane{LaneNotActionable}
	if _, err := BuildWorkPage(snapshot, changed, false); err == nil || !strings.Contains(err.Error(), "filters") {
		t.Fatalf("cursor silently crossed filters: %v", err)
	}
}

func TestWorkQueryResponseIsBoundedBeforeProjectionSerialization(t *testing.T) {
	small := workSnapshot(100)
	large := workSnapshot(1000)
	smallPage, err := BuildWorkPage(small, WorkQuery{Actor: queryActor}, false)
	if err != nil {
		t.Fatal(err)
	}
	largePage, err := BuildWorkPage(large, WorkQuery{Actor: queryActor}, false)
	if err != nil {
		t.Fatal(err)
	}
	smallJSON, _ := json.Marshal(smallPage)
	largeJSON, _ := json.Marshal(largePage)
	if len(smallPage.Items) != WorkPageDefault || len(largePage.Items) != WorkPageDefault {
		t.Fatalf("default page is not bounded: %d and %d", len(smallPage.Items), len(largePage.Items))
	}
	if growth := len(largeJSON) - len(smallJSON); growth > 64 {
		t.Fatalf("ten times the projection grew the page by %d bytes: %d then %d", growth, len(smallJSON), len(largeJSON))
	}
	fullSmall, _ := json.Marshal(small)
	fullLarge, _ := json.Marshal(large)
	if len(fullLarge) < len(fullSmall)*5 {
		t.Fatalf("fixture did not materially grow: %d then %d", len(fullSmall), len(fullLarge))
	}
}

func TestStatusAndWorkRowsCarryActionableTriageFields(t *testing.T) {
	conditions := strings.Repeat("full condition ", 40)
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{queryActor: {Name: "Codex"}, otherActor: {Name: "Claude"}},
		Statements: []workroom.Statement{
			{Event: "request:open", Actor: otherActor, Kind: workroom.KindRequest, Text: "open", Body: map[string]string{"conditions": conditions}},
			{Event: "request:reported", Actor: queryActor, Kind: workroom.KindRequest, Text: "reported"},
			{Event: "report:complete", Actor: otherActor, Kind: workroom.KindReport, Body: map[string]string{"status": "complete", "head": "head-reviewed"}},
		},
		Commitments: []workroom.Commitment{
			{Request: "request:open", Requester: otherActor, AddressedTo: queryActor, Status: "open"},
			{Request: "request:reported", Requester: queryActor, Performer: otherActor, Report: "report:complete", WaitingOn: queryActor, Status: "reported"},
		},
		Reviews: []workroom.Review{
			{Report: "review:old", Head: "head-reviewed", Verdict: "changes-requested", Ratified: true},
			{Report: "review:latest", Head: "head-reviewed", Verdict: "approved", Ratified: false},
			{Report: "review:retired", Head: "head-reviewed", Verdict: "approved", Ratified: true, Retired: true, Stale: true},
		},
	}
	snapshot := app.Snapshot{Genesis: "genesis", Head: "head", Depth: 3, Projection: projection}
	page, err := BuildWorkPage(snapshot, WorkQuery{Actor: queryActor, Limit: 10}, false)
	if err != nil {
		t.Fatal(err)
	}
	byRequest := make(map[string]WorkItem)
	for _, item := range page.Items {
		byRequest[item.Request] = item
	}
	open := byRequest["request:open"]
	if open.Conditions != conditions || len(open.Conditions) <= TextCap {
		t.Fatalf("work row truncated or omitted open conditions: %#v", open)
	}
	reported := byRequest["request:reported"]
	if reported.ReportStatus != "complete" || reported.ReportedHead != "head-reviewed" || reported.LatestReview == nil ||
		reported.LatestReview.Report != "review:retired" || reported.LatestReview.Verdict != "approved" || !reported.LatestReview.Ratified ||
		!reported.LatestReview.Retired || !reported.LatestReview.Stale {
		t.Fatalf("work row cannot settle reported work without inspect: %#v", reported)
	}

	status := BuildActorStatus(snapshot, nexus.Snapshot{}, Cursor{}, nil, queryActor, "Codex", true)
	if len(status.AvailableToYou) != 1 || status.AvailableToYou[0].Conditions != conditions {
		t.Fatalf("status row lost full open conditions: %#v", status.AvailableToYou)
	}
	if len(status.WaitingOnYou) != 1 {
		t.Fatalf("reported status row is missing: %#v", status.WaitingOnYou)
	}
	statusReported := status.WaitingOnYou[0]
	if statusReported.ReportStatus != "complete" || statusReported.ReportedHead != "head-reviewed" || statusReported.LatestReview == nil ||
		statusReported.LatestReview.Report != "review:retired" || statusReported.LatestReview.Verdict != "approved" ||
		!statusReported.LatestReview.Ratified || !statusReported.LatestReview.Retired || !statusReported.LatestReview.Stale {
		t.Fatalf("status row cannot settle reported work without inspect: %#v", statusReported)
	}
}

func inspectionSnapshot() app.Snapshot {
	request, promise, artifact, report := "request", "promise", "artifact", "report"
	projection := workroom.Projection{
		Actors: map[string]workroom.ActorState{queryActor: {Name: "Codex"}, otherActor: {Name: "Claude"}},
		Statements: []workroom.Statement{
			{Event: request, Actor: otherActor, Kind: workroom.KindRequest, Text: "implement it"},
			{Event: promise, Actor: queryActor, Kind: workroom.KindPromise, Text: "I will"},
			{Event: artifact, Actor: queryActor, Kind: workroom.KindArtifact, Text: "exact head", Body: map[string]string{"commit": "abc", "path": "."}},
			{Event: report, Actor: queryActor, Kind: workroom.KindReport, Text: "ready", Body: map[string]string{"artifact": artifact}},
		},
		Decisions: []workroom.Decision{
			{Event: request, Verdict: workroom.Effective}, {Event: promise, Verdict: workroom.Effective},
			{Event: artifact, Verdict: workroom.Effective}, {Event: report, Verdict: workroom.Effective},
		},
		Commitments: []workroom.Commitment{{Request: request, Requester: otherActor, AddressedTo: queryActor, Performer: queryActor, Promise: promise, Report: report, WaitingOn: otherActor, Status: "reported"}},
		Artifacts:   []workroom.Artifact{{Event: artifact, Path: ".", Commit: "abc"}},
		Reviews:     []workroom.Review{{Report: "review-report", Reviewer: otherActor, Verdict: "approved", Head: "abc", Artifact: artifact, Independence: workroom.IndependenceIndependent}},
		Provenance: map[string][]string{
			promise: {request}, artifact: {promise}, report: {promise, artifact}, "review-report": {artifact},
		},
	}
	return app.Snapshot{Genesis: "genesis", Head: "head", Depth: 4, Projection: projection}
}

func TestExactInspectionReturnsDecisionCommitmentProvenanceAndReviewEvidence(t *testing.T) {
	result, err := BuildItemInspection(inspectionSnapshot(), "report", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Statement == nil || result.Statement.Event != "report" || result.Decision == nil || result.Decision.Verdict != workroom.Effective {
		t.Fatalf("exact item or decision missing: %+v", result)
	}
	if result.Commitment == nil || result.Commitment.Request != "request" || result.Commitment.Promise != "promise" || result.Commitment.Report != "report" {
		t.Fatalf("commitment chain is incomplete: %+v", result.Commitment)
	}
	if len(result.ProvenanceBases) != 2 || !containsString(result.ProvenanceBases, "artifact") {
		t.Fatalf("direct provenance missing: %+v", result.ProvenanceBases)
	}
	if len(result.RelatedArtifacts) != 1 || result.RelatedArtifacts[0].Event != "artifact" {
		t.Fatalf("review artifact missing: %+v", result.RelatedArtifacts)
	}
	if len(result.RelatedReviews) != 1 || result.RelatedReviews[0].Report != "review-report" {
		t.Fatalf("related review missing: %+v", result.RelatedReviews)
	}
	if _, err := BuildItemInspection(inspectionSnapshot(), "unknown", false); err == nil {
		t.Fatal("unknown exact event was accepted")
	}
}

func TestExactInspectionBoundsEveryRepeatedRelationshipAndCountsOmissions(t *testing.T) {
	snapshot := inspectionSnapshot()
	for index := 0; index < InspectLinkCap+5; index++ {
		artifact := fmt.Sprintf("artifact:%02d", index)
		review := fmt.Sprintf("review:%02d", index)
		snapshot.Projection.Provenance["report"] = append(snapshot.Projection.Provenance["report"], fmt.Sprintf("basis:%02d", index))
		snapshot.Projection.Provenance[artifact] = []string{"report"}
		snapshot.Projection.Provenance[review] = []string{"report"}
		snapshot.Projection.Artifacts = append(snapshot.Projection.Artifacts, workroom.Artifact{Event: artifact, Path: fmt.Sprintf("path/%02d", index), Commit: "abc"})
		snapshot.Projection.Reviews = append(snapshot.Projection.Reviews, workroom.Review{Report: review, Artifact: artifact, Verdict: "approved"})
	}

	result, err := BuildItemInspection(snapshot, "report", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ProvenanceBases) != InspectLinkCap || result.ProvenanceBasesOmitted != 7 {
		t.Fatalf("provenance bound is not exact: shown=%d omitted=%d", len(result.ProvenanceBases), result.ProvenanceBasesOmitted)
	}
	if len(result.RelatedArtifacts) != InspectLinkCap || result.RelatedArtifactsOmitted != 6 {
		t.Fatalf("artifact bound is not exact: shown=%d omitted=%d", len(result.RelatedArtifacts), result.RelatedArtifactsOmitted)
	}
	if result.RelatedArtifacts[0].Event != "artifact" {
		t.Fatalf("bounding displaced the artifact directly cited by the inspected event: %+v", result.RelatedArtifacts[0])
	}
	if len(result.RelatedReviews) != InspectLinkCap || result.RelatedReviewsOmitted != 6 {
		t.Fatalf("review bound is not exact: shown=%d omitted=%d", len(result.RelatedReviews), result.RelatedReviewsOmitted)
	}
}

func TestExactInspectionDoesNotCopyLargeChainOrUnrelatedStatements(t *testing.T) {
	baseline, err := BuildItemInspection(inspectionSnapshot(), "report", false)
	if err != nil {
		t.Fatal(err)
	}
	large := inspectionSnapshot()
	largeText := strings.Repeat("large context ", 20000)
	for index := range large.Projection.Statements {
		if large.Projection.Statements[index].Event != "report" {
			large.Projection.Statements[index].Text = largeText
			large.Projection.Statements[index].Body = map[string]string{"large": largeText}
		}
	}
	for index := 0; index < 1000; index++ {
		large.Projection.Statements = append(large.Projection.Statements, workroom.Statement{Event: fmt.Sprintf("unrelated:%04d", index), Text: largeText})
	}
	selected, err := BuildItemInspection(large, "report", false)
	if err != nil {
		t.Fatal(err)
	}
	baselineJSON, _ := json.Marshal(baseline)
	selectedJSON, _ := json.Marshal(selected)
	if len(selectedJSON) != len(baselineJSON) {
		t.Fatalf("large surrounding projection changed exact inspection size: %d then %d", len(baselineJSON), len(selectedJSON))
	}
}
