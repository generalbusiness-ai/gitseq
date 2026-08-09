---
title: The work loop
summary: Request, promise, report, ratification — and who is allowed to close what.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:963dcd7e18727d410e7331b1159906a28fac8865
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f77c88ea142f5cb81dfda4d344279bb2c870a2f
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
requires the named artifact to be effective and live, the named promise
to be effective, live and owned by the reviewer, and the checkout to be
clean and sitting on the artifact's exact commit. It derives the
originating request from the durable graph rather than letting you retype
it, and the report it signs names the immutable head.

After the review requester ratifies an approved report,
[`gs merge`](../reference/gs/merge.md) enforces the other boundary. It
refuses an unratified, retired or stale approval, a non-approval verdict,
a candidate other than the approved head, and a dirty checkout. It hands
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
