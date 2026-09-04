---
date: 2026-09-04
status: candidate plan; implements nothing
origin: request
  git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f221f5affa0a71bbf434e7cf7f7d2ed28f8653fc
  (planner to claude), filed after Hugh said on 2026-09-04 that the graphical
  task UI is confusing. His four examples: Tailapp shows two open requests but
  only one visible chain; lineage is hard to follow in a large graph such as
  Chess "closed, not completed"; selecting a node should make its chain clear;
  and links that return to the same column are extremely hard to read.
evidence: three live workrooms, read once and pinned so later movement cannot
  erase the reading. Snapshot A at 2026-09-04T21:24Z, snapshot B at
  2026-09-04T21:32:52Z to 21:33:39Z, screenshots at 21:32:59Z to 21:33:50Z.
  Frontiers, genesis / head / depth.
  gitseq  5d26227488... / d93004541da179859f1a67112b179761d83a4372 / 17533 (A and B).
  Tailapp da732b0bda... / 54890ee31de9f06800b89848ab8ea30322cd4d7f / 3396 (A),
                          4140a2c0d1f1a306ee05e599a3132cd8b05d0acb / 3443 (B).
  Chess   1b96687964... / 00ed78b3475f66c9c893a009d84852e1fdf7dfd1 / 511 (A),
                          975a1528a751f617e4e87ba4d006ee22c7b7f3aa / 519 (B).
  Raw status dumps, pipeline replays, edge statistics and screenshots are in the
  investigation scratchpad under ui-evidence/. Five screenshots are committed
  beside this note. Every source citation is file:line at gitseq main
  be3b069cc8e013e9ebff6b9e0a70ef5e10c2cf5b, which is what the Tailapp and Chess
  residents actually serve.
---

# Navigating the task graph

Hugh reports four things. Three of them are real defects in the last two steps
of the pipeline, where a correct thread graph becomes pixels. One is a correct
aggregation that the screen never explains. This note reproduces all four,
names the cause of each at a line of source, and proposes three changes that
form one interaction design.

This note implements nothing. It authorizes nothing. Each change below needs
its own request.

## 1. What Hugh sees, reproduced

All three rooms were read through the UI's own modules. The replay scripts
import `ui/src/lib/rows.ts`, `ui/src/lib/outcomeMap.ts` and
`ui/src/lib/outcomeMapLayout.ts` directly, so the numbers below are the ones
the browser computes, not an approximation of them.

The Tailapp resident on `:7778` and the Chess resident on `:7779` both serve
bundle `index-BoOBW1Hh.js`. Its outcome-map sources are byte-identical to main
at `be3b069cc`. The gitseq resident on `:7777` is a separate matter; see
section 9.

### Example 1: Tailapp, "N open" but one chain

Population `live`, search empty, table sort at the default priority order. The
open count was 4 at snapshot A and 0 at snapshot B, eight minutes later.
Hugh's remembered "2 open" is a third point on the same curve, so the
mechanism was measured over every two-row subset of the four open rows in
snapshot A.

All six two-row subsets render as exactly one chain. The tightest case is
`#3393 + #3396`: two focal cards plus two context cards, six edges, one chain.

- `#3393` = `git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:1efc21198ff0976b794223d0b840bfa64c8cd726`,
  state `reported`.
- `#3396` = `git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:54890ee31de9f06800b89848ab8ea30322cd4d7f`,
  state `unclaimed`.

`#3396` records `rests_on` naming
`git:sha1:da732b0b...#git:sha1:f73a504a0610774f62c38f36af2e7c02c603fb0a`, which
is the report inside `#3393`'s thread. The connection is genuine, direct and
one hop. It is not invented and it is not transitive.

At snapshot B the open population is empty, so the graph does not render at
all: `ui/src/components/RequestList.tsx:200-203` short-circuits on
`rows.length === 0` before either presentation. Screenshot
`tailapp-live-table.png`.

### Example 2: Chess "closed, not completed", a large graph

Population `closed`, search empty, snapshot B.

Tab count and headline 14. Cards 13 focal plus 12 context. Edge groups 44.
Canvas 1320 x 2286 px inside a graph pane measured at 1022 x 510 px, so
fit-to-view is 0.2091 and a card is 52 x 26 px. Columns L0=5, L1=15, L2=3,
L3=2. 27 of the 44 edges are same-column, that is 61%, and the longest spans
1800 px vertically, about 15 card heights. 13 of the 44 edge labels are drawn
inside a card rectangle. Three cycle warnings, one of them a 9-thread cycle.

14 rows become 13 cards because request
`git:sha1:1b96687964cbba5a6089c526d7136b32ba9488a5#git:sha1:a75107775805addb8e6ca077f69f42692c2d1ae5`
(ticket #63) carries two commitment lifecycles, one `withdrawn` and one
`cancelled`, and renders as one card showing one of the two words.

Screenshots `chess-closed-graph.png` and `chess-closed-graph-selected.png`.
The selected card is #41,
`git:sha1:1b96687964cbba5a6089c526d7136b32ba9488a5#git:sha1:1a9f848fdbd3e8ba1d502e8e9852cbdae5f1da61`.
Clicking it emphasises 20 of 25 cards and dims 5.

### Example 3: Chess open, the small readable tree

Population `live`, search empty, snapshot B. Five rows, five focal cards plus
three context cards, 20 edges, fit scale 0.75. This is the case where the
drawing is legible, so it isolates the interaction defects from the density
defects.

```
col 0 (context)      col 1        col 2                  col 3
04443664 assert      #505         #506 in progress       #507 unclaimed
fb0ee436 request     awaiting     #511 in progress
37c2e25e report      merge        #517 in progress
```

Selecting #506,
`git:sha1:1b96687964cbba5a6089c526d7136b32ba9488a5#git:sha1:025a6e0168967478a876d5e6bb95afd36495d72c`,
emphasises 8 of 8 cards and dims nothing. Screenshot
`chess-live-graph-selected.png`. Seven of the 20 edges are same-column, and
four labels are drawn inside a card. The overlapping `rests on` and
`ratified by` text is visible in the left column.

## 2. Root causes

### (a) The count and the drawing count different things

**Classification: data defect for the mismatch, correct but unexplained
aggregation for the single chain.**

Trace.

1. **Population.** `ui/src/components/RequestList.tsx:80` calls `workRows`.
   `ui/src/lib/rows.ts:190-224` emits one row per entry in
   `projection.commitments`, keyed at `rows.ts:210` by
   `commitment.promise ?? commitment.report ?? commitment.request`.
   `RequestList.tsx:163` prints `filteredPopulations[key].length` on the tab
   and `RequestList.tsx:118-125` prints the same number as the headline.
   **The tab counts commitment lifecycles.**
2. **Graph membership.** `RequestList.tsx:102-106` hands exactly those rows to
   `buildOutcomeMap`. `ui/src/lib/outcomeMap.ts:186-204` maps each row to
   `rootOf(row.event)` and adds it to a set at `outcomeMap.ts:190`. One entry
   per thread root survives, and `outcomeMap.ts:192-200` lets the latest
   lifecycle supply the card's title and state. **The graph counts thread
   roots.**
3. **Bound.** `outcomeMap.ts:205-213` caps the focal set at
   `limits.nodes`, default 160. The overflow is warned about, but that warning
   goes into `graph.warnings` and renders as a bullet at
   `OutcomeMap.tsx:227-231`. The prominent bounded line at
   `OutcomeMap.tsx:222-226` reports only `omittedContextNodes` and
   `omittedEdges`, and `outcomeMap.ts:512-517` computes `focalNodes` as
   `focal.size`, a thread count that is already capped. **The headline number
   of omitted focal threads never reaches the screen except as a sentence in a
   list.**

Measured mismatches at the captured frontiers:

| room and population | tab count, rows | distinct threads | focal cards | focal threads omitted | cards holding more than one lifecycle |
| --- | --- | --- | --- | --- | --- |
| Chess `closed` | 14 | 13 | 13 | 0 | 1 |
| Tailapp `closed` | 79 | 77 | 77 | 0 | 1 |
| gitseq `closed` | 925 | 905 | 160 | 745 | 19 |
| gitseq `completed` | 1419 | 1414 | 160 | 1254 | 5 |

The gitseq case is the sharpest. The tab says 925 and the drawing shows 160,
and the only notice is one line in a bulleted warnings list below the fold.

**The second, larger mechanism.** Even when every row gets its own card, the
rows are joined into one visible chain by context expansion.
`outcomeMap.ts:314-323` adds every thread one hop away from a focal thread.
`outcomeMap.ts:448-453` keeps a context card only when a complete relation is
incident on it. Those context cards are the connective tissue.

This is correct. The joining relations are directly recorded, single hop, and
each retains its exact contributing event identifiers. The screen simply never
says "these two open requests are one chain because #3396 rests on the report
in #3393". `RequestList.tsx:196` says "same selected rows as the table, with
bounded direct context", which is true and answers nothing.

**Where a person finds every counted request today.** Only in the table. The
table lists exactly the counted rows and nothing else. The graph, the tab
count and the headline are three numbers with no stated relationship, and
nothing on the graph screen points back to the table as the complete list.

**Thread collapse is not a cause here.** `ui/src/lib/threads.ts:71-84`
(`crossesCommitment`) already refuses to treat a citation across a commitment
boundary as containment. In every population dumped, the number of rows
collapsed into another row's thread is zero.

### (b) Lineage is unreadable in a large graph

**Classification: rendering defect. The topology is correct.**

Trace.

1. **Layering.** `outcomeMap.ts:578-615` condenses the graph on non-superseded
   relations (`outcomeMap.ts:586-587`), then assigns each condensed component a
   longest-path depth (`outcomeMap.ts:602-612`). Every member of one strongly
   connected component receives the same column (`outcomeMap.ts:614-615`).
2. **Cycles are real.** They come from thread condensation. A review artifact
   in thread B rests on the request in thread A, and the next request in thread
   A rests on that artifact. Two forward event edges become a two-thread cycle.
   Chess `closed` has a 9-thread cycle; Tailapp `closed` has eleven cycles up
   to 8 threads.
3. **Intra-column ordering is dead code in practice.**
   `outcomeMap.ts:623-625` builds the same-column ordering graph from
   `relation.family === "superseded"` edges only. Across all three rooms there
   are 137, 1167 and 4579 effective supersede acts, and **every single one is
   intra-thread**, so `outcomeMap.ts:231` and `outcomeMap.ts:269` drop them
   before they can become a relation. Not one `superseded` edge was drawn in
   any of the five graphs measured. The 25 `rests-on` edges inside Chess
   column 1 are ignored for ordering. The 15 cards in that column are placed by
   the previous column's incoming rank and then by stable event order
   (`outcomeMap.ts:651-671`).
4. **Geometry.** `ui/src/lib/outcomeMapLayout.ts:21-47` stacks each column at a
   fixed 122 px card height plus a 28 px gap. Columns are independent stacks.
   Nothing aligns a dependent vertically with its basis.
5. **Scale.** The pane is a fixed `min-h-[32rem]`, 512 px, at
   `OutcomeMap.tsx:103`, and measured 1022 x 510 in the browser.
   `fitOutcomeScale` at `outcomeMapLayout.ts:54-58` takes
   `min(1, w/W, h/H)`, so a tall canvas is scaled by height. Chess `closed`
   fits at 0.2091. Tailapp `closed` fits at 0.0720, which is a card 18 x 9 px,
   and the drawing is then about 144 px wide inside a 1022 px pane, wasting
   86% of the available width. Screenshot `tailapp-closed-graph.png`.
6. **Labels.** `OutcomeMap.tsx:161` draws a permanent `<text>` label on every
   edge, positioned at `OutcomeMap.tsx:134-135`. For a same-column edge that x
   is `source.x + 124`, the horizontal centre of the source card. Measured
   collisions: Chess `live` 7 overlapping label pairs and 4 labels inside a
   card; Chess `closed` 24 and 13; Tailapp `closed` 66 and 39.

Point 6 is not only ugly, it contradicts the design this map implements.
`notes/2026-08-30-outcome-map.md:136-139` says the exact label appears on
hover, focus or selection, and that "there are no permanent words on every
line; on a dense graph they would cover the data they describe". That is
exactly what happened.

### (c) Selection does not reveal the chain

**Classification: correct but poorly explained aggregation, and it is faithful
to the design note, so it is a design defect rather than an implementation
one.**

`OutcomeMap.tsx:78-82` computes the emphasised set from
`connectedComponent(graph, selectedThread)`. `OutcomeMap.tsx:30-46` walks the
graph **undirected**, pushing both `relation.target` and `relation.source`.
`OutcomeMap.tsx:145`, `:180` and `:192` then dim only what falls outside that
component. `notes/2026-08-30-outcome-map.md:235-236` specifies exactly this.

The affordance therefore answers "what is in the same blob as this?" and Hugh
is asking "what does this rest on, and what came of it?".

Measured over every card in each graph, comparing the current undirected
component with a directed walk (ancestors along `rests-on` and `ratified-by`
backwards, continuation forwards). The last three columns are the card
actually clicked in the browser.

| room, population | cards | undirected mean | directed mean | clicked card, undirected | ancestors | continuation | directed union |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Chess `live` | 8 | 8.0 (100%) | 6.8 (84%) | 8 of 8 | 5 | 2 | 6 of 8 |
| Chess `closed` | 25 | 16.4 (65%) | 8.5 (34%) | 20 of 25 | 11 | 10 | 12 of 25 |
| Tailapp `closed` | 107 | 31.7 (30%) | 11.0 (10%) | 24 of 107 | 9 | 1 | 9 of 107 |

Chess `closed` #41 sits inside the 9-thread cycle, so 8 cards are both its
ancestors and its continuation. That overlap is a true fact about the log and
the design must say so rather than draw a line that is not there.

The only other lineage signals are the three boolean tags at
`OutcomeMap.tsx:169-173`. In Chess `live` all three context cards carry
`Basis outside view` and none carries `Root of view`, so the leftmost column
states that its own upstream is off screen and offers no way to reach it.

**Keyboard.** There is no keyboard lineage navigation and no `Escape`. The
only `tabIndex` in the file is on the edge group at `OutcomeMap.tsx:139`. The
`<svg>` block at `OutcomeMap.tsx:122-165` precedes the card buttons at
`OutcomeMap.tsx:166-210` in DOM order, so tab order visits every edge before
any card. On Chess `closed` that is 44 stops before the first card.

**Losing context.** `RequestList.tsx:11-21` deliberately lifts sort, query,
population and presentation into `ListView` so they survive opening a thread.
Selection, zoom and pan are not lifted: they are local state at
`OutcomeMap.tsx:60-64`, and `ui/src/App.tsx:262-288` unmounts `RequestList`
when a thread opens. Opening a card and pressing back returns to a refitted
graph with nothing selected. `notes/2026-08-30-outcome-map.md:238-243` asked
for the opposite.

### (d) Links that return to the same column

**Classification: correct data, defective rendering.**

`outcomeMapLayout.ts:63-70` always starts an edge at `source.x + 248`, the
right edge of the source, and ends at `target.x`, the left edge of the target,
with `bend = max(44, |endX - startX| / 2)`. For a same-column edge
`startX = x + 248` and `endX = x`, so the curve leaves rightwards, sweeps 124
px to the **left** of the column, and comes back. It crosses the column band
twice. The comment at `outcomeMapLayout.ts:60-62` says this is deliberate, so
that a line never appears to point into empty space.

No edge is drawn right to left anywhere. Measured backward edges: zero in all
five graphs. What Hugh reads as "backward" is this loop.

There is a second readability cost. Long jumps are drawn as one wide curve
passing behind the intervening columns with no routing to avoid them: Chess
`live` has six such edges, one of them spanning three columns.

## 3. What a chain is, from the record

The map draws threads, not events. A thread's internal lifecycle never becomes
an edge, because `outcomeMap.ts:231` and `outcomeMap.ts:269` drop any relation
whose endpoints share a root. Inside one card the fold records the ordinary
commitment lifecycle: request, promise, artifact or report, review verdict,
ratification, merge receipt. That is the thread view's business, and the card
is the handle on it.

Between cards, only three families exist, all built at
`outcomeMap.ts:215-312`.

**Ancestry and continuation.** `rests-on` and `ratified-by` are the same
projected relation, `projection.provenance`, split by whether the fold names a
ratification for that exact basis (`outcomeMap.ts:232-238`). Both run basis to
dependent. Walking them backwards from a card gives its ancestry. Walking them
forwards gives its continuation. These are the only edges the layout uses for
columns (`outcomeMap.ts:586-587`), and they are the only edges that should
define a chain.

**Transfer.** `commitments.successor_request` (`outcomeMap.ts:284-311`) is the
fold's own statement that a rejected or refiled round moved to another
request. That is continuation, not retirement. It is currently given the family
`superseded` at `outcomeMap.ts:304`, so it is drawn dashed and red like a
retirement and its legend word is `superseded`
(`OutcomeMap.tsx:9`, `:15`, `:91`). Only the tooltip text distinguishes it.
The contributor already carries `kind: "successor-request"`
(`outcomeMap.ts:9`, `:303`), so separating it is a read of a projected field,
not an inference. gitseq has 32 commitments with a `successor_request`;
Tailapp and Chess have none.

**Retirement.** A `supersede` act naming its target (`outcomeMap.ts:255-282`)
is secondary. It says a pointer was withdrawn. It is not a step along a chain
and must never be walked as one.

**Siblings.** Two cards are siblings when they share an immediate basis and
neither reaches the other. They are not on each other's chains. In Chess
`live`, #511 and #517 are siblings of #506: all three rest directly on #505.

**Secondary, everything else.** A context card is on the drawing because a
relation touches it, not because it is in the population
(`outcomeMap.ts:448-453`, `outcomeMap.ts:485`). Being in the same weakly
connected component is not a relation at all. That is the current selection
rule, and it is what change 2 replaces.

So: **a chain is a directed path of `rests-on` and `ratified-by` edges, plus
`successor_request` transfers, between thread cards.** Nothing else.

## 4. The remediation

Three changes. They are one design: the count line says what you are looking
at, selection says which part of it is yours, and the routing and scale make
that part readable. Each is a simplification of an existing function.

Common constraints for all three. No new dependency. No relation the fold does
not project. No invented edge. No hiding of unfinished work. No change to
population membership, to the thread-collapse rule, or to any fold contract.
All three are layer 7 only, within the boundary at
`docs/reference/architecture.md:1533-1535`: the browser may combine projected
fields for presentation and may not name a relation layer 5 does not project.

### Change 1: one counting function, one honest line

**Priority: first. It is the truth defect.**

Add one exported function to `ui/src/lib/outcomeMap.ts`, next to
`OutcomeMapStats`, that takes the focal rows and the built graph and returns
the reconciliation: rows counted, cards drawn, cards that stand for more than
one lifecycle, separate chains, context cards, focal threads omitted by the
bound. `RequestList.tsx:163` and `RequestList.tsx:118-125` keep printing the
row count, because that is what the population is. `RequestList.tsx:196`, the
one-line graph subtitle that currently says nothing useful, prints the
reconciliation.

```
14 counted over 13 threads, 13 drawn as 4 chains, 12 context cards, 0 not drawn
925 counted over 905 threads, 160 drawn as 94 chains, 745 threads not drawn
```

"745 threads not drawn" is a button that switches the presentation to `Table`,
which already lists exactly the 925 counted rows. That is how a person finds every
counted request: one existing control, reused. No new screen and no new
filter.

Touches: `outcomeMap.ts` (one new exported function over data it already has),
`RequestList.tsx:186-198` (the subtitle), `OutcomeMap.tsx:222-226` (fold the
context and edge omissions into the same sentence rather than a second one).

Why a simplification: three numbers on one screen currently come from three
places and disagree. After this they come from one function and are stated
together.

Never: do not count context cards in the population number
(`docs/reference/architecture.md:1508`). Do not change which rows are counted.
Do not omit the omission.

### Change 2: selection is directed lineage

**Priority: second. It is the highest-leverage change for Hugh's actual task.**

Replace `connectedComponent` at `OutcomeMap.tsx:30-46` with two directed walks
over the structural relations, producing four classes instead of one:

- **before**, the ancestry: walk `rests-on` and `ratified-by` from target to
  source, transitively.
- **after**, the continuation: walk the same edges from source to target, plus
  `successor_request` transfers.
- **also before and after**, when the card sits in a genuine cycle and is
  reachable both ways. Named honestly on the selection bar, never hidden.
- **sibling**, a card sharing an immediate basis with the selection and on
  neither path.

Everything else dims. Retirement edges are drawn but never walked.

Card styling. The path keeps full contrast with the selection ring at
`OutcomeMap.tsx:193`. Ancestry and continuation are distinguished by the
direction of the emphasised edge, not by two new colours. Siblings keep their
border and lose their fill emphasis. Everything else uses the existing
`opacity-25` at `OutcomeMap.tsx:192`.

The existing selection bar at `OutcomeMap.tsx:213-220` becomes the path
readout and the navigation control:

```
Selected: codex, implement item 5 of the second-application plan natively
11 before, 10 after, 8 of those are both: this card is inside a 9-thread cycle
[<- previous]  [next ->]  [Open full thread]
```

Left and right move the selection one step along the path and pan to keep the
new card in view at the current scale. `ArrowLeft` and `ArrowRight` do the
same while a card has focus. `Escape` clears the selection and refits.

Also in this change, because they are the same defect: move the `<svg>` block
after the card buttons in DOM order so tab reaches a card before an edge, and
lift `selectedThread`, `scale` and `offset` out of `OutcomeMap.tsx:60-64` into
`ListView` (`RequestList.tsx:15-21`) so back from a thread restores them, as
`notes/2026-08-30-outcome-map.md:238-243` already asked.

Touches: `OutcomeMap.tsx:30-46`, `:60-64`, `:78-82`, `:122-210`, `:213-220`;
`RequestList.tsx:15-21`; `notes/2026-08-30-outcome-map.md:235-236` needs its
sentence changed, and `docs/reference/architecture.md:1505-1513` needs the
selection sentence changed with a candidate artifact in the same head.

Why a simplification: one undirected walk becomes two directed walks over the
same edge list. The graph model does not change at all.

Never: do not walk a supersede act as continuation. Do not merge ancestry and
continuation when a cycle makes them overlap; say the overlap. Do not invent a
"blocked" relation from graph position, which
`notes/2026-08-30-outcome-map.md:263-276` already forbids.

### Change 3: routing, labels and pane height

**Priority: third. It makes the emphasised path readable.**

Three small edits, each restoring something already decided or measured.

1. **Delete the permanent edge label** at `OutcomeMap.tsx:161`. The label
   already exists on hover, focus and selection through the tooltip at
   `OutcomeMap.tsx:221` and the `aria-label` at `OutcomeMap.tsx:141`.
   `notes/2026-08-30-outcome-map.md:136-139` already forbids the permanent
   one. This removes 44 pieces of text from Chess `closed`, 13 of which are
   currently drawn inside a card, and 159 from Tailapp `closed`, 39 of them
   inside a card.
2. **Route a same-column edge inside its own column.** Give
   `outcomeEdgePath` at `outcomeMapLayout.ts:63-70` a same-column branch: leave
   the source at its bottom or top edge, run a short vertical connector offset
   into the 32 px margin band beside the column, and enter the target at its
   top or bottom edge. The arrowhead then states the direction directly, the
   line never crosses the column band, and no line points into empty space,
   which is what the comment at `outcomeMapLayout.ts:60-62` was protecting.
   This changes 27 of 44 edges on Chess `closed` and 71 of 159 on Tailapp
   `closed`.
3. **Let the graph pane use the height it has.** `OutcomeMap.tsx:103` fixes the
   pane at `min-h-[32rem]`. In a 1600 x 900 window that leaves the pane at 510
   px inside a page that has more. Measured effect of a 900 px pane: Chess
   `closed` fit rises from 0.2091 to 0.3797, Tailapp `closed` from 0.0720 to
   0.1308. That is a card of 94 x 46 px instead of 52 x 26 px on the Chess
   example.

Touches: `outcomeMapLayout.ts:63-70`, `OutcomeMap.tsx:103`,
`OutcomeMap.tsx:128-165`.

Why a simplification: it deletes a feature, replaces one path formula with a
two-case formula, and changes one layout class. It adds no control and no
library.

Never: do not fold a column with a "+N more". The graph already has an honest
global bound at `outcomeMap.ts:205-213`; a second, per-column hiding mechanism
would be a new way to lose unfinished work, and change 1 fixes the reporting
of the bound that already exists.

### Two alternatives that were measured and rejected

**Re-layering columns by lineage depth along the chain, instead of longest
path over the condensed graph.** Rejected for two reasons. First, the current
layering already guarantees that a basis is never drawn to the right of its
dependent, which `docs/reference/architecture.md:1509` requires; a depth walk
over a graph containing genuine cycles does not. Second, it does not pay.
Re-ordering Chess `closed` column 1 by its own intra-column structural edges
moves the number of same-column edges that point downward from 17 of 27 to 17
of 27, and by fold sequence to 18 of 27. Inside a 9-thread cycle no ordering
can make every edge point one way. The gutter route in change 3 carries the
direction on the arrowhead instead, which works whatever the order is.

**Fit-to-selection.** Rejected as a change of its own. Fitting to the
emphasised cards' bounding box raises Tailapp `closed` from 0.0720 to 0.1656
and Chess `closed` only from 0.2091 to 0.2407, because the emphasised cards
keep their original vertical spread. Stepping along the path at a readable
scale is both simpler and better.

## 5. Before and after

### The small tree: Chess `live`, #506 selected

Before. Fit scale 0.75, eight cards, 20 edges, seven of them looping out of
their own column and back, every edge carrying a permanent label. Clicking
#506 emphasises all eight cards. Screenshot `chess-live-graph-selected.png`.

```
5 open requests    same selected rows as the table, with bounded direct context

  col 0                col 1            col 2                col 3
 +--------------+     +----------+     +---------------+    +--------------+
 | 04443664  <--+-.   | #505     |     | #506          |    | #507         |
 | assert       |  |  | awaiting |---->| in progress   |--->| unclaimed    |
 | Basis outside|  |  | merge    |  .->|               |    |              |
 +--------------+  |  +----------+  |  +---------------+    +--------------+
        | rests on |       ^        |
        v          |       |        |  +---------------+
 +--------------+  |       |        |  | #511          |
 | fb0ee436  ---+--'       |        |  | in progress   |
 | request      |----------'        |  +---------------+
 | Basis outside|  rests on         |
 +--------------+                   |  +---------------+
        | ratified by               |  | #517          |
        v                           |  | in progress   |
 +--------------+                   |  +---------------+
 | 37c2e25e     |-------------------'
 | report       |   ratified by, +2 columns
 +--------------+

every card at full contrast; the three left cards cite each other in a
3-thread cycle whose edges loop 124px out of the column and back
```

After. Same layout, same cards, same edges. The count line reconciles. The
selection separates. Same-column edges run in the gutter. No permanent labels.

```
5 open requests
5 counted over 5 threads, 5 drawn as 1 chain, 3 context cards, 0 not drawn

  col 0                col 1            col 2                col 3
 +==============+     +==========+     +###############+    +==============+
 | 04443664     |     | #505     |     | #506          |    | #507         |
 | assert       |====>| awaiting |====>| in progress   |===>| unclaimed    |
 | before       |     | merge    |     | SELECTED      |    | after        |
 +==============+     | before   |     +###############+    +==============+
        |  gutter     +==========+       |
        v                 ^   ^          |  +---------------+
 +==============+         |   |          |  | #511          |   dimmed,
 | fb0ee436     |=========+   |          |  | in progress   |   sibling
 | request      |             |          |  +---------------+
 | before       |             |          |
 +==============+             |          |  +---------------+
        |  gutter             |          |  | #517          |   dimmed,
        v                     |          |  | in progress   |   sibling
 +==============+             |          |  +---------------+
 | 37c2e25e     |=============+
 | report, before|
 +==============+

Selected: Codex, revive dropped second-application item 6 ...
5 before, 2 after, 0 of those are both
[<- previous]  [next ->]  [Open full thread]

===  on the selected path        ---  present, not on the path
###  the selection itself        gutter  same-column edge inside its column
```

Six of eight cards emphasised. The two siblings dim. That is the whole answer
to "what does this rest on and what came of it", read off the drawing.

### The dense case: Chess `closed`, #41 selected

Before. Fit scale 0.2091, cards 52 x 26 px, 15 of 25 cards in column 1, 27 of
44 edges looping out of their own column and back, one of them spanning 1800
px, 13 labels drawn inside a card. Clicking #41 emphasises 20 of 25.
Screenshot `chess-closed-graph-selected.png`.

```
14 closed, not completed     same selected rows as the table, with bounded direct context

 col 0 (5)         col 1 (15 cards, 25 internal rests-on edges)      col 2 (3)  col 3 (2)
 [ctx]         .-->[1.0]---.
 [ctx]--------'   [1.1]    |    every one of these curves leaves the
 [ctx]            [1.2]    |    card on the RIGHT, sweeps 124px LEFT
 [FOCAL]          [1.3]<---+--. of the column, and comes back in
 [ctx]            ...      |  | on the LEFT of a card in the same
                  [1.7]#---+--' column, up to 1800px away
                  ...      |
                  [1.13]---'
 20 of 25 cards stay at full contrast when [1.7] is clicked
```

After. Same cards, same columns, same edges. Selection cuts the drawing to the
path. Same-column edges become short arrows in the column gutter. The pane
uses the window height, so fit rises to 0.3797 and a card is 94 x 46 px.

```
14 closed, not completed
14 counted over 13 threads, 13 drawn as 4 chains, 12 context cards, 0 not drawn

 col 0 (5)             col 1 (15)              col 2 (3)     col 3 (2)
 [0.0] dimmed          [1.0] dimmed            [2.0] dimmed  [3.0] dimmed
 [0.1] before =====.   [1.1] dimmed            [2.1] dimmed  [3.1] dimmed
 [0.2] before ====.|   [1.2] dimmed
 [0.3] dimmed    ||    [1.3] both   =v=
 [0.4] dimmed    |'===>[1.6] both   =v=
                 |     [1.7] SELECTED ###======>[2.2] after
                 '====>[1.8] both   =v=
                       [1.9] both   =v=
                       [1.10] both  =v=
                       [1.11] both  =v=
                       [1.12] both  =v=
                       [1.13] both  =v=
                       [1.4] [1.5] [1.14] dimmed

Selected: codex, implement item 5 of the second-application plan natively
11 before, 10 after, 8 of those are both: this card is inside a 9-thread cycle
[<- previous]  [next ->]  [Open full thread]

=v=  same-column edge as a short arrow in the column gutter, direction shown
```

Twelve of 25 cards emphasised instead of 20: two ancestors in column 0, the
selection, one continuation in column 2, and the eight cards that are both
because they share the selection's 9-thread cycle. Thirteen cards dim. The
cycle is stated in words rather than drawn as a knot, which is honest: those
eight cards really do cite the selection in both directions and no drawing can
make that a line. Pressing right walks the continuation one card at a time,
panning as it goes, which is how a 107-card Tailapp graph becomes navigable
without ever being legible as a whole.

## 6. Implementation sequence

Three steps, one per change, each its own request, each its own review. All of
it is layer 7. No projected field needs to be added, so no layer 5 or 6 change
is implied and no fold version is touched. If a later step finds it needs a
field the fold does not project, that is a layer 5 change and needs its own
request; it must not be derived in the browser
(`docs/reference/architecture.md:1533-1548`).

**Step 1, the count line.**
Files: `ui/src/lib/outcomeMap.ts`, `ui/src/components/RequestList.tsx`,
`ui/src/components/OutcomeMap.tsx`.
Validation, at rendered altitude in `ui/test/dom.test.mjs`: with two completed
lifecycles on one request the tab reads 2, the graph draws 1 card, and the
count line states both; with the focal bound forced below the population size
the line states the omitted number and its button switches to `Table`; the
table then lists every counted row. `ui/test/dom.test.mjs:814-854` already
asserts the 2-rows-to-1-card collapse and must be extended, not replaced.

**Step 2, directed lineage selection.**
Files: `ui/src/components/OutcomeMap.tsx`,
`ui/src/components/RequestList.tsx`, `notes/2026-08-30-outcome-map.md`,
`docs/reference/architecture.md`.
The architecture page states the selection behaviour at lines 1505 to 1513, so
this step changes a documented contract and must update that page in the same
head and publish its candidate artifact, per AGENTS.md step 4.
Validation: on a fixture with a known ancestry, a continuation, a sibling and
a cycle, selecting a card emphasises exactly the ancestors and the
continuation, marks the cycle members as both, marks the sibling as sibling
and dims the rest; a supersede edge is drawn and is not walked; `ArrowRight`
moves one step and `Escape` clears; tab order reaches a card before an edge;
opening a thread and returning restores the selection, scale and offset.

**Step 3, routing, labels and pane height.**
Files: `ui/src/lib/outcomeMapLayout.ts`, `ui/src/components/OutcomeMap.tsx`.
Validation: a same-column edge's path starts on a horizontal card edge and
never leaves the band between its column's left margin and its right margin; a
cross-column edge is unchanged; no `<text>` element is rendered for an
unselected, unfocused edge while the accessible name still carries the exact
relation reading; the existing bounded-rendering test at
`ui/test/dom.test.mjs:758-812` still passes at both maximum legal shapes, and a
new case renders the 107-card Tailapp shape within a fixed time and node
budget.

**Acceptance, stated as Hugh's own test in the request.**

1. He can account for "2 open" at a glance. The count line names the counted,
   the drawn, the chains and the omitted, and the omitted are one click from
   the complete list.
2. He can follow a chosen task's predecessor and successor path in the dense
   Chess example without untangling unrelated links. Emphasis falls from 20 of
   25 to 12 of 25, siblings are marked as siblings, and the previous and next
   controls walk the path one card at a time.
3. He can identify current work and its delivery state. Unchanged: the card
   already shows the projected lifecycle state and `waits on`, and this plan
   changes neither.
4. He can move between overview and detail predictably. `Escape` returns to
   the overview, and opening a thread and coming back restores the selection,
   the zoom and the pan.

**Usability walkthrough against the captured examples.**

Tailapp, open population, at a frontier where the count is 2. Open the board.
The tab says 2 and the count line says "2 counted over 2 threads, 2 drawn as 1
chain, 2 context cards, 0 not drawn". The words "1 chain" are the answer to Hugh's first
complaint, stated before he has to look for it. Switch to `Table` to see both
counted rows by name. Switch back and click either card. The path lights up
through the context card that joins them, and the selection bar says how many
cards are before and after. Press right to step to the next card on the path.
Press `Escape` to return to the whole view.

Chess, closed population. The tab says 14 and the count line says "14 counted
over 13 threads, 13 drawn as 4 chains, 12 context cards, 0 not drawn". The
gap between 14 and 13 is now on the screen instead of silent, and the table
is one click away for the row that shares a card. Click the
closed-not-completed card #41. Twelve of 25 cards stay lit; the selection bar
says 11 before, 10 after, and that 8 of those are both because the card sits
in a 9-thread cycle. Press left repeatedly to walk back through the ancestry
into column 0; press right to walk forward into column 2. Press Enter on the
selected card to open its thread; press back to return to the same selection,
zoom and pan.

## 7. Unresolved product choices

**One card per commitment, or one card per thread.** Recommended: keep one
card per thread, and state the difference in the count line. One card per
commitment would draw two cards with the same title and no edge between them
for Chess #63, which is worse than one card that says it holds two lifecycles.
It would also break the stated contract at
`docs/reference/architecture.md:1498` and the design at
`notes/2026-08-30-outcome-map.md:55-61`. The mockup in section 5 shows the
recommended answer: "14 counted over 13 threads" in the count line, with the
table one click away for the row that shares a card.

**Do context cards render, or are they only counted.** Recommended: keep
rendering them. They are the reason two open requests are one chain, and
hiding them would leave the chain claim unexplained. The count line already
separates them from the population, and change 2 dims the ones that are not on
the selected path, which is the real complaint. Shown in both after-mockups as
a separate "context cards" clause and as dimmed cards.

**Should `successor_request` keep the `superseded` family.** Recommended: no.
It is a transfer and it should read as continuation, with its own line style
and its own legend word. It is currently indistinguishable from a retirement
except in the tooltip (`outcomeMap.ts:304`, `OutcomeMap.tsx:9`, `:15`, `:91`).
This is not visible in Chess or Tailapp, which record none, but gitseq records
32. If change 2 ships without this, the continuation walk must still follow
`successor-request` contributors and must still refuse `supersede-act` ones.

**Is the `superseded` legend earning its place.** Open question, no
recommendation yet. Across all three rooms every one of the 5883 effective
supersede acts is intra-thread, so the dashed red style at `OutcomeMap.tsx:91`
has never rendered in these workrooms. It should not be removed on three
rooms' evidence, but it should be measured again before anyone spends effort
on it.

**Should `fit` become fit-to-selection.** Recommended: no, not on its own.
Measured and rejected in section 4; stepping along the path is simpler and
works better.

**Is the stale `:7777` bundle this plan's problem.** Recommended: no. File it
separately as a deployment fix. See section 9.

## 8. Regression checks

| check | covered today | new work |
| --- | --- | --- |
| tab count equals the rows the table lists | `ui/test/dom.test.mjs:814-854` asserts 2 rows and 1 card | assert the count line states both numbers and the chain count |
| collapse is honest | `dom.test.mjs:836` asserts the collapse happens | assert the screen says a card holds more than one lifecycle |
| the focal bound is reported prominently | not covered | new: force the bound below the population and assert the line, not only the warning bullet |
| context stays out of the population count | `outcome-map.test.mjs:347-377`, `dom.test.mjs:715-723` | unchanged |
| direction, basis left of dependent | `outcome-map.test.mjs:275-291` | unchanged; change 3 must not move a card |
| every edge starts and ends on a card boundary | `outcome-map.test.mjs:275-291` | extend to the new same-column route |
| selection emphasises a component | `dom.test.mjs:736-742` | replace: assert ancestors, continuation, cycle overlap, sibling and dimmed, on a fixture with all four |
| a supersede edge is drawn and not walked | not covered | new |
| keyboard: arrows step the path, `Escape` clears | not covered | new |
| tab reaches a card before an edge | not covered | new |
| selection, zoom and pan survive opening a thread | not covered | new |
| bounded rendering at both maximum legal shapes | `dom.test.mjs:758-812` | extend with the 107-card real shape and a time budget |
| edge labels are not permanent | not covered | new: no `<text>` for an unselected edge, accessible name unchanged |
| repeated presentation and population changes leak nothing | `dom.test.mjs:814-854` | unchanged |

Determinism is already covered at `outcome-map.test.mjs:235-246` and must stay
green: none of these changes may make placement depend on serialization order,
on table sort, or on which card is selected.

## 9. Separate observation: the gitseq resident serves a stale bundle

The resident on `:7777` (pid 3619, started 03:52 local, `./bin/gs serve`)
serves `index-Gw2yBtUp.js`, a build from before the outcome map landed. It has
no `Table | Graph` control at all. `bin/gs` and `~/.local/bin/gs` on disk both
report `vcs.revision=eb519a16604207f453c1e6148dad235af831603d`, built
2026-09-04T11:42:22Z, and `eb519a166:internal/service/uidist/index.html`
embeds `index-BoOBW1Hh.js`, which is what `:7778` and `:7779` serve. The
process is holding the assets it was started with.

Consequence for this note: every source citation is valid for what Hugh sees
on Tailapp and Chess, and none of it is valid for the gitseq room, which has
no graph. Consequence for anyone reading the gitseq board: they are looking at
an older UI without knowing it.

Recommended action, not taken here: file a separate request to restart the
gitseq resident on the current binary, and consider whether the served page
should state the build it was started from so a long-lived process cannot
silently diverge from the binary on disk. This note does not restart anything.
