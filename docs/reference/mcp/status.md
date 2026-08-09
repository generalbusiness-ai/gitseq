---
title: MCP status
summary: Project durable workroom state plus a composite cursor.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd731b2cc1986b3ca6fe9b0a0af3394790a3ee6b
---

# `status`

The orientation call. It answers what is waiting on you, what you are
waiting on, what needs your attention, and where the record currently
stands — and it hands back the cursor you pass to
[`wait`](wait.md).

It is written for an actor, not for a database. The answer is centred on
the caller.

## Arguments

No arguments.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice --server "http://127.0.0.1:$PORT" 2>/dev/null
```

## What comes back

| Field | Meaning |
|---|---|
| `you` | Your name, fingerprint and current roles. |
| `waiting_on_you` | Commitments where the next move is yours. |
| `you_are_waiting_on` | Commitments where it is not. |
| `not_actionable` | Commitments involving you that nobody can currently advance. |
| `needs_your_attention` | Your own acts that did not take force, and events that concern you. |
| `totals` | Depth, commitment counts by status, artifacts, stale artifacts, ineffective and disputed acts. |
| `live` | Presence and the live generation, or `degraded`. |
| `cursor` | The composite cursor. Pass it back to `wait`. |
| `follow_with_wait` | A reminder of exactly that. |

There is also a one-line text summary, which is usually enough:

```text
depth 1, you hold 3 roles, 0 waiting on you, 0 you are waiting on,
0 not actionable, 0 of your acts did not take force; live alice (1fb980b1de47)
```

## It is bounded

Every list is capped at 20 entries, each with its own skipped count, so a
shortened list reads as "20 of 500" rather than as a bare count. That
matters because some categories never discharge: stale, reneged and
cancelled commitments, and your own ineffective acts, accumulate forever.

When you need the whole projection rather than an orientation, read it
another way — `gs status --json` has no cap.

## Without a resident

If the service is unreachable, `status` still answers from the local log
and marks the live cursor `degraded`. Losing the resident changes what is
knowable, not the shape of the answer: the same digest is applied on both
paths.

## See also

- [`wait`](wait.md), [`gs status`](../gs/status.md)
