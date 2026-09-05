---
date: 2026-09-05
status: candidate design; implements nothing
origin: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ebd6aec062edb96c3984e4659ab9d09929c0271a
basis: Hugh-adopted workroom delivery-efficiency recommendation, delivered at 3f4c4969ad3afea608be521cf9b6e2422223c04e
---

# Check the implementation binding before review is signed

A reviewer can approve the right code and still name the wrong reporting
artifact. Merge authorization then correctly refuses, requiring another review
of unchanged code. Move the existing exact commitment lookup into the shared
review guard, and expose its explanation through the existing review command
and tool. Keep the reviewer's explicit artifact set and all merge-time checks.

## Evidence and existing behavior

At source head `3f4c4969`, `reviewguard.Build` names `artifacts[0]` in the
verdict. `Confirm` validates every cited artifact at three verified reads,
requires those reads to agree, and checks head-news acknowledgments. It does
not establish which implementation commitment that first artifact reports.
`cmd/gs/main.go:1154-1173` later requires exactly one projected commitment
whose `Report` equals the approval's named artifact during authorization.

I reproduced the three cases using the verified projection at depth 17671,
matching approval bodies to artifacts and exact `Commitment.Report` values:

| Exact candidate | Original primary: zero matches | Corrected primary: one match | Correction request |
|---|---|---|---:|
| `b424b752e0d822d32c316306f0ac03fe32a5cdaf` | `7e59a351`, `cmd/gs/main.go` | `30bf04c1`, `cmd/gs` | #16841 |
| `99ac69501571a52490c832ae366c3cf393881110` | `afcae0ab`, `cmd/gs/main.go` | `1e85d426`, `docs/reference/gs/merge.md` | #17182 |
| `1e868103416fea2ca832e2e740b4197940cf5057` | `6e6e9ad5`, `internal/apphost` | `f87eea0e`, `cmd/gitseq-mcp/selector_test.go` | #17356 |

These are current projection lookups of historical records, not reconstructed
historical snapshots. The correction requests independently state the same
binding failures and unchanged heads. The corrected reports resolve respectively
to requests `fd6d8c2f`, `b14722c1` and `72112af7`. The proposed guard would refuse
the original primary before signing, while retaining it as an examined companion.

## Smallest additional change

Add one pure binding resolver to `internal/reviewguard`, taking a verified
projection, exact candidate, explicitly examined artifact IDs and optional
implementation selectors. Move the existing exact-report lookup there so review
and authorization share it. This avoids a dependency cycle: merge planning
already imports reviewguard; reviewguard need not import merge planning or open
Git. This is a layer-7 guard over existing projected facts, not a fold rule.

An implementation selector names a request, or its exact promise/direct-report
identity when several lifecycles would otherwise match. It is separate from
`--promise`, which continues to name the reviewer's own commitment. Permit
repeated `--implementation` selectors for a combined candidate. Existing
unambiguous assigned reviews need no new selector: resolve their named primary
by exact report equality, then discover other implementation reports inside the
explicitly examined set. Never discover approval scope from every artifact at
the same Git head.

For each selected implementation, require one matching projected commitment,
a live effective reporting artifact at the exact head, and inclusion of that
artifact in the examined set. Refuse zero or multiple matches, a selector whose
report is absent, or a supplied primary that differs from the first selected
implementation's report. Report the expected event and path; do not silently
reorder, substitute, or add artifacts. Multiple lifecycles of one request must
be disambiguated, not resolved by recency in this helper.

Self-initiated work takes an explicit adopted-decision basis instead of an
implementation selector. Zero matches alone never chooses that path. Require
no implementation commitment claimed for that work, and show the exact adopted
proposal or satisfied authority-bearing request chain from `SKILL.md`. Preserve
the reviewer's duty to confirm its four authority facts. Assigned work cannot
escape a broken reporting link by choosing self-initiated mode. The review
request and promise still exist; no self-request or self-promise is introduced.

Add an optional read-only `--prepare` form of `gs review`, mirrored by the
review tool. It accepts the same scope inputs and returns the resolver's
explanation without a verdict, mutation, reservation or new lifecycle. Ordinary
verdict filing always runs the resolver, whether preparation was used or not.
The supplied artifact list is the reviewer's explicit confirmation; preparation
never signs companions merely because it found them.

## What the reviewer sees

For the cross-process-lock example, preparation reports:

```text
Candidate: 99ac69501571a52490c832ae366c3cf393881110
Implementation: b14722c1 / its exact commitment
Primary supplied: afcae0ab (cmd/gs/main.go)
Primary required: 1e85d426 (docs/reference/gs/merge.md)
Refused: supplied primary reports no implementation commitment.
No verdict recorded. Keep afcae0ab as a companion if examined.
```

After the reviewer explicitly corrects the inputs, show candidate, primary,
all examined event/path pairs, implementation request/promise/report triples,
target repository/ref and measured head, governing decision/request witnesses,
and intended implementation closures. A closure is conditional on a valid
sealed merge receipt; review itself closes only the review commitment.
Self-initiated output says there is no implementation commitment to close.

For multiple implementations, list every represented reporting artifact and
its own target and authority. Refuse ambiguous selection or incompatible
intended targets before a delivery review is signed. Supporting artifacts
without their own commitment remain legitimate companions. Do not imply that
unexamined work at the same head will be accepted.

The preview is advisory, not a reusable authorization ticket. Final filing
re-resolves the same explicit inputs inside all three `Confirm` reads and
compares the resulting bindings as well as the existing basis/news. A changed
candidate, reporting link, selected target/authority, or frontier during that
confirmation refuses before append. A report changed since preparation cannot
be silently substituted because the reviewer supplied exact artifact IDs.
Fresh unrelated news follows existing acknowledgment rules.

## Boundary with the adopted landing work

The [landing design](2026-09-04-landing-obligation.md) already owns result choice,
target inheritance, holds and releases (I1); target-bound receipts (I2);
authorization and hold-owner checks (I3); projected delivery facts (I4); UI
filing and display (I5); and their documentation (I6). This design duplicates
none of them and adds no result class, release authority or lifecycle word.
At this note's source head they are adopted interfaces, not delivered behavior.

Use those shared resolvers once delivered. Do not implement another ancestry
walk in reviewguard: target and hold authority must come from the same resolved
branch, including refusal of mixed-target ambiguity. Legacy records retain the
existing documented reading; prose is shown as evidence, never parsed into a
new machine-granted permission. A missing actionable target or unresolved
authority is reported explicitly rather than guessed from the checkout or
Git ancestry.

Preparation cannot call today's `mergeplan.Build` with a fabricated approval:
that function requires a real ratified verdict. It explains binding and intended
closures, not every prospective succession or merge refusal. Actual merge
planning, exact path succession, authorization remeasurement and receipt
admission remain authoritative after review. Neither preview nor approval
reserves a target head or replaces those checks.

## Acceptance and delivery

Focused tests must establish:

- The three recorded primary/report pairs above fail or pass the pure resolver
  as expected. Wrong-primary refusal leaves the workroom frontier unchanged.
- Two implementations in one candidate retain both reports and all explicitly
  examined companions; missing, duplicate or ambiguous bindings refuse.
- Explicit self-initiated work preserves its adopted authority and independent
  review without inventing an implementation commitment; zero-match assigned
  work cannot use that route.
- Changes between each confirmation read to head, report, target, hold-owner,
  artifact liveness or frontier refuse before append. A newly published
  companion is news, not automatically examined scope.
- CLI and MCP produce the same explanation and verdict inputs; reviewer
  independence, head-news checks and merge-time refusals remain effective.

First extract the exact lookup and add historical-pair resolver tests. Then
integrate it into shared confirmation and the two review surfaces, including
the optional preparation output. Bind target/authority explanation to delivered
landing resolvers before enabling those fields. Update layer 7's architecture
contract, review references and examples in the implementing head; publish its
candidate artifacts and obtain independent Architecture, Security and
Simplification review.

The decision requested is approval of this bounded integration. Exact flag/tool
field spelling remains an implementation choice; making preparation mandatory
or broadening it into a full pre-approval merge simulator is not recommended.
This note authorizes no production implementation by itself. Continue the
already adopted twenty-delivery trial to measure binding-only repeat reviews
and defects after a separately assigned implementation lands.
