# Bootstrap: the workroom

> Companion to [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md).
> The design note owns the contracts; this file owns the plan for living
> on them. Status: plan ratified, nothing built.

## The move

Build the system we use to build the system — and that *is* the demo.
The project's working substance becomes one log: decisions, dissent,
supersessions, roster changes, infrastructure acts. Every visible
artifact is a projection that can answer "what do you rest on?". A
visitor doesn't watch a staged scene; they clone the project and audit
it offline. The three demo stories collapse into views of this
workroom: the flight recorder is the project's own log rendered; honest
minutes are how design sessions actually run; the document that knows
when it's wrong is the project's own status page.

The bootstrap is uniquely low-risk because the substrate is git: if the
tooling collapses, we are left standing on ordinary git commits —
exactly the hand-run practice that produced Seq 1–6. The amnesiac layer
is the only part that can fail, and its loss is, by contract, a dropped
call.

## Log taxonomy

- **The workroom log** (`refs/seq/<genesis>`, one security domain =
  this repo): durable acts only. Decisions, ratifications,
  supersessions, roster and infrastructure events, status items.
  Chatter never lands here.
- **The design-note log** (`refs/seq/design`, currently hand-run):
  sealed via the continuation convention as the first production act —
  final checkpoint event, successor genesis minted by the real
  sequencer citing the predecessor's genesis and sealed head. Prose
  keeps its own cadence in the successor stream.
- **Ephemeral conversations**: minted per discussion, anchored at the
  coordinate they are about (the workroom genesis, a decision event, a
  file). Sequenced, signed, forgotten when everyone leaves. Promotion
  copies what mattered into the workroom log, citing frame hashes.

Logs are cheap; further splits (per-artifact streams) are later acts,
themselves audited.

## Staging

**Stage 0 — the smallest thing we can live in.**
Resident sequencer (the spike kernel grown an HTTP face: `submit`,
`watch` SSE), a `gs` CLI, two actor keys (operator + one agent), the
act vocabulary below, and a static-allowlist pre-append hook. From day
one, design decisions are events first; note edits cite them.

**Stage 1 — multiple agents, live (the scope of the bootstrap).**
The nexus hub as a real service: presence, ephemeral conversations,
capability issuance. One MCP server instance per actor (stdio,
custodial key). Roster fold governs membership; the capability chain
below replaces the static allowlist — that replacement is itself an
audited event. Agents and humans work concurrently with live
discussion, decisions, and real work.

**Stage 2 — the demonstration surface.**
`STATUS.md` (or a small page) rendered on every head advance: open
questions and active work items, each resting on the decisions that
framed them; superseding a decision flares its dependents stale, with
one-click provenance walks. Demo A on our own work; demos B and C fall
out of stages 1 and 0 respectively.

## Identity and auth (minimal but real)

- **Actor identity is an Ed25519 keypair, custodially held by that
  actor's MCP server.** LLM sessions never see private keys. Honesty
  statement (the CO1 statement): an actor signature proves which
  custodian key signed, not that the model "meant" it. The custody
  chain is short and inspectable: intent ← actor key ← key file ← MCP
  server launched under the operator's OS account with per-actor
  config.
- **Session→actor binding is transport auth; at stage 1 the transport
  is the OS.** One stdio MCP server per actor; filesystem permissions
  on the key file are the credential. Concurrent sessions as one actor
  are distinguished at the presence layer (session nonce in the
  announcement annotation and app-level payload metadata — never in
  the kernel intent).
- **The roster is a fold.** `actor-added {name, role, pubkey}` events,
  operator-ratified. The operator key is the root of *ratification*,
  not of the kernel (kernel roots stay in genesis).
- **Admission**: stage 0 = static pubkey allowlist in the sequencer
  hook (the roster events are the record, the config is the
  enforcement; drift between them is visible and accepted
  temporarily). Stage 1 = the designed chain: the nexus checks the
  roster at announce time and issues a short-lived signed capability
  (actor key, coordinate, claims `append`+`advertise`, expiry,
  renewed on heartbeat); the intent's capability hash binds it; the
  sequencer verifies the nexus signature offline and stays stateless.
- **Nexus issuer key is anchored as an event** (`nexus-key`,
  operator-ratified) in the workroom log — for one deployment, the
  workroom log is the profile's config log.
- **Roles are enforced by the fold, not the doorstep.** No
  schema-scoped capability claims: an agent that submits a
  ratify-shaped event lands in the log as a visible ineffective
  attempt. That is admission ≠ validity working, and it is a demo
  beat (injection produces an audited attempt, not a silent success).

Deferred without guilt: identity logs (until a key actually rotates),
`read`/`discover` claim enforcement (one domain; forge ACL is the
boundary), witnesses and checkpoint cadence (meaningless at this
scale). Each arrives later as an event in a log that shows it landing.

## Act vocabulary v0 (application profile)

Schema ids `workroom/<type>@0`. The engine set: `observation`,
`claim`, `decision`, `supersession`, `artifact-reference`; the human
acts `ratify`, `dissent`; the governance acts `actor-added`,
`nexus-key`, `seal`/`continue`. Payloads are small JSON; `rests_on`
carries what an act stands on — every durable act cites its basis.
The vocabulary never grows in the kernel; it grows here, by events.

## The tax, stated

Dogfooding pre-alpha tooling on the critical path invites the workroom
to demand features until the kernel/profile discipline erodes from
above. Mitigations: the workroom is an application profile; its
vocabulary lives in the log; anything it wants from the kernel arrives
as a designed wave, not a convenience patch. Event-first working adds
friction versus just talking; the ephemeral layer is the mitigation —
chatter stays cheap and forgettable, only promotion costs a deliberate
act, which is the practice working, not overhead.

## Agent guidance

The MCP server ships with [`SKILL.md`](SKILL.md) — the compact,
normative usage contract for agent actors. The implementation must
match it; when they disagree, one of them changes by an audited act.
