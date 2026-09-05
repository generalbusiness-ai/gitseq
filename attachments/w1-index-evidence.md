# W1 classifier index: exact implementation evidence

Candidate: 8e30e501c1eae67bfc957f3e3971e4307a51ed8f, branch
request/worktree-classification-index, one commit over main
2348ebe66e226a889f08a1b90ccc3d6a45f437e4. Draft PR 19.

Assigned request: b9cae3dabb72ab2ae2d9b450016d87a651914705.
Implementation promise: 60e23b056b904281e0bc2e78aef572d74b9595e9.
This reports source implementation only. Independent approval, sealed delivery,
push and a fresh ownership-safe W1 operational pass remain separate steps.
No checkout was deleted, no existing service was replaced, and no limit rose.

## Change and complexity

The previous classifier repeatedly compared every checkout with every row and
all heads attached to that row, then sorted every match. Its existing counter
would charge 220,933 steps for the captured case, exceeding 65,536 during the
second checkout. The original counter did not also charge graph-cache walks
or sorting comparisons; the new counter includes graph traversal and heap
comparisons, so these are not identical micro-operation counts.

One statement pass now joins only fold-selected receipt IDs and indexes every
direct head, commit and branch association, including retired artifacts and
older heads. It retains separate rows for separate promises. Statements with
no head/commit/branch value cannot add an association and skip that expansion.
Unique object and target indexes are built alongside these joins.

Checkout membership propagates in topological order through the same captured
immutable landingGraph. A bit per checkout is carried in machine words;
there is no new checkout cap or persistent cache. Missing boundaries retain
positive evidence but cannot prove absence. Cycles, cancellation or exhausted
walk/inspection budgets discard the entire batch. Target and remote receipt
measurements still use the existing graph and selected receipt evidence.

Each commitment joins its heads and branches to those membership masks once.
Rows with identical per-checkout ranks share a group: an exact total plus the
newest 20 row indexes. A heap of matching group cursors selects each checkout's
20 highest-ranked/newest rows. No omitted row contributes to a fabricated
settlement; protection and exact-tip settlement are accumulated before output
selection. This avoids materializing the checkout-by-row match product while
preserving exact omission counts and distinct promise identities.

Let S be statements, A visited direct associations, H row/head links, V/E the
captured graph, C checkouts, W=ceil(C/64), and G distinct match groups. The main
index work is O(S+A+(V+E+H)W), plus branch joins and existing bounded receipt
measurements. Output selection is O(CG + C*20*log G), rather than a full
checkout-by-row/head scan and full per-checkout sort. Groups retain at most
20 indexes each, and graph membership uses O(VW) words. All actual association,
word, graph and heap comparison visits consume the existing inspection budget;
Git input/output collectors retain their existing independent hard bounds.

## Measured outcomes

The committed anonymized fixture preserves 35 checkouts, 2,402 commitment rows,
12,012 statements, direct associations, selected receipts and a 1,248-node
captured graph. Its production index, ref refresh, measurements, membership
join and output selection consume 61,827 of 65,536 steps. Every result equals
the original exhaustive algorithm, including row order, primary fields, exact
omission counts and protection. All 35 captured checkouts remain protected.
The fixture omits text, identities, keys, paths, signatures and attachments;
all technical IDs and branch/repository/checkout names are consistently replaced.

A real HTTP GET /v0/worktrees through service.Handler in httptest.NewServer,
with app.Open on the live repository, returned HTTP 200 and 36 protected
checkouts with an empty deletable list. Warm elapsed time was 0.909928458 s.
Cold elapsed time was 49.744314958 s, including cold durable projection loading;
the three-second deadline applies inside ClassifyWorktrees, not that earlier
projection load. This was the actual route and live room, not a mocked fold
or browser acceptance. The temporary test server was closed. The ordinary
resident stayed running. A separate attempted gs serve correctly refused a
second service owner; its failed startup did not change the existing resident.

## Validation and limitations

Passed: focused worktree/receipt tests; captured-room differential; complete,
missing-parent, missing-object, shallow and missing-ref comparisons; 130-view
multiword membership; explicit distinct promises, every direct head/commit and
branch, retired artifact associations and selected receipts; existing live-ref
refresh/current/dirty/target/abandoned protection; 128-way fanout positive and
256/512-way refusal; cancellation, walk exhaustion, cycle and partial-field
clear controls; app/service race suites; final added index tests under race;
go vet ./...; documentation gates; candidate build.

Four actual Go overlay omission controls failed discriminating assertions:
removing direct head association; capping a group's exact total at 20;
treating an incomplete boundary as complete; leaving partial rows after failure.
The incomplete-boundary control initially failed to compile because the edit
left an unused loop variable; it was corrected to a compiling no-op and then
failed the intended ancestry assertion. No source files were changed by the
controls. Restored source tests and race checks pass.

The full go test ./... run passed every package except cmd/gitseq-mcp, where
TestWhoamiRetriesOneConcurrentFrontierMove and
TestWhoamiBoundsStallsAndRejectsRedirects exceeded timing expectations during
concurrent full/race/cold-endpoint load. Both pass in an isolated exact-source
recheck (2.04 s and 2.65 s). The complete MCP package recheck also passes (90.961 s); this evidence
does not claim the original full-suite command passed. Every package now has a
passing exact-source run, with the two original timing failures retained. CI 33961130242 is running;
Security 33961130200 passed at the exact candidate. Approval must inspect final
CI and remaining validation, not infer them from a green focused test.

## Required review conclusions

Architecture: layer 7 changes the internal advisory classifier algorithm and
updates architecture.md and landing-observations.md at this exact head. The
layer-6 fold/receipt/authority contract and wire fields are preserved. There is
one immutable Git graph, no new persistent cache and no deletion operation.

Security: treat projection-provided head/branch associations and local Git
availability conservatively. Preserve exact object admission, all existing
caps, missing/shallow unknowns, current ref refresh, unsettled and approved debt,
selected receipt authority and all-partial-clear behavior. No fetch, signature,
secret, identity, lifecycle or merge authorization change is intended.

Simplification: retain one receipt-field helper and one association pass;
reuse graph evidence for primary approval checks; keep only 20 row indexes per
equivalent rank group plus the exact count. Please request changes for a smaller
approach that meets the same bounds and preserves all required evidence.
