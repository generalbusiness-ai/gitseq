package workroom

import (
	"strings"
	"testing"
)

func unclaimedRetirement(t testing.TB, id string) Record {
	t.Helper()
	return event(t, id, operator, SchemaRetireUnclaimed, RetireIfUnclaimed{
		Target: "request",
		Text:   "retire before reassignment",
		Expectation: UnclaimedExpectation{
			Request: "request", Promise: CommitmentAbsent, Completion: CommitmentAbsent,
		},
	}, "request")
}

func unclaimedReplacement(t testing.TB, id, retirement string) Record {
	t.Helper()
	return event(t, id, operator, SchemaReassignRequest, ReassignIfUnclaimed{
		Text: "ask the other agent",
		Body: map[string]string{"to": other, "conditions": "finish it"},
		Expectation: UnclaimedExpectation{
			Request: "request", Retirement: retirement,
			Promise: CommitmentAbsent, Completion: CommitmentAbsent,
		},
	}, retirement)
}

func decisionFor(t testing.TB, projection Projection, event string) Decision {
	t.Helper()
	decision, ok := projection.Decision(event)
	if !ok {
		t.Fatalf("no decision for %s", event)
	}
	return decision
}

func TestReassignIfUnclaimedAcceptsUnrelatedInterleaving(t *testing.T) {
	records := append(directReportSeed(t), unclaimedRetirement(t, "retirement"))
	records = append(records,
		event(t, "unrelated", other, SchemaState, State{Kind: KindAssert, Text: "unrelated durable traffic"}, "seed"),
		unclaimedReplacement(t, "replacement", "retirement"),
	)
	projection := Fold(records)
	for _, id := range []string{"retirement", "unrelated", "replacement"} {
		if decision := decisionFor(t, projection, id); decision.Verdict != Effective {
			t.Fatalf("%s = %s (%s), want effective", id, decision.Verdict, decision.Reason)
		}
	}
	var replacement Statement
	for _, statement := range projection.Statements {
		if statement.Event == "replacement" {
			replacement = statement
		}
	}
	if replacement.Kind != KindRequest || replacement.Body["to"] != other {
		t.Fatalf("replacement did not project as the new request: %+v", replacement)
	}
}

func TestReassignIfUnclaimedRefusesBothPromiseRaceWindows(t *testing.T) {
	t.Run("promise already present", func(t *testing.T) {
		records := append(directReportSeed(t),
			event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
			unclaimedRetirement(t, "retirement"),
		)
		decision := decisionFor(t, Fold(records), "retirement")
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "admitted promise") {
			t.Fatalf("guarded retirement = %+v", decision)
		}
	})

	t.Run("promise lands between retirement and replacement", func(t *testing.T) {
		records := append(directReportSeed(t), unclaimedRetirement(t, "retirement"))
		// This is the #12760/#12761 ordering: the ordinary promise remains an
		// effective historical act even though its request was just retired.
		records = append(records,
			event(t, "late-promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
			unclaimedReplacement(t, "replacement", "retirement"),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, "late-promise"); decision.Verdict != Effective {
			t.Fatalf("race reproduction changed: late promise = %+v", decision)
		}
		decision := decisionFor(t, projection, "replacement")
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "admitted promise") {
			t.Fatalf("guarded replacement = %+v", decision)
		}
	})
}

func TestReassignIfUnclaimedRefusesDirectCompletionHiddenByWithdrawal(t *testing.T) {
	t.Run("completion before retirement", func(t *testing.T) {
		records := append(directReportSeed(t),
			event(t, "completion", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
			unclaimedRetirement(t, "retirement"),
		)
		decision := decisionFor(t, Fold(records), "retirement")
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "direct completion") {
			t.Fatalf("guarded retirement = %+v", decision)
		}
	})

	t.Run("completion between guarded acts", func(t *testing.T) {
		records := append(directReportSeed(t), unclaimedRetirement(t, "retirement"))
		records = append(records,
			event(t, "late-completion", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
			unclaimedReplacement(t, "replacement", "retirement"),
		)
		projection := Fold(records)
		if decision := decisionFor(t, projection, "late-completion"); decision.Verdict != Effective {
			t.Fatalf("race reproduction changed: late completion = %+v", decision)
		}
		decision := decisionFor(t, projection, "replacement")
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "direct completion") {
			t.Fatalf("guarded replacement = %+v", decision)
		}
	})
}

func TestReassignIfUnclaimedFailsClosedOnMalformedOrAmbiguousGuards(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
		event   string
		want    string
	}{
		{
			name: "absence omitted",
			records: append(directReportSeed(t), Record{
				ID: "bad", Actor: operator, Schema: SchemaRetireUnclaimed, RestsOn: []string{"request"},
				Payload: []byte(`{"target":"request","text":"retire","expectation":{"request":"request","promise":"","completion":""}}`),
			}),
			event: "bad", want: "explicitly require absent",
		},
		{
			name: "replacement names ordinary retirement",
			records: append(directReportSeed(t),
				event(t, "ordinary", operator, SchemaSupersede, Supersede{Target: "request", Text: "withdraw"}, "request"),
				unclaimedReplacement(t, "bad", "ordinary"),
			),
			event: "bad", want: "effective guarded retirement",
		},
		{
			name: "duplicate retirement citation",
			records: append(append(directReportSeed(t), unclaimedRetirement(t, "retirement")),
				event(t, "bad", operator, SchemaReassignRequest, ReassignIfUnclaimed{
					Text: "again", Body: map[string]string{"to": other},
					Expectation: UnclaimedExpectation{Request: "request", Retirement: "retirement", Promise: CommitmentAbsent, Completion: CommitmentAbsent},
				}, "retirement", "retirement"),
			),
			event: "bad", want: "exactly once",
		},
		{
			name: "replacement author differs",
			records: append(append(directReportSeed(t), unclaimedRetirement(t, "retirement")),
				event(t, "bad", other, SchemaReassignRequest, ReassignIfUnclaimed{
					Text: "again", Body: map[string]string{"to": agent},
					Expectation: UnclaimedExpectation{Request: "request", Retirement: "retirement", Promise: CommitmentAbsent, Completion: CommitmentAbsent},
				}, "retirement"),
			),
			event: "bad", want: "retirement author",
		},
		{
			name: "another live retirement makes the named one ambiguous",
			records: append(append(directReportSeed(t), unclaimedRetirement(t, "retirement")),
				event(t, "second-retirement", operator, SchemaSupersede, Supersede{Target: "request", Text: "also retire"}, "request"),
				unclaimedReplacement(t, "bad", "retirement"),
			),
			event: "bad", want: "one live retirement",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := decisionFor(t, Fold(test.records), test.event)
			if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, test.want) {
				t.Fatalf("decision = %+v, want ineffective containing %q", decision, test.want)
			}
		})
	}
}

func TestOrdinaryRequesterWithdrawalStillPermitsALivePromise(t *testing.T) {
	records := append(directReportSeed(t),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
		event(t, "withdraw", operator, SchemaSupersede, Supersede{Target: "request", Text: "withdraw deliberately"}, "request"),
	)
	decision := decisionFor(t, Fold(records), "withdraw")
	if decision.Verdict != Effective {
		t.Fatalf("ordinary requester withdrawal = %+v", decision)
	}
}

// staleRequestSeed is directReportSeed with one act of ground between the seed
// and the request, and that ground then withdrawn. The request still stands
// and still names its addressee; only something underneath it moved.
func staleRequestSeed(t testing.TB) []Record {
	t.Helper()
	records := directReportSeed(t)
	if last := records[len(records)-1]; last.ID != "request" {
		t.Fatalf("directReportSeed no longer ends with the request: %s", last.ID)
	}
	return append(records[:len(records)-1],
		event(t, "ground", operator, SchemaState, State{Kind: KindAssert, Text: "ground under the request"}, "seed"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "Do the thing",
			Body: map[string]string{"to": agent, "conditions": "it is done"}}, "ground"),
		event(t, "withdraw-ground", operator, SchemaSupersede, Supersede{Target: "ground", Text: "the ground moved"}, "ground"),
	)
}

func statementFor(t testing.TB, projection Projection, event string) Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement
		}
	}
	t.Fatalf("no statement projected for %s", event)
	return Statement{}
}

// Staleness is not a claim. A request nobody promised and nobody completed can
// be reassigned even after a basis moved under it — and the claim guard is
// untouched by that, so the same stale request refuses once it is promised.
func TestReassignIfUnclaimedActsOnAStaleUnclaimedRequest(t *testing.T) {
	t.Run("stale and unclaimed reassigns", func(t *testing.T) {
		records := staleRequestSeed(t)
		if !statementFor(t, Fold(records), "request").Stale {
			t.Fatal("the fixture request is not stale; this test would prove nothing")
		}
		records = append(records,
			unclaimedRetirement(t, "retirement"),
			unclaimedReplacement(t, "replacement", "retirement"),
		)
		projection := Fold(records)
		for _, id := range []string{"retirement", "replacement"} {
			if decision := decisionFor(t, projection, id); decision.Verdict != Effective {
				t.Fatalf("%s = %s (%s), want effective", id, decision.Verdict, decision.Reason)
			}
		}
		replacement := statementFor(t, projection, "replacement")
		if replacement.Kind != KindRequest || replacement.Body["to"] != other {
			t.Fatalf("replacement did not project as the new request: %+v", replacement)
		}
	})

	t.Run("stale and promised still refuses", func(t *testing.T) {
		records := append(staleRequestSeed(t),
			event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
			unclaimedRetirement(t, "retirement"),
		)
		decision := decisionFor(t, Fold(records), "retirement")
		if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, "admitted promise") {
			t.Fatalf("guarded retirement of a promised request = %+v", decision)
		}
	})
}
