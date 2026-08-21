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

The event is a **positional argument**, and exactly one is required.

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
REPORT=$(gs state --repo "$REPO" --as bot --kind report \
  --text 'done' --rests-on "$PROMISE")

gs provenance --repo "$REPO" "$REPORT"
```

```text
git:sha1:…#git:sha1:<report>
  git:sha1:…#git:sha1:<promise>
    git:sha1:…#git:sha1:<request>
      git:sha1:…#git:sha1:<seed>
```

## Reading it

Indentation is depth. An event reached by more than one path is printed
once in full and then marked `(already shown)`, so the output stays
finite on a graph that is not a tree.

This works in a fresh clone with no service and no local history beyond
the fetched sequence. It is the command an auditor uses to ask *what is
this claim standing on?*

## One event, every hop

The walk is complete and it is per event: it starts where you point it and
follows every basis it can resolve. It does not filter by kind, so an
artifact's chain and a request's chain are shown the same way.

The population-wide version of the same question — *which artifacts still
anchor to this path, however many hops away* — is
[`gs artifacts --reaches <path>`](artifacts.md). It follows artifact
provenance only, and answers about every artifact in the log at once
rather than about one event.

## What it does not tell you

It reports structure, not judgement. A basis appearing here does not mean
it was effective, live, or relevant — only that the author cited it.
Cross-read with [`gs status`](status.md) for verdicts and staleness.

A basis that does not resolve simply does not appear. That is the failure
mode of a mistyped citation: the act stands, and its intended support is
silently absent. See
[Event identifiers](../event-identifiers.md).

## See also

- [`gs status`](status.md), [`gs verify`](verify.md)
- [`gs artifacts`](artifacts.md), [`gs inspect`](inspect.md)
- [Publish and audit](../../how-to/publish-and-audit.md)
