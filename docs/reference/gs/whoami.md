---
title: gs whoami
summary: Show the selected signing actor and every actor key held in this checkout.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# `gs whoami`

Shows which actor a CLI command would select from `--as` or
`GITSEQ_ACTOR`, and lists every actor whose private key this checkout holds.
It does not sign or append anything.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | `GITSEQ_ACTOR` | Resolve this actor for the answer. The command still lists custody when neither is set. |
| `--json` | `false` | Emit the identity and custody view as JSON. |

It takes no positional arguments.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

GITSEQ_ACTOR=bot gs whoami --repo "$REPO"
gs whoami --repo "$REPO" --as alice --json
```

The human view names the signing actor, how it was selected, whether the
actor is provisioned, and whether its key is in local custody. The custody
list also includes a surviving key for a retired actor and marks that actor
as retired, because that mismatch needs attention rather than concealment.

`gs whoami` reports identity; it does not grant it. `--as` and
`GITSEQ_ACTOR` select only a key already held by this checkout. Durable
membership and roles remain in the roster.

## See also

- [`gs actors`](actors.md), [`gs work`](work.md)
- [Actors and authority](../../concepts/actors.md)
