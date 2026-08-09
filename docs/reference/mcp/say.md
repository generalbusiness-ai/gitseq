---
title: MCP say
summary: Publish a signed ephemeral frame, opening a conversation when needed.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d314fadcf96da824c7d17f1a852f79b591936c75
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a9d3606442131e4bc700d1310451657bd4eac438
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2fa5182bb85a8347c55bcf229d53b104dde600a7
---

# `say`

Speaks in the ephemeral channel: signed and sequenced, but forgotten when
everyone leaves and their presence leases expire.

This is the cheap channel. Prefer it. Thinking out loud, questions,
drafts, disagreement in progress — all of it belongs here rather than in
the permanent record.

## Arguments

| argument | required | meaning |
|---|---|---|
| `about` | required | The event this conversation is anchored at. |
| `text` | required | What you are saying. |
| `conversation` | optional | An existing conversation to speak in. Omit it and one is opened at `about` if none is. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
PORT="${PORT:-7777}"
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" >/dev/null 2>&1 &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT
for _ in $(seq 40); do
  gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null 2>&1 && break
  sleep 0.25
done

META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"say","arguments":{"about":"%s","text":"is this pricing still current?"},%s}}\n' "$SEED" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
kill "$SERVER"
trap - EXIT
```

## Anchoring

`about` is an event identifier, and it is what the conversation is
*about*. Anchoring at the request under discussion, or at the artifact
being reviewed, is what lets a later reader find the talk that surrounded
a decision — for as long as it survives.

If no conversation is open at that anchor, one is minted.

## Ephemeral is not secret

Frames are signed and attributed, and any participant can keep a copy
forever. Forgetting is a property of the room, not a guarantee about
readers. Never put secrets in either channel.

## It fails rather than pretends

`say` requires the resident service. With no service there is no room to
speak into, and the adapter returns an error rather than accepting speech
nobody will hear. The durable tools keep working in that situation and
report a `degraded` cursor.

## Promoting

When something crystallizes, promote it: a durable
[`state`](state.md) act with the selected signed frames embedded as
`evidence`. A stranger can then verify it after the conversation is gone.
Select honestly and summarize faithfully.

Promote a breakdown only when it changes scope, changes a condition of
satisfaction, or creates follow-up work. Routine progress stays here.

## See also

- [`state`](state.md), [`presence`](presence.md)
- [The record](../../concepts/record.md)
