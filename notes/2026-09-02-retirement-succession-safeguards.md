---
date: 2026-09-04, revived from the approved but unlanded head of 2026-09-02
status: candidate design; no implementation is authorized by this note
origin: request 54a38cc7. Historical head d26228c4, artifact #16619, and
  approval #16623 are reviewed evidence only.
bases: main 296e0fdd; the target-aware landing design in
  notes/2026-09-04-landing-obligation.md; and the landed reconciliation design
  in notes/2026-08-31-merged-head-commitment-reconciliation.md
does not: implement these rules, repair historical withdrawals, or reopen the
  target-aware landing and merge-accounting decisions
---

# Keep request succession from disappearing

## Decision sought

Ratify these three rules as one design before filing any implementation
request:

1. A terminal retirement of an unsatisfied request cites one already-filed
   effective successor request as a supersession basis, or explicitly says
   that the work is abandoned and gives the signing author's reason. A request
   holding an approved Git artifact uses the target-aware landing design's
   stricter carried-or-abandoned rule.
2. A broken structural successor chain qualifies exactly one existing
   commitment row for its root. That is the request row when one exists and
   otherwise the first promise row already projected for the root. It creates
   no row, work collection, owner, waiting party, or lifecycle status.
3. A request left unclaimed when its addressee's lease lapses is reassigned
   with `reassign_if_unclaimed`. Lease absence is never a reason to retire it
   and promise that a replacement will be filed later.

The rules address the failure seen in the 2026-09-02 audit: real work was
retired during staleness and lease recovery, while the promised replacement
never became a durable request. They do not try to repair the 502 historical
withdrawals found by that audit.

## What current main establishes

The 2026-09-02 audit concern remains sound, but current main makes six design
boundaries explicit.

1. `internal/workroom/schema.go` still exposes only
   `workroom/supersede@0`, whose payload is `target` plus `text`. There is no
   structured successor or abandonment choice. `decideSupersede` still checks
   the first basis and current author or ratifier authority, and
   `qualifyingRequestSuccessor` recognizes only the existing rejected-review
   transfer. This proposal is prospective; it does not reinterpret those
   records.
2. `internal/app/admission.go:311-360` runs position-aware admission for state
   and guarded-reassignment schemas, but has no ordinary supersession case.
   `kernel.Options.PostDedup` runs once for a genuinely new submission inside
   the compare-and-swap loop (`internal/kernel/kernel.go:86-93,584-605`). The
   request-retirement rule belongs there as early feedback and in the fold as
   the authoritative decision.
3. Guarded reassignment is already stale-tolerant and position-aware.
   `unclaimedExpectationReason` checks an effective request, explicit absent
   promise and completion, one live retirement, signer continuity, and exact
   first-basis shape (`internal/workroom/fold.go:1210-1280`). It does not yet
   project a missing second half as attention.
4. The global summary filters only `superseded`, `satisfied`, and `withdrawn`
   as terminal (`internal/statusview/view.go:125-135,374-387`). Retiring a
   promised root without a direct completion emits no separate request row;
   its existing promise rows become `cancelled` and remain visible in global
   `Attention`. The per-actor digest likewise leaves `cancelled` and `reneged`
   rows in `NotActionable` (`internal/statusview/actor.go:437-445`; commitment
   construction at `internal/workroom/fold.go:3189-3233`). A new fallback row
   would therefore duplicate an already-visible root.
5. `gs work` and MCP `work` share `statusview.BuildWorkPage`, including the
   service call at `internal/service/server.go:292`. Named statuses include
   matching terminal history, and an explicit staleness policy can include
   terminal-but-stale rows (`internal/statusview/query.go:307-324`). The design
   must qualify the existing row instead of relying on a summary or actor-lane
   filter to suppress a duplicate.
6. The thread spine selects the first commitment for a request, then reads its
   promise and report (`ui/src/lib/spine.ts:67-75,103-159`). Qualifying that
   same first row preserves the stations without an ordering exception.

Two newer designs constrain the vocabulary rather than replace this work.
The target-aware landing design already reserves `disposition=abandoned`, the
terminal status `abandoned`, and a carried successor for a request holding an
approved artifact. For that case its rule is authoritative: the successor
request rests on the live reporting artifact at the approved head, or the
retirement declares abandonment and gives a reason. This note supplies the
general request-retirement envelope and the broken-chain projection; it does
not add a competing direct-child test to the approved-artifact case.

The landed merge-reconciliation design also gives `merge_left_live` entries
the three classes `carried`, `sibling`, and `abandoned`. Those entries concern
live artifact candidates at exact paths. They are signed, verified accounting
and grant no retirement authority. An abandoned artifact remains live until
its author or a ratifier retires it. Request disposition in this note is a
different fact; neither one is inferred from the other.

The landing design itself contains one drafting conflict. Section 7 puts
`disposition=abandoned` on the supersession body, while section 9 lists
`disposition` among fields admitted by the original `workroom/state@3`
request. An abandonment is chosen at retirement time, so this design resolves
the shared contract in favour of section 7: the field belongs only to
`workroom/supersede@1`, not to `workroom/state@3`. The corresponding landing
implementation-contract correction removes `disposition` from section 9's
`state@3` field list. That reviewed correction must land before either
implementation. Four further section-7 corrections are explicit and complete:
whole-edge staleness suppression becomes retirement-bit-only suppression on
the qualifying edge; the exception is limited to a successor relation
qualified by an effective `supersede@1`, rather than every projected
`successor_request`; and carried succession gains the separate cause-specific
rule below and requires the successor to have the same requester as the target.
No other landing rule changes. Every historical `supersede@0`
`successor_request` edge keeps its transfer-created staleness, including the
`7f0f13c1` case that motivated the landing note's broader draft exception.

## 1. Put a lien on terminal request retirement

Introduce `workroom/supersede@1`. It keeps the existing `target` and `text`
and adds an optional string map named `body`. Its only defined field is
`body.disposition=abandoned`. The existing non-empty supersession `text` is
the signing author's reason for abandonment, so a separate reason field would
duplicate it. No other supersession body field is admitted.

A carried supersession does not repeat its successor in the payload. It rests
first and exactly once on the target request and cites exactly one later basis
that is an effective successor request. This extends the existing
`qualifyingRequestSuccessor` representation instead of creating two encodings
for one edge.

A new writer uses `supersede@1` whenever its target is a request. The
application boundary refuses every newly submitted `workroom/supersede@0`
whose target resolves to a request, while replay continues to admit every
historical `@0` decision exactly as recorded. The lien applies only to an
`@1` event when the target is an effective, unretired request and no projected
commitment for that request has reached `satisfied`.

That `@0` refusal belongs to the application boundary adapter (`internal/app`)
after kernel dedup, not to the layer-5 fold: the fold must continue to replay
every historical `@0` record under its recorded meaning.

For a request that does not hold an approved reporting artifact, the cited
successor basis is valid only when all these facts hold at the exact prefix the
retirement would join:

- the cited basis is one full canonical event identifier in this workroom;
- it names a different effective, unretired request already in the log;
- the successor was authored by the same requester as the target; and
- the successor directly rests on the target request.

For a request that does hold a live reporting artifact at a ratified approved
head, apply the landing design instead: the one cited successor basis must name
an effective, unretired request already in the log, authored by the same
requester as the target, that directly rests on that artifact, or
`body.disposition=abandoned` is present. This carries the approved head rather
than merely renaming the work. The landing design owns its remaining target
and head checks. This note adds only the same-requester precondition and does
not duplicate those existing checks.

An admitted successor becomes the commitment's existing `successor_request`
edge. For `supersede@1` only, `qualifyingRequestSuccessor` keeps its first-basis,
unique-successor, same-requester, and direct-rest checks but does not require a
ratified changes-requested artifact. Historical `supersede@0` keeps that
precondition exactly as `requestHasRatifiedChangesRequestedArtifact` enforces
it today (`internal/workroom/fold.go:527-530,1102-1138`), so replay cannot give
old retire-and-refile records a new edge or lifecycle. `retire-if-unclaimed@0`
also lowers to a `Supersede` at `internal/workroom/fold.go:485-489` and remains
deliberately outside this `supersede@1` exception. The approved-artifact
`@1` branch substitutes the landing design's direct-artifact carry check for
the general direct-request check. An abandonment keeps the terminal status
used by current ordinary requests. Once the landing design's fold slice is
active, an approved-artifact abandonment uses its distinct `abandoned` status.

### Settle only the staleness caused by the transfer

A successor that must rest on what its supersession retires would otherwise
be stale as soon as the retirement takes force. Extend the landing design's
transfer-staleness rule from rejected rounds to every successor relation
qualified by an effective `supersede@1`, but only on the edge that proves that
exact relation.

For a general succession, when the staleness pass examines the successor
request's direct basis edge to the retired target request, it ignores the
target's retirement only when the same admitted supersession projects that
successor as the target's `successor_request`. It does not ignore
`stale[target]`: reasoning staleness that already reached the target before
retirement still reaches the successor. Every other basis of the successor is
examined normally. This is a local change to `retiredBasis`, not `staleBasis`,
in `stalenessOf`'s existing edge pass
(`internal/workroom/fold.go:2277-2382`, especially `2320` and `2338`);
`2264-2276` is its two-line staleness wrapper. The succeeded-artifact
retirement precedent at `2333-2337` does not broaden it.

An approved-artifact carry has a different exact edge. The successor rests on
the reporting artifact, not directly on the retiring request, and that artifact
becomes stale through the old commitment when the request retires. On the
successor-to-artifact edge, a cause-specific checkpoint suppresses propagation
only when all active staleness below that artifact is accounted for by the one
request retirement whose admitted supersession qualified this carry. Implement
one memoised all-causes walk in `stalenessScope`, analogous to
`causesSettledAtReceipt` (`internal/workroom/fold.go:2636-2668`) but
keyed to exactly that named retiring-request cause, not to a receipt plan or a
date alone. Starting at the reporting artifact, it returns true only when every
live propagating retired or stale cause resolves to that exact request
retirement. A different retired or stale basis, an independently retired
artifact, or a cause whose identity cannot be resolved makes the checkpoint
return false and staleness propagates. The artifact keeps its own stale and
world-stale facts; only the carried successor begins the new current reasoning
epoch.

Both checks are position-aware. Build the qualified request-successor map on
the `stalenessScope`, resolving both the supersession record and its cited
successor record at or before `asOf`, as the scope-positioned artifact map does
at `internal/workroom/fold.go:2196-2238`. Do not use the end-of-log
`linkedRequestSuccessor` result at `1178-1189` for this decision. The
supersession, successor, artifact, approval, and both halves of each successor
edge must exist at or before the scope. Retiring the supersession removes its
`successor_request` edge and both exceptions. The cause walk memoises only
settled nodes and stays inside the existing ancestor closure, following the
pattern at `2636-2668`; it performs no Git or network read and allocates no
second general provenance table.

Every `@1` event aimed at an already-retired request refuses. A later event
cannot revise the recorded disposition or convert an in-flight guarded
reassignment. Retiring the supersession itself still removes that retirement
cause and its successor edge, as ordinary reversibility requires.

### Enforcement and stable errors

`internal/workroom` owns one position-aware request-retirement check. The fold
calls it from `decideSupersede`; `internal/app` calls the same check from
`admitApplication`. Construction signs the request first; the kernel then runs
`PostDedup` inside its compare-and-swap loop after deduplication and before any
commit is written. The fold remains the authoritative replay decision. A
frontier race can therefore refuse before append without pretending the local
signature did not already happen.

The user-facing `gs supersede`, `gs batch`, and MCP `supersede` writers select
`@1` for request targets and meet the same application admission. Audit every
other `app.VerbSupersede` producer: artifact cleanup in `cmd/gs/query.go`,
publication-fact replacement in `cmd/gs/publication.go`, merge artifact
succession in `internal/mergeplan`, and membership or role retirement in
`internal/app`, plus artifact-target fixture construction in
`internal/perfscenario/fixture.go:293`. Tests must prove those paths never
target a request, so keeping their non-request encoding cannot bypass the lien.

Schema validation rejects malformed field combinations before the fold. The
fold resolves target, closure, approved-artifact state, and successor against
the exact prefix. Stable errors are:

- missing choice: `request retirement must cite one effective live successor
  request, or set body.disposition=abandoned; give the reason in text`;
- conflicting choice: `request retirement cannot cite a successor and set
  body.disposition`;
- bad disposition: `request retirement body.disposition must be
  "abandoned"`;
- bad general successor: `request retirement must cite one already-filed
  effective, unretired direct child request by the same requester`;
- bad approved-head successor: preserve the target-aware landing design's
  exact carry refusal rather than introduce a second wording here;
- unknown field: `supersede body.<field> is not defined`; and
- already retired: `request is already retired; a later supersession cannot
  change its succession`.

These are admission errors, not ineffective acts that appear only after a
green write.

### Guarded reassignment is the one in-flight form

`reassign_if_unclaimed` is already a guarded, resumable pair. Its first event
uses `retire-if-unclaimed@0`; its second uses
`reassign-if-unclaimed@0`. The first event is not an ordinary supersession and
is not an abandonment. Its distinct schema and parsed `guardedRetirement`
marker bound the exemption; its expectation names the request and requires
promise and completion to be absent. The second event names that retirement
exactly.

Keep that protocol. Treat an effective guarded retirement with no effective
second half as `replacement-missing` attention until the caller retries the
same idempotency key. This is the only interval in which an unsatisfied retired
request may have no successor event yet. It is bounded by an explicit schema
and recovery operation, not by prose. A later ordinary supersession cannot
relabel it abandoned.

The fold identifies the exemption from `parsed.guardedRetirement`. A guarded
retirement that lowers through `decideSupersede` does not need the new body and
keeps all existing expectation checks.

## 2. Qualify a broken successor chain

Add an optional `succession` qualifier to the existing projected commitment:

```text
succession.status = broken
succession.reason = replacement-missing | successor-retired |
                    chain-abandoned
succession.tip = <last request event that resolved>
succession.expected = <unresolved successor event, when present>
```

The qualifier is derived only from signed structure:

- the one qualified successor request cited by an admitted request
  supersession, including a carried approved-artifact successor;
- the existing qualified `successor_request` edge for rejected rounds; and
- an effective `retire-if-unclaimed` plus its exact guarded replacement.

The detector never parses retirement text, branch names, ticket numbers,
presence, or request conditions. Starting at a structurally replaced request,
follow successors until one of these tips appears:

- an effective, unretired request with an `open`, `promised`, `reported`,
  `awaiting-merge`, or lifecycle-`stale` commitment is a live tip;
- a `satisfied` commitment is a satisfied tip;
- an explicitly abandoned request is an intentional terminal tip; or
- a retired successor without a further structural successor is broken.

An abandonment made directly from the root is intentional and is not broken.
A chain represented as transferred that later reaches abandonment before a
live or satisfied tip is qualified as `chain-abandoned`: the transfer chain
was dropped, while the final abandonment remains truthful.

Retiring a supersession removes the edge that supersession established. If no
other retirement cause remains, the request and commitment become live again.
Historical `@0` causes retain only their recorded meaning and never acquire a
new edge.

Every valid successor is later in the log than its parent, so cycles are
inadmissible. Build one request-to-successor map while folding, then memoize
the terminal result per request. Each request and edge is visited once: time is
O(requests + successor edges), storage is O(requests), and no Git or network
read occurs. Existing response and list caps still bound what leaves the
resident.

### Board and thread surface

Keep lifecycle status, waiting party, `successor_request`, promise, and report.
The qualifier adds evidence; it rewrites none of them.

Qualify exactly one existing commitment row per maximal broken chain: the
chain's root, the earliest request with no incoming structural successor.
Compute ordinary commitment rows first. If the fold emits a request row for
the root, including a direct-completion row carrying `Performer` and `Report`,
attach `succession` there. Otherwise promises already supply one or more rows;
attach it only to their first projected row. That row keeps its truthful
`cancelled` or `reneged` status, `Promise`, `Performer`, `Report`, `Stale`, and
`successor_request`. No intermediate request and no sibling promise row gains
the qualifier.

The first-row rule is a contract, not presentation trivia. The thread spine
already selects the first commitment whose request is the root, so it reads
the qualified row and preserves its promise and report stations. A direct
completion remains the qualified request row itself.

Existing aggregate commitment totals and the underlying commitment row count
do not change. `Attention` and `NotActionable` list lengths and their cap counts
may increase because one existing row becomes selectable; no commitment gains
a second row, succession count, or new population. Selection changes at the
actual filters. The global summary admits a qualified broken row to `Attention`
before its terminal filter. The per-actor digest similarly admits it to
`NotActionable`. `BuildWorkPage` admits it to that same lane before the default
non-actionable and terminal-stale omissions; named-status and explicit-
staleness queries still return the same row, never another copy. Existing
`cancelled` and `reneged` rows already pass the first two filters, but the
explicit rule is needed for qualified `superseded` and `withdrawn` rows.

Layer 7 changes both `needsAttention` and `inPopulation`, not the lifecycle
vocabulary (`ui/src/lib/rows.ts:144-167`). A row with
`succession.status=broken` sets `needsAttention=true`, enters `live` as existing
`needs attention`, and also remains in whichever lifecycle population its
status selects, including `closed`. It does not enter `moved` merely because of
this qualifier. The thread detail renders reason, tip, and expected successor
as evidence; the closed view continues to render its existing `superseded`,
`cancelled`, `reneged`, or `withdrawn` `RowState`. This adds no population and
no named row state.

Opening the row shows one recovery hint:

- for `replacement-missing`, retry the same `reassign_if_unclaimed`
  idempotency key; if the guarded-retirement author is unavailable, only that
  event's author or a ratifier can retire it, and only a ratifier is a viable
  recovery actor after the author has gone; after restoration, start a new
  guarded pair; or
- for the other reasons, file a current successor request, then retire the
  broken tip with that structural successor.

This is one qualifier and one existing board affordance. It creates no inferred
owner, named status, or second closure system. If implemented alongside the
merged-head reconciliation view, both extend the same bounded commitment-view
builder.

## 3. Reassign after lease lapse; never retire for it

Presence is advisory and session-bound. It cannot authorize a durable act or
prove that an actor abandoned work.

1. Absence of a live addressee may prompt the requester or a ratifier to
   inspect the request. It does not change the request.
2. If the request is still unclaimed, use `reassign_if_unclaimed` with the new
   addressee and a stable idempotency key. Do not use ordinary supersession.
3. If a promise or direct completion wins before the first act, the guarded
   retirement refuses and changes neither request nor replacement. If a race
   occurs after that first act but before the second, the request remains
   retired in the visible `replacement-missing` interval; retrying the same
   idempotency key either completes the pair or reports the precise failed
   expectation.
4. If no replacement actor is available, leave the request open. Do not record
   `abandoned` merely because a lease expired.

Current main admits acts on merely stale ground while recording the staleness,
and guarded reassignment deliberately has no staleness precondition. A retired
basis still needs the explicit dead-basis override. Ordinary request
retirement is never a workaround for the guarded checks.

The fold cannot enforce the operator's motive for an explicit abandonment;
presence is deliberately outside durable authority. The structural lien still
prevents the harmful shape: a bare retirement saying “will refile” is refused.

For a general request, the same-requester rule remains intentional even when
the requester has left. A ratifier may retire it but cannot manufacture a
successor in the departed requester's name; unless that requester already
filed the direct child, the truthful structural choice is abandonment. The
approved-artifact case remains governed by the target-aware carry rule above.
The same-requester rule applies there too: ratifier authority to retire a
request cannot grant a third party's request relief from its ordinary
staleness.

## Profile and migration

The new supersession payload, explicit successor projection, and succession
qualifier change decoded and projected bytes. Their implementation advances
the Workroom fold profile and matching application projection cache gate from
whatever version is current when it lands. Current main is
`workroom-fold@18`; the target-aware landing design already assigns `@19` to
its first fold slice, so parallel implementation must not reuse that number.
The implementing request chooses one ordered next profile after its actual
bases are fixed.

Schema version, not profile version, makes the lien prospective. Historical
`supersede@0` events replay under legacy rules. No migration writes the log.
The 502 audit withdrawals are grandfathered, and the detector classifies an
old row only when the existing log already has one of the structural edges
listed above.

Existing guarded pairs also replay. A missing second half gains a derived
qualifier and keeps its same idempotent recovery. Retiring the guarded
retirement restores the request only when no other retirement cause remains.

## Security and failure properties

- Supersession authority is unchanged: only the target's author or a current
  ratifier may retire an ordinary request. Citing a successor basis grants no
  role.
- Canonical identifiers are untrusted input. Schema validation checks shape;
  position-aware admission checks event, kind, decision, liveness, author,
  sequence, direct provenance, and, where applicable, approved-artifact carry.
- A bad or missing successor is caught by application admission after the
  request is signed but before any commit is written, then judged again by the
  authoritative fold on replay. A frontier race can refuse; it cannot bind
  another request.
- Succession staleness relief is not a blanket provenance exemption. The
  general branch suppresses only the retirement bit on the exact qualified
  request edge and preserves earlier staleness. The approved-artifact branch
  suppresses a stale artifact basis only after a scope-positioned, memoised
  cause walk proves every active cause is the one qualified request retirement;
  a different or unknown cause propagates staleness.
- The same requester must author both kinds of successor. A ratifier may
  authorize retirement but cannot use an approved-artifact carry to confer a
  freshness exception on a third party's request.
- Guarded retirement is structural and cannot be relabelled abandonment. Its
  signer continuity and request-local compare-and-swap remain intact.
- The detector is derived read-only state. It signs nothing, follows no URL,
  reads no key, and cannot satisfy, reopen, reassign, or retire a commitment.
- Response caps and text truncation remain at the status boundary. The
  qualifier carries event identifiers and a finite reason enum, not request
  text or secrets.
- A crash between guarded acts is visible and resumable. A promise or
  completion that wins the race prevents the first act, exactly as today.
- Merge `carried` or `abandoned` testimony remains artifact accounting only.
  It cannot confer request-retirement authority or silently retire an artifact.

## Required proof for implementation

An implementation request must require mutation-sensitive tests for:

- missing, conflicting, malformed, unrelated, cross-author, ineffective, and
  retired general successors; valid direct child; explicit abandonment; and
  the target-aware landing design's approved-head carry and abandonment cases;
- `admitApplication` and authoritative fold parity through `gs supersede`,
  `gs batch`, MCP `supersede`, and exact idempotent replay with byte-stable
  lifecycle projection for historical `@0`;
  plus proof that artifact cleanup, publication replacement, merge succession,
  actor retirement, and role revocation never aim their `@0` records at a
  request;
- no lien on a request already satisfied at the exact prefix;
- a general qualified successor staying fresh when the retired request was
  fresh, inheriting pre-existing staleness from that request, and becoming
  stale when any unrelated basis retires; mutation of the qualified successor
  identity must make the first case stale;
- an approved-artifact carry staying fresh only when the old request retirement
  accounts for every active cause below the artifact; an already-stale or
  independently retired artifact, an unrelated stale artifact basis, an
  unknown cause identity, a successor by another requester, and mutation of the
  carry edge must all stale or refuse the successor while leaving the artifact's
  own stale flags unchanged; a post-scope successor or supersession must not
  change an earlier staleness result;
- complete guarded reassignment, missing second half, idempotent recovery,
  author-gone ratifier recovery, and promise or completion races, including a
  stale request;
- attempted missing or ineffective successors establishing no edge; live,
  satisfied, retired, abandoned, and multi-hop tips; plus the linear-work
  bound;
- direct completion qualified in place with no duplicate; a promised root's
  first existing promise row qualified with its lifecycle, `Stale`, promise,
  performer, and report unchanged; aggregate totals and the underlying row
  count unchanged while attention-list and cap counts change truthfully; and
  every sibling promise row left unqualified;
- preserved promise and report stations in the thread spine, including a
  mutation that qualifies a later sibling rather than the first and makes the
  test fail;
- profile/checkpoint replay without migration; bounded global summary and
  board rendering; global `Attention`, per-actor `NotActionable`, default,
  named-status, and explicit-staleness work queries each carrying the
  qualifier on the one row they select, without adding a duplicate; a mutation
  that leaves a broken closed row's lifecycle population intact but fails to
  set `needsAttention` must make the rendering proof fail, rather than show it
  as ordinary `unclaimed`; and
- separation from merge left-live accounting: classification alone retires no
  artifact or request, and its `abandoned` word sets no request disposition.

The implementation head updates the architecture reference for every affected
layer. The application boundary adapter (`internal/app`) selects the schema and
performs post-dedup, pre-commit admission. Layer 5 (`internal/workroom`) gains
the `@1` schema,
request-retirement decision, structural successor projection, its two exact
succession-staleness checks, and in-place qualification. Layer 6
(`internal/statusview` and transport) carries the
qualifier through its existing summary, actor, work-query, inspect, and wire
rows and makes the bounded filter exceptions stated above. Layer 7 (`cmd/gs`,
MCP, and UI) writes `@1` for request targets, sets `needsAttention` for a
qualified broken row, selects that row into `live` while retaining its
lifecycle population, and renders its evidence without adding a population or
`RowState`. It must also update the CLI and
MCP supersede, reassign-if-unclaimed, status, and work references plus
`SKILL.md`, replacing the current false statement that guarded reassignment
requires a request to "remain live and fresh" with the actual rule: it must
remain live, while ordinary staleness is deliberately permitted. No
storage, signature, custody, resident single-writer, or Git integration
contract changes.

## Deliberate exclusions

This proposal does not implement any rule, repair historical withdrawals,
infer intent from prose or presence, create a new status or work population,
make guarded reassignment atomic, otherwise reopen the target-aware landing
design beyond the disposition-location and carried-staleness corrections named
above, or change merge receipt succession authority. Those exclusions keep the
scope to three facts: prevent new invisible request loss, show structural
breaks, and use the guarded path already meant for unclaimed reassignment.
