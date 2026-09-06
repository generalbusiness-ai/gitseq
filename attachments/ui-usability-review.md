# Make the workroom answer the reader's next question

Keep the current table and commitment spine. Make it easy to find a record,
read its evidence, and tell whether it asks for action. The largest additional
gap is the reading journey, now assigned as Notes/preview request #19022.
The existing graph-navigation plan remains a separate candidate plan.

I reviewed native Chrome against Gitseq UI source
`183211c3b5bff170171c5bfbe9bfbc3aacde08f6`, using Gitseq (~19,024–19,078
events), Tailapp (3,890–3,896), Chess (834–835), and Inventory (502).
The attached observation log records reproductions and limits. This is a
review of the workroom UI, not Chess game acceptance.

## What works

The table makes the next performer easy to scan. In Tailapp, filtering #3821
and choosing Completed showed exactly the three counted matching request rows.
Opening the delivery and returning preserved that filter and population.
The spine clearly separated a report at62c0d8f4 from its main merge a3b29b3b.
Chess's empty report station explicitly said the report was still owed by
Codex. Talk exposed notes alongside that spine. Related-artifact navigation,
browser Back/Forward, row focus and Enter all worked. Loaded interactions
remained responsive in the large Gitseq history during this qualitative check.

## Preferred order of work

1. **Complete the reading journey (#19022).** Search #19016 currently finds no
   note in the request population. Its raw link opens it, but the encoded
   canonical link reports “No record at this address”. Expanded details have
   no attachment control. Use clearly separated Requests and Notes views,
   with “Open #19016 — [title]” as an exact-number result independent of request
   filters. A note should show a short title, author, date and current flags.
   Open evidence and cited files in one preview showing their owning event,
   path and exact revision. Preserve the source thread while it is open.
   **Acceptance:** find #19016, read its Markdown and JSON attachments, open
   a reviewed source path at its exact head, copy/reload the encoded link,
   and use Escape/Back/Forward without losing useful context. Missing or
   ambiguous files must explain the problem. This is the first independently
   assessable slice; it does not require graph changes or a new lifecycle.

2. **Make attention labels literal.** Inventory showed “Nothing is waiting”
   and “16 for you”. The latter contained already-landed #426; opening it
   reduced16 to15, so it is unread history rather than16 outstanding duties.
   Prefer “Unread16”, with each item's completed/current status visible.
   Chess showed no open requests but two stale commitments still owed. Say
   “No active requests. 2 stale commitments need attention”, linking that
   population, and call its tab “Stale commitments” rather than implying the
   promises disappeared. A completed row's next actor should be “None —
   completed”, not “unassigned”. Tailapp's completed root was red because of
   historical evidence flags; show a separate explained evidence warning
   instead of making “satisfied” appear to mean failure.
   **Acceptance:** Inventory's empty board and unread menu make different
   claims; Chess#720 remains discoverable as owed; completed records never
   suggest they need a new assignee. Keep the fold's classifications intact.

3. **Keep the task title visible at narrow widths.** At400% browser zoom
   (~353 CSS pixels across), fixed state/actor/age columns put the title and
   event number off-screen. Keyboard focus scrolled the row vertically but
   did not reveal its title. At that width, use a two-line row:
   “#3821  Align root module …” followed by “Completed · main · no action”.
   Let the title wrap and move secondary details beneath it. Collapse long
   IDs and raw stale-basis lists behind a labelled technical-details control;
   keep the actual explanation, evidence and current duty ahead of them.
   **Acceptance:** at320–400 CSS pixels and200% text enlargement, the title,
   event number, current duty and preview close control remain reachable
   without horizontal page scrolling. Verify keyboard return focus too.

## Reconcile the work already in flight

The existing `notes/2026-09-04-task-graph-navigation.md` already addresses the
count mismatch and confusing lineage: count tasks consistently, distinguish
selected tasks from context, limit edges to task lineage, and centre a
selection at readable scale. Current Graph selection already outlines the
chosen node, dims unrelated cards and highlights connecting edges. Do not
reimplement that feature or describe it as absent. Its labels remain tiny,
context cards exceed the headline population, and a5-thread cycle warning
appeared. Keep the existing count/lineage/readable-navigation plan, revising
only its baseline and preserving the emphasis that works. It is not adopted
by this review.

I5's candidate799b5949 separately handles destination, approval and source
landing versus artifact-accounting facts. The current Tailapp delivery spine
showed a report and merge without the exact review station; use that real
thread as I5 acceptance evidence rather than creating a parallel landing UI.
Finish the reading slice before extending graph interaction.

## Limits

The narrow check used browser zoom/reflow, not a physical phone. Zoom was
restored to125%. Focus visibility was observed; contrast ratios, screen-reader
speech, a disconnected resident and an instrumented responsiveness budget were
not measured. Back returned to the correct Chess task but collapsed its open
details and Talk section. A future context-preservation check should include
those local expansion states. A room switcher might help frequent cross-room
work, but the current review used known URLs; discovery and configuration need
separate design and are outside the first slice. No source implementation or
new workflow authority is delivered by this review.
