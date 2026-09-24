---
title: gs batch
summary: Append an ordered chain of durable acts, loading and verifying the log once.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
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
| `--server` | | Forward each act to a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses a value other than a positive duration before signing anything. |
| `--cited-ok` | `false` | Allow a `supersede` or `retire-if-unclaimed` act whose target tracked documentation still names. Guarded retirement signs this admission observation separately from its fold-enforced commitment guard. |
| `--no-preflight` | `false` | File the act without asking the fold what it would decide first. See [Refused before signing](#refused-before-signing). |

The one positional argument names the file to read. `-`, or no argument at
all, reads standard input.

## The input

A JSON array of acts. Each entry carries an optional `label`, a `verb` of
`state`, `ratify`, `supersede`, `retire-if-unclaimed`, or
`reassign-if-unclaimed`, and that verb's usual fields: `kind`, `text`, `body`,
`rests_on`, `target`, `retirement`, and `idempotency_key`. The command
refuses unknown fields.

```text
[
  {"label": "req", "verb": "state", "kind": "request", "text": "Add a changelog",
   "body": {"to": "@bot", "conditions": "CHANGELOG.md exists",
            "target_ref": "refs/heads/main"},
   "rests_on": ["git:sha1:<genesis>#git:sha1:<event>"],
   "idempotency_key": "changelog-request"},
  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will add it",
   "rests_on": ["$req"], "idempotency_key": "changelog-promise"}
]
```

`rests_on`, `target`, `retirement` and the recognized event fields of an
entry's `body` each take a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier. The command resolves the whole chain against one
verified event set before the first append, so a chain carrying a reference
that names no event lands nothing at all; it names every resolution on
standard error, leaving the report on standard output unchanged.

A later act cites an earlier act of the same chain as `$label`, in
`rests_on`, `target`, or `retirement`. A label names an act the batch has yet
to mint, so it does not count as an event reference: it passes the resolver
untouched and resolves to the identifier minted for that act. The command
parses the whole file and checks every reference before the first append, so a
malformed entry, a duplicate label, or an unknown label or one defined later
lands nothing.

The array must make up the whole input. The command refuses anything after
it other than whitespace — a stray `]`, a second value — before the first
append.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

cat > "$REPO/chain.json" <<JSON
[
  {"label": "req", "verb": "state", "kind": "request", "text": "Add a changelog",
   "body": {"to": "@bot", "conditions": "CHANGELOG.md exists",
            "target_ref": "refs/heads/main"},
   "rests_on": ["$SEED"], "idempotency_key": "changelog-request"},
  {"label": "note", "verb": "state", "kind": "assert",
   "text": "the changelog convention is one entry per release",
   "rests_on": ["\$req"], "idempotency_key": "changelog-note"}
]
JSON

gs batch --repo "$REPO" --as alice "$REPO/chain.json"
```

## Refused before signing

Before the first append, this command asks the fold what it would decide about
every act of the chain, and refuses the whole chain when the fold would not
rule any one of them effective. The refusal names the act by its position:

```text
gs: act 1: the fold would rule this act ineffective: artifact state requires body.path
fix: an artifact names one file: --body path=<file>, at the exact string the merge will publish
file it as written with --no-preflight
```

The fold judges the chain in order, in a fold built for the question and thrown
away. It judges each act against the world the acts before it would make, so it
resolves a `$label` to the identifier under which it will judge that act, and
judges an act resting on one against it: a promise on `$request` counts as
exactly as effective here as it will in the log. The projection this process
holds stays untouched.

[Refused before signing](state.md#refused-before-signing) states the rest of
the rule: the reason comes from the fold itself, the fold decides again per act at
sequencing, and `--no-preflight` files the chain as written.

The fold judges a retry per act, because a chain does not behave as one thing.
The log already has an act whose `idempotency_key` this actor already holds: the
fold does not judge it again, it already belongs to the world against which the
fold judges the rest of the chain, and the sequencer replays it or refuses its
key. Its label names that real event, so the acts behind it stand on what
actually landed. Everything else in the chain counts as new, and the fold judges
it as new. So a chain whose prefix landed and whose suffix remains fresh — the
ordinary way to resume a broken chain — replays the prefix and
holds the suffix to the same check any first filing gets, and an exact retry of
the whole chain replays whole and appends nothing.

## The report

`batch` prints one JSON report naming, for every act in the chain, its
position, its label, the event it minted, and its outcome.

| Outcome | Meaning |
|---|---|
| `landed` | Appended by this run. |
| `replayed` | Its idempotency key matched an act already in the log; the run appended nothing. |
| `failed` | `gs` refused this act. |
| `skipped` | The run stopped before reaching this act. |

A failure adds a typed `error` and exits nonzero, so the report says
exactly which acts landed and which did not.

## It does not run atomically

Each event lives as a commit on `refs/seq/<genesis>`, and the kernel owns the
whole write for each one: envelope and actor signature checks, the payload
ceiling, the admission hook, the dedup index, sequencer signing, and the
compare-and-swap that publishes the commit. Building a chain of commits
outside that path, so the ref could move once, would mean repeating those
checks where the kernel cannot enforce them.

Per-act idempotency keys carry the recovery instead. Rerunning the same
file replays the prefix that already landed, without duplicating it, and
continues from the first act that did not. Acts given no idempotency key
cannot resume and land afresh.

For guarded reassignment, use adjacent `retire-if-unclaimed` and
`reassign-if-unclaimed` entries. The replacement names both the old request in
`target` and the first entry in `retirement`:

```json
[
  {"label":"retirement", "verb":"retire-if-unclaimed",
   "target":"git:sha1:<genesis>#git:sha1:<old-request>",
   "text":"retire before reassignment", "idempotency_key":"move-retirement"},
  {"verb":"reassign-if-unclaimed",
   "target":"git:sha1:<genesis>#git:sha1:<old-request>",
   "retirement":"$retirement", "text":"Ask the next agent",
   "body":{"to":"@next-agent","conditions":"the work is complete"},
   "idempotency_key":"move-request"}
]
```

The fold checks the same signed tuple as the purpose-specific command. Batch
labels save callers from retyping the retirement event identifier; the fold allows
unrelated interleaving, while a promise or direct completion refuses.

## Through a resident

`--server` forwards the same signed requests to the resident sequencer,
one at a time. That server holds the single verified frontier, and batch
semantics stay per-act exactly as they do locally.

Each act waits under the same `--deadline`, so batching does not shorten the
wait for any one of them. When one act's wait expires, the batch stops there and
says so — and whether you may simply run the file again depends on the
whole prefix, not on the act that timed out. Rerunning replays what landed only
where every act up to the failure carries an `idempotency_key`; an act given
none lands afresh, so rerunning would append a second copy of it. The refusal
says which of those two cases applies, and names the positions that have no
key. [`gs state`](state.md) has the rest.

## See also

- [`gs state`](state.md), [`gs ratify`](ratify.md),
  [`gs supersede`](supersede.md),
  [`gs reassign-if-unclaimed`](reassign-if-unclaimed.md)
- [Event identifiers](../event-identifiers.md)
