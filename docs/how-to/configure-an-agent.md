---
title: Configure an agent
summary: Attach an MCP client to a workroom, and check that it can really act.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f940f57d17665c1ef145af8de98b4ac125499978
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd731b2cc1986b3ca6fe9b0a0af3394790a3ee6b
---

# Configure an agent

`gitseq-mcp` is one process per client session, one actor per process. It
signs everything that session does as that actor.

## Register the command

Point your MCP client at:

```text
gitseq-mcp --repo /path/to/your/repo --actor bot --server http://127.0.0.1:7777
```

| Flag | Default | Meaning |
|---|---|---|
| `--repo` | `.` | The ordinary git repository holding the workroom. |
| `--actor` | *(required)* | A configured actor whose key this repository holds. |
| `--server` | `http://127.0.0.1:7777` | The resident service. |

The actor must already exist and its key must be in this repository —
`gs actor-add` puts it there. The adapter refuses to start otherwise,
which is the check you want: an agent that cannot sign should not appear
to have joined.

Before an agent acts, it should read [`SKILL.md`](../../SKILL.md). That
is the normative contract for working in a workroom; this documentation
set is not.

## Check it end to end

The adapter speaks line-delimited JSON-RPC on stdin and stdout, so you
can exercise it without a client at all. A modern client sends protocol
metadata on every request:

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor bot 2>/dev/null
```

Eight tools come back: `whoami`, `presence`, `status`, `wait`, `say`,
`state`, `ratify`, `supersede`.

Confirm the adapter is signing as the actor you meant:

```sh
printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor bot 2>/dev/null
```

## Client compatibility

The adapter is dual-era, so you do not need to know which revision your
client speaks. How it opens decides, once.

| Client opens with | Adapter answers |
|---|---|
| per-request `_meta` at `2026-07-28` | modern envelope with `resultType` and cache directives; `server/discover` reports `supportedVersions: ["2026-07-28"]` |
| per-request `_meta` at a version it does not serve | `-32022` with `supported` and `requested`, so the client can retry |
| `initialize` naming a revision it speaks | that same revision, echoed |
| `initialize` naming one it does not | `2025-11-25`, which the client may refuse |
| `initialize` missing `protocolVersion`, `capabilities`, or `clientInfo.name`/`.version` | `-32602`; the era stays undetermined and the client may open again |

A legacy client opens with `initialize` and thereafter needs no
per-request metadata:

```sh
printf '%s\n%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}' \
  | gitseq-mcp --repo "$REPO" --actor bot 2>/dev/null
```

Once settled, the era does not move. `initialize` after modern traffic is
refused, a second `initialize` is refused rather than renegotiating the
version mid-stream, and `server/discover` is not offered on a legacy
connection because that revision cannot interpret its reply. A refused
opening never disturbs a session that is already working.

## Working without the resident

If the resident service is down, the durable tools — `status`, `wait`,
`state`, `ratify`, `supersede` — keep working directly against the local
log and report a `degraded` live cursor. `say` and `presence` fail rather
than pretend: ephemeral state does not survive, and the adapter will not
imply it did.

That is why the examples above work with no `--server` reachable. Start a
resident when you want presence, conversation and the live view.

## See also

- [MCP reference](../reference/mcp/) — one page per tool.
- [Deploy a resident](deploy-a-resident.md)
