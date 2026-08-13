---
title: gs actors
summary: List principals, their current roles, and whether this repository holds their keys.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# `gs actors`

Prints every principal on the roster with the roles it holds **right
now**, and whether this repository holds its private key.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

gs actors --repo "$REPO"
```

```text
[
  {
    "name": "alice",
    "fingerprint": "571ad5a6…",
    "kind": "human",
    "roles": ["operator", "participant", "ratifier"],
    "custody": true
  },
  {
    "name": "bot",
    "fingerprint": "3868996c…",
    "kind": "agent",
    "roles": ["participant"],
    "custody": true
  }
]
```

## Reading it

**`roles` is current, not historical.** It answers what authority is live
at the moment you ask. Whether some past act was effective is a different
question, answered by the decisions in
[`gs status`](status.md). A grant can be effective, ratified, and confer
nothing today because a basis under it has been retired.

**`kind` grants nothing.** It says what a principal is.

**`custody` is local.** It means this repository holds the private key,
so this machine can sign as that actor. It is not durable state and says
nothing about the roster. An attached clone typically shows `custody:
false` for everyone.

**A retired principal is still listed.** Retiring a membership leaves the
principal on the roster with `retired: true` and an empty `roles`, because
the events it signed are permanent and dropping the row would leave those
signatures attributed to nothing. Read the flag, not the absence.

**`custody` and `retired` can disagree.** A retired principal whose key
file survives still shows `custody: true`. That is a local custody problem
this view is meant to make visible.

## See also

- [`gs role-grant`](role-grant.md), [`gs role-revoke`](role-revoke.md)
- [Actors and authority](../../concepts/actors.md)
