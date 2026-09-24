---
title: gs ratify
summary: Confer force on a statement, if you hold the authority for that target.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
---

# `gs ratify`

Appends a ratification of one target event. The fold decides whether it
confers anything, from its signer and its target.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The ratifying actor. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses any value other than a positive duration before it signs anything. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |
| `--no-preflight` | `false` | File the act without asking the fold what it would decide first. See [Refused before signing](#refused-before-signing). |

The command takes the target event as a **positional argument**, and flag
parsing stops at the first positional. Put every flag before it, or the
command reads the flags after it as further arguments and fails.

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

Before signing anything, this command asks the fold what it would decide
about the ratification, and refuses when the answer falls short of effective: an
unknown target, a record the fold refused, a retired one, a kind nobody may
ratify, a role this actor does not hold.

```text
gs: the fold would rule this act ineffective: statement kind is not ratifiable
fix: that kind has no satisfier: an artifact is closed by an approved merge, a request by a promise, report or supersession
file it as written with --no-preflight
```

[Refused before signing](state.md#refused-before-signing) states the whole
rule: the reason comes from the fold itself, the fold decides again at
sequencing, `--no-preflight` files the act as written, and this command leaves
four cases to the fold with no refusal here — including an exact retry under
an `--idempotency-key` this actor already holds, which replays the accepted
event however far the world has moved since.

## Who may ratify what

Authority depends on the target:

| Target | Who confers force |
|---|---|
| A `report` | The **live requester** of the request the promise rests on. Nobody else. |
| An `assert`, `propose`, or governance statement | An actor holding `ratifier`. |
| A `roster` grant | An actor holding the target-class authority: `operator` for an operator grant, otherwise `ratifier`. |

Human or agent names an identity kind, not an authority test. An agent with
a live `ratifier` grant may ratify.

The beneficiary of an authority grant may neither author nor ratify that
grant. Membership grants fall outside this rule. Report satisfaction
also remains separate: only the originating requester may ratify a report.

An assigned implementation that reaches Git does not use this command for a
second completion judgement. Its exact-head artifact reports the work, and the
sealed approved merge closes the implementation commitment. The review
requester still explicitly ratifies the review approval before that merge.
Explicit reports for work that does not merge continue to use the rule above.

You never ratify your own report. The work loop exists for exactly that:
whoever asked judges satisfaction.

## Strictness

Only `ratify` refuses a surplus citation. Its `rests_on` must name the
target and nothing else, so nobody can dress it up as resting on anything
more.

## The record keeps attempts

An unauthorized ratification that reaches the log does not count as an error.
It lands, the fold judges it ineffective, and [`gs status`](status.md) lists
it under **Attempts**, permanently. Read current state before retrying; do not
retry blindly.

This command refuses most of those attempts before signing them, so fewer of
them reach the log at all — see
[Refused before signing](#refused-before-signing). The ones that still land
come from cases it did not judge: an act filed with `--no-preflight`, a retry
under a key this actor already holds, an act whose world moved between the
check and the sequencer, and an act filed by a surface that makes no such
check. The record of an attempt stays permanent either way.

## See also

- [`gs state`](state.md), [`gs review`](review.md)
- [The work loop](../../concepts/work-loop.md)
