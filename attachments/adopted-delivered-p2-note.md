---
title: Keep stale work visible without changing its authority
summary: Reconcile the historical P1–P5 plan and bound the remaining lifecycle and attention correction.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3f96b40f6ab49cf2de503c92db1787d8271b9669
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f2b0f9c2b5cc9d2c68cbe8e3f401a57c5bed42a4
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8c93b4d4577389c55898a93b307f1f951d645fb8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bce39c253ab8f654cd788f5c152463dd0b6bfd11
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8a60f7689221433c1d85948c2bbe7c3cd6258388
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ebabce8ff6f39781c14a16378ffe3f06588d80f2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f5ca1c3b34c09a4a1a5f26ac366b94c748e3ca9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14f7a0a63a7462223746bfd82d35e5790b7fcfe5
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ea22065bd87c36e8aed84e03552cfcbb12c0893d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ea4ae86b6807b16474e5dc5ae7dfa485d2836fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5127d61121a2845e41e909ed88e8a75374dd6bea
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1c15fb28f5df85b922f551195b7b736f2488138
---

# Keep stale work visible without changing its authority

Hugh adopted this P2 decision on 9 September 2026 through proposal #19939
(`092dbfdd`) and ratification #22164 (`613d1a38`). It recovers the remaining
attention defect from historical proposal #14703 as one bounded implementation.
This revision refreshes source references and delivery evidence without changing
that decision. Receipt propagation and permission to begin work remain separate
questions. This note changes no production behavior; independent review and
sealed note delivery precede a separately assigned implementation.

## What remains from P1–P5

| Part | Current evidence and disposition |
|---|---|
| P1: stop causes arising after a receipt from staling its published successors | Conflicts with the implemented dated checkpoint. Ruling #11588, ratified by `25eab16d`, settles causes active at or before the receipt and propagates later causes. Its implementation was recut under #12104. These precede #14703; that later adoption proposed the opposite change, which remains undelivered. Keep the current rule while the conflict is explicitly decided separately. P2 changes no receipt edge or cause. The later #21944 delivery at `359b211c` preserves destination-bound implementation discharge when artifacts retire; it does not shield published successors from later staleness causes. |
| P2: keep lifecycle beside staleness | Remaining. An unreported live promise can still become status `stale` and disappear from the performer's actionable lane. Preserve `open` or `promised` with the existing stale flag and cause evidence. The other four live lifecycle states already survive ordinary staleness. |
| P3: admit stale bases and remove mandatory refiling | Admission is delivered by `3f75695c0fcd67d32cb2150bb9c416c22ae85ccd`, request #16262, receipt #16339. The boundary owns `stale_bases`; merely stale bases are admissible, while retired bases retain their explicit refusal/override rules. Preserve the earlier owned-stamp repair #15325: ordinary `stale`/`staleness` fields are admission-owned, with only the confined exception for a field owned by a ratified kind schema. The general P3 text change—replace author refiling of ordinary stale assigned requests with answering them like fresh requests—remains undelivered and is deferred by this decision. Keep current intake and SKILL discipline 11 until separately decided. This is distinct from the later authority-chain start checks, request #16406 and delivery `c8e84040de7dc3356ee83725b80a864a55bbe97b`, which remain in force below. |
| P4: drain existing orphans once | The eleven dispositions recorded in #14687 already occurred. Its design question is satisfied by report #16386 (`b2e0bbf2`), ratified by `e57a1fd2`. Later adopted landing design #17029 and delivered I1–I5 supply target-aware delivery and explicit unresolved-landing attention. Do not repeat the drain or revive ancestor-head closure or retirement at unchanged paths. |
| P5: keep corrections on their original commitment | Delivered in `ce18ec8742ddd8665afa284a9cde1ce946213435`, under #18175 and Hugh's adopted proposal #17682. Existing outcome, conditions, performer, destination and authority must remain the same; publish a corrected artifact citing the finding and obtain fresh review. A separate outcome or changed authority still needs an assignment. Planner's five-round observation is separate; this note claims no completed trial or effort saving. |

The original proposal remains ratified but ordinarily stale; its candidate
`ec80e0448f8aa794e011430663421d08f86587ab` describes a superseded world.
Historical adoption is evidence for this reconciliation, not fresh authority
for that old implementation plan. The artifact's evidence includes exact
requests, ratifications, delivered receipts and the original review.

Source-file links below name the exact baseline in ordinary repository Markdown.
The adjacent `path:line` references open the same source at this note artifact's
immutable head in Gitseq. Byte-equivalence checks and exact earlier revisions
accompany the artifact's evidence.

## Reproduced attention defect

At verified frozen frontier #19682 (`24b6353f5b41ef95a7d8737bc7e138fbaeda2520`),
request #19651 has live promise #19653, no report, status `stale`, `stale=true`
and `waiting_on=claude`. Running the then-current work-query code over that verified
snapshot returns the row in `not_actionable` and omits it from `waiting_on_you`.
The fold's no-report branch replaces the lifecycle; the query then excludes it.
Both are necessary parts of the failure. The historical run used the source
baseline named below; its signed evidence is retained.

This is a frozen historical case: the live binding assignment has since
advanced. The separate historical reader query found an unclaimed stale request
in `available_to_you`, with no invented promise, performer or waiting party.
Those observations used main `07753351d69c05dedec58f4e2561a793054312a5` and
reader `39c7a0e74f4a828ba8223acbacc3f2c82eb58016`; the fold and query files
were byte-identical then. They are not claims about today's deployed readers.
The frozen snapshot was verified from the signed log without actor or sequencer
keys and without rewinding a checkpoint.

The current source baseline is main
`0afa98aa26be6756682f28defb081043ecc8efa2`, including #21944 and the Case C
documentation delivery. Its fold profile is `workroom-fold@24`. The unclaimed
stale override ([fold.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/workroom/fold.go#L3629), `internal/workroom/fold.go:3629`) and the unreported-promise
override ([fold.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/workroom/fold.go#L3679), `internal/workroom/fold.go:3679`) remain; the query's classification
([query.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/statusview/query.go#L220), `internal/statusview/query.go:220`) is unchanged. A fresh cold replay of the signed #19682 frontier under Go 1.26.7
and this profile reproduces the live promise's `stale` status, its membership in
`not_actionable` and its omission from `waiting_on_you`. This current-code check
is separate from the original experiment. Reader observations in the artifact
are process-reported profiles, not attestations that current main is deployed.
Preserve the landed review-binding and destination-bound receipt-discharge
checks in the eventual implementation.

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
| Fold and model ([fold.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/workroom/fold.go#L127), `internal/workroom/fold.go:127`) | Remove the two stale-status overrides; retain stale qualifiers, causes, promise and waiting-party fields. Completion, retirement, transfer and landing precedence remain unchanged. |
| Profile and schemas ([schema.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/workroom/schema.go#L13), `internal/workroom/schema.go:13`) | Advance the next unused fold profile from the then-current version (at this source baseline `workroom-fold@24`). No new signed-event schema is required: record bytes and act-time authority are unchanged. Update projection descriptions and golden expectations. |
| Bounded status ([view.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/statusview/view.go#L129), `internal/statusview/view.go:129`), actor status ([actor.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/statusview/actor.go#L257), `internal/statusview/actor.go:257`) and work query ([query.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/statusview/query.go#L270), `internal/statusview/query.go:270`) | Route stale open/promised rows through their normal relationship lanes. Counts retain lifecycle totals and stale totals per lifecycle. Keep closed-history summaries, omission counts, limits, cursors and the independent approved-not-landed audit. |
| CLI ([main.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/cmd/gs/main.go#L2510), `cmd/gs/main.go:2510`), MCP ([main.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/cmd/gitseq-mcp/main.go#L710), `cmd/gitseq-mcp/main.go:710`) and HTTP ([server.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/service/server.go#L302), `internal/service/server.go:302`) | Use the same projection and filter contract; update input enums/help and output descriptions together. No adapter may silently return an older classification as current. |
| Browser types ([api.ts](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/ui/src/lib/api.ts#L68), `ui/src/lib/api.ts:68`) and row populations ([rows.ts](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/ui/src/lib/rows.ts#L48), `ui/src/lib/rows.ts:48`) | Include stale open/promised rows in open work and its existing reasoning-moved subset. Remove the duplicate stale-not-in-flight population, its counter and dead selection branches. Table, Graph and threads must agree. Preserve the landing audit, completed/closed populations, standing proposals and bounded context. |
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
still undelivered and deferred here. Current intake and [SKILL.md](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/SKILL.md#L367), `SKILL.md:367` remain:
answer the stale request, confirm unchanged conditions, availability and
governing decision, and ask its author to refile on current bases. Keep an
existing promise or report for ordinary staleness alone. P2 changes visibility,
not that workflow; changing it requires a separate explicit decision.

The current adoption rule ([SKILL.md](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/SKILL.md#L463), `SKILL.md:463`) remains in force. Before follow-on
work starts, an authority-bearing request chain must prove the requester's
adoption authority, explicit commissioning of the decision and following work,
independently approved and merged delivery, and effective, unretired, non-stale
bases at that start. Otherwise use ordinary proposal and ratification. Author
confirmation that an already-promised outcome and conditions are unchanged does
not create those missing facts. Ordinary staleness arising after authorized
work began is recorded; it does not by itself reopen adoption. Preserve the
current same-commitment correction rule ([AGENTS.md](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/AGENTS.md#L19), `AGENTS.md:19`).

P1 needs a separate policy choice, not a second branch hidden inside P2.
The implemented checkpoint ([fold.go](https://github.com/generalbusiness-ai/gitseq/blob/0afa98aa26be6756682f28defb081043ecc8efa2/internal/workroom/fold.go#L2911), `internal/workroom/fold.go:2911`) distinguishes
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

Hugh's ordinary adoption of this decision is recorded. Complete fresh independent
Architecture/Security/Simplification review and sealed note delivery for this
source reconciliation. Assign P2 separately on the then-current adopted,
independently reviewed and delivered decision and its behavior bases. P1 and any
change to P3 intake/refiling or authority need their own explicit decisions; P4's completed drain
and P5's delivered instruction clarification are not implementation backlog.
