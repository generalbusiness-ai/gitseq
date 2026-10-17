package workroom

import (
	"maps"
	"testing"
)

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

// unsettledWithStaleness is the receipt capture as the fold computed it before
// it stopped folding staleness for this question: every commitment row
// projected against the whole graph's staleness, then read for the statuses
// that still expect a move. It is kept here, verbatim, as the reference the
// cheaper capture must equal.
func unsettledWithStaleness(f *foldState) map[string]bool {
	active := make(map[string]bool)
	for _, commitment := range f.projectCommitments(f.stalenessNow().staleness(f.succeededRetirements()).stale) {
		switch commitment.Status {
		case "open", "promised", "reported", "awaiting-review", "awaiting-authorization", "awaiting-landing", "stale":
			active[commitment.Request] = true
			if commitment.Promise != "" {
				active[commitment.Promise] = true
			}
			if commitment.Report != "" {
				active[commitment.Report] = true
			}
		}
	}
	return active
}

// The set a receipt captures is read without staleness, on the argument that
// staleness never decides which rows exist or which events they carry, and
// only ever turns an open or promised row into a stale one. This fixture makes
// that argument carry weight. Every commitment resting on scratch goes stale
// when scratch is retired without a successor: an open request, a promise with
// nothing reported, a request answered by a direct report, a promise whose
// report waits on its requester, and a promise whose report was ratified.
// Three receipts land with left-live testimony, review promises are withdrawn
// before and after their merges, and protecting commitments settle afterwards.
// Before every record, and so at every receipt's incoming frontier, the
// captured set must equal the reference that folds staleness.
func TestReceiptUnsettledCaptureMatchesTheStalenessAwareAnswer(t *testing.T) {
	done := map[string]string{"verdict": "done"}
	records := receiptProtectionRecords(t, false)
	records = append(records,
		event(t, "scratch", agent, SchemaState, State{Kind: KindArtifact, Text: "scratch", Body: map[string]string{"path": "notes", "commit": "scratch-head"}}, "r0"),
		event(t, "drift-request", operator, SchemaState, State{Kind: KindRequest, Text: "use scratch", Body: map[string]string{"to": agent, "conditions": "scratch"}}, "scratch"),
		event(t, "drift-review", operator, SchemaState, State{Kind: KindRequest, Text: "review scratch", Body: map[string]string{"to": other, "conditions": "scratch"}}, "scratch"),
		event(t, "drift-promise", other, SchemaState, State{Kind: KindPromise, Text: "will review scratch"}, "drift-review"),
		event(t, "direct-request", operator, SchemaState, State{Kind: KindRequest, Text: "answer from scratch", Body: map[string]string{"to": agent, "conditions": "scratch"}}, "scratch"),
		event(t, "direct-report", agent, SchemaState, State{Kind: KindReport, Text: "answered", Body: done}, "direct-request"),
		event(t, "reported-request", operator, SchemaState, State{Kind: KindRequest, Text: "measure scratch", Body: map[string]string{"to": other, "conditions": "scratch"}}, "scratch"),
		event(t, "reported-promise", other, SchemaState, State{Kind: KindPromise, Text: "will measure"}, "reported-request"),
		event(t, "reported-report", other, SchemaState, State{Kind: KindReport, Text: "measured", Body: done}, "reported-promise"),
		event(t, "satisfied-request", operator, SchemaState, State{Kind: KindRequest, Text: "summarise scratch", Body: map[string]string{"to": other, "conditions": "scratch"}}, "scratch"),
		event(t, "satisfied-promise", other, SchemaState, State{Kind: KindPromise, Text: "will summarise"}, "satisfied-request"),
		event(t, "satisfied-report", other, SchemaState, State{Kind: KindReport, Text: "summarised", Body: done}, "satisfied-promise"),
		event(t, "satisfied-ratified", operator, SchemaRatify, Ratify{Target: "satisfied-report"}, "satisfied-report"),
		event(t, "retire-scratch", agent, SchemaSupersede, Supersede{Target: "scratch", Text: "abandoned without a successor"}, "scratch"),
		protectionReceipt(t, "sibling", "side-promise"),
	)
	records = append(records, protectionSuccessors(t)...)
	records = append(records, costRound(t, 0, true)...)
	records = append(records, costRound(t, 1, false)...)
	records = append(records,
		event(t, "settle-side", agent, SchemaSupersede, Supersede{Target: "side-promise", Text: "withdrawn"}, "side-promise"),
		event(t, "retire-older", agent, SchemaSupersede, Supersede{Target: "older", Text: "cleanup"}, "older"),
	)
	// The stale rows each receipt's frontier must show, by the request that
	// opens the row and the status the staleness-aware projection gives it.
	wantStale := map[string]string{
		"drift-request":     "stale",
		"drift-review":      "stale",
		"direct-request":    "reported",
		"reported-request":  "reported",
		"satisfied-request": "satisfied",
	}

	folder := NewFolder(nil)
	receipts := 0
	seenStale := make(map[string]int)
	var previous map[string]bool
	changed := false
	for _, record := range records {
		got, want := folder.state.unsettledCommitmentEvents(), unsettledWithStaleness(folder.state)
		if !maps.Equal(got, want) {
			t.Fatalf("before %s: captured unsettled set = %v, staleness-aware answer = %v", record.ID, got, want)
		}
		if previous != nil && !maps.Equal(previous, got) {
			changed = true
		}
		previous = got
		frontier := folder.state.projectCommitments(folder.state.stalenessNow().staleness(folder.state.succeededRetirements()).stale)
		folder.Append(record)
		if !folder.state.byID[record.ID].mergeAccountingPresent {
			continue
		}
		receipts++
		for _, commitment := range frontier {
			status, named := wantStale[commitment.Request]
			if !named || !commitment.Stale || commitment.Status != status {
				continue
			}
			// A row that reached its status through a completion must still
			// carry that completion when stale; that is the half of the row a
			// staleness-dependent projection could drop.
			if status != "stale" && commitment.Report == "" {
				continue
			}
			seenStale[commitment.Request]++
		}
	}
	if receipts != 3 {
		t.Fatalf("inert fixture: %d receipts captured an unsettled set, want 3", receipts)
	}
	for request, status := range wantStale {
		if seenStale[request] != receipts {
			t.Fatalf("inert fixture: %s was a stale %s row at %d of %d receipt frontiers", request, status, seenStale[request], receipts)
		}
	}
	if !changed {
		t.Fatal("inert fixture: the unsettled set never changed")
	}
}
