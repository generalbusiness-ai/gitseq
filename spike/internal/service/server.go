package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitseq/spike/internal/app"
	"gitseq/spike/internal/intent"
	"gitseq/spike/internal/kernel"
	"gitseq/spike/internal/nexus"
)

type Frontier struct {
	Genesis string `json:"genesis"`
	Head    string `json:"head"`
	Depth   int    `json:"depth"`
}

type Cursor struct {
	Frontier []Frontier   `json:"frontier"`
	Live     nexus.Cursor `json:"live"`
}

type Status struct {
	Durable app.Snapshot   `json:"durable"`
	Live    nexus.Snapshot `json:"live"`
	Cursor  Cursor         `json:"cursor"`
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
	workspace    *app.Workspace
	hub          *nexus.Hub
	mux          *http.ServeMux
	mu           sync.Mutex
	about        map[string]string
	sessions     map[string]string
	participants map[string]map[string]bool
}

func New(workspace *app.Workspace) (*Server, error) {
	hub, err := nexus.New(4096)
	if err != nil {
		return nil, err
	}
	server := &Server{
		workspace: workspace, hub: hub, mux: http.NewServeMux(),
		about: make(map[string]string), sessions: make(map[string]string), participants: make(map[string]map[string]bool),
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.Handle("GET /", uiHandler())
	s.mux.HandleFunc("GET /legacy", s.handleDemo)
	s.mux.HandleFunc("GET /v0/graph", s.handleGraph)
	s.mux.HandleFunc("GET /v0/actors", s.handleActors)
	s.mux.HandleFunc("POST /v0/act", s.handleAct)
	s.mux.HandleFunc("GET /v0/status", s.handleStatus)
	s.mux.HandleFunc("POST /v0/wait", s.handleWait)
	s.mux.HandleFunc("GET /v0/watch", s.handleWatch)
	s.mux.HandleFunc("POST /v0/submit", s.handleSubmit)
	s.mux.HandleFunc("GET /v0/presence", s.handlePresence)
	s.mux.HandleFunc("POST /v0/presence", s.handleAnnounce)
	s.mux.HandleFunc("DELETE /v0/presence/{session}", s.handleDepart)
	s.mux.HandleFunc("POST /v0/say", s.handleSay)
	s.mux.HandleFunc("GET /v0/conversations/{conversation}/frames", s.handleFrames)
}

func (s *Server) handleWatch(writer http.ResponseWriter, request *http.Request) {
	after, err := strconv.Atoi(request.URL.Query().Get("after_depth"))
	if err != nil || after < 0 {
		write(writer, nil, errors.New("after_depth must be a non-negative integer"))
		return
	}
	timeout := TimeoutFromQuery(request.URL.Query().Get("timeout_ms"))
	if timeout <= 0 || timeout > 30000 {
		timeout = 25000
	}
	deadline := time.NewTimer(time.Duration(timeout) * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := s.status(request.Context())
		if err != nil || status.Durable.Depth > after {
			write(writer, status, err)
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-deadline.C:
			write(writer, status, nil)
			return
		case <-ticker.C:
		}
	}
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

func (s *Server) handleWait(writer http.ResponseWriter, request *http.Request) {
	var input WaitRequest
	if err := decode(request, &input); err != nil {
		write(writer, nil, err)
		return
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
		status, err := s.status(request.Context())
		if err != nil {
			write(writer, nil, err)
			return
		}
		changes, _, liveErr := s.hub.ChangesSince(input.Cursor.Live)
		reset := errors.Is(liveErr, nexus.ErrReset)
		durableChanged := len(input.Cursor.Frontier) != 1 || input.Cursor.Frontier[0].Genesis != status.Durable.Genesis || input.Cursor.Frontier[0].Head != status.Durable.Head || input.Cursor.Frontier[0].Depth != status.Durable.Depth
		if reset || durableChanged || len(changes) > 0 {
			write(writer, WaitResponse{Status: status, LiveChanges: changes, Reset: reset}, nil)
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-deadline.C:
			write(writer, WaitResponse{Status: status}, nil)
			return
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
	result, err := s.workspace.Accept(request.Context(), submission)
	if err != nil {
		write(writer, nil, err)
		return
	}
	record, err := s.workspace.Record(request.Context(), result.Commit)
	write(writer, struct {
		Result kernel.Result `json:"result"`
		Record any           `json:"record"`
	}{Result: result, Record: record}, err)
}

func (s *Server) handlePresence(writer http.ResponseWriter, _ *http.Request) {
	write(writer, s.liveSnapshot(), nil)
}

type presenceRequest struct {
	Actor   string `json:"actor"`
	Session string `json:"session"`
	TTLMS   int    `json:"ttl_ms,omitempty"`
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
	// Expire and reconcile old leases before consulting the session binding.
	// Without this sweep, a crashed client can block reuse of its session ID by
	// a different actor until some unrelated status or presence read occurs.
	s.liveSnapshot()
	ttl := time.Duration(input.TTLMS) * time.Millisecond
	if ttl <= 0 || ttl > 2*time.Minute {
		ttl = 30 * time.Second
	}
	s.mu.Lock()
	if bound, exists := s.sessions[input.Session]; exists && bound != input.Actor {
		s.mu.Unlock()
		write(writer, nil, errors.New("session is already bound to another actor"))
		return
	}
	s.sessions[input.Session] = input.Actor
	change := s.hub.AnnounceFor(input.Session, actor.Name+" ("+actor.Fingerprint[:12]+")", ttl)
	s.mu.Unlock()
	write(writer, change, nil)
}

func (s *Server) handleDepart(writer http.ResponseWriter, request *http.Request) {
	session := request.PathValue("session")
	s.mu.Lock()
	forgotten := s.removeSessionLocked(session)
	change := s.hub.Depart(session)
	for _, conversation := range forgotten {
		s.hub.ForgetConversation(conversation)
	}
	s.mu.Unlock()
	write(writer, change, nil)
}

type sayRequest struct {
	Session      string `json:"session"`
	About        string `json:"about"`
	Conversation string `json:"conversation,omitempty"`
	Text         string `json:"text"`
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
	s.mu.Lock()
	actorName, exists := s.sessions[input.Session]
	if !exists || !s.hub.HasPresence(input.Session) {
		forgotten := s.removeSessionLocked(input.Session)
		for _, conversation := range forgotten {
			s.hub.ForgetConversation(conversation)
		}
		s.mu.Unlock()
		write(writer, nil, errors.New("session is not present"))
		return
	}
	_, private, err := s.workspace.Actor(actorName)
	if err != nil {
		s.mu.Unlock()
		write(writer, nil, err)
		return
	}
	conversation := input.Conversation
	if conversation == "" {
		conversation = s.about[input.About]
	}
	if conversation == "" {
		conversation, _, err = s.hub.OpenConversation()
		if err == nil {
			s.about[input.About] = conversation
		}
	}
	if err != nil {
		s.mu.Unlock()
		write(writer, nil, err)
		return
	}
	payload, _ := json.Marshal(map[string]string{"about": input.About, "text": input.Text})
	frame, err := s.hub.Publish(conversation, payload, private)
	if err == nil {
		if s.participants[conversation] == nil {
			s.participants[conversation] = make(map[string]bool)
		}
		s.participants[conversation][input.Session] = true
	}
	s.mu.Unlock()
	write(writer, frame, err)
}

func (s *Server) handleFrames(writer http.ResponseWriter, request *http.Request) {
	s.liveSnapshot()
	frames, err := s.hub.Frames(request.PathValue("conversation"))
	write(writer, frames, err)
}

func (s *Server) liveSnapshot() nexus.Snapshot {
	live := s.hub.Snapshot()
	s.mu.Lock()
	var forgotten []string
	for session := range s.sessions {
		if _, present := live.Presence[session]; !present {
			forgotten = append(forgotten, s.removeSessionLocked(session)...)
		}
	}
	for _, conversation := range forgotten {
		s.hub.ForgetConversation(conversation)
	}
	s.mu.Unlock()
	if len(forgotten) > 0 {
		live = s.hub.Snapshot()
	}
	return live
}

func (s *Server) removeSessionLocked(session string) []string {
	delete(s.sessions, session)
	var forgotten []string
	for conversation, participants := range s.participants {
		delete(participants, session)
		if len(participants) != 0 {
			continue
		}
		delete(s.participants, conversation)
		for about, candidate := range s.about {
			if candidate == conversation {
				delete(s.about, about)
			}
		}
		forgotten = append(forgotten, conversation)
	}
	return forgotten
}

func (s *Server) handleDemo(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write([]byte(demoHTML))
}

// guardMutation is the browser-facing boundary for state-changing calls:
// JSON content type only (text/plain is CORS-safelisted and needs no
// preflight), and when a browser identifies the request's provenance the
// origin must be this host and the fetch site same-origin. Non-browser
// clients (CLI, MCP) send neither header and pass.
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

func EncodeCursor(cursor Cursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeCursor(value string) (Cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, err
	}
	var cursor Cursor
	err = json.Unmarshal(data, &cursor)
	return cursor, err
}

func ActorFingerprintFromSubmission(submission kernel.Request) string {
	return intent.ActorFingerprint(submission.Signed.ActorKey)
}

func TimeoutFromQuery(value string) int {
	timeout, _ := strconv.Atoi(strings.TrimSpace(value))
	return timeout
}

const demoHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>gitseq workroom</title><style>
:root{color-scheme:dark;--ink:#f3f0e8;--muted:#a49f94;--line:#35332e;--hot:#f1b24a;--cool:#7fc8a9;--bad:#ef746f;background:#11110f}*{box-sizing:border-box}body{margin:0;font:15px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--ink);background:radial-gradient(circle at 80% 0,#292316 0,transparent 32rem),#11110f}main{max-width:1180px;margin:auto;padding:5rem 2rem}header{display:flex;justify-content:space-between;align-items:end;border-bottom:1px solid var(--line);padding-bottom:1.5rem}.eyebrow{color:var(--hot);text-transform:uppercase;letter-spacing:.16em;font-size:.75rem}h1{font:600 clamp(2.5rem,7vw,6rem)/.9 Georgia,serif;margin:.4rem 0}.pulse{color:var(--cool)}.grid{display:grid;grid-template-columns:1.5fr 1fr;gap:1rem;margin-top:2rem}.card{border:1px solid var(--line);background:#181815dd;padding:1.25rem;min-height:12rem}.card h2{font:500 1rem/1 ui-monospace;margin:0 0 1.25rem;color:var(--muted)}table{border-collapse:collapse;width:100%}th,td{text-align:left;padding:.6rem;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted);font-weight:400}.status{color:var(--hot)}.stale{color:var(--bad)}.presence{display:flex;flex-wrap:wrap;gap:.5rem}.actor{border:1px solid #355848;color:var(--cool);padding:.35rem .6rem}.empty{color:var(--muted)}code{color:var(--hot)}@media(max-width:760px){main{padding:2rem 1rem}.grid{grid-template-columns:1fr}header{display:block}}
</style></head><body><main><header><div><div class="eyebrow">append-only collaboration substrate</div><h1>gitseq<br>workroom</h1></div><div id="frontier" class="eyebrow">connecting…</div></header><div class="grid"><section class="card"><h2>commitment ledger</h2><div id="commitments"></div></section><section class="card"><h2>here now</h2><div id="presence" class="presence"></div></section><section class="card"><h2>artifact truth</h2><div id="artifacts"></div></section><section class="card"><h2>visible attempts</h2><div id="attempts"></div></section></div></main><script>
const esc=s=>String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));const short=s=>s&&s.length>20?s.slice(0,9)+'…'+s.slice(-8):s||'—';
async function refresh(){try{const r=await fetch('/v0/status',{cache:'no-store'}),s=await r.json(),p=s.durable.projection;frontier.textContent='depth '+s.durable.depth+' · '+short(s.durable.head)+' · live '+s.live.cursor.position;commitments.innerHTML=p.commitments.length?'<table><tr><th>state</th><th>requester → performer</th><th>waiting on</th></tr>'+p.commitments.map(c=>'<tr><td class="status">'+esc(c.status)+'</td><td>'+esc(short(c.requester))+' → '+esc(short(c.performer))+'<br><code>'+esc(short(c.request))+'</code></td><td>'+esc(short(c.waiting_on))+'</td></tr>').join('')+'</table>':'<span class="empty">No commitments yet.</span>';const people=Object.values(s.live.presence);presence.innerHTML=people.length?people.map(a=>'<span class="actor"><span class="pulse">●</span> '+esc(a)+'</span>').join(''):'<span class="empty">The nexus is cold. Durable state remains.</span>';artifacts.innerHTML=p.artifacts.length?'<table>'+p.artifacts.map(a=>'<tr><td class="'+(a.stale?'stale':'')+'">'+(a.stale?'STALE':'current')+'</td><td>'+esc(a.path)+'@<code>'+esc(short(a.commit))+'</code></td></tr>').join('')+'</table>':'<span class="empty">No bridged artifacts.</span>';const bad=p.decisions.filter(d=>d.verdict!=='effective');attempts.innerHTML=bad.length?bad.map(d=>'<p><span class="'+(d.verdict==='disputed'?'stale':'status')+'">'+esc(d.verdict)+'</span> <code>'+esc(short(d.event))+'</code><br>'+esc(d.reason)+'</p>').join(''):'<span class="empty">No ineffective or disputed acts.</span>'}catch(e){frontier.textContent='offline · durable data still in git'}}refresh();setInterval(refresh,1000);
</script></body></html>`
