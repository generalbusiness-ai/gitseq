---
title: gs review-request
summary: Ask another actor to review one exact head, resting on every artifact standing at it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs review-request`

Files the request that asks a different actor to review one exact head.

It writes the shape that [`gs review`](review.md) judges later. The
request rests on every live artifact of yours standing at that head — not
on the promise — and names the reporting artifact, the newest artifact
*resting on the promise*, in `body.artifact`,
because the verdict resolves its lane through that field. A review request
written any other way produces a verdict bound to nothing and a merge that
cannot use it.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor asking for the review. |
| `--head` | *(required)* | The exact full commit to review. |
| `--to` | *(required)* | The reviewing actor: a roster name, `@name`, or fingerprint. It must name a live roster actor, and it may not name you. |
| `--text` | *(lists the artifacts)* | The request text. The default lists every artifact at the head, marks the reporting one, and names the promise and the governing request. |
| `--conditions` | *(exact-head review)* | The conditions of satisfaction. The default asks for an independent exact-head review with explicit Architecture, Security and Simplification conclusions, filed with `gs review` naming the reporting artifact first and resting on the whole set. |
| `--replace` | `false` | Supersede your live review request for this promise in the same run, resting the supersession on the new request. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses any value other than a positive duration before it signs anything. |

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
  --text 'Add a changelog' --body to=@bot --body conditions='CHANGELOG.md exists' \
  --body target_ref="refs/heads/$BASE")
PROMISE=$(gs promise --repo "$REPO" --as bot "$REQUEST")

git -C "$REPO" switch -q -c request/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" add CHANGELOG.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
gs artifact --repo "$REPO" --as bot --head "$HEAD_COMMIT" --promise "$PROMISE" CHANGELOG.md >/dev/null

gs review-request --repo "$REPO" --as bot --head "$HEAD_COMMIT" --to @carol
```

## What it checks before signing

| what went wrong | the refusal names |
|---|---|
| no live artifact of yours stands at the head | `gs artifact`, to publish the head first |
| the artifacts rest on two of your promises | both promises, and that one request answers one commitment |
| the promise also carries live artifacts at another head | those artifacts and their heads, `gs artifact` for every path this head still changes, and `gs supersede` — with `--rests-on` its successor here, or `--cited-ok` where the head dropped the path — for a cited pointer publishing skips |
| you already have a live review request for this promise | that request, and `--replace` |
| `--to` names you | that a review comes from a different actor |
| `--to` names nobody on the roster | the live roster actors |

The mixed-head refusal saves the most work: `gs review` refuses such a set
at *signing*, after the reviewer has read everything.

An ordinary recut no longer reaches it.
[`gs artifact`](artifact.md#republishing-a-recut) retires the promise's
earlier-head artifacts when it publishes the new head, so publish and then
ask, with no supersession of your own in between.

A pointer that publication left alone still reaches this refusal, and the
refusal names each repair in turn.

Publication deliberately leaves live a path this head still changes that the
last run did not name. Publish it: name every path the refusal lists that this
head still changes.

Publication **skips** a pointer a documentation page still cites, with a
warning — it never refuses over one, and publishing again will skip it
again, whether or not this head publishes that path. Repoint the page at
the artifact for that path at this head, then retire the pointer with
`gs supersede <artifact> --rests-on <its successor here> --text <why>`.
Where this head no longer changes that path, no successor exists to
repoint at, and that one takes
[`gs supersede <artifact> --cited-ok`](supersede.md).

## Refiling cancels work in flight

A second review request for a lane does not add to the first. Staleness makes
no difference to this: a basis moving under the first request retires
nothing, so the reviewer's promise stays live on it. The reviewer's promise
rests on the first request, so refiling without retiring it leaves two open
rows, and a verdict filed against the retired one binds to nothing.

`--replace` does it in one run: it files the new request first, then
supersedes the old one with an act resting on the new request, so a reader
can follow where the review went. The supersession says plainly that it
releases any review promised on the old request.

Without `--replace` the command refuses a second request, and the refusal
names the request already live.

## What it produces

A `request` addressed to the reviewer, resting on every live artifact at
the head, carrying `body.artifact` (the reporting artifact),
`body.head`, `body.branch` when a single branch points at the head,
`body.no_git_artifact=true`, and the conditions. The command prints the
identifiers it appended on standard output, the request first.

A **stale** governing request draws a warning, not a refusal: the warning
names its author, and asking for the review remains the right next act.

## See also

- [`gs artifact`](artifact.md), [`gs review`](review.md), [`gs land`](land.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
