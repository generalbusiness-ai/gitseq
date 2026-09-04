# Retained evidence for the task-graph navigation note

Everything the note beside this directory measures can be re-derived here. The
scripts import `ui/src/lib/*.ts` directly, so they compute what the browser
computes rather than an approximation of it.

## Frontiers

The saved `GET /v0/status` responses are 1.0 MB to 36 MB, so they are not
committed. The text outputs below are what they produced, at these frontiers.

| room | genesis | head | depth | when |
| --- | --- | --- | --- | --- |
| gitseq | `5d2622748872b7e2dec3fe5c59e4be73a35e0bc8` | `d93004541da179859f1a67112b179761d83a4372` | 17533 | snapshot B, 2026-09-04T21:32:52Z |
| Tailapp | `da732b0bdaad4426ed4ad666b892d8a7c68f625f` | `54890ee31de9f06800b89848ab8ea30322cd4d7f` | 3396 | snapshot A, 2026-09-04T21:24Z |
| Tailapp | `da732b0bdaad4426ed4ad666b892d8a7c68f625f` | `4140a2c0d1f1a306ee05e599a3132cd8b05d0acb` | 3443 | snapshot B, 2026-09-04T21:32:52Z |
| Chess | `1b96687964cbba5a6089c526d7136b32ba9488a5` | `00ed78b3475f66c9c893a009d84852e1fdf7dfd1` | 511 | snapshot A, 2026-09-04T21:24Z |
| Chess | `1b96687964cbba5a6089c526d7136b32ba9488a5` | `975a1528a751f617e4e87ba4d006ee22c7b7f3aa` | 519 | snapshot B, 2026-09-04T21:32:52Z |

## How to run

```sh
# From a checkout with ui/node_modules present:
node --experimental-strip-types replay.mjs /path/to/status.json "chess, snapshot B"
node --experimental-strip-types twoopen.mjs /path/to/status.json "tailapp, snapshot A"
node --experimental-strip-types curve.mjs

# From a worktree without ui/node_modules, point GS_REPO at one that has it:
GS_REPO=/path/to/gitseq node --experimental-strip-types curve.mjs
```

`PROBE=<event-id-suffix>[,<event-id-suffix>]` makes `replay.mjs` report the
selection classification for named cards. The committed outputs used the cards
that were actually clicked in the browser.

## What each file pins

| file | pins |
| --- | --- |
| `replay.mjs` | tab counts, rows against threads against cards, groups, omissions, edge shapes, gutter-lane demand, column occupancy, fit scale and the rendered size of a 12px title, permanent-label collisions, and undirected against directed selection emphasis |
| `twoopen.mjs` | for every two-row subset of a room's open population, how many separate groups the drawing shows |
| `curve.mjs` | the evaluated horizontal extent of the same-column edge path, as against its control points |
| `replay-chess.txt` | Chess, snapshot B. 14 rows over 13 threads in `closed`; 27 of 44 edges same-column; 13 gutter lanes; fit 0.2091, a 12px title at 2.51px; card `1a9f848f` before 10, after 9, both 8 |
| `replay-tailapp.txt` | Tailapp, snapshot B. 79 rows over 77 threads in `closed`; 107 cards, 20 groups; 59 context cards and 222 relation groups omitted; fit 0.0720, a 12px title at 0.86px; card `f7f10b16` before 8, after 0 |
| `replay-gitseq.txt` | gitseq, snapshot B. 925 rows over 905 threads in `closed`, 160 cards drawn, **745 threads not drawn**; 19 cards hold more than one lifecycle |
| `twoopen-tailapp.txt` | all six synthetic two-row subsets of Tailapp's four open rows render as one group |
| `twoopen-chess.txt` | all ten synthetic two-row subsets of Chess's five open rows render as one group |
| `twoopen-gitseq.txt` | the counter-example: 4 of gitseq's 10 subsets stay as two groups, so this is a property of how tightly a room cites itself, not a universal one |
| `curve.txt` | the same-column curve reaches 20.075px past each side of the column, not the 124px of its control point |

## Synthetic subsets are not screenshots

`twoopen-*.txt` is a **synthetic** reading. It asks what the graph would draw
if the population held exactly those two rows. It is not a capture of a board
that said "2 open". At the moment the screenshots were taken, Tailapp's open
population was **zero**, and `notes/2026-09-04-task-graph-navigation/tailapp-live-table.png`
shows that empty board: `RequestList.tsx:200-203` short-circuits before either
presentation, so no graph is drawn at all. Hugh's remembered "2 open" is a
point on the same moving curve, and the mechanism is what the synthetic subsets
measure.

## What is not here

No resident was started, stopped or written to. No workroom event was filed.
The screenshots in the parent directory were taken with a headless browser
against the already-running residents on `:7777`, `:7778` and `:7779`; the
`JoinGate` overlay was removed client-side so the board could be read without
choosing an actor identity.

---

# Measurements and root causes

This is the detailed part of `notes/2026-09-04-task-graph-navigation.md`. The
plan states the changes; this states what was measured and why each symptom
happens. Line numbers are at gitseq main `be3b069cc8e013e9ebff6b9e0a70ef5e10c2cf5b`,
which is byte-identical to the bundle the Tailapp (`:7778`) and Chess
(`:7779`) residents serve. The gitseq resident (`:7777`) serves an older
bundle; see the last section.

## 1. Observed against synthetic

Two kinds of evidence are used and they are not interchangeable.

**Observed.** Five screenshots in the parent directory, taken with a headless
browser at viewport 1600 x 1000 CSS px, device scale 2, search box empty, table
sort at the default priority order, `Fit to view` as the graph applies it on
open. The graph pane measured 1022 x 510.

| screenshot | what it observes |
| --- | --- |
| `tailapp-live-table.png` | Tailapp `open` at snapshot B: **zero** open rows, "Nothing is waiting", no graph rendered |
| `chess-live-graph-selected.png` | Chess `open`, card `025a6e01` selected: 8 of 8 cards emphasised, 0 dimmed, overlapping `rests on` / `ratified by` labels in the left column |
| `chess-closed-graph.png` | Chess `closed` at fit 0.2091, 25 cards, 44 edges |
| `chess-closed-graph-selected.png` | Chess `closed`, card `1a9f848f` selected: 20 of 25 emphasised, 5 dimmed |
| `tailapp-closed-graph.png` | Tailapp `closed` at fit 0.0720, 107 cards, "Graph bounded: 59 context cards and 222 relation groups omitted" |

**Synthetic.** `twoopen-*.txt` asks what the graph *would* draw for exactly two
rows of a real open population. No board reading "2 open" was ever
photographed. At capture time Tailapp's open population was zero and
`RequestList.tsx:200-203` short-circuits on `rows.length === 0` before either
presentation, so no graph existed to photograph. Hugh's remembered "2 open" is
a point on a curve that moved from 4 to 0 in eight minutes; the synthetic
subsets measure the mechanism independently of where the count is at any
instant.

## 2. Tailapp: "N open" but one group

All six synthetic two-row subsets of Tailapp's four open rows render as one
group. The tightest is `#3393 + #3396`: two focal plus two context cards, six
edges.

- `#3393` = `git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:1efc21198ff0976b794223d0b840bfa64c8cd726`, `reported`
- `#3396` = `git:sha1:da732b0bdaad4426ed4ad666b892d8a7c68f625f#git:sha1:54890ee31de9f06800b89848ab8ea30322cd4d7f`, `unclaimed`

`#3396` records `rests_on` naming
`git:sha1:da732b0b...#git:sha1:f73a504a0610774f62c38f36af2e7c02c603fb0a`, the
report inside `#3393`'s thread. The connection is real, direct and one hop.
`twoopen-gitseq.txt` is the counter-example: 4 of gitseq's 10 subsets stay as
two groups, so this is a property of how tightly a room cites itself, not a
universal one.

## 3. The count mismatch

| room, population | rows | threads | focal cards | threads omitted | cards holding >1 lifecycle | groups |
| --- | --- | --- | --- | --- | --- | --- |
| Chess `closed` | 14 | 13 | 13 | 0 | 1 | 4 |
| Tailapp `closed` | 79 | 77 | 77 | 0 | 1 | 20 |
| gitseq `closed` | 925 | 905 | 160 | **745** | 19 | 94 |
| gitseq `completed` | 1419 | 1414 | 160 | **1254** | 5 | 37 |

**Trace.** `RequestList.tsx:80` calls `workRows`; `rows.ts:190-224` emits one
row per commitment, keyed at `:210` by `promise ?? report ?? request`, and
`RequestList.tsx:163` and `:118-125` print that count. `RequestList.tsx:102-106`
hands the same rows to `buildOutcomeMap`; `outcomeMap.ts:186-204` maps each to
`rootOf(row.event)`, keeps one entry per root, and lets the latest lifecycle
supply the card's state (`:192-200`). `outcomeMap.ts:205-213` caps the focal set
at 160 and warns, but `:512-517` reports `focalNodes` as the already-capped
size, so the prominent bounded line at `OutcomeMap.tsx:222-226` never mentions
it; the cap appears only as a bullet at `OutcomeMap.tsx:227-231`.

The row that shares a card on Chess `closed` is
`git:sha1:1b96687964cbba5a6089c526d7136b32ba9488a5#git:sha1:a75107775805addb8e6ca077f69f42692c2d1ae5`
(ticket #63), one lifecycle `withdrawn` and one `cancelled`, drawn as one card
showing one of the two words.

**The joining is genuine.** `outcomeMap.ts:314-323` adds threads one hop from a
focal thread; `:448-453` keeps only those carrying a complete incident
relation. Thread collapse is not a cause: `threads.ts:71-84` already refuses
containment across a commitment boundary, and rows collapsed into another row's
thread were zero in every population dumped. Today the complete list exists
only in the table, and nothing on the graph screen points to it.

**Classification.** Data defect for the mismatch; correct but unexplained
aggregation for the single group.

## 4. Why lineage is unreadable at size

**Layering is correct.** `outcomeMap.ts:578-615` condenses on non-superseded
relations (`:586-587`), assigns a longest-path depth (`:602-612`), and gives
every member of a strongly connected component one column (`:614-615`). Zero
edges are drawn right to left in any of the five graphs measured.

**The cycles are real.** A review artifact in thread B rests on the request in
thread A, and the next request in A rests on that artifact, so two forward
event edges become a two-thread cycle. Chess `closed` has one of 9 threads;
Tailapp `closed` has eleven, two of them 8 threads.

**The intra-column ordering pass is dead code in practice.**
`outcomeMap.ts:623-625` builds it from `family === "superseded"` edges only,
and **all 5883 effective supersede acts across the three rooms are
intra-thread** (137 Chess, 1167 Tailapp, 4579 gitseq; cross-thread 0 in each),
so `:231` and `:269` drop them before they can become relations. Not one
`superseded` edge was drawn in any graph measured. The 25 same-column
`rests-on` edges inside Chess column 1 are therefore ignored for ordering, and
those 15 cards are placed by the previous column's incoming rank and stable
event order (`:651-671`).

**Geometry compounds it.** `outcomeMapLayout.ts:21-47` stacks each column at a
fixed pitch with nothing aligning a dependent to its basis. The pane is exactly
the `min-h-[32rem]` floor (`OutcomeMap.tsx:103`) because its children are
absolutely positioned, so its natural height is zero; its width is capped by
`max-w-5xl` (`RequestList.tsx:144`), not by the window. `fitOutcomeScale`
(`outcomeMapLayout.ts:54-58`) then scales tall canvases by height.

| room, population | canvas | fit in 1022x510 | card | 12px title renders at | fit in 1022x900 | title at |
| --- | --- | --- | --- | --- | --- | --- |
| Chess `open` | 1320x486 | 0.7500 | 186x92 | 9.00px | 0.7500 | 9.00px |
| Chess `closed` | 1320x2286 | 0.2091 | 52x26 | 2.51px | 0.3797 | 4.56px |
| Chess `completed` | 1992x3486 | 0.1371 | 34x17 | 1.65px | 0.2490 | 2.99px |
| Tailapp `closed` | 1992x6636 | 0.0720 | 18x9 | 0.86px | 0.1308 | 1.57px |
| Tailapp `completed` | 1992x14886 | 0.0321 | 8x4 | 0.39px | 0.0582 | 0.70px |

**Nothing that keeps the whole graph on screen can be read.** Even a
full-window pane leaves the Chess `closed` title at 4.56 CSS px. This is why
the plan's answer is a zoom rule and a step, not a bigger pane.

**Permanent labels.** `OutcomeMap.tsx:161` draws a `<text>` on every edge,
placed at `:134-135`. Measured: Chess `open` 20 labels, 7 overlapping pairs, 4
inside a card; Chess `closed` 44, 24, 13; Tailapp `closed` 159, 66, 39.
`notes/2026-08-30-outcome-map.md:136-139` already forbids permanent labels.

**Classification.** Rendering defect; the topology is correct.

## 5. Why selection reveals nothing

`OutcomeMap.tsx:78-82` emphasises `connectedComponent`, which `:30-46` walks
undirected; `:145`, `:180` and `:192` dim only what falls outside.
`notes/2026-08-30-outcome-map.md:235-236` specifies exactly that, so this is a
design defect rather than an implementation one.

Counts below exclude the selected card itself.

| room, population | cards | undirected mean | directed mean | clicked card, undirected | before | after | both | siblings | emphasised |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Chess `open` | 8 | 8.0 (100%) | 6.8 (84%) | 8 of 8 | 4 | 1 | 0 | 2 | 6 of 8 |
| Chess `closed` | 25 | 16.4 (65%) | 8.5 (34%) | 20 of 25 | 10 | 9 | 8 | 0 | 12 of 25 |
| Tailapp `closed` | 107 | 31.7 (30%) | 11.0 (10%) | 24 of 107 | 8 | 0 | 0 | 3 | 9 of 107 |

Chess `closed` card `1a9f848f` sits in the 9-thread cycle, so 8 cards are
reachable in both directions. That overlap is a true fact about the log.

**Walk policy.** A relation is walkable when it carries a `provenance`
(`outcomeMap.ts:243`) or a `successor-request` (`:303`) contributor. A
`supersede-act` contributor (`:274`) is retirement and is never walked. The
filter must be at contributor granularity, not family granularity, because
`:304` puts transfers and retirements in the same `superseded` family and one
relation group can carry both kinds. Chess and Tailapp record no
`successor_request`; gitseq records 32.

**Other gaps.** The only lineage signals on a card are the three tags at
`OutcomeMap.tsx:169-173`; in Chess `open` all three context cards carry `Basis
outside view` and none carries `Root of view`, so the leftmost column says its
upstream is off screen and offers no way to reach it. There is no `Escape` and
no arrow navigation. The only `tabIndex` is the edge group at `:139`, rendered
before the cards (`:122-165` against `:166-210`), so `Tab` visits 44 edges
before the first card on Chess `closed`. `RequestList.tsx:11-21` lifts sort,
query, population and presentation into `ListView` but not selection, zoom or
pan; `App.tsx:262-288` unmounts the list on a detour, so all three are lost.
`OutcomeMap.tsx:70` also runs `fit()` in a mount effect, so lifting the state
alone would not preserve it. `notes/2026-08-30-outcome-map.md:238-243` and
`:198-200` asked against both.

**Classification.** Correct per the design note, so a design defect.

## 6. The same-column edge, evaluated

`outcomeMapLayout.ts:63-70` gives every edge one shape. For a same-column edge
the control points are at +372 and -124 relative to the column's left edge, but
`curve.txt` evaluates the cubic and the actual range is **-20.075 to 268.075**.
The curve reaches 20.075 px past each side, not 124. The control-point offset
is not the extent, and any claim of a 124 px excursion is wrong.

The real cost is not the excursion. It is that the curve crosses the full 248
px column band once and enters its target from the left, so a target cannot
tell you which of the 15 cards in its column the edge came from; and that every
same-column edge in a column occupies that one band.

**Gutter lane demand**, the number of same-column edges in one column that
overlap vertically and would therefore need separate lanes:

| room, population | same-column edges | longest span | worst overlapping set | lanes that fit in the 88px gap at 8px pitch |
| --- | --- | --- | --- | --- |
| Chess `open` | 7 of 20 | 300px | 5 | 6 |
| Chess `closed` | 27 of 44 | 1800px | 13 | 6 |
| Chess `completed` | 117 of 160 | 3000px | 26 | 6 |
| Tailapp `closed` | 71 of 159 | 2700px | 17 | 6 |
| Tailapp `completed` | 109 of 159 | 3600px | 40 | 6 |

Emphasising one card's whole lineage does not rescue this: the worst single
selection still pulls in 13 concurrent same-column lanes on Chess `closed` and
12 on Tailapp `closed`. **Moving overlapping curves into a narrow gutter does
not make them followable.** Only one edge at a time can be, which is why the
plan reserves lane 0 for the current step and resolves the rest by stepping.

**Classification.** Correct data, defective rendering.

## 7. Alternatives measured and rejected

**Re-layering columns by lineage depth.** The current layering already
guarantees a basis is never drawn right of its dependent, which
`docs/reference/architecture.md:1509` requires; a depth walk over a graph with
genuine cycles does not. It also does not pay: re-ordering Chess `closed`
column 1 by its own intra-column structural edges moves the same-column edges
pointing downward from 17 of 27 to 17 of 27, and by fold sequence to 18 of 27.
Inside a 9-thread cycle no ordering makes every edge point one way.

**Fit-to-selection.** Fitting to the emphasised cards' bounding box raises
Tailapp `closed` from 0.0720 to 0.1656 and Chess `closed` only from 0.2091 to
0.2407, because the emphasised cards keep their vertical spread. Both remain
unreadable.

**A bigger pane.** Letting the pane fill the scroll container helps the
overview and nothing else; see the fit table in section 4.

## 8. Separate observation: the gitseq resident serves a stale bundle

`:7777` (pid 3619, started 03:52 local, `./bin/gs serve`) serves
`index-Gw2yBtUp.js`, a build from before the outcome map landed, with no
`Table | Graph` control at all. `bin/gs` and `~/.local/bin/gs` on disk both
report `vcs.revision=eb519a16604207f453c1e6148dad235af831603d`, built
2026-09-04T11:42:22Z, and `eb519a166:internal/service/uidist/index.html` embeds
`index-BoOBW1Hh.js`, which is what `:7778` and `:7779` serve. The process is
holding the assets it started with.

Every source citation here is valid for what Hugh sees on Tailapp and Chess,
and none of it is valid for the gitseq room, which has no graph. Anyone reading
the gitseq board is looking at an older UI without knowing it.

Recommended and not done: file a separate request to restart that resident on
the current binary, and consider whether the served page should state the build
it started from, so a long-lived process cannot silently diverge from the
binary on disk. Nothing here restarts anything.

---

# Source anchors

Every behaviour the plan describes, at gitseq main
`be3b069cc8e013e9ebff6b9e0a70ef5e10c2cf5b`. The plan keeps only the anchors
that name what a change touches; the rest are here.

## Counting and population

| behaviour | anchor |
| --- | --- |
| one row per commitment; key is `promise ?? report ?? request` | `ui/src/lib/rows.ts:190-224`, `:210` |
| tab count and headline print that row count | `ui/src/components/RequestList.tsx:163`, `:118-125` |
| search filters the population, then both presentations | `RequestList.tsx:88-94`, `:102-106` |
| the graph maps rows to thread roots, latest lifecycle wins | `ui/src/lib/outcomeMap.ts:186-204`, `:190`, `:192-200` |
| focal threads capped at 160, warned but not headlined | `outcomeMap.ts:205-213`; `:512-517`; `OutcomeMap.tsx:222-226`, `:227-231` |
| the graph subtitle that says nothing useful today | `RequestList.tsx:196` |
| switching presentation to `table` | `RequestList.tsx:51` |
| empty population short-circuits before either presentation | `RequestList.tsx:200-203` |
| context cards stay outside the population count | `docs/reference/architecture.md:1508` |

## Relations and the walk

| behaviour | anchor |
| --- | --- |
| `provenance` contributor, the ordinary basis edge | `outcomeMap.ts:243` |
| `successor-request` contributor, a projected transfer | `outcomeMap.ts:303` |
| `supersede-act` contributor, a retirement | `outcomeMap.ts:274` |
| transfers and retirements share the `superseded` family | `outcomeMap.ts:304`; `OutcomeMap.tsx:9`, `:15`, `:91` |
| intra-thread relations are dropped before becoming edges | `outcomeMap.ts:231`, `:269` |
| one-hop context, kept only with a complete relation | `outcomeMap.ts:314-323`, `:448-453` |
| stable order used for every tie-break | `outcomeMap.ts:179-180` (`compareThreads`) |
| thread identity refuses containment across a commitment | `ui/src/lib/threads.ts:71-84` |

## Layout and geometry

| behaviour | anchor |
| --- | --- |
| condensation, longest-path depth, one column per component | `outcomeMap.ts:578-615`, `:586-587`, `:602-612`, `:614-615` |
| intra-column ordering built from `superseded` edges only | `outcomeMap.ts:623-625`, `:651-671` |
| card geometry; 88px column gap; 32px margin | `ui/src/lib/outcomeMapLayout.ts:7`, `:21-47` |
| fit scales by the tighter of width and height | `outcomeMapLayout.ts:54-58` |
| one edge path for every case; the same-column curve | `outcomeMapLayout.ts:60-62`, `:63-70` |
| graph pane, fixed at the `min-h-[32rem]` floor | `OutcomeMap.tsx:103` |
| pane width capped by the reading column | `RequestList.tsx:144` (`max-w-5xl`) |
| the scroll container the pane could fill | `RequestList.tsx:143` |
| node order used for layout and DOM order | `outcomeMap.ts:497` |

## Selection, view state and focus

| behaviour | anchor |
| --- | --- |
| emphasis from an undirected connected component | `OutcomeMap.tsx:78-82`, `:30-46` |
| dimming of cards and edges outside it | `OutcomeMap.tsx:145`, `:180`, `:192` |
| card type sizes: 12px title, 10px badges | `OutcomeMap.tsx:199`, `:202-206` |
| lineage tags on a card | `OutcomeMap.tsx:169-173` |
| selection bar and `Open full thread` | `OutcomeMap.tsx:213-220` |
| zoom, fit and reset controls | `OutcomeMap.tsx:93-96` |
| selection, scale and offset held as local state | `OutcomeMap.tsx:60-64` |
| fit forced in a mount effect | `OutcomeMap.tsx:70`, deps at `:69` |
| a vanished selection is cleared | `OutcomeMap.tsx:71-74` |
| view state that already survives a detour | `RequestList.tsx:11-21`, `:15-21` |
| the list unmounts when a thread opens | `ui/src/App.tsx:262-288` |
| `<svg>` before the cards in DOM order | `OutcomeMap.tsx:122-165` against `:166-210` |
| edge focusability, hit path, accessible name | `OutcomeMap.tsx:139`, `:160`, `:141` |
| permanent edge label and its placement | `OutcomeMap.tsx:161`, `:134-135` |
| relation tooltip | `OutcomeMap.tsx:221` |

## Contracts a change must update

| statement | anchor |
| --- | --- |
| one card is one thread | `docs/reference/architecture.md:1498`; `notes/2026-08-30-outcome-map.md:55-61` |
| the graph's documented presentation contract | `docs/reference/architecture.md:1505-1513` |
| basis drawn left of dependent | `docs/reference/architecture.md:1509` |
| what layer 7 may derive | `docs/reference/architecture.md:1533-1548` |
| selection emphasises a connected component | `notes/2026-08-30-outcome-map.md:235-236` |
| back restores population, search, selection, zoom, pan | `notes/2026-08-30-outcome-map.md:238-243` |
| fit must not override a zoom the user made | `notes/2026-08-30-outcome-map.md:198-200` |
| no permanent words on every line | `notes/2026-08-30-outcome-map.md:136-139` |
| the map must not synthesize blocking | `notes/2026-08-30-outcome-map.md:263-276` |

## Tests that already cover part of this

| test | covers |
| --- | --- |
| `ui/test/dom.test.mjs:697-756` | graph uses the table population and search; component selection; opens the thread |
| `ui/test/dom.test.mjs:758-812` | fit contains both maximum legal shapes in a real viewport |
| `ui/test/dom.test.mjs:814-854` | two lifecycles collapse to one card; no leak across populations |
| `ui/test/dom.test.mjs:715-723`, `:736-742` | context card membership; selection and open |
| `ui/test/outcome-map.test.mjs:235-246` | placement is independent of serialization order |
| `ui/test/outcome-map.test.mjs:275-291` | multiple roots leftmost; every arrow on a card boundary |
| `ui/test/outcome-map.test.mjs:347-377` | bounded context and complete edges, atomically |

---

# Unresolved product choices

Each with the recommendation the plan carries.

| choice | recommendation | why |
| --- | --- | --- |
| one card per commitment, or per thread | **per thread** | per commitment would draw two identically titled cards with no edge between them for Chess #63, and would break `docs/reference/architecture.md:1498` and `notes/2026-08-30-outcome-map.md:55-61` |
| render context cards, or count them only | **render them** | they are why two open tasks form one group; the plain line keeps them out of the task count and change 2 dims those off the path |
| should a transfer keep the `superseded` family | **no** | a `successor_request` is continuation, not retirement; today only the tooltip distinguishes them (`outcomeMap.ts:304`, `OutcomeMap.tsx:9`, `:15`, `:91`). gitseq records 32, Chess and Tailapp none. If change 2 ships first, the walk must still follow `successor-request` contributors both ways |
| does the `superseded` legend earn its place | **open, no recommendation** | all 5883 effective supersede acts in these three rooms are intra-thread, so the dashed style has never rendered there; three rooms are not enough to remove it |
| is the stale `:7777` bundle part of this work | **no** | a deployment fix with its own request; see the observation above |
