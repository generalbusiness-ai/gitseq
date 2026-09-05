package workroom

import "testing"

func TestLandingReceiptWitnessUsesValidatedMatchingReceipt(t *testing.T) {
	for _, ref := range []string{"", "refs/heads/main", "refs/heads/other"} {
		t.Run(ref, func(t *testing.T) {
			fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).receipt(t, ref)
			row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
			if ref == "refs/heads/other" {
				if row.LandingReceipt != "" || !row.ApprovedNotLanded {
					t.Fatalf("wrong target witnessed: %+v", row)
				}
				return
			}
			if row.LandingReceipt != lid("merge") || row.Status != "satisfied" || row.ApprovedNotLanded {
				t.Fatalf("matching receipt lost: %+v", row)
			}
			fixture.records = append(fixture.records, event(t, lid("retire-merge"), agent, SchemaSupersede, Supersede{Target: lid("merge"), Text: "withdraw receipt"}, lid("merge")))
			row = commitmentForPromise(t, Fold(fixture.records), fixture.promise)
			if row.LandingReceipt != "" {
				t.Fatalf("retired receipt still witnessed: %+v", row)
			}
		})
	}
	fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
	fixture.records = append(fixture.records, event(t, lid("fake-merge"), agent, SchemaState, State{Kind: KindAssert, Text: "just an assertion", Body: map[string]string{"merge_head": approvedHead, "merge_hold_warning": "true", "merge_target_ref": "refs/heads/main"}}, fixture.artifact))
	row := commitmentForPromise(t, Fold(fixture.records), fixture.promise)
	if row.LandingReceipt != "" || !row.ApprovedNotLanded {
		t.Fatalf("assertion became receipt: %+v", row)
	}
}
