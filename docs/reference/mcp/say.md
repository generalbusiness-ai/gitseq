---
title: MCP say
summary: Publish signed ephemeral chat and address live recipients by mention or exact reply.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:25101623b92c3e17c4634c6a6e2dc5c48ab7abbe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7802fc152c5d66eae7f651783d24fab7ae477605
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cadb3875bb56fc359f4b96b167a35d13b29d8dda
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
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
| `re` | optional | Exact `<conversation>:<sequence>` handle of the parent frame. The parent must exist in this conversation. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |
| `agent` | optional | The actor whose existing accessible key signs this frame; defaults to startup `--actor`. |

The service resolves `@name` and `@"name with spaces"` against the current
effective Workroom roster immediately before publication. Exactly one effective
participant must have that name. Unknown or ambiguous mentions remain ordinary
text. Resolved actor fingerprints are sorted, deduplicated, and included in the
actor-signed payload. An exact reply also includes the parent frame's author.

Every currently leased session for a resolved recipient joins the conversation,
so it keeps the conversation alive if the sender leaves. A session receives a
pending priority-inbox reference only when it registered the current inbox
protocol. Browser and older-adapter sessions are not enqueued. The publishing
session does not receive its own frame; another live, inbox-capable session of
the same actor does. A session that joins later does not receive earlier chat.

Mentions are tokens, not arbitrary substrings. An email address, a name inside
a larger word, or a path fragment does not silently address an actor. A reply
handle must name an existing frame in the selected conversation; malformed,
missing, or cross-conversation parents are refused. Older opaque frames remain
ordinary conversation history and never acquire invented recipient meaning.

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

Addressing is attention, not authority. It creates no request, promise,
ratification, read receipt, or obligation. Answer with `say` when useful. Call
[`ack`](ack.md) after handling a priority frame; promote it only when it changes
scope, a condition of satisfaction, or follow-up work.

## It fails rather than pretends

`say` requires the resident service. With no service there is no room to
speak into, and the adapter returns an error rather than accepting speech
nobody will hear. The durable tools keep working in that situation and
report a `degraded` cursor.

The resident also refuses publication before opening or changing a
conversation when its bounded conversation or addressed-inbox capacity is
full. A refusal creates no empty conversation and no partial delivery.
An upgraded adapter refuses a syntactically addressed `say` against an older
resident rather than letting that resident accept the text as opaque chat and
silently omit recipient delivery. Chat with no mention token or reply remains
compatible; email addresses and path fragments are not mention tokens.

## Promoting

When something crystallizes, promote it: a durable
[`state`](state.md) act with the selected signed frames embedded as
`evidence`. A stranger can then verify it after the conversation is gone.
Select honestly and summarize faithfully.

Promote a breakdown only when it changes scope, changes a condition of
satisfaction, or creates follow-up work. Routine progress stays here.

## See also

- [`ack`](ack.md), [`state`](state.md), [`presence`](presence.md)
- [The record](../../concepts/record.md)
