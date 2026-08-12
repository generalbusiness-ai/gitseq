---
title: MCP supersede
summary: Attempt to retire an act and propagate staleness.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# `supersede`

Retires one act and marks everything resting on it stale, transitively.
Nothing is deleted; the retired act stays in the log.

Prefer supersession to contradiction. It leaves the earlier position
standing, with a pointer to what replaced it.

## Arguments

| argument | required | meaning |
|---|---|---|
| `target` | required | The event identifier to retire. |
| `text` | required | Why. This is what a later reader gets. |
| `rests_on` | optional | Additional event identifiers. The target is placed first automatically. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

`text` is required for a reason: a retirement with no stated cause tells
the next reader that something changed and nothing about what.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
CLAIM=$(gs state --repo "$REPO" --as alice --kind assert \
  --text 'The pricing decision holds until the next review' --rests-on "$SEED")
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"supersede","arguments":{"target":"%s","text":"the review happened and the pricing changed"},%s}}\n' "$CLAIM" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
gs status --repo "$REPO"
```

## When to use it

**Replacing an artifact.** Record the new artifact and supersede the
previous one for the same path in the same step. That supersession is
what makes documents describing the old implementation flare; skip it and
the projection reports **succession not recorded**.

**Withdrawing your own request.** Whoever promised it is released, and
their promise stays in history as kept faith.

**Reneging.** Superseding your own promise is visible forever. Do it as
early as you know you cannot keep it.

**Changing governance.** The founding operator seed cannot be retired.
Other roster changes require current authority for their target. An
operator grant, or membership carrying a live or dormant operator grant,
requires a current `operator`; ordinary authorship of an older event is
not enough.

## The first-basis rule

A supersession must cite its target as the **first** basis. The adapter
puts it there; anything in `rests_on` follows.

## Reversible

Superseding a supersession restores the earlier act, and everything that
went stale because of it becomes current again. Liveness moves;
decisions do not. Governance restoration checks target-class authority
again at restoration time.

## See also

- [Staleness](../../concepts/staleness.md)
- [`gs supersede`](../gs/supersede.md)
