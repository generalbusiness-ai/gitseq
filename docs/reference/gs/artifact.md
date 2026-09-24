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
the fold takes the *newest* artifact on the promise as the reporting artifact,
so publish order decides which one a verdict may name, and a path the head did
not change makes a wire to nowhere — staleness travels along paths, so an
artifact at an untouched path can never flare.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor publishing the artifacts. |
| `--head` | *(required)* | The exact full commit every artifact stands at. The command refuses an abbreviation. |
| `--promise` | *(required)* | Your own live promise these artifacts report. |
| `--branch` | *(the branch pointing at the head)* | The branch named in each artifact's text. |
| `--report` | *(the first path)* | Which path carries the reporting artifact. The command publishes it last. |
| `--text` | | Extra text for the reporting artifact, such as the tests and conditions actually met. |
| `--rests-on` | | An extra basis for every artifact, repeatable: the behaviour a documentation page describes, or the decision the work adopts. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses a value other than a positive duration before signing anything. |

Paths come as positional arguments after the flags:
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

The last identifier printed names the reporting artifact: name it first when
you ask for review.

## What it checks before signing

| what went wrong | the refusal names |
|---|---|
| `--head` gives an abbreviation | that only a full object ID can take part in review or merge |
| `--head` names no commit here | the repository to fetch it into |
| `--promise` names no promise, or not yours | who signed it, and `gs promise` |
| `--promise` names a retired promise | that a withdrawn promise closes nothing |
| `--promise` rests on no request | that it projects dangling, and `gs promise` |
| a path the head did not change | the `git diff` that lists what it did change |
| a path given twice, or a `--report` outside the set | the path |
| `--rests-on` names a promise | that an artifact resting on two promises closes neither |
| `--rests-on` names an artifact this run retires | the path and head of that pointer, and that a citation withdrawn in the same batch describes a superseded world from birth |
| someone published this head's artifact for a path before and has retired it since | the retired event, the head the live artifact stands at, and that a replay cannot revive a withdrawn pointer |
| this repository lacks the request's target ref | the ref, and the `git fetch` that brings it |
| the head and the target share no ancestor | the ref to recut the work onto |

The command measures the change set from the merge base of the head and
the request's target ref, so it covers the whole of what this work adds to
the target, not only its last commit. When the command cannot measure the
target — the ref does not exist in this repository, or it shares no
ancestor with the head — it refuses rather than publishing unchecked: a
note saying the command skipped the check, on a page that says the check
happens, gives the worst of both. Only one case produces just a note: a
request that states no target at all, such as a review, leaves nothing to
measure against. A directory path covers the files under
it, which lets you maintain an area-wide pointer such as
`internal/statusview`; every other comparison uses exact strings, exactly
as the fold compares them.

A changed path no artifact names produces a **warning**, not a refusal: a
first artifact elsewhere in the tree counts as legitimate and only the
author knows which. The warning lists the paths, so publishing each one, or
knowing why not, becomes a decision rather than an oversight.

## Republishing a recut

A repair round and a recut onto a moved target both leave the promise
carrying artifacts at two heads. Publishing the new head retires what the
promise carried at the earlier one, in the same signed batch. The reach covers
every live artifact of yours that rests on this promise and stands at
another commit, and each of them falls into one of three cases.

| the earlier artifact's path | what happens |
|---|---|
| named in this run | retired, succeeded by the artifact this run publishes at that same path |
| not named, but this head still changes it | **left live**, with a warning: publish that path here too |
| this head no longer changes it | retired **bare**, with no successor |

The middle case makes a partial republish safe. `gs artifact` publishes
the paths you give it, and those need not cover everything the head changes;
retiring such a pointer bare would condemn behaviour still alive at the
new head, and nothing this run publishes covers it. The command cannot
measure against a request that states no target ref, so every unnamed path
falls into that case as well.

A bare retirement condemns whatever rested on the pointer. That gives the
honest answer for a path the head under review no longer touches: naming
an unrelated artifact as the successor would say the behaviour moved
somewhere it did not.

The command discloses the plan on standard error before signing anything,
and there a mistyped `--promise` shows itself as a list of paths you do not
recognise:

```text
gs: will retire docs/changelog.md at 1f0c9ab1…3d4e5f6 -> bare
gs: will retire CHANGELOG.md at 1f0c9ab1…3d4e5f6 -> CHANGELOG.md
```

and prints each retirement that landed on a line of its own, after the
identifiers:

```text
retired docs/changelog.md at 1f0c9ab1…3d4e5f6 -> bare
retired CHANGELOG.md at 1f0c9ab1…3d4e5f6 -> git:sha1…9c1d2e3
```

The command leaves another actor's artifact, an artifact standing at the
head this run publishes, and an artifact resting on a different promise
where they stand. Every retirement here retires the signer's own artifact,
which the fold admits on its own standing, so nothing in this grants a new
authority, and the fold judges the whole chain before the command reads the
signing key.

[`gs review-request`](review-request.md) needs one live head per promise,
and that leaves [`gs merge`](merge.md) nothing from an earlier round
to seal as a sibling or abandoned — a cleanup obligation the merge cannot
discharge for you.

The command **skips** a retirement that documentation still cites, with a
warning naming the page and the pointer left live; it never refuses the
publication itself over one. The citation guard asks you to repoint the page
at the successor first, and this very run publishes the successor, so
refusing would set a trap. Running the publication again does
not clear it either — the page still cites the pointer — so the repair falls
to you: repoint the page at the artifact for that path at this head, then
`gs supersede <artifact> --rests-on <its successor here> --text <why>`.
Where this head no longer changes the path, no successor exists to
repoint at, and that one takes
[`gs supersede <artifact> --cited-ok`](supersede.md). Until you do either,
the promise still carries two heads and `gs review-request` says so.

## Republishing a head after its artifact's retirement

The key combines the actor, the head and the path, so a rerun replays what
the first run filed. That makes an interrupted publication resumable, and it
also creates the one case where a replay goes wrong: once someone has
**retired** the earlier event, publishing that head again would hand back a
withdrawn pointer, report it as the artifact standing there, and rest the
retirement it carries on it — withdrawing the live artifact at the current
head in favour of a dead one.

The command refuses that publication before building anything. The refusal
names the retired event and the head this promise's live artifact stands at.
Publish the head the work stands at now, or run that head's publication
again; nothing can bring back a retired event, and nothing here tries. This
does not affect a key that names nothing, or one that names a live event: a
first publication and an ordinary retry both go on as before.

## What it produces

One `artifact` statement per path, in one batch, each carrying
`body.path` and `body.commit`, resting on the promise and on every
`--rests-on` given. The reporting artifact comes last. Its text carries
`--text` as a second paragraph; the others do not. One supersession
follows per earlier-head artifact retired, resting on the artifact at the
same path where this run publishes one.

The identifiers printed on their own lines name the artifacts alone, so the
last of them names the reporting artifact whether or not this run retired
anything.

An artifact serves as the implementation report for assigned work, so no separate
report follows it and the commitment moves to `awaiting-review`.

## See also

- [`gs promise`](promise.md), [`gs review-request`](review-request.md), [`gs batch`](batch.md)
- [Staleness](../../concepts/staleness.md)
