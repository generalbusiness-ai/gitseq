# Bootstrap: the workroom

> Companion to [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md).
> The design note owns the contracts; this file owns the plan for living
> on them. Status: plan ratified and review-repaired; nothing built.

## The move

Build the system we use to build the system — and that *is* the demo.
The project's working substance becomes one log: decisions, dissent,
supersessions, roster changes, infrastructure acts, work items. Every
visible artifact is a projection that can answer "what do you rest
on?". A visitor doesn't watch a staged scene; they clone the project
and audit it offline. The demo stories collapse into views of this
workroom: the flight recorder is the project's own log rendered;
honest minutes are how design sessions actually run; the document that
knows when it's wrong is the project's own status page.

The bootstrap is uniquely low-risk because the substrate is git: if
the tooling collapses, we are left standing on ordinary git commits —
exactly the hand-run practice that produced the log so far. The
amnesiac layer is the only part that can fail, and its loss is, by
contract, a dropped call.

## Log taxonomy

- **The workroom log** (`refs/seq/<genesis>`, one security domain =
  this repo): durable acts only. Decisions, ratifications,
  supersessions, roster and infrastructure events, work items.
  Chatter never lands here.
- **The design-note log** (`refs/seq/design`, currently hand-run):
  sealed via the continuation convention — but only after the
  continuation gate below passes. Prose keeps its own cadence in the
  successor stream.
- **Ephemeral conversations**: minted per discussion, anchored at the
  coordinate they are about. Sequenced, signed, forgotten when
  everyone leaves. Promotion copies what mattered into the workroom
  log — embedding the signed frames, not merely citing them.

Logs are cheap; further splits are later acts, themselves audited.

## Stages

**Stage 1 — profile core (the fold is the product).**
Before any HTTP, nexus, or MCP code: the workroom application profile
as a specification plus a pure projector, tested against golden
fixtures. It must define, exactly:

- Payload schemas for every act (`workroom/<type>@0`).
- Effective / ineffective / `disputed` rules, per act type.
- Proposed versus ratified decisions, and who may ratify (roster and
  ratifier authority as fold rules).
- Supersession semantics and **transitive staleness** — what flares
  when a basis dies, and how far the walk goes.
- The **work-item lifecycle** (open → progress → done/abandoned),
  as acts with schemas, not just a mention.
- The deterministic status/provenance projection: given a log, the
  projector emits the status document and the provenance walk for any
  item, byte-stable. Golden transcript fixtures are its test suite.

**Stage 2 — durable workroom (dogfooding begins here).**
Resident sequencer (the spike kernel grown an HTTP face: `submit`,
`watch`), the `gs` CLI, the **artifact bridge** (below), `gs attach`
for clone/audit, and the **continuation dry run**. Admission by
static pubkey allowlist. From this point, design decisions are events
first and source commits cite them.

**Stage 3 — live collaboration.**
Nexus hub (presence, ephemeral conversations, capability issuance),
MCP servers, the composite cursor contract (below), roster-fold
membership replacing the static allowlist as an audited event.
Multiple agents with live discussion, decisions, and real work.

**Stage 4 — demo surface.**
The live page and guided five-minute tour over genuine project
history, backed by the same projector as `STATUS.md`. Only polish
lives here; every capability it shows already exists by stage 3.

## The golden work session (acceptance story)

One authentic transcript is both the acceptance test and the demo
script. It must run on this repository, not a fixture world:

1. A human and two agents appear live (presence).
2. They discuss a real implementation question ephemerally.
3. An agent promotes selected signed frames into a proposed decision.
4. The human ratifies it; an agent ratification attempt remains
   visibly ineffective.
5. An implementation commit cites the decision and appears as an
   artifact-reference act.
6. The decision is superseded; the artifact visibly becomes stale.
7. The nexus is killed: presence and conversation disappear; durable
   acts remain; a restarted nexus resumes cold.
8. A fresh clone elsewhere verifies offline and walks from the stale
   artifact to its superseded basis.

If that works here, it is dogfood. If a visitor can follow it in five
minutes through a small live page backed by the same projector, it is
a great demo.

## Identity and auth (minimal but real)

- **Actor identity is an Ed25519 keypair held by a custodian proxy
  (the MCP server), not by the model.** Honesty statement, corrected
  after review: custody prevents *accidental* key handling — keys
  never enter model context or transcripts — but it is **not
  isolation from a tool-capable local agent**: a shell-capable agent
  running under the same OS account can read the key files. True
  isolation requires separate OS principals or an inaccessible
  keychain boundary, and arrives later as an audited hardening step
  if the threat model ever warrants it. Until then the guarantee is
  stated at its real strength.
- **Topology**: stdio MCP means **one server process per client
  session, configured for an actor**. Several sessions may share an
  actor identity deliberately; sessions are distinguished at the
  presence layer (session nonce in the announcement annotation and
  app-level payload metadata — never in the kernel intent).
- **The roster is a fold.** `actor-added {name, role, pubkey}`
  events, operator-ratified. The operator key is the root of
  *ratification*, not of the kernel.
- **Admission**: stage 2 = static pubkey allowlist in the sequencer
  hook (roster events are the record, config is the enforcement,
  drift visible and accepted temporarily). Stage 3 = the designed
  chain: the nexus checks the roster at announce time and issues a
  short-lived signed capability (actor key, coordinate, claims
  `append`+`advertise`, expiry, renewed on heartbeat); the intent's
  capability hash binds it; the sequencer verifies offline.
- **Nexus issuer key is anchored as an event** (`nexus-key`,
  operator-ratified) in the workroom log.
- **Roles are enforced by the fold, not the doorstep.** An agent that
  submits a ratify-shaped event lands as a visible ineffective
  attempt — admission ≠ validity working, and a demo beat.

## The composite cursor

`status` and `wait` span two regimes: durable log frontiers (never
reset) and the nexus's live cursor (reset on restart). One contract:

```
cursor {
  frontier: [(genesis, head-hash, depth) ...]   // durable, exact
  live:     {generation, position}              // nexus, resettable
}
```

- **Capture order is subscribe-before-snapshot**: take the nexus
  cursor *first*, then read durable heads. A durable commit landing
  between the two appears as a live change after the cursor —
  duplicated, never lost; clients dedupe by per-log depth
  monotonicity.
- **Reset algorithm**: on `ErrReset` (new nexus generation or
  trimmed history), the frontier remains valid — replay the durable
  delta from the frontier, retake a live snapshot, resume. Durable
  state is never lost to a live-layer reset; only presence and
  conversations are, by contract.

## The provenance chain, closed

- **`gs attach` / `gs clone`**: a normal git clone does not fetch
  `refs/seq/*`. The CLI configures the refspec, fetches the log
  namespaces, and runs the verifier — one command from URL to
  offline-auditable.
- **Promotion embeds evidence.** A promoted act carries the selected
  signed frames (or a transcript bundle) as attachments, plus the
  frame hashes. A stranger can verify honest minutes after the
  conversation is forgotten without hunting for a participant's
  copy; the degrading-reference grades still apply to whatever was
  *not* embedded.
- **The artifact bridge** — the piece that makes this dogfood on the
  critical path rather than a parallel journal:
  1. A decision event lands in the workroom log.
  2. The implementing source commit carries
     `Rests-On: <decision-event>` in its message.
  3. An `artifact-reference` act cites both the source commit and its
     governing decisions, so the projector can flare the artifact
     stale when a governing decision is superseded.

## The continuation gate

The genesis descriptor currently has **no continuation fields**;
sealing `refs/seq/design` before the format exists would discover the
format by irreversible act. Order of operations, hard gate:

1. Extend the genesis descriptor with predecessor fields (predecessor
   genesis, sealed head) and the `seal` act.
2. Build a **candidate successor** for a scratch copy of the hand-run
   log.
3. Acceptance: a fresh reader traverses predecessor → seal →
   successor and reproduces the projection across the seam.
4. Only then seal the real `refs/seq/design`. Migration is the
   acceptance test for continuation, not the act that discovers it.

## MCP: stateless by design

The MCP server targets the 2026-07-28 stateless MCP spec only — no
legacy-protocol compatibility, no session affinity, no server-held
subscription state. Every request is self-contained; `wait` is a
long-poll carrying the composite cursor; resumability *is* the
cursor, and the durable log is the queue. This is not an adaptation:
the design's snapshot+cursor and log-as-durable-queue contracts are
already the stateless shape, so the MCP layer stays thin.

## Act vocabulary v0 (application profile)

Schema ids `workroom/<type>@0`. The engine set: `observation`,
`claim`, `decision`, `supersession`, `artifact-reference`; the human
acts `ratify`, `dissent`; the work-item family `work-opened`,
`work-progress`, `work-closed`; the governance acts `actor-added`,
`nexus-key`, `seal`/`continue`. Exact payload schemas are the stage-1
deliverable; `rests_on` carries what an act stands on — every durable
act cites its basis. The vocabulary never grows in the kernel; it
grows here, by events.

## The tax, stated

Dogfooding pre-alpha tooling on the critical path invites the
workroom to demand features until the kernel/profile discipline
erodes from above. Mitigations: the workroom is an application
profile; its vocabulary lives in the log; anything it wants from the
kernel arrives as a designed wave, not a convenience patch.
Event-first working adds friction versus just talking; the ephemeral
layer is the mitigation — chatter stays cheap and forgettable, only
promotion costs a deliberate act.

## Agent guidance

The MCP server ships with [`SKILL.md`](SKILL.md) — the compact,
normative usage contract for agent actors. The implementation must
match it; when they disagree, one of them changes by an audited act.
