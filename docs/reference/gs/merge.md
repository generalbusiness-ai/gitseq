---
title: gs merge
summary: Merge only the exact head named by a live, ratified approval.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7bf4086034820826093f3e5b88f6076df77f2856
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f77c88ea142f5cb81dfda4d344279bb2c870a2f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f97dca2d5321a4abbf2ea61450ce40d43867579
---

# `gs merge`

Merges one approved commit into the checkout, after checking that the
approval really covers that commit and still stands.

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
records:

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

## Afterwards

Two things are still yours to do, in this order:

1. Record the **merge artifact**, and supersede the previous artifact for
   the same path as part of the same step. That supersession is what
   makes documents describing the old implementation flare.
2. Only then may the original requester ratify the implementation report.

The automatic receipt is not the implementation's merge artifact. You
must still record that artifact and its succession as described above.
Supply the required plain-language merge commit message with `--text`;
the receipt trailers are appended to it.

## See also

- [`gs review`](review.md), [`gs supersede`](supersede.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
