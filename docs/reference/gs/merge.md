---
title: gs merge
summary: Merge only the exact head named by a live, ratified approval.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b7c2ffe5efdd779aff87fe8736adc64f92223b78
---

# `gs merge`

Merges one approved commit into the checkout, after checking that the
approval really covers that commit and still stands.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--checkout` | *(required)* | The working tree receiving the merge. |
| `--candidate` | *(required)* | The full, lowercase, approved commit object ID. |
| `--approval` | *(required)* | The ratified approval report event. |

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
gs merge --repo "$REPO" --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$APPROVAL"
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

## Afterwards

Two things are still yours to do, in this order:

1. Retire every live artifact covering what the merge changed, and
   publish a successor at the path each area keeps using. That
   supersession is what makes documents describing the old
   implementation flare.
2. Only then may the original requester ratify the implementation
   report. Self-initiated work has no report to ratify: the ratified
   approval authorized the merge, and the merge artifact closes it.

Write the merge commit message in plain language: what changed and what
it affects.

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
