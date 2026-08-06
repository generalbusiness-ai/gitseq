# Bootstrap: the workroom

> Companion to [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md).
> The design note owns the contracts; this file owns the plan for
> living on them. Status: plan ratified, review-repaired, simplified;
> nothing built.

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

## Three acts

The entire durable vocabulary, schemas `workroom/<act>@0`:

- **`state`** — an attributed assertion. A payload `kind`
  distinguishes observation, claim, proposal, dissent, work item,
  artifact reference, roster entry, infrastructure key, seal.
  `rests_on` carries its basis; evidence (e.g. promoted conversation
  frames) rides as attachments. Statements are always on the record.
- **`ratify`** — confers collective force on a statement. A decision
  is a ratified proposal; the roster is ratified roster-statements;
  ratifier authority is itself a fold rule over the roster.
- **`supersede`** — retires an act, driving transitive staleness
  through `rests_on`. A closed work item is a superseded open one.
  Supersessions are acts and can themselves be superseded; staleness
  is computed from live supersessions only.

Everything else — attestation level (asserted/extracted), open work,
disputes, dissent-in-provenance — is derived by the fold from actor
role, ratification, and these three verbs. The vocabulary does not
grow; kinds do.

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
cursor { frontier: [(genesis, head) ...], live: {generation, position} }
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
