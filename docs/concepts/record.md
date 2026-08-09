---
title: The record
summary: What a durable event is, what the fold makes of it, and why a recorded act may carry no force.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:57e4bc379b4f3539155eb83b13c359567e436aff
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1539075831e59cbc39fefdd6a4e800ba2c150208
---

# The record

## Two channels

A workroom has two channels, and the difference between them is the
point.

**Ephemeral** speech — the `say` tool — is sequenced and signed, but
forgotten when everyone leaves and their presence leases expire. Use it
for thinking out loud, questions, drafts, disagreement in progress. It is
cheap; prefer it. It is not private: any participant can keep a copy.

**Durable** acts — `state`, `ratify`, `supersede` — are permanent,
ordered, attributed, and visible to everyone, including the ones that
turn out to have no effect.

Talk freely. Commit deliberately.

## What an event is

A durable act becomes one commit under `refs/seq/<genesis>`. The commit
carries a signed intent naming:

- the workroom it belongs to, by genesis hash;
- a schema, and a payload tree holding the statement and any attachments;
- `rests_on`, the list of events this one bears on;
- an idempotency namespace and key, so a retry lands once.

The intent is signed by the **actor**. The sequence commit is signed by
the **sequencer**. Two signatures, two questions: who wrote this, and who
admitted it at this position.

Its name is its [event identifier](../reference/event-identifiers.md),
which is what every other act cites.

## Kinds are speech acts

`assert`, `propose`, `request`, `promise`, `report`, `dissent`,
`artifact`, and a few governance kinds. These are things people do with
words, not types the substrate understands. Their meaning belongs to the
practice of the room; [`SKILL.md`](../../SKILL.md) is where this project
writes its own down.

A few kinds do carry structure the fold reads. An `artifact` must have
`body.path` and `body.commit`. A `request` must have `body.conditions`
and names its performer in `body.to`. Those are the hooks that make
commitments and staleness projectable.

## The fold

Reading the record means replaying it. The **fold** is a deterministic
function from the event sequence to a projection: commitments and who
they wait on, artifacts and whether they have gone stale, actors and
their current roles, and a decision for every act.

Two properties matter. It is **deterministic**, so every reader reaches
the same verdicts without trusting a server. And it is **a library, not a
service**: gitseq defines the record and never runs a fold on your
behalf. `gs status` runs one for you; so does the browser view; both are
readers like any other.

## Recorded is not effective

Every act gets a decision: `effective`, or not, with a reason. An act
whose author lacked the authority, or whose required citation does not
resolve, is still kept — visibly, permanently — as an attempt that did
not take force.

This is deliberate. Deleting failed attempts would make the log lie by
omission about what was tried. `gs status` lists them under **Attempts**.

How much of `rests_on` the fold checks depends on the act:

| Act | What must resolve | Surplus bases |
|---|---|---|
| `assert`, `artifact`, `propose`, other statements | nothing | carried unchecked |
| `promise` | one basis must be an effective `request` | carried unchecked |
| `report` | one basis must be an effective `promise` | carried unchecked |
| `supersede` | the target, and it must be the **first** basis | carried unchecked |
| `ratify` | the target, and it must be the **only** basis | **refused** |

Those required edges are what make the commitment chain hold: a promise
citing a request that does not exist is ineffective, and a report on that
promise is ineffective in turn, so an unearned approval cannot carry
force.

For the acts with a required edge, that edge is necessary and not
sufficient — the fold also checks who signed. A promise citing a
perfectly good request is ineffective when its author is not the
performer the request named.

Everything else in `rests_on` is a claim about meaning, and a substrate
with no ontology cannot check meaning. A mistyped identifier on an
`assert` is simply kept. The practical rule does not vary with the table:
copy identifiers whole, from the emitted event.

## Nothing is deleted, but things are retired

`supersede` retires an act and propagates staleness to everything resting
on it. Prefer supersession to contradiction: it leaves the earlier act in
place, with a pointer to what replaced it.

Retirement is reversible — superseding a supersession brings the earlier
act back — while a decision is not. Decisions are history and do not
move; liveness is current and does.

## Bounds

Every actor-controlled string in a signed intent is capped, causal
references are capped, and the whole envelope shares the workroom's
payload ceiling. Oversize input is refused before the sequence ref moves,
so a rejected act leaves no trace in the sequence at all. The numbers are
in [Limits](../reference/limits.md).

## See also

- [Event identifiers](../reference/event-identifiers.md)
- [Actors and authority](actors.md)
- [Staleness](staleness.md)
