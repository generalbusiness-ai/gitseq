// Package statusview builds bounded, truthful views of the durable workroom.
// It never changes the underlying projection; full audit and full rendering
// remain available to callers that explicitly ask for them.
package statusview

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	ListCap = 20
	TextCap = 240
)

type Totals struct {
	Commitments map[string]int `json:"commitments"`
	// StaleCommitments counts, per status, how many of those commitments carry
	// the stale qualifier. Staleness qualifies a status instead of replacing
	// it, so one count cannot say both things; a reader who only had
	// Commitments would see "satisfied 98" and never learn that half of those
	// rest on a basis somebody retired.
	StaleCommitments map[string]int `json:"stale_commitments,omitempty"`
	Artifacts        int            `json:"artifacts"`
	StaleArtifacts   int            `json:"stale_artifacts"`
	IneffectiveActs  int            `json:"ineffective_acts"`
	DisputedActs     int            `json:"disputed_acts"`
	Statements       int            `json:"statements"`
}

type Commitment struct {
	Request     string `json:"request"`
	Status      string `json:"status"`
	AddressedTo string `json:"addressed_to,omitempty"`
	// Stale qualifies Status rather than replacing it. The lifecycle word says
	// what was last done and who owes the next move; the qualifier says a basis
	// underneath it was retired, so the outcome is worth re-checking.
	Stale     bool   `json:"stale,omitempty"`
	Requester string `json:"requester"`
	Performer string `json:"performer,omitempty"`
	WaitingOn string `json:"waiting_on,omitempty"`
	Text      string `json:"text,omitempty"`
}

type Artifact struct {
	Event  string `json:"event"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	State  string `json:"state"`
	Notes  string `json:"notes,omitempty"`
}

type Attempt struct {
	Event    string `json:"event"`
	Sequence int    `json:"sequence,omitempty"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

type Summary struct {
	Genesis string `json:"genesis"`
	Head    string `json:"head"`
	Depth   int    `json:"depth"`
	Totals  Totals `json:"totals"`

	Actionable        []Commitment `json:"actionable"`
	ActionableOmitted int          `json:"actionable_omitted,omitempty"`
	Attention         []Commitment `json:"attention"`
	AttentionOmitted  int          `json:"attention_omitted,omitempty"`
	CurrentArtifacts  []Artifact   `json:"current_artifacts"`
	CurrentOmitted    int          `json:"current_artifacts_omitted,omitempty"`
	StaleArtifacts    []Artifact   `json:"stale_artifacts"`
	StaleOmitted      int          `json:"stale_artifacts_omitted,omitempty"`
	Attempts          []Attempt    `json:"attempts"`
	AttemptsOmitted   int          `json:"attempts_omitted,omitempty"`
}

// The lanes a commitment can be in. These are exactly the statuses the fold
// emits (see foldState.projectCommitments): open, promised, reported,
// satisfied, stale, cancelled, reneged, withdrawn.
//
// Open is the current fold vocabulary for an addressed request that has not
// been claimed. It is actionable without inventing a performer or waiting
// party, so the global view includes it while preserving those empty fields.
var actionable = map[string]bool{"open": true, "promised": true, "reported": true}

// Terminal commitments are done with: nobody owes a next move. They stay out
// of the bounded lists so the lists show work, not history — unless they carry
// the stale qualifier, in which case the world moved under a closed outcome
// and someone has to re-check it.
var terminal = map[string]bool{"satisfied": true, "withdrawn": true}

// Cap keeps the newest limit entries and reports exactly how many it omitted.
func Cap[T any](items []T, limit int) ([]T, int) {
	if len(items) <= limit {
		return items, 0
	}
	return items[len(items)-limit:], len(items) - limit
}

// Text normalizes whitespace and caps user-controlled text by bytes.
func Text(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= TextCap {
		return value
	}
	limit := TextCap - len("…")
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "…"
}

// ActorName resolves durable fingerprints without ever replacing an unknown
// identity with an empty label.
func ActorName(projection workroom.Projection, fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	if actor, ok := projection.Actors[fingerprint]; ok && actor.Name != "" {
		return actor.Name
	}
	return fingerprint
}

func Build(genesis, head string, depth int, projection workroom.Projection) Summary {
	statements := make(map[string]workroom.Statement, len(projection.Statements))
	for _, statement := range projection.Statements {
		statements[statement.Event] = statement
	}
	summary := Summary{Genesis: genesis, Head: head, Depth: depth, Totals: Totals{
		Commitments: make(map[string]int), StaleCommitments: make(map[string]int),
		Artifacts: len(projection.Artifacts), Statements: len(projection.Statements),
	}}
	for _, commitment := range projection.Commitments {
		summary.Totals.Commitments[commitment.Status]++
		if commitment.Stale {
			summary.Totals.StaleCommitments[commitment.Status]++
		}
		if terminal[commitment.Status] && !commitment.Stale {
			continue
		}
		view := Commitment{
			Request: commitment.Request, Status: commitment.Status, Stale: commitment.Stale,
			AddressedTo: Text(ActorName(projection, commitment.AddressedTo)),
			Requester:   Text(ActorName(projection, commitment.Requester)), Performer: Text(ActorName(projection, commitment.Performer)),
			WaitingOn: Text(ActorName(projection, commitment.WaitingOn)),
		}
		if statement, ok := statements[commitment.Request]; ok {
			view.Text = Text(statement.Text)
		}
		if actionable[commitment.Status] {
			summary.Actionable = append(summary.Actionable, view)
		} else {
			summary.Attention = append(summary.Attention, view)
		}
	}
	for _, artifact := range projection.Artifacts {
		// Retired and stale are separate facts on the artifact now, and both
		// mean the same thing to a reader looking for what is current: not
		// this one. They are kept apart in the state word because a withdrawn
		// pointer and a moved world call for different work.
		state := "current"
		switch {
		case artifact.Retired:
			state = "retired"
			summary.Totals.StaleArtifacts++
		case artifact.Stale:
			state = "stale"
			summary.Totals.StaleArtifacts++
		}
		var notes []string
		if artifact.DescribesSupersededWorld {
			notes = append(notes, "describes a superseded world")
		}
		if artifact.UnableToFlare {
			notes = append(notes, "unable to flare")
		}
		if artifact.SuccessionUnrecorded {
			notes = append(notes, "succession not recorded")
		}
		view := Artifact{Event: artifact.Event, Path: Text(artifact.Path), Commit: artifact.Commit, State: state, Notes: strings.Join(notes, ", ")}
		if state == "current" {
			summary.CurrentArtifacts = append(summary.CurrentArtifacts, view)
		} else {
			summary.StaleArtifacts = append(summary.StaleArtifacts, view)
		}
	}
	for _, decision := range projection.Decisions {
		switch decision.Verdict {
		case workroom.Ineffective:
			summary.Totals.IneffectiveActs++
		case workroom.Disputed:
			summary.Totals.DisputedActs++
		default:
			continue
		}
		summary.Attempts = append(summary.Attempts, Attempt{Event: decision.Event, Sequence: decision.Sequence, Verdict: string(decision.Verdict), Reason: Text(decision.Reason)})
	}
	summary.Actionable, summary.ActionableOmitted = Cap(summary.Actionable, ListCap)
	summary.Attention, summary.AttentionOmitted = Cap(summary.Attention, ListCap)
	summary.CurrentArtifacts, summary.CurrentOmitted = Cap(summary.CurrentArtifacts, ListCap)
	summary.StaleArtifacts, summary.StaleOmitted = Cap(summary.StaleArtifacts, ListCap)
	summary.Attempts, summary.AttemptsOmitted = Cap(summary.Attempts, ListCap)
	reverse(summary.Actionable)
	reverse(summary.Attention)
	reverse(summary.CurrentArtifacts)
	reverse(summary.StaleArtifacts)
	reverse(summary.Attempts)
	return summary
}

func reverse[T any](items []T) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}

func Render(summary Summary, source string) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "# Workroom status\n\nfrontier %s at depth %d", short(summary.Head), summary.Depth)
	if source != "" {
		fmt.Fprintf(&output, " (%s)", source)
	}
	output.WriteString("\n\n")
	keys := make([]string, 0, len(summary.Totals.Commitments))
	for key := range summary.Totals.Commitments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var counts []string
	for _, key := range keys {
		count := fmt.Sprintf("%s %d", key, summary.Totals.Commitments[key])
		// A commitment with no report has no outcome to preserve, so the fold
		// gives it the status "stale" outright. Repeating the qualifier there
		// would only say the same word twice.
		if stale := summary.Totals.StaleCommitments[key]; stale > 0 && key != "stale" {
			count += fmt.Sprintf(" (%d stale)", stale)
		}
		counts = append(counts, count)
	}
	fmt.Fprintf(&output, "Commitments: %s. Artifacts: %d current, %d stale. Attempts: %d ineffective, %d disputed.\n",
		strings.Join(counts, ", "), summary.Totals.Artifacts-summary.Totals.StaleArtifacts, summary.Totals.StaleArtifacts,
		summary.Totals.IneffectiveActs, summary.Totals.DisputedActs)
	renderCommitments(&output, "Actionable commitments", summary.Actionable, summary.ActionableOmitted)
	renderCommitments(&output, "Needs attention", summary.Attention, summary.AttentionOmitted)
	renderArtifacts(&output, "Current artifacts", summary.CurrentArtifacts, summary.CurrentOmitted)
	renderArtifacts(&output, "Stale artifacts", summary.StaleArtifacts, summary.StaleOmitted)
	output.WriteString("\n## Non-effective attempts\n\n")
	if len(summary.Attempts) == 0 {
		output.WriteString("None.\n")
	} else {
		for _, attempt := range summary.Attempts {
			fmt.Fprintf(&output, "- %s — %s: %s\n", name(attempt.Event, attempt.Sequence), attempt.Verdict, attempt.Reason)
		}
		writeOmitted(&output, len(summary.Attempts), summary.AttemptsOmitted)
	}
	return output.Bytes()
}

func renderCommitments(output *bytes.Buffer, title string, items []Commitment, omitted int) {
	fmt.Fprintf(output, "\n## %s\n\n", title)
	if len(items) == 0 {
		output.WriteString("None.\n")
		return
	}
	for _, item := range items {
		assignment := item.Performer
		if assignment == "" && item.AddressedTo != "" {
			assignment = "addressed to " + item.AddressedTo + " — unclaimed"
		} else if assignment == "" {
			assignment = "unclaimed"
		}
		status := item.Status
		if item.Stale && item.Status != "stale" {
			status += " (stale)"
		}
		fmt.Fprintf(output, "- %s — %s → %s — `%s`", status, item.Requester, assignment, short(item.Request))
		if item.Text != "" {
			fmt.Fprintf(output, ": %s", item.Text)
		}
		output.WriteByte('\n')
	}
	writeOmitted(output, len(items), omitted)
}

func renderArtifacts(output *bytes.Buffer, title string, items []Artifact, omitted int) {
	fmt.Fprintf(output, "\n## %s\n\n", title)
	if len(items) == 0 {
		output.WriteString("None.\n")
		return
	}
	for _, item := range items {
		fmt.Fprintf(output, "- %s@%s — `%s`", item.Path, short(item.Commit), short(item.Event))
		if item.Notes != "" {
			fmt.Fprintf(output, " — %s", item.Notes)
		}
		output.WriteByte('\n')
	}
	writeOmitted(output, len(items), omitted)
}

func writeOmitted(output *bytes.Buffer, listed, omitted int) {
	if omitted > 0 {
		fmt.Fprintf(output, "\nShowing %d of %d; %d older omitted.\n", listed, listed+omitted, omitted)
	}
}

// name renders an event as a reader says it. Falling back to the identifier in
// full rather than an abbreviation is deliberate: `short` elides the middle, so
// its output is visibly incomplete and cannot be mistaken for a name, but an
// event that has a number should be called by it.
func name(event string, sequence int) string {
	if sequence > 0 {
		return fmt.Sprintf("#%d", sequence)
	}
	return event
}

func short(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:8] + "…" + value[len(value)-7:]
}
