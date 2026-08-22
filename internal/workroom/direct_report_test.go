package workroom

import "testing"

// A promise shows work in flight. It is useful and it is not obligatory: an
// addressee who did the work and reported it should not have to file a claim
// after the fact for something already finished. These cases are the boundary
// of that, and each one is a rule the fold would otherwise have to be trusted
// to hold rather than shown to.
func directReportSeed(t testing.TB) []Record {
	t.Helper()
	return []Record{
		event(t, "seed", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "agent-joins", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "seed"),
		event(t, "agent-ratified", operator, SchemaRatify, Ratify{Target: "agent-joins"}, "agent-joins"),
		event(t, "other-joins", operator, SchemaState, State{Kind: KindRoster, Text: "other joins", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "participant"}}, "seed"),
		event(t, "other-ratified", operator, SchemaRatify, Ratify{Target: "other-joins"}, "other-joins"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "Do the thing", Body: map[string]string{"to": agent, "conditions": "it is done"}}, "seed"),
	}
}

func TestReportMayRestOnTheRequestItAnswers(t *testing.T) {
	tests := []struct {
		name    string
		extra   func(t testing.TB) []Record
		verdict Verdict
		reason  string
	}{
		{
			name: "the addressee may report directly on the request",
			extra: func(t testing.TB) []Record {
				return []Record{event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request")}
			},
			verdict: Effective,
		},
		{
			name: "somebody who was not asked may not",
			extra: func(t testing.TB) []Record {
				return []Record{event(t, "report", other, SchemaState, State{Kind: KindReport, Text: "done"}, "request")}
			},
			verdict: Ineffective,
			reason:  "only the requested performer may report directly on a request",
		},
		{
			name: "the promised shape still works and still binds to its promisor",
			extra: func(t testing.TB) []Record {
				return []Record{
					event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
					event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise"),
				}
			},
			verdict: Effective,
		},
		{
			// Not two answers. gs review has always cited both -- the promise
			// for the commitment, the request for provenance -- so every
			// verdict in the log has this shape, and reading it as a second
			// answer would have made the whole review history disputed. The
			// promise decides which commitment a report closes whenever there
			// is one.
			name: "a promise and its own request together are the promised shape",
			extra: func(t testing.TB) []Record {
				return []Record{
					event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
					event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise", "request"),
				}
			},
			verdict: Effective,
		},
		{
			// And the promisor rule still binds through that shape: citing the
			// request as well must not let somebody else close the promise.
			name: "citing the request too does not let a stranger close the promise",
			extra: func(t testing.TB) []Record {
				return []Record{
					event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
					event(t, "report", other, SchemaState, State{Kind: KindReport, Text: "done"}, "promise", "request"),
				}
			},
			verdict: Ineffective,
			reason:  "only the promisor may report completion",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projection := Fold(append(directReportSeed(t), test.extra(t)...))
			decision, ok := projection.Decision("report")
			if !ok {
				t.Fatal("the report has no decision")
			}
			if decision.Verdict != test.verdict {
				t.Fatalf("verdict = %q (%s), want %q", decision.Verdict, decision.Reason, test.verdict)
			}
			if test.reason != "" && decision.Reason != test.reason {
				t.Errorf("reason = %q, want %q", decision.Reason, test.reason)
			}
		})
	}
}

// One commitment, one closure. A live promise already claimed this request, so
// a report resting on the request instead would leave that promise open with
// nothing able to close it. The refusal names the promise so the reporter can
// refile against it rather than guess.
func TestADirectReportIsRefusedWhileTheReportersPromiseIsLive(t *testing.T) {
	records := append(directReportSeed(t),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
	)
	decision, _ := Fold(records).Decision("report")
	if decision.Verdict != Ineffective {
		t.Fatalf("verdict = %q, want ineffective", decision.Verdict)
	}
	if want := "report rests on the request while promise promise is live; report on the promise"; decision.Reason != want {
		t.Errorf("reason = %q, want %q", decision.Reason, want)
	}

	// Withdraw the claim first and the direct route is open: what the rule
	// protects is a live commitment, not the fact that one once existed. The
	// withdrawal has to precede the report, because a decision is made when
	// the record is folded and nothing later reopens it.
	withdrawn := append(directReportSeed(t),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "withdraw", agent, SchemaSupersede, Supersede{Target: "promise", Text: "withdrawn"}, "promise"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
	)
	if decision, _ := Fold(withdrawn).Decision("report"); decision.Verdict != Effective {
		t.Errorf("after withdrawing the promise, verdict = %q (%s), want effective", decision.Verdict, decision.Reason)
	}
}

// The board has to show this as work that was done, not as work nobody took.
func TestADirectReportProjectsAsClaimAndComplete(t *testing.T) {
	records := append(directReportSeed(t),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
	)
	find := func(projection Projection) Commitment {
		t.Helper()
		for _, commitment := range projection.Commitments {
			if commitment.Request == "request" {
				return commitment
			}
		}
		t.Fatal("no commitment for the request")
		return Commitment{}
	}

	reported := find(Fold(records))
	if reported.Status != "reported" {
		t.Errorf("status = %q, want reported", reported.Status)
	}
	if reported.Performer != agent {
		t.Errorf("performer = %q, want the reporter", reported.Performer)
	}
	if reported.Promise != "" {
		t.Errorf("promise = %q, want empty: there was no claim", reported.Promise)
	}
	if reported.Report != "report" {
		t.Errorf("report = %q, want the direct report", reported.Report)
	}
	if reported.WaitingOn != operator {
		t.Errorf("waiting_on = %q, want the requester", reported.WaitingOn)
	}

	// And the originating requester closes it, exactly as for a promised one.
	satisfied := find(Fold(append(records, event(t, "accept", operator, SchemaRatify, Ratify{Target: "report"}, "report"))))
	if satisfied.Status != "satisfied" {
		t.Errorf("status after ratification = %q, want satisfied", satisfied.Status)
	}
	if satisfied.WaitingOn != "" {
		t.Errorf("waiting_on after ratification = %q, want empty", satisfied.WaitingOn)
	}
}

// An artifact by the addressee resting on the request, carrying a commit, is
// the implementation report in the direct shape just as it is on a promise.
func TestADirectArtifactClosesTheRequestTheSameWay(t *testing.T) {
	records := append(directReportSeed(t),
		event(t, "artifact", agent, SchemaState, State{Kind: KindArtifact, Text: "implementation", Body: map[string]string{"path": "internal/workroom", "commit": "abc123"}}, "request"),
	)
	for _, commitment := range Fold(records).Commitments {
		if commitment.Request != "request" {
			continue
		}
		if commitment.Status != "reported" || commitment.Report != "artifact" || commitment.Performer != agent {
			t.Fatalf("commitment = %+v, want the artifact reported by the addressee", commitment)
		}
		return
	}
	t.Fatal("no commitment for the request")
}
