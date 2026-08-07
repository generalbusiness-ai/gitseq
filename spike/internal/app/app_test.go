package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitseq/spike/internal/intent"
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

func actRecord(t *testing.T, ctx context.Context, workspace *Workspace, actor string, act Act) workroom.Record {
	t.Helper()
	submission, err := workspace.Act(ctx, actor, act)
	if err != nil {
		t.Fatal(err)
	}
	return submission.Record
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
	request := actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindRequest, Text: "build", Body: map[string]string{"to": "agent", "conditions": "tests pass"}, RestsOn: []string{seed.ID}, IdempotencyKey: "request"})
	promise := actRecord(t, ctx, workspace, "agent", Act{Verb: VerbState, Kind: workroom.KindPromise, Text: "I promise", RestsOn: []string{request.ID}, IdempotencyKey: "promise"})
	report := actRecord(t, ctx, workspace, "agent", Act{Verb: VerbState, Kind: workroom.KindReport, Text: "done", RestsOn: []string{promise.ID}, IdempotencyKey: "report"})
	actRecord(t, ctx, workspace, "human", Act{Verb: VerbRatify, Target: report.ID, IdempotencyKey: "satisfy"})
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

func TestLinkedWorktreeSharesRepositoryWorkroom(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, seed, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}
	fromLinked, err := Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if fromLinked.GitDir == workspace.GitDir {
		t.Fatalf("linked checkout reused main git dir %q", fromLinked.GitDir)
	}
	if fromLinked.CommonDir != workspace.CommonDir || fromLinked.MetaDir != workspace.MetaDir || fromLinked.Store.Repo != workspace.Store.Repo {
		t.Fatalf("linked checkout did not share repository state: main=%+v linked=%+v", workspace, fromLinked)
	}
	if _, err := os.Stat(filepath.Join(fromLinked.CommonDir, "gitseq", "config.json")); err != nil {
		t.Fatalf("common gitseq config: %v", err)
	}
	before, err := fromLinked.Verify(ctx)
	if err != nil || before.Depth != 1 {
		t.Fatalf("verify linked worktree: verification=%+v err=%v", before, err)
	}
	actRecord(t, ctx, fromLinked, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "written from linked checkout",
		RestsOn: []string{seed.ID}, IdempotencyKey: "linked-write",
	})
	after, err := workspace.Snapshot(ctx)
	if err != nil || after.Depth != 2 {
		t.Fatalf("main checkout did not observe linked append: snapshot=%+v err=%v", after, err)
	}
	actRecord(t, ctx, workspace, "human", Act{
		Verb: VerbState, Kind: workroom.KindAssert, Text: "written after linked checkout",
		RestsOn: []string{seed.ID}, IdempotencyKey: "main-after-linked",
	})
	final, err := fromLinked.Snapshot(ctx)
	if err != nil || final.Depth != 3 {
		t.Fatalf("resident cache did not recover from external advance: snapshot=%+v err=%v", final, err)
	}
}

func TestLocalWorktreesProjectsLinkedCheckoutStateWithoutPaths(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, _, err := Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	mainBranchOutput, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil {
		t.Fatal(err)
	}
	mainBranch := strings.TrimSpace(string(mainBranchOutput))
	linked := filepath.Join(t.TempDir(), "linked checkout")
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "task/local-view", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(linked, "unfinished.txt"), []byte("local only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	views, err := workspace.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("worktrees = %#v", views)
	}
	byBranch := make(map[string]WorktreeView, len(views))
	for _, view := range views {
		byBranch[view.Branch] = view
		if strings.Contains(view.Checkout, string(filepath.Separator)) {
			t.Fatalf("checkout exposed a path: %#v", view)
		}
	}
	if main := byBranch[mainBranch]; !main.Current || main.State != "clean" || main.Head == "" {
		t.Fatalf("main checkout = %#v", main)
	}
	if linkedView := byBranch["task/local-view"]; linkedView.Current || linkedView.State != "dirty" || linkedView.Checkout != "linked checkout" || linkedView.Head == "" {
		t.Fatalf("linked checkout = %#v", linkedView)
	}

	fromLinked, err := Open(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	linkedViews, err := fromLinked.LocalWorktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(linkedViews) != 2 || linkedViews[0].Branch != "task/local-view" || !linkedViews[0].Current {
		t.Fatalf("selected linked checkout not projected first: %#v", linkedViews)
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
		record := actRecord(t, ctx, workspace, "human", Act{
			Verb: VerbState, Kind: workroom.KindRequest, Text: "address", Body: map[string]string{"to": address, "conditions": "canonical"},
			RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "address-" + string(rune('a'+index)),
		})
		decoded, err := workroom.Decode(record.Schema, record.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if got := decoded.(*workroom.State).Body["to"]; got != agent.Fingerprint {
			t.Fatalf("address %q encoded as %q", address, got)
		}
	}
	if _, err := workspace.Act(ctx, "human", Act{
		Verb: VerbState, Kind: workroom.KindRequest, Text: "bad", Body: map[string]string{"to": "missing", "conditions": "never"},
		RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "bad-address",
	}); err == nil {
		t.Fatal("unknown request performer was accepted by the application edge")
	}
}

func TestIdempotencyNamespaceIsStableAndLegacySafe(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	build := func(key string) string {
		request, err := workspace.BuildActRequest(ctx, private, "human", Act{
			Verb: VerbState, Kind: workroom.KindAssert, Text: key,
			RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := intent.Verify(request.Signed)
		if err != nil {
			t.Fatal(err)
		}
		return decoded.IdempotencyNS
	}
	if got := build("stable"); got != "workroom/v0" {
		t.Fatalf("new namespace = %q", got)
	}
	workspace.Config.IdempotencyNamespace = ""
	if got := build("legacy"); got != "gs/human" {
		t.Fatalf("legacy namespace = %q", got)
	}
}

func TestAgentRatifierAuthorityLifecycle(t *testing.T) {
	ctx := context.Background()
	workspace, seed, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	agent, _, err := workspace.AddActor(ctx, "human", "agent", "agent")
	if err != nil {
		t.Fatal(err)
	}
	proposal := func(key string) workroom.Record {
		return actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindPropose, Text: key, RestsOn: []string{seed.ID}, IdempotencyKey: key})
	}
	ratifyAsAgent := func(target, key string) workroom.Record {
		return actRecord(t, ctx, workspace, "agent", Act{Verb: VerbRatify, Target: target, IdempotencyKey: key})
	}
	verdict := func(event string) workroom.Verdict {
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		decision, ok := snapshot.Projection.Decision(event)
		if !ok {
			t.Fatalf("missing decision for %s", event)
		}
		return decision.Verdict
	}

	before := ratifyAsAgent(proposal("before-grant").ID, "before-grant-ratify")
	if got := verdict(before.ID); got != workroom.Ineffective {
		t.Fatalf("ordinary agent ratification = %s, want ineffective", got)
	}
	if _, err := workspace.GrantRole(ctx, "agent", "agent", "ratifier"); err != nil {
		t.Fatal(err)
	}
	selfGranted := ratifyAsAgent(proposal("after-self-grant").ID, "after-self-grant-ratify")
	if got := verdict(selfGranted.ID); got != workroom.Ineffective {
		t.Fatalf("self-granted agent ratification = %s, want ineffective", got)
	}
	grant, err := workspace.GrantRole(ctx, "human", "agent", "ratifier")
	if err != nil {
		t.Fatal(err)
	}
	grantState, err := workroom.Decode(grant[0].Schema, grant[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := grantState.(*workroom.State).Body["kind"]; got != "agent" {
		t.Fatalf("modern role grant kind = %q, want agent", got)
	}
	during := ratifyAsAgent(proposal("during-grant").ID, "during-grant-ratify")
	if got := verdict(during.ID); got != workroom.Effective {
		t.Fatalf("ratifier agent ratification = %s, want effective", got)
	}
	if _, err := workspace.RevokeRole(ctx, "human", "agent", "ratifier"); err != nil {
		t.Fatal(err)
	}
	after := ratifyAsAgent(proposal("after-revoke").ID, "after-revoke-ratify")
	if got := verdict(after.ID); got != workroom.Ineffective {
		t.Fatalf("revoked agent ratification = %s, want ineffective", got)
	}

	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorState := snapshot.Projection.Actors[agent.Fingerprint]
	if got := actorState.Kind; got != "agent" {
		t.Fatalf("projected actor kind = %q, want agent", got)
	}
	roles := actorState.Roles
	if len(roles) != 1 || roles[0] != "participant" {
		t.Fatalf("roles after revocation = %#v, want participant only", roles)
	}
	if len(actorState.RoleSources["participant"]) != 1 || actorState.MembershipEvent == "" {
		t.Fatalf("actor projection omitted membership provenance: %+v", actorState)
	}
	views, err := workspace.ActorViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Name != "agent" || views[0].Kind != "agent" || len(views[0].Roles) != 1 || views[0].Roles[0] != "participant" || !views[0].Custody {
		t.Fatalf("actor views did not join custody to durable state: %+v", views)
	}
}

func TestActorViewsEnumerateDurableActorsWithoutLocalCustody(t *testing.T) {
	ctx := context.Background()
	workspace, _, err := Init(ctx, testRepo(t), "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.AddActor(ctx, "human", "agent", "agent"); err != nil {
		t.Fatal(err)
	}
	attached := &Workspace{
		Repo: workspace.Repo, GitDir: workspace.GitDir, CommonDir: workspace.CommonDir, Store: workspace.Store,
		Config: Config{Version: 0, Genesis: workspace.Config.Genesis, ObjectFormat: workspace.Config.ObjectFormat, ReadOnly: true},
	}
	views, err := attached.ActorViews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Name != "agent" || views[1].Name != "human" {
		t.Fatalf("attached actor views omitted durable actors: %+v", views)
	}
	for _, view := range views {
		if view.Custody {
			t.Fatalf("attached view falsely reports local custody: %+v", view)
		}
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
	actRecord(t, ctx, workspace, "human", Act{Verb: VerbState, Kind: workroom.KindAssert, Text: "advance", RestsOn: []string{workspace.EventID(workspace.Config.Genesis)}, IdempotencyKey: "advance"})
	third, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.snapshotCache == cached || third.Head == first.Head || third.Depth != first.Depth+1 {
		t.Fatalf("advanced head reused stale snapshot: first=%+v third=%+v", first, third)
	}
}
