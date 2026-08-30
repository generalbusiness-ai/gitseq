---
title: gs verify
summary: Check every signature and the integrity of the sequence.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:328aa6777241e67d4b1a122ee45d4e4019eebd11
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:34c5f09e2f5bc4e4fa5acb7404ae9b7df4808e52
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ad5dd1bf5e0c2c325384f497ada3fdcda1b8fe52
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
---

# `gs verify`

Performs a full audit of the sequence: every actor signature, every
sequencer signature, every payload tree, and the commit chain from
genesis to head.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
gs state --repo "$REPO" --as alice --kind assert \
  --text 'a claim worth auditing' --rests-on "$SEED" >/dev/null

gs verify --repo "$REPO"
```

```text
{
  "Genesis": "55214fa4cdf843c1c3b2edd227cc2d73a8e48da7",
  "Head": "e1d43e8d9a24bf26a341d0d9309bc8d795de4510",
  "Depth": 14,
  "Events": 14
}
```

On a log whose sequencer key has never been rotated, `Depth` and `Events`
match, and that is itself a check: every commit on the first-parent chain
from genesis to head decoded as an event. A rotation is a commit and not
an event, so each one raises `Depth` above `Events` by one.

## What it establishes

- Each event's **actor signature** covers the intent that was signed, and
  the key is the one the roster attributes to that actor.
- Each sequence commit's **sequencer signature** validates against the
  key current at that position. Genesis pins exactly one canonical
  `ssh-ed25519` key, with no options, principals, comments or extra
  lines, so a genesis carrying an injected second key cannot validate an
  attacker-signed event.
- Where the sequencer key has been **rotated**, the audit carries the
  current key forward as it walks. A rotation must itself be signed by
  the key it replaces, and a commit signed under a retired key is refused
  from the rotation point onward. Rotation commits count in the depth but
  are not events, which is why `Depth` can exceed `Events` on a rotated
  log.
- Each event occupies the commit it claims, with matching envelope,
  causal trailers and payload tree.
- Payload sizes are within the workroom's ceiling.
- The verified head and depth do not move behind or away from the last
  frontier recorded in this repository's Gitseq config. Any verified read,
  including an explicit full audit, advances that local marker before it
  returns data from a newer head. A read at the unchanged head reuses the
  marker without rewriting the config. If the sequence advances but
  `.git/gitseq` cannot be written, the read fails closed and leaves the old
  marker in place.

It is an **explicit full audit**. It never consults a resident's
checkpoint cache, no matter how recent that cache is, because the point
of the command is to depend on nothing but the repository in front of
you.

## What it does not establish

`verify` answers *is this record internally sound and correctly signed, and
does it continue the frontier this repository already verified?* It does not
answer *is this the same record everyone else has?*

A repository that has already verified one branch refuses a shorter or
non-descendant branch. A first-time auditor has no such local memory: two fresh
copies that share a genesis but receive different internally valid branches
can each verify their first branch. Publication constrains this in practice —
a sequence only advances, so a push that Git refuses means the remote holds
something you do not — but detecting first-contact equivocation requires a
witness or trusted checkpoint.

It also says nothing about whether an act was **effective**, whether an
authority is live, or whether a document is stale. Signatures are one
question; the fold's verdicts are another. Use [`gs status`](status.md)
for those.

## Cost

A full audit is linear in the depth of the sequence, and the expensive
part is signature checking. On a long history it is not instant, and it
is not meant to be run in a loop — that is what the resident's
checkpointed restart is for.

## See also

- [`gs attach`](attach.md), [`gs status`](status.md)
- [Publish and audit](../../how-to/publish-and-audit.md)
