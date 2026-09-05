---
date: 2026-09-05
author: planner
status: Review note for Hugh; recommendation, no contract change
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3bf2e77ae61216358c2f6b028c92b096d6c52abc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:65cdaadb3b011262b9b09b672ef4477eaf8d630d
---

# Keep deferred obligations discoverable

Keep filing executable assignments when their prerequisites have landed, as
the [delivery-efficiency trial](2026-09-04-workroom-delivery-efficiency-review.md)
recommends. Pair that practice with one short check at each stage handoff:
read the adopted plan's remaining obligations, including its prose, and
identify what is delivered, assigned, ready to assign, or waiting on a named
prerequisite. These are planning descriptions, not new lifecycle states.

Two observations from 5 September show why the second half matters:

| Obligation | What the task board missed | Disposition at this review |
| --- | --- | --- |
| Inventory recovery sweep | Section 2.11 of the adopted shared-core plan says to move the recovery note and tests. No native assignment covered them. The I9 reviewer found the omission before approving removal. | Inventory #401 assigns the migration; #402 promises it. Gitseq coordination remains open. The source survives in Git history, and the landed architecture names the outstanding work and its process-death limits. |
| Shared-core input contracts, I5 | The nested-module prerequisite was delivered, but Tailapp's complete history through depth 3446 contained no I5 assignment. An empty inbox therefore did not mean the adopted plan was finished. | Tailapp request `e919a5d7` now commissions the bounded design under native authority, with implementation following its reviewed delivery. |

The first is an omitted assignment, not evidence that Inventory's completed
tasks were wrongly closed. The second is ready follow-on work, not a missed
promise. Keep those distinctions when assessing delivery quality. Neither
finding establishes how long an earlier check would have saved.

The smallest practice is to keep the next outcome, intended owner and actual
prerequisite visible in the existing plan. At handoff, check the outcome against
delivery evidence and commission work that is ready. If a missing obligation
is found, assign it durably; an explanatory assertion alone does not assign it.
Do not manufacture early requests merely to populate the board, add a second
task database, or require another approval for routine coordination. Record a
new durable explanation only when the check reveals a gap or changes scope.

For the existing 20-delivery trial, record these coverage findings alongside
review defects and request refiles. Check whether fewer early assignments
reduce refiling while preserving the full adopted outcome. Do not count a
shorter inbox by itself as an improvement.

The evidence and exact cross-room request IDs are preserved in Gitseq
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:65cdaadb3b011262b9b09b672ef4477eaf8d630d`.
It also records the remaining I6/I7/I10 prerequisites. This note recommends a
planning practice; it does not adopt a new instruction or authorize a feature.
