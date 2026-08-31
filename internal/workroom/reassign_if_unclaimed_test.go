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

// staleRequestSeed is directReportSeed with one basis under the request
// condemned: the request still stands, nobody has claimed or completed it, and
// something underneath it moved. That is the position the guard used to refuse
// and now admits.
func staleRequestSeed(t testing.TB) []Record {
	t.Helper()
	seeded := directReportSeed(t)
	if last := seeded[len(seeded)-1]; last.ID != "request" {
		t.Fatalf("directReportSeed no longer ends with the request: %s", last.ID)
	}
	records := append([]Record(nil), seeded[:len(seeded)-1]...)
	return append(records,
		event(t, "basis", operator, SchemaState, State{Kind: KindAssert, Text: "ground under the request"}, "seed"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "Do the thing",
			Body: map[string]string{"to": agent, "conditions": "it is done"}}, "basis"),
		event(t, "condemn", operator, SchemaSupersede, Supersede{Target: "basis", Text: "withdrawn with nothing in its place"}, "basis"),
	)
}

func statementFor(t testing.TB, projection Projection, event string) Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == event {
			return statement
		}
	}
	t.Fatalf("no statement for %s", event)
	return Statement{}
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

// Staleness is not a claim. A request whose ground moved is the one nobody
// promised, so refusing to reassign it stranded exactly the rows that needed a
// new addressee. The guard now reads only who claimed or completed the work.
func TestReassignIfUnclaimedReassignsAStaleUnclaimedRequest(t *testing.T) {
	records := append(staleRequestSeed(t), unclaimedRetirement(t, "retirement"))
	records = append(records, unclaimedReplacement(t, "replacement", "retirement"))
	projection := Fold(records)

	// Not merely a seed that folds. If the request is not stale here, the two
	// checks below say nothing about staleness at all.
	if request := statementFor(t, projection, "request"); !request.Stale {
		t.Fatalf("setup did not make the request stale: %+v", request)
	}
	for _, id := range []string{"retirement", "replacement"} {
		if decision := decisionFor(t, projection, id); decision.Verdict != Effective {
			t.Fatalf("%s over a stale request = %s (%s), want effective", id, decision.Verdict, decision.Reason)
		}
	}
	if replacement := statementFor(t, projection, "replacement"); replacement.Kind != KindRequest || replacement.Body["to"] != other {
		t.Fatalf("replacement did not project as the new request: %+v", replacement)
	}
}

// Dropping the staleness precondition drops nothing else: a stale request that
// somebody has claimed or completed still refuses, on the claim rather than on
// the staleness.
func TestReassignIfUnclaimedStillRefusesAClaimedStaleRequest(t *testing.T) {
	for _, test := range []struct {
		name  string
		claim Record
		want  string
	}{
		{
			name:  "promise",
			claim: event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "I will"}, "request"),
			want:  "admitted promise",
		},
		{
			name:  "direct completion",
			claim: event(t, "completion", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "request"),
			want:  "direct completion",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := append(staleRequestSeed(t), test.claim, unclaimedRetirement(t, "retirement"))
			projection := Fold(records)
			if request := statementFor(t, projection, "request"); !request.Stale {
				t.Fatalf("setup did not make the request stale: %+v", request)
			}
			decision := decisionFor(t, projection, "retirement")
			if decision.Verdict != Ineffective || !strings.Contains(decision.Reason, test.want) {
				t.Fatalf("guarded retirement = %+v, want ineffective naming %q", decision, test.want)
			}
			if strings.Contains(decision.Reason, "stale") {
				t.Fatalf("guarded retirement refused on staleness rather than the claim: %+v", decision)
			}
		})
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
