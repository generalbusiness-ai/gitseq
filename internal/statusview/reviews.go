package statusview

import (
	"fmt"

	"github.com/generalbusiness-ai/gitseq/internal/app"
	"github.com/generalbusiness-ai/gitseq/internal/workroom"
)

const (
	ReviewListDefault = 20
	ReviewListMax     = 50
)

// ReviewGate answers one question about the whole log rather than about one
// row: is the review queue quiet enough to run an irreversible step? It is the
// precondition the retirement runbook states — no review request still waiting
// for its first verdict, and no approval still out of the branch — and it is
// deliberately a fixed answer rather than a filter callers compose, because a
// gate that can be assembled wrongly is worse than no gate on a step that
// waiting cannot undo.
//
// The counts are whole-log totals and the lists beside them are bounded
// samples. Reporting a total next to a shortened list is the honest shape: a
// caller sees how much there is even when the page cannot name all of it.
type ReviewGate struct {
	Frontier Frontier `json:"frontier"`
	// Named counts live requests that name an artifact at all. Naming one is
	// what makes a request a review request; whether the name resolves is a
	// separate fact reported in Unresolved.
	Named int `json:"named"`
	// Awaiting counts those no effective verdict has settled.
	Awaiting int `json:"awaiting"`
	// Unresolved counts named artifact references that resolve to no live
	// artifact — an unexpanded placeholder, or a head that never merged. They
	// stay inside Named and Awaiting rather than being dropped: a malformed
	// review request that left the total silently once reported a queue as
	// quieter than it was, and a gate that can declare quiet while work is
	// outstanding is the one failure this cannot have.
	Unresolved           int      `json:"unresolved"`
	AwaitingRequests     []string `json:"awaiting_requests"`
	AwaitingOmitted      int      `json:"awaiting_requests_omitted,omitempty"`
	UnresolvedReferences []string `json:"unresolved_references"`
	UnresolvedOmitted    int      `json:"unresolved_references_omitted,omitempty"`
	// ApprovedHeads are the exact commits of ratified approvals that could
	// still be acted on, in the order the verdicts were signed. A retired
	// approval, and an approval or artifact describing a superseded world, are
	// left out: those heads are waiting for a current behaviour anchor, not
	// for a fresh verdict, and counting them would make the precondition
	// unreachable.
	//
	// The head comes from the review record, which carries the exact commit
	// the verdict was signed over. Going through the artifact's commit field
	// asks a second question whose answer only happens to agree.
	//
	// This one list is complete rather than capped, and the other two are
	// samples beside a total. A capped gate would let an omitted head decide
	// nothing while the caller read "quiet", and a gate that can declare quiet
	// with work outstanding is the one failure an irreversible step cannot
	// have. The list is bounded by the ratified approvals a workroom holds,
	// which is what the question is actually about.
	ApprovedHeads []string `json:"approved_heads"`
	Degraded      bool     `json:"degraded,omitempty"`
}

// Quiet reports whether nothing is still awaiting a first verdict. Whether the
// approved heads have landed is a question for Git, not for the projection, so
// it is settled by the caller that can ask.
func (gate ReviewGate) Quiet() bool { return gate.Awaiting == 0 }

// BuildReviewGate reads the settlement of every review request from the
// projection the fold produced.
//
// Two things it deliberately does not do. It does not decide settlement by
// scanning statements for a report carrying an approved verdict: the fold
// refuses some of those, and a report the fold refused must not be able to
// release an irreversible step. It reads projection.Reviews, which the fold
// populates only from reports it judged effective. And it keys settlement by
// the request event through projection.Commitments rather than by the artifact
// a request names, because several requests can name one artifact and a
// verdict on one of them settles only that one.
//
// Either canonical verdict settles a review. Waiting cannot turn a
// changes-requested into an approval, so counting only approvals would leave
// every closed-with-changes review in the total for ever.
func BuildReviewGate(durable app.Snapshot, limit int, degraded bool) (ReviewGate, error) {
	if limit == 0 {
		limit = ReviewListDefault
	}
	if limit < 1 || limit > ReviewListMax {
		return ReviewGate{}, fmt.Errorf("limit must be between 1 and %d", ReviewListMax)
	}
	projection := durable.Projection

	live := make(map[string]bool, len(projection.Artifacts))
	for _, artifact := range projection.Artifacts {
		if !artifact.Retired {
			live[artifact.Event] = true
		}
	}
	effective := make(map[string]bool, len(projection.Reviews))
	for _, review := range projection.Reviews {
		effective[review.Report] = true
	}
	settled := make(map[string]bool, len(projection.Commitments))
	for _, commitment := range projection.Commitments {
		if commitment.Report != "" && effective[commitment.Report] {
			settled[commitment.Request] = true
		}
	}
	worldStale := make(map[string]bool)
	for _, statement := range projection.Statements {
		if statement.DescribesSupersededWorld {
			worldStale[statement.Event] = true
		}
	}

	gate := ReviewGate{
		Frontier: Frontier{Genesis: durable.Genesis, Head: durable.Head, Depth: durable.Depth},
		Degraded: degraded,
	}
	var awaiting, unresolved []string
	for _, statement := range projection.Statements {
		if statement.Retired || statement.Kind != workroom.KindRequest || statement.Body["artifact"] == "" {
			continue
		}
		gate.Named++
		if !settled[statement.Event] {
			gate.Awaiting++
			awaiting = append(awaiting, statement.Event)
		}
		if !live[statement.Body["artifact"]] {
			gate.Unresolved++
			unresolved = append(unresolved, statement.Event)
		}
	}

	seen := make(map[string]bool)
	gate.ApprovedHeads = []string{}
	for _, review := range projection.Reviews {
		if review.Verdict != "approved" || !review.Ratified || review.Retired {
			continue
		}
		if worldStale[review.Report] || (review.Artifact != "" && worldStale[review.Artifact]) {
			continue
		}
		// A review with no head names no commit for Git to answer about.
		// Carrying it as an empty string would make the branch question
		// unanswerable rather than answered.
		if review.Head == "" || seen[review.Head] {
			continue
		}
		seen[review.Head] = true
		gate.ApprovedHeads = append(gate.ApprovedHeads, review.Head)
	}

	gate.AwaitingRequests, gate.AwaitingOmitted = Cap(awaiting, limit)
	gate.UnresolvedReferences, gate.UnresolvedOmitted = Cap(unresolved, limit)
	if gate.AwaitingRequests == nil {
		gate.AwaitingRequests = []string{}
	}
	if gate.UnresolvedReferences == nil {
		gate.UnresolvedReferences = []string{}
	}
	return gate, nil
}
