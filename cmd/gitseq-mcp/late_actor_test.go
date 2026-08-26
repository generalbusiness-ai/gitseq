package main

import (
	"context"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// A session that opened its workspace before an actor existed addresses that
// actor once another process adds it: fingerprint resolution goes through
// ResolveActor, so the fresh custody record is consulted on the miss and the
// adapter answers with the new actor's fingerprint instead of the empty
// string that means "matches nothing". Reverting the adapter to read the
// cached view alone fails this test by name.
func TestAdapterResolvesActorAddedAfterSessionStart(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	server := newServer("late", workspace.Repo)
	room := &room{workspace: workspace}
	if got, err := server.fingerprint(room); got != "" || err != nil {
		t.Fatalf("fingerprint before the actor exists = (%q, %v), want empty with no error", got, err)
	}
	external, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := external.AddActor(ctx, "human", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := server.fingerprint(room); got != added.Fingerprint || err != nil {
		t.Fatalf("fingerprint after the actor was added = (%q, %v), want %s", got, err, added.Fingerprint)
	}
}

// A state request addressed to a recipient created after the session opened
// still delivers: body.to resolves through a fresh re-read of local custody,
// so the signed record names the late recipient's fingerprint instead of
// refusing a live actor as unknown. Reverting the request path to the cached
// view alone fails this test by name.
func TestAdapterStateRequestDeliversToActorAddedAfterSessionStart(t *testing.T) {
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	server := newServer("human", workspace.Repo)
	if _, _, err := server.call(ctx, toolCall{Name: "whoami"}); err != nil {
		t.Fatal(err)
	}
	external, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := external.AddActor(ctx, "human", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "request", "text": "review the late-actor repair",
		"body":            map[string]any{"to": "@late", "conditions": "the late recipient receives it"},
		"rests_on":        []any{genesis.ID},
		"idempotency_key": "late-recipient",
	}})
	if err != nil {
		t.Fatal(err)
	}
	record := value.(map[string]any)["record"].(workroom.Record)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var statement *workroom.Statement
	for i := range snapshot.Projection.Statements {
		if snapshot.Projection.Statements[i].Event == record.ID {
			statement = &snapshot.Projection.Statements[i]
			break
		}
	}
	if statement == nil {
		t.Fatalf("submitted request did not project: %#v", value)
	}
	if statement.Kind != workroom.KindRequest || statement.Body["to"] != added.Fingerprint {
		t.Fatalf("request projected as %+v, want kind request delivered to the late recipient %s", statement, added.Fingerprint)
	}
}
