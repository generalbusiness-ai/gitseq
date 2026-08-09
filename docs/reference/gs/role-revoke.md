---
title: gs role-revoke
summary: Retire an explicit role grant, and everything derived from it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6918582b884b2f82fa7ab64242f40d12de845c39
---

# `gs role-revoke`

Supersedes the `roster` statement that granted a role. The role goes, and
so does every role that was riding on it.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | `operator` | The revoking actor. |
| `--actor` | *(required)* | Holder, as a name, `@name`, or fingerprint. |
| `--role` | *(required)* | The granted role to retire. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs role-grant --repo "$REPO" --as alice --actor bot --role ratifier >/dev/null

gs role-revoke --repo "$REPO" --as alice --actor bot --role ratifier
gs actors --repo "$REPO"
```

`bot` is back to `["participant"]`.

## Blast radius

Revoking retires the named grant **and every role derived from it**.
Revoking `operator` takes a principal from
`[operator, participant, ratifier]` to `[participant]`, because
`ratifier` was riding on `operator` and had no grant of its own.

An ordinary `ratifier` revocation looks narrow — `[participant,
ratifier]` to `[participant]` — but that is because nothing was derived
from it, not because revocation is narrow.

Retiring ratifications can also end a grant, but only by exhausting them:
the role survives until every live effective ratification of that grant
is retired.

## Reversible

Retirement can itself be retired. Superseding the supersession brings the
authority back, without anyone appending a new grant. "This grant confers
nothing" is a statement about right now, never a permanent fact.

To remove someone entirely, retire the **membership** instead: one
supersede, and every non-membership role that named it goes with it.

## See also

- [`gs role-grant`](role-grant.md), [`gs supersede`](supersede.md)
- [Actors and authority](../../concepts/actors.md)
