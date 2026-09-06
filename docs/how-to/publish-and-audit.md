---
title: Publish and audit
summary: Share the sequence, and verify it from a clone you did not create.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:617a0446bf89ef5ce8ccff6d095052d602d1dfc7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c4f0d4142bfa057005b09e59bc0a3462980842b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4355a1feed949547209289deed2b1b8775f7f8ed
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fc8a6371f65aee6c713e5ddfe4accbf28d7be6bb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:865d9ef7fdfa7fd732f4f46ce1b389dc8dab17db
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5f5e861fe8e66e258c0b189c15de98b3e5beba0f
---

# Publish and audit

The strongest check on a workroom is that a stranger with nothing but a
clone can confirm it: no service, no chat logs, no trust in you.

Both halves have to be arranged. Git ignores `refs/seq/*` on push and on
fetch, so an unpublished sequence looks exactly like a missing ref.

## Set up a workroom with something in it

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

gs state --repo "$REPO" --as alice --kind assert \
  --text 'The pricing decision holds until the next review' \
  --rests-on "$SEED"
```

## Publish

```sh
ORIGIN="$(mktemp -d)/origin.git"
git init -q --bare "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q origin "$BASE"
git -C "$REPO" push origin 'refs/seq/*:refs/seq/*'
```

The refspec has no leading `+`, deliberately. A sequence only ever
advances, so publishing is always a fast-forward. A push git refuses is
telling you the remote holds something your copy does not; forcing it
would rewind published history, and in a record whose whole purpose is
that positions are final, that is the one thing you must not be able to
do out of habit.

Keep fetch destinations separate from `refs/seq/*`, including in the
publisher. Git updates tracking refs after a successful push; mapping them
back onto the authoritative sequence can overwrite an append admitted during
the push. New remotes have no sequence fetch rule by default. Existing
read-only clones migrate through `attach`; remove either old direct mapping
from a writer and use `refs/remotes/<name>/seq/*` if it needs tracking.

Publish whenever you want others to see new events. It is the step that
makes the record shared rather than local.

## Audit

```sh
AUDIT="$(mktemp -d)/audit"
git clone -q "$ORIGIN" "$AUDIT"
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

`attach` fetches into `refs/remotes/origin/seq/*`, verifies the immutable
head, and imports it with a comparison against the prior local ref and saved
frontier. It removes both old rules that fetched directly into `refs/seq/*`,
preserving source-branch mappings and unrelated configuration.

Now read the record as an outsider:

```sh
gs verify --repo "$AUDIT"
gs status --repo "$AUDIT"
```

`verify` checks every actor signature, every sequencer signature, and the
integrity of the sequence, and reports the genesis, head, depth and event
count. It is an explicit full audit and never consults a resident's
cache.

This first audit proves the bytes and signatures the remote supplied. It
cannot prove that the remote supplied the latest authentic head: a remote
truncated to an older signed commit still looks internally valid to a clone
with no prior memory. Compare the reported head with a trusted checkpoint or
another witness when first-contact freshness matters.

Walk any event back through what it rests on:

```sh
EVENT=$(gs status --repo "$AUDIT" --json \
  | sed -n 's/.*"event": *"\([^"]*\)".*/\1/p' | tail -1)
gs provenance --repo "$AUDIT" "$EVENT"
```

## Fetching again later

Ordinary `git fetch` updates the remote tracking observation, including
remote rewinds. Run `attach` to verify and import newer events. It refuses a
rollback or sibling, invalid history, or a local ref that changed during
import, preserving the authoritative `refs/seq/*` ref and saved checkpoint:

```sh
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

Each successful verification also persists the signed head and depth in
`.git/gitseq/config.json`. Later verification refuses a shorter or sibling
sequence even if the authoritative ref was lost. Keep that config with the clone;
deleting it discards the clone's rollback memory.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `attach` reports a missing `refs/seq/...` ref | The sequence was never published. Push it, then rerun `attach` in the clone you already have. |
| `attach` refuses a non-descendant authoritative sequence | The remote no longer continues the local ref. Preserve both positions and investigate. |
| `attach` reports that the local rollback witness could not advance | A verified ref may be installed while the old checkpoint remains. Restore metadata writes, then retry or audit the actual local head; never rewind it. See the [failure boundary](../reference/gs/attach.md#what-it-changes-in-the-clone). |
| `attach` refuses a verified frontier rollback | The remote no longer continues the last head this clone verified. Preserve the clone and compare with another holder. |
| The clone warns that it is empty | Only `refs/seq/*` was pushed and no branch. Harmless for auditing. |

## Leaving

An attached clone is read-only unless local actor custody and a sequencer
endpoint are configured. Delete `.git/gitseq` and the extra `refs/seq/*`
fetch rule and you have an ordinary git repository.

## See also

- [`gs attach`](../reference/gs/attach.md),
  [`gs verify`](../reference/gs/verify.md),
  [`gs provenance`](../reference/gs/provenance.md)
