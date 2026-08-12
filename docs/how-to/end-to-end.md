---
title: Do a piece of work, end to end
summary: One complete path, from an empty directory to an audited record in a fresh clone.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# Do a piece of work, end to end

One path with nothing left out: create a workroom, add two participants,
ask for something, do it, review it at an exact head, merge it, and then
audit the whole thing from a clone made by someone who was not there.

Every command on this page runs. They are executed against a scratch
workroom by `make test`.

## What you need

Go 1.26 and git with SSH signing support. Build the two binaries and put
them on your path:

```text
make build
export PATH="$PWD/bin:$PATH"
```

`gs` is the command line and the resident service. `gitseq-mcp` is the
adapter an agent joins through.

## 1. A repository with a workroom in it

A workroom is an overlay on an ordinary repository — yours, not gitseq's.
It adds refs under `refs/seq/*` and private state under `.git/gitseq`,
and touches nothing else.

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)

gs init --repo "$REPO" --operator alice
```

`init` prints the **genesis** hash and the operator's fingerprint. Keep
the genesis: everyone who attaches later needs it.

Add a second principal and look at the roster:

```sh
gs actor-add --repo "$REPO" --as alice --name bot --kind agent
gs actor-add --repo "$REPO" --as alice --name carol --kind agent
gs actors --repo "$REPO"
```

`alice` holds `operator`, and `operator` carries `ratifier` with it.
`bot` and `carol` are participants and nothing more. Kind (`human`,
`agent`, `service`) says what a principal is and grants nothing; see
[Actors and authority](../concepts/actors.md).

## 2. Ask for something

```sh
REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a greeting file' \
  --body to=@bot \
  --body conditions='greeting.txt exists and the reviewer approves the exact head')
echo "$REQUEST"
```

Every durable command prints the **event identifier**, and that is what
later acts cite. Copy it whole — see
[Event identifiers](../reference/event-identifiers.md).

`bot` claims it:

```sh
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add the greeting' --rests-on "$REQUEST")
```

A promise rests on a request. A promise that rests on nothing dangles,
because nobody is positioned to declare it satisfied.

## 3. Do the work, on a branch

```sh
git -C "$REPO" switch -q -c task/greeting
echo 'hello' > "$REPO/greeting.txt"
git -C "$REPO" add greeting.txt
git -C "$REPO" commit -q -m "Add a greeting

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
```

The `Rests-On:` trailer bridges the commit to the decision that motivated
it. The event has to exist before the commit, or you are amending the
trailer in afterwards and changing the hash.

Then point at the exact commit, and report:

```sh
ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'Greeting implementation' \
  --body path=greeting.txt --body commit="$HEAD_COMMIT" \
  --rests-on "$REQUEST")

REPORT=$(gs state --repo "$REPO" --as bot --kind report \
  --text 'ready-for-review; greeting.txt added' \
  --body head="$HEAD_COMMIT" --rests-on "$PROMISE")
```

## 4. Review at that exact head

Review is a separate loop: `bot` asks, `carol` promises, `carol` signs a
verdict.

```sh
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review the greeting at its exact head' \
  --body to=@carol \
  --body conditions='confirm greeting.txt at the named head' \
  --body head="$HEAD_COMMIT" --body artifact="$ARTIFACT" \
  --rests-on "$ARTIFACT")

REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")

REVIEW=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED at this exact head')
```

`gs review` refuses to sign unless the checkout is clean and sitting on
the artifact's exact commit, so the verdict names a commit somebody
actually looked at. The review requester ratifies it:

```sh
gs ratify --repo "$REPO" --as bot "$REVIEW"
```

## 5. Merge the approved commit

```sh
git -C "$REPO" switch -q "$BASE"
gs merge --repo "$REPO" --as bot --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$REVIEW" \
  --text 'Merge the approved greeting and make it available on main.'
```

`gs merge` hands git the approved object ID, never the branch name, so
advancing `task/greeting` after approval cannot retarget the merge.
The approval is consumed by this one repository-wide landing. The merge
commit, a receipt ref, and a signed workroom assertion record its exact
candidate, target pre-head, and resulting merge head.

### Move the artifacts the merge moved

The merge changed files, and the artifacts pointing at those files now
name a commit that is no longer current. Ask git what changed, then ask
the workroom which paths its live artifacts already use:

```sh
MERGE=$(git -C "$REPO" rev-parse HEAD)
git -C "$REPO" diff --name-only "$MERGE^1" "$MERGE"
gs status --repo "$REPO"
```

Retire what the change covers and publish one successor per area. Here
one path covers it, so it is one of each:

```sh
MERGED_ARTIFACT=$(gs state --repo "$REPO" --as alice --kind artifact \
  --text 'Greeting, merged' \
  --body path=greeting.txt --body commit="$MERGE" --rests-on "$REQUEST")
gs supersede --repo "$REPO" --as bot --rests-on "$MERGED_ARTIFACT" \
  --text 'Superseded by the merge artifact at the same path.' "$ARTIFACT"
```

Keep the successor's EventID. A supersession that names its replacement
says *moved here*; one that does not says *gone*, and a reader following
the chain stops at a dead end with only prose to go on. Prose is not a
pointer. Note the order: `--rests-on` carries the successor alone, and
`gs supersede` puts the target first in the basis itself — and every flag
must precede the positional target, because flag parsing stops there.

Reuse the exact string the live artifact already uses. Paths match as
whole strings, so an artifact at `internal/workroom` never reaches one at
`internal/workroom/fold.go` — a near miss is silent, not an error.
[`gs merge`](../reference/gs/merge.md) enumerates the situations and their
reasons, including why nothing is ever published at `.`.

Now — and only now — the original requester closes the loop:

```sh
gs ratify --repo "$REPO" --as alice "$REPORT"
```

Attempts beyond your authority are not errors. They are recorded, marked
ineffective, and stay visible.

## 6. Read the record

```sh
gs status --repo "$REPO"
gs provenance --repo "$REPO" "$MERGED_ARTIFACT"
gs verify --repo "$REPO"
```

The audit follows the live artifact, not the retired one. Asking a
retired artifact for its provenance answers a question about history;
asking the current one answers the question you have.

`status` projects commitments and who they wait on, artifacts and whether
they have gone stale, and the attempts that took no force. `provenance`
walks back from any event through everything it rests on. `verify` checks
every signature and the integrity of the sequence.

## 7. Publish, and audit from a clone

The sequence lives in `refs/seq/*`, which git neither pushes nor fetches
by default. Publishing is deliberate:

```sh
ORIGIN="$(mktemp -d)/origin.git"
git init -q --bare "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q origin "$BASE"
git -C "$REPO" push origin 'refs/seq/*:refs/seq/*'
```

No leading `+`. A sequence only advances, so publishing is always a
fast-forward; a push git refuses is telling you the remote holds
something you do not.

Then be the auditor. Clone, attach, and check the record with no service,
no chat logs, and no trust in whoever produced it:

```sh
GENESIS=$(gs status --repo "$REPO" --json | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p' | head -1)
AUDIT="$(mktemp -d)/audit"
git clone -q "$ORIGIN" "$AUDIT"
gs attach --repo "$AUDIT" --remote origin --genesis "$GENESIS"
gs status --repo "$AUDIT"
```

`attach` adds a non-forcing fetch rule and pulls the sequence down.
Later attaches and ordinary fetches accept only initial or fast-forward
sequence refs. Successful verification also remembers the signed head and
depth in the clone's Gitseq config, so later verification refuses a shorter
or sibling sequence even if the tracking ref was lost.

A fresh clone has no earlier head to compare. Its first audit proves the
sequence it received is internally signed, not that no later authentic head
exists elsewhere. Use a trusted checkpoint or another witness when first-use
freshness matters.

If `attach` complains about a missing `refs/seq/...` ref, the sequence
was never published. Run the push above and rerun `attach` in the clone
you already have.

Attached clones are read-only unless local actor custody and a sequencer
endpoint are configured. Delete `.git/gitseq` and the extra fetch rule
and you have an ordinary repository — you can always leave with
everything.

## Where to go next

- [Run a work loop](run-a-work-loop.md) — the same loop, with the
  variations you will actually hit.
- [Deploy a resident](deploy-a-resident.md) — presence, live view, and
  what the loopback boundary trusts.
- [Configure an agent](configure-an-agent.md) — join over MCP.
- [The record](../concepts/record.md) — why any of this behaves as it
  does.
