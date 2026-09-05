# Worktree classification: focused complexity assessment

Scope: delivered Gitseq 2348ebe6, internal/app/worktree_landing.go and landing_graph.go, current worktree tests, and W1 report396031ee with its exact-tip manifest. Stack: Go, Git plumbing and the resident HTTP service. This assessment changes no source files.

## Finding and recommendation

The highest-impact issue is the nested scan at worktree_landing.go:214–277. For every checkout it scans every commitment row and every named head association, even when no association matches. Cached ancestry avoids a Git process per comparison, but the repeated visits still consume the shared work budget.

With C checkouts, R commitment rows, H row/head associations, S statements, E artifact provenance edges and A event/commitment joins, the classification work is O(S + E + A + C(R + H)), plus bounded Git graph work and sorting of the actual matches. Repeated keys can still produce a large A; the existing association guard correctly bounds that expansion and must remain.

Build bounded indices by exact named head and normalized branch once, then join them to the already bounded captured ancestry. Aggregate missing-head uncertainty for protecting rows without rescanning all rows per checkout. Preserve every matching row and its current rank, including the rank boost for unsettled work, exact-tip preference, branch preference and descending row-index tie break. Preserve the 20-row display limit and accurate omitted count. Do not collapse distinct promises merely because they share a request, head or label.

Target O(S + E + A + G + C + J), plus existing match ordering, where G is explicitly bounded graph/index traversal and J is actual matched associations. This is an implementation target, not a measured speedup. Dense real associations can still exhaust the budget: return honest unknown rather than truncating protection or increasing limits. Merely replacing row heads with unique-head comparisons for every checkout may still exceed 65536; demonstrate the full operation on the captured room.

## Evidence and risk

Independent replay of the explicit source charges against captured depth18635 confirms 35 checkouts, 2402 commitments, 12012 statements, 7973 artifact provenance edges, 9806 event/commitment associations and 2305 row/head associations. Including the initial checkout charge, preprocessing costs 56153; each checkout adds 4708, for a lower bound of 220933. The fixed 65536 budget expires during checkout2. The author measured the repeat endpoint at0.944seconds, below the3second deadline. Root verified the arithmetic from the captured JSON; root did not repeat the endpoint timing. The report's intermediate totals include the initial35checkout charge.

Risk is moderate because missing an unresolved association could produce unsafe deletion advice. Keep the fixed work/object/ref/tip/graph bounds, all-or-nothing reset after exhaustion/cancellation, current ref refresh, unknown-versus-negative distinction, verified receipt/remote/target evidence, and dirty/current/detached/target protections. Use per-call indices over the immutable snapshot; no persistent cache or second lifecycle model is needed.

## Validation and delivery

Existing tests read: TestWorktreeClassificationProtectsEveryNamedHead, TestWorktreeAssociationsShareTotalBudget and TestWorktreeBudgetExhaustionDiscardsPartialDeletionAdvice, plus service remote/routing controls. No test suite was rerun for this source-read assessment. The implementation must retain these protections and add a captured-real-room regression, small-fixture differential checks of row ordering and advice, missing-object and incomplete-graph controls, duplicate associations, cancellation and boundary tests. Count index build, traversal and joins rather than hiding work behind a new helper. An omission control must reproduce the old repeated-scan exhaustion or an actual lost protection.

Run focused app/service tests and race checks, required repository validation and exact-head independent Architecture/Security/Simplification review. Measure the actual current endpoint after delivered repair before rerunning owner-safe W1 cleanup. No deletion is justified by this report or by an unknown classification.
