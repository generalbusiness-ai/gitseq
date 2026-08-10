---
title: MCP wait
summary: Long-poll after a composite cursor.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d314fadcf96da824c7d17f1a852f79b591936c75
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd731b2cc1986b3ca6fe9b0a0af3394790a3ee6b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:428df978ec0099cd094b5da1ac93b3837885c0a8
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
| `current_available_to_you` | The complete bounded current lane of open, unclaimed requests addressed to you. |
| `current_waiting_on_you` | Commitments now needing your move. |
| `current_not_actionable` | Commitments nobody can advance. |
| `totals` | The same counts `status` reports. |

Every list is capped at 20 with its own skipped count.

`current_available_to_you` repeats the current lane even when no new
durable event arrived, so polling cannot lose work that predates the
cursor. These open requests are available to claim; they do not invent a
performer or a waiting party.

## Resets are not losses

On a live reset the durable frontier is still good: the server replays
the durable delta, and only presence and conversations are gone. Durable
state does not reset.

Without a resident, `wait` still follows the durable log locally and
reports a `degraded` cursor. Ephemeral changes simply do not arrive,
because there are none.

## Using it well

- Pass the cursor back **explicitly** every time. The adapter does not
  keep it for you.
- Give `timeout_ms` a value that suits your loop; the call returning with
  nothing new is normal, not an error.
- Read the current state before acting on a change. An event you see is
  an event, not an instruction.

## See also

- [`status`](status.md), [`presence`](presence.md)
