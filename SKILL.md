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
participants leave or their presence leases expire. Use it for thinking out loud, questions, drafts,
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
  gone, durable state is not. If the resident service is unavailable,
  durable status and waiting continue with a `degraded` live cursor;
  presence and `say` do not pretend to survive.
- `say {about, text}` — ephemeral frame in the conversation anchored
  at `about` (minted if none is open).
- `state {kind, text, body?, rests_on, evidence?}` — durable utterance.
  Kinds are speech acts: `assert` (a claim you can ground),
  `propose` (seeks ratification), `request` (asks an actor to act),
  `promise` (an undertaking — never one you can't keep), `report`
  (claims completion of your promise), `dissent` (objection, resting
  on what it contests), `artifact` (cites `path@commit`), plus
  governance kinds you will rarely use. Promotion from a
  conversation is `state` with the selected signed frames embedded
  as `evidence` — a stranger can then verify it after the
  conversation is forgotten. Select honestly, summarize faithfully.
- A request's `body.to` accepts a configured actor name, `@name`, or
  fingerprint. The application signs the fingerprint, and the fold requires
  that it identify a live roster actor at that position.
- `ratify {target}` — confers force when you hold the target-specific
  authority: the requester for a report, a ratifier for assertions,
  proposals, and governance. Human or agent is an identity kind, not an
  authority test; an agent with a live ratifier grant may ratify. Any other
  attempt is visibly ineffective.
- `supersede {target, text, rests_on}` — retire a prior act,
  propagating staleness to everything resting on it. Prefer
  supersession to contradiction.

**The work loop**: a `request` names whom it is to and its
conditions of satisfaction; a `promise` rests on a request — a
free-standing promise projects dangling, because no one is
positioned to declare it satisfied. You `report` against your
promise; the *requester* ratifies the report — never declare your
own work complete. An unratified report is honest status
("reported, awaiting satisfaction"), not a nag-worthy gap and not
failure. Superseding your own promise is **reneging**, visible
forever: do it as early as you know you cannot keep it — early
reneging is honorable, late reneging is not. If the requester
supersedes their request after your promise, you are released; the
promise stays in history as kept faith, not fault.

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

## The repo underneath

The workroom is an overlay on the ordinary git repo you are already
working in. Artifacts never live in the workroom — they are files,
branches, commits, and PRs, exactly as always. Your git work does
not change; the workroom carries the why.

- Cite artifacts as `path@commit`. Never copy a document into an
  event.
- Implementation request/promise/report bodies may carry advisory `branch`
  and exact `head` (or `commit`) fields so the local Work drawer can associate
  a checkout. Those hints do not make cleanliness or checkout presence
  durable; the artifact statement remains the exact implementation pointer.
- Implementing commits carry `Rests-On: <decision-event>` trailers
  (discipline 8); then `state {kind: artifact}` ties commit and
  decisions together.
- Always use the canonical full event ID:
  `git:<object-format>:<genesis>#git:<object-format>:<event-commit>`.
- A PR that matters durably is cited by its **head commit hash**
  (truth) with the URL as a hint.
- GitHub issues, PR reviews, and comment threads are conversations
  hosted on a forge: mutable, deletable, outside the log. Treat them
  like ephemeral chat — participate freely there; when something
  crystallizes, promote it: `state` the outcome with the relevant
  quotes embedded as evidence and the URL as a hint. Never rest a
  durable act on a bare URL.
- Design documents evolve by ordinary commits resting on the
  decisions that motivated them.

## The loop

Talk until something crystallizes; `state` a proposal embedding the
frames; an authorized ratifier adopts it or a participant dissents;
the projection updates; whatever rests on a superseded basis flares
stale and someone — often you — picks it up. Leave a log a stranger
could audit and understand.
