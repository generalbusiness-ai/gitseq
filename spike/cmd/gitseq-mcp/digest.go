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

// listCap bounds every per-actor list in these views. Two of them grow with
// the log rather than with open work and would otherwise be unbounded:
// terminal commitments (stale, reneged, cancelled) are never discharged and
// never leave, and acts the fold judged ineffective accumulate the same way.
// Leaving those uncapped would reintroduce, in a narrower dimension, the very
// cost this view exists to avoid. The actionable lists are capped on the same
// terms so that no list here can grow without a stated count of what it
// dropped.
const listCap = 20

// capList keeps the most recent entries and reports how many older ones it
// dropped. Recency is the right end to keep: the fold appends in admission
// order, so the tail is what the actor has least likely already seen.
func capList[T any](items []T, limit int) ([]T, int) {
	if len(items) <= limit {
		return items, 0
	}
	return items[len(items)-limit:], len(items) - limit
}

// capListHead keeps the first entries instead of the last, and exists for
// lists the fold has already sorted into a meaningful order rather than
// appended in time order. Roles are sorted alphabetically, so "most recent"
// means nothing for them, while the conventional names a reader is looking
// for sort early.
func capListHead[T any](items []T, limit int) ([]T, int) {
	if len(items) <= limit {
		return items, 0
	}
	return items[:limit], len(items) - limit
}

// actorView carries the caller's own standing. Roles is capped like every
// other list here: role names are not a closed vocabulary — a roster
// statement may confer any name — so an actor's role set grows with what the
// log has granted them and is not bounded by anything the substrate fixes.
type actorView struct {
	Name         string   `json:"name"`
	Fingerprint  string   `json:"fingerprint"`
	Roles        []string `json:"roles,omitempty"`
	RolesSkipped int      `json:"roles_skipped,omitempty"`
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
	Target  string `json:"target,omitempty"`
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
	DisputedActs     int            `json:"disputed_acts"`
	Statements       int            `json:"statements"`
	FullProjectionAt string         `json:"full_projection_at"`
}

type liveView struct {
	Generation string   `json:"generation,omitempty"`
	Position   uint64   `json:"position,omitempty"`
	Present    []string `json:"present,omitempty"`
	Degraded   bool     `json:"degraded,omitempty"`
}

// actionableStatuses is an allow-list on purpose. A commitment that is
// waiting is one someone can still act on; anything else — satisfied,
// withdrawn, reneged, or stale because its basis was retired — is history or
// needs a decision, not a queue entry. An allow-list also fails safe: a status
// added later stays out of the actionable lists until someone decides it
// belongs, rather than silently appearing as work.
var actionableStatuses = map[string]bool{"requested": true, "promised": true, "reported": true}

// involves decides whether a commitment is any of this actor's business.
// Membership must not be read from WaitingOn: the fold clears it for reneged
// and cancelled commitments, so keying on it made exactly the terminal states
// a party most needs to hear about unreachable.
func involves(commitment workroom.Commitment, fingerprint string) bool {
	return commitment.Requester == fingerprint ||
		commitment.Performer == fingerprint ||
		commitment.WaitingOn == fingerprint
}

// Every list here carries its own skipped count rather than a single shared
// one, because the lists are bounded independently and a reader needs to know
// which of them was shortened. Totals still give the whole-projection figures,
// so a skipped count is about this view and not about the record.
type actorStatus struct {
	You                  actorView          `json:"you"`
	Frontier             []service.Frontier `json:"frontier"`
	Cursor               service.Cursor     `json:"cursor"`
	WaitingOnYou         []commitmentView   `json:"waiting_on_you"`
	WaitingOnYouSkipped  int                `json:"waiting_on_you_skipped,omitempty"`
	YouAreWaiting        []commitmentView   `json:"you_are_waiting_on"`
	YouAreWaitingSkipped int                `json:"you_are_waiting_on_skipped,omitempty"`
	NotActionable        []commitmentView   `json:"not_actionable,omitempty"`
	NotActionableSkipped int                `json:"not_actionable_skipped,omitempty"`
	YourAttention        []eventView        `json:"needs_your_attention,omitempty"`
	YourAttentionSkipped int                `json:"needs_your_attention_skipped,omitempty"`
	Totals               totals             `json:"totals"`
	Live                 liveView           `json:"live"`
	FollowWithWait       string             `json:"follow_with_wait"`
}

// waitDelta mixes two kinds of thing, and the distinction matters. Durable is
// a true delta: it is cut at the caller's frontier and contains only what was
// admitted at or after it. The commitment lists are not — they are current
// actor-scoped state, restated so that a caller waking from a long wait knows
// what is on its plate without a second round trip. The fold records no depth
// at which a commitment's status changed, so a commitment-level delta is not
// computable here; saying so is better than implying a cut that does not
// exist.
type waitDelta struct {
	Cursor                      service.Cursor   `json:"cursor"`
	Reset                       bool             `json:"reset,omitempty"`
	Durable                     []eventView      `json:"durable,omitempty"`
	Skipped                     int              `json:"durable_skipped,omitempty"`
	CurrentWaitingOnYou         []commitmentView `json:"current_waiting_on_you,omitempty"`
	CurrentWaitingSkipped       int              `json:"current_waiting_on_you_skipped,omitempty"`
	CurrentNotActionable        []commitmentView `json:"current_not_actionable,omitempty"`
	CurrentNotActionableSkipped int              `json:"current_not_actionable_skipped,omitempty"`
	Live                        []nexus.Change   `json:"live,omitempty"`
	Totals                      totals           `json:"totals"`
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

func actIndex(projection workroom.Projection) map[string]workroom.Act {
	index := make(map[string]workroom.Act, len(projection.Acts))
	for _, act := range projection.Acts {
		index[act.Event] = act
	}
	return index
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
	// Ineffective and disputed are different verdicts and the fold keeps them
	// apart deliberately; collapsing them here would erase that distinction in
	// the one view most readers see.
	ineffective, disputed := 0, 0
	for _, decision := range snapshot.Decisions {
		switch decision.Verdict {
		case workroom.Ineffective:
			ineffective++
		case workroom.Disputed:
			disputed++
		}
	}
	return totals{
		Depth:            depth,
		Commitments:      counts,
		Artifacts:        len(snapshot.Artifacts),
		StaleArtifacts:   stale,
		IneffectiveActs:  ineffective,
		DisputedActs:     disputed,
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
		digest.You.Roles, digest.You.RolesSkipped = capListHead(actor.Roles, listCap)
		if actor.Name != "" {
			digest.You.Name = actor.Name
		}
	}
	for _, commitment := range projection.Commitments {
		if !involves(commitment, fingerprint) || commitment.Status == "satisfied" || commitment.Status == "withdrawn" {
			continue
		}
		view := viewCommitment(projection, statements, commitment)
		if !actionableStatuses[commitment.Status] {
			// Still open, but nobody can discharge it as it stands. Reporting a
			// stale review as work waiting on someone is the projection lying in
			// the quiet direction, so it is shown apart rather than counted in.
			digest.NotActionable = append(digest.NotActionable, view)
			continue
		}
		if commitment.WaitingOn == fingerprint {
			digest.WaitingOnYou = append(digest.WaitingOnYou, view)
		} else if commitment.WaitingOn != "" {
			digest.YouAreWaiting = append(digest.YouAreWaiting, view)
		}
	}
	// An act that failed to take force is what an actor is least likely to
	// notice, so this must cover every kind of act they can make. Statements
	// carry state kinds; ratify and supersede live in Acts, and joining only
	// through statements would silently hide exactly the authority failures an
	// agent most needs to see.
	acts := actIndex(projection)
	for _, decision := range projection.Decisions {
		if decision.Verdict == workroom.Effective {
			continue
		}
		view := eventView{Event: decision.Event, Verdict: string(decision.Verdict), Reason: decision.Reason}
		switch {
		case statements[decision.Event].Actor == fingerprint:
			statement := statements[decision.Event]
			view.Kind, view.Text = string(statement.Kind), truncate(statement.Text)
		case acts[decision.Event].Actor == fingerprint:
			act := acts[decision.Event]
			view.Kind, view.Text = act.Type, truncate(act.Text)
			view.Target = act.Target
		default:
			continue
		}
		view.Actor = actorName
		digest.YourAttention = append(digest.YourAttention, view)
	}
	// Bound last, once every list is assembled, so the caps apply to what the
	// actor would actually have seen rather than to an arbitrary prefix of the
	// scan.
	digest.WaitingOnYou, digest.WaitingOnYouSkipped = capList(digest.WaitingOnYou, listCap)
	digest.YouAreWaiting, digest.YouAreWaitingSkipped = capList(digest.YouAreWaiting, listCap)
	digest.NotActionable, digest.NotActionableSkipped = capList(digest.NotActionable, listCap)
	digest.YourAttention, digest.YourAttentionSkipped = capList(digest.YourAttention, listCap)
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
	// Ratify and supersede are not statements. Joining only through statements
	// left them blank in the delta, so a failed ratification arriving after the
	// cursor was unrecognisable — the very thing a delta is for.
	acts := actIndex(projection)
	for _, decision := range fresh {
		view := eventView{Event: decision.Event, Verdict: string(decision.Verdict), Reason: decision.Reason}
		if statement, ok := statements[decision.Event]; ok {
			view.Actor = name(projection, statement.Actor)
			view.Kind = string(statement.Kind)
			view.Text = truncate(statement.Text)
		} else if act, ok := acts[decision.Event]; ok {
			view.Actor = name(projection, act.Actor)
			view.Kind = act.Type
			view.Target = act.Target
			view.Text = truncate(act.Text)
		}
		delta.Durable = append(delta.Durable, view)
	}
	for _, commitment := range projection.Commitments {
		if !involves(commitment, fingerprint) || commitment.Status == "satisfied" || commitment.Status == "withdrawn" {
			continue
		}
		view := viewCommitment(projection, statements, commitment)
		switch {
		case !actionableStatuses[commitment.Status]:
			// Current state, not a cut: this is every terminal commitment the
			// actor is party to, whenever it turned. A caller that wants only
			// what turned since its cursor must read Durable, which is the
			// part that is genuinely cut at the frontier.
			delta.CurrentNotActionable = append(delta.CurrentNotActionable, view)
		case commitment.WaitingOn == fingerprint:
			delta.CurrentWaitingOnYou = append(delta.CurrentWaitingOnYou, view)
		}
	}
	// Terminal commitments never leave the projection, so this list grows with
	// the log until it is bounded.
	delta.CurrentWaitingOnYou, delta.CurrentWaitingSkipped = capList(delta.CurrentWaitingOnYou, listCap)
	delta.CurrentNotActionable, delta.CurrentNotActionableSkipped = capList(delta.CurrentNotActionable, listCap)
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
		// A count that silently means "capped at 20" would be the summary
		// lying, so every shortened list says so in the one line most readers
		// actually read.
		return fmt.Sprintf(
			"depth %d, %s waiting on you, %s you are waiting on, %s not actionable, %s of your acts did not take force; live %s",
			shaped.Totals.Depth,
			shown(len(shaped.WaitingOnYou), shaped.WaitingOnYouSkipped),
			shown(len(shaped.YouAreWaiting), shaped.YouAreWaitingSkipped),
			shown(len(shaped.NotActionable), shaped.NotActionableSkipped),
			shown(len(shaped.YourAttention), shaped.YourAttentionSkipped),
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
		return fmt.Sprintf("depth %d, %d new durable events%s%s; currently %s waiting on you, %s not actionable",
			shaped.Totals.Depth, len(shaped.Durable), skipped, reset,
			shown(len(shaped.CurrentWaitingOnYou), shaped.CurrentWaitingSkipped),
			shown(len(shaped.CurrentNotActionable), shaped.CurrentNotActionableSkipped))
	default:
		return tool + " ok"
	}
}

// shown renders a capped list's size honestly: bare when nothing was dropped,
// and "20 of 63" when it was.
func shown(listed, skipped int) string {
	if skipped == 0 {
		return fmt.Sprintf("%d", listed)
	}
	return fmt.Sprintf("%d of %d", listed, listed+skipped)
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
