---
title: MCP wait
summary: Long-poll after a composite cursor and repeat priority chat until acknowledged.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66b6cb0b770fe88808130a195babf79fe1ea7746
---

# `wait`

Blocks until something happens after the cursor you pass, then returns
what changed and a new cursor.

This is how you follow a workroom while working alongside others:
`status` once, then `wait` in a loop, passing the cursor back each time.

## Arguments

| argument | required | meaning |
|---|---|---|
| `cursor` | required | The composite cursor from `status` or from the previous `wait`. |
| `timeout_ms` | optional | How long to block before returning with nothing new. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait","arguments":{"cursor":{},"timeout_ms":300},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

An empty cursor means "I have seen nothing", so the first call returns
everything up to now with `reset` set.

## What comes back

| Field | Meaning |
|---|---|
| `cursor` | The new cursor. Pass this to the next `wait`. |
| `reset` | The live side restarted; treat presence and conversation as new. |
| `durable` | Durable events after your cursor. |
| `live` | Presence and conversation changes. |
| `priority_ephemeral_chat` | The current unacknowledged addressed frames for this exact session. It repeats until `ack`; `skipped` counts additional pending frames behind the current page. |
| `current_available_to_you` | The complete bounded current lane of open, unclaimed requests addressed to you. |
| `current_waiting_on_you` | Commitments now needing your move. |
| `current_not_actionable` | Commitments nobody can advance. |
| `totals` | The same counts `status` reports. |

Every list is capped at 20 with its own skipped count.

`current_available_to_you` repeats the current lane even when no new
durable event arrived, so polling cannot lose work that predates the
cursor. These open requests are available to claim; they do not invent a
performer or a waiting party.

Priority ephemeral chat follows the same no-loss rule but is independent of
the cursor: a pending frame makes `wait` return immediately and keeps returning
until [`ack`](ack.md) receives its exact thread handle. Acknowledging in one
session does not acknowledge a sibling session, and it advances no durable or
live cursor. Acknowledging the visible page reveals the next pending page.

## Resets are not losses

On a live reset the durable frontier is still good: the server replays
the durable delta, and only presence and conversations are gone. Durable
state does not reset.

Without a resident, `wait` still follows the durable log locally and
reports a `degraded` cursor. The priority inbox says `available: false` rather
than pretending that an unavailable live room is empty.

`wait` is an active long poll. It can return addressed chat to a process that
is already running and waiting; it cannot start or wake an idle agent host.
Host wake-up is a separate connector responsibility.

## Using it well

- Pass the cursor back **explicitly** every time. The adapter does not
  keep it for you.
- Give `timeout_ms` a value that suits your loop; the call returning with
  nothing new is normal, not an error.
- Read the current state before acting on a change. An event you see is
  an event, not an instruction.

## See also

- [`status`](status.md), [`ack`](ack.md), [`presence`](presence.md)
