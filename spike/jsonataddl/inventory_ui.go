package jsonataddl

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/generalbusiness-ai/gitseq/host"
)

const maxUIRequestBytes = 32 << 10

// InventoryUI is the deliberately small same-origin HTTP surface for spike
// three. It owns one disposable projection at a time and swaps it only after
// replay reaches the verified log frontier.
type InventoryUI struct {
	mu         sync.Mutex
	workspace  *host.Workspace
	signer     ed25519.PrivateKey
	credential string
	directory  string
	generation uint64
	profile    *Profile
	projection *Projection
	changed    chan struct{}
}

type applicationMetadata struct {
	Application  applicationIdentity      `json:"application"`
	SchemaDigest string                   `json:"schema_digest"`
	Events       map[string]eventMetadata `json:"events"`
	Tables       map[string]tableMetadata `json:"tables"`
	Views        []string                 `json:"views"`
	Frontier     Frontier                 `json:"frontier"`
}

type applicationIdentity struct {
	Name        string `json:"name"`
	FoldVersion string `json:"fold_version"`
	SourceURL   string `json:"source_url,omitempty"`
}

type eventMetadata struct {
	Fields []string `json:"fields"`
}

type tableMetadata struct {
	Columns    []columnMetadata `json:"columns"`
	PrimaryKey []string         `json:"primary_key"`
}

type columnMetadata struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type queryRequest struct {
	SQL              string    `json:"sql"`
	Parameters       []any     `json:"parameters"`
	ExpectedFrontier *Frontier `json:"expected_frontier,omitempty"`
}

type eventRequest struct {
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type eventResponse struct {
	EventID  string   `json:"event_id"`
	Decision string   `json:"decision"`
	Frontier Frontier `json:"frontier"`
}

// NewInventoryUI builds the first projection and returns an HTTP handler. The
// credential protects use of signer custody; it is never included in metadata
// or static assets.
func NewInventoryUI(ctx context.Context, workspace *host.Workspace, signer ed25519.PrivateKey, databaseDirectory, credential string) (*InventoryUI, error) {
	if workspace == nil {
		return nil, errors.New("workspace is required")
	}
	if len(signer) != ed25519.PrivateKeySize {
		return nil, errors.New("signer must be an ed25519 private key")
	}
	if len(credential) < 16 {
		return nil, errors.New("session credential must be at least 16 bytes")
	}
	if databaseDirectory == "" {
		return nil, errors.New("database directory is required")
	}
	profile, err := LoadInventory()
	if err != nil {
		return nil, err
	}
	log, err := workspace.Records(ctx)
	if err != nil {
		return nil, err
	}
	ui := &InventoryUI{
		workspace: workspace, signer: signer, credential: credential,
		directory: databaseDirectory, profile: profile, changed: make(chan struct{}),
	}
	projection, err := Build(ctx, profile, log, ui.databasePath())
	if err != nil {
		if projection != nil {
			projection.Close()
		}
		return nil, err
	}
	ui.projection = projection
	return ui, nil
}

func (ui *InventoryUI) databasePath() string {
	path := filepath.Join(ui.directory, fmt.Sprintf("inventory-%06d.sqlite", ui.generation))
	ui.generation++
	return path
}

func (ui *InventoryUI) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/" && request.Method == http.MethodGet:
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(inventoryHTML))
	case request.URL.Path == "/api/metadata" && request.Method == http.MethodGet:
		ui.serveMetadata(writer)
	case request.URL.Path == "/api/query" && request.Method == http.MethodPost:
		ui.serveQuery(writer, request)
	case request.URL.Path == "/api/events" && request.Method == http.MethodPost:
		ui.serveEvent(writer, request)
	case request.URL.Path == "/api/wait" && request.Method == http.MethodGet:
		ui.serveWait(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (ui *InventoryUI) serveMetadata(writer http.ResponseWriter) {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	writeJSON(writer, http.StatusOK, ui.metadataLocked())
}

func (ui *InventoryUI) metadataLocked() applicationMetadata {
	metadata := applicationMetadata{
		Application: applicationIdentity{
			Name: ui.profile.Application.Name, FoldVersion: ui.profile.Application.FoldVersion, SourceURL: ui.profile.Application.SourceURL,
		},
		SchemaDigest: ui.profile.SchemaDigest,
		Events:       make(map[string]eventMetadata), Tables: make(map[string]tableMetadata),
		Views: append([]string(nil), ui.projection.views...), Frontier: ui.projection.Frontier(),
	}
	for name, event := range ui.profile.events {
		metadata.Events[name] = eventMetadata{Fields: append([]string(nil), event.columns...)}
	}
	for name, table := range ui.projection.tables {
		item := tableMetadata{PrimaryKey: append([]string(nil), table.primary...)}
		for _, column := range table.columns {
			item.Columns = append(item.Columns, columnMetadata{Name: column.name, Type: column.declaredType})
		}
		metadata.Tables[name] = item
	}
	return metadata
}

func (ui *InventoryUI) serveQuery(writer http.ResponseWriter, request *http.Request) {
	var input queryRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()
	frontier := ui.projection.Frontier()
	if input.ExpectedFrontier != nil && !sameFrontier(*input.ExpectedFrontier, frontier) {
		writeJSON(writer, http.StatusConflict, map[string]any{"error": "projection frontier moved", "frontier": frontier})
		return
	}
	queryContext, cancel := context.WithTimeout(request.Context(), 250*time.Millisecond)
	defer cancel()
	result, err := ui.projection.Query(queryContext, input.SQL, input.Parameters...)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (ui *InventoryUI) serveEvent(writer http.ResponseWriter, request *http.Request) {
	wanted := "Bearer " + ui.credential
	provided := request.Header.Get("Authorization")
	if len(provided) != len(wanted) || subtle.ConstantTimeCompare([]byte(provided), []byte(wanted)) != 1 {
		writeJSON(writer, http.StatusUnauthorized, map[string]string{"error": "valid session credential required"})
		return
	}
	var input eventRequest
	if !decodeRequest(writer, request, &input) {
		return
	}
	definition, exists := ui.profile.events[input.EventType]
	if !exists {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "unknown event type"})
		return
	}
	if input.IdempotencyKey == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "idempotency_key is required"})
		return
	}
	if _, err := decodeEvent(definition, input.Payload); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	ui.mu.Lock()
	defer ui.mu.Unlock()
	record, err := ui.workspace.Append(request.Context(), ui.signer, host.Act{
		Schema: input.EventType, Payload: input.Payload, IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log, err := ui.workspace.Records(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	next, err := Build(request.Context(), ui.profile, log, ui.databasePath())
	if err != nil {
		if next != nil {
			next.Close()
		}
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "event appended but projection did not reach it: " + err.Error()})
		return
	}
	decision, err := next.Query(request.Context(), `SELECT decision FROM gitseq_decisions WHERE event_id = ?`, record.ID)
	if err != nil || len(decision.Rows) != 1 || len(decision.Rows[0]) != 1 {
		next.Close()
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "projection omitted the submitted event decision"})
		return
	}
	old := ui.projection
	ui.projection = next
	close(ui.changed)
	ui.changed = make(chan struct{})
	_ = old.Close()
	writeJSON(writer, http.StatusOK, eventResponse{EventID: record.ID, Decision: fmt.Sprint(decision.Rows[0][0]), Frontier: next.Frontier()})
}

// serveWait waits for the projection's verified frontier, not merely for an
// append notification. Registering the channel while holding the same mutex as
// the comparison prevents the usual check-then-sleep missed wake-up.
func (ui *InventoryUI) serveWait(writer http.ResponseWriter, request *http.Request) {
	after := request.URL.Query().Get("after")
	if after == "" {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "after frontier is required"})
		return
	}
	timeout := 10 * time.Second
	if raw := request.URL.Query().Get("timeout_ms"); raw != "" {
		milliseconds, err := strconv.Atoi(raw)
		if err != nil || milliseconds < 1 || milliseconds > 30000 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "timeout_ms must be between 1 and 30000"})
			return
		}
		timeout = time.Duration(milliseconds) * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		ui.mu.Lock()
		frontier := ui.projection.Frontier()
		changed := ui.changed
		ui.mu.Unlock()
		if frontier.VerifiedHead != after {
			writeJSON(writer, http.StatusOK, frontier)
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-timer.C:
			writer.Header().Set("Gitseq-Frontier", frontier.VerifiedHead)
			writer.WriteHeader(http.StatusNoContent)
			return
		case <-changed:
		}
	}
}

func sameFrontier(left, right Frontier) bool {
	return left.Genesis == right.Genesis && left.VerifiedHead == right.VerifiedHead &&
		left.VerifiedDepth == right.VerifiedDepth && left.InterpretedEvent == right.InterpretedEvent &&
		left.InterpretedPosition == right.InterpretedPosition && left.Complete == right.Complete &&
		left.GapEvent == right.GapEvent && left.GapReason == right.GapReason
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, target any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxUIRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "request must contain exactly one JSON value"})
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// Close releases the current read pool. Disposable database files remain as
// crash evidence for the spike caller to remove with its enclosing directory.
func (ui *InventoryUI) Close() error {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.projection.Close()
}

const inventoryHTML = `<!doctype html>
<html lang="en"><meta charset="utf-8"><title>Inventory projection spike</title>
<style>body{font:16px system-ui;max-width:60rem;margin:2rem auto;padding:0 1rem}textarea,input,select,button{font:inherit}textarea{width:100%;height:8rem}pre{background:#f4f4f4;padding:1rem;overflow:auto}</style>
<h1>Inventory projection</h1>
<p id="identity">Discovering application schema…</p>
<label>Session credential <input id="credential" type="password"></label>
<label>Event <select id="event"></select></label>
<textarea id="payload">{"id":"stock-1","sku":"ink","qty":5}</textarea>
<button id="submit">Submit, wait, and query</button>
<p id="status"></p><pre id="rows"></pre>
<script>
let metadata;
const query = async expected => {
  const response = await fetch('/api/query',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({sql:'SELECT sku, available FROM stock ORDER BY sku',parameters:[],expected_frontier:expected})});
  if(!response.ok) throw new Error(await response.text());
  rows.textContent=JSON.stringify(await response.json(),null,2);
};
const discover = async () => {
  metadata=await fetch('/api/metadata').then(r=>r.json());
  identity.textContent=metadata.application.name+' · '+metadata.application.fold_version+' · '+metadata.frontier.verified_head;
  for(const [name,schema] of Object.entries(metadata.events)){const option=document.createElement('option');option.value=name;option.textContent=name+' ('+schema.fields.join(', ')+')';event.append(option)}
  await query(metadata.frontier);
};
submit.onclick=async()=>{try{
  status.textContent='Submitting…'; const before=metadata.frontier;
  const response=await fetch('/api/events',{method:'POST',headers:{'content-type':'application/json','authorization':'Bearer '+credential.value},body:JSON.stringify({event_type:event.value,payload:JSON.parse(payload.value),idempotency_key:crypto.randomUUID()})});
  if(!response.ok) throw new Error(await response.text());
  status.textContent='Waiting for the projection frontier…';
  const waited=await fetch('/api/wait?after='+encodeURIComponent(before.verified_head)+'&timeout_ms=10000');
  if(waited.status===204) throw new Error('frontier did not advance within 10 seconds');
  if(!waited.ok) throw new Error(await waited.text());
  metadata=await fetch('/api/metadata').then(r=>r.json()); await query(metadata.frontier); status.textContent='Projection advanced and query re-ran.';
}catch(error){status.textContent=String(error)}};
discover().catch(error=>status.textContent=String(error));
</script></html>`

var _ http.Handler = (*InventoryUI)(nil)
