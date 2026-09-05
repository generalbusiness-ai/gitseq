package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// A selector that initially names no actor succeeds after another process adds
// that actor. A refused selection never seeds the binding cache, so the next
// call takes the cold validation path and sees the new custody record.
func TestAdapterResolvesActorAddedAfterSessionStart(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	server := newServer("late", workspace.Repo)
	if _, _, err := server.call(ctx, toolCall{Name: "whoami"}); !errors.Is(err, app.ErrUnknownActor) {
		t.Fatalf("whoami before the actor exists = %v, want unknown actor", err)
	}
	external, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	added, _, err := external.AddActor(ctx, "human", "late", "agent")
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := server.call(ctx, toolCall{Name: "whoami"})
	if err != nil {
		t.Fatalf("whoami after the actor was added: %v", err)
	}
	actor := value.(map[string]any)["actor"].(map[string]string)
	if actor["fingerprint"] != added.Fingerprint {
		t.Fatalf("whoami after the actor was added = %v, want %s", actor, added.Fingerprint)
	}
}

// A state request addressed to a recipient created after the session opened
// still delivers: body.to resolves through a fresh re-read of local custody,
// so the signed record names the late recipient's fingerprint instead of
// refusing a live actor as unknown. Reverting the request path to the cached
// view alone fails this test by name.
func TestAdapterStateRequestDeliversToActorAddedAfterSessionStart(t *testing.T) {
	parallelTest(t)
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

// authorizationRoom stands up one workroom, one adapter session, and one open
// request the session's own actor may report against, so an authorization
// report filed through the tool is judged on its own lifecycle basis and the
// only thing left to refuse it is the landing target it claims to have
// measured.
type authorizationRoom struct {
	ctx       context.Context
	workspace *app.Workspace
	server    *mcpServer
	request   string
	ref       string
}

func newAuthorizationRoom(t *testing.T) authorizationRoom {
	t.Helper()
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	room := authorizationRoom{
		ctx: ctx, workspace: workspace, server: newServer("human", workspace.Repo),
		ref: mergePlanTestGit(t, workspace.Repo, "symbolic-ref", "HEAD"),
	}
	request, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "release the landing hold",
		Body:    map[string]string{"to": "human", "conditions": "release the landing hold"},
		RestsOn: []string{genesis.ID}, IdempotencyKey: "authorization-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	room.request = request.Record.ID
	return room
}

// commit advances the workroom repository's checked-out branch, which is the
// landing target these reports measure.
func (r authorizationRoom) commit(t *testing.T, message string) string {
	t.Helper()
	mergePlanTestGit(t, r.workspace.Repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "--allow-empty", "-qm", message)
	return mergePlanTestGit(t, r.workspace.Repo, "rev-parse", "HEAD")
}

// file calls the adapter's state tool exactly as a session would.
func (r authorizationRoom) file(key, text, measured string) error {
	_, _, err := r.server.call(r.ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "report", "text": text,
		"body": map[string]any{
			"authorizes_request": r.request,
			"target_ref":         r.ref,
			"target_pre_head":    measured,
		},
		"rests_on":        []any{r.request},
		"idempotency_key": key,
	}})
	return err
}

func (r authorizationRoom) snapshot(t *testing.T) app.Snapshot {
	t.Helper()
	snapshot, err := r.workspace.Snapshot(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// The adapter files durable acts through the same boundary `gs state` uses, so
// an authorization or release report that measured a landing target the ref no
// longer holds is refused here too. Without the check this surface would be the
// way to sign a measurement the merge will later refuse.
func TestAdapterStateRefusesAnAuthorizationMeasuredAgainstAMovedTarget(t *testing.T) {
	parallelTest(t)
	room := newAuthorizationRoom(t)
	measured := room.commit(t, "the target the signer measured")
	moved := room.commit(t, "advance the target after the measurement")

	err := room.file("moved-authorization-target", "release the landing hold", measured)
	if err == nil || !strings.Contains(err.Error(), room.ref+" is at "+moved) {
		t.Fatalf("moved-target authorization error = %v", err)
	}
}

// The filing-time check reads a ref that moves; the act it guards does not.
// An exact retry of a report already accepted is a caller recovering a lost
// response, and it replays the event that was accepted when the world still
// agreed with the measurement. Re-measuring it would refuse a recovery while
// signing no fresh authorization at all.
func TestAdapterStateReplaysAnAcceptedAuthorizationAfterTheTargetMoves(t *testing.T) {
	parallelTest(t)
	room := newAuthorizationRoom(t)
	measured := room.commit(t, "the target the signer measured")
	if err := room.file("accepted-authorization", "release the landing hold", measured); err != nil {
		t.Fatalf("first submission: %v", err)
	}
	accepted := room.snapshot(t)

	moved := room.commit(t, "advance the target after the report was accepted")
	if err := room.file("fresh-authorization", "release the landing hold", measured); err == nil ||
		!strings.Contains(err.Error(), room.ref+" is at "+moved) {
		t.Fatalf("a fresh report measured against the moved target must be refused: %v", err)
	}
	if after := room.snapshot(t); after.Depth != accepted.Depth || after.Head != accepted.Head {
		t.Fatalf("the refused fresh report appended: depth %d -> %d", accepted.Depth, after.Depth)
	}

	if err := room.file("accepted-authorization", "release the landing hold", measured); err != nil {
		t.Fatalf("the exact retry was rejudged against the moved target: %v", err)
	}
	if after := room.snapshot(t); after.Depth != accepted.Depth || after.Head != accepted.Head {
		t.Fatalf("the exact retry appended instead of replaying: depth %d -> %d, head %s -> %s",
			accepted.Depth, after.Depth, accepted.Head, after.Head)
	}

	// The same key carrying different words is not the accepted act, so the
	// key alone buys nothing: the payload identity rule refuses it.
	if err := room.file("accepted-authorization", "release something else", measured); err == nil ||
		!strings.Contains(err.Error(), "idempotency key reused with different intent") {
		t.Fatalf("a changed payload under the accepted key must be refused: %v", err)
	}
	if after := room.snapshot(t); after.Depth != accepted.Depth || after.Head != accepted.Head {
		t.Fatalf("the changed-payload retry appended: depth %d -> %d", accepted.Depth, after.Depth)
	}
}
