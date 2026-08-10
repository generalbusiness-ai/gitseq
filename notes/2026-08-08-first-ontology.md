---
date: 2026-08-08
status: draft proposal — for review. Companion to
  2026-08-07-declared-kinds. Revised 2026-08-09 against the merged
  declared-kinds implementation: sections marked **built** describe
  what the fold does today and cite the code; everything else is
  proposal. Where the two disagreed, the code won and the proposal
  was restated. Revised again after review, where three claims that
  the code does less than it does — no surface computes an affordance,
  render routes acts between two views, nothing derives CLI or MCP
  behaviour from definitions — were replaced by the boundary each one
  actually sits at.
origin: the 2026-08-08 conversation on a fully data-driven ontology,
  and a review of keep's tag-definition implementation (`.tag/*`
  documents, write-path constraints, edge materialization, and the
  state-doc rule engine).
---

# The first ontology

The declared-kinds note specified the mechanism: definitions as
ratified governance statements, resolved position-aware, with a
finite total constraint algebra and fold activations judged by the
incumbent. This note specifies the content: the definition
vocabulary, the first ontology itself — kinds, tags, edge classes,
enumerations — where definitions live, and the presentation contract
that makes every surface read the ontology instead of embedding it.

Most of this note is still proposal. One part of it is not. The
declared-kinds wave has since merged, so the workroom already carries
a starter catalog of kind definitions, a vocabulary projection, and an
interpreter boundary the catalog may not cross. Each section below
says which side of that line it stands on and cites the code for the
built claims. The constraint algebra's ratified enumeration remains a
later wave's deliverable.

## The anchoring use case

The CLI, the MCP server, and the UI must all present the *available
acts* from the definitions in force — never from compiled-in lists.
A Kanban board's columns fall out of the declared lifecycle
enumeration; within-column ordering falls out of a declared
`priority` tag. The design payoff is symmetry: the same definition
that lets the fold *judge* an act after append lets a surface
*offer* it before — one constraint, two directions. When the fold
would rule "promise actor is not the requested performer," the
affordance projection has already withheld the promise affordance
from everyone but the requested performer. No surface can drift
from the fold, because both read the same data.

This is the goal, not the state — but one surface has built a piece of
it by hand, and the boundary runs through that piece rather than around
it. `semanticActions` in `ui/src/components/Toolbar.tsx` computes a
row's offered acts from the fold's state and the viewer's position:
only the actor a request is addressed to is offered accept, only the
performer of a promised commitment is offered mark done, only the
requester of a reported one is offered accept or needs work, only a
statement's own author is offered withdraw, and a retired or
ineffective statement offers nothing at all. Those offers are
transition shortcuts that mirror the fold's conditions — but they are
narrower mirrors, not the conditions themselves, and one of them
already drifts. The agree offer goes to every viewer of an effective
proposal who has not already ratified it: `Toolbar.tsx` checks
neither the viewer's roles nor the kind's satisfier, while
`kinds.go` declares the `propose` satisfier `role:ratifier` and the
fold refuses a ratification from an actor without that role. A
participant without the ratifier role is therefore offered agree and
refused after append — exactly the authorization drift the projection
this note proposes exists to prevent.

What it is not is the projection this note proposes. The conditions are
written in TypeScript and keyed off compiled-in kind names, not derived
from the definitions in force; the vocabulary projection carries no
affordances for it to read; it withholds an action without naming the
condition that failed; and it covers one surface, so the CLI and the
MCP server still offer nothing and refuse afterwards. Call it a
hand-coded partial affordance projection. The presentation contract
section says how far each surface has got.

## Definition documents

A definition is an ordinary ratified governance statement, one
statement per definition, each independently supersedable. Only the
first of these five exists:

- **kind-def** (built) — what an act of this kind is, per
  declared-kinds. A ratifiable kind on main today.
- **tag-def** (proposed) — a named body field: its value constraint,
  cardinality, applicability, and (if event-valued) edge semantics.
- **value-def** (proposed) — one value of an enumerated tag, child of its
  tag-def. A value is valid because its value-def is effective and
  unretired at the act's position — enumerations grow by ratified
  data, and each value has its own supersession lifecycle. (keep's
  "the sub-doc's existence makes the value valid," hardened from
  existence to effectivity.)
- **edge-class-def** (proposed) — a named relation class with declared
  traversal semantics.
- **profile** (proposed) — a presentation arrangement (a board, a
  status page section) that cites ontology enumerations. Profiles are
  governed documents distinct from kind-defs: a definition says what
  an act *is*; a profile says how a surface arranges it. This is the
  answer this note offers to declared-kinds open question 2;
  amendment 1 explains why the question is not yet closed.

**The interpreter boundary.** An earlier draft of this note claimed
that the first ontology includes kind-defs for the definition kinds
themselves, so that ontology growth is governed by the ontology. The
merged implementation does not allow that, on purpose, and the claim
is withdrawn.

`internal/workroom/kinds.go` names three kinds as fold-interpreted:
`kind-def`, `fold-activation`, and `roster`, from which membership and
authority are extracted. The fold reads these directly rather than
through their definitions, and `validateDefinition` refuses any
ratified kind-def that would redefine one of them. The starter catalog
does carry entries for `kind-def` and `fold-activation`, so they have
required fields, a render class and guidance in the vocabulary
projection — but those entries are fixed. No room can change what a
kind-def means by ratifying a kind-def.

The reason is worth stating rather than working around. Redefining a
fold-interpreted kind would change what the fold does without changing
how the fold reads it: the log would say one thing and the running
interpreter another, with nothing in the log to mark the divergence.
So "ontology growth is governed by the ontology" holds strictly below
the boundary and not across it.

Self-governance across the boundary would require the fold's reading
rule to be data too — which is the evaluator and the meta-interface,
changed by a fold activation, not by a definition. That is where
declared-kinds already places the growth pressure, and this note does
not move it. The consequence for the proposals here is direct: any
definition kind the fold must itself read in order to apply — most
obviously `tag-def`, since tags would constrain act bodies — joins the
fold-interpreted set on the same terms and gets the same fixed entry.
Only the definition kinds the fold never reads, such as `profile`,
could be governed as ordinary data.

Bootstrap circularity below the boundary is handled as declared-kinds
handles fold succession: the seed pins the initial ontology and the
incumbent judges changes to it.

**Location and fallback** (proposed). Storage and binding are
distinct. Today the starter catalog is compiled into the fold, and a
kind whose declared definition is retired falls back to its starter
entry; nothing is written into a room's seed at `gs init`. The
proposal is that the gitseq repository carry the bundled first
ontology and that `gs init` pin it — by copy or by content address —
into the new room's seed; from then on the room would own its ontology
and change it only by ratified supersession. There would be no
read-time fallback to another repository: a projection never consults
one. Upgrades from upstream would arrive as proposed supersessions the
room ratifies, never automatically. (keep's model — bundled docs
seeded at init, store-authoritative at runtime, hash-guarded upgrade —
with the hash guard hardened into ratification.) Live cross-room
reference is deferred, as declared-kinds open question 3.

## Declaration vocabulary (proposed)

None of the three shapes below is built. They are proposed body
shapes for the three definition kinds that do not yet exist. Taken
from keep's census (its whole running ontology needs six
declaration keys), hardened for replay: every check total, every
anomaly a typed refusal, all resolution position-aware, absence
semantics specified — never inherited from a library's error
message.

```
tag-def body:
  name:        the tag key
  applies-to:  kinds on which this tag may appear
  values:      one of — by-value-defs (closed enumeration)
                      | matches(RE2)
                      | type(string | event-id | actor-ref
                             | path | commit)
  cardinality: one | many
  ordered:     whether the enumeration carries rank order
               (required for board columns and priority)
  requires:    tag that must be present for this one to apply
  edge:        for event-valued tags — class (edge-class-def ref),
               inverse name, whether staleness flows
  guidance:    non-normative prose; the fold never reads it

value-def body:
  tag:         parent tag-def
  value:       the literal value
  rank:        position within an ordered enumeration
  guidance:    non-normative prose

edge-class-def body:
  name:        the class
  staleness:   flows | does-not-flow
  closure:     whether targets join the audit closure
  guidance:    non-normative prose
```

kind-def keeps the declared-kinds shape, and the merged
implementation confirms it: `name`, `fields`, `basis`, `satisfier`,
`render`, `staleness`, `lifecycle` and `guidance` are all required of
a declared kind-def. `render` stays where declared-kinds put it; see
amendment 1.

Two changes this note first proposed to that shape are not built, and
stand as proposals:

- **fields as tag-def references.** Today `fields` is canonical JSON
  holding the four-operator constraint array declared-kinds
  specified — `present`, `type`, `matches`, `one-of` — and there is no
  tag-def to refer to.
- **an edge-class dimension on basis constraints**,
  `count(kind-set, edge-class, min..max)`. Today a basis constraint is
  `{kinds, min, max}` and names no class.

## The catalog today (built)

`starterCatalog` in `internal/workroom/kinds.go` carries **thirteen**
definitions. An earlier draft of this note listed ten application
kinds and five definition kinds, four of which do not exist; the real
catalog is the ten, plus `admission-profile`, `kind-def` and
`fold-activation`. Every entry declares `staleness: propagates` and
reports `source: starter`, which is how a surface tells a
compatibility definition from a ratified one.

| kind | required body fields | basis | satisfier | render | lifecycle |
|---|---|---|---|---|---|
| admission-profile | bundle, contract, genesis | — | role:ratifier | governance | none |
| artifact | path, commit | — | none | artifact | none |
| assert | — | — | role:ratifier | note | none |
| dissent | — | — | none | dissent | none |
| fold-activation | fold (path-commit), entry, interface, toolchain, prefix (one-of `genesis`) | — | role:ratifier | governance | none |
| infra-key | service, public_key | — | role:ratifier | governance | none |
| kind-def | name, fields, basis, satisfier, render, staleness, lifecycle, guidance | — | role:ratifier | governance | none |
| promise | — | count(1..1, request) | none | commitment | promise |
| propose | — | — | role:ratifier | proposal | none |
| report | — | count(1..1, promise) | originating-requester | result | report |
| request | to (actor-ref), conditions | — | none | commitment | request |
| roster | actor, name, role | — | role:ratifier | governance | none |
| seal | — | — | role:ratifier | governance | none |

Three groups of rules live in the fold rather than in these
definitions. A reader comparing the table against behaviour will trip
on all of them, so they are stated here:

- **roster** requires `actor`, `name` and `role` as field constraints.
  It requires `kind` too, but conditionally and by the fold: a roster
  state whose `role` is `participant` and whose `kind` is empty is
  refused as ineffective. Grants that are not participant grants carry
  no `kind`, and the earlier draft's flat "actor, name, role, kind"
  was wrong on both counts.
- **request**, **promise** and **report** carry their loop rules in the
  fold, keyed off the `lifecycle` field rather than off field
  constraints: a request's `to` must name an actor the fold knows at
  that position, a promise's actor must be that `to`, and only the
  promisor may report. The `basis` column above states the count rule
  only; the actor rules are not expressible in the algebra as it
  stands.
- **artifact** requires only that `path` and `commit` be present.
  Nothing checks that the cited commit carries a `Rests-On:` trailer.
  That is still the open artifact-world-basis remainder, not a rule
  the catalog restates.

`tag-def`, `value-def`, `edge-class-def` and `profile` are proposals
in this note and appear in no catalog. An act naming one of them today
is refused `undefined-kind`.

## Proposed additions to the catalog

**Tags.** Three of the fields above already carry a constraint beyond
presence: `to` is typed actor-ref, `fold` is typed path-commit, and
`prefix` is `one-of` a single value. The rest are bare presence
checks. Tag-defs would type them: `actor` as actor-ref; `role` and
`kind` as closed enumerations; `conditions`, `name`, `service`,
`public_key` as strings. Typing `path` and `commit` separately would
also mean extending the built type set, which is exactly `string`,
`event-id`, `actor-ref` and `path-commit`. Plus one optional tag that
exercises ordered enumerations end to end:

- `priority` — applies-to request; cardinality one; ordered,
  by-value-defs: `high` > `normal` > `low`; absent means `normal`.
  Rooms extend it by adding value-defs, not by touching code.

**Enumerations.**

- roles: `operator`, `ratifier`, `participant`. The implication the
  fold applies — an operator holds ratifier and participant too — is
  computed, not declared, and would stay computed.
- principal kinds: `human`, `agent`, `service` (historic records may
  project `unspecified`; the compatibility boundary stays where the
  eighth wave put it).
- lifecycle states, *derived*: the fold already computes exactly eight
  — mainline `open` → `promised` → `reported` → `satisfied`, and
  exceptional `withdrawn`, `cancelled`, `reneged`, `stale`. Declaring
  them would name what the fold computes and give the mainline a rank
  order; the exceptional four stay unordered.
- priority, *asserted*: `high`, `normal`, `low`, ordered.

The derived/asserted distinction is normative. A work item's
lifecycle state is computed by the fold from the commitment loop
and can never be asserted by a tag — unlike keep, where status is
self-reported. Priority is asserted, because it is a claim of
intent, not a fact of the loop. The ontology declares both
enumerations; only one may appear in an act's body.

**Edge classes.** A basis constraint on main is `{kinds, min, max}`
and names no class, so all of this is proposal. Two classes:
`governs` (staleness flows; targets in the audit closure) and `cites`
(staleness does not flow) — adopting the candidate from declared-kinds
and answering the assert-2503e6de incident, that retiring a draft must
not flare a chain which merely cited it. The classed edges would be
basis edges: each kind-def basis constraint would name the class of
the edges it demands, and an unclassed basis edge would be `governs`,
which is today's semantics stated rather than changed. Event-valued
*tags* could declare edges later; the mechanism is defined here and
the first ontology would ship none.

## The presentation contract

**Built.** The fold serves a **vocabulary projection**: the
definitions in force at head, plus the binding state of the
interpreter (`bound`, `unbound`, or `uninterpretable`). It is one
projection for the room, not one per actor, and it names no
affordances.

The UI is the surface that reads it most, and it reads it for real. It
is not the only reader: the CLI and the MCP server each read it once on
the write path, as the bullets below record.

A kind's declared **render** class comes from the definition rather
than from a compiled-in list. Two behaviours follow the declared class
alone, and two keep compiled-name compatibility exceptions:

- **stream exclusion** follows the declaration. `belongsInRoom` in
  `ui/src/lib/util.ts` keeps an act out of the room stream when its
  class is `governance` or `artifact`, and `Stream.tsx` skips those
  statements. That is an exclusion and not a placement: render says
  what the stream leaves out, and says nothing about what the Work
  drawer holds.
- **the proposal tally** follows the declaration. A statement whose
  class is `proposal` carries a ratification tally on its row.
- **tint** does not follow it alone. `kindTint` gives the danger
  colour to a declared `dissent` render, but for every other render it
  falls back to a compiled-in map keyed by kind name — so a live
  definition that reclassifies the kind `dissent` as `note` stays
  danger-coloured, because `legacyKindTint` still matches the compiled
  name. `kinds.go` permits that redefinition.
- **dissent attachment** does not follow it alone either. `Stream.tsx`
  attaches a statement under its target when its declared render is
  `dissent` *or* its compiled kind name is `dissent`, so the kind
  `dissent` stays attached even when a live definition declares a
  different render.

**Lifecycle**, not render, is what finds work. The fold keys the whole
commitment loop off the declared `lifecycle` field —
`internal/workroom/fold.go` reads `request`, `promise` and `report`
lifecycles to decide what a promise may rest on, who may report, and
what status a commitment holds — and publishes the result as the
projection's commitments. `buildWorkProjection` in `ui/src/lib/work.ts`
builds the Work drawer from those commitments. The compiled-in kind
names `request` and `propose` serve only its ancestry discovery: the
topic walk treats an ancestor of one of those kinds as a topic root.
When no such ancestor exists, the builder falls back to the
commitment's own request event, so a custom kind with lifecycle
`request` still becomes a Work root. The compiled names bias grouping;
they do not gate rooting, and no declared field participates in
either.

So the two views are not an either/or, and an earlier draft of this
note was wrong to say render decides between them. A request appears in
the room stream, because its class is `commitment` rather than
`governance` or `artifact`, *and* it is the root of a Work topic,
because a commitment names it. Reading render as a router between two
surfaces predicts the wrong thing.

A vocabulary panel lists every definition with its render class, its
source, guidance, satisfier, staleness and lifecycle. Where the UI
holds no vocabulary at all it falls back to compiled-in kind names — a
stated compatibility path, not the design's end state.

**Proposed.** The rest of the contract, surface by surface, each with
whatever part of it is already standing:

- **Affordances.** Per authenticated actor, for each kind, whether
  that actor could perform it now, with the satisfied and unsatisfied
  conditions named. None of that is in the projection: the vocabulary
  carries definitions and binding state and no actor-relative judgement
  at all. What is built is the hand-coded stand-in named in the
  anchoring section, `semanticActions` in
  `ui/src/components/Toolbar.tsx` — the fold's conditions for seven row
  offers, written against compiled-in kind names, in one surface,
  offering no reason to the actor it withholds an action from. Six of
  its offers and the fold's conditions agree today because someone
  wrote them twice; the seventh, agree, already disagrees — it checks
  no roles where the fold requires `role:ratifier` — which is the
  drift this contract exists to prevent, present tense. The CLI
  and the MCP server withhold nothing: they learn a promise actor is
  wrong when the fold refuses the appended act. Deriving the conditions
  from definitions, publishing them per actor, and giving every surface
  the same answer is unbuilt.
- **CLI.** `gs state --kind <kind>` is already the generic verb and no
  subcommand is named after a kind, so the substance of the earlier
  `gs act <kind>` proposal is met; the rename is not the point and is
  dropped. One thing here is definition-driven already: after a `state`
  act lands, `warnUndefinedKind` in `cmd/gs/main.go` projects the log
  and calls `Vocabulary.UndefinedKindWarning`, which names the kinds
  this room actually defines and tells the author the act was recorded
  but no rule reads it. The list of kinds in that message comes from
  the live definitions, so a room that declared its own kinds names its
  own. Two things remain proposed. `kind` is still a free string flag,
  and help text, required-field prompting and value completion come
  from nothing — the read happens after the append, so it can warn but
  cannot offer. And `actor-add`, `role-grant` and `role-revoke` compose
  roster bodies from compiled-in field names, which is exactly the
  embedding this contract is meant to end.
- **MCP.** The tool list is a fixed set of eight hand-written tools
  with hand-written input schemas, and `state` accepts an open `body`
  object of strings. The one definition-driven part is the same warning
  as the CLI's: `withKindWarning` in `cmd/gitseq-mcp/main.go` attaches
  `Vocabulary.UndefinedKindWarning` to the `state` result. Generating
  descriptions and schemas from definitions, and making the tool list
  the affordance list, is unbuilt.
- **UI boards.** A board profile naming an ordered enumeration for its
  columns (lifecycle) and an ordered tag for within-column ranking
  (priority, then log position) needs both profiles and tag-defs.
  Neither exists, so neither does the board.

The standing rule for a surface that cannot interpret what it is
shown: render the typed conditions declared-kinds provides — the
`undefined-kind` and `uninterpretable` verdicts, and the `unbound`
binding status — never a built-in guess. The UI honours this for acts
it holds a projection about, except for the two compiled-name
compatibility exceptions recorded in the render section: tint and
dissent attachment still consult the compiled kind name even when a
live definition says otherwise. The CLI and the MCP server honour it
in the narrow write-path form named above. The UI's behaviour when it
holds no vocabulary at all is the compiled-in fallback, and closing
that gap — both gaps — is part of the affordance work.

## What stays outside the ontology

The floor, restated: the envelope and its signatures; the three
verbs as the meta-rule needs them; the seed-reading rule; the
evaluator for the definition language, versioned as the
meta-interface. A definition naming a primitive its evaluator lacks
is `uninterpretable`, typed — growth pressure lands on evaluator
releases, exactly as declared-kinds places it.

## Amendments to 2026-08-07-declared-kinds

These amend the declared-kinds *design*. None of them is built, and
the first has a built mechanism standing against it.

1. **`render` in kind-defs: withdrawn as an amendment, restated as a
   migration.** The earlier draft moved `render` out of kind-defs into
   profile documents and called open question 2 answered. It is not
   answered. `KindDefinition.Render` is a required field of every
   definition, validated against a closed set of seven render classes;
   the starter `kind-def` requires `body.render`; and the UI reads
   `definition.render` to exclude acts from the room stream, to decide
   which statements carry a ratification tally, and — for the
   `dissent` class only, with a compiled-name fallback otherwise — to
   tint badges.
   Profiles do not exist. Moving `render` out is therefore a
   migration with three parts — a profile kind, a superseding kind-def
   for every kind in the catalog, and a UI that reads profiles — and
   open question 2 stays open until someone is willing to pay for it.
   What can be said now is narrower than the earlier claim: the closed
   render-class set is a projection *hint* and cannot express an
   arrangement, so it cannot express a board. That is the argument for
   profiles; it is not yet a decision.
2. **"Definitions name states, they do not compute them" weakens.**
   Definitions also *order* states, and the direction of travel is
   that the calculus eventually expresses the loop's computation too.
   How much of the fold's rule set the ratified calculus can express
   is that deliverable's question; the enumerations and constraints in
   this note are its target.
3. **The `governs`/`cites` extension is adopted as design, not merely
   noted.** No basis constraint carries a class today, so adoption
   here commits the proposal, not the code.

## Open questions

1. The calculus enumeration (inherited; now with this note's
   ontology as its acceptance target).
2. The exact pinning form at `gs init` — copy into the seed, or
   content-address into the gitseq repo's objects.
3. Cross-room ontology reference (inherited).
4. Authority for ontology edits — bare ratifier grant or a stated
   quorum (inherited).
5. Whether `priority` ships in the first ontology or as the worked
   example of room-local extension.
6. Which of the proposed definition kinds fall inside the interpreter
   boundary. `tag-def` almost certainly does, because the fold would
   have to read it to apply a tag constraint, which makes it a fixed
   entry the catalog cannot redefine. `profile` almost certainly does
   not. `value-def` and `edge-class-def` are undecided, and the answer
   sets how much of the ontology can grow by ratification alone.
