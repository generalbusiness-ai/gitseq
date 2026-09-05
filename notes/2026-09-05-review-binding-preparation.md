---
date: 2026-09-05
status: candidate design; implements nothing
origin: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ebd6aec062edb96c3984e4659ab9d09929c0271a
basis: Hugh-adopted workroom delivery-efficiency recommendation, delivered at 3f4c4969ad3afea608be521cf9b6e2422223c04e
reconciliation: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0396db73006ee2eb8530e71fc92c6537f3b48316
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:203ed4c78aaa67ab5a05253b949f62dfc4c084fd
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:38aae2d094ba5f3f46bfe7c5e0e721d375225483
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b23b31c069d53b3d1b3fc3fe8c79306e98543c06
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:e9a4296399c2ece5d10f014163dff6baba7dca77
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:22cb07e91e19956f4ad81ba0b2ba1f09e74ee1ad
---

# Check the implementation binding before review is signed

A reviewer can approve the right code and still name the wrong reporting
artifact. Authorization then refuses, requiring another review of unchanged
code; merge without authorization can miss the assignment altogether. Put one
implementation classification contract in the shared review guard and consume
it in ordinary review, authorization and every merge. Expose its explanation
through the existing review command/tool. Keep explicit review scope and all
destination, authority and succession checks.

## Evidence and existing behavior

At current main `848641831`, `reviewguard.Build` names `artifacts[0]` in the
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

I also independently reproduced the I2 escape at exact source
`f21c135c8ea0c20c250e6eb3bc24169616fc1b1b`, in a separate detached checkout.
A real `state@3` request with `no_git_artifact=true` was promised by its
addressee, who published a candidate artifact against that promise. The fold
correctly left `Commitment.Report` empty: evidence does not discharge this
request. Nevertheless, real guarded review and ratification succeeded, and
`mergeCommand` without `--authorization` merged into `wrong-destination`,
advancing both Git and the sequence. `validateLanding` returns success on zero
report matches, calling them self-initiated work. Zero matches establish no such
fact. An otherwise equivalent assigned-artifact request targeting main refused
the wrong destination before either Git or the sequence changed.

The attached `TestBindingNoArtifactMergeControl` and output reproduce both
paths in 23.674 seconds. This is observed behavior of an I2 candidate, not a
claim that I2 or a fix is on main. Current-main source bases are refreshed
above; I1's target and hold resolution is delivered. The historical three-case
evidence remains historical, and the old candidate artifact is not a current
behavioral basis.

## Smallest additional change

Add one pure classification/binding resolver to `internal/reviewguard`, taking a verified
projection, exact candidate, explicitly examined artifact IDs and optional
implementation selectors. Move the existing exact-report lookup there so review
and authorization and merge share it. This avoids a dependency cycle: merge planning
already imports reviewguard; reviewguard need not import merge planning or open
Git. This is a layer-7 guard over existing projected facts, not a fold rule.

Its output distinguishes assigned delivery bindings, explicitly adopted
self-initiated delivery, evidence-only review, and refused/ambiguous inputs.
These are guard results, not new fold lifecycle or result classes. It returns
the exact witnesses it used so every consumer checks the same facts:

- Exact `Commitment.Report` equality selects assigned work. Also inspect the
  artifact's direct author-owned request/promise edge to catch a broken or
  absent reporting link: the promise performer, or direct request addressee,
  must match the artifact author. Use the existing projected commitment and
  request-result facts; do not repeat I1's target/hold ancestry traversal.
- An artifact directly filed by that performer on a `no_git_artifact` request
  is evidence. Publishing it and reviewing its contents remain legitimate.
  It cannot serve as a delivery primary or manufacture a landing obligation.
  A delivery attempt needs an actual implementation assignment or separately
  justified self-initiated artifact; an approval cannot change the request's
  result. Apply this rule even when no authorization argument was supplied.
- Companions can be examined evidence without claiming their requests as
  implementations to close. Every claimed implementation still needs its exact
  reporting artifact. Finding an unrelated old request through artifact
  provenance does not make work assigned: no arbitrary transitive ancestry
  search, prose parsing or same-head sweep is used for classification.
- Self-initiated delivery needs a positive explicit adopted-decision witness
  and an artifact filed for that work on that basis. A direct current assignment
  edge cannot be overridden by adding that witness or selecting another mode.
  Insufficient or conflicting provenance refuses with the missing witness;
  absence of a report is never evidence of independence. The reviewer remains
  responsible for checking that the declared scope describes the actual work.

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
never signs companions merely because it found them. An explicitly selected
evidence-only review can sign a verdict on evidence, but its classification is
not mergeable and confers no implementation closure. The signed verdict carries
the explicit scope selectors and classification witnesses, not a new authority.
Authorization and merge re-resolve them rather than trusting a mode flag.

This enforcement must be one delivery across review, authorization and merge:
ordinary review cannot omit it by skipping `--prepare`; merge cannot omit it
by dropping `--authorization` or by presenting an old zero-match approval.
Legacy approvals are reclassified from their actual primary and durable bases;
an unsupported self-initiation claim needs an explicit witnessed review, not
grandfathering from an empty lookup. A classification failure occurs before
reservation, Git mutation or receipt append. Existing receipt retry/recovery
still verifies already sealed facts and must not turn this guard into a way to
strand an already completed merge.

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
none of them and adds no fold result class, release authority or lifecycle word.
I1 is delivered at `83904fb2`; the relevant current-main files are unchanged.
I2 is the separately tested candidate above; the remaining stages are not
described here as delivered.

Consume I1's projected target/hold resolution now. Do not implement another ancestry
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
- Replay the I2 control through ordinary review, authorization and merge
  without authorization. Evidence-only review remains valid, but delivery
  refuses before Git/frontier mutation. Removing classification from each
  consumer must independently reproduce its bypass, using a historical
  approval fixture where needed so an earlier guard cannot mask a missing one.
- Preserve a separately adopted self-initiated primary with a historical
  request only in descriptive provenance, legitimate evidence companions and
  combined implementation reports. A direct no-artifact ownership edge or
  broken assigned report must not become self-initiated by changing a selector.
- Changes between each confirmation read to head, report, target, hold-owner,
  artifact liveness or frontier refuse before append. A newly published
  companion is news, not automatically examined scope.
- CLI and MCP produce the same explanation and verdict inputs; reviewer
  independence, head-news checks and merge-time refusals remain effective.

First extract the exact lookup and add historical-pair and I2 control tests.
Then integrate the same classification into shared confirmation, authorization
and all merge paths, including both review surfaces and optional preparation.
Use I1's existing target/authority facts. Update layer 7's architecture
contract, review references and examples in the implementing head; publish its
candidate artifacts and obtain independent Architecture, Security and
Simplification review.

The decision requested is approval of this bounded integration. Exact flag/tool
field spelling remains an implementation choice; making preparation mandatory
or broadening it into a full pre-approval merge simulator is not recommended.
This note authorizes no production implementation by itself. Continue the
already adopted twenty-delivery trial to measure binding-only repeat reviews
and defects after a separately assigned implementation lands.
