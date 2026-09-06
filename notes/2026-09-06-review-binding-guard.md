---
date: 2026-09-06
status: implemented; awaiting independent review
origin: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:76390a8672d6b8781391883224e8d96fc5ed0e9a
design: notes/2026-09-05-review-binding-preparation.md
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:76390a8672d6b8781391883224e8d96fc5ed0e9a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:38aae2d094ba5f3f46bfe7c5e0e721d375225483
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:45507fd7dbfada33516a004bc11d53b6c13f4a51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bd891443ff868623f2ad427b4a5becd32359e5d3
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
---

# Review binding guard: implementation account

This delivers the bounded decision adopted in #18073 and designed in
[the binding design](2026-09-05-review-binding-preparation.md): one pure
implementation classification in `internal/reviewguard`, consumed by ordinary
review, merge authorization and every merge path, with an optional read-only
preparation form. It adds no fold result class, lifecycle word, release
authority or ancestry walk.

## What changed

**One resolver.** `reviewguard.Resolve(projection, Scope)` takes the exact
candidate, the explicitly examined artifact events with the primary first, and
at most one of three selectors: implementation selectors, an adopted decision,
or evidence-only. It returns a `Binding` whose kind is one of:

- `assigned`: a projected commitment's `Report` equals the primary. Other
  implementation reports are discovered only inside the examined set. With
  selectors, each names exactly one commitment lifecycle by request, promise or
  report; its report must be live at the head and inside the examined set; the
  first selected report must be the supplied primary. A selector disambiguates
  the lifecycle of the report it names and never narrows the delivery: every
  other examined artifact that reports a commitment joins the resolved set with
  its target and hold, exactly as it would with no selector. Every
  implementation must owe its landing to one target.
- `self-initiated`: no commitment reports any examined artifact, the primary
  and the named decision rest directly on each other in either direction (the
  work rests on the decision, or the decision adopts this artifact, as a
  decision record's proposal does), the primary's own one-hop edge leads to no
  request, and the decision is a ratified proposal or a satisfied request.
- `evidence-only`: the primary's one-hop edge leads to a request addressed to
  its author that states `no_git_artifact=true`, and nothing examined reports
  a commitment.

A primary no commitment reports is refused with the missing witness: the
request it was filed for and the artifact that actually reports that request,
with its path; or that it is evidence; or that its reporting link is broken.
An assigned edge cannot be relabelled by a selector. The resolver reads one
hop of the primary's provenance and the projected commitment, target and hold
facts, and nothing else.

**Review.** `ConfirmSelection` resolves the binding at each of the three
confirming reads and refuses movement between them alongside the existing
basis and news comparison. `BuildBound` records `binding`, `implementations`
(a JSON array of request events) and `decision` in the verdict body.
`EvaluateVerdict` re-resolves a verdict that carries a binding at sequencing
and refuses a moved one. `Prepare` is one read with the same checks and an
explanation, no verdict.

**Surfaces.** `gs review` gains `--implementation` (repeatable),
`--self-initiated`, `--evidence-only` and `--prepare`; the MCP review tool
gains `implementations`, `self_initiated`, `evidence_only` and `prepare`, and
returns `recorded: false` with the explanation for preparation. Both call the
same functions and produce the same words.

**Merge.** `validateLanding` replaces its exact-report lane lookup with
`approvalBinding`, which rebuilds the scope from the approval's own citations
and selectors and resolves it. An evidence-only approval refuses before any
reservation, Git mutation or receipt append, with or without
`--authorization`; a self-initiated approval lands with no commitment to
close; an assigned approval keeps every existing destination, hold and release
rule, and a combined candidate may carry at most one held implementation.
`approvalImplementationCommitment`, on the authorization path, requires an
assigned binding. `mergeplan.Build` records the binding as a plan reason and
refuses evidence-only so `merge_plan` explains what the merge would refuse.
The sealed-receipt resume path is unchanged: it revalidates the sealed facts
and never re-runs this guard on an already completed merge.

**Legacy verdicts.** A verdict filed before bindings were recorded carries no
selectors; `ScopeFromVerdict` rebuilds its examined set from the citations
standing at the head and the resolver judges its actual primary. Zero report
matches refuse; nothing is grandfathered from an empty lookup.

## Examined scope

Changed paths at the exact head, each with its candidate artifact:
`internal/reviewguard/binding.go` (new resolver, scope, body fields,
explanation), `internal/reviewguard/confirm.go` (selection, three-read
binding agreement, preparation), `internal/reviewguard/reviewguard.go`
(`BuildBound`, admission re-resolution), `cmd/gs/main.go` (flags, preparation,
landing and authorization consumers), `cmd/gitseq-mcp/main.go` (tool schema and
handler), `internal/mergeplan/mergeplan.go` (plan reason), the tests named
below, `docs/reference/architecture.md` (layer 7 contract),
`docs/reference/gs/review.md`, `docs/reference/mcp/review.md`,
`docs/reference/gs/merge.md`, and this note.

## Tests

`internal/reviewguard/binding_test.go` pins the resolver: exact-report
binding; the historical wrong-primary shape in both directions, refusing with
the required report and path and admitting the corrected order; no
self-initiation from an empty lookup; evidence-only needing its no-artifact
edge and refusing relabels in both directions; self-initiated needing a
ratified decision the primary rests on, and refusing an assigned edge;
combined implementations with every companion, selector order, uncited and
duplicate selectors, unknown selectors and incompatible targets; a report
signed by another actor; ambiguous lifecycles; mode conflicts; a candidate
mismatch; `ScopeFromVerdict` round-tripping a recorded binding and
reclassifying a legacy verdict; and selectors that disambiguate one report's
lifecycle while every examined companion keeps its request, hold and target.

`internal/reviewguard/confirm_test.go` adds a binding that moves at the third
read and the recorded body fields with admission re-resolution.

`cmd/gs/binding_test.go` replays the I2 control through the real commands:
an evidence artifact refuses as an assigned delivery and as self-initiated,
signs as evidence, and its ratified approval refuses `gs merge` into a wrong
destination with and without `--authorization` before Git or the frontier
moves, and refuses in `merge_plan`; a hand-shaped legacy zero-match approval
refuses the same way; a self-initiated review needs its ratified proposal and
then lands; two implementations at one candidate keep both reports and close
both on the sealed receipt, refusing a reordered primary and an uncited
report; preparation names the required report for a wrong primary and appends
nothing; the authorization consumer refuses a non-assigned approval on its
own; and, from review finding
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:12182bd2e41e0c644e586933ccedd33fcb9008b7`,
selecting only the first of two examined implementations still records both
requests and still refuses the held companion at `gs merge` and the
differently targeted companion at `gs review`, appending nothing and moving
no ref.

## Correction round

Planner's finding 12182bd2 on the first candidate `4258167d` reproduced with
real commands that `--implementation` selecting one examined report let a
held or differently targeted companion escape the combined guards, because
`resolveAssigned` included only the selected commitments. The correction is
in the resolver alone: after the selectors are resolved, every other examined
artifact's reporting commitments join the set exactly as in the no-selector
branch, so confirmation, admission, preparation, authorization, merge and the
receipt all see one complete set. No consumer sweeps, no ancestry. The head
was also rebased onto landed main `33f69956` (the I6 documentation
reconciliation), which touched none of these paths.

`cmd/gitseq-mcp/review_test.go` proves the tool records the same body fields,
refuses the same relabel, and prepares without appending.

## Omission controls

Run on the committed tree by `rbg-mut.sh`, each removing one consumer or
guard and running only the control that must catch it:

| Mutant | Removed | Control | Result |
|---|---|---|---|
| A | review filing skips the resolver | evidence artifact signs as an assigned delivery | red |
| B | merge landing keeps the old zero-match pass-through | evidence-only and legacy zero-match approvals land | red |
| C | authorization consumer trusts the primary's exact report lookup | authorization of an evidence-only approval passes | red |
| D | merge plan skips the binding check | `merge_plan` admits an evidence-only approval | red |
| E | admission skips binding re-resolution | a verdict whose binding moved after confirmation seals | red |
| F | self-initiated inferred from an empty lookup | wrong primary and broken assignment pass | red |
| G | selectors may add an unexamined report | an uncited implementation report is closed | red |
| H | selectors drop examined companions | a selector hides a held or differently targeted companion at review and merge | red |

Each mutant was applied to a clean committed tree, the named control run
alone, and the tree restored with `git checkout` before the next.

## Gates

Run on the committed head with `rbg-gates.sh` and its follow-up:

- `gofmt -l` clean; `git diff --check` clean; `go vet ./...` and
  `go build ./...` exit 0.
- `go test -race -count=1` on reviewguard, mergeplan, app, cmd/gs and
  cmd/gitseq-mcp: all pass.
- `go test -count=1 ./...` over 35 packages: all pass. On the first
  candidate one timing test in cmd/gitseq-mcp
  (`TestWhoamiBoundsStallsAndRejectsRedirects`) timed out while two other
  suites shared the machine; on the corrected head the full suite ran alone
  and passed without it.
- `GOFLAGS=-count=1 make docs`: pass, after the decision-record how-to's
  three reviews were given their adopted-decision witness (that change is in
  this head).

The correction round reran mutants A through H and every gate above on the
committed corrected tree `7435a0d7`, rebased onto main `33f69956` and differing from the reviewed head only by this note: all eight mutants red
(H against both the command controls and the resolver unit test under one
mutation), every gate green.

Existing tests that reviewed artifacts with no request or promise edge were
given the ratified adoption they lacked: the nested cross-author fixtures in
cmd/gs and the merge-plan fixture in cmd/gitseq-mcp. They now name their
proposal with the self-initiated selector, which is the behaviour this
delivery introduces.

## Limits

The resolver trusts the fold's `Commitment.Report`, `Performer`, target and
hold facts; it does not re-derive them. Self-initiated authority is checked
for existence and force (a ratified proposal or a satisfied request), not for
the four authority facts in `SKILL.md`, which stay the reviewer's duty. A
combined candidate may include at most one held implementation. The
`merge_plan` tool explains the binding but, as before, needs a real ratified
approval; preparation is on the review surfaces. Existing approvals in every
workroom that rest on zero report matches will now refuse to merge until
reviewed again with an explicit witness; that is the adopted behaviour, and
the affected rows are the ones the design listed.
