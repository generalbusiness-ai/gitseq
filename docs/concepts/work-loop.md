---
title: The work loop
summary: How promises become exact artifacts, independently approved merges, or explicit reports.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# The work loop

## The shape

A **request** names whom it is to and its conditions of satisfaction. A
**promise** rests on that request and claims it. For implementing work, an
**artifact** rests on the promise, names the exact implementation head, and
serves as the completion report. An independent reviewer records a verdict;
the review requester ratifies an approval before `gs merge` may use it. The
merge of that approved exact head closes the implementation commitment.

```text
request ─▶ promise ─▶ artifact ─▶ independent verdict ─▶ ratify ─▶ merge ─▶ satisfied
 alice      bot         bot              reviewer            bot      approved chain
```

The artifact removes a duplicate `ready-for-review` record; it does not remove
scrutiny. The review, its verdict, its explicit pre-merge ratification, and the
different-agent rule are unchanged. The merge is the durable acceptance of the
implementation result, so no second ratification follows it.

Work that resolves without a merge still uses the general route: the promisor
files an explicit **report**, and the original requester ratifies it, so long
as that requester is still a live participant. You never declare your own
unmerged work complete. Retiring the requester's membership retires this
authority with it: a later ratification is kept in the log but judged
ineffective, even when the report was filed after they left.

A free-standing promise projects as dangling, because nobody is
positioned to declare it satisfied. A report on a promise that does not
resolve is ineffective in turn, so an unearned approval cannot carry
force.

## Every state, and what causes it

A commitment is one request paired with one promise. A request nobody has
promised is a commitment too, with the promise half empty. The fold
projects ten statuses and no others.

Before anyone promises:

```text
                    ┌─ requester retires the request ──▶ withdrawn
                    │
request ─▶ open ────┼─ a cited event is retired ────────▶ stale
                    │
                    └─ someone promises ───────────────▶ promised
```

After a promise, the same commitment continues:

```text
                    ┌─ requester retires the request ──▶ cancelled
                    │
                    ├─ promisor retires the promise ───▶ reneged
promised ───────────┤
                    ├─ a cited event is retired ───────▶ stale
                    │
                    ├─ promisor files explicit report ──▶ reported
                    │                                      │
                    │       requester ratifies report ─────┤
                    │                                      │
                    └─ promisor files artifact ─▶ awaiting-merge
                                                           ├─ approved exact head merges ─▶ satisfied
                                                           │
                         rejected repair explicitly moved ─┴─▶ superseded
```

| Status | What it means | Who caused it |
|---|---|---|
| `open` | Asked, unclaimed. | the requester, by asking |
| `withdrawn` | The request was retired before anyone promised. | the requester, or a ratifier |
| `promised` | Claimed, not yet reported. | the promisor |
| `reported` | Completion claimed by an explicit report, awaiting requester ratification. | the promisor |
| `awaiting-merge` | Completion claimed by an artifact, awaiting the independently approved exact-head merge the performer signs; waits on the performer. | the promisor |
| `superseded` | A ratified `changes-requested` verdict rejected the reporting artifact, and an explicit linked supersession moved the required repair to `successor_request`. | the requester, or a ratifier |
| `satisfied` | The approved exact head merged, or the requester accepted an explicit report. | the merge or the requester |
| `cancelled` | The request was retired after a promise existed. | the requester, or a ratifier |
| `reneged` | The promise was retired. | the promisor, or a ratifier |
| `stale` | Something it rests on died, and no live report stands. | nobody — a consequence |

Four details the diagram cannot carry.

**Retirement beats staleness.** A retired request projects `withdrawn`,
not `stale`, even when both are true.

**Cancelled beats reneged.** If the request and the promise are both
retired, the commitment reads `cancelled`.

**Existing completion authority is preserved.** A sealed merge is terminal.
Otherwise the newest live explicit report keeps the authority reports had
before artifacts could report implementation work. A conforming artifact is
the report when the promise has no live explicit report. It conforms when its
promisor authored it, it names a commit, and it rests on exactly one promise:
the promise it reports.

**`satisfied` and stale are not exclusive.** Staleness is computed while
the completion and closing records are read, so a commitment can be both
satisfied and stale. A later movement under an already merged result does not
erase the fact that the merge happened.

## Where this is defined

What looks like one workflow is enforced in three tiers: the fold, the
guarded commands, and convention. Nothing in the projection distinguishes
them, so it is worth saying plainly.

The fold enforces, in `internal/workroom/kinds.go`:

- which kinds carry a lifecycle edge, and the basis each requires — a
  promise rests on exactly one request, a report on exactly one promise;
- who may confer force, through each kind's satisfier: the originating
  requester for a report, the `ratifier` role for `propose`, `assert`
  and the governance kinds.

And in `internal/workroom/fold.go`:

- the ten statuses above, and which event causes each;
- that only the promisor may report — anyone else is refused with *only
  the promisor may report completion*;
- that a promisor's exact-head artifact resting on one promise discharges the
  same report obligation, and a sealed merge receipt for that artifact closes
  the commitment;
- that an act may be retired by the actor who made it, or by a ratifier,
  and that the retirement must rest first on its target;
- that an artifact names both a path and a commit;
- which artifact a review judges, resolved only when the cited artifact
  and the claimed head agree, and whether that review was independent,
  from the reviewer's and implementer's fingerprints;
- that a live artifact with a later live artifact at the same path owes
  its retirement, projected as an omitted supersession.

The **guarded commands** enforce a second tier that the fold projects but
does not itself refuse. [`gs review`](../reference/gs/review.md) requires
a clean checkout sitting on the artifact's exact commit.
[`gs merge`](../reference/gs/merge.md) refuses an approval that is not
ratified, a verdict that is not `approved`, a candidate that differs from
the approved head, an approval that does not causally rest on its named
artifact, an artifact whose commit differs from the candidate, an act
that is not projected as a review at all, and an approval signed by the
actor who implemented the head — or one whose independence the record
cannot determine.

The merge gate still requires the review approval to be ratified before it can
act. The merge does not ratify that review verdict; it closes the separate
implementation commitment after the approval chain has already authorized it.

What is left is **convention nothing checks**: branch and worktree
naming, the `Rests-On:` trailer on the source commit, deleting the
worktree after merge, and pushing to origin. The lifecycle also accepts a
promise followed straight by an explicit report for work that does not merge.
What stops such a report reaching `main` is the merge guard, not the fold.

So a green projection is narrower evidence than it looks, but not empty.
It shows that nobody claimed authority they did not hold, that each act
carries the bases its own kind requires — a promise resting on exactly one
request, an explicit report on a promise, or a reporting artifact on its
single promise — and, where work was merged through `gs merge`, that an
independent reviewer approved that exact commit and the approval was ratified. It
does not show that the branch was named well, that the commit carried
its trailer, or that anyone tidied up afterwards.

It also does not show that every citation resolves, and the difference
matters more than it sounds. The fold checks `rests_on` only against the
basis constraints of the citing kind: a reference that names no event in
this workroom is skipped in silence, and a kind with no basis constraints
never has its citations inspected at all. An effective artifact or assert
can therefore carry a citation pointing at nothing while the projection
stays green around it.

That is not a hole to route around; it is what "gitseq has no ontology"
costs. The fold cannot know which of your references were load-bearing.
But it means a mistyped or invented identifier fails quietly — it appends,
it reports success, and the act it was supposed to connect to simply never
hears about it. Resolve identifiers before citing them rather than
trusting a green projection to catch a bad one.

## Honest states

A completion artifact before merge, or an unratified explicit report, is
**honest status**, not failure. The artifact reads `awaiting-merge` and waits
on its performer, who signs the merge once the approval is ratified; the
explicit report reads `reported` and waits on its requester. Do not treat
either as a gap to be chased.

Superseding your own promise is **reneging**, and it stays visible
forever. Do it as early as you know you cannot keep it: early reneging is
honourable, late reneging is not.

If the requester supersedes the request after you promised, you are
released. The promise stays in history as kept faith, not fault.

An ordinary request retirement after a promise is `cancelled`. A rejected
implementation round is `superseded` only when the explicit retirement also
cites one effective repair child from the same requester, that child directly
rests on the old request, and a live ratified `changes-requested` verdict names
the reporting artifact and its exact head. `successor_request` is the pointer;
the child's later outcome stays on the child row rather than rewriting the
historical transfer.

If the requester read the request as unclaimed and wants to change its
addressee, the read can race a promise or direct completion. The guarded
`reassign_if_unclaimed` / `gs reassign-if-unclaimed` path signs the exact old
request, retires it only while it is live and fresh with neither fact present,
then publishes a replacement naming that exact retirement only if the facts
remain unchanged. Unrelated log traffic is allowed. A commitment change
refuses and requires a fresh read. Ordinary supersession remains the route for
a requester who knowingly withdraws promised work.

## Bridging work to code

Work that changes files has to be bridged to the decisions that motivated
it, or staleness tracking cannot see it — and then the record lies by
omission, which is the one failure this system exists to prevent.

Two halves:

1. The implementing commit carries a trailer, `Rests-On: <event>`. The
   governing event must exist **before** you make the commit; otherwise
   you have to amend the trailer in afterwards and the hash changes.
2. An `artifact` statement cites the commit as `path@commit`, rests on the
   promise it fulfils, and may also cite the decisions and implementation
   artifacts that govern it. That artifact is the implementation report.

The artifact is the durable pointer to implementation truth. Branch and
head hints in a request, promise or report body are conveniences for
associating a local checkout; they claim nothing about whether that
checkout is clean or current.

## Review at an exact head

A review verdict is about one immutable commit, not about a branch. If
the branch moves after the verdict is signed, the approval no longer
describes anything anyone looked at.

[`gs review`](../reference/gs/review.md) enforces that boundary. It
requires the named artifact to be effective and not retired, the named
promise to be effective, not retired and owned by the reviewer, and the
checkout to be clean and sitting on the artifact's exact commit. It
derives the originating request from the durable graph rather than
letting you retype it, and the report it signs names the immutable head.

Staleness is the one thing it does not refuse. Whether a moved world
matters to this exact commit is the reviewer's judgement, so the review
goes ahead and the verdict records what had moved.

After the review requester explicitly ratifies an approved report,
[`gs merge`](../reference/gs/merge.md) enforces the other boundary. It
refuses an unratified or retired approval or artifact, one that describes
a superseded world, a non-approval verdict, a candidate other than the
approved head, and a dirty checkout. Ordinary staleness is not on that
list: the reviewed head is immutable, so the merge lands it and records
what had moved in its receipt. It hands git the approved object ID, never
a branch name, so advancing the reviewed branch cannot retarget the
merge. Its sealed receipt then closes the implementation promise whose
reporting artifact was reviewed; it does not replace or imply the earlier
review ratification.

Running tests and poking at the checkout is still the reviewer's
evidence. These commands do not replace judgement; they fix the state at
which the judgement is recorded.

## Promoting from conversation

Talk in the ephemeral channel until something crystallizes, then promote
it: a durable act with the selected signed frames embedded as
`evidence`. A stranger can verify it later, after the conversation is
gone. Select honestly and summarize faithfully.

Promote a breakdown only when it changes scope, changes a condition of
satisfaction, or creates follow-up work. Routine progress stays
ephemeral.

## See also

- [Run a work loop](../how-to/run-a-work-loop.md) — the same shape as
  commands that run.
- [`gs state`](../reference/gs/state.md)
- [Staleness](staleness.md)
