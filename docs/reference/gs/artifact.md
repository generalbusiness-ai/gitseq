---
title: gs artifact
summary: Publish one artifact per changed path at an exact head, with the reporting artifact last.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs artifact`

Publishes the pointers one head owes: an artifact for every path it
changed, each resting on exactly one promise — yours — and the reporting
artifact published last.

Three facts make this a command rather than a loop over
[`gs state`](state.md): an artifact resting on two promises closes neither,
the reporting artifact is whichever artifact on the promise is *newest* so
publish order decides which one a verdict may name, and a path the head did
not change is a wire to nowhere — staleness travels along paths, so an
artifact at an untouched path can never flare.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor publishing the artifacts. |
| `--head` | *(required)* | The exact full commit every artifact stands at. An abbreviation is refused. |
| `--promise` | *(required)* | Your own live promise these artifacts report. |
| `--branch` | *(the branch pointing at the head)* | The branch named in each artifact's text. |
| `--report` | *(the first path)* | Which path carries the reporting artifact. It is published last. |
| `--text` | | Extra text for the reporting artifact, such as the tests and conditions actually met. |
| `--rests-on` | | An extra basis for every artifact, repeatable: the behaviour a documentation page describes, or the decision the work adopts. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

Paths are positional and come after the flags:
`gs artifact [flags] <path…>`. `--promise` and `--rests-on` accept
[short references](../event-identifiers.md#typing-one-at-a-boundary).

Each act carries a key derived from your fingerprint, the head and the
path, so an interrupted run replays what landed and continues from where
it stopped.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='CHANGELOG.md exists' \
  --body target_ref="refs/heads/$BASE")
PROMISE=$(gs promise --repo "$REPO" --as bot --branch request/changelog "$REQUEST")

git -C "$REPO" switch -q -c request/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
mkdir -p "$REPO/docs"
printf 'The changelog lists every release.\n' > "$REPO/docs/changelog.md"
git -C "$REPO" add CHANGELOG.md docs/changelog.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)

gs artifact --repo "$REPO" --as bot --head "$HEAD_COMMIT" --promise "$PROMISE" \
  --report CHANGELOG.md --text 'Tests: the file exists at this head.' \
  docs/changelog.md CHANGELOG.md
```

The last identifier printed is the reporting artifact: name it first when
you ask for review.

## What it checks before signing

| what is wrong | the refusal names |
|---|---|
| `--head` is abbreviated | that only a full object ID can take part in review or merge |
| `--head` is not a commit here | the repository to fetch it into |
| `--promise` is not a promise, or not yours | who signed it, and `gs promise` |
| `--promise` is retired | that a withdrawn promise closes nothing |
| `--promise` rests on no request | that it projects dangling, and `gs promise` |
| a path the head did not change | the `git diff` that lists what it did change |
| a path given twice, or a `--report` outside the set | the path |
| `--rests-on` names a promise | that an artifact resting on two promises closes neither |
| the request's target ref is not in this repository | the ref, and the `git fetch` that brings it |
| the head and the target share no ancestor | the ref to recut the work onto |

The change set is measured from the merge base of the head and the
request's target ref, so it is the whole of what this work adds to the
target, not only its last commit. When the target cannot be measured —
the ref is not in this repository, or it shares no ancestor with the head
— the command refuses rather than publishing unchecked: a note saying the
check was skipped, on a page that says the check happens, is the worst of
both. The one case that is only a note is a request that states no target
at all, such as a review: there is nothing to measure against. A directory path covers the files under
it, which is how an area-wide pointer such as `internal/statusview` is
maintained; every other comparison is an exact string, exactly as the fold
compares them.

A changed path no artifact names is a **warning**, not a refusal: a first
artifact elsewhere in the tree is legitimate and only the author knows
which. The warning lists the paths, so publishing each one, or being able
to say why not, is a decision rather than an oversight.

## What it produces

One `artifact` statement per path, in one batch, each carrying
`body.path` and `body.commit`, resting on the promise and on every
`--rests-on` given. The reporting artifact is last. Its text carries
`--text` as a second paragraph; the others do not.

An artifact is the implementation report for assigned work, so no separate
report follows it and the commitment moves to `awaiting-review`.

## See also

- [`gs promise`](promise.md), [`gs review-request`](review-request.md), [`gs batch`](batch.md)
- [Staleness](../../concepts/staleness.md)
