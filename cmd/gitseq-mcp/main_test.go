package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/mergeplan"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

func TestStatelessDiscoverAndToolList(t *testing.T) {
	parallelTest(t)
	workspace := initRepository(t, "repo")
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
	if got := len(result["tools"].([]any)); got != 15 {
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
		properties := schema["properties"].(map[string]any)
		if properties["repo"] == nil || properties["agent"] == nil {
			t.Fatalf("tool %q omits the common repo/agent selectors: %#v", definition["name"], properties)
		}
	}
	for _, name := range []string{"work", "artifacts", "inspect", "merge_plan"} {
		if listed[name] == nil {
			t.Fatalf("selective tool %q is missing: %#v", name, listed)
		}
	}
	workProperties := listed["work"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if workProperties["lanes"] == nil || workProperties["statuses"] == nil || workProperties["cursor"] == nil {
		t.Fatalf("work schema does not expose finite filters and continuation: %#v", workProperties)
	}
	artifactProperties := listed["artifacts"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if artifactProperties["paths"] == nil || artifactProperties["limit"] == nil || artifactProperties["cursor"] == nil {
		t.Fatalf("artifact schema does not expose exact paths and continuation: %#v", artifactProperties)
	}
	mergePlanProperties := listed["merge_plan"]["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if mergePlanProperties["authorization"] != nil {
		t.Fatalf("read-only merge_plan unexpectedly exposes structured merge authorization: %#v", mergePlanProperties)
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

// The adapter declares an output schema so that what it already returns is
// described rather than merely sent. Two things decide whether that
// declaration is honest: every tool has to carry one, and real non-empty
// structuredContent has to satisfy the schema its own tool declares. A schema
// that named keys or refused unnamed ones would turn a correct response into
// an invalid one, and validating here is what catches that.
//
// MCP 2026-07-28 admits any JSON Schema as an outputSchema, so the protocol
// does not force an object. Object is right for a narrower reason: every
// gitseq success payload encodes as a JSON object, and an object schema is the
// shape older protocol revisions understand too.
func TestEveryToolDeclaresAnOutputSchemaItsOwnResponsesSatisfy(t *testing.T) {
	// This end-to-end schema proof needs live resident responses. Keep it in
	// the sequential phase so package-local Git and HTTP saturation cannot
	// turn a success-shape assertion into a degraded fallback assertion.
	workspace := initRepository(t, "repo")
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
	// These calls cover both payload shapes the adapter produces: a Go map from
	// whoami and presence, and a statusview struct from status, work, and
	// artifacts, including the paged tools whose page here is empty.
	exercised := []struct {
		name      string
		arguments string
	}{
		{"whoami", "{}"},
		{"presence", "{}"},
		{"status", "{}"},
		{"work", "{}"},
		{"artifacts", `{"paths":["AGENTS.md"]}`},
	}
	var script strings.Builder
	fmt.Fprintf(&script, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"params\":{%s}}\n", meta)
	for index, call := range exercised {
		fmt.Fprintf(&script, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"tools/call\",\"params\":{%s,\"name\":%q,\"arguments\":%s}}\n", index+2, meta, call.name, call.arguments)
	}
	var output bytes.Buffer
	if err := server.run(context.Background(), strings.NewReader(script.String()), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(exercised)+1 {
		t.Fatalf("got %d responses, want %d: %s", len(lines), len(exercised)+1, output.String())
	}
	var listResponse map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &listResponse); err != nil {
		t.Fatal(err)
	}
	declared := make(map[string]map[string]any)
	for _, tool := range listResponse["result"].(map[string]any)["tools"].([]any) {
		definition := tool.(map[string]any)
		name := definition["name"].(string)
		// Naming the tool matters: a tool that misses the declaration is the one
		// thing this has to identify, not that some tool somewhere did.
		schema, isObject := definition["outputSchema"].(map[string]any)
		if !isObject {
			t.Fatalf("tool %q declares no outputSchema object: %#v", name, definition["outputSchema"])
		}
		if schema["type"] != "object" {
			t.Fatalf("tool %q declares outputSchema type %#v, want object", name, schema["type"])
		}
		// The exact boolean, not merely "not false". JSON Schema requires a
		// schema here, so a boolean or an object; a string "true" is invalid
		// and a conforming client rejects the whole declaration. Comparing
		// against the one admissible value rejects every wrong one at once,
		// including absent, which is what the payload checks below cannot see.
		if schema["additionalProperties"] != true {
			t.Fatalf("tool %q declares additionalProperties %#v, want the boolean true", name, schema["additionalProperties"])
		}
		declared[name] = schema
	}
	if len(declared) != 15 {
		t.Fatalf("got %d tools carrying an output schema, want 15: %#v", len(declared), declared)
	}
	for index, call := range exercised {
		var response map[string]any
		if err := json.Unmarshal([]byte(lines[index+1]), &response); err != nil {
			t.Fatal(err)
		}
		result := response["result"].(map[string]any)
		if result["isError"] != false {
			t.Fatalf("tool %q refused, so its response proves nothing about the schema: %#v", call.name, result)
		}
		payload, isObject := result["structuredContent"].(map[string]any)
		if !isObject || len(payload) == 0 {
			t.Fatalf("tool %q returned no non-empty structuredContent to validate: %#v", call.name, result["structuredContent"])
		}
		if err := schemaRejects(declared[call.name], payload); err != nil {
			t.Fatalf("tool %q returns structuredContent its own outputSchema rejects: %v", call.name, err)
		}
	}
}

// schemaRejects validates a payload against the part of JSON Schema the
// adapter declares: the type, the members the schema names, and whether it
// admits any others. A conformant client runs the same check and drops what
// fails it.
func schemaRejects(schema map[string]any, payload map[string]any) error {
	if schema["type"] != "object" {
		return fmt.Errorf("declared type %#v is not object", schema["type"])
	}
	if schema["additionalProperties"] == false {
		named, _ := schema["properties"].(map[string]any)
		for member := range payload {
			if _, isNamed := named[member]; !isNamed {
				return fmt.Errorf("member %q is not named by the schema, which admits no others", member)
			}
		}
	}
	return nil
}

func TestMissingPerRequestMetadataIsRejected(t *testing.T) {
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
	workspace, genesis := signedWorkspace(t, 1)
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
	if page := worked.(statusview.WorkPage); !page.Degraded || page.Frontier.Depth != 2 || page.Actor.Fingerprint != workspace.View().Actors["human"].Fingerprint {
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

func TestMergePlanToolMatchesSharedRefusal(t *testing.T) {
	workspace := initRepository(t, "merge-plan-refusal")
	server, _ := attachedServer(t, workspace, "human", "", http.DefaultClient)
	before, err := workspace.ReadOnlySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := strings.Repeat("0", 40)
	arguments := map[string]any{"candidate": candidate, "approval": "unknown-approval", "checkout": workspace.Repo}
	value, _, err := server.call(context.Background(), toolCall{Name: "merge_plan", Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	actor, private, err := workspace.Actor("human")
	if err != nil {
		t.Fatal(err)
	}
	want := mergeplan.Build(context.Background(), workspace, workspace.Repo, candidate, "unknown-approval", actor.Fingerprint,
		mergeplan.Signer{Name: "human", Private: private})
	got, ok := value.(mergeplan.Result)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP and shared refusal differ\nMCP: %#v\nshared: %#v", value, want)
	}
	after, err := workspace.ReadOnlySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || after.Depth != before.Depth {
		t.Fatalf("MCP merge_plan appended durable state: before=%s/%d after=%s/%d", before.Head, before.Depth, after.Head, after.Depth)
	}
}

type mergePlanBoundaryState struct {
	GitHead          string
	WorkroomHead     string
	Depth            int
	VerifiedFrontier string
	CheckpointRef    string
	CheckpointFile   string
	Config           string
}

func mergePlanTestGit(t testing.TB, repo string, arguments ...string) string {
	t.Helper()
	args := append([]string{"--no-optional-locks", "--no-replace-objects", "-C", repo}, arguments...)
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func captureMergePlanBoundaryState(t testing.TB, workspace *app.Workspace) mergePlanBoundaryState {
	t.Helper()
	snapshot, err := workspace.ReadOnlySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := json.Marshal(workspace.View().VerifiedFrontier)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := workspace.Store.Head(context.Background(), kernel.CheckpointRef(workspace.View().Genesis))
	if err != nil {
		checkpoint = "absent: " + err.Error()
	}
	pointer, err := os.ReadFile(filepath.Join(workspace.MetaDir, "checkpoints", workspace.View().Genesis+".json"))
	if err != nil {
		pointer = []byte("absent: " + err.Error())
	}
	config, err := os.ReadFile(filepath.Join(workspace.MetaDir, apphost.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	return mergePlanBoundaryState{
		GitHead: mergePlanTestGit(t, workspace.Repo, "rev-parse", "HEAD"), WorkroomHead: snapshot.Head, Depth: snapshot.Depth,
		VerifiedFrontier: string(frontier), CheckpointRef: checkpoint, CheckpointFile: string(pointer), Config: string(config),
	}
}

func allowedMergePlanFixture(t *testing.T) (*app.Workspace, string, string) {
	t.Helper()
	ctx := context.Background()
	workspace := initRepository(t, "merge-plan-allowed")
	mergePlanTestGit(t, workspace.Repo, "config", "user.name", "Test")
	mergePlanTestGit(t, workspace.Repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(workspace.Repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mergePlanTestGit(t, workspace.Repo, "add", "base.txt")
	mergePlanTestGit(t, workspace.Repo, "commit", "-m", "base")
	feature := filepath.Join(t.TempDir(), "feature")
	mergePlanTestGit(t, workspace.Repo, "worktree", "add", "-b", "merge-plan-candidate", feature)
	if err := os.WriteFile(filepath.Join(feature, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mergePlanTestGit(t, feature, "add", "feature.txt")
	mergePlanTestGit(t, feature, "commit", "-m", "add feature")
	candidate := mergePlanTestGit(t, feature, "rev-parse", "HEAD")
	if _, _, err := workspace.AddActor(ctx, "human", "reviewer", "agent"); err != nil {
		t.Fatal(err)
	}
	seed := genesisOf(t, workspace)
	artifact, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "feature artifact",
		Body: map[string]string{"path": "feature.txt", "commit": candidate}, RestsOn: []string{seed}, IdempotencyKey: "merge-plan-allowed-artifact",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindRequest, Text: "review feature",
		Body: map[string]string{"to": "@reviewer", "conditions": "approve the exact feature head"}, RestsOn: []string{artifact.Record.ID}, IdempotencyKey: "merge-plan-allowed-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := workspace.Act(ctx, "reviewer", app.Act{
		Verb: app.VerbState, Kind: workroom.KindPromise, Text: "review exact feature head",
		RestsOn: []string{request.Record.ID}, IdempotencyKey: "merge-plan-allowed-promise",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ := attachedServer(t, workspace, "reviewer", "", http.DefaultClient)
	_, _, err = reviewer.call(ctx, toolCall{Name: "review", Arguments: map[string]any{
		"artifacts": []any{artifact.Record.ID}, "promise": promise.Record.ID, "verdict": "approved",
		"text": "approved exact feature head", "idempotency_key": "merge-plan-allowed-review",
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report := snapshot.Projection.Statements[len(snapshot.Projection.Statements)-1]
	if _, err := workspace.Act(ctx, "human", app.Act{
		Verb: app.VerbRatify, Target: report.Event, IdempotencyKey: "merge-plan-allowed-ratification",
	}); err != nil {
		t.Fatal(err)
	}
	return workspace, candidate, report.Event
}

func TestMergePlanToolAllowedResultLeavesGitAndWorkroomAccelerationStateUnchanged(t *testing.T) {
	workspace, candidate, approval := allowedMergePlanFixture(t)
	server, _ := attachedServer(t, workspace, "human", "", http.DefaultClient)
	before := captureMergePlanBoundaryState(t, workspace)
	value, _, err := server.call(context.Background(), toolCall{Name: "merge_plan", Arguments: map[string]any{
		"candidate": candidate, "approval": approval, "checkout": workspace.Repo,
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(mergeplan.Result)
	if !ok || !result.Allowed {
		t.Fatalf("allowed MCP merge_plan result = %#v", value)
	}
	after := captureMergePlanBoundaryState(t, workspace)
	if after != before {
		t.Fatalf("allowed MCP merge_plan mutated governed or acceleration state\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestSelectiveToolsUseResidentSelectionWithoutFetchingStatus(t *testing.T) {
	parallelTest(t)
	workspace := initRepository(t, "repo")
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seed := snapshot.Projection.Actors[workspace.View().Actors["human"].Fingerprint].MembershipEvent
	if _, err := workspace.Act(context.Background(), "human", app.Act{
		Verb: app.VerbState, Kind: workroom.KindArtifact, Text: "live UI artifact",
		Body: map[string]string{"path": "ui", "commit": "candidate"}, RestsOn: []string{seed},
		IdempotencyKey: "mcp-artifact-selector-boundary",
	}); err != nil {
		t.Fatal(err)
	}
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
	if !ok || page.Frontier.Head == "" || page.Actor.Fingerprint != workspace.View().Actors["human"].Fingerprint {
		t.Fatalf("unexpected selective work response: %#v", value)
	}
	value, _, err = server.call(context.Background(), toolCall{Name: "artifacts", Arguments: map[string]any{"paths": []any{"ui"}}})
	if err != nil {
		t.Fatal(err)
	}
	artifactPage, ok := value.(statusview.ArtifactPage)
	if !ok || artifactPage.Frontier.Head == "" || artifactPage.MatchingTotal != 1 || len(artifactPage.Paths) != 1 || artifactPage.Paths[0] != "ui" {
		t.Fatalf("unexpected exact-path artifact response: %#v", value)
	}
	// State and reaches are CLI-only. MCP schemas are advisory, so the adapter
	// must also drop these undeclared fields at the executable boundary. If
	// either leaks through, retired selects zero and the missing anchor selects
	// zero; the established live exact-path contract selects the one row.
	for _, extra := range []map[string]any{{"state": "retired"}, {"reaches": "no/such/anchor"}} {
		arguments := map[string]any{"paths": []any{"ui"}}
		for key, value := range extra {
			arguments[key] = value
		}
		value, _, err = server.call(context.Background(), toolCall{Name: "artifacts", Arguments: arguments})
		if err != nil {
			t.Fatal(err)
		}
		page := value.(statusview.ArtifactPage)
		if page.MatchingTotal != 1 || page.Artifacts[0].Path != "ui" {
			t.Fatalf("undeclared MCP selector %v changed artifact semantics: %+v", extra, page)
		}
	}

	snapshot, err = workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	event := snapshot.Projection.Actors[workspace.View().Actors["human"].Fingerprint].MembershipEvent
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
	wantPaths := []string{
		"/v0/work-query",
		"/v0/artifact-query",
		"/v0/artifact-query",
		"/v0/artifact-query",
		"/v0/inspect",
	}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("selective tools used the wrong resident routes: %v", gotPaths)
	}
}

// Status and wait are bounded at the resident boundary, not after the adapter
// has already transferred and decoded the complete projection. Returning the
// right small MCP value is insufficient evidence: the old implementation did
// that too, after paying for /v0/status in full. Blocking both full routes
// makes this test fail if either transport regresses.
func TestStatusAndWaitUseBoundedResidentViews(t *testing.T) {
	parallelTest(t)
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
	if got, want := laneResponseLimit(attached, actorStatusResponseLimit, statusview.ListCap), int64(actorStatusResponseLimit)+int64(statusview.ListCap)*int64(workspace.View().PayloadCeiling); got != want {
		t.Fatalf("lane response limit = %d, want structurally bounded %d", got, want)
	}
	attached.identityNoticeChecked = true
	if err := server.announce(context.Background(), attached); err != nil {
		t.Fatal(err)
	}
	attached.joined()
	mu.Lock()
	paths = nil
	mu.Unlock()

	value, _, err := server.call(context.Background(), toolCall{Name: "status"})
	if err != nil {
		t.Fatal(err)
	}
	status := value.(actorStatus)
	if status.Frontier[0].Depth != 1 || status.You.Fingerprint != workspace.View().Actors["human"].Fingerprint {
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
	wantPaths := []string{"/v0/actor-status", "/v0/actor-wait"}
	if strings.Join(gotPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("status or wait used an unbounded resident route: %v", gotPaths)
	}
}

// attachedServer builds an adapter that has already joined one repository,
// which is the state a tool call leaves behind once it has named one.
func attachedServer(t testing.TB, workspace *app.Workspace, actor, baseURL string, client *http.Client) (*mcpServer, *room) {
	t.Helper()
	server := newServer(actor, workspace.Repo)
	server.client = residentclient.NewWithHTTP(client, residentHTTPTimeout)
	configured, err := workspace.ResolveActor(actor)
	if err != nil {
		t.Fatal(err)
	}
	head, err := workspace.Store.Head(context.Background(), kernel.Ref(workspace.View().Genesis))
	if err != nil {
		t.Fatal(err)
	}
	attached := &room{workspace: workspace, actor: actor, fingerprint: configured.Fingerprint, baseURL: strings.TrimRight(baseURL, "/"), validatedHead: head}
	genesis := workspace.View().Genesis
	server.byPath[roomSelection{path: server.repo, commonDir: workspace.CommonDir, genesis: genesis, actor: actor, fingerprint: configured.Fingerprint, keyFile: configured.KeyFile}] = attached
	server.byCommonDir[roomSelection{commonDir: workspace.CommonDir, genesis: genesis, actor: actor, fingerprint: configured.Fingerprint, keyFile: configured.KeyFile}] = attached
	return server, attached
}

func initRepository(t *testing.T, name string) *app.Workspace {
	t.Helper()
	workspace, _ := templateAtDepth(1).copy(t, name)
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

func TestStateToolRefusesMalformedRequestBeforeAppend(t *testing.T) {
	parallelTest(t)
	ctx := context.Background()
	workspace := initRepository(t, "repo")
	if _, _, err := workspace.AddActor(ctx, "human", "worker", "agent"); err != nil {
		t.Fatal(err)
	}
	server := newServer("human", workspace.Repo)
	for _, test := range []struct {
		name string
		body map[string]any
		want string
	}{
		{name: "missing conditions", body: map[string]any{"to": "@worker"}, want: "request state requires body.conditions"},
		{name: "unknown performer", body: map[string]any{"to": "@nobody", "conditions": "tests pass"}, want: "request body.to"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := depth(t, workspace)
			_, _, err := server.call(ctx, toolCall{Name: "state", Arguments: map[string]any{
				"kind": "request", "text": "malformed request", "body": test.body,
				"rests_on":        []any{genesisOf(t, workspace)},
				"idempotency_key": "mcp-malformed-" + strings.ReplaceAll(test.name, " ", "-"),
			}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if after := depth(t, workspace); after != before {
				t.Fatalf("refused MCP request changed depth from %d to %d", before, after)
			}
		})
	}
}

// repo is an adapter selector, not a resident wait field. Cover both forms so
// stripping it cannot make the named call fall back to the default room.
func TestResidentWaitKeepsRepositorySelectionOutOfTheRequestBody(t *testing.T) {
	parallelTest(t)
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
			if len(delta.Cursor.Frontier) != 1 || delta.Cursor.Frontier[0].Genesis != testCase.workspace.View().Genesis {
				t.Fatalf("wait answered from the wrong repository: %+v", delta.Cursor.Frontier)
			}
			if delta.Cursor.Live.Generation == "degraded" {
				t.Fatalf("resident wait unexpectedly degraded: %+v", delta.Cursor.Live)
			}
		})
	}
}

func TestResidentSayKeepsRepositorySelectionOutOfTheRequestBody(t *testing.T) {
	parallelTest(t)
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
	parallelTest(t)
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
	var announced struct {
		Credential string `json:"credential"`
	}
	post("/v0/presence", map[string]any{"actor": "other"}, &announced)
	var frame nexus.Frame
	post("/v0/say", map[string]any{"credential": announced.Credential, "about": genesisOf(t, workspace), "text": "@human please review"}, &frame)
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
	parallelTest(t)
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
	parallelTest(t)
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
				_, _ = writer.Write([]byte(`{"error":"credential is not valid"}`))
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
			_ = json.NewEncoder(writer).Encode(map[string]any{"credential": "credential:" + strings.Repeat("b", 64), "change": map[string]any{}})
		case request.Method == http.MethodPost && request.URL.Path == "/v0/actor-status":
			_ = json.NewEncoder(writer).Encode(actorStatus{You: statusview.ActorView{Fingerprint: workspace.View().Actors["human"].Fingerprint}})
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
	attached.credential = "credential:" + strings.Repeat("a", 64)
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
	parallelTest(t)
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
			if len(delta.Cursor.Frontier) != 1 || delta.Cursor.Frontier[0].Genesis != testCase.workspace.View().Genesis {
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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

	mainGitDir, mainCommonDir, err := apphost.ResolveGitDirs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	linkedGitDir, linkedCommonDir, err := apphost.ResolveGitDirs(ctx, linked)
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
	fingerprint := mainRoom.fingerprint
	genesis := mainRoom.workspace.View().Genesis
	keyFile := mainRoom.workspace.View().Actors["human"].KeyFile
	if len(server.byCommonDir) != 1 || server.byCommonDir[roomSelection{commonDir: mainCommonDir, genesis: genesis, actor: "human", fingerprint: fingerprint, keyFile: keyFile}] != mainRoom {
		t.Fatalf("common-directory cache does not hold the shared room: %+v", server.byCommonDir)
	}
	if server.byPath[roomSelection{path: absolute(repo), commonDir: mainCommonDir, genesis: genesis, actor: "human", fingerprint: fingerprint, keyFile: keyFile}] != mainRoom || server.byPath[roomSelection{path: absolute(linked), commonDir: mainCommonDir, genesis: genesis, actor: "human", fingerprint: fingerprint, keyFile: keyFile}] != mainRoom {
		t.Fatalf("checkout paths do not resolve to the shared room: %+v", server.byPath)
	}
}

// A directory with no workroom is an ordinary answer to one call, not a reason
// to refuse the connection: the adapter is installed once and pointed at many
// repositories, most of which are not workrooms.
func TestCallOutsideAWorkroomIsReportedWithoutFailingTheConnection(t *testing.T) {
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
		"actor": "other", "credential": "forged",
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

func TestCredentialsArePerRepositoryAndAbsentFromToolResultsAndURLs(t *testing.T) {
	// This test needs two independent resident departure observations. Keep it
	// sequential so package-local load cannot turn transport scheduling into a
	// missing credential-bound request.
	first := initRepository(t, "first")
	second := initRepository(t, "second")
	type observedRequest struct {
		path  string
		query string
		body  string
	}
	var mu sync.Mutex
	var departures []observedRequest
	serve := func(workspace *app.Workspace) *httptest.Server {
		resident, err := service.New(workspace)
		if err != nil {
			t.Fatal(err)
		}
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/v0/presence/depart" {
				body, _ := io.ReadAll(request.Body)
				request.Body = io.NopCloser(bytes.NewReader(body))
				mu.Lock()
				departures = append(departures, observedRequest{path: request.URL.Path, query: request.URL.RawQuery, body: string(body)})
				mu.Unlock()
			}
			resident.Handler().ServeHTTP(writer, request)
		}))
	}
	firstHTTP := serve(first)
	defer firstHTTP.Close()
	secondHTTP := serve(second)
	defer secondHTTP.Close()
	withdrawFirst, err := first.PublishResident(firstHTTP.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer withdrawFirst()
	withdrawSecond, err := second.PublishResident(secondHTTP.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer withdrawSecond()

	server := newServer("human", first.Repo)
	firstRoom, err := server.attend(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	secondRoom, err := server.attend(context.Background(), second.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if firstRoom.credentialValue() == "" || secondRoom.credentialValue() == "" || firstRoom.credentialValue() == secondRoom.credentialValue() {
		t.Fatal("adapter did not hold distinct resident-minted credentials per repository")
	}
	for _, repo := range []string{first.Repo, second.Repo} {
		value, _, err := server.call(context.Background(), toolCall{Name: "whoami", Arguments: map[string]any{"repo": repo}})
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(value)
		if bytes.Contains(encoded, []byte("credential:")) || bytes.Contains(encoded, []byte(`"session"`)) || bytes.Contains(encoded, []byte(`"credential"`)) {
			t.Fatalf("whoami disclosed resident authority: %s", encoded)
		}
	}
	server.depart(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(departures) != 2 {
		t.Fatalf("departure requests = %d, want one per repository", len(departures))
	}
	for _, request := range departures {
		if request.path != "/v0/presence/depart" || request.query != "" || !strings.Contains(request.body, `"credential":"credential:`) {
			t.Fatalf("departure did not keep authority in the fixed JSON body: %+v", request)
		}
	}
}

// A published address that stops answering is forgotten rather than retried
// forever, so a service restarted on another port is picked up without
// reconnecting the client.
func TestAServiceThatStopsAnsweringIsLookedUpAgain(t *testing.T) {
	parallelTest(t)
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

// An undefined kind now refuses before anything is signed, on both the
// resident and the degraded path, and the refusal names the live vocabulary.
// A defined kind is ordinary work: it lands, carries no warning, and the fold
// ruling still rides in the result.
func TestStateToolRefusesUndefinedKindsOnBothPaths(t *testing.T) {
	parallelTest(t)
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
			genesis := workspace.EventID(workspace.View().Genesis)

			before, err := workspace.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "commit", "text": "I will re-review task/x at exact head y",
				"rests_on": []any{genesis}, "idempotency_key": "undefined-kind",
			}})
			if err == nil {
				t.Fatal("state with an undefined kind was signed")
			}
			for _, want := range []string{`"commit"`, "no override exists", "kinds defined here:", "ratified kind-def"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not say %q", err, want)
				}
			}
			after, err := workspace.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if after.Depth != before.Depth {
				t.Fatalf("refused undefined kind changed depth %d -> %d", before.Depth, after.Depth)
			}

			// A defined kind is ordinary work and carries no warning, so a
			// refusal can never be mistaken for noise on every result.
			value, _, err := server.call(context.Background(), toolCall{Name: "state", Arguments: map[string]any{
				"kind": "assert", "text": "an ordinary claim",
				"rests_on": []any{genesis}, "idempotency_key": "defined-kind",
			}})
			if err != nil {
				t.Fatal(err)
			}
			if warned, held := value.(map[string]any)["warning"]; held {
				t.Fatalf("a defined kind warned anyway: %v", warned)
			}
			projected, ok := value.(map[string]any)["projected"].(map[string]any)
			if !ok || projected["verdict"] != string(workroom.Effective) {
				t.Fatalf("effective state result omitted the fold ruling: %#v", value)
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
	parallelTest(t)
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
	parallelTest(t)
	workspace := initRepository(t, "repo")
	if _, _, err := workspace.AddActor(context.Background(), "human", "claude.2", "agent"); err != nil {
		t.Fatal(err)
	}
	// Seed the alias through the stored configuration and reopen: the
	// configuration field is unexported, so custody is seeded from outside
	// internal/app by editing the file and taking the production load path.
	seeded := workspace.View()
	alias := seeded.Actors["human"]
	alias.Name = "human-alias"
	seeded.Actors[alias.Name] = alias
	if err := apphost.SaveConfig(workspace.MetaDir, seeded); err != nil {
		t.Fatal(err)
	}
	workspace, err := app.Open(context.Background(), workspace.Repo)
	if err != nil {
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
	// Do not depart: this is the crash case, so the first 30-second lease is
	// still visible when its replacement starts.
	second, _ := attachedServer(t, workspace, "human-alias", httpServer.URL, httpServer.Client())
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
	label := "human-alias (" + workspace.View().Actors["human"].Fingerprint[:12] + ")"
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
	parallelTest(t)
	workspace := initRepository(t, "repo")
	dead := httptest.NewServer(nil)
	baseURL := dead.URL
	client := dead.Client()
	dead.Close()
	server, attached := attachedServer(t, workspace, "human", baseURL, client)
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
	return templateAtDepth(depth).copy(tb, "repo")
}

func callWhoami(t testing.TB, workspace *app.Workspace, baseURL string, client *http.Client) map[string]any {
	t.Helper()
	server, _ := attachedServer(t, workspace, "human", baseURL, client)
	value, _, err := server.call(context.Background(), toolCall{Name: "whoami"})
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func TestWhoamiUsesBoundedEffectiveResidentOrientationWithoutLocalReplay(t *testing.T) {
	parallelTest(t)
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
	if durable.Fingerprint != workspace.View().Actors["human"].Fingerprint || durable.Kind != "human" || durable.MembershipEvent == "" || !containsString(durable.Roles, "participant") {
		t.Fatalf("resident lost effective identity: %+v", durable)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("key_file")) || bytes.Contains(encoded, []byte(workspace.View().Actors["human"].KeyFile)) || genesis.ID == "" {
		t.Fatalf("whoami leaked local custody or lost signed basis: %s", encoded)
	}
}

func TestWhoamiDisclosesCheckpointAndFullAuditFallbacks(t *testing.T) {
	parallelTest(t)
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
	parallelTest(t)
	ctx := context.Background()
	workspace, _ := signedWorkspace(t, 1)
	snapshot, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	orientation, ok := statusview.BuildOrientation(snapshot, workspace.View().Actors["human"].Fingerprint, "human")
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
	// This test coordinates a frontier move between two resident reads. Keep
	// it sequential so package-local load cannot delay the move past the
	// deliberately narrow retry window it is proving.
	ctx := context.Background()
	workspace, genesis := signedWorkspace(t, 1)
	first, err := workspace.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := workspace.View().Actors["human"].Fingerprint
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

// These four elapsed-time tests deliberately stay sequential. Go pauses every
// top-level test that called t.Parallel until all sequential top-level tests
// have returned, so none of the package's parallel Git and HTTP fixture work
// can interleave with these bounds.
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
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/v0/presence/depart" {
			called <- struct{}{}
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}
	}))
	defer func() {
		close(release)
		stalled.Close()
	}()
	workspace, _ := signedWorkspace(t, 1)
	server, current := attachedServer(t, workspace, "human", stalled.URL, stalled.Client())
	current.announced = true
	current.credential = "credential:" + strings.Repeat("a", 64)
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
	parallelTest(t)
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

	// The fold's own ruling is always present. An ineffective act still returns
	// a record and still reads as success, while omission on an effective act
	// would force a second projection read to distinguish effect from failure.
	ruled := known
	ruled.Decisions = []workroom.Decision{{Event: event, Verdict: workroom.Ineffective, Reason: "dangling promise has no request"}}
	notes = projectionNotes(ruled, report, event)
	if notes["verdict"] != string(workroom.Ineffective) || notes["reason"] != "dangling promise has no request" {
		t.Errorf("an ineffective ruling was reported as %+v", notes)
	}
	effective := known
	effective.Decisions = []workroom.Decision{{Event: event, Verdict: workroom.Effective}}
	if got := projectionNotes(effective, report, event)["verdict"]; got != string(workroom.Effective) {
		t.Errorf("an ordinary effective act omitted its fold ruling: %v", got)
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
	parallelTest(t)
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
	parallelTest(t)
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

// Every way a citation can be dead while still resolving is invisible in the
// submission result otherwise: the record lands, the verdict reads effective,
// and only the projection knows the ground moved before the act stood on it.
func TestNotesNameARestOnBasisThatWasAlreadyDead(t *testing.T) {
	parallelTest(t)
	const event = "git:sha1:g#git:sha1:new"
	const retired = "git:sha1:g#git:sha1:retired"
	const stale = "git:sha1:g#git:sha1:stale"
	const superseding = "git:sha1:g#git:sha1:supersede"
	projection := workroom.Projection{
		Statements: []workroom.Statement{
			{Event: retired, Retired: true},
			{Event: stale, Stale: true},
			{Event: "git:sha1:g#git:sha1:live"},
		},
		Acts:      []workroom.Act{{Event: superseding, Type: "supersede", Verdict: workroom.Effective}},
		Decisions: []workroom.Decision{{Event: event, Verdict: workroom.Effective}},
	}

	filing := app.Act{Kind: workroom.KindAssert, RestsOn: []string{retired, stale, superseding}}
	dead, reported := projectionNotes(projection, filing, event)["dead_rests_on"].(map[string]string)
	if !reported {
		t.Fatal("no dead_rests_on note for citations on retired, stale, and supersede bases")
	}
	want := map[string]string{retired: "retired", stale: "stale", superseding: "supersede"}
	if len(dead) != len(want) {
		t.Fatalf("dead_rests_on = %v, want exactly %v", dead, want)
	}
	for id, reason := range want {
		if dead[id] != reason {
			t.Errorf("dead_rests_on[%s] = %q, want %q", id, dead[id], reason)
		}
	}

	clean := app.Act{Kind: workroom.KindAssert, RestsOn: []string{"git:sha1:g#git:sha1:live"}}
	if _, reported := projectionNotes(projection, clean, event)["dead_rests_on"]; reported {
		t.Error("a citation on living ground was named as dead")
	}
}

// A report that sets body.verdict and is then ruled ineffective projects no
// review. Saying it did not set the field would send its author to fix
// something that is not wrong, while the real reason sits in the verdict note
// beside it. The live log holds exactly this shape.
func TestARefusedReportIsNotDescribedAsMissingItsVerdict(t *testing.T) {
	parallelTest(t)
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
	parallelTest(t)
	workspace := initRepository(t, "unreadable")
	if _, _, err := workspace.AddActor(context.Background(), "human", "claude", "agent"); err != nil {
		t.Fatal(err)
	}
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
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
	parallelTest(t)
	workspace := initRepository(t, "repo")
	server := newServer("human", workspace.Repo)
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
	parallelTest(t)
	first := initRepository(t, "first")
	second := initRepository(t, "second")

	server := newServer("human", first.Repo)
	firstFingerprint := first.View().Actors["human"].Fingerprint
	secondFingerprint := second.View().Actors["human"].Fingerprint
	firstRoom := &room{workspace: first, actor: "human", fingerprint: firstFingerprint, baseURL: "http://first.invalid"}
	secondRoom := &room{workspace: second, actor: "human", fingerprint: secondFingerprint, baseURL: "http://second.invalid"}
	server.byPath[roomSelection{path: first.Repo, commonDir: first.CommonDir, genesis: first.View().Genesis, actor: "human", fingerprint: firstFingerprint, keyFile: first.View().Actors["human"].KeyFile}] = firstRoom
	server.byPath[roomSelection{path: second.Repo, commonDir: second.CommonDir, genesis: second.View().Genesis, actor: "human", fingerprint: secondFingerprint, keyFile: second.View().Actors["human"].KeyFile}] = secondRoom
	server.byCommonDir[roomSelection{commonDir: first.CommonDir, genesis: first.View().Genesis, actor: "human", fingerprint: firstFingerprint, keyFile: first.View().Actors["human"].KeyFile}] = firstRoom
	server.byCommonDir[roomSelection{commonDir: second.CommonDir, genesis: second.View().Genesis, actor: "human", fingerprint: secondFingerprint, keyFile: second.View().Actors["human"].KeyFile}] = secondRoom

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
	parallelTest(t)
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
