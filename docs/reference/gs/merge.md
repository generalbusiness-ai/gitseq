---
title: gs merge
summary: Merge only the exact head named by a live, ratified approval.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
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

1. Retire every live artifact covering what the merge changed, and
   publish a successor at the path each area keeps using. That
   supersession is what makes documents describing the old
   implementation flare.
2. Only then may the original requester ratify the implementation
   report. Self-initiated work has no report to ratify: the ratified
   approval authorized the merge, and the merge artifact closes it.

The automatic receipt is not the implementation's merge artifact. You
must still record that artifact and its succession as described above.
Supply the required plain-language merge commit message with `--text`;
the receipt trailers are appended to it.

### Choosing the path

Retiring and publishing are two decisions, not one. Retire everything
live that covers the change; publish one successor per area. Keeping
them apart is what makes the choice determinate.

Paths match as exact strings. The projection keys artifacts by the path
field alone — no normalising, no prefixes, no globs — so an artifact at
`internal/workroom` never reaches a predecessor at
`internal/workroom/fold.go`, and that predecessor, with everything
resting on it, stays silent for good. Reuse the string the area already
uses rather than a better one.

| Situation | What to do |
|---|---|
| One live path covers the change | Publish the successor at that exact string, then retire the predecessor citing the successor. |
| A directory and something inside it are both live over the same changed file | The wider path wins: publish at the directory, retire the narrower artifact citing the wider one as its successor, and never publish at the narrower string again. |
| No live artifact covers the change | A first artifact, with nothing to retire. Pick the granularity a reader would cite and keep it stable, because later merges must match it. |
| The merge renamed a file whose old path has a live artifact, and the new path has none | Publish at the new path first, then retire the old-path artifact citing it. |
| The merge renamed a file into a path that already has a live artifact | Not a first artifact. Publish the successor at the destination path, superseding the artifact already there, and retire the old-path artifact citing that same successor. Two predecessors, one survivor. |
| The merge deleted a file with a live artifact | The only bare supersession. Nothing replaced it, so name the merge commit and let the flare ask whoever rested on it to re-anchor. |

Name the successor whenever there is one. A supersession that cites its
replacement says *moved here*; a bare one says *gone*. Getting that wrong
does not fail — it leaves a reader following the chain at a dead end,
holding prose about a successor they cannot resolve. Capture the EventID
when you publish (`ARTIFACT=$(gs state …)`) and pass it as `--rests-on`
before the positional target; `gs supersede` puts the target first in the
basis itself, so the flag carries the successor alone.

[`gs supersede`](supersede.md) is admitted only from the artifact's own
author or an actor holding `ratifier`. Ask that actor when the artifact
you must retire is not yours; never sign as them.

One path per artifact. A comma-joined string such as
`AGENTS.md,SKILL.md` is one path that no real predecessor or successor
can equal, so it flares nothing and nothing flares it. Record two
artifacts.

### Never publish at `.`

A path every merge rewrites is a global mutex. Everything anchored to it
flares whenever anyone merges anything, however unrelated, and a flare
carrying no information teaches people to ignore the flares that do.

Nothing needs to replace it. Which commit `main` carries is a question
for `git rev-parse main`; per area it is the live artifact at that path,
which already names the merge commit that last changed it.

## See also

- [`gs review`](review.md), [`gs supersede`](supersede.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
