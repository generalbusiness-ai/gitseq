package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
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

	nexus "github.com/generalbusiness-ai/gitseq/host/live"
	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/apphost"
	"github.com/generalbusiness-ai/gitseq/internal/intent"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/residentclient"
	"github.com/generalbusiness-ai/gitseq/internal/reviewguard"
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

const instructions = "Use status once and wait to follow the workroom; use work, artifacts, and inspect for selective durable follow-up; say ephemerally and promote deliberate acts with state."

const (
	orientationTimeout        = 2 * time.Second
	orientationResponseLimit  = 64 << 10
	actorStatusResponseLimit  = 1 << 20
	workResponseLimit         = 256 << 10
	artifactResponseLimit     = 2 << 20
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
	workspace   *app.Workspace
	actor       string
	fingerprint string

	mu         sync.Mutex
	baseURL    string
	credential string
	announced  bool
	inbox      bool
	// validatedHead is the exact durable frontier at which this actor's live
	// participant standing was last proved. A stable head keeps later calls
	// off the projection; movement requires one fresh bounded orientation (or
	// the verified local fallback) before the binding can be reused.
	validatedHead string
	// identityNoticeChecked records that this process has already looked for
	// other live sessions using its actor identity in this workroom. The check
	// runs before this session announces itself; repeating it afterwards would
	// count this session in its own warning.
	identityNoticeChecked bool
}

// roomSelection keeps repository generation and actor selection together. A
// selected path can be a symlink that later points elsewhere, and a common Git
// directory can be reinitialised under a new genesis, so neither path alone is
// a room identity. Resident state is reusable only across checkouts with the
// same freshly opened common directory, genesis, selector, and fingerprint.
type roomSelection struct {
	path        string
	commonDir   string
	genesis     string
	actor       string
	fingerprint string
	keyFile     string
}

// selectedIdentity is one call's custody proof. The key is loaded once,
// matched to the configured fingerprint, and then carried to every mutation
// in that call so validation and signing cannot observe different key files.
type selectedIdentity struct {
	workspace   *app.Workspace
	selector    string
	actor       apphost.Actor
	private     ed25519.PrivateKey
	orientation *service.Orientation
	local       *app.SourcedSnapshot
}

// selectionError preserves the underlying error class for callers that need
// to distinguish unknown custody from unreadable custody without disclosing a
// configured key path in the adapter's public error text.
type selectionError struct {
	message string
	cause   error
}

func (e *selectionError) Error() string { return e.message }
func (e *selectionError) Unwrap() error { return e.cause }

// resolveEndpoint names the service to use, re-reading the repository's
// published address whenever the last one is gone, so a service started or
// restarted after the adapter attached is picked up without reconnecting the
// client. It reports the three answers the advertisement actually has, rather
// than collapsing two of them into one boolean: an address, absence ("" with
// no error), or a record that is present and cannot be trusted (an error
// naming which of the six ways it fails).
//
// Keeping the third answer is what lets each caller pay its own price for it.
// A read may still answer from the verified local fold, because reading
// locally costs nothing but a stale view the caller is told about. A durable
// act may not: folding it locally because the record saying where to send it
// had been tampered with is a whole-log rebuild the author never asked for and
// cannot see. That is the same reasoning `gs` applies, and the two surfaces
// now agree.
//
// classify is which question is being asked, and it is the whole difference
// between the two callers. False asks what address this room is using, which
// the cached one answers without touching the disk: that is the read path, and
// it stays as cheap as it was. True asks what the record says now, which is
// the only question a durable act may act on. A room that had cached a good
// address and then met a tampered record would otherwise never look again, so
// the guard would protect a fresh room and quietly let a running one through —
// and a resident that later stopped would drop that act into the local fold
// with the tampering unmentioned. The record is untrusted input every time it
// is read, not only the first time.
func (r *room) resolveEndpoint(classify bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	advertised := r.baseURL
	if classify || advertised == "" {
		advertisement := r.workspace.ResidentAdvertisement()
		switch advertisement.State {
		case app.AdvertisementUnusable:
			return "", residentclient.UntrustedAdvertisement(advertisement.Reason)
		case app.AdvertisementPublished:
			advertised = advertisement.URL
		case app.NoAdvertisement:
			// Absence is not untrustworthiness. A repository that advertises
			// nothing acts locally as it always did, and a room already
			// holding an address keeps it: the address came from a record
			// that was trusted when it was read, and a service that has since
			// gone will say so by not answering, which is an honest fallback.
		}
	}
	if advertised == "" {
		return "", nil
	}
	validated, err := residentclient.ValidateURL(advertised)
	if err != nil {
		r.forget()
		return "", residentclient.UnusableAdvertisedURL(advertised, err)
	}
	if validated != r.baseURL {
		// The record names a different service than the one this room was
		// using. Everything the room holds — its credential, its announced
		// presence, its inbox — was minted by the old one and means nothing
		// at the new address, so the address is adopted without them.
		r.forget()
		r.baseURL = validated
	}
	return validated, nil
}

// durableEndpoint is the resolution a durable act must use: the record as it
// stands now, not the address this room happens to be holding.
func (r *room) durableEndpoint() (string, error) {
	return r.resolveEndpoint(true)
}

// endpoint is the reading a caller that may proceed without a resident wants:
// an address, or nothing usable. It discards which of absence and refusal it
// was, so only a caller that has looked at the reason may act on the
// difference.
func (r *room) endpoint() (string, bool) {
	base, _ := r.resolveEndpoint(false)
	return base, base != ""
}

// lost forgets an address that did not answer, and the presence announced
// through it, so the next call looks the service up again instead of retrying
// an address that has gone.
func (r *room) lost() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forget()
}

// forget drops the address and everything that only meant anything at it. The
// caller holds the lock.
func (r *room) forget() {
	r.baseURL = ""
	r.credential = ""
	r.announced = false
	r.inbox = false
}

func (r *room) joined() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.announced = true
}

func (r *room) credentialValue() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.credential
}

func (r *room) setCredential(credential string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credential = credential
}

func (r *room) clearLease() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credential = ""
	r.announced = false
	r.inbox = false
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

func (r *room) standingIsCurrent(head string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return head != "" && r.validatedHead == head
}

func (r *room) rememberValidatedHead(head string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validatedHead = head
}

type mcpServer struct {
	era       protocolEra
	actor     string
	repo      string
	client    *residentclient.Client
	notices   io.Writer
	deadlines residentDeadlinePolicy
	open      func(context.Context, string) (*app.Workspace, error)

	roomsMu     sync.Mutex
	byPath      map[roomSelection]*room
	byCommonDir map[roomSelection]*room
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
		client:      newResidentClient(),
		notices:     os.Stderr,
		deadlines:   defaultResidentDeadlines,
		open:        app.Open,
		byPath:      map[roomSelection]*room{},
		byCommonDir: map[roomSelection]*room{},
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
	// Every tool names the workroom and signing identity it acts through in the
	// same way. Both are selectors over resources the process can already
	// access; neither grants access or creates an identity.
	repoField := map[string]string{"type": "string", "description": "Repository whose workroom this call acts in; defaults to the working directory the adapter was started in."}
	agentField := map[string]string{"type": "string", "description": "Actor whose existing accessible key signs this call; defaults to startup --actor."}
	withSelection := func(properties map[string]any) map[string]any {
		fields := map[string]any{"repo": repoField, "agent": agentField}
		for name, schema := range properties {
			fields[name] = schema
		}
		return fields
	}
	definitions := []map[string]any{
		{"name": "whoami", "description": "Show the selected durable actor and workroom without disclosing its key or resident credential.", "inputSchema": object(withSelection(nil))},
		{"name": "presence", "description": "Inspect leased presence, or update this session's advisory activity. Focus does not claim or complete work.", "inputSchema": object(withSelection(map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"available", "busy", "waiting", "blocked"}},
			"focus":  map[string]any{"type": "array", "items": stringField, "maxItems": nexus.MaxFocusEvents},
			"note":   map[string]any{"type": "string", "maxLength": nexus.MaxActivityNoteBytes},
		}))},
		{"name": "status", "description": "Project durable work and this session's priority ephemeral chat; awaiting_ratification contains standing proposals this actor's roles may ratify, and available_to_you contains open unclaimed requests addressed to this actor.", "inputSchema": object(withSelection(nil))},
		{"name": "wait", "description": "Long-poll after a composite cursor; repeats unacknowledged priority ephemeral chat until ack is called.", "inputSchema": object(withSelection(map[string]any{"cursor": map[string]string{"type": "object"}, "timeout_ms": map[string]string{"type": "integer"}}), "cursor")},
		{"name": "work", "description": "Query the current actor's durable work through a bounded resident-side projection. Defaults return work still owed, including standing proposals this actor may ratify and addressed unclaimed requests; closed commitments carrying only ordinary staleness are counted in closed_stale_omitted instead of listed. Pass stale=include or name statuses to list them.", "inputSchema": object(withSelection(map[string]any{
			"lanes":    arrayOf(enum("available_to_you", "awaiting_ratification", "waiting_on_you", "you_are_waiting_on", "not_actionable")),
			"statuses": arrayOf(enum("open", "promised", "reported", "awaiting-merge", "awaiting-ratification", "superseded", "satisfied", "stale", "cancelled", "reneged", "withdrawn")),
			"stale":    enum("summary", "include", "only", "exclude"),
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": statusview.WorkPageMax},
			"cursor":   stringField,
		}))},
		{"name": "artifacts", "description": "Page through live artifact bases at exact path strings without fetching the full projection.", "inputSchema": object(withSelection(map[string]any{
			"paths":  map[string]any{"type": "array", "items": stringField, "minItems": 1, "maxItems": statusview.ArtifactPathMax},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": statusview.ArtifactPageMax},
			"cursor": stringField,
		}), "paths")},
		{"name": "inspect", "description": "Inspect one exact canonical durable event with its decision, commitment chain, direct provenance, and related review artifacts.", "inputSchema": object(withSelection(map[string]any{"event": stringField}), "event")},
		{"name": "say", "description": "Publish signed ephemeral chat. Unique @name mentions and exact replies address live recipient sessions for priority delivery.", "inputSchema": object(withSelection(map[string]any{"about": stringField, "text": stringField, "conversation": stringField, "re": stringField}), "about", "text")},
		{"name": "ack", "description": "Acknowledge exact priority-chat thread handles for this leased session. This is not a durable read receipt.", "inputSchema": object(withSelection(map[string]any{"threads": map[string]any{"type": "array", "items": stringField, "maxItems": nexus.MaxInboxFrames}}), "threads")},
		{"name": "state", "description": "Append a durable attributed utterance. Evidence values are embedded attachments. A request body addresses its performer as name, @name, or fingerprint; the signed event stores the fingerprint. allow_dead_basis rests on a retired basis anyway, signing body.dead_basis_override=true; a merely stale basis is admitted without it, with the staleness recorded in body.stale_bases.", "inputSchema": object(withSelection(map[string]any{"kind": stringField, "text": stringField, "body": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "rests_on": map[string]any{"type": "array", "items": stringField}, "evidence": map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}}, "allow_dead_basis": map[string]string{"type": "boolean"}, "idempotency_key": stringField}), "kind", "text", "rests_on")},
		{"name": "review", "description": "File a guarded review verdict against an exact head. ack_head_news acknowledges durable statements sequenced after the review request that name this head or lane; missing, duplicate, or extraneous acknowledgments refuse.", "inputSchema": object(withSelection(map[string]any{
			"artifacts":       map[string]any{"type": "array", "items": stringField, "minItems": 1},
			"promise":         stringField,
			"verdict":         enum("approved", "changes-requested"),
			"text":            stringField,
			"ack_head_news":   map[string]any{"type": "array", "items": stringField},
			"idempotency_key": stringField,
		}), "artifacts", "promise", "verdict", "text")},
		{"name": "ratify", "description": "Attempt to confer force on a statement; authority is decided by the fold.", "inputSchema": object(withSelection(map[string]any{"target": stringField, "idempotency_key": stringField}), "target")},
		{"name": "supersede", "description": "Attempt to retire an act and propagate staleness.", "inputSchema": object(withSelection(map[string]any{"target": stringField, "text": stringField, "rests_on": map[string]any{"type": "array", "items": stringField}, "idempotency_key": stringField}), "target", "text")},
		{"name": "reassign_if_unclaimed", "description": "Retire one live, unclaimed request and publish its replacement as a guarded, resumable pair. Staleness is no bar; unrelated durable traffic is allowed; any promise or direct completion refuses.", "inputSchema": object(withSelection(map[string]any{
			"old_request":     stringField,
			"to":              stringField,
			"text":            stringField,
			"conditions":      stringField,
			"retirement_text": stringField,
			"rests_on":        map[string]any{"type": "array", "items": stringField},
			"idempotency_key": stringField,
		}), "old_request", "to", "text", "conditions", "idempotency_key")},
	}
	// MCP provides outputSchema so that a tool can declare the shape of the
	// structuredContent it returns. This adapter returns structuredContent on
	// every success and declared nothing about it, so the declaration is a
	// matter of self-description and interoperation with the standard, not a
	// repair of any observed client.
	//
	// An object is the whole of what is proved. dispatch answers with a Go map
	// or a statusview struct, both of which encode as a JSON object, and a
	// refused call carries no structuredContent at all. No key is proved: tools
	// answer from the resident or from a degraded local fold, and a page comes
	// back empty or capped, so additionalProperties stays true and a conformant
	// client validating against this schema still accepts every response the
	// adapter makes today. Attaching after the definitions rather than inside
	// each one means a tool added later cannot be the one that forgets.
	for _, definition := range definitions {
		definition["outputSchema"] = map[string]any{"type": "object", "additionalProperties": true}
	}
	return definitions
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
	return s.attendAs(ctx, repo, s.actor)
}

func (s *mcpServer) attendAs(ctx context.Context, repo, actor string) (*room, error) {
	current, err := s.attachAs(ctx, repo, actor)
	if err != nil {
		return nil, err
	}
	if err := s.attendRoom(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *mcpServer) attendRoom(ctx context.Context, current *room) error {
	if !current.sharedIdentityNoticeChecked() {
		if err := s.warnSharedIdentity(ctx, current); err != nil {
			return err
		}
		current.markSharedIdentityNoticeChecked()
	}
	if !current.present() {
		if err := s.announce(ctx, current); err == nil {
			current.joined()
		}
	}
	return nil
}

func (s *mcpServer) attach(ctx context.Context, repo string) (*room, error) {
	return s.attachAs(ctx, repo, s.actor)
}

func (s *mcpServer) attachAs(ctx context.Context, repo, actor string) (*room, error) {
	path := s.repo
	if strings.TrimSpace(repo) != "" {
		path = absolute(repo)
	}
	workspace, err := s.open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("no gitseq workroom for %s: %w", path, err)
	}
	identity, err := s.validateSelection(ctx, workspace, actor)
	if err != nil {
		return nil, err
	}
	return s.attachIdentity(path, identity), nil
}

func (s *mcpServer) attachIdentity(path string, identity *selectedIdentity) *room {
	commonDir := identity.workspace.CommonDir
	genesis := identity.workspace.View().Genesis
	selection := roomSelection{path: path, commonDir: commonDir, genesis: genesis, actor: identity.selector, fingerprint: identity.actor.Fingerprint, keyFile: identity.actor.KeyFile}
	commonSelection := roomSelection{commonDir: commonDir, genesis: genesis, actor: identity.selector, fingerprint: identity.actor.Fingerprint, keyFile: identity.actor.KeyFile}
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	// A selector remapped to another fingerprint starts a new custody session.
	// Clear every older generation before either reusing or creating the newly
	// validated room, including when configuration later rotates back. Rooms
	// never hold a private key, and clearLease drops the only locally usable
	// credential. The old advisory resident row may remain until its bounded
	// lease expires; retaining a signer merely to depart it would cross the
	// custody boundary this cache is meant to enforce.
	for held, current := range s.byPath {
		if held.path == selection.path && held.actor == selection.actor &&
			(held.commonDir != selection.commonDir || held.genesis != selection.genesis || held.fingerprint != selection.fingerprint || held.keyFile != selection.keyFile) {
			current.clearLease()
		}
	}
	for held, current := range s.byCommonDir {
		if held.commonDir == commonSelection.commonDir && held.actor == commonSelection.actor &&
			(held.genesis != commonSelection.genesis || held.fingerprint != commonSelection.fingerprint || held.keyFile != commonSelection.keyFile) {
			current.clearLease()
		}
	}
	if existing, ok := s.byPath[selection]; ok {
		existing.rememberValidatedHead(identityValidatedHead(identity))
		return existing
	}
	// A linked worktree is a checkout of a repository, not a second workroom:
	// two paths share a room only while both the selected name and validated
	// fingerprint still match.
	current, ok := s.byCommonDir[commonSelection]
	if !ok {
		current = &room{workspace: identity.workspace, actor: identity.selector, fingerprint: identity.actor.Fingerprint, validatedHead: identityValidatedHead(identity)}
		s.byCommonDir[commonSelection] = current
	} else {
		current.rememberValidatedHead(identityValidatedHead(identity))
	}
	s.byPath[selection] = current
	return current
}

func identityValidatedHead(identity *selectedIdentity) string {
	if identity.orientation != nil {
		return identity.orientation.Frontier.Head
	}
	if identity.local != nil {
		return identity.local.Snapshot.Head
	}
	return ""
}

// cachedRoom resolves only the repository and its small custody record. It
// deliberately does not reopen the application workspace: Open verifies the
// immutable host binding by walking the first-parent log, which is correct on
// the first attachment but an O(depth) tax on every later tool call. The
// generation, configured fingerprint, and key path make a cache hit exact;
// changing any of them takes the cold validation path and clears the old
// lease in attachIdentity.
func (s *mcpServer) cachedRoom(ctx context.Context, path, actorName string) (*room, bool, error) {
	_, commonDir, err := apphost.ResolveGitDirs(ctx, path)
	if err != nil {
		return nil, false, fmt.Errorf("no gitseq workroom for %s: %w", path, err)
	}
	config, err := apphost.LoadConfig(apphost.MetaDir(commonDir))
	if err != nil {
		return nil, false, fmt.Errorf("no gitseq workroom for %s: %w", path, err)
	}
	configured, ok := config.Actors[actorName]
	if !ok {
		return nil, false, nil
	}
	pathSelection := roomSelection{path: path, commonDir: commonDir, genesis: config.Genesis, actor: actorName, fingerprint: configured.Fingerprint, keyFile: configured.KeyFile}
	commonSelection := pathSelection
	commonSelection.path = ""

	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()
	if current := s.byPath[pathSelection]; current != nil {
		return current, true, nil
	}
	if current := s.byCommonDir[commonSelection]; current != nil {
		s.byPath[pathSelection] = current
		return current, true, nil
	}
	return nil, false, nil
}

// selectedActor returns the startup actor only when the call omitted agent.
// An explicitly empty or non-string selector is malformed, not permission to
// fall back and sign as somebody else.
func (s *mcpServer) selectedActor(call toolCall) (string, bool, error) {
	raw, present := call.Arguments["agent"]
	if !present {
		return s.actor, false, nil
	}
	actor, ok := raw.(string)
	if !ok || strings.TrimSpace(actor) == "" {
		return "", true, errors.New("agent must name a configured actor; refusing to fall back to the startup actor")
	}
	return actor, true, nil
}

func selectedRepo(call toolCall) (string, bool, error) {
	raw, present := call.Arguments["repo"]
	if !present {
		return "", false, nil
	}
	repo, ok := raw.(string)
	if !ok || strings.TrimSpace(repo) == "" {
		return "", true, errors.New("repo must name an accessible Gitseq workroom; refusing to fall back to the startup repository")
	}
	return repo, true, nil
}

// validateSelection loads one existing key, binds it to configuration, and
// proves the same fingerprint is a current participant. The resident's
// bounded orientation avoids a full local replay when available; verified
// local state is the degraded fallback.
func (s *mcpServer) validateSelection(ctx context.Context, workspace *app.Workspace, actorName string) (*selectedIdentity, error) {
	actor, private, err := selectedActorCustody(workspace, actorName)
	if err != nil {
		return nil, err
	}
	actual := intent.ActorFingerprint(private.Public().(ed25519.PublicKey))
	if actual != actor.Fingerprint {
		return nil, fmt.Errorf("cannot use selected agent %q in %s: accessible key fingerprint %s does not match configured fingerprint %s", actorName, workspace.CommonDir, actual, actor.Fingerprint)
	}
	probe := &room{workspace: workspace, actor: actorName, fingerprint: actor.Fingerprint}
	s.roomsMu.Lock()
	if existing := s.byCommonDir[roomSelection{commonDir: workspace.CommonDir, genesis: workspace.View().Genesis, actor: actorName, fingerprint: actor.Fingerprint, keyFile: actor.KeyFile}]; existing != nil {
		probe = existing
	}
	s.roomsMu.Unlock()
	residentContext, cancel := context.WithTimeout(ctx, orientationTimeout)
	var orientation service.Orientation
	var residentErr error
	for attempt := 0; attempt < 2; attempt++ {
		orientation, residentErr = s.currentResidentOrientation(residentContext, probe, actor.Fingerprint)
		if residentErr == nil || residentContext.Err() != nil {
			break
		}
	}
	cancel()
	if residentErr == nil {
		return &selectedIdentity{workspace: workspace, selector: actorName, actor: actor, private: private, orientation: &orientation}, nil
	}
	durable, err := workspace.SnapshotWithSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot validate selected agent %q in %s: %w", actorName, workspace.CommonDir, err)
	}
	roster, ok := durable.Snapshot.Projection.Actors[actor.Fingerprint]
	if !ok || roster.Retired || !containsString(roster.Roles, "participant") {
		return nil, fmt.Errorf("cannot use selected agent %q in %s: actor is not a live participant in the effective durable roster", actorName, workspace.CommonDir)
	}
	return &selectedIdentity{workspace: workspace, selector: actorName, actor: actor, private: private, local: &durable}, nil
}

// selectedActorCustody reads the accessible key while keeping its path out of
// public failures. errors.Is still sees ErrUnknownActor on a real miss and the
// underlying filesystem error on broken custody.
func selectedActorCustody(workspace *app.Workspace, actorName string) (apphost.Actor, ed25519.PrivateKey, error) {
	actor, private, err := workspace.Actor(actorName)
	if err == nil {
		return actor, private, nil
	}
	message := fmt.Sprintf("cannot use selected agent %q in %s: existing actor key is not accessible", actorName, workspace.CommonDir)
	if errors.Is(err, app.ErrUnknownActor) {
		message = fmt.Sprintf("cannot use selected agent %q in %s: actor is not configured", actorName, workspace.CommonDir)
	}
	return apphost.Actor{}, nil, &selectionError{message: message, cause: err}
}

// revalidateSelection is the hot cache-hit path. It re-reads the selected key
// for every call and proves that it still matches the exact cached binding.
// An unchanged workroom head reuses the standing proof; movement triggers one
// bounded resident orientation or the verified local fallback. Neither path
// reopens or replays the immutable application binding.
func (s *mcpServer) revalidateSelection(ctx context.Context, current *room) (*selectedIdentity, error) {
	actor, private, err := selectedActorCustody(current.workspace, current.actor)
	if err != nil {
		return nil, err
	}
	actual := intent.ActorFingerprint(private.Public().(ed25519.PublicKey))
	if actor.Fingerprint != current.fingerprint || actual != current.fingerprint {
		return nil, fmt.Errorf("cannot use selected agent %q in %s: accessible key fingerprint %s does not match cached configured fingerprint %s", current.actor, current.workspace.CommonDir, actual, current.fingerprint)
	}
	identity := &selectedIdentity{workspace: current.workspace, selector: current.actor, actor: actor, private: private}
	head, err := current.workspace.Store.Head(ctx, kernel.Ref(current.workspace.View().Genesis))
	if err != nil {
		return nil, fmt.Errorf("cannot validate selected agent %q in %s: %w", current.actor, current.workspace.CommonDir, err)
	}
	if current.standingIsCurrent(head) {
		return identity, nil
	}

	residentContext, cancel := context.WithTimeout(ctx, orientationTimeout)
	var orientation service.Orientation
	var residentErr error
	for attempt := 0; attempt < 2; attempt++ {
		orientation, residentErr = s.currentResidentOrientation(residentContext, current, actor.Fingerprint)
		if residentErr == nil || residentContext.Err() != nil {
			break
		}
	}
	cancel()
	if residentErr == nil {
		identity.orientation = &orientation
		current.rememberValidatedHead(orientation.Frontier.Head)
		return identity, nil
	}
	durable, err := current.workspace.SnapshotWithSource(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot validate selected agent %q in %s: %w", current.actor, current.workspace.CommonDir, err)
	}
	roster, ok := durable.Snapshot.Projection.Actors[actor.Fingerprint]
	if !ok || roster.Retired || !containsString(roster.Roles, "participant") {
		return nil, fmt.Errorf("cannot use selected agent %q in %s: actor is not a live participant in the effective durable roster", current.actor, current.workspace.CommonDir)
	}
	identity.local = &durable
	current.rememberValidatedHead(durable.Snapshot.Head)
	return identity, nil
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
	repo, _, err := selectedRepo(call)
	if err != nil {
		return nil, nil, err
	}
	actor, _, err := s.selectedActor(call)
	if err != nil {
		return nil, nil, err
	}
	path := s.repo
	if repo != "" {
		path = absolute(repo)
	}
	current, cached, err := s.cachedRoom(ctx, path, actor)
	if err != nil {
		return nil, nil, err
	}
	var workspace *app.Workspace
	if cached {
		workspace = current.workspace
	} else {
		workspace, err = s.open(ctx, path)
		if err != nil {
			return nil, nil, fmt.Errorf("no gitseq workroom for %s: %w", path, err)
		}
	}
	// A durable mutation must refuse an untrusted resident advertisement before
	// it reads any signing key. Besides preserving the fail-closed transport
	// boundary, that ordering means a malformed record cannot be mistaken for a
	// selected-actor custody failure. submit rechecks the same record after the
	// validated room is attached and again before a local fallback, so this is a
	// precedence check rather than a cached authorization.
	if durableTool(call.Name) {
		probe := &room{workspace: workspace, actor: actor}
		if _, err := probe.durableEndpoint(); err != nil {
			return nil, nil, untrustedAdvertisementRefusal(err)
		}
	}
	var identity *selectedIdentity
	if cached {
		identity, err = s.revalidateSelection(ctx, current)
	} else {
		identity, err = s.validateSelection(ctx, workspace, actor)
		if err == nil {
			current = s.attachIdentity(path, identity)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	if call.Name != "whoami" {
		if err := s.attendRoom(ctx, current); err != nil {
			return nil, current, err
		}
	}
	value, err := s.dispatch(ctx, call, current, identity)
	return value, current, err
}

func durableTool(name string) bool {
	switch name {
	case "state", "review", "ratify", "supersede", "reassign_if_unclaimed":
		return true
	default:
		return false
	}
}

func (s *mcpServer) dispatch(ctx context.Context, call toolCall, current *room, identity *selectedIdentity) (any, error) {
	switch call.Name {
	case "whoami":
		return s.whoami(ctx, current, identity)
	case "presence":
		update := map[string]any{}
		for _, field := range []string{"status", "focus", "note"} {
			if value, present := call.Arguments[field]; present {
				update[field] = value
			}
		}
		own, err := s.announceUpdate(ctx, current, update)
		if err != nil {
			return nil, err
		}
		if !current.inboxAvailable() {
			if err := s.registerInbox(ctx, current); err != nil {
				return nil, err
			}
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
		// The resident selects this bounded view before encoding. The local
		// fallback applies the same digest so losing the resident changes what
		// is knowable, not the shape of the answer.
		var status actorStatus
		err := s.postForSessionBoundedJSON(ctx, current, "/v0/actor-status", map[string]any{"credential": current.credentialValue()}, laneResponseLimit(current, actorStatusResponseLimit, statusview.ListCap), &status)
		if isTransportError(err) || inboxProtocolUnavailable(err) {
			local, localErr := s.localStatus(ctx, current)
			if localErr != nil {
				return nil, localErr
			}
			return s.digest(current, local, true, identity), nil
		}
		if err != nil {
			return nil, err
		}
		if status.You.Fingerprint != current.fingerprint {
			s.revokeLease(ctx, current)
			return nil, errors.New("resident status identity does not match the validated actor fingerprint")
		}
		return status, nil
	case "wait":
		arguments := residentArguments(call.Arguments)
		arguments["credential"] = current.credentialValue()
		requested := requestedCursor(arguments)
		var delta waitDelta
		err := s.postForSessionBoundedJSON(ctx, current, "/v0/actor-wait", arguments, laneResponseLimit(current, actorStatusResponseLimit, statusview.ListCap), &delta)
		if isTransportError(err) || inboxProtocolUnavailable(err) {
			local, localErr := s.waitDurable(ctx, current, arguments)
			if localErr != nil {
				return nil, localErr
			}
			return digestWait(local, requested, identity.actor.Fingerprint, current.actor, true), nil
		}
		if err != nil {
			return nil, err
		}
		return delta, nil
	case "work":
		var input statusview.WorkQuery
		arguments := clone(call.Arguments)
		delete(arguments, "repo")
		delete(arguments, "agent")
		if err := remarshal(arguments, &input); err != nil {
			return nil, err
		}
		input.Actor = identity.actor.Fingerprint
		var page statusview.WorkPage
		if err := s.postBoundedJSON(ctx, current, "/v0/work-query", input, laneResponseLimit(current, workResponseLimit, statusview.WorkPageMax), &page); err != nil {
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
	case "artifacts":
		var input statusview.ArtifactQuery
		arguments := clone(call.Arguments)
		delete(arguments, "repo")
		delete(arguments, "agent")
		if err := remarshal(arguments, &input); err != nil {
			return nil, err
		}
		var page statusview.ArtifactPage
		if err := s.postBoundedJSON(ctx, current, "/v0/artifact-query", input, artifactResponseLimit, &page); err != nil {
			if !isTransportError(err) {
				return nil, err
			}
			durable, localErr := current.workspace.Snapshot(ctx)
			if localErr != nil {
				return nil, localErr
			}
			return statusview.BuildArtifactPage(durable, input, true)
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
		arguments["credential"] = current.credentialValue()
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
		return s.postForSession(ctx, current, "/v0/inbox/ack", map[string]any{"credential": current.credentialValue(), "threads": stringSlice(call.Arguments["threads"])})
	case "state":
		kind, _ := call.Arguments["kind"].(string)
		text, _ := call.Arguments["text"].(string)
		body := stringMap(call.Arguments["body"])
		rests := stringSlice(call.Arguments["rests_on"])
		evidence := make(map[string][]byte)
		for name, content := range stringMap(call.Arguments["evidence"]) {
			evidence[name] = []byte(content)
		}
		allowDead, _ := call.Arguments["allow_dead_basis"].(bool)
		return s.submit(ctx, current, app.Act{Verb: app.VerbState, Kind: workroom.Kind(kind), Text: text, Body: body, RestsOn: rests, Attachments: evidence, IdempotencyKey: stringValue(call.Arguments["idempotency_key"]), AllowDeadBasis: allowDead}, identity)
	case "review":
		return s.review(ctx, current, call, identity)
	case "ratify":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, current, app.Act{Verb: app.VerbRatify, Target: target, IdempotencyKey: stringValue(call.Arguments["idempotency_key"])}, identity)
	case "supersede":
		target := stringValue(call.Arguments["target"])
		return s.submit(ctx, current, app.Act{Verb: app.VerbSupersede, Target: target, Text: stringValue(call.Arguments["text"]), RestsOn: stringSlice(call.Arguments["rests_on"]), IdempotencyKey: stringValue(call.Arguments["idempotency_key"])}, identity)
	case "reassign_if_unclaimed":
		return s.reassignIfUnclaimed(ctx, current, call, identity)
	default:
		return nil, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (s *mcpServer) reassignIfUnclaimed(ctx context.Context, current *room, call toolCall, identity *selectedIdentity) (any, error) {
	oldRequest := stringValue(call.Arguments["old_request"])
	key := stringValue(call.Arguments["idempotency_key"])
	if oldRequest == "" || stringValue(call.Arguments["to"]) == "" || stringValue(call.Arguments["text"]) == "" || stringValue(call.Arguments["conditions"]) == "" || key == "" {
		return nil, errors.New("reassign_if_unclaimed requires old_request, to, text, conditions, and idempotency_key")
	}
	retirementText := stringValue(call.Arguments["retirement_text"])
	if retirementText == "" {
		retirementText = "retire unclaimed request before reassignment"
	}
	retirement, err := s.submit(ctx, current, app.Act{
		Verb: app.VerbRetireIfUnclaimed, Target: oldRequest, Text: retirementText,
		IdempotencyKey: key + "/retirement",
	}, identity)
	if err != nil {
		return nil, err
	}
	retirementRecord, ok := submissionRecord(retirement)
	if !ok {
		return nil, errors.New("guarded retirement returned no durable record")
	}
	replacement, err := s.submit(ctx, current, app.Act{
		Verb: app.VerbReassignIfUnclaimed, Target: oldRequest, Retirement: retirementRecord.ID,
		Text: stringValue(call.Arguments["text"]),
		Body: map[string]string{
			"to": stringValue(call.Arguments["to"]), "conditions": stringValue(call.Arguments["conditions"]),
		},
		RestsOn: stringSlice(call.Arguments["rests_on"]), IdempotencyKey: key + "/request",
	}, identity)
	if err != nil {
		return nil, fmt.Errorf("guarded retirement %s landed or replayed, but its replacement was refused: %w; re-read the old request before retrying", retirementRecord.ID, err)
	}
	return map[string]any{"retirement": retirement, "request": replacement}, nil
}

func submissionRecord(value any) (workroom.Record, bool) {
	result, ok := value.(map[string]any)
	if !ok {
		return workroom.Record{}, false
	}
	record, ok := result["record"].(workroom.Record)
	return record, ok
}

// laneResponseLimit preserves the byte ceiling while allowing every capped
// row to carry one full conditions value. Status and wait expose conditions
// only on their single available lane; work has one page-wide row cap, so rows
// is the complete possible conditions-bearing count for each response.
// Conditions are themselves bounded by the repository's signed payload
// ceiling. The adapter-wide ceiling remains the final bound even for a
// repository configured with an unusually large payload.
func laneResponseLimit(current *room, base int64, rows int) int64 {
	if current == nil || rows <= 0 {
		return base
	}
	ceiling := current.workspace.View().PayloadCeiling
	if ceiling == 0 {
		return base
	}
	maximum := uint64(residentResponseLimit)
	if uint64(base) >= maximum || ceiling > (maximum-uint64(base))/uint64(rows) {
		return residentResponseLimit
	}
	return base + int64(ceiling*uint64(rows))
}

func (s *mcpServer) whoami(ctx context.Context, current *room, identity *selectedIdentity) (any, error) {
	actor := identity.actor
	view := current.workspace.View()
	if identity.orientation != nil {
		return map[string]any{
			"actor": publicActor(actor), "durable": identity.orientation.You, "protocol": protocolVersion,
			"repo": current.workspace.CommonDir, "genesis": view.Genesis,
			"frontier": identity.orientation.Frontier, "source": residentOrientationSource, "degraded": false,
		}, nil
	}
	if identity.local != nil {
		orientation, ok := statusview.BuildOrientation(identity.local.Snapshot, actor.Fingerprint, actor.Name)
		if !ok {
			return nil, errors.New("configured actor is not in the effective durable roster")
		}
		return map[string]any{
			"actor": publicActor(actor), "durable": orientation.You, "protocol": protocolVersion,
			"repo": current.workspace.CommonDir, "genesis": view.Genesis,
			"frontier": orientation.Frontier, "source": string(identity.local.Source), "degraded": true,
		}, nil
	}
	residentContext, cancel := context.WithTimeout(ctx, orientationTimeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		orientation, err := s.currentResidentOrientation(residentContext, current, actor.Fingerprint)
		if err == nil {
			return map[string]any{
				"actor": publicActor(actor), "durable": orientation.You, "protocol": protocolVersion,
				"repo": current.workspace.CommonDir, "genesis": view.Genesis,
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
		"actor": publicActor(actor), "durable": orientation.You, "protocol": protocolVersion,
		"repo": current.workspace.CommonDir, "genesis": view.Genesis,
		"frontier": orientation.Frontier, "source": string(local.Source), "degraded": true,
	}, nil
}

func (s *mcpServer) currentResidentOrientation(ctx context.Context, current *room, fingerprint string) (service.Orientation, error) {
	genesis := current.workspace.View().Genesis
	before, err := current.workspace.Store.Head(ctx, kernel.Ref(genesis))
	if err != nil {
		return service.Orientation{}, err
	}
	var orientation service.Orientation
	if err := s.getBoundedJSON(ctx, current, "/v0/orientation/"+fingerprint, orientationResponseLimit, &orientation); err != nil {
		return service.Orientation{}, err
	}
	after, err := current.workspace.Store.Head(ctx, kernel.Ref(genesis))
	if err != nil {
		return service.Orientation{}, err
	}
	if before != after {
		return service.Orientation{}, errors.New("workroom head moved while resident orientation was read")
	}
	if orientation.ProjectionVersion != service.OrientationProjectionVersion ||
		orientation.Frontier.Genesis != genesis || orientation.Frontier.Head != after ||
		orientation.Frontier.Depth < 0 || orientation.You.Fingerprint != fingerprint ||
		orientation.You.Name == "" || orientation.You.Kind == "" || orientation.You.MembershipEvent == "" ||
		len(orientation.You.Roles) > statusview.ListCap || orientation.You.RolesSkipped < 0 ||
		!containsString(orientation.You.Roles, "participant") {
		return service.Orientation{}, errors.New("resident orientation does not match local durable evidence")
	}
	return orientation, nil
}

func publicActor(actor apphost.Actor) map[string]string {
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

// review files a guarded verdict through internal/reviewguard. It parses only
// the tool's arguments: discovery, acknowledgment validation, canonical
// encoding, and act construction are the guard's, and admission judges the act
// again at sequencing against the frontier this surface confirmed. The MCP
// surface holds no working tree, so the reviewed head comes from the durable
// artifact row rather than from a checkout.
func (s *mcpServer) review(ctx context.Context, current *room, call toolCall, identity *selectedIdentity) (any, error) {
	cited, err := reviewguard.CheckCitations(stringSlice(call.Arguments["artifacts"]))
	if err != nil {
		return nil, err
	}
	promise := stringValue(call.Arguments["promise"])
	verdict := stringValue(call.Arguments["verdict"])
	text := stringValue(call.Arguments["text"])
	if text == "" {
		return nil, errors.New("review requires text")
	}
	headNews := stringSlice(call.Arguments["ack_head_news"])

	// The tool holds no working tree, so every guarded read takes the
	// reviewed head from the durable artifact row; Confirm runs the shared
	// three-read choreography with its exact-set, same-read, acknowledgment,
	// and build checks, exactly as the command line runs it.
	read := func() (reviewguard.Basis, []reviewguard.News, workroom.Projection, error) {
		workspace := identity.workspace
		snapshot, err := workspace.Snapshot(ctx)
		if err != nil {
			return reviewguard.Basis{}, nil, workroom.Projection{}, err
		}
		basis, news, err := reviewguard.ReviewBasis(reviewguard.Read{
			Projection:          snapshot.Projection,
			ReviewerFingerprint: identity.actor.Fingerprint,
			FrontierEvent:       workspace.EventID(snapshot.Head),
			NoCheckout:          true,
		}, cited[0], promise)
		return basis, news, snapshot.Projection, err
	}
	body, restsOn, err := reviewguard.Confirm(read, cited, headNews, verdict, text)
	if err != nil {
		return nil, err
	}
	return s.submit(ctx, current, app.Act{
		Verb: app.VerbState, Kind: workroom.KindReport, Text: text,
		Body: body, RestsOn: restsOn, GuardedReview: true,
		IdempotencyKey: stringValue(call.Arguments["idempotency_key"]),
	}, identity)
}

// untrustedAdvertisementRefusal is what a durable call says when it will not
// send an act through a record it cannot trust. It names nothing about the
// failure itself, which the wrapped reason already says, and adds only what
// the caller needs next: that the log is untouched, and the two ways on.
func untrustedAdvertisementRefusal(reason error) error {
	return fmt.Errorf("%w; nothing was appended: repair or remove that record, or fold this act locally on purpose with `gs` and --server -", reason)
}

// submit is the one durable path through this adapter, and the one place an
// untrustworthy advertisement is a refusal rather than a vacancy. The record
// is classified first, from the file rather than from the address this room
// has been using, and before the signing key is read or anything is built, so
// a repository whose record has been tampered with costs the caller a message
// and nothing else — whether the tampering happened before this session
// attached or an hour into it. It is read once more if the resident then fails
// to answer, so the fallback below can never become the local fold this
// refuses. The refusal is per call: the room, its attachment and its session
// survive it, and the next call resolves the record again, so repairing or
// removing it is all it takes to carry on.
func (s *mcpServer) submit(ctx context.Context, current *room, act app.Act, identity *selectedIdentity) (any, error) {
	base, err := current.durableEndpoint()
	if err != nil {
		return nil, untrustedAdvertisementRefusal(err)
	}
	if len(identity.private) == 0 {
		return nil, errors.New("selected identity has no signing key")
	}
	request, err := identity.workspace.BuildActRequest(ctx, identity.private, identity.selector, act)
	if err != nil {
		return nil, err
	}
	if base != "" {
		requestContext, cancel := context.WithTimeout(ctx, s.deadlineFor("/v0/submit"))
		submission, err := s.client.Submit(requestContext, identity.workspace, base, request)
		cancel()
		if err == nil {
			value := map[string]any{"result": submission.Result, "record": submission.Record}
			return s.withKindWarning(ctx, current, act, value), nil
		}
		err = residentClientError(current, err)
		if !isTransportError(err) {
			return nil, err
		}
		// The resident did not answer, and the local fold below is the
		// fallback for exactly that. But the record can be tampered with while
		// a call is in flight, and losing the connection is what a tamper
		// followed by a stopped service looks like from here, so the record is
		// read once more before this act is folded anywhere. Transport loss
		// through a record that still reads is an honest fallback; transport
		// loss through one that does not is the case this whole guard exists
		// to refuse.
		if _, recheck := current.durableEndpoint(); recheck != nil {
			return nil, fmt.Errorf("the resident did not answer this act (%v), and the local fold is not an honest substitute for it: %w; repair or remove that record and act again, or fold locally on purpose with `gs` and --server -", err, recheck)
		}
	}
	submission, err := identity.workspace.AcceptSubmission(ctx, request)
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
//
// It also reports how the fold read the act, which is a broader question than
// whether the kind is defined. Everything about a successful append says
// success — a record comes back, the statement is ruled effective, the
// commitment moves — and none of that says the act became what its author
// meant. Acts have landed here reading as reviews and projecting as no review,
// and approvals have landed unable to authorize the merge they were written to
// authorize. Those are not spelling mistakes the tool could have caught; they
// are the difference between what was written and what was read, and only the
// projection knows it.
func (s *mcpServer) withKindWarning(ctx context.Context, current *room, act app.Act, value any) any {
	result, ok := value.(map[string]any)
	if !ok {
		return value
	}
	// One snapshot serves both answers. main moved the undefined-kind warning
	// into residentclient.UndefinedKindWarning, which takes its own snapshot;
	// this call site needs one anyway for the projection notes, and a snapshot
	// can be a cold fold, so calling the helper here would pay for that twice
	// on every act. The warning is derived from the snapshot already in hand,
	// which is exactly what that helper does with its own.
	snapshot, err := current.workspace.Snapshot(ctx)
	if err != nil {
		if act.Verb == app.VerbState {
			result["warning"] = fmt.Sprintf("cannot tell whether kind %q is defined here: %v", act.Kind, err)
		}
		// Every verb is promised these notes, so every verb is told when they
		// could not be produced. Returning silently would be the failure this
		// whole disclosure exists to prevent, arriving through the disclosure
		// itself: a result that looks exactly like one where the fold found
		// nothing to report.
		result["projected"] = map[string]any{
			"unavailable": fmt.Sprintf("the projection could not be read after this act landed, so nothing here says how the fold read it: %v", err),
		}
		return result
	}
	if act.Verb == app.VerbState {
		if warning := snapshot.Vocabulary.UndefinedKindWarning(act.Kind); warning != "" {
			result["warning"] = warning
		}
	}
	if notes := projectionNotes(snapshot.Projection, act, submittedEvent(value)); len(notes) > 0 {
		result["projected"] = notes
	}
	return result
}

// submittedEvent digs the new event's identifier out of the tool result. The
// result is a decoded JSON map when the resident sequenced the act and a typed
// submission when this process did, so it is read back through JSON rather than
// type-asserted two ways.
func submittedEvent(value any) string {
	var submitted struct {
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if remarshal(value, &submitted) != nil {
		return ""
	}
	return submitted.Record.ID
}

// projectionNotes says how the fold read this act, in the author's own terms.
//
// Each note answers a question the author cannot otherwise ask without a
// separate projection query, and each corresponds to a way an act has already
// gone wrong here while every visible signal said it had gone right. The notes
// describe; they do not refuse. An act that lands is the author's to correct,
// and refusing unfamiliar bodies would narrow a deliberately open structure.
//
// This is a report, not a guarantee. It says what the fold made of the act at
// this moment, and reading it is weaker than querying the projection — a later
// supersession can change any of it. Treat a clean set of notes as the absence
// of the known traps rather than the presence of correctness.
func projectionNotes(projection workroom.Projection, act app.Act, event string) map[string]any {
	if event == "" {
		return nil
	}
	notes := map[string]any{}

	// A target naming no event in this workroom. `supersede` and `ratify` carry
	// their subject here rather than in rests_on, and a fabricated one is the
	// cheapest mistake to make and the quietest to survive: the act appends, the
	// record comes back, and the fold rules it ineffective for a target it has
	// never seen. This is not hypothetical — an act of exactly this shape was
	// filed against this workroom while this change sat in review.
	if act.Target != "" && !resolves(projection, act.Target) {
		notes["unresolved_target"] = act.Target
	}

	// The fold's own ruling, including plain effect. Omitting the common case
	// forced every caller to ask the projection whether an absent verdict meant
	// effect or merely meant this reporting step failed.
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			notes["verdict"] = string(decision.Verdict)
			if decision.Reason != "" {
				notes["reason"] = decision.Reason
			}
		}
	}

	// Citations that name nothing in this workroom. A fabricated or mistyped
	// identifier is skipped in silence by validateBasis unless the kind's own
	// basis constraints happen to need it, so the act connects to nothing and
	// says so nowhere.
	var unresolved []string
	for _, reference := range act.RestsOn {
		if !resolves(projection, reference) {
			unresolved = append(unresolved, reference)
		}
	}
	if len(unresolved) > 0 {
		notes["unresolved_rests_on"] = unresolved
	}

	// Citations that resolve but were already dead when the act landed on
	// them: the cited record superseded, stale because its own basis moved, or
	// itself an effective supersession. A landed record reads as success all
	// the same, and nothing else in the result tells resting on living ground
	// from citing a corpse — which is how authors end up building on ground
	// they would never have chosen had they been told.
	if dead := workroom.DeadBases(projection, act.RestsOn); len(dead) > 0 {
		reasons := make(map[string]string, len(dead))
		for id, basis := range dead {
			reasons[id] = string(basis)
		}
		notes["dead_rests_on"] = reasons
	}

	// Whether a report became a review, and if so what a merge would make of
	// it. A report reads as a review to any human the moment its text says
	// "approved"; the fold only sees body.verdict.
	// Whether a report became a review is answered from what was submitted and
	// what the fold decided, never from the absence of a Review row alone. A
	// report that sets body.verdict and is then ruled ineffective — no promise,
	// or another precondition unmet — projects no review, and saying it did not
	// set the field would be telling its author to fix something that is not
	// wrong while the real reason sits in the verdict note above.
	if act.Kind == workroom.KindReport {
		review, found := reviewOf(projection, event)
		verdict := strings.TrimSpace(act.Body["verdict"])
		switch {
		case found && review.Artifact == "":
			notes["review"] = "yes, but no artifact resolved; `gs merge` refuses an approval whose rests_on omits the artifact for the reviewed head"
		case found:
			notes["review"] = "yes, judging artifact " + review.Artifact
		case verdict == "":
			notes["review"] = "no: the fold reads a review from body.verdict, which this report does not set"
		default:
			notes["review"] = fmt.Sprintf("no: body.verdict is %q, but the fold projected no review for this report — see the verdict and reason above for what it refused", verdict)
		}
	}
	return notes
}

// resolves reports whether an identifier names an event this workroom holds.
// A wrong one is indistinguishable from a right one until something asks.
//
// Decisions, not statements. There is exactly one decision per durable record,
// while statements hold only utterances — so ratify and supersede are events
// with no statement, and the fold explicitly allows superseding a supersession.
// Searching statements would have called those citations unresolved: a check
// written to catch fabricated identifiers, reporting real ones as fabricated,
// which is worse than not checking at all because it teaches readers to ignore
// it.
func resolves(projection workroom.Projection, event string) bool {
	for _, decision := range projection.Decisions {
		if decision.Event == event {
			return true
		}
	}
	return false
}

func reviewOf(projection workroom.Projection, event string) (workroom.Review, bool) {
	for _, review := range projection.Reviews {
		if review.Report == event {
			return review, true
		}
	}
	return workroom.Review{}, false
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
	actor, err := current.workspace.ResolveActor(current.actor)
	if err != nil {
		fmt.Fprintln(s.noticeWriter(), "gitseq-mcp: shared-identity check skipped:", err)
		return nil
	}
	value, err := s.get(ctx, current, "/v0/presence-count?actor="+url.QueryEscape(current.actor))
	if isTransportError(err) {
		fmt.Fprintln(s.noticeWriter(), "gitseq-mcp: shared-identity check skipped; the resident service is unavailable:", err)
		return nil
	}
	if sharedIdentityCountUnavailable(err) {
		fmt.Fprintln(s.noticeWriter(), "gitseq-mcp: shared-identity check skipped; this resident does not support live identity counts")
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
	_, err := s.announceUpdate(ctx, current, nil)
	if err != nil {
		return err
	}
	current.markSharedIdentityNoticeChecked()
	return s.registerInbox(ctx, current)
}

func (s *mcpServer) registerInbox(ctx context.Context, current *room) error {
	_, err := s.post(ctx, current, "/v0/inbox/register", map[string]any{"credential": current.credentialValue(), "version": service.InboxProtocolVersion})
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

// announceUpdate asks the resident to mint a credential for a new attachment,
// or renews the credential already held for this exact repository. The direct
// HTTP creation response is consumed here and the credential never enters an
// MCP tool result, diagnostic, log, URL or cross-repository field.
func (s *mcpServer) announceUpdate(ctx context.Context, current *room, update map[string]any) (any, error) {
	request := map[string]any{"actor": current.actor, "ttl_ms": 30000}
	for name, value := range update {
		request[name] = value
	}
	if credential := current.credentialValue(); credential != "" {
		request["credential"] = credential
	}
	result, err := s.post(ctx, current, "/v0/presence", request)
	var refusal *residentclient.HTTPError
	if errors.As(err, &refusal) && refusal.Message == "credential is not valid" && current.credentialValue() != "" {
		current.clearLease()
		delete(request, "credential")
		result, err = s.post(ctx, current, "/v0/presence", request)
	}
	if err != nil {
		return nil, err
	}
	response, ok := result.(map[string]any)
	if !ok {
		return nil, errors.New("resident presence response is not an object")
	}
	if current.credentialValue() == "" {
		credential, _ := response["credential"].(string)
		if credential == "" {
			return nil, errors.New("resident did not mint a credential")
		}
		current.setCredential(credential)
	}
	var status actorStatus
	if err := s.postBoundedJSON(ctx, current, "/v0/actor-status", map[string]any{"credential": current.credentialValue()}, laneResponseLimit(current, actorStatusResponseLimit, statusview.ListCap), &status); err != nil || status.You.Fingerprint != current.fingerprint {
		s.revokeLease(ctx, current)
		return nil, errors.New("resident credential identity does not match the validated actor fingerprint")
	}
	current.joined()
	return response["change"], nil
}

func (s *mcpServer) revokeLease(ctx context.Context, current *room) {
	credential := current.credentialValue()
	if credential != "" {
		_, _ = s.post(ctx, current, "/v0/presence/depart", map[string]any{"credential": credential})
	}
	current.clearLease()
}

func sayNeedsInbox(arguments map[string]any) bool {
	return stringValue(arguments["re"]) != "" || service.HasMentionToken(stringValue(arguments["text"]))
}

// attended lists the repository-and-actor rooms this adapter has joined. Each
// selection owns a distinct lease, so all of them must be renewed and
// withdrawn independently.
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
				identity, err := s.revalidateSelection(ctx, current)
				if err != nil || identity.actor.Fingerprint != current.fingerprint {
					s.revokeLease(ctx, current)
					fmt.Fprintln(errorsTo, "gitseq-mcp: presence renewal stopped; selected identity is no longer valid")
					continue
				}
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
		if _, ok := current.endpoint(); !ok {
			continue
		}
		requestContext, cancel := context.WithTimeout(ctx, s.deadlineForShutdown())
		_, _ = s.post(requestContext, current, "/v0/presence/depart", map[string]any{"credential": current.credentialValue()})
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
	if !errors.As(err, &refusal) || refusal.Message != "credential is not valid" {
		return result, err
	}
	current.clearLease()
	if err := s.announce(ctx, current); err != nil {
		return nil, err
	}
	setRequestCredential(value, current.credentialValue())
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

// postForSessionBoundedJSON keeps the restart repair used by the other
// session routes without first decoding a bounded response into interface{}.
func (s *mcpServer) postForSessionBoundedJSON(ctx context.Context, current *room, path string, value any, limit int64, target any) error {
	err := s.postBoundedJSON(ctx, current, path, value, limit, target)
	var refusal *residentclient.HTTPError
	if !errors.As(err, &refusal) || refusal.Message != "credential is not valid" {
		return err
	}
	current.clearLease()
	if err := s.announce(ctx, current); err != nil {
		return err
	}
	setRequestCredential(value, current.credentialValue())
	return s.postBoundedJSON(ctx, current, path, value, limit, target)
}

func setRequestCredential(value any, credential string) {
	if request, ok := value.(map[string]any); ok {
		request["credential"] = credential
	}
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
		strings.Contains(refusal.Message, `unknown field "session"`) || strings.Contains(refusal.Message, `unknown field "credential"`)
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
	if path == "/v0/wait" || path == "/v0/actor-wait" {
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

// repo and agent select the adapter attachment. They are not part of any
// resident service request, whose strict decoders accept only the endpoint's
// own wire fields.
func residentArguments(input map[string]any) map[string]any {
	output := clone(input)
	delete(output, "repo")
	delete(output, "agent")
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
	arguments := clone(call.Arguments)
	delete(arguments, "repo")
	delete(arguments, "agent")
	if encoded, err := json.Marshal(arguments); err == nil {
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
		"credential": current.credentialValue(),
		"events":     attentionEvents(call, result),
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
