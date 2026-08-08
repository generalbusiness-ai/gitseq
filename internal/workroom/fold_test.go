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
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindPropose, Text: "proposal"}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaSupersede, Supersede{Target: "e1", Text: "reason"}, "e1"),
	})
	if len(projection.Acts) != 2 {
		t.Fatalf("acts = %#v", projection.Acts)
	}
	if projection.Acts[0].Type != "ratify" || projection.Acts[0].Target != "e1" || projection.Acts[0].Verdict != Effective {
		t.Fatalf("ratify projection = %#v", projection.Acts[0])
	}
	if projection.Acts[1].Type != "supersede" || projection.Acts[1].Text != "reason" {
		t.Fatalf("supersede projection = %#v", projection.Acts[1])
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
