package workroom

import "testing"

func receiptProtectionRecords(t *testing.T, same bool) []Record {
	t.Helper()
	basis := "side-promise"
	if same {
		basis = "impl-promise"
	}
	return append(reviewRecords(t)[:5],
		event(t, "impl-request", operator, SchemaState, State{Kind: KindRequest, Text: "deliver", Body: map[string]string{"to": agent, "conditions": "deliver"}}, "r0"),
		event(t, "impl-promise", agent, SchemaState, State{Kind: KindPromise, Text: "deliver"}, "impl-request"),
		event(t, "side-request", operator, SchemaState, State{Kind: KindRequest, Text: "side", Body: map[string]string{"to": agent, "conditions": "side"}}, "r0"),
		event(t, "side-promise", agent, SchemaState, State{Kind: KindPromise, Text: "side"}, "side-request"),
		event(t, "older", agent, SchemaState, State{Kind: KindArtifact, Text: "older", Body: map[string]string{"path": "spike", "commit": "older-head"}}, basis),
		event(t, "corrected", agent, SchemaState, State{Kind: KindArtifact, Text: "corrected", Body: map[string]string{"path": "spike", "commit": "corrected-head"}}, "impl-promise"),
		event(t, "review-request", agent, SchemaState, State{Kind: KindRequest, Text: "review", Body: map[string]string{"to": other, "conditions": "exact head"}}, "corrected"),
		event(t, "review-promise", other, SchemaState, State{Kind: KindPromise, Text: "review"}, "review-request"),
		event(t, "approval", other, SchemaState, State{Kind: KindReport, Text: "approved", Body: map[string]string{"verdict": "approved", "head": "corrected-head", "artifact": "corrected"}}, "review-promise", "corrected"),
		event(t, "ratification", agent, SchemaRatify, Ratify{Target: "approval"}, "approval"),
	)
}

func protectionReceipt(t *testing.T, class, commitment string) Record {
	t.Helper()
	return leftLiveReceipt(t, "merge", "approval", "corrected-head", "merged", `{"corrected":"spike"}`,
		`{"older":{"class":"`+class+`","commitment":"`+commitment+`"}}`)
}

func protectionSuccessors(t *testing.T) []Record {
	t.Helper()
	return []Record{
		event(t, "successor", agent, SchemaState, State{Kind: KindArtifact, Text: "landed", Body: map[string]string{"path": "spike", "commit": "merged"}}, "merge"),
		event(t, "retire-corrected", agent, SchemaSupersede, Supersede{Target: "corrected", Text: "landed"}, "corrected", "merge", "successor"),
	}
}

func requireProtection(t *testing.T, projection Projection, verified bool, debt int) {
	t.Helper()
	accounting := statementByEvent(t, projection, "merge").MergeLeftLive
	if len(accounting) != 1 || accounting[0].Artifact != "older" || accounting[0].Verified != verified {
		t.Fatalf("historical protection = %+v, want verified=%v", accounting, verified)
	}
	if projection.OmittedSupersessions != debt {
		t.Fatalf("current cleanup debt = %d, want %d", projection.OmittedSupersessions, debt)
	}
	if decision, _ := projection.Decision("retire-corrected"); decision.Verdict != Effective {
		t.Fatalf("left-live testimony changed independent retirement authority: %+v", decision)
	}
}

func TestReceiptProtectionBeforeAtAndAfterClosure(t *testing.T) {
	for _, same := range []bool{true, false} {
		name, protector := "separate promise", "side-promise"
		if same {
			name, protector = "own closing promise", "impl-promise"
		}
		t.Run(name, func(t *testing.T) {
			folder := NewFolder(nil)
			for _, record := range receiptProtectionRecords(t, same) {
				folder.Append(record)
			}
			if !folder.state.unsettledCommitmentEvents()[protector] {
				t.Fatal("inert fixture: protection was already settled before receipt")
			}
			folder.Append(protectionReceipt(t, "sibling", protector))
			// The receipt alone closes its implementation commitment, before
			// successor publication or retirement can explain the difference.
			if folder.state.unsettledCommitmentEvents()["impl-promise"] {
				t.Fatal("receipt did not close its implementation promise")
			}
			for _, record := range protectionSuccessors(t) {
				folder.Append(record)
			}
			debt := 0
			if same {
				debt = 1
			}
			requireProtection(t, folder.Projection(), true, debt)
			if !same {
				folder.Append(event(t, "settle-side", agent, SchemaSupersede, Supersede{Target: protector, Text: "withdrawn"}, protector))
				requireProtection(t, folder.Projection(), true, 1)
			}
			folder.Append(event(t, "retire-older", agent, SchemaSupersede, Supersede{Target: "older", Text: "cleanup due after protection ended"}, "older"))
			requireProtection(t, folder.Projection(), true, 0)
		})
	}
}

func TestReceiptProtectionCannotBorrowPastOrFutureCommitments(t *testing.T) {
	for _, how := range []string{"retired before", "satisfied before", "unrelated", "created after"} {
		t.Run(how, func(t *testing.T) {
			records := receiptProtectionRecords(t, false)
			claim := "side-promise"
			switch how {
			case "retired before":
				records = append(records, event(t, "settle-side", agent, SchemaSupersede, Supersede{Target: claim, Text: "withdrawn"}, claim))
			case "satisfied before":
				records = append(records,
					event(t, "side-done", agent, SchemaState, State{Kind: KindReport, Text: "done", Body: map[string]string{"verdict": "done"}}, claim),
					event(t, "side-accepted", operator, SchemaRatify, Ratify{Target: "side-done"}, "side-done"))
			case "unrelated":
				claim = "impl-promise"
			case "created after":
				claim = "later-promise"
			}
			records = append(records, protectionReceipt(t, "sibling", claim))
			records = append(records, protectionSuccessors(t)...)
			before := Fold(records)
			requireProtection(t, before, false, 1)
			// A newer live promise naming the old artifact cannot repair a
			// witness that was invalid on the receipt's incoming frontier.
			records = append(records,
				event(t, "later-request", operator, SchemaState, State{Kind: KindRequest, Text: "take older", Body: map[string]string{"to": agent, "conditions": "older"}}, "older"),
				event(t, "later-promise", agent, SchemaState, State{Kind: KindPromise, Text: "take older"}, "later-request"))
			requireProtection(t, Fold(records), false, 1)
		})
	}
}

func TestReceiptCannotCallItsOwnProtectedDraftAbandoned(t *testing.T) {
	records := append(receiptProtectionRecords(t, true), protectionReceipt(t, "abandoned", ""))
	records = append(records, protectionSuccessors(t)...)
	requireProtection(t, Fold(records), false, 1)
}
