package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/kernel"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/statusview"
)

type Frontier = statusview.Frontier
type Cursor = statusview.Cursor
type Orientation = statusview.Orientation
type WorkQuery = statusview.WorkQuery
type WorkPage = statusview.WorkPage
type InspectRequest = statusview.InspectRequest
type ItemInspection = statusview.ItemInspection

const OrientationProjectionVersion = statusview.OrientationProjectionVersion

type Status struct {
	Durable app.Snapshot   `json:"durable"`
	Live    nexus.Snapshot `json:"live"`
	Cursor  Cursor         `json:"cursor"`
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
}

func New(workspace *app.Workspace) (*Server, error) {
	hub, err := nexus.New(4096)
	if err != nil {
		return nil, err
	}
	server := &Server{workspace: workspace, hub: hub, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.Handle("GET /", uiHandler())
	s.mux.HandleFunc("GET /legacy", s.handleDemo)
	s.mux.HandleFunc("GET /v0/graph", s.handleGraph)
	s.mux.HandleFunc("GET /v0/worktrees", s.handleWorktrees)
	s.mux.HandleFunc("GET /v0/actors", s.handleActors)
	s.mux.HandleFunc("GET /v0/orientation/{fingerprint}", s.handleOrientation)
	s.mux.HandleFunc("POST /v0/act", s.handleAct)
	s.mux.HandleFunc("GET /v0/status", s.handleStatus)
	s.mux.HandleFunc("GET /v0/status-summary", s.handleStatusSummary)
	s.mux.HandleFunc("POST /v0/work-query", s.handleWorkQuery)
	s.mux.HandleFunc("POST /v0/inspect", s.handleInspect)
	s.mux.HandleFunc("POST /v0/wait", s.handleWait)
	s.mux.HandleFunc("POST /v0/submit", s.handleSubmit)
	s.mux.HandleFunc("GET /v0/presence", s.handlePresence)
	s.mux.HandleFunc("POST /v0/presence", s.handleAnnounce)
	s.mux.HandleFunc("DELETE /v0/presence/{session}", s.handleDepart)
	s.mux.HandleFunc("POST /v0/say", s.handleSay)
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
	// Capture live first. A concurrent durable append is then either in the
	// durable snapshot or strictly beyond its returned frontier.
	live := s.liveSnapshot()
	durable, err := s.workspace.Snapshot(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{Durable: durable, Live: live, Cursor: Cursor{Frontier: []Frontier{{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}}, Live: live.Cursor}}, nil
}

func (s *Server) handleStatus(writer http.ResponseWriter, request *http.Request) {
	status, err := s.status(request.Context())
	write(writer, status, err)
}

func (s *Server) handleStatusSummary(writer http.ResponseWriter, request *http.Request) {
	status, err := s.status(request.Context())
	if err != nil {
		write(writer, nil, err)
		return
	}
	summary := SummaryStatus{
		Durable: statusview.Build(status.Durable.Genesis, status.Durable.Head, status.Durable.Depth, status.Durable.Projection),
		Live:    status.Live, Cursor: status.Cursor,
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
	var input WaitRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
	}
	var response WaitResponse
	changed, err := Poll(request.Context(), input.TimeoutMS, func() (bool, error) {
		status, err := s.status(request.Context())
		if err != nil {
			return false, err
		}
		changes, _, liveErr := s.hub.ChangesSince(input.Cursor.Live)
		reset := errors.Is(liveErr, nexus.ErrReset)
		response = WaitResponse{Status: status, LiveChanges: changes, Reset: reset}
		return reset || DurableChanged(input.Cursor.Frontier, status.Durable) || len(changes) > 0, nil
	})
	if err != nil {
		if request.Context().Err() != nil {
			return
		}
		write(writer, nil, err)
		return
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
	change, err := s.hub.AnnounceSessionActivity(input.Session, input.Actor, actor.Name+" ("+actor.Fingerprint[:12]+")", ttl, nexus.ActivityUpdate{
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
	// Re threads a frame under an earlier one, as "<conversation>:<sequence>".
	// It remains a payload annotation; the nexus sequences replies normally.
	Re string `json:"re,omitempty"`
}

func (s *Server) handleSay(writer http.ResponseWriter, request *http.Request) {
	var input sayRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
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
	body := map[string]string{"about": input.About, "text": input.Text}
	if input.Re != "" {
		body["re"] = input.Re
	}
	payload, _ := json.Marshal(body)
	frame, err := s.hub.PublishForSession(input.Session, input.About, input.Conversation, payload, private)
	write(writer, frame, err)
}

func (s *Server) handleFrames(writer http.ResponseWriter, request *http.Request) {
	s.liveSnapshot()
	frames, err := s.hub.Frames(request.PathValue("conversation"))
	write(writer, frames, err)
}

func (s *Server) liveSnapshot() nexus.Snapshot {
	return s.hub.Snapshot()
}

func (s *Server) handleDemo(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write([]byte(demoHTML))
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
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(writer).Encode(value)
}

const demoHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>gitseq workroom</title><style>
:root{color-scheme:dark;--ink:#f3f0e8;--muted:#a49f94;--line:#35332e;--hot:#f1b24a;--cool:#7fc8a9;--bad:#ef746f;background:#11110f}*{box-sizing:border-box}body{margin:0;font:15px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--ink);background:radial-gradient(circle at 80% 0,#292316 0,transparent 32rem),#11110f}main{max-width:1180px;margin:auto;padding:5rem 2rem}header{display:flex;justify-content:space-between;align-items:end;border-bottom:1px solid var(--line);padding-bottom:1.5rem}.eyebrow{color:var(--hot);text-transform:uppercase;letter-spacing:.16em;font-size:.75rem}h1{font:600 clamp(2.5rem,7vw,6rem)/.9 Georgia,serif;margin:.4rem 0}.pulse{color:var(--cool)}.grid{display:grid;grid-template-columns:1.5fr 1fr;gap:1rem;margin-top:2rem}.card{border:1px solid var(--line);background:#181815dd;padding:1.25rem;min-height:12rem}.card h2{font:500 1rem/1 ui-monospace;margin:0 0 1.25rem;color:var(--muted)}table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:.6rem;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted);font-weight:400}.status{color:var(--hot)}.stale{color:var(--bad)}.presence{display:flex;flex-wrap:wrap;gap:.5rem}.actor{border:1px solid #355848;color:var(--cool);padding:.35rem .6rem}.empty{color:var(--muted)}code{color:var(--hot)}@media(max-width:760px){main{padding:2rem 1rem}.grid{grid-template-columns:1fr}header{display:block}}
</style></head><body><main><header><div><div class="eyebrow">append-only collaboration substrate</div><h1>gitseq<br>workroom</h1></div><div id="frontier" class="eyebrow">connecting…</div></header><div class="grid"><section class="card"><h2>requests and commitments</h2><div id="commitments"></div></section><section class="card"><h2>here now</h2><div id="presence" class="presence"></div></section><section class="card"><h2>artifact truth</h2><div id="artifacts"></div></section><section class="card"><h2>visible attempts</h2><div id="attempts"></div></section></div></main><script>
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));const short=s=>s&&s.length>20?s.slice(0,9)+'…'+s.slice(-8):s||'—';
const relationship=c=>!c.promise&&c.addressed_to?'addressed to '+short(c.addressed_to)+' · unclaimed':c.performer?short(c.requester)+' → '+short(c.performer):'—';
const waiting=c=>c.waiting_on?'waiting on '+short(c.waiting_on):'—';
async function refresh(){try{const r=await fetch('/v0/status',{cache:'no-store'}),s=await r.json(),p=s.durable.projection;frontier.textContent='depth '+s.durable.depth+' · '+short(s.durable.head)+' · live '+s.live.cursor.position;commitments.innerHTML=p.commitments.length?'<table><tr><th>state</th><th>qualifiers</th><th>relationship</th><th>waiting</th></tr>'+p.commitments.map(c=>'<tr><td class="status">'+esc(c.status)+'</td><td class="'+(c.stale?'stale':'')+'">'+(c.stale?'stale':'')+'</td><td>'+esc(relationship(c))+'<br><code>'+esc(short(c.request))+'</code></td><td>'+esc(waiting(c))+'</td></tr>').join('')+'</table>':'<span class="empty">No commitments yet.</span>';const people=Object.values(s.live.presence);presence.innerHTML=people.length?people.map(a=>'<span class="actor"><span class="pulse">●</span> '+esc(a)+'</span>').join(''):'<span class="empty">The nexus is cold. Durable state remains.</span>';artifacts.innerHTML=p.artifacts.length?'<table>'+p.artifacts.map(a=>'<tr><td class="'+(a.stale?'stale':'')+'">'+(a.stale?'STALE':'current')+'</td><td>'+esc(a.path)+'@<code>'+esc(short(a.commit))+'</code></td></tr>').join('')+'</table>':'<span class="empty">No bridged artifacts.</span>';const bad=p.decisions.filter(d=>d.verdict!=='effective');attempts.innerHTML=bad.length?bad.map(d=>'<p><span class="'+(d.verdict==='disputed'?'stale':'status')+'">'+esc(d.verdict)+'</span> <code>'+esc(short(d.event))+'</code><br>'+esc(d.reason)+'</p>').join(''):'<span class="empty">No ineffective or disputed acts.</span>'}catch(e){frontier.textContent='offline · durable data still in git'}}refresh();setInterval(refresh,1000);
</script></body></html>`
