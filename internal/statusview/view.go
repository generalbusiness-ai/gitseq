// Package statusview builds bounded, truthful views of the durable workroom.
// It never changes the underlying projection; full audit and full rendering
// remain available to callers that explicitly ask for them.
package statusview

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"
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

// hostile reports whether a rune must be shown as an escape rather than sent
// to a terminal as itself: every C0 and C1 control, DEL, and every format
// character — the bidi overrides and isolates, and the zero-width marks that
// let one string print as another. Whitespace never reaches here, because the
// caller folds it first.
func hostile(value rune) bool {
	return value < 0x20 || value == 0x7f || (value >= 0x80 && value <= 0x9f) || unicode.Is(unicode.Cf, value)
}

// neutralized renders a list of user-controlled strings for display. The
// durable values are unchanged; a caller that needs to match a path exactly
// reads the record, not the view.
func neutralized(values []string) []string {
	rendered := make([]string, len(values))
	for index, value := range values {
		rendered[index] = Text(value)
	}
	return rendered
}

// Safe neutralizes user-controlled text a caller renders whole: same escapes
// as Text, but no one-line fold and no byte cap. Newline and tab survive,
// because a caller that shows the whole thing wants its shape; carriage return
// does not, because it repaints a line already written.
func Safe(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		switch {
		case decoded == utf8.RuneError && size == 1:
			fmt.Fprintf(&out, `\x%02x`, value[index])
		case decoded == '\n' || decoded == '\t':
			out.WriteRune(decoded)
		case hostile(decoded):
			if decoded <= 0xff {
				fmt.Fprintf(&out, `\x%02x`, decoded)
			} else {
				fmt.Fprintf(&out, `\u%04x`, decoded)
			}
		default:
			out.WriteString(value[index : index+size])
		}
		index += size
	}
	return out.String()
}

// Text renders user-controlled text as one safe terminal line and caps it by
// bytes. Runs of whitespace fold to single spaces; anything that could move
// the cursor, repaint the screen, reorder the line, or hide itself is written
// as a visible escape. Invalid UTF-8 is escaped byte by byte rather than
// silently replaced, so a reader can tell malformed input from a real U+FFFD.
//
// The durable bytes are untouched. This is what a bounded view shows, not what
// the log holds, and anyone who needs the original can ask for the record.
func Text(value string) string {
	var out strings.Builder
	// safe is the length of the longest prefix that still leaves room for the
	// ellipsis, so truncation lands on a token boundary and never inside an
	// escape.
	safe, separate := 0, false
	for index := 0; index < len(value); {
		decoded, size := utf8.DecodeRuneInString(value[index:])
		var token string
		switch {
		case decoded == utf8.RuneError && size == 1:
			token = fmt.Sprintf(`\x%02x`, value[index])
		case unicode.IsSpace(decoded):
			separate = out.Len() > 0
			index += size
			continue
		case hostile(decoded):
			if decoded <= 0xff {
				token = fmt.Sprintf(`\x%02x`, decoded)
			} else {
				token = fmt.Sprintf(`\u%04x`, decoded)
			}
		default:
			token = value[index : index+size]
		}
		index += size
		width := len(token)
		if separate {
			width++
		}
		if out.Len()+width > TextCap {
			return out.String()[:safe] + "…"
		}
		if separate {
			out.WriteByte(' ')
			separate = false
		}
		out.WriteString(token)
		if out.Len() <= TextCap-len("…") {
			safe = out.Len()
		}
	}
	return out.String()
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

// artifactState reports how a reader should treat an artifact, and any notes
// worth saying about it. Retired and stale are separate facts and both mean
// "not this one" to somebody looking for what is current; they are kept apart
// in the word because a withdrawn pointer and a moved world call for
// different work. It is a function so the totals pass and the shaping pass
// cannot drift about what "current" means.
func artifactState(artifact workroom.Artifact) (string, string) {
	state := "current"
	switch {
	case artifact.Retired:
		state = "retired"
	case artifact.Stale:
		state = "stale"
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
	return state, strings.Join(notes, ", ")
}

func commitmentViews(projection workroom.Projection, statements map[string]workroom.Statement, sequences map[string]int, source []workroom.Commitment) []Commitment {
	if len(source) == 0 {
		return nil
	}
	views := make([]Commitment, 0, len(source))
	for _, commitment := range source {
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
		views = append(views, view)
	}
	return views
}

func artifactViews(sequences map[string]int, source []workroom.Artifact) []Artifact {
	if len(source) == 0 {
		return nil
	}
	views := make([]Artifact, 0, len(source))
	for _, artifact := range source {
		state, notes := artifactState(artifact)
		views = append(views, Artifact{Event: artifact.Event, Sequence: sequences[artifact.Event], Path: Text(artifact.Path), Commit: artifact.Commit, State: state, Notes: notes})
	}
	return views
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
	// Two passes, and the split is the point. The first walks everything,
	// because the totals and the omitted counts are facts about the whole
	// projection and would be wrong if computed from a sample. It keeps only
	// source records, which are already resident and hold no copied text. The
	// second shapes the rows that survive Cap.
	//
	// Shaping is what costs: Text runs strings.Fields and strings.Join, so it
	// allocates twice per field, and ActorName resolves a fingerprint through
	// the roster. Doing that for every commitment in a workroom of any age, to
	// then discard all but ListCap of them, is work spent on rows nobody reads
	// -- and it is spent on attacker-influenced text, which is the part that
	// matters once that text has to be escaped rather than merely trimmed.
	//
	// Nothing about selection depends on a shaped value: lane membership comes
	// from the commitment status, the artifact flags, the decision verdict and
	// the statement kind, and order is projection order throughout. That is
	// what makes moving the boundary safe rather than merely faster.
	var actionableSource, attentionSource []workroom.Commitment
	for _, commitment := range projection.Commitments {
		summary.Totals.Commitments[commitment.Status]++
		if commitment.Stale {
			summary.Totals.StaleCommitments[commitment.Status]++
		}
		if terminal[commitment.Status] {
			continue
		}
		if actionable[commitment.Status] {
			actionableSource = append(actionableSource, commitment)
		} else {
			attentionSource = append(attentionSource, commitment)
		}
	}
	var currentSource, staleSource []workroom.Artifact
	for _, artifact := range projection.Artifacts {
		state, _ := artifactState(artifact)
		switch state {
		case "retired":
			summary.Totals.StaleArtifacts++
			summary.Totals.RetiredArtifacts++
		case "stale":
			summary.Totals.StaleArtifacts++
		}
		if artifact.DescribesSupersededWorld {
			summary.Totals.WorldStaleArtifacts++
		}
		if state == "current" {
			currentSource = append(currentSource, artifact)
		} else {
			staleSource = append(staleSource, artifact)
		}
	}
	var attemptSource []workroom.Decision
	for _, decision := range projection.Decisions {
		switch decision.Verdict {
		case workroom.Ineffective:
			summary.Totals.IneffectiveActs++
		case workroom.Disputed:
			summary.Totals.DisputedActs++
		default:
			continue
		}
		attemptSource = append(attemptSource, decision)
	}
	// Dissent carries no lifecycle and satisfies nothing, so it appears in no
	// commitment lane and no artifact list. Before this it appeared nowhere at
	// all on the human page: the projection knew about it and only a JSON
	// reader could find it.
	var dissentSource []workroom.Statement
	for _, statement := range projection.Statements {
		if statement.Kind != workroom.KindDissent || statement.Retired {
			continue
		}
		dissentSource = append(dissentSource, statement)
	}

	actionableSource, summary.ActionableOmitted = Cap(actionableSource, ListCap)
	attentionSource, summary.AttentionOmitted = Cap(attentionSource, ListCap)
	currentSource, summary.CurrentOmitted = Cap(currentSource, ListCap)
	staleSource, summary.StaleOmitted = Cap(staleSource, ListCap)
	attemptSource, summary.AttemptsOmitted = Cap(attemptSource, ListCap)
	dissentSource, summary.DissentsOmitted = Cap(dissentSource, ListCap)

	summary.Actionable = commitmentViews(projection, statements, sequences, actionableSource)
	summary.Attention = commitmentViews(projection, statements, sequences, attentionSource)
	summary.CurrentArtifacts = artifactViews(sequences, currentSource)
	summary.StaleArtifacts = artifactViews(sequences, staleSource)
	for _, decision := range attemptSource {
		summary.Attempts = append(summary.Attempts, Attempt{Event: decision.Event, Sequence: decision.Sequence, Verdict: string(decision.Verdict), Reason: Text(decision.Reason)})
	}
	for _, statement := range dissentSource {
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
