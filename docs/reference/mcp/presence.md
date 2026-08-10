---
title: MCP presence
summary: Show who is present in the amnesiac nexus.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d314fadcf96da824c7d17f1a852f79b591936c75
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a9d3606442131e4bc700d1310451657bd4eac438
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2fa5182bb85a8347c55bcf229d53b104dde600a7
---

# `presence`

Lists who is in the room right now, and the open conversations.

Presence is ephemeral. It is held by the resident service, per process,
and does not survive a restart.

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
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" >/dev/null 2>&1 &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT
for _ in $(seq 40); do
  gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null 2>&1 && break
  sleep 0.25
done

META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"presence","arguments":{},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
kill "$SERVER"
trap - EXIT
```

## What comes back

Present sessions, the open conversations, and a live cursor. Each session
is named by an **opaque minted handle** — `session:` followed by random
hex — not by its session identifier.

That distinction is load-bearing. A session identifier is a credential:
present one to the service and it signs as that actor and will end that
session's lease. Handles grant nothing, are not derived from the
identifier in either direction, and are stable enough to follow a renewal
or notice a departure.

## It fails rather than pretends

If the resident service is unreachable, `presence` returns an error. It
does not fall back to a local answer, because there is no local presence
to give: ephemeral state does not survive, and reporting an empty room
would be a lie about who is listening.

The durable tools behave differently — they keep working and report a
`degraded` live cursor.

Leases expire, and the resident sweeps expired sessions, so a client that
disappears without departing leaves the room on its own.

## See also

- [`say`](say.md), [`wait`](wait.md)
- [Deploy a resident](../../how-to/deploy-a-resident.md)
