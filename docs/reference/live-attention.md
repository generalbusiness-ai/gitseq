---
title: MCP live attention
summary: The bounded advisory adjunct every tool result carries when a resident can answer.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
---

# Live attention

Every completed MCP tool call carries a `live_attention` adjunct beside
its own result, whenever the resident can answer for it. It reports two
things: addressed chat this session has not acknowledged, and live actors
whose leased focus names an event this call just touched.

It exists so an agent notices those things while doing its work, instead
of remembering to poll `presence`, `status`, or `wait`. Live
collaboration stays optional; this only removes the need to remember.

## What it carries

| Field | Meaning |
|---|---|
| `available` | Whether the resident answered. `false` is the honest degraded answer, never an error. |
| `cursor` | The live room cursor at the moment of the read. |
| `frames` | This session's unacknowledged addressed messages, bounded to the priority page. |
| `pending` | How many addressed messages are unacknowledged in total. |
| `omitted` | How many of those are not in `frames`, so a shortened list says so. |
| `actors` | Live actors whose leased focus contains an event this call named or returned. |
| `omitted_actors` | Actors beyond the cap, counted rather than dropped. |

Each actor row carries the full durable fingerprint, the configured name,
how many of that actor's live sessions matched, the exact event
identifiers that matched, the leased status, a note when one is set, and
`activity_changed_at`.

## What it does not mean

`live_attention` is leased, advisory, and ephemeral. It creates no
ownership, no promise, no authority, no completion, and no durable read
receipt. Nothing in it advances the durable sequence. A client that
discards it entirely loses awareness and nothing else.

It never fails your call. The durable act has already happened by the
time the attention read runs, so a resident that cannot answer yields
`available: false` and the tool result is otherwise untouched. An
attention read that failed is not an error you need to handle.

An actor appearing in `actors` has said, through a lease that expires,
that they are attending to that event. They have not claimed it, promised
it, or acquired any standing over it, and you gain none by seeing them.

## How actors are matched

Matching is exact string equality on canonical event identifiers the call
already named or returned. There is no prefix matching, no normalisation,
and no inference about which events relate to which others. A guess about
relatedness would be the adapter asserting a relationship nobody stated,
and presenting it as an observation.

An identifier counts as named only when it stands as a whole token. A
canonical identifier sitting inside a longer run of identifier bytes is
part of that longer token, not a mention of the event, so nothing matches
it.

Your own sessions are filtered out before actors are aggregated, so one
person working from two windows reads as one actor with two matching
sessions rather than as two people. Identity is keyed on the durable
fingerprint, so two actors who happen to share a display name stay two
rows.

`activity_changed_at` is observed by the resident and moves only when
status, focus, or note actually changes. A heartbeat renewal leaves it
alone, so an old timestamp means an old decision rather than a quiet
client.

## Repetition and acknowledgement

Addressed frames keep appearing until you acknowledge them with
[`ack`](mcp/ack.md). Reading is not acknowledging: a tool call that ignores
the adjunct does not consume it, and the next call reports the same
frames again. Acknowledgement is per leased session, so acknowledging in
one session never clears another's.

The guaranteed text block of every result states pending chat and
relevant actors in prose, so a client that reads only text still sees the
interruption rather than having it hidden in structured content.

## Bounds

One call asks about at most 32 event identifiers, drawn from its input
and its result together, and reports at most 16 actors. See
[Limits](limits.md) for the full table and for the addressed-chat
bounds the frames page inherits.

## See also

- [`ack`](mcp/ack.md), [`say`](mcp/say.md) — the addressed-chat half.
- [`presence`](mcp/presence.md) — setting your own leased status and focus.
- [`status`](mcp/status.md), [`wait`](mcp/wait.md) — the explicit reads this
  adjunct saves you from having to remember.
