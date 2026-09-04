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
  ui/src/lib/spine.ts. Measurements, root-cause traces, screenshots, runnable
  replay scripts with their outputs, and a Source anchors index giving the
  file:line for every behaviour named here are in
  notes/2026-09-04-task-graph-navigation/evidence/, which states the frontiers
  and separates observed screenshots from synthetic replays. Three workrooms
  were read once and pinned on 2026-09-04 between 21:24Z and 21:34Z: gitseq at
  depth 17533, Tailapp at 3396 and 3443, Chess at 511 and 519.
---

# Navigating the task graph

Three changes answer Hugh's four complaints. The evidence directory beside this
note holds the measurements, traces, screenshots and a Source anchors index
giving the file:line for every behaviour named here. This note implements
nothing; each change needs its own request.

## 1. Vocabulary

**Task**: one request and its commitments, which is what the tab counts and the
table lists. **Card**: one thread, the identity a table row opens; a task with
several commitment lifecycles is one card, so tasks and cards differ.
**Group**: a weakly connected component over the drawn relations. It may branch
and may contain cycles, so the screen says "group", never "chain".

**Walkable**: a relation carrying a `provenance` or `successor-request`
contributor. A `supersede-act` is retirement: drawn, never walked. The filter
is on contributors, not families, because the projection puts transfers and
retirements in one `superseded` family. **Before / after**: walkable relations
followed backwards / forwards, a transfer both ways, counts excluding the
selection.

## 2. Change 1: say it plainly, then account for it

The tab counts commitment lifecycles, the graph counts thread roots and caps
them at 160, and the cap appears only as a bullet below the drawing: on gitseq
`closed` the tab says 925 and the drawing shows 160.

One exported function returns every number, and the screen shows the plain
sentence first.

```
14 tasks, shown in 4 groups.                                        [details]
  12 related cards shown for context; 1 task shares a card with another;
  0 not shown.

925 tasks, shown in 94 groups. 745 tasks not shown, see the table.  [details]
  0 related cards shown for context; 19 tasks share a card with another.
```

Anything hidden is named on the first line, in tasks, with the table one click
away. The rest is honest accounting and sits behind `[details]`, a disclosure
on the same line rather than a new toolbar control: never omitted, only never
first.

Touches `outcomeMap.ts` (one function over data it already has),
`RequestList.tsx:196` and `OutcomeMap.tsx:222-231`. Never count context cards
as tasks.

## 3. Change 2: selection is a reading loop

Selection today emphasises an undirected component: 8 of 8 cards on Chess
`open`. Directed lineage cuts that to 12 of 25 on Chess `closed`, but a 12px
title renders at 2.51 CSS px there, and at 4.56 even in a full-window pane. No
view of the whole graph is readable, so the answer is a loop, not a highlight.

**Two levels and a step.** The *current step* is the selected card, the card
stepped from, and the one edge between them, at full stroke with its relation
shown. *On the path* is everything reachable before or after, at full card
contrast and reduced edge weight. Everything else keeps the existing dim.
Direction comes from the arrowheads and from the labelled `back` and `next`.

**Why this is the smallest.** Two directed walks and one bright edge are the
least that answers "what does this rest on, and what came of it". Three things
I proposed are gone. *Both* was a third card style covering 8 of the 12
emphasised cards on Chess `closed`; styling most of the emphasis separately is
decoration, so the cycle is stated in the bar. *Sibling* was a fourth style; it
becomes plain dimming plus a count in the bar, keeping the distinction the
request asks for at no visual cost. And two of the four arrow keys now do
nothing unless a branch exists. Both walks stay in the model, because the
counts need them.

**Entering.** Search already filters both presentations. `Tab` selects the
first card in layout order, `Enter` opens its thread, and returning from a
thread pre-selects that thread's card when present.

**Zoom and pan.** `READABLE = 1`: a card's 12px title and 10px badges are
already the smallest type the design uses.

- Selecting raises `scale` to `READABLE` when lower, then centres the card:
  `offset = (paneW/2 - (x+124)*scale, paneH/2 - (y+61)*scale)`.
- Above `READABLE` the scale holds, and it pans only when the card is not
  wholly in the pane. Stepping never changes scale and pans by that rule.
- `Escape` clears the selection and restores the scale and offset held before
  the first selection. `Fit to view` and `Reset` are unchanged.

Designed for the captured 1600 x 1000 viewport, pane 1022 x 510: width capped
by the `max-w-5xl` reading column, not the window; height at the
`min-h-[32rem]` floor, because the pane's children are absolutely positioned.
Scale 1 shows three columns and three cards of a column.

**Stepping** keeps an ordered visited path.

- `ArrowRight` takes the first continuation not on the path, ordered by the
  projection's own `compareThreads`, fold sequence then event id. `ArrowLeft`
  mirrors it over predecessors and steps back along the path.
- Only when the bar reports more than one candidate do `ArrowUp` and
  `ArrowDown` cycle them, replacing the last path element instead of extending
  it, so alternatives stay reachable without branching history.
- A card on the path is never offered again, so every walk is finite. When all
  continuations are visited, `ArrowRight` is disabled and the bar says so.

**Keeping the view.** Lifting selection, scale and offset into `ListView` is
not enough: a mount effect refits and would overwrite them. Let `viewKey` =
repository + population + query + presentation. `fit()` runs only on a
`viewKey` change or `Fit to view`; otherwise the saved values win, including
after a live-poll resize. A vanished selection clears the selection and path
and keeps the view.

**Focus, without changing what is on top.** `Tab` visits 44 edges before a card
on Chess `closed`, because the `<svg>` precedes them, and reordering alone
would lift the edges' 14px hit paths above the cards. Reorder to cards then
`<svg>`, and pin the stack: cards `relative z-10`, `<svg>` `z-0` with
`pointer-events: none`, hit paths keeping `pointer-events: stroke`. Cards then
win painting and hit-testing, empty canvas still pans, and edges keep their
`tabIndex`, `role` and accessible name.

Touches `OutcomeMap.tsx` and `RequestList.tsx`. Never walk a `supersede-act`,
and never synthesize "blocked" from graph position.

## 4. Change 3: routing and labels

**Delete the permanent edge label.** The existing design forbids words on every
line, and 13 of 44 land inside a card on Chess `closed`. The text stays in the
tooltip, the accessible name and the current step.

**Route same-column edges vertically.** One path shape serves every case today,
so a same-column edge crosses the whole 248 px column band and enters its
target from the left, and the target cannot say which of 15 column-mates it
came from. Its excursion is 20.075 px past each side, not the 124 px of its
control point; `evidence/curve.txt` evaluates it. Add a same-column branch that
leaves the source's bottom or top edge, runs in a lane inside the 88 px column
gap, and enters the target's top or bottom edge, so the arrowhead states
direction.

**Lanes are bounded, and bounds alone are not enough.** Six lanes at an 8 px
pitch fit the gap, but demand is 13 on Chess `closed` and 17 on Tailapp, and
one card's lineage still needs 13. So lane 0 is reserved for the current step
and holds one edge; lanes 1 to 5 take the other on-path same-column edges by a
sweep over their vertical intervals in `compareThreads` order; beyond six the
lanes are reused at the on-path weight, resolved by stepping rather than
tracing. Unselected, everything uses the reused lanes: the overview shows
structure and promises no followable line.

**The pane** filling the scroll container with `32rem` as a floor moves Chess
`closed` from 0.2091 to 0.3797: better for the overview, not the answer to
legibility.

Touches `outcomeMapLayout.ts:63-70` and `OutcomeMap.tsx`. Never fold a column
with a "+N more"; the global bound exists and change 1 reports it.

## 5. Mockups

### 5.1 Chess `open`, small: stepped back from `#506` to `#505`

Today: 8 of 8 cards emphasised, 20 permanent labels, 7 same-column edges
crossing their own band (`chess-live-graph-selected.png`).

```
5 tasks, shown in 1 group.                                        [details]

 col 0            |lane|  col 1             col 2            col 3
                  | 0 1|
 04443664       ==|=|=||=> #505         ##> #506         ==> #507
                  | v ||   awaiting merge   in progress      unclaimed
 fb0ee436       ==|===||=> CURRENT STEP
                  |   v|      ^
 37c2e25e       ==|====|======+             #511 in progress
                  |    |                    #517 in progress

#505 rests on 04443664.   Reverse: 04443664 is a direct basis for #505.

#505 awaiting merge | 4 before, 1 after | 2 siblings share its basis
step 2 of 2, 3 predecessors here
[<- back] [alt ^] [alt v] [next ->]              [Open full thread]
```

**How it reads.** Type sits at its designed size, so titles are legible without
leaning in. One line is lit, one sentence sits under the drawing, and the other
nineteen labels are gone. Column 0's same-column edges are short marks in two
lanes instead of loops across the band. `#511` and `#517` recede without
vanishing, and the bar says why they are there. Four buttons, no new toolbar.

### 5.2 Chess `closed`, dense: one step inside the 9-thread cycle

Today: 25 cards, 44 edges, fit 0.2091 so a card is 52 x 26 px; 27 same-column
edges, worst overlapping set 13; clicking `1a9f848f` emphasises 20 of 25
(`chess-closed-graph-selected.png`). Below, the walk started at `1.6` and
stepped to `1.7`.

```
14 tasks, shown in 4 groups.                                      [details]
scale 1.00, centred on the current step.  Fit to view shows all 25.

 col 0        | lanes |  col 1                     col 2      col 3
              |0 1 2 3|
 0.1        ==|=======|=> 1.3               ==v
 0.2        ==|=======|=> 1.6   ###############v  <- stepped from here
 0.0 0.3 0.4  |       |   1.7   CURRENT STEP <#|=v
              |       |   1.8               <==|=+
              |       |   1.9               <==|=+   ===> 2.2
              |       |   1.10              <==+
              |       |   1.11 1.12 1.13
              |       |   1.0 1.1 1.2 1.4 1.5 1.14              3.0 3.1

1.7 rests on 1.6.   Reverse: 1.6 is a direct basis for 1.7.  5 contributors.

codex: implement item 5 of the second-application plan natively
10 before, 9 after, 8 of them both ways: this card is in a 9-thread cycle
step 2 of 2, 7 continuations here
[<- back] [alt ^] [alt v] [next ->]              [Open full thread]
```

**How it reads.** The same 25 cards, but quiet: twelve carry on-path weight,
thirteen recede, one edge is bright. At scale 1 the current card is as readable
as a table row, and its relation is spelled out rather than inferred from a
curve. The cycle is admitted in words instead of drawn as a knot, so Hugh is
told why eight cards sit on both sides. `next ->` moves one card at a time and
eventually says there is nowhere new to go; `Escape` returns to the calm
overview at the zoom it had.

## 6. Validation and acceptance

Asserted on rendered output.

| check | today | new | Hugh's test |
| --- | --- | --- | --- |
| the plain line names tasks, groups and any hidden tasks, first | `dom.test.mjs:814-854` | primary wording; `[details]` reveals context, shared cards, hidden count; hidden count switches to `table` | account for "2 open"; reach every task |
| a group is a weakly connected component | none | branching case 1; split case 2 | "one group" is explained |
| context outside the task count; basis left of dependent | `outcome-map.test.mjs:347-377`, `:275-291`, `dom.test.mjs:715-723` | unchanged; no card moves | follow a path |
| before and after, selection excluded; siblings dim and are counted in the bar | replaces `dom.test.mjs:736-742` | fixture with a branch, a cycle and a sibling | follow a path |
| transfer walks both ways; supersede act never walks | none | `successor_request` both directions; supersede drawn, not walked | preserve durable meaning |
| branch order, alternatives, finite cycle | none | `compareThreads`-first unvisited step; up and down inert unless a branch exists, then they swap the last step; cycle exhausts and disables `ArrowRight` | follow a path without untangling |
| zoom and pan | none | selecting at 0.2091 sets scale 1 and centres; stepping keeps scale | read the card you are on |
| `Escape`, fit policy, return from a detour | none | `Escape` restores the pre-selection view; refit only on a `viewKey` change or `Fit to view`; selection, path and view survive a thread and back | move between overview and detail |
| focus and pointer order | none | `Tab` reaches a card first; a card under an edge takes the click; empty canvas pans | keyboard and pointer |
| labels not permanent; same-column lanes | `outcome-map.test.mjs:275-291` endpoints | no `<text>` unless selected or focused; vertical route; step alone in lane 0; never over 6 lanes | read a dense column |
| bounded rendering and determinism | `dom.test.mjs:758-812`, `outcome-map.test.mjs:235-246` | the 107-card shape within a time and node budget; placement independent of order, sort and selection | stability at size |

**Walkthrough.** Tailapp `open` reading 2: "2 tasks, shown in 1 group",
`Table` names both, `Tab` selects a card at scale 1, `next ->` follows the
joining relation, `Escape` returns. Chess `closed`: "14 tasks, shown in 4
groups", `[details]` explains the shared card, `1a9f848f` gives 10 before and 9
after, `next ->` steps the cycle until it is exhausted, `Enter` opens the
thread and back restores everything.

## 7. Sequence and open choices

Three requests, three reviews, all layer 7. No projected field is added.

1. **The plain line.** `outcomeMap.ts`, `RequestList.tsx`, `OutcomeMap.tsx`.
2. **Selection, stepping, view state, focus order.** `OutcomeMap.tsx`,
   `RequestList.tsx`. The substantive change: the walk with its branch and
   cycle rules, the zoom and pan rules, the fit policy and the stacking are
   most of the work here. It changes the presentation contract at
   `docs/reference/architecture.md:1505-1513` and the selection rule at
   `notes/2026-08-30-outcome-map.md:235-236`, so both are updated in the same
   head with a candidate artifact, per AGENTS.md step 4.
3. **Routing, labels, pane.** `outcomeMapLayout.ts`, `OutcomeMap.tsx`.

Five product choices stay open, each with a recommendation, in the evidence
directory's Unresolved product choices table: one card per thread rather than
per commitment, context cards rendered rather than counted only, a transfer
given its own line style, the `superseded` legend left as it is for now, and
the stale `:7777` bundle filed as a separate deployment fix.
