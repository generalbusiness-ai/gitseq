---
title: gs merge
summary: Merge an approved exact head and publish its artifact succession.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# `gs merge`

Merges one approved commit into the checkout, after checking that the
approval really covers that commit and still stands. It then accounts for the
live artifact pointers the merge changed and publishes their successors as one
resumable batch.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor signing the durable merge receipt. |
| `--checkout` | *(required)* | The working tree receiving the merge. |
| `--candidate` | *(required)* | The full, lowercase, approved commit object ID. |
| `--approval` | *(required)* | The ratified approval report event. |
| `--text` | *(required)* | A plain-language description of the change and its impact. This begins the merge commit message. |
| `--server` | | Submit the durable merge receipt through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

It takes no positional arguments.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name carol --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists')
PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST")
git -C "$REPO" switch -q -c task/changelog
printf '# Changelog\n' > "$REPO/CHANGELOG.md"
git -C "$REPO" add CHANGELOG.md
git -C "$REPO" commit -q -m "Add a changelog

Rests-On: $REQUEST"
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
ARTIFACT=$(gs state --repo "$REPO" --as bot --kind artifact \
  --text 'Changelog implementation' \
  --body path=CHANGELOG.md --body commit="$HEAD_COMMIT" --rests-on "$PROMISE")
REVIEW_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Review at the exact head' --body to=@carol \
  --body conditions='confirm the named head' --rests-on "$ARTIFACT")
REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")
APPROVAL=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED at this exact head')
gs ratify --repo "$REPO" --as bot "$APPROVAL" >/dev/null

git -C "$REPO" switch -q "$BASE"
gs merge --repo "$REPO" --as bot --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$APPROVAL" \
  --text 'Merge the approved changelog and make it available on main.'
```

It prints the resulting merge commit.

## When the world moved

A superseded world is judged as of the verdict, not as of now.

A reviewer answers for the world they were shown. An artifact that already
described a superseded world when they signed it carries a judgement that
repeating cannot repair, and it still stops the merge. A retirement landing
*after* the verdict is different: the head is immutable, the artifact still
points at it, and the reviewer had no chance to see the move. That is news, and
news belongs in the merge receipt beside ordinary staleness. The same rule
bounds what a co-signed artifact can reach.

The date is the fold's. `world_superseded_at` is the earliest retirement still
accounting for the moved world, taken across every basis rather than the first
one that carries the flag, so the order a signer wrote its citations in cannot
change it, and a supersession that has itself been superseded is not a cause.
`merge` compares that one number against the verdict's position, for the
approval and for its artifact alike: they move together when a basis under both
is retired, so dating one and not the other would refuse the very verdicts this
admits.

The same rule is enforced by the fold, not only here. A signed merge receipt
appended straight to the log never passes through this command, so a check that
lived only in the CLI would leave that door open; `gs merge` and the fold ask
the same question of the same graph.

When the fold reports no active cause the merge refuses. An undated superseded
world is a projection this command cannot date, not a permission to land.

## What it refuses

| Situation | Why |
|---|---|
| The approval is ineffective, unratified, retired, or already described a superseded world when it was signed | An approval that no longer stands approves nothing. Ordinary staleness is not on this list; see below, and a world that moved after the verdict is recorded rather than refused. |
| The approved artifact is ineffective, retired, or already described a superseded world when the verdict was signed | Same, from the other side of the chain. A world that moved *after* the verdict is recorded, not refused; see below. |
| The verdict is not `approved` | `changes-requested` is not a merge authorization. |
| `--candidate` differs from the approved head | The reviewer looked at a different commit. |
| The approval does not rest on the artifact it names | The chain from verdict to code is broken. |
| `--as` is not the actor whose approved work is landing | A ratified approval is public. Without this, any participant could spend its single use, move the target, and strand the succession the fold would then refuse. |
| The artifact's commit differs from `--candidate` | Same, from the other end. |
| The approval was already used or is reserved by another merge | One approval authorizes exactly one merge. |
| The candidate is already contained in the target | There is no new approved landing to record. |
| `--text` is blank | The immutable merge receipt also needs a useful merge description. |
| The checkout is dirty | The merge result would contain unreviewed work. |
| The checkout belongs to another repository | The workroom does not govern it. |
| `--candidate` is not a full lowercase object ID | An abbreviation could become ambiguous later. |

Every check runs twice, immediately before git is invoked.

### Staleness is recorded, not refused

Ordinary reasoning staleness — a basis under the approval or the artifact
was retired — does not stop a merge. The reasoning moved; the reviewed
head did not. It is still the immutable commit the reviewer signed for, so
`merge` lands that exact head and writes what had moved into the receipt:
a `Gitseq-Staleness:` trailer on the merge commit, and `stale` and
`staleness` in the durable assertion. A stale approval therefore merges
the head it named.

Two narrower facts are refusals. Retirement withdraws the pointer, so a
retired approval or artifact proposes nothing. And when the retired
ancestor was itself an artifact, the record `describes a superseded
world`: the behaviour it covers has been replaced, so the verdict no
longer speaks for what would land. That case needs a fresh artifact on
current bases and a fresh review, not another verdict on the same chain.

A refused merge leaves the signed approval standing and asks only that the
record be brought up to date first.

### The receipt is a checkpoint for what it published

A receipt that records staleness keeps it. What the merge settles is the
successor it publishes: on that single edge, ordinary staleness causes already
active at or before the receipt's own position do not make the successor stale
at birth. The receipt stays historically stale, and only its successor begins a
new current implementation epoch.

This is a fold rule, not a command check. `gs merge` still only validates and
constructs the receipt and the successors; nothing here changes what the
command refuses, what a receipt may retire, or which candidates it leaves live.

The exception is narrow and fails closed. It applies only when:

| Condition | Why |
|---|---|
| The receipt holds an authorized retirement plan | Every `merge_*` field is written by the actor asking for the checkpoint. The plan is the part an independent approval chain already validated. |
| It carries `merge_left_live` and a canonical `merge_changed_paths` | That pair is the version seam. A receipt with neither field, one half, or a non-canonical frontier keeps its existing projection exactly. |
| The artifact cites the receipt directly | The checkpoint travels one edge and is not inherited. |
| The artifact is signed by the receipt's own author | An actor cannot hand another actor's record a checkpoint. |
| The artifact stands at the receipt's exact `merge_head` | The merge published it; it did not merely follow the merge. |
| The artifact stands at a declared successor path | The same bound the receipt's own retirement authority uses. |

Anything else — a record that merely cites a receipt, a bystander at the same
head, an artifact at an undeclared path — goes stale as usual. Whether an
individual `merge_left_live` claim verifies is not read: that testimony is
accounting about other actors' candidates and grants no freshness either way.

Three facts still flare the successor. A cause that arose after the receipt was
never seen by the merge. A planned retirement whose successor chain was later
condemned answered for nothing after all. And direct retirement of the receipt
itself withdraws the pointer the successor stands on.

Causes are weighed one at a time and dated. Asking only whether the receipt was
already stale as of its own position is a different and wrong question: with one
old cause and one new one both live, the receipt was stale then and is stale
now, and the cheap comparison would settle the new cause along with the old.
A cause the fold cannot date fails closed and settles nothing.

`describes_superseded_world` is unchanged by all of this. World staleness
already present at the verdict still invalidates merge authority, a world that
moved after the verdict is still recorded, and every other basis of the
successor is read exactly as before.

## Why an object ID and not a branch

`gs merge` passes the approved full object ID to `git merge --no-ff`,
never a branch name. Advancing the reviewed branch after approval
therefore cannot retarget the merge — the commit that was approved is the
commit that lands.

## Approval scope and receipt

A ratified approval is repository-scoped and single-use. It authorizes
the exact candidate to land once in any one clean checkout belonging to
the same repository. The first successful use fixes the target's
pre-merge head. The approval cannot then be replayed into another branch
or linked worktree.

Concurrent callers first reserve
`refs/gitseq/merge-receipts/<approval-hash>` with an atomic compare-and-swap.
Only one caller can proceed. A successful merge leaves three matching
records, followed by the artifact succession authorized by that receipt:

- merge commit trailers naming `Gitseq-Approval`, `Gitseq-Candidate`,
  `Gitseq-Target-Pre-Head`, `Gitseq-Changed-Paths`, and
  `Gitseq-Left-Live`;
- the repository receipt ref, advanced from the target's pre-merge head
  to the merge head; and
- a signed workroom assertion naming the approval, candidate, target
  pre-head, and merge head.

Git receipts are checked across all refs. The signed workroom assertion
also prevents replay if local refs and the branch carrying the merge are
later lost. An interrupted reservation fails closed so it can be
inspected before any later merge is allowed.

The assertion and every successor and retirement use deterministic
idempotency keys. If submission stops part-way, run the same command again in
the checkout still at that merge head. It finds the immutable Git receipt and
resumes the missing suffix; it does not merge a second time or retire a
successor it already published.

Before creating the merge commit, the command builds the signed request for
every act in that succession batch. It checks each request with the kernel's
exact genesis-ceiling accounting and, when `--server` is used, with the
resident's exact JSON request limit. Intra-batch labels are replaced with
canonical event identifiers of the same encoded length as the identifiers the
sequencer will mint. A batch that cannot be admitted is refused while `HEAD`,
the workroom log, and the receipt reservation are still unchanged.

When the reviewed candidate artifact rests on its implementer's promise, that
artifact already serves as the implementation report. The sealed receipt
closes that commitment; no implementation ratification follows the merge. The
review approval remains separate and must still be explicitly ratified before
this command accepts it.

## Artifact succession

The command reads the first-parent diff of the merge that actually lands. It
treats stale artifacts as live until they are retired and deduplicates work
across changed files. For a landed file it publishes at that exact changed-file
path, retires only in-target predecessors at the same exact string, and seals
why every other covering candidate stayed live.

| Situation | Enforced result |
|---|---|
| One or more live artifacts have the exact changed-file path | One successor is published at that exact string. Every in-target predecessor at that exact string is retired; other candidates are accounted for and stay live. |
| A directory covers a landed destination | The file successor is still published at the exact changed-file path. A wider directory already in the target world stays live, is not selected for retirement by that destination, and is sealed as `carried`; an outside-world candidate is sealed as a sibling or abandoned candidate. |
| A non-target candidate has an unsettled commitment naming its head or reaching its artifact | It stays live and `Gitseq-Left-Live` records it as a sibling with the protecting commitment. |
| A non-target candidate has no unsettled commitment | It stays live and `Gitseq-Left-Live` records it as abandoned. Its author or a `ratifier` owes the bare supersession. |
| Testimony names a settled, mismatched, or unknown commitment | The receipt remains effective and grants no extra authority. The testimony is unverified and the successor keeps its succession warning. |
| An artifact appears after the sealed snapshot | It is outside the plan and the successor warns that succession was not recorded. A later merge at the path accounts for it. |
| No live artifact has an added or modified file's exact path | A first artifact is published at the changed file path. |
| A file is renamed | Its exact old path is retired without a successor there. Removing the source may retire in-target covering directories and publish the widest directory successor because their contents changed. The destination receives a first artifact or the successor for exact-path predecessors; a directory covering only that landed destination stays live. |
| A file is deleted | Its exact old path is retired with no successor. Removing it may retire in-target covering directories and publish the widest directory successor because their contents changed. |
| A successor rests on the predecessor the same merge retires | The successor stays current. The work stood on what it replaces, and the merge that publishes one withdraws the other in the same act, so that withdrawal is not news arriving underneath it. Only artifacts that merge actually published — at its merge head, at a path it declared — read it that way; any other record citing the receipt goes stale as usual. |

`workroom/state@1` and the current `workroom/state@2` refuse new artifacts at
`.` and refuse comma-joined pseudo-paths. Historical `state@0` artifacts keep
their original decisions but valid historical paths remain candidates for
retirement and succession. New raw submissions cannot use retired state@0 or
state@1 admission to bypass current rules.

`Gitseq-Changed-Paths:` carries the sorted, deduplicated JSON array of exact
old and new paths from the merge's first-parent diff. The durable
`merge_changed_paths` field preserves the same frontier. On retry, the command
recomputes the diff and refuses a missing, malformed, non-canonical, or
mismatched frontier. This prevents a broad successor such as `dir` from making
an unrelated live artifact at `dir/b` part of a merge that changed only
`dir/a`.

`Gitseq-Left-Live:` carries deterministic JSON matching the durable
`merge_left_live` body field. It maps each artifact event to either
`{"class":"carried"}`, `{"class":"sibling","commitment":"<event id>"}`, or
`{"class":"abandoned"}`. `carried` records a current wider pointer in the
landed target and carries no cleanup duty. Sibling and abandoned remain
outside-world candidates. The field grants no retirement authority. The fold
checks both it and the sealed changed-path frontier from durable log facts at
the receipt position and uses them only to make the published successor's
accounting stable. The recorded classification remains historical evidence;
the current owed-supersession count stops suppressing a sibling once its named
commitment settles or retires. Receipts without both prospective fields parse
and project exactly as before.

### Citations across a merge

Documentation names the artifacts that vouch for the behaviour it describes, so
the pages cite exactly the pointers a merge has to retire. Refusing every cited
retirement would refuse every merge in a documented area, and the usual advice —
repoint the pages first — cannot be followed, because the successor does not
exist until the merge lands.

So the two cases are separated. A retirement this merge succeeds goes through:
the supersession names the successor artifact, the successor stands at the same
path or at a directory covering it, and a page naming the old pointer flares and
is told where to re-anchor. A retirement with no successor is refused before
`HEAD` moves, naming the pages, exactly as
[`gs supersede`](supersede.md) refuses one: nothing replaces the pointer, so the
pages would be left with nowhere to go. Retiring it anyway is a deliberate act
with `gs supersede --cited-ok` once the pages have moved.

The documentation gate reads the same distinction. A citation of a retired
artifact whose retirement names a covering successor is reported as a flare; a
citation of a retirement that names nothing still fails the set.

Before moving `HEAD`, `merge` runs this check on the tracked tree that will
receive the merge. Git prepares the merge with `--no-commit`, the command checks
the actual staged result, and a refusal aborts the tentative merge with the
target unchanged. Only then does it create the receipt commit and publish the
durable succession.

### Who may retire another actor's pointer

Free-standing [`gs supersede`](supersede.md) requires the target's author or an
actor holding `ratifier`. Merge succession is the one narrow exception, and the
fold checks all of it from the log alone:

- the supersession cites a merge receipt signed by the same actor;
- that receipt cites a ratified, effective approval whose verdict is `approved`
  and whose head is the merged candidate;
- the approval cites an implementation artifact standing at that candidate and
  written by someone other than the approver;
- the receipt is signed by the author of that implementation artifact — the
  merger of an approved head is the actor whose work it is;
- the target's path lies on the path lineage of one of the paths that approval
  reviewed — the same string, or one path containing the other, which the
  command reads in the narrower single direction described below; and
- the target carries a successor path in the receipt's signed plan, that path
  covers the target's own path, and the supersession cites the successor
  artifact published there.

### What bounds this, and what does not

The fold is pure over records. It holds no repository, so it cannot open the
merge head, read its diff, or establish that any merge happened at all. Every
other field of a receipt — the merge head, the retirement plan, the successor
list — is written by the same actor asking for the authority, and a signer can
publish an artifact at any path. So none of those fields bounds anything.

The approval does. The reviewer is the one party to a merge who did not write
the receipt, so what the receipt may reach is read from the verdict's own
citations: **the artifacts the approval rests on**, and nothing else. Their path
lineages are the whole reach.

One head is one body of work and a body of work spans the paths it changes, so
a verdict citing a single artifact could succeed the pointer in one tree while
the other three it changed stayed on a predecessor nothing would supersede.
`gs review` therefore takes `--artifact` more than once, and a reviewer signs
the whole set they read. An approval citing one artifact reaches one path, which
is what every approval written before this existed does.

The set is not inferred. Anything derived from what the implementer published
is written by the actor asking for the authority: seeding a candidate at an
unrelated path, then obtaining an approval that cites only the legitimate one,
would have reached that path too. Requiring the claims to predate the verdict
closes minting afterwards and does nothing about seeding beforehand. Citation is
what closes both, because a record's bases are fixed when it is signed.

Each member is still checked on its own: effective, not withdrawn, standing at
the exact head the verdict names, and the implementer's own, so a citation
cannot smuggle in a pointer belonging to someone else or describing another
commit. `merge` holds every member to the same staleness rule it holds the
primary to: ordinary staleness passes and is recorded, while a member that
already described a superseded world at the verdict stops the merge.

What none of this establishes is worth saying. Holding no repository, the fold
cannot open the approved commit or read its diff, so it does not know that the
head touches the paths cited; it knows that the reviewer signed for them. Without it, the author of a single approved implementation could invent
a merge head, name a stranger's artifact anywhere in the log, publish a
successor at its path, and retire it.

`merge` checks that same signer before it starts, not only the fold afterwards.
The fold sees a receipt, and a receipt is written after Git has committed: by
then the target has moved and the approval is spent, so a refusal there arrives
too late to be obeyed.

That fingerprint is the whole test, and no role stands in for it — not
`ratifier`, which [`gs supersede`](supersede.md) otherwise lets retire anything.
A role is live standing and can be revoked between the check and the acts it
would authorize, while the tentative merge runs or while the succession lands
one act at a time; the fold would then refuse what the check allowed, after
`HEAD` had moved. The author of an approved artifact is a fact about a record
that has already happened, so nothing can withdraw it mid-merge. A merge signed
by anyone else needs an authorization that survives concurrent revocation, and
there is none today.

The cost is stated rather than worked around. A merge carries cross-author
authority only within the paths its approval reviewed and only for in-target
predecessors in its sealed retirement plan. Other candidates stay with their
own authors or an actor holding `ratifier`, which is where they were before
merge succession existed. The receipt accounts for those candidates without
turning testimony into authority.

A retirement with no successor — a deleted path — takes no authority from a
merge either, because nothing the merge published stands over it to bound the
claim.

`merge` reads a reviewed path in one direction only: it reaches that path and
whatever stands beneath it, and nothing above it. An approval reviewing
`docs/how-to/x.md` reaches another actor's pointer at that exact path and not
one at bare `docs`, because the wider pointer speaks for trees the head never
put in front of the reviewer. Reaching upward was the older reading, and it let
a merge that reviewed a leaf claim the tree containing it. The fold's own
lineage test is unchanged and still reads both directions; this is the command
holding itself to the narrower rule while the target is still where it was. It
governs merges run from here on. No sealed receipt is reinterpreted.

The check belongs to fresh merges only, and runs once. It runs in preflight,
after Git has reserved the receipt ref and staged the tentative merge, but
before the merge commit exists, before `HEAD` moves, and before any durable
workroom record is appended; succession recording never re-applies it.
Resuming an interrupted merge instead finds the immutable Git receipt and
appends its recorded suffix without replanning and without this guard, so a
receipt sealed under an older reading of reach keeps exactly the authority it
was sealed with.

### Restart residents at the merged commit

The merge-succession change advanced the state schema to `workroom/state@1`,
and the commitment-lifecycle change advanced the fold profile to
`workroom-fold@4`. The schema/fold split advances the schemas to
`workroom/state@2` and `workroom/ratify@1`, and advances the application
projection profile to `workroom-fold@5`. Older state and ratification records
remain readable, but a binary built before these changes projects commitments
under the old contract and cannot interpret the new schemas. Restart every
resident sequencer and MCP adapter at the merged commit.

The prospective left-live accounting rule advances the projection profile from
`workroom-fold@10` to `workroom-fold@11`. Historical receipts without
`merge_left_live` and `merge_changed_paths` retain their existing projection
behavior.

The receipt checkpoint described above advances the current projection profile
from `workroom-fold@11` to `workroom-fold@12`. A cache written under `@11`
answers with merge successors stale at birth, so it is rejected and the history
replayed. Historical receipts without the prospective pair are unaffected.

## See also

- [`gs review`](review.md), [`gs supersede`](supersede.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
