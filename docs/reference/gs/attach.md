---
title: gs attach
summary: Add a non-forcing sequence fetch rule to a clone, fetch, and verify.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d53a83b7b606df6a80335f6257d59a4093681dfc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9ed176d95eeb6777c0a3538cc8e400684184b68
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9b34fc905db82c93fe54c49c7868a245cc4440eb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:abc86d18c4f7b00f47fc2341e3ecf2217a97beb4
---

# `gs attach`

Makes an ordinary clone able to read a workroom: installs the
`refs/seq/*` fetch rule, fetches the sequence, writes the local config,
and verifies.

Run it in the clone, not in the repository that holds the workroom.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The clone to attach. |
| `--remote` | `origin` | The git remote to fetch the sequence from. |
| `--genesis` | *(required)* | The workroom's genesis hash. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')

ORIGIN="$(mktemp -d)/origin.git"
git init -q --bare "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q origin "$BASE"
git -C "$REPO" push -q origin 'refs/seq/*:refs/seq/*'

AUDIT="$(mktemp -d)/audit"
git clone -q "$ORIGIN" "$AUDIT"
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

It prints the same verification summary as
[`gs verify`](verify.md).

## What it changes in the clone

| Change | Why |
|---|---|
| Adds `refs/seq/*:refs/seq/*` to `remote.<name>.fetch` | Git ignores these refs otherwise. |
| Removes a legacy `+refs/seq/*:refs/seq/*` rule, if present | Older builds installed a forcing rule, which would let a rewind through. |
| Fetches with `--atomic --no-tags` | All or nothing. |
| Writes `.git/gitseq/config.json` | Genesis, object format, read-only mode, and the last verified frontier. |

The rule is deliberately non-forcing. Later attaches and ordinary
`git fetch` runs accept an initial fetch or a fast-forward and nothing
else. A rewound remote is rejected, and the local `refs/seq/*` frontier
does not move.

After verification succeeds, `attach` records the exact signed head and
depth in the local config. Later attaches preserve that marker. Verification
then requires the new signed sequence to contain the recorded commit at the
recorded depth. This catches a shorter or sibling sequence even if the local
`refs/seq/*` ref was deleted and Git therefore had no ref left to compare.
It also keeps a truncated sequence from making a previously spent idempotency
key look unused.

Later verified reads and explicit audits advance the marker before returning
data from a newer head. Reads at the unchanged head reuse it without rewriting
the config. The clone may be read-only for workroom acts, but `.git/gitseq`
must remain writable when the frontier advances. If it is not writable, the
read or audit fails closed and leaves the previous marker in place.

The marker is local memory, not a public witness. On the first attach there is
no earlier frontier to compare. An old but internally valid signed sequence
can therefore pass and become the first marker. Detecting that case requires a
trusted checkpoint or a witness that knows a later head. `attach` cannot
recover commits that the remote no longer provides.

Run it again whenever you want the newer events:

```sh
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

## The other half is a push

`attach` arranges the fetch side. Nothing arranges the push side, so
publishing stays a deliberate act in the repository that holds the
workroom:

```sh
git -C "$REPO" push origin 'refs/seq/*:refs/seq/*'
```

No leading `+`. A sequence only advances, so publishing is always a
fast-forward.

## When it fails

| Symptom | Cause |
|---|---|
| A missing `refs/seq/...` ref | The sequence was never published. Push it, then rerun `attach` in the clone you already have — the clone is fine, only the refs were missing. |
| The fetch is rejected | The remote rewound its sequence. Your frontier is untouched. |
| A verified frontier rollback is refused | The fetched sequence is shorter than or does not continue the last sequence this clone verified. Keep the local config and investigate the remote. |
| The local rollback witness cannot advance | The sequence advanced but `.git/gitseq` could not be updated. Restore write access to that local metadata; the previous marker remains trusted. |
| Verification fails | The sequence you fetched does not audit. Do not work around this. |

## Read-only

An attached clone is read-only unless local actor custody and a sequencer
endpoint are configured; `gs serve` refuses to serve one. Delete
`.git/gitseq` and the extra fetch rule and you have an ordinary git
repository back.

Checkpoint refs are local to a resident and are not fetched.

## See also

- [Publish and audit](../../how-to/publish-and-audit.md)
- [`gs verify`](verify.md), [`gs provenance`](provenance.md)
