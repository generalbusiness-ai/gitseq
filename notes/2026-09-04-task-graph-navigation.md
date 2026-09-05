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
  file:line for every behaviour named here, the lineage-against-citations
  analysis and the accounting arithmetic are in
  notes/2026-09-04-task-graph-navigation/evidence/, which states the frontiers
  and separates observed screenshots from synthetic replays. Three workrooms
  were read once and pinned on 2026-09-04 between 21:24Z and 21:34Z: gitseq at
  depth 17533, Tailapp at 3396 and 3443, Chess at 511 and 519.
---

# Navigating the task graph

Hugh's four complaints have one root cause between them, and fixing it makes
the rest small. The evidence directory beside this note holds every
measurement, the source anchors, and the two rendered mockups. This note
implements nothing; each change needs its own request.

## 1. The defect under all four complaints

The board groups every recorded `rests on` citation between two threads into
one line and presents the result as lineage. Most are not lineage: at Chess
`closed`, 23 of the 44 drawn relations carry only citations, such as a later
assertion citing an old request or a report citing the artifacts it covers.
Each is real and acyclic. Grouping them manufactures the 9-thread cycle, the 27
same-column edges, and the click that lights 20 of 25 cards.

**Lineage** is a relation carrying either a `provenance` contributor whose
dependent statement is a `request` and whose basis statement is a `request`, a
`promise` or an `artifact`, or a `successor-request` contributor. That is
exactly: a request depending on another request or on its promise, a request
depending on an artifact, and the fold's own transfer. Each proves dependence
and no more: a request on an artifact may be a review, an implementation or a
prerequisite, and a request on a request does not say the earlier one
authorised the later. Name a kind only where the record does. Everything else
is a **citation**, listed on the selected card, never drawn as lineage and
never walked. A `supersede-act` is a **retirement**: also detail-only, listed
under that name, never drawn and never walked. A card with no incoming lineage
relation reads "no recorded parent", still distinct from "basis outside view".

Under that rule, across every population in three rooms, cycles go to **zero**;
and since longest-path layering of an acyclic graph puts every edge in a later
column, same-column edges go to **zero** too. Chess `closed` card `#41` goes
from 10 before, 9 after, 8 both ways to **1 before, 6 after, 0 both**, on 8 of
25 cards. Mean cards on path fall from 16.4 to 3.7 of 25 on Chess `closed`, and
from 31.7 to 2.9 of 107 on Tailapp `closed`.

## 2. Change 1: one unit, said plainly

A **task** is a request, which is a thread root, which is what a card draws.
Commitment rows are the secondary unit. Today the tab counts rows, the graph
counts roots, and nothing reconciles them.

```
13 tasks, all shown in 8 groups.                                    [details]
  14 commitment rows, 1 task with more than one; 12 related cards for context.

905 tasks, 160 shown in 124 groups, 745 not shown, see the table.   [details]
  925 commitment rows, 19 tasks with more than one; 758 rows not shown.
```

The tab and the headline count tasks, so one unit is user-facing everywhere.
Hidden work is named on the first line, in tasks, with the table one click
away. Shown and hidden are derived from actual row-to-drawn-root membership,
never by subtracting counts of different things: at gitseq `closed`, 925 rows
minus the 167 sitting on the 160 drawn roots leaves 758 hidden rows, while 905
tasks minus 160 leaves 745 hidden tasks, and the two differ because 19 tasks
carry more than one row.

Touches `outcomeMap.ts` (one exported function), `rows.ts` (a task count beside
the row count), `RequestList.tsx:196` and `OutcomeMap.tsx:222-231`.

## 3. Change 2: draw lineage, keep citations on demand

Apply the rule in section 1. Lineage relations are drawn and laid out;
citations and retirements are neither drawn nor laid out, and appear in the
selected card's detail, each under its own name. Columns come from the lineage
graph alone.

This is a smaller model, not a bigger one: it filters an existing contributor
list by two projected fields the browser already reads, and it removes the
strongly-connected-component handling, the same-column ordering pass and the
cycle warnings that existed only to cope with manufactured cycles.

Touches `outcomeMap.ts` and `OutcomeMap.tsx`. Never present a citation as
lineage, never draw or walk a retirement, never name a relation the record does
not carry.

## 4. Change 3: selection, stepping and the view

With cycles and same-column edges gone, the interaction that was needed to
survive them is gone too. What remains.

**Three weights, one policy.** *Current step*: the selected card, the card
arrived from, and the one edge between them, at full stroke with its relation
shown. *On path*: everything before or after, full card contrast, reduced edge
weight. *Everything else dims*, siblings included; the bar counts them. That is
the only sibling policy: dim, and named in the bar.

**Neighbour chooser and back history.** The bar names the neighbours of the
**current** card in the pending direction, each by its task title and the
relation the record carries — "file the item-5 repair closure (depends on this
request)" — with exact event ids and contributors in the card's detail.
`ArrowUp` and `ArrowDown` change which neighbour is offered, without moving.
`ArrowRight` commits to it and pushes the card left behind onto a back history.
`ArrowLeft` pops that history, or, when it is empty, moves to the offered
predecessor. Order among neighbours is the projection's own `compareThreads`,
fold sequence then event id, so the
offer is deterministic. There is no visited-path rule and no cycle
termination, because the lineage graph is acyclic: a walk ends when a card has
no continuation, and the bar says so.

**Zoom and pan.** `READABLE = 1`, because a card's 12px title and 10px badges
are already the smallest type the design uses. Selecting raises `scale` to
`READABLE` when lower and centres the card at
`offset = (paneW/2 - (x+124)*scale, paneH/2 - (y+61)*scale)`; at or above
`READABLE` the scale holds and it pans only when the card is not wholly in the
pane. Stepping never changes scale. `Escape` restores the scale and offset held
before the first selection; `Fit to view` and `Reset` are unchanged. Designed
for the captured 1600 x 1000 viewport, pane 1022 x 510: width capped by the
`max-w-5xl` reading column, height at the `min-h-[32rem]` floor because the
pane's children are absolutely positioned. Scale 1 shows three columns and
three cards of a column, more than the 3.7 an average selection puts on path.

**Keeping the view.** Lifting selection, scale and offset into `ListView` is
not enough: a mount effect refits and would overwrite them. Let `viewKey` =
repository + population + query + presentation. `fit()` runs only on a
`viewKey` change or `Fit to view`; otherwise the saved values win, including
after a live-poll resize. A vanished selection clears the selection and history
and keeps the view.

**Focus and stacking.** `Tab` visits 44 edges before a card on Chess `closed`,
because the `<svg>` precedes the cards, and reordering alone would lift the
edges' 14px hit paths above them. Reorder to cards then `<svg>` and pin the
stack: cards `relative z-10`, `<svg>` `z-0` with `pointer-events: none`, hit
paths keeping `pointer-events: stroke`. Cards then win painting and
hit-testing, empty canvas still pans, and edges keep their `tabIndex`, `role`
and accessible name.

**Delete the permanent edge label.** The existing design already forbids words
on every line, and 13 of 44 land inside a card on Chess `closed`. The text
stays in the tooltip, the accessible name and the current step.

Touches `OutcomeMap.tsx` and `RequestList.tsx`.

## 5. What the smaller model removed

Gone: gutter-lane routing and its lane budget, the same-column edge path, the
drawn retirement edge, the visited-path state machine and its cycle-termination
rule, and the "both ways" and sibling emphasis levels. Kept: two directed
walks, because before and after are separate answers; sibling counts, because
the request asks siblings be distinguished and one bar clause does it; and the
zoom, pan, saved-view, focus, stacking and transfer rules, which the
restriction does not touch.

## 6. Mockups

Both rendered at real size: cards 248 x 122, 88 px gaps, 12px titles, pane
1022 x 510, scale 1.00, centred on the selection.

- `after-chess-live.svg`, small. Selection `025a6e01`, arrived from `6f7dea46`
  by `>`. 3 before, 1 after, 2 siblings, 5 of 8 on path.
- `after-chess-closed.svg`, dense. Selection `1a9f848f` (#41), arrived from
  `221f7932`. 1 before, 6 after, 8 of 25 on path, `next` naming 1 of 5
  destinations, and 23 citations and retirements one key away.

In both, the lit edge, the sentence below the drawing, the two endpoints and
the buttons describe one action. The before screenshots sit beside them,
`chess-live-graph-selected.png` and `chess-closed-graph-selected.png`, where
the same click lights 8 of 8 and 20 of 25 at a scale rendering a 12px title at
2.51 px.

**How they read.** Titles are legible without leaning in. One line is bright
and one sentence says what it means, instead of 20 or 44 labels competing with
the cards. The line under it names where `>` goes. Off-path work recedes
rather than vanishing. Nothing new sits in the
toolbar: the interaction is four buttons on the line that already existed.

## 7. Validation and acceptance

| check | today | new | Hugh's test |
| --- | --- | --- | --- |
| one unit; hidden tasks named first; details reveal rows | `dom.test.mjs:814-854` | tasks in tab, headline and line; hidden derived from drawn-root membership; details show rows and multi-row tasks; hidden count opens the table | account for "2 open"; reach every task |
| lineage rule: dependent is a request on request/promise/artifact, plus transfers | none | each admitted pair drawn; assert, report and artifact citations not drawn; a retirement neither drawn nor walked; no relation labelled with a kind the record does not carry | lineage means lineage |
| citations and retirements reachable, each under its own name | none | selected card lists its citations and, separately, its retirements, with exact event ids | show omitted content honestly |
| no recorded parent stays distinct from basis outside view | `outcome-map.test.mjs:334-346` | both states asserted on one fixture | preserve ambiguity |
| lineage graph is acyclic; no same-column lineage edge | `outcome-map.test.mjs:321-333` warns on cycles | assert zero cycles and zero same-column edges on the Chess `closed` shape | read a dense column |
| before and after, selection excluded; siblings dim and counted | replaces `dom.test.mjs:736-742` | fixture with a branch and a sibling; one dim policy | follow a path |
| neighbour chooser and back history | none | up and down change the offer without moving; `>` commits and pushes; `<` pops, then falls back to the offered predecessor; order is `compareThreads`; the offer names the destination task and its recorded relation in words, ids in detail | follow a path without untangling |
| zoom, pan, `Escape`, fit policy, return from a detour | none | selecting at 0.2091 sets scale 1 and centres; stepping keeps scale; `Escape` restores; refit only on `viewKey` change or `Fit to view`; selection, history and view survive a thread and back | read the card you are on; move between overview and detail |
| focus and pointer order | none | `Tab` reaches a card first; a card under an edge takes the click; empty canvas pans | keyboard and pointer |
| labels not permanent | none | no `<text>` unless selected or focused; accessible name unchanged | read a dense column |
| bounded rendering and determinism | `dom.test.mjs:758-812`, `outcome-map.test.mjs:235-246` | the 107-card shape within a time and node budget; placement independent of order, sort and selection | stability at size |

**Walkthrough.** Tailapp `open` reading 2: the line says how many groups the
recorded relations actually give, and `Table` names both tasks. `Tab` selects a
card at scale 1, `>` follows one lineage edge and prints it, `Escape` returns.
Chess `closed`: "13 tasks, all shown in 8 groups", `[details]` explains the one
task with two rows, and selecting `#41` lights 8 of 25 with 1 before and 6
after; `^`/`v` pick among five continuations, `>` takes one, `<` comes back.

## 8. Sequence and open choices

Three requests, three reviews, all layer 7. No projected field is added.

1. **One unit.** `outcomeMap.ts`, `rows.ts`, `RequestList.tsx`, `OutcomeMap.tsx`.
2. **The lineage rule.** `outcomeMap.ts`, `OutcomeMap.tsx`. It changes the
   presentation contract at `docs/reference/architecture.md:1505-1513`, updated
   in the same head with a candidate artifact.
3. **Selection and the view.** `OutcomeMap.tsx`, `RequestList.tsx`. It changes
   the selection rule at `notes/2026-08-30-outcome-map.md:235-236`, the same way.

Open choices sit in the evidence directory's tables, with a recommendation
each. The live one is whether a `report` counts as a lineage basis: excluding
it is codex's enumeration and the plan's recommendation, including it adds 4
edges and merges one group on Chess `closed` and would capture "repair what the
review found". Both stay acyclic and same-column free.
