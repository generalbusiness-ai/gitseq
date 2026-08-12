---
title: gs state
summary: Append a durable, attributed utterance.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:dd237f3445f2123f9c1db55af0aaec93f0b457ce
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1539075831e59cbc39fefdd6a4e800ba2c150208
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:62b994a7172b30964ca5e659602b18dbe46ee06d
---

# `gs state`

Appends one durable statement, signed as the named actor, and prints its
event identifier and nothing else.

This is the general-purpose durable command. `ratify` and `supersede` are
the only two acts it cannot make.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The signing actor. |
| `--kind` | *(required)* | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `--text` | *(required)* | The statement itself, in plain language. |
| `--body` | | `key=value`, repeatable. Structured fields. |
| `--rests-on` | | An event identifier, repeatable. What this act bears on. |
| `--evidence` | | `name=path`, repeatable. Files embedded as attachments. |
| `--server` | | Submit through a resident sequencer instead of writing locally. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' \
  --body to=@bot --body conditions='CHANGELOG.md exists' \
  --rests-on "$SEED")

gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST"
```

## Body fields the fold reads

Most of `body` is free-form and means whatever the room's practice says.
A few fields are structural, because the room's declared vocabulary
requires them — read `status.durable.vocabulary.definitions` for the
catalog in force rather than trusting this list to be complete:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer: a configured name, `@name`, or fingerprint. The signed event stores the fingerprint, and it must identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

Implementation requests, promises and reports may also carry `branch` and
`head` (or `commit`) as advisory hints, so a local tool can associate a
checkout. They claim nothing about that checkout being clean or current;
the `artifact` is the durable pointer.

## Citing

`--rests-on` is how an act says what it bears on. Copy identifiers whole
from the emitted event — see
[Event identifiers](../event-identifiers.md).

A statement with an empty `rests_on` is almost always wrong. It is
accepted, and then nothing can ever make it stale; the fold marks
artifacts in that state `unable to flare`.

Required edges, by kind:

- a `promise` needs one basis that is an effective `request`, **and** the
  signer must be the performer that request named;
- a `report` needs one basis that is an effective `promise`, signed by
  the promisor. Before anything is appended, filing checks the active
  vocabulary for exactly one effective promise-lifecycle basis and checks that
  its promisor is the report signer. An error tells the caller which rule the
  draft violates; the fold remains authoritative if the log moves meanwhile.

Anything else in `rests_on` is carried unchecked.

## Evidence

`--evidence name=path` embeds a file as an attachment, so a promotion
from conversation can be verified after the conversation is gone. Select
honestly and summarize faithfully; embedded bytes count against the
[payload ceiling](../limits.md).

## Retrying safely

Pass `--idempotency-key` with a stable value. A replayed submission
reports that the act already landed rather than appending a second one.
If you see a replay report, your act is in the log — do not submit a
variant.

## Local or through the resident

Without `--server`, the act is written straight to the local sequence.
With it, the act is submitted to a resident sequencer, which is what
makes concurrent appends from several actors safe. Both land in the same
sequence.

## See also

- [`gs ratify`](ratify.md), [`gs supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
