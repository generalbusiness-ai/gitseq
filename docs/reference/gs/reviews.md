---
title: gs reviews
summary: Report whether the review queue is quiet — nothing awaiting a first verdict, no approved head still out of a branch.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
---

# `gs reviews`

Answers one question about the whole log: is the review queue quiet? It
reports how many review requests are still waiting for a first verdict,
how many name an artifact that resolves to nothing, and which approved
heads are not yet ancestors of a named branch.

It is a gate, not a listing. Run it before a step that waiting cannot
undo — a retirement wave, a batch migration — and it **exits non-zero**
while anything is outstanding, after printing what.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--branch` | `main` | The branch an approved head must already be an ancestor of. |
| `--checkout` | `--repo` | The checkout whose Git history answers the ancestry question. |
| `--limit` | `20` | How many events to name under each count, 1 to 50. |
| `--json` | `false` | Emit the report as JSON instead of the human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the verified local read if that fails. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'ordinary seed'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
BRANCH=$(git -C "$REPO" rev-parse --abbrev-ref HEAD)

gs reviews --repo "$REPO" --branch "$BRANCH"

ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'the changelog stands here' \
  --body path=CHANGELOG.md --body commit="$(git -C "$REPO" rev-parse HEAD)" \
  --rests-on "$SEED")
gs state --repo "$REPO" --as alice --kind request \
  --text 'bot: review the changelog head' --body to=@bot \
  --body conditions='approve or request changes' --body artifact="$ARTIFACT" \
  --rests-on "$ARTIFACT" >/dev/null

if gs reviews --repo "$REPO" --branch "$BRANCH"; then
  echo 'unexpectedly quiet'
else
  echo 'the gate refused, which is the point'
fi
```

## What counts

A request is a **review request** when it names an artifact at all.
Whether the name resolves is a separate fact, reported as its own number:
an unexpanded placeholder or a head that never merged is counted as
awaiting rather than dropped. A gate that can declare quiet while work is
outstanding is worse than no gate, because the step it releases is the one
that cannot be undone by waiting longer.

A request is **settled** by an effective verdict on its own chain. Two
things follow:

- Only verdicts the fold judged effective count. A report the fold refused
  carries a verdict in its body and settles nothing.
- Settlement is keyed by the request event, not by the artifact a request
  names. Several requests can name one artifact, and a verdict on one of
  them settles only that one.

Either canonical verdict settles a review. Waiting cannot turn a
`changes-requested` into an approval, so counting only approvals would
leave every closed-with-changes review in the total for ever.

An **approved head** is counted when its approval is ratified, not
retired, and neither the approval nor its artifact describes a superseded
world. Those excluded heads are waiting for a current behaviour anchor,
not for a fresh verdict on the same chain, and counting them would make
the precondition unreachable. The head comes from the review record
itself, which carries the exact commit the verdict was signed over.

## Three answers about a branch, not two

Git says one of three things about a commit: it is an ancestor, it is not,
or the check never ran because this clone does not hold the commit. The
report keeps the third apart. It stops the gate like the second, because
an unfetched approval is not a landed one — but the repair is to fetch it,
not to merge it.

A branch name Git cannot resolve is refused outright. Reporting every head
as out of a branch that does not exist is a confident wrong answer.

## Bounded and fail closed

The awaiting, unresolved and approved-head lists are bounded samples:
`--limit` names how many to print, and the report says how many it left out
beside the whole-log count. Display omission is not check omission: `gs`
classifies the complete actionable approved-head population against Git, then
prints bounded samples of the heads that are out of the branch or unknown.
Either population keeps the gate closed. Already-landed heads stop mattering,
even when more historical approvals exist than one display page can hold. Git
runs a fixed number of processes, independent of the number of heads.

## See also

- [`gs review`](review.md), [`gs merge`](merge.md), [`gs artifacts`](artifacts.md)
- [Staleness](../../concepts/staleness.md)
