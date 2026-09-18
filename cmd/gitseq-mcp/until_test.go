package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

// The `until` filter as this adapter carries it: what it advertises, what it
// puts on the wire, and what it does when there is no resident to put it to.

func TestWaitToolAdvertisesTheUntilFilter(t *testing.T) {
	parallelTest(t)
	var wait map[string]any
	for _, tool := range tools() {
		if tool["name"] == "wait" {
			wait = tool
		}
	}
	if wait == nil {
		t.Fatal("the adapter serves no wait tool")
	}
	encoded, err := json.Marshal(wait["inputSchema"])
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	until, declared := schema.Properties["until"]
	if !declared {
		t.Fatal("the wait tool's schema declares no `until`; a caller cannot ask for the filter it documents")
	}
	if strings.Join(until.Enum, ",") != "actionable,any" {
		t.Errorf("until enum = %v, want exactly actionable and any", until.Enum)
	}
}

// `any` is the absent field, not the word. A resident built before the filter
// decodes this request strictly and refuses any spelling of it, while already
// implementing exactly what `any` asks for.
func TestWaitToolSendsAnyAsAnAbsentField(t *testing.T) {
	parallelTest(t)
	workspace, _ := signedWorkspace(t, 1)
	resident, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []string
	recorder := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v0/actor-wait" {
			body, _ := io.ReadAll(request.Body)
			request.Body = io.NopCloser(bytes.NewReader(body))
			mu.Lock()
			bodies = append(bodies, string(body))
			mu.Unlock()
		}
		resident.Handler().ServeHTTP(response, request)
	}))
	t.Cleanup(recorder.Close)
	server, attached := attachedServer(t, workspace, "human", recorder.URL, recorder.Client())
	attached.identityNoticeChecked = true
	if err := server.announce(context.Background(), attached); err != nil {
		t.Fatal(err)
	}
	attached.joined()

	for _, until := range []any{nil, "any", "actionable"} {
		arguments := map[string]any{"cursor": map[string]any{}, "timeout_ms": 1}
		if until != nil {
			arguments["until"] = until
		}
		if _, _, err := server.call(context.Background(), toolCall{Name: "wait", Arguments: arguments}); err != nil {
			t.Fatalf("wait with until=%v: %v", until, err)
		}
	}
	mu.Lock()
	seen := append([]string(nil), bodies...)
	mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("the resident saw %d waits, want 3: %v", len(seen), seen)
	}
	bodies = seen
	for index, body := range bodies[:2] {
		if strings.Contains(body, `"until"`) {
			t.Errorf("wait %d sent an `until` field for the unfiltered case: %s", index, body)
		}
	}
	if !strings.Contains(bodies[2], `"until":"actionable"`) {
		t.Errorf("the filtered wait did not send until=actionable: %s", bodies[2])
	}
}

// With no resident the adapter folds the log itself, and the filter has to
// mean the same thing there. Before this it did not: the degraded wait
// returned on any durable change whatever the caller asked for.
func TestDegradedWaitHonoursTheUntilFilter(t *testing.T) {
	parallelTest(t)
	workspace, genesis := signedWorkspace(t, 1)
	dead := httptest.NewServer(nil)
	baseURL, client := dead.URL, dead.Client()
	dead.Close()
	server, attached := attachedServer(t, workspace, "human", baseURL, client)

	// A second actor files something that is nobody's business but their own.
	if _, _, err := workspace.AddActor(context.Background(), "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	other, err := workspace.ResolveActor("other")
	if err != nil {
		t.Fatal(err)
	}
	// The genesis record is the waiting actor's own, so anything resting on it
	// is theirs by rule three. The unrelated act therefore has to stand on one
	// of the other actor's own records, which is what work between two other
	// people looks like.
	foothold, err := workspace.Act(context.Background(), "other", app.Act{Verb: app.VerbState,
		Kind: workroom.KindAssert, Text: "their own ground", RestsOn: []string{genesis.ID},
		IdempotencyKey: "degraded-foothold"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := server.localStatus(context.Background(), attached)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Act(context.Background(), "other", app.Act{Verb: app.VerbState,
		Kind: workroom.KindAssert, Text: "between other people", RestsOn: []string{foothold.Record.ID},
		IdempotencyKey: "degraded-unrelated"}); err != nil {
		t.Fatal(err)
	}

	unfiltered, _, err := server.call(context.Background(), toolCall{Name: "wait",
		Arguments: map[string]any{"cursor": before.Cursor, "timeout_ms": 1, "until": "any"}})
	if err != nil {
		t.Fatal(err)
	}
	if delta := unfiltered.(waitDelta); !delta.Changed {
		t.Fatalf("the degraded wait under `any` did not report the durable change: %+v", delta)
	}

	filtered, _, err := server.call(context.Background(), toolCall{Name: "wait",
		Arguments: map[string]any{"cursor": before.Cursor, "timeout_ms": 400, "until": "actionable"}})
	if err != nil {
		t.Fatal(err)
	}
	delta := filtered.(waitDelta)
	if delta.Changed || len(delta.Accepted) != 0 {
		t.Fatalf("the degraded wait under `actionable` woke on an act between two other actors: %+v", delta)
	}

	// The same degraded path must still wake on what is this actor's.
	if _, err := workspace.Act(context.Background(), "other", app.Act{Verb: app.VerbState,
		Kind: workroom.KindRequest, Text: "for the human", RestsOn: []string{foothold.Record.ID},
		Body: map[string]string{"to": workspace.View().Actors["human"].Fingerprint,
			"conditions": "say so", "no_git_artifact": "true"},
		IdempotencyKey: "degraded-addressed"}); err != nil {
		t.Fatal(err)
	}
	if other.Fingerprint == "" {
		t.Fatal("the second actor has no fingerprint")
	}
	woken, _, err := server.call(context.Background(), toolCall{Name: "wait",
		Arguments: map[string]any{"cursor": before.Cursor, "timeout_ms": 400, "until": "actionable"}})
	if err != nil {
		t.Fatal(err)
	}
	if delta := woken.(waitDelta); !delta.Changed || len(delta.Accepted) == 0 {
		t.Fatalf("the degraded wait under `actionable` missed a request addressed to this actor: %+v", delta)
	}
	if _, _, err := server.call(context.Background(), toolCall{Name: "wait",
		Arguments: map[string]any{"cursor": before.Cursor, "timeout_ms": 1, "until": "whenever"}}); err == nil {
		t.Error("the degraded wait accepted an `until` that is neither actionable nor any")
	}
	if _, err := statusview.ParseUntil("actionable"); err != nil {
		t.Fatal(err)
	}
}
