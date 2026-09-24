---
title: gs provenance
summary: Walk back from one event through everything it rests on.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# `gs provenance`

Prints the transitive basis tree of one event: what it rests on, what
those rest on, and so down to the seed.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |

The command takes the event as a **positional argument**, and requires exactly
one. It takes a [short reference](../event-identifiers.md#typing-one-at-a-boundary)
as well as the canonical identifier, and resolves it against the projection it
has already folded. This command records nothing.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
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

gs provenance --repo "$REPO" "$REPORT"
```

```text
git:sha1:…#git:sha1:<report>
  git:sha1:…#git:sha1:<promise>
    git:sha1:…#git:sha1:<request>
      git:sha1:…#git:sha1:<seed>
```

## Reading it

Indentation shows depth. The command prints an event reached by more than
one path once in full and then marks it `(already shown)`, so the output
stays finite on a graph that does not form a tree.

This works in a fresh clone with no service and no local history beyond
the fetched sequence. An auditor uses this command to ask *what does this
claim stand on?*

## One event, every hop

The walk misses nothing and runs per event: it starts where you point it and
follows every basis it can resolve. It does not filter by kind, so it shows
an artifact's chain and a request's chain the same way.

[`gs artifacts --reaches <path>`](artifacts.md) answers the population-wide
version of the same question — *which artifacts still anchor to this path,
however many hops away*. It follows artifact
provenance only, and answers about every artifact in the log at once
rather than about one event.

## What it does not tell you

It reports structure, not judgement. A basis appearing here does not show
that it had effect, liveness, or relevance — only that the author cited it.
Cross-read with [`gs status`](status.md) for verdicts and staleness.

A basis that does not resolve simply does not appear. That describes the failure
mode of a mistyped citation: the act stands, and its intended support
silently goes missing. See
[Event identifiers](../event-identifiers.md).

## See also

- [`gs status`](status.md), [`gs verify`](verify.md)
- [`gs artifacts`](artifacts.md), [`gs inspect`](inspect.md)
- [Publish and audit](../../how-to/publish-and-audit.md)
