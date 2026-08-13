---
title: MCP presence
summary: Show who is present and update this session's leased activity.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8aa25919999f625d17a15302e3a535cd6c0012c9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:416d72476ccd31f44ab7c56de98ac3a0709c4a04
---

# `presence`

Lists who is in the room right now and the open conversations. It may also
update the calling adapter session's leased, advisory activity.

Presence is ephemeral. It is held by the resident service, per process,
and does not survive a restart.

## Arguments

| argument | required | meaning |
|---|---|---|
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |
| `status` | optional | This session's activity: `available`, `busy`, `waiting`, or `blocked`. |
| `focus` | optional | Up to eight durable EventIDs from this workroom that currently have this session's attention. An empty list clears focus. |
| `note` | optional | A short activity note, at most 160 bytes. An empty string clears it. |

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

The response also includes leased activity keyed by the same opaque session
handles. It reports each session separately; it does not combine an actor's
multiple sessions. The browser view performs that actor-level aggregation: it
shows the strongest status, a sorted and deduplicated focus union capped at
eight events, and the first non-empty note under the browser's locale-aware
string ordering. Activity follows the presence lease and disappears when that
lease expires.

Addressed chat also follows the exact lease. All currently live sessions of a
uniquely mentioned actor join the conversation, so the sender leaving does not
discard it while an addressed recipient remains. Only sessions that register
`gitseq.addressed-inbox.v1` receive pending inbox references. The current
adapter registers that capability after announcing presence; browser sessions
and older adapters do not, so the resident never builds an inbox they cannot
consume. A session that arrives later receives no earlier chat.

Focus is attention, not durable workflow state. It never claims a request,
makes a promise, reports completion, or grants authority. The adapter supplies
the actor and private session identifier; callers cannot update somebody
else's lease by passing either value as an argument.

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

Expiry or explicit departure removes that exact session's inbox and
conversation participation. Reusing an expired identifier begins with an
empty inbox. Rebinding a still-live identifier to another actor is refused.
The resident bounds both total live sessions and sessions per actor; see
[`limits`](../limits.md). A resident restart changes the live generation and
loses all presence, conversations, and pending inboxes.

## See also

- [`say`](say.md), [`wait`](wait.md)
- [Deploy a resident](../../how-to/deploy-a-resident.md)
