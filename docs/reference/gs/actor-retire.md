---
title: gs actor-retire
summary: Retire a principal's membership and delete its key from local custody.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
---

# `gs actor-retire`

Supersedes the `roster` statement that made the principal a member, and
deletes the actor's key from local custody — but only after the fold
judges the supersession effective. An ineffective attempt leaves both
membership and custody untouched.

The principal stays in the projection with `retired: true` and no
roles: the events it signed are permanent, so forgetting it would leave
those signatures attributed to nothing, and a reader must still be able
to tell it from a live actor. A retired principal cannot be addressed
by a request, cannot ratify, and cannot be granted a role. Retiring the
retirement returns the principal to membership, because liveness is
reversible and a verdict is not.

Retire an instance identity when its engagement ends.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The retiring actor. |
| `--actor` | *(required)* | Principal to retire, as a name, `@name`, or fingerprint. |
| `--server` | advertised resident, if any | This command has no resident write path and refuses a URL. Pass `-` to choose the local fold. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs actor-retire --repo "$REPO" --as alice --actor bot
gs actors --repo "$REPO"
```

The roster then lists `bot` with `retired: true` and no roles.

## See also

- [`gs actor-add`](actor-add.md)
- [`gs role-revoke`](role-revoke.md)
- [Actors and authority](../../concepts/actors.md)
