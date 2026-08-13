---
title: gs merge
summary: Merge an approved exact head and publish its artifact succession.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# `gs merge`

Merges one approved commit into the checkout, after checking that the
approval really covers that commit and still stands. It then retires the live
artifact pointers the merge changed and publishes their successors as one
resumable batch.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor signing the durable merge receipt. |
| `--checkout` | *(required)* | The working tree receiving the merge. |
| `--candidate` | *(required)* | The full, lowercase, approved commit object ID. |
| `--approval` | *(required)* | The ratified approval report event. |
| `--text` | *(required)* | A plain-language description of the change and its impact. This begins the merge commit message. |
| `--server` | | Submit the durable merge receipt through a resident sequencer instead of writing locally. |

It takes no positional arguments.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name carol --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists')
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST")
git -C "$REPO" switch -q -c task/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" add CHANGELOG.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'Changelog implementation' \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" --rests-on "$REQUEST")
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review at the exact head' --body to=@carol \
  --body conditions='confirm the named head' --rests-on "$ARTIFACT")
REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")
APPROVAL=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED at this exact head')
gs ratify --repo "$REPO" --as bot "$APPROVAL" >/dev/null

git -C "$REPO" switch -q "$BASE"
gs merge --repo "$REPO" --as bot --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$APPROVAL" \
  --text 'Merge the approved changelog and make it available on main.'
```

It prints the resulting merge commit.

## What it refuses

| Situation | Why |
|---|---|
| The approval is ineffective, unratified, retired or stale | An approval that no longer stands approves nothing. |
| The approved artifact is retired or stale | Same, from the other side of the chain. |
| The verdict is not `approved` | `changes-requested` is not a merge authorization. |
| `--candidate` differs from the approved head | The reviewer looked at a different commit. |
| The approval does not rest on the artifact it names | The chain from verdict to code is broken. |
| The artifact's commit differs from `--candidate` | Same, from the other end. |
| The approval was already used or is reserved by another merge | One approval authorizes exactly one merge. |
| The candidate is already contained in the target | There is no new approved landing to record. |
| `--text` is blank | The immutable merge receipt also needs a useful merge description. |
| The checkout is dirty | The merge result would contain unreviewed work. |
| The checkout belongs to another repository | The workroom does not govern it. |
| `--candidate` is not a full lowercase object ID | An abbreviation could become ambiguous later. |

Every check runs twice, immediately before git is invoked.

`merge` keeps the strict reading of staleness that
[`gs review`](review.md) gives up. The latitude belongs where a reviewer
is present to exercise it, and nobody is present here. A refused merge
leaves the signed approval standing and asks only that the record be
brought up to date first.

## Why an object ID and not a branch

`gs merge` passes the approved full object ID to `git merge --no-ff`,
never a branch name. Advancing the reviewed branch after approval
therefore cannot retarget the merge — the commit that was approved is the
commit that lands.

## Approval scope and receipt

A ratified approval is repository-scoped and single-use. It authorizes
the exact candidate to land once in any one clean checkout belonging to
the same repository. The first successful use fixes the target's
pre-merge head. The approval cannot then be replayed into another branch
or linked worktree.

Concurrent callers first reserve
`refs/gitseq/merge-receipts/<approval-hash>` with an atomic compare-and-swap.
Only one caller can proceed. A successful merge leaves three matching
records, followed by the artifact succession authorized by that receipt:

- merge commit trailers naming `Gitseq-Approval`, `Gitseq-Candidate`, and
  `Gitseq-Target-Pre-Head`;
- the repository receipt ref, advanced from the target's pre-merge head
  to the merge head; and
- a signed workroom assertion naming the approval, candidate, target
  pre-head, and merge head.

Git receipts are checked across all refs. The signed workroom assertion
also prevents replay if local refs and the branch carrying the merge are
later lost. An interrupted reservation fails closed so it can be
inspected before any later merge is allowed.

The assertion and every successor and retirement use deterministic
idempotency keys. If submission stops part-way, run the same command again in
the checkout still at that merge head. It finds the immutable Git receipt and
resumes the missing suffix; it does not merge a second time or retire a
successor it already published.

## Artifact succession

The command reads the first-parent diff of the merge that actually lands. It
treats stale artifacts as live until they are retired, deduplicates work across
changed files, publishes all successors, and then retires every covered
predecessor in the same batch.

| Situation | Enforced result |
|---|---|
| One live path covers the change | One successor is published at that exact string and every live predecessor there is retired. |
| A directory and something inside it both cover one changed file | The wider directory wins. One successor is published there; every wider and narrower predecessor is retired. |
| No live artifact covers an added or modified file | A first artifact is published at the changed file path. |
| A file is renamed | Its exact old path is retired without a successor there. The destination receives a first artifact or the successor for the live path already covering it. |
| A file is deleted | Its exact old path is retired with no successor. A live covering directory still receives its successor because the directory changed. |

`workroom/state@1` refuses new artifacts at `.` and refuses comma-joined
pseudo-paths. Historical `state@0` artifacts keep their original decisions but
valid historical paths remain candidates for retirement and succession. New
raw submissions cannot use `state@0` to bypass the path rule.

### Citations across a merge

Documentation names the artifacts that vouch for the behaviour it describes, so
the pages cite exactly the pointers a merge has to retire. Refusing every cited
retirement would refuse every merge in a documented area, and the usual advice —
repoint the pages first — cannot be followed, because the successor does not
exist until the merge lands.

So the two cases are separated. A retirement this merge succeeds goes through:
the supersession names the successor artifact, the successor stands at the same
path or at a directory covering it, and a page naming the old pointer flares and
is told where to re-anchor. A retirement with no successor is refused before
`HEAD` moves, naming the pages, exactly as
[`gs supersede`](supersede.md) refuses one: nothing replaces the pointer, so the
pages would be left with nowhere to go. Retiring it anyway is a deliberate act
with `gs supersede --cited-ok` once the pages have moved.

The documentation gate reads the same distinction. A citation of a retired
artifact whose retirement names a covering successor is reported as a flare; a
citation of a retirement that names nothing still fails the set.

Before moving `HEAD`, `merge` runs this check on the tracked tree that will
receive the merge. Git prepares the merge with `--no-commit`, the command checks
the actual staged result, and a refusal aborts the tentative merge with the
target unchanged. Only then does it create the receipt commit and publish the
durable succession.

### Who may retire another actor's pointer

Free-standing [`gs supersede`](supersede.md) requires the target's author or an
actor holding `ratifier`. Merge succession is the one narrow exception, and the
fold checks all of it from the log alone:

- the supersession cites a merge receipt signed by the same actor;
- that receipt cites a ratified, effective approval whose verdict is `approved`
  and whose head is the merged candidate;
- the approval cites an implementation artifact standing at that candidate and
  written by someone other than the approver;
- the receipt is signed by the author of that implementation artifact — the
  merger of an approved head is the actor whose work it is; and
- the target carries a successor path in the receipt's signed plan, that path
  covers the target's own path, and the supersession cites the successor
  artifact published there.

A receipt therefore reaches exactly the tree its merge republished, and nothing
else. A retirement with no successor — a deleted path — takes no authority from
a merge at all and stays with the target's own author or a ratifier.

### Restart residents at the merged commit

This head advances the state schema to `workroom/state@1` and the fold profile
to `workroom-fold@3`. A binary built before it cannot interpret records written
after it. Restart every resident sequencer and MCP adapter at the merged commit.

## See also

- [`gs review`](review.md), [`gs supersede`](supersede.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
