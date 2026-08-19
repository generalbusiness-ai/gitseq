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
	// StaleArtifacts counts every artifact that is not current: retired ones
	// and ones a retirement reached. RetiredArtifacts and WorldStaleArtifacts
	// name the two facts inside it that a reader has to act on, because
	// ordinary staleness reaches nearly every artifact in a long-lived
	// workroom and a number that large says nothing on its own.
	StaleArtifacts      int `json:"stale_artifacts"`
	RetiredArtifacts    int `json:"retired_artifacts"`
	WorldStaleArtifacts int `json:"world_stale_artifacts"`
	IneffectiveActs     int `json:"ineffective_acts"`
	DisputedActs        int `json:"disputed_acts"`
	Statements          int `json:"statements"`
}

type Commitment struct {
	Request     string `json:"request"`
	Status      string `json:"status"`
	AddressedTo string `json:"addressed_to,omitempty"`
	// Stale qualifies Status rather than replacing it. The lifecycle word says
	// what was last done and who owes the next move; the qualifier says a basis
	// underneath it was retired, so the reasoning moved.
	//
	// It stays in this payload and it stays out of the rendered rows. Ordinary
	// reasoning staleness blocks nothing and reaches nearly every commitment
	// here, so a mark on each row is a warning that fires everywhere and
	// carries no information; Totals.StaleCommitments says how many rows in
	// each lane carry it, which is the same fact without the noise.
	Stale     bool   `json:"stale,omitempty"`
	Requester string `json:"requester"`
	Performer string `json:"performer,omitempty"`
	WaitingOn string `json:"waiting_on,omitempty"`
	Text      string `json:"text,omitempty"`
	// Sequence is the request event's number, carried so the row can be named
	// the way a reader says it rather than by an abbreviation nobody can
	// resolve back.
	Sequence int `json:"sequence,omitempty"`
}

type Artifact struct {
	Event    string `json:"event"`
	Sequence int    `json:"sequence,omitempty"`
	Path     string `json:"path"`
	Commit   string `json:"commit"`
	State    string `json:"state"`
	Notes    string `json:"notes,omitempty"`
}

type Attempt struct {
	Event    string `json:"event"`
	Sequence int    `json:"sequence,omitempty"`
	Verdict  string `json:"verdict"`
	Reason   string `json:"reason,omitempty"`
}

// Dissent is a recorded disagreement standing against the act it concerns.
// It is append-only and never rewrites what it dissents from, which is exactly
// why it has to be shown: the record it names still reads as it always did, so
// a page that omits the dissent tells a reader the act is unopposed.
type Dissent struct {
	Event           string `json:"event"`
	Sequence        int    `json:"sequence,omitempty"`
	Actor           string `json:"actor"`
	Against         string `json:"against,omitempty"`
	AgainstSequence int    `json:"against_sequence,omitempty"`
	Text            string `json:"text,omitempty"`
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
	Dissents          []Dissent    `json:"dissents"`
	DissentsOmitted   int          `json:"dissents_omitted,omitempty"`
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
// of the bounded lists so the lists show work, not history.
//
// Ordinary reasoning staleness does not bring one back. A basis moving under a
// closed commitment is the normal condition of an append-only log — nearly
// nine in ten closed commitments here carry it — so promoting each one into a live
// lane buried the handful of rows that were genuinely unfinished. The counts
// in Totals.StaleCommitments keep the fact, per status, without the rows.
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
	// Every durable record has a decision, so this is the one lookup that can
	// name any event a view row cites — a commitment's request and an
	// artifact's event alike. Statements would not do: ratify and supersede
	// are records without statements, and a row naming one of those would
	// silently fall back to the identifier.
	sequences := make(map[string]int, len(projection.Decisions))
	for _, decision := range projection.Decisions {
		sequences[decision.Event] = decision.Sequence
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
		if terminal[commitment.Status] {
			continue
		}
		view := Commitment{
			Request: commitment.Request, Sequence: sequences[commitment.Request],
			Status: commitment.Status, Stale: commitment.Stale,
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
			summary.Totals.RetiredArtifacts++
		case artifact.Stale:
			state = "stale"
			summary.Totals.StaleArtifacts++
		}
		var notes []string
		if artifact.DescribesSupersededWorld {
			notes = append(notes, "describes a superseded world")
			summary.Totals.WorldStaleArtifacts++
		}
		if artifact.UnableToFlare {
			notes = append(notes, "unable to flare")
		}
		if artifact.SuccessionUnrecorded {
			notes = append(notes, "succession not recorded")
		}
		view := Artifact{Event: artifact.Event, Sequence: sequences[artifact.Event], Path: Text(artifact.Path), Commit: artifact.Commit, State: state, Notes: strings.Join(notes, ", ")}
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
	// Dissent carries no lifecycle and satisfies nothing, so it appears in no
	// commitment lane and no artifact list. Before this it appeared nowhere at
	// all on the human page: the projection knew about it and only a JSON
	// reader could find it.
	for _, statement := range projection.Statements {
		if statement.Kind != workroom.KindDissent || statement.Retired {
			continue
		}
		view := Dissent{
			Event: statement.Event, Sequence: sequences[statement.Event],
			Actor: Text(ActorName(projection, statement.Actor)), Text: Text(statement.Text),
		}
		// A dissent rests on the act it concerns, so the first basis names it.
		if bases := projection.Provenance[statement.Event]; len(bases) > 0 {
			view.Against = bases[0]
			view.AgainstSequence = sequences[bases[0]]
		}
		summary.Dissents = append(summary.Dissents, view)
	}
	summary.Actionable, summary.ActionableOmitted = Cap(summary.Actionable, ListCap)
	summary.Attention, summary.AttentionOmitted = Cap(summary.Attention, ListCap)
	summary.CurrentArtifacts, summary.CurrentOmitted = Cap(summary.CurrentArtifacts, ListCap)
	summary.StaleArtifacts, summary.StaleOmitted = Cap(summary.StaleArtifacts, ListCap)
	summary.Attempts, summary.AttemptsOmitted = Cap(summary.Attempts, ListCap)
	summary.Dissents, summary.DissentsOmitted = Cap(summary.Dissents, ListCap)
	reverse(summary.Actionable)
	reverse(summary.Attention)
	reverse(summary.CurrentArtifacts)
	reverse(summary.StaleArtifacts)
	reverse(summary.Attempts)
	reverse(summary.Dissents)
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
	// The artifact line names the two facts a reader acts on — a pointer that
	// was withdrawn, and an artifact whose implementation has been replaced —
	// beside the ordinary staleness that reaches almost everything. One
	// "stale" figure covering all of it read as an alarm and answered nothing.
	fmt.Fprintf(&output, "Commitments: %s. Artifacts: %d current, %d stale, %d retired, %d describing a superseded world. Attempts: %d ineffective, %d disputed.\n",
		strings.Join(counts, ", "), summary.Totals.Artifacts-summary.Totals.StaleArtifacts,
		summary.Totals.StaleArtifacts-summary.Totals.RetiredArtifacts, summary.Totals.RetiredArtifacts,
		summary.Totals.WorldStaleArtifacts, summary.Totals.IneffectiveActs, summary.Totals.DisputedActs)
	renderCommitments(&output, "Actionable commitments", summary.Actionable, summary.ActionableOmitted)
	renderCommitments(&output, "Needs attention", summary.Attention, summary.AttentionOmitted)
	renderArtifacts(&output, "Current artifacts", summary.CurrentArtifacts, summary.CurrentOmitted)
	renderArtifacts(&output, "Stale artifacts", summary.StaleArtifacts, summary.StaleOmitted)
	// Before the attempts section, because a dissent is a live objection to
	// something that did take force, while an attempt is something that never
	// did. A reader scanning for what not to act on wants the first one.
	output.WriteString("\n## Dissents\n\n")
	if len(summary.Dissents) == 0 {
		output.WriteString("None.\n")
	} else {
		for _, dissent := range summary.Dissents {
			fmt.Fprintf(&output, "- %s by %s", name(dissent.Event, dissent.Sequence), dissent.Actor)
			if dissent.Against != "" {
				fmt.Fprintf(&output, " against %s", name(dissent.Against, dissent.AgainstSequence))
			}
			if dissent.Text != "" {
				fmt.Fprintf(&output, ": %s", dissent.Text)
			}
			output.WriteString("\n")
		}
		writeOmitted(&output, len(summary.Dissents), summary.DissentsOmitted)
	}
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
		// No stale mark here. Ordinary reasoning staleness reaches most of
		// these rows and stops none of them, so the mark fired everywhere and
		// told a reader nothing about which row to pick. The totals line above
		// carries it per lane — "reported 27 (24 stale)" — and `--all` still
		// prints it per commitment in its own column.
		fmt.Fprintf(output, "- %s — %s → %s — `%s`", item.Status, item.Requester, assignment, name(item.Request, item.Sequence))
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
		fmt.Fprintf(output, "- %s@%s — `%s`", item.Path, short(item.Commit), name(item.Event, item.Sequence))
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
