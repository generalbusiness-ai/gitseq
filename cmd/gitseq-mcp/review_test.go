package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// reviewFixture builds the durable lane one guarded review needs: a candidate
// commit, an implementer's artifact at it, a review request, and the
// reviewer's promise. It returns the workspace, the artifact event, and the
// promise event.
func reviewFixture(t *testing.T) (*app.Workspace, string, string) {
	t.Helper()
	ctx := context.Background()
	workspace := initRepository(t, "review")
	repo := workspace.Repo
	if _, _, err := workspace.AddActor(ctx, "human", "reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "--allow-empty", "-m", "candidate").CombinedOutput(); err != nil {
		t.Fatalf("candidate commit: %v: %s", err, output)
	}
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	candidate := strings.TrimSpace(string(head))
	genesis := workspace.View().Genesis
	fingerprint := func(name string) string {
		return workspace.View().Actors[name].Fingerprint
	}
	artifact, err := workspace.Act(ctx, "human", app.Act{
		Verb:           app.VerbState,
		Kind:           workroom.KindArtifact,
		Text:           "feature artifact",
		Body:           map[string]string{"path": "feature.txt", "commit": candidate},
		RestsOn:        []string{"git:sha1:" + genesis},
		IdempotencyKey: "mcp-review-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState,
		Kind: workroom.KindRequest,
		Text: "review feature",
		Body: map[string]string{
			"to":         fingerprint("reviewer"),
			"conditions": "exact head",
		},
		RestsOn:        []string{artifact.Record.ID},
		IdempotencyKey: "mcp-review-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb:           app.VerbState,
		Kind:           workroom.KindPromise,
		Text:           "review exact head",
		RestsOn:        []string{request.Record.ID},
		IdempotencyKey: "mcp-review-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, artifact.Record.ID, promise.Record.ID
}

func TestReviewToolFilesAGuardedVerdict(t *testing.T) {
	parallelTest(t)
	workspace, artifact, promise := reviewFixture(t)
	server, _ := attachedServer(t, workspace, "reviewer", "", nil)

	value, _, err := server.call(context.Background(), toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "APPROVED exact head",
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	record := result["record"].(workroom.Record)
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == record.ID {
			body = statement.Body
		}
	}
	if body == nil {
		t.Fatalf("verdict %s did not project as a statement", record.ID)
	}
	// Body parity with `gs review`: same reserved fields, same citation shape.
	if body["verdict"] != "approved" || body["artifact"] != artifact || body["review_path"] != "reviewguard@1" {
		t.Fatalf("verdict body = %#v", body)
	}
	// The promise is sequenced after the request, so it is matched head news:
	// the canonical array records it in sequence order even though the verdict
	// cites it, and the frontier this surface confirmed is recorded beside it.
	if body["head_news_acknowledged"] != `["`+promise+`"]` || body["review_frontier"] == "" {
		t.Fatalf("verdict lost its guard fields: %#v", body)
	}
	var acknowledged []string
	if err := json.Unmarshal([]byte(body["head_news_acknowledged"]), &acknowledged); err != nil {
		t.Fatalf("head_news_acknowledged is not canonical JSON: %v", err)
	}
	// rests_on starts with the promise, then the request, then the artifacts.
	wantPrefix := fmt.Sprintf("%s,%s,%s", promise, firstRequestOf(t, snapshot.Projection, promise), artifact)
	if got := joinProvenance(snapshot.Projection.Provenance[record.ID]); !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("provenance = %s, want prefix %s", got, wantPrefix)
	}
}

func TestReviewToolRefusesUnacknowledgedHeadNewsAndTakesExactAcks(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace, artifact, promise := reviewFixture(t)
	server, _ := attachedServer(t, workspace, "reviewer", "", nil)

	// A stranger's statement naming the reviewed head lands after the request:
	// head news the verdict may not sign over.
	news, err := workspace.Act(ctx, "human", app.Act{
		Verb:           app.VerbState,
		Kind:           workroom.KindAssert,
		Text:           "the head was mentioned elsewhere",
		Body:           map[string]string{"head": headOfRepository(t, workspace.Repo)},
		RestsOn:        []string{artifact},
		IdempotencyKey: "mcp-head-news",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "must not be signed",
	}})
	if err == nil || !strings.Contains(err.Error(), news.Record.ID) || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("unacknowledged news error = %v", err)
	}

	_, _, err = server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "must not be signed",
		"ack_head_news": []any{news.Record.ID, news.Record.ID},
	}})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate acknowledgment error = %v", err)
	}

	_, _, err = server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "must not be signed",
		"ack_head_news": []any{genesisOf(t, workspace)},
	}})
	if err == nil || !strings.Contains(err.Error(), "extraneous") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("extraneous acknowledgment error = %v", err)
	}

	value, _, err := server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "APPROVED with news seen",
		"ack_head_news": []any{news.Record.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	record := result["record"].(workroom.Record)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(snapshot.Projection.Provenance[record.ID], news.Record.ID) {
		t.Fatalf("verdict provenance %v does not rest on the acknowledged news", snapshot.Projection.Provenance[record.ID])
	}
	for _, statement := range snapshot.Projection.Statements {
		if statement.Event == record.ID && statement.Body["head_news_acknowledged"] != `["`+promise+`","`+news.Record.ID+`"]` {
			t.Fatalf("acknowledged array = %#v, want the matched news in sequence order", statement.Body["head_news_acknowledged"])
		}
	}
}

// Caller-controlled acknowledgment values reach review-tool refusals only
// quoted and bounded, exactly as the command line's refusals do: both
// surfaces run the same reviewguard.Confirm choreography over their own
// reads.
func TestReviewToolRefusalsBoundCallerSuppliedValues(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace, artifact, promise := reviewFixture(t)
	server, _ := attachedServer(t, workspace, "reviewer", "", nil)
	huge := strings.Repeat("x", 4096)
	prefix := fmt.Sprintf("%q", huge[:120])
	_, _, err := server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "must not be signed",
		"ack_head_news": []any{huge, huge},
	}})
	if err == nil || !strings.Contains(err.Error(), "is given twice") ||
		!strings.Contains(err.Error(), prefix) || len(err.Error()) > 300 {
		t.Fatalf("duplicate acknowledgment error not bounded and diagnostic: %v", err)
	}
	_, _, err = server.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact}, "promise": promise,
		"verdict": "approved", "text": "must not be signed",
		"ack_head_news": []any{huge},
	}})
	if err == nil || !strings.Contains(err.Error(), "extraneous") ||
		!strings.Contains(err.Error(), prefix) || len(err.Error()) > 300 {
		t.Fatalf("extraneous acknowledgment error not bounded and diagnostic: %v", err)
	}
}

func TestStateToolSignsTheDeadBasisEscapeWhenAllowed(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace := initRepository(t, "dead-basis")
	server, _ := attachedServer(t, workspace, "human", "", nil)

	value, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "ground truth", "idempotency_key": "mcp-dead-ground",
	}})
	if err != nil {
		t.Fatal(err)
	}
	ground := value.(map[string]any)["record"].(workroom.Record).ID
	retire, _, err := server.call(ctx, toolCall{Name: "supersede", Arguments: map[string]any{
		"target": ground, "text": "withdrawn", "idempotency_key": "mcp-dead-retire",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if retire == nil {
		t.Fatal("supersede returned nothing")
	}

	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "standing on withdrawn ground",
		"rests_on": []any{ground}, "idempotency_key": "mcp-dead-refused",
	}})
	if err == nil || !strings.Contains(err.Error(), "already-dead basis") || !strings.Contains(err.Error(), "allow_dead_basis") {
		t.Fatalf("dead-basis state error = %v", err)
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head {
		t.Fatal("refused dead-basis state changed the workroom")
	}

	if _, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "I saw it",
		"rests_on": []any{ground}, "allow_dead_basis": true, "idempotency_key": "mcp-dead-allowed",
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if last.Body["dead_basis_override"] != "true" {
		t.Fatalf("escape did not sign the override: %#v", last.Body)
	}
}

// A generic state write may not speak for admission: a body carrying the
// guarded-review marker, a verdict word, or any other reserved field refuses
// before anything is signed, whatever kind it claims.
func TestStateToolRefusesReservedBodyFieldSpoofing(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace := initRepository(t, "spoofing")
	server, _ := attachedServer(t, workspace, "human", "", nil)
	before, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "report", "text": "a verdict that never went through the guard",
		"body":            map[string]any{"review_path": "reviewguard@1", "verdict": "approved"},
		"rests_on":        []any{genesisOf(t, workspace)},
		"idempotency_key": "mcp-spoofed-verdict",
	}})
	if err == nil || !strings.Contains(err.Error(), "reserved admission field") {
		t.Fatalf("spoofed verdict-shaped report error = %v", err)
	}
	for _, field := range []string{"head_news_acknowledged", "review_frontier"} {
		_, _, err = server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
			"kind": "assert", "text": "speaking for admission",
			"body": map[string]any{field: "true"}, "rests_on": []any{genesisOf(t, workspace)},
			"idempotency_key": "mcp-spoof-" + field,
		}})
		if err == nil || !strings.Contains(err.Error(), "reserved admission field") {
			t.Fatalf("spoofed body.%s error = %v", field, err)
		}
	}
	after, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("refused spoofing changed workroom: %s/%d -> %s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

// The undefined-kind refusal closes the door with directions, not just a no:
// a command-shaped kind names the dedicated command or tool that does exist.
func TestStateToolRefusesCommandShapedKindsByNamingTheirRoute(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace := initRepository(t, "routes")
	server, _ := attachedServer(t, workspace, "human", "", nil)
	for _, test := range []struct {
		kind string
		want string
	}{
		{kind: "ratify", want: "gs ratify"},
		{kind: "supersede", want: "supersede tools"},
		{kind: "review", want: "gs review"},
	} {
		before, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
			"kind": test.kind, "text": "the wrong door",
			"rests_on": []any{genesisOf(t, workspace)}, "idempotency_key": "route-" + test.kind,
		}})
		if err == nil || !strings.Contains(err.Error(), "no override exists") || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("kind %q error = %v, want a refusal naming %q", test.kind, err, test.want)
		}
		after, err := workspace.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Head != before.Head {
			t.Fatalf("refused %q changed workroom", test.kind)
		}
	}
}

func firstRequestOf(t *testing.T, projection workroom.Projection, promise string) string {
	t.Helper()
	for _, basis := range projection.Provenance[promise] {
		for _, statement := range projection.Statements {
			if statement.Event == basis && statement.Kind == workroom.KindRequest {
				return statement.Event
			}
		}
	}
	t.Fatal("promise has no request basis")
	return ""
}

func joinProvenance(events []string) string {
	return strings.Join(events, ",")
}

func headOfRepository(t *testing.T, repo string) string {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(head))
}
