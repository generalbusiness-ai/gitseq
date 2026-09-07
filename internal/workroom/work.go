package workroom

import (
	"fmt"
	"strings"

	"github.com/generalbusiness-ai/gitseq/internal/safetext"
)

// The named populations a reader counts work in. They are the fold's own
// lifecycle words grouped once, here, because every surface was grouping them
// separately: the browser's tabs said "open 5" while the command line and the
// resident summary said "open 1", and both were right about different
// questions. One derivation answers both, and the surfaces print the same
// numbers or a test fails.
//
// The grouping is a partition of the lifecycle words the fold emits. Six mean
// work is in flight; satisfied means it completed; five mean it stopped
// without completing; stale on its own means a commitment nobody reported and
// whose reasoning moved. Nothing is in two of them.
var (
	openLifecycles     = []string{"open", "promised", "reported", "awaiting-review", "awaiting-authorization", "awaiting-landing"}
	closedNotCompleted = map[string]bool{
		"superseded": true, "cancelled": true, "reneged": true, "withdrawn": true, "abandoned": true,
	}
)

// WorkScopeWorkroom says a summary counted every commitment at this frontier.
// A count taken from a search or from one actor's lane is a different number
// and must say so rather than be read as this one.
const WorkScopeWorkroom = "workroom"

// WorkSummary is the named commitment populations, counted once for every
// surface: the bounded and complete status pages, the resident summary, the
// MCP totals and the browser's tabs.
//
// Three rules hold it together, and each one was a way of being wrong before:
//
//   - Open, Completed, ClosedNotCompleted, Stale and Other partition the
//     commitments. Their sum is Commitments exactly.
//   - ReasoningMoved and ArtifactLandingAudit are not members of that
//     partition. The first is a subset of Open; the second is an audit that
//     may fall on any row, live or finished. Adding every number here is not a
//     commitment total.
//   - Acts awaiting ratification are a different duty, owed by a role holder
//     rather than by a performer, and are counted nowhere in this struct.
type WorkSummary struct {
	// Scope names the selection these counts cover, so a filtered count is
	// never read as a workroom-wide one.
	Scope       string `json:"scope"`
	Commitments int    `json:"commitments"`
	// Open is the grouped population: every commitment still in flight. It is
	// deliberately not the lifecycle word "open", which is one of the six
	// inside it and is reported separately in OpenLifecycles.
	Open int `json:"open"`
	// OpenLifecycles breaks Open down by the fold's own word, always carrying
	// all six keys, so a zero lane is stated rather than missing.
	OpenLifecycles     map[string]int `json:"open_lifecycles"`
	Completed          int            `json:"completed"`
	ClosedNotCompleted int            `json:"closed_not_completed"`
	Stale              int            `json:"stale"`
	// Other counts a lifecycle word this grouping does not know. It is zero in
	// every shipped fold; it exists so that a status the fold learns to emit
	// later is visibly uncounted instead of quietly dropped out of the total.
	Other int `json:"other,omitempty"`
	// ReasoningMoved is the open commitments carrying ordinary staleness: a
	// subset of Open, not a seventh population.
	ReasoningMoved int `json:"reasoning_moved"`
	// ArtifactLandingAudit is approved-but-not-landed across every commitment,
	// finished ones included. It overlaps the populations above.
	ArtifactLandingAudit int `json:"artifact_landing_audit"`
}

// WorkOf counts the whole workroom at this frontier.
func WorkOf(projection Projection) WorkSummary {
	summary := WorkSummary{
		Scope: WorkScopeWorkroom, Commitments: len(projection.Commitments),
		OpenLifecycles: make(map[string]int, len(openLifecycles)),
	}
	for _, status := range openLifecycles {
		summary.OpenLifecycles[status] = 0
	}
	for _, commitment := range projection.Commitments {
		if commitment.ApprovedNotLanded {
			summary.ArtifactLandingAudit++
		}
		_, isOpen := summary.OpenLifecycles[commitment.Status]
		switch {
		case isOpen:
			summary.Open++
			summary.OpenLifecycles[commitment.Status]++
			if commitment.Stale {
				summary.ReasoningMoved++
			}
		case commitment.Status == "satisfied":
			summary.Completed++
		case closedNotCompleted[commitment.Status]:
			summary.ClosedNotCompleted++
		case commitment.Status == "stale":
			summary.Stale++
		default:
			summary.Other++
		}
	}
	return summary
}

// RenderWork is the one wording for these counts. The bounded page and the
// complete page print the identical two lines, because the whole point is that
// a reader can hold two surfaces side by side and see the same figures.
func RenderWork(summary WorkSummary) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Work, %s: %d open, %d completed, %d closed and not completed, %d stale and not in flight",
		safetext.Safe(summary.Scope), summary.Open, summary.Completed, summary.ClosedNotCompleted, summary.Stale)
	if summary.Other > 0 {
		fmt.Fprintf(&out, ", %d in no named population", summary.Other)
	}
	fmt.Fprintf(&out, ", of %d commitments.\n", summary.Commitments)
	lanes := make([]string, 0, len(openLifecycles))
	for _, status := range openLifecycles {
		lanes = append(lanes, fmt.Sprintf("%s %d", status, summary.OpenLifecycles[status]))
	}
	fmt.Fprintf(&out, "Open by lifecycle: %s. Overlapping counts: %d open resting on reasoning that moved, %d under an artifact landing audit. Acts awaiting ratification are a separate duty and are not in the commitment total.\n",
		strings.Join(lanes, ", "), summary.ReasoningMoved, summary.ArtifactLandingAudit)
	return out.String()
}
