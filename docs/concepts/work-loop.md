---
title: The work loop
summary: Request, promise, report, ratification — and who is allowed to close what.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b2edd696b01e4ce953cf31194eb1a3dbb67e9b56
---

# The work loop

## The shape

A **request** names whom it is to and its conditions of satisfaction. A
**promise** rests on that request and claims it. A **report** rests on
the promise and claims completion. The **requester** — nobody else —
ratifies the report.

```text
request  ──rests_on──  promise  ──rests_on──  report  ──ratify──  satisfied
   ▲                      ▲                     ▲                    ▲
 alice                   bot                   bot                 alice
```

You never declare your own work complete. That is the whole reason the
requester holds ratification: satisfaction is a judgement by the person
who asked, not a status the doer sets.

A free-standing promise projects as dangling, because nobody is
positioned to declare it satisfied. A report on a promise that does not
resolve is ineffective in turn, so an unearned approval cannot carry
force.

## Every state, and what causes it

A commitment is one request paired with one promise. A request nobody has
promised is a commitment too, with the promise half empty. The fold
projects eight statuses and no others.

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
                    └─ promisor reports ───────────────▶ reported
                                                            │
                                    requester ratifies ─────┴─▶ satisfied
```

| Status | What it means | Who caused it |
|---|---|---|
| `open` | Asked, unclaimed. | the requester, by asking |
| `withdrawn` | The request was retired before anyone promised. | the requester, or a ratifier |
| `promised` | Claimed, not yet reported. | the promisor |
| `reported` | Completion claimed, awaiting judgement. | the promisor |
| `satisfied` | The requester accepted the report. | the requester |
| `cancelled` | The request was retired after a promise existed. | the requester, or a ratifier |
| `reneged` | The promise was retired. | the promisor, or a ratifier |
| `stale` | Something it rests on died, and no live report stands. | nobody — a consequence |

Four details the diagram cannot carry.

**Retirement beats staleness.** A retired request projects `withdrawn`,
not `stale`, even when both are true.

**Cancelled beats reneged.** If the request and the promise are both
retired, the commitment reads `cancelled`.

**The newest live report wins.** Reports are read newest first and
retired ones are skipped, so superseding a report and filing a
replacement is a supported repair, not a duplicate.

**`satisfied` and stale are not exclusive.** Staleness is computed while
the report is read and survives ratification, so a commitment can be
both satisfied and stale. That combination is the normal outcome of a
completed loop, because the merge retires the branch artifact that the
report and the approval both legitimately cite.

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

- the eight statuses above, and which event causes each;
- that only the promisor may report — anyone else is refused with *only
  the promisor may report completion*;
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

What is left is **convention nothing checks**: branch and worktree
naming, the `Rests-On:` trailer on the source commit, deleting the
worktree after merge, and pushing to origin. The lifecycle alone would
also accept a promise followed straight by a report, with no branch and
no artifact — what stops that work reaching `main` is the merge guard,
not the fold.

So a green projection is narrower evidence than it looks, but not empty.
It shows that nobody claimed authority they did not hold, that each act
carries the bases its own kind requires — a promise resting on exactly
one request, a report on a promise — and, where work was merged through
`gs merge`, that an independent reviewer approved that exact commit. It
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

An unratified report is **honest status**, not failure. It reads
"reported, awaiting satisfaction". Do not treat it as a gap to be chased.

Superseding your own promise is **reneging**, and it stays visible
forever. Do it as early as you know you cannot keep it: early reneging is
honourable, late reneging is not.

If the requester supersedes the request after you promised, you are
released. The promise stays in history as kept faith, not fault.

## Bridging work to code

Work that changes files has to be bridged to the decisions that motivated
it, or staleness tracking cannot see it — and then the record lies by
omission, which is the one failure this system exists to prevent.

Two halves:

1. The implementing commit carries a trailer, `Rests-On: <event>`. The
   governing event must exist **before** you make the commit; otherwise
   you have to amend the trailer in afterwards and the hash changes.
2. An `artifact` statement cites the commit as `path@commit` and rests on
   the decisions that govern it.

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

After the review requester ratifies an approved report,
[`gs merge`](../reference/gs/merge.md) enforces the other boundary, and
keeps the strict reading review gives up. It refuses an unratified,
retired or stale approval or artifact, a non-approval verdict, a
candidate other than the approved head, and a dirty checkout. It hands
git the approved object ID, never a branch name, so advancing the
reviewed branch cannot retarget the merge.

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
