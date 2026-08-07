package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
	"net/http/httptest"
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
	server := &mcpServer{workspace: workspace, actor: "human", baseURL: httpServer.URL, session: "mcp:test", client: httpServer.Client()}
	if err := server.announce(context.Background()); err != nil {
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
	if got := len(result["tools"].([]any)); got != 8 {
		t.Fatalf("got %d tools", got)
	}
	for _, tool := range result["tools"].([]any) {
		schema := tool.(map[string]any)["inputSchema"].(map[string]any)
		if properties, exists := schema["properties"]; exists && properties == nil {
			t.Fatalf("tool schema contains properties:null: %#v", tool)
		}
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
	server := &mcpServer{workspace: workspace, actor: "human", baseURL: baseURL, session: "mcp:offline", client: client}

	value, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := value.(service.Status)
	if !ok || status.Durable.Depth != 1 || status.Live.Cursor.Generation != "degraded" {
		t.Fatalf("unexpected degraded status: %#v", value)
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

	status, err = server.localStatus(context.Background())
	if err != nil || status.Durable.Depth != 2 {
		t.Fatalf("durable append did not land: status=%+v err=%v", status, err)
	}
	waited, err := server.call(context.Background(), toolCall{Name: "wait", Arguments: map[string]any{"cursor": status.Cursor, "timeout_ms": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if response := waited.(service.WaitResponse); response.Status.Durable.Depth != 2 || response.Reset {
		t.Fatalf("unexpected degraded wait response: %+v", response)
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
