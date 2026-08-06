package workroom

import (
	"bytes"
	"testing"
)

const (
	operator = "actor:operator"
	agent    = "actor:agent"
	other    = "actor:other"
)

func event(t *testing.T, id, actor, schema string, payload any, rests ...string) Record {
	t.Helper()
	encoded, err := Encode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Record{ID: id, Actor: actor, Schema: schema, Payload: encoded, RestsOn: rests}
}

func golden(t *testing.T) Projection {
	t.Helper()
	return Fold([]Record{
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
	})
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
	if got := projection.Roles[other]; len(got) != 1 || got[0] != "ratifier" {
		t.Fatalf("restored roles = %#v", got)
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
	for _, field := range [][]byte{[]byte(`"decisions": []`), []byte(`"statements": []`), []byte(`"commitments": []`), []byte(`"artifacts": []`)} {
		if !bytes.Contains(encoded, field) {
			t.Fatalf("projection omitted stable empty array %s: %s", field, encoded)
		}
	}
}

func TestCanonicalPayloadRejectsAlternateEncoding(t *testing.T) {
	if _, err := Decode(SchemaState, []byte(`{"text":"x","kind":"assert"}`)); err == nil {
		t.Fatal("accepted non-canonical field order")
	}
}
