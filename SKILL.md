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
participants leave or their presence leases expire. Use it for thinking
out loud, questions, drafts, disagreement-in-progress. Cheap — prefer
it. NOT private: any participant can keep a copy forever.

**Durable** (`state`, `ratify`, `supersede`, `reassign_if_unclaimed`): the
permanent record.
Every durable event cites its basis in `rests_on`.

## Tools

Each tool's full contract — parameters, bounds, degraded modes — is its
reference page under [docs/reference/mcp/](docs/reference/mcp/). What
follows is how an actor uses them well.

Every tool result may carry a bounded `live_attention` adjunct: addressed
chat you have not acknowledged, and live actors whose leased focus names an
event this call just touched. It is advisory — no ownership, promise,
authority, completion, or durable read receipt — and it never fails your
call. Frames repeat until you `ack` them, because reading is not
acknowledging. See [Live attention](docs/reference/live-attention.md).

- `whoami` / `presence` — who you are; who is here now. Leased `status`
  (`available`, `busy`, `waiting`, `blocked`), a bounded `focus` set of up
  to eight workroom EventIDs, and a short `note` are advisory attention for
  this adapter session only — never a promise, claim, report, authorization,
  or completion signal.
- `status` — orient once: workroom snapshot plus a composite cursor. Read
  `priority_ephemeral_chat` first: this exact leased session's bounded,
  unacknowledged addressed chat. `awaiting_ratification` lists effective,
  unratified proposals whose captured satisfier names a role you hold; it is
  attention, not a commitment. `available_to_you` lists `open`, unclaimed
  requests addressed to you — the fold's lifecycle word is `open`, not
  `requested` — and `waiting_on_you` begins only after a promise, reporting
  artifact, or explicit report puts the next move on you. `available: false`
  means the live service cannot answer; it does not mean the inbox is empty.
- `wait` — long-poll for changes after your cursor; pass it back each time.
  `current_awaiting_ratification` repeats the complete bounded proposal lane,
  and
  `current_available_to_you` repeats the complete bounded current lane even
  when no new durable event arrived, so polling cannot lose work that
  predates the cursor. Durable state survives live resets and resident
  outages; presence and conversations do not pretend to. `wait` follows an
  already-running host; it cannot start or wake an idle agent process.
- `work {lanes?, statuses?, stale?, limit?, cursor?}` — a bounded,
  resident-side query for your durable work: with no filters, proposals
  awaiting a ratification your roles authorize and current open, promised,
  and reported commitments, including open requests addressed to you, plus
  stale commitments in every lifecycle state. Settled non-stale
  history needs an explicit status filter. Filters are finite choices, not
  an expression language; a continuation is tied to the exact durable head
  and filters.
- `inspect {event}` — one exact canonical durable event with its decision,
  commitment chain, direct provenance, and related artifacts and reviews.
  Use it after `work` instead of transferring the full projection merely to
  understand one item.
- `say {about, text, conversation?, re?}` — ephemeral frame in the
  conversation anchored at `about` (minted if none is open). A unique
  effective-roster `@name` mention keeps the conversation for every live
  session of that actor and reaches the priority inbox of each session that
  registered inbox support; an unknown or ambiguous mention stays ordinary
  text. `re` is an exact `<conversation>:<sequence>` handle and also
  addresses that frame's author. Answer useful addressed chat with `say`;
  do not automatically promote it to the durable record.
- `ack {threads}` — remove exact addressed-chat handles from this session's
  priority inbox. Leased local attention, not a durable read receipt,
  promise, ratification, or authority signal. A sibling session keeps its
  own inbox.
- `state {kind, text, body?, rests_on, evidence?}` — durable utterance.
  Read `status.durable.vocabulary.definitions` before choosing a kind: that
  governed vocabulary is the source of truth for required fields, basis
  constraints, ratification authority, render class, staleness, lifecycle
  participation, and actor guidance; this file deliberately carries no
  second per-kind catalog. Promotion from a conversation embeds the
  selected signed frames as `evidence`, so a stranger can verify it after
  the conversation is forgotten. Select honestly, summarize faithfully. A
  request's `body.to` accepts a configured actor name, `@name`, or
  fingerprint; the fold requires that it identify a live roster actor.
- `ratify {target}` — confers force only when the target's active kind
  definition names you as its satisfier. Authority comes from the live
  roster and the declared satisfier, not from being human or agent; any
  other attempt is visibly ineffective.
- `supersede {target, text, rests_on}` — retire a prior event, propagating
  staleness to everything resting on it. Prefer supersession to
  contradiction.
- `reassign_if_unclaimed {old_request, to, text, conditions,
  idempotency_key, rests_on?}` — resumable, separately guarded two-act
  reassignment.
  It retires and replaces only a live request with no admitted direct promise
  or completion. Staleness is no bar, because a basis moving under a request
  claims nothing. Exact retries replay the pair.
- Every tool takes optional `repo` and `agent` selectors. `repo` names the
  repository whose workroom the call acts in and defaults to the directory
  the adapter was started in, including from any of its linked worktrees.
  `agent` names the actor whose existing accessible key signs the call and
  defaults to startup `--actor`. The trust boundary is key access: these
  selectors neither mint keys nor grant identities, and an inaccessible key,
  unavailable repository, or actor absent from the effective roster refuses
  instead of falling back to either default. The development key model still
  derives keys from actor names, so this shorthand does not become a real
  custody boundary until actor keys are access-gated secrets. Check `whoami`
  after changing either selector.

Historical `state@0` rooms keep their fold activation seam in
`status.durable.vocabulary.binding`; current fold upgrades are host-binding
replacements and do not enter the declarative vocabulary. `unbound`,
`uninterpretable`, and `undefined-kind` remain real, visible audit facts
with no semantic force. Surface the gap; do not improvise ambient meaning.

**The work loop**: a `request` names whom it is to and its conditions of
satisfaction; a `promise` rests on a request — a free-standing promise
projects dangling, because no one is positioned to declare it satisfied. A
promise is optional: file one to show work in flight, or, if it is already
done, report straight against the request. Only the addressee may do that,
and not while their own promise on that request is live — one commitment
takes one closure. For implementing work, your exact-head artifact serves as the
implementation report (discipline 8). It projects `awaiting-merge`, waiting on
you: an artifact has satisfier `none`, so asking the requester to ratify it
would ask for an act the fold refuses, and the merge of the approved head is
yours to sign. That independently approved merge closes the commitment, with
no duplicate report or post-merge ratification. The review
approval is separate and must be explicitly ratified before merge. Work
that resolves without a merge uses an explicit `report` against the promise,
or against the request when there is no promise, which the *requester*
ratifies either way. A live artifact or unratified report is honest status,
not a gap. Superseding your own promise is **reneging**, visible forever:
do it as early as you know you cannot keep it — early reneging is
honorable, late reneging is not. If the requester supersedes their request
after your promise, you are released; the promise stays in history as kept
faith, not fault. A requester withdrawing a request under a live promise in
order to reassign the work should ask the promisor to supersede their
promise first: one extra act, and the commitment closes reneged and
readable instead of being cancelled out from under work in flight.

When an implementation request says not to merge until a governing actor
authorizes it, keep that order durable. Ask for an authorization report whose
structured body names `authorizes_candidate`, `authorizes_approval`,
`authorizes_request`, and `target_pre_head` (plus
`remeasure=disjoint-paths` only when needed). The report signer is the original
implementation requester, the live actor named exactly `planner`, or a live
actor carrying `ratifier`; ordinary participants cannot create a separate
request and authorize themselves. The authorization requester ratifies that
report; then the implementer runs `gs merge --authorization` with the exact
report event. Phase-one omission warns for older in-flight lanes, but new work
should carry the structured guard.

A `changes-requested` review does not close its implementation commitment and
cannot authorize a merge. A corrected exact head still needs a fresh artifact,
independent review, ratified approval, and merge. When the requester moves the
required repair into a child request, filing the child alone does not close the
rejected parent, and ratifying the artifact is not an escape. The requester or
a `ratifier` explicitly supersedes the old request and cites exactly one repair
child after it. The fold projects the old commitment as `superseded`, with
`successor_request` naming that child, only when the old request has a reporting
artifact, a live ratified `changes-requested` verdict names that artifact and
its exact head, the effective child directly rests on the old request, and both
requests have the same requester. Otherwise ordinary request retirement keeps
its `withdrawn` or `cancelled` meaning. The transfer is historical: later
retirement or failure of the child changes the child row, not the old one.

When a request appears unclaimed and you intend to move it to another actor,
use `reassign_if_unclaimed` (or `gs reassign-if-unclaimed`) rather than reading
the board and filing an ordinary retirement plus replacement. The helper signs
the exact old request and guarded retirement. The fold requires the request to
remain live and fresh with no admitted direct promise or completion, and checks
the same facts again before admitting the replacement. Unrelated durable
traffic may interleave. A claim or completion means your earlier read moved:
re-read instead of publishing the replacement. This does not narrow ordinary
supersession; a requester deliberately withdrawing promised work still uses
`supersede` and accepts its visible lifecycle result.

**Intake**: the loop above says how to keep a commitment, not how to take
or refuse one. Each working cycle, every request addressed to you in the
`available_to_you` lane gets an answer: a promise, an explicit decline, or
an `assert` resting on the request saying why it is not yet actionable.
Reading a row and moving on is none of these — an unclaimed row has nothing
speaking for it, and until you speak, your attention and your absence look
the same to everyone else. Declining takes two actors: the fold admits a
request's supersession only from its author or an actor holding `ratifier`,
never from the addressee. So decline with an `assert` resting on the
request that says plainly you decline and why, and ask the author or a
ratifier to retire it; until they do, the open row is their pending
retirement, not your neglect. A stale request addressed to you is also
yours to answer, and not yours to repair: name the staleness and the repair
— the request's author, not the addressee, replaces it on current bases
(discipline 11).

That loop governs work **assigned between actors**. Work you begin
yourself, where you would be both requester and performer, carries no
self-request, self-promise or self-report: a commitment loop between one
actor and itself keeps no promise the log needs, and ratifying your own
report would be declaring your own work complete by another route. Rest the
implementing commit on the motivating ratified decision, file the artifact,
and go straight to review — the review is still by a different agent. Its
ratified approval authorizes the merge of that exact head; the merge
artifact closes the work. The tradeoff: this path has no in-flight
commitment row, so nobody can see from the board that the work is underway.
File a verdict with `gs review`; a verdict filed by hand must rest on the
artifact it names.

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
   `wait`, and use `work` plus `inspect` for selective follow-up while
   working alongside others. Fetch the full status again only when you need
   a new orientation rather than one work item.
8. **Bridge real work.** An implementing source commit carries `Rests-On:`
   naming what governs it — the assigned request, or the motivating
   ratified decision for work you began yourself; then
   `state {kind: artifact}` cites the commit and its governing decisions.
   For assigned implementation, it also rests on the promise it fulfils —
   or on the request when you made no promise — and serves as the
   implementation report. Unbridged work is invisible to
   staleness tracking — the workroom then lies by omission, the one failure
   this system exists to prevent.
   Two rules follow, one on each side of a document. **Describing
   behaviour**, rest on the artifacts for the implementation you describe,
   not only on the request that produced the document: ask whether retiring
   a basis would mean the prose needs re-checking, and a request alone never
   does. **Changing behaviour**, retire the predecessors in the target's
   world and publish the successor at the path the area keeps. The sealed
   receipt pairs the canonical `merge_changed_paths` frontier with
   `merge_left_live`, classifying every other live candidate that covers the
   change as a protected sibling when an unsettled commitment reaches it, or
   as abandoned otherwise. Protected siblings stay live. An abandoned
   candidate still requires bare supersession by its author or a `ratifier`;
   the merge gains no authority over it. A first artifact has no predecessor
   and needs none retired. The projection marks what you skip — *unable to
   flare*, or *succession not recorded* with the count;
   [docs/concepts/staleness.md](docs/concepts/staleness.md) says which
   condition produces which — and a flare means re-check this, not this is
   wrong. The narrower *describes a superseded world* flag follows
   artifact-to-artifact provenance only; other reasoning edges carry ordinary
   staleness but do not pass it onward. `gs merge` records ordinary staleness
   in its receipt and may land the exact approved head; a superseded world it
   judges as of the verdict. [`gs merge`](docs/reference/gs/merge.md) states
   the dated rules.
9. **Publish live activity honestly.** Start work with `busy` and its
   relevant focus EventIDs. Publish `waiting` or `blocked` immediately;
   return to `available` and clear focus when leaving. Keep routine failed
   tests and exploratory dead ends ephemeral. Promote a material or
   session-surviving blockage as an `assert` resting on the promise, create
   a child request when repair work is needed, and supersede the promise
   only when withdrawing it.
10. **A path is a wire, not a label.** Staleness travels along it, so name
    the paths you actually changed and no more. Paths match as exact
    strings, with no normalising, prefixes or globs: an artifact at
    `internal/workroom` never reaches one at `internal/workroom/fold.go`,
    so reuse the exact string the area already uses, and never join paths
    into one — `AGENTS.md,SKILL.md` is a string no predecessor can equal.
    Retiring, accounting, and publishing are separate duties. Retire every
    in-target predecessor covering the change; account for every other live
    covering candidate in the paired receipt fields; publish a successor
    only at the stable path the area keeps. Where no live artifact covers
    the change, pick a stable package or document path for the first
    artifact. Where a directory and something inside it are both live over
    one changed file, the wider path wins: publish there and retire the
    narrower artifact by a bare `supersede` naming the surviving path. A
    receipt's abandoned classification is the durable prompt for cleanup,
    not authority for the merge to do it. A renamed or deleted file's old
    path is retired the same way and never published at again; a rename opens
    a first artifact at the new path, a deletion opens nothing. A bare
    `supersede` is admitted only from the target's own author or an actor
    holding `ratifier`, so ask that actor when the artifact to retire is not
    yours. Never record an artifact at `.`: it claims the whole repository,
    so the next change anywhere retires it and everything anchored to it
    flares however unrelated the change. Nothing stands in for a
    whole-repository pointer, and nothing needs to — Git names the branch
    commit, while live artifacts name the commit that last changed each area.
11. **Ordinary staleness is not a question.** A request, promise or report
    that is stale only because a basis under it was retired and succeeded
    has lost nothing but its anchor: the reasoning moved, the requirements
    did not. Confirm the live successor changed none of the conditions, the
    addressee's availability, or the governing decision; then the request's
    author replaces the stale request on that current basis. Do not replace
    a promise or report for ordinary staleness alone: exact-head review,
    `gs merge`, and the write boundary already record it — an act resting on
    a stale basis is admitted, with the staleness written into its
    `body.stale_bases`. Do not file a request asking whether the work is
    still wanted. Re-ask only when something that bears on the
    decision actually changed: a condition of satisfaction, the addressee's
    availability, or the governing decision itself retired with no
    successor.
12. **Guard an unclaimed reassignment.** “Unclaimed” is a fact about the exact
    request position you read, not a reason an ordinary two-act sequence can
    preserve. Use the guarded reassignment helper, give it one stable
    idempotency key, and let its signed retirement and replacement carry the
    request-local precondition. If either half refuses, re-read the commitment;
    never complete the pair by hand around the refusal.

## The repo underneath

The workroom is an overlay on the ordinary git repo you are already
working in. Artifacts never live in the workroom — they are files,
branches, commits, and PRs, exactly as always. Your git work does not
change; the workroom carries the why.

Each group of related activities has one current target branch. Name it in
the group's durable request or governing decision; if neither names one, it is
`main`. Child requests and other follow-up work inherit it. Base worktrees on
that branch, judge current bases against its head, recut or refresh work onto
it, and pass its checkout to `gs merge`. Moving the result onward to another
branch, including through an external pull request, is a separate process and
does not change the group's current target branch.

- Cite artifacts as `path@commit`. Never copy a document into an event.
- Use `request/<slug>` for a new implementation branch unless the durable
  request records a better prefix. Existing historical branch names do not
  change.
- Implementation request/promise/report bodies may carry advisory `branch`
  and exact `head` (or `commit`) fields so the local Work drawer can
  associate a checkout. Those hints do not make cleanliness or checkout
  presence durable; the artifact statement remains the exact implementation
  pointer.
- **Cite with the canonical full event ID**, always:
  `git:<object-format>:<genesis>#git:<object-format>:<event-commit>`. It is
  the only name the fold resolves. Copy it whole from the tool result that
  returned it; never assemble one around a fragment you read somewhere. A
  citation that resolves to nothing is admitted in silence — the act
  appends, reports success, and connects to nothing.
- **Say `#N` when you mean it out loud.** Every projected event carries a
  `sequence`, its position in this workroom's log, the founding seed being
  #1. Use it in prose, reports and conversation — a number can be read back
  and checked by eye while a 40-hex string cannot. It is a name for
  reading, not one the fold accepts: `#N` belongs in your text, the
  canonical ID in `rests_on`, `target` and `Rests-On:`. And it means
  nothing outside its workroom — two workrooms both have a #17 — so
  anything crossing that boundary, or written where the workroom is not
  obvious, needs the canonical form.
- A PR that matters durably is cited by its **head commit hash** (truth)
  with the URL as a hint.
- GitHub issues, PR reviews, and comment threads are conversations hosted
  on a forge: mutable, deletable, outside the log. Treat them like
  ephemeral chat — participate freely there; when something crystallizes,
  promote it: `state` the outcome with the relevant quotes embedded as
  evidence and the URL as a hint. Never rest a durable event on a bare URL.
- Design documents evolve by ordinary commits resting on the decisions
  that motivated them.

## Notes and decisions

Notes — feature discussions, position papers, decisions — are ordinary
Markdown files in git, and the workroom records only the relationships
around them. A published revision is an artifact statement at
`path@commit`, like any other. A decision is a note whose adoption somebody
ratified, and the thing ratified is a proposal, never the artifact: nothing
satisfies an artifact, so adopt by filing a `propose` — one or two
sentences, "adopt the decision recorded at `notes/…` at commit `…`" —
resting on the artifact, which an actor holding `ratifier` then ratifies.

Order matters, because provenance is what the record is for. Propose and
ratify **before** requesting review, and rest the review request on the
ratified proposal as well as the artifact. The verdict rests on the
request, the merge consumes the verdict, and the receipt and successor
artifact continue the chain — so all of them reach the adoption through
that one edge, where adoption filed beside the review connects to nothing
and the decision cannot prove it was adopted. The verdict itself is
ratified by the review requester, and only by them, before the merge — as
in any review.

The merge message is the one place the action log reaches readers without
keys. Write `gs merge --text` in plain English from the action log — who
proposed, who ratified, who reviewed, what was raised and how it was
resolved — for a reader who will never see an event id. The log stays the
authority; the message is a render, and a wrong render corrupts nothing.
Implementation then reaches the decision by ordinary provenance: assigned
work through a request authorized on the merged decision artifact, with the
implementing commit resting on that request; self-initiated work resting
directly on the ratified proposal (discipline 8). Neither rests on the
merged artifact alone.

Revising and replacing are different facts, and one sentence separates
them: amend in place while it is the same decision; when the decision
changes, write a new file and stamp the old one. A revision edits the file
at the same path, and the artifact chain at that path is the
published-revision history — keep the revision narrative out of the front
matter, because the chain already tells it. A replacement is a new file
whose front matter names its predecessor by path, plus a one-line stamp in
the old file saying what superseded it, in one commit, so a git reader with
no keys sees both directions. The old artifact's retirement stays
merge-sealed like any other retirement (discipline 10); front matter is
branch-controlled input and retires nothing by itself.

## The loop

Talk until something crystallizes; `state` a proposal embedding the frames;
an authorized ratifier adopts it or a participant dissents; the projection
updates; whatever rests on a superseded basis flares stale and someone —
often you — picks it up. Leave a log a stranger could audit and understand.
