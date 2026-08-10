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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/service"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
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

const (
	orientationTimeout        = 2 * time.Second
	orientationResponseLimit  = 64 << 10
	residentOrientationSource = "resident_statusview_current"
)

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

// room is one attached workroom: the repository's workspace, the resident
// service holding it when one has published itself, and whether this session
// has announced its presence there. The last two move as services come and go,
// and the presence heartbeat reads them while a call is in flight, so they are
// held behind a lock that is never kept across a request.
type room struct {
	workspace *app.Workspace

	mu        sync.Mutex
	baseURL   string
	announced bool
	// checked records that the sole-identity check has already been made for
	// this workroom. It survives a lost service, because losing an address
	// says nothing about who holds the name, and re-checking after our own
	// presence is live would refuse this session its own identity.
	checked bool
}

// endpoint names the service to use, re-reading the repository's published
// address whenever the last one is gone, so a service started or restarted
// after the adapter attached is picked up without reconnecting the client.
func (r *room) endpoint() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.baseURL == "" {
		published, ok := r.workspace.ResidentURL()
		if !ok {
			return "", false
		}
		r.baseURL = published
	}
	validated, err := validateResidentURL(r.baseURL)
	if err != nil {
		r.baseURL = ""
		r.announced = false
		return "", false
	}
	r.baseURL = validated
	return validated, true
}

// lost forgets an address that did not answer, and the presence announced
// through it, so the next call looks the service up again instead of retrying
// an address that has gone.
func (r *room) lost() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseURL = ""
	r.announced = false
}

func (r *room) joined() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.announced = true
}

func (r *room) present() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.announced
}

func (r *room) identityChecked() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checked
}

func (r *room) identityIsOurs() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checked = true
}

type mcpServer struct {
	era     protocolEra
	actor   string
	repo    string
	session string
	client  *http.Client

	roomsMu     sync.Mutex
	byPath      map[string]*room
	byCommonDir map[string]*room
}

// actorEnvironment is how a concurrent instance is told which provisioned
// identity it is, when the process is not started with an explicit --actor.
const actorEnvironment = "GITSEQ_ACTOR"

func main() {
	repo := flag.String("repo", "", "default repository for calls that do not name one (default: working directory)")
	actor := flag.String("actor", "", "configured actor; defaults to "+actorEnvironment)
	serverURL := flag.String("server", "", "retired: the resident service is read from the repository")
	flag.Parse()
	name := *actor
	if name == "" {
		name = strings.TrimSpace(os.Getenv(actorEnvironment))
	}
	if name == "" {
		fatal(errors.New("no actor identity: pass --actor, or set " + actorEnvironment + " to the identity this instance signs as"))
	}
	// The service belongs to a repository, so naming one here could only ever
	// be right for a single workroom. Registrations written before that was
	// true keep working; refusing them would strand a session over an argument
	// nothing needs.
	if *serverURL != "" {
		fmt.Fprintln(os.Stderr, "gitseq-mcp: --server is ignored; the resident service is read from the repository it serves")
	}
	server := newServer(name, *repo)
	// Attaching the default repository here is a courtesy, not a
	// precondition: presence appears before the first tool call when there is
	// a workroom to join, and one installation still serves whatever
	// repository a later call names.
	//
	// A shared identity is the exception. It is not a missing workroom to be
	// picked up later; it is this instance signing as a name another live
	// session already holds, and it must be refused at the door where the
	// operator can still fix it in one command.
	if _, err := server.attend(context.Background(), ""); err != nil {
		var shared *sharedIdentityError
		if errors.As(err, &shared) {
			fatal(err)
		}
		fmt.Fprintln(os.Stderr, "gitseq-mcp:", err)
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

func newServer(actor, repo string) *mcpServer {
	return &mcpServer{
		actor:       actor,
		repo:        absolute(repo),
		session:     "mcp:" + randomID(),
		client:      newResidentClient(),
		byPath:      map[string]*room{},
		byCommonDir: map[string]*room{},
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
	// Every tool names the workroom it acts in the same way, because the
	// adapter serves whatever repository it is pointed at rather than one
	// repository chosen when it was installed.
	repoField := map[string]string{"type": "string", "description": "Repository whose workroom this call acts in; defaults to the working directory the adapter was started in."}
	withRepo := func(properties map[string]any) map[string]any {
		fields := map[string]any{"repo": repoField}
		for name, schema := range properties {
			fields[name] = schema
		}
		return fields
	}
	return []map[string]any{
		{"name": "whoami", "description": "Show the configured durable actor and ephemeral session.", "inputSchema": object(withRepo(nil))},
		{"name": "presence", "description": "Show who is present in the amnesiac nexus.", "inputSchema": object(withRepo(nil))},
		{"name": "status", "description": "Project durable workroom state plus a composite cursor.", "inputSchema": object(withRepo(nil))},
		{"name": "wait", "description": "Long-poll after a composite cursor.", "inputSchema": object(withRepo(map[string]any{"cursor": map[string]string{"type": "object"}, "timeout_ms": map[string]string{"type": "integer"}}), "cursor")},
		{"name": "say", "description": "Publish a signed ephemeral frame, opening a conversation at about when needed.", "inputSchema": object(withRepo(map[string]any{"about": stringField, "text": stringField, "conversation": stringField}), "about", "text")},
		{"name": "state", "description": "Append a durable attributed utterance. Evidence values are embedded attachments. A request body addresses its performer as name, @name, or fingerprint; the signed event stores the fingerprint.", "inputSchema": object(withRepo(map[string]any{"kind": stringField, "text": stringField, "body": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "rests_on": map[string]any{"type": "array", "items": stringField}, "evidence": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "idempotency_key": stringField}), "kind", "text", "rests_on")},
		{"name": "ratify", "description": "Attempt to confer force on a statement; authority is decided by the fold.", "inputSchema": object(withRepo(map[string]any{"target": stringField, "idempotency_key": stringField}), "target")},
		{"name": "supersede", "description": "Attempt to retire an act and propagate staleness.", "inputSchema": object(withRepo(map[string]any{"target": stringField, "text": stringField, "rests_on": map[string]any{"type": "array", "items": stringField}, "idempotency_key": stringField}), "target", "text")},
	}
}

// absolute names a repository the same way every time, so two calls that mean
// the same checkout share one attachment and an error can say which directory
// was actually looked in.
func absolute(repo string) string {
	if strings.TrimSpace(repo) == "" {
		repo = "."
	}
	resolved, err := filepath.Abs(repo)
	if err != nil {
		return repo
	}
	return resolved
}

// attend attaches the named repository, or the default one when a call does
// not name it, and announces this session there. Attachment is per repository
// rather than per process: one installation serves whatever repository a call
// names, and the resident service is read from that repository, so an act can
// never be posted to a service holding a different workroom.
func (s *mcpServer) attend(ctx context.Context, repo string) (*room, error) {
	current, err := s.attach(ctx, repo)
	if err != nil {
		return nil, err
	}
	if !current.identityChecked() {
		if err := s.requireSoleIdentity(ctx, current); err != nil {
			return nil, err
		}
		current.identityIsOurs()
	}
	if !current.present() {
		if err := s.announce(ctx, current); err == nil {
			current.joined()
		}
	}
	return current, nil
}

func (s *mcpServer) attach(ctx context.Context, repo string) (*room, error) {
	path := s.repo
	if strings.TrimSpace(repo) != "" {
		path = absolute(repo)
	}
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if existing, ok := s.byPath[path]; ok {
		return existing, nil
	}
	workspace, err := app.Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("no gitseq workroom for %s: %w", path, err)
	}
	if _, _, err := workspace.Actor(s.actor); err != nil {
		return nil, fmt.Errorf("actor %q cannot act in the workroom for %s: %w", s.actor, path, err)
	}
	// A linked worktree is a checkout of a repository, not a second workroom:
	// two paths that share a common git directory share one attachment and one
	// cached projection.
	current, ok := s.byCommonDir[workspace.CommonDir]
	if !ok {
		current = &room{workspace: workspace}
		s.byCommonDir[workspace.CommonDir] = current
	}
	s.byPath[path] = current
	return current, nil
}

func (s *mcpServer) call(ctx context.Context, call toolCall) (any, error) {
	var current *room
	var err error
	// Whoami's resident read has an explicit two-second bound. Attaching the
	// selected repository is local; announcing presence is an independent live
	// side effect and must not move outside that bound by stalling first.
	if call.Name == "whoami" {
		current, err = s.attach(ctx, stringValue(call.Arguments["repo"]))
	} else {
		current, err = s.attend(ctx, stringValue(call.Arguments["repo"]))
	}
	if err != nil {
		return nil, err
	}
	switch call.Name {
	case "whoami":
		return s.whoami(ctx, current)
	case "presence":
		return s.get(ctx, current, "/v0/presence")
	case "status":
		// The digest is applied on both paths so that losing the resident
		// service changes what is knowable, not the shape of the answer.
		value, err := s.get(ctx, current, "/v0/status")
		if isTransportError(err) {
			local, localErr := s.localStatus(ctx, current)
			if localErr != nil {
				return nil, localErr
			}
			return s.digest(current, local, true), nil
		}
		if err != nil {
			return nil, err
		}
		var status service.Status
		if err := remarshal(value, &status); err != nil {
			return nil, err
		}
		return s.digest(current, status, false), nil
	case "wait":
		requested := requestedCursor(call.Arguments)
		value, err := s.post(ctx, current, "/v0/wait", call.Arguments)
		if isTransportError(err) {
			local, localErr := s.waitDurable(ctx, current, call.Arguments)
			if localErr != nil {
				return nil, localErr
			}
			return digestWait(local, requested, s.fingerprint(current), s.actor, true), nil
		}
		if err != nil {
			return nil, err
		}
		var response service.WaitResponse
		if err := remarshal(value, &response); err != nil {
			return nil, err
		}
		return digestWait(response, requested, s.fingerprint(current), s.actor, false), nil
	case "say":
		arguments := clone(call.Arguments)
		arguments["session"] = s.session
		return s.post(ctx, current, "/v0/say", arguments)
	case "state":
		kind, _ := call.Arguments["kind"].(string)
		text, _ := call.Arguments["text"].(string)
		body := stringMap(call.Arguments["body"])
		rests := stringSlice(call.Arguments["rests_on"])
		evidence := make(map[string][]byte)
		for name, content := range stringMap(call.Arguments["evidence"]) {
			evidence[name] = []byte(content)
		}
		return s.submit(ctx, current, app.Act{Verb: app.VerbState, Kind: workroom.Kind(kind), Text: text, Body: body, RestsOn: rests, Attachments: evidence, IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	case "ratify":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, current, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	case "supersede":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, current, app.Act{Verb: app.VerbSupersede, Target: target, Text: stringValue(call.Arguments["text"]), RestsOn: stringSlice(call.Arguments["rests_on"]), IdempotencyKey: stringValue(call.Arguments["idempotency_key"])})
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s *mcpServer) whoami(ctx context.Context, current *room) (any, error) {
	actor := current.workspace.Config.Actors[s.actor]
	residentContext, cancel := context.WithTimeout(ctx, orientationTimeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		orientation, err := s.currentResidentOrientation(residentContext, current, actor.Fingerprint)
		if err == nil {
			return map[string]any{
				"actor": publicActor(actor), "durable": orientation.You, "session": s.session, "protocol": protocolVersion,
				"repo": current.workspace.CommonDir, "genesis": current.workspace.Config.Genesis,
				"frontier": orientation.Frontier, "source": residentOrientationSource, "degraded": false,
			}, nil
		}
		if residentContext.Err() != nil {
			break
		}
	}

	local, err := current.workspace.SnapshotWithSource(ctx)
	if err != nil {
		return nil, err
	}
	orientation, ok := statusview.BuildOrientation(local.Snapshot, actor.Fingerprint, actor.Name)
	if !ok {
		return nil, errors.New("configured actor is not in the effective durable roster")
	}
	return map[string]any{
		"actor": publicActor(actor), "durable": orientation.You, "session": s.session, "protocol": protocolVersion,
		"repo": current.workspace.CommonDir, "genesis": current.workspace.Config.Genesis,
		"frontier": orientation.Frontier, "source": string(local.Source), "degraded": true,
	}, nil
}

func (s *mcpServer) currentResidentOrientation(ctx context.Context, current *room, fingerprint string) (service.Orientation, error) {
	before, err := current.workspace.Store.Head(ctx, kernel.Ref(current.workspace.Config.Genesis))
	if err != nil {
		return service.Orientation{}, err
	}
	var orientation service.Orientation
	if err := s.getBoundedJSON(ctx, current, "/v0/orientation/"+fingerprint, orientationResponseLimit, &orientation); err != nil {
		return service.Orientation{}, err
	}
	after, err := current.workspace.Store.Head(ctx, kernel.Ref(current.workspace.Config.Genesis))
	if err != nil {
		return service.Orientation{}, err
	}
	if before != after {
		return service.Orientation{}, errors.New("workroom head moved while resident orientation was read")
	}
	if orientation.ProjectionVersion != service.OrientationProjectionVersion ||
		orientation.Frontier.Genesis != current.workspace.Config.Genesis || orientation.Frontier.Head != after ||
		orientation.Frontier.Depth < 0 || orientation.You.Fingerprint != fingerprint ||
		orientation.You.Name == "" || orientation.You.Kind == "" || orientation.You.MembershipEvent == "" ||
		len(orientation.You.Roles) > statusview.ListCap || orientation.You.RolesSkipped < 0 ||
		!containsString(orientation.You.Roles, "participant") {
		return service.Orientation{}, errors.New("resident orientation does not match local durable evidence")
	}
	return orientation, nil
}

func publicActor(actor app.Actor) map[string]string {
	return map[string]string{"name": actor.Name, "fingerprint": actor.Fingerprint}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *mcpServer) submit(ctx context.Context, current *room, act app.Act) (any, error) {
	_, private, err := current.workspace.Actor(s.actor)
	if err != nil {
		return nil, err
	}
	request, err := current.workspace.BuildActRequest(ctx, private, s.actor, act)
	if err != nil {
		return nil, err
	}
	value, err := s.post(ctx, current, "/v0/submit", request)
	if !isTransportError(err) {
		if err != nil {
			return value, err
		}
		return s.withKindWarning(ctx, current, act, value), nil
	}
	submission, err := current.workspace.AcceptSubmission(ctx, request)
	if err != nil {
		return nil, err
	}
	return s.withKindWarning(ctx, current, act, map[string]any{"result": submission.Result, "record": submission.Record, "degraded": true}), nil
}

// withKindWarning puts the undefined-kind warning inside the tool result the
// caller reads. An agent writing over MCP is exactly who filed a promise under
// a kind this room does not define and was told nothing, so a line in a log it
// never reads would be no warning at all. The act itself is untouched: it
// landed, and it still projects as undefined-kind.
func (s *mcpServer) withKindWarning(ctx context.Context, current *room, act app.Act, value any) any {
	if act.Verb != app.VerbState {
		return value
	}
	result, ok := value.(map[string]any)
	if !ok {
		return value
	}
	snapshot, err := current.workspace.Snapshot(ctx)
	if err != nil {
		result["warning"] = fmt.Sprintf("cannot tell whether kind %q is defined here: %v", act.Kind, err)
		return result
	}
	if warning := snapshot.Vocabulary.UndefinedKindWarning(act.Kind); warning != "" {
		result["warning"] = warning
	}
	return result
}

// sharedIdentityError marks the one attachment failure that must stop the
// process rather than be reported and retried: another live session already
// signs as this instance's name.
type sharedIdentityError struct{ message string }

func (e *sharedIdentityError) Error() string { return e.message }

// requireSoleIdentity refuses to attach a second live session to one durable
// identity. Concurrent instances signing as one name make the log say that a
// name did something when one of several instances did, and they satisfy the
// different-agent review rule by spelling rather than by fingerprint. Sharing
// the name is the path of least resistance, so it is made an error at the door
// where the operator can still fix it in one command.
//
// The check reads live presence, so it is exactly as good as the resident
// service. When presence cannot be read the instance still starts: a stopped
// service must not stop the work. That degradation is printed, not assumed
// away, and it is the stated limit of this guard along with the race between
// two instances starting at the same moment, which neither will see.
//
// It is made once per workroom, before this session announces itself there.
// Afterwards our own presence is in the snapshot, so asking again would refuse
// this session the identity it already holds.
func (s *mcpServer) requireSoleIdentity(ctx context.Context, current *room) error {
	actor := current.workspace.Config.Actors[s.actor]
	value, err := s.get(ctx, current, "/v0/presence")
	if isTransportError(err) {
		fmt.Fprintln(os.Stderr, "gitseq-mcp: shared-identity check skipped; the resident service is unavailable:", err)
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot nexus.Snapshot
	if err := remarshal(value, &snapshot); err != nil {
		return err
	}
	label := actor.Name + " (" + actor.Fingerprint[:12] + ")"
	held := 0
	for _, present := range snapshot.Presence {
		if present == label {
			held++
		}
	}
	if held == 0 {
		return nil
	}
	return &sharedIdentityError{message: fmt.Sprintf("identity %q is already live in %d other session(s); concurrent instances must not share one key. Provision an instance identity — gs actor-add --as <operator> --name %s.2 — and start this instance with %s=%s.2",
		actor.Name, held, actor.Name, actorEnvironment, actor.Name)}
}

func (s *mcpServer) announce(ctx context.Context, current *room) error {
	_, err := s.post(ctx, current, "/v0/presence", map[string]any{"actor": s.actor, "session": s.session, "ttl_ms": 30000})
	if err == nil {
		// Presence carries a name, not a session, so once ours is live the
		// snapshot can no longer tell this instance from a stranger. The door
		// check is closed for this workroom rather than left to refuse us our
		// own identity later.
		current.identityIsOurs()
	}
	return err
}

// attended lists the workrooms this session has joined. Presence is a property
// of a workroom, so a session that has acted in two of them must be renewed
// and withdrawn in both.
func (s *mcpServer) attended() []*room {
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	rooms := make([]*room, 0, len(s.byCommonDir))
	for _, current := range s.byCommonDir {
		if current.present() {
			rooms = append(rooms, current)
		}
	}
	return rooms
}

func (s *mcpServer) heartbeat(ctx context.Context, errorsTo io.Writer) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, current := range s.attended() {
				if err := s.announce(ctx, current); err != nil {
					fmt.Fprintln(errorsTo, "gitseq-mcp: presence renewal degraded:", err)
				}
			}
		}
	}
}

func (s *mcpServer) depart(ctx context.Context) {
	for _, current := range s.attended() {
		base, ok := current.endpoint()
		if !ok {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/v0/presence/"+s.session, nil)
		if err != nil {
			continue
		}
		response, err := s.client.Do(request)
		if err == nil {
			response.Body.Close()
		}
	}
}

// errNoResident is the absence of a resident service, reported as a transport
// failure so that every caller already written to survive an unreachable
// service also survives one that was never started.
var errNoResident = errors.New("no resident service is published for this workroom; run `gs serve` in it for presence and conversation")

func (s *mcpServer) get(ctx context.Context, current *room, path string) (any, error) {
	base, ok := current.endpoint()
	if !ok {
		return nil, transportError{errNoResident}
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	return s.do(current, request)
}

func (s *mcpServer) post(ctx context.Context, current *room, path string, value any) (any, error) {
	base, ok := current.endpoint()
	if !ok {
		return nil, transportError{errNoResident}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return s.do(current, request)
}

func (s *mcpServer) getBoundedJSON(ctx context.Context, current *room, path string, limit int64, target any) error {
	base, ok := current.endpoint()
	if !ok {
		return transportError{errNoResident}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		current.lost()
		return transportError{err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("resident returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("resident response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("resident returned trailing JSON")
	}
	return nil
}

func (s *mcpServer) do(current *room, request *http.Request) (any, error) {
	response, err := s.client.Do(request)
	if err != nil {
		current.lost()
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

func validateResidentURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("resident service must be an http loopback URL without credentials")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("resident service must name a loopback address")
		}
	}
	return strings.TrimRight(raw, "/"), nil
}

func newResidentClient() *http.Client {
	return &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("resident redirects are not allowed")
	}}
}

func (s *mcpServer) localStatus(ctx context.Context, current *room) (service.Status, error) {
	durable, err := current.workspace.Snapshot(ctx)
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

func (s *mcpServer) waitDurable(ctx context.Context, current *room, arguments map[string]any) (service.WaitResponse, error) {
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
		durable, err := current.workspace.Snapshot(ctx)
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
