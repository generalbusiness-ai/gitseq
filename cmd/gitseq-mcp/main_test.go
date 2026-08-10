package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestStatelessDiscoverAndToolList(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()
	server, attached := attachedServer(t, workspace, "human", httpServer.URL, httpServer.Client())
	if err := server.announce(context.Background(), attached); err != nil {
		t.Fatal(err)
	}
	defer server.depart(context.Background())
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\",\"params\":{" + meta + "}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{" + meta + "}}\n{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{" + meta + ",\"name\":\"whoami\",\"arguments\":{}}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses: %s", len(lines), output.String())
	}
	var discovery map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &discovery); err != nil {
		t.Fatal(err)
	}
	discoverResult := discovery["result"].(map[string]any)
	if discoverResult["resultType"] != "complete" || len(discoverResult["supportedVersions"].([]any)) != 1 || discoverResult["protocolVersion"] != nil {
		t.Fatalf("non-conforming discovery result: %#v", discoverResult)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &response); err != nil {
		t.Fatal(err)
	}
	result := response["result"].(map[string]any)
	if result["resultType"] != "complete" {
		t.Fatalf("tool list has no complete result type: %#v", result)
	}
	if got := len(result["tools"].([]any)); got != 10 {
		t.Fatalf("got %d tools", got)
	}
	listed := make(map[string]map[string]any)
	for _, tool := range result["tools"].([]any) {
		definition := tool.(map[string]any)
		listed[definition["name"].(string)] = definition
		schema := definition["inputSchema"].(map[string]any)
		if properties, exists := schema["properties"]; exists && properties == nil {
			t.Fatalf("tool schema contains properties:null: %#v", tool)
		}
	}
	for _, name := range []string{"work", "inspect"} {
		if listed[name] == nil {
			t.Fatalf("selective tool %q is missing: %#v", name, listed)
		}
	}
	workProperties := listed["work"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if workProperties["lanes"] == nil || workProperties["statuses"] == nil || workProperties["cursor"] == nil {
		t.Fatalf("work schema does not expose finite filters and continuation: %#v", workProperties)
	}
	var callResponse map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &callResponse); err != nil {
		t.Fatal(err)
	}
	callResult := callResponse["result"].(map[string]any)
	if callResult["resultType"] != "complete" || callResult["isError"] != false || callResult["structuredContent"] == nil {
		t.Fatalf("non-conforming tool result: %#v", callResult)
	}
}

func TestMissingPerRequestMetadataIsRejected(t *testing.T) {
	server := &mcpServer{}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["result"] != nil || response["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatalf("missing metadata response: %#v", response)
	}
}

// A legacy client opens with initialize and then omits _meta entirely. Both
// halves matter: rejecting either one leaves such a client unable to attach,
// with no fall-forward mechanism of its own.
func TestLegacyHandshakeServesToolsWithoutPerRequestMetadata(t *testing.T) {
	server := &mcpServer{}
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"legacy\",\"version\":\"1\"}}}\n" +
			"{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected one reply per request, got %d: %s", len(lines), output.String())
	}
	var initialize map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
		t.Fatal(err)
	}
	result, ok := initialize["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize rejected: %#v", initialize)
	}
	// The client's own revision is echoed back, so it need not downgrade.
	if result["protocolVersion"] != "2025-06-18" || result["serverInfo"] == nil {
		t.Fatalf("non-conforming initialize result: %#v", result)
	}
	var list map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &list); err != nil {
		t.Fatal(err)
	}
	listResult, ok := list["result"].(map[string]any)
	if !ok {
		t.Fatalf("legacy tools/list rejected: %#v", list)
	}
	if len(listResult["tools"].([]any)) == 0 {
		t.Fatalf("legacy tools/list served no tools: %#v", listResult)
	}
	// Modern envelope fields are meaningless to a client that negotiated once.
	if listResult["resultType"] != nil || listResult["ttlMs"] != nil || listResult["_meta"] != nil {
		t.Fatalf("legacy result carries modern envelope: %#v", listResult)
	}
}

// Initialization opens a connection, so it happens once. A second one would
// renegotiate the version mid-stream and change what the client was already
// told the connection is speaking.
func TestSecondInitializeIsRejectedAndDoesNotChangeTheNegotiatedVersion(t *testing.T) {
	server := &mcpServer{}
	open := func(version string) string {
		return "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"" + version +
			"\",\"capabilities\":{},\"clientInfo\":{\"name\":\"legacy\",\"version\":\"1\"}}}\n"
	}
	input := strings.NewReader(open("2025-06-18") + strings.Replace(open("2024-11-05"), "\"id\":1", "\"id\":2", 1))
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two replies, got %d: %s", len(lines), output.String())
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	negotiated := first["result"].(map[string]any)["protocolVersion"]
	if negotiated != "2025-06-18" {
		t.Fatalf("first initialize negotiated %v", negotiated)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["result"] != nil || second["error"] == nil {
		t.Fatalf("second initialize accepted, renegotiating the version: %#v", second)
	}
	if server.era != eraLegacy {
		t.Fatalf("era disturbed by the refused second initialize: %v", server.era)
	}
}

// The opening selects the era once. A connection that has spoken modern must
// not be able to hand itself back to the legacy handshake, because doing so
// would shed the per-request metadata the modern revision requires.
func TestInitializeAfterModernRequestIsRejectedAndDoesNotChangeEra(t *testing.T) {
	server := &mcpServer{}
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{" + meta + "}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"legacy\",\"version\":\"1\"}}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/list\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected three replies, got %d: %s", len(lines), output.String())
	}
	var modern, crossed, after map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &modern); err != nil {
		t.Fatal(err)
	}
	if modern["result"] == nil {
		t.Fatalf("modern tools/list rejected: %#v", modern)
	}
	if err := json.Unmarshal([]byte(lines[1]), &crossed); err != nil {
		t.Fatal(err)
	}
	if crossed["result"] != nil || crossed["error"] == nil {
		t.Fatalf("initialize accepted on a modern connection: %#v", crossed)
	}
	if server.era != eraModern {
		t.Fatalf("era changed away from modern: %v", server.era)
	}
	// The decisive check: metadata may not become optional afterwards.
	if err := json.Unmarshal([]byte(lines[2]), &after); err != nil {
		t.Fatal(err)
	}
	if after["result"] != nil {
		t.Fatalf("metadata-free request served after rejected initialize: %#v", after)
	}
}

// Latching the era is a commitment for the whole connection, so it must not be
// made on a handshake that never established what the client speaks.
func TestMalformedInitializeIsRejectedAndLeavesEraUndetermined(t *testing.T) {
	for name, params := range map[string]string{
		"empty object":               `{}`,
		"missing capabilities":       `{"protocolVersion":"2025-06-18","clientInfo":{"name":"x"}}`,
		"missing clientInfo":         `{"protocolVersion":"2025-06-18","capabilities":{}}`,
		"missing clientInfo.version": `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"x"}}`,
		"empty protocolVersion":      `{"protocolVersion":"","capabilities":{},"clientInfo":{"name":"x"}}`,
		"wrong types":                `{"protocolVersion":7,"capabilities":[],"clientInfo":"nope"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := &mcpServer{}
			input := strings.NewReader(
				"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":" + params + "}\n" +
					"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
			var output bytes.Buffer
			if err := server.run(context.Background(), input, &output); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			var initialize, follow map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
				t.Fatal(err)
			}
			failure, ok := initialize["error"].(map[string]any)
			if !ok || failure["code"] != float64(-32602) {
				t.Fatalf("malformed initialize accepted: %#v", initialize)
			}
			if server.era != eraUndetermined {
				t.Fatalf("era latched by a malformed handshake: %v", server.era)
			}
			if err := json.Unmarshal([]byte(lines[1]), &follow); err != nil {
				t.Fatal(err)
			}
			if follow["result"] != nil {
				t.Fatalf("metadata-free request served after malformed initialize: %#v", follow)
			}
		})
	}
}

// server/discover belongs to the modern revision; a legacy connection must not
// receive the modern envelope through it.
func TestDiscoverIsUnavailableOnALegacyConnection(t *testing.T) {
	server := &mcpServer{}
	input := strings.NewReader(
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"legacy\",\"version\":\"1\"}}}\n" +
			"{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"server/discover\",\"params\":{}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	var discover map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &discover); err != nil {
		t.Fatal(err)
	}
	if discover["result"] != nil {
		t.Fatalf("legacy connection received a modern discover envelope: %#v", discover)
	}
	if discover["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unexpected discover error: %#v", discover)
	}
}

// A modern client that names a version we do not speak must be told what we do
// speak, so it can retry rather than give up.
func TestUnsupportedModernVersionNamesSupportedVersions(t *testing.T) {
	server := &mcpServer{}
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{\"_meta\":{\"io.modelcontextprotocol/protocolVersion\":\"1900-01-01\",\"io.modelcontextprotocol/clientCapabilities\":{}}}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	failure, ok := response["error"].(map[string]any)
	if !ok || failure["code"] != float64(-32022) {
		t.Fatalf("expected UnsupportedProtocolVersionError: %#v", response)
	}
	data, ok := failure["data"].(map[string]any)
	if !ok || data["requested"] != "1900-01-01" {
		t.Fatalf("error does not echo the requested version: %#v", failure)
	}
	supported, ok := data["supported"].([]any)
	if !ok || len(supported) == 0 || supported[0] != protocolVersion {
		t.Fatalf("error does not name supported versions: %#v", failure)
	}
}

func TestDurableToolsDegradeWithoutResidentService(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, genesis, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	dead := httptest.NewServer(nil)
	baseURL := dead.URL
	client := dead.Client()
	dead.Close()
	server, attached := attachedServer(t, workspace, "human", baseURL, client)

	value, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	// The MCP surface answers with an actor digest, but degradation must still
	// be stated rather than papered over, and durable depth must still be real.
	digest, ok := value.(actorStatus)
	if !ok || digest.Totals.Depth != 1 || !digest.Live.Degraded {
		t.Fatalf("unexpected degraded status: %#v", value)
	}
	if digest.Live.Generation != "" {
		t.Fatalf("degraded status invented a live generation: %#v", digest.Live)
	}

	value, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "durable while the nexus is down", "rests_on": []any{genesis.ID}, "idempotency_key": "offline-state",
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["degraded"] != true {
		t.Fatalf("direct durable submission was not marked degraded: %#v", result)
	}

	status, err := server.localStatus(context.Background(), attached)
	if err != nil || status.Durable.Depth != 2 {
		t.Fatalf("durable append did not land: status=%+v err=%v", status, err)
	}
	waited, err := server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{"cursor": status.Cursor, "timeout_ms": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if delta := waited.(waitDelta); delta.Totals.Depth != 2 || delta.Reset {
		t.Fatalf("unexpected degraded wait response: %+v", delta)
	}

	worked, err := server.call(context.Background(), toolCall{Name: "work", Arguments: map[string]any{"limit": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page := worked.(statusview.WorkPage); !page.Degraded || page.Frontier.Depth != 2 || page.Actor.Fingerprint != workspace.Config.Actors["human"].Fingerprint {
		t.Fatalf("unexpected degraded work page: %+v", page)
	}
	inspected, err := server.call(context.Background(), toolCall{Name: "inspect", Arguments: map[string]any{"event": genesis.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if item := inspected.(statusview.ItemInspection); !item.Degraded || item.Event != genesis.ID || item.Decision == nil {
		t.Fatalf("unexpected degraded inspection: %+v", item)
	}

	projection, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decision := range projection.Projection.Decisions {
		if decision.Verdict == workroom.Effective && decision.Event != genesis.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("offline durable state did not project: %+v", projection.Projection.Decisions)
	}
}

func TestSelectiveToolsUseResidentSelectionWithoutFetchingStatus(t *testing.T) {
	workspace := initRepository(t, "repo")
	resident, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var paths []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		if request.URL.Path == "/v0/status" {
			http.Error(response, "selective tools must not fetch the full projection", http.StatusInternalServerError)
			return
		}
		resident.Handler().ServeHTTP(response, request)
	}))
	defer httpServer.Close()

	server, attached := attachedServer(t, workspace, "human", httpServer.URL, httpServer.Client())
	// This test concerns query routing, not presence or the sole-identity gate.
	// Mark the already-attached test session as joined so those independent
	// calls cannot obscure which endpoints work and inspect choose.
	attached.checked = true
	attached.announced = true

	value, err := server.call(context.Background(), toolCall{Name: "work", Arguments: map[string]any{"limit": 1}})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := value.(statusview.WorkPage)
	if !ok || page.Frontier.Head == "" || page.Actor.Fingerprint != workspace.Config.Actors["human"].Fingerprint {
		t.Fatalf("unexpected selective work response: %#v", value)
	}

	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	event := snapshot.Projection.Actors[workspace.Config.Actors["human"].Fingerprint].MembershipEvent
	value, err = server.call(context.Background(), toolCall{Name: "inspect", Arguments: map[string]any{"event": event}})
	if err != nil {
		t.Fatal(err)
	}
	inspection, ok := value.(statusview.ItemInspection)
	if !ok || inspection.Event != event || inspection.Statement == nil || inspection.Decision == nil {
		t.Fatalf("unexpected exact inspection response: %#v", value)
	}

	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	if strings.Join(gotPaths, ",") != "/v0/work-query,/v0/inspect" {
		t.Fatalf("selective tools used the wrong resident routes: %v", gotPaths)
	}
}

// attachedServer builds an adapter that has already joined one repository,
// which is the state a tool call leaves behind once it has named one.
func attachedServer(t testing.TB, workspace *app.Workspace, actor, baseURL string, client *http.Client) (*mcpServer, *room) {
	t.Helper()
	server := newServer(actor, workspace.Repo)
	server.session = "mcp:test"
	server.client = client
	attached := &room{workspace: workspace, baseURL: strings.TrimRight(baseURL, "/")}
	server.byPath[server.repo] = attached
	server.byCommonDir[workspace.CommonDir] = attached
	return server, attached
}

func initRepository(t *testing.T, name string) *app.Workspace {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	workspace, _, err := app.Init(context.Background(), repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func serveRepository(t *testing.T, workspace *app.Workspace) {
	t.Helper()
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	t.Cleanup(httpServer.Close)
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
}

func depth(t *testing.T, workspace *app.Workspace) int {
	t.Helper()
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Depth
}

// repo is an adapter selector, not a resident wait field. Cover both forms so
// stripping it cannot make the named call fall back to the default room.
func TestResidentWaitKeepsRepositorySelectionOutOfTheRequestBody(t *testing.T) {
	here := initRepository(t, "here")
	elsewhere := initRepository(t, "elsewhere")
	serveRepository(t, here)
	serveRepository(t, elsewhere)
	server := newServer("human", here.Repo)

	for _, testCase := range []struct {
		name      string
		workspace *app.Workspace
		arguments map[string]any
	}{
		{name: "default repository", workspace: here, arguments: map[string]any{}},
		{name: "named repository", workspace: elsewhere, arguments: map[string]any{"repo": elsewhere.Repo}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := server.call(context.Background(), toolCall{Name: "status", Arguments: testCase.arguments})
			if err != nil {
				t.Fatal(err)
			}
			status := value.(actorStatus)
			arguments := clone(testCase.arguments)
			arguments["cursor"] = status.Cursor
			arguments["timeout_ms"] = 1
			value, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: arguments})
			if err != nil {
				t.Fatal(err)
			}
			delta := value.(waitDelta)
			if len(delta.Cursor.Frontier) != 1 || delta.Cursor.Frontier[0].Genesis != testCase.workspace.Config.Genesis {
				t.Fatalf("wait answered from the wrong repository: %+v", delta.Cursor.Frontier)
			}
			if delta.Cursor.Live.Generation == "degraded" {
				t.Fatalf("resident wait unexpectedly degraded: %+v", delta.Cursor.Live)
			}
		})
	}
}

func TestResidentSayKeepsRepositorySelectionOutOfTheRequestBody(t *testing.T) {
	here := initRepository(t, "here")
	elsewhere := initRepository(t, "elsewhere")
	serveRepository(t, here)
	serveRepository(t, elsewhere)
	server := newServer("human", here.Repo)

	for _, testCase := range []struct {
		name      string
		workspace *app.Workspace
		arguments map[string]any
	}{
		{name: "default repository", workspace: here, arguments: map[string]any{}},
		{name: "named repository", workspace: elsewhere, arguments: map[string]any{"repo": elsewhere.Repo}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			arguments := clone(testCase.arguments)
			arguments["about"] = genesisOf(t, testCase.workspace)
			arguments["text"] = "spoken in " + testCase.name
			if _, err := server.call(context.Background(), toolCall{Name: "say", Arguments: arguments}); err != nil {
				t.Fatal(err)
			}
			value, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: testCase.arguments})
			if err != nil {
				t.Fatal(err)
			}
			conversations, ok := value.(map[string]any)["conversations"].([]any)
			if !ok || len(conversations) != 1 {
				t.Fatalf("say did not reach the selected repository: %#v", value)
			}
		})
	}
}

func TestFallbackWaitPreservesDefaultAndNamedRepositorySelection(t *testing.T) {
	here := initRepository(t, "here")
	elsewhere := initRepository(t, "elsewhere")
	server := newServer("human", here.Repo)

	for _, testCase := range []struct {
		name      string
		workspace *app.Workspace
		arguments map[string]any
	}{
		{name: "default repository", workspace: here, arguments: map[string]any{}},
		{name: "named repository", workspace: elsewhere, arguments: map[string]any{"repo": elsewhere.Repo}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := server.call(context.Background(), toolCall{Name: "status", Arguments: testCase.arguments})
			if err != nil {
				t.Fatal(err)
			}
			status := value.(actorStatus)
			arguments := clone(testCase.arguments)
			arguments["cursor"] = status.Cursor
			arguments["timeout_ms"] = 1
			value, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: arguments})
			if err != nil {
				t.Fatal(err)
			}
			delta := value.(waitDelta)
			if len(delta.Cursor.Frontier) != 1 || delta.Cursor.Frontier[0].Genesis != testCase.workspace.Config.Genesis {
				t.Fatalf("fallback wait answered from the wrong repository: %+v", delta.Cursor.Frontier)
			}
			if delta.Cursor.Live.Generation != "degraded" {
				t.Fatalf("fallback wait invented a resident: %+v", delta.Cursor.Live)
			}
		})
	}
}

// One installation serves whatever repository a call names. The default is the
// working directory the adapter was started in, so nothing has to be
// configured for the common case, and an act must land in the workroom the
// call named rather than in whichever one the adapter happened to open first.
func TestCallsActInTheRepositoryTheyName(t *testing.T) {
	here := initRepository(t, "here")
	elsewhere := initRepository(t, "elsewhere")
	server := newServer("human", here.Repo)

	if _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "spoken in the default repository", "rests_on": []any{genesisOf(t, here)}, "idempotency_key": "default-repo",
	}}); err != nil {
		t.Fatal(err)
	}
	if got, other := depth(t, here), depth(t, elsewhere); got != 2 || other != 1 {
		t.Fatalf("default call landed wrong: here=%d elsewhere=%d", got, other)
	}

	if _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"repo": elsewhere.Repo, "kind": "assert", "text": "spoken in the named repository", "rests_on": []any{genesisOf(t, elsewhere)}, "idempotency_key": "named-repo",
	}}); err != nil {
		t.Fatal(err)
	}
	if got, other := depth(t, elsewhere), depth(t, here); got != 2 || other != 2 {
		t.Fatalf("named call landed wrong: elsewhere=%d here=%d", got, other)
	}

	value, err := server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": elsewhere.Repo}})
	if err != nil {
		t.Fatal(err)
	}
	if reported := value.(map[string]any)["repo"]; reported != elsewhere.CommonDir {
		t.Fatalf("whoami named the wrong workroom: %v", reported)
	}
}

// A directory with no workroom is an ordinary answer to one call, not a reason
// to refuse the connection: the adapter is installed once and pointed at many
// repositories, most of which are not workrooms.
func TestCallOutsideAWorkroomIsReportedWithoutFailingTheConnection(t *testing.T) {
	server := newServer("human", t.TempDir())
	_, err := server.call(context.Background(), toolCall{Name: "status"})
	if err == nil || !strings.Contains(err.Error(), "no gitseq workroom for") {
		t.Fatalf("unexpected error outside a workroom: %v", err)
	}
	elsewhere := initRepository(t, "elsewhere")
	if _, err := server.call(context.Background(), toolCall{Name: "status", Arguments: map[string]any{"repo": elsewhere.Repo}}); err != nil {
		t.Fatalf("a named workroom was unreachable after an unattachable default: %v", err)
	}
}

// The service is found in the repository rather than configured, so a client
// that was told nothing still joins the live workroom, and one that was told
// about a service holding another workroom does not act through it.
func TestResidentServiceIsFoundInTheRepository(t *testing.T) {
	workspace := initRepository(t, "repo")
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()

	server := newServer("human", workspace.Repo)
	value, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if status := value.(actorStatus); !status.Live.Degraded {
		t.Fatalf("status was not degraded before any service published itself: %+v", status.Live)
	}

	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
	value, err = server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	live, ok := value.(actorStatus)
	if !ok {
		t.Fatalf("the published service was not used: %#v", value)
	}
	if live.Live.Degraded {
		t.Fatalf("the published service answered as degraded: %+v", live.Live)
	}
	presence, err := server.call(context.Background(), toolCall{Name: "presence"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := presence.(map[string]any)
	if !ok {
		t.Fatalf("presence returned the wrong shape: %#v", presence)
	}
	livePresence, ok := snapshot["presence"].(map[string]any)
	if !ok || len(livePresence) == 0 {
		t.Fatalf("the session announced no presence through the published service: %#v", presence)
	}
}

// Whoami establishes bounded durable orientation before the session joins a
// live room. Ordinary tools still attend the selected repository normally.
func TestWhoamiDoesNotAttendBeforeOrdinaryTool(t *testing.T) {
	workspace := initRepository(t, "repo")
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}

	server := newServer("human", workspace.Repo)
	if _, err := server.call(context.Background(), toolCall{Name: "whoami"}); err != nil {
		t.Fatal(err)
	}
	if got := len(server.attended()); got != 0 {
		t.Fatalf("whoami attended %d rooms before establishing orientation", got)
	}
	if _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
		t.Fatal(err)
	}
	if got := len(server.attended()); got != 1 {
		t.Fatalf("ordinary tool attended %d rooms, want 1", got)
	}
}

func TestPresenceToolUpdatesOnlyItsOwnBoundedLease(t *testing.T) {
	workspace := initRepository(t, "repo")
	if _, _, err := workspace.AddActor(context.Background(), "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	event := snapshot.Projection.Decisions[0].Event
	value, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{
		"status": "blocked", "focus": []any{event}, "note": "waiting on review",
		// These are not tool fields and must not override adapter custody.
		"actor": "other", "session": "forged",
	}})
	if err != nil {
		t.Fatal(err)
	}
	returned := value.(map[string]any)
	var own nexus.Change
	if err := remarshal(returned["own"], &own); err != nil {
		t.Fatal(err)
	}
	if own.Activity == nil || own.Activity.Status != nexus.ActivityBlocked {
		t.Fatalf("presence did not identify its own updated lease: %+v", own)
	}
	var live nexus.Snapshot
	if err := remarshal(value, &live); err != nil {
		t.Fatal(err)
	}
	if len(live.Presence) != 1 || len(live.Activity) != 1 {
		t.Fatalf("presence update announced another identity: %+v", live)
	}
	for handle, label := range live.Presence {
		activity := live.Activity[handle]
		if !strings.HasPrefix(label, "human (") || activity.Status != nexus.ActivityBlocked || len(activity.Focus) != 1 || activity.Focus[0] != event {
			t.Fatalf("presence tool did not update its own lease: label=%q activity=%+v", label, activity)
		}
	}
	inspected, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var renewed nexus.Change
	if err := remarshal(inspected.(map[string]any)["own"], &renewed); err != nil {
		t.Fatal(err)
	}
	if renewed.Kind != "renewal" || renewed.ID != own.ID || renewed.Cursor != own.Cursor || renewed.Activity == nil || renewed.Activity.Status != nexus.ActivityBlocked {
		t.Fatalf("presence could not inspect its own preserved lease: %+v", renewed)
	}

	tooMany := make([]any, nexus.MaxFocusEvents+1)
	for index := range tooMany {
		tooMany[index] = event
	}
	if _, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{"focus": tooMany}}); err == nil {
		t.Fatal("presence tool accepted unbounded focus")
	}
}

// A published address that stops answering is forgotten rather than retried
// forever, so a service restarted on another port is picked up without
// reconnecting the client.
func TestAServiceThatStopsAnsweringIsLookedUpAgain(t *testing.T) {
	workspace := initRepository(t, "repo")
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	if _, err := workspace.PublishResident(deadURL); err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)
	if _, err := server.call(context.Background(), toolCall{Name: "status"}); err != nil {
		t.Fatal(err)
	}

	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	moved := httptest.NewServer(workroomServer.Handler())
	defer moved.Close()
	if _, err := workspace.PublishResident(moved.URL); err != nil {
		t.Fatal(err)
	}
	value, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if status, ok := value.(actorStatus); !ok || status.Live.Degraded {
		t.Fatalf("the moved service was not found again: %#v", value)
	}
}

// The author who wrote a promise under a kind this room does not define was
// writing over MCP, so the warning belongs in the tool result: in the
// structured payload and in the one text block every client reads. It has to
// arrive whether the act went through the resident service or straight to the
// log because none was answering. The act itself still lands and still
// projects as undefined-kind.
func TestStateToolCarriesTheUndefinedKindWarningInItsResult(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		resident bool
	}{
		{"through the resident service", true},
		{"degraded, with no resident service", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			workspace := initRepository(t, "repo")
			var baseURL string
			var client *http.Client
			if testCase.resident {
				workroomServer, err := service.New(workspace)
				if err != nil {
					t.Fatal(err)
				}
				httpServer := httptest.NewServer(workroomServer.Handler())
				defer httpServer.Close()
				baseURL, client = httpServer.URL, httpServer.Client()
			} else {
				dead := httptest.NewServer(nil)
				baseURL, client = dead.URL, dead.Client()
				dead.Close()
			}
			server, _ := attachedServer(t, workspace, "human", baseURL, client)
			genesis := workspace.EventID(workspace.Config.Genesis)

			value, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "commit", "text": "I will re-review task/x at exact head y",
				"rests_on": []any{genesis}, "idempotency_key": "undefined-kind",
			}})
			if err != nil {
				t.Fatalf("state with an undefined kind failed: %v", err)
			}
			result, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("state result has the wrong shape: %#v", value)
			}
			warning, _ := result["warning"].(string)
			for _, want := range []string{`"commit"`, "no rule reads it", "undefined-kind", "does not form", "kinds defined here:"} {
				if !strings.Contains(warning, want) {
					t.Fatalf("state result warning %q does not say %q", warning, want)
				}
			}
			if summary := summarize("state", value); !strings.Contains(summary, warning) {
				t.Fatalf("the text block %q does not carry the warning", summary)
			}

			snapshot, err := workspace.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Depth != 2 {
				t.Fatalf("depth = %d, want the act to have landed at 2", snapshot.Depth)
			}
			landed := false
			for _, decision := range snapshot.Projection.Decisions {
				if decision.Event != genesis && decision.Verdict == workroom.UndefinedKind {
					landed = true
				}
			}
			if !landed {
				t.Fatalf("the act did not project as undefined-kind: %+v", snapshot.Projection.Decisions)
			}

			// A defined kind is ordinary work and carries no warning, so the
			// warning cannot pass by being attached to every result.
			value, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "assert", "text": "an ordinary claim",
				"rests_on": []any{genesis}, "idempotency_key": "defined-kind",
			}})
			if err != nil {
				t.Fatal(err)
			}
			if warned, held := value.(map[string]any)["warning"]; held {
				t.Fatalf("a defined kind warned anyway: %v", warned)
			}
		})
	}
}

func genesisOf(t *testing.T, workspace *app.Workspace) string {
	t.Helper()
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Head
}

// Presence renewal runs beside whatever call is in flight and reads the same
// attachment, so the two must not race over where the service is.
func TestPresenceRenewalRunsBesideCalls(t *testing.T) {
	workspace := initRepository(t, "repo")
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)
	if _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
		t.Fatal(err)
	}

	var working sync.WaitGroup
	for caller := 0; caller < 4; caller++ {
		working.Add(1)
		go func() {
			defer working.Done()
			for turn := 0; turn < 10; turn++ {
				if _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	working.Add(1)
	go func() {
		defer working.Done()
		for turn := 0; turn < 10; turn++ {
			for _, current := range server.attended() {
				if err := server.announce(context.Background(), current); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	working.Wait()
	server.depart(context.Background())
}

// One identity, one live session. A second instance under the same name would
// make the log say a name did work that one of several instances did, and
// would satisfy the different-agent review rule by spelling alone.
func TestSecondInstanceRefusesToShareALiveIdentity(t *testing.T) {
	workspace := initRepository(t, "repo")
	if _, _, err := workspace.AddActor(context.Background(), "human", "claude.2", "agent"); err != nil {
		t.Fatal(err)
	}
	workroomServer, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workroomServer.Handler())
	defer httpServer.Close()
	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}

	first := newServer("human", workspace.Repo)
	if _, err := first.attend(context.Background(), ""); err != nil {
		t.Fatalf("first instance refused a free identity: %v", err)
	}
	second := newServer("human", workspace.Repo)
	_, err = second.attend(context.Background(), "")
	var shared *sharedIdentityError
	if !errors.As(err, &shared) || !strings.Contains(err.Error(), "already live") {
		t.Fatalf("second instance under one identity = %v", err)
	}
	if !strings.Contains(err.Error(), actorEnvironment) {
		t.Fatalf("refusal does not say how to fix it: %v", err)
	}
	// The refusal has to hold on every call, not only the first: a session
	// that was turned away at the door must not act through a cached
	// attachment.
	if _, err := second.call(context.Background(), toolCall{Name: "presence"}); !errors.As(err, &shared) {
		t.Fatalf("a refused instance still acted: %v", err)
	}
	distinct := newServer("claude.2", workspace.Repo)
	if _, err := distinct.attend(context.Background(), ""); err != nil {
		t.Fatalf("a distinct instance identity was refused: %v", err)
	}

	first.depart(context.Background())
	if _, err := second.attend(context.Background(), ""); err != nil {
		t.Fatalf("identity stayed held after its session departed: %v", err)
	}
}

// The check is only as good as the resident service, and a stopped service
// must not stop the work. Starting anyway is the stated limit, not an oversight.
func TestIdentityCheckIsSkippedWhenPresenceCannotBeRead(t *testing.T) {
	workspace := initRepository(t, "repo")
	dead := httptest.NewServer(nil)
	baseURL := dead.URL
	client := dead.Client()
	dead.Close()
	server, attached := attachedServer(t, workspace, "human", baseURL, client)
	server.session = "mcp:offline"
	if err := server.requireSoleIdentity(context.Background(), attached); err != nil {
		t.Fatalf("unreadable presence blocked startup: %v", err)
	}
}

func signedWorkspace(tb testing.TB, depth int) (*app.Workspace, workroom.Record) {
	tb.Helper()
	ctx := context.Background()
	repo := filepath.Join(tb.TempDir(), "repo")
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		tb.Fatalf("git init: %v: %s", err, output)
	}
	workspace, genesis, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		tb.Fatal(err)
	}
	for index := 1; index < depth; index++ {
		if _, err := workspace.Act(ctx, "human", app.Act{
			Verb: app.VerbState, Kind: workroom.KindAssert, Text: "signed orientation history",
			RestsOn: []string{genesis.ID}, IdempotencyKey: fmt.Sprintf("orientation-history-%d", index),
		}); err != nil {
			tb.Fatal(err)
		}
	}
	return workspace, genesis
}

func callWhoami(t testing.TB, workspace *app.Workspace, baseURL string, client *http.Client) map[string]any {
	t.Helper()
	server, _ := attachedServer(t, workspace, "human", baseURL, client)
	server.session = "mcp:test-whoami"
	value, err := server.call(context.Background(), toolCall{Name: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func TestWhoamiUsesBoundedEffectiveResidentOrientationWithoutLocalReplay(t *testing.T) {
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 2)
	warm, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := warm.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	residentService, err := service.New(warm)
	if err != nil {
		t.Fatal(err)
	}
	resident := httptest.NewServer(residentService.Handler())
	defer resident.Close()

	fresh, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	checkpointRef := kernel.CheckpointRef(workspace.Config.Genesis)
	checkpointHead, err := workspace.Store.Head(ctx, checkpointRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.Store.UpdateRef(ctx, checkpointRef, workspace.Config.Genesis, checkpointHead); err != nil {
		t.Fatal(err)
	}
	eventCommit := strings.TrimPrefix(snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1].Event, workspace.EventID(""))
	blobOutput, err := exec.Command("git", "--git-dir", workspace.Store.Repo, "rev-parse", eventCommit+":event").CombinedOutput()
	if err != nil {
		t.Fatalf("resolve event blob: %v: %s", err, blobOutput)
	}
	blob := strings.TrimSpace(string(blobOutput))
	if err := os.Remove(filepath.Join(workspace.Store.Repo, "objects", blob[:2], blob[2:])); err != nil {
		t.Fatal(err)
	}

	result := callWhoami(t, fresh, resident.URL, resident.Client())
	if result["source"] != residentOrientationSource || result["degraded"] != false {
		t.Fatalf("resident fast path not used: %#v", result)
	}
	frontier := result["frontier"].(statusview.Frontier)
	if frontier.Head != snapshot.Head || frontier.Depth != snapshot.Depth {
		t.Fatalf("resident frontier differs: %+v", frontier)
	}
	durable := result["durable"].(statusview.ActorView)
	if durable.Fingerprint != workspace.Config.Actors["human"].Fingerprint || durable.Kind != "human" || durable.MembershipEvent == "" || !containsString(durable.Roles, "participant") {
		t.Fatalf("resident lost effective identity: %+v", durable)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("key_file")) || bytes.Contains(encoded, []byte(workspace.Config.Actors["human"].KeyFile)) || genesis.ID == "" {
		t.Fatalf("whoami leaked local custody or lost signed basis: %s", encoded)
	}
}

func TestWhoamiDisclosesCheckpointAndFullAuditFallbacks(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 3)
	checkpointWriter, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpointWriter.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	checkpointRef := kernel.CheckpointRef(workspace.Config.Genesis)
	checkpointHead, err := workspace.Store.Head(ctx, checkpointRef)
	if err != nil {
		t.Fatal(err)
	}
	dead := httptest.NewServer(nil)
	baseURL, client := dead.URL, dead.Client()
	dead.Close()

	fresh, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := callWhoami(t, fresh, baseURL, client)
	if checkpoint["source"] != string(app.SnapshotSourceSignedCheckpointTail) || checkpoint["degraded"] != true {
		t.Fatalf("checkpoint fallback was not disclosed: %#v", checkpoint)
	}
	if err := workspace.Store.UpdateRef(ctx, checkpointRef, workspace.Config.Genesis, checkpointHead); err != nil {
		t.Fatal(err)
	}
	cold, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	full := callWhoami(t, cold, baseURL, client)
	if full["source"] != string(app.SnapshotSourceColdFullAudit) || full["degraded"] != true {
		t.Fatalf("cold full audit was not disclosed: %#v", full)
	}
	for _, result := range []map[string]any{checkpoint, full} {
		encoded, _ := json.Marshal(result)
		if bytes.Contains(encoded, []byte("key_file")) {
			t.Fatalf("fallback leaked custody: %s", encoded)
		}
	}
}

func TestWhoamiRejectsUntrustedOrUnboundedResidentAnswers(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orientation, ok := statusview.BuildOrientation(snapshot, workspace.Config.Actors["human"].Fingerprint, "human")
	if !ok {
		t.Fatal("missing effective actor")
	}
	base, _ := json.Marshal(orientation)
	var oversizedValue map[string]any
	if err := json.Unmarshal(base, &oversizedValue); err != nil {
		t.Fatal(err)
	}
	if orientationResponseLimit != 64<<10 {
		t.Fatalf("orientation response limit = %d, want 64 KiB", orientationResponseLimit)
	}
	oversizedValue["you"].(map[string]any)["roles"] = []any{"participant", strings.Repeat("x", 64<<10)}
	oversized, _ := json.Marshal(oversizedValue)
	for name, response := range map[string][]byte{
		"malformed":     []byte("{"),
		"trailing json": append(append([]byte(nil), base...), []byte(" {}")...),
		"oversized":     oversized,
	} {
		t.Run(name, func(t *testing.T) {
			resident := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(response) }))
			defer resident.Close()
			result := callWhoami(t, workspace, resident.URL, resident.Client())
			if result["source"] == residentOrientationSource || result["degraded"] != true {
				t.Fatalf("invalid resident answer was accepted: %#v", result)
			}
		})
	}
	for name, mutate := range map[string]func(map[string]any){
		"projection version mismatch": func(value map[string]any) {
			value["projection_version"] = "statusview-orientation@0"
		},
		"foreign genesis": func(value map[string]any) {
			value["frontier"].(map[string]any)["genesis"] = strings.Repeat("0", len(snapshot.Genesis))
		},
		"stale head":     func(value map[string]any) { value["frontier"].(map[string]any)["head"] = snapshot.Genesis },
		"negative depth": func(value map[string]any) { value["frontier"].(map[string]any)["depth"] = -1 },
		"actor mismatch": func(value map[string]any) { value["you"].(map[string]any)["fingerprint"] = "foreign" },
		"missing name":   func(value map[string]any) { value["you"].(map[string]any)["name"] = "" },
		"missing kind":   func(value map[string]any) { value["you"].(map[string]any)["kind"] = "" },
		"missing member": func(value map[string]any) { value["you"].(map[string]any)["membership_event"] = "" },
		"role mismatch":  func(value map[string]any) { value["you"].(map[string]any)["roles"] = []any{"ratifier"} },
		"role overflow": func(value map[string]any) {
			roles := []any{"participant"}
			for index := 0; index < statusview.ListCap; index++ {
				roles = append(roles, fmt.Sprintf("extra-%d", index))
			}
			value["you"].(map[string]any)["roles"] = roles
		},
		"negative omission": func(value map[string]any) { value["you"].(map[string]any)["roles_skipped"] = -1 },
		"unknown field":     func(value map[string]any) { value["invented"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			resident := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(writer).Encode(value) }))
			defer resident.Close()
			result := callWhoami(t, workspace, resident.URL, resident.Client())
			if result["source"] == residentOrientationSource || result["degraded"] != true {
				t.Fatalf("untrusted resident answer was accepted: %#v", result)
			}
		})
	}
}

func TestWhoamiRetriesOneConcurrentFrontierMove(t *testing.T) {
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	first, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := workspace.Config.Actors["human"].Fingerprint
	var calls atomic.Int32
	resident := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		number := calls.Add(1)
		current := first
		if number == 1 {
			if _, err := workspace.Act(ctx, "human", app.Act{Verb: app.VerbState, Kind: workroom.KindAssert, Text: "concurrent", RestsOn: []string{genesis.ID}, IdempotencyKey: "concurrent"}); err != nil {
				t.Error(err)
			}
		} else {
			current, err = workspace.Snapshot(ctx)
			if err != nil {
				t.Error(err)
			}
		}
		orientation, _ := statusview.BuildOrientation(current, fingerprint, "human")
		_ = json.NewEncoder(writer).Encode(orientation)
	}))
	defer resident.Close()
	result := callWhoami(t, workspace, resident.URL, resident.Client())
	if calls.Load() != 2 || result["source"] != residentOrientationSource || result["degraded"] != false {
		t.Fatalf("concurrent frontier was not retried coherently: calls=%d result=%#v", calls.Load(), result)
	}
}

func TestWhoamiBoundsStallsAndRejectsRedirects(t *testing.T) {
	if orientationTimeout != 2*time.Second {
		t.Fatalf("orientation timeout = %s, want 2s", orientationTimeout)
	}
	workspace, _ := signedWorkspace(t, 1)
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	started := time.Now()
	result := callWhoami(t, workspace, stalled.URL, stalled.Client())
	stalled.Close()
	// The resident attempt owns the two-second production timeout. Leave a
	// separate bounded allowance for the signed local fallback and scheduler
	// overhead, which are deliberately included in this end-to-end clock.
	if elapsed := time.Since(started); elapsed > orientationTimeout+2*time.Second || result["degraded"] != true {
		t.Fatalf("stalled resident was not bounded: elapsed=%s result=%#v", elapsed, result)
	}
	var followed atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL+request.URL.Path, http.StatusFound)
	}))
	defer source.Close()
	result = callWhoami(t, workspace, source.URL, newResidentClient())
	if followed.Load() != 0 || result["source"] == residentOrientationSource || result["degraded"] != true {
		t.Fatalf("resident redirect was followed: followed=%d result=%#v", followed.Load(), result)
	}
	for _, raw := range []string{"https://127.0.0.1:7777", "http://example.com", "http://user@127.0.0.1:7777", "http://127.0.0.1:7777/path"} {
		if _, err := validateResidentURL(raw); err == nil {
			t.Fatalf("accepted unsafe resident URL %q", raw)
		}
	}
}

func TestWhoamiWarmResidentAtSignedDepthIsSubsecond(t *testing.T) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 64)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil || snapshot.Depth != 64 {
		t.Fatalf("signed depth = %d, want 64 (err=%v)", snapshot.Depth, err)
	}
	residentService, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	resident := httptest.NewServer(residentService.Handler())
	defer resident.Close()
	fresh, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result := callWhoami(t, fresh, resident.URL, resident.Client())
	if elapsed := time.Since(started); elapsed >= time.Second || result["source"] != residentOrientationSource {
		t.Fatalf("warm signed-depth orientation: elapsed=%s result=%#v", elapsed, result)
	}
}

func BenchmarkWhoamiAtActualSignedDepth(b *testing.B) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(b, 128)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil || snapshot.Depth != 128 {
		b.Fatalf("signed benchmark depth = %d, want 128 (err=%v)", snapshot.Depth, err)
	}
	residentService, err := service.New(workspace)
	if err != nil {
		b.Fatal(err)
	}
	resident := httptest.NewServer(residentService.Handler())
	defer resident.Close()
	b.Run("warm_resident", func(b *testing.B) {
		for range b.N {
			fresh, _ := app.Open(ctx, workspace.Repo)
			if result := callWhoami(b, fresh, resident.URL, resident.Client()); result["source"] != residentOrientationSource {
				b.Fatalf("warm source = %#v", result)
			}
		}
	})
	b.Run("new_resident", func(b *testing.B) {
		for range b.N {
			residentWorkspace, _ := app.Open(ctx, workspace.Repo)
			server, err := service.New(residentWorkspace)
			if err != nil {
				b.Fatal(err)
			}
			httpServer := httptest.NewServer(server.Handler())
			fresh, _ := app.Open(ctx, workspace.Repo)
			result := callWhoami(b, fresh, httpServer.URL, httpServer.Client())
			httpServer.Close()
			if result["source"] != residentOrientationSource {
				b.Fatalf("new resident source = %#v", result)
			}
		}
	})
	dead := httptest.NewServer(nil)
	deadURL, deadClient := dead.URL, dead.Client()
	dead.Close()
	b.Run("unavailable_signed_checkpoint", func(b *testing.B) {
		for range b.N {
			fresh, _ := app.Open(ctx, workspace.Repo)
			if result := callWhoami(b, fresh, deadURL, deadClient); result["source"] != string(app.SnapshotSourceSignedCheckpointTail) {
				b.Fatalf("checkpoint source = %#v", result)
			}
		}
	})
}

func BenchmarkWhoamiColdFullAuditAtActualSignedDepth(b *testing.B) {
	ctx := context.Background()
	workspace, _ := signedWorkspace(b, 128)
	writer, err := app.Open(ctx, workspace.Repo)
	if err != nil {
		b.Fatal(err)
	}
	snapshot, err := writer.Snapshot(ctx)
	if err != nil || snapshot.Depth != 128 {
		b.Fatalf("signed benchmark depth = %d, want 128 (err=%v)", snapshot.Depth, err)
	}
	dead := httptest.NewServer(nil)
	deadURL, deadClient := dead.URL, dead.Client()
	dead.Close()
	checkpointRef := kernel.CheckpointRef(workspace.Config.Genesis)
	for b.Loop() {
		checkpointHead, err := workspace.Store.Head(ctx, checkpointRef)
		if err != nil {
			b.Fatal(err)
		}
		if err := workspace.Store.UpdateRef(ctx, checkpointRef, workspace.Config.Genesis, checkpointHead); err != nil {
			b.Fatal(err)
		}
		fresh, _ := app.Open(ctx, workspace.Repo)
		if result := callWhoami(b, fresh, deadURL, deadClient); result["source"] != string(app.SnapshotSourceColdFullAudit) {
			b.Fatalf("full audit source = %#v", result)
		}
	}
}
