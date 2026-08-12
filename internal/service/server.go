package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
)

type Frontier = statusview.Frontier
type Cursor = statusview.Cursor
type Orientation = statusview.Orientation
type WorkQuery = statusview.WorkQuery
type WorkPage = statusview.WorkPage
type InspectRequest = statusview.InspectRequest
type ItemInspection = statusview.ItemInspection

const (
	OrientationProjectionVersion = statusview.OrientationProjectionVersion
	InboxProtocolVersion         = "gitseq.addressed-inbox.v1"
)

type Status struct {
	Durable app.Snapshot   `json:"durable"`
	Live    nexus.Snapshot `json:"live"`
	Cursor  Cursor         `json:"cursor"`
	Inbox   *nexus.Inbox   `json:"inbox,omitempty"`
}

// SummaryStatus is the bounded resident response used by the default CLI.
// /v0/status remains the complete browser and audit projection.
type SummaryStatus struct {
	Durable statusview.Summary `json:"durable"`
	Live    nexus.Snapshot     `json:"live"`
	Cursor  Cursor             `json:"cursor"`
}

type WaitRequest struct {
	Cursor    Cursor `json:"cursor"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	Session   string `json:"session,omitempty"`
}

type WaitResponse struct {
	Status      Status         `json:"status"`
	LiveChanges []nexus.Change `json:"live_changes,omitempty"`
	Reset       bool           `json:"reset,omitempty"`
}

type Server struct {
	workspace *app.Workspace
	hub       *nexus.Hub
	mux       *http.ServeMux
	observer  observe.Observer
}

func New(workspace *app.Workspace) (*Server, error) {
	return NewObserved(workspace, nil)
}

// NewObserved adds bounded resident observations. Exporter configuration
// remains the responsibility of the command that composes the server.
func NewObserved(workspace *app.Workspace, observer observe.Observer) (*Server, error) {
	hub, err := nexus.New(4096)
	if err != nil {
		return nil, err
	}
	workspace.SetObserver(observer)
	server := &Server{workspace: workspace, hub: hub, mux: http.NewServeMux(), observer: observer}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return observe.HTTPHandler(s.observer, s.mux) }

func (s *Server) routes() {
	s.mux.Handle("GET /", uiHandler())
	s.mux.HandleFunc("GET /v0/graph", s.handleGraph)
	s.mux.HandleFunc("GET /v0/worktrees", s.handleWorktrees)
	s.mux.HandleFunc("GET /v0/actors", s.handleActors)
	s.mux.HandleFunc("GET /v0/orientation/{fingerprint}", s.handleOrientation)
	s.mux.HandleFunc("POST /v0/act", s.handleAct)
	s.mux.HandleFunc("GET /v0/status", s.handleStatus)
	s.mux.HandleFunc("POST /v0/status", s.handleSessionStatus)
	s.mux.HandleFunc("GET /v0/status-summary", s.handleStatusSummary)
	s.mux.HandleFunc("POST /v0/work-query", s.handleWorkQuery)
	s.mux.HandleFunc("POST /v0/inspect", s.handleInspect)
	s.mux.HandleFunc("POST /v0/wait", s.handleWait)
	s.mux.HandleFunc("POST /v0/submit", s.handleSubmit)
	s.mux.HandleFunc("GET /v0/presence", s.handlePresence)
	s.mux.HandleFunc("GET /v0/rebuild", s.handleRebuild)
	s.mux.HandleFunc("POST /v0/presence", s.handleAnnounce)
	s.mux.HandleFunc("DELETE /v0/presence/{session}", s.handleDepart)
	s.mux.HandleFunc("POST /v0/say", s.handleSay)
	s.mux.HandleFunc("POST /v0/inbox/register", s.handleInboxRegister)
	s.mux.HandleFunc("POST /v0/inbox/ack", s.handleInboxAck)
	s.mux.HandleFunc("POST /v0/attention", s.handleAttention)
	s.mux.HandleFunc("GET /v0/conversations/{conversation}/frames", s.handleFrames)
}

func (s *Server) handleOrientation(writer http.ResponseWriter, request *http.Request) {
	fingerprint := request.PathValue("fingerprint")
	if fingerprint == "" {
		write(writer, nil, errors.New("fingerprint is required"))
		return
	}
	snapshot, err := s.workspace.Snapshot(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	orientation, ok := statusview.BuildOrientation(snapshot, fingerprint, "")
	if !ok {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": "actor is not in the effective durable roster"})
		return
	}
	write(writer, orientation, nil)
}

func (s *Server) status(ctx context.Context) (Status, error) {
	observation, err := s.hub.Observe("", nil)
	if err != nil {
		return Status{}, err
	}
	return s.statusFromLive(ctx, observation, false)
}

func (s *Server) statusFromLive(ctx context.Context, observation nexus.Observation, includeInbox bool) (Status, error) {
	// Capture live first. A concurrent durable append is then either in the
	// durable snapshot or strictly beyond its returned frontier.
	durable, err := s.workspace.Snapshot(ctx)
	if err != nil {
		return Status{}, err
	}
	status := Status{Durable: durable, Live: observation.Snapshot, Cursor: Cursor{Frontier: []Frontier{{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}}, Live: observation.Snapshot.Cursor}}
	if includeInbox {
		inbox := observation.Inbox
		status.Inbox = &inbox
	}
	return status, nil
}

func (s *Server) handleStatus(writer http.ResponseWriter, request *http.Request) {
	status, err := s.status(request.Context())
	write(writer, status, err)
}

type sessionStatusRequest struct {
	Session string `json:"session"`
}

func (s *Server) handleSessionStatus(writer http.ResponseWriter, request *http.Request) {
	var input sessionStatusRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	if input.Session == "" {
		write(writer, nil, errors.New("session is required"))
		return
	}
	observation, err := s.hub.Observe(input.Session, nil)
	if err != nil {
		write(writer, nil, err)
		return
	}
	status, err := s.statusFromLive(request.Context(), observation, true)
	write(writer, status, err)
}

func (s *Server) handleStatusSummary(writer http.ResponseWriter, request *http.Request) {
	status, err := s.status(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	started := time.Now()
	summary := SummaryStatus{
		Durable: statusview.Build(status.Durable.Genesis, status.Durable.Head, status.Durable.Depth, status.Durable.Projection),
		Live:    status.Live, Cursor: status.Cursor,
	}
	if s.observer != nil {
		s.observer.Record(request.Context(), observe.Measurement{Operation: observe.OperationStatusView, Path: observe.PathStatusSummary, Outcome: observe.OutcomeOK, Duration: time.Since(started), Items: int64(status.Durable.Depth)})
	}
	write(writer, summary, nil)
}

func (s *Server) handleWorkQuery(writer http.ResponseWriter, request *http.Request) {
	var input WorkQuery
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	durable, err := s.workspace.Snapshot(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	page, err := statusview.BuildWorkPage(durable, input, false)
	write(writer, page, err)
}

func (s *Server) handleInspect(writer http.ResponseWriter, request *http.Request) {
	var input InspectRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	durable, err := s.workspace.Snapshot(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	inspection, err := statusview.BuildItemInspection(durable, input.Event, false)
	write(writer, inspection, err)
}

func (s *Server) handleWait(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	var input WaitRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	var response WaitResponse
	changed, err := Poll(request.Context(), input.TimeoutMS, func() (bool, error) {
		observation, err := s.hub.Observe(input.Session, &input.Cursor.Live)
		if err != nil {
			return false, err
		}
		status, err := s.statusFromLive(request.Context(), observation, input.Session != "")
		if err != nil {
			return false, err
		}
		response = WaitResponse{Status: status, LiveChanges: observation.Changes, Reset: observation.Reset}
		pending := status.Inbox != nil && len(status.Inbox.Frames) > 0
		return observation.Reset || DurableChanged(input.Cursor.Frontier, status.Durable) || len(observation.Changes) > 0 || pending, nil
	})
	if err != nil {
		if s.observer != nil {
			s.observer.Record(request.Context(), observe.Measurement{Operation: observe.OperationWait, Path: observe.PathLongPoll, Outcome: observe.Classify(request.Context(), err), Duration: time.Since(started), Items: 1})
		}
		if request.Context().Err() != nil {
			return
		}
		write(writer, nil, err)
		return
	}
	if s.observer != nil {
		s.observer.Record(request.Context(), observe.Measurement{Operation: observe.OperationWait, Path: observe.PathLongPoll, Outcome: observe.OutcomeOK, Duration: time.Since(started), Items: 1})
	}
	if !changed {
		response.LiveChanges = nil
		response.Reset = false
	}
	write(writer, response, nil)
}

func DurableChanged(frontier []Frontier, durable app.Snapshot) bool {
	return len(frontier) != 1 || frontier[0].Genesis != durable.Genesis || frontier[0].Head != durable.Head || frontier[0].Depth != durable.Depth
}

// Poll is the single wait clock used by composite service waits and durable-only
// degraded clients. It checks immediately and reports false on ordinary timeout.
func Poll(ctx context.Context, timeoutMS int, check func() (bool, error)) (bool, error) {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 25 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		done, err := check()
		if err != nil || done {
			return done, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func (s *Server) handleSubmit(writer http.ResponseWriter, request *http.Request) {
	var submission kernel.Request
	if err := decode(request, &submission); err != nil {
		write(writer, nil, err)
		return
	}
	result, err := s.workspace.AcceptSubmission(request.Context(), submission)
	if err != nil {
		write(writer, nil, err)
		return
	}
	write(writer, result, nil)
}

func (s *Server) handlePresence(writer http.ResponseWriter, _ *http.Request) {
	write(writer, s.liveSnapshot(), nil)
}

// rebuildReport says whether a cold verified rebuild is running and how far it
// has got. Running is false in the ordinary warm case, and a caller that reads
// it should stay quiet then rather than render a completed bar.
type rebuildReport struct {
	Running  bool `json:"running"`
	Verified int  `json:"verified,omitempty"`
	Total    int  `json:"total,omitempty"`
}

// handleRebuild answers while the rebuild it reports on is still running, which
// is the only reason it exists. Every other read here goes through Snapshot and
// therefore queues behind the audit — that is what made a cold start look like a
// broken page rather than a slow one. This one takes no lock at any layer.
//
// It deliberately serves no projection. On a fold-profile change the previous
// checkpoint is invalid by construction, so anything this binary could offer
// from before is unverified under a contract it no longer implements. Progress
// with no work data is the honest answer; a stale projection labelled current
// would not be.
func (s *Server) handleRebuild(writer http.ResponseWriter, _ *http.Request) {
	progress, running := s.workspace.RebuildProgress()
	write(writer, rebuildReport{Running: running, Verified: progress.Verified, Total: progress.Total}, nil)
}

type presenceRequest struct {
	Actor   string                `json:"actor"`
	Session string                `json:"session"`
	TTLMS   int                   `json:"ttl_ms,omitempty"`
	Status  *nexus.ActivityStatus `json:"status,omitempty"`
	Focus   *[]string             `json:"focus,omitempty"`
	Note    *string               `json:"note,omitempty"`
}

func (s *Server) handleAnnounce(writer http.ResponseWriter, request *http.Request) {
	var input presenceRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	actor, _, err := s.workspace.Actor(input.Actor)
	if err != nil || input.Session == "" {
		if err == nil {
			err = errors.New("session is required")
		}
		write(writer, nil, err)
		return
	}
	if input.Focus != nil {
		if len(*input.Focus) > nexus.MaxFocusEvents {
			write(writer, nil, errors.New("focus exceeds the eight-event limit"))
			return
		}
		snapshot, snapshotErr := s.workspace.Snapshot(request.Context())
		if snapshotErr != nil {
			write(writer, nil, snapshotErr)
			return
		}
		known := make(map[string]bool, len(snapshot.Projection.Decisions))
		for _, decision := range snapshot.Projection.Decisions {
			known[decision.Event] = true
		}
		for _, event := range *input.Focus {
			if !known[event] {
				write(writer, nil, errors.New("focus must contain only EventIDs from this workroom"))
				return
			}
		}
	}
	ttl := time.Duration(input.TTLMS) * time.Millisecond
	if ttl <= 0 || ttl > 2*time.Minute {
		ttl = 30 * time.Second
	}
	change, err := s.hub.AnnounceSessionIdentity(input.Session, input.Actor, actor.Fingerprint, actor.Name+" ("+actor.Fingerprint[:12]+")", ttl, nexus.ActivityUpdate{
		Status: input.Status, Focus: input.Focus, Note: input.Note,
	})
	write(writer, change, err)
}

func (s *Server) handleDepart(writer http.ResponseWriter, request *http.Request) {
	session := request.PathValue("session")
	change := s.hub.Depart(session)
	write(writer, change, nil)
}

type sayRequest struct {
	Session      string `json:"session"`
	About        string `json:"about"`
	Conversation string `json:"conversation,omitempty"`
	Text         string `json:"text"`
	InboxVersion string `json:"inbox_version,omitempty"`
	// Re threads a frame under an exact earlier one. The nexus validates it and
	// signs the parent's author into the final recipient list.
	Re string `json:"re,omitempty"`
}

var mentionPattern = regexp.MustCompile(`@(?:"([^"]+)"|([A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?))`)

func addressedRecipients(text string, snapshot app.Snapshot) []string {
	byName := make(map[string][]string)
	for fingerprint, actor := range snapshot.Projection.Actors {
		if actor.Retired || !containsString(actor.Roles, "participant") {
			continue
		}
		name := strings.ToLower(actor.Name)
		byName[name] = append(byName[name], fingerprint)
	}
	recipients := make(map[string]bool)
	for _, name := range mentionNames(text) {
		matches := byName[strings.ToLower(name)]
		if len(matches) == 1 {
			recipients[matches[0]] = true
		}
	}
	result := make([]string, 0, len(recipients))
	for fingerprint := range recipients {
		result = append(result, fingerprint)
	}
	sort.Strings(result)
	return result
}

func mentionNames(text string) []string {
	var names []string
	for _, match := range mentionPattern.FindAllStringSubmatchIndex(text, -1) {
		if !mentionBoundaryBefore(text, match[0]) || !mentionBoundaryAfter(text, match[1]) {
			continue
		}
		start, end := match[2], match[3]
		if start < 0 {
			start, end = match[4], match[5]
		}
		names = append(names, text[start:end])
	}
	return names
}

// HasMentionToken reports whether text contains the same bounded mention
// syntax that addressedRecipients resolves. Callers use it only to select the
// versioned transport; roster membership and ambiguity are still resolved by
// the service immediately before publication.
func HasMentionToken(text string) bool {
	return len(mentionNames(text)) > 0
}

func mentionBoundaryBefore(text string, index int) bool {
	if index == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:index])
	return unicode.IsSpace(r) || strings.ContainsRune("([{<,;:!?", r)
}

func mentionBoundaryAfter(text string, index int) bool {
	if index == len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[index:])
	return unicode.IsSpace(r) || strings.ContainsRune(")]}>.,;:!?", r)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Server) handleSay(writer http.ResponseWriter, request *http.Request) {
	var input sayRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	if input.InboxVersion != "" && input.InboxVersion != InboxProtocolVersion {
		write(writer, nil, errors.New("unsupported inbox protocol version"))
		return
	}
	if input.Session == "" || input.About == "" || input.Text == "" {
		write(writer, nil, errors.New("session, about, and text are required"))
		return
	}
	actorName, exists := s.hub.SessionActor(input.Session)
	if !exists {
		write(writer, nil, errors.New("session is not present"))
		return
	}
	_, private, err := s.workspace.Actor(actorName)
	if err != nil {
		write(writer, nil, err)
		return
	}
	snapshot, err := s.workspace.Snapshot(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	frame, err := s.hub.PublishMessageForSession(input.Session, input.Conversation, nexus.Message{
		About: input.About, Text: input.Text, Re: input.Re,
		Recipients: addressedRecipients(input.Text, snapshot),
	}, private)
	write(writer, frame, err)
}

type inboxAckRequest struct {
	Session string   `json:"session"`
	Threads []string `json:"threads"`
}

type inboxRegisterRequest struct {
	Session string `json:"session"`
	Version string `json:"version"`
}

func (s *Server) handleInboxRegister(writer http.ResponseWriter, request *http.Request) {
	var input inboxRegisterRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	if input.Session == "" {
		write(writer, nil, errors.New("session is required"))
		return
	}
	if input.Version != InboxProtocolVersion {
		write(writer, nil, errors.New("unsupported inbox protocol version"))
		return
	}
	err := s.hub.EnableInbox(input.Session)
	write(writer, map[string]string{"version": InboxProtocolVersion}, err)
}

func (s *Server) handleInboxAck(writer http.ResponseWriter, request *http.Request) {
	var input inboxAckRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	if input.Session == "" {
		write(writer, nil, errors.New("session is required"))
		return
	}
	acknowledged, err := s.hub.Acknowledge(input.Session, input.Threads)
	write(writer, map[string]int{"acknowledged": acknowledged}, err)
}

func (s *Server) handleFrames(writer http.ResponseWriter, request *http.Request) {
	s.liveSnapshot()
	frames, err := s.hub.Frames(request.PathValue("conversation"))
	write(writer, frames, err)
}

func (s *Server) liveSnapshot() nexus.Snapshot {
	return s.hub.Snapshot()
}

// guardMutation is the browser-facing boundary for state-changing calls:
// JSON content type only (text/plain is CORS-safelisted and needs no
// preflight), and when a browser identifies the request's provenance the
// origin must be this host and the fetch site same-origin. Non-browser
// clients send neither provenance header and pass.
func guardMutation(request *http.Request) error {
	if contentType := request.Header.Get("Content-Type"); request.Method != http.MethodDelete && !strings.HasPrefix(contentType, "application/json") {
		return errors.New("mutations require application/json")
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		if parsed, err := url.Parse(origin); err != nil || parsed.Host != request.Host {
			return errors.New("cross-origin mutation refused")
		}
	}
	if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return errors.New("cross-site mutation refused")
	}
	return nil
}

func decode(request *http.Request, target any) error {
	if err := guardMutation(request); err != nil {
		return err
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func write(writer http.ResponseWriter, value any, err error) {
	started := time.Now()
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
		recordEncoding(writer, started, err)
		return
	}
	encodeErr := json.NewEncoder(writer).Encode(value)
	recordEncoding(writer, started, encodeErr)
}

func recordEncoding(writer http.ResponseWriter, started time.Time, err error) {
	ctx, observer := observe.ResponseObserver(writer)
	if observer != nil {
		observer.Record(ctx, observe.Measurement{Operation: observe.OperationEncode, Path: observe.PathNone, Outcome: observe.Classify(ctx, err), Duration: time.Since(started), Items: 1})
	}
}

// attentionRequest asks what the caller should notice alongside its own result.
// Events are the exact durable identifiers the calling tool named or returned;
// they are matched by equality and nothing is inferred from them.
type attentionRequest struct {
	Session string   `json:"session"`
	Events  []string `json:"events,omitempty"`
}

// AttentionReport is advisory throughout. Every field is leased or ephemeral,
// and none of it confers ownership, obligation, authority, completion, or a
// durable read receipt. A client that ignores it entirely loses nothing but
// awareness.
type AttentionReport struct {
	// Available says the resident answered. A false value is the honest
	// degraded answer and never an error: the durable operation this rides
	// alongside has already happened.
	Available bool          `json:"available"`
	Cursor    *nexus.Cursor `json:"cursor,omitempty"`
	// Frames are this session's unacknowledged addressed messages, bounded.
	Frames []nexus.InboxFrame `json:"frames,omitempty"`
	// Pending is how many are unacknowledged in total and Omitted how many of
	// those are not in Frames, so a truncated list says so rather than reading
	// as the whole of it.
	Pending int `json:"pending"`
	Omitted int `json:"omitted,omitempty"`
	// Actors are live actors whose leased focus names one of the given events.
	Actors        []nexus.AttentionActor `json:"actors,omitempty"`
	OmittedActors int                    `json:"omitted_actors,omitempty"`
}

// handleAttention reports live attention for one session.
//
// It fails soft by construction. A session that is not present, or that never
// registered an inbox, still gets an answer: the actor half of the question is
// about other people's leases and does not depend on the caller holding one.
// The alternative — refusing the whole read because half of it is unavailable —
// would make an advisory adjunct behave like a precondition.
func (s *Server) handleAttention(writer http.ResponseWriter, request *http.Request) {
	var input attentionRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	s.liveSnapshot()
	report := AttentionReport{Available: true}
	if input.Session != "" {
		if snapshot, inbox, err := s.hub.SnapshotForSession(input.Session); err == nil {
			cursor := snapshot.Cursor
			report.Cursor = &cursor
			report.Frames = inbox.Frames
			report.Pending = len(inbox.Frames) + inbox.Skipped
			report.Omitted = inbox.Skipped
		}
	}
	report.Actors, report.OmittedActors = s.hub.FocusedOn(input.Session, input.Events)
	write(writer, report, nil)
}
