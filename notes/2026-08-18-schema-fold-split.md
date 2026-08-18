# Separating fold upgrades from vocabulary evolution

2026-08-18. **Status: implementation candidate, not merged** — the direction is
authorized; this note is the governing specification and records the
compatibility choices made by the candidate implementation. It changes the
Workroom meta-kind contract and updates
`docs/reference/architecture.md`. The kernel stays schema-agnostic; the one
kernel-side change (the companion checkpoint `Profile` decoupling) *removes* an
over-coupling to the application fold rather than adding fold awareness.

## The problem

Workroom presents two meta-kinds as one facility, but they sit on opposite
sides of the code/data line:

- **`kind-def`** is *declarative data*. It defines a statement kind through a
  finite, data-only constraint algebra (`present` / `type` / `matches` /
  `one-of` over `string` / `event-id` / `actor-ref` / `path-commit`), with
  guidance that is explicitly "powerless in the fold." It grows the
  **vocabulary** the *existing* fold validates. No new code runs.
- **`fold-activation`** is *code*. Its body carries a `fold` field of type
  `path-commit` — a pointer at a fold implementation — and it "binds a
  published fold to a precise transition." Activating it changes what the fold
  *does*.

Before the candidate, both were listed together in `foldInterpreted`
(`{kind-def, fold-activation, roster}`) and marked non-redefinable — but for
different reasons: one is trusted code, the other is the schema-agnostic
validator's own bootstrap. And
the conflation is visible at the payload boundary: before this implementation,
a `kind-def` body could carry a `fold` string that the decoder silently ignored,
while the neighbouring starter meta-kind gave the same vocabulary surface a
real code-pointer meaning. The Go `KindDefinition` value was already data-only;
the missing guarantee was rejecting the code pointer at the declared-data
boundary and removing activation from the current catalog.

Meanwhile the host-binding model (`internal/app`) now resolves *which
interpreter a repository is bound to* once, before the workspace can fold or
append. The fold is already selected by the binding. That makes
`fold-activation`, as a separate in-fold meta-kind, partly redundant with
binding for the "which fold" question — and it reveals that the two concerns
have different owners.

## The proposal

Split the facility along the code/data line, giving each half the owner its
authority implies.

**Host binding owns code/fold upgrades.** Activating a fold is running new
trusted code — the highest-authority operation in the system. That authority
belongs to the binding, which is anchored to the application the repository
committed to at init (the mechanism the chess host-binding work landed).
Concretely:

- Keep `KindDefinition` free of a `Fold` field and reject `body.fold` rather
  than silently carrying it as extra statement data. A kind definition may no
  longer carry a code pointer.
- Reframe fold activation (the `path-commit`→code pointer plus its transition)
  as a **facet of the host binding**, signed by the binding authority and
  verified against the bound application — uniform with how the initial
  interpreter is already chosen. A fold *upgrade* becomes "the binding moves to
  a new interpreter version at transition T," not "a kind you define."

**An optional vocabulary library owns in-fold schema evolution.** `kind-def`
stays the pure declarative facility: introduce statement kinds via the
constraint algebra, validated by the *existing* fold, no code. It becomes a
**library** an application or repository can ship — a starter-only repository
needs none; a richer one declares or imports a vocabulary. It never changes
what the fold does, only what statement shapes it accepts.

The invariant that makes the split clean:

> **Code that changes the fold is authorized by the host binding. Data that
> extends the vocabulary is validated by the existing fold.** Different
> authority, different side of the code/data line.

### Why it is worth doing

- **It removes a privilege-escalation-shaped seam.** Today a "kind definition"
  can carry a `Fold` pointer, so the low-authority declarative facility
  inherits the high-authority code-upgrade capability. After the split, only
  the binding can change the fold.
- **It makes the authority gradient explicit.** Declaring a vocabulary is
  ordinary ratified data; activating a fold is a binding-level, app-anchored
  act. Bundling them hid that difference.
- **It keeps the kernel where it belongs** — sequencing signed commits,
  schema-agnostic. All of this lives in the Workroom / app layer.

## Migration

The log is append-only, so historical `fold-activation` records stay readable
and their past decisions do not change; `state@0` remains decodable. The change
governs *new* activations: after it lands, a fold upgrade is expressed through
the host binding, and `KindDefinition` no longer accepts `Fold`. Existing rooms
that activated a fold under the old meta-kind (the "existing rooms must declare
prefix=genesis" path) use the latter bridge: the fold honors already-ratified
state@0/state@1 activation history and preserves the same transition and
post-transition uninterpretable seam. The current starter catalog omits
`fold-activation`; state@2 use is undefined, and application admission refuses
both a new state@0/state@1 activation and a new ratification@0 of a previously
unratified legacy activation. Ratification@1 independently makes an activation
ineffective if it bypasses admission. A later upgrade is an initializer-key-signed
host-binding replacement. This is append-only: historical records and their
decisions do not change, while no new application-level activation can take
force.

## What chess needs

**For correctness: nothing new.** Chess's rules — legal moves, turn order, win
detection — are semantic, and the finite declarative algebra cannot express
them, so they live in chess's **bound fold (code)**, which chess already owns
via host binding. Chess needs neither `kind-def` nor `fold-activation`-as-a-
kind-def; its binding is the whole story. This proposal is, in fact, what lets
chess's model read cleanly: the interpreter it is bound to owns its fold, and
nothing in the generic definition facility competes for that role.

The extension chess's model actually wants is on the binding side: host binding
should carry and be able to **upgrade** the interpreter/fold version at a
transition — the concern this note moves `fold-activation` into. The init-time
binding gives selection; the follow-on is upgrade under the binding.

**Optional, not now:** chess could declare its *statement envelopes* (the shape
of a move or game record, not the rules) as vocabulary, so generic surfaces —
the status view, the MCP tools — can render chess records without understanding
chess. That is a structural, tooling-only nicety, contingent on this split, and
explicitly out of scope here.

## Scope and non-goals

- In scope: enforce the already-data-only `KindDefinition` boundary by refusing
  `body.fold`; move fold activation/upgrade under host binding with its trust
  anchored to the bound application; keep `kind-def`
  as a pure declarative vocabulary facility; specify the migration bridge for
  historically-activated rooms; update `docs/reference/architecture.md`.
- Out of scope: implementing a chess statement-envelope vocabulary; a general
  package/registry mechanism for shipping vocabulary libraries (the *shape* is
  defined here; a distribution mechanism is later work). The **one** kernel
  change in scope is the checkpoint `Profile` decoupling in the companion
  section below — the kernel stays schema-agnostic; that change removes an
  over-coupling to the application fold, it does not add fold awareness to the
  kernel.

## Companion: the kernel checkpoint should not be gated on the fold

The same code/data boundary applies to the restart cache, and the current
coupling there is unnecessarily conservative.

The kernel checkpoint (`internal/kernel/checkpoint.go`) caches **kernel-verified
event material**, not folded application state. The compact format stores
payloads and attachments while commit metadata and signed envelopes remain
bound to the named sequence and are checked during restart. Kernel verification
(signatures, sequencer key rotations, first-parent chain, object format,
genesis) is independent of any application fold. Yet the historical
`CheckpointOptions.Profile` was "the application fold contract," and
`readCheckpointCandidate` rejected a stored checkpoint whenever
`stored.Profile != profile`. So changing the fold discarded a still-valid
authenticated prefix and forced a full cold re-verification from genesis —
paying to re-check signatures and rotations that did not change.

**The decoupling.** Key the kernel checkpoint only on *kernel-verification
identity* — schema, object format, genesis, and the sequencer key lineage —
and drop `Profile` from its eligibility test. The already-deployed compact
schema v2 still carries its historical profile field; schema v3 preserves the
compact encoding but writes no profile. The reader accepts v1 and v2 after full
authentication and ignores their historical profile field. Move `Profile` to
gate the separate in-memory
**application projection cache**. Then a fold change keeps the authenticated
event prefix reusable and re-derives only the projection, which is the cheap
part (the fold runs at ~10^5 events/second; re-verifying signatures and reading
objects from Git is the expensive part being needlessly repeated today).

**The safety invariant that makes reuse-across-folds sound:**

> The kernel checkpoint caches only kernel-verified event material and
> kernel-verification identity, and **never** application-fold-derived state.
> The application projection cache is separate and is what a fold change
> invalidates.

That invariant holds in today's `checkpoint` struct (it stores events, not
projection), so the decoupling is safe now — but it must be stated and guarded,
because a future optimization that cached fold output inside the checkpoint
would silently make cross-fold reuse unsound. The checkpoint verification path
is security-critical; this change is small but must be reviewed as such.

**Why it was coupled.** One cache, one key, chosen before the
kernel-verification / application-fold boundary was drawn. The library /
application split above is exactly what makes the boundary explicit, which is
why revisiting the checkpoint's `Profile` gate belongs with it.

**Scope note.** This companion change touches the kernel
(`internal/kernel/checkpoint.go`), unlike the Workroom/app split above. It is
in scope for this design as the boundary's corollary, but should be implemented
and reviewed as a distinct, security-sensitive item — either a separately
reviewed commit in the same effort or a child request once the boundary lands.
