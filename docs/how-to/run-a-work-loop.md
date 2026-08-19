---
title: Run a work loop
summary: Claim work, publish its exact artifact, get it reviewed, and close it by merge or explicit report.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:94fcda5debd84534bcc09c45e4645f236f72d73e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f77c88ea142f5cb81dfda4d344279bb2c870a2f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1f97dca2d5321a4abbf2ea61450ce40d43867579
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0bc609f818e6f168c4c3e68c02089718a82bb01f
---

# Run a work loop

[End to end](end-to-end.md) walks the happy path once. This page is the
loop on its own, plus the variations you will hit: changes requested,
work you cannot finish, and work discovered halfway through.

## Setup used below

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name carol --kind agent >/dev/null
```

## Ask, claim, and publish the implementation

```sh
REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot \
  --body conditions='CHANGELOG.md exists and is reviewed at an exact head')

PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add the changelog' --rests-on "$REQUEST")
```

Address a request with `body.to`, as a configured name, `@name`, or a
fingerprint. The signed event stores the fingerprint, and the fold
requires it to identify a live roster actor.

`body.conditions` is required. A request without conditions of
satisfaction cannot be satisfied, so the fold refuses it.

Implement on a named branch, never on the integration branch:

```sh
git -C "$REPO" switch -q -c task/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" add CHANGELOG.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)

ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'Changelog implementation; make test and make vet pass' \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" \
  --rests-on "$PROMISE")
```

State the tests and conditions **actually** met in the artifact. It is both
the durable pointer to the exact head and the implementation report. A second
`ready-for-review` report would repeat those same facts.

## Review, and what happens when changes are requested

```sh
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review task/changelog at its exact head' --body to=@carol \
  --body conditions='confirm the changelog at the named head' \
  --body head="$HEAD_COMMIT" --body artifact="$ARTIFACT" \
  --rests-on "$ARTIFACT")

REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")

CHANGES=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict changes-requested \
  --text 'CHANGES-REQUESTED: the changelog has no entries')
gs ratify --repo "$REPO" --as bot "$CHANGES"
```

A `changes-requested` verdict returns the work to the implementer. Any
change to the head invalidates an approval, so after fixing it you record
a **new** artifact at the new head and ask for review again:

```sh
printf '# Changelog\n\n- Added a changelog.\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" commit -q -a -m "Give the changelog an entry

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)

ARTIFACT2=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'Changelog implementation at the repaired head' \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" \
  --rests-on "$PROMISE")

REVIEW_REQUEST2=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Re-review at the repaired head' --body to=@carol \
  --body conditions='confirm the repaired head' \
  --body head="$HEAD_COMMIT" --body artifact="$ARTIFACT2" \
  --rests-on "$ARTIFACT2")
REVIEW_PROMISE2=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will re-review' --rests-on "$REVIEW_REQUEST2")
APPROVAL=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT2" --promise "$REVIEW_PROMISE2" \
  --verdict approved --text 'APPROVED at the repaired head')
gs ratify --repo "$REPO" --as bot "$APPROVAL"
```

## Merge and close

```sh
git -C "$REPO" switch -q "$BASE"
gs merge --repo "$REPO" --as bot --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$APPROVAL" \
  --text 'Merge the approved changelog and make it available on main.'
```

The command records the merge, publishes the successor artifact, and retires
the covered candidate artifacts in one resumable batch. Its sealed receipt
also closes the implementation commitment. The approval was already ratified
before merge; no implementation report or ratification follows it.

## Work you cannot finish

Supersede your own promise, and say why:

```sh
ABANDONED=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will also rewrite the parser' --rests-on "$REQUEST")
gs supersede --repo "$REPO" --as bot \
  --text 'reneging: the parser rewrite needs a decision I cannot make' "$ABANDONED"
```

This is **reneging**, and it is visible forever. Do it as early as you
know. Early reneging is honourable; late reneging is not.

## Work discovered halfway through

Do not quietly widen the job. Create a child request resting on the
current request or promise, and implement that separately:

```sh
CHILD=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'The changelog needs a release-date convention' \
  --body to=@alice --body conditions='a stated convention, in the changelog' \
  --rests-on "$REQUEST")
gs state --repo "$REPO" --as bot --kind assert \
  --text 'Found while adding the changelog: no convention for release dates' \
  --rests-on "$CHILD" >/dev/null
```

An `assert` can preserve the evidence for a breakdown, but it is not a
substitute for the request that assigns the follow-up work.

## Check the loop

```sh
gs status --repo "$REPO"
```

## See also

- [The work loop](../concepts/work-loop.md) — why it is shaped this way.
- [`gs review`](../reference/gs/review.md),
  [`gs merge`](../reference/gs/merge.md)
