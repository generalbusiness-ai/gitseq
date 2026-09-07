package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/safetext"
)

func RenderJSON(projection Projection) ([]byte, error) {
	data, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func RenderStatus(projection Projection) []byte {
	var output bytes.Buffer
	sequences := projection.sequences()
	authors := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		authors[statement.Event] = statement.Actor
	}
	output.WriteString("# Workroom status\n\n")
	if summary := projection.Summary(); summary != "" {
		fmt.Fprintf(&output, "%s\n\n", summary)
	}
	output.WriteString("## Requests and commitments\n\n")
	if len(projection.Commitments) == 0 {
		output.WriteString("No commitments.\n")
	} else {
		output.WriteString("| status | qualifiers | requester | assignment | request | waiting on |\n")
		output.WriteString("|---|---|---|---|---|---|\n")
		for _, commitment := range projection.Commitments {
			qualifiers := ""
			if commitment.Stale {
				qualifiers = "stale"
			}
			assignment := short(commitment.Performer)
			if commitment.Promise == "" && commitment.AddressedTo != "" {
				assignment = "addressed to " + short(commitment.AddressedTo) + " — unclaimed"
			}
			fmt.Fprintf(&output, "| %s | %s | %s | %s | %s | %s |\n", escape(commitment.Status), qualifiers, short(commitment.Requester), escape(assignment), name(commitment.Request, sequences), short(commitment.WaitingOn))
		}
	}
	output.WriteString("\n## Reviews\n\n")
	if len(projection.Reviews) == 0 {
		output.WriteString("No reviews.\n")
	} else {
		counts := make(map[string]int)
		for _, review := range projection.Reviews {
			counts[review.Independence]++
		}
		fmt.Fprintf(&output, "%d independent, %d self-signed, %d unresolved.\n",
			counts[IndependenceIndependent], counts[IndependenceSelfReview], counts[IndependenceUnresolved])
		// Only reviews the record cannot vouch for are listed. An independent
		// verdict is the expected case and says nothing a reader must act on;
		// a verdict signed by the implementer, or one whose implementer cannot
		// be identified, is exactly what this section exists to surface.
		var flagged []Review
		for _, review := range projection.Reviews {
			if review.Independence != IndependenceIndependent && !review.Retired {
				flagged = append(flagged, review)
			}
		}
		if len(flagged) > 0 {
			output.WriteString("\n| independence | verdict | reviewer | head | report |\n")
			output.WriteString("|---|---|---|---|---|\n")
			for _, review := range flagged {
				independence := review.Independence
				if review.Independence == IndependenceSelfReview {
					independence = "SELF-SIGNED — reviewer implemented this head"
				}
				fmt.Fprintf(&output, "| %s | %s | %s | %s | %s |\n",
					escape(independence), escape(review.Verdict), short(review.Reviewer), short(review.Head), name(review.Report, sequences))
			}
		}
	}
	output.WriteString("\n## Artifacts\n\n")
	if len(projection.Artifacts) == 0 {
		output.WriteString("No artifacts.\n")
	} else {
		unableToFlare, successionUnrecorded := 0, 0
		// One unrecorded succession at a long-lived path repeats on every later
		// link of that chain, so the count of artifacts overstates how many
		// situations a reader has to act on. Rows and paths are both reported,
		// alongside the count of supersessions actually owed.
		successionPaths := make(map[string]struct{})
		output.WriteString("| state | artifact | event | notes |\n")
		output.WriteString("|---|---|---|---|\n")
		for _, artifact := range projection.Artifacts {
			status := "current"
			switch {
			case artifact.Succeeded:
				// Replaced, not withdrawn. The retirement named where the
				// behaviour went, so a reader following this row has somewhere
				// to go. A bare RETIRED does not, and the two need reading
				// differently.
				status = "SUCCEEDED — replaced at the same path"
			case artifact.Retired:
				status = "RETIRED — withdrawn with no successor"
			case artifact.DescribesSupersededWorld:
				status = "STALE — describes a superseded world"
			case artifact.Stale:
				status = "STALE"
			}
			var notes []string
			if artifact.UnableToFlare {
				unableToFlare++
				notes = append(notes, "unable to flare")
			}
			if len(artifact.IneffectiveBases) > 0 {
				notes = append(notes, "rests on ineffective support: "+namesOf(artifact.IneffectiveBases, sequences))
			}
			if artifact.SuccessionUnrecorded {
				successionUnrecorded++
				successionPaths[artifact.Path] = struct{}{}
				note := "succession not recorded"
				if artifact.LivePredecessors > 1 {
					note = fmt.Sprintf("succession not recorded (%d live predecessors)", artifact.LivePredecessors)
				}
				notes = append(notes, note)
			}
			fmt.Fprintf(&output, "| %s | %s@%s | %s | %s |\n", status, escape(artifact.Path), short(artifact.Commit), name(artifact.Event, sequences), escape(strings.Join(notes, ", ")))
		}
		if unableToFlare > 0 || successionUnrecorded > 0 {
			output.WriteString("\n")
		}
		if unableToFlare > 0 {
			fmt.Fprintf(&output, "%d cite no basis and can never go stale; their silence is not currency.\n", unableToFlare)
		}
		if successionUnrecorded > 0 {
			// Rows record what happened; the owed count is what to do about it,
			// and the two differ. With A, B and C at one path the repair is two
			// supersessions, and superseding A clears B's warning without
			// touching C's. Where every later artifact was itself withdrawn the
			// row still stands as history while nothing is owed, so the two
			// figures are stated separately rather than as one number.
			fmt.Fprintf(&output, "%d artifacts across %d paths follow a live artifact covering the merge without superseding or accounting for it; supersessions still owed: %d, counting once each predecessor a live successor stands in for.\n", successionUnrecorded, len(successionPaths), projection.OmittedSupersessions)
		}
		for _, statement := range projection.Statements {
			for _, accounting := range statement.MergeLeftLive {
				fmt.Fprintf(&output, "%s carries %s.\n", name(statement.Event, sequences), escape(renderLeftLive(accounting, sequences, authors, projection.Actors)))
			}
		}
	}
	renderDissent(&output, projection, sequences)
	renderRatified(&output, projection, sequences)
	renderUninterpretable(&output, projection, sequences)
	output.WriteString("\n## Attempts\n\n")
	for _, decision := range projection.Decisions {
		if decision.Verdict != Effective {
			fmt.Fprintf(&output, "- `%s` — **%s**: %s\n", name(decision.Event, sequences), decision.Verdict, escape(explainDecisionReason(decision.Reason)))
		}
	}
	return output.Bytes()
}

// renderDissent lists every effective, unretired dissent with the record it
// stands against and that record's current state. The bounded status shows
// the newest twenty; the complete render must not show fewer than the
// summary, so it shows them all. A dissent rests on the act it concerns, so
// the first basis names it.
func renderDissent(output *bytes.Buffer, projection Projection, sequences map[string]int) {
	verdicts := projection.verdicts()
	states := projection.recordStates()
	output.WriteString("\n## Standing dissent\n\n")
	count := 0
	for _, statement := range projection.Statements {
		if statement.Kind != KindDissent || statement.Retired || verdicts[statement.Event] != Effective {
			continue
		}
		count++
		fmt.Fprintf(output, "- %s by %s", name(statement.Event, sequences), short(statement.Actor))
		if bases := projection.Provenance[statement.Event]; len(bases) > 0 {
			state := states[bases[0]]
			if state == "" {
				state = "unknown"
			}
			fmt.Fprintf(output, " against %s (%s)", name(bases[0], sequences), state)
		}
		if statement.Text != "" {
			fmt.Fprintf(output, ": %s", escape(statement.Text))
		}
		output.WriteString("\n")
	}
	if count == 0 {
		output.WriteString("None.\n")
	}
}

// renderRatified lists every statement whose ratification stands now, with
// the act that ratifies it. Ratified is the fold's own reading of authority
// and appears nowhere else on the human page: a proposal that became a
// decision was previously visible only to a JSON reader.
func renderRatified(output *bytes.Buffer, projection Projection, sequences map[string]int) {
	states := projection.recordStates()
	output.WriteString("\n## Ratified statements\n\n")
	count := 0
	for _, statement := range projection.Statements {
		if !statement.Ratified {
			continue
		}
		if count == 0 {
			output.WriteString("| kind | statement | ratified by | state |\n")
			output.WriteString("|---|---|---|---|\n")
		}
		count++
		fmt.Fprintf(output, "| %s | %s | %s | %s |\n", escape(string(statement.Kind)), name(statement.Event, sequences), name(statement.RatifiedBy, sequences), states[statement.Event])
	}
	if count == 0 {
		output.WriteString("None.\n")
	}
}

// renderUninterpretable lists the records the fold could not read as any
// governed kind: statements of an undefined kind, grouped by the kind they
// claimed, and records whose payload could not be interpreted at all. Each
// also appears among the attempts with its refusal; this section gives the
// undefined ones back their text, which is the only disposition they have.
func renderUninterpretable(output *bytes.Buffer, projection Projection, sequences map[string]int) {
	output.WriteString("\n## Uninterpretable records\n\n")
	texts := make(map[string]string, len(projection.Statements))
	for _, statement := range projection.Statements {
		texts[statement.Event] = statement.Text
	}
	kinds := make([]string, 0, len(projection.OpaqueKinds))
	for kind := range projection.OpaqueKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	count := 0
	for _, kind := range kinds {
		for _, event := range projection.OpaqueKinds[kind] {
			count++
			fmt.Fprintf(output, "- undefined kind `%s`: %s", escape(kind), name(event, sequences))
			if text := texts[event]; text != "" {
				fmt.Fprintf(output, ": %s", escape(text))
			}
			output.WriteString("\n")
		}
	}
	for _, decision := range projection.Decisions {
		if decision.Verdict == Uninterpretable {
			count++
			fmt.Fprintf(output, "- uninterpretable payload: %s: %s\n", name(decision.Event, sequences), escape(decision.Reason))
		}
	}
	if count == 0 {
		output.WriteString("None.\n")
	}
}

// verdicts indexes every record's decision, for the same reason sequences
// does: a statement row survives its own refusal, so the row alone cannot say
// whether the record took force.
func (p Projection) verdicts() map[string]Verdict {
	index := make(map[string]Verdict, len(p.Decisions))
	for _, decision := range p.Decisions {
		index[decision.Event] = decision.Verdict
	}
	return index
}

// recordStates says, for every record the fold decided, the one word a reader
// needs beside a record another record points at. A record the fold refused
// is named by its verdict — ineffective, undefined-kind, uninterpretable —
// not by its lifecycle: it is neither stale nor retired only because nothing
// refused ever takes force, and calling it current would say the opposite of
// what happened. Decisions are the source, one per record, so a payload the
// fold could not even read into a statement row is still named. Effective
// statements read current, stale or retired; effective acts read by type.
// Unknown is reserved for a target this log does not hold at all.
func (p Projection) recordStates() map[string]string {
	states := make(map[string]string, len(p.Decisions))
	for _, decision := range p.Decisions {
		if decision.Verdict != Effective {
			states[decision.Event] = string(decision.Verdict)
		}
	}
	for _, statement := range p.Statements {
		if _, refused := states[statement.Event]; refused {
			continue
		}
		switch {
		case statement.Retired:
			states[statement.Event] = "retired"
		case statement.Stale:
			states[statement.Event] = "stale"
		default:
			states[statement.Event] = "current"
		}
	}
	for _, act := range p.Acts {
		if _, refused := states[act.Event]; refused {
			states[act.Event] += " " + act.Type + " act"
			continue
		}
		states[act.Event] = act.Type + " act"
	}
	return states
}

func namesOf(events []string, sequences map[string]int) string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, name(event, sequences))
	}
	return strings.Join(names, ", ")
}

func renderLeftLive(accounting LeftLiveAccounting, sequences map[string]int, authors map[string]string, actors map[string]ActorState) string {
	if !accounting.Verified {
		testimony := accounting.Class
		if accounting.Artifact != "" {
			testimony += " for " + name(accounting.Artifact, sequences)
		}
		if accounting.Commitment != "" {
			testimony += " under " + name(accounting.Commitment, sequences)
		}
		if testimony == "" {
			testimony = "entry"
		}
		return "UNVERIFIED left-live testimony: " + testimony + " — " + accounting.Reason
	}
	if accounting.Class == "sibling" {
		return "left live at merge: sibling under " + name(accounting.Commitment, sequences) + " — artifact " + name(accounting.Artifact, sequences)
	}
	if accounting.Class == "carried" {
		return "left live at merge: carried current artifact " + name(accounting.Artifact, sequences)
	}
	author := authors[accounting.Artifact]
	responsible := short(author)
	if actor, ok := actors[author]; ok && actor.Name != "" {
		responsible = "@" + actor.Name
	}
	if responsible == "" {
		responsible = "artifact author"
	}
	return "left live at merge: abandoned artifact " + name(accounting.Artifact, sequences) + ", retirement owed by " + responsible
}

func RenderProvenance(projection Projection, event string) []byte {
	var output bytes.Buffer
	seen := make(map[string]bool)
	var walk func(string, int)
	walk = func(current string, depth int) {
		if current == "" {
			return
		}
		fmt.Fprintf(&output, "%s%s", strings.Repeat("  ", depth), current)
		if seen[current] {
			output.WriteString(" (already shown)\n")
			return
		}
		output.WriteByte('\n')
		seen[current] = true
		for _, basis := range projection.Provenance[current] {
			walk(basis, depth+1)
		}
	}
	walk(event, 0)
	return output.Bytes()
}

// escape is the one boundary every actor-controlled string crosses on its
// way onto this page: the shared safetext policy first, so a control byte, a
// newline or a bidi override is shown as a visible escape and cannot add a
// line or repaint a terminal, then the pipe, so a cell cannot end its table
// row. The durable bytes are untouched.
func escape(value string) string {
	return strings.ReplaceAll(safetext.Safe(value), "|", "\\|")
}

// sequences indexes every durable record by its number. Decisions are the right
// source because there is exactly one per record: statements would miss ratify
// and supersede, which are events a citation can perfectly well name.
func (p Projection) sequences() map[string]int {
	index := make(map[string]int, len(p.Decisions))
	for _, decision := range p.Decisions {
		index[decision.Event] = decision.Sequence
	}
	return index
}

// name keeps the workroom-local sequence beside the only identifier durable
// commands accept. #N is readable but does not resolve at an action boundary.
func name(event string, sequences map[string]int) string {
	if sequence := sequences[event]; sequence > 0 {
		return fmt.Sprintf("#%d %s", sequence, event)
	}
	return event
}

func explainDecisionReason(reason string) string {
	if reason == "dangling promise has no request" {
		return reason + ". Add exactly one live request event with --rests-on. See docs/reference/gs/state.md#citing"
	}
	if reason == "report cites a request other than the one its promise answers" {
		return reason + ". File against the one live promise you made. See docs/reference/gs/state.md#citing"
	}
	return reason
}

func short(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:8] + "…" + value[len(value)-7:]
}

func SortedStatementIDs(projection Projection) []string {
	ids := make([]string, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		ids = append(ids, statement.Event)
	}
	sort.Strings(ids)
	return ids
}
