---
date: 2026-08-20
status: proposal — for review. Specification only. No kernel or fold
  code changes accompany this note, and none should follow from it
  until a separate proposal is ratified. Sections marked **built**
  describe what main carries today and cite the code; everything else
  is proposal.
origin: hugh's 2026-08-20 priority — composite kinds to compact the
  ceremony — under request `a84b6441`, promise `40f8bfd0`. Written in
  the declaration vocabulary of 2026-08-08-first-ontology, against
  the merged declared-kinds implementation.
---

# Composite kinds

Two acts in the workroom's loop are almost always appended in pairs,
and the pairing carries no information the log does not already hold.
A performer's promise repeats the request it rests on. A review
request repeats the artifact it points at. This note specifies two
composite kinds that fuse each pair into one record, in the
first-ontology declaration vocabulary, and says precisely what the
fusion must not lose.

- **undertake** — request and promise fused. Legal when an actor
  claims work for itself. Rests on a ratified decision or on a
  directive addressed to the claimant.
- **submit** — artifact and conditions-met report fused at an exact
  head, and standing as the request for review.

Adoption is a separate ratified proposal. This note proposes; it does
not enact. Nothing in it changes what the fold does today.

## What the ceremony costs

The numbers are measured, not estimated, and they come from two
places: the depth-4651 measurement of 2026-08-12 and the supported-host
cycle that merged as `ca2836ab` this morning.

From the 2026-08-12 measurement, at durable depth 4,651 across 180
merges: about **26 durable records per merge**, and about **735 records
a day**. Retirement bookkeeping outnumbered the pointers it maintained
— 890 supersede acts against 812 artifact statements. Those 890
retirements targeted 565 artifacts, 180 requests, 76 reports and 34
promises: requests were withdrawn and refiled five times as often as
promises were, which says the shape of the assignment moved far more
than the claim on it did. Of 796 commitments, 286 (36%) never closed
with a report at all. The follow-up on 2026-08-19,
at depth 6,315, put the daily rate at about 240 records a day after the
three simplification lanes landed, so the process has already got
cheaper once; this note is about what remains.

The live cycle shows where the remainder sits. The supported-host
repair runs from sequence 7166 to 7221 in the current log. Counting
only its own records:

| what | records |
|---|---|
| routing the child request (7166–7170) | 5 |
| the implementer's promise (7172) | 1 |
| implementation artifacts at six paths (7174–7179) | 6 |
| review request, review promise, verdict, ratification (7180, 7183, 7188, 7189) | 4 |
| merge note (7190) | 1 |
| successor artifacts at seven paths (7191–7197) | 7 |
| merge tail: 19 supersessions and 5 succession authorizations (7198–7221) | 24 |
| **total** | **48** |

Thirteen of those 48 records carry content — the six implementation
artifacts and the seven successors. The other 35 are ceremony or
bookkeeping.

Two of those 35 are the subject of this note.

**The routing detour, 7166–7170.** Codex held a ratified
changes-requested verdict and knew exactly what to build. It could not
promise anything, because a promise must rest on a request and no
request existed. So codex asked the planner to issue one:

```
7166  request  codex → planner   00279a9c…d03727c52742
      "Planner: issue one focused implementation child to Codex …
       Do not implement the repair yourself."
7167  promise  planner           23573154…1587f9bc0d3b
7168  request  planner → codex   2d0a7aad…5a79d8538ad6
7169  report   planner           a4e86ec7…ec02a60f0efe
7170  ratify   codex             106bf86f…2b24a5c1
7172  promise  codex             2de66853…f25193ed266f
```

Five records so that the sixth could exist. The conditions in 7168 are
codex's own conditions from 7166, carried across with the repair scope
appended. Nobody learned anything at 7167, 7169 or 7170 that 7166 had
not already said.

**The review request, 7180.** Its body is
`{artifact, branch, conditions, head, to}`. The `artifact` field names
`bac269be`, one of the six artifacts codex had just published; `head`
repeats the commit that artifact already carries. Two of the five
fields are a copy of the record it points at, and the pointing is the
only reason it exists.

Neither cost is the largest one. The 24-record merge tail is, and
neither composite touches it. That is stated here rather than buried,
because a proposal that claims more than it delivers is worse than a
smaller one that is honest: **the composites remove six records from
this 48-record cycle, about 13%.** The bookkeeping tail is a different
problem for a different note.

## What is already fused (built)

One of the two composites is half-built, and the note would be wrong
to propose it as though from nothing.

`latestCompletion` in `internal/workroom/fold.go` already treats an
artifact as the report that closes a promise. The rule reads: the
record's declared render class is `artifact`, its `body.commit` is
non-empty, its author is the promisor, and it rests on exactly one
promise — that promise. When such a record exists and no live explicit
report does, the commitment moves to `reported` with the artifact as
its report. The comment in the fold states the reasoning in the same
terms this note uses: "the promisor's artifact already carries the
exact head and the promise it fulfils, so filing a second report would
duplicate the same assertion."

AGENTS.md step 3 is the prose form of the same rule: "it is the
implementation report, so do not file a duplicate `ready-for-review`
report."

So the artifact-plus-report half of `submit` is behaviour on main.
What `submit` adds is a name for it, a machine-visible conditions
field, and — the part that is not built at all — standing as the
request for review.

The other thing worth noting before the definitions: **almost every
artifact rule in the fold keys off the declared render class, not off
the compiled kind name.** Succession lineage, the state@1 path
refusals, merge-receipt plans, `reviewedPaths`, review independence and
`artifactPath` all test `definition.Render == RenderArtifact`. Only two
places in the tooling test the name `artifact`: `validateReview` in
`cmd/gs/main.go`, which requires the primary citation to be a standing
statement of kind `artifact`, and the successor publisher in
`cmd/gs/succession.go`, which writes `Kind: workroom.KindArtifact`.
That asymmetry decides most of the migration story below.

## undertake

### Definition

```
kind-def body:
  name:      undertake
  fields:    present(to), type(to, actor-ref), present(conditions)
  basis:     count({propose, request, submit}, 1..1)
  satisfier: none
  render:    commitment
  staleness: propagates
  lifecycle: undertaking            <- new lifecycle value
  guidance:  One actor claiming work for itself. The conditions are
             the claimant's own statement of what will satisfy it,
             binding on the claimant exactly as a request's
             conditions bind a performer.
```

### What the fold checks

Four total checks, each with a typed refusal:

1. `body.to` names an actor in the live roster at this position — the
   rule the fold applies to any `request` lifecycle today.
2. `body.to` equals the record's own author. This is what makes the
   fusion legal, and it is the whole of the legality condition. An
   undertake addressed to anyone else is `ineffective`: assigning work
   to another actor is a request, and a request must be claimed by the
   actor it names.
3. Exactly one effective basis of kind `propose`, of lifecycle
   `request`, or of kind `submit`. More than one is `disputed`, as it
   is for a promise today; none is `ineffective`, with the reason
   "undertaking has no authority" by analogy with "dangling promise
   has no request".
4. If the basis is a `propose`, it is ratified at this position
   (`f.ratified`). If the basis is a lifecycle `request`, its
   `body.to` equals the claimant — today's promise-actor rule,
   unchanged. If the basis is a `submit`, the rules in the
   basis-constraint section below apply, including the independence
   rule that keeps the claimant off its own submit.

The declaration and the checks name the same bases two ways on
purpose, because that is what the code already does. `promise` today
declares `basis: count(1..1, request)`, a kind-keyed constraint the
generic `validateBasis` enforces, and the fold *separately* re-reads
its bases through `basesOfLifecycle(RestsOn, LifecycleRequest)` for the
actor rule. The kind-keyed constraint is the declared floor; the
lifecycle read is what makes a room-declared kind under another name
work. Both apply here unchanged.

Check 4 is where "a ratified decision or a directive" becomes two
cases of one act, and the second case is exactly today's promise.
**An undertake resting on a directive is a promise.** The composite
generalises the claim act rather than sitting beside it; whether the
`promise` kind is then retired is an open question below, not a
consequence.

### What commitment row it opens and closes

The fold projects commitments by walking every effective record of
lifecycle `request` and looking for its promise dependents. An
undertake participates on both sides:

- Resting on a directive, an undertake is found where
  `directDependents(request, LifecyclePromise)` looks today. The row
  is unchanged: requester the assigner, performer the claimant, status
  `promised`, waiting on the claimant.
- Resting on a ratified decision, an undertake **is** the row. Request
  and promise are the same event; requester and performer are the same
  actor; status is `promised`, waiting on the claimant. The commitment
  opens on the log at the moment the actor claims the work, which is
  the whole point of the composite.

It closes exactly as a promise closes: by a completion resting on it —
a `submit`, or a lifecycle report that the requester ratifies, or a
sealed merge receipt. Because the requester and the performer are the
same actor, the ratification arm is self-ratification and is
therefore not a check on anything. That is not a defect introduced
here; AGENTS.md already says a commitment loop between one actor and
itself keeps no promise the log needs. What closes self-claimed
implementing work is the review approval and the merge receipt, both
signed by other actors.

### How staleness reaches it

`staleness: propagates`, like every kind in the starter catalog. An
undertake goes stale when its basis is retired, or when its basis is
stale and the basis's own kind propagates. The practical consequence
is one to state plainly: **an undertake resting on a ratified decision
inherits that decision's staleness directly, with no request between
them to absorb it.** Today a superseded decision stales the request,
and the request stales the promise, two hops. Fused, it is one hop to
the same result, so no record escapes a flare it would have received.

What fusion does remove is a second chance to notice. This workroom
has a standing failure mode — an act recorded on a basis the author
did not check, stale from birth — and today the request is a place
where that becomes visible before the claim is made. An undertake
collapses the two into one signature, so the basis check happens once
or not at all. The mitigation is not a fold rule; it is that the fold
already reports the staleness on the undertake itself, in the row the
composite exists to create.

## submit

### Definition

```
kind-def body:
  name:      submit
  fields:    present(path), present(commit), present(conditions),
             type(to, actor-ref)              [to is optional]
  basis:     count({promise, undertake}, 1..1)
  satisfier: none
  render:    artifact
  staleness: propagates
  lifecycle: none
  guidance:  The claimant pointing at the exact head that satisfies
             the claim, saying which conditions were met and which
             gates were run, and asking for review. One submit per
             head; plain artifacts carry the other paths.
```

`conditions` is the field that distinguishes a submit from a bare
artifact: it is the report half, and it is the same content the
implementer writes today in the artifact's `text` and copies again
into the review request's `body.conditions`.

`to` is optional on purpose. Naming a reviewer is a courtesy, not a
constraint the fold should enforce, and the workroom already behaves
that way: unclaimed reviews get rerouted within minutes by whichever
agent is free. When `to` is absent, any actor other than the
submitter may claim the review.

### What the fold checks

1. `path` and `commit` are present, and `path` is neither `.` nor a
   comma-joined pseudo-path. These are the state@1 artifact rules, and
   a submit gets them for free by declaring `render: artifact`.
2. Exactly one basis that is a claim: kind `promise` or kind
   `undertake` by the declared constraint, lifecycle `promise` or
   `undertaking` by the fold's own read.
3. The submit's author is the claimant of that basis — the promisor,
   or the undertaker. This is the built `isArtifactReport` author test
   stated as an admission rule instead of a discovery rule.

Check 3 is a strengthening, and it is worth naming. Today the author
test lives inside `latestCompletion`, which decides whether an
artifact *counts as* a report; an artifact by the wrong author is
simply not picked up, silently. As an admission rule the wrong author
gets a typed refusal at append time.

### What commitment row it opens and closes

It closes the row its basis opened, by the built artifact-as-report
rule, with one extension: the candidate scan in `latestCompletion`
must also follow dependents of an `undertaking`, not only of a
`promise`. Status moves to `reported`, waiting on the requester — or,
for a self-claimed undertaking, waiting on the reviewer, since there
is no other requester to wait on.

It opens the review row by standing where the review request stands:
the reviewer's promise rests on the submit. See the basis-constraint
change below.

### How staleness reaches it

`staleness: propagates`. A submit is an artifact for every purpose the
projection has: it occupies its `path`, it is a predecessor a successor
must retire, it flares what rests on it when superseded, and it
carries `describes_superseded_world` across a direct retired-artifact
edge. The succession rules in AGENTS.md apply to it unchanged, because
they are written in terms of paths and live artifacts, and a submit is
live at a path like any other.

One consequence follows from fusing the report into the artifact, and
it is already true of today's artifact-as-report: **retiring the
artifact withdraws the completion claim.** The fold carves out one
exception, that merge-driven succession retires the candidate artifact
as it publishes the mainline successor and that planned retirement must
not erase the merge which satisfied the promise. A submit inherits both
the rule and the exception, because both are written against render
class.

## The basis-constraint change

Today the `promise` kind declares `basis: count(1..1, request)` and the
fold reads it through `basesOfLifecycle(RestsOn, LifecycleRequest)`.
The change is:

```
promise    basis:  count({request, submit}, 1..1)
undertake  basis:  count({propose, request, submit}, 1..1)
```

with a corresponding widening of the fold's promise check. A review is
claimed the same way either kind claims anything, so the two carry the
same `submit` arm and the same rules; `undertake` carries `propose` as
well because only it may claim from a decision.

Three rules attach to the `submit` arm:

- The promisor is **not** the submitter. Fusing the review request
  into the submit puts the reviewer's claim directly on the
  implementer's own record, so independence has to be enforced where
  the claim lands. It is enforced today only in `gs review`'s
  validator — "review actor signed the artifact under review; an
  independent reviewer must sign the verdict" — which is a CLI
  guard, not a fold rule. This change moves it into the fold, which is
  a strengthening the note claims as a benefit and not a side effect.
- If the submit carries `to`, the promisor is that actor. If it does
  not, any actor satisfying the previous rule may claim.
- The row this opens is a review commitment: requester the submitter,
  performer the reviewer, status `promised`. The submit is its request
  event.

A submit therefore closes one row and opens another with the same
record. That is unusual and should be said outright rather than left
to be discovered: it is the same shape as a report that is also a
question. The fold already tolerates one record playing two roles —
an artifact is a pointer and, conditionally, a report — so this is a
second instance of an existing pattern, not a new kind of thing.

## What the fusion must preserve

Three guarantees. Each is stated as what would break if the fusion
were done carelessly, because that is the only useful form.

**1. Promise before report.** No completion may exist that no claim
authorised. The fusion removes the *request*-before-promise gap for
self-claimed work; it must not remove the claim-before-completion one.
It does not: a submit rests on exactly one promise or undertaking, and
its author must be that claim's claimant. There is no path by which a
submit exists without a prior claim by the same actor, because the
basis constraint is `1..1` and the author check is total. The fold's
"only the promisor may report" becomes "only the claimant may submit",
with the same force. The 36% of commitments that never closed with a
report are a different failure — claims that produced nothing — and
neither composite addresses it.

**2. Exact-head approval invalidation.** An approval names one
immutable commit and is invalidated by any movement of the head. This
survives fusion untouched, because the head lives in `body.commit`,
not in the loop's shape. Rewrite the branch and the commit id changes,
so the old submit still names the old commit, the approval still names
the old commit, and `gs merge` still refuses a candidate that is not
the approved head. `validateReview` re-reads its basis immediately
before signing and refuses if anything moved between the two reads;
`reviewedPaths` admits only cited artifacts whose `body.commit` equals
the reviewed head. Both tests read the body. Fusing the pointer into
the report cannot weaken a check that reads the pointer.

The one thing to watch is that a submit is *also* the review request,
so a second submit at a new head after a changes-requested verdict is
both the new pointer and the new review request. That is correct
behaviour — a new head needs a new review — but it means the
implementer must retire the superseded submit, or two live submits
stand at one path. That failure is already common: the 2026-08-12
measurement found 23 paths carrying two or more live artifacts,
excluding `.`. A submit standing where an artifact stood makes it no
worse, and no better.

**3. The reviewer's claim act.** `submit` removes the review
*request*. It must not remove the review *promise*, and it does not.
The reviewer still appends a claim resting on the submit. Three
things depend on that record existing:

- It is the only row that says a review is in flight, who holds it,
  and when they took it. Without it an approved head and an ignored
  head look the same on the board.
- `gs review --promise` requires it and refuses a verdict signed by an
  actor who did not make it. The verdict rests on the promise; a
  verdict with nothing to rest on is a report with no promise, which
  the fold refuses as ineffective. It is not a hypothetical failure:
  at depth 7,235 this log holds 23 records refused with exactly that
  reason.
- It is where independence is checked at claim time rather than at
  verdict time, which is the earlier and cheaper place to fail.

Fusing the reviewer's promise into the verdict would save one record
and cost all three. It is not proposed.

## Worked example: the supported-host repair, replayed

The same cycle as the table above, rewritten with both composites in
force. Event ids are the real ones from the log at depth 7235;
`(none)` marks a record that would not be appended.

| today | with composites |
|---|---|
| 7166 request codex→planner `00279a9c…d03727c52742` | (none) |
| 7167 promise planner `23573154…1587f9bc0d3b` | (none) |
| 7168 request planner→codex `2d0a7aad…5a79d8538ad6` | (none) |
| 7169 report planner `a4e86ec7…ec02a60f0efe` | (none) |
| 7170 ratification codex `106bf86f…2b24a5c1` | (none) |
| 7172 promise codex `2de66853…f25193ed266f` | **undertake** by codex, `to: codex`, conditions as filed in 7166, resting on the ratified verdict `fcd6a4be` |
| 7174–7179 six artifacts | **one submit** at the primary path with `conditions`, five plain artifacts at the others, all at commit `fc63ec40` |
| 7180 review request `fda4ec31…d11444944e88` | (none) — the submit is the request |
| 7183 promise claude `4620edfd…cb9a6b5d5f83` | unchanged: claude's review claim, resting on the submit |
| 7188 report claude approved `a2e6f257…0162f2ffb967` | unchanged |
| 7189 ratification `8b473e1f…fc2a02df` | unchanged |
| 7190 assert `5994a902…a7fa976d6785` | unchanged (a courtesy note, not required) |
| 7191–7197 seven successors, 7198–7221 merge tail | unchanged |

Six records removed from 48, and the six implementation artifacts
become one submit plus five plain artifacts, not five records. The
verdict, its ratification, the merge receipt, the successors and the
retirement tail are all untouched.

Two details from the real records are worth keeping in view. The
review request 7180 carried `body.artifact = bac269be…a18f5c2edad2`
and `body.head = fc63ec4037e32cf41a24ed22aff0413e3b8183b8`; artifact
`bac269be` carries that same commit. Under this proposal the reviewer
would rest its promise on the submit directly and neither field would
be written twice. And request 7166's conditions were reproduced into
7168 with the original request's conditions and the repair scope
appended: 1,146 characters written by codex, re-emitted as 2,137
characters by the planner, so that a promise would have something to
rest on.

## Interaction with the existing surfaces

**gs review.** Two compiled-name checks in `validateReview` would
refuse a submit. The primary citation is fetched as a standing
statement of kind `artifact` (`cmd/gs/main.go`), and the review
promise's unique basis is fetched as kind `request`. Under this
proposal both become render- and lifecycle-tests: the primary citation
is any standing statement whose declared render is `artifact`, and the
promise's unique basis is any standing statement of lifecycle
`request` or kind `submit`. The `reviewBasis.Request` field then holds
the submit event, and the verdict rests on `{promise, submit,
artifacts…}` exactly as it rests on `{promise, request, artifacts…}`
today.

Everything else in the review path already reads render and needs no
change: the independence test, the set validation that every co-signed
artifact stands at the exact head, and the staleness line the verdict
writes into its own body.

**gs merge.** Unchanged. The merge gate reads the approval and the
artifacts it cites, tests `Render == RenderArtifact`, matches
`body.commit` against the merge candidate, and derives its succession
plan from `reviewedPaths`. A submit is an artifact to all four. The
one rule to re-check at implementation time is the co-signing bound:
a receipt may retire only artifacts the verdict named, so a head that
publishes one submit plus five plain artifacts still needs all six
named in `gs review --artifact`, exactly as six artifacts do today.

**Succession.** Unchanged, and this is the reason `submit` declares
`render: artifact` rather than a render class of its own. The
succession rules are written in exact path strings against live
artifacts. A submit occupies a path, is retired by a successor at that
exact string, and is republished by `cmd/gs/succession.go` as a plain
`artifact` — the successor is a mainline pointer, not a claim, and
should not carry `conditions` or a promise basis. So the composite
appears on the branch side of a merge and never on the mainline side.

**The UI.** The 2026-08-19 simplification replaced the earlier
stream-and-drawer arrangement, so the first-ontology note's built
claims about `belongsInRoom` and `buildWorkProjection` no longer
describe this tree; both files are gone. What stands now is
`ui/src/lib/rows.ts`, which builds every work row from
`projection.commitments` — request, requester, performer, status,
waiting-on — and reads no kind name at all except to ask whether the
actor waited on is a human. A composite that projects a correct
commitment row therefore needs no change here, which is the strongest
argument for specifying both composites in terms of the commitment
they project rather than in terms of anything a surface would have to
learn.

Two places do still read compiled kind names, and both want a test
rather than a change. `ui/src/lib/spine.ts` labels a retired statement
as a withdrawal when its kind is one of `request`, `promise` or
`report`, and treats a retired `artifact` as superseded — so an
`undertake` or a `submit` would fall through both branches and be
drawn without its label. The fix is to read the declared lifecycle and
render, which is what the presentation contract asks for anyway.

## Reconciliation with the AGENTS.md self-initiated rule

AGENTS.md already eliminates the self-request, self-promise and
self-report for same-actor work: "the implementing commit rests
directly on the motivating ratified decision, and the durable filing
is the artifact plus the review request only." It also names the price:
"this path shows no in-flight commitment row, so nobody can see from
the board that the work is underway."

So `undertake` does not reduce the record count for self-initiated
work. Counted honestly, one changed path, one review round:

| | today, assigned | today, self-initiated | with composites, assigned | with composites, self-initiated |
|---|---|---|---|---|
| request | 1 | 0 | 1 | 0 |
| claim | 1 | 0 | 1 | 1 (undertake) |
| implementation pointer | 1 | 1 | 1 (submit) | 1 (submit) |
| review request | 1 | 1 | 0 | 0 |
| review promise | 1 | 1 | 1 | 1 |
| verdict | 1 | 1 | 1 | 1 |
| verdict ratification | 1 | 1 | 1 | 1 |
| merge receipt | 1 | 1 | 1 | 1 |
| successor + supersession | 2 | 2 | 2 | 2 |
| **total** | **10** | **8** | **9** | **8** |
| in-flight row on the board | yes | **no** | yes | **yes** |

What `undertake` adds beyond the AGENTS.md rule is exactly one thing:
**the visible in-flight commitment row, at no extra record.** The
AGENTS.md rule buys its saving by trading that row away; the composite
buys the same saving a different way and keeps it. For assigned work
the rule does not apply at all, and the saving comes from `submit`
alone.

There is a second, less countable gain. The AGENTS.md rule is prose
that a reader must apply correctly: same actor, so drop three records,
and rest on the decision instead. `undertake` is the same rule as a
kind the fold checks — `to` must equal the author, the basis must be
ratified — so getting it wrong is a typed refusal rather than a
malformed log nobody notices. Roughly two thirds of AGENTS.md is
process prose of this sort. Turning a paragraph of it into an
admission rule is worth more than the record it saves.

The rule and the composite cannot both be in force. If `undertake` is
adopted, the self-initiated-work paragraph in AGENTS.md is replaced by
one sentence naming the kind. That replacement belongs to the adoption
proposal, not to this note.

## Migration and compatibility

The two composites sit on opposite sides of the interpreter boundary
described in the first-ontology note, and the difference decides how
each is adopted.

**submit can be a ratified kind-def today, with two CLI fixes.**
`render: artifact` and `lifecycle: none` are both inside the closed
sets `validateDefinition` enforces. A ratified `submit` kind-def would
be admitted, its records would be artifacts to the whole fold by
render, and `latestCompletion` would already treat them as the
promisor's report — no fold change at all for the promise arm. The two
blockers are both compiled-name checks in `gs review`, named above.
The `undertake` arm of its basis constraint and the review-request arm
of its behaviour need the fold change below.

**undertake requires an evaluator release.** `validateDefinition`
closes the lifecycle set to `none`, `request`, `promise` and `report`
and returns `unsupported lifecycle` for anything else, which projects
as `uninterpretable`. A room cannot ratify a new lifecycle value. That
is the boundary working as designed: the fold reads lifecycle directly
to decide what a promise may rest on, who may report, and what status
a commitment holds, so a definition that changed it would change what
the fold does without changing how the fold reads it. Growth pressure
lands on the evaluator, exactly where declared-kinds puts it, and
`undertake` arrives as a **fold activation**, not as a ratification.

That is also the honest answer to "behind a ProfileVersion or behind
vocabulary adoption?" — neither, for `undertake`. The admission
profile governs bundle, contract and genesis, and the record schema
version governs record shape; a new lifecycle value is neither. It is
an interpreter change with its own succession, judged by the
incumbent.

**Old folds and old records.** A kind is a body field, so no schema
version changes and no existing record is reinterpreted. A fold
without the new evaluator rules `undefined-kind` on both composites,
which is the typed, honest verdict. The order of operations is the one
this workroom has already been bitten by: restart the resident and the
adapters on the new binary before rebuilding the CLI, or the old fold
refuses records the new CLI writes.

**Prior kind additions.** The path is established. Declared kinds
arrived as an evaluator wave with a compiled starter catalog, and the
catalog reports `source: starter` so a surface can tell a
compatibility definition from a ratified one. `submit` can join that
catalog or arrive as a room-local ratified definition; `undertake`
must join the catalog, because its lifecycle value has to exist in the
evaluator first. A phased adoption is therefore available and probably
right: ship `submit` first, measure, then decide about `undertake`.

## Open questions

1. Does `undertake` subsume `promise` entirely, retiring the kind, or
   stand beside it? Subsuming is cleaner vocabulary and a larger
   migration; standing beside it means two kinds for one concept, which
   is the thing this note exists to reduce.
2. Does `to` belong on `submit` at all? Leaving it off entirely would
   make every review an open claim and would match how reviews are in
   fact rerouted here; carrying it preserves the ability to ask a
   named reviewer.
3. One submit per head, or one per changed path? This note proposes one
   per head with plain artifacts elsewhere, on the grounds that the
   conditions are a property of the head. Per-path submits would make
   every path's artifact self-describing at the cost of repeating the
   conditions.
4. Should review independence move into the fold, as the basis-
   constraint section proposes, or stay a CLI guard? Moving it makes
   the guarantee total but makes independence a fold concept, which it
   currently is not.
5. What authority does the ratified-decision basis need? This inherits
   first-ontology open question 4 — bare ratifier grant or a stated
   quorum — and `undertake` makes it sharper, because a self-claim on
   a thinly ratified decision is a shorter path from opinion to work
   than anything the log allows today.
6. Does the assigned-work case deserve its own composite? A request and
   its claim by two different actors cannot be fused. But the
   2026-08-12 measurement found 180 request retirements against 34
   promise retirements, and 36% of commitments never closing at all,
   which says the expensive failure is a request that is refiled or
   abandoned rather than a request that is claimed too slowly. That is
   a bigger finding than either composite here, and neither composite
   addresses it.
