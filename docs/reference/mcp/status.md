---
title: MCP status
summary: Project durable work, live presence, and this session's priority ephemeral chat.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
---

# `status`

The orientation call. It answers what is available to you, what is
waiting on you, what you are waiting on, what needs your attention, and
where the record currently stands — and it hands back the cursor you pass to
[`wait`](wait.md).

It is written for an actor, not for a database. The answer is centred on
the caller.

## Arguments

| argument | required | meaning |
|---|---|---|
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

Every tool takes `repo`. Naming a different repository acts in that
repository's workroom instead; the adapter is installed once and serves
whatever repository a call names. Linked worktrees of one repository are
one workroom, not several.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## What comes back

| Field | Meaning |
|---|---|
| `you` | Your name, fingerprint and current roles. |
| `frontier` | The genesis, head and depth this answer was folded at. |
| `awaiting_ratification` | Effective, unratified, live proposals whose captured role satisfier you currently hold. |
| `available_to_you` | Unclaimed requests addressed to you, including requests whose bases have become stale. |
| `waiting_on_you` | Commitments where you have an admissible next act. |
| `you_are_waiting_on` | Commitments involving you where the next move is another actor's, including artifact completions awaiting the performer's merge. |
| `not_actionable` | Commitments involving you that nobody can currently advance. |
| `needs_your_attention` | Your own acts that did not take force, and events that concern you. |
| `totals` | Depth, commitment counts by status with a stale count beside each, artifact counts split into stale, retired and superseded-world, and ineffective and disputed acts. |
| `live` | Presence and the live generation, or `degraded`. |
| `priority_ephemeral_chat` | This exact session's bounded, unacknowledged addressed frames. `available` is false when the resident is unavailable; `skipped` counts additional pending frames behind the current page. |
| `cursor` | The composite cursor. Pass it back to `wait`. |
| `follow_with_wait` | A reminder of exactly that. |

The resident-minted credential is not part of this response. It remains
private adapter state and is absent from durable, live, diagnostic and summary
views. The resident's ordinary HTTP status and browser join view separately
state the trusted-process boundary under which that credential is meaningful.

There is also a one-line text summary, which is usually enough:

```text
priority ephemeral chat: 0 unacknowledged; depth 1, you hold 3 roles, 0 awaiting your ratification, 0 addressed to you, 0 waiting on you, 0 you are waiting on,
0 not actionable, 0 of your acts did not take force; live alice (1fb980b1de47)
```

`available_to_you` is not waiting debt. Each entry has no performer, promise,
or waiting party; it merely names you as the actor who may claim it. Its status
is normally `open`. If the request's bases moved before anyone claimed it, its
status is `stale` and its `stale` flag is `true`, but the unfinished request
remains in this lane. `waiting_on_you` begins only after a promise or explicit
report gives you an admissible next act. A reporting artifact instead projects
`awaiting-merge` waiting on its performer: artifacts have satisfier `none`, so
the requester cannot ratify one, and the performer signs the merge. The
implementation commitment closes only when an independently approved exact
head merges.

`awaiting_ratification` is also not a commitment. Each row names the proposal
in `event`, its author, kind, text, captured satisfier, and staleness qualifier;
it has no request, performer, promise, or waiting party. The row is present
only while the proposal is effective, unratified, unsuperseded, and has no
standing direct dissent. It is selected for every actor who currently holds
the role named by the proposal's captured satisfier. Ratification,
supersession, or dissent clears it. Ordinary staleness does not hide it.

A rejected implementation parent closes as terminal `superseded` only after an
explicit qualifying linked supersession. Its row carries `successor_request`
naming the repair child; it appears in history rather than a live lane. The
child's later outcome does not rewrite that pointer.

Lane rows carry the same action fields as [`work`](work.md): full
`conditions` for open and stale unclaimed requests, `report_status`, `reported_head`, and the
latest effective review for that exact head with its explicit `ratified` flag.
Routine triage therefore does not need one `inspect` call per row.

The lanes hold work still owed. A superseded, satisfied, or withdrawn commitment is
finished, and ordinary reasoning staleness under it does not reopen it —
that staleness blocks nothing and reaches most closed commitments, so a
lane full of it hid the rows that were still owed. `totals
.stale_commitments` counts it per status instead, and
[`work`](work.md) with `stale=include` lists every one of the records.
Each row that is listed still carries its own `stale` field.

## It is bounded

Every list is capped at 20 entries, each with its own skipped count, so a
shortened list reads as "20 of 500" rather than as a bare count. That
matters because some categories never discharge: stale, reneged and
cancelled commitments, and your own ineffective acts, accumulate forever.

The resident applies those caps before it encodes the response. The adapter
does not fetch the complete projection and discard most of it afterwards, so
the bytes transferred grow with the bounded view and its counters rather than
with workroom depth.

When you need the whole projection rather than an orientation, read it
another way — [`gs status --json`](../gs/status.md) has no cap.

## Without a resident

The adapter finds the resident from the repository it is acting in: a
service publishes the address it bound, with the genesis it holds, and
the adapter uses it only when that genesis matches. There is no URL to
configure and nothing to keep in step.

If no service answers, `status` still answers from the local log and
marks the live cursor `degraded`. Losing the resident changes what is
knowable, not the shape of the answer: the same digest is applied on both
paths. `priority_ephemeral_chat.available` is false; the adapter does not
invent an empty live inbox.

An upgraded adapter can also meet an older resident that does not implement
the private inbox routes yet. It keeps durable status working and marks
priority chat unavailable until that resident is upgraded or restarted. It
does not report an empty inbox as if the older service had checked one.
The adapter registers the versioned inbox capability only with a resident that
implements it; ordinary presence alone never opts a session into delivery.

## See also

- [`wait`](wait.md), [`work`](work.md), [`artifacts`](artifacts.md), [`ack`](ack.md), [`gs status`](../gs/status.md)
