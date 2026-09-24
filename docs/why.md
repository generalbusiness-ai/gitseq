---
title: Why gitseq exists
summary: The problem gitseq solves, and the shape of its answer.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# Why gitseq exists

## The problem

Many documents your team keeps serve as caches of earlier decisions, and
nothing normally invalidates them. The design note, the runbook, the quote, the
dashboard, the onboarding page: someone rendered each from some state of the
discussion, and it then kept serving reads after the discussion moved on. The
usual repair has a human asking in a channel whether the page remains current,
which works about as well as it sounds.

Agents make this sharper rather than different. More gets said, more gets
done, and it matters more who stands behind each act.

## Why git alone does not fix it

Git solved the naming half of the problem. Content addressing answers
*do these bytes match the ones I saw before?* exactly and cheaply.

It cannot answer *does this still hold?*, and not by oversight. Currency
needs a clock, and git deliberately has no final one. The order of `main`
stays editorial and revisable, every branch forms another partial order, and
every rebase moves the hands. That makes the right design for source code
and the wrong one for commitments. You cannot say "as of commit X" to a
colleague and have it mean one fixed thing to both of you, because X's
position in history remains negotiable.

## The shape of the answer

gitseq adds the missing half: a sequenced log carried in ordinary git
refs under `refs/seq/*`. Positions in that sequence stay final. Nothing
rewrites them, and no merge reorders them.

Three small mechanisms, and the value lies entirely in their composition.

**Every act gets a position.** A durable act carries its author's signature,
takes one position on admission, and never moves. "As of #4312" means the same
thing to every reader, forever.

**Acts point backwards.** Each act names what it rests on: the request it
answers, the decision it implements, the claim it disputes. Together they form
a dependency graph pinned to a clock, rather than a wiki full of links
with no before and after.

**Tracked artifacts name the acts they describe.** Once an artifact statement
names a page at an exact commit and the acts governing it, *does this need
another look?* becomes a deterministic question. Retire one of those acts and
the fold marks the artifact stale. That means re-check it; it does not declare
the page wrong.

## What that buys

The record answers ordinary questions mechanically:

- What did we agree to, and when relative to everything else?
- Who waits on whom?
- What evidence supports this claim?
- Did anyone adopt, dispute, withdraw, replace this?
- Which tracked artifacts need re-checking, and what moved underneath them?

The answers come as projections, not decrees. Every reader replays the same
deterministic fold over the same signed events and reaches the same
verdicts. Acts that exceeded their author's authority stay visible as
attempts without gaining force, so the log records what someone tried as well
as what took effect.

## What gitseq deliberately does not do

It has **no ontology**. `request`, `promise`, `artifact` and the rest name
speech acts belonging to the practice of a particular room, not types the
substrate understands. Two rooms can use different vocabularies over the
same machinery.

It does **not interpret on your behalf**. The fold lives in a library that
readers and applications run; no server's reading carries authority.

It does **not discover causal edges from files, imports, or prose**. Someone
has to register the artifact and the premises it rests on. Unanchored work
stays invisible, and gitseq does not judge whether revised prose holds up.

It does **not hold your work hostage**. Artifacts stay where they always
lived — files, commits, branches. Delete `.git/gitseq` and the extra fetch
rule and you have an ordinary repository back.

## Where to go next

- [Do a piece of work, end to end](how-to/end-to-end.md) — the same ideas
  as a sequence of commands that run.
- [The record](concepts/record.md) — what makes up an event and what the fold
  does with it.
- [Staleness](concepts/staleness.md) — what a flare covers, and the one
  known, open gap.
