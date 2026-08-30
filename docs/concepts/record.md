---
title: The record
summary: What a durable event is, what the fold makes of it, and why a recorded act may carry no force.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
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

A room **declares** its kinds, and the projection carries that
declaration at `status.durable.vocabulary`. Each declared kind states its
required fields, its basis constraints, who may ratify it, how it
renders, how staleness travels through it, and whether it takes part in
the commitment lifecycle. That catalog, not this page, is the source of
truth for what a kind demands. A statement whose kind the vocabulary does
not declare stays visible and carries no semantic force: an
`undefined-kind` reading is a gap to surface, not a meaning to improvise.

The declarations are what make commitments and staleness projectable. An
`artifact` must have `body.path` and `body.commit`. A `request` must have
`body.conditions` and names its performer in `body.to`.

Which rules admit events is itself governed. A workroom runs the
**admission profile** named by the newest live, ratified governance
statement for its genesis — naming a bundle and a contract — and falls
back to a bootstrap profile derived from genesis when there is none.
Likewise, if the record has no fold binding, or the binding names an
interpreter this reader does not hold, the projection reports `unbound`
or `uninterpretable`. Those are audit gaps, not warnings to click
through.

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

How much of `rests_on` the fold checks depends on the event:

| Event | What must resolve | Surplus bases |
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

An artifact keeps the permissive admission rule in the table. It participates
in an assigned implementation commitment only when its promisor authored it,
it names a commit, and its bases contain exactly one promise: the promise it
reports. A sealed merge of the independently approved exact head then closes
that commitment; ordinary artifacts remain ordinary artifacts.

For events with a required edge, that edge is necessary and not
sufficient — the fold also checks who signed. A promise citing a
perfectly good request is ineffective when its author is not the
performer the request named.

Everything else in `rests_on` is a claim about meaning, and a substrate
with no ontology cannot check meaning. It can still check existence. An
identifier for this workroom that names no event in it is refused before
it is sequenced, whatever the kind, because the log is append-only and a
dangling reference admitted once can never be repaired. What the
substrate cannot resolve at all — another workroom's identifier, a string
that is not an identifier — it keeps, and on an `assert` that costs the
act nothing. The practical rule does not vary with the table: copy
identifiers whole, from the emitted event.

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
