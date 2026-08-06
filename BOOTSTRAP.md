# Bootstrap: the workroom

> Companion to [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md).
> The design note owns the contracts; this file owns the plan for
> living on them. Status: plan ratified, review-repaired, simplified;
> dogfood implementation in progress.

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
reports. `ratify` is context-sensitive: proposals and governance
statements take roster-derived ratifier authority; a report takes
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
   (`submit` + `watch` HTTP over the spike kernel), `gs` CLI,
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

## The golden work session (acceptance story)

Run on this repository, not a fixture world:

1. A human and two agents appear live.
2. They discuss a real implementation question ephemerally.
3. An agent states a proposal, embedding the selected signed frames.
4. The human ratifies it; an agent ratification stays visibly
   ineffective.
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
- Session→actor binding is transport auth; for stdio the OS is the
  transport. Sessions sharing an actor are distinguished at the
  presence layer (session nonce in annotations, never in the intent).
- The operator key is the root of ratification, not of the kernel.
- The nexus signing key lands as a ratified `state{kind:infra-key}`.
- Roles are enforced by the fold, never the doorstep: an unauthorized
  ratification is a visible ineffective attempt — a demo beat.

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
self-contained; `wait` is a long-poll carrying the composite cursor;
the durable log is the queue. Snapshot+cursor already *is* the
stateless shape, so the MCP layer stays thin. Tools: `say`, `state`,
`ratify`, `supersede`, `status`, `wait`, `presence`, `whoami` —
eight; `say` mints a conversation when none is open at the
coordinate; promotion is `state` with frames embedded.

## The tax, stated

Dogfooding pre-alpha tooling invites the workroom to demand features
until layering erodes. Mitigations: the workroom is an application
profile; its kinds live in the log; kernel wants arrive as designed
waves. Event-first work adds friction; the ephemeral layer is the
relief valve — only promotion costs a deliberate act.

## Agent guidance

[`SKILL.md`](SKILL.md) is the normative usage contract for agent
actors. The implementation must match it; when they disagree, one of
them changes by an audited act.
