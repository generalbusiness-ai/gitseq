---
date: 2026-08-30
status: draft — root-cause analysis and plan, for ratification. Nothing
  here is built. Statements marked **exists** cite what main carries at
  95c2b1e3; measurements are from the workroom projection at depth 14680
  and cite records by #N sequence.
origin: hugh, 2026-08-30, after 17 rows sat in the board's "stale, not in
  flight" population and 11 of them turned out to be finished work that
  nothing had closed. "Why do items end up in a state where they need to be
  individually investigated and dredged out of the mud? We need a plan that
  fixes the root cause." Revised after an independent review that found
  two blocking holes in the first draft's P4 and one refuted mechanism
  claim; both are recorded below.
---

# Staleness: root cause and plan

## The symptom

On 2026-08-30 the "stale, not in flight" population held 17 commitments.
Investigating each one by hand found:

- 11 were finished. Seven remote-link rounds (#13345, #13390, #13427,
  #13446, #13471, #13489, #13518) had heads that were ancestors of main since
  2026-08-28, round eight as a byte-identical recut; four
  completion-reliability rows (#13314, #13545, #13603, #13714) had landed
  through a rebuilt head at merge `ebdca9fa`. None had closed.
- 4 were real open work (#13492, #13888, #13890, #13943) whose addressee had
  answered "stale — please refile". For two of them (#13888, #13890) no
  refile was possible: there was no fresh in-main artifact at `internal/app`
  or `SKILL.md` to refile onto (#13953 measured this).
- 2 were decisions for the operator (#13523, #14486), one already made.

Draining them took about 30 durable acts and a reading of the git graph.
That is the cost this plan removes.

## Measurements

- 154 of 332 live artifacts are stale. There is no fresh in-main artifact at
  `internal/app`, `SKILL.md` or `internal/workroom`; the only fresh ones at
  those paths stand on unmerged candidates.
- The merge-published `internal/app` artifact #14231 (receipt #14225) is
  stale. Eleven retired bases under its receipt chain are dated after the
  receipt. Seven are the merge's own planned retirements of the candidate's
  artifacts (#14234–#14251); the receipt's plan settles those. Four are
  artifacts a candidate artifact rested on, retired by **later** merges at
  their own paths: `docs/reference/architecture.md` (#14379), `cmd/gs`
  (#14383), `docs/how-to/use-in-a-cloned-repo.md` (#14601),
  `docs/reference/gs/serve.md` (#14624). Each reaches the successor over an
  artifact-to-artifact edge, the one edge ordinary staleness never quiets.
  `internal/app` on main did not change in any of those merges.
- 9 of the 23 commitments in a live status (open, promised, reported,
  awaiting-merge) or in status `stale` carry the stale flag.

## The mechanism, in five parts

Part 1 is the source. Parts 2 and 3 turn it into hidden rows and blocked
writes. Part 4 says why finished rows do not close. Part 5 multiplies them.

### 1. Later succession at another path flares a merge's published successors

**exists.** A merge receipt rests on the approval (`cmd/gs/succession.go:788`);
the approval rests on the candidate's artifacts; each candidate artifact
rests on the artifact it replaced at its path; the merge-published artifact
rests on the receipt (`succession.go:799`). The fold already knows a
successor must not be born stale: `receiptCheckpointSettles`
(`internal/workroom/fold.go:2574`) settles every cause active at the
receipt's position, and [docs/concepts/staleness.md](../docs/concepts/staleness.md)
lines 102–114 state it. The same page states the gap: "A cause that arose
after the receipt … still flare[s] the successor."

The carrying edge is artifact provenance under the candidate's artifacts,
not the review reasoning: a succeeded retirement on a request, promise,
report or approval edge is already quieted (`fold.go:2311-2324`). But a
candidate artifact at path Q rests on the previous artifact at Q. When a
later merge at Q retires that previous artifact, the edge is
artifact-to-artifact, it is not quieted, the candidate artifact goes stale,
and staleness runs up through the approval and receipt to the successor at
every path the merge published — including paths that merge did not touch
and no later merge has touched. Merges happen several times a day; the
`internal/app` successor above was flared by the first later merge at
`architecture.md`, #14379, 154 positions after it was published.

Note also that `causesSettledAtReceipt` (`fold.go:2625-2657`) applies none of
the succession quieting that `stalenessOf` applies — the node test at `:2636`
and the edge recursion at `:2652` both ignore `successors` — so the walk also
fails on quieted non-artifact edges. That is a smaller defect than the policy
gap, and the policy change below makes it moot: once post-receipt causes stop
at a published successor, quieting has no observable effect on the walk. It
is recorded here so the measurement in P1 is read correctly, not as a
separate fix.

Staleness was defined to mean "the reasoning this rests on moved." For a
merge-published artifact it now means "some later merge happened at a path
the candidate once touched." The artifact asserts one immutable fact — *this
is what stands at path P at commit C on main* — and no retirement of
reasoning can make that fact wrong. Only a later successor at P retires it,
and succession already does that.

### 2. Staleness is promoted from a flag to a status

**exists.** `fold.go:3220` (open rows with no completion) and
`fold.go:3266-3268` (promised rows with no completion) set
`Status = "stale"`, replacing the lifecycle word. The browser's "stale, not in
flight" population is exactly `status == "stale"` (`ui/src/lib/rows.ts:91`),
and `rowState` there says: "Outside the live populations the fold has already
said this commitment is not in flight." A row with a completion is never
flipped: `entry.Report` is set for every completion at `fold.go:3248`, so the
guard at `:3266` does not fire. (The first draft claimed a merge-satisfied
row could be flipped; review refuted that.)

A promised row is in flight — the performer is building, or has built and
not yet published. The fold hides it because a basis two hops away was
succeeded.

### 3. Staleness gates writes

**exists.** `refuseDeadBases` (`internal/app/admission.go:173`) refuses a
merely-stale basis exactly like a retired one unless the actor signs
`dead_basis_override` (only `DeadBasisSupersede` is skipped, `:176`).
`reassign_if_unclaimed` refuses a stale request (`fold.go:1225-1226`).
AGENTS.md step 1 (lines 28–31) tells the addressee to "name the staleness
and ask the author to refile on current bases"; SKILL.md's intake paragraph
(lines 173–177) says "name the staleness and the repair — the request's
author, not the addressee, replaces it on current bases"; SKILL.md
discipline 11 (lines 272–278) tells the author to replace it.

With part 1, the refile lands on a basis that is stale again by the time the
addressee reads it. #13953 measured this: both requests rested on the newest
artifact at their paths, both artifacts were stale, and a refile would name
the same artifacts. Planner then filed the fix for this very gate (#13888)
and could not reassign it because the reassign guard refused it as stale
(#13962). Every actor learned to spend acts on overrides, re-anchors and
refiles instead of work.

### 4. A merge closes only the exact head it approved

**exists.** `fold.go:3353-3384` builds `mergedArtifacts`: a completion is
satisfied by a receipt only when the artifact is in the approval's
`rests_on` **and** in the receipt's succession plan **and** authored by the
merger **and** stands at the receipt's `merge_candidate`. That is the right
bound — the merge closes only what the reviewer saw and the merger signed.
A promised row whose head is an *earlier* head on the same branch — every
earlier round of a repair lane — matches none of it. Its closure is a
performer report plus requester ratification (`fold.go:1021-1037`), or
supersession. AGENTS.md step 5 says the receipt "closes an assigned
implementation commitment"; it does, for exactly one commitment.

### 5. One request per review round

**exists as practice, not code.** AGENTS.md step 1: "If work is discovered
mid-flight, create a child request." Step 4: "Any change to the head
invalidates the approval and returns the implementation to step 3." The
remote-link lane read the first sentence as applying to review findings and
filed a child request, promise and seven artifacts per round. Eight rounds
produced eight commitments for one change, and part 4 could close only the
last. Every earlier one is an orphan the merge cannot reach.

## The plan

Each item is one bounded request with a test that fails when the change is
reverted. P1, P2 and P3's fold half change projection bytes for existing
logs and land under **one** fold profile bump (`internal/workroom/schema.go`
`ProfileVersion`), deployed in the state@1 order: restart the resident and
adapters on the new binary before rebuilding `gs`.

### P1. A merge-published artifact is a staleness root

A cause dated after the receipt does not flare the artifacts that receipt
published. Two post-receipt causes are kept and still flare: retirement of
the receipt itself (the merge was withdrawn or disputed), and condemnation
of a planned succession (a successor the receipt named was itself
withdrawn). A cause the fold cannot date stays unsettled, as today
(`fold.go:2639`, `when == 0`): unknown still means no. What stops flaring,
stated so it is accepted explicitly: post-receipt retirement of the
approval, the review promise, the review request, the governing request or
decision, and any artifact under the candidate's provenance. The
immutable-fact argument above applies to all of them — none can change what
stands at P at C. Documents that rest on a *succeeded* artifact still flare;
that is the signal staleness is for, and it is untouched.

Measurement before the change, on the branch: run the walk with
`stalenessOf`'s quieting applied at the edge (`fold.go:2649-2652`, carrying
edge type into the node test) and split today's stale successors' causes
into true artifact-provenance causes and walk artefacts. For #14231 the
split is already known — four provenance causes remain — so quieting alone
cannot clear it; the measurement is evidence for the ratification, not a
separate landing.

- Site: `fold.go:2574-2660`; `docs/concepts/staleness.md` lines 110–114
  rewritten; `docs/reference/architecture.md` layer 5, "The receipt
  freshness checkpoint" (lines 854–879), whose sentence at line 858 — "A
  cause arising after the receipt still propagates" — is the contract this
  reverses; updated in the same head.
- Test that flips: `internal/workroom/receipt_checkpoint_test.go`
  `TestReceiptCheckpointDoesNotSettleACauseThatAroseAfterTheReceipt` inverts
  for a succession cause; two new tests keep the receipt-retired and
  condemned-succession flares; one keeps the undated-cause refusal.
- Effect: merge-published artifacts stay fresh until succeeded at their own
  path. Requests resting on them stay fresh. The "no fresh basis" loop
  cannot occur.

### P2. `stale` is a flag, never a status

Replace the two status assignments. `fold.go:3220` → the row stays `open`;
`fold.go:3266-3268` → `entry.Stale = stale[request] || stale[promise]` set
unconditionally in the default branch (promised-row entries are built
without `Stale` at `:3229`, so a bare deletion would drop the flag). Every
consumer of the status value changes with it:

- `internal/statusview/actor.go:291` (intake lane: an open-stale row is
  unclaimed), `internal/statusview/query.go:133` (`knownStatuses`), `:179-190`
  (normalisation) and `:195-197` (cache key), `internal/statusview/view.go:489-491`
  (count rendering), `cmd/gs/succession.go:374` (`unsettledCommitment`,
  protected-sibling classification), `cmd/gitseq-mcp/main.go:640` (tool
  schema enum).
- `work {statuses: ["stale"]}` keeps working: normalisation maps it to
  `stale: only` over the four live statuses, so no caller breaks.
- `ui/src/lib/rows.ts`: delete the `stale` population; it equals `moved`
  (live rows with the flag, `rows.ts:88-89`).
- `docs/reference/gs/status.md`, `docs/reference/mcp/status.md`,
  `docs/reference/mcp/work.md`, `SKILL.md:58-61`,
  `docs/reference/architecture.md` layer 6, "Staleness is held apart from
  lifecycle" (lines 1134–1156).
- Test: a promised row with no completion whose request is stale projects
  `promised` with `stale: true`; an open row whose request is stale projects
  `open` with `stale: true` and appears in the addressee's unclaimed lane.
- Effect: in-flight work is never hidden.

### P3. Admission refuses retired bases only

Fold half, already promised by claude as #13888: `refuseDeadBases` refuses a
retired basis and admits a stale one with the staleness recorded on the row;
`reassign_if_unclaimed` drops its staleness precondition (`fold.go:1226`).
Discipline half, this note: delete "name the staleness and ask the author to
refile on current bases" from AGENTS.md lines 28–31 and SKILL.md lines
173–177, and the sentence "then the request's author replaces the stale
request on that current basis" from discipline 11 (SKILL.md lines 272–278).
Replace with: *a stale request is answered like a fresh one; the performer
re-checks the conditions against current main and records that check in the
artifact text (the artifact is the report, AGENTS.md step 3).* Refile only
when a condition, the addressee, or the governing decision actually changed
— discipline 11's last sentence already says so.

- Test: #13888's mutation tests; a docs test that the deleted sentences are
  gone.
- Effect: no override ceremony, no refile loop. With P1 the case is rare;
  with P3 it is harmless when it happens.

### P4. Drain finished rows by their own closure; no new fold rule

The first draft proposed closing every commitment whose head is an ancestor
of the merged head, and retiring artifacts at unchanged paths. Review found
both unsafe: every commit on main is an ancestor of every future merge, so
an actor could get an unrelated commitment closed by naming an old main
commit; and a retirement at an unchanged path names no successor there, so
it is a condemnation. Both are withdrawn.

With P5 there is one commitment per lane and the exact-head rule in part 4
closes it — provided the final round's reporting artifact stands at a path
the merge changes, because the receipt's plan is cut to changed paths
(`fold.go:1505-1519`) and an artifact at an unchanged path is never in it.
The orphaned rows that exist today are drained once, by the shape verified
on 2026-08-30: the performer files a closure report resting on its promise
with `body.commit` = the merged head and the dead-basis override, and the
requester ratifies; the row reads `satisfied` (reports #14669–#14676 and
their ratifications). Rows whose named head was replaced by a rebuilt head
close by requester supersession naming the merge; those read `cancelled`,
with the merge named in the retirement text. The drain-decision request
#14687 (`71e7387c`) is answered by this section and is retired on
ratification.

If orphaned rows recur after P5, the bounded design is: `gs merge` records
the landed commit set in the receipt as an explicit field, and the fold
closes only a commitment whose completion artifact is by the promisor, in
the approval's `rests_on`, and at a commit in that set. It is not proposed
here.

### P5. One implementation request per lane

AGENTS.md step 1: "If work is discovered mid-flight" becomes "If the *scope*
changes — a new condition of satisfaction, or work the request did not ask
for". A `changes-requested` verdict returns the implementer to step 3 on the
same request and promise; they publish a new head and fresh artifacts
resting on the same promise. No child request for review findings. Each
round's review is still its own request addressed to the reviewer, as step 4
requires. SKILL.md's loop paragraph says the same in one sentence.

- Site: AGENTS.md steps 1, 4; SKILL.md.
- Test: docs test that the old sentence is gone.
- Effect: one implementation commitment per change, closed by the merge of
  its final head under the existing rule.

## Order

1. **P2 + P3-fold + P1** in one head, one profile bump — restores
   visibility, unblocks filing, stops the self-staling. P1's branch
   measurement is part of that head's evidence.
2. **P3-text + P5** — text only, one head, any time.
3. **One-time drain** of today's orphaned rows by the P4 shape; no code.

## What changes and what does not

Changes: two status assignments become a flag; one admission branch and one
guard precondition are removed; one walk gains one boundary (post-receipt
causes stop at a published successor); four sentences of discipline are
replaced by one. No new kind, act, lane or field.

Does not change: retirement still propagates. A bare `supersede` and a
succeeded artifact still flare every document and decision that rests on
them. `gs merge` still refuses a verdict shown a superseded world and still
records ordinary staleness in the receipt. Path-exact succession, the
no-`.`-artifact rule, the merge closure bound in part 4, and reviewer
independence are untouched.
