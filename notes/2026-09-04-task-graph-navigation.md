---
date: 2026-09-04
status: candidate plan; implements nothing
origin: request
  git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f221f5affa0a71bbf434e7cf7f7d2ed28f8653fc
  (planner to claude), filed after Hugh said on 2026-09-04 that the graphical
  task UI is confusing. His four examples: Tailapp shows two open requests but
  only one visible chain; lineage is hard to follow in a large graph such as
  Chess "closed, not completed"; selecting a node should make its chain clear;
  and links that return to the same column are extremely hard to read. Repaired
  under child request
  git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:12a29f21c86b782a8fb5ec233ebfc920246dfa9f
  after codex requested changes.
evidence: the measured source at gitseq main
  be3b069cc8e013e9ebff6b9e0a70ef5e10c2cf5b, whose behaviour this note
  documents: ui/src/components/OutcomeMap.tsx, ui/src/components/RequestList.tsx,
  ui/src/lib/outcomeMap.ts, ui/src/lib/outcomeMapLayout.ts, ui/src/lib/rows.ts,
  ui/src/lib/spine.ts. Measurements, root-cause traces, screenshots and runnable
  replay scripts with their outputs are in
  notes/2026-09-04-task-graph-navigation/evidence/, which states the frontiers
  and separates observed screenshots from synthetic replays. Three workrooms
  were read once and pinned on 2026-09-04 between 21:24Z and 21:34Z: gitseq at
  depth 17533, Tailapp at 3396 and 3443, Chess at 511 and 519.
---

# Navigating the task graph

Three changes answer Hugh's four complaints. The evidence directory beside this
note holds the measurements, root-cause traces and screenshots. This note
implements nothing; each change needs its own request.

## 1. Vocabulary

**Card**: one thread, the identity a table row opens (`outcomeMap.ts:186-204`).
One request with several commitment lifecycles is one card, so rows and cards
are different counts.

**Group**: a weakly connected component over the drawn relations. A group may
branch and may contain cycles, so the screen says "group", never "chain".

**Walkable**: a relation carrying a `provenance` (`outcomeMap.ts:243`) or
`successor-request` (`:303`) contributor. A `supersede-act` (`:274`) is
retirement: drawn, never walked. The filter is on contributors, not families,
because `:304` puts transfers and retirements in one `superseded` family.

**Before / after**: walkable relations followed backwards / forwards; a
transfer is walked both ways. **Both**: reachable each way, inside a genuine
cycle. **Sibling**: shares an immediate walkable basis, on neither path. Counts
exclude the selection itself.

## 2. Change 1: one counting function, one honest line

The tab counts commitment lifecycles (`rows.ts:190-224`, printed at
`RequestList.tsx:163`, `:118-125`). The graph counts thread roots
(`outcomeMap.ts:186-204`), capped at 160 (`:205-213`), reporting the cap only
as a bullet (`OutcomeMap.tsx:227-231`). gitseq `closed`: the tab says 925, the
drawing shows 160.

One exported function in `outcomeMap.ts` returns every number the screen
prints, replacing `RequestList.tsx:196`.

```
14 counted over 13 threads, 13 drawn as 4 groups, 12 context cards, 0 not drawn
925 counted over 905 threads, 160 drawn as 94 groups, 745 threads not drawn
```

"745 threads not drawn" sets `presentation` to `table` (`RequestList.tsx:51`),
which already lists exactly the counted rows. Never count context cards in the
population number (`docs/reference/architecture.md:1508`), and never omit the
omission.

## 3. Change 2: selection is a directed reading loop

`OutcomeMap.tsx:78-82` emphasises an undirected component (`:30-46`): 8 of 8
cards on Chess `open`. Directed lineage cuts that to 12 of 25 on Chess
`closed`, but at the served fit of 0.2091 a 12px title renders at 2.51 CSS px
and a full-window pane reaches 4.56. Nothing showing the whole graph is
readable, so this is a loop, not a highlight.

**Levels.** *Current step*: the selection, the card stepped from, and the one
edge between them, full stroke with its relation text shown. *Lineage*: before,
after, both, sibling, full card contrast, reduced edge weight. *Off the path*:
existing `opacity-25` (`:192`). Direction comes from the emphasised edge, not
new colours.

**Entering.** Search already filters both presentations
(`RequestList.tsx:88-94`, `:102-106`). `Tab` enters and selects the first card
in layout order (`outcomeMap.ts:497`); `Enter` opens its thread; returning from
a thread pre-selects that thread's card when present.

**Zoom and pan.** `READABLE = 1`: the 12px title (`OutcomeMap.tsx:199`) and 10px
badges (`:202-206`) are already the smallest type the design uses.

- Selecting raises `scale` to `READABLE` when lower, then centres the card:
  `offset = (paneW/2 - (x+124)*scale, paneH/2 - (y+61)*scale)`.
- At or above `READABLE` the scale is untouched; pan only when the card is not
  wholly in the pane. Stepping never changes scale and pans by the same rule.
- `Escape` clears the selection and restores the scale and offset held before
  the first selection. `Fit to view` and `Reset` (`:95-96`) are unchanged.

Designed for the captured 1600 x 1000 viewport, pane 1022 x 510: width capped
by `max-w-5xl` (`RequestList.tsx:144`), height at the `min-h-[32rem]` floor
(`:103`). At scale 1 that shows three columns and three cards of a column.

**Stepping** keeps an ordered visited path.

- `ArrowRight` takes the first continuation not on the path, ordered by
  `compareThreads` (fold sequence, then event id, `outcomeMap.ts:179-180`).
  `ArrowLeft` mirrors it over predecessors and steps back along the path.
- `ArrowUp` and `ArrowDown` cycle the other candidates at this step, replacing
  the last path element, so alternatives stay reachable without branching the
  history.
- A card already on the path is never offered, so every walk is finite. When
  all continuations are visited, `ArrowRight` is disabled and the bar says so.

**Keeping the view.** Lifting `selectedThread`, `scale` and `offset`
(`OutcomeMap.tsx:60-64`) into `ListView` (`RequestList.tsx:15-21`) is not
sufficient: `:70` fits in a mount effect and would overwrite them. Let
`viewKey` = repository + population + query + presentation. `fit()` runs only
on a `viewKey` change or `Fit to view`; otherwise the saved scale and offset
win, including after a live-poll resize
(`notes/2026-08-30-outcome-map.md:198-200`). A vanished selection clears the
selection and path and keeps the view (`:71-74`, unpaired from the refit).

**Focus, without changing what is on top.** `Tab` visits 44 edges before a card
on Chess `closed`, because the `<svg>` (`:122-165`) precedes the cards
(`:166-210`). Reordering alone would lift the edges' 14px hit paths (`:160`)
above them. Reorder to cards then `<svg>`, and pin the stack: cards
`relative z-10`; `<svg>` `z-0` with `pointer-events: none`; hit paths keep
`pointer-events: stroke`. Cards stay on top for paint and hit-testing, empty
canvas still pans, and edges keep `tabIndex`, `role` and `aria-label` (`:141`).

Never walk a `supersede-act`; never merge before and after when a cycle
overlaps them; never synthesize "blocked" from position
(`notes/2026-08-30-outcome-map.md:263-276`).

## 4. Change 3: routing and labels

**Delete the permanent edge label** (`OutcomeMap.tsx:161`), which
`notes/2026-08-30-outcome-map.md:136-139` already forbids and which puts 13 of
44 labels inside a card on Chess `closed`. The text stays in the tooltip
(`:221`), the accessible name (`:141`) and the current step.

**Route same-column edges vertically.** `outcomeMapLayout.ts:63-70` gives every
edge one shape; a same-column edge crosses the whole 248 px column band and
enters its target from the left, so the target cannot say which of 15 cards in
its column it came from. Its excursion is 20.075 px past each side, not the 124
px of its control point (`evidence/curve.txt`). Add a same-column branch
leaving the source's bottom or top edge, running in a lane inside the 88 px
column gap (`outcomeMapLayout.ts:7`), entering the target's top or bottom edge,
so the arrowhead states direction.

**Lanes are bounded, and bounds alone are not enough.** Six lanes at an 8 px
pitch fit the gap; demand is 13 on Chess `closed` and 17 on Tailapp `closed`,
and one card's lineage still needs 13. So lane 0 is reserved for the current
step and holds one edge; lanes 1 to 5 take the rest of the emphasised
same-column edges by a sweep over their vertical intervals in `compareThreads`
order; beyond six, lanes are reused and those edges keep the lineage weight,
resolved by stepping rather than tracing. With no selection everything uses the
reused lanes: the overview shows structure and promises no followable line.

**The pane is a minor, separate gain.** Filling the scroll container
(`RequestList.tsx:143`) with `32rem` as a floor moves Chess `closed` from
0.2091 to 0.3797, a title at 4.56 px: good for the overview, not the answer to
legibility. Never fold a column with a "+N more"; the global bound exists
(`outcomeMap.ts:205-213`) and change 1 reports it.

## 5. Mockups

### 5.1 Chess `open`: stepping back from `#506` to `#505`

Today: 8 of 8 cards emphasised, 20 permanent labels, 7 same-column edges
crossing their own band (`chess-live-graph-selected.png`).

```
5 counted over 5 threads, 5 drawn as 1 group, 3 context cards, 0 not drawn

 col 0            |lane|  col 1             col 2            col 3
                  | 0 1|
 04443664 before =|=|=||=> #505         ##> #506         ==> #507
                  | v ||   awaiting merge   in progress      unclaimed
 fb0ee436 before =|===||=> CURRENT STEP     before           after
                  |   v|      ^
 37c2e25e before =|====|======+             #511 in progress   dimmed, sibling
                  |    |                    #517 in progress   dimmed, sibling

#505 rests on 04443664.   Reverse: 04443664 is a direct basis for #505.

#505 awaiting merge | 4 before, 1 after, 0 both ways
step 2 of 2 on this path, 3 predecessors here, 0 already visited
[<- back] [alt ^] [alt v] [next ->]              [Open full thread]

##  current step, alone in lane 0    ==  lineage    (plain)  dimmed
```

Column 0's three same-column edges take lanes 0 and 1 instead of crossing the
band; the current step holds lane 0 alone and prints its relation once below
the drawing. `alt ^` and `alt v` move between `#505`'s three predecessors.

### 5.2 Chess `closed`: one step inside the 9-thread cycle

Today: 25 cards, 44 edges, fit 0.2091 so a card is 52 x 26 px; 27 same-column
edges, worst overlapping set 13; clicking `1a9f848f` emphasises 20 of 25
(`chess-closed-graph-selected.png`). Below, the walk started at `1.6` and
stepped to `1.7`.

```
14 counted over 13 threads, 13 drawn as 4 groups, 12 context cards, 0 not drawn
scale 1.00, centred on the current step.  Fit to view shows all 25.

 col 0        | lanes |  col 1                     col 2      col 3
              |0 1 2 3|
 0.1 before ==|=======|=> 1.3  both     ==v
 0.2 before ==|=======|=> 1.6  both  ###|#v  <- stepped from here
 0.0 0.3 0.4  |       |   1.7  CURRENT <#|=|=v
   dimmed     |       |   1.8  both     <=|=|=+
              |       |   1.9  both     <=|=|=+  ===> 2.2 after
              |       |   1.10 both     <=+ |
              |       |   1.11 both     <===+
              |       |   1.12 both    1.13 both
              |       |   1.0 1.1 1.2 1.4 1.5 1.14 dimmed   3.0 3.1 dimmed

1.7 rests on 1.6.   Reverse: 1.6 is a direct basis for 1.7.  5 contributors.

codex: implement item 5 of the second-application plan natively
10 before, 9 after, 8 of those both ways (this card is in a 9-thread cycle)
step 2 of 2 on this path, 7 continuations here, 0 already visited
[<- back] [alt ^] [alt v] [next ->]              [Open full thread]
```

Twelve of 25 cards carry lineage weight; 13 dim. The reader never faces 13
overlapping gutter paths: one edge is the current step, alone in lane 0, at
scale 1, with its relation printed. `next ->` walks the cycle one edge at a
time, then reports it exhausted.

## 6. Validation and acceptance

Asserted on rendered output.

| check | today | new | Hugh's test |
| --- | --- | --- | --- |
| counted, threads, cards, groups, omitted all stated; omitted switches to `table` | `dom.test.mjs:814-854` | whole line; button target | account for "2 open"; reach every counted request |
| a group is a weakly connected component | none | branching case 1; split case 2 | "one group" explained |
| context outside the count; basis left of dependent | `outcome-map.test.mjs:347-377`, `:275-291`, `dom.test.mjs:715-723` | unchanged; no card moves | follow a path |
| before / after / both / sibling, selection excluded | replaces `dom.test.mjs:736-742` | fixture with a branch, a cycle, a sibling | follow a path |
| transfer walks both ways; supersede act never walks | none | `successor_request` both directions; supersede drawn, not walked | preserve durable meaning |
| branch order, alternatives, finite cycle | none | `compareThreads`-first unvisited step; `ArrowUp`/`Down` swap the last step; cycle exhausts, `ArrowRight` disabled | follow a path without untangling |
| zoom and pan | none | select at 0.2091 sets scale 1 and centres; stepping keeps scale | read the card you are on |
| `Escape`, fit policy, return from a detour | none | `Escape` restores the pre-selection view; refit only on `viewKey` change or `Fit to view`; selection, path, view survive a thread and back | move between overview and detail |
| focus and pointer order | none | `Tab` reaches a card first; a card under an edge takes the click; empty canvas pans | keyboard and pointer |
| labels not permanent; same-column lanes | `outcome-map.test.mjs:275-291` endpoints | no `<text>` unless selected or focused; vertical route; step alone in lane 0; never over 6 lanes | read a dense column |
| bounded rendering and determinism | `dom.test.mjs:758-812`, `outcome-map.test.mjs:235-246` | 107-card shape within a time and node budget; placement independent of order, sort and selection | stability at size |

**Walkthrough.** Tailapp `open` reading 2: the line states one group, `Table`
names both rows, `Tab` selects a card at scale 1, `next ->` follows the joining
relation, `Escape` returns. Chess `closed`: the line states 14 over 13 threads;
`1a9f848f` gives 10 before, 9 after, 8 both ways; `next ->` steps the cycle
until it is exhausted; `Enter` opens the thread and back restores everything.

## 7. Sequence and open choices

Three requests, three reviews, all layer 7. No projected field is added; if a
step needs one, that is a layer 5 request of its own
(`docs/reference/architecture.md:1533-1548`).

1. **Count line.** `outcomeMap.ts`, `RequestList.tsx`, `OutcomeMap.tsx`.
2. **Selection, stepping, view state, focus order.** `OutcomeMap.tsx`,
   `RequestList.tsx`. The substantive change: the walk with its branch and
   cycle rules, the zoom and pan rules, the fit policy and the stacking are
   most of the work here. It changes
   `docs/reference/architecture.md:1505-1513` and
   `notes/2026-08-30-outcome-map.md:235-236`, so both are updated in the same
   head with a candidate artifact, per AGENTS.md step 4.
3. **Routing, labels, pane.** `outcomeMapLayout.ts`, `OutcomeMap.tsx`.

Open choices, with recommendations. Keep one card per **thread**: per
commitment would draw two identically titled cards with no edge between them
(`docs/reference/architecture.md:1498`). Keep **context cards**: they are why
two open requests form one group, and change 2 dims those off the path. Give a
**transfer** its own line style and legend word instead of the `superseded` one
it shares (`outcomeMap.ts:304`); gitseq records 32, the other rooms none.
Whether the **`superseded` legend** earns its place is open: all 5883 effective
supersede acts in these rooms are intra-thread, so it has never rendered. The
stale `:7777` **bundle** is a deployment fix with its own request.
