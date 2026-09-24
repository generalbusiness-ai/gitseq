---
title: gs land
summary: Ratify, merge, push and clean up one approved head, in that order and no other.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs land`

Finishes an approved lane: ratify the approval when that falls to you,
preview the merge, run it, push the target, and — only once Git says the
target contains the head — remove the worktree and branch the work happened
on.

It adds no authority. The merge it runs uses [`gs merge`](merge.md)'s own
locked transaction, with the same validation, succession and receipt. It
adds the order, the refusals that cost most when they arrive late,
and a lease on every deletion.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor recording the merge receipt. [`gs merge`](merge.md) requires the implementer the approval names, and refuses anyone else. |
| `--approval` | *(required)* | The approval report [`gs review`](review.md) filed. `gs land` reads the head it approved from it. |
| `--checkout` | *(required)* | The **target** checkout: the working tree standing on the request's target ref, not the candidate's worktree. |
| `--text` | *(required)* | A plain-language description of the change and its impact, for a reader who will never see an event id. |
| `--cleanup` | `false` | After the target provably contains the candidate, remove its worktree and delete its branch here and on origin. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; `gs land` refuses a value that does not form a positive duration before signing anything. |

It takes no positional arguments. `--approval` accepts a
[short reference](../event-identifiers.md#typing-one-at-a-boundary).

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

WORK="$(dirname "$REPO")/changelog"
git -C "$REPO" worktree add -q -b request/changelog "$WORK"
printf '# Changelog\n' > "$WORK/CHANGELOG.md"
git -C "$WORK" add CHANGELOG.md
git -C "$WORK" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$WORK" rev-parse HEAD)

ARTIFACT=$(gs artifact --repo "$REPO" --as bot --head "$HEAD_COMMIT" --promise "$PROMISE" CHANGELOG.md)
REVIEW_REQUEST=$(gs review-request --repo "$REPO" --as bot --head "$HEAD_COMMIT" --to @carol)
REVIEW_PROMISE=$(gs promise --repo "$REPO" --as carol "$REVIEW_REQUEST")
APPROVAL=$(gs review --repo "$REPO" --as carol --checkout "$WORK" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED. Architecture, Security and Simplification: no findings.')

ORIGIN="$(dirname "$REPO")/origin.git"
git init -q --bare "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q origin "$BASE"
git -C "$REPO" push -q origin request/changelog

gs land --repo "$REPO" --as bot --approval "$APPROVAL" --checkout "$REPO" \
  --text 'Add a changelog so releases are readable.' --cleanup
```

The example ratifies nothing by hand: `bot` asked for the review, so
`gs land` ratifies the approval on the way past and says so on standard
error. It prints the landed head on standard output.

## The order, and why each step comes where it does

1. **Read the approval.** `gs land` reads the head to land from the
   verdict; nobody retypes it. It refuses a `changes-requested` verdict, or
   a retired one, here.
2. **Check the checkout.** `--checkout` must stand on the request's target
   ref. The common mistake passes the candidate's worktree, and the
   refusal says so in those words.
3. **Check for a clean tree.** A merge refuses a dirty checkout, including
   untracked files — after it has reserved the approval. This refusal
   names the files instead.
4. **Ratify the approval if that falls to you.** Only the review
   requester may. When you made that request the separate command adds only
   ceremony; otherwise, the refusal names who must run
   [`gs ratify`](ratify.md). It happens *after* the two checks above, and
   before the preview that needs it, because a ratification makes a durable
   act, and a run that will refuse over the checkout must leave the
   log exactly as long as it found it.
5. **Preview.** The read-only [merge plan](merge-plan.md) runs before
   staging or reserving anything. A refusal prints every reason it gave.
6. **Merge.** `gs merge`'s locked path. If the workroom frontier moved
   between planning and landing, that refusal leaves nothing behind, so
   `gs land` plans and runs the merge once more.
7. **Push.** `git push origin <target ref>`. A repository with no origin
   makes an ordinary arrangement; `gs land` reports it rather than failing.
   A push that fails with an origin present *does* count as an error: the
   next step would delete the only other copy of those commits.
8. **Clean up,** with `--cleanup` only.

`gs land` signs no authorization. When a request's landing sits **held**, its
hold owner releases it, through the report sequence
[`gs merge`](merge.md) describes, and this command carries no
`--authorization` flag: it lands on the ratified approval alone, and where
the compatibility window permits an unreleased held landing the merge
warns and the receipt records it. Obtain the release first, then land, and
use `gs merge --authorization` when the lane needs the exact report named.

## What `--cleanup` will not do

Nobody can reverse a deletion, so a measurement gates each step, rather than
the previous command appearing to work:

- `git merge-base --is-ancestor <candidate> <target ref>` must exit 0. A
  non-zero exit means either "not an ancestor" or "the check never ran",
  and the exit status tells the two apart; neither deletes anything.
- Exactly one local branch must point at the candidate. For none, or more
  than one, `gs land` reports it and deletes nothing, because it then cannot
  decide which branch the work happened on.
- `gs land` deletes the local branch with `git update-ref -d <ref> <candidate>`,
  one compare-and-swap against the tip just measured. A branch somebody
  advanced in between keeps its commits, and the refusal names both tips.
- On origin, `gs land` reads the remote tip first. A tip other than the head
  that landed carries work this landing did not include: `gs land` keeps and
  reports it, never deletes it. A tip that matches the landed head gets deleted
  under `--force-with-lease=<ref>:<candidate>`, so the remote rejects one that
  moves between the reading and the push rather than overwriting it.
- `gs land` removes the worktree first, then the local branch, then the branch
  on origin. A failure to remove the worktree stops the sequence with the
  branch intact, and nothing about the remote can fail the command: the
  landing has already completed, so `gs land` reports what remains.

## See also

- [`gs merge`](merge.md), [`gs merge-plan`](merge-plan.md), [`gs ratify`](ratify.md)
- [`gs review-request`](review-request.md), [`gs work`](work.md)
