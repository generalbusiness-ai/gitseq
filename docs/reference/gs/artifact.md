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
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a resident that is cold, loaded, or folding a large log; a value that is not a positive duration is refused before anything is signed. |

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
| `--rests-on` names an artifact this run retires | the path and head of that pointer, and that a citation withdrawn in the same batch describes a superseded world from birth |
| this head's artifact for a path was published before and retired since | the retired event, the head the live artifact stands at, and that a replay cannot revive a withdrawn pointer |
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

## Republishing a recut

A repair round and a recut onto a moved target both leave the promise
carrying artifacts at two heads. Publishing the new head retires what the
promise carried at the earlier one, in the same signed batch. The reach is
every live artifact of yours that rests on this promise and stands at
another commit, and each of them falls into one of three cases.

| the earlier artifact's path | what happens |
|---|---|
| named in this run | retired, succeeded by the artifact this run publishes at that same path |
| not named, but this head still changes it | **left live**, with a warning: publish that path here too |
| this head no longer changes it | retired **bare**, with no successor |

The middle case is why a partial republish is safe. `gs artifact` publishes
the paths it is given, and those need not be everything the head changes;
retiring such a pointer bare would condemn behaviour that is alive at the
new head, and nothing this run publishes covers it. A request that states
no target ref cannot be measured against, so every unnamed path falls into
that case as well.

A bare retirement condemns whatever rested on the pointer. That is the
honest answer for a path the head under review no longer touches: naming
an unrelated artifact as the successor would say the behaviour moved
somewhere it did not.

The plan is disclosed on standard error before anything is signed, which
is where a mistyped `--promise` shows itself as a list of paths you do not
recognise:

```text
gs: will retire docs/changelog.md at 1f0c9ab1…3d4e5f6 -> bare
gs: will retire CHANGELOG.md at 1f0c9ab1…3d4e5f6 -> CHANGELOG.md
```

and each retirement that landed is printed on a line of its own, after the
identifiers:

```text
retired docs/changelog.md at 1f0c9ab1…3d4e5f6 -> bare
retired CHANGELOG.md at 1f0c9ab1…3d4e5f6 -> git:sha1…9c1d2e3
```

Another actor's artifact, an artifact standing at the head being
published, and an artifact resting on a different promise are all left
where they are. Every retirement here is of the signer's own artifact,
which the fold admits on its own standing, so nothing in this is a new
authority, and the whole chain is judged by the fold before the signing
key is read.

One live head per promise is what [`gs review-request`](review-request.md)
needs, and it leaves [`gs merge`](merge.md) nothing from an earlier round
to seal as a sibling or abandoned — a cleanup obligation the merge cannot
discharge for you.

A retirement that documentation still cites is **skipped**, with a warning
naming the page and the pointer left live; the publication itself is never
refused over one. The citation guard asks you to repoint the page at the
successor first, and the successor is the artifact this very run
publishes, so refusing would be a trap. Running the publication again does
not clear it either — the page still cites the pointer — so the repair is
yours: repoint the page at the artifact for that path at this head, then
`gs supersede <artifact> --rests-on <its successor here> --text <why>`.
Where this head no longer changes the path there is no successor to
repoint at, and that one takes
[`gs supersede <artifact> --cited-ok`](supersede.md). Until either is
done the promise still carries two heads and `gs review-request` says so.

## Republishing a head whose artifact was retired

The key is the actor, the head and the path, so a rerun replays what the
first run filed. That is what makes an interrupted publication resumable,
and it is also the one case where a replay is wrong: once the earlier
event has been **retired**, publishing that head again would hand back a
withdrawn pointer, report it as the artifact standing there, and rest the
retirement it carries on it — withdrawing the live artifact at the current
head in favour of a dead one.

That publication is refused, before anything is built. The refusal names
the retired event and the head this promise's live artifact stands at.
Publish the head the work is at now, or run that head's publication again;
a retired event cannot be brought back, and nothing here tries. A key that
names nothing, or one that names a live event, is unaffected: a first
publication and an ordinary retry both go on as before.

## What it produces

One `artifact` statement per path, in one batch, each carrying
`body.path` and `body.commit`, resting on the promise and on every
`--rests-on` given. The reporting artifact is last. Its text carries
`--text` as a second paragraph; the others do not. One supersession
follows per earlier-head artifact retired, resting on the artifact at the
same path where this run publishes one.

The identifiers printed on their own lines are the artifacts alone, so the
last of them is the reporting artifact whether or not this run retired
anything.

An artifact is the implementation report for assigned work, so no separate
report follows it and the commitment moves to `awaiting-review`.

## See also

- [`gs promise`](promise.md), [`gs review-request`](review-request.md), [`gs batch`](batch.md)
- [Staleness](../../concepts/staleness.md)
