---
title: External PR handoff workflow
summary: Complete reviewed work in Gitseq when a different system owns the later pull request and merge.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6a67c4cf343b464d25edac35a660b32ef5600cd9
---

# External PR handoff workflow

## Decision to make

Some repositories do not use Gitseq to land work on `main`. A task may target
an integration or release branch in a large repository, while a forge, release
team, or other external process owns the later pull request and merge.

Gitseq should finish when it has produced an accepted, reviewed, published
handoff. It should not claim that the pull request was opened, accepted, or
merged when those acts are outside its authority.

The recommended model is:

> Gitseq owns the immutable handoff from an exact target base to an exact
> candidate head. The external process owns everything after that handoff.

This is different from `gs merge`. A Gitseq merge changes a target checkout,
publishes path successors, and closes the implementation commitment with a
sealed receipt. An external handoff changes no target checkout and publishes
no source-path successors. It closes through an explicit report and requester
ratification.

## What works today

The current model already has most of the required pieces.

- A request can name any branch and any exact base in its conditions. Neither
  the fold nor `gs review` requires the candidate to target `main`.
- The implementing commit and review verdict name immutable commit object IDs,
  not mutable branch names.
- An implementation artifact may rest on a promise and receive independent
  review at its exact head.
- Work that does not use `gs merge` can close through an explicit `report`
  against the implementation promise. The requester ratifies that report.
  The fold deliberately gives the explicit report precedence over an
  unmerged artifact.
- `gs publish` can record what a remote already accepted without minting source
  artifacts, although its present contract is per watched path rather than a
  proof of one whole handoff branch.

Two current facts make this process awkward rather than clean.

First, every implementation artifact resting on the promise projects the
commitment as `awaiting-merge`. That is false presentation for a request whose
terminal action is a handoff. The later explicit report fixes the terminal
state, but the board is misleading while review is under way.

Second, the artifact path namespace has no target or delivery dimension. An
artifact for a candidate aimed at `release/next` looks like another candidate
for the same source path on `main`. While its commitment is unsettled, an
unrelated `gs merge` must protect it as a sibling. After settlement it becomes
an abandoned live candidate unless its author retires it. A large external-PR
change can therefore add substantial, conceptually false succession traffic.

These are projection and model gaps. They are not reasons to put the external
pull request inside Gitseq.

## Process available now

This compatibility process uses the current vocabulary. It is suitable while
the first-class handoff subject proposed below does not yet exist.

### 1. Request the delivery, not the eventual merge

The request says plainly that its delivery mode is external handoff and that
the later pull request and merge are out of scope. Record these advisory body
fields where the client permits them:

```text
delivery_mode = external-pr-handoff
target_ref = refs/heads/release/next
target_base = <full object ID>
delivery_remote = origin
delivery_ref = refs/heads/handoff/<request slug>
```

The conditions define the terminal result:

- the candidate is based on the named `target_base`;
- the full required test and review gates pass;
- the delivery remote has accepted the exact candidate head at the named
  delivery ref;
- an independent reviewer has approved that exact head against the recorded
  target base; and
- the implementer has filed an explicit handoff report for requester
  acceptance.

The mutable refs are labels and locations. The two full object IDs are the
authority.

### 2. Claim and branch from the exact base

The addressee promises the request, fetches the target repository, verifies
that `target_ref` currently resolves to `target_base`, and creates the request
branch and worktree at that exact commit. The implementing commits carry
`Rests-On:` naming the request.

If the target moved before work began, do not silently substitute the new
head. The requester either confirms a fresh base in a replacement request or
records a current-basis amendment through the normal durable process.

### 3. Refresh immediately before review

Before publishing the review subject, fetch `target_ref` again.

- If it still names `target_base`, continue.
- If it moved, integrate the new base by the repository's normal rebase or
  merge policy. The candidate head changes, so run the gates again and review
  the new exact head.
- Do not infer from a clean textual merge that the old review still applies.

This gives the reviewer one exact base-to-head change to examine. Target
movement after the verdict belongs to the external process unless the request
explicitly requires the target to stay pinned until acceptance.

### 4. Use bounded component artifacts

Publish implementation artifacts at the stable component paths the repository
already uses. A directory path may cover a component; one artifact per file is
not required when the established path wire is the component root. Never use
`.` or comma-joined pseudo-paths.

Request independent review of every artifact that covers the change. The
review request names the exact candidate head and target base. The reviewer
records Architecture, Security, and Simplification conclusions and verifies
that the diff they examined is `target_base...candidate_head`.

This step temporarily produces `awaiting-merge` and ordinary artifact
succession traffic. Those are known compatibility limitations, not the desired
long-term presentation.

### 5. Publish the candidate branch

Push the exact candidate head to the named delivery ref. Re-read the remote ref
and refuse the handoff if it does not equal the reviewed head. Use `gs publish`
for paths covered by the repository's existing publication policy; do not add
a repository-wide watch merely to make this workflow appear covered.

The handoff report records the remote and ref as hints and the full accepted
head as the fact. A URL may be included as a hint, but it is never a causal
basis.

### 6. Close by report and ratification

After the approval is ratified, the implementer files one explicit `report`
against the implementation promise. It also cites the reviewed artifacts, the
ratified approval, and any applicable publication facts. Its text states:

- target ref and exact reviewed target base;
- exact candidate head;
- remote delivery ref and the head observed there;
- tests and conditions met; and
- that pull-request creation and merge are outside Gitseq.

The requester verifies the handoff and ratifies this report. The implementation
commitment becomes `satisfied`; no merge receipt follows.

### 7. Retire temporary candidate artifacts

After report ratification, the artifact author bare-retires the external
candidate artifacts. They have no successor in Gitseq's target world. Their
retired records and the satisfied report preserve the audit trail, while the
live artifact map no longer presents them as main-line candidates.

Retiring before requester acceptance is wrong: it withdraws the only exact
implementation pointer while the commitment is still unsettled. Retiring
after acceptance may make the closed report ordinarily stale, but it remains
satisfied, which truthfully records that its supporting candidate has left the
live succession set.

## First-class supported process

The compatibility process should be replaced by one governed `handoff`
subject. This is the smallest model that makes the presentation and succession
truthful.

### Handoff record

Add a `handoff` kind with its own render class, not `artifact`. Require:

```text
mode = external-pr
head = <full candidate object ID>
target_ref = <full refs/heads/... name>
target_base = <full object ID>
remote = <configured remote name>
remote_ref = <full refs/heads/... delivery ref>
published_head = <same full object ID as head>
```

The authoring command validates the repository facts before signing:

- both object IDs are full lowercase IDs in the repository's object format;
- `target_base` is an ancestor of `head`;
- the remote delivery ref resolves to `head`;
- the target and delivery refs are full branch refs; and
- `published_head` equals `head`.

The event rests on the implementation promise. It names the whole immutable
base-to-head change and deliberately carries no artifact `path`. Git can
reconstruct the changed paths from the two commits, so a large repository does
not need a payload-sized path manifest. The command may record a bounded count
and a canonical digest of the sorted NUL-separated changed paths as a useful
cross-check, but those are not substitutes for the commits.

Because `handoff` is not an artifact:

- it never enters source-path succession;
- it cannot be mistaken for a main-line candidate;
- it creates no predecessor retirement debt; and
- it needs no cleanup act after acceptance.

### Review

Extend `gs review` to accept exactly one governed subject type: the current
artifact set or one handoff. A handoff verdict binds all of these values:

- handoff event;
- exact candidate head;
- exact target base;
- target ref as a label; and
- the review frontier and existing staleness evidence.

The reviewer examines `target_base...head`. If either commit is unavailable,
the target ref no longer has the required base before review, or the delivery
ref no longer has the head, review refuses. Any candidate-head change requires
a new handoff and a fresh verdict.

The existing independent-review and Architecture, Security, and
Simplification rules remain unchanged.

### Projection and closure

A live handoff on an implementation promise projects `awaiting-handoff`, not
`awaiting-merge`. After a ratified approval it projects `ready-for-external-pr`
until the explicit completion report arrives.

The explicit report remains the only closure. It cites the promise, handoff,
and ratified approval. Requester ratification changes the implementation
commitment to `satisfied`. No new merge-like authority and no automatic
acceptance command are needed.

The graph and table show the delivery mode and target ref, but derive no PR or
merge state from a forge URL.

## Boundaries and failure cases

### Target movement

The default rule is strict until review: refresh the target immediately before
the verdict and produce a new candidate head if it moved. After the verdict,
target movement is external-process state and does not rewrite the accepted
Gitseq handoff.

A future optional disjoint-path remeasurement could admit a moved target
without changing the candidate, using the same temporal and path-overlap
discipline as structured merge authorization. It is not part of the first
implementation; the strict rule is easier to explain and audit.

### Rebase or repair

A rebase, merge-from-target, amended commit, or repair changes the candidate
head. Publish a new handoff and obtain a new review. Retire or supersede the
old handoff according to the governed handoff rule; never edit it in place.

### External rejection or abandonment

Requester acceptance says that Gitseq delivered what it promised. A later PR
rejection does not reopen that historical commitment. Record the external
outcome as an assertion if it matters. If more work is required, file a child
request resting on the satisfied handoff report and the recorded external
finding.

An externally discovered defect follows the same rule. The external comment or
URL is evidence, not a causal basis. Promote the defect into a durable assert,
then assign the repair with a child request before changing code.

### External success

Gitseq need not observe the eventual merge. If the repository wants that
history, an adapter may record an app-validated assertion that an external
system reports the handoff head reachable from a named integration commit.
That assertion neither closes the already satisfied request nor publishes
source-path successors.

### Remote branch deletion

Keep the delivery ref until the external process reaches a terminal outcome.
The Gitseq log preserves event evidence but not Git blobs. If the external
process abandons the candidate and deletes its ref, the repository's retention
policy decides whether to keep a dedicated archival ref.

## Invariants

1. Mutable refs locate work; full object IDs identify what was delivered and
   reviewed.
2. Gitseq never claims an external PR or merge occurred merely because it
   produced a handoff.
3. One implementation head has one exact handoff and one exact review. A head
   change requires both again.
4. A target move before review requires a refreshed candidate. A target move
   after review belongs to the external process by default.
5. External handoffs do not enter artifact path succession and cannot create
   main-line predecessor retirement debt.
6. The exact base and head describe the whole change, so repository size does
   not force a path list into the signed event.
7. An explicit report and requester ratification close the implementation
   commitment. No merge receipt follows.
8. External rejection or defects create follow-up evidence and child requests;
   they do not rewrite a completed handoff.
9. Repository-root and comma-joined pseudo-path artifacts remain forbidden.
10. Review independence and Architecture, Security, and Simplification checks
    are identical for merge and handoff delivery.

## Implementation plan

1. Add the governed `handoff` kind, required fields, bounds, render class, and
   fold tests. Keep it out of the artifact map and staleness path wires.
2. Add `gs handoff` to validate the exact base, head, refs, remote publication,
   ancestry, and optional changed-path digest before signing.
3. Generalize the guarded review subject from an artifact set to either an
   artifact set or one handoff. Preserve exact-head, reviewer-independence,
   staleness, and head-news checks.
4. Project `awaiting-handoff` and `ready-for-external-pr`, including target ref,
   through status, work, inspect, CLI, MCP, and the table and graph views.
5. Document the compatibility process and first-class process in the agent
   skill and user workflow pages. Do not change the default main-line merge
   path.
6. Add end-to-end tests for target refresh, remote-ref mismatch, changed head,
   report/ratification closure, external rejection follow-up, and absence from
   artifact succession and merge left-live accounting.

The first implementation should stop there. Forge adapters, automatic PR
creation, merge observation, disjoint target remeasurement, and archival-ref
policy are separate follow-on decisions.
