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
| `--server` | advertised resident, if any | This command has no resident write path and refuses a URL. Pass `-` to choose the local fold. |

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

Both events matter. The fold treats a membership grant as live when it
treats the grant statement as live **and** at least one effective
ratification of it as live.

## Kind does not confer authority

`kind` describes the nature of a principal. It confers nothing. An agent with a
`ratifier` grant may ratify; a human without one may not. To give
authority, use [`gs role-grant`](role-grant.md).

## Custody

The command writes the private key under `.git/gitseq/actors/` in
**this** repository. That lets the resident service sign for this actor,
and explains why the service binds loopback only.

A principal can exist on the roster without this repository holding its
key — the normal case for a clone. `gs actors` reports custody
separately from roles.

## See also

- [`gs actors`](actors.md), [`gs role-grant`](role-grant.md)
- [Actors and authority](../../concepts/actors.md)
