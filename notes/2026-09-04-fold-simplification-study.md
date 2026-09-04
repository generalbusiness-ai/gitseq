---
date: 2026-09-04
status: candidate design study. Research only. It adopts nothing, authorizes
  no implementation, and changes no contract in
  `docs/reference/architecture.md`. Any option it describes needs its own
  ratified proposal and its own requests before a line of it is written.
origin: request 4c69d2d6 from planner, "Research and deliver a current-main
  design study on simplifying the workroom fold into a more visible,
  maintainable state machine while preserving the language-action foundation
  and complete historical event meaning."
bases: main a30406b3 (code citations were derived at e2c29034; the only
  change between them is spike/querysandbox, which this note does not cite);
  notes/2026-09-04-landing-obligation.md; docs/reference/architecture.md
  layer 5
---

# Simplifying the workroom fold

Every claim about current code in this note cites `path:line` at commit
e2c29034. Every number is reproducible by a command named in section
"How to verify this note". Where I did not measure something, I say so.

## Scope

The subject is `internal/workroom`: the schema family, admission, the fold,
and the projection it publishes. The note covers the packages that read that
projection only far enough to say what a fold change would cost them.

The target-aware landing obligation in
[`notes/2026-09-04-landing-obligation.md`](2026-09-04-landing-obligation.md)
is treated as a required use case throughout. That note is itself a candidate
design, not adopted behaviour, so this note asks of every option: could the
option express I1 through I6 without a second rewrite. It does not assume the
landing obligation lands, and it does not assume `main` is the only target.

## What this note does not do

It does not implement anything. It does not file the follow-on requests an
option would need. It does not measure a cold fold of the live log, because
that needs a `gs` or resident call and this work was scoped to read-only
inspection of the tree. It does not claim the Tailapps core is unsuitable for
any purpose; it claims only that adopting it for the workroom fold today is
not supported by measured evidence.

---

## 1. Decision and invariant map of the present fold

The governing request asked for a "transition/invariant map". Section 1.1
explains why only half the fold has transitions, and the rest of this note
uses "decision" for the other half. That is a deliberate correction, not a
drafting slip: calling an overlapping decision cascade a transition machine
hides its semantics rather than exposing them.

### 1.1 Shape, and one word retired

The fold is two machines that share one state object, not one machine.

**Machine A, admission.** `foldState.append` (`internal/workroom/fold.go:453`,
156 lines) consumes one verified `Record` (`fold.go:21`) and emits one
`Decision` (`fold.go:31`) carrying one of five verdicts: `effective`,
`ineffective`, `disputed`, `undefined-kind`, `uninterpretable`
(`fold.go:11-19`). A decision, once emitted, is never revisited. Machine A is
a genuine state machine: its transition function is a pure function of the
record and the prefix state.

**Machine B, projection.** `foldState.project` (`fold.go:2796`, 261 lines)
walks the whole record list and rebuilds every published row from scratch.
It keeps no state between calls. Commitment status, staleness, succession
warnings, review independence, and the actor roster are all recomputed each
time from end-of-log knowledge.

Machine B is not a state machine. It is a **decision function**: status is a
pure function of final facts about the whole log, with no prior status as
input. `projectCommitments` takes no previous state and reads none
(`fold.go:3168`); every arm of both its cascades tests a fact, never a state.
That is the single most important structural fact in this note, and section 3
is mostly about what to do with it.

So "transition" is retired for the closure half of the fold, in this note and
in anything that follows it. Machine A has transitions, because a decision
depends on the prefix that preceded it. Machine B has none. The words used
from here on are:

| Word | Means | Applies to |
|---|---|---|
| transition | next state from prior state and an input | Machine A only |
| decision function | status from final facts, no prior status | Machine B |
| closure table | the proposed data form of that decision function | Option B |

The landing note's section 4 draws its states with arrows
(`notes/2026-09-04-landing-obligation.md`). Those arrows are a reader's
narrative of how a row typically travels. They are not the implementation and
Option B does not make them one.

Entry points are `Fold` (`fold.go:403`), `Evaluate` (`fold.go:410`), and the
incremental `NewFolder`/`Append`/`Projection` triple (`fold.go:417`, `:438`,
`:443`) that the resident uses to extend a folded prefix without redecoding it.

### 1.2 Payloads and vocabulary

Five payload types decode from eight schema strings (`schema.go:12-25`,
`schema.go:108-147`): `State`, `Ratify`, `Supersede`, `RetireIfUnclaimed`,
`ReassignIfUnclaimed`. Payloads must be canonical JSON, checked by
re-encoding and comparing bytes (`schema.go:195-201`).

The two guarded schemas are lowered at admission into the ordinary `Supersede`
and `State` shapes, with the signed expectation kept on the side
(`fold.go:484-500`). Every later rule then sees one shape. This is a genuine
simplification already present, and any option must keep it.

Thirteen kinds are defined: eleven in `schema.go:29-41` plus `kind-def` and
`fold-activation` in `kinds.go:14-17`. Kinds are data, not code: a
`KindDefinition` (`kinds.go:88-99`) carries field constraints, basis-count
constraints, a satisfier, a render class, a staleness mode, and a lifecycle.
The constraint algebra is closed: four field operators (`kinds.go:21-26`),
four value types (`kinds.go:30-35`), three staleness modes (`kinds.go:65-69`),
four lifecycles (`kinds.go:73-78`), seven render classes (`kinds.go:53-61`).

Three kinds are reserved from redefinition because the fold reads them
directly rather than through their definition: `kind-def`, `fold-activation`,
`roster` (`kinds.go:294`).

### 1.3 Authority

| Act | Rule | Site |
|---|---|---|
| any statement after the seed | author must hold `participant` | `fold.go:679-681` |
| the seed | must self-seed an operator roster row | `fold.go:747-752` |
| promise | actor must equal the request's `body.to` | `fold.go:764-766` |
| report on a promise | only the promisor | `fold.go:804-806` |
| report direct on a request | only the addressee, and only with no live promise of their own | `fold.go:824-836` |
| ratify a report | only the originating requester, who must still be live | `fold.go:1023-1040` |
| ratify anything else | the satisfier role, usually `ratifier` | `fold.go:1041-1047` |
| ratify a roster authority grant | not the beneficiary; operator grants need operator standing | `fold.go:1011-1020` |
| supersede own record | always allowed | `fold.go:1091` |
| supersede another's record | `ratifier`, or roster rules for governance | `fold.go:1071-1089`, `:1091` |
| supersede another's artifact | an authorized merge receipt the actor signed | `fold.go:1098`, `:1341-1366` |

Departure is enforced in three separate places with three distinct reasons
(`fold.go:2100-2104`). A retired actor keeps a roster row with no roles
(`fold.go:2993-3001`) so its old signatures still attribute.

### 1.4 Causality

`rests_on` is typed by the lifecycle of the governing definition, not by kind
(`dependentKey`, `fold.go:354-357`). Basis counting is data-driven through
`BasisConstraint` (`kinds.go:45-49`) and checked in `validateBasis`
(`fold.go:858-900`), which then overrides the generic message with a
lifecycle-specific one for promises and reports (`fold.go:884-895`).

Two acts have hard positional basis rules. A ratify must rest on exactly its
target and nothing else (`fold.go:990-992`). A supersede must rest first on
its target (`fold.go:1055-1057`).

`reportClaim` (`fold.go:793`, 50 lines) decides once which commitment a report
answers, and the answer is memoized in `admittedClaims` (`fold.go:770`,
`:2113-2120`). This is the fold's one deliberate exception to recomputation,
and its comment says why: three places counting independently is how they came
to disagree.

### 1.5 Retirement

Retirement is a counter, not a set. `retirementCauses` maps event to number of
live superseders (`fold.go:371`); `changeRetirement` (`fold.go:2125-2156`)
walks the supersession chain and flips effect on and off as counts cross zero,
so superseding a supersession restores its target. `retired` is one map lookup
(`fold.go:2158-2160`).

Retirement is retroactive in effect but never rewrites a decision. A retired
`kind-def` changes which definition governs later positions
(`refreshDefinition`, `fold.go:917-941`) and never re-judges an emitted one.

### 1.6 Staleness

`stalenessScope` (`fold.go:2174-2190`) binds one staleness computation to the
position its causes are judged against. `stalenessNow` asks with end-of-log
knowledge; `stalenessAsOf` asks at a merge verdict's position. Both run
`stalenessOf` (`fold.go:2277`, 107 lines), which is the single hardest
function in the package.

Two facts are produced in one pass: ordinary `stale`, and the narrower
`describes_superseded_world` with its date `causedAt`. The edge walk carries
five exceptions:

1. a merge plan's own intended retirements do not stale the receipt
   (`fold.go:2306-2311`);
2. a supersession's edge to its own target does not stale it
   (`fold.go:2312-2314`);
3. a succeeded retirement does not stale reasoning edges unless the
   succession chain is condemned (`fold.go:2332-2337`);
4. staleness already covered by the merge plan does not propagate
   (`fold.go:2339-2342`);
5. the receipt checkpoint settles staleness on exactly one basis edge
   (`fold.go:2350-2352`).

The world flag has its own date rule: taken across every basis, never the
first, with a `Terminal` basis blocked from passing its own date up
(`fold.go:2358-2374`).

### 1.7 Commitment closure

`projectCommitments` (`fold.go:3168`, 120 lines) emits two row families per
request.

The **request row** exists when there is a direct completion or no promises at
all (`fold.go:3189`). Precedence, first match wins:

```
successorRequest != ""      -> superseded
retired(request)            -> withdrawn
completion is a report      -> reported, then satisfied if ratified
completion is an artifact   -> awaiting-merge, then satisfied if a receipt merged it
stale(request)              -> stale
otherwise                   -> open
```

The **promise row**, one per promise (`fold.go:3238`):

```
successorRequest != ""      -> superseded
retired(request)            -> cancelled
retired(promise)            -> reneged
completion is a report      -> reported, then satisfied if ratified
completion is an artifact   -> awaiting-merge, then satisfied if a receipt merged it
no completion and stale     -> stale
otherwise                   -> promised
```

Ten status words in all: `open`, `promised`, `superseded`, `withdrawn`,
`cancelled`, `reneged`, `reported`, `awaiting-merge`, `satisfied`, `stale`.

`latestCompletion` (`fold.go:3303-3357`) chooses between an explicit report and
a reporting artifact, with a sealed merge terminal.

There is no table anywhere, and there are no edges to find. Both cascades are
first-match over overlapping conditions: a request that is retired and stale
and has a successor matches three arms, and only the arm order says which
answer wins. That order is the semantics, it lives in two `switch` statements,
and nothing prints it.

The guards being overlapping is the defect, not the ordering. Ordering is what
the code currently uses to hide the overlap.

### 1.8 Review and landing

A review is any effective report carrying `body.verdict`
(`fold.go:2925-2940`). Its independence is resolved after the projection loop
because a verdict may name an artifact that has not been read yet
(`resolveReviews`, `fold.go:3419-3441`).

The merge receipt is an `assert` statement whose body fields are interpreted
as an authority-bearing structure (`validateMergeReceiptNow`,
`fold.go:1446-1524`). It carries a JSON object inside a body string
(`merge_retirements`, `fold.go:1508`), a JSON array inside another
(`merge_changed_paths`, `fold.go:1653`), and a third JSON structure for
left-live testimony (`fold.go:1532-1625`). None of those field names appears
in any `KindDefinition`.

That is the fold's largest undeclared seam, and section 2 measures it.

### 1.9 Legacy event behaviour

Three state schema versions and two ratify versions coexist
(`schema.go:16-25`). The differences are semantic, not cosmetic:

- `state@0` records are exempt from the artifact-path rules that refuse `.`
  and comma-joined pseudo-paths (`fold.go:694-703`). The comment says why:
  changing an old decision during a refold would erase effective artifacts
  from provenance.
- `fold-activation` is defined only for `state@0` and `state@1`
  (`fold.go:686-688`, `kinds.go:197-205`), and only `ratify@0` may ratify one
  (`fold.go:1002-1004`).
- Once a fold activation is ratified, every later record is `uninterpretable`
  (`fold.go:502-508`). The fold refuses to guess past a seam it does not hold.

`ProfileVersion` is `workroom-fold@18` (`schema.go:16`). It gates the
projection cache only (`internal/app/host.go:34`, `:80`). The log is never
rewritten; a profile advance means "reject the cache and replay".

### 1.10 Invariants I believe the fold holds

Each is stated so a reviewer can try to break it.

1. A decision, once emitted, is never changed by a later record.
2. `project()` is a pure function of the record list. Same log, same bytes.
3. Retirement is reversible and its effect is exactly the live-cause count.
4. Staleness never reopens a finished commitment; it qualifies a status.
5. Authority is never inferred from prose, only from roster, satisfier, or a
   signed receipt plan.
6. A commitment has exactly one closure. One promise, one report.
7. The fold reads no clock, no repository, no network. It is total over its
   input.
8. An undefined kind is recorded and visible, and confers nothing
   (`kinds.go:140-155`).
9. A schema the fold does not know leaves the act ineffective, never
   reinterpreted as a weaker act it does know (`schema.go:125-126`).
10. Every statement carries the lifecycle and satisfier of the definition in
    force at its own position, not the current one (`fold.go:60`, `:80`).

I did not prove any of these. Nine and ten are asserted by comments and by
tests; one through eight are structural readings. Section 5 proposes how to
turn them into checked properties.

---

## 2. Measured complexity evidence

Branch counts below are `grep -cE '\b(if|switch|case|for)\b'`. That method
overcounts, because the word `for` occurs in prose comments, and undercounts
nothing. It is used only to compare files against each other, never as an
absolute figure. `gocyclo` is not installed here.

| File | Lines | `^func ` | Branch words |
|---|---:|---:|---:|
| `internal/workroom/fold.go` | 3565 | 108 | 627 |
| `internal/workroom/kinds.go` | 435 | 13 | 73 |
| `internal/workroom/schema.go` | 300 | 10 | 52 |
| `internal/workroom/render.go` | 257 | 10 | 52 |
| `internal/workroom/admission_profile.go` | 109 | 4 | 13 |
| `internal/workroom/dead_bases.go` | 70 | 1 | 12 |
| `internal/mergeplan/mergeplan.go` | 1377 | 45 | 264 |
| `internal/statusview/actor.go` | 643 | 20 | 97 |
| `internal/statusview/view.go` | 613 | 19 | 86 |
| `internal/statusview/query.go` | 598 | 10 | 100 |
| `internal/app/app.go` | 2578 | 68 | 401 |
| `internal/app/admission.go` | 410 | 15 | 68 |

The six longest functions in `fold.go`:

| Lines | Span | Function |
|---:|---|---|
| 261 | 2796-3056 | `project` |
| 156 | 453-608 | `append` |
| 120 | 3168-3287 | `projectCommitments` |
| 109 | 666-774 | `decideState` |
| 107 | 2277-2383 | `stalenessOf` |
| 94 | 1532-1625 | `validateMergeLeftLiveNow` |

### Counts a reviewer can check

| Thing | Count | Where |
|---|---:|---|
| verdicts | 5 | `fold.go:13-19` |
| kinds | 13 | `schema.go:29-41`, `kinds.go:14-17` |
| wire schemas | 8 | `schema.go:16-25` |
| payload types | 5 | `schema.go:110-124` |
| commitment statuses in the fold | 10 | `fold.go:3168-3287` |
| statuses the query layer accepts | 11 | `statusview/query.go:138-141` |
| `Projection` fields | 10 | `fold.go:261-278` |
| `Statement` fields | 16 | `fold.go:38-111` |
| `Artifact` fields | 12 | `fold.go:126-177` |
| `foldState` fields | 17 | `fold.go:359-387` |
| `parsedRecord` fields | 18 | `fold.go:285-317` |
| distinct refusal reasons in `fold.go` | 36 | `grep -o 'Reason: *"[^"]*"'` |
| `Decision{` constructions | 47 | `grep -c 'Decision{'` |
| test functions in `internal/workroom` | 200 | `grep -c '^func Test'` |
| profile versions issued | 18 | `schema.go:16`, and `@1` through `@18` in docs |
| commits that moved `ProfileVersion` | 20 | `git log -G` |

### Growth

`fold.go` first appeared at its current path on 2026-08-08 at 755 lines. It is
3565 lines at e2c29034, twenty-seven days later. That is 4.7 times in under a
month, across 58 commits touching the file. The workroom package has 4736
non-test lines and 6990 test lines, a test-to-code ratio of 1.48.

Eighteen profile versions in thirty days is the clearest single figure in this
note. Each one means the published projection bytes changed for a fixed log,
every cache was rejected, and history was replayed.

### Dependency knots

**Knot 1: the undeclared merge seam.** The fold reads 23 distinct
`state.Body["..."]` field names (`grep -o 'Body\["[a-z_]*"\]'`). Eleven of
them appear nowhere in `kinds.go`: `head`, `kind`, `verdict`, and the seven
`merge_*` fields plus `merge_left_live`. A twelfth, `artifact`, is undeclared
as a field and is missed by the command only because the same word is a kind
name. So roughly half the vocabulary the fold actually interprets is invisible
to the vocabulary layer that exists to describe it. `kind-def` can declare a
kind that requires `merge_head`; it cannot declare what `merge_head` means,
because the meaning is 400 lines of Go in `fold.go:1341-1760`.

**Knot 2: staleness, succession, and merge authority are one cluster.**
Thirty functions in `fold.go` have `stale`, `retire`, `success`, or `condemn`
in their names; twenty-two have `merge`, `receipt`, or `LeftLive`. Those sets
call each other. `validateMergeReceiptNow` calls `stalenessAsOf`, which calls
`succeededRetirements`, which calls `artifactPath` and `isArtifact`, and
`stalenessOf` in turn reads sealed merge plans back out of the records
(`fold.go:2305`). Neither concept can be read, tested, or changed alone. The
comment at `fold.go:2174-2190` is explicit that this coupling is deliberate,
because a second walker with its own edge rules diverged three reviews running.
Deliberate coupling is still coupling: it means the cheapest safe change to
either concept is a profile advance.

**Knot 3: the status word is spread across 24 files.** `grep -rln
'awaiting-merge'`, excluding `notes/` and the generated UI embed, finds 24
paths at e2c29034. The landing-obligation note found 23 at an earlier head and
already lists them as a blocker (section 11 of that note). One fold word costs
24 files to rename.

**Knot 4: the status namespace is shared with a non-fold word.**
`statusview/query.go:138-141` accepts eleven statuses. Ten come from the fold.
`awaiting-ratification` is minted by the query layer itself for a different row
family (`query.go:293-302`) and never appears in the fold. The MCP `work` tool
publishes all eleven in one enum (`cmd/gitseq-mcp/main.go:702`). A caller
cannot tell from the enum which words are fold facts.

**Knot 5: decided-once facts sit beside recomputed facts.** `admittedClaims`
(`fold.go:381`) and `linkedSuccessorRequest` (`fold.go:316`) and the sealed
merge fields on `parsedRecord` (`fold.go:298-304`) are snapshots taken at
admission. Everything else in `project()` is recomputed. Nothing in the type
system separates the two, so a new rule that reads a recomputed fact where it
should read a sealed one is a plausible mistake that compiles.

**Knot 6: path covering is implemented four times, and the left-live class
vocabulary is spelled three ways.** Covering: `pathCovers`
(`fold.go:1373`), `closestCoveringPath` (`fold.go:1731`),
`artifactCoversPath` (`mergeplan.go:474`), `widerPath` (`mergeplan.go:746`).
The three left-live class words are constants at `mergeplan.go:36-38` and bare
string literals at `mergeplan.go:1034-1036`, `fold.go:1588-1606`,
`fold.go:2885-2895`, and `render.go:169-172`. The same words in
`cmd/gs/publication.go` are a different concept and are not counted. This is
the shape of a vocabulary that exists in prose and in four heads, not in one
declaration.

**Knot 7: `project()` runs three times per write cycle.** It is called from
the reader refresh (`internal/app/app.go:2434`), the post-write fast path
(`app.go:2132`), and once per admitted act inside the admission dry run
(`internal/app/admission.go:408`), plus once more on the read-only diagnostic
path (`app.go:2241`). Each call is 261 lines that rebuild `liveByPath`,
`implementers`, `artifactCommits`, `artifactsByCommit`, and the whole
staleness closure from nothing. Whatever else changes, this is the
materialization boundary any performance work has to start from.

**Knot 8: the projection is slices, and everyone scans them.**
`Projection` (`fold.go:261-278`) publishes eight slices and two maps.
Consumers index them themselves, by linear scan, in at least four places:
`describesSupersededWorld` (`app/admission.go:291-303`), `sequenceOf`,
`decisionOf`, `reasonOf` (`reviewguard/reviewguard.go:170-193`),
`Projection.Decision` (`fold.go:3541`), `Projection.Review`
(`fold.go:3480`), and `DeadBases` (`dead_bases.go:35-45`), which rebuilds
three maps on every call. The shape of the published contract is itself a
design decision, and it is not obviously the right one.

### Where the complexity comes from

My reading of the 3565 lines:

| Category | Rough share | Evidence |
|---|---|---|
| essential language-action semantics | 25% | `decideState`, `reportClaim`, `decideRatify`, `validateBasis`, roster |
| historical compatibility | 10% | `state@0`/`state@1` branches, `fold-activation` bridge, legacy receipt reading |
| merge and succession policy | 30% | `fold.go:1341-1760`, plus the receipt terms in `stalenessOf` |
| staleness dating and exemptions | 20% | `fold.go:2174-2760` |
| optimization | 5% | interning (`fold.go:615-644`), `closure` (`fold.go:2389`), `dependents` index |
| accidental coupling | 10% | knots 1, 4, 5 above |

The shares are my estimate from reading, not a measurement. The point they
support is measurable and narrower: merge and staleness together are half the
file, and neither is a language-action primitive. Request, promise, report,
ratify, supersede, and the roster fit in about a quarter of it.

---

## 3. Options

### Baseline: keep the current structure

Continue as now. Add the landing obligation by editing `projectCommitments`,
`latestCompletion`, `unsettledCommitmentEvents`, and the receipt validator in
place, and by touching the 24 status sites.

Cost: the landing note already estimates this and it is large. The three new
states, the inheritance walk, and the transfer-staleness exception all land in
one head under one profile advance (`@19`). The result is one more layer of
precedence in a cascade that already has six arms per row family.

Benefit: no migration risk, no new abstraction, no new failure mode. Every
existing test keeps its meaning. This is the honest default and the option
every other one must beat.

Risk: `fold.go` grew 4.7 times in a month. Nothing in the current structure
resists the next such month.

### Option B: an explicit closure decision table

Keep every rule. Change where the commitment rules live, and make the overlap
that ordering currently hides impossible to write.

Introduce inside `internal/workroom`:

- a `CommitmentStatus` enum, one Go type, one place;
- a set of named, pure **facts** computed once per commitment row from the
  fold state: `retired_request`, `retired_promise`, `has_live_promise`,
  `completion_kind` (`none` | `report` | `artifact`), `completion_ratified`,
  `sealed_receipt`, `successor_request`, `stale`, and, for the landing
  obligation, `has_target`, `held`, `released`,
  `approval_names_completion`, `receipt_matches_target`;
- a **closure table**: an unordered set of `{guard, status, waits_on}` rows,
  where `guard` is a conjunction over the named facts;
- one evaluator, about twenty lines: compute the fact vector, find the
  matching row, emit the status.

`projectCommitments` becomes: gather facts, evaluate the table, emit rows.

**The guards are mutually exclusive and the set is total.** This is the whole
point and it is what distinguishes the proposal from the code it replaces. The
table is a decision function over the fact vector, not an ordered cascade.
Where today's arms overlap, the table must state the disjointness explicitly:
`withdrawn` is not "retired request" but "retired request AND no successor
request", and `stale` is not "stale" but "stale AND no completion AND not
retired AND no successor". Writing the precedence out as exclusion is the
work, and it is the part that exposes semantics the cascade currently keeps
implicit. Section 5.4 gives the three tests that hold the property.

Because the guards are disjoint, **ordering is semantically irrelevant**. The
evaluator must not depend on row order, and the test suite must prove it: one
test shuffles the table with a fixed seed and asserts the projection is
byte-identical. If any shuffle changes an answer, the guards are not disjoint
and the change is not finished.

What this buys, concretely:

- the ten (soon thirteen) statuses and the exact conditions for each become
  one printable object, instead of two `switch` blocks whose meaning is their
  line order;
- overlap becomes a compile-time-shaped bug caught by a test, rather than a
  precedence question settled by whoever edits the cascade next;
- adding `awaiting-review`, `awaiting-authorization`, `awaiting-landing`
  becomes three rows plus three facts, and adding them forces an author to
  say how they exclude the rows already there.

What it does not buy: nothing changes about staleness, merge authority, or
admission. Those stay native. This option is scoped to the one part of the
fold that is a decision function written as a cascade.

Cost: no profile advance if the table reproduces current behaviour exactly,
which section 5 must prove; one advance if it does not, and then the
difference must be catalogued and intended. My estimate is that
`projectCommitments` and `latestCompletion` (175 lines together) become about
120 lines of table plus 40 of evaluator, so the line count barely moves. The
win is visibility, not size.

Risk, stated plainly: writing the exclusions out may show that some current
answers are not disjoint at all, meaning today's behaviour depends on
precedence in a way nobody intended. That is a discovery, not a failure of the
option, but it turns S2 from a refactor into a behaviour question and the
catalogue in section 5.1 is where it must surface.

Second risk: the fact vocabulary could grow until a "fact" is really a
sub-cascade. The guard is that every fact must be a pure function of fold
state with no reference to another fact, and must be nameable in one line.

**A second cascade of the same shape exists next door.** `mergeplan.Build`
(`internal/mergeplan/mergeplan.go:1183-1322`) is thirteen named checks in a
fixed order, with twenty `return fail(...)` sites and fifteen `Reason` codes,
and its allow-reasons are appended in one batch at the end
(`mergeplan.go:1314-1320`) rather than in evaluation order. It has the same
problem as `projectCommitments`: the order is the semantics, and the order is
only visible by reading the function top to bottom. If the table pattern works
for commitment closure it should be applied here next. That is a separate
request, not part of S2, and it is listed here so the option's value is not
underestimated.

### Option C: versioned declarative commitment-status policy

**Name the thing accurately first, because the accurate name is much smaller
than "workflow layer".** Option C is *configurable commitment-status labelling
over a closed host fact vocabulary*: a workroom may rename the statuses it
shows and split a non-terminal one into finer non-terminal ones. It is not
general workflow generation and cannot become it. Novel speech acts, novel
facts, multi-party approval structures, authority rules, and merge semantics
all still require host Go code and a profile change.

### The four immutable host facts

An earlier draft of this note let a declaration choose status names, guards,
terminal flags and waiting-party selectors freely, and said the design was
safe because authority stayed native. That was wrong, and the reason is
measurable: **terminality is already load-bearing for merge**, in three
hard-coded sets that a declaration would have been choosing between.

| Site | Set | What it decides |
|---|---|---|
| `internal/workroom/fold.go:1762-1768` | `open`, `promised`, `reported`, `awaiting-merge`, `stale` | which artifacts a merge may seal as a protected sibling rather than abandoned, read at `fold.go:1552` and `:2887` through `liveProtector` (`:1781`) and `commitmentProtectsArtifact` (`:1795`) |
| `internal/mergeplan/mergeplan.go:830-837` | the same five | the merge preflight's active-commitment set, read at `mergeplan.go:782` |
| `internal/statusview/view.go:125` | `open`, `promised`, `reported`, `awaiting-merge`, **without** `stale` | the actionable lane on every board |

Two of those three gate merge behaviour, and the three already disagree with
each other about `stale`. Letting a declaration name a status that falls
outside those sets, or move a row into one, is letting a declaration decide
whether a candidate artifact is protected at merge time. That is authority.

So the contract is corrected. The host computes and **freezes**, per
commitment row, four facts that no declaration can read past, write, or
reinterpret:

| Immutable host fact | Type | Fixed by |
|---|---|---|
| semantic lifecycle class | closed host enum (`open`, `promised`, `reported`, `awaiting-merge`, `satisfied`, `withdrawn`, `cancelled`, `reneged`, `superseded`, `stale`, plus whatever a profile advance adds) | the closure table of Option B, host Go |
| terminality | bool | the class, never the declaration |
| merge protection, that is unsettled-or-not | bool | the class, and it is what `fold.go:1762` and `mergeplan.go:830` read |
| accountable party | fingerprint or none | the class and the row, never the declaration |

A declaration may do exactly two things:

1. **Label.** Attach a display name to a host class. `reneged` may read as
   `withdrawn-by-performer`. The class, terminality, protection and
   accountable party are untouched.
2. **Partition a non-terminal class into finer non-terminal labels**, using
   host facts as the discriminator. Every part inherits the parent's
   terminality, protection and accountable party, without exception and
   without the declaration mentioning them.

A declaration may not change which class is terminal, may not change which
rows count as unsettled, may not change who is accountable, and may not move a
row between classes. It has no syntax for any of those, which is the point: a
validator that refuses a forbidden field is weaker than a language that cannot
spell it.

**The projection publishes both.** The host class stays a machine-readable
field, and it is what `unsettledCommitmentEvents` and
`mergeplan.unsettledCommitment` read. The declared label is a second field, for
humans. The load-bearing consumers never see the declaration at all, which is
the structural guarantee rather than a promise about validator quality.

### Two declarations the load-time validator rejects

Both were examples in the earlier draft of this note. Both are unsafe, and
saying why is more useful than deleting them.

**Rejected: the unratified artifact.** A no-merge workroom declares that a
commitment whose completion is an artifact reads as `done`, terminal.

> Refused: *terminality is a host fact. Class `awaiting-merge` is
> non-terminal; a declaration may relabel it but may not mark it terminal.*

What it would have done: `done` is in none of the three sets above, so
`liveProtector` (`fold.go:1781`) would find no unsettled commitment protecting
the candidate, `validateMergeLeftLiveNow` (`fold.go:1552`) would classify that
artifact as **abandoned** rather than a protected sibling, and
`mergeplan.unsettledCommitment` (`mergeplan.go:830`) would drop it from the
active set. A live, unmerged, unapproved candidate would become terminal and
unprotected, and a later merge elsewhere could legitimately retire it. No
ratified report, no merge receipt, no abandonment declaration and no
succession would have been involved. That is manufacturing terminality, and
it is exactly the "approved head never lands" loss the landing note exists to
close.

**Rejected: stale outranking reported.** A workroom declares that a row which
is both stale and reported reads as `stale` rather than `reported`.

> Refused: *a declaration may not move a row between host classes. `stale` and
> `reported` are distinct classes with distinct accountable parties.*

What it would have done: `reported` carries `WaitingOn = requester`, and
`stale` carries none, so the requester would silently stop being the
accountable party for work that is finished and waiting on their
ratification. The row also leaves the actionable lane, because
`statusview/view.go:125` does not contain `stale`, so live work with a filed
report disappears from every board while remaining merge-protected. It hides
live work rather than manufacturing terminality, and the two harms are
different, which is why both counterexamples are kept.

### Three declarations the validator accepts

- *Relabelling.* `reneged` reads as `withdrawn-by-performer`. Pure label; the
  class stays `reneged`, terminal, unprotected, nobody accountable.
- *The landing note's split.* Partition the non-terminal class
  `awaiting-merge` into `awaiting-review` and `awaiting-landing` on the host
  fact `approval_names_completion`, once that fact exists. Both parts are
  non-terminal, both stay in the unsettled set, both keep the performer
  accountable. This is the flagship legal case and it is exactly what the
  landing note asks for.
- *Splitting the queue.* Partition non-terminal `open` into
  `open-unaddressed` and `open-addressed` on whether `body.to` names a live
  actor. Both non-terminal, both unsettled, requester accountable in both.

What a declaration **cannot** express, and never will under this design:

- *A two-reviewer rule.* "Satisfied needs approvals from two distinct
  ratifiers" needs a fact that counts distinct approving actors. That fact
  does not exist, and adding it is host Go plus a profile advance. The
  declaration cannot count.
- *A new speech act.* A `delegate` kind that moves a promise between actors
  needs admission rules, a basis constraint, and an authority rule.
  `kind-def` can name the kind (`kinds.go:88-99`), but the lifecycle roles are
  a closed set of four (`kinds.go:73-78`) and nothing declarative binds a new
  one.
- *Its own authority.* "In this workroom the performer may ratify their own
  report" is a change to `decideRatify` (`fold.go:1023-1040`). Section 4 makes
  this permanently non-declarable, and that is the design's main safety
  property, not a limitation to be lifted later.
- *Its own merge semantics.* Which predecessors a receipt may retire is
  `fold.go:1341-1760`. A declaration cannot widen it, which is the whole
  reason the receipt is trusted.

So the honest summary: Option C lets a third party choose the **words**, and
lets them cut a non-terminal class more finely. It does not let them choose
the boundaries of closure, and it does not let them define a workflow. Anyone
who wants either wants a different application profile on the same kernel,
which the architecture already supports through the `host` package
(`docs/reference/architecture.md`, "An application outside this module"), and
which is a much larger undertaking than a declaration.

What it buys, given that limit: a workroom can rename its statuses and split
non-terminal ones without a fork, and the landing note's `awaiting-merge`
split becomes a policy version rather than a fold edit. That is a real but
modest benefit, and it is smaller than the earlier draft of this note claimed.

Why not now, in one line each:

- the fact vocabulary must be closed before it is published, and section 1
  shows we do not yet agree what the facts are;
- eighteen profile versions in thirty days says the workroom's own workflow is
  not stable enough to be a versioned artifact; a declarative layer over a
  moving semantics is a second thing to move;
- a declared workflow changes the projection contract, so the versioning
  problem moves rather than disappears;
- authority must stay non-declarable (section 4), so the layer covers
  commitment closure only, which is Option B's scope. Option C is Option B
  plus a publication surface.

Option C is the right destination. It is the wrong next step.

---

## 4. How third parties could define different closure policies

The condition asked how third parties could define different workflows. The
honest answer, argued in Option C, is that they cannot and should not: what
they can define is **status labelling**. The constraint is stated once and
everything follows from it: a third party may declare **what a host class is
called, and how to cut a non-terminal class more finely**, and may never
declare **what the facts mean, which classes are terminal, which rows a merge
must protect, or who is allowed to act**.

### What stays fixed

These are not negotiable and no declaration may touch them:

- the five verdicts and their meanings;
- roster membership, roles, and the satisfier rules
  (`fold.go:679`, `:1011-1047`);
- one promise per request, one closure per commitment
  (`fold.go:793-842`);
- signature and canonical-payload checking (`schema.go:143-145`);
- retirement, staleness, and merge-receipt authority
  (`fold.go:1341-1760`, `:2174-2760`);
- the reserved kinds (`kinds.go:294`);
- the four immutable host facts per commitment row set out in Option C:
  semantic lifecycle class, terminality, merge protection, and accountable
  party. These are not merely non-declarable, they are unreachable from the
  declaration grammar. The unsettled sets at `internal/workroom/fold.go:1762`
  and `internal/mergeplan/mergeplan.go:830` read the host class and never a
  declared label.

A declaration that could weaken any of these is refused at declaration time,
not at evaluation time. That is the same posture `validateDefinition`
(`kinds.go:296-382`) already takes: the whole grammar is checked when the
definition lands, so a name no actor could ever satisfy never enters the
catalog.

### What a declaration may contain

Only data, from a closed set:

- a display label per host class, matching the existing identifier grammar
  (`kinds.go:288`);
- zero or more partitions of a **non-terminal** host class into finer
  non-terminal labels, each part discriminated by a conjunction over the same
  fixed fact vocabulary Option B defines, and checked at declaration time for
  completeness and non-overlap within the parent class.

There is no terminal flag and no waiting-party selector in the grammar. Both
were in an earlier draft of this note and both are removed: terminality and
the accountable party are host facts, and a declaration that could set them
could decide whether a candidate artifact is protected at merge. Parts inherit
terminality, merge protection and accountable party from the class they
partition, and the declaration cannot name those fields at all.

The load-time validator therefore checks four things: every host class has
exactly one label; every partition names a non-terminal parent; the parts of a
partition are complete and mutually exclusive over the fact space; and no part
introduces a fact outside the closed vocabulary. A declaration that passes
cannot express a closure or protection change, because the grammar has no
term for one.

No expressions. No user functions. No regular expressions beyond the existing
`matches` operator, which is already compiled and validated at declaration
(`kinds.go:328-330`). No arithmetic. No iteration count that is not bounded by
the record's own basis list.

### Why not executable policy

Three reasons, in order of weight.

1. **Determinism is the product.** The projection cache, the merge gate, and
   every reader's ability to re-derive a verdict all rest on same log giving
   same bytes. The gitseq JSONata spike measured this exact hazard and
   documented it: six expressions in the pinned Go JSONata port return
   different orderings across repeated evaluations because the port ranges
   over a Go map (`spike/jsonataddl/CORPUS.md`). The spike's answer was to
   narrow the language until the nondeterminism was unreachable. A closed fact
   vocabulary starts where that narrowing ends.

2. **Totality without a clock.** The same corpus note records that
   "deterministic evaluation-step and allocation bounds, and a complete
   safe-number contract, remain production blockers". A wall-clock timeout is
   not a semantic bound: a fold that times out on one machine and not another
   has two meanings. A guard table over named booleans is total by
   construction and needs no bound at all.

3. **Authority cannot be delegated to data written by the party it governs.**
   A workroom's own participants would be declaring the rules that decide what
   their acts mean. Keeping authority in the fold and lifecycle in the
   declaration is the line that makes the delegation safe.

### Migration and identity

A declared workflow must participate in fold identity. The mechanism to copy is
Tailapps' `DialectComponent`, which hashes the full canonical serialization of
every semantic field so that "changing any field changes the identity digest
whether or not anyone remembers to bump the version". Applied here: the
active workflow declaration's canonical bytes become a component of the
projection cache key alongside `ProfileVersion`. That removes the single
largest human error in the current scheme, which is forgetting to advance
`@18`.

---

## 5. Differential replay and property testing

Nothing in section 3 may land without this, and it is worth building even if
nothing in section 3 lands.

### 5.1 Differential replay over the real log

The workroom's own sequence at e2c29034 holds 17,123 commits
(`git rev-list --count refs/seq/5d26...`). That is the corpus.

One structural fact makes a whole-log replay ordinary. **There is no
projection checkpoint.** The kernel checkpoint caches verified event material
and nothing else, and it deliberately carries no profile: the `Profile` field
on the checkpoint record exists only to decode `checkpoint@1` and `@2`, and
current checkpoints never write it
(`internal/kernel/checkpoint.go:122-124`). The fold is replayed over the
verified events on every cold start, so replaying it twice with two folds is
not a new mode of operation.

**Replaying is ordinary. Rendering at every prefix is not, and an earlier
draft of this note wrongly called it cheap.** The final projection renders to
40,300,446 bytes and one render takes about 0.37 s locally. Render cost is
roughly linear in projection size and the projection grows with the prefix, so
the mean prefix costs about half the final one. Every-prefix replay is
therefore quadratic in the log:

| Quantity | Every prefix, all 17,123 | Arithmetic |
|---|---:|---|
| renders | 17,123 | one per prefix |
| serialized bytes | about 345 GB | 17,123 x 20.2 MB mean |
| wall time, one pass | about 53 min | 17,123 x 0.185 s |
| wall time, three passes | about 2.6 h | oracle, candidate, pre-change |

That is not a gate anyone will run, so it is not a gate. Section 5.1 below
replaces it with a bounded scheme and states the ceilings.

**No cheaper canonical hash exists in the tree, and it is worth being exact
about why.** `perflane.CorrectnessDigest` (`internal/perflane/digest.go:48`)
marshals the value, decodes it to `any`, and marshals again before hashing, so
it is strictly more work than one `RenderJSON`. `perfscenario.snapshotDigest`
(`internal/perfscenario/run.go:791`) is one `json.Marshal` plus SHA-256, which
is cheaper than `MarshalIndent` by the indentation bytes but still serializes
the whole projection on every call. Neither changes the exponent. A digest
that is genuinely sub-quadratic would have to be incremental over rows, and
`project()` (`fold.go:2796`) rebuilds every row from scratch and reports no
dirty set, so an incremental digest needs the fold to say what changed. That
is new machinery, it is not S0's job, and this note does not propose it.

**The oracle problem, and the answer.** At S0 there is only one
implementation, so comparing the current reducer with itself gates nothing.
The oracle has to be created before the candidate exists, and it has to be
something a later change cannot silently move. Three pieces, in this order:

1. **A `Projector` seam.** One interface in `internal/workroom`, satisfied
   today by the existing fold with no behaviour change:

   ```go
   type Projector interface {
       Fold(records []Record) Projection
   }
   ```

   `Fold` (`fold.go:403`) already has exactly this signature, so the seam
   costs a type declaration. Every candidate in S1 through S3 implements it,
   and the harness takes two `Projector` values and a record slice. That is
   how a later candidate plugs in.

2. **A bounded sampled oracle, committed at S0.** Render with `RenderJSON`
   (`render.go:11`, already canonical) at a fixed sample of prefix indices:
   every 256th, matching the kernel's checkpoint cadence
   (`internal/kernel/checkpoint.go:34`), plus the final index. At 17,123
   events that is 66 multiples of 256 up to 16,896, plus the final, so
   **K = 67 renders**.

   | Quantity | Ceiling | Expected |
   |---|---:|---:|
   | renders per pass | 67 | 67 |
   | wall time per pass | 24.8 s (67 x 0.37 s) | about 12.6 s |
   | bytes serialized per pass | 2.70 GB (67 x final size) | about 1.37 GB |
   | bytes committed to Git | about 5 KB | about 5 KB |

   The ceiling assumes every sample is as large as the final projection, which
   it is not: sample sizes grow with the prefix, so the expected figures use
   the mean sample index (about 8,704, or 50.83% of the log) against the same 40,300,446-byte
   final render. Three passes stay under 90 s including localization, which is
   a gate people will actually run.

   **What is committed is the digest list, not the JSON.** 67 lines of
   `index<TAB>sha256`, about 5 KB. The 1.37 GB of rendered JSON is
   intermediate and is never stored. This keeps the oracle reviewable, which
   is the property that makes a frozen golden an oracle rather than a mirror:
   a regenerated digest file shows as a diff naming exactly which sample
   indices moved. Regeneration stays an explicit act with its own diff, the
   way `TestRegenerateGoldens` (`fold_test.go:3601`) already regenerates the
   two existing fixtures.

   **Localization on failure is bisection, not a per-prefix chain.** A sample
   mismatch at index `s` means the first divergent prefix lies in
   `(s-256, s]`. Binary search over that window costs at most
   log2(256) = **8 further renders**, under 3 s, and names the exact event.
   Total on-failure cost per pass stays under 30 s.

   **What a sampled oracle misses, stated plainly.** It misses a *transient*
   difference: one that appears after a sample and disappears before the next,
   so both bracketing samples agree and bisection never runs. The realistic
   shape is a defect in how a row projects between a supersession and its own
   supersession when both land inside one 256-event window. Sampling cannot
   see it, and no amount of committed goldens on this corpus will.

   That gap is closed somewhere else, and honestly: on the **generated
   corpora of section 5.2**, which are hundreds of events rather than 17,123,
   so every-prefix comparison there costs milliseconds and is the default. The
   generator is exactly where retire-and-restore pairs are deliberately packed
   close together, so transient differences are its home ground rather than
   the live log's. The incrementality property in section 5.3 already compares
   at every `k` on those corpora. Optionally the harness also takes a dense
   window: full every-prefix comparison over W consecutive indices of the live
   log, defaulting to the last 512, at W x 0.37 s, for a reviewer who wants to
   sweep a specific era. The division of labour is: the live log proves
   agreement on what really happened, and the generated corpora prove
   agreement at every position.

3. **The pre-change binary as a second oracle.** For any stage, the
   `Projector` from the merge-base commit can be built and run beside the
   candidate. This catches a candidate and a regenerated golden that were
   wrong together. It costs one package build, because the fold imports no
   Git, HTTP, or MCP (`schema.go:1-3`), so an old copy compiles standalone.

**The perturbation test proves the harness works, and it must land inside the
sample.** A committed test implements `Projector` as the current fold plus
exactly one deliberate defect, and asserts the harness reports a difference.
The defect is small and semantic, not structural: invert one guard, treating
`retired(promise)` as `cancelled` rather than `reneged` (the arms at
`fold.go:3251` and `:3254`).

Why that defect survives sampling: it is **persistent, not transient**. Once a
promise is retired, its row carries the wrong status in every later prefix,
because nothing un-retires it and the counter that would
(`changeRetirement`, `fold.go:2125-2156`) needs a further supersession that
the log does not contain. So the difference is present at the final index, and
the final index is always rendered. The sample catches it without depending on
where in the log the retirement fell.

One dependency has to be named rather than assumed: this argument holds only
if the live log contains at least one retired promise. I did not verify that,
because it needs a `gs` or resident read that was outside this note's scope.
So the perturbation test is written against the **generated corpus**, where a
retired promise is constructed on purpose and detection is guaranteed by
construction, and it runs against the live log as a second, reporting-only
case. A gate that depends on what history happens to contain is not a gate.

The test fails if the harness reports no difference, so a harness that has
quietly stopped comparing cannot pass. Without this the whole of S0 is
unfalsifiable.

The comparison itself:

1. read the verified records once;
2. run oracle and candidate `Projector`s over the same slice;
3. compare `RenderJSON` bytes at each of the 67 sampled prefixes;
4. on the first mismatching sample, bisect its 256-event window to name the
   exact event, then emit a structured diff keyed by prefix index, projection
   field, and event id.

Byte-for-byte is the bar. Where a change is intended to alter output, the
difference must be **catalogued**, not tolerated: a checked-in file listing
every field and every event id expected to differ, with a one-line reason
each, and the test fails if the observed difference set is not exactly that
set. An empty catalogue is the normal state.

The two existing fixtures stay and must also be byte-stable:
`internal/workroom/testdata/legacy_projection.golden.json` and
`precondition_projection.golden.json` (`fold_test.go:1162`, `:1269`).

The live log is not a substitute for generated coverage. It contains what has
happened, and most refusal branches have never happened. Section 5.2 is not
optional for that reason.

### 5.2 Generated differential

Two of the hardest bugs in the current fold were found this way and the
counterexamples are pinned in the suite
(`fold_test.go:4753`, `:4806`, both commented "Found by randomized
differential"). The generator that found them is not in the tree. That is a
gap: the tool that found two staleness-dating defects is not runnable by the
next person.

Committing it is cheap and is worth doing regardless of which option is
chosen.

**Legal sequences alone are not enough, and that is the point of the
generator.** The live log contains only acts that were admitted. Thirty-six
distinct refusal reasons exist in `fold.go` and 47 `Decision{}` constructions,
and most of those branches have never been reached by a real event. A
generator that emits only well-formed legal acts reproduces the corpus's blind
spot in a different order. So the generator emits both, in a fixed proportion,
from a seeded PRNG with the seed printed on failure.

Six adversarial families, each with a stated oracle so a failure means a
violated invariant rather than undefined behaviour:

| Family | Examples | Oracle |
|---|---|---|
| malformed payload | truncated JSON, non-canonical key order, unknown field, trailing value, duplicate key, unknown schema string | exactly one `ineffective` or `uninterpretable` decision, no panic, and the record still appears in `Decisions` and `Provenance` (`schema.go:134-146`) |
| schema-valid, semantically invalid | promise with two requests, report on two promises, report by a non-promisor, ratify not resting exactly on its target, supersede not resting first on its target, `kind-def` redefining a reserved kind | the specific refusal reason for that rule, matched exactly, so a rule that silently stops firing is caught (`fold.go:858-900`, `:990`, `:1055`, `kinds.go:300`) |
| invalid bases | basis naming an unknown id, a later id (forward reference), the record's own id, a duplicated id, a basis list at the length bound | no panic; a citation the fold cannot resolve confers nothing and `UnableToFlare` is set where applicable (`fold.go:2762`) |
| departed and unauthorized actors | act by a never-admitted key, act by a retired member, ratification by the beneficiary, operator grant ratified by a non-operator, cross-author supersession without receipt or `ratifier` | `ineffective` with the matching departure or authority reason (`fold.go:2100-2104`, `:1011-1020`, `:1091-1100`) |
| duplicate and redefinition | repeated event id, a kind redefined while a live predecessor stands, a redefinition ratified then retired then restored, a definition whose basis kind is undefined | duplicate id is `disputed` (`fold.go:468-470`); redefinition with a live predecessor is `disputed` (`fold.go:1004-1007`); the governing definition is the one with the latest live ratification (`fold.go:917-941`) |
| receipt and accounting adversaries | receipt naming an unratified approval, an approval for another head, a plan reaching outside the reviewed paths, `merge_left_live` with a dangling artifact, `merge_changed_paths` non-canonical, a receipt whose implementer is not the artifact author | the plan is empty or the entry is unverified with a reason, and in no case does the receipt mint retirement authority over a path the approval did not cite (`fold.go:1446-1524`, `:1532-1625`) |

The generator's job is not to guess the right answer. For every adversarial
case the oracle is either an exact refusal reason or an invariant from section
5.3, both of which are checkable without a second implementation. A case for
which neither can be stated does not belong in the generator, because a test
that accepts whatever happens is a test that ratifies a regression.

### 5.3 Properties

Each maps to an invariant in section 1.10.

| Property | Statement |
|---|---|
| determinism | `Fold(r)` rendered twice is byte-identical |
| incrementality | `Fold(r)` equals `NewFolder(r[:k])` then `Append` the rest, for every `k` |
| decision immutability | the decision for event `i` in `Fold(r[:n])` is the same for every `n > i` |
| retirement involution | scoped below |
| staleness monotonicity | adding a record never clears a `stale` flag except through the documented exceptions |
| closure uniqueness | no request has two rows with a non-empty `Report` for one promise |
| authority closure | no projected row grants an actor with no live membership any act |
| totality | no input record list panics, and every record gets exactly one decision |

**Retirement involution, scoped.** The loose form is false and must not be
written as a test. Superseding X and then superseding that supersession leaves
the log two events longer, so `Decisions`, `Statements`, `Acts` and
`Provenance` all differ: the two supersession acts are themselves permanent
records with their own rows, sequences, and citations. `Sequence` on every
later record differs too, because it is the record's position
(`fold.go:283`).

The property is about **retirement-dependent semantic fields only**. Let `P0`
be the projection before the supersession and `P2` the projection after both
acts. Then for every event id present in `P0`:

- `Statement.Retired`, `.Stale`, `.DescribesSupersededWorld`,
  `.WorldSupersededAt`, `.Ratified`, `.RatifiedBy` are equal in `P0` and `P2`;
- `Artifact.Retired`, `.Succeeded`, `.Stale`, `.DescribesSupersededWorld`,
  `.WorldSupersededAt`, `.LivePredecessors`, `.SuccessionUnrecorded` are equal;
- `Projection.OmittedSupersessions` is equal;
- the `ActorState` role sets and `Retired` flag are equal;
- every `Commitment` row keyed by `(Request, Promise)` present in `P0` has the
  same `Status`, `WaitingOn`, `Stale`, `Report` and `SuccessorRequest` in
  `P2`;
- the active `KindDefinition` for every kind is equal, including `RatifiedBy`.

Not asserted, and deliberately so: row counts, sequence numbers, the presence
of the two new act rows, `Provenance` contents, and anything derived from log
length. Those change, correctly.

The mechanism this pins is `changeRetirement` (`fold.go:2125-2156`), whose
counter is meant to make retirement exactly reversible, and `activeRetirements`
(`fold.go:2429`), whose dating must forget a withdrawn cause. Both have had
defects in this area, which is why the property is worth the precision.

### 5.4 Model test

Option B makes three more tests possible and together they are the strongest
part of the set. The closure table is a decision function over a finite fact
vector, so the whole input space can be enumerated. The thirteen facts listed
in Option B are one three-valued (`completion_kind`) and twelve boolean, so
the space is 3 x 2^12 = 12,288 vectors. That is small enough to enumerate in a
unit test in milliseconds, and it stays small: each further boolean fact
doubles it, so even twenty facts is under a million.

Because the model is a decision function and not a machine, the tests are
about the guard set, not about reachable paths.

| Test | Assertion | What it catches |
|---|---|---|
| **completeness** | every fact vector matches at least one row; an unmatched vector fails the test loudly rather than falling through to a default | a fact combination nobody thought about, silently answered `open` |
| **non-overlap** | every fact vector matches at most one row | two guards that both claim a case, where today only line order decides |
| **order independence** | the projection over the golden corpus is byte-identical after shuffling the table rows with a fixed seed | an evaluator that quietly reintroduced first-match |

Reachability is a separate and weaker question, and it belongs to the fact
layer rather than the table. Some fact vectors are unreachable in a real log:
`retired_promise` with `completion_kind = none` and `has_live_promise` true is
contradictory. The table must still answer them, because totality is what the
completeness test buys. A fourth, optional test may mark vectors as
unreachable and assert that the generator in 5.2 never produces one; a
violation there means either the fact set is wrong or a real log can reach a
state the author called impossible. Both are worth knowing, and neither is a
reason to leave a hole in the table.

Note what this replaces. The earlier draft of this note proposed enumerating
`(state, fact vector)` pairs and asserting reachability from `open`. That was
the transition framing, and it was wrong: there is no prior state to enumerate
against.

### 5.5 Mutation sensitivity

Every guard added under any option gets one test that goes red when the guard
is deleted. This repository already requires that, and the landing note lists
it per acceptance test. The differential harness makes it cheaper: delete the
guard, run the replay, and the catalogue is non-empty.

---

## 6. Migration, rollback, observability, bounds, and deletion budget

### Stages

| Stage | Work | Gate to the next stage |
|---|---|---|
| S0 | commit the `Projector` seam, the 67-sample digest oracle, the replay harness with bisection, the perturbation test, and the adversarial generator | the perturbation test detects its planted defect on the generated corpus; the harness is byte-clean against the committed digests; one pass stays under 30 s |
| S1 | extract the named fact set from `projectCommitments` and `latestCompletion` with no behaviour change | replay catalogue stays empty against both oracles |
| S2 | replace the two cascades with the mutually exclusive closure table and evaluator | replay catalogue stays empty; completeness, non-overlap and order-independence tests pass; no profile advance |
| S3 | land the landing obligation's statuses as table rows, under profile `@19` | catalogue lists exactly the intended differences; the 24 status sites move in one head |
| S4 | make the fact vocabulary a published contract in `docs/reference/architecture.md` | layer 5 contract updated and its artifact republished in the same head |
| S5 | only if S0 to S4 held: consider Option C | a separate design note and its own ratified proposal |

S0 through S2 change no behaviour and need no profile advance. That is the
whole point of their ordering: the risky part (S3) starts from a proven-equal
base.

### Rollback

Rollback is already solved and needs no new machinery. Each stage is a merge;
reverting it restores the previous `ProfileVersion`, every cache written under
the newer value is rejected by `internal/app/host.go:80`, and history replays.
The log is never rewritten, so no rollback can lose an event. The one thing a
rollback cannot undo is an act that was admitted under the new rules and would
be refused under the old ones. S3 is the only stage that can create such acts,
which is another reason to keep S0 to S2 behaviour-identical.

### Observability

Three things should be visible and currently are not:

1. the active fact vector for a commitment, on `gs inspect`, so a disputed row
   can be diagnosed without reading Go;
2. the table row that decided a status, by index, on the same surface;
3. a projection-build duration and record count, already partly available
   through `internal/observe`, exposed per fold so a regression is visible
   before it is reported.

### Performance bounds

What I measured: `go test ./internal/workroom/ -count=1` takes 7.15 s and the
sequence holds 17,123 events.

What the structure says, before any timing. `project()` is 261 lines with no
memoization, and it runs three times per write cycle (knot 7). The fold's cost
is paid in full at every cold start, because no projection is checkpointed.
Both facts are arguments for materializing the projection incrementally, and
both are independent of which option in section 3 is chosen. If a performance
request is filed, that is where it should start, not in the fold's rule
structure.

What I did not measure: cold fold time on the live log, and memory. Doing that
needs a resident or a `gs` call and was out of scope here. Before S2 lands,
the bound should be set by measurement, not by this note. The proposed shape:

- cold fold of the live log must not regress by more than 5% against the
  pre-change head, measured through `make perf`;
- `project()` must stay linear in record count. The current implementation is
  linear plus the staleness pass, which is linear in edges. A table evaluator
  is linear in commitments times table rows, and the table is small and
  constant, so the asymptotics do not change;
- peak resident memory must not regress by more than 5%. The interning in
  `fold.go:615-644` exists because this was a real problem; a fact vector per
  commitment is small, but the harness should confirm rather than assume.

### Deletion budget

Concrete, in lines, at e2c29034:

| Target | Lines now | After | Net |
|---|---:|---:|---:|
| `projectCommitments` (`fold.go:3168-3287`) | 120 | ~60 table rows plus evaluator | roughly flat |
| `latestCompletion` (`fold.go:3303-3357`) | 55 | ~30, as three named facts | -25 |
| `project()` commitment section | part of 261 | unchanged | 0 |
| duplicated status vocabulary in `statusview/query.go:138-141` and `cmd/gitseq-mcp/main.go:702` | 2 sites | 1 generated from the fold's own enum | -1 site |

Honest total: Option B deletes roughly 25 to 40 lines and moves about 150.
Anyone selling it as a size reduction is selling the wrong thing. The budget
that matters is different, and is checkable:

- one place where the ten (soon thirteen) statuses are listed, instead of
  three (`fold.go`, `statusview/query.go`, `cmd/gitseq-mcp/main.go`);
- one place where the conditions are expressed, as disjoint guards instead of
  two ordered cascades;
- overlap and gaps proved absent by test rather than by review.

If S2 does not achieve all three, it should be reverted.

---

## 7. Recommendation

**Do not rewrite the fold. Do build the differential harness. Then do Option B,
scoped to commitment closure only.**

In order:

1. **S0 now, unconditionally.** The randomized differential that found two
   real defects is not in the tree, and there is no preserved oracle. Nothing
   else in this note is safe without both, and both are useful even if every
   other recommendation is rejected.
2. **S1 and S2 before the landing obligation lands, if the schedule allows.**
   The landing note's I1 adds three statuses, a hold, a release, an
   inheritance walk, and a staleness exception to a cascade that is already at
   the limit of what a reader can hold. Doing it into a table with explicit
   exclusions is easier than doing it into the cascade and extracting the
   table afterwards.
3. **If the schedule does not allow it, do the landing obligation first and
   S1/S2 after.** The extraction is behaviour-preserving either way. This is a
   sequencing preference, not a blocker on I1.
4. **Do not adopt a declarative policy layer now.** Revisit at S5, with the
   fact vocabulary settled and the profile churn slowed, and revisit it under
   its accurate name: configurable commitment-status policy, not workflow.
5. **Do not depend on Tailapps or on JSONata for the workroom fold.** Reasons
   in the prior-art section.

### Rejected alternatives

**Full rewrite of `internal/workroom`.** Rejected. The 3565 lines encode a
year of judgements in a month, most of them recorded as comments explaining a
defect that was actually observed. A rewrite discards the comments and keeps
the tests, which is the wrong half. The differential harness would catch
regressions in the projection, but not in admission refusals that the live log
never exercises.

**Splitting `fold.go` into files without changing structure.** Rejected as the
primary move, though it is harmless and could accompany S1. Splitting a
decision cascade across four files makes the precedence harder to see, not
easier.

**Extracting staleness and merge into their own package.** Rejected for now.
The coupling documented at `fold.go:2174-2190` is deliberate and the comment
records that a second walker diverged three times. A package boundary would
recreate exactly that second walker unless the shared state moves too, and
moving the shared state is the rewrite that was just rejected.

**Making the merge receipt a declared kind with declared fields.** Deferred,
not rejected. It would close knot 1 and it is the single most valuable
follow-on after S2. It needs its own note because the receipt's authority
semantics are not expressible in the current constraint algebra, and extending
that algebra to express them is how declaration turns into interpretation.

**Adopting the Tailapps `jsonataddl` core.** Rejected for the workroom fold.
See below.

### Go and no-go criteria

Go on S0 if: nothing. Build it. Its own exit conditions are that the
perturbation test in section 5.1 fails when the harness is disabled and passes
when it is not, and that one full pass over the live log stays inside the
24.8 s render ceiling plus 3 s of bisection. A harness that cannot detect a
planted defect is not a gate, and one that takes an hour will not be run.

Go on S2 if all of:

- the differential replay over the live log and both goldens is byte-identical
  before and after S1, against both oracles in section 5.1;
- the completeness, non-overlap and order-independence tests in section 5.4
  all pass over the full enumerated fact space;
- the three deletion-budget items in section 6 are all achieved;
- cold fold time and resident memory are within 5% of the pre-change head.

No-go on S2, and revert, if any of:

- the table needs a guard that cannot be expressed over the named fact set;
- the guards cannot be made mutually exclusive without changing an answer,
  which means today's behaviour depends on precedence in a way nobody
  intended; that is a behaviour question and it needs its own request, not a
  refactor;
- the catalogue of intended differences is non-empty at S2, where behaviour
  was meant to be identical;
- the status count after S3 exceeds fifteen. That would mean the workflow, not
  the representation, is the problem, and no table fixes that.

Go on S5, the declarative labelling layer, only if all of:

- the profile version has been stable for sixty days;
- at least two distinct labellings are actually wanted by someone, with the
  differences written down, and each is expressible as labels and
  non-terminal partitions alone;
- the fact vocabulary has not changed in thirty days;
- the four immutable host facts are separate published projection fields, and
  `unsettledCommitmentEvents` (`internal/workroom/fold.go:1762`) and
  `unsettledCommitment` (`internal/mergeplan/mergeplan.go:830`) read the host
  class rather than any status word a declaration can influence;
- a declaration participates mechanically in the cache key, so the version
  cannot be forgotten.

No-go on S5 if the declaration grammar has any term for terminality, merge
protection, or the accountable party, whatever the validator does with it. The
two counterexamples in Option C are the acceptance test: both must be
unspellable, not merely refused.

---

## Prior art: Tailapps `jsonataddl` v0.1.3

### What is available locally

The module was not in the cache and was downloaded into a scratch module
outside this worktree, so this repository's `go.mod` is unchanged. The source
is at `$(go env GOMODCACHE)/github.com/generalbusiness-ai/tailapps@v0.1.3/jsonataddl/`.

**One framing correction the request's premise did not have.** Tailapps is not
independent prior art. It is the same GitHub organisation as gitseq, and its
`jsonataddl/README.md` states that the shared semantic contract is gitseq's own
`notes/2026-08-26-jsonata-ddl-application-interface.md`. Tailapps is a sibling
implementation of a gitseq specification. That makes it strong evidence that
the specification is implementable, and no evidence at all that the
specification is right for the workroom.

gitseq also already contains its own implementation of the same design at
`spike/jsonataddl/` (3980 lines including tests), and already depends on
`github.com/jsonata-go/jsonata` in `go.mod:8`.

### The boundary worth reusing

Three ideas from that core are worth taking, and none of them requires taking
JSONata or SQLite.

1. **Validate a plan; never execute it.** The Tailapps core returns a
   validated mutation plan and the host applies it inside its own transaction.
   The workroom fold already does something close: a merge receipt's plan is
   sealed at admission (`fold.go:1401-1409`) and read back later rather than
   recomputed. Making that pattern explicit and universal is the reusable idea.

2. **Mechanical identity.** Its `DialectComponent` hashes the canonical
   serialization of every semantic field, so identity changes whether or not
   anyone remembers to bump a version. Applied to `ProfileVersion`, that fixes
   a real hazard: eighteen manual advances in thirty days is eighteen chances
   to forget.

3. **Confinement checked at load, not at evaluation.** Its AST walk rejects
   lambdas outright and checks every call against a nineteen-function
   allowlist. The workroom equivalent already exists in `validateDefinition`
   (`kinds.go:296-382`), and section 4's closed fact vocabulary is the same
   idea one level up.

### Why reuse is not appropriate for the workroom fold

1. **The determinism evidence is in this repository and it is negative.**
   `spike/jsonataddl/CORPUS.md` documents six JSONata expressions whose Go
   results vary across repeated evaluation, and three environment-dependent
   ones. The spike's response was to refuse them at profile load. It then
   states plainly that "deterministic evaluation-step and allocation bounds,
   and a complete safe-number contract, remain production blockers". The
   workroom fold's whole value is that same log gives same bytes. Buying an
   evaluator that has open determinism blockers to get that property is
   backwards.

2. **The size comparison does not say what it looks like.** The Tailapps core
   is about 2900 non-test Go lines against `fold.go`'s 3565. But the core is a
   general engine, and gitseq would still have to write its dialect, host
   adapter, orchestration, frontier semantics, and every workroom rule as
   declarations. The 3565 lines are not the engine; they are the program. The
   comparison would only be fair against a workroom expressed in that DDL, and
   nobody has written one.

3. **It solves the wrong problem.** The workroom fold's difficulty is not
   expressing rules. It is that merge, succession, and staleness are coupled to
   each other and to authority. A declarative surface over the same coupling is
   the same coupling in a language with fewer types.

4. **Every layer-5 dependency is a compatibility axis.**
   `docs/reference/architecture.md` names six axes. Adding an interpreter adds
   a seventh that has to be negotiated forever, in exchange for a benefit that
   Option B delivers without it.

5. **It is not yet stable enough to depend on.** The extraction note's open
   question about where the library lives is still open, and its own answer
   says a dedicated module is the end state "when a second host adopts the
   core". Being that second host is a commitment, not a reuse.

**Not rejected forever.** The right time to revisit is S5, and the right
question then is narrower than "adopt the core": it is whether the workflow
declaration format of section 4 should be expressed in that DDL rather than in
a workroom-specific one.

---

## How to verify this note

Run all of these from the worktree at commit e2c29034.

```sh
# line, function, and branch counts (section 2)
for f in internal/workroom/fold.go internal/workroom/kinds.go \
         internal/workroom/schema.go internal/workroom/render.go \
         internal/workroom/admission_profile.go internal/workroom/dead_bases.go \
         internal/mergeplan/mergeplan.go internal/statusview/view.go \
         internal/statusview/query.go internal/statusview/actor.go \
         internal/app/app.go internal/app/admission.go; do
  printf "%-45s lines=%-6s funcs=%-4s branches=%-5s\n" "$f" \
    "$(wc -l < $f | tr -d ' ')" "$(grep -c '^func ' $f)" \
    "$(grep -cE '\b(if|switch|case|for)\b' $f)"
done

# the longest functions in fold.go
awk '/^func /{name=$0; start=NR} /^}/{if(start){print (NR-start+1)"\t"start"-"NR"\t"name; start=0}}' \
  internal/workroom/fold.go | sort -rn | head

# struct field counts
sed -n '285,317p' internal/workroom/fold.go | grep -cE '^\s+[a-z][A-Za-z]* '   # parsedRecord = 18
sed -n '359,387p' internal/workroom/fold.go | grep -cE '^\s+[a-z][A-Za-z]* '   # foldState = 17
sed -n '261,278p' internal/workroom/fold.go | grep -cE '^\s+[A-Z]'             # Projection = 10
sed -n '38,111p'  internal/workroom/fold.go | grep -cE '^\s+[A-Z][A-Za-z]* '   # Statement = 16
sed -n '126,177p' internal/workroom/fold.go | grep -cE '^\s+[A-Z][A-Za-z]* '   # Artifact = 12

# refusal reasons and decisions
grep -o 'Reason: *"[^"]*"' internal/workroom/fold.go | sort -u | wc -l         # 36
grep -c 'Decision{' internal/workroom/fold.go                                  # 47

# commitment statuses the fold emits
grep -on 'Status = "[a-z-]*"\|Status: "[a-z-]*"' internal/workroom/fold.go | sort -t'"' -k2 -u

# statuses the query layer accepts, and the MCP enum
sed -n '138,141p' internal/statusview/query.go
sed -n '702p' cmd/gitseq-mcp/main.go

# tests
cat internal/workroom/*_test.go | grep -c '^func Test'                         # 200
go test ./internal/workroom/ -count=1                                          # 7.15s here

# body fields the fold reads, and which are undeclared (knot 1)
grep -o 'Body\["[a-z_]*"\]' internal/workroom/fold.go | sed 's/Body\["//;s/"\]//' | sort -u | wc -l
comm -23 \
  <(grep -o 'Body\["[a-z_]*"\]' internal/workroom/fold.go | sed 's/Body\["//;s/"\]//' | sort -u) \
  <(grep -o '"[a-z_]*"' internal/workroom/kinds.go | tr -d '"' | sort -u)
# prints 11; "artifact" is a twelfth, missed because the same word is a kind name

# four path-covering implementations, three spellings of the class words (knot 6)
grep -n 'func pathCovers\|func closestCoveringPath' internal/workroom/fold.go
grep -n 'func artifactCoversPath\|func widerPath' internal/mergeplan/mergeplan.go
grep -rn '"carried"\|"sibling"\|"abandoned"' --include='*.go' internal/ | grep -v _test

# project() call sites (knot 7)
grep -rn '\.Projection()' --include='*.go' internal/ cmd/ | grep -v _test

# the kernel checkpoint carries no projection and no profile (section 5)
sed -n '117,131p' internal/kernel/checkpoint.go

# the three hard-coded unsettled/actionable sets, two of which gate merge (option C)
sed -n '/func (f \*foldState) unsettledCommitmentEvents/,/^}/p' internal/workroom/fold.go
sed -n '/func unsettledCommitment/,/^}/p' internal/mergeplan/mergeplan.go
sed -n '125p' internal/statusview/view.go
grep -n 'unsettledCommitmentEvents' internal/workroom/fold.go      # 1552, 1762, 2887
grep -n 'unsettledCommitment(' internal/mergeplan/mergeplan.go     # 782, 830

# no cheaper canonical hash exists (section 5.1)
cat internal/perflane/digest.go                                    # marshal, decode, marshal
sed -n '791,797p' internal/perfscenario/run.go                     # marshal + sha256, still whole-projection
grep -n 'checkpointInterval' internal/kernel/checkpoint.go         # 34: = 256

# sample arithmetic: 66 multiples of 256 below 17123, plus the final index
python3 -c 'n=17123; k=n//256; print(k, k*256, k+1)'               # 66 16896 67

# the second cascade (option B)
sed -n '1183,1322p' internal/mergeplan/mergeplan.go | grep -c 'return fail('
grep -c 'Code:' internal/mergeplan/mergeplan.go

# the status word's blast radius (knot 3)
grep -rln 'awaiting-merge' . --exclude-dir=.git | sed 's|^\./||' \
  | grep -v '^notes/' | grep -v 'internal/service/uidist/' | sort | wc -l      # 24

# growth and profile churn
git log --oneline -- internal/workroom/fold.go | wc -l                         # 58
git show 8b112bb7e:internal/workroom/fold.go | wc -l                           # 755, dated 2026-08-08
git log -G'ProfileVersion.*workroom-fold@' --oneline \
  -- internal/workroom/schema.go internal/workroom/fold.go | wc -l             # 20
grep -roh 'workroom-fold@[0-9]*' docs/ SKILL.md internal/ | sort -u -t@ -k2 -n

# corpus size for the differential (section 5)
git rev-list --count refs/seq/5d2622748872b7e2dec3fe5c59e4be73a35e0bc8         # 17123

# the pinned counterexamples whose generator is not in the tree
grep -n 'randomized differential' internal/workroom/fold_test.go

# the JSONata determinism evidence (prior art)
sed -n '1,50p' spike/jsonataddl/CORPUS.md
grep -n 'jsonata' go.mod

# Tailapps, downloaded into a scratch module, not into this go.mod
ls "$(go env GOMODCACHE)/github.com/generalbusiness-ai/tailapps@v0.1.3/jsonataddl/"
```

### Limits of this note

- The complexity shares in section 2 ("where the complexity comes from") are my
  estimate from reading the file, not a measurement. The measured claims around
  them are the function sizes and the theme counts.
- The branch-word counts use `grep`, which counts the words `if`, `for`,
  `switch`, and `case` inside comments and strings. They are comparative only.
- No cold-fold timing or memory figure appears here, because taking one needs a
  resident or `gs` call that was out of scope. Section 6 makes measuring it a
  gate rather than asserting a number.
- The line-count effect of Option B is an estimate. The three deletion-budget
  items that are not estimates are counted sites, and those are the go and
  no-go criteria.
- I did not read all 4932 lines of `fold_test.go`. The 200 test-function count
  is exact; my reading of what they cover is not exhaustive.
- The invariants in section 1.10 are readings, not proofs. That is what section
  5 is for.
- The two cost figures section 5.1 is built on, a 40,300,446-byte final
  projection and about 0.37 s per render, are planner's local measurements
  reported in review 83ae88ef, not mine. Everything derived from them, the
  345 GB, the 53 minutes, the 24.8 s ceiling and the 2.70 GB ceiling, is
  arithmetic over those two numbers and moves with them. Re-measure before
  treating any ceiling as a contract, and note that render cost is assumed
  linear in projection size, which I did not verify.
- Whether the live log contains a retired promise is unverified, so the
  perturbation test is specified against the generated corpus rather than
  against history. Section 5.1 says so where it matters.
