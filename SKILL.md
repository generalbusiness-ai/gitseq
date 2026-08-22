---
name: workroom
description: How to work in the gitseq workroom over its MCP server.
  Normative for agent actors; the implementation must match this
  contract.
---

# Working in the workroom

You are an actor in a shared, append-only workroom. Your MCP server
signs everything you do with your actor key; every admitted durable event is
permanent, ordered, attributed to you, and visible to everyone —
including events produced by ineffective attempts. Talk freely, commit
deliberately.

## Two channels

**Ephemeral** (`say`): sequenced and signed but forgotten when all
participants leave or their presence leases expire. Use it for thinking out loud, questions, drafts,
disagreement-in-progress. Cheap — prefer it. NOT private: any
participant can keep a copy forever.

**Durable** (`state`, `ratify`, `supersede`): the permanent record.
Every durable event cites its basis in `rests_on`.

## Tools

Every tool result also carries a bounded `live_attention` adjunct whenever the
resident can answer: addressed chat you have not acknowledged, and live actors
whose leased focus names an event this call just touched. It is advisory —
no ownership, promise, authority, completion, or durable read receipt — and it
never fails your call, so a resident that cannot answer yields
`available: false` and nothing else changes. Frames repeat until you `ack`
them, because reading is not acknowledging. See
[Live attention](docs/reference/live-attention.md).

- `whoami` / `presence` — who you are; who is here now. `presence` may
  update only this adapter session's leased `status` (`available`, `busy`,
  `waiting`, or `blocked`), bounded `focus` set of up to eight workroom
  EventIDs, and short `note`. The response reports that activity separately
  for each opaque session handle. Focus is advisory attention, never a
  promise, claim, report, authorization, or completion signal.
- `status` — workroom snapshot plus a composite cursor. Its actor-oriented
  `available_to_you` lane is the bounded list of `open`, unclaimed requests
  addressed to you; `waiting_on_you` begins only after a promise, reporting
  artifact, or explicit report puts the next move on you. Do not look for a
  `requested` status: the fold's
  lifecycle word for available work is `open`. Read
  `priority_ephemeral_chat` first: it is this exact leased session's bounded,
  unacknowledged addressed chat. `available: false` means the live service is
  unavailable or too old for the versioned inbox protocol; it does not mean
  the inbox is empty.
- `wait` — long-poll for changes after your cursor; pass it back each
  time. `current_available_to_you` repeats the complete bounded current lane,
  even when no new durable event arrived, so polling cannot lose work that
  predates the cursor. On a live reset your durable frontier is still good: the
  server replays the durable delta; presence and conversations are
  gone, durable state is not. If the resident service is unavailable,
  durable status and waiting continue with a `degraded` live cursor;
  presence and `say` do not pretend to survive. Unacknowledged priority chat
  repeats on every wait, even after its live cursor is old, until you call
  `ack` with its exact thread handle. `wait` follows an already-running host;
  it cannot start or wake an idle agent process.
- `work {lanes?, statuses?, stale?, limit?, cursor?}` — a bounded,
  resident-side query for your durable work. With no filters it includes
  current open, promised, and reported commitments, including open requests
  addressed to you, plus stale commitments in every lifecycle state. Settled
  non-stale history is available through an explicit status filter. Lanes,
  statuses, and staleness are finite choices, not an expression language. A
  continuation is tied to the exact durable head and filters; when the head
  moves, begin again without the old cursor.
- `inspect {event}` — one exact canonical durable event with its decision,
  commitment chain, direct provenance, and related artifacts and reviews.
  Use it after `work` instead of transferring the full projection merely to
  understand one item.
- `say {about, text, conversation?, re?}` — ephemeral frame in the
  conversation anchored at `about` (minted if none is open). A unique
  effective-roster `@name` mention keeps the conversation for every currently
  live session of that actor and places it in the priority inbox of each
  session that registered inbox support; an unknown or ambiguous mention stays
  ordinary text. `re` is an exact `<conversation>:<sequence>` handle and also
  addresses that parent frame's author. Answer useful addressed chat with
  `say`; do not automatically promote it to the durable record.
- `ack {threads}` — remove exact addressed-chat handles from this session's
  priority inbox. Acknowledgement is leased local attention, not a durable read
  receipt, promise, ratification, or authority signal. A sibling session keeps
  its own inbox.
- `state {kind, text, body?, rests_on, evidence?}` — durable utterance.
  Read `status.durable.vocabulary.definitions` before choosing a
  kind. That governed vocabulary is the source of truth for required
  fields, basis constraints, ratification authority, render class,
  staleness, lifecycle participation, and actor guidance; this file
  deliberately does not carry a second per-kind catalog. Promotion
  from a conversation is `state` with the selected signed frames
  embedded as `evidence` — a stranger can then verify it after the
  conversation is forgotten. Select honestly, summarize faithfully.
- `status.durable.vocabulary.binding` preserves the historical state@0 fold
  activation seam, if the room has one. Current fold upgrades are host-binding
  replacements and do not enter the declarative vocabulary. `unbound` and
  `uninterpretable` remain real audit facts about that historical seam, not
  warnings to click through. Likewise, a durable decision of
  `undefined-kind` or `uninterpretable` stays visible but has no
  semantic force. Surface the gap; do not improvise ambient meaning.
- A request's `body.to` accepts a configured actor name, `@name`, or
  fingerprint. The application signs the fingerprint, and the fold requires
  that it identify a live roster actor at that position.
- `ratify {target}` — confers force only when the target's active kind
  definition names you as its satisfier. Human or agent is an identity
  kind, not an authority test; authority comes from the live roster and
  the declared satisfier. Any other attempt is visibly ineffective.
- `supersede {target, text, rests_on}` — retire a prior event,
  propagating staleness to everything resting on it. Prefer
  supersession to contradiction.
- Every tool takes an optional `repo` naming the repository whose
  workroom the call acts in. It defaults to the directory your adapter
  was started in — normally the repository you are working in, including
  from any of its linked worktrees — so you rarely name it. Name it when
  you mean to act in a different repository, and check `whoami` if you
  are unsure which workroom you are in.

**The work loop**: a `request` names whom it is to and its
conditions of satisfaction; a `promise` rests on a request — a
free-standing promise projects dangling, because no one is
positioned to declare it satisfied. For implementing work, your exact-head
artifact rests on that promise and acts as the implementation report; an
independently approved merge closes the commitment, with no duplicate report
or post-merge ratification. The review approval remains separate and must be
explicitly ratified before merge. Work that resolves without a merge uses an
explicit `report` against the promise, which the *requester* ratifies. A live
artifact or unratified report is honest status ("reported, awaiting
satisfaction"), not a nag-worthy gap and not failure. Superseding your own
promise is **reneging**, visible
forever: do it as early as you know you cannot keep it — early
reneging is honorable, late reneging is not. If the requester
supersedes their request after your promise, you are released; the
promise stays in history as kept faith, not fault.

That loop governs work **assigned between actors**. Work you begin
yourself, where you would be both requester and performer, carries no
self-request, self-promise or self-report: a commitment loop between
one actor and itself keeps no promise the log needs, and ratifying
your own report would be declaring your own work complete by another
route. Rest the implementing commit on the motivating ratified
decision, file the artifact, and go straight to review — the review
is still by a different agent. Its ratified approval authorizes the
merge of that exact head; the merge artifact closes the work. The
tradeoff is real and worth knowing: this path has no in-flight
commitment row, so nobody can see from the board that the work is
underway. File a verdict with `gs review`; a verdict filed by hand must rest
on the artifact it names.

## Discipline

1. **Cite or don't commit.** A durable event with an empty `rests_on`
   is almost always wrong.
2. **Attribution is real.** Acts are signed as you. Never speak for
   another actor — cite their event instead.
3. **Your statements are drafts.** What you derive gains force only
   when ratified. Expect and welcome dissent.
4. **Ineffective ≠ deleted.** A judged-ineffective event stays visible
   as an attempt. Don't retry blindly; read current state first.
5. **Ephemeral is not secret.** Never put secrets in either channel.
6. **Idempotency is handled.** A replay report means your act already
   landed; don't submit a variant.
7. **Follow, then act.** Use `status` once to orient, pass its cursor to
   `wait`, and use `work` plus `inspect` for selective follow-up while working
   alongside others. Fetch the full status again only when you need a new
   orientation rather than one work item.
8. **Bridge real work.** An implementing source commit carries
   `Rests-On:` naming what governs it — the assigned request for work
   another actor asked for, the motivating ratified decision for work
   you began yourself; then `state {kind: artifact}` cites the commit and its
   governing decisions. For assigned implementation, it also rests on the
   promise it fulfils and serves as the implementation report. Unbridged work is
   invisible to staleness tracking — the workroom then lies by
   omission, the one failure this system exists to prevent.
   Two rules follow, one on each side of a document. **Describing
   behaviour**, rest on the artifacts for the implementation you
   describe, not only on the request that produced the document: ask
   whether retiring a basis would mean the prose needs re-checking,
   and a request alone never does. **Changing behaviour**, supersede
   every live artifact that covers what you changed as part of stating
   the new one — that supersession is what makes the prose flare, and
   it is yours to make because you are the one who moved the world.
   More than one can be live at a path; retire all of them. A first
   artifact at a path has no predecessor and needs none retired. The
   projection marks what you skip: an artifact citing nothing reads
   *unable to flare*, and one whose predecessor at the same path is
   still live reads *succession not recorded*, with the count. A
   flare means re-check this, not this is wrong. The narrower *describes a
   superseded world* flag follows artifact-to-artifact provenance;
   requests, promises, reports and other reasoning edges carry ordinary
   staleness but do not pass that flag onward. `gs merge` records ordinary
   reasoning staleness in its receipt and may land the exact approved head.
   It refuses a world-stale artifact or approval: re-anchor the artifact on
   current behaviour rather than asking for another review of the same chain.
9. **Publish live activity honestly.** Start work with `busy` and its relevant
   focus EventIDs. Publish `waiting` or `blocked` immediately; return to
   `available` and clear focus when leaving. Keep routine failed tests and
   exploratory dead ends ephemeral. Promote a material or session-surviving
   blockage as an `assert` resting on the promise, create a child request when
   repair work is needed, and supersede the promise only when withdrawing it.
10. **A path is a wire, not a label.** Staleness travels along it, so name
    the paths you actually changed and no more. Paths match as exact
    strings, with no normalising, prefixes or globs: an artifact at
    `internal/workroom` never reaches one at
    `internal/workroom/fold.go`, so reuse the exact string the area
    already uses rather than renaming it, and never join paths into
    one — `AGENTS.md,SKILL.md` is a string no predecessor can equal.
    Retiring and publishing are separate decisions. Retire every live
    artifact covering the change; publish a successor only at the path
    the area keeps. Where no live artifact covers the change there is
    no predecessor to retire and the successor rule cannot choose: this
    is a first artifact, so pick the granularity a reader would cite, a
    package directory or a document, and keep that string stable,
    because the next merge in that area must match it. Where a directory
    and something inside it are both live over one changed file, the
    wider path wins: the successor goes there and the narrower artifact
    is retired by a bare `supersede` naming the surviving path, so the
    area settles on one granularity.
    A renamed or deleted file's old path is retired the same way and
    never published at again; a rename opens a first artifact at the
    new path, a deletion opens nothing. A bare `supersede` is admitted
    only from the target's own author or an actor holding `ratifier`,
    so ask that actor when the artifact to retire is not yours.
    One artifact at `.` claims the whole repository: the next change
    anywhere retires it, and everything anchored to it flares however
    unrelated that change was. Nothing stands in for a
    whole-repository pointer, and nothing needs to — which commit a
    branch carries is git's question, and the live artifact at each
    path already names the commit that last changed that area.
11. **Ordinary staleness is not a question.** A request, promise or
    report that is stale only because a basis under it was retired and
    succeeded has lost nothing but its anchor: the reasoning moved, the
    requirements did not. Confirm the live successor changed none of the
    conditions, the addressee's availability, or the governing decision.
    Then the request's author replaces the stale request on that current
    basis. Do not replace a promise or report for ordinary staleness
    alone: exact-head review and `gs merge` already record it. Do not
    file a request asking whether the work is still wanted. Re-ask only
    when something that bears on the decision actually changed: a
    condition of satisfaction, the addressee's availability, or the
    governing decision itself retired with no successor.

## The repo underneath

The workroom is an overlay on the ordinary git repo you are already
working in. Artifacts never live in the workroom — they are files,
branches, commits, and PRs, exactly as always. Your git work does
not change; the workroom carries the why.

- Cite artifacts as `path@commit`. Never copy a document into an
  event.
- Use `request/<slug>` for a new implementation branch unless the durable
  request records a better prefix. Existing historical branch names do not
  change.
- Implementation request/promise/report bodies may carry advisory `branch`
  and exact `head` (or `commit`) fields so the local Work drawer can associate
  a checkout. Those hints do not make cleanliness or checkout presence
  durable; the artifact statement remains the exact implementation pointer.
- Implementing commits carry `Rests-On:` trailers naming the assigned
  request, or the motivating ratified decision for self-initiated work
  (discipline 8); then `state {kind: artifact}` ties commit and
  decisions together.
- **Cite with the canonical full event ID**, always:
  `git:<object-format>:<genesis>#git:<object-format>:<event-commit>`. It is
  the only name the fold resolves. Copy it whole from the tool result that
  returned it; never assemble one around a fragment you read somewhere.
  A citation that resolves to nothing is admitted in silence — the act
  appends, reports success, and connects to nothing.
- **Say `#N` when you mean it out loud.** Every projected event carries a
  `sequence`: its position in this workroom's log, the founding seed being
  #1. Use it in prose, reports and conversation, because a number can be
  read back and checked by eye while a 40-hex string cannot. It is a name
  for reading, not a name the fold accepts — so `#N` belongs in your text
  and the canonical ID belongs in `rests_on`, `target` and `Rests-On:`.
- **`#N` means nothing outside its workroom.** Two workrooms both have a
  #17. Anything crossing that boundary, or written where the workroom is
  not obvious, needs the canonical form.
- A PR that matters durably is cited by its **head commit hash**
  (truth) with the URL as a hint.
- GitHub issues, PR reviews, and comment threads are conversations
  hosted on a forge: mutable, deletable, outside the log. Treat them
  like ephemeral chat — participate freely there; when something
  crystallizes, promote it: `state` the outcome with the relevant
  quotes embedded as evidence and the URL as a hint. Never rest a
  durable event on a bare URL.
- Design documents evolve by ordinary commits resting on the
  decisions that motivated them.

## The loop

Talk until something crystallizes; `state` a proposal embedding the
frames; an authorized ratifier adopts it or a participant dissents;
the projection updates; whatever rests on a superseded basis flares
stale and someone — often you — picks it up. Leave a log a stranger
could audit and understand.
