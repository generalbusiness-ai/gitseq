---
title: gs attach
summary: Fetch into separate tracking refs and import a verified sequence into a clone.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:617a0446bf89ef5ce8ccff6d095052d602d1dfc7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c4f0d4142bfa057005b09e59bc0a3462980842b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7d6f6997c01a89e509dec03f68fc6ba4fb4125fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4355a1feed949547209289deed2b1b8775f7f8ed
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fc8a6371f65aee6c713e5ddfe4accbf28d7be6bb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:865d9ef7fdfa7fd732f4f46ce1b389dc8dab17db
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5f5e861fe8e66e258c0b189c15de98b3e5beba0f
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
| Fetches the exact selected genesis into tracking with `--atomic --no-tags` | A deleted or missing remote sequence refuses instead of reusing a retained tracking value. |
| Writes `.git/gitseq/config.json` | Genesis, object format, read-only mode, and the last verified frontier. |

The tracking rule permits replacement because it records what the remote
currently advertises, including a rewind or sibling. Ordinary `git fetch`
updates only that observation. It does not import new workroom events.

`attach` fetches the exact selected remote ref; if it is absent, the command
refuses even when an older tracking value remains locally. It fully verifies
the immutable fetched head before changing
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

On a first attach, the read-only configuration is created exclusively before
the ref comparison. If that comparison loses, the configuration can remain
with its genesis and format but no verified frontier or signing custody. It is
not deleted on failure: another operation may already use it. Retry against
the actual local head. Concurrent creators keep the stored identity; a caller
that loses creation to a different genesis refuses.

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
