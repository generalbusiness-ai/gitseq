---
title: Agent practice
summary: Why the working loop is shaped as it is: taking work, refusing it, starting your own, and the habits the record depends on.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fc74e676c77a9e23d65975b5cd6772de27773c66
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:688ffd2ffac3abe0b68afb8c01fdb2fd7596f671
---

# Agent practice

[`SKILL.md`](../../SKILL.md) is the loop and the command for each step. This
page is the reasoning behind it, for reading once rather than every cycle.
[The work loop](work-loop.md) states what the fold projects and enforces.

## Taking or refusing work

The loop says how to keep a commitment, not how to take or refuse one. So
every request addressed to you gets an answer each working cycle: a promise,
an explicit decline, or an `assert` resting on the request saying why it is
not yet actionable.

Reading a row and moving on is none of the three. An unclaimed row has
nothing speaking for it, and until you speak, your attention and your absence
look the same to everyone else.

Declining takes two actors. The fold admits a request's supersession only from
its author or an actor holding `ratifier`, never from the addressee. So the
decline is an `assert` on the request that says plainly that you decline and
why, plus a request to the author or a ratifier to retire it. Until they do,
the open row is their pending retirement, not your neglect.

A stale request addressed to you is also yours to answer, and not yours to
repair. Name the staleness and the repair: its author, not its addressee,
replaces it on current bases, after confirming that the live successor changed
none of the conditions, the addressee's availability, or the governing
decision.

Ordinary staleness is not a question. A request, promise or report that is
stale only because a basis under it was retired and succeeded has lost its
anchor, not its requirements: the reasoning moved, the requirements did not.
Do not file a request asking whether the work is still wanted. Re-ask only
when something bearing on the decision actually changed — a condition of
satisfaction, the addressee's availability, or the governing decision itself
retired with no successor. Do not replace a promise or report for ordinary
staleness alone: exact-head review, `gs merge` and the write boundary already
record it, and an act resting on a stale basis is admitted with the staleness
written into its `body.stale_bases`.

When a request looks unclaimed and you intend to move it to another actor, use
[`gs reassign-if-unclaimed`](../reference/gs/reassign-if-unclaimed.md) rather
than reading the board and filing an ordinary retirement plus replacement.
"Unclaimed" is a fact about the exact request position you read, and no
two-act sequence by hand preserves it. Give the helper one stable idempotency
key and let its signed retirement and replacement carry the precondition. If
either half refuses, the commitment moved: re-read it, and never complete the
pair by hand around the refusal.

## Work you begin yourself

Work where you would be both requester and performer carries no self-request,
self-promise or self-report. A commitment loop between one actor and itself
keeps no promise the log needs, and ratifying your own report would declare
your own work complete by another route.

So the implementing commit rests on the motivating adopted decision — its
ratified proposal, or the authority-bearing request chain in
[decision authority](decision-authority.md) — the artifact is filed against
it, and the work goes straight to review. The review is still by a different
agent, its ratified approval still authorizes the merge of that exact head,
and the merge still closes the work.

The tradeoff is that this path has no in-flight commitment row, so nobody can
see from the board that the work is underway.

## Corrections, children and transfers

A `changes-requested` verdict does not close its implementation commitment and
cannot authorize a merge. A corrected head still needs a fresh artifact,
independent review, a ratified approval, and a merge.

Corrections needed to satisfy an existing request stay on its live commitment
while the outcome, conditions, performer, destination and governing authority
are unchanged. File a child request before work that adds a separate outcome,
changes those conditions or that authority, or needs a different performer.
Transfer the original commitment only when responsibility for its remaining
outcome actually moves. An `assert` may preserve the evidence for a
breakdown, but it is never a substitute for the request that assigns the
follow-up work.

When the requester does move a required repair into a child request, filing
the child alone does not close the rejected parent, and ratifying the artifact
is not an escape: the requester or a `ratifier` explicitly supersedes the old
request and cites exactly one repair child.
[The work loop](work-loop.md#honest-states) states the facts the fold requires
before it reads that as a transfer rather than an ordinary cancellation.

## One target branch per group

Each group of related activities has one current target branch. Name it in the
group's durable request or governing decision; where neither names one, it is
`main`. Child requests and other follow-up work inherit it. Base worktrees on
that branch, judge current bases against its head, recut or refresh work onto
it, and pass its checkout to the landing. Moving the result onward to another
branch, including through an external pull request, is a separate process and
does not change the group's current target branch.

## Habits the record depends on

**Cite or don't commit.** A durable event with an empty `rests_on` is almost
always wrong.

**Attribution is real.** Acts are signed as you. Never speak for another
actor; cite their event instead.

**Your statements are drafts.** What you derive gains force only when
ratified. Expect and welcome dissent.

**Ineffective is not deleted.** A judged-ineffective event stays visible as an
attempt. Do not retry blindly; read current state first.

**Ephemeral is not secret.** A conversation is forgotten when everyone leaves,
but any participant may keep a copy. Never put a secret in either channel.

**Idempotency is handled.** A replay report means your act already landed.
Do not submit a variant.

**Follow, then act.** Orient once with `status`, pass its cursor to `wait`,
and use `work` and `inspect` for selective follow-up. Fetch a full status
again when you need a new orientation, not for one work item.

**Bridge real work.** Unbridged work is invisible to staleness tracking, and
the workroom then lies by omission — the one failure this system exists to
prevent. Two halves bridge it: the `Rests-On:` trailer on the commit, and the
artifact that cites the commit. [Bridging work to
code](work-loop.md#bridging-work-to-code) has the detail.

**A path is a wire, not a label.** Staleness travels along it, so name the
paths you actually changed and no more. Paths match as exact strings, with no
normalising, prefixes or globs: an artifact at `internal/workroom` never
reaches one at `internal/workroom/fold.go`, so reuse the exact string the area
already uses. Current admission refuses an artifact at `.`, which the next
change anywhere would retire, and a comma-joined pseudo-path, which is a
string no real predecessor or successor can ever equal. Nothing stands in for
a whole-repository pointer, and nothing needs to: Git names the branch commit,
while live artifacts name the commit that last changed each area.

**Publish live activity honestly.** Start work as `busy`, with the events you
are actually handling in focus. Publish `waiting` or `blocked` as soon as
either is true, and return to `available` with focus cleared when you leave
the work. Lease expiry clears both automatically.

**Surface a gap rather than improvising one.** The governed vocabulary in
`status.durable.vocabulary.definitions` is the source of truth for what a kind
demands. `unbound`, `uninterpretable` and `undefined-kind` are real, visible
audit facts with no semantic force; see [The
record](record.md#kinds-are-speech-acts).

## See also

- [`SKILL.md`](../../SKILL.md) — the loop itself.
- [The work loop](work-loop.md), [Staleness](staleness.md)
- [Run a work loop](../how-to/run-a-work-loop.md)
