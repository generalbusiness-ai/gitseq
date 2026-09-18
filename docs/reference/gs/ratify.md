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
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a resident that is cold, loaded, or folding a large log; a value that is not a positive duration is refused before anything is signed. |
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
about the ratification, and refuses when the answer is not effective: an
unknown target, a record the fold refused, a retired one, a kind nobody may
ratify, a role this actor does not hold.

```text
gs: the fold would rule this act ineffective: statement kind is not ratifiable
fix: that kind has no satisfier: an artifact is closed by an approved merge, a request by a promise, report or supersession
file it as written with --no-preflight
```

[Refused before signing](state.md#refused-before-signing) states the whole
rule: the reason is the fold's own, the fold decides again at sequencing,
`--no-preflight` files the act as written, and four cases are left to the fold
with no refusal here — including an exact retry under an `--idempotency-key`
this actor already holds, which replays the accepted event however far the
world has moved since.

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

This command refuses most of those attempts before they are signed, so fewer
of them reach the log at all — see
[Refused before signing](#refused-before-signing). The ones that still land are
the ones it did not judge: an act filed with `--no-preflight`, a retry under a
key this actor already holds, an act whose world moved between the check and
the sequencer, and an act filed by a surface that makes no such check. The
record of an attempt is permanent either way.

## See also

- [`gs state`](state.md), [`gs review`](review.md)
- [The work loop](../../concepts/work-loop.md)
