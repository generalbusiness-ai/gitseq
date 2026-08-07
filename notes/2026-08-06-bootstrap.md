# Bootstrap: the workroom

> Companion to [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md).
> The design note owns the contracts; this file owns the plan for
> living on them. Status: plan ratified, review-repaired, simplified;
> dogfood implementation, acceptance walkthrough, and first contract-repair
> pass complete.

## The move

Build the system we use to build the system — and that *is* the demo.
The project's working substance becomes one log; every visible
artifact is a projection that can answer "what do you rest on?". A
visitor doesn't watch a staged scene; they clone the project and
audit it offline. The bootstrap is low-risk because the substrate is
git: if the tooling collapses we are left standing on ordinary git
commits — the current hand-run practice.

## Logs

- **The workroom log**: durable acts only. Chatter never lands here.
- **The design-note log** (`refs/seq/design`, hand-run): sealed via
  the continuation gate below; prose continues in the successor.
- **Ephemeral conversations**: anchored to what they're about,
  forgotten when everyone leaves. What mattered is promoted inward
  with the signed frames embedded as evidence.

One security domain: this repo.

## The repo underneath

The workroom is an overlay on an ordinary git repository — the same
one. **It stores no artifacts, ever**: artifacts are files in the
working tree, committed on branches, merged through PRs, exactly as
always. The workroom is coordination in parallel refs of the same
clone — why, on what basis, who promised what, what's stale. Git
answers what/who/when; the workroom answers why/on-what-basis.
Statements cite artifacts as `path@commit`; documents are never
copied into events. Forge-hosted coordination (issues, PR threads,
reviews) is mutable, deletable, and outside the log — exactly the
trust shape of ephemeral chat, and handled the same way: discuss
freely there, promote what crystallizes with quotes embedded and the
URL as a hint. Adoption is `gs attach` on any existing repo; removal
leaves a normal repo — workroom refs are inert extras, trailers are
inert text. The overlay adds meaning; it never takes hostages.

## Three acts, Language-Action kinds

The entire durable vocabulary, schemas `workroom/<act>@0`:

- **`state`** — an attributed utterance. `rests_on` carries its
  basis; evidence (promoted conversation frames, quoted forge
  threads) rides as attachments. Statements are always on the
  record.
- **`ratify`** — confers collective force on a statement (Searle's
  declarative). A decision is a ratified proposal; the roster is
  ratified roster-statements; ratifier authority is itself a fold
  rule over the roster.
- **`supersede`** — retires an act, driving transitive staleness
  through `rests_on`. Supersessions can themselves be superseded;
  staleness is computed from live supersessions only.

Statement `kind`s follow the Language-Action conventions
(Winograd/Flores):

- `assert` — assertive: a claim or observation, evidence-bearing.
- `propose` — decision-seeking; ratification adopts it.
- `request` — directive: asking an actor to act.
- `promise` — commissive: an undertaking, resting on a request or
  free-standing. (Named `promise`, never `commit` — this system
  layers over git, and "commit" is taken.)
- `report` — completion claim, resting on the promise.
- `dissent` — decline or objection, resting on what it contests.
- `artifact` — a record pointer (`path@commit`), not a speech act.
- governance: `roster`, `infra-key`, `seal` — declaratives, effected
  by ratification.

**The conversation-for-action loop** is a projection, never
admission. The chain:

```
requester:  state{kind: request}   body: to, conditions of
                                   satisfaction, optional due
performer:  state{kind: promise}   rests_on the request
performer:  state{kind: report}    rests_on the promise
requester:  ratify the report   →  satisfied
```

Satisfaction is declared by the requester; the performer only
reports. `ratify` is context-sensitive: assertions, proposals, and
governance statements take roster-derived ratifier authority; a report takes
exactly the authority of its originating requester; anything else is
visible but ineffective. A **free-standing promise lands but
projects dangling in v0** — without a request there is no
structurally identified party to declare satisfaction; if offers
ever matter, their acceptance gets modeled explicitly later rather
than weakening the loop now.

Terminal and interim states, morally distinct — the ledger must
preserve these differences or it becomes a blame instrument:

- request superseded by requester, unanswered → **withdrawn**
- request superseded after a promise → **cancelled** (the performer
  is released; the historical promise stays visible)
- promise superseded by its maker → **reneged**, never neutral
- report superseded by its maker → report withdrawn; the promise
  **reopens**
- report ratified by the requester → **satisfied**
- report unratified → **reported**, awaiting satisfaction — not
  performer failure
- a governing basis dies → **stale**
- structural ambiguity → **disputed**

The status page's center falls out: who is waiting on whom, for
what, under which still-live basis.

Lineage, stated carefully: The Coordinator accepted free-form text,
but users opened *typed conversations* and were offered
state-dependent response menus; gitseq applies kinds only at
deliberate durable promotion, with all exploratory conversation
outside the grammar — a direct repair of that classic
interaction-design failure. It does **not** make Suchman's broader
objection disappear: a commitment ledger can itself become an
imposed accountability regime. Optional promotion, append-only
dissent, and visible ineffective acts make that power contestable,
not absent — the politics of durable accountability stay visible.
The history is a controversy, not a simple failure (Winograd 1987,
Suchman 1994, Winograd's 1994 response). The verbs are the ontology
that never grows; kinds are its designated growth seat, and a
forty-year-tested convention beats inventing one gradually.
Custody's saga fold and this loop share primitives (causal indexing,
target resolution, effectiveness, staleness traversal), not a state
machine — a common abstraction may emerge after both folds exist,
not before.

## Stages

1. **Profile core (the fold is the product).** The three act schemas,
   effectiveness rules, ratifier authority, transitive staleness, and
   a deterministic projector (status + provenance walks, byte-stable)
   tested against golden transcript fixtures. Pure library; no
   services.
2. **Durable workroom (dogfooding begins).** Resident sequencer
   (`submit` + `status`/`wait` HTTP over the spike kernel), `gs` CLI,
   `gs attach` (configure `refs/seq/*` refspecs, fetch, verify — a
   normal clone doesn't), the artifact bridge, the continuation dry
   run. Admission: static pubkey allowlist.
3. **Live collaboration.** Nexus (presence, ephemeral conversations),
   one MCP server per client session configured for an actor.
   Admission stays the static allowlist at both services — every
   actor here is operator-approved. The designed capability chain
   (nexus-issued, offline-verified) activates when the first
   non-operator-trusted actor arrives, as an audited event.
4. **Demo surface.** A small live page and guided five-minute tour —
   the same projector as `STATUS.md`, only polish.

The implementation completed all four stages on this
repository. The acceptance story below was exercised through the CLI,
HTTP service, MCP adapter, browser surface, a cold nexus restart, and
an independently fetched clone. The first adversarial dogfood review also
landed temporal role revocation, canonical actor addressing, pinned and
cached verification, exact MCP envelopes, degraded durable operation, and
leased live sessions. Rotation, checkpoint/witness formats, capability
tokens, and latency targets remain deliberate production hardening work
rather than hidden claims of this spike.

## The golden work session (acceptance story)

Run on this repository, not a fixture world:

1. A human and two agents appear live.
2. They discuss a real implementation question ephemerally.
3. An agent states a proposal, embedding the selected signed frames.
4. An ordinary agent's ratification stays visibly ineffective; an agent with
   an explicit ratifier grant ratifies it successfully.
5. An implementation commit cites the decision; an artifact statement
   lands.
6. The decision is superseded; the artifact visibly flares stale.
7. The nexus is killed: presence and conversation vanish, durable
   acts remain, restart resumes cold.
8. A fresh clone elsewhere verifies offline and walks from the stale
   artifact to its superseded basis.

Works here → dogfood. Followable by a visitor in five minutes → demo.

## Identity and auth

- Actor identity is an Ed25519 keypair held by a custodian proxy (the
  MCP server). Honesty: custody prevents *accidental* key handling —
  keys never enter model context — but is not isolation from a
  tool-capable agent on the same OS account. Real isolation (separate
  principals or a keychain boundary) is a later audited hardening
  step.
- The resident HTTP service is a trusted, loopback-only multi-actor
  custodian. It must not be exposed as a network service without a new auth
  design. For stdio MCP, the OS is the transport and each process is
  configured for one actor.
- Session→actor bindings are leased for 30 seconds and renewed by the MCP
  adapter every 10 seconds. Sessions sharing an actor remain distinct in the
  presence layer (session nonce, never in the durable intent). A session may
  speak only as its bound actor; when the last participant in a conversation
  departs or expires, the resident service forgets its frames.
- The operator key is the root of ratification, not of the kernel.
- The nexus signing key lands as a ratified `state{kind:infra-key}`.
- Principal kind (`human`, `agent`, or `service`) is descriptive identity,
  projected separately from authority. Every admitted principal has the
  neutral `participant` role; independently ratified roster statements grant
  authority roles such as `ratifier` and `witness`. Humanity never implies
  authority, and agenthood never excludes it.
- Roles are enforced by the fold, never the doorstep: each authority check
  consults effective, ratified, unsuperseded roster statements at that log
  position. An authority grant is live only while the participant membership
  it rests on remains live. Demotion affects later acts without rewriting
  earlier verdicts; an unauthorized ratification is a visible ineffective
  attempt — a demo beat.
- New workrooms use the stable application idempotency namespace
  `workroom/v0`; actor fingerprint remains a separate part of dedup identity.
  Existing workrooms without that config field retain their historical
  `gs/<actor-name>` namespace so an outstanding retry cannot append twice
  during upgrade.

## The composite cursor

`status`/`wait` span durable frontiers (never reset) and the nexus's
live position (reset on restart):

```
cursor { frontier: [(genesis, head, depth) ...], live: {generation, position} }
```

Capture is subscribe-before-snapshot (duplicates possible, loss
impossible; dedupe by per-log depth). On live reset the frontier
stays valid: replay the durable delta, retake a live snapshot,
resume. Only presence and conversations are lost, by contract.

## The artifact bridge

What keeps the workroom on the critical path instead of beside it:
a decision lands → the implementing source commit carries
`Rests-On: <decision-event>` → an artifact statement cites both, so
the projector flares the artifact when a governing decision dies.
The canonical event identifier, including in `Rests-On:` trailers, is
`git:<object-format>:<genesis>#git:<object-format>:<event-commit>`.

## The continuation gate

The genesis descriptor has no continuation fields yet. Hard order:
extend the descriptor (predecessor genesis, sealed head); build a
candidate successor on a *scratch copy* of the hand-run log; a fresh
reader must traverse predecessor → seal → successor and reproduce
the projection; only then seal the real `refs/seq/design`. Migration
is the acceptance test for continuation, not the act that discovers
it.

## MCP: stateless

Targets the 2026-07-28 stateless MCP spec only. Every request is
self-contained and carries the required protocol version and client
capabilities metadata; complete results carry `resultType` and server info.
`wait` is a long-poll carrying the composite cursor;
the durable log is the queue. Snapshot+cursor already *is* the
stateless shape, so the MCP layer stays thin. Tools: `say`, `state`,
`ratify`, `supersede`, `status`, `wait`, `presence`, `whoami` —
eight; `say` mints a conversation when none is open at the
coordinate; promotion is `state` with frames embedded. If the resident
service is down, `state`, `ratify`, and `supersede` submit through the local
durable sequencer, while `status` and `wait` project the Git log with a
`degraded` live cursor. Presence and `say` correctly remain unavailable.


## The witness (secretary role)

Ephemerality's ergonomic gap is filled by a participant, not a
feature. A **witness** is an ordinary agent actor with a recognized
roster role: it sits in the room, notices when talk crystallizes,
and **proposes** set-downs. The witness role itself never confers
ratification, though the same agent may independently hold a ratifier
grant. Its exit-time behavior
is speech, not a dialog: "Before we lose this — three things looked
decisional; shall I set them down?", each a proposal with the signed
frames embedded as evidence, one act for an authorized ratifier to adopt. The
division of labor honors the Coordinator lesson: witnessing performs
classification without silently acquiring commitment authority. No new
substrate: SKILL.md is its instructions, promotion-with-evidence its
verb, proposal-as-poll its review surface.

The UI floor beneath it (works in agent-free rooms): the room shows
its own mortality ambiently — unpromoted chat visually ages, and the
**last holder** sees a quiet inline status ("you're the last one
holding this conversation") at the only moment a cue is honest,
because leaving last is the destruction event. Personal memory is
the client's right, exercised locally: a transcript of sessions you
attended, marked "your memory, not the room's," never citable as
force. A modal at departure is refused: it demands recall at the
wrong moment and reframes forgetting as loss.

## The tax, stated

Dogfooding pre-alpha tooling invites the workroom to demand features
until layering erodes. Mitigations: the workroom is an application
profile; its kinds live in the log; kernel wants arrive as designed
waves. Event-first work adds friction; the ephemeral layer is the
relief valve — only promotion costs a deliberate act.

## Agent guidance

[`SKILL.md`](../SKILL.md) is the normative usage contract for agent
actors. The implementation must match it; when they disagree, one of
them changes by an audited act.
