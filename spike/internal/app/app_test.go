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
	_, _, err = workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Submit(ctx, "human", workroom.SchemaState, workroom.State{Kind: workroom.KindRequest, Text: "build", Body: map[string]string{"to": "agent", "conditions": "tests pass"}}, []string{seed.ID}, nil, "request")
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

func TestBuildRequestCanonicalizesActorAddresses(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	for index, address := range []string{"agent", "@agent", agent.Fingerprint} {
		record, err := workspace.Submit(ctx, "human", workroom.SchemaState, workroom.State{
			Kind: workroom.KindRequest, Text: "address", Body: map[string]string{"to": address, "conditions": "canonical"},
		}, []string{workspace.EventID(workspace.Config.Genesis)}, nil, "address-"+string(rune('a'+index)))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := workroom.Decode(record.Schema, record.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if got := decoded.(*workroom.State).Body["to"]; got != agent.Fingerprint {
			t.Fatalf("address %q encoded as %q", address, got)
		}
	}
	if _, err := workspace.Submit(ctx, "human", workroom.SchemaState, workroom.State{
		Kind: workroom.KindRequest, Text: "bad", Body: map[string]string{"to": "missing", "conditions": "never"},
	}, []string{workspace.EventID(workspace.Config.Genesis)}, nil, "bad-address"); err == nil {
		t.Fatal("unknown request performer was accepted by the application edge")
	}
}

func TestSnapshotCachesTheVerifiedHead(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached := workspace.snapshotCache
	second, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.snapshotCache != cached || first.Head != second.Head {
		t.Fatal("unchanged head did not reuse the verified snapshot")
	}
	if _, err := workspace.Submit(ctx, "human", workroom.SchemaState, workroom.State{Kind: workroom.KindAssert, Text: "advance"}, []string{workspace.EventID(workspace.Config.Genesis)}, nil, "advance"); err != nil {
		t.Fatal(err)
	}
	third, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.snapshotCache == cached || third.Head == first.Head || third.Depth != first.Depth+1 {
		t.Fatalf("advanced head reused stale snapshot: first=%+v third=%+v", first, third)
	}
}
