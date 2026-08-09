---
title: MCP whoami
summary: Show the configured durable actor and the ephemeral session.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d314fadcf96da824c7d17f1a852f79b591936c75
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6918582b884b2f82fa7ab64242f40d12de845c39
---

# `whoami`

Reports which actor this adapter process signs as, what the roster
currently says about that actor, and the session handle for this
connection.

Call it first in a new session. Everything else you do is signed as this
actor, permanently.

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
call() { printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s,%s}}\n' "$1" "$2" "$META"; }

call whoami '{}' | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## What comes back

| Field | Meaning |
|---|---|
| `actor` | Local configuration: name, fingerprint, key file. |
| `repo` | The repository this call acted in, as its git common directory. |
| `genesis` | The genesis hash of that repository's workroom. |
| `durable` | What the roster says now: kind, roles, membership event, and which grants each role came from. |
| `session` | This connection's ephemeral handle. |
| `protocol` | The protocol version the adapter serves. |

`repo` and `genesis` are worth reading before you act. One adapter serves
whatever repository a call names, so they are the answer to "which
workroom am I about to speak in".

`actor` is local and `durable` is the record. They can disagree: a
repository can hold a key for a principal whose membership has been
retired. Trust `durable` for authority questions.

`durable.role_sources` names the grant behind each role, which is what
you would supersede to remove it.

## See also

- [`gs actors`](../gs/actors.md)
- [Actors and authority](../../concepts/actors.md)
