---
title: Keep stale work visible without changing its authority
summary: Reconcile the historical P1–P5 plan and bound the remaining lifecycle and attention correction.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:608be185aaba9343eba9175c04bf10a20a04b015
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b664e8dd2592ed1afded59585fab0ba7378a682e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f85c910b62d17846463092a668e7af6d19b20fb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:982bfe9e7df98bde8c6f8797112498fb300baf4a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:efb1cee98973256e776e07c3de278c95f781685c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:84777074e8f726a673e6afbab13a8189fb1b4726
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f5ca1c3b34c09a4a1a5f26ac366b94c748e3ca9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0f2c5ac05d9e834d7e824680eafa805e43a1c04d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9032b1529724a17ee2a200a01363a61ab8d76325
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b3a68c6256221f6cba88ff1ee5d392e305a5fe33
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1c15fb28f5df85b922f551195b7b736f2488138
---

# Keep stale work visible without changing its authority

This decision recovers the remaining attention defect from historical proposal
#14703. It proposes P2 as one bounded implementation. Receipt propagation and
permission to begin work remain separate questions. No production change is
made by this note; ordinary adoption, independent review and sealed delivery
must precede a separately assigned implementation.

## What remains from P1–P5

| Part | Current evidence and disposition |
|---|---|
| P1: stop causes arising after a receipt from staling its published successors | Conflicts with the implemented dated checkpoint. Ruling #11588, ratified by `25eab16d`, settles causes active at or before the receipt and propagates later causes. Its implementation was recut under #12104. These precede #14703; that later adoption proposed the opposite change, which remains undelivered. Keep the current rule while the conflict is explicitly decided separately. P2 changes no receipt edge or cause. |
| P2: keep lifecycle beside staleness | Remaining. An unreported live promise can still become status `stale` and disappear from the performer's actionable lane. Preserve `open` or `promised` with the existing stale flag and cause evidence. The other four live lifecycle states already survive ordinary staleness. |
| P3: admit stale bases and remove mandatory refiling | Admission is delivered by `3f75695c0fcd67d32cb2150bb9c416c22ae85ccd`, request #16262, receipt #16339. The boundary owns `stale_bases`; merely stale bases are admissible, while retired bases retain their explicit refusal/override rules. Preserve the earlier owned-stamp repair #15325: ordinary `stale`/`staleness` fields are admission-owned, with only the confined exception for a field owned by a ratified kind schema. The general P3 text change—replace author refiling of ordinary stale assigned requests with answering them like fresh requests—remains undelivered and is deferred by this decision. Keep current intake and SKILL discipline 11 until separately decided. This is distinct from the later authority-chain start checks, request #16406 and delivery `c8e84040de7dc3356ee83725b80a864a55bbe97b`, which remain in force below. |
| P4: drain existing orphans once | The eleven dispositions recorded in #14687 already occurred. Its design question is satisfied by report #16386 (`b2e0bbf2`), ratified by `e57a1fd2`. Later adopted landing design #17029 and delivered I1–I5 supply target-aware delivery and explicit unresolved-landing attention. Do not repeat the drain or revive ancestor-head closure or retirement at unchanged paths. |
| P5: keep corrections on their original commitment | Delivered in `ce18ec8742ddd8665afa284a9cde1ce946213435`, under #18175 and Hugh's adopted proposal #17682. Existing outcome, conditions, performer, destination and authority must remain the same; publish a corrected artifact citing the finding and obtain fresh review. A separate outcome or changed authority still needs an assignment. Planner's five-round observation is separate; this note claims no completed trial or effort saving. |

The original proposal remains ratified but ordinarily stale; its candidate
`ec80e0448f8aa794e011430663421d08f86587ab` describes a superseded world.
Historical adoption is evidence for this reconciliation, not fresh authority
for that old implementation plan. The artifact's evidence includes exact
requests, ratifications, delivered receipts and the original review.

Source pointers below open at the note artifact’s immutable head. Exact earlier
source revisions and their byte-equivalence checks accompany its evidence.

## Reproduced attention defect

At verified frozen frontier #19682 (`24b6353f5b41ef95a7d8737bc7e138fbaeda2520`),
request #19651 has live promise #19653, no report, status `stale`, `stale=true`
and `waiting_on=claude`. Running the current work-query code over that verified
snapshot returns the row in `not_actionable` and omits it from `waiting_on_you`.
The fold's no-report branch (`internal/workroom/fold.go:3527`) replaces the
lifecycle; the query's classification (`internal/statusview/query.go:220`)
then excludes it. Both are necessary parts of the failure.

This is a frozen historical case: the live binding assignment has since
advanced. A separate actual current-reader query confirms that an unclaimed
stale request remains in `available_to_you` with no invented promise, performer
or waiting party. The code baseline is main
`33f69956167115d7cc3235b6b1864ed904f2802a`; the four readers run reviewed
`39c7a0e74f4a828ba8223acbacc3f2c82eb58016`. Relevant behavior files are
byte-identical. The read-only snapshot was verified from the signed log without actor
or sequencer keys and without rewinding its checkpoint.

## Bounded P2 implementation

Compute lifecycle from commitments, completions and landing obligations only.
Compute ordinary staleness alongside it, without erasing causes or changing any
act's effectiveness. A stale unclaimed request remains `open`; a stale live
promise without a completion remains `promised`. Preserve `reported`,
`awaiting-review`, `awaiting-authorization` and `awaiting-landing`, including
their existing next actor. Preserve every closed lifecycle and the three
terminal values. Staleness neither cancels a promise nor grants execution or
landing authority.

One implementation head must reconcile these consumers together:

| Surface | Required change and preserved contract |
|---|---|
| Fold and model (`internal/workroom/fold.go:120`) | Remove the two stale-status overrides; retain stale qualifiers, causes, promise and waiting-party fields. Completion, retirement, transfer and landing precedence remain unchanged. |
| Profile and schemas (`internal/workroom/schema.go:13`) | Advance the next unused fold profile from the then-current version (now `workroom-fold@22`). No new signed-event schema is required: record bytes and act-time authority are unchanged. Update projection descriptions and golden expectations. |
| Bounded status (`internal/statusview/view.go:124`), actor status (`internal/statusview/actor.go:254`) and work query (`internal/statusview/query.go:270`) | Route stale open/promised rows through their normal relationship lanes. Counts retain lifecycle totals and stale totals per lifecycle. Keep closed-history summaries, omission counts, limits, cursors and the independent approved-not-landed audit. |
| CLI (`cmd/gs/main.go:2387`), MCP (`cmd/gitseq-mcp/main.go:700`) and HTTP (`internal/service/server.go:285`) | Use the same projection and filter contract; update input enums/help and output descriptions together. No adapter may silently return an older classification as current. |
| Browser types (`ui/src/lib/api.ts:65`) and row populations (`ui/src/lib/rows.ts:48`) | Include stale open/promised rows in open work and its existing reasoning-moved subset. Remove the duplicate stale-not-in-flight population, its counter and dead selection branches. Table, Graph and threads must agree. Preserve the landing audit, completed/closed populations, standing proposals and bounded context. |
| Maintained references and examples | Reconcile the affected I6 status/work/work-loop pages, architecture and tool schema examples. Keep historical evidence labelled as historical; do not rewrite recorded events. |

For transition, accept the deprecated singleton filter `statuses=["stale"]`
as `stale=only` over the six live lifecycle states. It is a filter alias, never
an emitted lifecycle. Explicitly document that it now includes stale live
completion/landing states as well as open/promised work. Reject combinations
with another status or a conflicting explicit stale policy rather than silently
widening or narrowing them; callers can express those queries using the real
statuses and `stale=only`. Keep any compatibility handling in the shared query
normalizer, with CLI/MCP/HTTP tests. Remove the alias only through a later
announced compatibility change.

Build and test readers, embedded UI and clients at the same reviewed head.
Demonstrate that an old projection cache is rejected and replayed under the new
profile while verified event checkpoints remain intact. Deploy the resident
reader before enabling the matching producer/client path, renew owned adapters,
and verify current status through each supported path. Do not solve mismatch
by relaxing strict decoding, resetting witnesses or restarting foreign hosts.
A separately authorized operational request owns deployment and rollback.

## Authority and receipt limits

General intake and decision-start authority are separate. Historical P3 proposed
removing mandatory author refiling for ordinary stale assigned requests, even
where no authority-bearing decision chain is involved. That text change is
still undelivered and deferred here. Current intake and `SKILL.md:368` remain:
answer the stale request, confirm unchanged conditions, availability and
governing decision, and ask its author to refile on current bases. Keep an
existing promise or report for ordinary staleness alone. P2 changes visibility,
not that workflow; changing it requires a separate explicit decision.

The current adoption rule (`SKILL.md:455`) remains in force. Before follow-on
work starts, an authority-bearing request chain must prove the requester's
adoption authority, explicit commissioning of the decision and following work,
independently approved and merged delivery, and effective, unretired, non-stale
bases at that start. Otherwise use ordinary proposal and ratification. Author
confirmation that an already-promised outcome and conditions are unchanged does
not create those missing facts. Ordinary staleness arising after authorized
work began is recorded; it does not by itself reopen adoption. Preserve the
current same-commitment correction rule (`AGENTS.md:19`).

P1 needs a separate policy choice, not a second branch hidden inside P2.
The implemented checkpoint (`internal/workroom/fold.go:2785`) distinguishes
old from new causes per cause, including mixed cases. A future proposal to
shield later causes must state how genuine later invalidation will still reach
successors and dependent documents. It must retain receipt-retirement,
condemned-succession and undated-cause refusal; exact signer, head and path
eligibility; the prospective paired-field gate; and unchanged descriptive-world
staleness. It must account for historical receipts and unrelated borrowers.
Until that reconciliation is adopted, later causes continue to propagate.

## Acceptance before delivery

1. At the same verified frontier, stale promised work remains in the performer's
   waiting lane and stale unclaimed work remains available. Assert both row
   membership and the former omission; assert empty unclaimed performer/promise
   fields. Include stale open, promised and all four live completion states.
2. Retired requests/promises, explicit transfer or abandonment, satisfied
   reports and sealed deliveries keep their current lifecycle, terminal and
   waiting-party results. Inactive authority and retired implementation inputs
   still refuse at their existing boundaries.
3. Test explicit statuses, all four stale policies, the deprecated singleton
   alias and rejected combinations through shared query, CLI, MCP and HTTP.
   Reconcile totals, page limits, cursors and closed-history omissions against
   the exact returned populations; preserve approved-not-landed rows.
4. In native browser tests, the same stale promise appears in Table and Graph
   open work, with the correct thread actor and reasoning-moved count. The
   obsolete population is absent. Closed work must not leak into open work;
   counts must not double-count the moved subset or landing audit.
5. Replay the same signed fixture through old and new fold profiles; show the
   expected lifecycle change and unchanged authority/effectiveness, cause and
   terminal evidence. A deliberately retained old-cache profile must fail the
   compatibility control. Keep producer/schema guards intact.
6. Existing dated-checkpoint, mixed-cause, receipt-retirement, undated-cause and
   world-staleness controls remain unchanged and pass. Review an omission
   mutant that drops a stale promised row and a lifecycle-override mutant that
   restores `status=stale`; both must fail their acceptance checks.

Obtain Hugh's ordinary adoption of this exact decision, then independent
Architecture/Security/Simplification review and sealed note delivery. Assign P2
separately on the then-current adopted decision and behavior bases. P1 and any
change to P3 intake/refiling or authority need their own explicit decisions; P4's completed drain
and P5's delivered instruction clarification are not implementation backlog.
