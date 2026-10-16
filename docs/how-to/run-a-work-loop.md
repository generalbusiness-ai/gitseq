---
title: Run a work loop
summary: Claim work, publish its exact artifact, get it reviewed, and close it by merge or explicit report.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9635b67a3b4d7a4d31ff9c7b099214cea9cef7fd
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ade22d9c1efeb7dc0121631b32d38484a71cb7ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8f5434f1c274cda75b68d3e272a1c50877c63734
---

# Run a work loop

[End to end](end-to-end.md) walks the happy path once. This page is the
loop on its own, plus the variations you will hit: changes requested,
work you cannot finish, and work discovered halfway through.

## Setup used below

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
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
  --body conditions='CHANGELOG.md exists and is reviewed at an exact head' \
  --body target_ref=refs/heads/main)

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
git -C "$REPO" switch -q -c request/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" add CHANGELOG.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
test -s "$REPO/CHANGELOG.md"
git -C "$REPO" diff --check HEAD^

ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'CHANGELOG.md is nonempty; git diff --check HEAD^ passes' \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" \
  --rests-on "$PROMISE")
```

State the tests and conditions **actually** met in the artifact. It is both
the durable pointer to the exact head and the implementation report. A second
`ready-for-review` report would repeat those same facts.

## Work discovered halfway through

Do not quietly widen the job. Work that adds a separate outcome, or changes
the conditions, authority or performer, gets a child request resting on the
current request or promise. Implement that outcome separately. A review finding
that shows an existing condition unmet is a correction, covered below.

While adding this changelog, bot discovers that release dates need a convention
and asks alice to add it:

```sh
CHILD=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'The changelog needs a release-date convention' \
  --body to=@alice --body conditions='a stated convention, in the changelog' \
  --body target=inherit --rests-on "$REQUEST")
gs state --repo "$REPO" --as bot --kind assert \
  --text 'Found while adding the changelog: no convention for release dates' \
  --rests-on "$CHILD" >/dev/null
```

The child inherits the target branch and remains open for alice. An `assert`
can preserve the evidence, but cannot assign the follow-up work by itself.

## Review, and what happens when changes are requested

```sh
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review request/changelog at its exact head' --body to=@carol \
  --body conditions='confirm the changelog at the named head' \
  --body head="$HEAD_COMMIT" --body artifact="$ARTIFACT" \
  --body no_git_artifact=true --rests-on "$ARTIFACT")

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
a **new** artifact at the new head and ask for review again. The correction
stays on the same request and promise: the outcome, conditions, performer,
destination and authority are unchanged, so it needs no child request and
no transfer. Cite the finding in the new artifact's text:

```sh
printf '# Changelog\n\n- Added a changelog.\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" commit -q -a -m "Give the changelog an entry

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)

ARTIFACT2=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text "Changelog implementation at the repaired head, answering $CHANGES" \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" \
  --rests-on "$PROMISE")

REVIEW_REQUEST2=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Re-review at the repaired head' --body to=@carol \
  --body conditions='confirm the repaired head' \
  --body head="$HEAD_COMMIT" --body artifact="$ARTIFACT2" \
  --body no_git_artifact=true --rests-on "$ARTIFACT2")
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

In a separate assignment, alice asks bot to rewrite a parser. Bot accepts,
then discovers that the rewrite needs a decision bot cannot make. Supersede
your own unfinished promise, and say why:

```sh
PARSER_REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Rewrite the parser' --body to=@bot \
  --body conditions='the parser rewrite passes its existing tests' \
  --body target_ref=refs/heads/main)
PARSER_PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will rewrite the parser' --rests-on "$PARSER_REQUEST")
gs supersede --repo "$REPO" --as bot \
  --text 'reneging: the parser rewrite needs a decision I cannot make' "$PARSER_PROMISE"
```

This is **reneging**, and it is visible forever. Do it as early as you
know. Early reneging is honourable; late reneging is not. The request remains
unfinished and available for a fresh promise. Only alice or a ratifier may
retire it. Withdrawing this promise leaves the completed changelog intact.

## Check the loop

```sh
gs status --repo "$REPO"
```

The changelog request is satisfied by its sealed landing. The parser request
shows `reneged`, with no live promise and no retirement of the request. The
release-date child is open for alice and still targets `refs/heads/main`.

## See also

- [The work loop](../concepts/work-loop.md) — why it is shaped this way.
- [`gs review`](../reference/gs/review.md),
  [`gs merge`](../reference/gs/merge.md)
