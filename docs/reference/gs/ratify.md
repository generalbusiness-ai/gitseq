---
title: gs ratify
summary: Confer force on a statement, if you hold the authority for that target.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:de8f9a5e18097414d9a96c259340d7ca876e11da
---

# `gs ratify`

Appends a ratification of one target event. Whether it confers anything
is decided by the fold, from who signed it and what the target is.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The ratifying actor. |
| `--server` | | Submit through a resident sequencer instead of writing locally. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |

The target event is a **positional argument**, and flag parsing stops at
the first positional. Put every flag before it, or the flags after it are
read as further arguments and the command fails.

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

gs ratify --repo "$REPO" --as alice "$REPORT"
```

## Who may ratify what

Authority is target-specific:

| Target | Who confers force |
|---|---|
| A `report` | The **requester** of the request the promise rests on. Nobody else. |
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

An unauthorized ratification is not an error. It is appended, judged
ineffective, and listed under **Attempts** in
[`gs status`](status.md), permanently. Read current state before
retrying; do not retry blindly.

## See also

- [`gs state`](state.md), [`gs review`](review.md)
- [The work loop](../../concepts/work-loop.md)
