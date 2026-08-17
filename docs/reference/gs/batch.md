---
title: gs batch
summary: Append an ordered chain of durable acts, loading and verifying the log once.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9ce1c5c256729043ed29e41058e4e6ffb1085229
---

# `gs batch`

Appends an ordered chain of acts in one process, signed as one actor.

Every other durable subcommand loads and verifies the whole log before it
appends, so a chain filed one command at a time pays that cost once per
act. `batch` pays it once for the chain: it opens the workroom, verifies
the log, and then appends every act against that one frontier.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor signing every act in the chain. |
| `--server` | | Forward each act to a resident sequencer instead of writing locally. |
| `--cited-ok` | `false` | Allow a `supersede` act whose target tracked documentation still names. Without it the whole batch is refused before its first append, and the pages are listed. |

The one positional argument is the file to read. `-`, or no argument at
all, reads standard input.

## The input

A JSON array of acts. Each entry carries an optional `label`, a `verb` of
`state`, `ratify` or `supersede`, and that verb's usual fields: `kind`,
`text`, `body`, `rests_on`, `target` and `idempotency_key`. Unknown
fields are refused.

```text
[
  {"label": "req", "verb": "state", "kind": "request", "text": "Add a changelog",
   "body": {"to": "@bot", "conditions": "CHANGELOG.md exists"},
   "rests_on": ["git:sha1:<genesis>#git:sha1:<event>"],
   "idempotency_key": "changelog-request"},
  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will add it",
   "rests_on": ["$req"], "idempotency_key": "changelog-promise"}
]
```

A later act cites an earlier act of the same chain as `$label`, in
`rests_on` or in `target`, and the label resolves to the identifier
minted for that act. The whole file is parsed and every reference checked
before the first append, so a malformed entry, a duplicate label, or a
label that is unknown or defined later lands nothing.

The array must be the whole input. Anything after it other than
whitespace — a stray `]`, a second value — is refused before the first
append.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

cat > "$REPO/chain.json" <<JSON
[
  {"label": "req", "verb": "state", "kind": "request", "text": "Add a changelog",
   "body": {"to": "@bot", "conditions": "CHANGELOG.md exists"},
   "rests_on": ["$SEED"], "idempotency_key": "changelog-request"},
  {"label": "note", "verb": "state", "kind": "assert",
   "text": "the changelog convention is one entry per release",
   "rests_on": ["\$req"], "idempotency_key": "changelog-note"}
]
JSON

gs batch --repo "$REPO" --as alice "$REPO/chain.json"
```

## The report

`batch` prints one JSON report naming, for every act in the chain, its
position, its label, the event it minted, and its outcome.

| Outcome | Meaning |
|---|---|
| `landed` | Appended by this run. |
| `replayed` | Its idempotency key matched an act already in the log; nothing was appended. |
| `failed` | This act was refused. |
| `skipped` | The run stopped before reaching this act. |

A failure adds a typed `error` and exits nonzero, so the report says
exactly which acts landed and which did not.

## It is not atomic

Events are commits on `refs/seq/<genesis>`, and the kernel owns the whole
write for each one: envelope and actor signature checks, the payload
ceiling, the admission hook, the dedup index, sequencer signing, and the
compare-and-swap that publishes the commit. Building a chain of commits
outside that path, so the ref could move once, would mean repeating those
checks where the kernel cannot enforce them.

Per-act idempotency keys carry the recovery instead. Rerunning the same
file replays the prefix that already landed, without duplicating it, and
continues from the first act that did not. Acts given no idempotency key
are not resumable and land afresh.

## Through a resident

`--server` forwards the same signed requests to the resident sequencer,
one at a time. That server holds the single verified frontier, and batch
semantics stay per-act exactly as they are locally.

## See also

- [`gs state`](state.md), [`gs ratify`](ratify.md),
  [`gs supersede`](supersede.md)
- [Event identifiers](../event-identifiers.md)
