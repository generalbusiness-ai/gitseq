---
title: Configure an agent
summary: Attach an MCP client to a workroom, and check that it can really act.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66b6cb0b770fe88808130a195babf79fe1ea7746
---

# Configure an agent

`gitseq-mcp` is one process per client session, one actor per process. It
signs everything that session does as that actor.

## Register the command

Point your MCP client at:

```text
gitseq-mcp --actor bot
```

| Flag | Default | Meaning |
|---|---|---|
| `--actor` | *(required, or `GITSEQ_ACTOR`)* | A configured actor whose key the repository holds. |
| `--repo` | *(working directory)* | The default repository for calls that do not name one. |

There is no default actor name. When `--actor` is absent the adapter
reads the `GITSEQ_ACTOR` environment variable, and refuses to start if
neither names an identity — a copied setup can never silently attribute
one instance's work to a name that several instances share. Provision
each concurrent instance its own identity and set `GITSEQ_ACTOR` in
that instance's environment.

Register it **once**. The repository is a parameter of the call, not of
the installation: a call with no `repo` acts in the working directory the
adapter was started in, or in `--repo` when that was given, and any call
may name another repository instead. Linked worktrees of one repository
are one workroom, not several.

There is no service URL to configure. The adapter reads the address the
resident published in the repository it is acting in, and uses it only
when the genesis recorded with it matches that workroom. An address that
stops answering is forgotten and looked up again on the next call, so a
service started later is picked up without reconnecting the client.
`--server` is retired: passing it prints a notice and changes nothing.

The actor must exist and its key must be in the repository being acted
in — `gs actor-add` puts it there. A repository with no workroom, or one
where the actor is not configured, **fails that call and says so**. It
does not stop the adapter, because one installation serves many
repositories and one bad target should not strand the session.

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

Eleven tools come back: `whoami`, `presence`, `status`, `wait`, `work`,
`inspect`, `say`, `ack`, `state`, `ratify`, `supersede`. Every one of them
accepts an optional `repo`.

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
log and report a `degraded` live cursor. `work` and `inspect` make the
same bounded selection from a verified local snapshot and mark the
response `degraded: true`, and priority chat is marked unavailable. `say`,
`ack`, and `presence` fail rather than
pretend: ephemeral state does not survive, and the adapter will not
imply it did.

That is why the examples above work with no resident running at all.
Start one when you want presence, conversation and the live view; the
adapter will find it in the repository without being reconfigured.

## See also

- [MCP reference](../reference/mcp/) — one page per tool.
- [Deploy a resident](deploy-a-resident.md)
