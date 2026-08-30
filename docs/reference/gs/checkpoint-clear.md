---
title: gs checkpoint-clear
summary: Clear both persistent checkpoint selectors so the next process performs a cold audit.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
---

# `gs checkpoint-clear`

Clears the two repository-local selectors for the signed verification
checkpoint. The next new process audits the sequence from genesis and, if it
has sequencer custody, publishes a fresh checkpoint.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs status --repo "$REPO" >/dev/null

gs checkpoint-clear --repo "$REPO"
```

```text
{
  "checkpoint": "cleared",
  "genesis": "55214fa4cdf843c1c3b2edd227cc2d73a8e48da7"
}
```

The command removes
`.git/gitseq/checkpoints/<genesis>.json` and rewinds
`refs/gitseq/checkpoints/<genesis>` to genesis. It does not change the durable
sequence, delete actor-signed events, or require an actor identity. Its JSON
result names the genesis and reports `"checkpoint":"cleared"`.

Stop a resident before running the command if that resident must restart cold.
The command cannot erase another process's already verified memory.

Set `GITSEQ_CHECKPOINT=off` on a command or resident process to disable both
checkpoint loading and checkpoint publication without changing either
persistent selector. [`gs verify`](verify.md) is always a cold audit and does
not need the switch.

The local Git ref is also the garbage-collection root for the checkpoint
object. The JSON file is only an application-owned selector. Neither is
authority: every selected object still passes the full checkpoint identity,
history, payload, key-rotation, and sequencer-signature checks.
