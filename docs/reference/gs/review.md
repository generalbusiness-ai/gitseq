---
title: gs review
summary: Check the exact artifact checkout, then sign a review verdict against it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b78567c9fdc10c79087ae72099fb8397715fb1a8
---

# `gs review`

Signs a review report that names one immutable commit, after checking
that the reviewer really was looking at it.

This is the enforced verdict boundary. A review of "the branch" is a
review of nothing in particular, because the branch can move afterwards.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The reviewing actor. |
| `--checkout` | *(required)* | The working tree the reviewer examined. |
| `--artifact` | *(required)* | An artifact standing at the reviewed head. Repeat it to sign the whole set you read: the first is the artifact the verdict names, and every citation bounds what a later [`gs merge`](merge.md) receipt may retire. Each must be live and stand at the same head. |
| `--promise` | *(required)* | The reviewer's own promise to review. |
| `--verdict` | *(required)* | `approved` or `changes-requested`. |
| `--text` | *(required)* | The review itself. |
| `--ack-head-news` | | An event identifier, repeatable. Durable statements sequenced after the review request that name this head or lane are head news: the command refuses until you acknowledge exactly that set, once each, and every acknowledged event becomes a citation of the verdict. News the verdict already cites counts once and needs no separate flag. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |

It takes no positional arguments.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
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
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" --rests-on "$PROMISE")
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review at the exact head' --body to=@carol \
  --body conditions='confirm the named head' --rests-on "$ARTIFACT")
REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")

gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED; the changelog exists at this head'
```

## What it checks before signing

Durable checks:

- the named **artifact** is effective and not retired;
- the named **promise** is effective, not retired, and owned by the
  reviewer;
- the promise rests on exactly one standing `request`, which is copied
  from the graph rather than retyped;
- the reviewer did not sign the artifact under review. Independence is
  compared by fingerprint, so a self-signed verdict is refused here
  rather than left for [`gs merge`](merge.md) to catch.

Local checks on `--checkout`:

- it belongs to the same repository as the workroom;
- it is clean, including no untracked files;
- its `HEAD` is the artifact's full commit ID.

Every one of those is re-read immediately before signing, and the command
aborts if anything moved in between. The verdict names the immutable
commit, so a later checkout movement cannot retarget it.

A linked worktree is a fine checkout: gitseq state belongs to the common
directory, and the selected worktree stays an ordinary git context.

## Staleness does not stop a review

Retired and stale are different facts. Retired means this act was
superseded; stale means something underneath it was. A stale artifact
still names the commit it always named, and whether the movement matters
to *that commit* is exactly the reviewer's question. Refusing would leave
it permanently unanswered by the only party positioned to answer it.

So `review` goes ahead and records what had moved. The verdict body then
carries `stale=true` and a `staleness` line naming which of the artifact,
promise and request are stale, whether the movement was in the world they
describe, and the retired bases that caused it — up to four of them, with
a count of the rest, because a verdict is a message and
[`gs provenance`](provenance.md) is the projection.

The signed report therefore says plainly that the world had moved and the
reviewer signed anyway.

## What it produces

A `report` resting on the promise, the request, and the artifact, with
`body.verdict`, `body.head` and `body.artifact`, plus `body.stale` and
`body.staleness` when something underneath had moved. Naming the
artifact is what lets the projection say who implemented the head, so an
approval written any other way can leave independence unresolved and
unmergeable. The review requester ratifies it; then, for an approval,
[`gs merge`](merge.md) can use it.

## What it does not replace

Running the tests, building the binary, reading the diff, poking at git
plumbing — all of that is the reviewer's evidence and none of it is
automated here. `gs review` guards the **state at which the verdict is
signed**, not the judgement.

## After a change to the head

Any change to the head invalidates an approval. The implementer records a
**new** artifact at the new head and asks for review again; the old
approval describes a commit nobody is proposing any more.

## Signing more than one artifact

A head usually changes several maintained paths, and each keeps its own
artifact. Citing only one of them approves the head but leaves the rest of the
succession unauthorized, so the merge that lands it can retire the predecessor
in one tree and not the others.

Repeat `--artifact` for every artifact you actually read. The set travels as the
report's own bases, which is what lets `gs merge` treat it as reviewed: a set
the implementer assembles proves nothing, because the implementer is the party
asking for the authority. What you cite is what a receipt for this head may
reach.

## See also

- [`gs merge`](merge.md), [`gs ratify`](ratify.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
