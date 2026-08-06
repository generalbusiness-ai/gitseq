package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"gitseq/spike/internal/workroom"
)

func testRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return repo
}

func TestWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Submit(ctx, "human", workroom.SchemaState, workroom.State{Kind: workroom.KindRequest, Text: "build", Body: map[string]string{"to": agent.Fingerprint, "conditions": "tests pass"}}, []string{seed.ID}, nil, "request")
	if err != nil {
		t.Fatal(err)
	}
	promise, err := workspace.Submit(ctx, "agent", workroom.SchemaState, workroom.State{Kind: workroom.KindPromise, Text: "I promise"}, []string{request.ID}, nil, "promise")
	if err != nil {
		t.Fatal(err)
	}
	report, err := workspace.Submit(ctx, "agent", workroom.SchemaState, workroom.State{Kind: workroom.KindReport, Text: "done"}, []string{promise.ID}, nil, "report")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Submit(ctx, "human", workroom.SchemaRatify, workroom.Ratify{Target: report.ID}, []string{report.ID}, nil, "satisfy"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Depth != 7 || len(snapshot.Projection.Commitments) != 1 || snapshot.Projection.Commitments[0].Status != "satisfied" {
		t.Fatalf("unexpected snapshot: depth=%d commitments=%+v", snapshot.Depth, snapshot.Projection.Commitments)
	}
	if _, err := Open(ctx, repo); err != nil {
		t.Fatal(err)
	}
}
