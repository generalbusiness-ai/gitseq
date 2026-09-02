---
title: gs work
summary: Select the work one actor still owes or is owed, bounded and paged.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1055e9d1a044c420c25d249f91c79988cfcda4d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
---

# `gs work`

Selects one actor's commitments and pending ratification attention by relationship lane, row state
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
| `--lane` | all five | Relationship lane: `awaiting_ratification`, `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`. Repeat to name several. |
| `--status` | | Row state: the commitment lifecycle states or `awaiting-ratification`. Repeat to name several. |
| `--stale` | `summary` | Staleness policy: `summary`, `include`, `only`, or `exclude`. |
| `--limit` | `20` | Page size, 1 to 50. |
| `--cursor` | | The opaque continuation from a previous page. |
| `--json` | `false` | Emit the page as JSON instead of the human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the verified local read if that fails. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

There is no default actor, for the same reason there is none on a write:
a default was a name several concurrent instances shared, and a lane read
under the wrong identity is the wrong answer rather than a slower one.
If neither `--as` nor `GITSEQ_ACTOR` selects an actor, the refusal names the
non-retired actors whose keys this checkout holds. Use [`gs whoami`](whoami.md)
to inspect the same custody view directly.

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
request text, who the work waits on when anyone is named, and the latest effective review for
the reported head. Those are the facts needed to act on a row without a
second call. An unclaimed request addressed to the selected actor stays in
`available_to_you` if its bases move: its status becomes `stale`, its `stale`
flag is `true`, and its full conditions remain present. Claimed and closed
stale commitments keep their existing lanes.

The exception to the commitment-shaped row is `awaiting_ratification`. It
names the proposal in `event` and leaves `request` empty, because a proposal is
not a commitment. The row appears to every actor holding the role in the
proposal's captured satisfier and carries its author, kind, text, satisfier,
and staleness qualifier. Ratification, supersession, or standing direct
dissent removes it.

An artifact completion has status `awaiting-merge` and waits on its performer.
Its kind has satisfier `none`, so requester ratification is not an admissible
closing act; the performer merges the independently approved exact head, and
that merge closes it.

A rejected implementation parent has terminal status `superseded` only after
an explicit qualifying linked supersession. Its JSON row carries
`successor_request`, the exact repair child; request that status explicitly to
read the historical transfer.

The human view prints the request's full canonical event ID. `#N` remains a
useful display index in one workroom, but it is not accepted in `--rests-on`,
targets, or `Rests-On:` trailers. `--json` also carries every event ID in full.

`--stale summary`, which is what a call naming no policy receives, answers
*what is still owed*. A superseded, satisfied, or withdrawn commitment carrying only
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
