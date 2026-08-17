---
title: Publish and audit
summary: Share the sequence, and verify it from a clone you did not create.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9ce1c5c256729043ed29e41058e4e6ffb1085229
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9b34fc905db82c93fe54c49c7868a245cc4440eb
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

Publish whenever you want others to see new events. It is the step that
makes the record shared rather than local.

## Audit

```sh
AUDIT="$(mktemp -d)/audit"
git clone -q "$ORIGIN" "$AUDIT"
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

`attach` adds a non-forcing `refs/seq/*` fetch rule, fetches atomically,
and then verifies. If an older build left a forced rule behind, `attach`
replaces it first.

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

The rule `attach` installs is non-forcing, so ordinary `git fetch` and
later `attach` runs accept an initial fetch or a fast-forward and nothing
else. A remote that has rewound is rejected, and the auditor's existing
`refs/seq/*` frontier does not move:

```sh
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
```

Each successful verification also persists the signed head and depth in
`.git/gitseq/config.json`. Later verification refuses a shorter or sibling
sequence even if the tracking ref was lost. Keep that config with the clone;
deleting it discards the clone's rollback memory.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `attach` reports a missing `refs/seq/...` ref | The sequence was never published. Push it, then rerun `attach` in the clone you already have. |
| `git fetch` fails on a sequence ref | The remote rewound. Your frontier is intact; find out what happened upstream. |
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
