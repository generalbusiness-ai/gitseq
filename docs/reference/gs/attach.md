---
title: gs attach
summary: Fetch into separate tracking refs and import a verified sequence into a clone.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
---

# `gs attach`

Makes an ordinary clone able to read a workroom: installs the
remote tracking rule, fetches the sequence, verifies it, and advances the
authoritative local ref only if its history continues the clone’s prior state.

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
| Adds `+refs/seq/*:refs/remotes/<name>/seq/*` to `remote.<name>.fetch` | Fetch and push may update remote observations without replacing the authoritative sequence. |
| Removes both legacy `refs/seq/*:refs/seq/*` and `+refs/seq/*:refs/seq/*` rules | Either mapping can let a successful push overwrite a concurrent local append. Other configured mappings and values remain. |
| Fetches tracking refs with `--atomic --no-tags` | The fetched tracking updates succeed together; they are still untrusted. |
| Writes `.git/gitseq/config.json` | Genesis, object format, read-only mode, and the last verified frontier. |

The tracking rule permits replacement because it records what the remote
currently advertises, including a rewind or sibling. Ordinary `git fetch`
updates only that observation. It does not import new workroom events.

`attach` fully verifies the immutable fetched head before changing
`refs/seq/<genesis>`. It checks continuation of both the authoritative ref
observed before fetching and the saved verified frontier. A final Git
compare-and-swap refuses if the authoritative ref changed meanwhile. A remote
rollback, sibling, invalid signature, failed validation or lost comparison
leaves the authoritative ref and saved checkpoint untouched.

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

Import and checkpoint persistence are two writes. If the ref comparison
succeeds and the later checkpoint write fails, `attach` reports that partial
state as an error: the verified head is installed and the prior checkpoint
remains. It never rewinds the ref to undo the import, since another append may
already follow it. Restore metadata write access and rerun `attach`. If the
local head advanced beyond the fetched candidate, preserve it and run
`gs verify` to audit and remember that actual head; an older remote candidate
still refuses. This is not a cross-store crash-atomic transaction.

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
fast-forward. Never configure a fetch destination under `refs/seq/*`: Git
also updates tracking destinations after a push, which can replace an append
admitted during that push. For an existing publisher with either old rule,
remove that exact rule and use `refs/remotes/<name>/seq/*` for tracking.
`attach` performs this migration for read-only clones.

## When it fails

| Symptom | Cause |
|---|---|
| A missing `refs/seq/...` ref | The sequence was never published. Push it, then rerun `attach` in the clone you already have — the clone is fine, only the refs were missing. |
| `attach` refuses a non-descendant authoritative sequence | The remote is older than or diverges from the local sequence. Preserve both and investigate; tracking data is not acceptance. |
| The authoritative ref changed during import | Another writer advanced it after the initial observation. Retry from the new position; the failed comparison changed no checkpoint. |
| A verified frontier rollback is refused | The fetched sequence is shorter than or does not continue the last sequence this clone verified. Keep the local config and investigate the remote. |
| The local rollback witness cannot advance | The verified ref may already have advanced, but `.git/gitseq` could not be updated. Preserve the ref and previous marker, restore write access and retry or audit as described above. |
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
