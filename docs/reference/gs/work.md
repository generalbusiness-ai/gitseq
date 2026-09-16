---
title: gs work
summary: Select the work one actor still owes or is owed, bounded and paged.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f85c910b62d17846463092a668e7af6d19b20fb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:adafb7b0046989609ff369efcac5acb605aa403a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c2c9d0ef010bb7227472c4b8ada52a33f4723e5
---

# `gs work`

Selects one actor's commitments and pending ratification attention by relationship lane, row state
and staleness policy, and pages through the result.

This is the same selection the MCP [`work`](../mcp/work.md) tool and the
resident's `/v0/work-query` route make, through the same code. The
filters, the caps and the cursor mean one thing on every surface, and
`--json` prints the same page shape those surfaces return.

## Flags

The additional explicit lane `approved_not_landed` selects the actor as
performer or hold owner. It includes legacy satisfied rows with delivery debt
and preserves the waiting party. The five existing lanes remain the default.
Rows expose [landing evidence and current Git observations](../landing-observations.md).

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | | The actor whose work is selected. Required; falls back to `GITSEQ_ACTOR`. |
| `--lane` | all five | Relationship lane: `awaiting_ratification`, `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`; add `approved_not_landed` explicitly for that audit. Repeat to name several. |
| `--status` | | Row state: the commitment lifecycle states or `awaiting-ratification`. Repeat to name several. |
| `--target-ref` | | Exact destination filter, such as `refs/heads/release`. |
| `--approved-not-landed` | absent | Filter delivery debt; `--approved-not-landed=false` explicitly selects rows without it. |
| `--stale` | `summary` | Staleness policy: `summary`, `include`, `only`, or `exclude`. |
| `--limit` | `20` | Page size, 1 to 50. |
| `--cursor` | | The opaque continuation from a previous page. |
| `--json` | `false` | Emit the page as JSON instead of the human view. |
| `--next` | `false` | Print one exact command line for the act each selected row owes, instead of the human view. |
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
  --text 'Run the tests' --body to=@bot --body conditions='the result is in the report' \
  --body no_git_artifact=true --rests-on "$SEED")
gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will run them' --rests-on "$REQUEST" >/dev/null

gs work --repo "$REPO" --as bot
gs work --repo "$REPO" --as alice --lane you_are_waiting_on
gs work --repo "$REPO" --as bot --status promised --stale exclude --limit 5
gs work --repo "$REPO" --as bot --json | head -8
gs work --repo "$REPO" --as bot --next
```

## `--next`: what to type

The page says what each row is. `--next` says what to do about it: for
every row it returns, one `#` comment naming the row and one command line
you can copy.

```text
# open available_to_you git:sha1:…#git:sha1:…
#   Add a changelog
gs promise --as bot git:sha1:…#git:sha1:…
#   or decline: gs state --as bot --kind assert --rests-on git:sha1:…#git:sha1:… --text '<why you decline>', and ask alice to retire it
```

It is a formatter over the same rows, not a second rule set, and it prints
the same selection: `--lane`, `--status`, `--stale`, `--limit` and
`--cursor` all still apply. The mapping is one line per lane:

| the row | the act it owes |
|---|---|
| an unclaimed request addressed to you | [`gs promise`](promise.md), with the decline written out beside it |
| a proposal awaiting your ratification | [`gs ratify`](ratify.md) |
| a promise of yours with nothing published | [`gs artifact`](artifact.md) |
| a promised review request | [`gs review`](review.md), as a skeleton |
| an artifact awaiting review with no request out | [`gs review-request`](review-request.md) |
| an approved head you asked the review for | [`gs ratify`](ratify.md), then [`gs land`](land.md) |
| a row awaiting landing | [`gs land`](land.md) |
| a report you must ratify as its requester | [`gs ratify`](ratify.md) |
| an assert resting on one of your promises | [`gs inspect`](inspect.md), with its first line |

A row that owes you nothing says so and says why — "waiting on carol", "a
review request is live and no verdict has been filed yet" — because a row
that prints nothing and a row that owes nothing must not look the same.

Facts the bounded page does not carry appear as angle-bracketed
placeholders: `<head>`, `<path…>`, `<reviewer>`, `<checkout on
refs/heads/main>`. A command line with a hole in it is still the shape of
the act, and it is honest about the part only you know. A row that is
stale carries a note naming its successor, or the requester to ask for a
refile, above the act it still owes.

Asserts are not commitments and never appear as rows, so `--next` scans
for asserts resting on your live promises and prints the ten newest. A
breakdown somebody filed against your work is otherwise found only by
whoever thought to look.

Because those acts depend on facts the page does not carry, `--next` reads
the projection locally even when the page itself came from a resident.

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

An artifact completion has status `awaiting-review` until a ratified approval
names it, and then `awaiting-landing` — or `awaiting-authorization`, waiting on
the hold owner, when the request is held. Its kind has satisfier `none`, so
requester ratification is not an admissible closing act; the performer merges
the independently approved exact head, and that merge closes it.

A request has terminal status `superseded` only after an explicit supersession
that names where the work went: a qualifying linked transfer of a rejected
implementation round, or a successor request that rests on the approved head
and so carries it. Its JSON row carries `successor_request`, the exact child;
request that status explicitly to read the historical transfer. A supersession
that declared the approved head dropped reads `abandoned` instead.

The human view prints the request's full canonical event ID beside its `#N`
record number, and `--json` carries every event ID in full. Either can be
typed back: `--rests-on` and the target of every command that takes one accept
the number as well as the identifier, and resolve it before signing. A
`Rests-On:` trailer is the exception and takes the full identifier only, since
a commit message is not a boundary anything resolves at. See
[Event identifiers](../event-identifiers.md#typing-one-at-a-boundary).

`--stale summary`, which is what a call naming no policy receives, answers
*what is still owed*. A superseded, satisfied, withdrawn or abandoned commitment carrying only
ordinary reasoning staleness, with no approved-artifact landing debt, is counted in `closed_stale_omitted` rather
than listed. Naming any `--status` also overrides the summary. The other
three policies return exactly what they say: `include` adds the closed
stale rows, `only` returns records carrying staleness in any lifecycle
state, `exclude` returns records carrying none.

An approved artifact can retain landing debt after source closure, so the
audit preserves the original status and waiting party. `latest_review` and
the fold-selected `approval` are distinct fields: a newer review is not itself
a sealed landing.

A cursor is bound to its exact durable head **and** its exact filters. Changing
either is refused rather than silently splicing two selections into one
answer; restart the query instead. Current Git observations may change between
pages without moving that durable frontier.

## Cost

Selection happens before any rendering, and the response is bounded by the
page cap rather than by workroom depth. With `--server`, the selection
happens at the resident and nothing larger than the page crosses the
socket. Without it, the local read folds the log the way
[`gs status`](status.md) does and then selects.

## See also

- [`gs promise`](promise.md), [`gs artifact`](artifact.md), [`gs review-request`](review-request.md), [`gs land`](land.md)
- [`gs status`](status.md), [`gs inspect`](inspect.md), [`gs artifacts`](artifacts.md)
- [MCP `work`](../mcp/work.md)
