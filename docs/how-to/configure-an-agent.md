---
title: Configure an agent
summary: Attach an MCP client to a workroom, and check that it can really act.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
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
neither names an identity.

More than one session may deliberately use the same actor. Durable records
attribute their work to that actor's fingerprint, not to an individual client
session. The adapter therefore allows the attachment, but prints a warning
naming how many other sessions are live. Reviews between those sessions carry
no independent force because review independence is enforced by fingerprint,
and the sessions can race on claims and leased presence. Give concurrent loops
distinct actors only when they need independent authority or review; a second
device, a planner and worker acting as one principal, or a replacement process
during the prior session's 30-second lease may reuse the actor intentionally.

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

When a resident is available, it mints a private credential for this adapter
and binds it to the selected repository and actor. The adapter keeps one such
credential per repository in process memory, renews it, and replaces it after
resident restart. It never places the credential in `whoami`, another MCP tool
result, a URL, a durable event, a log or a diagnostic. Do not ask an agent to
print or persist it; the MCP surface deliberately gives it no value to print.

Starting a resident accepts its boundary: trusted processes only, every
process inside this resident boundary can act as every actor key this
application can open. The resident prints that sentence next to its address on
every start. The credential above names one client's session so the resident
can renew and revoke it. It is not authentication between agents, and it does
not protect the keys from another process running under the same account:
anything running as that account can ask the resident to act as any actor
whose key the repository holds.

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
adapter will find it in the repository without being reconfigured. The
[deployment guide](deploy-a-resident.md) shows the invocation, the readiness
check, and the trust boundary you take on by running one.

## See also

- [MCP reference](../reference/mcp/) — one page per tool.
- [Deploy a resident](deploy-a-resident.md)
