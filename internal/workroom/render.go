package workroom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
			fmt.Fprintf(&output, "%d artifacts across %d paths follow a live artifact at the same path without superseding it; supersessions still owed: %d, counting once each predecessor a live successor stands in for.\n", successionUnrecorded, len(successionPaths), projection.OmittedSupersessions)
		}
	}
	output.WriteString("\n## Attempts\n\n")
	for _, decision := range projection.Decisions {
		if decision.Verdict != Effective {
			fmt.Fprintf(&output, "- `%s` — **%s**: %s\n", name(decision.Event, sequences), decision.Verdict, escape(decision.Reason))
		}
	}
	return output.Bytes()
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

func escape(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
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

// name renders an event the way a reader says it. The fallback is the whole
// identifier rather than an abbreviation: `short` elides the middle so its
// output is visibly incomplete, which is right for a git object a reader can
// still resolve, and wrong for an event name that must round-trip.
func name(event string, sequences map[string]int) string {
	if sequence := sequences[event]; sequence > 0 {
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

func SortedStatementIDs(projection Projection) []string {
	ids := make([]string, 0, len(projection.Statements))
	for _, statement := range projection.Statements {
		ids = append(ids, statement.Event)
	}
	sort.Strings(ids)
	return ids
}
