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

// Revisions up to and including 2025-11-25 open with an `initialize`
// handshake instead of per-request metadata. Speaking both eras is what the
// spec calls a dual-era server; without it, a legacy client (Claude Code
// among them) cannot attach at all, because legacy clients have no way to
// fall forward.
const newestLegacyVersion = "2025-11-25"

var legacyVersions = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

const instructions = "Use status and wait to follow the workroom; say ephemerally and promote deliberate acts with state."

// The era is a property of the connection rather than of a single request:
// a legacy client announces itself once with `initialize` and thereafter
// omits the `_meta` a modern client repeats on every call.
type protocolEra int

const (
	eraUndetermined protocolEra = iota
	eraModern
	eraLegacy
)

// unsupportedVersionError marks the one validation failure a modern client can
// recover from, so the dispatcher can answer with the negotiable error shape.
type unsupportedVersionError struct{ requested string }

func (e *unsupportedVersionError) Error() string {
	return fmt.Sprintf("unsupported protocol version %q", e.requested)
}

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
	// Data carries the supported/requested version pair of an
	// UnsupportedProtocolVersionError; absent on every other error.
	Data any `json:"data,omitempty"`
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
	era       protocolEra
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
		// A legacy client opens with `initialize`, which predates `_meta` and so
		// cannot carry it; answering that request is what puts the connection
		// into legacy era for every request that follows.
		//
		// Initialization opens a connection, so it must come first and happen
		// once. Arriving after modern traffic, it would let a client shed the
		// per-request metadata that revision requires; arriving a second time,
		// it would renegotiate the version mid-stream and change what the
		// client has already been told. In both cases the era is settled, so
		// the request is refused without disturbing it.
		if request.Method == "initialize" {
			if s.era != eraUndetermined {
				response.Error = &rpcError{
					Code:    -32600,
					Message: "initialize must be the first request on a connection; the protocol era is already established",
				}
			} else if result, failure := s.initializeLegacy(request.Params); failure != nil {
				response.Error = failure
			} else {
				response.Result = result
			}
			if err := encoder.Encode(response); err != nil {
				return err
			}
			continue
		}
		if s.era != eraLegacy {
			if err := validateProtocolMeta(request.Params); err != nil {
				response.Error = &rpcError{Code: -32602, Message: err.Error()}
				var version *unsupportedVersionError
				if errors.As(err, &version) {
					// Name what we speak, so the client can retry at a mutually
					// supported version instead of only seeing a rejection.
					response.Error.Code = -32022
					response.Error.Message = "Unsupported protocol version"
					response.Error.Data = map[string]any{
						"supported": []string{protocolVersion, newestLegacyVersion},
						"requested": version.requested,
					}
				}
				if err := encoder.Encode(response); err != nil {
					return err
				}
				continue
			}
			s.era = eraModern
		}
		switch request.Method {
		case "ping":
			response.Result = s.result(map[string]any{})
		case "server/discover":
			// Discovery belongs to the modern revision. Answering it on a legacy
			// connection would hand back an envelope that revision has no
			// meaning for, so it is simply absent from what was negotiated.
			if s.era == eraLegacy {
				response.Error = &rpcError{
					Code:    -32601,
					Message: "server/discover is not available on protocol version " + newestLegacyVersion,
				}
				break
			}
			response.Result = complete(map[string]any{
				"supportedVersions": []string{protocolVersion},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"instructions":      instructions,
				"ttlMs":             3600000,
				"cacheScope":        "public",
			})
		case "tools/list":
			response.Result = s.result(map[string]any{"tools": tools(), "ttlMs": 3600000, "cacheScope": "public"})
		case "tools/call":
			var call toolCall
			if err := json.Unmarshal(request.Params, &call); err != nil {
				response.Error = &rpcError{Code: -32602, Message: err.Error()}
				break
			}
			value, err := s.call(ctx, call)
			if err != nil {
				response.Result = s.result(map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}})
			} else {
				// The text block is a summary, not a second copy. Restating the
				// structured payload as pretty-printed JSON doubled every
				// response while adding nothing a client could not already read.
				response.Result = s.result(map[string]any{"isError": false, "content": []map[string]string{{"type": "text", "text": summarize(call.Name, value)}}, "structuredContent": value})
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
	// An absent version is a malformed request, not a negotiation: the client
	// never named a version, so there is nothing to offer it an alternative to.
	if envelope.Meta.Version == "" {
		return errors.New("request _meta must include io.modelcontextprotocol/protocolVersion")
	}
	if envelope.Meta.Version != protocolVersion {
		return &unsupportedVersionError{requested: envelope.Meta.Version}
	}
	if envelope.Meta.ClientCapabilities == nil {
		return errors.New("request _meta must include io.modelcontextprotocol/clientCapabilities")
	}
	return nil
}

// initializeLegacy answers the pre-2026 handshake and latches the connection
// into legacy era. The reply names a version the client already knows: its own
// when we speak it, otherwise the newest legacy revision we support, which the
// client is free to refuse.
//
// The handshake is validated before it is honoured. Latching the era is a
// commitment for the rest of the connection — everything after it may omit the
// per-request metadata — so it must not be made on a request that never
// established what the client speaks. On any failure the era is left
// undetermined and the client may open again.
func (s *mcpServer) initializeLegacy(params json.RawMessage) (map[string]any, *rpcError) {
	var request struct {
		ProtocolVersion *string         `json:"protocolVersion"`
		Capabilities    *map[string]any `json:"capabilities"`
		ClientInfo      *struct {
			// The legacy revisions type clientInfo as Implementation, which
			// requires both fields; accepting a half-populated one would latch
			// the era on a handshake the client never completed.
			Name    *string `json:"name"`
			Version *string `json:"version"`
		} `json:"clientInfo"`
	}
	if len(params) == 0 {
		return nil, &rpcError{Code: -32602, Message: "initialize requires params"}
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid initialize params: " + err.Error()}
	}
	switch {
	case request.ProtocolVersion == nil || *request.ProtocolVersion == "":
		return nil, &rpcError{Code: -32602, Message: "initialize requires protocolVersion"}
	case request.Capabilities == nil:
		return nil, &rpcError{Code: -32602, Message: "initialize requires capabilities"}
	case request.ClientInfo == nil || request.ClientInfo.Name == nil || *request.ClientInfo.Name == "":
		return nil, &rpcError{Code: -32602, Message: "initialize requires clientInfo.name"}
	case request.ClientInfo.Version == nil || *request.ClientInfo.Version == "":
		return nil, &rpcError{Code: -32602, Message: "initialize requires clientInfo.version"}
	}
	version := newestLegacyVersion
	if legacyVersions[*request.ProtocolVersion] {
		version = *request.ProtocolVersion
	}
	s.era = eraLegacy
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "gitseq-workroom", "version": "0.1.0"},
		"instructions":    instructions,
	}, nil
}

// result shapes a tool-bearing reply for the connection's era. Legacy results
// carry neither the modern envelope nor its cache directives, which have no
// meaning in a revision that negotiated once at the door.
func (s *mcpServer) result(fields map[string]any) map[string]any {
	if s.era != eraLegacy {
		return complete(fields)
	}
	delete(fields, "ttlMs")
	delete(fields, "cacheScope")
	return fields
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
		snapshot, err := s.workspace.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"actor": actor, "durable": snapshot.Projection.Actors[actor.Fingerprint], "session": s.session, "protocol": protocolVersion}, nil
	case "presence":
		return s.get(ctx, "/v0/presence")
	case "status":
		// The digest is applied on both paths so that losing the resident
		// service changes what is knowable, not the shape of the answer.
		value, err := s.get(ctx, "/v0/status")
		if isTransportError(err) {
			local, localErr := s.localStatus(ctx)
			if localErr != nil {
				return nil, localErr
			}
			return s.digest(local, true), nil
		}
		if err != nil {
			return nil, err
		}
		var status service.Status
		if err := remarshal(value, &status); err != nil {
			return nil, err
		}
		return s.digest(status, false), nil
	case "wait":
		requested := requestedCursor(call.Arguments)
		value, err := s.post(ctx, "/v0/wait", call.Arguments)
		if isTransportError(err) {
			local, localErr := s.waitDurable(ctx, call.Arguments)
			if localErr != nil {
				return nil, localErr
			}
			return digestWait(local, requested, s.fingerprint(), s.actor, true), nil
		}
		if err != nil {
			return nil, err
		}
		var response service.WaitResponse
		if err := remarshal(value, &response); err != nil {
			return nil, err
		}
		return digestWait(response, requested, s.fingerprint(), s.actor, false), nil
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
		return s.submit(ctx, app.Act{Verb: app.VerbState, Kind: workroom.Kind(kind), Text: text, Body: body, RestsOn: rests, Attachments: evidence, IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	case "ratify":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	case "supersede":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, app.Act{Verb: app.VerbSupersede, Target: target, Text: stringValue(call.Arguments["text"]), RestsOn: stringSlice(call.Arguments["rests_on"]), IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s *mcpServer) submit(ctx context.Context, act app.Act) (any, error) {
	_, private, err := s.workspace.Actor(s.actor)
	if err != nil {
		return nil, err
	}
	request, err := s.workspace.BuildActRequest(ctx, private, s.actor, act)
	if err != nil {
		return nil, err
	}
	value, err := s.post(ctx, "/v0/submit", request)
	if !isTransportError(err) {
		return value, err
	}
	submission, err := s.workspace.AcceptSubmission(ctx, request)
	if err != nil {
		return nil, err
	}
	return map[string]any{"result": submission.Result, "record": submission.Record, "degraded": true}, nil
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
	return statusFromDurable(durable), nil
}

func statusFromDurable(durable app.Snapshot) service.Status {
	live := nexus.Snapshot{Cursor: nexus.Cursor{Generation: "degraded"}, Presence: map[string]string{}}
	return service.Status{
		Durable: durable,
		Live:    live,
		Cursor: service.Cursor{
			Frontier: []service.Frontier{{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}},
			Live:     live.Cursor,
		},
	}
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
	var response service.WaitResponse
	changed, err := service.Poll(ctx, input.TimeoutMS, func() (bool, error) {
		durable, err := s.workspace.Snapshot(ctx)
		if err != nil {
			return false, err
		}
		status := statusFromDurable(durable)
		reset := input.Cursor.Live.Generation != "" && input.Cursor.Live.Generation != "degraded"
		response = service.WaitResponse{Status: status, Reset: reset}
		return service.DurableChanged(input.Cursor.Frontier, durable) || reset, nil
	})
	if err != nil {
		return service.WaitResponse{}, err
	}
	if !changed {
		response.Reset = false
	}
	return response, nil
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
