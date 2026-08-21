---
title: gs inspect
summary: Read one exact durable event with its decision, commitment chain, direct bases and related review evidence.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1055e9d1a044c420c25d249f91c79988cfcda4d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0d87b56bb5146f67931203a41039e3d511ce503e
---

# `gs inspect`

Reads one exact event: the statement or act it is, the fold's decision on
it, the commitment chain it belongs to, the bases it rests on directly,
and the artifacts and reviews related to it.

It is the follow-up to a row from [`gs work`](work.md) or
[`gs artifacts`](artifacts.md) — one event, in full, without folding the
whole projection into a terminal. It is the same selection the MCP
[`inspect`](../mcp/inspect.md) tool makes, through the same code, and
`--json` prints the same shape.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--json` | `false` | Emit the inspection as JSON instead of the human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the verified local read if that fails. |

The event is a **positional argument**, and exactly one is required. It
must be a canonical event identifier; an abbreviation resolves to nothing.
See [Event identifiers](../event-identifiers.md).

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists' \
  --rests-on "$SEED")
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST")

gs inspect --repo "$REPO" "$PROMISE"
gs inspect --repo "$REPO" --json "$PROMISE" | head -8
```

## Reading it

Direct bases only. `provenance_bases` is one hop — what this event itself
cited — capped, with the number omitted reported beside it. Follow the
whole tree with [`gs provenance`](provenance.md), or ask which artifacts
a chain anchors to with [`gs artifacts --reaches`](artifacts.md).

The related artifacts and reviews are ranked: the event's own first, then
its commitment chain's, then the rest. Each list is capped and says how
many it left out.

## What it refuses

An event the durable projection does not hold is a refusal naming that
fact, not an empty inspection. An empty page for a mistyped identifier
would read like a real answer about a real event that happens to have
nothing on it.

## See also

- [`gs work`](work.md), [`gs artifacts`](artifacts.md), [`gs provenance`](provenance.md)
- [MCP `inspect`](../mcp/inspect.md)
