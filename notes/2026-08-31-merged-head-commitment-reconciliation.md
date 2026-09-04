---
date: 2026-09-04, revived from the approved but unlanded head of 2026-08-31
status: candidate design; no implementation is authorized by this note
origin: request e1bede35, which asks for a current note reconciled with the
  target-aware landing model. The 2026-08-31 head
  30768e3f5aabaee53074e26723f8e3644ef0d1f9, its artifact #16110, and its
  approval #16140 are reviewed evidence only.
bases: main 860ee61a, and the adopted target-aware landing model in
  notes/2026-09-04-landing-obligation.md
does not: implement anything, begin the audit's recovery recuts, or reopen the
  landing model, whose sections it treats as settled. It proposes one merge
  flag, one receipt field, one fold clause, and the protocol that makes them
  single-use and resumable. Each needs its own request resting on this note's
  adoption.
---

# Reconcile implementation commitments already incorporated into their target

Git reachability and Workroom satisfaction answer different questions.
Reachability says that Git can find one commit from another. Satisfaction says
that the requested work was reported by its performer and accepted through the
request's authority path. The board loses the first fact and must not invent
the second.

Add a target-aware reconciliation view that finds unresolved commitments whose
approved head is already present in the request's own target ref. Qualify the
existing commitment rows immediately after a merge and in normal status reads.
Then give those rows a terminal that the target-aware landing model actually
admits, because under that model an explicit report no longer closes a landing
request and `gs merge` refuses a candidate the target already contains. Without
that terminal the rows are permanently undrainable.

This design adds no new commitment status beyond the three the landing note
already defines, no new durable kind, and no automatic artifact retirement.

## What changed since the 2026-08-31 head

The 2026-08-31 head was written before the landing model existed. Nine
substantive things are different.

1. **The reference ref is per request, not global.** The old note read one
   configured integration ref, defaulting to `refs/heads/main`. The landing
   note makes the destination durable as `target_repo`, `target_ref`,
   `target_head` on the request itself (landing note section 1). The detector
   now reads each row's own target. There is no global default and no
   configuration setting to add.
2. **`awaiting-merge` is not the candidate set.** The landing note section 4
   splits it into `awaiting-review`, `awaiting-authorization`, and
   `awaiting-landing`, with the old word kept as no alias. The candidate set is
   restated in those words.
3. **The already-landed report path is closed for landing requests.** The old
   note's central drain was a performer report plus requester ratification. The
   landing note section 3 makes a plain explicit report on a landing request
   ineffective with "request owes a landing to `<target_ref>`; land it or
   supersede it", and makes a `resolution` report nonterminal evidence that can
   never be ratified into `satisfied`. That path now survives only for
   `no_git_artifact=true` requests and for legacy records that read as
   no-artifact ones (landing note section 9).
4. **The gap that closure creates is named and given a terminal.** See "The gap
   the landing note leaves open" and "The incorporation receipt protocol"
   below. This is the largest addition.
5. **The write convenience moved.** The old note proposed
   `gs reconcile-merged --request <event> --report`, a second write command
   composing an ordinary report. That report is now inadmissible. The write
   becomes a flag on the one command that already signs receipts, and
   `gs reconcile-merged` is left as a read view only.
6. **The visibility surface already exists.** The old note asked for a new
   `incorporated` qualifier on rows. The landing note section 8 already defines
   `approved_not_landed`, a `gs work` lane, a `/v0/work-query` filter, and
   `/v0/worktrees` fields. The qualifier shrinks to two fields inside those
   rows rather than a surface of its own.
7. **`landed-then-removed` and `target_gone` are not this note's to invent.**
   The landing note section 6 already covers a target ref reset behind a
   receipt and a target ref deleted or renamed. The old note had no vocabulary
   for either.
8. **The thread-actions wording repair is withdrawn.** The old note said the
   `fe36ff06` `awaiting-merge` prose needed the clause "or report if the head
   already landed", carried by the thread-actions lane. That clause would now
   describe an inadmissible act. The landed thread-actions note declines a
   temporary interpretation of `awaiting-merge` and defers to the landing
   note's I5 (`notes/2026-08-22-thread-actions.md:144-150`). Nothing in this
   note asks for UI wording before I5.
9. **Rollout depends on the landing model.** The old note's three steps stood
   alone. They now sit behind I1 and I4.

Everything else from the 2026-08-31 head is carried unchanged in substance: the
non-inference guardrail, the full-object-ID rule, the bounded post-merge
warning, the precedence analysis, the refusal of the three unsafe candidates,
and the layer split.

## The question the fold does not answer

The fold reads no repository (`docs/reference/architecture.md`, layer 5). It
therefore cannot know that an approved head is already reachable from the ref
its request named. Nothing joins the two facts, so a row can sit in
`awaiting-landing` while its content has been in the target for weeks. The
audit that produced the landing note counted fifty approved heads that never
reached `main`; the reverse population, heads that reached it without a
receipt, is the one this note addresses.

The join is what is absent, not the ability to compute it. `Landings` answers
reachability per commit (`internal/gitstore/landing.go:58`), bounded at sixteen
per batch (`:52`); the resident serves it at `POST /v0/landed`
(`internal/service/server.go:121`); the browser thread marks landed heads from
it (`ui/src/lib/spine.ts:195`, `ui/src/components/Thread.tsx:97`); and the
merge preflight already tests containment
(`internal/mergeplan/mergeplan.go:1256`).

## Decision

Three earlier candidates are not safe or sufficient as stated.

1. **Do not let a merge receipt close every commitment on an ancestor.** A
   receipt is signed by the implementer whose exact head was approved
   (`internal/workroom/fold.go:1477`). The fold bounds that receipt's
   cross-author authority to the reviewed artifact tree
   (`internal/workroom/fold.go:1446-1524`). Letting the signer name arbitrary
   ancestor commitments or retire artifacts at unchanged paths would turn one
   approval into authority over unrelated work. The fold also has no repository
   from which to check the claimed ancestry.
2. **Do not project `satisfied` from reachability alone.** A promise's `head`
   field is an advisory checkout hint, and the landing note says the same of
   `target_head`: "advisory, never a proof" (section 1). Even a reporting
   artifact proves only the commit and paths its performer named. An ancestor
   may be partial work, may have entered the ref through a different decision,
   or may fail conditions that are not reducible to file bytes. A
   byte-identical recut has the same limit. Satisfaction stays a signed
   authority decision.
3. **Do not rely on a publication discipline alone.** Avoiding artifacts at
   unchanged paths reduces noise but neither exposes nor closes an existing
   promise.

Choose the fourth shape: **machine-detected, authority-preserving
reconciliation**. Repository reachability produces advisory evidence. A signed
act turns that evidence into a closure. Rows become visible automatically; they
do not become satisfied automatically.

## What the detector says

For each candidate row the detector reads that row's own `target_repo` and
`target_ref` from the fold projection, peels the ref with
`git rev-parse --verify <ref>^{commit}`, and compares. Only the resulting full
object ID is reported or signed. Abbreviated hashes, unpeeled tags, symbolic
names, and working-tree contents are not evidence. Output always carries both
`target_ref` and the resolved head, so a later ref move cannot silently change
what an earlier result claimed. A legacy row reads `refs/heads/main` of the
workroom's own repository and carries the `legacy` flag, exactly as the landing
note section 9 prescribes; the detector adds no second legacy rule of its own.

Candidate statuses are `promised`, `stale`, `reported`, `awaiting-review`,
`awaiting-authorization`, and `awaiting-landing`. Terminal rows are excluded.
Each candidate carries one evidence class.

| Evidence | Meaning | May it close the row? |
| --- | --- | --- |
| `reported-head-reachable` | A live or stale reporting artifact names commit `H`, and `H` is reachable from the resolved target head. | No. It is the precondition for the incorporation receipt below, not the receipt. |
| `head-hint-reachable` | A promise or explicit report body carries advisory head `H`, `H` is reachable from the resolved target head, and no reporting artifact proves it. | No. It is a prompt to investigate. |

The detector does not parse request conditions, decide that two requests are
the same, or read commit messages for identifiers. It never reads a target from
prose; the landing note section 1 already forbids the fold to do that, and
layer 7 gains no licence the fold lacks.

## The gap the landing note leaves open

Take a landing request whose reporting artifact is approved and whose approved
head is already reachable from `target_ref`, with no sealed receipt naming it.
Three doors are shut at once.

- The report door: a plain report is ineffective, and a `resolution` report is
  nonterminal evidence that cannot be ratified into `satisfied` (landing note
  section 3).
- The merge door: `gs merge` refuses with "approved candidate is already
  contained in the target" (`internal/mergeplan/mergeplan.go:1256-1257`,
  tabulated at `docs/reference/gs/merge.md:137`). There is no new landing to
  record, so there is no receipt.
- The supersession door: carried means a successor request takes the head
  forward, and abandoned means the approved head was deliberately dropped
  (landing note section 7). Neither is true here, and recording either would be
  a false statement in the log.

The row therefore stays `awaiting-landing`, and `approved_not_landed` stays
true forever, because that fact is false only when a sealed receipt names the
artifact with a matching `merge_target_repo` and `merge_target_ref` (landing
note section 8). Detection alone would add a permanent unclearable warning to
every such row. That is worse than silence.

Open one door, and only one. `gs merge` gains `--incorporated`. With that flag
the already-contained check inverts: the candidate must be reachable from the
target head. Every other check is unchanged. The implementer still signs. The
approval must still be ratified, independent, and exact. A held request must
still carry an effective release from its hold owner. The checkout's ref must
still be the request's `target_ref`. The target ref is never advanced, and no
artifact is published or retired.

The receipt records that fact and nothing more, under one new field
`merge_incorporation=prior`. Without that field the receipt would claim a
landing event that did not happen at that moment, and the log would be wrong
about its own history. With it the receipt says exactly what is true: this
approved candidate is contained in this ref at this head, observed and signed
by the implementer under a ratified approval.

The guardrail is intact. The fold still infers nothing. It reads a signed
receipt, as it does today, and `satisfied` still stands on the receipt alone
(landing note section 6). Reachability is a layer-7 precondition the CLI checks
before it lets the implementer sign, with the same
`git merge-base --is-ancestor` call the preflight already makes.

The cost, stated plainly: this weakens one refusal that is currently absolute,
and a reviewer should test whether the remaining checks are enough. The
strongest of them is that only the implementer may sign, and only a ratified
independent approval of that exact head admits the receipt, which is the
authority a normal landing needs. The next section states the protocol that
makes the flag single-use and resumable, and the one fold rule that lets the
receipt close its commitment without borrowing merge succession authority.

## The incorporation receipt protocol

A merge today is a two-phase transaction: a Git commit carrying the receipt
trailers, then a durable append recording the succession. The process can die
between them, so recovery reads the Git side and finishes the durable side. An
incorporation must be equally resumable and must spend its approval exactly
once. Skipping the Git object would strand both properties, because a
reservation ref left at the ordinary target commit carries no candidate,
approval, or incorporation metadata.

**The Git object.** Under the same `.merge.lock` (`cmd/gs/main.go:53`, `:662`),
build one new commit with `git commit-tree`, never with `git merge` and never
with `git commit`:

| part | value |
| --- | --- |
| tree | the observed target head's tree, byte for byte, so the object adds nothing |
| first parent | the observed target head |
| second parent | the approved candidate |
| message | `--text`, then the same `Gitseq-*` trailers an ordinary receipt carries, plus `Gitseq-Incorporation: prior` |

Two parents in that order are not decoration. `ReadReceipt` accepts a receipt
commit only when it has exactly two parents, the first equal to
`Gitseq-Target-Pre-Head` and the second equal to `Gitseq-Candidate`
(`internal/mergeplan/mergeplan.go:949-956`). Using the same shape means the
existing reader, and therefore the existing recovery path, needs no new parser.
`Gitseq-Retirements` and `Gitseq-Successors` must be present and non-empty
strings for the same reader, so they carry their empty encodings, `{}` and
`[]`, along with an empty `Gitseq-Changed-Paths` and an empty
`Gitseq-Left-Live`.

`git commit-tree` touches neither `HEAD`, the index, nor the working tree. The
commit is reachable only from the approval's receipt ref
(`refs/gitseq/merge-receipts/<sha256 of approval>`,
`internal/mergeplan/mergeplan.go:905-907`). It is auditable, it is off the target
ref, and nothing ever fast-forwards the target onto it.

**Single use.** One reference transaction, straight from absent to the receipt
commit, fed to `git update-ref --stdin`:

```
start
verify refs/heads/<target> <target_pre_head>
create refs/gitseq/merge-receipts/<key> <receipt-commit>
prepare
commit
```

`create` requires the ref not to exist, so exactly one process wins and every
later one gets the existing "approval is already reserved or used by another
merge" refusal. That is the same guarantee the merge gets from the empty old
value at `cmd/gs/main.go:775`; the difference is the new value and the added
`verify`. An incorporation makes no intermediate reservation and needs no
second advance, so it never runs the merge's advance step (`:843`).

`verify` with an old value and no new value asserts the ref's current value
without changing it. Git applies every command in one transaction: all of them
succeed or none does, and `prepare` locks each named ref before `commit`
applies them. So a target ref that moves between the step 3 remeasure and this
transaction aborts the whole thing, and no receipt ref is created. Without the
`verify` line the receipt could seal a `Gitseq-Target-Pre-Head` that was
already stale at its own commit point, because Git does not honour the
`.merge.lock` and any other process may move the branch.

The transaction verifies the target ref; it never writes it. The target stays
exactly where it was.

The merge reserves first because it must hold the approval across a `git merge`
that mutates the working tree, and it cleans up on failure with a deferred
delete (`cmd/gs/main.go:780-786`). An incorporation mutates nothing before the
transaction, so it has nothing to hold and nothing to clean up. Copying the
merge's reserve-then-advance here would create a state the merge cannot
produce and this design cannot recover. A hard kill between the reservation and
`commit-tree` would park the ref on an ordinary target commit carrying no
receipt trailers. The deferred delete does not run after a hard kill, no
receipt object exists for `git log --all` to find, and a retry cannot re-plan
because the empty-old-value create now sees an existing ref. The approval would
be stranded with no act able to spend or release it. One unreferenced object
then one reference transaction is both simpler and strictly more recoverable.

**Ordering.** Five steps, in this order.

1. Run the existing durable admission preflight on the prospective acts
   (`cmd/gs/main.go:822-824`). Nothing durable and nothing referenced exists
   yet, so an act that cannot cross the admission boundary fails while there is
   nothing to resume.
2. Write the off-target receipt object with `git commit-tree`. It is
   unreferenced at this point.
3. Remeasure. Re-resolve the target ref and re-read the workroom frontier, and
   restart from step 1 if either moved, exactly as the merge refuses a moved
   frontier at `cmd/gs/main.go:806-809`. The object written in step 2 becomes
   garbage and is ignored.
4. Run the reference transaction above. This is the sole commit point: it
   proves the target ref still holds `target_pre_head` and creates the receipt
   ref, spending the approval and making the object reachable, all or nothing.
5. Append the durable receipt.

**What step 3 does and does not buy.** The two remeasured facts have different
guarantees, and the note must not overstate either.

The target ref is fully covered. Step 3 detects a move early and step 4's
`verify` proves the value again inside the transaction, so a move at any point
before the commit point aborts rather than seals a stale
`Gitseq-Target-Pre-Head`.

The workroom frontier is not, and cannot be. `preflightBatchAdmission`
(`cmd/gs/main.go:1576-1626`) checks batch shape and the kernel and resident
byte ceilings without appending anything, so it is a shape and size check, not
a reservation. The snapshot recheck (`:806-809`) detects frontier movement
before the Git transaction, but it is not atomic with that transaction and not
atomic with the later durable append either. Git's reference transaction covers
Git refs only; it says nothing about the workroom log. Durable admission stays
authoritative after the Git commit point: if the frontier moved in the window,
the append is judged on its own terms and may be refused, and the outcome is a
receipt ref pointing at an object with no durable receipt, which is exactly the
resumable state row three of the crash table describes. Nothing here makes the
Git ref update atomic with the workroom frontier, and no part of this design
depends on that.

**Resume.** `existingGitMergeReceipt` finds a prior receipt commit with
`git log --all --grep=Gitseq-Approval: <approval>`
(`cmd/gs/main.go:955-968`). `--all` reaches `refs/gitseq/`, so a receipt commit
made reachable by step 4 is found exactly like an on-target one, and an object
from a crash before step 4 is unreachable and correctly invisible. Five cases:

| crash point | ref state | object state | retry |
| --- | --- | --- | --- |
| any point before the step 4 transaction | absent | absent, or written but unreferenced and collectable by `git gc` | re-plan from step 1; the orphan is ignorable and needs no cleanup |
| the step 4 transaction aborts, because the target moved or the receipt ref exists | absent; the transaction never writes the target ref, which stays at the value held when the transaction began, possibly the racer-written value rather than the earlier measurement | unreferenced | re-plan from step 1 against the new target head, or refuse if another spender holds the receipt ref |
| after the transaction, before the durable append | at the receipt commit | reachable | resume the durable append from the trailers on that commit |
| after the durable append | at the receipt commit | reachable | refuse as already used, naming the receipt |
| competing spender, any time | held by the winner | the loser's object unreferenced | the loser's `create` fails, the whole transaction aborts, and it writes nothing durable |

**What retry validates.** Retry reads the commit at the receipt ref through
`ReadReceipt`, which already requires `Gitseq-Approval`, `Gitseq-Candidate`,
`Gitseq-Target-Pre-Head`, `Gitseq-Retirements`, and `Gitseq-Successors` to be
present and non-empty, and requires exactly two parents whose first equals
`Gitseq-Target-Pre-Head` and whose second equals `Gitseq-Candidate`
(`internal/mergeplan/mergeplan.go:954-955`). On top of that it checks, and
refuses on any mismatch:

| trailer | checked against |
| --- | --- |
| `Gitseq-Approval` | `--approval`, as the merge already does at `cmd/gs/main.go:963-966` |
| `Gitseq-Candidate` | `--candidate`, giving the existing "approval was already used for candidate" refusal (`cmd/gs/main.go:698-700`) |
| `Gitseq-Authorization` and `Gitseq-Authorization-Ratification` | `--authorization`, present together or neither, as at `cmd/gs/main.go:701-712` |
| `Gitseq-Target-Pre-Head` | the target ref's current value, and the candidate must still be contained in it. Step 4's `verify` guarantees this held at the commit point; a later legitimate move of the target is judged as `landed-then-removed` or ordinary movement, not as a bad receipt |
| `Gitseq-Incorporation` | must read `prior`; a `prior` retry against an ordinary receipt, or the reverse, refuses |
| `Gitseq-Retirements`, `Gitseq-Successors`, `Gitseq-Changed-Paths` | must be the empty encodings |

One check must differ from the ordinary resume. Today the resume requires the
checkout's `HEAD` to equal the receipt's `merge_head`
(`cmd/gs/main.go:722-726`), which is right when the receipt is on the target
ref and wrong here, because an incorporation never moves `HEAD`. For a `prior`
receipt the equivalent is the `Gitseq-Target-Pre-Head` row above: the checkout's
ref is still the request's `target_ref` and the candidate is still contained.

**Moved target.** If the target ref moved between planning and signing,
`target_pre_head` no longer matches and the act refuses, as every merge already
does (landing note section 6). Retry re-observes the head. The only fact
remeasured is containment: if the candidate is no longer reachable from the
target ref, for example after a reset behind it, the incorporation refuses and
the row is a `landed-then-removed` case for its requester to judge.

**The durable receipt.** Fields, exactly:

| field | value |
| --- | --- |
| `merge_approval` | the ratified approval event |
| `merge_candidate` | the approved head, full lowercase |
| `merge_target_repo`, `merge_target_ref` | from the landing note section 6 |
| `merge_target_pre_head` | the observed target head |
| `merge_head` | the receipt commit, not the target head |
| `merge_incorporation` | `prior` |
| `merge_retirements`, `merge_successors`, `merge_changed_paths` | empty, and the fold must refuse a `prior` receipt whose plan is not empty |

`merge_head` naming the receipt commit rather than the target head keeps the
log honest: the receipt points at a real object whose parents state both the
head observed and the candidate incorporated, not at a target commit that says
nothing about this approval.

**The fold association.** `mergedArtifacts` today binds a receipt to a
reporting artifact only when that artifact is in the receipt's validated
retirement plan (`internal/workroom/fold.go:3385`), after checking that the
approval rests on it (`:3384`), that its author is the receipt's signer
(`:3389`), and that its `commit` equals `merge_candidate` (`:3393`). A `prior` receipt
has an empty plan, so those rules alone would close nothing.

Add one rule, and only one: when a receipt carries `merge_incorporation=prior`
and its retirement plan is empty, the plan-membership condition is not
required, and the three identity conditions still are. The binding is by
artifact event id through the approval's own `rests_on`, never by path. The
receipt reaches no path, publishes no successor, and changes no artifact's
liveness. `validateMergeReceiptNow` keeps its reviewed-path bound
(`internal/workroom/fold.go:1446-1524`); an empty plan reaches nothing, so the
bound is satisfied vacuously rather than waived.

This answers the reviewed-path-changed-later case directly. If a path the
approval reviewed has different bytes in the target today, the incorporation
still closes the commitment, because the commitment is about the approved head
that is contained in the ref, and it publishes nothing at that path and retires
no live pointer there. The later bytes are somebody else's landing with its own
receipt and its own artifacts. Reachability never becomes succession authority.

**Tests R3 must add, each mutation-sensitive.**

1. Hard kill between `commit-tree` and the step 4 transaction: the receipt ref
   is absent afterwards, `existingGitMergeReceipt` finds nothing, and a plain
   retry re-plans from step 1 and succeeds, leaving one durable receipt.
2. Hard kill after the transaction and before the durable append: the ref is at
   the receipt commit, and retry resumes the durable append from its trailers
   and appends exactly once.
3. Retry after the durable append: refused as already used, with no second
   receipt appended and the ref unmoved.
4. Competing spender: a second process for the same approval loses the
   `create`, so its whole transaction aborts, it appends nothing durable, and
   it leaves the winner's receipt and ref untouched.
5. The intermediate reservation is forbidden. A variant that first runs
   `git update-ref <ref> <target_pre_head> ""` before `commit-tree`, and is
   then hard-killed, must fail this suite: the ref is left at an ordinary
   target commit, `existingGitMergeReceipt` finds no receipt, and the retry in
   test 1 refuses instead of succeeding. This test exists to keep
   reserve-then-advance out of the implementation.
6. Retry naming a different candidate, approval, or authorization against an
   existing `prior` receipt: refused, one refusal per row of the
   retry-validation table.
7. `Gitseq-Incorporation` mismatch both ways: a `prior` retry against an
   ordinary receipt refuses, and an ordinary retry against a `prior` receipt
   refuses.
8. Moved target seen by the remeasure: after `commit-tree` and before step 3,
   move the target ref. The remeasure restarts, the first object is left
   unreferenced, and the act succeeds against the new head while the candidate
   is still contained and refuses once it is not.
9. Moved target racing the transaction: move the target ref exactly between the
   step 3 remeasure and the step 4 transaction. The `verify` fails, the whole
   transaction aborts, the receipt ref is absent, the transaction leaves the
   target ref at the racer-written value, the `commit-tree` object stays
   unreferenced, and no durable receipt is appended.
10. Removing the `verify` line from the step 4 transaction makes test 9 go red:
    without it the receipt ref is created and a receipt seals a
    `Gitseq-Target-Pre-Head` that was already stale at its commit point.
11. Moved workroom frontier, both branches: a move the step 3 remeasure can
    see restarts from step 1 with no receipt appended against the stale
    measurement. For a move in the window after the transaction, the durable
    append evaluates against the current state: when the intervening event is
    unrelated admissible traffic the append succeeds and the receipt seals
    normally; when the intervening event invalidates the receipt acts, durable
    admission refuses, and only that refusal leaves the reachable receipt-only
    state of crash-table row three rather than a bad receipt. The test
    exercises both cases.
12. A reviewed path whose bytes changed later in the target: the commitment
    closes, no artifact is published or retired at that path, and every live
    pointer is unchanged.
13. A `prior` receipt carrying a non-empty `merge_retirements`,
    `merge_successors`, or `merge_changed_paths` is ineffective.
14. Removing the `prior` clause from `mergedArtifacts` leaves the commitment
    open, so test 12 goes red.
15. Removing the empty-plan condition from that clause lets a `prior` receipt
    with a planted plan retire an artifact, so test 13 goes red.
16. Removing the step 3 remeasure makes tests 8 and 11 go red.
17. The incorporation reference transaction never writes the target ref. On
    success the target ref remains at `target_pre_head`. On abort it remains
    at the value held when the transaction began, which may be the
    racer-written value and may differ from the earlier measurement. The
    oracle compares against the value read immediately before the transaction,
    not against the step 3 measurement.

## Where the result appears

Add two fields to the commitment row the landing note section 8 already
defines: `incorporated`, the evidence class or empty, and
`incorporated_head`, the resolved full target head the class was measured
against. No new top-level collection, no new lane, no second count.

The qualifier never changes the lane, waiting party, status, count, or action
ownership. `approved_not_landed` keeps its own definition; `incorporated`
explains why one of those rows is stuck rather than merely late.

`gs status`, MCP `status`, `gs work`, MCP `work`, `gs inspect`, and the browser
work board show the two fields on rows they already bound. The resident
recomputes when the durable frontier moves or a target ref moves. The fold and
its checkpoints stay independent of the repository.

After `gs merge` updates a target ref, it runs the same detector against the new
exact head and prints newly qualifying unresolved rows. That is a warning after
the merge, not another merge gate and not a receipt field. A warning failure
must never make a completed Git merge look as though it did not happen. The
warning prints at most `WorkPageDefault` rows
(`internal/statusview/query.go:18`) and states the omitted count, so one merge
cannot produce unbounded output.

## How a row closes

Two routes, chosen by what the request owes.

**A landing request** closes through the incorporation receipt.

1. The performer reads the request conditions and the detector's evidence.
2. If the conditions are in fact met, the performer runs
   `gs merge --incorporated` against a checkout of the request's `target_ref`,
   naming the same candidate and ratified approval an ordinary landing would
   name, plus `--text` describing what is being recorded and why.
3. The command follows the five steps of the protocol above: durable admission
   preflight; the off-target, unreferenced `commit-tree` object; the target-ref
   and workroom-frontier remeasure, restarting if either moved; the one
   reference transaction that verifies the target ref and creates the receipt
   ref, which is the sole commit point; then the durable append.
4. The fold admits the receipt under the ordinary receipt rules plus the one
   `prior` clause, and the commitment projects `satisfied`.

No requester ratification follows, because the sealed receipt is already the
requester's pre-authorized acceptance for a landing request, exactly as it is
for an ordinary landing.

**A no-artifact request**, and a legacy record that reads as one, closes as
today: an explicit report resting on the promise, or on the request when there
was no promise, then requester ratification. The report's visible signed text
states the target ref, the resolved full commit ID, and why the incorporated
implementation satisfies the conditions. It adds no `body.head`, `body.commit`,
`body.verdict`, `body.status`, or other invented field. A stale basis takes the
existing dead-basis override, which records that the basis was seen and does
not make it current.

Neither route is automatic. Only the performer can truthfully say the
conditions were met. If the performer is unavailable the row stays visible. A
requester or `ratifier` may retire the request under the ordinary rules, but
that records cancellation or withdrawal, not satisfaction, and for a request
holding an approved head the carried-or-abandoned rule applies (landing note
section 7).

## Completion precedence

The landing note section 5 gives the landing list, highest first: a sealed
merge receipt naming the completion artifact, then the newest live reporting
artifact at an approved head, then the newest live reporting artifact without
an approval. An incorporation receipt is a sealed merge receipt and takes rank
one. It does not need a new rank.

The two lists never mix. `latestCompletion`
(`internal/workroom/fold.go:3303-3357`) today lets any admitted report override
an unmerged artifact, and the fold simplification study describes that cascade
as it stands (`notes/2026-09-04-fold-simplification-study.md`, section 1.7,
"Commitment closure"). Under the landing model no report is a completion on a
landing request, so an artifact can be overtaken only by a newer artifact or
sealed by a receipt. The implementing head must test that an incorporation
receipt reaches rank one over an existing artifact, and that an earlier
completion stays immutable history rather than disappearing.

The same study calls the closure half of the fold a decision function over
final facts, not a state machine (section 1, "Machine B"). Nothing here adds a
prior-status dependency: `incorporated` is a fact about the repository at read
time and never enters the fold's fact vector.

## Existing rows and rollout

The first implementation computes the qualifier for the current projection at
once, so live historical rows with readable Git evidence appear without a
migration event. They do not close automatically.

Rows already closed on 2026-08-30 by seven ratified reports and four explicit
request retirements stay terminal history and are not rewritten. Under the
landing note section 9 those `satisfied` rows stay `satisfied`, and
`approved_not_landed` is computed over them too, flagged `legacy`. This note
reinterprets no old event.

Three bounded steps, each its own request.

| step | scope | depends on |
| --- | --- | --- |
| R1 | the detector: extend `Landings` and the merge preflight's containment test into a per-row, target-aware classifier, plus the `gs reconcile-merged` read view | landing note I1 and I4 |
| R2 | compose `incorporated` and `incorporated_head` into existing status, work, inspect, MCP, and browser rows, and add the bounded post-merge warning | R1 |
| R3 | `gs merge --incorporated`: the off-target receipt commit, the inverted containment check, the `merge_incorporation` receipt field with its empty-plan requirement, the adapted resume check, and the one `mergedArtifacts` clause, with all seventeen protocol tests and the `docs/reference/gs/merge.md` refusal table updated in the same head | R1, landing note I2 and I3 |

R3 is the only step that changes durable shape. R1 and R2 are read-only and can
land first without committing to it. That order is deliberate: if a reviewer
rejects the incorporation receipt, the detector still earns its place by making
the stuck rows visible, and the argument moves to what else could drain them.

## Architecture

Layer 1 supplies reachability and exact object identity and assigns no Workroom
meaning. Layers 2 through 4 are unchanged. The layer-5 fold continues to own
commitment status, receipt validation, and authority; it receives no ref and
reads no repository. Layer 6 stays repository-independent. Layer 7 composes Git
evidence with the projection for CLI, resident, MCP, and browser use, and uses
existing write actions plus the one new merge flag.

R2 adds repository-aware fields to a projected row, so it changes the layer-6
and layer-7 contract in `docs/reference/architecture.md` and must update that
page in the same head and publish its artifact.

R3 is split across layers and must say so. The receipt commit, the reference
transaction, the containment check, the remeasure, and the resume condition are
layer 7, and
they change the receipt shape, so R3 updates `docs/reference/gs/merge.md` in
the same head.
The `mergedArtifacts` clause is a layer-5 fold rule, so R3 also states whether
the layer-5 contract in `docs/reference/architecture.md` moves and updates that
page in the same head if it does. Nothing in R3 gives the fold a ref or a
repository: it reads the same signed receipt fields it reads today, plus one.

`docs/concepts/work-loop.md` is already scheduled for rewrite by the landing
note's I6; this note adds the incorporation route to that page's scope.

## Security and failure posture

- A merger-authored receipt field confers no new authority. `merge_incorporation`
  is descriptive: it cannot name another commitment, reach another actor's
  artifact, or widen the retirement plan. A `prior` receipt whose plan is not
  empty is ineffective, so the field can never buy succession authority.
- The `prior` clause in `mergedArtifacts` closes exactly the commitment whose
  ratified approval names the artifact at the receipt's candidate, signed by
  that artifact's author. It reaches no path and changes no artifact's
  liveness, so a later unreviewed change at a reviewed path cannot be published
  or retired through it.
- The approval stays single-use through one absent-to-receipt `create` inside
  a Git reference transaction. That `create` is the mutual exclusion, in the
  same sense as the merge's empty old value at `cmd/gs/main.go:775`; the
  `.merge.lock` is an outer advisory one that Git itself does not honour, which
  is why the same transaction carries a `verify` of the target ref. The
  transaction is also the commit point, so a crashed or aborted incorporation
  leaves either nothing referenced or a receipt commit naming its own
  candidate, approval, and a target pre-head that was current when the ref was
  created, and never an approval held by a ref that carries no receipt
  metadata.
- The receipt commit is never advanced onto the target ref and is written with
  `git commit-tree`, so an incorporation cannot move `HEAD`, the index, or the
  working tree. Its tree equals the target tree, so it introduces no bytes.
- The one weakened refusal is stated above. Every other refusal in
  `docs/reference/gs/merge.md:119-142` applies unchanged under `--incorporated`,
  including implementer-only signing (`:134`), exact candidate match (`:132`,
  `:135`), single approval use (`:136`), and the reviewed-path bound on
  retirements (`:142`).
- Revisions are peeled with `git rev-parse --verify <ref>^{commit}` and compared
  as full object IDs. `mergeplan.CanonicalCommit` already refuses a
  non-canonical head, and the merge already refuses an abbreviated `--candidate`
  (`docs/reference/gs/merge.md:141`).
- A target ref that moves between detection and signing invalidates the
  preflight. `gs merge` re-resolves before it appends, and the landing note
  section 6 already requires `merge_target_pre_head` to equal the ref's current
  value at merge time for every merge.
- `gs merge` holds the repository-wide merge lock across observation, Git
  mutation, durable succession, and cleanup (`cmd/gs/main.go:53`, `:662`), so
  `--incorporated` inherits the same serialization.
- Missing and malformed objects fail closed for the affected row only.
  `Landings` already answers a malformed name rather than dropping it
  (`internal/gitstore/landing.go:70-72`), so a bad row cannot hide a good one.
- Counts and rows use the existing status and work bounds; path and error text
  use the existing output sanitation.
- Repository reads are local and read-only. The detector fetches nothing and
  trusts no remote ref implicitly. Remote publication stays advisory, as the
  landing note section 6 says.

## Simplification

Two projected fields, one read view, and one flag on the command that already
signs receipts. No second row collection, no content-equality engine, no MCP
write method, no durable `included` kind, no eleventh commitment status, no
automatic acceptance, no merge-wide artifact retirement, and no second write
command. The 2026-08-31 head proposed the last of those; dropping it is the
main simplification in this revision.

The incorporation protocol adds no mechanism either. It reuses the receipt
commit shape, the approval-specific receipt ref and its create-if-absent
guarantee, the admission preflight, the frontier remeasure, the recovery scan,
and the merge lock, all as they stand. It uses one fewer step than the merge,
because it has no working-tree mutation to hold an approval across. The
genuinely new parts are one trailer, one body field, one inverted check, one
`verify` line in an ordinary Git reference transaction, one adapted resume
condition, and one clause in `mergedArtifacts`. Anything larger
would be building a second succession system beside the one that already works.

## How to verify this note

Every claim above is checkable offline at main 860ee61a.

```sh
# The historical head this note revives.
git show 30768e3f5aabaee53074e26723f8e3644ef0d1f9:notes/2026-08-31-merged-head-commitment-reconciliation.md

# The report door, the merge door, and the supersession door.
sed -n '/## 3\./,/## 4\./p' notes/2026-09-04-landing-obligation.md
sed -n '1250,1260p' internal/mergeplan/mergeplan.go
grep -n 'already contained in the target' docs/reference/gs/merge.md

# The commitment states this note names, and the ones it replaces.
sed -n '/## 4\. Commitment states/,/## 5\./p' notes/2026-09-04-landing-obligation.md
grep -rn 'awaiting-merge' internal/workroom/fold.go internal/statusview internal/mergeplan

# The existing reachability machinery the detector reuses.
sed -n '50,80p' internal/gitstore/landing.go
grep -n 'v0/landed' internal/service/server.go
grep -n 'landed' ui/src/lib/spine.ts

# Completion precedence, current code and current description.
sed -n '3303,3357p' internal/workroom/fold.go
sed -n '/### 1.7 Commitment closure/,/### 1.8/p' notes/2026-09-04-fold-simplification-study.md

# The receipt commit shape the incorporation receipt reuses, and the ref.
sed -n '912,960p' internal/mergeplan/mergeplan.go
sed -n '900,908p' internal/mergeplan/mergeplan.go

# The CAS semantics, the frontier remeasure, the preflight, and the resume path.
sed -n '772,790p' cmd/gs/main.go
sed -n '802,825p' cmd/gs/main.go
sed -n '696,745p' cmd/gs/main.go
sed -n '951,970p' cmd/gs/main.go

# The plan-membership condition the one new fold clause relaxes.
sed -n '3359,3396p' internal/workroom/fold.go

# The warning bound, the merge lock, and the withdrawn UI wording repair.
sed -n '15,20p' internal/statusview/query.go
sed -n '48,54p' cmd/gs/main.go
sed -n '140,152p' notes/2026-08-22-thread-actions.md
```
