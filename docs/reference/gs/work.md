---
title: gs work
summary: Select the work one actor still owes or is owed, bounded and paged.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1055e9d1a044c420c25d249f91c79988cfcda4d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0d87b56bb5146f67931203a41039e3d511ce503e
---

# `gs work`

Selects one actor's commitments by relationship lane, lifecycle status
and staleness policy, and pages through the result.

This is the same selection the MCP [`work`](../mcp/work.md) tool and the
resident's `/v0/work-query` route make, through the same code. The
filters, the caps and the cursor mean one thing on every surface, and
`--json` prints the same page shape those surfaces return.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | | The actor whose work is selected. Required; falls back to `GITSEQ_ACTOR`. |
| `--lane` | all four | Relationship lane: `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`. Repeat to name several. |
| `--status` | | Lifecycle status: `open`, `promised`, `reported`, `satisfied`, `stale`, `cancelled`, `reneged`, `withdrawn`. Repeat to name several. |
| `--stale` | `summary` | Staleness policy: `summary`, `include`, `only`, or `exclude`. |
| `--limit` | `20` | Page size, 1 to 50. |
| `--cursor` | | The opaque continuation from a previous page. |
| `--json` | `false` | Emit the page as JSON instead of the human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the verified local read if that fails. |

There is no default actor, for the same reason there is none on a write:
a default was a name several concurrent instances shared, and a lane read
under the wrong identity is the wrong answer rather than a slower one.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists' \
  --rests-on "$SEED")
gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST" >/dev/null

gs work --repo "$REPO" --as bot
gs work --repo "$REPO" --as alice --lane you_are_waiting_on
gs work --repo "$REPO" --as bot --status promised --stale exclude --limit 5
gs work --repo "$REPO" --as bot --json | head -8
```

## Reading it

The header names the frontier the answer was taken at. The line under it
gives the whole-log totals — how many commitments matched, how many this
page returned, how many came before it and how many remain — so a
shortened list never reads as a complete one.

Each row carries its lifecycle status, its lane, the request event, the
request text, who the work waits on, and the latest effective review for
the reported head. Those are the facts needed to act on a row without a
second call.

`--stale summary`, which is what a call naming no policy receives, answers
*what is still owed*. A satisfied or withdrawn commitment carrying only
ordinary reasoning staleness is counted in `closed_stale_omitted` rather
than listed. Naming any `--status` also overrides the summary. The other
three policies return exactly what they say: `include` adds the closed
stale rows, `only` returns records carrying staleness in any lifecycle
state, `exclude` returns records carrying none.

A cursor is bound to its exact head **and** its exact filters. Changing
either is refused rather than silently splicing two selections into one
answer; restart the query instead.

## Cost

Selection happens before any rendering, and the response is bounded by the
page cap rather than by workroom depth. With `--server`, the selection
happens at the resident and nothing larger than the page crosses the
socket. Without it, the local read folds the log the way
[`gs status`](status.md) does and then selects.

## See also

- [`gs status`](status.md), [`gs inspect`](inspect.md), [`gs artifacts`](artifacts.md)
- [MCP `work`](../mcp/work.md)
