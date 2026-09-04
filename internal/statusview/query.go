package statusview

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	WorkPageDefault = 20
	WorkPageMax     = 50
	InspectLinkCap  = 50
)

type WorkLane string

const (
	LaneAvailable            WorkLane = "available_to_you"
	LaneAwaitingRatification WorkLane = "awaiting_ratification"
	LaneWaitingOnYou         WorkLane = "waiting_on_you"
	LaneYouAreWaitingOn      WorkLane = "you_are_waiting_on"
	LaneNotActionable        WorkLane = "not_actionable"
)

type StaleFilter string

const (
	// StaleSummary is the policy a caller who names none gets. Ordinary
	// reasoning staleness on a closed commitment is history: it blocks
	// nothing, it reaches nearly nine closed commitments in ten here, and listing
	// each one buried the work still owed. Those rows are counted in
	// WorkPage.ClosedStaleOmitted rather than returned. Every other staleness
	// fact is untouched, and every row that is returned still carries its own
	// stale field.
	StaleSummary StaleFilter = "summary"
	StaleInclude StaleFilter = "include"
	StaleOnly    StaleFilter = "only"
	StaleExclude StaleFilter = "exclude"
)

// WorkQuery is deliberately finite. The resident accepts no expression
// language: callers choose named relationship lanes, lifecycle statuses, and
// one staleness policy, then continue through an opaque head-bound cursor.
type WorkQuery struct {
	Actor    string     `json:"actor"`
	Lanes    []WorkLane `json:"lanes,omitempty"`
	Statuses []string   `json:"statuses,omitempty"`
	// Stale defaults to StaleSummary, which is not the same as StaleInclude.
	// The default answers "what is still owed"; include, only and exclude are
	// the explicit policies and each returns exactly what it always did.
	Stale  StaleFilter `json:"stale,omitempty"`
	Limit  int         `json:"limit,omitempty"`
	Cursor string      `json:"cursor,omitempty"`
}

type ActorRef struct {
	Fingerprint string `json:"fingerprint"`
	Name        string `json:"name"`
}

// WorkReview is the latest effective review for the exact reported head.
// Ratified, Retired, and Stale are deliberately not omitted when false: callers
// need to distinguish an actionable verdict from a row that has no review
// evidence at all.
type WorkReview struct {
	Report   string `json:"report"`
	Verdict  string `json:"verdict"`
	Ratified bool   `json:"ratified"`
	Retired  bool   `json:"retired"`
	Stale    bool   `json:"stale"`
}

// WorkDetails carries the bounded facts needed to act on a commitment row.
// Conditions is neutralized but not truncated for unclaimed requests whose
// lifecycle status is open or stale; the number of rows remains capped by the
// surrounding status or work page.
type WorkDetails struct {
	Conditions   string      `json:"conditions,omitempty"`
	ReportStatus string      `json:"report_status,omitempty"`
	ReportedHead string      `json:"reported_head,omitempty"`
	LatestReview *WorkReview `json:"latest_review,omitempty"`
}

type WorkItem struct {
	// Event is present for attention that is not a commitment. Pending
	// ratification rows use it instead of pretending the proposal is a request.
	Event            string    `json:"event,omitempty"`
	Kind             string    `json:"kind,omitempty"`
	Author           *ActorRef `json:"author,omitempty"`
	Satisfier        string    `json:"satisfier,omitempty"`
	Request          string    `json:"request"`
	Lane             WorkLane  `json:"lane"`
	Status           string    `json:"status"`
	SuccessorRequest string    `json:"successor_request,omitempty"`
	Stale            bool      `json:"stale,omitempty"`
	Requester        ActorRef  `json:"requester"`
	AddressedTo      *ActorRef `json:"addressed_to,omitempty"`
	Performer        *ActorRef `json:"performer,omitempty"`
	WaitingOn        *ActorRef `json:"waiting_on,omitempty"`
	Promise          string    `json:"promise,omitempty"`
	Report           string    `json:"report,omitempty"`
	Text             string    `json:"text,omitempty"`
	WorkDetails
	unclaimedRequest bool
}

type WorkPage struct {
	Frontier      Frontier   `json:"frontier"`
	Actor         ActorRef   `json:"actor"`
	Items         []WorkItem `json:"items"`
	MatchingTotal int        `json:"matching_total"`
	Returned      int        `json:"returned"`
	Before        int        `json:"before"`
	Remaining     int        `json:"remaining"`
	NextCursor    string     `json:"next_cursor,omitempty"`
	// ClosedStaleOmitted counts the closed commitments the default staleness
	// policy summarized instead of listing. Nothing is hidden: pass
	// stale=include, or name the statuses, to get every one of them back.
	ClosedStaleOmitted int  `json:"closed_stale_omitted,omitempty"`
	Degraded           bool `json:"degraded,omitempty"`
}

type workCursor struct {
	Version int    `json:"v"`
	Head    string `json:"head"`
	Offset  int    `json:"offset"`
	Filter  string `json:"filter"`
}

var knownStatuses = map[string]bool{
	"open": true, "promised": true, "reported": true, "superseded": true, "satisfied": true,
	"awaiting-review": true, "awaiting-authorization": true, "awaiting-landing": true, "abandoned": true,
	"stale": true, "cancelled": true, "reneged": true, "withdrawn": true, "awaiting-ratification": true,
}

var knownLanes = map[WorkLane]bool{
	LaneAvailable: true, LaneAwaitingRatification: true, LaneWaitingOnYou: true, LaneYouAreWaitingOn: true, LaneNotActionable: true,
}

func actorRef(projection workroom.Projection, fingerprint string) *ActorRef {
	if fingerprint == "" {
		return nil
	}
	return &ActorRef{Fingerprint: fingerprint, Name: Text(ActorName(projection, fingerprint))}
}

func normalizeWorkQuery(input WorkQuery) (WorkQuery, string, error) {
	if input.Actor == "" {
		return WorkQuery{}, "", errors.New("actor is required")
	}
	if input.Limit == 0 {
		input.Limit = WorkPageDefault
	}
	if input.Limit < 1 || input.Limit > WorkPageMax {
		return WorkQuery{}, "", fmt.Errorf("limit must be between 1 and %d", WorkPageMax)
	}
	if input.Stale == "" {
		input.Stale = StaleSummary
	}
	if input.Stale != StaleSummary && input.Stale != StaleInclude && input.Stale != StaleOnly && input.Stale != StaleExclude {
		return WorkQuery{}, "", fmt.Errorf("unknown stale filter %q", input.Stale)
	}
	if len(input.Lanes) == 0 {
		input.Lanes = []WorkLane{LaneAvailable, LaneAwaitingRatification, LaneWaitingOnYou, LaneYouAreWaitingOn, LaneNotActionable}
	}
	laneSet := make(map[WorkLane]bool, len(input.Lanes))
	for _, lane := range input.Lanes {
		if !knownLanes[lane] {
			return WorkQuery{}, "", fmt.Errorf("unknown work lane %q", lane)
		}
		laneSet[lane] = true
	}
	input.Lanes = input.Lanes[:0]
	for lane := range laneSet {
		input.Lanes = append(input.Lanes, lane)
	}
	sort.Slice(input.Lanes, func(i, j int) bool { return input.Lanes[i] < input.Lanes[j] })

	statusSet := make(map[string]bool, len(input.Statuses))
	for _, status := range input.Statuses {
		if !knownStatuses[status] {
			return WorkQuery{}, "", fmt.Errorf("unknown commitment status %q", status)
		}
		statusSet[status] = true
	}
	input.Statuses = input.Statuses[:0]
	for status := range statusSet {
		input.Statuses = append(input.Statuses, status)
	}
	sort.Strings(input.Statuses)

	fingerprintInput := struct {
		Actor    string      `json:"actor"`
		Lanes    []WorkLane  `json:"lanes"`
		Statuses []string    `json:"statuses"`
		Stale    StaleFilter `json:"stale"`
	}{input.Actor, input.Lanes, input.Statuses, input.Stale}
	encoded, _ := json.Marshal(fingerprintInput)
	sum := sha256.Sum256(encoded)
	return input, hex.EncodeToString(sum[:]), nil
}

func commitmentLane(commitment workroom.Commitment, actor string) WorkLane {
	// The branch order mirrors BuildActorStatus exactly, so gs work and gs
	// status classify one row identically. Reading not_actionable off an empty
	// WaitingOn conflated two different facts: a row nobody owes a move on, and
	// an actionable row whose performer has not promised yet. The second is a
	// request its author is actively chasing, and it belongs in the waiting lane.
	if addressedTo(commitment, actor) {
		return LaneAvailable
	}
	if !involves(commitment, actor) {
		return ""
	}
	if !actionable[commitment.Status] {
		return LaneNotActionable
	}
	if commitment.WaitingOn == actor {
		return LaneWaitingOnYou
	}
	return LaneYouAreWaitingOn
}

func decodeWorkCursor(raw, head, filter string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("invalid work cursor")
	}
	var cursor workCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.Version != 1 || cursor.Offset < 0 {
		return 0, errors.New("invalid work cursor")
	}
	if cursor.Head != head {
		return 0, fmt.Errorf("work cursor is for head %s; current head is %s", cursor.Head, head)
	}
	if cursor.Filter != filter {
		return 0, errors.New("work cursor does not match these filters")
	}
	return cursor.Offset, nil
}

func encodeWorkCursor(head, filter string, offset int) string {
	encoded, _ := json.Marshal(workCursor{Version: 1, Head: head, Offset: offset, Filter: filter})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// BuildWorkPage selects actor work from the resident's in-memory projection.
// It never marshals the complete projection, and its response size is bounded
// by WorkPageMax rather than by workroom depth.
func BuildWorkPage(durable app.Snapshot, input WorkQuery, degraded bool) (WorkPage, error) {
	query, filter, err := normalizeWorkQuery(input)
	if err != nil {
		return WorkPage{}, err
	}
	actor, ok := durable.Projection.Actors[query.Actor]
	if !ok || actor.Retired {
		return WorkPage{}, errors.New("actor is not in the effective durable roster")
	}
	offset, err := decodeWorkCursor(query.Cursor, durable.Head, filter)
	if err != nil {
		return WorkPage{}, err
	}
	lanes := make(map[WorkLane]bool, len(query.Lanes))
	for _, lane := range query.Lanes {
		lanes[lane] = true
	}
	statuses := make(map[string]bool, len(query.Statuses))
	for _, status := range query.Statuses {
		statuses[status] = true
	}
	items := make([]WorkItem, 0, query.Limit)
	matching, closedStale := 0, 0
	consider := func(item WorkItem) {
		position := matching
		matching++
		if position >= offset && len(items) < query.Limit {
			items = append(items, item)
		}
	}
	// Ratification attention comes first in the default page. It is not a
	// commitment and should not be buried behind commitment history merely
	// because both share one bounded query surface.
	if lanes[LaneAwaitingRatification] && (len(statuses) == 0 || statuses["awaiting-ratification"]) {
		pending := awaitingRatifications(durable.Projection, query.Actor)
		for index := len(pending) - 1; index >= 0; index-- {
			row := pending[index]
			if query.Stale == StaleOnly && !row.Stale || query.Stale == StaleExclude && row.Stale {
				continue
			}
			consider(WorkItem{
				Event: row.Event, Kind: row.Kind, Author: actorRef(durable.Projection, row.actorFingerprint),
				Satisfier: row.Satisfier, Lane: LaneAwaitingRatification, Status: "awaiting-ratification",
				Stale: row.Stale, Text: row.Text,
			})
		}
	}
	for index := len(durable.Projection.Commitments) - 1; index >= 0; index-- {
		commitment := durable.Projection.Commitments[index]
		lane := commitmentLane(commitment, query.Actor)
		if lane == "" || !lanes[lane] {
			continue
		}
		if len(statuses) > 0 {
			if !statuses[commitment.Status] {
				continue
			}
		} else if !actionable[commitment.Status] && !commitment.Stale {
			continue
		} else if query.Stale == StaleSummary && terminal[commitment.Status] {
			// Named statuses and an explicit staleness policy both say the
			// caller wants this history. Naming neither asks for the work
			// still owed, so a superseded, satisfied or withdrawn commitment that only
			// carries ordinary staleness is counted, not listed.
			closedStale++
			continue
		}
		if query.Stale == StaleOnly && !commitment.Stale || query.Stale == StaleExclude && commitment.Stale {
			continue
		}
		requester := actorRef(durable.Projection, commitment.Requester)
		item := WorkItem{Request: commitment.Request, Lane: lane, Status: commitment.Status, Stale: commitment.Stale,
			SuccessorRequest: commitment.SuccessorRequest, Promise: commitment.Promise, Report: commitment.Report, AddressedTo: actorRef(durable.Projection, commitment.AddressedTo),
			Performer: actorRef(durable.Projection, commitment.Performer), WaitingOn: actorRef(durable.Projection, commitment.WaitingOn),
			unclaimedRequest: isUnclaimedRequest(commitment)}
		if requester != nil {
			item.Requester = *requester
		}
		consider(item)
	}
	enrichWorkRows(durable.Projection, workItemTargets(items))
	if offset > matching {
		return WorkPage{}, errors.New("work cursor is beyond the matching result")
	}
	end := offset + len(items)
	page := WorkPage{
		Frontier: Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth},
		Actor:    ActorRef{Fingerprint: query.Actor, Name: Text(ActorName(durable.Projection, query.Actor))},
		Items:    items, MatchingTotal: matching, Returned: len(items), Before: offset,
		Remaining: matching - end, ClosedStaleOmitted: closedStale, Degraded: degraded,
	}
	if end < matching {
		page.NextCursor = encodeWorkCursor(durable.Head, filter, end)
	}
	return page, nil
}

type workRowTarget struct {
	Request           string
	Report            string
	IncludeConditions bool
	Text              *string
	Details           *WorkDetails
	artifact          string
}

func workItemTargets(items []WorkItem) []workRowTarget {
	targets := make([]workRowTarget, len(items))
	for index := range items {
		targets[index] = workRowTarget{Request: items[index].Request, Report: items[index].Report,
			IncludeConditions: items[index].unclaimedRequest,
			Text:              &items[index].Text, Details: &items[index].WorkDetails}
	}
	return targets
}

// enrichWorkRows scans each projection collection once for only the already
// capped rows. It avoids both a second inspect call per row and a projection-
// sized index allocated merely to decorate a bounded response.
func enrichWorkRows(projection workroom.Projection, targets []workRowTarget) {
	requests := make(map[string][]int)
	reports := make(map[string][]int)
	for index, target := range targets {
		requests[target.Request] = append(requests[target.Request], index)
		if target.Report != "" {
			reports[target.Report] = append(reports[target.Report], index)
		}
	}
	for _, statement := range projection.Statements {
		for _, index := range requests[statement.Event] {
			if targets[index].Text != nil {
				*targets[index].Text = Text(statement.Text)
			}
			if targets[index].IncludeConditions && targets[index].Details != nil {
				targets[index].Details.Conditions = Safe(statement.Body["conditions"])
			}
		}
		for _, index := range reports[statement.Event] {
			details := targets[index].Details
			if details == nil {
				continue
			}
			details.ReportStatus = statement.Body["status"]
			details.ReportedHead = statement.Body["head"]
			if details.ReportedHead == "" {
				details.ReportedHead = statement.Body["commit"]
			}
			if details.ReportedHead == "" {
				targets[index].artifact = statement.Body["artifact"]
			}
		}
	}
	artifactTargets := make(map[string][]int)
	for index, target := range targets {
		if target.artifact != "" {
			artifactTargets[target.artifact] = append(artifactTargets[target.artifact], index)
		}
	}
	for _, artifact := range projection.Artifacts {
		for _, index := range artifactTargets[artifact.Event] {
			targets[index].Details.ReportedHead = artifact.Commit
		}
	}
	headTargets := make(map[string][]int)
	for index, target := range targets {
		if target.Details != nil && target.Details.ReportedHead != "" {
			headTargets[target.Details.ReportedHead] = append(headTargets[target.Details.ReportedHead], index)
		}
	}
	for _, review := range projection.Reviews {
		for _, index := range headTargets[review.Head] {
			targets[index].Details.LatestReview = &WorkReview{
				Report: review.Report, Verdict: review.Verdict, Ratified: review.Ratified,
				Retired: review.Retired, Stale: review.Stale,
			}
		}
	}
}

type InspectRequest struct {
	Event string `json:"event"`
}

type ItemInspection struct {
	Frontier                Frontier             `json:"frontier"`
	Event                   string               `json:"event"`
	Statement               *workroom.Statement  `json:"statement,omitempty"`
	Act                     *workroom.Act        `json:"act,omitempty"`
	Decision                *workroom.Decision   `json:"decision,omitempty"`
	Commitment              *workroom.Commitment `json:"commitment,omitempty"`
	ProvenanceBases         []string             `json:"provenance_bases"`
	ProvenanceBasesOmitted  int                  `json:"provenance_bases_omitted,omitempty"`
	RelatedArtifacts        []workroom.Artifact  `json:"related_artifacts,omitempty"`
	RelatedArtifactsOmitted int                  `json:"related_artifacts_omitted,omitempty"`
	RelatedReviews          []workroom.Review    `json:"related_reviews,omitempty"`
	RelatedReviewsOmitted   int                  `json:"related_reviews_omitted,omitempty"`
	Degraded                bool                 `json:"degraded,omitempty"`
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func BuildItemInspection(durable app.Snapshot, event string, degraded bool) (ItemInspection, error) {
	if strings.TrimSpace(event) == "" {
		return ItemInspection{}, errors.New("event is required")
	}
	projection := durable.Projection
	statements := statementIndex(projection)
	acts := actIndex(projection)
	decisions := make(map[string]workroom.Decision, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		decisions[decision.Event] = decision
	}
	result := ItemInspection{Frontier: Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth}, Event: event, Degraded: degraded}
	if statement, ok := statements[event]; ok {
		copy := statement
		result.Statement = &copy
	}
	if act, ok := acts[event]; ok {
		copy := act
		result.Act = &copy
	}
	if decision, ok := decisions[event]; ok {
		copy := decision
		result.Decision = &copy
	}
	if result.Statement == nil && result.Act == nil && result.Decision == nil {
		return ItemInspection{}, errors.New("event is not in the durable projection")
	}

	chain := map[string]bool{event: true}
	for _, commitment := range projection.Commitments {
		if event != commitment.Request && event != commitment.Promise && event != commitment.Report {
			continue
		}
		copy := commitment
		for _, member := range []string{commitment.Request, commitment.Promise, commitment.Report} {
			if member != "" {
				chain[member] = true
			}
		}
		result.Commitment = &copy
		break
	}

	bases := append([]string{}, projection.Provenance[event]...)
	result.ProvenanceBases, result.ProvenanceBasesOmitted = Cap(bases, InspectLinkCap)
	artifactByEvent := make(map[string]workroom.Artifact, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		artifactByEvent[artifact.Event] = artifact
	}
	relatedArtifact := make(map[string]int)
	addArtifact := func(candidate string, rank int) {
		if _, ok := artifactByEvent[candidate]; ok {
			if previous, held := relatedArtifact[candidate]; !held || rank < previous {
				relatedArtifact[candidate] = rank
			}
		}
	}
	for member := range chain {
		rank := 1
		if member == event {
			rank = 0
		}
		addArtifact(member, rank)
		for _, basis := range projection.Provenance[member] {
			addArtifact(basis, rank)
		}
		if statement, ok := statements[member]; ok {
			addArtifact(statement.Body["artifact"], rank)
		}
	}
	for artifact, directBases := range projection.Provenance {
		if _, ok := artifactByEvent[artifact]; !ok {
			continue
		}
		for member := range chain {
			if containsString(directBases, member) {
				addArtifact(artifact, 2)
			}
		}
	}
	artifactEvents := make([]string, 0, len(relatedArtifact))
	for artifact := range relatedArtifact {
		artifactEvents = append(artifactEvents, artifact)
	}
	sort.Slice(artifactEvents, func(i, j int) bool {
		left, right := artifactEvents[i], artifactEvents[j]
		if relatedArtifact[left] != relatedArtifact[right] {
			return relatedArtifact[left] < relatedArtifact[right]
		}
		return left < right
	})
	result.RelatedArtifactsOmitted = len(artifactEvents) - min(len(artifactEvents), InspectLinkCap)
	artifactEvents = artifactEvents[:min(len(artifactEvents), InspectLinkCap)]
	for _, artifact := range artifactEvents {
		result.RelatedArtifacts = append(result.RelatedArtifacts, artifactByEvent[artifact])
	}

	type rankedReview struct {
		review workroom.Review
		rank   int
	}
	var reviews []rankedReview
	for _, review := range projection.Reviews {
		rank := 2
		if review.Report == event {
			rank = 0
		} else if chain[review.Report] || chain[review.Artifact] {
			rank = 1
		}
		_, artifactRelated := relatedArtifact[review.Artifact]
		related := chain[review.Report] || chain[review.Artifact] || artifactRelated
		if !related {
			for member := range chain {
				if containsString(projection.Provenance[review.Report], member) {
					related = true
					break
				}
			}
		}
		if related {
			reviews = append(reviews, rankedReview{review: review, rank: rank})
		}
	}
	sort.SliceStable(reviews, func(i, j int) bool { return reviews[i].rank < reviews[j].rank })
	result.RelatedReviewsOmitted = len(reviews) - min(len(reviews), InspectLinkCap)
	reviews = reviews[:min(len(reviews), InspectLinkCap)]
	for _, review := range reviews {
		result.RelatedReviews = append(result.RelatedReviews, review.review)
	}
	return result, nil
}
