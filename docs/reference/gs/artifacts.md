---
title: gs artifacts
summary: Select artifacts by exact path, by lifecycle state, and by the anchor their provenance reaches.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1055e9d1a044c420c25d249f91c79988cfcda4d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0d87b56bb5146f67931203a41039e3d511ce503e
---

# `gs artifacts`

Selects artifact statements without fetching the whole projection, by
three things: the exact path they were recorded at, the lifecycle state
they are in, and whether their chain of artifact bases reaches an anchor.

The exact live-path selection uses the same page-building code and JSON page
shape as the MCP [`artifacts`](../mcp/artifacts.md) tool and the resident's
`/v0/artifact-query` route. Lifecycle and provenance selectors are CLI-only:
they do not widen either remote request contract.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--path` | | An exact artifact path. Repeat to name several; at most 20. |
| `--state` | `live` | Lifecycle state: `live`, `retired`, `succeeded`, or `all`. |
| `--reaches` | | Select artifacts whose chain of artifact bases reaches an artifact recorded at this exact path. |
| `--limit` | `20` | Page size, 1 to 50. |
| `--cursor` | | The opaque continuation from a previous page. |
| `--json` | `false` | Emit the page as JSON instead of the human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the verified local read if that fails. |

Either `--path` or `--reaches` is required. A query naming neither is the
request for every artifact in the log, and it is refused.

## Paths are exact strings

The projection keys artifacts by the path field alone: no normalising, no
prefix matching, no globbing. `internal/workroom` and
`internal/workroom/fold.go` are unrelated paths to it. Ask for the string
that was recorded, or the answer is empty.

## The four states

| `--state` | What comes back |
|---|---|
| `live` | Not retired. This is what a query naming no state receives. |
| `retired` | Superseded with no successor named: the pointer was withdrawn and there is nowhere to follow it to. |
| `succeeded` | Superseded by an act that rested on an artifact covering the same path: the pointer moved, and the log says where. |
| `all` | Every artifact at the selected paths, in any state. |

Live means **not retired**. Staleness is a different fact and does not
answer this question: a stale artifact still occupies its path and is
still the predecessor a successor has to retire. Every returned row
carries its own `succeeded`, `stale`, `retired` and
`describes_superseded_world` fields whatever state was selected, so a
row from an `--state all` query says which of the three lifecycles it is
in rather than leaving the reader to infer it.

`retired` says the pointer was withdrawn and `succeeded` says a successor was
named. Reading `retired` alone cannot tell a replaced artifact from a
withdrawn one,
which is the difference that matters to anyone standing on it: one says
where the behaviour went, the other says go and look.

## `--reaches`

`--reaches <path>` follows artifact provenance transitively. An artifact
resting on an artifact recorded at that path is selected, and so is one
resting on *that*, for as many hops as the chain has. This is the anchor a
document follows to say which behaviour it describes, so it answers "what
still points at this?" rather than "what cites it directly?".

Artifacts recorded at the anchor path itself are excluded. They are the
anchor, not something anchored to it.

Naming both `--path` and `--reaches` intersects them: rows must satisfy
both.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
COMMIT=$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")

MODULE=$(gs state --repo "$REPO" --as alice --kind artifact \
  --text 'the module stands here' \
  --body path=internal/thing --body commit="$COMMIT" --rests-on "$SEED")
PAGE=$(gs state --repo "$REPO" --as alice --kind artifact \
  --text 'the page describing the module' \
  --body path=docs/thing.md --body commit="$COMMIT" --rests-on "$MODULE")
gs state --repo "$REPO" --as alice --kind artifact \
  --text 'a guide resting on that page' \
  --body path=docs/guide.md --body commit="$COMMIT" --rests-on "$PAGE" >/dev/null

gs artifacts --repo "$REPO" --path internal/thing
gs artifacts --repo "$REPO" --reaches internal/thing
gs artifacts --repo "$REPO" --path docs/thing.md --state all --limit 5

gs supersede --repo "$REPO" --as alice --text 'withdrawn, no successor' \
  --cited-ok "$MODULE" >/dev/null
gs artifacts --repo "$REPO" --path internal/thing --state retired
gs artifacts --repo "$REPO" --path internal/thing --json | head -8
```

The guide comes back from `--reaches internal/thing` even though it rests
on the page and not on the module. A walk that stopped after one hop
would report it as unanchored.

## Reading it

The counts line gives the whole-log total beside what this page returned,
so a bounded answer says how much it left out. An unknown path is an
empty page rather than a refusal — nothing was ever recorded there, which
is an answer. A selector matching every artifact pages like any other.

## See also

- [`gs status`](status.md), [`gs provenance`](provenance.md), [`gs reviews`](reviews.md),
  [`gs supersession-plan`](supersession-plan.md), [`gs staleness-wave`](staleness-wave.md)
- [MCP `artifacts`](../mcp/artifacts.md)
