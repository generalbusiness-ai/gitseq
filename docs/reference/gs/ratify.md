---
title: gs ratify
summary: Confer force on a statement, if you hold the authority for that target.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
---

# `gs ratify`

Appends a ratification of one target event. Whether it confers anything
is decided by the fold, from who signed it and what the target is.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The ratifying actor. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |
| `--no-preflight` | `false` | File the act without asking the fold what it would decide first. See [Refused before signing](#refused-before-signing). |

The target event is a **positional argument**, and flag parsing stops at
the first positional. Put every flag before it, or the flags after it are
read as further arguments and the command fails.

The target takes a [short reference](../event-identifiers.md#typing-one-at-a-boundary): the `#N` record number a display prints, or
an unambiguous prefix or suffix of the event hash, as well as the canonical
identifier. The ratification signs the canonical identifier either way.

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

gs ratify --repo "$REPO" --as alice "$REPORT"
```

## Refused before signing

Before anything is signed, this command asks the fold what it would decide
about the act as written, and refuses when the answer is not effective:

```text
gs: the fold would rule this act ineffective: statement kind is not ratifiable
fix: that kind has no satisfier: an artifact is closed by an approved merge, a request by a promise, report or supersession
file it as written with --no-preflight
```

The reason is the fold's own, word for word. The line beneath it is the only
thing this boundary adds, and a reason nobody has written a line for prints
alone. Nothing has happened when this prints: no signing key has been read, no
act has been built, and the log stands where it stood.

It is advice, not authority. No rule lives here — the check folds the
prospective record with the same fold the sequencer runs, over the projection
this checkout last verified — and the fold judges the act again at sequencing,
against the world it actually joins. That second judgement is the one that
counts. When the question cannot be put honestly the command stays quiet and
files the act as it always did: a workroom this process cannot fold, or an act
whose shape this boundary does not build. `--server` is the case worth knowing:
the act joins the resident's frontier, so the check runs only while the resident
stands exactly where this checkout does, and is skipped otherwise.

Nothing about an admitted act changes. `--no-preflight` skips the check and
files the act exactly as written — for the deliberate replay of a shape the fold
refuses, or to record an attempt that should be visible as one.

A retry is judged as a fresh act. Whether the log already holds this act under
its `--idempotency-key` is a question only the signing key can answer, and the
check runs before that key is read, so an exact retry of an act the log already
accepted is judged against the world as it stands now. If that world has moved
under the act — the target retired since, the role that authorized it revoked
since — the retry is refused although the append would have replayed the
accepted event and changed nothing. `--no-preflight` is the escape, and the
replay is exactly the act it always was.

A malformed invocation is answered differently, and earlier still: an undefined
flag, a missing required flag, a subject given as a flag where a positional
argument belongs, or an event reference that names nothing here prints this
command's flags and one worked example, and exits non-zero with nothing touched.

## Who may ratify what

Authority is target-specific:

| Target | Who confers force |
|---|---|
| A `report` | The **live requester** of the request the promise rests on. Nobody else. |
| An `assert`, `propose`, or governance statement | An actor holding `ratifier`. |
| A `roster` grant | An actor holding the target-class authority: `operator` for an operator grant, otherwise `ratifier`. |

Human or agent is an identity kind, not an authority test. An agent with
a live `ratifier` grant may ratify.

The beneficiary of an authority grant may neither author nor ratify that
grant. Membership grants are separate from this rule. Report satisfaction
also remains separate: only the originating requester may ratify a report.

An assigned implementation that reaches Git does not use this command for a
second completion judgement. Its exact-head artifact reports the work, and the
sealed approved merge closes the implementation commitment. The review
approval is still explicitly ratified before that merge. Explicit reports for
work that does not merge continue to use the rule above.

You never ratify your own report. That is the point of the work loop:
satisfaction is judged by whoever asked.

## Strictness

`ratify` is the one act that refuses a surplus citation. Its `rests_on`
must be the target and nothing else, so it cannot be dressed up as
resting on anything more.

## Attempts are kept

An unauthorized ratification that reaches the log is not an error. It is
appended, judged ineffective, and listed under **Attempts** in
[`gs status`](status.md), permanently. Read current state before retrying; do
not retry blindly.

This command now refuses most of those attempts before they are signed, so
fewer of them reach the log at all — see
[Refused before signing](#refused-before-signing). The ones that still land are
the ones it could not judge: an act filed with `--no-preflight`, one whose world
moved between the check and the sequencer, and one filed by a surface that makes
no such check. The record of an attempt is permanent either way.

## See also

- [`gs state`](state.md), [`gs review`](review.md)
- [The work loop](../../concepts/work-loop.md)
