---
title: MCP ratify
summary: Attempt to confer force on a statement; the fold decides authority.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a3cd3c438a2a5eaac579ddc22ccccde367a49177
---

# `ratify`

Appends a ratification of one target event. The word *attempt* in the
tool description means what it says: the fold, not you, decides whether it
confers anything.

## Arguments

| argument | required | meaning |
|---|---|---|
| `target` | required | The event to ratify. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter started in, or to its `--repo` when it started with one. |
| `agent` | optional | The actor whose existing accessible key signs this call; defaults to startup `--actor`. |

`target` takes a [short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as the canonical identifier, and the
result names what it resolved in a `resolved` field.

`ratify` takes no `rests_on`. It cites its target and nothing else, and
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
  --text 'Run the tests' --body to=@bot --body conditions='the result is in the report' \
  --body no_git_artifact=true --rests-on "$SEED")
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will run them' --rests-on "$REQUEST")
REPORT=$(gs state --repo "$REPO" --as bot --kind report \
  --text 'all tests pass' --rests-on "$PROMISE")
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ratify","arguments":{"target":"%s"},%s}}\n' "$REPORT" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## Who may ratify what

Authority depends on the target:

- a **report** takes ratification from the live requester of the request its
  promise rests on, and from nobody else;
- **assertions, proposals and governance statements** take ratification from
  an actor holding `ratifier`; an `operator` grant specifically requires a
  current `operator`.

Agent identity does not bar ratification. An agent with a live `ratifier`
grant may ratify; identity kind does not test authority.

The beneficiary of an authority grant may neither author nor ratify that
grant. This does not change report satisfaction: only the originating
requester may ratify a report.

An assigned implementation that merges has no post-merge implementation
ratification. Its exact-head artifact serves as its report, and the sealed
approved merge closes that commitment. The independent review approval still
requires explicit ratification before merge. Explicit reports for work that
does not merge keep the rule above.

Never ratify your own report. Whoever asked judges satisfaction.

## An attempt beyond your authority does not count as an error

It lands in the log, the fold judges it ineffective, and it stays visible
forever with its reason. By design, the log records what an actor tried as
well as what took effect.

So read the current state before retrying, and do not submit a variant of
an act that already landed.

Every successful write result includes `projected.verdict` with the fold's
ruling, including `effective`, plus `projected.reason` when the ruling explains
a refusal or dispute. This reports the decision after the record landed, not a
preview made by the adapter.

## See also

- [`state`](state.md), [`supersede`](supersede.md)
- [`gs ratify`](../gs/ratify.md)
