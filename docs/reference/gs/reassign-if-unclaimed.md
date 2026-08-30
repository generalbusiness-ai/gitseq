---
title: gs reassign-if-unclaimed
summary: Reassign one request only if it is still fresh and nobody has claimed or completed it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
---

# `gs reassign-if-unclaimed`

Retires one open request and publishes its replacement as a guarded pair.
Use it when you read a request as unclaimed and want to change its addressee.

The guard is request-local. Unrelated durable events may land between the two
acts. A promise or direct completion on the old request refuses the operation,
as does a stale or already-retired request. The replacement names the exact
guarded retirement and refuses if the commitment changed after that retirement.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The requester signing both acts. |
| `--to` | *(required)* | The replacement request's addressee. A name, `@name`, or fingerprint. |
| `--text` | *(required)* | The replacement request text. |
| `--conditions` | *(required)* | Observable conditions of satisfaction. |
| `--retirement-text` | `retire unclaimed request before reassignment` | Why the old request is retired. |
| `--rests-on` | | An additional current basis for the replacement, repeatable. Its guarded retirement is placed first automatically. |
| `--server` | | Submit through a resident sequencer. The repository's advertised resident is the default; `-` forces the local fold. |
| `--idempotency-key` | *(required)* | Stable base key. The command derives separate retirement and request keys so a retry can resume between acts. |
| `--cited-ok` | `false` | Record the caller's admission override and retire even though tracked documentation still names the old request. It does not change the fold's commitment guard. |

The old request is the one positional argument. Put every flag before it.

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
  --body to=@first --body conditions='the release is checked' --rests-on "$SEED")

gs reassign-if-unclaimed --repo "$REPO" --as alice --to @second \
  --text 'Check the release' --conditions 'the release is checked' \
  --rests-on "$SEED" --idempotency-key release-check-reassignment "$OLD"
```

The command prints JSON containing the retirement and replacement request event
identifiers. If the first act lands and the second loses a race, the error names
the retirement. Re-read the old request, then retry the exact command only when
the guard still describes what you intend. An exact retry replays the landed
prefix instead of appending it again.

## Deliberate withdrawal is different

This helper protects the statement “nobody claimed or completed this request.”
It is not a general restriction on requesters. To deliberately cancel work that
someone already promised, use [`gs supersede`](supersede.md); ordinary
supersession keeps its existing authority and lifecycle meaning.
