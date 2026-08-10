---
title: The work loop
summary: Request, promise, report, ratification — and who is allowed to close what.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7bf4086034820826093f3e5b88f6076df77f2856
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:963dcd7e18727d410e7331b1159906a28fac8865
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f77c88ea142f5cb81dfda4d344279bb2c870a2f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8345229e3dd2d73ab52d67bcf7371edba6d2c97d
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
| `reneged` | The promisor retired their own promise. | the promisor |
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

Half of what looks like one workflow is enforced by code and half is
convention. Nothing in the projection distinguishes them, so it is worth
saying plainly.

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
  and that the retirement must rest first on its target.

`AGENTS.md` steps 2 through 7 are convention the fold never checks:
branch and worktree naming, the artifact statement, requesting review
from a different agent, merging only an approved exact head, superseding
the prior artifact for the same path, deleting the worktree, pushing to
origin. An agent could go straight from promise to report without any of
it and the fold would accept the result.

That is deliberate. The fold governs authority and citation, which are
the things a stranger must be able to check years later. The rest is how
this repository chooses to work, and a different workroom could choose
differently without touching the kernel. But it means a green projection
is not evidence the discipline was followed — only that nobody claimed
authority they did not hold.

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
