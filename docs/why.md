---
title: Why gitseq exists
summary: The problem gitseq solves, and the shape of its answer.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# Why gitseq exists

## The problem

Every document your team keeps is a cache of a conversation, and nothing
invalidates it. The design note, the runbook, the quote, the dashboard,
the onboarding page: each was rendered from some state of the discussion
and then kept serving reads after the discussion moved on. The usual
repair is a human asking in a channel whether the page is still current,
which works about as well as it sounds.

Agents make this sharper rather than different. More gets said, more gets
done, and it matters more who stands behind each act.

## Why git alone does not fix it

Git solved the naming half of the problem. Content addressing answers
*is this the same bytes I saw before?* exactly and cheaply.

It cannot answer *is this still true?*, and not by oversight. Currency
needs a clock, and git deliberately has no final one. The order of `main`
is editorial and revisable, every branch is another partial order, and
every rebase moves the hands. That is the right design for source code
and the wrong one for commitments. You cannot say "as of commit X" to a
colleague and have it mean one fixed thing to both of you, because X's
position in history is negotiable.

## The shape of the answer

gitseq adds the missing half: a sequenced log carried in ordinary git
refs under `refs/seq/*`. Positions in that sequence are final. Nothing
rewrites them, and no merge reorders them.

Three small mechanisms, and the value is entirely in their composition.

**Every act gets a position.** A durable act is signed by its author,
admitted at one position, and never moves. "As of #4312" means the same
thing to every reader, forever.

**Acts point backwards.** Each act names what it rests on: the request it
answers, the decision it implements, the claim it disputes. The result is
a dependency graph pinned to a clock, rather than a wiki full of links
with no before and after.

**Documents name the acts they describe.** Once a page says which acts
govern it, *is this still current?* stops being a question you ask a
person. Retire one of those acts and the page is marked stale, along with
the reason and the event that caused it.

## What that buys

The record answers ordinary questions mechanically:

- What did we agree to, and when relative to everything else?
- Who is waiting on whom?
- What evidence supports this claim?
- Was this adopted, disputed, withdrawn, replaced?
- Is this document still current, and if not, what made it stale?

The answers are projections, not decrees. Every reader replays the same
deterministic fold over the same signed events and reaches the same
verdicts. Acts that exceeded their author's authority stay visible as
attempts without gaining force, so the log records what was tried as well
as what took effect.

## What gitseq deliberately does not do

It has **no ontology**. `request`, `promise`, `artifact` and the rest are
speech acts belonging to the practice of a particular room, not types the
substrate understands. Two rooms can use different vocabularies over the
same machinery.

It does **not interpret on your behalf**. The fold is a library that
readers and applications run; there is no server whose reading is
authoritative.

It does **not hold your work hostage**. Artifacts stay where they always
were — files, commits, branches. Delete `.git/gitseq` and the extra fetch
rule and you have an ordinary repository back.

## Where to go next

- [Do a piece of work, end to end](how-to/end-to-end.md) — the same ideas
  as a sequence of commands that run.
- [The record](concepts/record.md) — what an event is and what the fold
  does with it.
- [Staleness](concepts/staleness.md) — what a flare covers, and the one
  gap that is known and open.
