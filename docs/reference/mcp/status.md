---
title: MCP status
summary: Project durable work, live presence, and this session's priority ephemeral chat.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66b6cb0b770fe88808130a195babf79fe1ea7746
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
| `available_to_you` | Open, unclaimed requests addressed to you. |
| `waiting_on_you` | Commitments where the next move is yours. |
| `you_are_waiting_on` | Commitments where it is not. |
| `not_actionable` | Commitments involving you that nobody can currently advance. |
| `needs_your_attention` | Your own acts that did not take force, and events that concern you. |
| `totals` | Depth, commitment counts by status, artifacts, stale artifacts, ineffective and disputed acts. |
| `live` | Presence and the live generation, or `degraded`. |
| `priority_ephemeral_chat` | This exact session's bounded, unacknowledged addressed frames. `available` is false when the resident is unavailable; `skipped` counts additional pending frames behind the current page. |
| `cursor` | The composite cursor. Pass it back to `wait`. |
| `follow_with_wait` | A reminder of exactly that. |

There is also a one-line text summary, which is usually enough:

```text
priority ephemeral chat: 0 unacknowledged; depth 1, you hold 3 roles, 0 addressed to you, 0 waiting on you, 0 you are waiting on,
0 not actionable, 0 of your acts did not take force; live alice (1fb980b1de47)
```

`available_to_you` is not waiting debt. Each entry is still `open`, with
no performer, promise, or waiting party; it merely names you as the actor
who may claim it. `waiting_on_you` begins only after a promise or report
puts the next move on you.

## It is bounded

Every list is capped at 20 entries, each with its own skipped count, so a
shortened list reads as "20 of 500" rather than as a bare count. That
matters because some categories never discharge: stale, reneged and
cancelled commitments, and your own ineffective acts, accumulate forever.

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

- [`wait`](wait.md), [`ack`](ack.md), [`gs status`](../gs/status.md)
