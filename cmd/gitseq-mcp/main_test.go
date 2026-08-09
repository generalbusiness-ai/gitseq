package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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

// attachedServer builds an adapter that has already joined one repository,
// which is the state a tool call leaves behind once it has named one.
func attachedServer(t *testing.T, workspace *app.Workspace, actor, baseURL string, client *http.Client) (*mcpServer, *room) {
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

func depth(t *testing.T, workspace *app.Workspace) int {
	t.Helper()
	snapshot, err := workspace.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Depth
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
	if len(presence.(map[string]any)) == 0 {
		t.Fatal("the session announced no presence through the published service")
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
