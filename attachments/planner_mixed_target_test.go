package workroom

import "testing"

func TestReviewHoldCannotComeFromAnUnselectedTargetBranch(t *testing.T) {
	inherit := func(held bool) map[string]string {
		body := map[string]string{"to": agent, "conditions": "do it", "target": "inherit"}
		if held {
			body["landing"] = "held"
			body["hold_owner"] = bystander
		}
		return body
	}
	ancestry := []Record{
		event(t, lid("target-a"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Land main with independent release", Body: landingBody(agent, "landing", "held", "hold_owner", other)}, lid("w0")),
		event(t, lid("legacy-wrapper-a"), operator, SchemaState, State{Kind: KindRequest, Text: "Legacy child", Body: map[string]string{"to": agent, "conditions": "do it"}}, lid("target-a")),
		event(t, lid("target-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Different target", Body: landingBody(agent, "target_ref", "refs/heads/experiment")}, lid("w0")),
		event(t, lid("wrapper-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Inherit experiment", Body: inherit(false)}, lid("target-b")),
		event(t, lid("held-b"), operator, SchemaStateV3, State{Kind: KindRequest, Text: "Delegate experiment hold", Body: inherit(true)}, lid("wrapper-b")),
	}
	fixture := landingRoundFor(t, ancestry, event(t, lid("mixed-child"), operator, SchemaStateV3,
		State{Kind: KindRequest, Text: "Inherit the nearest target", Body: inherit(false)}, lid("legacy-wrapper-a"), lid("held-b"))).ratifiedApproval(t)
	projection := Fold(fixture.records)
	wrongRelease := Fold(fixture.release(t, bystander).records)
	t.Logf("unrelated hold owner release verdict=%+v; resulting row=%+v", decisionFor(t, wrongRelease, lid("release")), commitmentForPromise(t, wrongRelease, fixture.promise))
	if decision := decisionFor(t, projection, fixture.request); decision.Verdict == Ineffective {
		return
	}
	row := commitmentForPromise(t, projection, fixture.promise)
	if row.TargetRef != "refs/heads/main" || row.HoldOwner != other {
		t.Fatalf("hold must remain with selected target or ambiguity must refuse; got %+v", row)
	}
}
