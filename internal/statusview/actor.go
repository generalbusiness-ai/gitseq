package statusview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/nexus"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const DeltaCap = 50

type Frontier struct {
	Genesis string `json:"genesis"`
	Head    string `json:"head"`
	Depth   int    `json:"depth"`
}

type Cursor struct {
	Frontier []Frontier   `json:"frontier"`
	Live     nexus.Cursor `json:"live"`
}

type ActorView struct {
	Name            string   `json:"name"`
	Fingerprint     string   `json:"fingerprint"`
	Kind            string   `json:"kind,omitempty"`
	MembershipEvent string   `json:"membership_event,omitempty"`
	Roles           []string `json:"roles,omitempty"`
	RolesSkipped    int      `json:"roles_skipped,omitempty"`
}

// OrientationProjectionVersion binds a resident orientation answer to the
// exact actor projection semantics understood by its client.
const OrientationProjectionVersion = "statusview-orientation@1"

// Orientation is the bounded effective identity answer needed by a fresh
// client. You reuses the capped actor view exposed by status; membership is
// exact, while any omitted non-semantic roles are counted explicitly.
type Orientation struct {
	You               ActorView `json:"you"`
	Frontier          Frontier  `json:"frontier"`
	ProjectionVersion string    `json:"projection_version"`
}

type CommitmentView struct {
	Request     string `json:"request"`
	Status      string `json:"status"`
	AddressedTo string `json:"addressed_to,omitempty"`
	// Stale qualifies Status; it never replaces it. See statusview.Commitment.
	// The lanes no longer reopen a closed commitment for it: ActorTotals
	// .StaleCommitments counts it per status instead.
	Stale     bool   `json:"stale,omitempty"`
	Requester string `json:"requester"`
	Performer string `json:"performer,omitempty"`
	Text      string `json:"text,omitempty"`
	Promise   string `json:"promise,omitempty"`
	Report    string `json:"report,omitempty"`
	WorkDetails
}

type EventView struct {
	Event   string `json:"event"`
	Actor   string `json:"actor"`
	Kind    string `json:"kind,omitempty"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
	Target  string `json:"target,omitempty"`
	Text    string `json:"text,omitempty"`
}

type ActorTotals struct {
	Depth       int            `json:"depth"`
	Commitments map[string]int `json:"commitments,omitempty"`
	// StaleCommitments counts, per status, how many carry the stale qualifier.
	// This is where ordinary staleness on closed commitments is reported: the
	// lanes above hold work still owed, and satisfied or withdrawn rows are
	// counted here rather than listed.
	StaleCommitments map[string]int `json:"stale_commitments,omitempty"`
	Artifacts        int            `json:"artifacts"`
	// StaleArtifacts counts every artifact that is not current. The two facts
	// inside it a reader has to act on are counted separately, because
	// ordinary staleness reaches nearly all of them.
	StaleArtifacts      int    `json:"stale_artifacts"`
	RetiredArtifacts    int    `json:"retired_artifacts"`
	WorldStaleArtifacts int    `json:"world_stale_artifacts"`
	IneffectiveActs     int    `json:"ineffective_acts"`
	DisputedActs        int    `json:"disputed_acts"`
	Statements          int    `json:"statements"`
	FullProjectionAt    string `json:"full_projection_at"`
}

type LiveView struct {
	Generation string   `json:"generation,omitempty"`
	Position   uint64   `json:"position,omitempty"`
	Present    []string `json:"present,omitempty"`
	Degraded   bool     `json:"degraded,omitempty"`
}

type AddressedFrame struct {
	Actor        string   `json:"actor"`
	ActorName    string   `json:"actor_name,omitempty"`
	Text         string   `json:"text"`
	About        string   `json:"about"`
	Conversation string   `json:"conversation"`
	Sequence     uint64   `json:"sequence"`
	Re           string   `json:"re,omitempty"`
	Recipients   []string `json:"recipients"`
	Thread       string   `json:"thread"`
}

type InboxView struct {
	Available bool             `json:"available"`
	Frames    []AddressedFrame `json:"frames"`
	Skipped   int              `json:"skipped,omitempty"`
}

type ActorStatus struct {
	You                   ActorView        `json:"you"`
	Frontier              []Frontier       `json:"frontier"`
	Cursor                Cursor           `json:"cursor"`
	AvailableToYou        []CommitmentView `json:"available_to_you"`
	AvailableToYouSkipped int              `json:"available_to_you_skipped,omitempty"`
	WaitingOnYou          []CommitmentView `json:"waiting_on_you"`
	WaitingOnYouSkipped   int              `json:"waiting_on_you_skipped,omitempty"`
	YouAreWaiting         []CommitmentView `json:"you_are_waiting_on"`
	YouAreWaitingSkipped  int              `json:"you_are_waiting_on_skipped,omitempty"`
	NotActionable         []CommitmentView `json:"not_actionable,omitempty"`
	NotActionableSkipped  int              `json:"not_actionable_skipped,omitempty"`
	YourAttention         []EventView      `json:"needs_your_attention,omitempty"`
	YourAttentionSkipped  int              `json:"needs_your_attention_skipped,omitempty"`
	Totals                ActorTotals      `json:"totals"`
	Live                  LiveView         `json:"live"`
	PriorityChat          InboxView        `json:"priority_ephemeral_chat"`
	FollowWithWait        string           `json:"follow_with_wait"`
}

type WaitDelta struct {
	Cursor                      Cursor           `json:"cursor"`
	Reset                       bool             `json:"reset,omitempty"`
	Durable                     []EventView      `json:"durable,omitempty"`
	Skipped                     int              `json:"durable_skipped,omitempty"`
	CurrentAvailableToYou       []CommitmentView `json:"current_available_to_you,omitempty"`
	CurrentAvailableToSkipped   int              `json:"current_available_to_you_skipped,omitempty"`
	CurrentWaitingOnYou         []CommitmentView `json:"current_waiting_on_you,omitempty"`
	CurrentWaitingSkipped       int              `json:"current_waiting_on_you_skipped,omitempty"`
	CurrentNotActionable        []CommitmentView `json:"current_not_actionable,omitempty"`
	CurrentNotActionableSkipped int              `json:"current_not_actionable_skipped,omitempty"`
	Live                        []nexus.Change   `json:"live,omitempty"`
	PriorityChat                InboxView        `json:"priority_ephemeral_chat"`
	Totals                      ActorTotals      `json:"totals"`
}

var semanticRoles = map[string]bool{"operator": true, "participant": true, "ratifier": true}

func CapRoles(roles []string, limit int) ([]string, int) {
	if len(roles) <= limit {
		kept := append([]string(nil), roles...)
		for index := range kept {
			kept[index] = Text(kept[index])
		}
		return kept, 0
	}
	kept := make([]string, 0, limit)
	seen := make(map[string]bool, limit)
	for _, role := range roles {
		if len(kept) < limit && semanticRoles[role] {
			kept = append(kept, role)
			seen[role] = true
		}
	}
	for _, role := range roles {
		if len(kept) >= limit {
			break
		}
		if !seen[role] {
			kept = append(kept, role)
			seen[role] = true
		}
	}
	for index := range kept {
		kept[index] = Text(kept[index])
	}
	return kept, len(roles) - len(kept)
}

func statementIndex(projection workroom.Projection) map[string]workroom.Statement {
	index := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		index[statement.Event] = statement
	}
	return index
}

func actIndex(projection workroom.Projection) map[string]workroom.Act {
	index := make(map[string]workroom.Act, len(projection.Acts))
	for _, act := range projection.Acts {
		index[act.Event] = act
	}
	return index
}

func viewCommitment(projection workroom.Projection, commitment workroom.Commitment) CommitmentView {
	return CommitmentView{
		Request: commitment.Request, Status: commitment.Status, Stale: commitment.Stale,
		AddressedTo: Text(ActorName(projection, commitment.AddressedTo)),
		Requester:   Text(ActorName(projection, commitment.Requester)), Performer: Text(ActorName(projection, commitment.Performer)),
		Promise: commitment.Promise, Report: commitment.Report,
	}
}

// fillCommitmentDetails scans history for only the rows that survived their
// caps. Building event-sized indexes merely to label at most twenty rows per
// lane would make a bounded response allocate in proportion to the whole log.
func fillCommitmentDetails(projection workroom.Projection, groups ...[]CommitmentView) {
	var targets []workRowTarget
	for _, group := range groups {
		for index := range group {
			view := &group[index]
			targets = append(targets, workRowTarget{Request: view.Request, Report: view.Report, Status: view.Status,
				Text: &view.Text, Details: &view.WorkDetails})
		}
	}
	enrichWorkRows(projection, targets)
}

func actorTotals(projection workroom.Projection, depth int) ActorTotals {
	counts, staleCounts := make(map[string]int), make(map[string]int)
	for _, commitment := range projection.Commitments {
		counts[commitment.Status]++
		if commitment.Stale {
			staleCounts[commitment.Status]++
		}
	}
	stale, retired, world, ineffective, disputed := 0, 0, 0, 0, 0
	for _, artifact := range projection.Artifacts {
		if artifact.Retired || artifact.Stale {
			stale++
		}
		if artifact.Retired {
			retired++
		}
		if artifact.DescribesSupersededWorld {
			world++
		}
	}
	for _, decision := range projection.Decisions {
		switch decision.Verdict {
		case workroom.Ineffective:
			ineffective++
		case workroom.Disputed:
			disputed++
		}
	}
	return ActorTotals{Depth: depth, Commitments: counts, StaleCommitments: staleCounts, Artifacts: len(projection.Artifacts), StaleArtifacts: stale,
		RetiredArtifacts: retired, WorldStaleArtifacts: world,
		IneffectiveActs: ineffective, DisputedActs: disputed, Statements: len(projection.Statements),
		FullProjectionAt: "GET /v0/status, gs status --all, gs status --json, or work with stale=include"}
}

func actorLive(live nexus.Snapshot, degraded bool) LiveView {
	view := LiveView{Degraded: degraded}
	if degraded {
		return view
	}
	view.Generation, view.Position = live.Cursor.Generation, live.Cursor.Position
	seen := make(map[string]bool, len(live.Presence))
	for _, who := range live.Presence {
		if !seen[who] {
			seen[who] = true
			view.Present = append(view.Present, Text(who))
		}
	}
	sort.Strings(view.Present)
	return view
}

func involves(commitment workroom.Commitment, fingerprint string) bool {
	return commitment.Requester == fingerprint || commitment.AddressedTo == fingerprint || commitment.Performer == fingerprint || commitment.WaitingOn == fingerprint
}

// addressedTo identifies the request-shaped work an actor may claim. The fold
// deliberately leaves Performer and WaitingOn empty until a promise takes
// force; putting these requests in WaitingOnYou would invent a commitment.
func addressedTo(commitment workroom.Commitment, fingerprint string) bool {
	return commitment.Status == "open" && commitment.AddressedTo == fingerprint && commitment.Promise == "" && commitment.Performer == "" && commitment.WaitingOn == ""
}

func inboxView(projection workroom.Projection, inbox *nexus.Inbox, degraded bool) InboxView {
	view := InboxView{Available: !degraded, Frames: []AddressedFrame{}}
	if degraded || inbox == nil {
		return view
	}
	view.Skipped = inbox.Skipped
	for _, frame := range inbox.Frames {
		actorName := ActorName(projection, frame.Actor)
		view.Frames = append(view.Frames, AddressedFrame{
			Actor: frame.Actor, ActorName: Text(actorName), Text: frame.Text, About: frame.About,
			Conversation: frame.Conversation, Sequence: frame.Sequence, Re: frame.Re,
			Recipients: append([]string(nil), frame.Recipients...), Thread: frame.Thread,
		})
	}
	return view
}

func actorAttention(projection workroom.Projection, fingerprint, actorName string) []EventView {
	nonEffective := make(map[string]workroom.Decision)
	for _, decision := range projection.Decisions {
		if decision.Verdict != workroom.Effective {
			nonEffective[decision.Event] = decision
		}
	}
	if len(nonEffective) == 0 {
		return nil
	}
	byEvent := make(map[string]EventView)
	for _, statement := range projection.Statements {
		decision, wanted := nonEffective[statement.Event]
		if !wanted || statement.Actor != fingerprint {
			continue
		}
		byEvent[statement.Event] = EventView{Event: statement.Event, Actor: Text(actorName), Kind: string(statement.Kind),
			Verdict: string(decision.Verdict), Reason: decision.Reason, Text: Text(statement.Text)}
	}
	for _, act := range projection.Acts {
		decision, wanted := nonEffective[act.Event]
		if !wanted || act.Actor != fingerprint {
			continue
		}
		byEvent[act.Event] = EventView{Event: act.Event, Actor: Text(actorName), Kind: act.Type,
			Verdict: string(decision.Verdict), Reason: decision.Reason, Target: act.Target, Text: Text(act.Text)}
	}
	attention := make([]EventView, 0, len(byEvent))
	for _, decision := range projection.Decisions {
		if view, ok := byEvent[decision.Event]; ok {
			attention = append(attention, view)
		}
	}
	return attention
}

func BuildActorStatus(durable app.Snapshot, live nexus.Snapshot, cursor Cursor, inbox *nexus.Inbox, fingerprint, actorName string, degraded bool) ActorStatus {
	projection := durable.Projection
	digest := ActorStatus{You: ActorView{Name: Text(actorName), Fingerprint: fingerprint}, Frontier: cursor.Frontier, Cursor: cursor,
		Totals: actorTotals(projection, durable.Depth), Live: actorLive(live, degraded), PriorityChat: inboxView(projection, inbox, degraded),
		FollowWithWait: "pass cursor back to wait to receive only what changes after it"}
	if actor, ok := projection.Actors[fingerprint]; ok {
		digest.You.Kind, digest.You.MembershipEvent = actor.Kind, actor.MembershipEvent
		digest.You.Roles, digest.You.RolesSkipped = CapRoles(actor.Roles, ListCap)
		if actor.Name != "" {
			digest.You.Name = Text(actor.Name)
		}
	}
	for _, commitment := range projection.Commitments {
		if !involves(commitment, fingerprint) || terminal[commitment.Status] {
			continue
		}
		view := viewCommitment(projection, commitment)
		if !actionable[commitment.Status] {
			digest.NotActionable = append(digest.NotActionable, view)
		} else if addressedTo(commitment, fingerprint) {
			digest.AvailableToYou = append(digest.AvailableToYou, view)
		} else if commitment.WaitingOn == fingerprint {
			digest.WaitingOnYou = append(digest.WaitingOnYou, view)
		} else if commitment.WaitingOn != "" {
			digest.YouAreWaiting = append(digest.YouAreWaiting, view)
		}
	}
	digest.YourAttention = actorAttention(projection, fingerprint, actorName)
	digest.AvailableToYou, digest.AvailableToYouSkipped = Cap(digest.AvailableToYou, ListCap)
	digest.WaitingOnYou, digest.WaitingOnYouSkipped = Cap(digest.WaitingOnYou, ListCap)
	digest.YouAreWaiting, digest.YouAreWaitingSkipped = Cap(digest.YouAreWaiting, ListCap)
	digest.NotActionable, digest.NotActionableSkipped = Cap(digest.NotActionable, ListCap)
	digest.YourAttention, digest.YourAttentionSkipped = Cap(digest.YourAttention, ListCap)
	fillCommitmentDetails(projection, digest.AvailableToYou, digest.WaitingOnYou, digest.YouAreWaiting, digest.NotActionable)
	return digest
}

// BuildOrientation selects one effective durable actor from the same
// projection used by status. Unknown actors are refused rather than being
// represented by local configuration alone.
func BuildOrientation(durable app.Snapshot, fingerprint, actorName string) (Orientation, bool) {
	actor, ok := durable.Projection.Actors[fingerprint]
	if !ok {
		return Orientation{}, false
	}
	name := actorName
	if actor.Name != "" {
		name = actor.Name
	}
	roles, skipped := CapRoles(actor.Roles, ListCap)
	return Orientation{
		You: ActorView{Name: Text(name), Fingerprint: fingerprint, Kind: actor.Kind, MembershipEvent: actor.MembershipEvent,
			Roles: roles, RolesSkipped: skipped},
		Frontier:          Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth},
		ProjectionVersion: OrientationProjectionVersion,
	}, true
}

func BuildWait(durable app.Snapshot, cursor Cursor, live []nexus.Change, reset bool, requested Cursor, inbox *nexus.Inbox, fingerprint, actorName string, degraded bool) WaitDelta {
	projection := durable.Projection
	delta := WaitDelta{Cursor: cursor, Reset: reset, Live: live, PriorityChat: inboxView(projection, inbox, degraded), Totals: actorTotals(projection, durable.Depth)}
	from := 0
	for _, frontier := range requested.Frontier {
		if frontier.Genesis == "" || frontier.Genesis == durable.Genesis {
			from = frontier.Depth
		}
	}
	if from < 0 || from > len(projection.Decisions) {
		from = len(projection.Decisions)
	}
	fresh := projection.Decisions[from:]
	if len(fresh) > DeltaCap {
		delta.Skipped = len(fresh) - DeltaCap
		fresh = fresh[len(fresh)-DeltaCap:]
	}
	wanted := make(map[string]bool, len(fresh))
	for _, decision := range fresh {
		wanted[decision.Event] = true
	}
	statements := make(map[string]workroom.Statement, len(fresh))
	for _, statement := range projection.Statements {
		if wanted[statement.Event] {
			statements[statement.Event] = statement
		}
	}
	acts := make(map[string]workroom.Act, len(fresh))
	for _, act := range projection.Acts {
		if wanted[act.Event] {
			acts[act.Event] = act
		}
	}
	for _, decision := range fresh {
		view := EventView{Event: decision.Event, Verdict: string(decision.Verdict), Reason: decision.Reason}
		if statement, ok := statements[decision.Event]; ok {
			view.Actor, view.Kind, view.Text = Text(ActorName(projection, statement.Actor)), string(statement.Kind), Text(statement.Text)
		} else if act, ok := acts[decision.Event]; ok {
			view.Actor, view.Kind, view.Target, view.Text = Text(ActorName(projection, act.Actor)), act.Type, act.Target, Text(act.Text)
		}
		delta.Durable = append(delta.Durable, view)
	}
	for _, commitment := range projection.Commitments {
		if !involves(commitment, fingerprint) || terminal[commitment.Status] {
			continue
		}
		view := viewCommitment(projection, commitment)
		if !actionable[commitment.Status] {
			delta.CurrentNotActionable = append(delta.CurrentNotActionable, view)
		} else if addressedTo(commitment, fingerprint) {
			delta.CurrentAvailableToYou = append(delta.CurrentAvailableToYou, view)
		} else if commitment.WaitingOn == fingerprint {
			delta.CurrentWaitingOnYou = append(delta.CurrentWaitingOnYou, view)
		}
	}
	delta.CurrentAvailableToYou, delta.CurrentAvailableToSkipped = Cap(delta.CurrentAvailableToYou, ListCap)
	delta.CurrentWaitingOnYou, delta.CurrentWaitingSkipped = Cap(delta.CurrentWaitingOnYou, ListCap)
	delta.CurrentNotActionable, delta.CurrentNotActionableSkipped = Cap(delta.CurrentNotActionable, ListCap)
	fillCommitmentDetails(projection, delta.CurrentAvailableToYou, delta.CurrentWaitingOnYou, delta.CurrentNotActionable)
	if degraded {
		delta.Cursor.Live = nexus.Cursor{Generation: "degraded"}
	}
	return delta
}

func Summarize(tool string, value any) string {
	switch shaped := value.(type) {
	case ActorStatus:
		priority := fmt.Sprintf("priority ephemeral chat: %d unacknowledged", len(shaped.PriorityChat.Frames))
		if !shaped.PriorityChat.Available {
			priority = "priority ephemeral chat unavailable"
		} else if shaped.PriorityChat.Skipped > 0 {
			priority += fmt.Sprintf(", %d additional pending", shaped.PriorityChat.Skipped)
		}
		return fmt.Sprintf("%s; depth %d, you hold %s roles, %s addressed to you, %s waiting on you, %s you are waiting on, %s not actionable, %s of your acts did not take force; live %s", priority,
			shaped.Totals.Depth, Shown(len(shaped.You.Roles), shaped.You.RolesSkipped),
			Shown(len(shaped.AvailableToYou), shaped.AvailableToYouSkipped),
			Shown(len(shaped.WaitingOnYou), shaped.WaitingOnYouSkipped), Shown(len(shaped.YouAreWaiting), shaped.YouAreWaitingSkipped),
			Shown(len(shaped.NotActionable), shaped.NotActionableSkipped), Shown(len(shaped.YourAttention), shaped.YourAttentionSkipped), LiveLabel(shaped.Live))
	case WaitDelta:
		reset, skipped := "", ""
		if shaped.Reset {
			reset = ", live reset"
		}
		if shaped.Skipped > 0 {
			skipped = fmt.Sprintf(", %d older events omitted", shaped.Skipped)
		}
		priority := fmt.Sprintf("priority ephemeral chat: %d unacknowledged", len(shaped.PriorityChat.Frames))
		if !shaped.PriorityChat.Available {
			priority = "priority ephemeral chat unavailable"
		} else if shaped.PriorityChat.Skipped > 0 {
			priority += fmt.Sprintf(", %d additional pending", shaped.PriorityChat.Skipped)
		}
		return fmt.Sprintf("%s; depth %d, %d new durable events%s%s; currently %s addressed to you, %s waiting on you, %s not actionable", priority,
			shaped.Totals.Depth, len(shaped.Durable), skipped, reset,
			Shown(len(shaped.CurrentAvailableToYou), shaped.CurrentAvailableToSkipped),
			Shown(len(shaped.CurrentWaitingOnYou), shaped.CurrentWaitingSkipped), Shown(len(shaped.CurrentNotActionable), shaped.CurrentNotActionableSkipped))
	case WorkPage:
		suffix := ""
		if shaped.Remaining > 0 {
			suffix = fmt.Sprintf(", %d remain", shaped.Remaining)
		}
		if shaped.ClosedStaleOmitted > 0 {
			suffix += fmt.Sprintf("; %d closed commitments carry ordinary staleness and were summarized, pass stale=include to list them", shaped.ClosedStaleOmitted)
		}
		return fmt.Sprintf("depth %d, %d of %d matching work items returned%s", shaped.Frontier.Depth, shaped.Returned, shaped.MatchingTotal, suffix)
	case ArtifactPage:
		suffix := ""
		if shaped.Remaining > 0 {
			suffix = fmt.Sprintf(", %d remain", shaped.Remaining)
		}
		return fmt.Sprintf("depth %d, %d of %d live artifacts returned%s", shaped.Frontier.Depth, shaped.Returned, shaped.MatchingTotal, suffix)
	case ItemInspection:
		kind := "event"
		if shaped.Statement != nil {
			kind = string(shaped.Statement.Kind)
		} else if shaped.Act != nil {
			kind = shaped.Act.Type
		}
		verdict := "without a decision"
		if shaped.Decision != nil {
			verdict = "decision " + string(shaped.Decision.Verdict)
		}
		return fmt.Sprintf("inspected %s at depth %d: %s, %s provenance bases, %s related artifacts, %s related reviews",
			kind, shaped.Frontier.Depth, verdict,
			Shown(len(shaped.ProvenanceBases), shaped.ProvenanceBasesOmitted),
			Shown(len(shaped.RelatedArtifacts), shaped.RelatedArtifactsOmitted),
			Shown(len(shaped.RelatedReviews), shaped.RelatedReviewsOmitted))
	default:
		return tool + " ok"
	}
}

func Shown(listed, skipped int) string {
	if skipped == 0 {
		return fmt.Sprintf("%d", listed)
	}
	return fmt.Sprintf("%d of %d", listed, listed+skipped)
}

func LiveLabel(live LiveView) string {
	if live.Degraded {
		return "degraded"
	}
	if len(live.Present) == 0 {
		return "nobody present"
	}
	return strings.Join(live.Present, ", ")
}
