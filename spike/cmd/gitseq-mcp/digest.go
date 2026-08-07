package main

// The MCP surface answers a different question from the HTTP surface. A
// browser and an offline auditor want the whole projection; an agent wants to
// know what is waiting on it and what has changed since it last looked.
// Handing an agent the full projection is not merely wasteful — it grows with
// the log, so the cost of asking "anything for me?" rises forever while the
// answer stays roughly the same size.
//
// Nothing here narrows the record. /v0/status still serves the complete
// projection, the UI still reads it, and a fresh clone still audits from the
// log itself. These types are a view for one actor, and every one of them
// carries totals so a reader can tell what was summarised rather than guess.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gitseq/spike/internal/nexus"
	"gitseq/spike/internal/service"
	"gitseq/spike/internal/workroom"
)

// deltaCap bounds how many events a single wait may enumerate. A cursor that
// has fallen a long way behind gets the most recent window and an honest count
// of what it skipped, rather than the whole history under a different name.
const deltaCap = 50

// textCap bounds quoted act text. Full text stays one status call away by
// event id; what an agent needs here is enough to recognise the act.
const textCap = 240

type actorView struct {
	Name        string   `json:"name"`
	Fingerprint string   `json:"fingerprint"`
	Roles       []string `json:"roles,omitempty"`
}

type commitmentView struct {
	Request   string `json:"request"`
	Status    string `json:"status"`
	Requester string `json:"requester"`
	Performer string `json:"performer,omitempty"`
	Text      string `json:"text,omitempty"`
	Promise   string `json:"promise,omitempty"`
	Report    string `json:"report,omitempty"`
}

type eventView struct {
	Event   string `json:"event"`
	Actor   string `json:"actor"`
	Kind    string `json:"kind,omitempty"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
	Text    string `json:"text,omitempty"`
}

// totals exist so that summarising never becomes hiding. Whatever a view
// omits, these say how much was omitted.
type totals struct {
	Depth            int            `json:"depth"`
	Commitments      map[string]int `json:"commitments,omitempty"`
	Artifacts        int            `json:"artifacts"`
	StaleArtifacts   int            `json:"stale_artifacts"`
	IneffectiveActs  int            `json:"ineffective_acts"`
	Statements       int            `json:"statements"`
	FullProjectionAt string         `json:"full_projection_at"`
}

type liveView struct {
	Generation string   `json:"generation,omitempty"`
	Position   uint64   `json:"position,omitempty"`
	Present    []string `json:"present,omitempty"`
	Degraded   bool     `json:"degraded,omitempty"`
}

type actorStatus struct {
	You            actorView          `json:"you"`
	Frontier       []service.Frontier `json:"frontier"`
	Cursor         service.Cursor     `json:"cursor"`
	WaitingOnYou   []commitmentView   `json:"waiting_on_you"`
	YouAreWaiting  []commitmentView   `json:"you_are_waiting_on"`
	YourAttention  []eventView        `json:"needs_your_attention,omitempty"`
	Totals         totals             `json:"totals"`
	Live           liveView           `json:"live"`
	FollowWithWait string             `json:"follow_with_wait"`
}

type waitDelta struct {
	Cursor       service.Cursor   `json:"cursor"`
	Reset        bool             `json:"reset,omitempty"`
	Durable      []eventView      `json:"durable,omitempty"`
	Skipped      int              `json:"durable_skipped,omitempty"`
	WaitingOnYou []commitmentView `json:"waiting_on_you,omitempty"`
	Live         []nexus.Change   `json:"live,omitempty"`
	Totals       totals           `json:"totals"`
}

func truncate(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= textCap {
		return text
	}
	return text[:textCap] + "…"
}

// name renders a fingerprint as the actor's name where the roster knows one,
// falling back to the fingerprint so an unknown principal is never silently
// blank.
func name(projection workroom.Projection, fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	if actor, ok := projection.Actors[fingerprint]; ok && actor.Name != "" {
		return actor.Name
	}
	return fingerprint
}

func statementIndex(projection workroom.Projection) map[string]workroom.Statement {
	index := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		index[statement.Event] = statement
	}
	return index
}

func viewCommitment(projection workroom.Projection, statements map[string]workroom.Statement, commitment workroom.Commitment) commitmentView {
	view := commitmentView{
		Request:   commitment.Request,
		Status:    commitment.Status,
		Requester: name(projection, commitment.Requester),
		Performer: name(projection, commitment.Performer),
		Promise:   commitment.Promise,
		Report:    commitment.Report,
	}
	if statement, ok := statements[commitment.Request]; ok {
		view.Text = truncate(statement.Text)
	}
	return view
}

func summarizeTotals(snapshot workroom.Projection, depth int) totals {
	counts := map[string]int{}
	for _, commitment := range snapshot.Commitments {
		counts[commitment.Status]++
	}
	stale := 0
	for _, artifact := range snapshot.Artifacts {
		if artifact.Stale {
			stale++
		}
	}
	ineffective := 0
	for _, decision := range snapshot.Decisions {
		if decision.Verdict != workroom.Effective {
			ineffective++
		}
	}
	return totals{
		Depth:            depth,
		Commitments:      counts,
		Artifacts:        len(snapshot.Artifacts),
		StaleArtifacts:   stale,
		IneffectiveActs:  ineffective,
		Statements:       len(snapshot.Statements),
		FullProjectionAt: "GET /v0/status, or gs status --repo <path>",
	}
}

func viewLive(status service.Status, degraded bool) liveView {
	view := liveView{Degraded: degraded}
	if degraded {
		return view
	}
	view.Generation = status.Live.Cursor.Generation
	view.Position = status.Live.Cursor.Position
	// Presence is keyed by session, and one actor may hold several. Who is
	// here is a question about people, so collapse the sessions.
	seen := make(map[string]bool, len(status.Live.Presence))
	for _, who := range status.Live.Presence {
		if seen[who] {
			continue
		}
		seen[who] = true
		view.Present = append(view.Present, who)
	}
	sort.Strings(view.Present)
	return view
}

// digestStatus answers "what is my situation" for one actor. The commitment
// lists are the actionable part: what is waiting on me, and what I am waiting
// on someone else for. Attention carries my own acts the fold judged
// ineffective, because an act that silently failed to take force is the thing
// an agent is least likely to notice and most needs to.
func digestStatus(status service.Status, fingerprint, actorName string, degraded bool) actorStatus {
	projection := status.Durable.Projection
	statements := statementIndex(projection)
	digest := actorStatus{
		You:            actorView{Name: actorName, Fingerprint: fingerprint},
		Cursor:         status.Cursor,
		Totals:         summarizeTotals(projection, status.Durable.Depth),
		Live:           viewLive(status, degraded),
		FollowWithWait: "pass cursor back to wait to receive only what changes after it",
	}
	digest.Frontier = status.Cursor.Frontier
	if actor, ok := projection.Actors[fingerprint]; ok {
		digest.You.Roles = actor.Roles
		if actor.Name != "" {
			digest.You.Name = actor.Name
		}
	}
	for _, commitment := range projection.Commitments {
		if commitment.Status == "satisfied" || commitment.Status == "withdrawn" {
			continue
		}
		switch {
		case commitment.WaitingOn == fingerprint:
			digest.WaitingOnYou = append(digest.WaitingOnYou, viewCommitment(projection, statements, commitment))
		case commitment.Requester == fingerprint && commitment.WaitingOn != "":
			digest.YouAreWaiting = append(digest.YouAreWaiting, viewCommitment(projection, statements, commitment))
		}
	}
	for _, decision := range projection.Decisions {
		if decision.Verdict == workroom.Effective {
			continue
		}
		statement, ok := statements[decision.Event]
		if !ok || statement.Actor != fingerprint {
			continue
		}
		digest.YourAttention = append(digest.YourAttention, eventView{
			Event:   decision.Event,
			Actor:   actorName,
			Kind:    string(statement.Kind),
			Verdict: string(decision.Verdict),
			Reason:  decision.Reason,
			Text:    truncate(statement.Text),
		})
	}
	return digest
}

// digestWait answers "what changed since I last looked". The caller's frontier
// depth is the cut: events at or beyond it are new to them. A cursor that has
// fallen further behind than deltaCap gets the most recent window plus a count
// of what it missed, so the omission is stated rather than silent.
func digestWait(response service.WaitResponse, requested service.Cursor, fingerprint, actorName string, degraded bool) waitDelta {
	status := response.Status
	projection := status.Durable.Projection
	statements := statementIndex(projection)
	delta := waitDelta{
		Cursor: status.Cursor,
		Reset:  response.Reset,
		Live:   response.LiveChanges,
		Totals: summarizeTotals(projection, status.Durable.Depth),
	}
	from := 0
	for _, frontier := range requested.Frontier {
		if frontier.Genesis == "" || frontier.Genesis == status.Durable.Genesis {
			from = frontier.Depth
		}
	}
	decisions := projection.Decisions
	if from < 0 || from > len(decisions) {
		from = len(decisions)
	}
	fresh := decisions[from:]
	if len(fresh) > deltaCap {
		delta.Skipped = len(fresh) - deltaCap
		fresh = fresh[len(fresh)-deltaCap:]
	}
	for _, decision := range fresh {
		view := eventView{Event: decision.Event, Verdict: string(decision.Verdict), Reason: decision.Reason}
		if statement, ok := statements[decision.Event]; ok {
			view.Actor = name(projection, statement.Actor)
			view.Kind = string(statement.Kind)
			view.Text = truncate(statement.Text)
		}
		delta.Durable = append(delta.Durable, view)
	}
	for _, commitment := range projection.Commitments {
		if commitment.WaitingOn != fingerprint {
			continue
		}
		if commitment.Status == "satisfied" || commitment.Status == "withdrawn" {
			continue
		}
		delta.WaitingOnYou = append(delta.WaitingOnYou, viewCommitment(projection, statements, commitment))
	}
	if degraded {
		delta.Cursor.Live = nexus.Cursor{Generation: "degraded"}
	}
	return delta
}

// summarize is what the MCP result carries as human-readable text. It
// deliberately does not restate the structured payload: repeating a large
// object as pretty-printed JSON alongside itself doubled every response for no
// added information.
func summarize(tool string, value any) string {
	switch shaped := value.(type) {
	case actorStatus:
		return fmt.Sprintf(
			"depth %d, %d waiting on you, %d you are waiting on, %d of your acts ineffective; live %s",
			shaped.Totals.Depth, len(shaped.WaitingOnYou), len(shaped.YouAreWaiting), len(shaped.YourAttention),
			liveLabel(shaped.Live))
	case waitDelta:
		reset := ""
		if shaped.Reset {
			reset = ", live reset"
		}
		skipped := ""
		if shaped.Skipped > 0 {
			skipped = fmt.Sprintf(", %d older events omitted", shaped.Skipped)
		}
		return fmt.Sprintf("depth %d, %d new durable events%s%s, %d waiting on you",
			shaped.Totals.Depth, len(shaped.Durable), skipped, reset, len(shaped.WaitingOnYou))
	default:
		return tool + " ok"
	}
}

func liveLabel(live liveView) string {
	if live.Degraded {
		return "degraded"
	}
	if len(live.Present) == 0 {
		return "nobody present"
	}
	return strings.Join(live.Present, ", ")
}

// fingerprint resolves this process's configured actor to the identity the
// projection speaks in. An unconfigured actor yields the empty string, which
// simply matches nothing rather than matching everyone.
func (s *mcpServer) fingerprint() string {
	return s.workspace.Config.Actors[s.actor].Fingerprint
}

func (s *mcpServer) digest(status service.Status, degraded bool) actorStatus {
	return digestStatus(status, s.fingerprint(), s.actor, degraded)
}

// remarshal re-decodes a value the HTTP path returned as generic JSON into the
// typed shape the digest needs. The service and the adapter already share
// these types; this only bridges the untyped hop through the wire.
func remarshal(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

// requestedCursor recovers the caller's cursor from the tool arguments so the
// delta knows where they had already read to. A missing or malformed cursor
// means "I have seen nothing", which yields the most recent window rather than
// an error.
func requestedCursor(arguments map[string]any) service.Cursor {
	var cursor service.Cursor
	raw, ok := arguments["cursor"]
	if !ok {
		return cursor
	}
	if err := remarshal(raw, &cursor); err != nil {
		return service.Cursor{}
	}
	return cursor
}
