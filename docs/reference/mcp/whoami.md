---
title: MCP whoami
summary: Show the configured durable actor and the ephemeral session.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f940f57d17665c1ef145af8de98b4ac125499978
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6918582b884b2f82fa7ab64242f40d12de845c39
---

# `whoami`

Reports which actor this adapter process signs as, what the roster
currently says about that actor, and the session handle for this
connection.

Call it first in a new session. Everything else you do is signed as this
actor, permanently.

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
call() { printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s,%s}}\n' "$1" "$2" "$META"; }

call whoami '{}' | gitseq-mcp --repo "$REPO" --actor alice \
  --server "http://127.0.0.1:$PORT" 2>/dev/null
```

## What comes back

| Field | Meaning |
|---|---|
| `actor` | Local configuration: name, fingerprint, key file. |
| `durable` | What the roster says now: kind, roles, membership event, and which grants each role came from. |
| `session` | This connection's ephemeral handle. |
| `protocol` | The protocol version the adapter serves. |

`actor` is local and `durable` is the record. They can disagree: a
repository can hold a key for a principal whose membership has been
retired. Trust `durable` for authority questions.

`durable.role_sources` names the grant behind each role, which is what
you would supersede to remove it.

## See also

- [`gs actors`](../gs/actors.md)
- [Actors and authority](../../concepts/actors.md)
