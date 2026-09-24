---
title: gs reassign-if-unclaimed
summary: Reassign one request only if nobody has claimed or completed it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
---

# `gs reassign-if-unclaimed`

Retires one open request and publishes its replacement as a guarded pair.
Use it when you read a request as unclaimed and want to change its addressee.

The guard concerns only this request. Unrelated durable events may land
between the two acts. A promise or direct completion on the old request
refuses the operation, as does an already-retired request. The replacement
names the exact guarded retirement and refuses if the commitment changed after
that retirement.

Staleness does not bar it. Nobody has yet claimed a request that went stale
through the retirement of a basis under it, which matches exactly the statement
this helper protects, and such a request, more than any other, likely needs a
new owner. It reassigns like any other.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The requester signing both acts. |
| `--to` | *(required)* | The replacement request's addressee. A name, `@name`, or fingerprint. |
| `--text` | *(required)* | The replacement request text. |
| `--conditions` | *(required)* | Observable conditions of satisfaction. |
| `--body` | | `key=value`, repeatable. Further fields for the replacement request. The replacement counts as a new request and states its own result here: `target_ref`, `target=inherit`, or `no_git_artifact=true`. `--to` and `--conditions` overwrite whatever this states. |
| `--retirement-text` | `retire unclaimed request before reassignment` | The reason for retiring the old request. |
| `--rests-on` | | An additional current basis for the replacement, repeatable. The command places its guarded retirement first automatically. |
| `--server` | | Submit through a resident sequencer. The repository's advertised resident serves as the default; `-` forces the local fold. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses any value other than a positive duration before it signs anything. |
| `--idempotency-key` | *(required)* | Stable base key. The command derives separate retirement and request keys so a retry can resume between acts. |
| `--cited-ok` | `false` | Record the caller's admission override and retire even though tracked documentation still names the old request. It does not change the fold's commitment guard. |

The command takes the old request as its one positional argument. Put every flag
before it.

The old request and every `--rests-on` value take a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier. The command resolves both acts of the pair against
one verified event set before it signs either.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name first --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name second --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
OLD=$(gs state --repo "$REPO" --as alice --kind request --text 'Check the release' \
  --body to=@first --body conditions='the release is checked' \
  --body no_git_artifact=true --rests-on "$SEED")

gs reassign-if-unclaimed --repo "$REPO" --as alice --to @second \
  --text 'Check the release' --conditions 'the release is checked' \
  --body no_git_artifact=true \
  --rests-on "$SEED" --idempotency-key release-check-reassignment "$OLD"
```

The command signs the replacement request as
`workroom/reassign-if-unclaimed@1`, under which the fold reads its stated result
exactly as it reads a `workroom/state@3` request. The replacement inherits
nothing from the retired request: a replacement of a legacy request must say in
its own words what it owes.

The pair consists of two acts in order, so a refusal the replacement earns
after the retirement has landed would leave the old request withdrawn with
nobody asked to do the work. Before appending the retirement, the command
therefore checks everything about the replacement that your stated values can
settle: a missing `--to`
or `--conditions`, an address current custody does not hold — a performer
since retired included — a reserved admission field, a missing or
doubled result, a `target_ref` outside `refs/heads/`, one naming a ref that
does not resolve here, a supplied `target_repo` or `target_head`, and an
`--idempotency-key` already spent on some other act. Each of those refuses with
nothing appended and the old request still open. What nobody can know then
stays where it belongs: the check of the guard on the old request — no admitted
promise, no direct completion, no prior retirement — runs at each act's append,
against the frontier that act actually joins.

The command prints JSON containing the retirement and replacement request event
identifiers. If the first act lands and the second loses a race, the error names
the retirement. Re-read the old request, then retry the exact command only when
the guard still describes what you intend. An exact retry replays the landed
prefix instead of appending it again. The command authors the replacement on
the same path as [`gs state`](state.md#retrying-a-request), so the log answers
its retry before anything reads a ref, and the preflight answers it the same
way: a key already holding a replacement counts as a retry only when the whole
command — old request, words, bases and body — rebuilds to that accepted act,
in which case it replays even after the branch its `target_ref` named has gone
or the performer it named has left the roster. The command refuses a reused key
that names a different old request, a different branch, or any other change as
a reused key before any retirement, rather than answering it with the accepted
replacement or letting it withdraw a second request in its name.

## Deliberate withdrawal differs

This helper protects the statement “nobody claimed or completed this request.”
It places no general restriction on requesters. To deliberately cancel work that
someone already promised, use [`gs supersede`](supersede.md); ordinary
supersession keeps its existing authority and lifecycle meaning.
