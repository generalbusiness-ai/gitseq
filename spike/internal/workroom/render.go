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
	output.WriteString("# Workroom status\n\n")
	if summary := projection.Summary(); summary != "" {
		fmt.Fprintf(&output, "%s\n\n", summary)
	}
	output.WriteString("## Commitments\n\n")
	if len(projection.Commitments) == 0 {
		output.WriteString("No commitments.\n")
	} else {
		output.WriteString("| status | requester | performer | request | waiting on |\n")
		output.WriteString("|---|---|---|---|---|\n")
		for _, commitment := range projection.Commitments {
			fmt.Fprintf(&output, "| %s | %s | %s | %s | %s |\n", escape(commitment.Status), short(commitment.Requester), short(commitment.Performer), short(commitment.Request), short(commitment.WaitingOn))
		}
	}
	output.WriteString("\n## Artifacts\n\n")
	if len(projection.Artifacts) == 0 {
		output.WriteString("No artifacts.\n")
	} else {
		unableToFlare, successionUnrecorded := 0, 0
		output.WriteString("| state | artifact | event | notes |\n")
		output.WriteString("|---|---|---|---|\n")
		for _, artifact := range projection.Artifacts {
			status := "current"
			switch {
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
				notes = append(notes, "succession not recorded")
			}
			fmt.Fprintf(&output, "| %s | %s@%s | %s | %s |\n", status, escape(artifact.Path), short(artifact.Commit), short(artifact.Event), escape(strings.Join(notes, ", ")))
		}
		if unableToFlare > 0 || successionUnrecorded > 0 {
			output.WriteString("\n")
		}
		if unableToFlare > 0 {
			fmt.Fprintf(&output, "%d cite no basis and can never go stale; their silence is not currency.\n", unableToFlare)
		}
		if successionUnrecorded > 0 {
			fmt.Fprintf(&output, "%d follow a live artifact at the same path without superseding it, so anything resting on that predecessor still reads current.\n", successionUnrecorded)
		}
	}
	output.WriteString("\n## Attempts\n\n")
	for _, decision := range projection.Decisions {
		if decision.Verdict != Effective {
			fmt.Fprintf(&output, "- `%s` — **%s**: %s\n", short(decision.Event), decision.Verdict, escape(decision.Reason))
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
