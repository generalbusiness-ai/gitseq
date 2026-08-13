---
title: gs actor-add
summary: Add a principal to the workroom and generate its key.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# `gs actor-add`

Generates a signing key for a new principal, records it locally, and
appends a ratified `roster` statement admitting it as a participant.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor performing the admission. Needs authority to grant membership. |
| `--name` | *(required)* | Name of the new principal. |
| `--kind` | `agent` | `human`, `agent`, or `service`. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null

gs actor-add --repo "$REPO" --as alice --name bot --kind agent
```

It prints the new actor and the two events it appended — the roster
statement and its ratification:

```text
{
  "actor": {"name": "bot", "fingerprint": "3868996c…", "key_file": "…/bot.key"},
  "events": ["git:sha1:…#git:sha1:232df7d2…", "git:sha1:…#git:sha1:9359e821…"]
}
```

Both events matter. A membership grant is live when the grant statement
is live **and** at least one effective ratification of it is live.

## Kind is not authority

`kind` describes what a principal is. It confers nothing. An agent with a
`ratifier` grant may ratify; a human without one may not. To give
authority, use [`gs role-grant`](role-grant.md).

## Custody

The private key is written under `.git/gitseq/actors/` in **this**
repository. That is what makes the resident service able to sign for this
actor, and it is why the service binds loopback only.

A principal can exist on the roster without this repository holding its
key — that is the normal case for a clone. `gs actors` reports custody
separately from roles.

## See also

- [`gs actors`](actors.md), [`gs role-grant`](role-grant.md)
- [Actors and authority](../../concepts/actors.md)
