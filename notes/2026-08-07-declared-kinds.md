---
date: 2026-08-07
status: proposal — awaiting ratification; on adoption the design note
  gains its wave entry by an ordinary commit resting on the decision.
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
  satisfier:   who may ratify acts of this kind (roster role | originating-requester | none)
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

The normative half is a **closed declarative constraint language** —
the eight fields above, interpreted by a fixed primitive set — never
embedded code. The `guidance` half is the agent-facing text SKILL.md
now carries per kind, moved to where a fresh actor's `status` can
serve it: the room describes its own vocabulary. The split is
keep's: its tag-definition documents separate machine-readable
declarations (`_constrained`, `_inverse`, `_singular`,
`_value_regex`) from prose and classifier prompts. Keep can afford
prompts as behavior because one trusted service interprets them;
a fold that every replayer must reproduce cannot. Here the machine
half is deterministic and the prose half is explicitly powerless.

What this closes, from the review: validation misses (required
fields and basis shape become checkable for *any* kind, not the
compiled ten); the silent-opacity of unknown kinds; hardcoded render
omissions (dissent is invisible on the status page today because
rendering is code — under declared render classes, omitting a kind
requires superseding its definition, an audited act); and the
artifact-basis convention (an `artifact` definition whose `basis`
requires a governing decision turns SKILL.md discipline 8 from hope
into a checked rule).

What it honestly does not close: the commitment loop's attribution
logic (who superseded, who may declare satisfaction) stays
interpreter machinery — definitions name states, they do not compute
them. And no definition can know the world changed; it can only
require the citation that lets the change propagate.

## Fold activations

A fold activation is a governance statement binding interpretation
to an implementation *in this repository*:

```
state {kind: fold-activation}  body: {
  fold:   path@commit of the fold implementation
  from:   its own position, implicitly — activation is forward-only
}
```

The repo is the substrate and the security domain; the overlay
stores no artifacts, and the fold is an ordinary artifact. So the
interpreter for every span of the log lives in the same clone the
auditor already holds, content-addressed, and the log states which
one governs where. Changing the rules becomes a move in the game
the rules govern — the design note's sentence, "fold definitions
are themselves logged in the practice's own log," made concrete.

**The meta-rule is the frozen fixed point.** Something must
bootstrap. The rule that cannot itself be activated in-band:

> Walk the log. At each position, the governing fold is the one
> named by the newest effective, ratified, unsuperseded
> fold-activation at or before that position; before any
> activation, the bundled fold governs. Apply it.

That paragraph earns the status of the canonical intent encoding:
kernel-adjacent, exactly specified, versioned almost never. It is
the only interpreter left outside the log, and it is a paragraph,
not a program.

Consequences, each with its posture stated:

- **Piecewise projection.** Positions are judged by the fold in
  force at their span; verdicts stay append-fixed. Fold state
  carried across an activation boundary needs an explicit, tested
  handoff — a schema migration's discipline. The gate is golden
  fixtures *spanning* each boundary: the old fold's projection up to
  the seam, the new fold's beyond it, both pinned.
- **Audit closure widens.** Fetching `refs/seq/*` does not fetch the
  branch holding the cited fold. `gs attach` must bring activation
  targets over, and a missing interpreter object is a typed, stated
  absence — `uninterpretable: interpreter not held` — cured by a
  fetch, not a software release. Strictly better than the status
  quo, where the cure for version skew is out-of-band upgrade.
- **The trust asymmetry is named.** Verifying the *record* stays
  pure signature-checking, no execution. Reproducing the *meaning*
  means running, or reimplementing to spec, the code the log names.
  This is not a new cost — the fold was always code — but today it
  is ambient, and this makes it an explicit decision. A cautious
  auditor can verify the activation chain and match the named
  source against others' attestations without executing anything.
  Reproducible builds and the fold's pure-library, no-cgo shape keep
  "run what the log names" from meaning "trust a blob."

## Growth pressure, relocated honestly

The primitive set — the eight definition fields and the constraint
language's operators — is still finite and still lives in code. The
difference is that its versions are now in the repo and bound
position-by-position from the log, so "which primitives does this
span understand" has an in-band answer. A definition that names a
primitive its governing fold lacks is `uninterpretable`, typed,
recoverable by activating a newer fold. Growth pressure lands on
the primitive set through activations, or it does not land — the
kernel/profile discipline, one level up.

## Migration

1. Starter kind-definitions for the ten current kinds, reproducing
   today's projections **byte-for-byte** against pinned golden
   transcript fixtures — which forces the fixture files the review
   found missing, and which stage 1 always required.
2. The first fold-activation names the fold implementing this note,
   at its merge commit. History before it is governed by the bundled
   fold, per the meta-rule's default — the eighth-wave
   reinterpretation stays as it is, now stated rather than implied.
3. `undefined kind` and `uninterpretable` join `disputed` as typed
   projection outcomes.
4. SKILL.md's per-kind prose moves into definitions' `guidance`;
   SKILL.md keeps the discipline and the loop, and points at the
   room's own vocabulary as the source of kind truth.

## Lineage

- **keep's tag-definition documents** — ontology as governed
  content, discoverable in-band; the machine/prose split adopted,
  hardened for adversarial replay. Keep's `act` taxonomy is the
  same Winograd/Flores family as the workroom's kinds; the two
  systems converged independently, which is evidence the seam is
  real.
- **Activation heights** (consensus systems) — rules-change-at-
  position, adopted; gitseq adds a signed, ratified, contestable
  record of the change itself.
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
ones; an ontology that grows without redeploying services; the end
of silent fold reinterpretation; and a self-describing room. The
costs it accepts: seam migrations must be engineered and fixture-
gated; the meta-rule becomes permanent, so it must be tiny and
exact; ontology editing becomes a new authority surface, governed
by the same ratification, dissent, and supersession as everything
else.

## Open questions

1. The constraint language's concrete shape — a CBOR/JSON-schema
   subset, or a smaller purpose-built vocabulary? Smaller is
   probably truer; schema languages smuggle in more than eight
   fields' worth of semantics.
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
