---
title: gs merge
summary: Merge an approved exact head and publish its artifact succession.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0581948abafe7fda01c7e4bcafaae5337297c601
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4b69abf701279b7e30b83e0e539eb26fbc8b8779
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:df626b67d31ee72ba4f7af7d29c8ed4246fc04ec
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:608be185aaba9343eba9175c04bf10a20a04b015
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bd891443ff868623f2ad427b4a5becd32359e5d3
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
| `--authorization` | | A ratified merge-authorization report carrying the exact structured bindings described below. For a held state@3 request, this names the hold owner's exact release; an unheld state@3 request refuses it. Legacy lanes retain optional phase-one authorization. See the held-landing compatibility window below. |
| `--text` | *(required)* | A plain-language description of the change and its impact. This begins the merge commit message, or — when the target already contains the candidate — serves as the text of the incorporation receipt. |
| `--server` | | Submit the durable merge receipt through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses a value other than a positive duration before it signs anything. |

It takes no positional arguments.

`--approval` and `--authorization` name durable events and each takes a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier; the command resolves it and names it on standard
error before it builds the receipt. `--candidate` names an ordinary Git commit,
not an event, and the command never resolves it.

A merge receipt's `merge_approval`, `merge_authorization` and
`merge_authorization_ratification` hold event identifiers, and this command
composes them from values already canonical. Written by hand into a body, they
resolve like any other recognized field; see
[Body fields](../event-identifiers.md#body-fields-and-why-only-some-of-them).

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name carol --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists' \
  --body target_ref="refs/heads/$BASE" --body landing=held)
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
  --body conditions='confirm the named head' \
  --body no_git_artifact=true --rests-on "$ARTIFACT")
REVIEW_PROMISE=$(gs state --repo "$REPO" --as carol --kind promise \
  --text 'I will review it' --rests-on "$REVIEW_REQUEST")
APPROVAL=$(gs review --repo "$REPO" --as carol --checkout "$REPO" \
  --artifact "$ARTIFACT" --promise "$REVIEW_PROMISE" \
  --verdict approved --text 'APPROVED at this exact head')
gs ratify --repo "$REPO" --as bot "$APPROVAL" >/dev/null

git -C "$REPO" switch -q "$BASE"
TARGET_PRE_HEAD=$(git -C "$REPO" rev-parse HEAD)
AUTH_REQUEST=$(gs state --repo "$REPO" --as bot --kind request \
  --text 'Authorize this exact approved merge' --body to=@alice \
  --body conditions='lift do-not-merge only for the structured bindings below' \
  --body no_git_artifact=true \
  --rests-on "$REQUEST" --rests-on "$APPROVAL")
AUTHORIZATION=$(gs state --repo "$REPO" --as alice --kind report \
  --text 'Authorize only this candidate and approval on the measured target' \
  --body authorizes_candidate="$HEAD_COMMIT" \
  --body authorizes_approval="$APPROVAL" \
  --body authorizes_request="$REQUEST" \
  --body target_pre_head="$TARGET_PRE_HEAD" \
  --body target_repo="git:sha1:$GENESIS" \
  --body target_ref="refs/heads/$BASE" \
  --rests-on "$AUTH_REQUEST")
gs ratify --repo "$REPO" --as bot "$AUTHORIZATION" >/dev/null
gs merge --repo "$REPO" --as bot --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval "$APPROVAL" \
  --authorization "$AUTHORIZATION" \
  --text 'Merge the approved changelog and make it available on main.'
```

It prints the resulting merge commit.

## When the world moved

Gitseq judges a superseded world as of the verdict, not as of now.

A reviewer answers for the world shown to them. An artifact that already
described a superseded world when they signed it carries a judgement that
repeating cannot repair, and it still stops the merge. A retirement landing
*after* the verdict differs: the head stays immutable, the artifact still points
at it, and the reviewer had no chance to see the move. That counts as news, and
news belongs in the merge receipt beside ordinary staleness. The same rule
bounds what a co-signed artifact can reach.

The fold supplies the date. `world_superseded_at` names the earliest retirement
still accounting for the moved world, taken across every basis rather than the
first one that carries the flag, so the order a signer wrote its citations in
cannot change it, and a supersession that a later one has itself superseded does
not count as a cause. `merge` compares that one number against the verdict's
position, for the approval and for its artifact alike: they move together when a
retirement reaches a basis under both, so dating one and not the other would
refuse the very verdicts this admits.

The fold enforces the same rule too, not only this command. A signed merge
receipt appended straight to the log never passes through this command, so a
check that lived only in the CLI would leave that door open; `gs merge` and the
fold ask the same question of the same graph.

When the fold reports no active cause the merge refuses. An undated superseded
world gives this command a projection it cannot date, not a permission to land.

## What it refuses

| Situation | Why |
|---|---|
| The approval lacks effect or ratification, has retired, or already described a superseded world when its reviewer signed it | An approval that no longer stands approves nothing. Ordinary staleness does not appear on this list; see below, and the merge records a world that moved after the verdict rather than refusing it. |
| `--authorization` names an ineffective, unratified, retired, or world-stale report | Nobody has durably adopted the report, someone has withdrawn it, or it already described a replaced implementation world when it gained force. |
| The authorization's `authorizes_candidate`, `authorizes_approval`, or `authorizes_request` differs from the merge | Authorization stays exact and cannot float to another head, verdict, or implementation lane. |
| The authorization report does not close an authorization request | A free-standing report does not amount to the governed act the requester adopted. |
| On a legacy lane, someone other than the original implementation requester signed the authorization report, the live actor named exactly `planner`, or a live actor carrying `ratifier` | Ordinary participants cannot authorize their own merge by creating, answering, and ratifying a separate request with copied bindings. |
| On a held `state@3` lane, someone other than the hold owner signed the release, or that owner no longer counts as a live roster actor | The request delegated its release to one actor by name. A planner or ratifier who wants the landing supersedes the request instead of signing around its owner, and an authority read out of a retired fingerprint belongs to nobody. |
| The authorization does not state `target_repo` and `target_ref` on a `state@3` lane | A report naming only a pre-head says nothing about which branch of which repository held it, so it would read as authority for the same commit landing anywhere. |
| A stated `target_repo` or `target_ref` differs from the request's resolved destination, or from the destination measured in the checkout | The merge needs both comparisons: the first alone lets a signer and a stale checkout agree with each other, the second alone lets the signer describe wherever the merge happens to stand. |
| A hold covers the implementation request, no release stands in force for this candidate and approval, and the call gives `--authorization` | The window lets such a landing proceed unauthorized and warned. It does not let the merge seal some other actor's report as the authority for it. |
| Someone filed an authorization or release report whose `target_ref` no longer holds its `target_pre_head` | [`gs state`](state.md) refuses it when the author writes the report, rather than leaving it for the merge to discover. An exact retry of a report already accepted replays that report instead of measuring it again. `remeasure=disjoint-paths` relaxes this to an ancestry check. |
| The sequencer did not order its ratification before the prospective receipt | A later ratification cannot retroactively order an earlier merge. |
| A Git receipt carries `Gitseq-Authorization` without the exact `Gitseq-Authorization-Ratification` witness, or the report's current `ratified_by` differs | Recovery cannot prove that the authorization had force before the Git commit. |
| `target_pre_head` differs from the current target without `remeasure=disjoint-paths` | The authorization measured against another target world. |
| Disjoint-path remeasurement finds a path changed by both the candidate and current target since `target_pre_head` | The newer target may affect the authorized merge and needs a fresh authorization. |
| The approved artifact lacks effect, has retired, or already described a superseded world when the reviewer signed the verdict | Same, from the other side of the chain. The merge records, and does not refuse, a world that moved *after* the verdict; see below. |
| The approval's implementation binding refuses: its primary reports no commitment and names no adopted decision, its selected implementations no longer resolve, or someone has retired their reporting artifacts | The merge re-resolves what the review covered from the verdict's own citations and selectors through the shared `internal/reviewguard` resolver. The merge reclassifies an approval filed before Gitseq recorded bindings from its actual primary; an empty lookup grandfathers nothing. |
| The approval comes from an evidence-only review | Its author filed the primary against a request that owes no Git artifact. Reviewing it broke no rule; landing it discharges nothing, so the merge refuses before Git or the workroom moves, with or without `--authorization`. |
| The verdict says something other than `approved` | `changes-requested` authorizes no merge. |
| `--candidate` differs from the approved head | The reviewer looked at a different commit. |
| The approval does not rest on the artifact it names | The chain from verdict to code has a break. |
| `--as` names someone other than the actor whose approved work lands | Anyone can see a ratified approval. Without this, any participant could spend its single use, move the target, and strand the succession the fold would then refuse. |
| The artifact's commit differs from `--candidate` | Same, from the other end. |
| Another merge already used or has reserved the approval | One approval authorizes exactly one merge. |
| `--text` has no content | The immutable merge receipt also needs a useful merge description. |
| The checkout has uncommitted changes | The merge result would contain unreviewed work. |
| The checkout belongs to another repository | The workroom does not govern it. |
| `--candidate` does not give a full lowercase object ID | An abbreviation could become ambiguous later. |
| The retirement plan reaches another actor's artifact outside every path the approval reviewed | Re-file the approval so it covers that path, or ask the artifact's author or an actor holding `ratifier` to retire it. The refusal names the event, path, and reviewed paths. |
| The checkout has a detached HEAD | A merge lands into a checked-out branch. A detached checkout offers no destination, and a receipt could name none. |
| The checkout's branch differs from the implementation request's target ref | The request says where it owes its result. Landing it anywhere else discharges nothing and would seal a receipt saying otherwise. |
| The checkout's repository differs from the request's target repository | Same, for the other half of the destination. |
| The target ref, repository, or pre-head moved between planning and the merge | The command computed the plan against a destination that no longer stands there. |
| The sealed ref has moved off its sealed pre-head when the landing advances it | The landing works as a compare-and-swap. A ref that moved keeps the move, and the command writes nothing. |
| No hold covers the implementation request and the call gives `--authorization` | An unheld request asked for no release, so a merge that presents one claims an authority nobody granted. Requests filed before `workroom/state@3` keep the phase-one reading, and this rule does not affect them. |
| A resumed receipt names a destination this checkout does not stand on | Recovery proves where the receipt landed instead of assuming it landed here. |
| A resumed receipt carries `Gitseq-Target-Repo` without `Gitseq-Target-Ref`, or the reverse | Half a destination proves nothing. A receipt with both absent counts as a legacy receipt and passes. |
| The durable receipt's target, target pre-head, or hold warning disagrees with the sealed Git trailers | The binding lives in two places and someone can rewrite only one of them afterwards, so recovery reads both and requires them equal. |
| The checkout's `HEAD` names a ref outside `refs/heads/` | A merge lands into a branch. This command will seal no other destination. |
| The approved artifact reports for more than one commitment lane | The destination comes from one request. Two lanes reporting one artifact name no single answer. |
| `Gitseq-Hold-Warning` appears with any value but `true` | The compatibility-window marker states a fact read back out of a commit message, so it either matches exactly or counts for nothing. |
| A resumed receipt's checkout does not belong to the workroom repository | Recovery reads a sealed receipt out of the checkout and appends its durable suffix to this workroom; the two must share one repository. |
| A merge of an implementation request whose hold has a release gives an `--authorization` other than that release | One report lifted the hold. A merge under any other one does not land under the release the hold owner signed. |

The shared read-only evaluator runs before any receipt reservation. `merge`
then resolves structured authorization and proves that the target head, staged
changed paths, and verified Workroom frontier still equal that evaluated plan
before it moves `HEAD`.

### Where a merge lands

An implementation request owes its Git artifact to a named destination, and
this command puts it there. Every merge therefore measures the
destination in the governed checkout rather than taking it from anything a
signer wrote, and seals it:

| receipt field | Git trailer | value |
|---|---|---|
| `merge_target_repo` | `Gitseq-Target-Repo:` | The workroom genesis id of the checkout's repository. |
| `merge_target_ref` | `Gitseq-Target-Ref:` | The full branch ref from `git symbolic-ref HEAD`. |
| `merge_target_pre_head` | `Gitseq-Target-Pre-Head:` | The commit that ref held immediately before `HEAD` moved. |

The destination the request owes comes from the fold, not from this command.
A `workroom/state@3` request states its target triple by value or inherits it
from its ancestry; a request filed under an older schema whose commitment ever
carried a reporting artifact reads as owing `refs/heads/main` of this
workroom's own repository, and carries the `legacy` flag. `merge` compares the
checkout against that resolved answer, whether or not the caller gives
`--authorization`, and refuses every mismatch in the table above.

The command then binds the landing to that measurement rather than measuring
again before it. `git commit` resolves `HEAD` at the instant it writes, so no
check taken beforehand can say where it lands: a `pre-commit` hook that runs
`git symbolic-ref HEAD refs/heads/other` moves the whole merge to another branch
while the receipt goes on naming the branch this command measured. So `merge`
does not commit through `HEAD` at all. It writes the staged tree with
`git write-tree`, builds the merge commit with
`git commit-tree <tree> -p <sealed pre-head> -p <candidate>`, and advances the
sealed ref with `git update-ref <sealed ref> <new commit> <sealed pre-head>` — a
compare-and-swap. No window separates the measurement from the landing: a ref
that moved makes the update fail, the concurrent move stands, and the commit
object no ref points at stays unreachable.

The command writes no branch ref except through that compare-and-swap. The index
and working tree already hold the landed tree, since the command built the merge
commit from them, so `git merge --quit` finishes the checkout: it forgets the
in-progress merge state and touches no ref. A `git reset --hard` would write the
branch `HEAD` names a second time, from a stale reading — rolling back a
fast-forward that landed on the target after the swap, or dragging a branch that
someone switched `HEAD` to in the meantime — and no check taken before it can
close that window. A `HEAD` retargeted underneath the merge, or a target that
moved on after the landing, changes nothing about where the work landed; the
command says so on standard error and leaves every ref alone.

Two consequences follow from not committing through `HEAD`. Commit hooks —
`pre-commit`, `prepare-commit-msg`, `commit-msg`, `post-commit` — do not run for
a merge; the governed act offers no place for repository-local scripts to
intervene. And the command uses the commit message verbatim, where
`git commit -m` would have applied its default cleanup, so a `--text` containing
comment lines or trailing whitespace now reaches the receipt as written.

The comparison needs a request, and it finds one through the commitment lane
that reports the approved artifact. An approval whose artifact serves as no
commitment lane's report gets **no destination check at all**, and merges into
any branch of the workroom's repository; its receipt still seals the destination
it measured. That covers independently reviewed self-initiated work, where one
actor acts as both requester and performer and no commitment row exists. It also
covers an artifact published against a request that stated
`no_git_artifact=true`: the fold never makes such an artifact that commitment's
report, so the command finds no lane and enforces no destination. That second
case marks a gap, not a design choice, and closing it has its own request.

#### Held landings and the compatibility window

A `state@3` request may carry `landing=held`. One ratified structured
authorization report signed by the hold owner lifts its hold, and the
fold decides whether such a release stands in force for this exact candidate and
approval. For one release, a merge of a held request with no such release in
force produces a **warning** on standard error rather than a refusal, and both
receipts record that it used the window: `merge_hold_warning=true` in the
durable receipt and `Gitseq-Hold-Warning: true` on the merge commit. When the
window closes that case becomes a refusal.

A landing of a released hold seals exactly that release. The receipt records it
as `merge_authorization` with its ratification as
`merge_authorization_ratification`, and the merge commit carries the matching
`Gitseq-Authorization` and `Gitseq-Authorization-Ratification` trailers, whether
or not `--authorization` named it; passing a different report refuses. The merge
then validates the release as it validates any authorization: the same bindings,
the same ratification witness, the same ordering. Only the hold owner may sign
it, the owner must remain a live roster actor at merge time, and the
phase-one list of original implementation requester, actor named `planner`, or
live `ratifier` does not apply — a delegated owner outside that list signs a
valid release, and a member of that list who does not own the hold does not.

A release also states `target_repo` and `target_ref`, and the merge requires
both to equal the request's resolved destination and the destination it
measured in the checkout. While the window stays open, a held request with no
release in force may still land warned, but only with no `--authorization` at
all: the merge refuses any other report named there.

An unheld `state@3` request needs no authorization of any kind. The implementer
merges on the ratified exact approval, the merge refuses `--authorization`, and
the receipt carries no authorization fields. Only legacy lanes keep the optional
phase-one authorization whose omission warns.

#### Legacy receipts

A receipt carrying neither `Gitseq-Target-Repo` nor `Gitseq-Target-Ref` predates
both. It reads as `refs/heads/main` of this workroom's own repository, carries
the legacy flag, and resumes without acquiring fields its author never signed.
The same reading applies to a pre-`state@3` request, and it has a cost: such a
request now lands only into `refs/heads/main` of the workroom's repository, so a
repository with a default branch other than `main` cannot merge work filed under
the older schema until it restates that work's target under `state@3`. Gitseq
computes elsewhere, as repository-derived advisory facts, whether that ref still
contains a landed head afterwards and whether a remote carries it; nothing here
reads them, and no fold satisfaction depends on them.

### Structured merge authorization

`--authorization` names a Workroom `report`, not a new kernel or fold kind.
The requester of the authorization work must ratify the report, and the
report must carry these exact body fields:

| field | value |
|---|---|
| `authorizes_candidate` | The full commit passed as `--candidate`. |
| `authorizes_approval` | The ratified implementation approval passed as `--approval`. |
| `authorizes_request` | The original implementation request whose reporting artifact the approval names. |
| `target_pre_head` | The full commit at which the authorization measured the target. |
| `target_repo`, `target_ref` | A state@3 release requires them; they must match the request and measured checkout. Legacy authorizations may omit them, but the merge checks them when present. |
| `remeasure` | Optional. The merge accepts only `disjoint-paths`. |

The approval's named artifact must project as the report of exactly one
implementation request. The command checks `authorizes_request` this way; shared
prose, branch names, and actor names do not stand in for that lane. The
authorization report must likewise close exactly one authorization commitment.
On a held state@3 request, only the hold owner signs the release. On a legacy
lane, only the original implementation requester, the live actor named exactly
`planner`, or a live `ratifier` may sign it. Requester ratification does not
widen either signer rule.

Normally `target_pre_head` must still equal the checkout's `HEAD`. With
`remeasure=disjoint-paths`, the merge accepts a newer `HEAD` only when it
descends from the measured head and the exact old/new paths changed by the
candidate do not intersect those changed on the target since the measurement.
Rename and delete sources count, as do copy and addition destinations.

On legacy lanes, omitting `--authorization` prints a compatibility warning and
proceeds under the existing approval guard. When the caller passes the flag, the
merge enforces every binding. The merge commit records both
`Gitseq-Authorization:` and `Gitseq-Authorization-Ratification:`. The second
trailer carries the exact event ID of the sequencer-admitted ratification that
gave the report force. Because that unpredictable ID already sits inside the
later Git commit, it serves as the temporal witness that ratification existed
before Git moved. The durable receipt records matching `merge_authorization` and
`merge_authorization_ratification` fields and rests on both events.

Historical receipts with both authorization fields absent remain valid legacy
receipts. A receipt carrying authorization without its ratification witness
fails closed. On recovery, the command revalidates the report, both exact
commitments, every binding, the governing signer, and target measurement
against the sealed target pre-head; it also requires the report's current
`ratified_by` to equal the sealed witness before appending any durable suffix.
The command refuses a later authorization passed while resuming a legacy
receipt: writing the merge commit fixes the ordering.

Use the delivered `landing=held` request fields for a hold. The current
[compatibility window](#held-landings-and-the-compatibility-window) and its
receipt warning differ from legacy optional authorization. Source delivery
does not itself deploy readers or change the active host binding.

### Merge records staleness; it does not refuse it

Ordinary reasoning staleness — a retirement of a basis under the approval or
the artifact — does not stop a merge. The reasoning moved; the reviewed
head did not. It remains the immutable commit the reviewer signed for, so
`merge` lands that exact head and writes what had moved into the receipt:
a `Gitseq-Staleness:` trailer on the merge commit, and `stale` and
`staleness` in the durable assertion. A stale approval therefore merges
the head it named.

Two narrower facts cause refusals. Retirement withdraws the pointer, so a
retired approval or artifact proposes nothing. A `describes_superseded_world`
cause already present at the verdict also refuses, as does a flagged cause the
fold cannot date. The merge records a cause arising after the verdict rather
than refusing it. A refusal needs a fresh artifact on current bases and a fresh
review, not another verdict on the same chain.

A refused merge leaves the signed approval standing and asks only that someone
bring the record up to date first.

### The receipt checkpoints what it published

A receipt that records staleness keeps it. The merge settles the
successor it publishes: on that single edge, ordinary staleness causes already
active at or before the receipt's own position do not make the successor stale
at birth. The receipt stays historically stale, and only its successor begins a
new current implementation epoch.

This rule belongs to the fold, not to a command check. `gs merge` still only
validates and constructs the receipt and the successors; nothing here changes
what the command refuses, what a receipt may retire, or which candidates it
leaves live.

The exception stays narrow and fails closed. It applies only when:

| Condition | Why |
|---|---|
| The receipt holds an authorized retirement plan | The actor asking for the checkpoint writes every `merge_*` field. The plan forms the part an independent approval chain already validated. |
| It carries `merge_left_live` and a canonical `merge_changed_paths` | That pair marks the version seam. A receipt with neither field, one half, or a non-canonical frontier keeps its existing projection exactly. |
| The artifact cites the receipt directly | The checkpoint travels one edge, and nothing inherits it. |
| The receipt's own author signed the artifact | An actor cannot hand another actor's record a checkpoint. |
| The artifact stands at the receipt's exact `merge_head` | The merge published it; it did not merely follow the merge. |
| The artifact stands at a declared successor path | The same bound the receipt's own retirement authority uses. |

Anything else — a record that merely cites a receipt, a bystander at the same
head, an artifact at an undeclared path — goes stale as usual. The fold does
not read whether an individual `merge_left_live` claim verifies: that testimony
accounts for other actors' candidates and grants no freshness either way.

Three facts still flare the successor. The merge never saw a cause that arose
after the receipt. A planned retirement whose successor chain a later act
condemned answered for nothing after all. And direct retirement of the receipt
itself withdraws the pointer the successor stands on.

The fold weighs and dates causes one at a time. Asking only whether the receipt
had already gone stale as of its own position asks a different and wrong
question: with one old cause and one new one both live, the receipt had gone
stale then and stays stale now, and the cheap comparison would settle the new
cause along with the old.
A cause the fold cannot date fails closed and settles nothing.

None of this changes `describes_superseded_world`. World staleness
already present at the verdict still invalidates merge authority, the merge
still records a world that moved after the verdict, and the fold reads every
other basis of the successor exactly as before.

## Incorporation: the head the target already has

An approved head sometimes reaches its target branch without this command: a
pull request merged in a forge, a push by hand, a fast-forward somebody took
locally. The work landed, but nothing signed for it, so the commitment stays
`awaiting-landing` with `approved_not_landed` set. No new landing remains to
merge, a plain report on a landing request stays ineffective, and supersession
would falsely say that someone carried or abandoned the head.

So `gs merge` records the truth instead of refusing it. Run the same command
you would have run to land the head — same `--checkout`, `--candidate`,
`--approval`, `--authorization`, `--text`, `--server`. When the checkout's
target ref already contains the candidate and every other check passes,
the command appends one durable receipt and touches nothing in Git.

No flag controls this. The command measures containment rather than taking a
claim, with the same `git merge-base --is-ancestor` the preflight already ran.

**What the receipt carries.** The ordinary merge-receipt assertion, with one
added field and an empty succession:

| field | value |
|---|---|
| `merge_incorporation` | `prior`, the only value it ever takes |
| `merge_head` | the candidate itself, because the command made no merge commit |
| `merge_candidate` | the approved head |
| `merge_target_repo`, `merge_target_ref`, `merge_target_pre_head` | the destination measured in the checkout, exactly as an ordinary merge measures it |
| `merge_retirements`, `merge_successors`, `merge_left_live`, `merge_changed_paths` | `{}`, `[]`, `{}` and `[]` |

The assertion takes its own text from the `--text` you gave. An ordinary merge
keeps that text in the merge commit message; an incorporation writes no commit,
so the receipt offers the only place it can live.

**What Git keeps.** The target ref, `HEAD`, the index and the working tree stay
exactly as before. The command writes no commit object, creates no receipt
ref, and advances no branch. The command prints the candidate as the head and
says on standard error that it recorded an incorporation rather than a merge.

**What the receipt may do.** It closes its own commitment, under the ordinary
receipt rules: the fold reads the same ratified, independent, exact-head
approval chain it reads for any receipt, and the row becomes `satisfied`,
terminal `landed`, with `approved_not_landed` false. No requester ratification
follows, exactly as for an ordinary landing.

It may do nothing else. An empty plan reaches no path, publishes no successor
and retires no predecessor, so reachability never becomes succession authority.
A receipt carrying `merge_incorporation=prior` and anything at all in its plan
confers nothing: the encoder refuses to write one, the recorded-plan reader
refuses to read one, and the fold gives one written by hand no authority and no
delivery. Gitseq refuses any other value of `merge_incorporation` the same way.

**Single use.** One approval still buys one receipt. The command refuses a
second run, naming the receipt the first appended. Two runs that race past that
check use the same deterministic idempotency key: when they built the identical
act the second replays the first, and when their observations differ (another
`--text`, a target head that moved between them) Gitseq refuses the second as an
idempotency conflict. Either way exactly one receipt exists.

**What nobody re-checks.** The command measures containment once, before the
durable append, and no Git reference transaction guards the window between them.
A target that moves forward in that window still contains the candidate. A
force-push that drops the candidate in that window leaves a receipt whose
`merge_target_pre_head` held true when the command read it; the fold cannot
verify containment for any receipt, so this carries the same exposure a receipt
written by hand has always had, accepted rather than closed.

**What this does not solve.** Containment concerns commits, not content. A
squashed or rebased landing puts different commits in the target, so the
target does not contain the approved candidate and no incorporation applies:
those rows still have no admissible closer. Recognising a landing by its
content rather than its commits needs a separate design, and this command does
not attempt it.

## One mutating merge at a time

`gs merge` takes one exclusive `.merge.lock` in the repository-shared Gitseq
metadata directory before it looks for an existing receipt or validates and
plans a fresh merge. Every linked checkout of that repository uses the same
lock. The command holds it through target and workroom remeasurement, approval
reservation, the tentative Git merge, commit, receipt-ref publication, durable
succession, and every returned-error cleanup. A second `gs merge` therefore
waits until the first has either sealed the whole transaction or removed its
reservation and aborted its tentative merge; it then reads the resulting HEAD
and workroom frontier for itself.

The lock uses the host advisory-lock primitive, not a persistent ownership
record.
The operating system releases it if the process dies, so a crash cannot leave
a stale lock file blocking all later work. Git or receipt state left by that
crash remains visible and fails closed for inspection and recovery.

The merge lock sits outermost. Snapshot and append paths may take `.config.lock`
inside it. Merge never takes `.publication.lock`, and publication never takes
`.merge.lock`; publication may separately take `.config.lock`. On platforms
without the required advisory file locking, mutating merge refuses instead of
running without exclusion. Direct `git` commands fall outside this cooperative
boundary and can still disturb an in-flight merge.

## Why an object ID and not a branch

`gs merge` passes the approved full object ID to `git merge --no-ff`,
never a branch name. Advancing the reviewed branch after approval
therefore cannot retarget the merge — the approved commit lands, and no
other.

## Approval scope and receipt

A ratified approval serves a single use, and it authorizes the exact candidate
to land once, into the branch its implementation request owes it to, in a clean
checkout of this workroom's repository. The first successful use fixes the
target's pre-merge head. Nobody can then replay the approval into another branch
or linked worktree.

Concurrent callers first reserve
`refs/gitseq/merge-receipts/<approval-hash>` with an atomic compare-and-swap.
Only one caller can proceed. A successful merge leaves three matching
records, followed by the artifact succession authorized by that receipt:

- merge commit trailers naming `Gitseq-Approval`, `Gitseq-Candidate`, optional
  paired `Gitseq-Authorization` and `Gitseq-Authorization-Ratification`,
  `Gitseq-Target-Repo`, `Gitseq-Target-Ref`, `Gitseq-Target-Pre-Head`,
  optional `Gitseq-Hold-Warning`, `Gitseq-Changed-Paths`, and
  `Gitseq-Left-Live`;
- the repository receipt ref, advanced from the target's pre-merge head
  to the merge head; and
- a signed workroom assertion naming the approval, candidate, target
  repository, target ref, target pre-head, merge head, and optional paired
  authorization and ratification.

The command checks Git receipts across all refs. The signed workroom assertion
also prevents replay if the repository later loses local refs and the branch
carrying the merge. An interrupted reservation fails closed so someone can
inspect it before the command allows any later merge.

The assertion and every successor and retirement use deterministic
idempotency keys. If submission stops part-way, run the same command again in
the checkout still at that merge head. It finds the immutable Git receipt and
resumes the missing suffix; it does not merge a second time or retire a
successor it already published. The command matches effective acts already
recorded by merger, words, body and ordered citations. It retains their
historical staleness testimony; current staleness does not turn a completed act
into a different idempotent request. Missing acts still pass normal admission.
When the complete suffix exists, the command returns without changing Git or
the durable log, and `merge-plan` reports `complete`. This retry does not
recreate a successor that someone retired after delivery.

Before creating the merge commit, the command builds the signed request for
every act in that succession batch. It checks each request with the kernel's
exact genesis-ceiling accounting and, when the caller uses `--server`, with the
resident's exact JSON request limit. The command replaces intra-batch labels
with canonical event identifiers of the same encoded length as the identifiers
the sequencer will mint. The command refuses a batch that admission would refuse
while `HEAD`, the workroom log, and the receipt reservation remain unchanged.

When the reviewed candidate artifact rests on its implementer's promise, that
artifact already serves as the implementation report. The sealed receipt closes
that commitment at its resolved destination; no implementation ratification
follows the merge. Delivery includes every eligible reporting artifact named by
the exact-head approval, not only the primary artifact. It does not depend on
retiring those artifacts: an empty retirement cut or a carried report still
counts as delivery. The review approval remains separate, and someone must still
explicitly ratify it before this command accepts it.

## Artifact succession

The command reads the first-parent diff of the merge that actually lands. It
treats stale artifacts as live until retirement and deduplicates work
across changed files. It retires covered predecessors in the target's world,
publishes their successors, and seals why every other covered candidate stayed
live.

| Situation | Enforced result |
|---|---|
| One live exact path covers an added or modified file, or a rename destination | The merge publishes one successor at that exact changed string. It retires in-target predecessors at that string; it accounts for other candidates, and they stay live. |
| A directory and something inside it both cover a landed path | The exact changed path receives the successor. An in-target wider pointer stays live as carried, with no cleanup obligation; outside-target candidates count as siblings or abandoned. |
| The merge removes a rename source or deleted file | The merge retires its exact old path with no successor there. Because removal changes covering directories, the merge may retire in-target directory pointers and publish the widest directory successor. |
| An in-target wider pointer covers a landed destination | It stays live and `Gitseq-Left-Live` records it as carried. It creates no cleanup obligation. |
| A non-target candidate has an unsettled commitment naming its head or reaching its artifact | It stays live and `Gitseq-Left-Live` records it as a sibling with the protecting commitment. |
| A non-target candidate has no unsettled commitment | It stays live and `Gitseq-Left-Live` records it as abandoned. Its author or a `ratifier` owes the bare supersession. |
| Testimony names a settled, mismatched, or unknown commitment | The receipt remains effective and grants no extra authority. The testimony stays unverified and the successor keeps its succession warning. |
| An artifact appears after the sealed snapshot | It falls outside the plan and the successor warns that the merge did not record succession. A later merge at the path accounts for it. |
| No live artifact covers an added or modified file | The merge publishes a first artifact at the changed file path. |
| The merge renames a file | The merge retires its exact old path without a successor there. The destination receives a first artifact or the successor for the live path already covering it. |
| The merge deletes a file | The merge retires its exact old path with no successor. A live covering directory still receives its successor because the directory changed. |
| A successor rests on the predecessor the same merge retires | The successor stays current. The work stood on what it replaces, and the merge that publishes one withdraws the other in the same act, so that withdrawal does not count as news arriving underneath it. Only artifacts that merge actually published — at its merge head, at a path it declared — read it that way; any other record citing the receipt goes stale as usual. |

`workroom/state@1` and later state schemas refuse new artifacts at
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
`merge_left_live` body field. It maps each artifact event to
`{"class":"carried"}`, `{"class":"sibling","commitment":"<event id>"}`, or
`{"class":"abandoned"}`. The field grants no retirement authority. The fold
checks both it and the sealed changed-path frontier from durable log facts at
the receipt's incoming frontier and uses them only to make the published successor's
accounting stable. The recorded classification remains historical evidence;
the current owed-supersession count stops suppressing a sibling once its named
commitment settles or retires. A carried pointer remains current and never
enters that cleanup count. Receipts without both prospective fields parse and
project exactly as before.

The receipt can itself settle the promise that protected an older candidate.
That sibling claim still verifies against the frontier before the receipt,
while the older candidate becomes cleanup debt as soon as the promise closes.
A promise already settled or retired before the receipt cannot protect its
claim, and a later promise cannot repair it. Fold profile `workroom-fold@20`
corrects this accounting when replaying existing signed receipts; it does not
rewrite them or grant new retirement authority. The artifact's author or a
ratifier still performs the cleanup.

Gitseq reports a live artifact that the receipt neither retires nor classifies
as left live against the receipt, as not classified by it. A file the merge
deletes does not count as such an artifact. It has no successor at its old path,
so the reviewed paths that bound cross-author retirement authority never reach
it, but the receipt does name it in `merge_retirements` with the empty successor
and its author does retire it afterwards. Fold profile `workroom-fold@23` reads
that pair — the receipt's own signed plan entry mapping the artifact to the
empty successor, and a standing retirement recorded by the artifact's author or
by a ratifier — as accounting for the deletion, and drops both the warning and
the cleanup count it added.

The explicit empty JSON string makes up the whole of the deletion shape, and the
fold reads it from the plan exactly as the receipt signed it, with its value
type intact and no normalising or conversion. A plan entry mapping the artifact
to any other string claims a surviving destination instead, so it answers at
that destination's path and carries no authority when the review did not cover
it, and an entry with the value `null` names no successor at all: both stay
reported as not classified by the receipt, however effective a retirement
follows them. A plan carrying any other value type for an entry, a number, a
boolean, an array or an object, does not form a plan the fold can read, so
Gitseq admits the whole receipt with no retirement plan: it retires nothing,
publishes no accounting and reports no entry at all. A named entry nobody
retired, one whose supersession the fold refused, and one naming anything other
than a live covered artifact stay visible too, and so does a covered artifact
the plan never named. The narrow authority map stays unchanged: naming an
artifact in a receipt still retires nothing on its own.

### Citations across a merge

Documentation names the artifacts that vouch for the behaviour it describes, so
the pages cite exactly the pointers a merge has to retire. Refusing every cited
retirement would refuse every merge in a documented area, and the usual advice —
repoint the pages first — nobody can follow, because the successor does not
exist until the merge lands.

So the command separates the two cases. A retirement this merge succeeds goes
through: the supersession names the successor artifact, the successor stands at
the same path or at a directory covering it, and a page naming the old pointer
flares and learns where to re-anchor. The command refuses a retirement with no
successor before `HEAD` moves, naming the pages, exactly as
[`gs supersede`](supersede.md) refuses one: nothing replaces the pointer, so the
pages would have nowhere to go. Retiring it anyway takes a deliberate act with
`gs supersede --cited-ok` once the pages have moved.

The documentation gate reads the same distinction. The gate reports a citation
of a retired artifact whose retirement names a covering successor as a flare; a
citation of a retirement that names nothing still fails the set.

Before reserving the approval or moving `HEAD`, the shared evaluator prepares
the exact merge in a disposable clone and runs this check on that staged result.
The governed checkout therefore cannot hide a citation the candidate adds or
retain one the candidate deletes. `merge` consumes the validated succession,
then stages the candidate in the governed checkout and verifies that its
changed-path frontier still matches the plan. It does not repeat the same
pre-commit citation scan against a second checkout. Only then does it create
the receipt commit and publish the durable succession; retry-safe succession
recording still validates the landed checkout before appending its suffix.

### Who may retire another actor's pointer

Free-standing [`gs supersede`](supersede.md) requires the target's author or an
actor holding `ratifier`. Merge succession makes the one narrow exception, and
the fold checks all of it from the log alone:

- the supersession cites a merge receipt signed by the same actor;
- that receipt cites a ratified, effective approval whose verdict reads
  `approved` and whose head matches the merged candidate;
- the approval cites an implementation artifact standing at that candidate and
  written by someone other than the approver;
- the author of that implementation artifact signs the receipt — an
  approved head's own author merges it;
- the target's path lies on the path lineage of one of the paths that approval
  reviewed — the same string, or one path containing the other, which the
  command reads in the narrower single direction described below; and
- the target carries a successor path in the receipt's signed plan, that path
  covers the target's own path, and the supersession cites the successor
  artifact published there.

### What bounds this, and what does not

The fold works purely over records. It holds no repository, so it cannot open
the merge head, read its diff, or establish that any merge happened at all. The
same actor asking for the authority writes every other field of a receipt — the
merge head, the retirement plan, the successor list — and a signer can publish
an artifact at any path. So none of those fields bounds anything.

The approval does. Of the parties to a merge, only the reviewer did not write
the receipt, so the fold reads what the receipt may reach from the verdict's own
citations: **the artifacts the approval rests on**, and nothing else. Their path
lineages make up the whole reach.

One head holds one body of work, and a body of work spans the paths it changes,
so a verdict citing a single artifact could succeed the pointer in one tree
while the other three it changed stayed on a predecessor nothing would
supersede. `gs review` therefore takes `--artifact` more than once, and a
reviewer signs the whole set they read. An approval citing one artifact reaches
one path, which matches what every approval written before this existed does.

Nothing infers the set. The actor asking for the authority writes anything
derived from what the implementer published: seeding a candidate at an
unrelated path, then obtaining an approval that cites only the legitimate one,
would have reached that path too. Requiring the claims to predate the verdict
closes minting afterwards and does nothing about seeding beforehand. Citation
closes both, because signing a record fixes its bases.

Gitseq still checks each member on its own: effective, not withdrawn, standing
at the exact head the verdict names, and the implementer's own, so a citation
cannot smuggle in a pointer belonging to someone else or describing another
commit. `merge` holds every member to the same staleness rule it holds the
primary to: ordinary staleness passes and the merge records it, while a member
that already described a superseded world at the verdict stops the merge.

What none of this establishes deserves saying. Holding no repository, the fold
cannot open the approved commit or read its diff, so it does not know that the
head touches the paths cited; it knows that the reviewer signed for them.
Without it, the author of a single approved implementation could invent a merge
head, name a stranger's artifact anywhere in the log, publish a successor at its
path, and retire it.

`merge` checks that same signer before it starts, not only the fold afterwards.
The fold sees a receipt, and the merger writes a receipt after Git has
committed: by then the target has moved and the merge has spent the approval, so
a refusal there arrives too late for anyone to obey.

That fingerprint makes the whole test, and no role stands in for it — not
`ratifier`, which [`gs supersede`](supersede.md) otherwise lets retire anything.
A role counts as live standing, and someone can revoke it between the check and
the acts it would authorize, while the tentative merge runs or while the
succession lands one act at a time; the fold would then refuse what the check
allowed, after `HEAD` had moved. The authorship of an approved artifact states a
fact about a record that has already happened, so nothing can withdraw it
mid-merge. A merge signed by anyone else needs an authorization that survives
concurrent revocation, and none exists today.

This page states the cost rather than working around it. A merge carries
cross-author authority only within the paths its approval reviewed and only for
in-target predecessors in its sealed retirement plan. Other candidates stay with
their own authors or an actor holding `ratifier`, where they sat before merge
succession existed. The receipt accounts for those candidates without turning
testimony into authority.

A retirement with no successor — a deleted path — takes no authority from a
merge either, because nothing the merge published stands over it to bound the
claim.

`merge` reads a reviewed path in one direction only: it reaches that path and
whatever stands beneath it, and nothing above it. An approval reviewing
`docs/how-to/x.md` reaches another actor's pointer at that exact path and not
one at bare `docs`, because the wider pointer speaks for trees the head never
put in front of the reviewer. The merge carries the wider in-target pointer,
which needs no retirement authority; a wider outside-target candidate remains
with its author. The older reading reached upward, and it let a merge that
reviewed a leaf claim the tree containing it. The fold's own
lineage test stays unchanged and still reads both directions; here the command
holds itself to the narrower rule while the target has not yet moved. It
governs merges run from here on. It reinterprets no sealed receipt.

The check belongs to fresh merges only, and runs once in the shared read-only
evaluator, before Git reserves the receipt ref and before the command appends
any durable Workroom record. The evaluator stages the exact candidate against
the exact target in a disposable clone, so classification uses the same
prospective changed paths without touching the governed checkout. Succession
recording never re-applies the guard. Resuming an interrupted merge instead
finds the immutable Git receipt and appends its recorded suffix without
replanning and without this guard, so a receipt sealed under an older reading of
reach keeps exactly the authority it carried when sealed.

### Reader compatibility and deployment

The merge-succession change advanced the state schema to `workroom/state@1`,
and the commitment-lifecycle change advanced the fold profile to
`workroom-fold@4`. The schema/fold split advances the schemas to
`workroom/state@2` and `workroom/ratify@1`, and advances the application
projection profile to `workroom-fold@5`. Older state and ratification records
remain readable, but a binary built before these changes projects commitments
under the old contract and cannot interpret the new schemas. These describe
historical compatibility transitions, not instructions to restart a service
after each source merge. Reader deployment and host-binding replacement need
their own authorized transition and compatibility evidence; a pushed source
head or sealed implementation receipt performs neither.

The prospective left-live accounting rule advances the projection profile from
`workroom-fold@10` to `workroom-fold@11`. Historical receipts without
`merge_left_live` and `merge_changed_paths` retain their existing projection
behavior.

The receipt checkpoint described above advances the current projection profile
from `workroom-fold@11` to `workroom-fold@12`. A cache written under `@11`
answers with merge successors stale at birth, so Gitseq rejects it and replays
the history. This does not affect historical receipts without the prospective
pair.

## See also

- [`gs review`](review.md), [`gs supersede`](supersede.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
