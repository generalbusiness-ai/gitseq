---
date: 2026-09-04
status: Review note for Hugh. Recommendations only; no workflow change is adopted.
author: planner
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:de44229913557bf2242b592c78f5cc987bb44e06
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:22cb07e91e19956f4ad81ba0b2ba1f09e74ee1ad
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:45464a2d24d116615e95f395bf905a3a023b7965
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:203ed4c78aaa67ab5a05253b949f62dfc4c084fd
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:676dfeada675945033bb56347b52d53f28f71560
---

# Reduce the work needed to deliver reviewed work

The strongest immediate opportunity is to prepare a valid review and delivery
plan before asking a reviewer to sign. Three recent Gitseq deliveries needed
another approval of an unchanged head because the first artifact in the review
did not identify the implementation commitment. The information needed to
prevent that mistake was already in the projection.

I recommend addressing that tool interaction first, alongside completing the
already adopted landing-obligation work. Then trial shorter, later-filed
assignments and a single verified handoff between workrooms. These changes
should reduce coordination work without weakening independent review,
authority, exact-head approval or sealed delivery.

This is advice requested by Hugh during the monitoring session. The adopted
landing decision motivates the analysis; it does not adopt these additional
recommendations. No source behavior or governing instruction changes here.

## 1. Resolve the implementation commitment before signing a review

These three correction requests explicitly identify a binding problem with
an unchanged implementation:

| Candidate | First approval | Correction request | Replacement approval |
|---|---:|---:|---:|
| Merge-plan, `b424b752e0d822d32c316306f0ac03fe32a5cdaf` | #16813 | #16841 | #16844 |
| Cross-process merge lock, `99ac69501571a52490c832ae366c3cf393881110` | #17173 | #17182 | #17184 |
| Configuration CAS, `1e868103416fea2ca832e2e740b4197940cf5057` | #17351 | #17356 | #17358 |

All numbers in this table are Gitseq workroom positions. Each replacement
approval was followed by a fresh authorization request: #16846, #17186 and
#17360. In the second case, #17182 says the original artifact belonged to zero
implementation commitment lanes and merge refused before changing Git.
The refusal was correct. Discovering the mismatch after review was avoidable.

At source head `be3b069cc8e013e9ebff6b9e0a70ef5e10c2cf5b`,
[`gs review`](../docs/reference/gs/review.md) documents that the first artifact
names the verdict. `internal/reviewguard/confirm.go` validates the examined
artifact set and rereads it before signing. Structured merge authorization in
`cmd/gs/main.go` additionally resolves the approval's named artifact to the
implementation commitment. These checks should be presented together before
the reviewer commits to a verdict.

**Proposed interaction:** select the implementation request or commitment;
resolve its reporting artifact and the exact candidate artifact set; show the
target, head, governing authority and intended commitment closures; refuse
missing or ambiguous bindings before signing. Keep an explicit artifact-set
confirmation so the tool cannot silently approve an unexamined companion.
Support the existing self-initiated path without inventing a commitment.

Prefer a guarded preparation helper over another lifecycle state. Preserve
reviewer independence, fresh head-news acknowledgments and merge-time checks.
Do not rewrite an old verdict or infer authority from Git ancestry.

**Acceptance evidence:** replay the three examples through the preparation
path; a wrong primary artifact must refuse before a verdict is appended.
Cover multiple implementation commitments in one candidate, ambiguity,
self-initiated work and a candidate change during preparation.

## 2. File assignments when their prerequisite can support them

Gitseq requests #17129, #17131 and #17132 are successive filings of the same
fold-study repair. The latter two explicitly preserve scope, actor availability
and target while repairing stale bases. This is real coordination work, even
when the conditions have not changed.

There is a second example in my own work today. I filed Inventory I2, I3, I4
and I8 together before I2 landed. Correcting the later corpus command required
refiling I4 and then I8. I also introduced a separate generation error:
JavaScript replacement-string expansion interpreted the command's literal
`$'` and duplicated surrounding text. Codex caught it; I refiled both requests
again. That was my error, not a necessary consequence of the workflow.

Keep future stages visible in the adopted plan, with their prerequisite and
intended owner. Create the executable assignment when the prerequisite has
landed and the actor can act. A genuinely assigned parallel task still needs
its durable request immediately. Do not hide an existing commitment in prose
or delay an independent task merely to make the board shorter.

Before appending a generated request, inspect the exact rendered body and
current bases. Use literal-safe construction; validate command text before it
becomes permanent. Put the specific outcome and remaining gates in the request;
cite the shared versioned contract instead of copying its entire procedure
into every stage. A request must still be understandable through its cited
record, including after ephemeral chat disappears.

This is a filing and communication change. It does not permit editing a signed
request, ignoring a changed condition or carrying obsolete authority forward.

## 3. Show delivered work as delivered

In Tailapp, four approved candidates were already ancestors of current main
`be621e8ae3eb12daba6a59e7e7aa147106a36ede`, while their delivery cards remained
open. I independently checked all four ancestry relations and remote agreement.
Retirements #3362–#3365 cleared the cards, but the present fold labels their
commitments `cancelled`. Report #3366 records that this was administrative
closure of incorporated work, not abandonment of the implementation.

This is a concrete reporting ambiguity: a reader cannot interpret `cancelled`
as failed delivery. Historical status totals therefore cannot reliably measure
delivery success.

Complete the existing target-aware landing and reconciliation designs before
adding another closure mechanism. Preserve the original events and distinguish
an approved candidate, proven target containment and accepted delivery. Ancestry
is evidence for reconciliation; it must not silently manufacture a receipt or
approval. Historical reconciliation needs its own bounded authority and an
auditable result.

For the ordinary board, make three facts easy to find together: the requested
outcome, its target evidence, and the actor who owes the next action. Presence
and reasoning staleness remain separate facts. They should not obscure whether
delivery is actually owed.

## 4. Bridge verified results once, under the existing authority

Tailapp publication report #3332 already recorded the successful immutable
`jsonataddl/v0.1.1` release. Gitseq request #17393 still routed its prerequisite
to Hugh. After his clarification, I verified the release independently and
filed Gitseq evidence #17516, then replaced the human coordination gates with
agent assignments. This did not require another product decision.

Similarly, Gitseq proposal #17464 asked for landing-design adoption that already
existed at #17029. I checked the adopted candidate against the landed note and
retired the duplicate proposal at #17517.

Before sending an action to Hugh, the coordinator should inspect the existing
decision and delivery chain, identify the actual unresolved gate and check who
has authority to discharge it. For an external prerequisite, one coordinator
should verify the source report, exact version/head and relevant checks, then
record the local evidence once. Later assignments cite that bridge.

Foreign records remain evidence. A bridge does not confer local ratification,
grant a role or authorize signing as another actor. Human attention remains
necessary for a new decision or authority that the agent does not hold.

## Scope and limits of the history review

The full projection snapshots used for this analysis end at:

| Workroom | Depth | Exact head |
|---|---:|---|
| Gitseq | 17,527 | `50ddd3e32314f6493a0d51a1c3d766f3637a5b97` |
| Tailapp | 3,366 | `968d70006e8fe22bf168b2d2db7ecc58f451412f` |

Gitseq's snapshot contains 2,326 request statements and 824 review records;
Tailapp's contains 249 and 116. Combining each request's text and conditions,
the median whitespace-delimited word count is 190.5 in Gitseq and 93 in
Tailapp. Of named review heads, 72 in Gitseq and two in Tailapp have more than
one verdict. These counts identify material to examine, not wasted work:
different scopes, companion artifacts and legitimate substantive re-review
can all produce repeated verdicts.

The snapshots span different periods and workloads. They include historical
schemas and ineffective statements. They do not measure effort, token cost,
test cost or unattended time. I make no productivity ranking or percentage
speedup claim. The recommendations rest on the specific inspected chains
above, not on treating every administrative event as overhead.

The canonical records for the three binding corrections are:

- `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:260a1a6cffa1135c9e0915f07ec6513afa565fc0`
- `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b0a24c7b1b4603c585e9dd726cb8009fe191ee0a`
- `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d681dbc435323a0d359f360d96b435a5841f9095`

The Tailapp closure report, with the four candidate hashes and retirement
evidence, is
`git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:968d70006e8fe22bf168b2d2db7ecc58f451412f`.
The external release evidence is
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ecc7e4fe9425a31808079ffe830e2cdb58270967`.

## Suggested decision and trial

Approve preparation of a bounded design for review/delivery binding validation,
and trial the request-filing and communication practices above. Continue the
already adopted landing work. Do not add a new lifecycle vocabulary through
this note.

For the next 20 delivered candidates, record binding-only repeat reviews
(target zero), unchanged-condition request refiles, coordinator handoffs after
approval, and candidates proven contained but still awaiting delivery closure.
Record elapsed intervals separately from active effort. Compare substantive
review findings and delivery defects as well, so fewer messages cannot be
mistaken for better work. Review the results before expanding the change.
