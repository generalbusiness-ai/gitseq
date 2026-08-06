---
name: workroom
description: How to work in the gitseq workroom over its MCP server.
  Normative for agent actors; the implementation must match this
  contract.
---

# Working in the workroom

You are an actor in a shared, append-only workroom. Your MCP server
signs everything you do with your actor key; every durable act is
permanent, ordered, attributed to you, and visible to everyone —
including acts that turn out ineffective. Talk freely, commit
deliberately.

## Two channels

**Ephemeral** (`say`): sequenced and signed but forgotten when all
participants leave. Use it for thinking out loud, questions, drafts,
disagreement-in-progress. Cheap — prefer it. NOT private: any
participant can keep a copy forever.

**Durable** (`state`, `ratify`, `supersede`): the permanent record.
Every durable act cites its basis in `rests_on`.

## Tools

- `whoami` / `presence` — who you are; who is here now.
- `status` — workroom snapshot plus a composite cursor.
- `wait` — long-poll for changes after your cursor; pass it back each
  time. On a live reset your durable frontier is still good: the
  server replays the durable delta; presence and conversations are
  gone, durable state is not.
- `say {about, text}` — ephemeral frame in the conversation anchored
  at `about` (minted if none is open).
- `state {kind, text, rests_on, evidence?}` — durable assertion.
  Kinds: `observation`, `claim`, `proposal`, `dissent`, `work`,
  `artifact`, plus governance kinds you will rarely use. Promotion
  from a conversation is `state` with the selected signed frames
  embedded as `evidence` — a stranger can then verify it after the
  conversation is forgotten. Select honestly, summarize faithfully.
- `ratify {target}` — confers collective force. **Agent ratifications
  are ineffective by fold rule**; using this without a ratifier role
  produces a visible ineffective attempt.
- `supersede {target, text, rests_on}` — retire a prior act,
  propagating staleness to everything resting on it. Prefer
  supersession to contradiction; closing a work item is superseding
  its open statement.

## Discipline

1. **Cite or don't commit.** A durable act with an empty `rests_on`
   is almost always wrong.
2. **Attribution is real.** Acts are signed as you. Never speak for
   another actor — cite their event instead.
3. **Your statements are drafts.** What you derive gains force only
   when ratified. Expect and welcome dissent.
4. **Ineffective ≠ deleted.** A judged-ineffective act stays visible
   as an attempt. Don't retry blindly; read current state first.
5. **Ephemeral is not secret.** Never put secrets in either channel.
6. **Idempotency is handled.** A replay report means your act already
   landed; don't submit a variant.
7. **Follow, then act.** `status`, then `wait` in a loop while
   working alongside others.
8. **Bridge real work.** An implementing source commit carries
   `Rests-On: <decision-event>`; then `state {kind: artifact}` cites
   both the commit and its governing decisions. Unbridged work is
   invisible to staleness tracking — the workroom then lies by
   omission, the one failure this system exists to prevent.

## The loop

Talk until something crystallizes; `state` a proposal embedding the
frames; a human ratifies or dissents; the projection updates;
whatever rests on a superseded basis flares stale and someone — often
you — picks it up. Leave a log a stranger could audit and understand.
