package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/nexus"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
)

const protocolVersion = "2026-07-28"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type protocolMeta struct {
	Version            string         `json:"io.modelcontextprotocol/protocolVersion"`
	ClientCapabilities map[string]any `json:"io.modelcontextprotocol/clientCapabilities"`
}

type mcpServer struct {
	workspace *app.Workspace
	actor     string
	baseURL   string
	session   string
	client    *http.Client
}

func main() {
	repo := flag.String("repo", ".", "ordinary Git repository")
	actor := flag.String("actor", "", "configured actor")
	serverURL := flag.String("server", "http://127.0.0.1:7777", "resident workroom URL")
	flag.Parse()
	if *actor == "" {
		fatal(errors.New("--actor is required"))
	}
	workspace, err := app.Open(context.Background(), *repo)
	if err != nil {
		fatal(err)
	}
	if _, _, err := workspace.Actor(*actor); err != nil {
		fatal(err)
	}
	server := &mcpServer{workspace: workspace, actor: *actor, baseURL: strings.TrimRight(*serverURL, "/"), session: "mcp:" + randomID(), client: &http.Client{}}
	if err := server.announce(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "gitseq-mcp: presence degraded:", err)
	}
	presenceContext, stopPresence := context.WithCancel(context.Background())
	go server.heartbeat(presenceContext, os.Stderr)
	defer func() {
		stopPresence()
		server.depart(context.Background())
	}()
	if err := server.run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fatal(err)
	}
}

func (s *mcpServer) run(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}})
			continue
		}
		if len(request.ID) == 0 {
			continue
		}
		response := rpcResponse{JSONRPC: "2.0", ID: request.ID}
		if request.JSONRPC != "2.0" {
			response.Error = &rpcError{Code: -32600, Message: "invalid JSON-RPC request"}
			if err := encoder.Encode(response); err != nil {
				return err
			}
			continue
		}
		if err := validateProtocolMeta(request.Params); err != nil {
			response.Error = &rpcError{Code: -32602, Message: err.Error()}
			if err := encoder.Encode(response); err != nil {
				return err
			}
			continue
		}
		switch request.Method {
		case "server/discover":
			response.Result = complete(map[string]any{
				"supportedVersions": []string{protocolVersion},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"instructions":      "Use status and wait to follow the workroom; say ephemerally and promote deliberate acts with state.",
				"ttlMs":             3600000,
				"cacheScope":        "public",
			})
		case "tools/list":
			response.Result = complete(map[string]any{"tools": tools(), "ttlMs": 3600000, "cacheScope": "public"})
		case "tools/call":
			var call toolCall
			if err := json.Unmarshal(request.Params, &call); err != nil {
				response.Error = &rpcError{Code: -32602, Message: err.Error()}
				break
			}
			value, err := s.call(ctx, call)
			if err != nil {
				response.Result = complete(map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}})
			} else {
				encoded, _ := json.MarshalIndent(value, "", "  ")
				response.Result = complete(map[string]any{"isError": false, "content": []map[string]string{{"type": "text", "text": string(encoded)}}, "structuredContent": value})
			}
		default:
			response.Error = &rpcError{Code: -32601, Message: "method not found"}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func validateProtocolMeta(params json.RawMessage) error {
	var envelope struct {
		Meta protocolMeta `json:"_meta"`
	}
	if len(params) == 0 {
		return errors.New("request params must contain protocol _meta")
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return fmt.Errorf("invalid request params: %w", err)
	}
	if envelope.Meta.Version != protocolVersion {
		return fmt.Errorf("unsupported protocol version %q", envelope.Meta.Version)
	}
	if envelope.Meta.ClientCapabilities == nil {
		return errors.New("request _meta must include io.modelcontextprotocol/clientCapabilities")
	}
	return nil
}

func complete(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields)+2)
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": map[string]string{"name": "gitseq-workroom", "version": "0.1.0"}}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func tools() []map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "additionalProperties": false}
		if properties != nil {
			schema["properties"] = properties
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	stringField := map[string]string{"type": "string"}
	return []map[string]any{
		{"name": "whoami", "description": "Show the configured durable actor and ephemeral session.", "inputSchema": object(nil)},
		{"name": "presence", "description": "Show who is present in the amnesiac nexus.", "inputSchema": object(nil)},
		{"name": "status", "description": "Project durable workroom state plus a composite cursor.", "inputSchema": object(nil)},
		{"name": "wait", "description": "Long-poll after a composite cursor.", "inputSchema": object(map[string]any{"cursor": map[string]string{"type": "object"}, "timeout_ms": map[string]string{"type": "integer"}}, "cursor")},
		{"name": "say", "description": "Publish a signed ephemeral frame, opening a conversation at about when needed.", "inputSchema": object(map[string]any{"about": stringField, "text": stringField, "conversation": stringField}, "about", "text")},
		{"name": "state", "description": "Append a durable attributed utterance. Evidence values are embedded attachments. A request body addresses its performer as name, @name, or fingerprint; the signed event stores the fingerprint.", "inputSchema": object(map[string]any{"kind": stringField, "text": stringField, "body": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "rests_on": map[string]any{"type": "array", "items": stringField}, "evidence": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "idempotency_key": stringField}, "kind", "text", "rests_on")},
		{"name": "ratify", "description": "Attempt to confer force on a statement; authority is decided by the fold.", "inputSchema": object(map[string]any{"target": stringField, "idempotency_key": stringField}, "target")},
		{"name": "supersede", "description": "Attempt to retire an act and propagate staleness.", "inputSchema": object(map[string]any{"target": stringField, "text": stringField, "rests_on": map[string]any{"type": "array", "items": stringField}, "idempotency_key": stringField}, "target", "text")},
	}
}

func (s *mcpServer) call(ctx context.Context, call toolCall) (any, error) {
	switch call.Name {
	case "whoami":
		actor := s.workspace.Config.Actors[s.actor]
		return map[string]any{"actor": actor, "session": s.session, "protocol": protocolVersion}, nil
	case "presence":
		return s.get(ctx, "/v0/presence")
	case "status":
		value, err := s.get(ctx, "/v0/status")
		if isTransportError(err) {
			return s.localStatus(ctx)
		}
		return value, err
	case "wait":
		value, err := s.post(ctx, "/v0/wait", call.Arguments)
		if isTransportError(err) {
			return s.waitDurable(ctx, call.Arguments)
		}
		return value, err
	case "say":
		arguments := clone(call.Arguments)
		arguments["session"] = s.session
		return s.post(ctx, "/v0/say", arguments)
	case "state":
		kind, _ := call.Arguments["kind"].(string)
		text, _ := call.Arguments["text"].(string)
		body := stringMap(call.Arguments["body"])
		rests := stringSlice(call.Arguments["rests_on"])
		evidence := make(map[string][]byte)
		for name, content := range stringMap(call.Arguments["evidence"]) {
			evidence[name] = []byte(content)
		}
		return s.submit(ctx, workroom.SchemaState, workroom.State{Kind: workroom.Kind(kind), Text: text, Body: body}, rests, evidence, stringValue(call.Arguments["idempotency_key"]))
	case "ratify":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, workroom.SchemaRatify, workroom.Ratify{Target: target}, []string{target}, nil, stringValue(call.Arguments["idempotency_key"]))
	case "supersede":
		target := stringValue(call.Arguments["target"])
		rests := append([]string{target}, stringSlice(call.Arguments["rests_on"])...)
		return s.submit(ctx, workroom.SchemaSupersede, workroom.Supersede{Target: target, Text: stringValue(call.Arguments["text"])}, rests, nil, stringValue(call.Arguments["idempotency_key"]))
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s *mcpServer) submit(ctx context.Context, schema string, payload any, rests []string, attachments map[string][]byte, key string) (any, error) {
	_, private, err := s.workspace.Actor(s.actor)
	if err != nil {
		return nil, err
	}
	request, err := s.workspace.BuildRequest(ctx, private, s.actor, schema, payload, rests, attachments, key)
	if err != nil {
		return nil, err
	}
	value, err := s.post(ctx, "/v0/submit", request)
	if !isTransportError(err) {
		return value, err
	}
	result, err := s.workspace.Accept(ctx, request)
	if err != nil {
		return nil, err
	}
	record, err := s.workspace.Record(ctx, result.Commit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": result, "record": record, "degraded": true}, nil
}

func (s *mcpServer) announce(ctx context.Context) error {
	_, err := s.post(ctx, "/v0/presence", map[string]any{"actor": s.actor, "session": s.session, "ttl_ms": 30000})
	return err
}

func (s *mcpServer) heartbeat(ctx context.Context, errorsTo io.Writer) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.announce(ctx); err != nil {
				fmt.Fprintln(errorsTo, "gitseq-mcp: presence renewal degraded:", err)
			}
		}
	}
}

func (s *mcpServer) depart(ctx context.Context) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodDelete, s.baseURL+"/v0/presence/"+s.session, nil)
	response, err := s.client.Do(request)
	if err == nil {
		response.Body.Close()
	}
}

func (s *mcpServer) get(ctx context.Context, path string) (any, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	return s.do(request)
}

func (s *mcpServer) post(ctx context.Context, path string, value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return s.do(request)
}

func (s *mcpServer) do(request *http.Request) (any, error) {
	response, err := s.client.Do(request)
	if err != nil {
		return nil, transportError{err}
	}
	defer response.Body.Close()
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		if object, ok := value.(map[string]any); ok {
			return nil, httpStatusError{status: response.StatusCode, message: stringValue(object["error"])}
		}
		return nil, httpStatusError{status: response.StatusCode, message: "HTTP " + response.Status}
	}
	return value, nil
}

type transportError struct{ error }

func isTransportError(err error) bool {
	var target transportError
	return errors.As(err, &target)
}

type httpStatusError struct {
	status  int
	message string
}

func (e httpStatusError) Error() string { return e.message }

func (s *mcpServer) localStatus(ctx context.Context) (service.Status, error) {
	durable, err := s.workspace.Snapshot(ctx)
	if err != nil {
		return service.Status{}, err
	}
	live := nexus.Snapshot{Cursor: nexus.Cursor{Generation: "degraded"}, Presence: map[string]string{}}
	return service.Status{
		Durable: durable,
		Live:    live,
		Cursor: service.Cursor{
			Frontier: []service.Frontier{{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}},
			Live:     live.Cursor,
		},
	}, nil
}

func (s *mcpServer) waitDurable(ctx context.Context, arguments map[string]any) (service.WaitResponse, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return service.WaitResponse{}, err
	}
	var input service.WaitRequest
	if err := json.Unmarshal(encoded, &input); err != nil {
		return service.WaitResponse{}, err
	}
	timeout := time.Duration(input.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 25 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := s.localStatus(ctx)
		if err != nil {
			return service.WaitResponse{}, err
		}
		frontier := status.Cursor.Frontier[0]
		durableChanged := len(input.Cursor.Frontier) != 1 || input.Cursor.Frontier[0] != frontier
		reset := input.Cursor.Live.Generation != "" && input.Cursor.Live.Generation != "degraded"
		if durableChanged || reset {
			return service.WaitResponse{Status: status, Reset: reset}, nil
		}
		select {
		case <-ctx.Done():
			return service.WaitResponse{}, ctx.Err()
		case <-deadline.C:
			return service.WaitResponse{Status: status}, nil
		case <-ticker.C:
		}
	}
}

func clone(input map[string]any) map[string]any {
	output := make(map[string]any, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func stringMap(value any) map[string]string {
	result := make(map[string]string)
	if input, ok := value.(map[string]any); ok {
		for key, value := range input {
			if text, ok := value.(string); ok {
				result[key] = text
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func stringSlice(value any) []string {
	var result []string
	if input, ok := value.([]any); ok {
		for _, value := range input {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func randomID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gitseq-mcp:", err)
	os.Exit(1)
}
