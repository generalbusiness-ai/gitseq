---
title: The work loop
summary: How promises become exact artifacts, independently approved merges, or explicit reports.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:608be185aaba9343eba9175c04bf10a20a04b015
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd7ea9e4bc9d97dd95133d999766029d1bd60cf6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2198b8aaa2da6921f555c380d24385edaabcb787
---

# The work loop

## The shape

A **request** names its addressee, conditions of satisfaction and result.
For a new request, choose a target branch, inherit a request target, or state
`no_git_artifact=true`. The filing boundary resolves a named branch to this
repository and its current head; callers supply neither measurement.
A **promise** rests on that request and shows that work has started. For implementing work, an
**artifact** rests on the promise, names the exact implementation head, and
serves as the completion report. An independent reviewer records a verdict;
the review requester ratifies an approval before `gs merge` may use it. The
sealed merge of that approved exact head into the owed destination closes the
implementation commitment. Approval, Git incorporation and a sealed landing
remain separate facts.

```text
request ─▶ promise ─▶ artifact ─▶ independent verdict ─▶ ratify ─▶ merge ─▶ satisfied
 alice      bot         bot              reviewer            bot      approved chain
```

The artifact removes a duplicate `ready-for-review` record; it does not remove
scrutiny. The review, its verdict, its explicit pre-merge ratification, and the
different-agent rule remain unchanged. The merge constitutes the durable
acceptance of the implementation result, so no second ratification follows it.

Work whose request explicitly owes no Git artifact uses the general route: the promisor
files an explicit **report**, and the original requester ratifies it, so long
as that requester remains a live participant. You never declare your own
unmerged work complete. Retiring the requester's membership retires this
authority with it: the log keeps a later ratification but the fold judges it
ineffective, even when the promisor filed the report after they left.

A promise stays optional. The addressee may report directly against the request
when they hold no live promise of their own on it. Once they promise, their artifact
or report closes that one claim. Self-initiated work follows the adopted
basis directly, without a self-request or self-promise; see
[the agent discipline](../../SKILL.md).

A free-standing promise projects as dangling, because nobody holds the
position to declare it satisfied. The fold rules a report on a promise that
does not resolve ineffective in turn, so an unearned approval cannot carry
force.

## Every state, and what causes it

A commitment pairs one request with one promise. A request nobody has
promised counts as a commitment too, with the promise half empty. The fold
projects the thirteen statuses below. `terminal` separately records a landed,
reported or abandoned closure; it adds no further lifecycle status.

Before anyone promises:

```text
                    ┌─ requester retires the request ──▶ withdrawn
                    │
request ─▶ open ────┼─ a cited event is retired ────────▶ stale
                    │
                    └─ someone promises ───────────────▶ promised
```

After a promise, the artifact route passes through `awaiting-review`, then
`awaiting-authorization` if a hold needs release, and `awaiting-landing`.
A sealed approved merge closes it as `satisfied`. The no-Git-artifact route
passes through `reported`; requester ratification closes it as `satisfied`.
Retirement and explicit transfer or abandonment have the meanings below.

| Status | What it means | Who caused it |
|---|---|---|
| `open` | Asked, unclaimed. | the requester, by asking |
| `withdrawn` | Someone retired the request before anyone promised. | the requester, or a ratifier |
| `promised` | Claimed, not yet reported. | the promisor |
| `reported` | Completion claimed by an explicit report, awaiting requester ratification. | the promisor |
| `awaiting-review` | Completion claimed by an artifact that no ratified approval names yet; waits on the performer, who next obtains independent review. | the promisor |
| `awaiting-authorization` | A ratified approval names the artifact, the request carries a hold, and no release names this candidate and approval; waits on the hold owner. | the requester, by holding the landing |
| `awaiting-landing` | A ratified approval names the artifact and nothing holds it; waits on the performer, who signs the merge into the target ref. | the promisor |
| `superseded` | An explicit linked supersession transferred a rejected repair, or carried an approved artifact into a successor request. `successor_request` names that request. | the requester, or a ratifier |
| `satisfied` | The approved exact head merged, or the requester accepted an explicit report. | the merge or the requester |
| `cancelled` | Someone retired the request after a promise existed. | the requester, or a ratifier |
| `reneged` | Someone retired the promise. | the promisor, or a ratifier |
| `abandoned` | A supersession deliberately dropped an approved head instead of carrying it. | the requester, or a ratifier |
| `stale` | Something it rests on died, and no live report stands. | nobody — a consequence |

Five details qualify these states.

**Retirement beats staleness.** A retired request projects `withdrawn`,
not `stale`, even when both hold.

**Unfinished stale claims retain their evidence.** When no live completion reports a claim, current folding can replace
`promised` with `stale` while retaining the promise and waiting party. Status and work then classify a claimed stale row as `not_actionable`;
an unclaimed stale request remains available to its addressee. Staleness
means neither withdrawal nor completion.

**Cancelled beats reneged.** If someone retires both the request and the
promise, the commitment reads `cancelled`.

**Completion follows the stated result.** A request owing a landing cannot
close through an explicit report: an admitted `resolution` report serves as
evidence in `latest_resolution` and changes neither status nor waiting party. A sealed
receipt takes precedence; otherwise the newest live approved reporting artifact,
then the newest live reporting artifact, answers the claim. An explicit
no-Git-artifact request closes only through a report and requester ratification.
An artifact on that request stays visible but does not report its completion.
Legacy requests retain their historical completion authority.

**`satisfied` and stale can coexist.** The fold computes staleness while
it reads the completion and closing records, so one commitment can carry
both satisfied and stale. A later movement under an already merged result
does not erase the fact that the merge happened.

## Where the definitions live

Three tiers enforce what looks like one workflow: the fold, the guarded
commands, and convention. Nothing in the projection distinguishes them, so
this page says so plainly.

The fold enforces, in `internal/workroom/kinds.go`:

- which kinds carry a lifecycle edge, and the basis each requires — a
  promise rests on exactly one request, a report answers one promise or an admissible direct request;
- who may confer force, through each kind's satisfier: the originating
  requester for a report, the `ratifier` role for `propose`, `assert`
  and the governance kinds.

And in `internal/workroom/fold.go`:

- the statuses above, their resolved destination and hold, and which event causes each;
- that its promisor reports a promise, and its addressee reports a direct
  request only while that actor has no live promise on it;
- that a promisor's exact-head artifact resting on one promise discharges the
  same report obligation, and a sealed merge receipt for that artifact closes
  the commitment;
- that the actor who made an act, or a ratifier, may retire it, and that
  the retirement must rest first on its target;
- that an artifact names both a path and a commit;
- which artifact a review judges, resolved only when the cited artifact
  and the claimed head agree, and, from the reviewer's and implementer's
  fingerprints, whether that review counted as independent;
- that a live artifact with a later live artifact at the same path owes
  its retirement, projected as an omitted supersession.

The **guarded commands** enforce a second tier that the fold projects but
does not itself refuse. [`gs review`](../reference/gs/review.md) requires
a clean checkout sitting on the artifact's exact commit.
[`gs merge`](../reference/gs/merge.md) refuses an unratified approval, a
verdict other than `approved`, a candidate that differs from
the approved head, an approval that does not causally rest on its named
artifact, an artifact whose commit differs from the candidate, an act
that the fold does not project as a review at all, and an approval signed by the
actor who implemented the head — or one whose independence the record
cannot determine.

The merge gate still requires a ratified review approval before it can
act. The merge does not ratify that review verdict; it closes the separate
implementation commitment after the approval chain has already authorized it.

The rest falls to **convention nothing checks**: branch and worktree
naming, the `Rests-On:` trailer on the source commit, deleting the
worktree after merge, and pushing to origin. The lifecycle also accepts a
promise followed straight by an explicit report for work that does not merge.
The merge guard, not the fold, stops such a report reaching `main`.

So a green projection gives narrower evidence than it appears to, but not
empty evidence. It shows that nobody claimed authority they did not hold,
that each act carries the bases its own kind requires — a promise resting on exactly one
request, an explicit report or reporting artifact on its admitted claim — and, where `gs merge` merged the work, that an
independent reviewer approved that exact commit and the approval received
ratification. It does not show that anyone named the branch well, that the
commit carried its trailer, or that anyone tidied up afterwards.

New admission refuses a canonical `rests_on` identifier that names this
workroom but no position in its sequence. Admission carries foreign-workroom
identifiers and other opaque references without that check, and historical
unresolved citations remain readable. The fold assigns meaning only to bases
it can resolve. Copy full canonical identifiers from tool results and inspect
their meaning; an effective record does not prove that a foreign citation
supports its claim.

## The result, hold and delivery audit

A named result supplies `target_ref=refs/heads/<branch>`; the boundary fills
`target_repo` and the advisory filing-time `target_head`. `target=inherit`
walks request ancestry up to eight levels and refuses missing or conflicting
nearest targets. An explicit no-Git-artifact request stops inheritance through
it. See [request authoring](../reference/gs/state.md#request-authoring-what-a-request-owes).

A landing request may state `landing=held`, with the requester as owner unless
`hold_owner` names another live actor. Inheritance preserves the hold for that
destination. Only its owner may release it, through the exact ratified report
on the performer's authorization request. A new unheld request needs no
release. The [merge contract](../reference/gs/merge.md#held-landings-and-the-compatibility-window)
describes the current warning window for an unreleased hold and the distinct
legacy authorization behavior; those compatibility rules do not turn approval
into a release.

`approved_not_landed` asks whether the selected approved artifact has a sealed
receipt for its destination. It does not depend on source closure or current
Git ancestry: a receipt may close the source commitment through changed-path
companions while carrying the selected artifact. Legacy satisfied rows can
therefore remain in the artifact landing audit. Preserve their historical
status and projected waiting party. Current ref removal, missing objects or
unknown ancestry never erase a sealed receipt. See
[landing observations](../reference/landing-observations.md) for the fields,
count labels and bounded Git checks.

## Honest states

A completion artifact before merge, or an unratified explicit report, counts
as **honest status**, not failure. The artifact reads `awaiting-review` and waits
on its performer until an approval names it, then waits on the hold owner
if the hold needs release or on the performer for landing; the
explicit report reads `reported` and waits on its requester. Do not treat
either as a gap to chase.

Superseding your own promise counts as **reneging**, and it stays visible
forever. Do it as early as you know you cannot keep it: early reneging
earns honour, late reneging does not.

If the requester supersedes the request after you promised, that releases
you. The promise stays in history as kept faith, not fault.

An ordinary request retirement after a promise reads `cancelled`. A rejected
implementation round reads `superseded` only when the explicit retirement also
cites one effective repair child from the same requester, that child directly
rests on the old request, and a live ratified `changes-requested` verdict names
the reporting artifact and its exact head. `successor_request` holds the pointer;
the child's later outcome stays on the child row rather than rewriting the
historical transfer.

If the requester read the request as unclaimed and wants to change its
addressee, the read can race a promise or direct completion. The guarded
`reassign_if_unclaimed` / `gs reassign-if-unclaimed` path signs the exact old
request, retires it only while it stays live and fresh with neither fact present,
then publishes a replacement naming that exact retirement only if the facts
remain unchanged. The path allows unrelated log traffic. A commitment change
refuses and requires a fresh read. Ordinary supersession remains the route for
a requester who knowingly withdraws promised work.

## Bridging work to code

Work that changes files needs a bridge to the decisions that motivated
it, or staleness tracking cannot see it — and then the record lies by
omission, the one failure this system exists to prevent.

Two halves:

1. The implementing commit carries a trailer, `Rests-On: <event>`. The
   governing event must exist **before** you make the commit; otherwise
   you have to amend the trailer in afterwards and the hash changes.
2. An `artifact` statement cites the commit as `path@commit`, rests on the
   promise it fulfils, and may also cite the decisions and implementation
   artifacts that govern it. That artifact serves as the implementation report.

The artifact serves as the durable pointer to implementation truth. Branch and
head hints in a request, promise or report body offer conveniences for
associating a local checkout; they claim nothing about that checkout's
cleanliness or currency.

## Review at an exact head

A review verdict concerns one immutable commit, not a branch. If
the branch moves after the reviewer signs the verdict, the approval no longer
describes anything anyone looked at.

[`gs review`](../reference/gs/review.md) enforces that boundary. It
requires an effective, unretired named artifact; an effective, unretired
named promise that the reviewer owns; and a clean checkout sitting on the
artifact's exact commit. It
derives the originating request from the durable graph rather than
letting you retype it, and the report it signs names the immutable head.

Staleness alone it does not refuse. The reviewer judges whether a moved
world matters to this exact commit, so the review goes ahead and the
verdict records what had moved.

After the review requester explicitly ratifies an approved report,
[`gs merge`](../reference/gs/merge.md) enforces the other boundary. It
refuses an unratified or retired approval or artifact, one that already
described a superseded world when the reviewer signed the verdict, a non-approval verdict, a candidate other than the
approved head, and a dirty checkout. That list omits ordinary staleness:
the reviewed head cannot change, so the merge lands it and records
what had moved in its receipt. An undated superseded world refuses; the
merge records a world that moved only after the verdict. It hands Git the approved object ID, never
a branch name, so advancing the reviewed branch cannot retarget the
merge. Its sealed receipt then closes the implementation promise whose
reporting artifact received review; it does not replace or imply the earlier
review ratification.

Running tests and poking at the checkout still supply the reviewer's
evidence. These commands do not replace judgement; they fix the state at
which the reviewer records the judgement.

## Promoting from conversation

Talk in the ephemeral channel until something crystallizes, then promote
it: a durable act with the selected signed frames embedded as
`evidence`. A stranger can verify it later, after the conversation has
gone. Select honestly and summarize faithfully.

Promote a breakdown only when it changes scope, changes a condition of
satisfaction, or creates follow-up work. Routine progress stays
ephemeral.

## See also

- [Run a work loop](../how-to/run-a-work-loop.md) — the same shape as
  commands that run.
- [`gs state`](../reference/gs/state.md)
- [Staleness](staleness.md)
