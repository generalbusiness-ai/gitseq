package workroom

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMergeDischargesReviewedArtifactOutsideRetirementPlan(t *testing.T) {
	for _, carried := range []bool{false, true} {
		t.Run(fmt.Sprintf("carried=%v", carried), func(t *testing.T) {
			fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, "refs/heads/main")
			last := &fixture.records[len(fixture.records)-1]
			var receipt State
			if err := json.Unmarshal(last.Payload, &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Body["merge_retirements"] = `{}`
			receipt.Body["merge_successors"] = `["internal/workroom/fold.go"]`
			receipt.Body["merge_changed_paths"] = `["internal/workroom/fold.go"]`
			receipt.Body["merge_left_live"] = `{}`
			if carried {
				receipt.Body["merge_left_live"] = fmt.Sprintf(`{%q:{"class":"carried"}}`, fixture.artifact)
			}
			*last = event(t, last.ID, last.Actor, last.Schema, receipt, last.RestsOn...)
			projection := Fold(fixture.records)
			row := commitmentForPromise(t, projection, fixture.promise)
			if carried {
				accounting := statementByEvent(t, projection, lid("merge")).MergeLeftLive
				if len(accounting) != 1 || !accounting[0].Verified || accounting[0].Class != "carried" {
					t.Fatalf("carried fixture is not verified: %+v", accounting)
				}
			}
			if artifactByEvent(t, projection, fixture.artifact).Retired {
				t.Fatal("delivery retired the carried reporting artifact")
			}
			if row.Status != "satisfied" || row.LandingReceipt != lid("merge") || row.ApprovedNotLanded {
				t.Fatalf("valid empty-retirement receipt did not discharge delivery: %+v", row)
			}
		})
	}
}

func TestMergeDischargeKeepsReceiptAuthorityGuards(t *testing.T) {
	for _, change := range []string{"unratified", "candidate", "signer", "repository", "ref", "uncited approval", "null plan", "retired receipt", "retired approval"} {
		t.Run(change, func(t *testing.T) {
			fixture := landingRound(t, landingBody(agent))
			if change != "unratified" {
				fixture = fixture.ratifiedApproval(t)
			}
			if change == "retired approval" {
				fixture.records = append(fixture.records, event(t, lid("retire-approval"), other, SchemaSupersede,
					Supersede{Target: fixture.approval, Text: "withdraw approval"}, fixture.approval))
			}
			fixture = fixture.receipt(t, "refs/heads/main")
			last := &fixture.records[len(fixture.records)-1]
			var receipt State
			if err := json.Unmarshal(last.Payload, &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Body["merge_retirements"] = `{}`
			switch change {
			case "candidate":
				receipt.Body["merge_candidate"] = repairHead
			case "signer":
				last.Actor = other
			case "repository":
				receipt.Body["merge_target_repo"] = "git:sha1:2222222222222222222222222222222222222222"
			case "ref":
				receipt.Body["merge_target_ref"] = "refs/heads/other"
			case "uncited approval":
				last.RestsOn = []string{fixture.artifact}
			case "null plan":
				receipt.Body["merge_retirements"] = `null`
			}
			*last = event(t, last.ID, last.Actor, last.Schema, receipt, last.RestsOn...)
			if change == "retired receipt" {
				fixture.records = append(fixture.records, event(t, lid("retire-receipt"), agent, SchemaSupersede,
					Supersede{Target: lid("merge"), Text: "withdraw receipt"}, lid("merge")))
			}
			row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
			if row.Status == "satisfied" || row.LandingReceipt != "" {
				t.Fatalf("invalid or wrong-destination receipt discharged commitment: %+v", row)
			}
		})
	}
}

func TestMergeDischargesEveryEligibleReviewedDeliveryWithoutRetiringIt(t *testing.T) {
	fixture := landingRound(t, landingBody(agent))
	// Add another delivery before the review, and make the exact approval
	// sign both. Only the first is its primary artifact.
	extra := []Record{
		event(t, lid("second-request"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Document it", Body: landingBody(agent)}, lid("w0")),
		event(t, lid("second-promise"), agent, SchemaState, State{Kind: KindPromise, Text: "I will document it"}, lid("second-request")),
		event(t, lid("second-artifact"), agent, SchemaState, State{Kind: KindArtifact, Text: "Documentation at candidate", Body: map[string]string{"path": "docs", "commit": approvedHead}}, lid("second-promise")),
	}
	position := len(fixture.records) - 3
	fixture.records = append(append(append([]Record(nil), fixture.records[:position]...), extra...), fixture.records[position:]...)
	fixture.records[len(fixture.records)-1].RestsOn = append(fixture.records[len(fixture.records)-1].RestsOn, lid("second-artifact"))
	fixture = fixture.ratifiedApproval(t).receipt(t, "refs/heads/main")
	last := &fixture.records[len(fixture.records)-1]
	var receipt State
	if err := json.Unmarshal(last.Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Body["merge_retirements"] = `{}`
	*last = event(t, last.ID, last.Actor, last.Schema, receipt, last.RestsOn...)
	projection := Fold(fixture.records)
	for _, promise := range []string{fixture.promise, lid("second-promise")} {
		row := commitmentForPromise(t, projection, promise)
		if row.Status != "satisfied" || row.LandingReceipt != lid("merge") || row.ApprovedNotLanded {
			t.Fatalf("reviewed delivery was lost: %+v", row)
		}
	}
}

func TestMergeDeliveryKeepsTheReceiptForEachDestination(t *testing.T) {
	for _, refs := range [][]string{{"refs/heads/main", "refs/heads/other"}, {"refs/heads/other", "refs/heads/main"}} {
		t.Run(refs[0], func(t *testing.T) {
			fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
			want := ""
			for i, ref := range refs {
				fixture = fixture.receipt(t, ref)
				last := &fixture.records[len(fixture.records)-1]
				var receipt State
				if err := json.Unmarshal(last.Payload, &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.Body["merge_retirements"] = `{}`
				id := lid(fmt.Sprintf("merge-%d", i))
				*last = event(t, id, last.Actor, last.Schema, receipt, last.RestsOn...)
				if ref == "refs/heads/main" {
					want = id
				}
			}
			row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
			if row.Status != "satisfied" || row.LandingReceipt != want || row.ApprovedNotLanded {
				t.Fatalf("another destination hid the matching delivery: %+v", row)
			}
		})
	}
}
