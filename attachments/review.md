# Recover the adopted staleness plan

Hugh, the useful next change is to keep a task's lifecycle and responsible actor visible when its reasoning becomes stale. This was already adopted in #14703. It should be recovered as unfinished work, with a current design check, rather than proposed again as a new idea.

The [historical plan](historical-plan.md) at ec80e044 was proposed by Planner in #14703 and ratified by Hugh through 0027017c. Current inspection still shows the proposal effective and ratified. In the complete history through #19553, its only direct descendant is that ratification; the candidate's only direct descendant is the proposal. I found no later assignment naming that plan. The note is absent from current main. Its source evidence now describes a superseded world, so its old measurements and bundled delivery sequence cannot simply be reused.

There is a current consequence. At Gitseq frontier #19653, Claude's accepted promise #19653 on review-binding request #19651 has `waiting_on: claude`, but the actual work query for Claude places it in `not_actionable` with `status: stale`. The request remains live and Claude explicitly confirmed its current conditions. A fresh promised Tailapp task appears in `waiting_on_you` in the control. These were read-only queries, not acts signed as either performer. [Exact queries and results](evidence.json) are attached.

The [fold](internal/workroom/fold.go@80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74:3527) overwrites `promised` with `stale`. The [lane classifier](internal/statusview/query.go@2348ebe66e226a889f08a1b90ccc3d6a45f437e4:221) rejects that status before considering the waiting actor. Unclaimed stale requests are different: [intake deliberately includes them](internal/statusview/actor.go@2348ebe66e226a889f08a1b90ccc3d6a45f437e4:316). This is a demonstrated claimed-work gap, not a claim that all stale work is invisible. Both classifier files match current main and the running reader's source; the cited fold matches current main.

The old plan needs a partial recovery:

| Adopted part | Current disposition |
|---|---|
| P1: stop later causes at a published receipt successor | Still contrary to the [current receipt rule](internal/workroom/fold.go@80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74:2817). Reconcile later decisions and security implications explicitly; do not reverse this rule as a side effect of a display fix. |
| P2: stale is a flag beside lifecycle | Not delivered in the current fold. Recover this first, including attention lanes, filters, browser counts and compatibility behavior. |
| P3 admission: allow stale bases with recorded evidence | Delivered in main 3f75695c and the admission-owned testimony repair. Preserve it. |
| P3 instructions: recheck unchanged stale work instead of compulsory refiling | The [current instructions](SKILL.md@80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74:235) still require author replacement. Reconcile this with the old adoption and later authority rules before editing policy. |
| P4: drain existing orphans without a new closure rule | Later drain and landing work exists; #14687 is satisfied. Preserve completed dispositions and examine only evidenced residual obligations. |
| P5: keep same-outcome repairs on one commitment | Delivered through the later repair-continuity adoption #17682 and main ce18ec874. Do not redo it. |

My earlier note #18836 described stale-assignment continuity as a new recommendation. This discovery corrects that account: much of its intended direction was already adopted in #14703, although the decision was never delivered as a current plan. #18959 remains useful evidence of the refiling loop.

I recommend one small recovery design: account for all five parts, identify any later superseding decisions, and give the remaining work explicit owners and gates. Prioritize lifecycle/attention continuity; keep receipt propagation and adoption authority explicit. Retain stale evidence, retired-input refusals, independent exact-head review and sealed delivery. Do not add another status or per-task ceremony. A current adopted and independently reviewed decision should govern subsequent implementation. This note records the finding; it changes no policy or runtime behavior.
