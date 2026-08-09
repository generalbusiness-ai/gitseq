---
date: 2026-08-07
status: adopted 2026-08-07 — proposal a80d687d ratified by hugh
  (event 978a9674) after three rounds of independent review; the
  design note carries the twelfth-wave ledger entry. First
  catalog/application/UI wave implemented under request fa5e579f and
  promise b92876eb; published-fold execution split to causal child
  request 7bb74568.
origin: the 2026-08-07 design-goal review (assert 95e79d71) and a
  conversation about keep's tag-definition model; crystallizing quotes
  ride as evidence on the proposal act that cites this note.
---

# Declared kinds and fold activations

The log carries its own root of trust (genesis anchors the keys). It
does not yet carry its own meaning: the workroom's ontology lives in
three places that drift independently — required fields in
`schema.go`, behavior in `fold.go`, prose in `SKILL.md` — and the
binding between the log and the fold that interprets it is implicit:
whatever build you happen to run. This note proposes closing both
gaps with the machinery the workroom already has. No new verbs.

This note claims an architecture and its transition protocol. It
does not claim achieved deterministic self-description: the
constraint algebra below is a candidate whose ratified enumeration
is the first implementing wave's deliverable, separately reviewed.

## The hazard, already real

Two incidents from the bootstrap locate the problem precisely.

The documentation went false without a flare: the dual-era merge
changed the world the docs describe, not any act the docs rest on.
That is a *linking* failure (tracked as artifact-world-basis), but
its family is larger: description and behavior held in different
places, with nothing that dies when they diverge.

The eighth-wave simplification collapsed thirteen act types into
three verbs and reinterpreted every prior event through a
compatibility boundary. The right decision — and a semantic change
to all recorded history that the record itself does not mention. A
replayer learns which interpretation governs from the commit they
built, not from the log they audit. That is the docs-era incident's
shape applied to the fold itself.

## Kind definitions

A kind-definition is an ordinary governance statement:

```
state {kind: kind-def}  body: {
  name:        the kind being defined
  fields:      required body fields, with value shapes
  basis:       constraints on rests_on — which kinds, what cardinality
  satisfier:   who may ratify acts of this kind
  render:      projection class — where acts of this kind surface
  staleness:   whether acts of this kind propagate and receive staleness
  lifecycle:   commitment-loop participation, if any
  guidance:    non-normative prose for actors (never consulted by the fold)
}
```

Ratified by roster authority like any declarative; versioned by
supersession; resolved position-aware, exactly as roster grants are:
an act is checked against the definition in force at its own
position. Acts of a kind with no definition in force project as a
typed `undefined kind` — visible, never silent. Acts before their
kind's definition stay opaque; definitions are forward-only and
verdicts remain fixed at append. No retroactive semantics, ever.

The normative half must be a **finite constraint algebra with total
semantics** — enumerated operators, every evaluation terminating in
a decision or a typed refusal, never embedded code. A closed set of
property *names* does not bound a language; the operators and their
value grammars must themselves be enumerated. The candidate algebra,
stated so feasibility is checkable and adoption has a concrete
object:

```
fields:     present(name) | type(name, string|event-id|actor-ref|path-commit)
            | matches(name, RE2) | one-of(name, v1..vn)
basis:      count(kind-set, min..max), kinds drawn from defined kinds
satisfier:  role:<name> | originating-requester | none
render:     one value from an enumerated class set
staleness:  propagates | terminal | exempt
lifecycle:  one value from the enumerated loop participations
```

Totality rules: RE2 only (no backtracking, decidable); no recursion,
no reference to anything outside the enveloped body and rests_on; an
evaluation that cannot complete is a typed refusal, never an error.
Whether these operators are the right final set is the implementing
wave's question (open question 1); that the set must be finite,
enumerated, and total is this note's requirement.

The `guidance` half is the agent-facing text SKILL.md now carries
per kind, moved to where a fresh actor's `status` can serve it: the
room describes its own vocabulary. The split is keep's: its
tag-definition documents separate machine-readable declarations
(`_constrained`, `_inverse`, `_singular`, `_value_regex`) from prose
and classifier prompts. Keep can afford prompts as behavior because
one trusted service interprets them; a fold that every replayer must
reproduce cannot. Here the machine half is deterministic and the
prose half is explicitly powerless.

What this closes, and exactly what it leaves open. Closed for any
*defined* kind: validation misses (required fields and basis shape
become checkable), silent opacity of unknown kinds (`undefined
kind` is a stated projection), and render-omission-by-code (once
render classes govern the page, dropping a kind from view requires
superseding its definition — an audited act). Addressed in part,
with the predecessor requests staying open for the remainder:
projection-honesty retains the staleness-through-ineffective-bases
policy and the status page's treatment of ratified decisions and
opaque acts — and, after this note's first review, a live incident
(assert 2503e6de) added a further remainder: `rests_on` conflates
**governing basis** with **historical lineage**, so retiring a
draft flared an active chain that merely cited its review.
Distinguishing those two relations joins the projection-honesty
remainder, and the candidate algebra above cannot express it —
`count(kind-set, min..max)` classes edges by kind alone. A
candidate extension — basis edges classed `governs` or `cites`,
with staleness flowing only along `governs` — is noted for the
implementing wave, not adopted here. Artifact-world-basis retains
the distinct describes-superseded-world projection, the handling of
unbridged or empty-basis artifacts, and verification that a cited
commit carries its `Rests-On:` trailer — a mandatory artifact basis
narrows that request, it does not absorb it. And no definition can know the
world changed; it can only require the citation that lets the
change propagate. The commitment loop's attribution logic (who
superseded, who may declare satisfaction) stays interpreter
machinery throughout — definitions name states, they do not compute
them.

## Fold activations

A fold activation is a governance statement binding interpretation
to an implementation *in this repository*:

```
state {kind: fold-activation}  body: {
  fold:       path@commit of the fold implementation source
  entry:      package path of the fold's entry point
  interface:  meta-interface version the fold implements
  toolchain:  pinned build toolchain
}
```

The statement alone confers nothing; effect arrives only through
ratification, below. The repo is the substrate and the security
domain; the overlay stores no artifacts, and the fold is an ordinary
artifact. So the interpreter for every span of the log lives in the
same clone the auditor already holds, content-addressed, and the log
states which one governs where. Changing the rules becomes a move in
the game the rules govern — the design note's sentence, "fold
definitions are themselves logged in the practice's own log," made
concrete.

### The meta-rule and the transition protocol

Something must bootstrap, and it must not smuggle in a second
interpreter. Judging an activation "effective, ratified,
unsuperseded" is fold vocabulary — so the meta-rule never performs
that judgment itself. **The incumbent judges the succession**, and
**transitions are irrevocable**:

> Walk the log with the initially bound fold governing from
> genesis. The incumbent fold judges every act — including
> activation statements and everything later done to them. When the
> incumbent's own projection shows an activation ratified,
> governance transitions to the named fold at the position
> immediately after the ratifying event. A transition, once
> crossed, is a permanent fact of the timeline: governance changes
> again only at the next ratified activation. The log's total order
> makes same-position transitions inexpressible.

This is the constitutional pattern: the old rules govern the
adoption of the new rules. The circularity is gone because
"effective, ratified, unsuperseded" is the incumbent's judgment,
made with the semantics that were already in force. Irrevocability
makes the protocol total over everything actors can legally do to
activation-related acts, because there is no retirement state
machine to leave uncovered:

- A supersession of the activation *statement* that lands before
  its ratification prevents the transition — the incumbent judges
  ratification of a retired statement ineffective. Superseding that
  supersession before ratification restores ratifiability. All of
  this is ordinary incumbent jurisdiction over ordinary acts.
- A supersession targeting the activation or its ratification after
  the transition necessarily lands after the ratifying event (total
  order), hence after the transition: it is visible dissent with no
  governance effect.
- Retiring a predecessor activation while a successor governs has
  no governance effect, and no later event can be stranded by it,
  because fallback does not exist: **reversion is re-activation** —
  to govern by an earlier fold again, activate it again, through
  the same ratified path.

Governance is therefore monotone: every boundary is the position of
a ratifying event, fixed in the total order; no forward event
rewrites, re-governs, or reverts an earlier span; and there is no
fallback target that might itself have been retired. An activation
confers nothing between its statement and its ratification, so
replay agrees about that span before and after the ratification
lands. Verdicts stay append-fixed by construction.

**The initial binding.** The span before the first transition must
not keep the ambient-build ambiguity this design exists to end. A
new workroom names its initial fold in the profile's seed statement
— the same act that seeds the roster — under the same publication
contract; reading that one field is meta-rule vocabulary, as the
genesis descriptor's fields are kernel vocabulary. An existing
workroom's first activation carries an explicit **prefix binding**:
the named fold governs from genesis to the transition. This binds
the previously unbound — it changes no in-band verdict, because the
prefix had none; it replaces each reader's ambient build with one
declared, ratified, contestable choice. It carries one consistency
obligation, checkable by any reader: judged under the named prefix
fold, the first activation and its ratification must themselves
come out effective — a prefix binding that invalidates its own
adoption is `uninterpretable`, typed. A log with neither seed
binding nor ratified prefix binding projects as **unbound** — a
stated condition, not a silent default.

The meta-rule — walk, delegate judgment to the incumbent,
transition irrevocably after ratification, read the initial
binding — earns the status of the canonical intent encoding:
kernel-adjacent, exactly specified, versioned almost never. It is
the only interpreter left outside the log.

### Publishing a fold

`path@commit` alone closes neither audit nor execution, and the
note must not pretend otherwise.

**Reachability.** `refs/seq/*` does not advertise source commits,
and fetch-by-bare-hash is not a portable Git contract. A fold cited
by an activation must be reachable from a published ref in a
dedicated namespace — `refs/folds/*` — published alongside
`refs/seq/*`; `gs attach` configures both refspecs. An activation
whose target is unreachable from that namespace projects
`uninterpretable: interpreter not held`, carrying the ref as a
hint. The cure is fetching the published fold refs — still a fetch,
not a software release, but a *specified* fetch.

**Execution.** A source path specifies no entry point, interface,
toolchain, or dependency closure, so the activation body carries
all four: the entry package, the meta-interface version it
implements — the interface the meta-rule drives, comprising
judgment, projection, and the state-handoff function invoked at a
seam — and the pinned toolchain. Content closure is the repo's:
the cited commit must **vendor its entire module closure**, so the
fold builds from repository objects alone — no network, no proxy,
no resolution. The pinned toolchain is the one input the
repository does not carry, and it is named as exactly that. The
fold must be platform-independent pure Go — no cgo, no unsafe, no
ambient I/O, no platform conditionals — and its determinism
obligation is over *bytes, not builds*: readers on different
machines building the same pinned toolchain version must produce
identical projection bytes, with the seam fixtures as the check.

Every unmet prerequisite is typed and named, never folded into a
generic failure: `uninterpretable: interpreter not held` (objects
unreachable), `toolchain not held`, `vendor incomplete`, `does not
build` (the entry package is invalid, or compilation or
construction fails — build success is itself a prerequisite, and a
raw compiler error is not a projection outcome), `interface
unsupported`, `fixture mismatch`. The reproducibility
claim is correspondingly conditional and stated: reproducing
meaning requires holding the published fold refs and the pinned
toolchain. A reader lacking either still holds a fully verified
record — signatures, order, intent bindings — plus a typed, named
gap where meaning would be. Never a silent one.

### Consequences

- **Piecewise projection.** Positions are judged by the fold in
  force at their span; verdicts stay append-fixed. Fold state
  carried across an activation boundary needs an explicit, tested
  handoff — a schema migration's discipline, crossing the
  meta-interface's state-handoff function. The gate is golden
  fixtures *spanning* each boundary: the old fold's projection up to
  the seam, the new fold's beyond it, both pinned.
- **The trust asymmetry is named.** Verifying the *record* stays
  pure signature-checking, no execution. Reproducing the *meaning*
  means running, or reimplementing to spec, the code the log names.
  This is not a new cost — the fold was always code — but today it
  is ambient, and this makes it an explicit decision. A cautious
  auditor can verify the activation chain and match the named
  source against others' attestations without executing anything.
  Reproducible projection bytes — fixture-checked across machines
  and toolchain builds — and the pure-library shape keep "run what
  the log names" from meaning "trust a blob."

## Growth pressure, relocated honestly

Two different kinds of growth, with different costs. A new *kind*
composed from already-supported primitives is a ratified definition:
no release, no restart, no redeployment — this is the growth the
ontology gets for free. A new *primitive* — a new operator, a new
render class, a new lifecycle participation — is a new fold
implementation, landed through an activation under the publication
contract above, with its seam fixtures. "Ontology growth without
redeploying services" means exactly the first of these and never
the second. A definition naming a primitive its governing fold
lacks is `uninterpretable`, typed, recoverable by activating a
newer fold. Growth pressure lands on the primitive set through
activations, or it does not land — the kernel/profile discipline,
one level up.

## Migration

1. Starter kind-definitions for the ten current kinds, reproducing
   today's projections **byte-for-byte** against pinned golden
   transcript fixtures — which forces the fixture files the review
   found missing, and which stage 1 always required.
2. The ratified enumeration of the constraint algebra, with its
   total semantics, as the first implementing deliverable —
   separately reviewed, adopting or amending the candidate above.
3. The first fold-activation names the fold implementing this note,
   at its merge commit, published under `refs/folds/*`, and carries
   the prefix binding: the same fold governs from genesis to the
   transition — which is what every current reader already
   computes, now declared and checkable under the consistency
   obligation. The eighth-wave reinterpretation stays as it is,
   stated rather than implied.
4. `undefined kind` and `uninterpretable` join `disputed` as typed
   projection outcomes.
5. SKILL.md's per-kind prose moves into definitions' `guidance`;
   SKILL.md keeps the discipline and the loop, and points at the
   room's own vocabulary as the source of kind truth.

### Wave 1 implementation record

The first separately reviewed wave makes the candidate algebra concrete
without pretending the fold-execution runtime already exists:

- `fields` is canonical JSON containing an array of `{op,name}` records;
  `type` adds one of `string`, `event-id`, `actor-ref`, or `path-commit`,
  `matches` adds an RE2 `pattern`, and `one-of` adds a finite `values`
  array. Operators and operands are closed and rejected when mixed.
- `basis` is canonical JSON containing zero or more
  `{kinds,min,max}` count constraints. Every range is finite and every
  named kind must be defined at the definition statement's position.
- Satisfiers are `none`, `originating-requester`, or `role:<name>`;
  render classes are `note`, `proposal`, `commitment`, `result`,
  `dissent`, `artifact`, or `governance`; staleness modes are
  `propagates`, `terminal`, or `exempt`; lifecycle values are `none`,
  `request`, `promise`, or `report`. There are no extension strings.
- Every name a definition introduces — the kind, a role in a
  satisfier, a required body field, a basis kind — must be an
  identifier: lowercase ASCII words joined by single hyphens or
  underscores. A name outside that grammar is refused as
  `uninterpretable` rather than accepted into a catalog where no
  actor could ever satisfy it.
- Three kinds are read by the fold directly rather than through their
  definitions — `kind-def`, `fold-activation`, and `roster`, from
  which membership and authority are extracted — so none of them may
  be redefined. Redefining `roster` would let a participant name
  themselves as the satisfier of their own authority grant.
- Where several ratified declarations of one kind survive at once,
  the governing one is whichever holds the live ratification standing
  latest in the total order. Force arrives at ratification, so
  contesting a retirement restores a version without displacing one
  ratified after it, and no already-emitted verdict moves.
- The ten compatibility definitions and the two meta-kind definitions
  are one catalog used by validation, fold judgment, ratification,
  projection, and UI placement. A pinned legacy transcript fixture
  guards the pre-migration projection bytes. Ratified definitions take
  effect forward from their ratification; retirement removes them and
  retirement's supersession restores them without revisiting an earlier
  verdict.
- Status serves active definitions and their guidance plus the binding
  state. The application and UI surface `undefined-kind`,
  `uninterpretable`, and `unbound` rather than hiding an act or selecting
  an ambient interpretation.
- A prefix activation and its ratification now form an irrevocable
  recorded boundary. A prefix binding governs from genesis to its
  transition, so a record reaching no further than the transition
  projects as `bound`. Until the named interpreter can be fetched,
  checked, built, and handed state, meaning beyond that seam projects
  the typed `uninterpretable: activated interpreter execution is not
  held` gap — one gap, however many acts it refuses.
  The publication/execution trust boundary — `refs/folds/*`, reachability,
  pinned toolchain, vendor closure, construction, interface and seam
  fixtures — is assigned to child request 7bb74568 before implementation,
  rather than being smuggled into this UI-facing wave.

## Lineage

- **keep's tag-definition documents** — ontology as governed
  content, discoverable in-band; the machine/prose split adopted,
  hardened for adversarial replay. Keep's `act` taxonomy is the
  same Winograd/Flores family as the workroom's kinds; the two
  systems converged independently, which is evidence the seam is
  real.
- **Constitutional amendment** — the incumbent rules govern the
  adoption of their successors; gitseq adds a signed, ratified,
  contestable record of each transition, at a precise position.
- **Activation heights** (consensus systems) — rules-change-at-
  position, adopted.
- **Schema migrations** — the state-handoff discipline at
  boundaries.
- **CT's lesson stands** — the log records, readers judge. Nothing
  here runs in the kernel; admission still checks shape only.
- **The Coordinator, again** — kinds applied at deliberate
  promotion, never imposed on talk; definitions make the imposed
  layer *itself* contestable: a kind's meaning can be dissented
  from, superseded, and audited. The politics stay visible.

## The tax, stated

A definition interpreter is more machinery than ten hardcoded
kinds, and for a three-actor room it is not smaller. What it buys:
one governed source of truth where there are now three drifting
ones; free growth for kinds within the supported primitives; the
end of silent fold reinterpretation; and a self-describing room.
The costs it accepts: seam migrations must be engineered and
fixture-gated; the meta-rule becomes permanent, so it must be tiny
and exact; the fold-publication contract (refs, entry, interface,
toolchain) must be maintained as carefully as the intent encoding;
and ontology editing becomes a new authority surface, governed by
the same ratification, dissent, and supersession as everything
else.

## Open questions

1. The constraint algebra's ratified enumeration — which of the
   candidate operators survive, and what concrete grammar carries
   them? Purpose-built and small is probably truer than a schema-
   language subset; schema languages smuggle in more than an
   algebra's worth of semantics.
2. Do render classes belong in kind-definitions or in a separate
   projector profile? A definition says what an act *is*; how a
   page arranges it is arguably the projector's own governed
   document.
3. Cross-workroom definition sharing — can a room adopt another
   room's definitions by reference (a `Rests-On:` into a foreign
   log), or only by copying? Reference implies the foreign domain
   joins the audit closure.
4. Activation policy — is a bare ratifier grant enough authority to
   change the rules of interpretation, or does activation deserve a
   higher quorum stated in the roster? A values choice; the
   machinery supports either.
