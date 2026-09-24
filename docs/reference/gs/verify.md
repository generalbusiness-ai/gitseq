---
title: gs verify
summary: Check every signature and the integrity of the sequence.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:328aa6777241e67d4b1a122ee45d4e4019eebd11
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:34c5f09e2f5bc4e4fa5acb7404ae9b7df4808e52
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ad5dd1bf5e0c2c325384f497ada3fdcda1b8fe52
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:52966895e59050b9a39308e6069ddb9ae7bd0c2e
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

On a log whose sequencer key nobody has ever rotated, `Depth` and `Events`
match, and that match itself serves as a check: every commit on the
first-parent chain from genesis to head decoded as an event. A rotation counts
as a commit and not an event, so each one raises `Depth` above `Events` by one.

## What it establishes

- Each event's **actor signature** covers the signed intent, and the key
  matches the one the roster attributes to that actor.
- Each sequence commit's **sequencer signature** validates against the
  key current at that position. Genesis pins exactly one canonical
  `ssh-ed25519` key, with no options, principals, comments or extra
  lines, so a genesis carrying an injected second key cannot validate an
  attacker-signed event.
- Where someone has **rotated** the sequencer key, the audit carries the
  current key forward as it walks. The key a rotation replaces must itself
  sign that rotation, and the audit refuses a commit signed under a retired
  key from the rotation point onward. Rotation commits count in the depth but
  do not count as events, which explains why `Depth` can exceed `Events` on a
  rotated log.
- Each event occupies the commit it claims, with matching envelope,
  causal trailers and payload tree.
- The signed envelope, the inline payload and every attachment together
  fit within the workroom's ceiling. The ceiling covers all three as one
  total, not each of them separately.
- The verified head and depth do not move away from the last frontier
  recorded in this repository's Gitseq config, and never move behind it. Any
  verified read, including an explicit full audit, advances that local marker
  before it returns data from a newer head. A read at the unchanged head
  reuses the marker without rewriting the config. Gitseq admits a read that
  finishes at a head shorter than the marker only where the authoritative ref
  still stands on the recorded head or continues it, and the read's own head
  sits as that recorded head's ancestor at exactly the depth between them:
  another appender overtook that read, so it returns the world it verified and
  leaves the marker where the appender put it. Gitseq refuses anything
  else — a ref that moved back, a sibling line, a depth the distance does not
  bear out — as a rollback, and the refusal names which test failed. If the
  sequence advances but the read cannot write `.git/gitseq`, it fails closed
  and leaves the old marker in place.

It performs an **explicit full audit**. It never consults a resident's
checkpoint cache, however recent that cache, because the command exists
to depend on nothing but the repository in front of
you.

## What it does not establish

`verify` answers *does this record hold together internally, carry correct
signatures, and continue the frontier this repository already verified?* It
does not answer *does everyone else have this same record?*

A repository that has already verified one branch refuses a non-descendant
branch, and a shorter one unless its authoritative ref still continues the
head that repository recorded — the case where an appender overtook a long
read, rather than the read pointing at a rewound sequence.
A first-time auditor has no such local memory: two fresh
copies that share a genesis but receive different internally valid branches
can each verify their first branch. Publication constrains this in practice —
a sequence only advances, so a push that Git refuses means the remote holds
something you do not — but detecting first-contact equivocation requires a
witness or trusted checkpoint.

It also says nothing about whether the fold judged an act **effective**,
whether an authority remains live, or whether a document has gone stale.
Signatures pose one question; the fold's verdicts pose another. Use
[`gs status`](status.md) for those.

## Cost

A full audit scales linearly with the depth of the sequence, and signature
checking makes up the expensive part. On a long history it does not finish
instantly, and it does not suit a loop — the resident's checkpointed restart
serves that purpose.

## See also

- [`gs attach`](attach.md), [`gs status`](status.md)
- [Publish and audit](../../how-to/publish-and-audit.md)
