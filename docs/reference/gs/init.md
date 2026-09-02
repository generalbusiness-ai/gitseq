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

Run it once per repository. It is the only command that creates a
genesis, and genesis is immutable.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The ordinary git repository to overlay. |
| `--operator` | *(required, or `GITSEQ_ACTOR`)* | Name of the founding actor. There is no default name. |
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
it, and there is no way to discover it from a clone that has not fetched
the sequence.

## What it writes

| Path | Contents |
|---|---|
| `refs/seq/<genesis>` | The sequence. The seed is its first commit. |
| `.git/gitseq/config.json` | Genesis, object format, payload ceiling, actor list. |
| `.git/gitseq/actors/<name>.key` | Private keys. Local, never published. |

Your branches, tags and working tree are untouched.

## Choices that cannot be changed later

- **The payload ceiling.** Every reader validates against the value
  recorded in genesis, so raising it afterwards would invalidate the
  workroom. Choose deliberately if you expect large attachments.
- **The object format.** Taken from the repository.

Genesis also pins the **first** sequencer key: exactly one canonical
`ssh-ed25519` public key, key type, one space, base64 wire key, with no
options, principals, comments or extra lines. Creation and auditor
decoding share that validator, so a genesis carrying an injected second
key cannot validate an attacker-signed event. That key is not permanent
— it can be rotated in band, and readers carry the current key forward as
they audit. What rotation does and does not recover is in
[Limits](../limits.md).

## The operator

The founding actor holds `operator`, which carries `ratifier` with it —
there is no earlier ratifier available to grant one. The seed is the sole
grant that confers without a ratification.

Add everyone else with [`gs actor-add`](actor-add.md).

## See also

- [`gs actors`](actors.md), [`gs attach`](attach.md)
- [Limits](../limits.md)
