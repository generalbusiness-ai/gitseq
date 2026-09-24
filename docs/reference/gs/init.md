---
title: gs init
summary: Create a workroom in an ordinary git repository.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:328aa6777241e67d4b1a122ee45d4e4019eebd11
---

# `gs init`

Creates a workroom over an existing git repository: generates the
sequencer key and the operator's actor key, writes genesis, and appends
the seed roster statement.

Run it once per repository. No other command creates a genesis, and
genesis never changes.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The ordinary git repository to overlay. |
| `--operator` | *(required, or `GITSEQ_ACTOR`)* | Name of the founding actor. It has no default name. |
| `--payload-ceiling` | `1048576` | Maximum bytes for a signed envelope plus its inline payload and attachments. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice
```

It prints the genesis hash, the operator actor, and the seed event:

```text
{
  "genesis": "55214fa4cdf843c1c3b2edd227cc2d73a8e48da7",
  "operator": {
    "name": "alice",
    "fingerprint": "571ad5a6…",
    "key_file": ".../.git/gitseq/actors/alice.key"
  },
  "seed": "git:sha1:55214fa4…#git:sha1:0b41bd09…"
}
```

Keep the genesis hash. Everyone who attaches to this workroom later needs
it, and a clone that has not fetched the sequence has no way to discover
it.

## What it writes

| Path | Contents |
|---|---|
| `refs/seq/<genesis>` | The sequence. The seed forms its first commit. |
| `.git/gitseq/config.json` | Genesis, object format, payload ceiling, actor list. |
| `.git/gitseq/actors/<name>.key` | Private keys. Local, never published. |

`gs init` leaves your branches, tags and working tree untouched.

## Choices you cannot change later

- **The payload ceiling.** Every reader validates against the value
  recorded in genesis, so raising it afterwards would invalidate the
  workroom. Choose deliberately if you expect large attachments.
- **The object format.** Taken from the repository.

Genesis also pins the **first** sequencer key: exactly one canonical
`ssh-ed25519` public key, key type, one space, base64 wire key, with no
options, principals, comments or extra lines. Creation and auditor
decoding share that validator, so a genesis carrying an injected second
key cannot validate an attacker-signed event. That key has no permanence
— you can rotate it in band, and readers carry the current key forward as
they audit. [Limits](../limits.md) describes what rotation does and does
not recover.

## The operator

The founding actor holds `operator`, which carries `ratifier` with it —
no earlier ratifier exists to grant one. The seed remains the sole grant
that confers without a ratification.

Add everyone else with [`gs actor-add`](actor-add.md).

## See also

- [`gs actors`](actors.md), [`gs attach`](attach.md)
- [Limits](../limits.md)
