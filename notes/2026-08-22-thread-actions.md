# Acting from the thread view

Design note, 2026-08-22. Draft for discussion; nothing built yet.

## The problem

Request #8162 asks hugh to change repository settings. Hugh opens the thread
and cannot see how to answer it. The only durable actions in the UI today are
the row shortcuts in a hover toolbar (`semanticActions` in
`ui/src/components/Toolbar.tsx`): they appear only while the pointer rests on
the one row they belong to, and "mark done" appears only on a promise row
after the viewer has promised. To close #8162 from the browser the operator
has to know to hover the request, click *accept*, then hover the new promise
row and click *mark done*, then type a report with no guidance about what
the report should say. The UI simplification of 2026-08-19 removed the
earlier report form and left this path, which nobody finds.

The detail panel (thread-record-detail, in review) fixes seeing the record.
This note is about answering it.

## What the operator should experience

One place to act. At the bottom of the spine, above the composer, a fixed
bar titled **Your move** lists every durable act the viewer can take on this
thread right now, as plain verbs, and nothing else. It is computed from the
fold's state and the viewer's position, exactly as the hover shortcuts are
today, but it is always visible and it is the only place these verbs live.
When the viewer has no move the bar says so ("nothing for you to do here")
rather than disappearing, so a blank is never ambiguous.

Pressing a verb puts the composer into that mode: a chip names the act, the
fields the act needs appear, the text box is focused with a sensible
prefill, Enter sends, Escape cancels back to plain talk. One composer, one
send button, one error line. After a send the spine redraws from the
projection and the bar recomputes; the operator sees their own act land as a
station or a row.

The verbs, by who the viewer is:

| Viewer is | Verbs | Act filed |
|---|---|---|
| Addressee of an open request | **Accept** · **Done** · **Can't** | promise on the request · report (see below) · dissent on the request |
| Performer with a live promise | **Done** · **Blocked** · **Withdraw** | report on the promise · assert on the promise · supersede the promise |
| Requester of a reported commitment | **Accept** · **Needs work** | ratify the report · dissent on the report |
| Ratifier, thread root is a proposal | **Agree** · **Disagree** | ratify · dissent |
| Addressee of a review request | **Review** | report with `verdict`, `head`, `artifact` (what `gs review` files) |
| Author of any live record in the thread | **Withdraw** | supersede that record |
| Anyone | **Note** | assert resting on the root |

Talk stays in the composer's default mode and is not a move.

**Done** is the act this note exists for. Until proposal 6952579a lands, a
report must rest on a promise, so for an addressee with no promise *Done*
files the promise and the report as two records under one idempotency scope
and one button; the operator never learns that two were needed. Once the
fold admits a report on a request, *Done* files one report resting on the
request. Either way the report form is the same:

- the request's **conditions of satisfaction**, shown verbatim above the
  text box as the thing the report is answering — read, not ticked;
- **text**: what was done and which conditions were met;
- **commit** (optional): an exact head, for work that lives in git;
- **status** (optional, default `satisfied`): the word the fold's
  commitment row will show.

Nothing here is structured beyond what the fold already reads. Conditions
are prose in `body.conditions`; the form shows them, it does not parse them.

## What changes

- `ui/src/lib/moves.ts` (new): `movesFor(root, index, me)` returns the verb
  list above. It is `semanticActions` moved out of the toolbar, widened to
  the thread rather than one row, and given the three missing verbs
  (*Done* for an addressee, *Blocked*, *Note*). Pure; tested.
- `ui/src/components/Thread.tsx`: a `YourMove` bar between the expanders
  and the composer; the composer gains modes `report`, `review`, `assert`
  with their fields; `RowToolbar` and `semanticActions` are deleted from the
  thread — a verb lives in one place.
- `ui/src/components/Toolbar.tsx`: shrinks to `ToolbarButton`, or goes.
- The request list gains nothing. Acting happens in the thread.

## What stays the same

The authorization gating and the one-flight idempotency key per intention
are unchanged; so is every record shape the fold reads. The thread's spine
and expanders are unchanged. No new semantics: the bar shows what the fold
would admit, and the fold still decides.

## Tests

- `movesFor` for each row of the table, including the viewer who is both
  requester and addressee, a retired root, and an ineffective root.
- Browser: pressing *Done* on an unpromised addressed request sends promise
  then report with the same scope; pressing it again before the projection
  updates sends nothing; the conditions appear in the form; Escape restores
  talk mode.
- Mutation: remove the *Done* branch and the browser test fails.

## Open question

Whether *Can't* should exist. A dissent on a request says "I will not do
this"; the requester then withdraws or reassigns. Without it the only honest
answer to a request one will not do is silence, which the board reads as
unclaimed. I think it should exist and say so plainly.
