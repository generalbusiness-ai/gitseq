package workroom

import (
	"encoding/json"
	"strings"
	"testing"
)

// priorReceipt rewrites the fixture's sealed receipt into the incorporation
// shape: merge_incorporation=prior, merge_head equal to the candidate, and the
// four empty encodings. mutate gets the body afterwards, so a test can plant
// exactly the one field it is about.
func (f landingFixture) priorReceipt(t testing.TB, mutate func(map[string]string)) landingFixture {
	t.Helper()
	f = f.receipt(t, "refs/heads/main")
	last := &f.records[len(f.records)-1]
	var receipt State
	if err := json.Unmarshal(last.Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Body["merge_incorporation"] = IncorporationPrior
	receipt.Body["merge_head"] = approvedHead
	receipt.Body["merge_retirements"] = `{}`
	receipt.Body["merge_successors"] = `[]`
	receipt.Body["merge_left_live"] = `{}`
	receipt.Body["merge_changed_paths"] = `[]`
	receipt.Text = "Record the head the target already carries"
	if mutate != nil {
		mutate(receipt.Body)
	}
	*last = event(t, last.ID, last.Actor, last.Schema, receipt, last.RestsOn...)
	return f
}

// An incorporation receipt closes its commitment exactly as an ordinary
// landing does: satisfied, terminal landed, approved_not_landed false. The
// empty plan reaches no path, so nothing else in the log moves.
func TestPriorIncorporationReceiptClosesItsCommitment(t *testing.T) {
	t.Parallel()
	fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).priorReceipt(t, nil)
	projection := Fold(fixture.records)
	row := commitmentForPromise(t, projection, fixture.promise)
	if row.Status != "satisfied" || row.Terminal != "landed" || row.ApprovedNotLanded || row.LandingReceipt != lid("merge") {
		t.Fatalf("incorporation receipt did not close the commitment: %+v", row)
	}
	if artifactByEvent(t, projection, fixture.artifact).Retired {
		t.Fatal("an incorporation retired the reporting artifact it delivered")
	}
	if statement := statementByEvent(t, projection, lid("merge")); statement.Body["merge_incorporation"] != IncorporationPrior {
		t.Fatalf("receipt statement lost its incorporation field: %+v", statement.Body)
	}
}

// The empty-plan requirement, one encoding at a time, and the unknown-value
// refusal beside it. Each of these is a receipt that says a landing happened
// before it was signed and then claims succession on top of that; none of them
// may deliver anything. Removing the merge_incorporation clause from
// validateMergeReceiptNow lets every row here close the commitment.
func TestPriorIncorporationReceiptWithAPlantedPlanDeliversNothing(t *testing.T) {
	t.Parallel()
	for name, plant := range map[string]func(map[string]string){
		"retires a predecessor": func(body map[string]string) {
			body["merge_retirements"] = `{"` + lid("artifact") + `":"internal/workroom"}`
		},
		"publishes a successor": func(body map[string]string) { body["merge_successors"] = `["internal/workroom"]` },
		"declares a changed path": func(body map[string]string) {
			body["merge_changed_paths"] = `["internal/workroom/fold.go"]`
		},
		"claims left-live testimony": func(body map[string]string) {
			body["merge_left_live"] = `{"` + lid("artifact") + `":{"class":"carried"}}`
		},
		"unreadable successors":    func(body map[string]string) { body["merge_successors"] = `not json` },
		"unknown incorporation":    func(body map[string]string) { body["merge_incorporation"] = "later" },
		"names another merge head": func(body map[string]string) { body["merge_head"] = "0123456789abcdef0123456789abcdef01234567" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t).priorReceipt(t, plant)
			projection := Fold(fixture.records)
			row := commitmentForPromise(t, projection, fixture.promise)
			if row.Status == "satisfied" || row.LandingReceipt != "" || !row.ApprovedNotLanded {
				t.Fatalf("a prior receipt that %s delivered its commitment: %+v", name, row)
			}
			if artifactByEvent(t, projection, fixture.artifact).Retired {
				t.Fatalf("a prior receipt that %s retired an artifact", name)
			}
		})
	}
}

// The authority half of the same rule, from the door a receipt's reach is
// actually exercised through. A prior receipt with a planted retirement plan
// confers no cross-author supersession: the artifact stays live and the act
// that tried to retire it is ineffective. Removing the clause makes the
// supersession effective and this test red.
func TestPriorIncorporationConfersNoCrossAuthorRetirement(t *testing.T) {
	t.Parallel()
	const foreignPath = "internal/workroom/fold.go"
	fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
	fixture.records = append(fixture.records,
		event(t, lid("foreign"), other, SchemaState, State{Kind: KindArtifact, Text: "Reviewer's own pointer",
			Body: map[string]string{"path": foreignPath, "commit": filingHead}}, lid("w0")))
	fixture = fixture.priorReceipt(t, func(body map[string]string) {
		body["merge_retirements"] = `{"` + lid("foreign") + `":"internal/workroom"}`
	})
	fixture.records = append(fixture.records,
		event(t, lid("successor"), agent, SchemaState, State{Kind: KindArtifact, Text: "Merge published the current artifact",
			Body: map[string]string{"path": "internal/workroom", "commit": approvedHead}}, lid("merge")),
		event(t, lid("retire-foreign"), agent, SchemaSupersede, Supersede{Target: lid("foreign"), Text: "Merge retired a covered predecessor."},
			lid("foreign"), lid("merge"), lid("successor")))
	projection := Fold(fixture.records)
	if artifactByEvent(t, projection, lid("foreign")).Retired {
		t.Fatal("a prior incorporation receipt retired another actor's artifact")
	}
	decision := decisionFor(t, projection, lid("retire-foreign"))
	if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "may not supersede") {
		t.Fatalf("cross-author supersession on a prior receipt = %s: %s", decision.Verdict, decision.Reason)
	}
}

// The control. An ordinary receipt carries no merge_incorporation field, and
// the same cross-author retirement it always authorized still lands. Without
// this, a clause that refused every receipt would look correct above.
func TestOrdinaryReceiptKeepsItsCrossAuthorRetirement(t *testing.T) {
	t.Parallel()
	const foreignPath = "internal/workroom/fold.go"
	fixture := landingRound(t, landingBody(agent)).ratifiedApproval(t)
	fixture.records = append(fixture.records,
		event(t, lid("foreign"), other, SchemaState, State{Kind: KindArtifact, Text: "Reviewer's own pointer",
			Body: map[string]string{"path": foreignPath, "commit": filingHead}}, lid("w0")))
	fixture = fixture.receipt(t, "refs/heads/main")
	last := &fixture.records[len(fixture.records)-1]
	var receipt State
	if err := json.Unmarshal(last.Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Body["merge_retirements"] = `{"` + lid("foreign") + `":"internal/workroom"}`
	*last = event(t, last.ID, last.Actor, last.Schema, receipt, last.RestsOn...)
	mergeHead := receipt.Body["merge_head"]
	fixture.records = append(fixture.records,
		event(t, lid("successor"), agent, SchemaState, State{Kind: KindArtifact, Text: "Merge published the current artifact",
			Body: map[string]string{"path": "internal/workroom", "commit": mergeHead}}, lid("merge")),
		event(t, lid("retire-foreign"), agent, SchemaSupersede, Supersede{Target: lid("foreign"), Text: "Merge retired a covered predecessor."},
			lid("foreign"), lid("merge"), lid("successor")))
	projection := Fold(fixture.records)
	if !artifactByEvent(t, projection, lid("foreign")).Retired {
		t.Fatal("an ordinary merge receipt lost its cross-author retirement authority")
	}
	row := commitmentForPromise(t, projection, fixture.promise)
	if row.Status != "satisfied" || row.LandingReceipt != lid("merge") {
		t.Fatalf("an ordinary merge receipt stopped closing its commitment: %+v", row)
	}
}
