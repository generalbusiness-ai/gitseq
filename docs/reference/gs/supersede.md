---
title: gs supersede
summary: Retire an act, and propagate staleness to everything resting on it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# `gs supersede`

Retires one act and marks everything that rests on it stale,
transitively. Nothing is deleted; the retired act stays in the log with a
pointer to what replaced it.

Prefer supersession to contradiction.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The superseding actor. |
| `--text` | *(required)* | Why. This is what a later reader gets. |
| `--rests-on` | | An additional event identifier, repeatable. The target is added first automatically. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |
| `--cited-ok` | `false` | Retire even though tracked documentation still names the target. Without it the retirement is refused and the pages are listed, because a page resting on a withdrawn pointer fails the documentation gate. Use it for a migration that retires first and re-anchors after. |

The target is a **positional argument**, and flag parsing stops at the
first positional. Put every flag before it.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

CLAIM=$(gs state --repo "$REPO" --as alice --kind assert \
  --text 'The pricing decision holds until the next review' --rests-on "$SEED")
NOTE=$(gs state --repo "$REPO" --as alice --kind assert \
  --text 'The quote uses that pricing' --rests-on "$CLAIM")

gs supersede --repo "$REPO" --as alice \
  --text 'the review happened and the pricing changed' "$CLAIM"
gs status --repo "$REPO"
```

The note resting on the retired claim is now stale. That is a signal to
re-check it, not a verdict that it is wrong.

## What it is for

**Replacing an artifact.** When new work lands at a path, record the new
artifact and supersede the previous one for the same path, as one step.
That supersession is what makes documents describing the old
implementation flare. Skip it, and `gs status` marks the new artifact
**succession not recorded**.

**Withdrawing a request.** If a requester supersedes a request after
someone promised it, the promisor is released. The promise stays in
history as kept faith.

When you are reassigning a request because it appeared unclaimed, use
[`gs reassign-if-unclaimed`](reassign-if-unclaimed.md). Its signed pair refuses
if a promise or direct completion raced your read. Ordinary supersession stays
available for a requester deliberately withdrawing promised work.

**Reneging.** Superseding your own promise is reneging, and it is visible
forever. Do it as early as you know you cannot keep it.

**Revoking authority.** [`gs role-revoke`](role-revoke.md) is a
supersession of a roster grant, with the derived-role handling built in.
The founding operator seed cannot be retired. Other governance changes
require current authority for their target: changing an operator grant,
or membership carrying a live or dormant operator grant, requires an
`operator` rather than an ordinary `ratifier`.

## The first-basis rule

A supersession must cite its target as the **first** basis. `gs
supersede` puts it there for you; anything you pass with `--rests-on` is
appended after. If you construct the act by hand and get the order wrong,
it is ineffective.

## Reversible

Superseding a supersession restores the earlier act, and everything that
went stale because of it becomes current again. Liveness is current and
moves; decisions are history and do not. Governance restoration is the
exception to ordinary author-owned supersession: its target-class
authority is checked again at the time of restoration.

## See also

- [Staleness](../../concepts/staleness.md)
- [`gs status`](status.md)
- [`gs reassign-if-unclaimed`](reassign-if-unclaimed.md)
