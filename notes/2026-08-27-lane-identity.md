---
title: Lane identity — associating work items with worktrees and branches
date: 2026-08-27
status: proposed
refines: proposal c781f3f5 items 5 and 6 (2026-08-27 board-trust audit remediation)
---

# Lane identity — associating work items with worktrees and branches

## Problem

The association between a durable work item (a request and its commitment)
and the git state that carries its implementation (a branch, a worktree, a
head) is prose. Request texts name branches informally; `body.branch` and
`body.head` are advisory and unvalidated; worktree paths appear nowhere in
the log at all.

The 2026-08-27 worktree audit showed what that costs. 73 worktrees existed
against roughly 25 live commitments. Finding which lane owned
`request/public-host-boundary` required reading promise prose, because no
request body named it. Three worktrees held uncommitted work reachable from
no ref, with no board trace naming an owner. Recut chains left up to six
checkouts per lane family, and nothing at merge or retirement time could
name the checkout that had become removable, because nothing knew it
existed.

The root defect: the durable log depends on knowing git's mutable
namespace (branch names, worktree paths), instead of git carrying the
work-item identity in its own immutable content. The join must be
computable, not remembered.

## Decision

Anchor lane identity in content-addressed facts, derive the rest by
tooling. Four layers, strongest first.

### 1. The reverse index over `Rests-On:` trailers

Implementing commits already carry `Rests-On:` naming the governing
request event. The commit hash seals that trailer, so commit → work-item
is already durable and unforgeable. Build the missing direction: the
resident scans `git for-each-ref` (and `git worktree list`), parses the
trailer at each tip's lineage, and joins branch and worktree to the
commitment in the projection.

This makes "which lanes exist, which commitment governs each, which are
orphaned" a standing table, with no new convention required. Orphan
detection — a worktree whose branch has no non-settled commitment, or a
head reachable from no ref — becomes two queries instead of a forensic
audit.

### 2. Worktree identity stamped at creation

A `gs worktree <request-id>` verb creates the worktree and branch and
writes `git config --worktree gitseq.request=<full canonical event id>`.
Worktree identity is inherently local, so worktree-local config is its
correct home. This yields:

- Any `gs` command run inside the worktree knows its governing lane and
  refuses durable acts once that lane settles — enforcing the one-writer
  rule and preventing work on dead lanes.
- A commit hook appends the correct `Rests-On:` trailer automatically,
  removing the transcription errors already present in the log.
- Merge receipts and request retirements can *compute* the worktree that
  has become removable, closing the gap that leaves orphans when a lane
  ends without a merge.

### 3. Deterministic lane refs

Human-chosen `request/<slug>` names collide across recuts; that is how one
lane family accumulated six checkouts. Tooling maintains
`refs/gitseq/lanes/<request-event-hash>` pointing at the lane tip. The
ref names the lane exactly, in both directions, survives worktree
deletion, and gives every recut its own ref without anyone choosing a
name. `request/<slug>` remains as a human-friendly alias.

### 4. Validated body fields, for display only

`body.branch` is promoted from advisory prose to a validated field on
request and promise kinds, joined by `body.worktree`. These serve
rendering and search; layers 1–3 remain the source of truth, because
names drift and directories move.

## Consequences

- Merge (AGENTS.md step 6) and retirement both gain a computable answer
  to "which checkout is now removable", so cleanup can be prompted — or
  performed — at every lane ending, not only at merges.
- A periodic hygiene gate (worktrees on settled lanes; refless heads)
  becomes cheap enough to run every planner tick.
- Duplicate checkouts at one head become detectable at creation time and
  can be refused.
- Mutation-testing scratch worktrees are created and destroyed inside the
  test run; they never persist to hold a refless commit.

## Boundaries

Nothing here adds git concepts to the sequencing kernel: the fold and
vocabulary do not learn about branches or worktrees. Layers 1–3 live in
the resident, the `gs` CLI, and repo-local git metadata. Layer 4 touches
only kind field validation. Historical branch names do not change.
