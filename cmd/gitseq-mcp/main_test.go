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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
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
	if got := len(result["tools"].([]any)); got != 11 {
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

	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
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

	value, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
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
	waited, _, err := server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{"cursor": status.Cursor, "timeout_ms": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if delta := waited.(waitDelta); delta.Totals.Depth != 2 || delta.Reset {
		t.Fatalf("unexpected degraded wait response: %+v", delta)
	}

	worked, _, err := server.call(context.Background(), toolCall{Name: "work", Arguments: map[string]any{"limit": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if page := worked.(statusview.WorkPage); !page.Degraded || page.Frontier.Depth != 2 || page.Actor.Fingerprint != workspace.Config.Actors["human"].Fingerprint {
		t.Fatalf("unexpected degraded work page: %+v", page)
	}
	inspected, _, err := server.call(context.Background(), toolCall{Name: "inspect", Arguments: map[string]any{"event": genesis.ID}})
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
	// This test concerns query routing, not presence or the shared-identity notice.
	// Mark the already-attached test session as joined so those independent
	// calls cannot obscure which endpoints work and inspect choose.
	attached.identityNoticeChecked = true
	attached.announced = true

	value, _, err := server.call(context.Background(), toolCall{Name: "work", Arguments: map[string]any{"limit": 1}})
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
	value, _, err = server.call(context.Background(), toolCall{Name: "inspect", Arguments: map[string]any{"event": event}})
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

// Status and wait are bounded at the resident boundary, not after the adapter
// has already transferred and decoded the complete projection. Returning the
// right small MCP value is insufficient evidence: the old implementation did
// that too, after paying for /v0/status in full. Blocking both full routes
// makes this test fail if either transport regresses.
func TestStatusAndWaitUseBoundedResidentViews(t *testing.T) {
	if actorStatusResponseLimit != 1<<20 {
		t.Fatalf("bounded actor response limit = %d, want 1 MiB", actorStatusResponseLimit)
	}
	workspace := initRepository(t, "bounded-status")
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
		if request.URL.Path == "/v0/status" || request.URL.Path == "/v0/wait" {
			http.Error(response, "MCP must not fetch the complete projection", http.StatusInternalServerError)
			return
		}
		resident.Handler().ServeHTTP(response, request)
	}))
	defer httpServer.Close()

	server, attached := attachedServer(t, workspace, "human", httpServer.URL, httpServer.Client())
	attached.identityNoticeChecked = true
	attached.announced = true
	announcement, _ := json.Marshal(map[string]any{"actor": "human", "session": server.session, "ttl_ms": 60_000})
	response, err := http.Post(httpServer.URL+"/v0/presence", "application/json", bytes.NewReader(announcement))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	mu.Lock()
	paths = nil
	mu.Unlock()

	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	status := value.(actorStatus)
	if status.Frontier[0].Depth != 1 || status.You.Fingerprint != workspace.Config.Actors["human"].Fingerprint {
		t.Fatalf("unexpected bounded status: %+v", status)
	}
	value, _, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{
		"cursor": status.Cursor, "timeout_ms": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if delta := value.(waitDelta); delta.Totals.Depth != 1 {
		t.Fatalf("unexpected bounded wait: %+v", delta)
	}

	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	if strings.Join(gotPaths, ",") != "/v0/actor-status,/v0/actor-wait" {
		t.Fatalf("status or wait used an unbounded resident route: %v", gotPaths)
	}
}

// attachedServer builds an adapter that has already joined one repository,
// which is the state a tool call leaves behind once it has named one.
func attachedServer(t testing.TB, workspace *app.Workspace, actor, baseURL string, client *http.Client) (*mcpServer, *room) {
	t.Helper()
	server := newServer(actor, workspace.Repo)
	server.session = "mcp:test"
	server.client = residentclient.NewWithHTTP(client, residentHTTPTimeout)
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
			value, _, err := server.call(context.Background(), toolCall{Name: "status", Arguments: testCase.arguments})
			if err != nil {
				t.Fatal(err)
			}
			status := value.(actorStatus)
			arguments := clone(testCase.arguments)
			arguments["cursor"] = status.Cursor
			arguments["timeout_ms"] = 1
			value, _, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: arguments})
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
			if _, _, err := server.call(context.Background(), toolCall{Name: "say", Arguments: arguments}); err != nil {
				t.Fatal(err)
			}
			value, _, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: testCase.arguments})
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

func TestAdapterInjectsItsPrivateSessionForPriorityChatAndAck(t *testing.T) {
	workspace := initRepository(t, "priority-chat")
	if _, _, err := workspace.AddActor(context.Background(), "human", "other", "agent"); err != nil {
		t.Fatal(err)
	}
	resident, err := service.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(resident.Handler())
	defer httpServer.Close()
	server, attached := attachedServer(t, workspace, "human", httpServer.URL, httpServer.Client())
	if err := server.announce(context.Background(), attached); err != nil {
		t.Fatal(err)
	}
	beforeValue, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	before := beforeValue.(actorStatus)
	post := func(path string, input any, output any) {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(httpServer.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("POST %s returned %s", path, response.Status)
		}
		if output != nil {
			if err := json.NewDecoder(response.Body).Decode(output); err != nil {
				t.Fatal(err)
			}
		}
	}
	post("/v0/presence", map[string]any{"actor": "other", "session": "speaker"}, nil)
	var frame nexus.Frame
	post("/v0/say", map[string]any{"session": "speaker", "about": genesisOf(t, workspace), "text": "@human please review"}, &frame)
	waitValue, _, err := server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{"cursor": before.Cursor, "timeout_ms": 50}})
	if err != nil {
		t.Fatal(err)
	}
	delta := waitValue.(waitDelta)
	if !delta.PriorityChat.Available || len(delta.PriorityChat.Frames) != 1 || delta.PriorityChat.Frames[0].Text != "@human please review" {
		t.Fatalf("adapter wait priority chat = %+v", delta.PriorityChat)
	}
	var inline *nexus.InboxFrame
	for _, change := range delta.Live {
		if change.Frame != nil {
			inline = change.Frame
		}
	}
	if inline == nil || inline.Text != "@human please review" {
		t.Fatalf("adapter wait live delta omitted the addressed frame: %+v", delta.Live)
	}

	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	status := value.(actorStatus)
	if !status.PriorityChat.Available || len(status.PriorityChat.Frames) != 1 || status.PriorityChat.Frames[0].ActorName != "other" {
		t.Fatalf("adapter status priority chat = %+v", status.PriorityChat)
	}
	thread := frame.Conversation + ":" + strconv.FormatUint(frame.Sequence, 10)
	if _, _, err := server.call(context.Background(), toolCall{Name: "ack", Arguments: map[string]any{"threads": []any{thread}}}); err != nil {
		t.Fatal(err)
	}
	value, _, err = server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if inbox := value.(actorStatus).PriorityChat; !inbox.Available || len(inbox.Frames) != 0 {
		t.Fatalf("adapter ack left priority chat pending: %+v", inbox)
	}
}

func TestNewAdapterDegradesHonestlyAgainstLegacyResidentInboxProtocol(t *testing.T) {
	workspace := initRepository(t, "legacy-resident")
	var sayCalls atomic.Int64
	legacy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v0/status":
			http.Error(writer, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		case request.Method == http.MethodPost && request.URL.Path == "/v0/wait":
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":"json: unknown field \"session\""}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v0/say":
			sayCalls.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]any{"opaque": true})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer legacy.Close()
	server, attached := attachedServer(t, workspace, "human", legacy.URL, legacy.Client())
	var notices bytes.Buffer
	server.notices = &notices
	attached.announced = true

	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	status := value.(actorStatus)
	if !status.Live.Degraded || status.PriorityChat.Available {
		t.Fatalf("legacy resident status invented inbox support: %+v", status)
	}
	if !strings.Contains(notices.String(), "resident does not support live identity counts") {
		t.Fatalf("legacy resident did not receive a visible skipped-check diagnostic: %q", notices.String())
	}
	if strings.Contains(notices.String(), "resident service is unavailable") {
		t.Fatalf("reachable legacy resident was reported unavailable: %q", notices.String())
	}
	value, _, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{"cursor": status.Cursor, "timeout_ms": 1}})
	if err != nil {
		t.Fatal(err)
	}
	delta := value.(waitDelta)
	if delta.Cursor.Live.Generation != "degraded" || delta.PriorityChat.Available {
		t.Fatalf("legacy resident wait invented inbox support: %+v", delta)
	}
	if _, _, err := server.call(context.Background(), toolCall{Name: "say", Arguments: map[string]any{"about": genesisOf(t, workspace), "text": "@human addressed"}}); err == nil {
		t.Fatal("addressed say was sent to a resident without inbox support")
	}
	if sayCalls.Load() != 0 {
		t.Fatalf("legacy resident received %d addressed say calls", sayCalls.Load())
	}
	if _, _, err := server.call(context.Background(), toolCall{Name: "say", Arguments: map[string]any{
		"about": genesisOf(t, workspace), "text": "email human@example.test and see docs/@human/file",
	}}); err != nil {
		t.Fatalf("ordinary text containing @ did not remain legacy-compatible: %v", err)
	}
	if sayCalls.Load() != 1 {
		t.Fatalf("legacy resident received %d say calls, want one unaddressed call", sayCalls.Load())
	}
}

func TestAddressedSayFailsClosedWhenResidentDowngradesDuringSessionRepair(t *testing.T) {
	workspace := initRepository(t, "downgraded-resident")
	var sayCalls atomic.Int64
	var opaquePublishes atomic.Int64
	legacy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v0/say":
			call := sayCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, `{"error":"malformed request"}`, http.StatusBadRequest)
				return
			}
			if call == 1 {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(`{"error":"session is not present"}`))
				return
			}
			if _, versioned := body["inbox_version"]; versioned {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(`{"error":"json: unknown field \\"inbox_version\\""}`))
				return
			}
			opaquePublishes.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]any{"opaque": true})
		case request.Method == http.MethodPost && request.URL.Path == "/v0/presence":
			_ = json.NewEncoder(writer).Encode(map[string]any{})
		case request.Method == http.MethodPost && request.URL.Path == "/v0/inbox/register":
			http.NotFound(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer legacy.Close()

	server, attached := attachedServer(t, workspace, "human", legacy.URL, legacy.Client())
	attached.identityNoticeChecked = true
	attached.announced = true
	attached.setInboxAvailable(true)

	_, _, err := server.call(context.Background(), toolCall{Name: "say", Arguments: map[string]any{
		"about": genesisOf(t, workspace),
		"text":  "@human addressed",
	}})
	if err == nil {
		t.Fatal("addressed say survived a downgrade to a resident without inbox support")
	}
	if sayCalls.Load() != 2 {
		t.Fatalf("addressed say made %d calls, want initial refusal and one safe retry", sayCalls.Load())
	}
	if opaquePublishes.Load() != 0 {
		t.Fatalf("legacy resident accepted %d addressed says as opaque chat", opaquePublishes.Load())
	}
	if attached.inboxAvailable() {
		t.Fatal("adapter retained inbox capability after the downgraded resident refused registration")
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
			value, _, err := server.call(context.Background(), toolCall{Name: "status", Arguments: testCase.arguments})
			if err != nil {
				t.Fatal(err)
			}
			status := value.(actorStatus)
			arguments := clone(testCase.arguments)
			arguments["cursor"] = status.Cursor
			arguments["timeout_ms"] = 1
			value, _, err = server.call(context.Background(), toolCall{Name: "wait", Arguments: arguments})
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

	if _, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "spoken in the default repository", "rests_on": []any{genesisOf(t, here)}, "idempotency_key": "default-repo",
	}}); err != nil {
		t.Fatal(err)
	}
	if got, other := depth(t, here), depth(t, elsewhere); got != 2 || other != 1 {
		t.Fatalf("default call landed wrong: here=%d elsewhere=%d", got, other)
	}

	if _, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
		"repo": elsewhere.Repo, "kind": "assert", "text": "spoken in the named repository", "rests_on": []any{genesisOf(t, elsewhere)}, "idempotency_key": "named-repo",
	}}); err != nil {
		t.Fatal(err)
	}
	if got, other := depth(t, elsewhere), depth(t, here); got != 2 || other != 2 {
		t.Fatalf("named call landed wrong: elsewhere=%d here=%d", got, other)
	}

	value, _, err := server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": elsewhere.Repo}})
	if err != nil {
		t.Fatal(err)
	}
	if reported := value.(map[string]any)["repo"]; reported != elsewhere.CommonDir {
		t.Fatalf("whoami named the wrong workroom: %v", reported)
	}
}

func TestAdapterStartsFromMainAndLinkedWorktreeInOneWorkroom(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")
	if output, err := exec.Command("git", "init", "-q", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	workspace, seed, err := app.Init(ctx, repo, "human", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}

	mainAdapter := newServer("human", repo)
	linkedAdapter := newServer("human", linked)
	mainValue, mainRoom, err := mainAdapter.call(ctx, toolCall{Name: "status"})
	if err != nil {
		t.Fatalf("start from main checkout: %v", err)
	}
	linkedValue, linkedRoom, err := linkedAdapter.call(ctx, toolCall{Name: "status"})
	if err != nil {
		t.Fatalf("start from linked checkout: %v", err)
	}
	if mainValue.(actorStatus).Totals.Depth != 1 || linkedValue.(actorStatus).Totals.Depth != 1 {
		t.Fatalf("initial status disagreed: main=%+v linked=%+v", mainValue, linkedValue)
	}
	if mainRoom.workspace.GitDir == linkedRoom.workspace.GitDir || mainRoom.workspace.CommonDir != linkedRoom.workspace.CommonDir {
		t.Fatalf("adapter conflated checkout and repository scopes: main git=%q common=%q linked git=%q common=%q", mainRoom.workspace.GitDir, mainRoom.workspace.CommonDir, linkedRoom.workspace.GitDir, linkedRoom.workspace.CommonDir)
	}

	if _, _, err := mainAdapter.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "spoken from main", "rests_on": []any{seed.ID}, "idempotency_key": "main-adapter-write",
	}}); err != nil {
		t.Fatalf("state from main checkout: %v", err)
	}
	if _, _, err := linkedAdapter.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
		"kind": "assert", "text": "spoken from linked checkout", "rests_on": []any{seed.ID}, "idempotency_key": "linked-adapter-write",
	}}); err != nil {
		t.Fatalf("state from linked checkout: %v", err)
	}
	mainValue, _, err = mainAdapter.call(ctx, toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	linkedValue, _, err = linkedAdapter.call(ctx, toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if mainValue.(actorStatus).Totals.Depth != 3 || linkedValue.(actorStatus).Totals.Depth != 3 {
		t.Fatalf("adapters did not share one durable sequence: main=%+v linked=%+v", mainValue, linkedValue)
	}
	if snapshot, err := workspace.Snapshot(ctx); err != nil || snapshot.Depth != 3 {
		t.Fatalf("main workspace did not observe both adapter writes: snapshot=%+v err=%v", snapshot, err)
	}
}

func TestOneAdapterSharesRoomAcrossLinkedWorktrees(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	linked := filepath.Join(root, "linked")
	if output, err := exec.Command("git", "init", "-q", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "ordinary seed").CombinedOutput(); err != nil {
		t.Fatalf("seed ordinary history: %v: %s", err, output)
	}
	if _, _, err := app.Init(ctx, repo, "human", 1<<20); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", repo, "worktree", "add", "-qb", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("add linked worktree: %v: %s", err, output)
	}

	mainGitDir, mainCommonDir, err := app.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	linkedGitDir, linkedCommonDir, err := app.ResolveGitDirs(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if mainGitDir == linkedGitDir || mainCommonDir != linkedCommonDir {
		t.Fatalf("checkout and repository identities disagree: main git=%q common=%q linked git=%q common=%q", mainGitDir, mainCommonDir, linkedGitDir, linkedCommonDir)
	}

	server := newServer("human", repo)
	mainRoom, err := server.attach(ctx, repo)
	if err != nil {
		t.Fatalf("attach main checkout: %v", err)
	}
	linkedRoom, err := server.attach(ctx, linked)
	if err != nil {
		t.Fatalf("attach linked checkout: %v", err)
	}
	if mainRoom != linkedRoom {
		t.Fatal("one adapter allocated separate room and projection caches for linked checkouts")
	}
	if len(server.byCommonDir) != 1 || server.byCommonDir[mainCommonDir] != mainRoom {
		t.Fatalf("common-directory cache does not hold the shared room: %+v", server.byCommonDir)
	}
	if server.byPath[absolute(repo)] != mainRoom || server.byPath[absolute(linked)] != mainRoom {
		t.Fatalf("checkout paths do not resolve to the shared room: %+v", server.byPath)
	}
}

// A directory with no workroom is an ordinary answer to one call, not a reason
// to refuse the connection: the adapter is installed once and pointed at many
// repositories, most of which are not workrooms.
func TestCallOutsideAWorkroomIsReportedWithoutFailingTheConnection(t *testing.T) {
	server := newServer("human", t.TempDir())
	_, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err == nil || !strings.Contains(err.Error(), "no gitseq workroom for") {
		t.Fatalf("unexpected error outside a workroom: %v", err)
	}
	elsewhere := initRepository(t, "elsewhere")
	if _, _, err := server.call(context.Background(), toolCall{Name: "status", Arguments: map[string]any{"repo": elsewhere.Repo}}); err != nil {
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
	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if status := value.(actorStatus); !status.Live.Degraded {
		t.Fatalf("status was not degraded before any service published itself: %+v", status.Live)
	}

	if _, err := workspace.PublishResident(httpServer.URL); err != nil {
		t.Fatal(err)
	}
	value, _, err = server.call(context.Background(), toolCall{Name: "status"})
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
	presence, _, err := server.call(context.Background(), toolCall{Name: "presence"})
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
	if _, _, err := server.call(context.Background(), toolCall{Name: "whoami"}); err != nil {
		t.Fatal(err)
	}
	if got := len(server.attended()); got != 0 {
		t.Fatalf("whoami attended %d rooms before establishing orientation", got)
	}
	if _, _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
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
	value, _, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{
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
	inspected, _, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{}})
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
	if _, _, err := server.call(context.Background(), toolCall{Name: "presence", Arguments: map[string]any{"focus": tooMany}}); err == nil {
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
	if _, _, err := server.call(context.Background(), toolCall{Name: "status"}); err != nil {
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
	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
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

			value, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
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
			value, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
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
	if _, _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
		t.Fatal(err)
	}

	var working sync.WaitGroup
	for caller := 0; caller < 4; caller++ {
		working.Add(1)
		go func() {
			defer working.Done()
			for turn := 0; turn < 10; turn++ {
				if _, _, err := server.call(context.Background(), toolCall{Name: "presence"}); err != nil {
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

// A crashed adapter leaves its presence lease behind for up to thirty seconds.
// Its replacement must be able to start immediately under the same actor
// identity, while making the review-authority and coordination consequences
// visible to the operator.
func TestCrashRestartSharesALiveIdentityWithAWarning(t *testing.T) {
	workspace := initRepository(t, "repo")
	if _, _, err := workspace.AddActor(context.Background(), "human", "claude.2", "agent"); err != nil {
		t.Fatal(err)
	}
	alias := workspace.Config.Actors["human"]
	alias.Name = "human-alias"
	workspace.Config.Actors[alias.Name] = alias
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
	first.session = "mcp:before-crash"
	if _, err := first.attend(context.Background(), ""); err != nil {
		t.Fatalf("first instance refused a free identity: %v", err)
	}
	// Do not depart: this is the crash case, so the first 30-second lease is
	// still visible when its replacement starts.
	second, _ := attachedServer(t, workspace, "human-alias", httpServer.URL, httpServer.Client())
	second.session = "mcp:after-restart"
	var notices bytes.Buffer
	second.notices = &notices
	if _, err := second.attend(context.Background(), ""); err != nil {
		t.Fatalf("replacement instance refused a shared actor identity: %v", err)
	}
	warning := notices.String()
	for _, want := range []string{`identity "human-alias"`, "1 other session", "no independent force", "race on claims and presence"} {
		if !strings.Contains(warning, want) {
			t.Errorf("shared-identity warning %q does not contain %q", warning, want)
		}
	}
	if _, _, err := second.call(context.Background(), toolCall{Name: "presence"}); err != nil {
		t.Fatalf("replacement instance could not act: %v", err)
	}
	if got := notices.String(); got != warning {
		t.Fatalf("shared-identity warning repeated after attachment: before %q, after %q", warning, got)
	}
	var live nexus.Snapshot
	response, err := http.Get(httpServer.URL + "/v0/presence")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&live); err != nil {
		t.Fatal(err)
	}
	label := "human-alias (" + workspace.Config.Actors["human"].Fingerprint[:12] + ")"
	held := 0
	for _, present := range live.Presence {
		if present == label {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("live sessions under the alias label = %d, want 1", held)
	}
	third := newServer("human", workspace.Repo)
	third.session = "mcp:parallel-worker"
	var thirdNotices bytes.Buffer
	third.notices = &thirdNotices
	if _, err := third.attend(context.Background(), ""); err != nil {
		t.Fatalf("third session refused a shared actor identity: %v", err)
	}
	if warning := thirdNotices.String(); !strings.Contains(warning, "2 other session(s)") {
		t.Fatalf("third session warning does not report both other sessions: %q", warning)
	}
	distinct := newServer("claude.2", workspace.Repo)
	var distinctNotices bytes.Buffer
	distinct.notices = &distinctNotices
	if _, err := distinct.attend(context.Background(), ""); err != nil {
		t.Fatalf("a distinct instance identity was refused: %v", err)
	}
	if distinctNotices.Len() != 0 {
		t.Fatalf("a distinct actor received a shared-identity warning: %q", distinctNotices.String())
	}
}

// The check is only as good as the resident service, and a stopped service
// must not stop the work. Starting anyway is the stated limit, not an oversight.
func TestSharedIdentityWarningIsSkippedWhenPresenceCannotBeRead(t *testing.T) {
	workspace := initRepository(t, "repo")
	dead := httptest.NewServer(nil)
	baseURL := dead.URL
	client := dead.Client()
	dead.Close()
	server, attached := attachedServer(t, workspace, "human", baseURL, client)
	server.session = "mcp:offline"
	var notices bytes.Buffer
	server.notices = &notices
	if err := server.warnSharedIdentity(context.Background(), attached); err != nil {
		t.Fatalf("unreadable presence blocked startup: %v", err)
	}
	if !strings.Contains(notices.String(), "shared-identity check skipped") {
		t.Fatalf("degraded-open startup did not explain the skipped warning: %q", notices.String())
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
	value, _, err := server.call(context.Background(), toolCall{Name: "whoami"})
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
	if err := workspace.InvalidateCheckpoint(ctx); err != nil {
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
	if err := workspace.InvalidateCheckpoint(ctx); err != nil {
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
	result = callWhoami(t, workspace, source.URL, source.Client())
	if followed.Load() != 0 || result["source"] == residentOrientationSource || result["degraded"] != true {
		t.Fatalf("resident redirect was followed: followed=%d result=%#v", followed.Load(), result)
	}
	for _, raw := range []string{"https://127.0.0.1:7777", "http://example.com", "http://user@127.0.0.1:7777", "http://127.0.0.1:7777/path"} {
		if _, err := validateResidentURL(raw); err == nil {
			t.Fatalf("accepted unsafe resident URL %q", raw)
		}
	}
}

func TestResidentRequestDeadlinesPreserveCallerCancellation(t *testing.T) {
	if residentCallTimeout != 10*time.Second || residentWaitTimeout != 35*time.Second || residentHTTPTimeout != 40*time.Second {
		t.Fatalf("resident deadline policy changed: call=%s wait=%s client=%s", residentCallTimeout, residentWaitTimeout, residentHTTPTimeout)
	}
	if client := newResidentClient(); client.Timeout() != residentHTTPTimeout {
		t.Fatalf("resident HTTP backstop = %s, want %s", client.Timeout(), residentHTTPTimeout)
	}
	policy := newServer("human", "").deadlines
	if policy.call != residentCallTimeout || policy.wait != residentWaitTimeout || policy.shutdown != residentShutdownTimeout {
		t.Fatalf("server deadline policy = %#v", policy)
	}

	workspace, _ := signedWorkspace(t, 1)
	t.Run("adapter timeout includes response body", func(t *testing.T) {
		stalled := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte("{"))
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
		}))
		defer stalled.Close()
		server, current := attachedServer(t, workspace, "human", stalled.URL, stalled.Client())
		server.deadlines.call = 50 * time.Millisecond

		started := time.Now()
		_, err := server.get(context.Background(), current, "/v0/status")
		elapsed := time.Since(started)
		var timeout residentTimeoutError
		if !errors.As(err, &timeout) || !errors.Is(err, context.DeadlineExceeded) || !isTransportError(err) {
			t.Fatalf("stalled response error = %T %v, want resident timeout transport error", err, err)
		}
		if elapsed > time.Second {
			t.Fatalf("stalled response took %s, want a bounded call", elapsed)
		}
	})

	t.Run("caller cancellation is not relabelled", func(t *testing.T) {
		stalled := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer stalled.Close()
		server, current := attachedServer(t, workspace, "human", stalled.URL, stalled.Client())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := server.get(ctx, current, "/v0/status")
		var timeout residentTimeoutError
		if !errors.Is(err, context.Canceled) || errors.As(err, &timeout) || !isTransportError(err) {
			t.Fatalf("cancelled request error = %T %v, want distinct caller cancellation", err, err)
		}
	})
}

func TestResidentShutdownHasIndependentDeadline(t *testing.T) {
	if residentShutdownTimeout != 2*time.Second {
		t.Fatalf("resident shutdown timeout = %s, want 2s", residentShutdownTimeout)
	}
	called := make(chan struct{}, 1)
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			called <- struct{}{}
			<-request.Context().Done()
		}
	}))
	defer stalled.Close()
	workspace, _ := signedWorkspace(t, 1)
	server, current := attachedServer(t, workspace, "human", stalled.URL, stalled.Client())
	current.announced = true
	server.deadlines.shutdown = 50 * time.Millisecond

	started := time.Now()
	server.shutdown()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stalled shutdown took %s, want a bounded departure", elapsed)
	}
	select {
	case <-called:
	default:
		t.Fatal("shutdown did not attempt resident departure")
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
	for b.Loop() {
		if err := workspace.InvalidateCheckpoint(ctx); err != nil {
			b.Fatal(err)
		}
		fresh, _ := app.Open(ctx, workspace.Repo)
		if result := callWhoami(b, fresh, deadURL, deadClient); result["source"] != string(app.SnapshotSourceColdFullAudit) {
			b.Fatalf("full audit source = %#v", result)
		}
	}
}

// Every trap these notes exist for looked like success at the moment it was
// filed: a record came back, the statement was ruled effective, the commitment
// moved. Each case here is one that has actually happened in this workroom, so
// each is asserted on its own rather than through a table that would let one
// quietly stop firing.
func TestProjectionNotesSayHowTheFoldReadTheAct(t *testing.T) {
	const event = "git:sha1:g#git:sha1:report"
	report := app.Act{Kind: workroom.KindReport, RestsOn: []string{"git:sha1:g#git:sha1:promise"}}
	// Existence is read from decisions now, because there is one per durable
	// record and statements hold only utterances. The fixture carries both so
	// it describes a real projection rather than half of one.
	known := workroom.Projection{
		Statements: []workroom.Statement{{Event: "git:sha1:g#git:sha1:promise"}},
		Decisions:  []workroom.Decision{{Event: "git:sha1:g#git:sha1:promise", Verdict: workroom.Effective}},
	}

	// A report that reads as a review to any human and sets no body.verdict is
	// no review at all to the fold. This is the failure the request was filed
	// for: three such acts landed, all effective, none projecting as reviews.
	notes := projectionNotes(known, report, event)
	if got, _ := notes["review"].(string); !strings.HasPrefix(got, "no:") {
		t.Errorf("a report that is no review was described as %q", got)
	}

	// A review whose artifact did not resolve is the failure that has blocked a
	// merge three times: `gs merge` wants the artifact in the approval's own
	// rests_on and will not walk to find it.
	reviewed := known
	reviewed.Reviews = []workroom.Review{{Report: event, Verdict: "approved"}}
	notes = projectionNotes(reviewed, report, event)
	if got, _ := notes["review"].(string); !strings.Contains(got, "gs merge") {
		t.Errorf("a review with no artifact was described as %q, without saying merge would refuse it", got)
	}

	resolved := known
	resolved.Reviews = []workroom.Review{{Report: event, Verdict: "approved", Artifact: "git:sha1:g#git:sha1:art"}}
	notes = projectionNotes(resolved, report, event)
	if got, _ := notes["review"].(string); !strings.HasPrefix(got, "yes, judging artifact") {
		t.Errorf("a resolved review was described as %q", got)
	}

	// A citation naming nothing in this workroom is skipped in silence by
	// validateBasis unless the kind's own basis constraints happen to need it.
	// Three acts have been filed here around an invented identifier.
	invented := app.Act{Kind: workroom.KindAssert, RestsOn: []string{"git:sha1:g#git:sha1:promise", "git:sha1:g#git:sha1:nothing"}}
	notes = projectionNotes(known, invented, event)
	unresolved, _ := notes["unresolved_rests_on"].([]string)
	if len(unresolved) != 1 || unresolved[0] != "git:sha1:g#git:sha1:nothing" {
		t.Errorf("unresolved citations = %v, want the one naming nothing", unresolved)
	}
	if _, reported := projectionNotes(known, report, event)["unresolved_rests_on"]; reported {
		t.Error("a citation that does resolve was reported as unresolved")
	}

	// The fold's own ruling, when it is anything but plain effect. An
	// ineffective act still returns a record and still reads as success.
	ruled := known
	ruled.Decisions = []workroom.Decision{{Event: event, Verdict: workroom.Ineffective, Reason: "dangling promise has no request"}}
	notes = projectionNotes(ruled, report, event)
	if notes["verdict"] != string(workroom.Ineffective) || notes["reason"] != "dangling promise has no request" {
		t.Errorf("an ineffective ruling was reported as %+v", notes)
	}
	effective := known
	effective.Decisions = []workroom.Decision{{Event: event, Verdict: workroom.Effective}}
	if _, noisy := projectionNotes(effective, report, event)["verdict"]; noisy {
		t.Error("an ordinary effective act was annotated with a verdict it did not need")
	}

	// With no event to look up there is nothing honest to say.
	if notes := projectionNotes(known, report, ""); notes != nil {
		t.Errorf("notes were invented for an unidentified act: %+v", notes)
	}
}

// supersede and ratify carry their subject as a target rather than in rests_on,
// and an earlier version of these notes returned before looking at either,
// because it reported only on `state`. The gap was not theoretical: an act with
// a fabricated target was filed against this workroom while this change sat in
// review, and the fold ruled it "supersede target is unknown" while the tool
// result said only that the act had landed.
func TestNotesReportATargetNamingNothing(t *testing.T) {
	const event = "git:sha1:g#git:sha1:act"
	known := workroom.Projection{
		Statements: []workroom.Statement{{Event: "git:sha1:g#git:sha1:real"}},
		Decisions:  []workroom.Decision{{Event: "git:sha1:g#git:sha1:real", Verdict: workroom.Effective}},
	}

	invented := app.Act{Verb: app.VerbSupersede, Target: "git:sha1:g#git:sha1:invented"}
	if got := projectionNotes(known, invented, event)["unresolved_target"]; got != "git:sha1:g#git:sha1:invented" {
		t.Errorf("unresolved_target = %v, want the target that names nothing", got)
	}

	real := app.Act{Verb: app.VerbRatify, Target: "git:sha1:g#git:sha1:real"}
	if _, reported := projectionNotes(known, real, event)["unresolved_target"]; reported {
		t.Error("a target that does resolve was reported as unresolved")
	}

	// A state act carries no target, and inventing a note for one would be
	// noise on every ordinary append.
	if _, reported := projectionNotes(known, app.Act{Verb: app.VerbState}, event)["unresolved_target"]; reported {
		t.Error("an act with no target was annotated with one")
	}
}

// Ratify and supersede are durable records with no statement, and the fold
// explicitly allows superseding a supersession. Resolving citations through
// statements therefore called real identifiers fabricated — a check written to
// catch invented names, reporting valid ones as invented. That is worse than no
// check, because a warning that cries wolf is a warning people learn to skip.
func TestCitationsResolveThroughEveryDurableRecordNotOnlyStatements(t *testing.T) {
	const supersession = "git:sha1:g#git:sha1:supersede"
	const statement = "git:sha1:g#git:sha1:promise"
	const event = "git:sha1:g#git:sha1:new"

	// A supersession exists as a decision and nothing else, which is exactly
	// the shape statements cannot see.
	projection := workroom.Projection{
		Statements: []workroom.Statement{{Event: statement}},
		Decisions: []workroom.Decision{
			{Event: statement, Verdict: workroom.Effective},
			{Event: supersession, Verdict: workroom.Effective},
			{Event: event, Verdict: workroom.Effective},
		},
	}

	// As a target: superseding a supersession is legitimate and must not be
	// reported as naming nothing.
	retire := app.Act{Verb: app.VerbSupersede, Target: supersession}
	if got, reported := projectionNotes(projection, retire, event)["unresolved_target"]; reported {
		t.Errorf("a valid supersession target was reported unresolved as %v", got)
	}

	// As a citation: the same identifier in rests_on.
	citing := app.Act{Kind: workroom.KindAssert, RestsOn: []string{supersession, statement}}
	if got, reported := projectionNotes(projection, citing, event)["unresolved_rests_on"]; reported {
		t.Errorf("valid citations were reported unresolved as %v", got)
	}

	// And an identifier that truly names nothing is still caught, so the fix
	// is not simply switching the check off.
	invented := app.Act{Kind: workroom.KindAssert, RestsOn: []string{"git:sha1:g#git:sha1:nothing"}}
	if _, reported := projectionNotes(projection, invented, event)["unresolved_rests_on"]; !reported {
		t.Error("a fabricated citation was no longer reported")
	}
}

// A report that sets body.verdict and is then ruled ineffective projects no
// review. Saying it did not set the field would send its author to fix
// something that is not wrong, while the real reason sits in the verdict note
// beside it. The live log holds exactly this shape.
func TestARefusedReportIsNotDescribedAsMissingItsVerdict(t *testing.T) {
	const event = "git:sha1:g#git:sha1:report"
	projection := workroom.Projection{
		Decisions: []workroom.Decision{{Event: event, Verdict: workroom.Ineffective, Reason: "report has no promise"}},
	}

	stated := app.Act{Kind: workroom.KindReport, Body: map[string]string{"verdict": "changes-requested"}}
	notes := projectionNotes(projection, stated, event)
	got, _ := notes["review"].(string)
	if strings.Contains(got, "does not set") {
		t.Errorf("a report that set body.verdict was told it had not: %q", got)
	}
	if !strings.Contains(got, "changes-requested") {
		t.Errorf("the review note does not repeat the verdict actually submitted: %q", got)
	}
	// The reason it was refused must still be reachable from the same result.
	if notes["reason"] != "report has no promise" {
		t.Errorf("the fold's reason was not carried alongside: %v", notes["reason"])
	}

	// A report that genuinely sets nothing still gets the original message.
	silent := app.Act{Kind: workroom.KindReport}
	if got, _ := projectionNotes(projection, silent, event)["review"].(string); !strings.Contains(got, "does not set") {
		t.Errorf("a report with no verdict was described as %q", got)
	}
}

// Every verb is promised these notes, so every verb must be told when they
// could not be produced. Before this, a failed snapshot gave `state` a warning
// and gave ratify and supersede nothing at all — a result identical to one
// where the fold looked and found nothing worth saying. That is the exact
// failure this disclosure exists to prevent, arriving through the disclosure.
func TestEveryVerbIsToldWhenTheProjectionCouldNotBeRead(t *testing.T) {
	workspace := initRepository(t, "unreadable")
	server, attached := attachedServer(t, workspace, "claude", "", nil)

	// Break the repository underneath so Snapshot genuinely fails, rather than
	// faking the failure at a seam the production path does not use.
	if err := os.RemoveAll(workspace.Repo); err != nil {
		t.Fatal(err)
	}

	for _, act := range []app.Act{
		{Verb: app.VerbState, Kind: workroom.KindAssert},
		{Verb: app.VerbRatify, Target: "git:sha1:g#git:sha1:target"},
		{Verb: app.VerbSupersede, Target: "git:sha1:g#git:sha1:target"},
	} {
		value := map[string]any{"event": "git:sha1:g#git:sha1:landed"}
		result, ok := server.withKindWarning(context.Background(), attached, act, value).(map[string]any)
		if !ok {
			t.Fatalf("%s did not return a result map", act.Verb)
		}
		projected, present := result["projected"].(map[string]any)
		if !present {
			t.Errorf("%s returned no projected notes when the projection could not be read: %v", act.Verb, result)
			continue
		}
		if _, said := projected["unavailable"]; !said {
			t.Errorf("%s reported projected notes without saying they were unavailable: %v", act.Verb, projected)
		}
	}
}

// Attention is an adjunct, so the properties worth pinning are the ones that
// make it safe to ignore: it must never fail a call, never invent a
// relationship, and never be invisible to a client that reads only text.
func TestLiveAttentionIsAdvisoryAndFailsSoft(t *testing.T) {
	workspace := initRepository(t, "repo")

	t.Run("an unreachable resident yields unavailable, not an error", func(t *testing.T) {
		dead := httptest.NewServer(nil)
		baseURL, client := dead.URL, dead.Client()
		dead.Close()
		server, attached := attachedServer(t, workspace, "human", baseURL, client)
		report := server.liveAttention(context.Background(), attached, toolCall{Name: "status"}, nil)
		if report["available"] != false {
			t.Fatalf("a dead resident produced %+v, want available=false", report)
		}
		if summary := attentionSummary(report); summary != "" {
			t.Fatalf("an unavailable read produced text: %q", summary)
		}
	})

	t.Run("no room yields unavailable rather than finding one", func(t *testing.T) {
		server := newServer("human", workspace.Repo)
		server.session = "mcp:test"
		if report := server.liveAttention(context.Background(), nil, toolCall{Name: "status"}, nil); report["available"] != false {
			t.Fatalf("a nil room produced %+v", report)
		}
		if len(server.byPath) != 0 {
			t.Fatalf("the attention read attached a room as a side effect: %+v", server.byPath)
		}
	})
}

// Event extraction is the point where an adjunct could start guessing. It reads
// exact canonical identifiers out of what the call said and what it returned,
// and nothing else: no prefixes, no bare hashes, no words that look like refs.
func TestAttentionEventsAreExactAndBounded(t *testing.T) {
	const real = "git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const other = "git:sha1:cccccccccccccccccccccccccccccccccccccccc#git:sha1:dddddddddddddddddddddddddddddddddddddddd"

	got := attentionEvents(toolCall{Name: "inspect", Arguments: map[string]any{"event": real}}, map[string]any{"related": other})
	if len(got) != 2 || got[0] != real || got[1] != other {
		t.Fatalf("events = %v, want the identifier from the input and the one from the result", got)
	}

	// Near misses must not match. Each of these is something a looser pattern
	// would have accepted, and each would be the adapter asserting a link.
	for _, near := range []string{
		"git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"git:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"git:sha1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA#git:sha1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
	} {
		if got := attentionEvents(toolCall{Arguments: map[string]any{"x": near}}, nil); len(got) != 0 {
			t.Fatalf("near miss %q matched as %v", near, got)
		}
	}

	// A result naming thousands of events must not put thousands on the wire.
	many := make([]string, 0, maxAttentionEvents*3)
	for index := 0; index < maxAttentionEvents*3; index++ {
		many = append(many, fmt.Sprintf("git:sha1:%040d#git:sha1:%040d", index, index))
	}
	if got := attentionEvents(toolCall{}, many); len(got) != maxAttentionEvents {
		t.Fatalf("collected %d events, want the cap of %d", len(got), maxAttentionEvents)
	}

	// The same identifier named twice is one event, not two.
	if got := attentionEvents(toolCall{Arguments: map[string]any{"a": real, "b": real}}, real); len(got) != 1 {
		t.Fatalf("a repeated identifier produced %v", got)
	}
}

// The guaranteed text block is what a client that ignores structured content
// still sees. If an interruption exists only in structuredContent it is
// invisible to a reader of the transcript, which defeats the purpose.
func TestAttentionSummaryStatesTheInterruptionInText(t *testing.T) {
	summary := attentionSummary(map[string]any{
		"available": true,
		"pending":   float64(3),
		"omitted":   float64(1),
		"actors": []any{
			map[string]any{"name": "codex", "status": "busy"},
			map[string]any{"name": "planner", "status": "available"},
		},
		"omitted_actors": float64(2),
	})
	for _, want := range []string{"3 unacknowledged addressed messages", "1 not shown", "codex (busy)", "planner", "2 more", "advisory"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q omits %q", summary, want)
		}
	}
	if strings.Contains(summary, "planner (available)") {
		t.Fatalf("an ordinary available status was decorated: %q", summary)
	}

	// Nothing to say means nothing said. A line that always appears becomes
	// furniture and stops being read.
	if got := attentionSummary(map[string]any{"available": true, "pending": float64(0)}); got != "" {
		t.Fatalf("an empty report produced text: %q", got)
	}
	if got := attentionSummary(map[string]any{"available": false}); got != "" {
		t.Fatalf("an unavailable report produced text: %q", got)
	}
	if got := attentionSummary(nil); got != "" {
		t.Fatalf("a nil report produced text: %q", got)
	}
}

// A failing tool call is exactly when a caller most needs to know that somebody
// addressed them, so the adjunct rides on the error path too. This drives the
// real JSON-RPC loop rather than the helper, because the property is about the
// envelope the client actually receives.
func TestToolErrorResultStillCarriesLiveAttention(t *testing.T) {
	workspace := initRepository(t, "repo")
	server := newServer("human", workspace.Repo)
	server.session = "mcp:test"
	defer server.depart(context.Background())

	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	// An unknown tool name is refused by the dispatcher, which is the simplest
	// way to reach the error branch without breaking anything real.
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{" + meta + ",\"name\":\"no-such-tool\",\"arguments\":{}}}\n")
	var output bytes.Buffer
	if err := server.run(context.Background(), input, &output); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode %s: %v", output.String(), err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %s", output.String())
	}
	if result["isError"] != true {
		t.Fatalf("expected an error result, got %#v", result)
	}
	attention, present := result["live_attention"]
	if !present {
		t.Fatalf("an error result dropped the attention adjunct: %#v", result)
	}
	report, ok := attention.(map[string]any)
	if !ok {
		t.Fatalf("live_attention is not an object: %#v", attention)
	}
	// With no resident reachable in this test the honest answer is unavailable,
	// and the point is that the field is present and truthful rather than absent.
	if _, held := report["available"]; !held {
		t.Fatalf("the adjunct does not say whether it is available: %#v", report)
	}
	// The error text must still be the first thing a reader sees.
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("error result has no content: %#v", result)
	}
	first := content[0].(map[string]any)
	if first["text"] == "" {
		t.Fatalf("the error message was displaced: %#v", content)
	}
}

// A call that names a repository must read that repository's attention and no
// other. The adjunct is assembled after the durable work, and an earlier draft
// chose the room at that point — preferring the adapter default, falling back
// to "the only attachment". With two repositories attached, that hands back
// another workroom's addressed inbox and leased focus: a disclosure across a
// boundary the caller drew deliberately with arguments.repo.
func TestAttentionReadsTheRoomTheCallActedIn(t *testing.T) {
	first := initRepository(t, "first")
	second := initRepository(t, "second")

	server := newServer("human", first.Repo)
	server.session = "mcp:test"
	firstRoom := &room{workspace: first, baseURL: "http://first.invalid"}
	secondRoom := &room{workspace: second, baseURL: "http://second.invalid"}
	server.byPath[first.Repo] = firstRoom
	server.byPath[second.Repo] = secondRoom
	server.byCommonDir[first.CommonDir] = firstRoom
	server.byCommonDir[second.CommonDir] = secondRoom

	// The adapter default is the first repository. A call naming the second
	// must still be answered about the second.
	_, acted, err := server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": second.Repo}})
	if err != nil {
		t.Fatalf("call into the second repository failed: %v", err)
	}
	if acted == nil {
		t.Fatal("call returned no room for a successful invocation")
	}
	if acted.workspace.Repo != second.Repo {
		t.Fatalf("call acted in %q but reported room %q; the adjunct would read the wrong workroom",
			second.Repo, acted.workspace.Repo)
	}

	// And a call naming the first is answered about the first, so the test
	// cannot pass by always returning the second.
	_, acted, err = server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": first.Repo}})
	if err != nil {
		t.Fatalf("call into the first repository failed: %v", err)
	}
	if acted.workspace.Repo != first.Repo {
		t.Fatalf("call acted in %q but reported room %q", first.Repo, acted.workspace.Repo)
	}

	// Room selection failing yields no room at all, rather than a guess. The
	// adjunct then reports unavailable, which is the honest answer.
	_, acted, err = server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": filepath.Join(t.TempDir(), "not-a-repo")}})
	if err == nil {
		t.Fatal("expected an error selecting a nonexistent repository")
	}
	if acted != nil {
		t.Fatalf("a failed selection still produced a room: %+v", acted.workspace.Repo)
	}
	if report := server.liveAttention(context.Background(), acted, toolCall{Name: "whoami"}, nil); report["available"] != false {
		t.Fatalf("no room did not yield available=false: %+v", report)
	}
}

// The identifier pattern needs boundaries, not just shape. Without them a
// canonical-looking identifier can be cut out of the middle of a longer token,
// and the adjunct would report actors focused on an event the caller never
// named — inference dressed as observation.
func TestAttentionEventsRequireTokenBoundaries(t *testing.T) {
	const real = "git:sha1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#git:sha1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	for _, embedded := range []string{
		"x" + real,        // a leading byte makes it a different token
		real + "c",        // a trailing hex byte extends the identifier
		"deadbeef" + real, // buried after other hex
		real + "0",        // one more hex digit
		// The trailing byte need not be hex to make this a longer token. An
		// earlier boundary excluded only hex on the right, so every case above
		// passed while these did not: the identifier is a prefix of the token,
		// and reporting it would be the inference the docs rule out.
		real + "z", // a non-hex letter still extends the token
		real + "Z", // and in either case
		real + "g", // the first letter past the hex alphabet
		real + ":", // a colon continues a scheme-shaped token
		real + "#", // as does a second separator
	} {
		if got := attentionEvents(toolCall{Arguments: map[string]any{"x": embedded}}, nil); len(got) != 0 {
			t.Fatalf("embedded identifier %q was extracted as %v", embedded, got)
		}
	}

	// Ordinary delimiters around a whole identifier still match: the boundary
	// rule must not make the feature unusable in real text.
	for _, framed := range []string{real, " " + real + " ", "(" + real + ")", "\"" + real + "\"", real + ".", "see " + real + " now"} {
		if got := attentionEvents(toolCall{Arguments: map[string]any{"x": framed}}, nil); len(got) != 1 || got[0] != real {
			t.Fatalf("framed identifier %q produced %v", framed, got)
		}
	}

	// Two identifiers separated by a single delimiter must both survive: the
	// boundary byte is consumed by the first match and must not hide the second.
	const other = "git:sha1:cccccccccccccccccccccccccccccccccccccccc#git:sha1:dddddddddddddddddddddddddddddddddddddddd"
	if got := attentionEvents(toolCall{Arguments: map[string]any{"x": real + " " + other}}, nil); len(got) != 2 {
		t.Fatalf("two adjacent identifiers produced %v", got)
	}
}
