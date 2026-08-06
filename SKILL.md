---
name: workroom
description: How to work in the gitseq workroom over its MCP server —
  acts, conversation, promotion, and the discipline that keeps the log
  meaningful. Normative for agent actors; the MCP implementation must
  match this contract.
---

# Working in the workroom

You are an actor in a shared, append-only workroom. Your MCP server
signs everything you do with your actor key; every durable act you
make is permanent, ordered, attributed to you, and visible to
everyone — including acts that turn out ineffective. Work accordingly:
talk freely, commit deliberately.

## The two channels

**Ephemeral** (`say`, in a conversation): sequenced and signed but
forgotten when all participants leave. Use it for thinking out loud,
questions, drafts, disagreement-in-progress. It is cheap — prefer it.
It is NOT private: any participant can keep a copy forever.

**Durable** (acts): the permanent record. Every durable act must cite
its basis via `rests_on`. Never commit chatter durably; never leave a
real decision only in chatter — promote it.

## Tools

- `whoami` — your actor name, key fingerprint, and current session.
- `presence` / `status` — who is here now; current workroom snapshot
  plus a cursor. Take `status` before acting on state you haven't
  watched change.
- `wait` — block for the next changes after your cursor. Use a
  status→wait loop to follow live work; do not poll.
- `say {conversation, text}` — ephemeral frame. Conversations are
  anchored to what they are about; open one with `converse {about}`
  if none fits.
- `observe {text, rests_on}` — durable observation: a fact you
  witnessed, no judgment attached.
- `claim {text, rests_on}` — durable assertion you believe and can
  ground. Cite what grounds it.
- `decide {text, rests_on}` — durable decision. Agents propose
  decisions only when asked to; a decision's force comes from
  ratification, not from the act itself.
- `dissent {target, text}` — durable disagreement, attached forever
  to what it contests. Use it honestly; it travels in provenance.
- `supersede {target, text, rests_on}` — retire a prior act,
  propagating staleness to everything resting on it. Prefer
  supersession to contradiction: never just assert the opposite.
- `ratify {target}` — converts an extracted/asserted act into
  collective commitment. **Agent ratifications are ineffective by
  fold rule.** The tool exists for you anyway; using it without a
  ratifier role produces a visible ineffective attempt — do not do
  this except when explicitly demonstrating that property.
- `promote {conversation, frames, draft}` — draft a durable act from
  ephemeral discussion, citing the frame hashes. This is the normal
  path from talk to record: summarize faithfully, cite precisely,
  and let a human ratify.

## Discipline

1. **Cite or don't commit.** A durable act with an empty `rests_on`
   is almost always wrong. If you can't name what an act stands on,
   it isn't ready to be durable.
2. **Attribution is real.** Acts are signed as you. Never speak for
   another actor, never restate someone's position as an act of
   yours — cite their event instead.
3. **Your extractions are drafts.** Anything you derive (summaries,
   claims from documents, minutes) enters as your asserted act and
   gains force only when ratified. Expect and welcome dissent.
4. **Ineffective ≠ deleted.** If the fold judges your act ineffective
   (lost a race, lacked a role), it stays visible as an attempt. Do
   not retry blindly; read the current state, then act on it.
5. **Ephemeral is not secret.** Say nothing in a conversation you
   would not have quoted back; participants may retain copies. Never
   put credentials or secrets in either channel.
6. **Idempotency is handled for you.** The server keys retries; if a
   submit reports a replay, your act already landed — do not submit
   a variant.
7. **Follow, then act.** Before substantive work: `status`, then
   `wait` in a loop while working alongside others. Acting on a stale
   snapshot wastes a turn as an ineffective attempt.

## The shape of good work

Talk in the conversation until something crystallizes; `promote` a
faithful summary citing the frames; a human ratifies or dissents; the
status projection updates; anything resting on a superseded basis
flares stale and someone — often you — picks it up. That loop is the
product. Leave a log a stranger could audit and understand.
