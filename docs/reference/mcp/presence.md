---
title: MCP presence
summary: Show who is present and update this session's leased activity.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:25101623b92c3e17c4634c6a6e2dc5c48ab7abbe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7802fc152c5d66eae7f651783d24fab7ae477605
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:94cadf30855bd467e8b29a4529297c63eac4cb7b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:baf0fe4b855ba94003b7c663c343a8ac9089ea84
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
hex — not by its private credential.

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
the actor. On the first attachment, the resident mints a private credential
from 256 bits of system randomness and binds it to that actor and repository.
The adapter keeps it in process memory; callers cannot supply, read or update
somebody else's credential through the MCP tool.

That distinction is load-bearing. The private credential authorizes renewal,
speech, acts, inbox access and departure for one exact lease. Public handles
grant nothing, are not derived from the credential in either direction, and
are stable enough to follow a renewal or notice a departure. The credential
never appears in this tool's result or any other ordinary MCP result.

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
conversation participation. An expired, revoked, malformed, guessed,
cross-actor or cross-repository credential is refused with the same fixed
error. A new attachment receives a new credential and an empty inbox.
The resident bounds both total live sessions and sessions per actor; see
[`limits`](../limits.md). A resident restart changes the live generation and
loses all presence, conversations, pending inboxes and credentials. The adapter
remints on its next resident call without exposing the replacement.

## See also

- [`say`](say.md), [`wait`](wait.md)
- [Deploy a resident](../../how-to/deploy-a-resident.md)
