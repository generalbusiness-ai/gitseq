---
title: MCP state
summary: Append a durable attributed utterance.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d314fadcf96da824c7d17f1a852f79b591936c75
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:dd237f3445f2123f9c1db55af0aaec93f0b457ce
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1539075831e59cbc39fefdd6a4e800ba2c150208
---

# `state`

Appends one durable statement, signed as this session's actor. It is the
MCP counterpart of [`gs state`](../gs/state.md), and everything it
appends is permanent.

## Arguments

| argument | required | meaning |
|---|---|---|
| `kind` | required | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `text` | required | The statement, in plain language. |
| `rests_on` | required | Array of event identifiers. What this act bears on. |
| `body` | optional | String map of structured fields. |
| `evidence` | optional | String map of `name` to content, embedded as attachments. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

`rests_on` is required by the schema — an act citing nothing is almost
always a mistake, and requiring the field makes that a decision rather
than an omission. It may still be an empty array, and then nothing can
ever make the act stale.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"state","arguments":{"kind":"request","text":"Add a changelog","body":{"to":"@bot","conditions":"CHANGELOG.md exists"},"rests_on":["%s"]},%s}}\n' "$SEED" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## Body fields the fold reads

Read `status.durable.vocabulary.definitions` before choosing a kind: that
catalog is the source of truth for required fields, basis constraints and
ratification authority. The structural fields you will meet most are:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer, as a name, `@name`, or fingerprint. The signed event stores the fingerprint, and the fold requires it to identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

Implementation requests, promises and reports may carry `branch` and
`head` as advisory checkout hints. They claim nothing about that checkout
being clean or current.

## Evidence

`evidence` is a map of name to content, embedded as attachments in the
signed payload. This is how a promotion from ephemeral conversation stays
verifiable after the conversation is gone. Embedded bytes count against
the [payload ceiling](../limits.md).

## Idempotency

Pass `idempotency_key` when you might retry. A replay reports that your
act already landed; do not then submit a variant.

## Without a resident

`state` keeps working against the local log and marks the result
`degraded`. The act is real and permanent either way; what you lose is
the sequencer's coordination with other writers.

## See also

- [`ratify`](ratify.md), [`supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
