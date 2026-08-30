---
date: 2026-08-30
status: candidate design; no UI implementation is authorized by this note
origin: Hugh's discussion with planner about seeing top-level requests and the
  work that flows from them, refined against a local interactive mock-up and
  the current Gitseq projection
---

# An outcome map for the work board

The board answers which requests are open. It does not yet show the larger
shape of the work: which requests began as operator-level outcomes, which
requests were discovered while pursuing them, which decisions gated later
work, and which attempts were replaced. That shape is present in the durable
log, but it is distributed across thread views and provenance links.

Add a second presentation of the same board population: a horizontal,
zoomable outcome map. Each card is one thread-level work item. Lines between
cards render only relations the log records. Clicking a card selects its
connected work flow; opening it replaces the map with the existing full-width
thread view. The current table remains the precise compact view.

This is a presentation design. It changes no workroom kind, fold rule,
authority boundary, or durable event shape. In particular, it does not infer
conditions from prose and does not claim that a request is blocked when the
projection cannot prove that.

## The operator's question

The map should make four questions cheap:

1. What are the top-level outcomes in this population?
2. What work flowed from each outcome, and through which recorded relation?
3. Who or what does the next visible step wait on?
4. Which branches are current, replaced, complete, or resting on reasoning
   that moved?

The detailed conversation is not part of this screen. It already has a good
home in the thread view.

## One card is one thread

A card represents the same thread identity a row in the current list opens.
It is not one card per durable statement. Promises, reports, artifacts,
reviews, ratifications, and supersessions normally remain inside their
thread. This keeps a long repair lane readable as work rather than as a
transcript.

The card carries only information needed to scan the map:

- ticket and short title;
- current projected state, always written as text;
- the actor named by the projection as `waits on`, or `not named` when the
  projection names nobody;
- the request author and, when compactly available, its direct governing
  basis, such as a ratified proposal or another request;
- ordinary staleness or attention as a qualifier, without replacing the
  lifecycle state; and
- an optional work-class label when that vocabulary exists.

The card does not summarize `body.conditions`. Those conditions remain plain
human-readable "done when" text in the thread. Parsing them would turn prose
into an unreliable hidden protocol and would make the picture claim
relations nobody signed.

## The displayed graph

The graph is a projection over the currently selected board population plus
the minimum recorded context needed to explain its relations.

### Nodes

The focal nodes are the rows in the chosen population after search is
applied. Several focal roots may occupy the leftmost column. A contextual
ancestor, decision gate, or replacement may remain visible even when it is
outside that population, but it must be marked `context` and must not be
included in the population count.

A thread is a root in this picture when it has no incoming displayed
thread-to-thread relation. That does not mean it has no durable basis. A
direct artifact or decision basis outside the displayed node classes is
shown as a compact basis chip on the card or in its selection summary.

### Direction

Lines run from basis to dependent, left to right. If event B records that it
rests on event A, the arrow is A to B. The tooltip says both readings so the
direction is not ambiguous:

> A is a direct basis for B. Recorded as B rests on A.

No transitive edge is drawn. Transitive reachability determines selection and
layout, not another asserted relation.

### Collapsing event relations to threads

A direct relation between events in two different threads becomes one
thread-to-thread line. Several direct relations between the same pair of
threads share one line; its tooltip lists each exact relation. Relations whose
two endpoints are inside one thread stay in the thread view.

This aggregation must retain the exact event identifiers. A user can focus an
edge, read every contributing relation, and open either endpoint thread. The
map is a compact index over durable facts, not a lossy source of new facts.

## Three visible relation families

The first version should distinguish three families. They all come from
recorded data, but they answer different questions.

| Relation | Meaning in the map | Default line |
| --- | --- | --- |
| direct basis | a statement in the dependent thread directly rests on a statement in the basis thread | solid |
| ratification gate | the dependent thread directly rests on a ratification act, or on a reviewed chain whose cross-thread gate is that act | dotted |
| supersession | a recorded supersession replaces or retires work represented by another visible thread | dashed |

The exact label appears when the line is hovered, focused, or selected. There
are no permanent words on every line; on a dense graph they would cover the
data they describe. Line style is repeated in the tooltip and legend, so the
distinction never depends on colour alone.

### Direct basis is the task-flow edge

Most discovered work is not supersession. It is a child request resting on a
parent request or on the promise under which the work was discovered. Those
recorded provenance edges are the main flow of the map and determine its
layers.

When a child rests on a promise, the collapsed line connects the promise's
request thread to the child's request thread. The tooltip names the exact
promise rather than pretending the child directly cited the parent request.

### Ratification is usually a gate, not another card

Ratification normally happens inside a thread and should not create a card.
It becomes graph-visible when another thread directly cites the ratification
act or the reviewed chain it authorizes. The collapsed line is styled as a
ratification gate and its tooltip names the target statement, the ratification
act, and the dependent event.

This makes a useful distinction visible: a proposal or review may exist, but
later work begins only after the recorded act that gave it force.

### Supersession is replacement history

Supersession usually records withdrawal, retirement, or a replacement attempt;
it does not usually represent a spawned subtask. It therefore supplies
important context without becoming the primary layout relation.

The current branch remains visually dominant. Replaced branches are subdued
and their dashed line says who superseded what. A view control may hide
supersession history after the graph semantics are established, but the
default mock-up should keep it visible so its value can be judged with real
work.

## Layout

The primary layout is horizontal:

```text
top-level outcome  ->  planned or discovered work  ->  repairs  ->  landing
top-level outcome  ->  another branch              ->  follow-up
top-level outcome  ->  gated decision               ->  implementation
```

Use a deterministic layered layout. Recorded basis relations determine the
earliest possible column. Supersession edges influence ordering within and
between adjacent layers but do not drag a replacement history into the main
flow when doing so would make the dependency order less clear.

The graph may become wider than the viewport. Provide pan, zoom, `fit`, and
`reset`. `Fit` may run when the population first opens or changes; it must not
override a zoom or pan the user has made while inspecting the same graph.
Repeated rendering of the same projection, population, and search must place
cards in the same locations.

Cycles and parallel relations are data the layout must survive. A cycle is
rendered with a returning line and a named warning in the selection summary;
it must not crash the view or silently drop an edge. Parallel relations share
a route only when the complete set remains available in the tooltip.

No layout library is selected here. The implementation should first define
and test the graph projection contract, then choose the smallest library or
local algorithm that satisfies it.

## Board controls stay outside the graph

The current population controls remain board-level controls:

- open;
- reasoning moved;
- stale, not in flight;
- completed;
- closed, not completed; and
- awaiting ratification.

Search remains beside them. Add `table | graph` as another board-level
choice. Changing presentation must not change the selected population or
search query. Counts continue to count focal rows only, never contextual
cards pulled into the graph.

Table mode retains the current column sorting and opens a thread directly.
Graph mode replaces table sorting with layout controls. It must not add a
second, independent interpretation of which rows belong to a population.

## Selection and the thread transition

A single click or keyboard activation selects a card and its connected
component, dims unrelated cards, and opens a narrow selection rail. Selection
does not change layout.

The rail is for rapid comparison, not for the full thread. It shows the card
fields, direct incoming and outgoing relations, exact edge labels, and an
`Open full thread` action. Opening the full thread replaces the board content
with the existing thread view at its current useful width. Back returns to
the exact population, search, presentation, selection, zoom, and pan position
where practical.

An edge is itself focusable. Selecting it highlights its two endpoint cards
and shows the same exact relation text available in its tooltip. Touch users
get the selected-edge summary rather than a hover-only feature.

## What the map can say about actionability

The map can make projected actionability easier to see, but it must not invent
it.

It can state:

- the lifecycle state the fold projects;
- the next actor the projection names in `waits on`;
- whether nobody is named;
- whether a promise, report, review, ratification, or merge-related event is
  present in the thread; and
- which recorded bases and replacements connect the work.

It cannot currently prove:

- that an actor is free to act merely because they are named;
- that an unnamed row is blocked;
- that one request must wait for another unless a recorded relation says so;
- that prose in `body.conditions` names a dependency; or
- that age means blockage rather than neglect, priority, or deliberate delay.

The visual must therefore use `waits on`, `not named`, and recorded state. It
must not synthesize a `blocked` badge from graph position, age, conditions, or
agent memory.

If durable execution blocking is later needed, design an optional explicit
event-to-event relation with its own meaning and lifecycle. Do not overload
the human-readable conditions field or the existing `waits on` actor.

## Work class is orthogonal to durable kind

The governed kinds describe speech and authority: request, promise, report,
artifact, propose, assert, and related acts. They do not currently say whether
the requested outcome is a feature, bug, chore, investigation, or governance
change.

If the proposed work-class vocabulary is adopted, show it as an independent
label. Do not add `feature` or `chore` as new durable kinds, and do not reuse
the lifecycle colour scale.

Use three redundant channels:

- projected state: restrained fill or tone plus written state;
- durable kind: written noun in the card detail or relation tooltip; and
- work class: compact neutral badge plus a simple icon or shape.

Unknown or absent work class is normal and must not make a card look broken.
The graph reader can ship before any writer requires the metadata. Historical
work remains readable.

## Stability, accessibility, and truthful failure

The implementation is acceptable only if these invariants hold:

1. A card and all of its incident rendered relations enter and leave a filter
   atomically. Filtering cannot leave disconnected arrows.
2. Selection, tooltip display, and rail opening do not move cards.
3. Auto-fit never overrides a manual view transform in the same graph.
4. Every visible line resolves to one or more exact recorded relations.
5. No relation is derived from conditions prose or agent memory.
6. No `blocked` state is synthesized.
7. The same inputs produce the same placement.
8. Cards and edges are reachable and operable by keyboard, with visible
   focus. Tooltips have a focus and touch equivalent.
9. State, relation family, selection, context, and work class do not depend
   on colour alone.
10. A malformed or cyclic relation set produces a bounded warning and a
    usable partial graph, not a blank board.

Rendering should stay bounded. Build the collapsed graph once per projection
frontier, then filter and lay out the selected population. Avoid walking the
full provenance graph separately for every card. The first implementation
should measure the current workroom's open population and a deliberately
dense fixture before selecting thresholds for edge aggregation or context
limits.

## Proposed implementation slices

This note authorizes none of these slices. After adoption and review, file
implementation requests in dependency order.

1. **Graph projection.** Define thread nodes, exact collapsed relations,
   contextual inclusion, and deterministic fixtures. No drawing yet.
2. **Read-only graph presentation.** Horizontal layout, cards, lines, labels,
   pan, zoom, fit, selection, and accessibility. Use the existing population
   and search state.
3. **Navigation.** Selection rail, full-thread transition, and exact state
   restoration on back.
4. **Optional work-class reader.** Only after its vocabulary is independently
   adopted. Missing metadata remains ordinary.
5. **Optional explicit blocking relation.** A separate design, only if the
   operator still needs machine-readable execution blocking after using the
   map with real data.

Each slice should preserve the current table as a complete fallback. The
first release should not add graph editing or drag-to-rewrite provenance; the
map reads the workroom.

## Review questions

Independent review should answer these questions against the exact note head:

- Does the collapse from durable events to thread cards preserve the meaning
  and exact identity of every visible relation?
- Are ratification and supersession visible where they add task-flow context
  without turning the map back into an event transcript?
- Can the chosen root and context rules make a focal row appear top-level in
  a misleading way?
- Does the `waits on` presentation stay within what the projection proves?
- Are table and graph two presentations of one population, rather than two
  competing board models?
- Are layout, filter, focus, and navigation invariants precise enough to test?
- Is any part of the design simpler to remove without weakening the
  operator's four questions?

