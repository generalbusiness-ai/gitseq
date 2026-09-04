---
date: 2026-09-04
status: candidate design. Ratified proposal #16918 adopted the scope before
  this note existed; this exact note is adopted only when an exact-artifact
  proposal naming its path and commit is ratified. It authorizes no production
  implementation. Each numbered follow-on request below must be filed and
  satisfied on its own.
origin: audit e2699f17 of approved work that never landed, planner dissent
  #16915 on the first remedy, hugh's ratification of the target-aware
  replacement #16918 (event 5a79613e), and the ratified changes-requested
  review 71190ebb of the first candidate, repaired under request 4abf0084
  after its predecessor repair child 7f0f13c1 was retired for the
  transfer-created staleness that section 7 now addresses
---

# The landing obligation

An implementation request owes a Git artifact to a named destination. Today
the workroom records the artifact and the approval, but not the destination,
and it lets an ordinary completion report close the commitment as `satisfied`
with nothing landed anywhere. The audit found eight approved features lost that
way and fifty approved heads that never reached `main`.

This note makes the destination durable, makes landing to it the obligation,
makes a hold state on that obligation rather than a completion mode, binds
receipts to the destination, and projects "approved, not landed" relative to
the destination on every surface.

Vocabulary used throughout:

- **target**: the repository and branch ref an implementation request owes its
  result to. `main` is only what a legacy record reads as when it names none.
- **landing request**: a request that names a target. It owes a Git artifact.
- **no-artifact request**: a request that states `no_git_artifact=true`. It
  owes no Git artifact and is the only kind a report can close.
- **landing**: a sealed merge receipt incorporating the approved candidate into
  the target ref.
- **hold**: a durable instruction that the landing waits for an exact release.
- **release**: a ratified structured authorization report, signed by the hold
  owner, that lifts a hold for one candidate, one approval, one request, and
  one measured target head.

## 1. The explicit result choice

Every `workroom/state@3` request states what it owes, and the fold refuses one
that does not. There are two result classes, and three explicit encodings
that select between them:

| result class | encoding | meaning |
|---|---|---|
| landing | the target triple by value: `target_repo`, `target_ref`, `target_head`, all three present | the request owes a Git artifact landed into the named ref |
| landing | `target=inherit`, with the triple absent | the same obligation, with the triple taken from request ancestry (section 2) |
| no artifact | `no_git_artifact=true` | the request owes no Git artifact: a review, a design conversation, a decision, an operation |

A `state@3` request with none of the three encodings is ineffective with the
reason "request states no result: name a target, inherit one, or state
no_git_artifact". One with more than one encoding is ineffective with
"request states more than one result". One with a partial triple is
ineffective with "target triple is incomplete". The fold never infers the
class from prose conditions. Only records older than `state@3` read a default
(section 9).

The triple:

| field | value | rule |
|---|---|---|
| `target_repo` | the workroom genesis id (`git:sha1:<genesis>`) of the repository the ref lives in | must equal the workroom's own genesis until multi-repository targets exist; any other value refuses |
| `target_ref` | a full branch ref, for example `refs/heads/main` or `refs/heads/release-2` | must start with `refs/heads/`; any other ref namespace refuses, because `gs merge` lands only into a checked-out branch and no other merge mechanism is specified |
| `target_head` | the full commit the ref resolved to when the request was filed | full lowercase object id; advisory, never a proof |

The fold stores these verbatim. It does not resolve refs; it has no
repository. Layer 7 (CLI, MCP, UI) fills `target_head` by resolving the ref at
filing time and refuses to file when the ref does not resolve.

**Movement.** A target ref that moves after filing changes nothing durable.
`target_head` is the measurement at filing; the authorization and the merge
each re-measure (sections 3 and 6). A ref deleted or renamed after filing is
reported by the projection as `target_gone` on rows that still owe a landing
there; the row's owed move is unchanged and the requester either retargets or
abandons.

**Retargeting.** The target is part of the request. Changing it is a new
request that supersedes the old one under the carried-or-abandoned rule
(section 7). No act edits a target in place.

## 2. Inheritance

The `target=inherit` encoding is admissible only when the walk below finds
exactly one triple; otherwise the request refuses as stated.

The traversal rule, stated once: starting from the request, visit `rests_on`
edges that point at request statements, breadth first, in recorded edge order,
stopping at depth eight; the nearest triples are the value triples found at
the smallest depth at which any is found, and nothing deeper is read. The
bound is new, not an existing contract: the fold has no provenance walk today,
and the status inspection cap of fifty repeated links is a rendering bound, not
an admission rule. Eight is chosen because it exceeds the longest
request-to-request chain in the current log by a margin and keeps admission
cost fixed per request; a chain deeper than that restates the triple by value.
Then:

- exactly one nearest triple: the request inherits all three fields;
- several nearest requests carrying the same triple: the request inherits it;
- several nearest requests carrying different triples: ineffective with
  "conflicting target ancestry; restate all three target fields";
- no triple within the bound: ineffective with "no target to inherit".

A request that states any target field by value must state all three; a
value triple ends the walk for its own descendants. A `no_git_artifact=true`
request inherits nothing and its descendants find no triple through it: the
walk does not pass through a no-artifact request. Review requests,
authorization requests, and other no-artifact requests therefore sit under a
landing parent without acquiring its obligation.

## 3. Terminal outcomes, the hold, and the release

A landing request's commitment closes in exactly one of two ways: a sealed
receipt into its target (section 6), or a supersession that carries or
abandons its approved head (section 7). A no-artifact request's commitment
closes by explicit report and requester ratification, as today.

**Reports on a landing request.** An explicit `report` resting on a landing
request, or on a promise under one, is handled by its body:

- a plain report, with neither `verdict` nor `resolution`: ineffective, with
  the reason "request owes a landing to <target_ref>; land it or supersede
  it". It cannot complete the commitment and cannot outrank its artifact;
- a report carrying `verdict`: ineffective on the landing request. Review
  verdicts belong only to their own no-artifact review commitments, which is
  where `gs review` already files them;
- a report carrying `resolution` with a reason: admitted as **nonterminal
  evidence**. The fold records it and the projection shows it on the row as
  `latest_resolution`, but it is never a completion, never changes the
  commitment's status or waiting party, and can never be ratified into
  `satisfied`. This retains proposal #16918 section 5's resolution-with-reason
  exception precisely: the proposal said such a report cannot discharge the
  landing obligation, and here it cannot change the landing state at all.

There is no external-handoff terminal; hugh rejected that model at #15757.

**Hold.** A landing request may carry `landing=held`. Children inherit it
with the target. The hold owner is the requester unless `hold_owner` names
another actor by fingerprint; the request author may name any actor on the
roster at admission, because delegating the release is the author's decision.
An unknown or retired fingerprint refuses.

**Release.** A hold is released by the existing structured authorization
report (`authorizes_candidate`, `authorizes_approval`, `authorizes_request`,
`target_pre_head`, plus the two new bindings `target_repo` and `target_ref`).
The parties:

| step | who |
|---|---|
| files the authorization request, a `no_git_artifact=true` request addressed to the hold owner | the performer |
| signs the authorization report | exactly the hold owner: `hold_owner` when set, otherwise the landing request's requester |
| ratifies the report | the authorization requester, that is, the performer |

This changes the authority rule. Today `gs merge` accepts an authorization
signed by the original implementation requester, the live actor named exactly
`planner`, or a live `ratifier`. For a held landing request that three-way list
no longer applies: the signer must be the hold owner, nobody else, because the
request author delegated to that actor by name. A `ratifier` who wants to
force a landing does not release; they supersede the request as carried to a
new request with a hold they own, which stays visible as an act of authority.
I3 updates the refusal table in `docs/reference/gs/merge.md` and the signer
sentence in `SKILL.md`. For an unheld landing request no authorization is
needed, and one filed anyway is judged under the current rule until phase two
of the authorization guard retires it.

**What the receipt seals.** A landing of an unheld request seals no
authorization: the receipt carries neither `merge_authorization` nor
`merge_authorization_ratification`, and a merge that names one is refused with
"request landing is not held; drop --authorization". A landing of a released
hold seals exactly the release: the receipt carries both fields, the
authorization must name this request, candidate, and approval, and its
ratification witness must precede the merge commit, all as `gs merge` already
enforces for phase one.

Prose such as "do not merge" carries no force. Layer 7 warns when a request
text contains that phrase and the body lacks `landing=held`, and offers to add
the field.

## 4. Commitment states

The fold's commitment projection gains three states, one of them replacing
`awaiting-merge`.

| status | meaning | waits on |
|---|---|---|
| `awaiting-review` | a live reporting artifact exists and no ratified approval names it | the performer, whose next move is to obtain independent review |
| `awaiting-authorization` | a ratified approval names the reporting artifact, the request is held, and no effective release names this candidate and approval | the hold owner |
| `awaiting-landing` | a ratified approval names the reporting artifact, and either the request is not held or an effective release names this candidate and approval; no sealed receipt into the target yet | the performer |

`awaiting-merge` today covers both the pre-approval and the approved case.
It is split: the pre-approval case becomes `awaiting-review`, the approved
case becomes `awaiting-landing`, and the old word is not kept as an alias, so
every enumeration site must change in the same head (section 11). A ratified
`changes-requested` verdict leaves the row `awaiting-review` until the
rejected-round successor transfer moves it, exactly as today.

Transitions:

```
promised ── reporting artifact ──▶ awaiting-review
awaiting-review ── ratified approval ──┬── held ──▶ awaiting-authorization ── release ──▶ awaiting-landing
                                       └── not held ─────────────────────────────────────▶ awaiting-landing
awaiting-landing ── sealed receipt into target ──▶ satisfied
awaiting-review | awaiting-authorization | awaiting-landing ── request superseded carried ──▶ superseded
awaiting-review | awaiting-authorization | awaiting-landing ── request superseded abandoned ──▶ abandoned
```

`abandoned` is a terminal status distinct from `cancelled`: it says an approved
head was deliberately dropped (section 7). Retirement precedence stays as
documented: retirement beats staleness, cancelled beats reneged, and
abandoned beats cancelled when the supersession declares it.

## 5. Completion precedence and one closure

One promise carries at most one effective completion at a time. Precedence,
highest first, for a landing request:

1. a sealed merge receipt naming the completion artifact;
2. the newest live reporting artifact at an approved head;
3. the newest live reporting artifact without an approval.

A resolution report (section 3) is evidence beside the completion, never in
this list. For a no-artifact request the completion is the newest live
explicit report, ratified or not, exactly as today. The two lists never mix:
the fold's `latestCompletion` currently lets any admitted report override an
unmerged artifact, and under this design no report is a completion on a
landing request, so an artifact can only be overtaken by a newer artifact or
sealed by a receipt. Each refusal is a fold decision, recorded as ineffective
like any other, so the write boundary can name it before signing.

## 6. Receipts bound to the target

`gs merge` records two more fields in the sealed receipt beside
`merge_target_pre_head` and `merge_head`:

| field | value |
|---|---|
| `merge_target_repo` | the workroom genesis id of the checkout's repository |
| `merge_target_ref` | the full branch ref the checkout had checked out, from `git symbolic-ref HEAD` |

Refusals added to the existing table:

- the checkout is detached, or its ref is not the implementation request's
  `target_ref` (inherited or stated);
- `merge_target_pre_head` is not the current value of that ref at merge time
  (already enforced through `target_pre_head` when an authorization is given;
  now enforced for every merge);
- the request is held and no effective release names this candidate and
  approval, once section 9's compatibility window closes;
- the request is not held and `--authorization` is given (section 3).

**Ref facts after the receipt.** Incorporation is ancestry from
`merge_head`. A ref that later moves forward still contains the landing; one
that is reset behind it no longer does, and the projection shows the row as
`landed-then-removed` with the receipt kept as history. A deleted or renamed
ref shows `target_gone`. These are layer-7 facts computed from the repository,
never fold facts; the fold's `satisfied` stands on the receipt alone.

**Remote publication** is advisory. `/v0/worktrees` and `gs status` report,
per target ref, whether the configured remote's copy of that ref contains
`merge_head`. No receipt or report claims it.

**Legacy receipts** without the two fields read as
`refs/heads/main` of the workroom's own repository and are marked `legacy`.

## 7. Carried-or-abandoned succession

Superseding a request whose commitment holds a live reporting artifact at an
approved head is admitted only when one of these holds:

- the supersession's successor is a request that rests on that artifact, so
  the head is carried and the old commitment projects `superseded` with
  `successor_request`; or
- the supersession body states `disposition=abandoned` and a reason, and the
  old commitment projects `abandoned`.

Any other supersession of such a request is ineffective with the reason
"request holds approved head <commit>; carry it in the successor or declare
abandoned". This makes the refile-cancels loss mode impossible to do by
accident and makes abandoned approved heads a queryable set.

**Transfer-created staleness.** The rejected-round successor transfer has a
defect observed on 2026-09-04 while this note was in review: a repair child
must rest on the rejected request, the transfer then retires that request,
and retirement propagates staleness across the child's own required direct
edge, so the child is stale the moment it becomes the successor. Request
7f0f13c1 was retired for exactly this. The fold gains one narrow exception:
when a retirement's successor is the retired request's `successor_request`,
the direct edge from that successor to the retired request does not carry
staleness. The exception is keyed to that one edge and that one relation. It
does not suppress staleness that reaches the successor by any other edge, it
does not apply to carried or abandoned successions under this section, and it
does not apply when the successor rests on the retired request without being
named as its `successor_request`. Tests: a transfer leaves the successor
fresh; retiring any other basis of the successor still stales it; a request
that merely rests on a retired request without being its transfer successor
still stales; removing the exception makes the first test go red. Because the
exception changes what the fold decides about existing logs, it lands in I1
under the same profile advance.

## 8. Target-relative visibility

`approved_not_landed` is a projection fact per commitment, true when a ratified
approval names a reporting artifact for the commitment, the commitment's
request names or inherits a target, no sealed receipt names that artifact with
a matching `merge_target_repo` and `merge_target_ref`, and the commitment is
not `abandoned`. It never means "absent from `main`".

Surfaces, all bounded like their existing rows:

| surface | change |
|---|---|
| fold commitment row | `target_repo`, `target_ref`, `hold_owner`, `release` (event or empty), `approval`, `candidate`, `latest_resolution` (event or empty), `terminal` (`landed`, `reported`, `abandoned`, or empty), `approved_not_landed` |
| `gs status`, MCP `status` | a count of `approved_not_landed` rows beside the existing totals |
| `gs work`, MCP `work` | a lane `approved_not_landed` listing those rows for the acting actor as performer or hold owner; lane membership does not change the waiting party |
| `gs inspect`, MCP `inspect` | the fields above on the commitment block |
| `/v0/worktrees` | per branch: `approved` (event or empty), `landed_into` (ref or empty), `remote_contains` (bool or unknown), `row` (the commitment it maps to, or empty) |
| `/v0/work-query` | filter by `approved_not_landed` and by `target_ref` |
| Table | a Target column (short ref, `legacy` badge when read by default) and a state text that uses the new words |
| Graph | the same card state text through the shared thread spine; no new relation family, because target is a property, not a recorded relation |

The browser may combine projected fields for presentation but may not infer a
target, a hold, or a landing the fold or the repository did not state, in line
with the layer-7 authority boundary in `docs/reference/architecture.md`.

## 9. Compatibility and legacy migration

The fold advances to `workroom-fold@19`. A cache written under `@18` is
rejected and history replayed.

Admission of the new fields is keyed by statement schema: `workroom/state@3`
requires the section 1 choice on every request and admits `target_repo`,
`target_ref`, `target_head`, `target`, `no_git_artifact`, `landing`,
`hold_owner`, and `disposition`; the same names on a `state@2` record are
opaque body text and confer nothing. This is the existing precedent for
schema-keyed admission and answers the retroactivity question: older logs keep
their old reading.

Legacy reading under `@19` applies only to requests admitted under `state@2`
or earlier:

- a legacy request whose commitment ever carried a reporting artifact reads
  as a landing request targeting `refs/heads/main` of the workroom's own
  repository, flagged `legacy`; one that never did reads as a no-artifact
  request. This is the one place the fold reads the choice from anything but
  the field, and it reads it from admitted artifacts, never from prose;
- existing `awaiting-merge` rows read as `awaiting-review` when no ratified
  approval names their artifact and as `awaiting-landing` when one does;
- existing `satisfied` rows stay `satisfied`, including those the audit found
  closed by ordinary report; the `approved_not_landed` fact is computed over
  them too, so the audit's set appears in the new lane at once, flagged
  `legacy`;
- existing receipts read as section 6 says.

Compatibility window: for one release the merge refusal for a held request
without a release is a warning, matching phase one of the authorization guard.
Every other refusal in this note is enforced from the first head that lands
it, because each one only refuses acts that are new under `state@3`.

Unmerged worktrees: `/v0/worktrees` maps each branch to its rows. A branch is
listed under `deletable` only when no commitment that names a head on it is
unsettled, where unsettled means `open`, `promised`, `awaiting-review`,
`awaiting-authorization`, `awaiting-landing`, `reported`, or `stale`, and no
row on it is `approved_not_landed`. A branch with an `abandoned` or
`satisfied` row and nothing unsettled is deletable. The listing is advice;
deletion is W1's work and a human or the branch's author does it.

## 10. The six review blockers from #15756

They were raised against the rejected handoff design and transfer here.

1. **Authorship.** The fold admits a report or reporting artifact on a
   promise only from the promise's actor, mirroring the existing promisor-only
   report rule; it admits a release only from exactly the hold owner (section
   3). Fold tests, each run once with the guard removed to prove it live:
   requester-not-owner (the requester names another actor as `hold_owner` and
   then signs a release: refused); owner-not-ratifier (a hold owner with no
   `ratifier` role signs: admitted); planner-not-owner (`planner` signs a
   release for a hold it does not own: refused); ratifier-not-owner (a
   `ratifier` signs a release for a hold it does not own: refused);
   `hold_owner` naming an actor not on the roster: refused at admission.
2. **Profile version and retroactivity.** Section 9: `workroom-fold@19`,
   `workroom/state@3`, schema-keyed admission.
3. **Non-merge authority.** An ordinary report, a resolution report, or a
   release is never consumable as merge authority; only a ratified approval
   report naming the artifact is. Stated as an invariant on the receipt
   validator, with a mutation test that feeds each of the three as
   `--approval` and expects refusal.
4. **Enumeration sites.** Section 11 lists every status-word site; the
   implementing head changes them together and adds a test that greps the
   tree for `awaiting-merge` and fails if it survives outside history.
5. **Completion precedence.** Section 5, with a test per adjacent pair.
6. **Ref verification at acceptance.** The release re-resolves the target ref
   and refuses unless it equals `target_pre_head`; the merge re-resolves it
   again before moving `HEAD`. Between verdict and ratification a force-push
   can move the ref; both checks are at act time, so it cannot go unseen.

## 11. Implementation plan

Architecture layers touched: 5 (application profile and interpreter: fold
admission, projection states, profile version), 6 (projections and queries:
status, work, inspect, work-query), 7 (CLI, MCP, UI, worktrees endpoint,
skills). Layer 2 (kernel) and layer 3 (nexus) are unchanged: no new kind and
no kernel signing change; the release-signer rule is a layer-5 authority rule.

Enumeration sites that must change in one head (blocker 4):

- `internal/workroom/fold.go`: `projectCommitments`, `unsettledCommitmentEvents`,
  `latestCompletion`, and the receipt validator; tests in `fold_test.go`,
  `direct_report_test.go`, `request_supersession_test.go`;
- `internal/mergeplan/mergeplan.go`: `unsettledCommitment`; `mergeplan_test.go`;
- `internal/statusview/view.go`: `actionable`; `internal/statusview/query.go`:
  `knownStatuses`; `view_test.go`, `actor_test.go`;
- `internal/app/app_test.go`;
- `cmd/gitseq-mcp/main.go`: the `statuses` enum of the `work` tool;
- `docs/concepts/work-loop.md`, `docs/reference/gs/status.md`,
  `docs/reference/gs/work.md`, `docs/reference/mcp/status.md`,
  `docs/reference/mcp/work.md`, `docs/reference/architecture.md`, `SKILL.md`,
  `AGENTS.md`;
- `ui/src/lib/rows.ts` (`LIVE_STATUSES` and the state text), `ui/src/lib/spine.ts`
  (the thread spine's awaiting-merge branch, which Graph cards also read),
  `ui/src/lib/api.ts` (the status vocabulary comment and type), and
  `ui/test/interaction.test.mjs`.

The list is what `grep -rn awaiting-merge` finds outside `notes/` at main
d09a5a8d; the grep test in section 10 keeps it honest as the tree moves.

Follow-on requests, each filed on its own with this note as a basis.

| id | scope | depends on |
|---|---|---|
| I1 | fold: `state@3` admission of the section 1 choice and the fields it governs, the inheritance walk, states in section 4, precedence in section 5, the transfer-staleness exception in section 7, release authorship in section 10, `abandoned`, `approved_not_landed`, profile `@19`; updates the layer-5 contract in `docs/reference/architecture.md` in the same head and publishes that page's artifact | this note |
| I2 | `gs merge`: receipt fields and refusals in section 6, legacy reading | I1 |
| I3 | authorization guard: `target_repo` and `target_ref` bindings, hold-owner signer rule, re-resolution at act time, `merge.md` refusal table | I1 |
| I4 | projections and surfaces: status, work lane, inspect, work-query, `/v0/worktrees` fields; updates the layer-6 and layer-7 contract in `docs/reference/architecture.md` in the same head and publishes that page's artifact | I1 |
| I5 | UI: Table column and state text, Graph card text through the spine, filing-time target resolution and the prose-hold warning | I4 |
| I6 | documentation: every page in the list above, plus `docs/reference/gs/merge.md` and `SKILL.md` | I1, I2, I3, I4, I5 |
| W1 | worktree classification and deletion using the `deletable` listing | I4 |

I2, I3, and I4 each depend only on I1 and may proceed in parallel with one
another. I5 waits for I4. I6 waits for all five implementation requests,
because it documents landed behaviour. W1 waits for I4.

Acceptance tests each request must add, all mutation-sensitive:

- a `state@3` request with neither shape, with both, or with a partial triple
  is ineffective; a request with the full triple or with `no_git_artifact=true`
  is effective;
- a `target_ref` outside `refs/heads/` is ineffective;
- inheritance: a single nearest triple is inherited; two nearest requests with
  the same triple inherit; two nearest requests with different triples refuse
  unless all three fields are restated; a no-artifact request blocks the walk
  for its descendants; no triple within depth eight refuses;
- a held request with an approved artifact projects `awaiting-authorization`
  waiting on the hold owner; removing the hold check makes it
  `awaiting-landing` and the test goes red;
- the five release-authority cases in section 10;
- conditional authorization: a receipt for an unheld request carries no
  authorization fields and a merge that passes one refuses; a receipt for a
  released hold carries exactly the release and its ratification witness;
- a plain or verdict-shaped explicit report on a landing request is
  ineffective; a resolution report is admitted, appears as
  `latest_resolution`, and leaves status, waiting party, and completion
  unchanged even after a ratification attempt; the same plain report on a
  no-artifact request projects `reported`;
- a reporting artifact with no ratified approval projects `awaiting-review`
  waiting on the performer, and a ratified approval moves it to
  `awaiting-landing` or `awaiting-authorization` according to the hold;
- superseding a request with an approved head without carry or abandonment is
  ineffective; carrying projects `superseded`; abandoning projects `abandoned`;
- the four transfer-staleness cases in section 7;
- a branch with an `awaiting-review`, `promised`, or held row is not
  `deletable`; one with only `satisfied` or `abandoned` rows is;
- `gs merge` on a checkout whose ref is not the target refuses; on a matching
  ref it records both new fields; a legacy receipt still validates;
- `approved_not_landed` is true for the audit fixture and false after a receipt
  into the named ref, and stays true after a receipt into a different ref,
  because only a matching-target receipt discharges the obligation;
- the enumeration grep test fails when any site still says `awaiting-merge`.

## What this note does not do

It adds no kind and no external-handoff lifecycle, and changes one signing
rule, the release signer for held requests in section 3. It does
not infer targets, holds, or landings from prose. It does not begin the
recovery recuts, whose order the audit fixed and whose requests planner has
filed separately. It changes no contract in `docs/reference/architecture.md`
by itself; I1 and I4 will, and each must update that page in the same head.
