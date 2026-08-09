package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

const (
	operator = "actor:operator"
	agent    = "actor:agent"
	other    = "actor:other"
)

func event(t testing.TB, id, actor, schema string, payload any, rests ...string) Record {
	t.Helper()
	encoded, err := Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Record{ID: id, Actor: actor, Schema: schema, Payload: encoded, RestsOn: rests}
}

func BenchmarkFoldRosterHeavy(b *testing.B) {
	for _, actors := range []int{100, 1000, 5000} {
		records := rosterHeavyHistory(b, actors)
		b.Run(fmt.Sprintf("actors-%d", actors), func(b *testing.B) {
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

func BenchmarkFoldRequestHeavy(b *testing.B) {
	for _, requests := range []int{1000, 5000, 20000} {
		records := requestHeavyHistory(b, requests)
		b.Run(fmt.Sprintf("requests-%d", requests), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Fold(records)
			}
		})
	}
}

func BenchmarkFolderAppendToProjectionRequestHeavy(b *testing.B) {
	records := requestHeavyHistory(b, 20000)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		folder := NewFolder(records)
		record := event(b, fmt.Sprintf("append:%d", index), operator, SchemaState, State{
			Kind: KindRequest, Text: "request", Body: map[string]string{"to": agent, "conditions": "done"},
		}, "e0")
		b.StartTimer()
		folder.Append(record)
		projection := folder.Projection()
		if len(projection.Commitments) != 20001 {
			b.Fatalf("commitments = %d", len(projection.Commitments))
		}
	}
}

func TestFolderDoesNotRetainTransportPayloads(t *testing.T) {
	payload, err := Encode(State{Kind: KindAssert, Text: "decoded"})
	if err != nil {
		t.Fatal(err)
	}
	folder := NewFolder([]Record{{
		ID: "event", Actor: "actor", Schema: SchemaState, Payload: payload,
		Attachments: map[string][]byte{"evidence": []byte("large transport bytes")},
	}})
	if len(folder.state.records) != 1 {
		t.Fatalf("records = %d", len(folder.state.records))
	}
	retained := folder.state.records[0].record
	if retained.Payload != nil || retained.Attachments != nil {
		t.Fatalf("folder retained transport bytes: payload=%d attachments=%d", len(retained.Payload), len(retained.Attachments))
	}
}

func requestHeavyHistory(t testing.TB, requests int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
		event(t, "agent", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "agent-ratified", operator, SchemaRatify, Ratify{Target: "agent"}, "agent"),
	}
	for index := range requests {
		id := fmt.Sprintf("request:%d", index)
		records = append(records, event(t, id, operator, SchemaState, State{
			Kind: KindRequest, Text: "request", Body: map[string]string{"to": agent, "conditions": "done"},
		}, "e0"))
	}
	return records
}

func rosterHeavyHistory(t testing.TB, actors int) []Record {
	t.Helper()
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Operator", "role": "operator"}}),
	}
	for index := range actors {
		actor := fmt.Sprintf("actor:%d", index)
		membership := fmt.Sprintf("membership:%d", index)
		membershipRatification := fmt.Sprintf("membership-ratification:%d", index)
		grant := fmt.Sprintf("grant:%d", index)
		grantRatification := fmt.Sprintf("grant-ratification:%d", index)
		proposal := fmt.Sprintf("proposal:%d", index)
		ratification := fmt.Sprintf("proposal-ratification:%d", index)
		records = append(records,
			event(t, membership, operator, SchemaState, State{Kind: KindRoster, Text: "join", Body: map[string]string{"actor": actor, "kind": "agent", "name": actor, "role": "participant"}}, "e0"),
			event(t, membershipRatification, operator, SchemaRatify, Ratify{Target: membership}, membership),
			event(t, grant, operator, SchemaState, State{Kind: KindRoster, Text: "grant", Body: map[string]string{"actor": actor, "kind": "agent", "name": actor, "role": "ratifier"}}, membership),
			event(t, grantRatification, operator, SchemaRatify, Ratify{Target: grant}, grant),
			event(t, proposal, operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
			event(t, ratification, actor, SchemaRatify, Ratify{Target: proposal}, proposal),
		)
	}
	return records
}

func golden(t *testing.T) Projection {
	t.Helper()
	return Fold(goldenRecords(t))
}

func goldenRecords(t testing.TB) []Record {
	t.Helper()
	return []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "Agent joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "Build the projector", Body: map[string]string{"to": agent, "conditions": "golden projection is byte-stable"}}, "e0"),
		event(t, "e4", agent, SchemaState, State{Kind: KindPromise, Text: "I will build it"}, "e3"),
		event(t, "e5", agent, SchemaState, State{Kind: KindReport, Text: "Projection passes", Body: map[string]string{"result": "go test ./..."}}, "e4"),
		event(t, "e6", agent, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e8", agent, SchemaState, State{Kind: KindArtifact, Text: "Projector implementation", Body: map[string]string{"path": "internal/workroom", "commit": "abc123"}}, "e3", "e7"),
		event(t, "e9", operator, SchemaSupersede, Supersede{Target: "e3", Text: "Requirements changed"}, "e3"),
	}
}

func TestGoldenCommitmentAndArtifactProjection(t *testing.T) {
	projection := golden(t)
	if got := projection.Commitments[0].Status; got != "cancelled" {
		t.Fatalf("commitment status = %q", got)
	}
	if !projection.Artifacts[0].Stale {
		t.Fatal("artifact did not become stale when its governing request died")
	}
	decision, _ := projection.Decision("e6")
	if decision.Verdict != Ineffective {
		t.Fatalf("agent self-ratification = %s", decision.Verdict)
	}
	decision, _ = projection.Decision("e7")
	if decision.Verdict != Effective {
		t.Fatalf("requester satisfaction = %s: %s", decision.Verdict, decision.Reason)
	}
}

func TestResidentFolderMatchesWholeFoldAtEveryPrefix(t *testing.T) {
	records := goldenRecords(t)
	folder := NewFolder(nil)
	for index, record := range records {
		folder.Append(record)
		got, err := json.Marshal(folder.Projection())
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(Fold(records[:index+1]))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("prefix %d incremental projection differs\ngot  %s\nwant %s", index+1, got, want)
		}
	}
}

func TestResidentFolderProjectionMutationDoesNotCorruptState(t *testing.T) {
	folder := NewFolder(goldenRecords(t))
	projection := folder.Projection()
	projection.Statements[0].Body["actor"] = "attacker"
	projection.Statements[0].Body["role"] = "none"

	fresh := folder.Projection()
	if fresh.Statements[0].Body["actor"] != operator || fresh.Statements[0].Body["role"] != "operator" {
		t.Fatalf("projection mutation corrupted resident state: %+v", fresh.Statements[0].Body)
	}
}

func TestReportAwaitsRequester(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "do it", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
		event(t, "e4", agent, SchemaState, State{Kind: KindPromise, Text: "yes"}, "e3"),
		event(t, "e5", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "e4"),
	}
	projection := Fold(records)
	if got := projection.Commitments[0].Status; got != "reported" {
		t.Fatalf("status = %q", got)
	}
	if got := projection.Commitments[0].WaitingOn; got != operator {
		t.Fatalf("waiting on = %q", got)
	}
}

func TestStaleCommitmentPreservesReportedTerminalState(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "basis", operator, SchemaState, State{Kind: KindAssert, Text: "basis"}, "e0"),
		event(t, "request", operator, SchemaState, State{Kind: KindRequest, Text: "do it", Body: map[string]string{"to": agent, "conditions": "done"}}, "basis"),
		event(t, "promise", agent, SchemaState, State{Kind: KindPromise, Text: "yes"}, "request"),
		event(t, "report", agent, SchemaState, State{Kind: KindReport, Text: "done"}, "promise"),
		event(t, "satisfied", operator, SchemaRatify, Ratify{Target: "report"}, "report"),
		event(t, "basis-retired", operator, SchemaSupersede, Supersede{Target: "basis", Text: "basis changed"}, "basis"),
	}
	projection := Fold(records)
	commitment := projection.Commitments[0]
	if commitment.Status != "satisfied" || commitment.Report != "report" || !commitment.Stale || commitment.WaitingOn != "" {
		t.Fatalf("stale satisfied commitment = %+v", commitment)
	}
	if status := RenderStatus(projection); !bytes.Contains(status, []byte("| satisfied | stale |")) {
		t.Fatalf("status omitted stale qualifier:\n%s", status)
	}
	reportedRecords := append([]Record(nil), records[:7]...)
	reportedRecords = append(reportedRecords, records[8])
	reported := Fold(reportedRecords).Commitments[0]
	if reported.Status != "reported" || reported.Report != "report" || !reported.Stale || reported.WaitingOn != operator {
		t.Fatalf("stale reported commitment = %+v", reported)
	}

	unreportedRecords := append([]Record(nil), records[:6]...)
	unreportedRecords = append(unreportedRecords, records[8])
	unreported := Fold(unreportedRecords)
	commitment = unreported.Commitments[0]
	if commitment.Status != "stale" || commitment.Report != "" || !commitment.Stale || commitment.WaitingOn != agent {
		t.Fatalf("unreported stale commitment changed semantics = %+v", commitment)
	}
}

func TestDanglingWrongActorAndAmbiguousActsAreTotal(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", agent, SchemaState, State{Kind: KindPromise, Text: "unasked promise"}, "e0"),
		event(t, "e4", operator, SchemaState, State{Kind: KindRequest, Text: "one", Body: map[string]string{"to": agent, "conditions": "one"}}, "e0"),
		event(t, "e5", operator, SchemaState, State{Kind: KindRequest, Text: "two", Body: map[string]string{"to": agent, "conditions": "two"}}, "e0"),
		event(t, "e6", agent, SchemaState, State{Kind: KindPromise, Text: "ambiguous"}, "e4", "e5"),
		event(t, "e7", other, SchemaState, State{Kind: KindPromise, Text: "wrong actor"}, "e4"),
	})
	if len(projection.Decisions) != 8 {
		t.Fatalf("got %d decisions for 8 events", len(projection.Decisions))
	}
	want := map[string]Verdict{"e3": Ineffective, "e6": Disputed, "e7": Ineffective}
	for event, verdict := range want {
		decision, _ := projection.Decision(event)
		if decision.Verdict != verdict {
			t.Errorf("%s = %s, want %s", event, decision.Verdict, verdict)
		}
	}
}

func TestRosterSupersessionRevokesAndResurrectionRestoresAuthority(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "Carol joins", Body: map[string]string{"actor": other, "name": "Carol", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindPropose, Text: "before demotion"}, "e0"),
		event(t, "e4", other, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaSupersede, Supersede{Target: "e1", Text: "demote Carol"}, "e1"),
		event(t, "e6", operator, SchemaState, State{Kind: KindPropose, Text: "while demoted"}, "e0"),
		event(t, "e7", other, SchemaRatify, Ratify{Target: "e6"}, "e6"),
		event(t, "e8", operator, SchemaSupersede, Supersede{Target: "e5", Text: "restore Carol"}, "e5"),
		event(t, "e9", operator, SchemaState, State{Kind: KindAssert, Text: "after restoration"}, "e0"),
		event(t, "e10", other, SchemaRatify, Ratify{Target: "e9"}, "e9"),
	})
	for event, want := range map[string]Verdict{"e4": Effective, "e7": Ineffective, "e10": Effective} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", event, decision.Verdict, decision.Reason, want)
		}
	}
	actor := projection.Actors[other]
	if got := actor.Kind; got != "unspecified" {
		t.Fatalf("legacy actor kind = %q, want unspecified", got)
	}
	if got := actor.Roles; len(got) != 2 || got[0] != "participant" || got[1] != "ratifier" {
		t.Fatalf("restored roles = %#v", got)
	}
}

func TestMembershipRevocationRevokesDependentAuthority(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "grant ratifier", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "ratifier"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
		event(t, "e5", operator, SchemaState, State{Kind: KindPropose, Text: "before departure"}, "e0"),
		event(t, "e6", agent, SchemaRatify, Ratify{Target: "e5"}, "e5"),
		event(t, "e7", operator, SchemaSupersede, Supersede{Target: "e1", Text: "remove agent"}, "e1"),
		event(t, "e8", operator, SchemaState, State{Kind: KindPropose, Text: "after departure"}, "e0"),
		event(t, "e9", agent, SchemaRatify, Ratify{Target: "e8"}, "e8"),
	})
	for event, want := range map[string]Verdict{"e6": Effective, "e9": Ineffective} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != want {
			t.Errorf("%s = %s (%s), want %s", event, decision.Verdict, decision.Reason, want)
		}
	}
	if _, exists := projection.Actors[agent]; exists {
		t.Fatalf("revoked member retained a projected actor: %+v", projection.Actors[agent])
	}
}

func TestModernOperatorGrantPreservesAgentKindAndMembershipBasis(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent joins", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRoster, Text: "grant operator", Body: map[string]string{"actor": agent, "kind": "human", "name": "Wrong name", "role": "operator"}}, "e1"),
		event(t, "e4", operator, SchemaRatify, Ratify{Target: "e3"}, "e3"),
	})
	actor := projection.Actors[agent]
	if actor.Name != "Agent" || actor.Kind != "agent" || actor.MembershipEvent != "e1" {
		t.Fatalf("operator grant changed identity or membership basis: %+v", actor)
	}
	want := []string{"operator", "participant", "ratifier"}
	if len(actor.Roles) != len(want) {
		t.Fatalf("roles = %#v, want %#v", actor.Roles, want)
	}
	for index := range want {
		if actor.Roles[index] != want[index] {
			t.Fatalf("roles = %#v, want %#v", actor.Roles, want)
		}
	}
}

func TestAuthorityGrantCannotSubstituteForMembership(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "grant without membership", Body: map[string]string{"actor": other, "kind": "agent", "name": "Other", "role": "ratifier"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindRequest, Text: "not a participant", Body: map[string]string{"to": other, "conditions": "never"}}, "e0"),
		event(t, "e4", operator, SchemaState, State{Kind: KindPropose, Text: "not a ratifier"}, "e0"),
		event(t, "e5", other, SchemaRatify, Ratify{Target: "e4"}, "e4"),
	})
	for event, reason := range map[string]string{
		"e3": "requested performer is not in the live roster",
		"e5": "actor lacks ratifier role",
	} {
		decision, _ := projection.Decision(event)
		if decision.Verdict != Ineffective || decision.Reason != reason {
			t.Errorf("%s = %+v, want ineffective %q", event, decision, reason)
		}
	}
	if _, exists := projection.Actors[other]; exists {
		t.Fatalf("authority-only record projected a participant: %+v", projection.Actors[other])
	}
}

func TestRequestRejectsPerformerOutsideLiveRoster(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRequest, Text: "unknown", Body: map[string]string{"to": agent, "conditions": "done"}}, "e0"),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "requested performer is not in the live roster" {
		t.Fatalf("request decision = %#v", decision)
	}
}

func TestDuplicateIDCannotPoisonOriginal(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e0", other, SchemaState, State{Kind: KindAssert, Text: "duplicate"}),
		event(t, "e1", operator, SchemaState, State{Kind: KindAssert, Text: "claim"}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
	})
	if projection.Decisions[0].Verdict != Effective || projection.Decisions[1].Verdict != Disputed || projection.Decisions[3].Verdict != Effective {
		t.Fatalf("duplicate poisoned decisions: %#v", projection.Decisions)
	}
	if len(projection.Statements) != 2 || !projection.Statements[1].Ratified {
		t.Fatalf("duplicate poisoned statements: %#v", projection.Statements)
	}
}

func TestSupersedingSupersessionResurrectsTarget(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaSupersede, Supersede{Target: "e1", Text: "withdraw"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e2", Text: "undo withdrawal"}, "e2"),
	})
	if projection.Statements[1].Retired {
		t.Fatal("proposal did not resurrect")
	}
}

func TestIncrementalRetirementHandlesMultipleCausesAndNestedResurrection(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaSupersede, Supersede{Target: "e1", Text: "first retirement"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "second retirement"}, "e1"),
		event(t, "e4", operator, SchemaSupersede, Supersede{Target: "e2", Text: "remove first cause"}, "e2"),
		event(t, "e5", operator, SchemaSupersede, Supersede{Target: "e3", Text: "remove second cause"}, "e3"),
		event(t, "e6", operator, SchemaSupersede, Supersede{Target: "e4", Text: "restore first cause"}, "e4"),
	}
	wantRetired := []bool{false, true, true, true, false, true}
	for offset, want := range wantRetired {
		projection := Fold(records[:offset+2])
		var got bool
		for _, statement := range projection.Statements {
			if statement.Event == "e1" {
				got = statement.Retired
				break
			}
		}
		if got != want {
			t.Errorf("prefix through e%d: retired = %v, want %v", offset+1, got, want)
		}
	}
}

func TestProjectionIsByteStable(t *testing.T) {
	projection := golden(t)
	one, err := RenderJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	two, err := RenderJSON(golden(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("JSON projection is not byte-stable")
	}
	if !bytes.Equal(RenderStatus(projection), RenderStatus(golden(t))) {
		t.Fatal("status projection is not byte-stable")
	}
}

func TestProvenanceRendersBranchesAtTheirActualDepth(t *testing.T) {
	projection := Projection{Provenance: map[string][]string{"artifact": {"decision", "commit"}, "decision": {"seed"}}}
	want := "artifact\n  decision\n    seed\n  commit\n"
	if got := string(RenderProvenance(projection, "artifact")); got != want {
		t.Fatalf("provenance:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmptyCollectionsRenderAsArrays(t *testing.T) {
	projection := Fold(nil)
	encoded, err := RenderJSON(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range [][]byte{[]byte(`"decisions": []`), []byte(`"acts": []`), []byte(`"statements": []`), []byte(`"commitments": []`), []byte(`"artifacts": []`)} {
		if !bytes.Contains(encoded, field) {
			t.Fatalf("projection omitted stable empty array %s: %s", field, encoded)
		}
	}
}

func TestProjectionIncludesEverySemanticReplyAct(t *testing.T) {
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "reason"}, "e1"),
	}
	for index := range records {
		records[index].Timestamp = int64(100 + index)
	}
	projection := Fold(records)
	if len(projection.Acts) != 2 {
		t.Fatalf("acts = %#v", projection.Acts)
	}
	if projection.Acts[0].Type != "ratify" || projection.Acts[0].Target != "e1" || projection.Acts[0].Verdict != Effective {
		t.Fatalf("ratify projection = %#v", projection.Acts[0])
	}
	if projection.Acts[1].Type != "supersede" || projection.Acts[1].Text != "reason" {
		t.Fatalf("supersede projection = %#v", projection.Acts[1])
	}
	if projection.Statements[1].Timestamp != 101 || projection.Acts[0].Timestamp != 102 || projection.Acts[1].Timestamp != 103 {
		t.Fatalf("projection lost event timestamps: statements=%#v acts=%#v", projection.Statements, projection.Acts)
	}
}

func TestCanonicalPayloadRejectsAlternateEncoding(t *testing.T) {
	if _, err := Decode(SchemaState, []byte(`{"text":"x","kind":"assert"}`)); err == nil {
		t.Fatal("accepted non-canonical field order")
	}
}

func TestParticipantRosterRequiresActorKind(t *testing.T) {
	_, err := Encode(State{Kind: KindRoster, Text: "joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "participant"}})
	if err == nil {
		t.Fatal("participant roster without an actor kind was accepted")
	}
}

// worldRecords seeds a roster and appends the case under test.
func worldRecords(t testing.TB, extra ...Record) []Record {
	t.Helper()
	base := []Record{
		event(t, "w0", operator, SchemaState, State{Kind: KindRoster, Text: "Operator begins the workroom", Body: map[string]string{"actor": operator, "name": "Human", "role": "operator"}}),
		event(t, "w1", operator, SchemaState, State{Kind: KindRoster, Text: "Agent joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "agent"}}, "w0"),
		event(t, "w2", operator, SchemaRatify, Ratify{Target: "w1"}, "w1"),
	}
	return append(base, extra...)
}

func artifactByEvent(t *testing.T, projection Projection, id string) Artifact {
	t.Helper()
	for _, artifact := range projection.Artifacts {
		if artifact.Event == id {
			return artifact
		}
	}
	t.Fatalf("no artifact projected for %s", id)
	return Artifact{}
}

func statementByEvent(t *testing.T, projection Projection, id string) Statement {
	t.Helper()
	for _, statement := range projection.Statements {
		if statement.Event == id {
			return statement
		}
	}
	t.Fatalf("no statement projected for %s", id)
	return Statement{}
}

// A retired artifact under a document means the world moved, and the mark must
// survive intermediate hops. The distinction is the whole point: only this
// kind of staleness means go and re-read the code.
func TestRetiredArtifactMarksDependentsAsDescribingASupersededWorld(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference page", Body: map[string]string{"path": "docs/reference/gs.md", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaState, State{Kind: KindAssert, Text: "The reference documents every subcommand"}, "w4"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation, replacing aaa111", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ccc333"}}, "w0"),
		event(t, "w7", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ccc333"}, "w3", "w6"),
	)
	projection := Fold(records)

	page := artifactByEvent(t, projection, "w4")
	if !page.Stale || !page.DescribesSupersededWorld {
		t.Fatalf("page resting on a retired artifact: stale=%v world=%v", page.Stale, page.DescribesSupersededWorld)
	}
	claim := statementByEvent(t, projection, "w5")
	if !claim.Stale || !claim.DescribesSupersededWorld {
		t.Fatalf("second hop lost the distinction: stale=%v world=%v", claim.Stale, claim.DescribesSupersededWorld)
	}
}

// Ordinary staleness must not claim the world moved. The golden fixture's
// artifact goes stale because its governing request died, which is the
// argument dying, not the code changing.
func TestRetiredArgumentIsStaleWithoutDescribingASupersededWorld(t *testing.T) {
	artifact := golden(t).Artifacts[0]
	if !artifact.Stale {
		t.Fatal("golden artifact is no longer stale; the fixture no longer tests this")
	}
	if artifact.DescribesSupersededWorld {
		t.Fatal("a retired request was reported as the world moving")
	}
}

// The succession warning is a to-do, so doing the work must clear it.
func TestSuccessionWarningAppearsUntilThePredecessorIsSuperseded(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation, replacing aaa111", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ccc333"}}, "w0"),
		event(t, "w5", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ccc333"}, "w3", "w4"),
	)

	before := Fold(records[:len(records)-1])
	if !artifactByEvent(t, before, "w4").SuccessionUnrecorded {
		t.Fatal("a live predecessor at the same path raised no warning")
	}
	after := Fold(records)
	if artifactByEvent(t, after, "w4").SuccessionUnrecorded {
		t.Fatal("warning survived the supersession that answers it")
	}
	if artifactByEvent(t, after, "w3").SuccessionUnrecorded {
		t.Fatal("the first artifact at a path warned about a predecessor it does not have")
	}
}

// A different path is a different thing; the fold must not infer that two
// spellings mean the same tree.
func TestSuccessionWarningComparesPathsExactly(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI and its docs", Body: map[string]string{"path": "spike/cmd/gs,docs", "commit": "ccc333"}}, "w0"),
	)
	if artifactByEvent(t, Fold(records), "w4").SuccessionUnrecorded {
		t.Fatal("a comma-joined path was treated as succeeding one of its members")
	}
}

// An artifact with no basis can never go stale, so its silence must not read
// as currency.
func TestUnbridgedArtifactIsMarkedUnableToFlare(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "Unbridged work", Body: map[string]string{"path": "spike", "commit": "aaa111"}}),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "Bridged work", Body: map[string]string{"path": "ui", "commit": "bbb222"}}, "w0"),
	)
	projection := Fold(records)
	if !artifactByEvent(t, projection, "w3").UnableToFlare {
		t.Fatal("an artifact citing nothing was not marked unable to flare")
	}
	if artifactByEvent(t, projection, "w4").UnableToFlare {
		t.Fatal("an artifact citing a basis was marked unable to flare")
	}
}

// The marks are for humans, so they must reach the human page and not only
// the JSON.
func TestStatusPageCarriesTheArtifactMarks(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation", Body: map[string]string{"path": "spike/cmd/gs", "commit": "aaa111"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI reference page", Body: map[string]string{"path": "docs/reference/gs.md", "commit": "bbb222"}}, "w3"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "Unbridged work", Body: map[string]string{"path": "ui", "commit": "ccc333"}}),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "CLI implementation again", Body: map[string]string{"path": "spike/cmd/gs", "commit": "ddd444"}}, "w0"),
		event(t, "w7", agent, SchemaSupersede, Supersede{Target: "w3", Text: "Behaviour replaced at ddd444"}, "w3", "w6"),
	)
	page := string(RenderStatus(Fold(records)))
	for _, want := range []string{
		"STALE — describes a superseded world",
		"unable to flare",
		"cite no basis and can never go stale",
	} {
		if !bytes.Contains([]byte(page), []byte(want)) {
			t.Fatalf("status page omits %q\n%s", want, page)
		}
	}
}

// One unrecorded succession at a long-lived path repeats on every later link
// of that chain, so the page must report situations as well as rows. A mark
// that trains readers to scroll past it is worse than no mark.
func TestStatusPageCountsSuccessionPathsAsWellAsArtifacts(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "five", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	// Three supersessions are owed: a1 and a2 on the spike path, b1 on ui.
	// Not two, which is the path count.
	if projection.OmittedSupersessions != 3 {
		t.Fatalf("owed supersessions = %d, want 3", projection.OmittedSupersessions)
	}
	page := RenderStatus(projection)
	want := "3 artifacts across 2 paths follow a live artifact at the same path without superseding it; supersessions still owed: 3"
	if !bytes.Contains(page, []byte(want)) {
		t.Fatalf("page does not separate owed acts from rows and paths, want %q\n%s", want, page)
	}
}

// A predecessor owes its retirement only while something live stands in its
// place. Withdraw the successor and the predecessor is the current artifact
// for its path again, so following an owed-supersession warning there would
// retire the current artifact for a replacement that no longer exists.
func TestWithdrawnSuccessorLeavesNothingOwed(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaSupersede, Supersede{Target: "w4", Text: "withdrawn; a1 stands again"}, "w4"),
		// Positive control: a live successor on another path still owes one.
		// Without it this passes for an implementation that counts nothing.
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	withdrawn := artifactByEvent(t, projection, "w4")
	if !withdrawn.Stale {
		t.Fatal("a2 is not retired; the supersession was ineffective and the case is untested")
	}
	if projection.OmittedSupersessions != 1 {
		t.Fatalf("owed supersessions = %d, want 1 (b1 only; a1's only successor was withdrawn)", projection.OmittedSupersessions)
	}
	// The row stays as history — a2 really did follow a live a1 — while the
	// owed count says there is nothing left to do about it.
	if !withdrawn.SuccessionUnrecorded {
		t.Fatal("a2 stopped recording that it followed a live a1")
	}
}

// Rows and owed acts agree in the fixture above, so it cannot show the
// aggregate is the right quantity. A fourth artifact on one path makes them
// differ: four rows notice a live ancestor while four supersessions are owed
// and a naive sum of per-row figures would give seven.
func TestOwedSupersessionsDivergeFromRowCount(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "four", Body: map[string]string{"path": "spike", "commit": "a4"}}, "w0"),
		event(t, "w7", agent, SchemaState, State{Kind: KindArtifact, Text: "five", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
		event(t, "w8", agent, SchemaState, State{Kind: KindArtifact, Text: "six", Body: map[string]string{"path": "ui", "commit": "b2"}}, "w0"),
	)
	projection := Fold(records)
	if projection.OmittedSupersessions != 4 {
		t.Fatalf("owed supersessions = %d, want 4 (a1, a2, a3, b1)", projection.OmittedSupersessions)
	}
	sum := 0
	for _, artifact := range projection.Artifacts {
		sum += artifact.LivePredecessors
		if artifact.Commit == "a4" && artifact.LivePredecessors != 3 {
			t.Fatalf("a4 live predecessors = %d, want 3", artifact.LivePredecessors)
		}
	}
	if sum == projection.OmittedSupersessions {
		t.Fatalf("per-row sum %d equals the owed count; the fixture cannot tell them apart", sum)
	}
}

// Retiring the immediate predecessor must not clear the warning while an
// earlier artifact at the same path is still live.
func TestRetiringOnePredecessorDoesNotHideAnEarlierLiveOne(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "one", Body: map[string]string{"path": "spike", "commit": "a1"}}, "w0"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "two", Body: map[string]string{"path": "spike", "commit": "a2"}}, "w0"),
		event(t, "w5", operator, SchemaSupersede, Supersede{Target: "w4", Text: "retire the middle one"}, "w4"),
		event(t, "w6", agent, SchemaState, State{Kind: KindArtifact, Text: "three", Body: map[string]string{"path": "spike", "commit": "a3"}}, "w0"),
	)
	projection := Fold(records)
	seen := false
	for _, artifact := range projection.Artifacts {
		if artifact.Commit != "a3" {
			continue
		}
		seen = true
		if !artifact.SuccessionUnrecorded {
			t.Fatal("a3 reports a recorded succession while a1 is still live")
		}
		if artifact.LivePredecessors != 1 {
			t.Fatalf("a3 live predecessors = %d, want 1 (a1 only; a2 retired)", artifact.LivePredecessors)
		}
	}
	if !seen {
		t.Fatal("a3 missing from the projection")
	}
}

// A statement citing only events the log does not contain is as unable to
// flare as one citing nothing: supersede needs a resolvable target.
func TestArtifactCitingOnlyUnresolvableBasesCannotFlare(t *testing.T) {
	records := worldRecords(t,
		event(t, "w3", agent, SchemaState, State{Kind: KindArtifact, Text: "dangling", Body: map[string]string{"path": "spike", "commit": "a1"}}, "does-not-exist"),
		event(t, "w4", agent, SchemaState, State{Kind: KindArtifact, Text: "anchored", Body: map[string]string{"path": "ui", "commit": "b1"}}, "w0"),
	)
	projection := Fold(records)
	checked := 0
	for _, artifact := range projection.Artifacts {
		switch artifact.Commit {
		case "a1":
			checked++
			if !artifact.UnableToFlare {
				t.Fatal("artifact citing only an unresolvable basis reads as able to flare")
			}
		case "b1":
			// Positive control: one resolvable basis is a handle a future
			// supersession can take. Without this the assertion above passes
			// for an implementation marking every artifact unable to flare.
			checked++
			if artifact.UnableToFlare {
				t.Fatal("artifact with a resolvable basis marked unable to flare")
			}
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d artifacts, want 2", checked)
	}
}
