---
title: gs land
summary: Ratify, merge, push and clean up one approved head, in that order and no other.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs land`

Finishes an approved lane: ratify the approval when that is yours to do,
preview the merge, run it, push the target, and — only once Git says the
head is in the target — remove the worktree and branch the work was done
on.

It adds no authority. The merge it runs is [`gs merge`](merge.md)'s own
locked transaction, with the same validation, succession and receipt. What
it adds is the order, the refusals that cost most when they arrive late,
and a lease on every deletion.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor recording the merge receipt. [`gs merge`](merge.md) requires it to be the implementer the approval names, and refuses anyone else. |
| `--approval` | *(required)* | The approval report [`gs review`](review.md) filed. The head it approved is read from it. |
| `--checkout` | *(required)* | The **target** checkout: the working tree standing on the request's target ref, not the candidate's worktree. |
| `--text` | *(required)* | A plain-language description of the change and its impact, for a reader who will never see an event id. |
| `--cleanup` | `false` | After the candidate is provably in the target, remove its worktree and delete its branch here and on origin. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a resident that is cold, loaded, or folding a large log; a value that is not a positive duration is refused before anything is signed. |

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
error. The landed head is printed on standard output.

## The order, and why each step is where it is

1. **Read the approval.** The head to land is read from the verdict, not
   retyped. A `changes-requested` verdict, or a retired one, is refused
   here.
2. **Check the checkout.** `--checkout` must stand on the request's target
   ref. Passing the candidate's worktree is the common mistake, and the
   refusal says so in those words.
3. **Check it is clean.** A merge refuses a dirty checkout, including
   untracked files — after it has reserved the approval. This refusal
   names the files instead.
4. **Ratify the approval if it is yours to ratify.** Only the review
   requester may. When you are that requester the separate command is
   ceremony; when you are not, the refusal names who must run
   [`gs ratify`](ratify.md). It happens *after* the two checks above, and
   before the preview that needs it, because a ratification is a durable
   act and a run that is going to refuse over the checkout must leave the
   log exactly as long as it found it.
5. **Preview.** The read-only [merge plan](merge-plan.md) runs before
   anything is staged or reserved. A refusal prints every reason it gave.
6. **Merge.** `gs merge`'s locked path. If the workroom frontier moved
   between planning and landing, that refusal leaves nothing behind, so
   the merge is planned and run once more.
7. **Push.** `git push origin <target ref>`. A repository with no origin
   is an ordinary arrangement and is reported, not failed. A push that
   fails with an origin present *is* an error: the next step would delete
   the only other copy of those commits.
8. **Clean up,** with `--cleanup` only.

`gs land` signs no authorization. A request whose landing is **held** is
released by its hold owner, through the report sequence
[`gs merge`](merge.md) describes, and this command carries no
`--authorization` flag: it lands on the ratified approval alone, and where
the compatibility window permits an unreleased held landing the merge
warns and the receipt records it. Obtain the release first, then land, and
use `gs merge --authorization` when the lane needs the exact report named.

## What `--cleanup` will not do

Deletion is irreversible, so each step is gated on a measurement rather
than on the previous command appearing to work:

- `git merge-base --is-ancestor <candidate> <target ref>` must exit 0. A
  non-zero exit means either "not an ancestor" or "the check never ran",
  and the two are told apart by the exit status; neither deletes anything.
- Exactly one local branch must point at the candidate. None, or more than
  one, is reported and nothing is deleted, because which branch the work
  was done on is then not decidable.
- The local branch is deleted with `git update-ref -d <ref> <candidate>`,
  one compare-and-swap against the tip just measured. A branch somebody
  advanced in between keeps its commits, and the refusal names both tips.
- On origin, the remote tip is read first. A tip that is not the head that
  landed carries work this landing did not include: it is kept and
  reported, never deleted. A tip that is the landed head is deleted under
  `--force-with-lease=<ref>:<candidate>`, so one that moves between the
  reading and the push is rejected by the remote rather than overwritten.
- The worktree is removed first, then the local branch, then the branch on
  origin. A failure to remove the worktree stops the sequence with the
  branch intact, and nothing about the remote can fail the command: the
  landing is already complete, so what is left is reported.

## See also

- [`gs merge`](merge.md), [`gs merge-plan`](merge-plan.md), [`gs ratify`](ratify.md)
- [`gs review-request`](review-request.md), [`gs work`](work.md)
