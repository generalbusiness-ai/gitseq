---
title: gs role-grant
summary: Grant a durable authority role, and ratify the grant.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# `gs role-grant`

Appends a `roster` statement conferring a role, and ratifies it. The
grant rests on the target's membership as its first basis.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The granting actor. Needs authority to grant. |
| `--actor` | *(required)* | Recipient, as a name, `@name`, or fingerprint. |
| `--role` | *(required)* | The role to confer, for example `ratifier`. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

gs role-grant --repo "$REPO" --as alice --actor bot --role ratifier
gs actors --repo "$REPO"
```

`bot` now shows `["participant", "ratifier"]`.

## When a grant confers

A non-membership grant is live only while three things hold: the grant
statement is live, at least one effective ratification of it is live, and
the **membership it named as its first basis** is live. The fold looks at
the first basis and nowhere else.

The ratification condition is a disjunction. One grant may be ratified
more than once, and any surviving ratification keeps the role, so
retiring one of two changes nothing.

Effectiveness and current authority are different questions.
`gs role-grant` records an act, and its verdict is settled forever;
whether the role is live now is answered only by
[`gs actors`](actors.md), and only for the moment you ask.

## Roles in use here

| Role | Confers |
|---|---|
| `participant` | Membership. Every other role rests on it. |
| `operator` | The founding authority. Carries `ratifier` with it. |
| `ratifier` | May confer force on statements, proposals and governance. |

Rooms may define others; the substrate does not know what they mean.

## See also

- [`gs role-revoke`](role-revoke.md), [`gs ratify`](ratify.md)
- [Actors and authority](../../concepts/actors.md)
