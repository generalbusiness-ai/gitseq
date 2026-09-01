package main

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestMCPReassignIfUnclaimedOwnsAndReplaysTheGuardedPair(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace := initRepository(t, "reassign-mcp")
	if _, _, err := workspace.AddActor(ctx, "human", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "old request",
		Body:    map[string]string{"to": "@worker", "conditions": "finish"},
		RestsOn: []string{snapshot.Projection.Decisions[0].Event}, IdempotencyKey: "mcp-old-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	resident, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(resident.Handler())
	defer httpServer.Close()
	server, attached := attachedServer(t, workspace, "human", httpServer.URL, httpServer.Client())
	if err := server.announce(ctx, attached); err != nil {
		t.Fatal(err)
	}
	defer server.depart(ctx)
	call := toolCall{Name: "reassign_if_unclaimed", Arguments: map[string]any{
		"old_request": request.Record.ID, "to": "@worker", "text": "replacement request",
		"conditions": "finish on current bases", "idempotency_key": "mcp-reassign",
	}}
	first, _, err := server.call(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.RetireActor(ctx, "human", "@worker"); err != nil {
		t.Fatal(err)
	}
	second, _, err := server.call(ctx, call)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	firstPair := first.(map[string]any)
	secondPair := second.(map[string]any)
	for _, key := range []string{"retirement", "request"} {
		firstRecord, ok := submissionRecord(firstPair[key])
		if !ok || firstRecord.ID == "" {
			t.Fatalf("first %s = %#v", key, firstPair[key])
		}
		secondRecord, ok := submissionRecord(secondPair[key])
		if !ok || secondRecord.ID != firstRecord.ID {
			t.Fatalf("second %s = %#v, want %s", key, secondPair[key], firstRecord.ID)
		}
	}
}
