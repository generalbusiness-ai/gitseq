package main
import (
 "testing"
 "github.com/generalbusiness-ai/gitseq/internal/intent"
 "github.com/generalbusiness-ai/gitseq/internal/kernel"
 "github.com/generalbusiness-ai/gitseq/internal/workroom"
)
func TestPlannerLegacyAcceptedRequestRetriesThroughCLI(t *testing.T) {
	fixture := newAuthoringFixture(t)
	agent, err := fixture.workspace.ResolveActor("agent")
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := fixture.workspace.Actor("operator")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := workroom.Encode(workroom.State{Kind: workroom.KindRequest, Text: "legacy request",
		Body: map[string]string{"to": agent.Fingerprint, "conditions": "the old way"}})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fixture.workspace.Store.WritePayloadTree(fixture.ctx, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	view := fixture.workspace.View()
	signed, err := intent.Sign(intent.Intent{
		Version: intent.Version,
		Target:  "git:" + view.ObjectFormat + ":" + view.Genesis,
		Schema:  workroom.SchemaState, PayloadTree: "git:" + view.ObjectFormat + ":" + tree,
		RestsOn:        []string{fixture.seed},
		IdempotencyNS:  view.IdempotencyNamespace,
		IdempotencyKey: "legacy-request",
	}, private)
	if err != nil {
		t.Fatal(err)
	}
	request := kernel.Request{Signed: signed, Payload: payload}
	first, err := kernel.Submit(fixture.ctx, fixture.workspace.Store, request,
		kernel.Options{SigningKey: view.SequencerKey})
	if err != nil {
		t.Fatal(err)
	}
	event := fixture.workspace.EventID(first.Head)
	if schema := fixture.schemaOf(event); schema != workroom.SchemaState {
		t.Fatalf("the legacy act was stored as %q", schema)
	}
	if body := fixture.body(event); body["target_ref"] != "" || len(body) != 2 {
		t.Fatalf("the legacy body was rewritten: %+v", body)
	}

	frontier := fixture.frontier()
	if _, err := fixture.file("legacy-fresh-refusal", "fresh no choice", map[string]string{"to": agent.Fingerprint, "conditions": "the old way"}); err == nil { t.Fatal("fresh request with no result choice was admitted") }
	second, err := fixture.file("legacy-request", "legacy request", map[string]string{"to": agent.Fingerprint, "conditions": "the old way"})
	if err != nil { t.Fatalf("actual CLI exact retry of accepted state@2 request: %v", err) }
	if second != event { t.Fatalf("CLI retry returned %s, want original %s", second, event) }
	if after := fixture.frontier(); after != frontier {
		t.Fatalf("the legacy retry appended: %s to %s", frontier, after)
	}
	if schema := fixture.schemaOf(event); schema != workroom.SchemaState {
		t.Fatalf("the replayed act reads as %q; a retry re-signed history", schema)
	}
	if _, err := fixture.file("legacy-request-fresh", "legacy request", map[string]string{"to": agent.Fingerprint, "conditions": "the old way"}); err == nil { t.Fatal("fresh key without result choice accepted") }
}
