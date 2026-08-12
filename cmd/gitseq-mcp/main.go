package main

import (
	"bufio"
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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
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

const instructions = "Use status once and wait to follow the workroom; use work and inspect for selective durable follow-up; say ephemerally and promote deliberate acts with state."

const (
	orientationTimeout        = 2 * time.Second
	orientationResponseLimit  = 64 << 10
	workResponseLimit         = 256 << 10
	inspectionResponseLimit   = 2 << 20
	residentResponseLimit     = 64 << 20
	residentCallTimeout       = 10 * time.Second
	residentWaitTimeout       = 35 * time.Second
	residentShutdownTimeout   = 2 * time.Second
	residentHTTPTimeout       = 40 * time.Second
	residentOrientationSource = "resident_statusview_current"
)

type residentDeadlinePolicy struct {
	call     time.Duration
	wait     time.Duration
	shutdown time.Duration
}

var defaultResidentDeadlines = residentDeadlinePolicy{
	call:     residentCallTimeout,
	wait:     residentWaitTimeout,
	shutdown: residentShutdownTimeout,
}

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
	inbox     bool
	// identityNoticeChecked records that this process has already looked for
	// other live sessions using its actor identity in this workroom. The check
	// runs before this session announces itself; repeating it afterwards would
	// count this session in its own warning.
	identityNoticeChecked bool
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
	validated, err := residentclient.ValidateURL(r.baseURL)
	if err != nil {
		r.baseURL = ""
		r.announced = false
		r.inbox = false
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
	r.inbox = false
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

func (r *room) setInboxAvailable(available bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inbox = available
}

func (r *room) inboxAvailable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inbox
}

func (r *room) sharedIdentityNoticeChecked() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.identityNoticeChecked
}

func (r *room) markSharedIdentityNoticeChecked() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identityNoticeChecked = true
}

type mcpServer struct {
	era       protocolEra
	actor     string
	repo      string
	session   string
	client    *residentclient.Client
	notices   io.Writer
	deadlines residentDeadlinePolicy

	roomsMu     sync.Mutex
	byPath      map[string]*room
	byCommonDir map[string]*room
}

// actorEnvironment is how a concurrent instance is told which provisioned
// identity it is, when the process is not started with an explicit --actor.
const actorEnvironment = residentclient.ActorEnvironment

func main() {
	repo := flag.String("repo", "", "default repository for calls that do not name one (default: working directory)")
	actor := flag.String("actor", "", "configured actor; defaults to "+actorEnvironment)
	serverURL := flag.String("server", "", "retired: the resident service is read from the repository")
	flag.Parse()
	name, err := residentclient.ResolveActor("--actor", *actor)
	if err != nil {
		fatal(err)
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
	if _, err := server.attend(context.Background(), ""); err != nil {
		fmt.Fprintln(os.Stderr, "gitseq-mcp:", err)
	}
	presenceContext, stopPresence := context.WithCancel(context.Background())
	go server.heartbeat(presenceContext, os.Stderr)
	defer func() {
		stopPresence()
		server.shutdown()
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
		notices:     os.Stderr,
		deadlines:   defaultResidentDeadlines,
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
			value, acted, err := s.call(ctx, call)
			// Attention rides beside the result on both paths. A failed tool
			// call is exactly when a caller most needs to know that somebody
			// addressed them or is looking at the same event, and the durable
			// outcome above is already decided either way.
			// The room the call actually acted in, never a room chosen after
			// the fact. When selection itself failed there is no room, and the
			// adjunct reports unavailable rather than guessing at one.
			attention := s.liveAttention(ctx, acted, call, value)
			text := attentionSummary(attention)
			if err != nil {
				content := []map[string]string{{"type": "text", "text": err.Error()}}
				if text != "" {
					content = append(content, map[string]string{"type": "text", "text": text})
				}
				response.Result = s.result(map[string]any{"isError": true, "content": content, "live_attention": attention})
			} else {
				// The text block is a summary, not a second copy. Restating the
				// structured payload as pretty-printed JSON doubled every
				// response while adding nothing a client could not already read.
				content := []map[string]string{{"type": "text", "text": summarize(call.Name, value)}}
				if text != "" {
					content = append(content, map[string]string{"type": "text", "text": text})
				}
				// live_attention is a sibling of structuredContent, not a wrapper
				// around it. structuredContent stays exactly the tool's own
				// payload: notes/2026-08-07-bootstrap-task-cycle.md documents a
				// consumer that reads it directly, and an adjunct that rewrote
				// the envelope would break every such reader to deliver news
				// they did not ask for.
				response.Result = s.result(map[string]any{"isError": false, "content": content, "structuredContent": value, "live_attention": attention})
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
	enum := func(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }
	arrayOf := func(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
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
		{"name": "presence", "description": "Inspect leased presence, or update this session's advisory activity. Focus does not claim or complete work.", "inputSchema": object(withRepo(map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"available", "busy", "waiting", "blocked"}},
			"focus":  map[string]any{"type": "array", "items": stringField, "maxItems": nexus.MaxFocusEvents},
			"note":   map[string]any{"type": "string", "maxLength": nexus.MaxActivityNoteBytes},
		}))},
		{"name": "status", "description": "Project durable work and this session's priority ephemeral chat; available_to_you contains open unclaimed requests addressed to this actor.", "inputSchema": object(withRepo(nil))},
		{"name": "wait", "description": "Long-poll after a composite cursor; repeats unacknowledged priority ephemeral chat until ack is called.", "inputSchema": object(withRepo(map[string]any{"cursor": map[string]string{"type": "object"}, "timeout_ms": map[string]string{"type": "integer"}}), "cursor")},
		{"name": "work", "description": "Query the current actor's durable work through a bounded resident-side projection. Defaults include addressed unclaimed work and stale commitments.", "inputSchema": object(withRepo(map[string]any{
			"lanes":    arrayOf(enum("available_to_you", "waiting_on_you", "you_are_waiting_on", "not_actionable")),
			"statuses": arrayOf(enum("open", "promised", "reported", "satisfied", "stale", "cancelled", "reneged", "withdrawn")),
			"stale":    enum("include", "only", "exclude"),
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": statusview.WorkPageMax},
			"cursor":   stringField,
		}))},
		{"name": "inspect", "description": "Inspect one exact canonical durable event with its decision, commitment chain, direct provenance, and related review artifacts.", "inputSchema": object(withRepo(map[string]any{"event": stringField}), "event")},
		{"name": "say", "description": "Publish signed ephemeral chat. Unique @name mentions and exact replies address live recipient sessions for priority delivery.", "inputSchema": object(withRepo(map[string]any{"about": stringField, "text": stringField, "conversation": stringField, "re": stringField}), "about", "text")},
		{"name": "ack", "description": "Acknowledge exact priority-chat thread handles for this leased session. This is not a durable read receipt.", "inputSchema": object(withRepo(map[string]any{"threads": map[string]any{"type": "array", "items": stringField, "maxItems": nexus.MaxInboxFrames}}), "threads")},
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
	if !current.sharedIdentityNoticeChecked() {
		if err := s.warnSharedIdentity(ctx, current); err != nil {
			return nil, err
		}
		current.markSharedIdentityNoticeChecked()
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

// call selects the room this invocation acts in, then dispatches. It returns
// that exact room alongside the result, including on error paths, because the
// attention adjunct must read the room the call used and no other.
//
// Choosing a room after the fact is the bug this shape prevents. A caller that
// passes arguments.repo acts in a workroom that need not be the adapter
// default, and an adjunct that resolved "the default" or "the only attachment"
// could hand back another repository's addressed inbox and leased focus. That
// is a disclosure across a boundary the caller drew deliberately.
func (s *mcpServer) call(ctx context.Context, call toolCall) (any, *room, error) {
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
		return nil, nil, err
	}
	value, err := s.dispatch(ctx, call, current)
	return value, current, err
}

func (s *mcpServer) dispatch(ctx context.Context, call toolCall, current *room) (any, error) {
	switch call.Name {
	case "whoami":
		return s.whoami(ctx, current)
	case "presence":
		update := map[string]any{"actor": s.actor, "session": s.session, "ttl_ms": 30000}
		for _, field := range []string{"status", "focus", "note"} {
			if value, present := call.Arguments[field]; present {
				update[field] = value
			}
		}
		own, err := s.post(ctx, current, "/v0/presence", update)
		if err != nil {
			return nil, err
		}
		value, err := s.get(ctx, current, "/v0/presence")
		if err != nil {
			return nil, err
		}
		live, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("resident presence response is not an object")
		}
		live["own"] = own
		return live, nil
	case "status":
		// The digest is applied on both paths so that losing the resident
		// service changes what is knowable, not the shape of the answer.
		value, err := s.postForSession(ctx, current, "/v0/status", map[string]any{"session": s.session})
		if isTransportError(err) || inboxProtocolUnavailable(err) {
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
		arguments := residentArguments(call.Arguments)
		arguments["session"] = s.session
		requested := requestedCursor(arguments)
		value, err := s.postForSession(ctx, current, "/v0/wait", arguments)
		if isTransportError(err) || inboxProtocolUnavailable(err) {
			local, localErr := s.waitDurable(ctx, current, arguments)
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
	case "work":
		var input statusview.WorkQuery
		arguments := clone(call.Arguments)
		delete(arguments, "repo")
		if err := remarshal(arguments, &input); err != nil {
			return nil, err
		}
		input.Actor = s.fingerprint(current)
		var page statusview.WorkPage
		if err := s.postBoundedJSON(ctx, current, "/v0/work-query", input, workResponseLimit, &page); err != nil {
			if !isTransportError(err) {
				return nil, err
			}
			durable, localErr := current.workspace.Snapshot(ctx)
			if localErr != nil {
				return nil, localErr
			}
			return statusview.BuildWorkPage(durable, input, true)
		}
		return page, nil
	case "inspect":
		input := statusview.InspectRequest{Event: stringValue(call.Arguments["event"])}
		var inspection statusview.ItemInspection
		if err := s.postBoundedJSON(ctx, current, "/v0/inspect", input, inspectionResponseLimit, &inspection); err != nil {
			if !isTransportError(err) {
				return nil, err
			}
			durable, localErr := current.workspace.Snapshot(ctx)
			if localErr != nil {
				return nil, localErr
			}
			return statusview.BuildItemInspection(durable, input.Event, true)
		}
		return inspection, nil
	case "say":
		arguments := residentArguments(call.Arguments)
		arguments["session"] = s.session
		if sayNeedsInbox(arguments) {
			if !current.inboxAvailable() {
				return nil, errors.New("addressed chat is unavailable until the resident supports gitseq.addressed-inbox.v1")
			}
			// The version travels with the mutation as well as registration. If
			// a new resident is replaced by an old binary at the same URL between
			// calls, that binary's strict decoder refuses the retry instead of
			// accepting addressed text as opaque chat.
			arguments["inbox_version"] = service.InboxProtocolVersion
		}
		return s.postForSession(ctx, current, "/v0/say", arguments)
	case "ack":
		if !current.inboxAvailable() {
			return nil, errors.New("priority chat acknowledgement is unavailable until the resident supports gitseq.addressed-inbox.v1")
		}
		return s.postForSession(ctx, current, "/v0/inbox/ack", map[string]any{"session": s.session, "threads": stringSlice(call.Arguments["threads"])})
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
	base, available := current.endpoint()
	if available {
		requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor("/v0/submit"))
		submission, err := s.client.Submit(requestContext, current.workspace, base, request)
		cancel()
		if err == nil {
			value := map[string]any{"result": submission.Result, "record": submission.Record}
			return s.withKindWarning(ctx, current, act, value), nil
		}
		err = residentClientError(current, err)
		if !isTransportError(err) {
			return nil, err
		}
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
	if warning := residentclient.UndefinedKindWarning(ctx, current.workspace, act.Kind); warning != "" {
		result["warning"] = warning
	}
	return result
}

// warnSharedIdentity tells an operator when this actor identity already has
// live sessions. That is not an attribution failure: durable records identify
// the actor fingerprint, not a process, and review independence is enforced by
// fingerprint. Sessions using one fingerprint therefore cannot review each
// other independently. They can still race on claims and leased presence, so
// the adapter makes that coordination risk visible without refusing legitimate
// multi-device, planner/worker, or crash-restart use.
//
// The check reads live presence, so it is exactly as good as the resident
// service. When presence cannot be read the instance still starts: a stopped
// service must not stop the work. That degradation is printed, not assumed
// away, and it is the stated limit of this guard along with the race between
// two instances starting at the same moment, which neither will see.
//
// It is made once per workroom, before this session announces itself there.
func (s *mcpServer) warnSharedIdentity(ctx context.Context, current *room) error {
	actor := current.workspace.Config.Actors[s.actor]
	value, err := s.get(ctx, current, "/v0/presence-count?actor="+url.QueryEscape(s.actor))
	if isTransportError(err) || sharedIdentityCountUnavailable(err) {
		fmt.Fprintln(s.noticeWriter(), "gitseq-mcp: shared-identity check skipped; the resident service is unavailable:", err)
		return nil
	}
	if err != nil {
		return err
	}
	var count struct {
		Count int `json:"count"`
	}
	if err := remarshal(value, &count); err != nil {
		return err
	}
	if count.Count == 0 {
		return nil
	}
	fmt.Fprintf(s.noticeWriter(), "gitseq-mcp: warning: identity %q is live in %d other session(s); reviews between sessions of this identity carry no independent force, and concurrent sessions may race on claims and presence\n", actor.Name, count.Count)
	return nil
}

func (s *mcpServer) noticeWriter() io.Writer {
	if s.notices != nil {
		return s.notices
	}
	return os.Stderr
}

func (s *mcpServer) announce(ctx context.Context, current *room) error {
	_, err := s.post(ctx, current, "/v0/presence", map[string]any{"actor": s.actor, "session": s.session, "ttl_ms": 30000})
	if err != nil {
		return err
	}
	current.markSharedIdentityNoticeChecked()
	_, err = s.post(ctx, current, "/v0/inbox/register", map[string]any{"session": s.session, "version": service.InboxProtocolVersion})
	if inboxProtocolUnavailable(err) {
		current.setInboxAvailable(false)
		return nil
	}
	if err != nil {
		return err
	}
	current.setInboxAvailable(true)
	return nil
}

func sayNeedsInbox(arguments map[string]any) bool {
	return stringValue(arguments["re"]) != "" || service.HasMentionToken(stringValue(arguments["text"]))
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

// shutdown gives departure its own whole-operation bound. It intentionally
// does not inherit the serving context: that context is cancelled when the
// client disconnects, while the resident still needs one bounded chance to
// remove this session from every room it attended.
func (s *mcpServer) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), s.deadlineForShutdown())
	defer cancel()
	s.depart(ctx)
}

func (s *mcpServer) depart(ctx context.Context) {
	for _, current := range s.attended() {
		base, ok := current.endpoint()
		if !ok {
			continue
		}
		requestContext, cancel := context.WithTimeout(ctx, s.deadlineForShutdown())
		_ = s.client.Delete(requestContext, base, "/v0/presence/"+s.session)
		cancel()
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
	requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor(path))
	defer cancel()
	value, err := s.client.GetValue(requestContext, base, path, residentResponseLimit)
	return value, residentClientError(current, err)
}

func (s *mcpServer) post(ctx context.Context, current *room, path string, value any) (any, error) {
	base, ok := current.endpoint()
	if !ok {
		return nil, transportError{errNoResident}
	}
	requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor(path))
	defer cancel()
	result, err := s.client.PostValue(requestContext, base, path, value, residentResponseLimit)
	return result, residentClientError(current, err)
}

// postForSession repairs the one honest resident-restart race: this adapter
// may remember a lease from the previous process generation. Re-announcing
// the same private session once restores only ephemeral state; durable work is
// never retried through this path.
func (s *mcpServer) postForSession(ctx context.Context, current *room, path string, value any) (any, error) {
	result, err := s.post(ctx, current, path, value)
	var refusal *residentclient.HTTPError
	if !errors.As(err, &refusal) || refusal.Message != "session is not present" {
		return result, err
	}
	if err := s.announce(ctx, current); err != nil {
		return nil, err
	}
	return s.post(ctx, current, path, value)
}

func (s *mcpServer) postBoundedJSON(ctx context.Context, current *room, path string, value any, limit int64, target any) error {
	base, ok := current.endpoint()
	if !ok {
		return transportError{errNoResident}
	}
	requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor(path))
	defer cancel()
	err := s.client.PostJSON(requestContext, base, path, value, limit, target)
	return residentClientError(current, err)
}

func (s *mcpServer) getBoundedJSON(ctx context.Context, current *room, path string, limit int64, target any) error {
	base, ok := current.endpoint()
	if !ok {
		return transportError{errNoResident}
	}
	requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor(path))
	defer cancel()
	err := s.client.GetJSON(requestContext, base, path, limit, target)
	return residentClientError(current, err)
}

type transportError struct{ error }

func (e transportError) Unwrap() error { return e.error }

type residentTimeoutError struct{ error }

func (e residentTimeoutError) Error() string {
	return "resident request timed out: " + e.error.Error()
}

func (e residentTimeoutError) Unwrap() error { return e.error }

func residentTransportError(err error) error {
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return transportError{residentTimeoutError{err}}
	}
	return transportError{err}
}

func residentClientError(current *room, err error) error {
	if err == nil {
		return nil
	}
	if residentclient.IsTransportError(err) || (residentclient.IsReadError(err) &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))) {
		current.lost()
		return residentTransportError(err)
	}
	return err
}

func isTransportError(err error) bool {
	var target transportError
	return errors.As(err, &target)
}

func inboxProtocolUnavailable(err error) bool {
	var refusal *residentclient.HTTPError
	if !errors.As(err, &refusal) {
		return false
	}
	return refusal.StatusCode == http.StatusMethodNotAllowed || refusal.StatusCode == http.StatusNotFound ||
		strings.Contains(refusal.Message, `unknown field "session"`)
}

func sharedIdentityCountUnavailable(err error) bool {
	var refusal *residentclient.HTTPError
	return errors.As(err, &refusal) && (refusal.StatusCode == http.StatusMethodNotAllowed || refusal.StatusCode == http.StatusNotFound)
}

func (s *mcpServer) deadlineFor(path string) time.Duration {
	deadlines := s.deadlines
	if deadlines.call <= 0 || deadlines.wait <= 0 || deadlines.shutdown <= 0 {
		deadlines = defaultResidentDeadlines
	}
	if path == "/v0/wait" {
		return deadlines.wait
	}
	return deadlines.call
}

func (s *mcpServer) deadlineForShutdown() time.Duration {
	if s.deadlines.shutdown > 0 {
		return s.deadlines.shutdown
	}
	return defaultResidentDeadlines.shutdown
}

func validateResidentURL(raw string) (string, error) {
	return residentclient.ValidateURL(raw)
}

func newResidentClient() *residentclient.Client {
	return residentclient.New(residentHTTPTimeout)
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

// repo selects the adapter attachment. It is not part of any resident service
// request, whose strict decoders accept only the endpoint's own wire fields.
func residentArguments(input map[string]any) map[string]any {
	output := clone(input)
	delete(output, "repo")
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

// eventIDPattern matches a canonical durable event identifier exactly:
// genesis and event, both full hashes. Matching this and nothing else is what
// keeps the attention adjunct an observation rather than a guess. A looser
// pattern — a bare hash, a prefix, a word that looks like a reference — would
// let the adapter assert a relationship nobody stated.
// The surrounding bytes matter as much as the identifier. Without boundaries
// this pattern happily extracts a canonical-looking identifier out of the
// middle of a longer token — "xgit:sha1:<40>#git:sha1:<40>y" would yield one —
// and the adjunct would then report actors focused on an event the caller
// never named. The documentation promises exact canonical identifiers and no
// inference, so the match must fail on any adjacent identifier byte.
//
// Both boundaries test the same class, and that symmetry is the whole point.
// An earlier form excluded alphanumerics, colon and hash on the left but only
// hex on the right, which reads as a boundary and is not one: a trailing "z"
// left the canonical prefix extractable out of a longer token. What makes a
// byte a boundary is that it cannot belong to an identifier, and that question
// has one answer regardless of which end is being asked.
const eventIDByte = `0-9a-zA-Z:#`

var eventIDPattern = regexp.MustCompile(
	`(^|[^` + eventIDByte + `])(git:sha1:[0-9a-f]{40}#git:sha1:[0-9a-f]{40})($|[^` + eventIDByte + `])`)

// maxAttentionEvents bounds what one call asks about. A tool that returns a
// whole projection names thousands of events; asking about all of them would
// turn an adjunct into the most expensive part of the call.
const maxAttentionEvents = 32

// attentionEvents collects the durable identifiers this call named or returned.
// Both directions matter: asking about an event and being handed one are the
// two ways a caller comes to be looking at it.
func attentionEvents(call toolCall, result any) []string {
	var scanned []byte
	if encoded, err := json.Marshal(call.Arguments); err == nil {
		scanned = append(scanned, encoded...)
	}
	if result != nil {
		if encoded, err := json.Marshal(result); err == nil {
			scanned = append(scanned, encoded...)
		}
	}
	seen := map[string]bool{}
	events := make([]string, 0, 8)
	// FindAllSubmatch with an overlapping-safe scan: the trailing boundary byte
	// is consumed by each match, so two adjacent identifiers separated by one
	// delimiter would otherwise hide the second. Scanning from the end of the
	// captured identifier rather than the whole match keeps both visible.
	for offset := 0; offset < len(scanned); {
		match := eventIDPattern.FindSubmatchIndex(scanned[offset:])
		if match == nil {
			break
		}
		id := string(scanned[offset+match[4] : offset+match[5]])
		offset += match[5]
		if seen[id] {
			continue
		}
		seen[id] = true
		events = append(events, id)
		if len(events) == maxAttentionEvents {
			break
		}
	}
	return events
}

// liveAttention reads what the caller should notice alongside its own result.
//
// It never returns an error and never propagates one. The durable act this
// rides beside has already happened; failing the call because an advisory read
// failed would make awareness a precondition for work, which is precisely the
// coupling the request forbids. An unreachable or unhappy resident yields
// available=false and nothing more.
func (s *mcpServer) liveAttention(ctx context.Context, current *room, call toolCall, result any) map[string]any {
	unavailable := map[string]any{"available": false}
	if current == nil {
		return unavailable
	}
	if _, ok := current.endpoint(); !ok {
		return unavailable
	}
	value, err := s.post(ctx, current, "/v0/attention", map[string]any{
		"session": s.session,
		"events":  attentionEvents(call, result),
	})
	if err != nil {
		return unavailable
	}
	report, ok := value.(map[string]any)
	if !ok {
		return unavailable
	}
	return report
}

// attentionSummary is the guaranteed text. Structured content is optional for a
// client; the text block is not, so an interruption that exists only in
// structuredContent is invisible to anyone who reads only the transcript.
func attentionSummary(report map[string]any) string {
	if report == nil || report["available"] != true {
		return ""
	}
	var parts []string
	if pending := intValue(report["pending"]); pending > 0 {
		noun := "messages"
		if pending == 1 {
			noun = "message"
		}
		part := fmt.Sprintf("%d unacknowledged addressed %s", pending, noun)
		if omitted := intValue(report["omitted"]); omitted > 0 {
			part += fmt.Sprintf(" (%d not shown)", omitted)
		}
		parts = append(parts, part)
	}
	if actors, ok := report["actors"].([]any); ok && len(actors) > 0 {
		var names []string
		for _, entry := range actors {
			if row, ok := entry.(map[string]any); ok {
				name := stringValue(row["name"])
				if status := stringValue(row["status"]); status != "" && status != "available" {
					name += " (" + status + ")"
				}
				names = append(names, name)
			}
		}
		noun := "actors are"
		if len(names) == 1 {
			noun = "actor is"
		}
		part := fmt.Sprintf("%d live %s focused on what you just touched: %s", len(names), noun, strings.Join(names, ", "))
		if omitted := intValue(report["omitted_actors"]); omitted > 0 {
			part += fmt.Sprintf(", and %d more", omitted)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	// Named as advisory in the text itself. The adjunct confers nothing, and a
	// reader who only ever sees this line should not have to infer that.
	return "Live (advisory, no durable force): " + strings.Join(parts, "; ") + "."
}

func intValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	}
	return 0
}
