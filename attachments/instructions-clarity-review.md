# Make the ordinary work loop visible

Review of `AGENTS.md` and `SKILL.md` at delivered main
`2348ebe66e226a889f08a1b90ccc3d6a45f437e4`, for request #18384.
Both file blobs are unchanged from the request's `ce18ec8742ddd8665afa284a9cde1ce946213435`
anchors. Their inherited reasoning staleness does not make this fresh review
commission obsolete; Planner explicitly reconfirmed its scope and performer.

The rules usually distinguish the cases correctly. Their arrangement makes
exceptions look like extra steps in every task. Keep AGENTS as the short work
sequence, and SKILL as the authoritative explanation of exceptions. Four edits
would make the largest difference without adding a workflow.

## 1. Keep authorization conditional

The authorization paragraph opens with a held request, but ends with “new work
should carry the structured guard.” Reading the last sentence alone expands a
conditional requirement into a universal approval round. The extra authorization
requested on the unheld continuity change is a concrete example of this failure.

Add an explicit heading immediately above the paragraph:

> **Only when the implementation request holds the merge**

Replace its last sentence with:

> For a held request, use the structured authorization guard. The documented
> compatibility warning applies only to the older lanes it names. An unheld
> request follows the ordinary review, approval ratification and merge sequence;
> this paragraph adds no authorization step to it.

Keep the bindings, authorized signer, requester ratification and merge command
in this conditional section. Do not duplicate them in the ordinary sequence.

**Pending dependency:** I3 request #18325 (`27970d2a…`) is still promised in the
reviewed workroom snapshot. It changes the current signer sentence and completes
target-bound authorization. Apply this editorial change to I3's delivered text,
not by preserving the old requester/planner/ratifier fallback for modern held
work. I3's adopted rule is the actual hold owner, including an owner without
`ratifier`; its legacy compatibility remains explicitly scoped. This report does
not claim that I3 has landed or implement its policy.

## 2. Give AGENTS one short work sequence

The current first step contains intake, refusal, staleness, corrections, transfer,
self-initiated authority and an observability tradeoff. Step 5 contains most of
the succession specification. Neither is a step a reader can quickly check.

Use the following as the replacement sequence. Move the current exception text
to the indicated SKILL homes before removing its duplicate; do not discard it.

> 1. Read `SKILL.md` and current `status`. Answer every request addressed to you
>    each cycle: promise the work, decline it, or explain durably why it is not
>    actionable. For already completed work, follow the direct-report rule in
>    [Intake](SKILL.md#intake). For work you initiate yourself, follow
>    [Self-initiated work](SKILL.md#self-initiated-work).
> 2. Implement in a new `request/<slug>` branch and worktree, unless the request
>    specifies another prefix. Never develop or commit on `main`. Every
>    implementing commit carries `Rests-On:` naming its governing assigned
>    request or adopted decision.
> 3. Publish an artifact for the exact implementation head and state the tests
>    and conditions actually met. For assigned work it rests on your promise,
>    or on the request if there is no promise, and serves as the implementation
>    report. Do not add a duplicate ready-for-review report. Descriptive
>    documentation also rests on the behavior artifacts it describes.
> 4. Obtain independent review of that exact head, artifact and governing bases.
>    The reviewer promises the review and files the verdict with `gs review`;
>    a manual verdict must rest on its named artifact. The review requester
>    ratifies the verdict. Record all three [review conclusions](#review-conclusions).
>    A correction or changed head returns to step 3; follow
>    [Corrections and transfer](SKILL.md#corrections-and-transfer).
> 5. Merge only the exact approved head after approval ratification. Follow
>    [Held merges](SKILL.md#held-merges) when the request holds the merge.
>    Use the group's current target and perform publication, retirement and
>    candidate accounting in the same sealed merge, under
>    [Artifact succession](SKILL.md#artifact-succession). Write a concise
>    plain-English merge message describing the change and its impact.
> 6. Verify delivery, push changes to `main` to `origin`, and delete the merged
>    worktree and its branch. Finish the reviews and cleanup for work you take on.

The linked headings above are **proposed headings**, not claims that those
anchors already exist. The editorial patch must create each destination and
check every link. Keep the existing Architecture, Security and Simplification
requirements intact under `Review conclusions`, including the four authority
facts and same-head architecture contract update. Keep separate closure rules
in SKILL: the sealed receipt closes assigned implementation without another
report or implementation ratification; a non-merge report needs requester
ratification. Self-initiated work has no implementation commitment to close.

## 3. Keep each detailed rule in one home

Make the existing paragraphs navigable; avoid a new summary that leaves all old
summaries in place. The proposed destinations and preservation checks are:

| Rule | Authoritative home and content to retain |
|---|---|
| Intake | Promote existing SKILL Intake to a heading. Retain addressee-only direct reporting, no direct report over one's live promise, durable refusal, retirement authority and stale-request handling. |
| Corrections and transfer | Add a heading to the existing paragraph after authorization. Retain all unchanged-outcome tests, child triggers, actual responsibility transfer and the exact guarded superseded-parent conditions. |
| Self-initiated work | Add a heading to the existing self-initiated paragraph. Link to Notes and decisions for adoption, all four authority facts, timing, reviewer confirmation and the exact commit/artifact/review bases. Preserve the lack of an in-flight commitment row. |
| Artifact succession | Give discipline 10 a heading. Move any unique receipt details from discipline 8 here, including paired changed-path/left-live accounting and first-artifact handling. Retain exact strings, added/modified/renamed/deleted cases, covering directories, carried/sibling/abandoned classification, stale-but-live predecessors, author/ratifier cleanup authority, and the ban on dot or comma-joined paths. Link to the merge refusal table and dot-artifact migration. |
| Staleness | Keep discipline 11 and the dated superseded-world rules linked from succession. Preserve ordinary reasoning staleness, artifact-only propagation, timing relative to verdict, unknown-date refusal and publishing on current behavior bases after refusal. |
| Target and completion | Keep target inheritance and separate onward promotion under The repo underneath. Keep closure in the work loop; combine duplicate merged-worktree cleanup instructions in AGENTS step 6. |

Attribution, canonical full IDs, ephemeral secrecy, idempotency, ineffective-act
visibility and leased activity keep their existing SKILL homes. Keep the AGENTS
plain-English guidance and project principles. These edits shorten repeated
explanations, not the authority or evidence required.

## 4. Scope “drafts” to the governed kind

Discipline 3 currently says every statement gains force only when ratified.
That conflicts with the tool guidance that kind definitions govern satisfaction:
requests and promises can already be effective, and artifacts have no satisfier.

Replace discipline 3 with:

> **Use the kind's declared satisfaction rule.** Read the governed vocabulary
> before filing. Ratify only when that definition requires it and names you as
> satisfier. A proposal awaiting adoption is a draft; do not assume every kind
> needs ratification. Expect dissent, and inspect the projected verdict.

This states the existing vocabulary contract; it does not grant a new signer or
remove review approval ratification.

## Four walkthroughs

| Case | Current reading hazard | Actions after the edit |
|---|---|---|
| Ordinary assigned change | The last authorization sentence can look universal. | Answer request; own worktree; request-based commit; exact artifact; independent promised review; requester ratifies approval; sealed target merge; push and cleanup. No separate authorization round when unheld. |
| Same-outcome correction | Child-request and transfer details obscure the now-adopted continuity rule. | Keep the existing implementation promise, cite the finding, correct and publish the new head, get fresh independent review and ratify it, then merge and clean up. Create a child only for the stated change of scope, conditions, authority or performer. |
| Self-initiated work | “Use requests to track all tasks” can imply a self-request before the later exception. | Establish the adopted decision and authority basis first; commit on those bases in its own worktree; publish artifact; obtain independent review and ratification; merge and clean up. No self-request, self-promise or self-report. |
| Genuinely held merge | The signer exception and universal-looking guard can be confused with ordinary review. | Complete exact-head review and its ratification; obtain the properly bound release from the hold owner under delivered I3 and its scoped compatibility; ratify that release as required; re-resolve target and merge with that exact authorization. A held request stays held until its applicable guard is satisfied. |

## Editorial boundary and acceptance

One issue needs a **separate policy decision**, not an editorial shortcut:
mandatory author refiling of stale requests can conflict with the adopted
instruction to retain a live unchanged commitment. The current workroom resolves
specific cases through explicit current commissioning and viability evidence.
Do not silently replace the written refiling rule with “all stale work may
continue,” or require withdrawal of every live promise. Planner should resolve
that policy boundary through its normal adoption process if a general rule is
wanted. This review proposes no such policy change.

The first editorial slice should be the held-only heading and closing sentence,
plus the corrected kind-satisfaction sentence, rebased on delivered I3. Its
acceptance is simple: the ordinary walkthrough creates no extra authorization,
the held walkthrough still checks all I3 bindings and signer rules, and neither
requests nor artifacts acquire an invented ratification step. Then shorten the
work sequence and consolidate duplicates with the preservation table as a
review checklist. Verify the four walkthroughs, every new link, and the absence
of any removed substantive requirement. No source implementation was made in
this review.
