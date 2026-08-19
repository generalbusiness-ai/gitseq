# The simplest interface for seeing what waits

2026-08-19. **Status: design note, revised once on operator feedback, awaiting
ratification by hugh.** No source changes follow until it is ratified;
implementation is a separate request resting on the ratified decision. Visual
previews for this note are in `notes/2026-08-19-ui-simplification-preview.html`,
built from the real board at depth 6500.

**Revision 1**, on hugh's feedback (assert `6c6f739a`): the tabular view sorts by
clicking a column header and the ordering subheadings are gone — the priority
rule stays, as the default sort rather than as prose; and the compact railway is
drawn **vertically, as a rail alongside the spine rows**, rather than as a
separate horizontal drawing above them. That also removed a duplication the first
draft carried, where the spine list and the railway rendered the same records
twice.

**Revision 2**, on codex's changes-requested review (report `a9da48f2`): the
acceptance example was factually wrong. It claimed the approved head `b3bf3083`
was not an ancestor of main; the head had in fact merged on 2026-08-12. The
example is restated to current git truth below, and the correction is written up
rather than quietly patched, because the mistake is the note's own argument
demonstrated on its author. Codex re-derived every other figure and reproduced
all of them.

Governing request:
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:123fc7f4c2971d4f2c48d0d9c09d8a76d1aedc21`.

Hugh asked one question on 2026-08-19: what is the simplest and most compact
effective interface for a human operator to see what is unclaimed, unfinished,
and otherwise active? This note answers it as a screen inventory, a row
specification, an ordering rule, and a thread specification in which the
railway stays but stops drawing everything. Every element that survives is
justified against one test: **does it help answer "what waits, and on whom?"**

## What is there now

The web UI is 6,433 lines across 29 files under `ui/src`. Two top-level
destinations, Work and Activity, chosen from a header nav; one right-hand pane
holding either a thread or a profile.

The Work view (`WorkDrawer.tsx`, 635 lines) puts eleven controls in front of the
first row: a search box, three lifecycle checkboxes carrying counts, three
personal filter buttons carrying counts, a "Mine" toggle, an author select, and
a switch between a topic list and a board. Below them, commitments are grouped
into "topics" derived by walking first-provenance edges (`buildWorkProjection`
in `lib/work.ts`), and each topic card prints three more counts.

Four faults, which are all the same fault.

**Counts lead nowhere.** The "Active" figure hugh saw as 57 is
`workCommitmentCounts` over every actor's open, promised and reported
commitments — 42 as this note is written. The number sits on a checkbox; the
rows it counted are somewhere else. `lib/work.ts` carries thirty lines of
comment explaining why
three headline figures once disagreed and how they were reconciled — a defect
that exists only because the numbers were computed separately from the rows.

**The display unit is not the work unit.** The fold tracks commitments. The
screen groups topics. The per-topic counts are the seam between them.

**Configuration stands in for a decision.** Two presentations, six filters and
an author select ask the operator to build the view that should have been the
default. None of them prioritise. All of them hide.

**Private state poses as shared state.** Follows and read watermarks live in
browser local storage, per browser and per actor (`lib/memory.ts`). "Unread" and
"Following" are true on one machine and false on the next.

And the thread view drowns. Take hugh's example, record **#3923** — the re-cut
back-pressure repair lane, request `48c709bd36f3751dc09f7791c8a388423eed1338`.
Seventy-one durable records descend from it: 40 statements (15 artifacts, 7
reports, 7 asserts, 6 requests, 5 promises) and 31 acts (27 supersessions, 4
ratifications). All fifteen artifacts are retired; none is live. `ThreadPane`
renders that reply tree in provenance order with acts interleaved, and
`ThreadRailway` draws every node of it. Nothing on either surface answers the
only question the operator has, which is what the status of this work is.

## Screen inventory

Two screens: **the list** and **the thread**.

### Deleted

| Deleted | Why |
| --- | --- |
| The board presentation (`WorkBoard`, `BoardCard`, `PresentationButton`) | A second layout of the same rows is configuration, not information. |
| Topics: the grouping, `TopicList`, `TopicCounts`, follow buttons, alias and title chips | A hierarchy no durable record names, and the origin of the counts that disagreed. |
| The three lifecycle checkboxes and their counts | State belongs in the row, not on a control. |
| "Unread" and "Following", and the browser-local memory behind them | Read positions that do not sync are a promise the UI cannot keep. |
| "Mine" and the author select | Search finds; the ordering rule already surfaces your work. |
| The Vocabulary panel | Reference material. It belongs in `docs/`. |
| The Activity destination and its header nav (`Stream.tsx` as a screen, `Composer.tsx`) | The whole log in reverse order is not an operating surface. Its events stay reachable inside a thread. |
| The "Other attention" collapsed block | Its contents become ordinary rows in the one list. |
| The Thread/Railway tab pair | The railway sits inside the thread view; there is no view to choose between. |

### Surviving

| Survives | What it answers |
| --- | --- |
| The list of open requests, one line each | The whole question. This is the screen. |
| One search box over that list | With grouping and filters gone, the only way left to find a named thread. |
| The thread view | "What does this one wait on?" |
| **The thread railway**, vertical and salient by default | "How did it get here, and what relates to what?" — kept because relationships between records are real and a flat list loses them. Drawn as a rail beside the spine rows, so it costs a gutter rather than a second band of screen. |
| Sortable column headers on the list | "Show me the same work by age, or by who owes it." |
| The composer inside the thread | Acting is why the operator looked. |
| The presence strip | Who is here to act. |
| The "for you" counter | What is addressed to me. It already steps to the oldest unseen row on click, which satisfies the count rule. |
| Join gate and actor identity | Signing is a custody decision, never defaulted. |
| The connection indicator | A page showing stale data must say so. |

## The row

One line, fixed columns, no wrapping at 1,024 pixels:

```
state · waits on · age · title · #ticket
```

**state** — one word from a closed set of four:

- `unclaimed` — open, no promise.
- `in progress` — promised, no report.
- `reported` — a report is filed and the requester has not closed it.
- `needs attention` — world-stale (`describes_superseded_world`), disputed, or a
  promise resting on no request.

Ordinary staleness never sets this state, and neither does the lifecycle-stale
status. That restraint is the difference between a flare and noise: 27 of the 42
live commitments carry the ordinary stale qualifier, so a row state fed from it
would colour nearly two rows in three, while world-staleness selects **11** — the rows
whose approval or artifact a merge will actually refuse.

Only `needs attention` is coloured. No fifth state: whether a reported row is
already approved and waiting on a merge is a fact about *who* it waits on, and
belongs in the next column.

**waits on** — one name: the actor who must act next, from the commitment's
`waiting_on`, which `internal/statusview` already computes (`WorkItem.WaitingOn`
in `query.go`). Not requester and performer and addressed-to — one name. A human
name is emphasised, an agent name plain, read from the roster's actor `kind`,
which the projection already carries (`hugh` is `kind=human` with roles
`operator, participant, ratifier`; `claude`, `codex` and `planner` are
`kind=agent`).

**age** — time since the last durable event anywhere in the thread, not since
the request was filed. A three-week-old request that moved an hour ago is not
neglected work. Rendered `3m`, `6h`, `7d`.

**title** — the request's first line, truncated to the row.

**#ticket** — the log's own number, the only handle a person can say aloud.
`eventName` in `lib/api.ts` already refuses eight-hex prefixes; the row keeps
that discipline.

Nothing else. Not the author, the topic, the temporary-discussion count,
worktree pills, focus badges, ratified badges, or per-row staleness.

Every field except `age` is in `statusview.WorkItem` today. Request c4d7ce8c
(#6356) adds the reported head, the report's `body.status`, and the latest
review verdict with its ratified flag — which is what lets **waits on** say
"codex, to re-anchor" rather than a bare name, with no per-row `inspect` call.

## Ordering: the default sort

One rule decides what the operator sees first, and the column headers let them
ask a different question without changing what is on the list.

The **default sort** is the priority rule:

1. `needs attention`, oldest first.
2. `unclaimed`, oldest first.
3. Waiting on a human, oldest first.
4. Everything else — waiting on an agent — most recently moved first.

Groups 1 to 3 cannot move without a person. Group 4 is running and only needs
watching, so recency is right there. Oldest-first inside the first three groups
because in a queue nobody is claiming, age is the only signal of neglect.

The groups are not announced. There are no subheadings above the rows: the
`state` column and the single colour on `needs attention` already say which
group a row is in, and a subheading that repeats the column beside it is the
same fact printed twice. What the screen shows instead is a small marker in the
header row saying the list is in priority order.

**Every column header sorts.** Click `state`, `waits on`, `age`, `title` or the
ticket number and the list reorders by that column; click again to reverse;
click a third time and it returns to priority order. That is how the default
stays reachable without a separate reset control.

Sorting is not the configuration this note argues against, and the difference is
worth stating because it is the line the whole design sits on. **A sort reorders
the rows that are there; a filter decides which rows exist.** Filters were
deleted because they hide work and because the operator cannot tell, from a
filtered screen, what they are not seeing. Sorting hides nothing: the row count
is identical before and after, every row remains on screen, and one more click
returns to the priority order. Direct manipulation of a column beats a
configuration panel that has to be set up before the screen means anything.

Measured on the 2026-08-19 board at depth 6500, the default sort partitions the
42 live commitments into 11 needing attention, 8 unclaimed, **0 waiting on a
human**, and 23 running. An empty third group is the most useful thing this screen can tell
hugh — nothing is blocked on him — and today's Work view cannot say it at all.
Sorting by `waits on` is how he would ask the same question of a longer list.

## What the list holds

The list is the **42 live commitments**: open, promised, or reported. It is not
everything the fold knows, and the two things it leaves out are left out on
purpose, each with one quiet line beneath the list that clicks through to
exactly the rows it counts:

- **27 of these rest on reasoning that has moved.** Ordinary staleness, per
  request 427b3bd3 (#6349): aggregated, uncoloured, never a row state.
- **110 stale requests nobody claimed.** Lifecycle-stale commitments, out of the
  default list because they are not work in flight. Reachable in one click.

Without that second line the list would be 152 rows, and the 110 would sort
above the 42 that are actually moving.

## Counts

Exactly one number at the head of the list: **"42 open requests."** It equals the
rows below it. The only other numbers on the screen are the two summary lines
above, and each opens to exactly its own rows.

No lane counts, no per-topic counts, no counts on filters, because there are no
filters. This restraint matters at the current scale: 956 of 1,199 artifacts
carry ordinary staleness, so a per-row flag is noise, while world-staleness is
rare and is the thing that actually blocks a merge.

## The thread: one vertical rail

The spine and the railway are **one thing**, not two. An earlier draft of this
note listed the five spine records as rows and then drew the same five records
again as a horizontal railway above them. That is the same fact rendered twice,
occupying two bands of a screen the whole design exists to shorten.

The railway is therefore drawn **vertically, in a narrow gutter down the left of
the spine rows**, one node per row, at that row's own vertical position, with
the rail connecting them. Reading down the rail and reading down the rows is the
same movement. A branch — the blocker — leaves the rail sideways beside the row
it concerns, which is exactly where a reader is already looking.

```
 ●  request    #3923  codex → claude, re-cut the back-pressure repair
 │
 ●  promise    #3924  claude claimed it
 │
 ●  report     #4043  ready for review at acf13441
 │
 ◉  verdict    #4075  approved by codex, ratified, over b3bf3083
 │
 ●  merge      44d8b4fa  b3bf3083 landed on main, 2026-08-12
 │╲
 │ ✕  open     #4043  reported 7d ago, never ratified — still waits on codex
```

Each row carries what happened, who signed it, when, and its `#ticket`. A
station that has not happened is a hollow node and a dim row naming what would
fill it and who owes it — `promise — unclaimed, addressed to codex`. The rail
and the blocker branch are the only coloured things on the screen.

This is what the railway was always for. Relationships between records are real,
and a list loses them; a rail alongside the list keeps both, and costs one gutter
rather than a second drawing.

By default the rail carries the **salient set**, computed from the fold rather
than chosen by hand:

1. the root request;
2. the effective promise that claimed it;
3. the effective artifact or report that answered it;
4. the latest effective review verdict on that head, with its ratified flag;
5. the merge that landed the approved head, **asked of git at render time** and
   never stored as a typed field, or the ratification that closed the
   commitment;
6. any **live blocker** — a commitment that shipped but was never closed,
   world-staleness on the approved artifact, a retired basis, a dispute, or an
   ineffective act somebody is still waiting on.

Station 5 is the one that reads two sources. The fold knows whether the
commitment closed; only git knows whether the code landed. A thread can be in
either state without the other, and the two disagreeing is common enough that it
deserves a station rather than a footnote: work that shipped and stayed open is
invisible on every surface the room has today.

Everything else is elided behind four expanders, each labelled with its count,
each opening to exactly the records it counted:

- **Repair chain** — retired predecessor artifacts and the supersessions that
  retired them.
- **Earlier rounds** — superseded review verdicts.
- **Superseded claims** — refiled requests, replaced promises and reports,
  ratifications.
- **Talk** — asserts, notes, presence, chat.

Elision is stated, never silent. An expander whose count is zero is not drawn.
Any record the fold ruled ineffective is marked wherever it appears rather than
given a bucket of its own.

### The acceptance example, #3923

Under this rendering the back-pressure lane puts **five nodes and one blocker**
on the rail, out of seventy-one records:

| Station | Record | What it says |
| --- | --- | --- |
| request | **#3923** | codex → claude, re-cut the shipped back-pressure repair |
| promise | **#3924** | claude claimed it |
| report | **#4043** | claude reported it done at head `acf13441` |
| verdict | **#4075** | codex, **approved**, ratified, over exact head `b3bf3083` |
| merge | **`44d8b4fa`** | `b3bf3083` landed on main on 2026-08-12 |
| blocker | report **#4043** | filed 7 days ago, never ratified — the commitment is still `reported` and still waits on codex |

Sixty-six records go behind the expanders: `Repair chain 41` (14 retired
artifacts, and all 15 in this thread are retired, plus 27 supersessions),
`Earlier rounds 3` (verdicts #4015 and #4033, changes-requested, and #4054, an
earlier approval over head `acf13441`), `Superseded claims 15`, `Talk 7`.

The status of this work, at a glance, is therefore: **shipped but never closed —
the code has been on main since 2026-08-12, and the commitment has sat
`reported`, waiting on codex to ratify report #4043, for seven days since.**

That sentence needs *both* sources, which is the point. The fold alone says
`reported, waiting on codex` and would leave a reader thinking nothing had
landed. Git alone says `b3bf3083` is an ancestor of main and would leave them
thinking the work was done. Neither is the answer. The rail's merge station is
the one place the two are joined, and joining them is what turns four facts into
a sentence a person can act on: *stop reviewing this and close it.*

Note what the salience rule had to get right to say it. Three of the four
verdicts in this thread are superseded rounds, and the one that counts is the
**latest** effective verdict, #4075 — not #4054, which is also an approval, also
ratified, and one screen higher in the reply tree. A rendering that showed the
first approval it found would name the wrong head. Salience is computed from the
fold, which is the only reason it can be trusted.

### What this example cost to get right

The first two heads of this note said the opposite. They rendered the merge
station as *never happened* and concluded the work was approved but unmergeable,
resting on a check that reported `b3bf3083` was not an ancestor of main. The
check was wrong. It ran in a directory that is not a git repository, exited 128,
and the shell fallback beside it turned that failure into a confident negative —
a command that never ran, read as evidence of absence. The merge had in fact
landed on 2026-08-12, seven days *before* the claim was written, so the sentence
was false when it was made rather than overtaken later. Independent review caught
it by re-deriving the ancestry rather than reading the note.

This is not an aside; it is the thesis with the author as the worked example.
The whole argument for a salient rail is that a person should not have to
assemble the current state by hand from seventy-one records, because when they
do, they get it wrong and then publish it. Two properties of the design follow
directly, and both are load-bearing rather than decorative:

- **Every station is derived, never asserted.** The merge station asks git at
  render time; it is not a field anybody types. A design where that station can
  be stale by hand reproduces this defect at scale.
- **A check that fails must not read as a negative.** The row state comes from
  facts the fold and git return, and a query that errors renders as unknown, not
  as absence. `unable_to_flare` already carries this idea in the projection: an
  artifact whose silence is not evidence that it is current.

### The rest of the thread

Nothing else. The four expanders above are the whole of it — an earlier draft of
this note put a second set of disclosures below the railway for provenance,
repair and discussion, which is the same three mechanisms twice under different
names. One list of counted expanders, opening to records, is enough. Beneath
them, the composer.

An ordinary thread is therefore five railed rows, four collapsed expanders and a
composer. That fits one screen at any usable window height, which is the
acceptance test.

## Acceptance

1. One list; one search box; no filter controls; no presentation switch.
2. Every row is one line and does not wrap at 1,024 pixels.
3. Every column header sorts, and a third click restores the priority order.
4. No ordering subheadings appear above the rows.
5. Every count on the list screen — the headline and the two summary lines —
   opens to exactly the rows it counts, and so does every expander count in a
   thread. There are no other numbers on either screen.
6. The thread draws one vertical rail beside the spine rows, not a separate
   drawing; each rail node sits at the vertical position of the row it belongs
   to.
7. A thread with no repair and nothing expanded fits one screen.
8. The #3923 thread answers "what is the status of this work" from its default
   rendering alone, without opening an expander.
9. Every surviving element in the inventory states which part of "what waits,
   and on whom?" it answers.

Weight is a consequence, not a condition. The deletions remove roughly 2,000
lines outright; the railway survives, gains a salience filter, and absorbs the
spine list instead of sitting beside it.

## For ratification

Three choices are worth settling before implementation rather than during it.

- **The commit-graph railway** (`Railway.tsx`, today embedded in the Work view's
  "Repository context" panel, drawing the newest 80 commits of main) is a
  different thing from the thread railway. Proposal: keep it, but move it inside
  the thread and scope it to the commits that carry that thread's records, drawn
  vertically on the same rail as the records themselves — a commit and the
  artifact naming it belong on one line, not in two pictures. A graph of main's
  last 80 commits answers nothing about what waits.
- **Deleting the Activity destination** is the largest single deletion, 1,343
  lines with `Composer.tsx`. Proposal: delete it. Flagged because it is the one
  place a reader can watch the room in real time, and losing that is a real
  loss, not only a saving.
- **Closed requests get no destination.** Search finds them, and a closed row
  shows its terminal state. If that proves wrong in use, one "include closed"
  toggle is the smallest repair — and the only configuration this design would
  accept back.
