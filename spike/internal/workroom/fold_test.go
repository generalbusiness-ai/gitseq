package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

func kindDefinitionState(t *testing.T, definition KindDefinition) State {
	t.Helper()
	fields, err := json.Marshal(definition.Fields)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := json.Marshal(definition.Basis)
	if err != nil {
		t.Fatal(err)
	}
	return State{Kind: KindKindDef, Text: "Define " + string(definition.Name), Body: map[string]string{
		"name": string(definition.Name), "fields": string(fields), "basis": string(basis),
		"satisfier": definition.Satisfier, "render": string(definition.Render),
		"staleness": string(definition.Staleness), "lifecycle": string(definition.Lifecycle),
		"guidance": definition.Guidance,
	}}
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
	want, err := os.ReadFile("testdata/legacy_projection.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, want) {
		t.Fatalf("legacy projection bytes changed; update only through an explicit migration review\n%s", one)
	}
	if !bytes.Equal(RenderStatus(projection), RenderStatus(golden(t))) {
		t.Fatal("status projection is not byte-stable")
	}
}

func TestDeclaredKindIsPositionAwareAndReturnsToUndefinedWhenRetired(t *testing.T) {
	definition := KindDefinition{
		Name: "review-note",
		Fields: []FieldConstraint{
			{Operator: FieldPresent, Name: "topic"},
			{Operator: FieldPresent, Name: "priority"},
			{Operator: FieldOneOf, Name: "priority", Values: []string{"high", "low"}},
		},
		Basis: []BasisConstraint{}, Satisfier: "role:ratifier", Render: RenderNote,
		Staleness: StalenessTerminal, Lifecycle: LifecycleNone,
		Guidance: "Keep one review finding with an explicit priority.",
	}
	records := []Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: "review-note", Text: "too early", Body: map[string]string{"topic": "fold", "priority": "high"}}),
		event(t, "e2", operator, SchemaState, kindDefinitionState(t, definition), "e0"),
		event(t, "e3", operator, SchemaRatify, Ratify{Target: "e2"}, "e2"),
		event(t, "e4", operator, SchemaState, State{Kind: "review-note", Text: "declared", Body: map[string]string{"topic": "fold", "priority": "high"}}),
		event(t, "e5", operator, SchemaState, State{Kind: "review-note", Text: "bad value", Body: map[string]string{"topic": "fold", "priority": "urgent"}}),
		event(t, "e6", operator, SchemaRatify, Ratify{Target: "e4"}, "e4"),
		event(t, "e7", operator, SchemaSupersede, Supersede{Target: "e2", Text: "retire the definition"}, "e2"),
		event(t, "e8", operator, SchemaState, State{Kind: "review-note", Text: "undefined again", Body: map[string]string{"topic": "fold", "priority": "low"}}),
		event(t, "e9", operator, SchemaSupersede, Supersede{Target: "e7", Text: "restore the definition"}, "e7"),
		event(t, "e10", operator, SchemaState, State{Kind: "review-note", Text: "defined again", Body: map[string]string{"topic": "fold", "priority": "low"}}),
	}
	prefix := Evaluate(records[:7])
	for eventID, want := range map[string]Verdict{"e1": UndefinedKind, "e4": Effective, "e5": Ineffective, "e6": Effective} {
		decision, _ := prefix.Projection.Decision(eventID)
		if decision.Verdict != want {
			t.Errorf("%s verdict = %s (%s), want %s", eventID, decision.Verdict, decision.Reason, want)
		}
	}
	var active *KindDefinition
	for index := range prefix.Vocabulary.Definitions {
		if prefix.Vocabulary.Definitions[index].Name == definition.Name {
			active = &prefix.Vocabulary.Definitions[index]
		}
	}
	if active == nil || active.Source != "e2" || active.RatifiedBy != "e3" || active.Guidance != definition.Guidance {
		t.Fatalf("active definition = %+v", active)
	}
	retired := Evaluate(records[:9])
	decision, _ := retired.Projection.Decision("e8")
	if decision.Verdict != UndefinedKind {
		t.Fatalf("post-retirement verdict = %+v", decision)
	}
	for _, current := range retired.Vocabulary.Definitions {
		if current.Name == definition.Name {
			t.Fatalf("retired definition remained active: %+v", current)
		}
	}
	full := Evaluate(records)
	decision, _ = full.Projection.Decision("e10")
	if decision.Verdict != Effective {
		t.Fatalf("restored definition verdict = %+v", decision)
	}
	if got := full.Projection.OpaqueKinds["review-note"]; len(got) != 2 || got[0] != "e1" || got[1] != "e8" {
		t.Fatalf("undefined kind projection = %#v", got)
	}
}

func TestInvalidConstraintAlgebraIsTypedUninterpretable(t *testing.T) {
	for _, test := range []struct {
		name, fields, basis, reason string
	}{
		{name: "invalid regex", fields: `[{"op":"matches","name":"topic","pattern":"["}]`, basis: `[]`, reason: "error parsing regexp"},
		{name: "null fields", fields: `null`, basis: `[]`, reason: "fields must be a JSON array"},
		{name: "null basis", fields: `[]`, basis: `null`, reason: "basis must be a JSON array"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := State{Kind: KindKindDef, Text: test.name, Body: map[string]string{
				"name": "bad", "fields": test.fields, "basis": test.basis,
				"satisfier": "none", "render": "note", "staleness": "propagates", "lifecycle": "none", "guidance": "never active",
			}}
			projection := Fold([]Record{
				event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
				event(t, "e1", operator, SchemaState, invalid),
				event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
			})
			definition, _ := projection.Decision("e1")
			if definition.Verdict != Uninterpretable || !strings.Contains(definition.Reason, test.reason) {
				t.Fatalf("invalid definition = %+v", definition)
			}
			ratification, _ := projection.Decision("e2")
			if ratification.Verdict != Ineffective || ratification.Reason != "ratify target is not effective" {
				t.Fatalf("invalid definition ratification = %+v", ratification)
			}
		})
	}
}

func TestDefinitionReplacementRequiresRetiringTheLivePredecessor(t *testing.T) {
	first := KindDefinition{
		Name: "finding", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderNote, Staleness: StalenessPropagates,
		Lifecycle: LifecycleNone, Guidance: "First guidance.",
	}
	second := first
	second.Guidance = "Second guidance."
	result := Evaluate([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, first)),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, second), "d1"),
		event(t, "d2r0", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "d1s", operator, SchemaSupersede, Supersede{Target: "d1", Text: "replace the definition"}, "d1"),
		event(t, "d2r1", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
	})
	refused, _ := result.Projection.Decision("d2r0")
	if refused.Verdict != Disputed || !strings.Contains(refused.Reason, "live predecessor") {
		t.Fatalf("replacement without retirement = %+v", refused)
	}
	accepted, _ := result.Projection.Decision("d2r1")
	if accepted.Verdict != Effective {
		t.Fatalf("replacement after retirement = %+v", accepted)
	}
	for _, definition := range result.Vocabulary.Definitions {
		if definition.Name == second.Name && (definition.Source != "d2" || definition.Guidance != second.Guidance) {
			t.Fatalf("replacement definition = %+v", definition)
		}
	}
}

func TestDeclaredLifecycleKindsDriveTheCommitmentLoop(t *testing.T) {
	workOrder := KindDefinition{
		Name: "work-order", Fields: present("to", "conditions"), Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecycleRequest, Guidance: "Request a concrete unit of work.",
	}
	undertaking := KindDefinition{
		Name: "undertaking", Fields: []FieldConstraint{}, Basis: countKinds(1, 1, "work-order"),
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecyclePromise, Guidance: "Accept one work order.",
	}
	delivery := KindDefinition{
		Name: "delivery", Fields: []FieldConstraint{}, Basis: countKinds(1, 1, "undertaking"),
		Satisfier: SatisfierOriginatingRequester, Render: RenderResult, Staleness: StalenessPropagates,
		Lifecycle: LifecycleReport, Guidance: "Report an undertaking to its requester.",
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "agent", Body: map[string]string{"actor": agent, "kind": "agent", "name": "Agent", "role": "participant"}}, "e0"),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, workOrder), "e0"),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, undertaking), "d0r"),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "d2", operator, SchemaState, kindDefinitionState(t, delivery), "d1r"),
		event(t, "d2r", operator, SchemaRatify, Ratify{Target: "d2"}, "d2"),
		event(t, "r0", operator, SchemaState, State{Kind: "work-order", Text: "build", Body: map[string]string{"to": agent, "conditions": "tests pass"}}),
		event(t, "p0", agent, SchemaState, State{Kind: "undertaking", Text: "I will"}, "r0"),
		event(t, "x0", agent, SchemaState, State{Kind: "delivery", Text: "done"}, "p0"),
		event(t, "x0r", operator, SchemaRatify, Ratify{Target: "x0"}, "x0"),
	})
	if len(projection.Commitments) != 1 || projection.Commitments[0].Status != "satisfied" {
		t.Fatalf("declared lifecycle commitment = %+v", projection.Commitments)
	}
	if projection.Commitments[0].Request != "r0" || projection.Commitments[0].Promise != "p0" || projection.Commitments[0].Report != "x0" {
		t.Fatalf("declared lifecycle lineage = %+v", projection.Commitments[0])
	}
}

func TestDeclaredLifecycleKindsRemainTotalWithoutExpectedBasis(t *testing.T) {
	undertaking := KindDefinition{
		Name: "undertaking", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierNone, Render: RenderCommitment, Staleness: StalenessPropagates,
		Lifecycle: LifecyclePromise, Guidance: "An intentionally incomplete promise definition.",
	}
	delivery := KindDefinition{
		Name: "delivery", Fields: []FieldConstraint{}, Basis: []BasisConstraint{},
		Satisfier: SatisfierOriginatingRequester, Render: RenderResult, Staleness: StalenessPropagates,
		Lifecycle: LifecycleReport, Guidance: "An intentionally incomplete report definition.",
	}
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "d0", operator, SchemaState, kindDefinitionState(t, undertaking)),
		event(t, "d0r", operator, SchemaRatify, Ratify{Target: "d0"}, "d0"),
		event(t, "d1", operator, SchemaState, kindDefinitionState(t, delivery)),
		event(t, "d1r", operator, SchemaRatify, Ratify{Target: "d1"}, "d1"),
		event(t, "p0", agent, SchemaState, State{Kind: "undertaking", Text: "no request"}),
		event(t, "x0", agent, SchemaState, State{Kind: "delivery", Text: "no promise"}),
	})
	for eventID, reason := range map[string]string{
		"p0": "promise lifecycle basis count is 0, want exactly one request",
		"x0": "report lifecycle basis count is 0, want exactly one promise",
	} {
		decision, _ := projection.Decision(eventID)
		if decision.Verdict != Ineffective || decision.Reason != reason {
			t.Errorf("%s decision = %+v", eventID, decision)
		}
	}
}

func TestFoldActivationRecordsPrefixBoundaryThenNamesExecutionGap(t *testing.T) {
	activation := State{Kind: KindFoldActivation, Text: "activate the next fold", Body: map[string]string{
		"fold": "spike/internal/workroom@abc123", "entry": "gitseq/spike/internal/workroom",
		"interface": "workroom-fold@1", "toolchain": "go1.25.0", "prefix": "genesis",
	}}
	result := Evaluate([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, activation),
		event(t, "e2", operator, SchemaRatify, Ratify{Target: "e1"}, "e1"),
		event(t, "e3", operator, SchemaState, State{Kind: KindAssert, Text: "after the seam"}),
	})
	if result.Vocabulary.Binding.Status != "uninterpretable" || len(result.Vocabulary.Binding.Transitions) != 1 {
		t.Fatalf("binding = %+v", result.Vocabulary.Binding)
	}
	transition := result.Vocabulary.Binding.Transitions[0]
	if transition.Activation != "e1" || transition.Ratification != "e2" || !transition.Prefix {
		t.Fatalf("transition = %+v", transition)
	}
	decision, _ := result.Projection.Decision("e3")
	if decision.Verdict != Uninterpretable || decision.Reason != "uninterpretable: activated interpreter execution is not held" {
		t.Fatalf("post-seam decision = %+v", decision)
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

func TestParticipantRosterKindIsJudgedByTheFold(t *testing.T) {
	projection := Fold([]Record{
		event(t, "e0", operator, SchemaState, State{Kind: KindRoster, Text: "seed", Body: map[string]string{"actor": operator, "kind": "human", "name": "Human", "role": "operator"}}),
		event(t, "e1", operator, SchemaState, State{Kind: KindRoster, Text: "joins", Body: map[string]string{"actor": agent, "name": "Agent", "role": "participant"}}),
	})
	decision, _ := projection.Decision("e1")
	if decision.Verdict != Ineffective || decision.Reason != "participant roster state requires body.kind" {
		t.Fatalf("participant roster decision = %+v", decision)
	}
}
