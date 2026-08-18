---
title: MCP ratify
summary: Attempt to confer force on a statement; authority is decided by the fold.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:20e9622903b0b55e46955f625ee929212a076024
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:de8f9a5e18097414d9a96c259340d7ca876e11da
---

# `ratify`

Appends a ratification of one target event. The word *attempt* in the
tool description is accurate: whether it confers anything is the fold's
decision, not yours.

## Arguments

| argument | required | meaning |
|---|---|---|
| `target` | required | The event identifier to ratify. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |

There is no `rests_on`. `ratify` cites its target and nothing else, and
refuses any surplus citation — the one act in the system strict enough to
do so.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists' \
  --rests-on "$SEED")
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST")
REPORT=$(gs state --repo "$REPO" --as bot --kind report \
  --text 'done' --rests-on "$PROMISE")
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ratify","arguments":{"target":"%s"},%s}}\n' "$REPORT" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## Who may ratify what

Authority is specific to the target:

- a **report** is ratified by the requester of the request its promise
  rests on, and by nobody else;
- **assertions, proposals and governance statements** are ratified by an
  actor holding `ratifier`; an `operator` grant specifically requires a
  current `operator`.

Being an agent is not a bar. An agent with a live `ratifier` grant may
ratify; identity kind is not an authority test.

The beneficiary of an authority grant may neither author nor ratify that
grant. This does not change report satisfaction: only the originating
requester may ratify a report.

An assigned implementation that merges has no post-merge implementation
ratification. Its exact-head artifact serves as its report, and the sealed
approved merge closes that commitment. The independent review approval still
requires explicit ratification before merge. Explicit reports for work that
does not merge keep the rule above.

Never ratify your own report. Satisfaction is judged by whoever asked.

## An attempt beyond your authority is not an error

It is appended, judged ineffective, and stays visible forever with its
reason. That is the design: the log records what was tried as well as
what took effect.

So read the current state before retrying, and do not submit a variant of
an act that already landed.

## See also

- [`state`](state.md), [`supersede`](supersede.md)
- [`gs ratify`](../gs/ratify.md)
