---
title: Lane identity, reconciled against current source
date: 2026-09-09
status: candidate
reconciles: notes/2026-08-27-lane-identity.md@6131249c (adopted by proposal cb755aa1, ratified 986a1ca2)
base: main d1ae6ade996af7167f22a2170dfaa6a200e004e1
---

# Lane identity, reconciled

Every source reference below is a `path:line` reference, which the reading
view resolves against this note's own exact head. None pins a revision,
because a reference naming another commit is refused as one this record does
not cite. None is a Markdown link either: the local-links gate resolves a
link target as a path relative to this file and stats it, so a `path:line`
target fails that gate while the same text in an inline code span both passes
it and opens the preview. The lines were read at main
`d1ae6ade996af7167f22a2170dfaa6a200e004e1`. Where a line has since moved, the
reference names its position at this head rather than the position it was
read at, because a reference that opened somewhere else would be worse than
none.

The superseded design is absent from this head, so it is cited by revision as
`notes/2026-08-27-lane-identity.md@6131249c` and is not a reference.

## Problem at current main

The adopted design named a real gap and it is still open: nothing joins a
checkout or a branch to the durable work that governs it by the commit's own
content. `refs/gitseq/lanes` holds nothing, `cmd/gs` has no `worktree`
subcommand (`cmd/gs/main.go:108`), and no `gitseq.*` Git config key
is read anywhere. Two deliveries since adoption narrow what is left: a read only
checkout inventory with protection and deletion advice, and a validated durable
field for the destination a request owes.

## The trailer distinction the old text missed

The adopted text said a commit hash seals a `Rests-On:` trailer, "so commit to
work item is already durable and unforgeable". Two different trailers wear that
name. A kernel event commit carries `Rests-On:` in a signed envelope
(`internal/intent/envelope.go:22`) and compares it byte for byte
with the signed intent (`internal/kernel/kernel.go:1535`); a
mismatch refuses the event, so that trailer is unforgeable. An implementing
source commit carries `Rests-On:` as ordinary message text that nobody signs and
nothing verifies, read only for display
(`internal/gitstore/graph.go:45`). Anyone who can write a commit can
name any event. The hash seals the bytes of a claim, never its truth.

## Trust boundary

Four things stay distinct: **claimed identity** (a source trailer, a Git author
ident, a per checkout record, a lane ref, a branch name, a body path);
**verified durable evidence** (a signed statement and the actor its signature
names, the request naming an addressee, the promise its performer signed, the
artifact naming an exact head, the ratified approval, the sealed receipt);
**local location** (a path, a label, a lock, a lease); and **actual permission**
(what the fold admits, what the filesystem allows). A claim is shown as claimed
and never as governing until durable evidence corroborates it; it never widens
the deletable set and never authorises a signature. A shared head, an expired
lease, or a missing claim never makes a tree removable, and branch controlled
input retires nothing by itself (`SKILL.md:539`). Every
association is scoped to one workroom by genesis; a value naming another genesis
is carried as typed.

## Layer 1: bounded reverse association, read only

The resident derives from Git a table of checkouts and branches, the durable
work each claims, and the durable work that corroborates it. It reuses, without
adding a competing store: the checkout inventory with its 8 second cache, 128
checkout cap, symlink resolution and basename only labels
(`internal/app/app.go:679`); the hardened trailer scan with NUL
framing and per object hash re verification
(`internal/gitstore/graph.go:40`); the bounded ref inventory and the
ancestry graph that answers unknown rather than false
(`internal/app/landing_graph.go:77`); canonical resolution
with typed refusals naming at most eight candidates
(`internal/eventref/eventref.go:32`); and the closed Git
environment allowlist (`internal/app/app.go:357`). New is one
association pass, taking an explicit ref set and depth limit instead of the fixed
newest 81 (`internal/service/ui.go:60`), walking first parent lineage
from each branch tip until it meets the resolved target ref or a limit.

- `claimed`: a trailer, a lane ref or a checkout record names a resolvable event
  here. Nothing else is examined, because a claim grants nothing.
- `corroborated`: a signed artifact statement names this exact commit, and the
  existing owned edge ties the actor its signature names to the governing
  record, as a standing promise that actor signed or a request addressed to that
  actor (`internal/reviewguard/binding.go:234`), or, for self
  initiated work, as the adopted decision the same code already recognises
  (`internal/reviewguard/binding.go:408`).
- `unresolved`: names no event here, or more than one; the typed refusal and its
  candidate list are carried verbatim.
- `foreign`: a canonical identifier of another genesis.

Corroboration attaches to a commit, never to a branch: a tip whose lineage holds
a corroborated commit is corroborated at that commit and claimed beyond it,
because the later commits are not the head anyone signed for. The only actor in
the rule is the one an event signature names: no Git ident is read, matched or
mapped, and no second actor identity system appears. A commit's author ident is
committer controlled, alongside its subject, body and trailer values
(`internal/gitstore/graph.go:36`), so an attacker who copies a real
performer's author text and their exact `Rests-On:` trailer still produces a
`claimed` association and nothing more. Promotion needs a signed artifact naming
that commit, and that needs the performer's key.

Association is a pure function of the captured Git snapshot and the projection
frontier, and ambiguity never picks a winner. Missing objects, shallow
boundaries, a foreign genesis, budget exhaustion and deadline expiry all yield
unknown; unknown never proves a negative, and the whole batch's advice is
discarded on exhaustion, as today. Bounds are the existing 65,536 step budget,
4,096 commitment cap and 3 second deadline
(`internal/app/worktree_landing.go:12`,
`internal/app/landing.go:45`), plus 4,096 refs, 256 branch tips,
512 commits per tip and 1 MiB per commit object. Results cache under the durable
frontier paired with the observed ref inventory digest, so ref movement at an
unchanged frontier invalidates the cache. This layer writes nothing, anywhere.

## Layer 2: guarded creation and stamping

`gs worktree <selector>` creates one checkout for one governing record. The
selector resolves through `internal/eventref`, so `#N`, an unambiguous fragment
and a canonical identifier all work. The record may be a request addressed to
the acting actor, or an adopted decision, which is how self initiated work is
named without inventing a self request
(`internal/reviewguard/binding.go:408`). Refusals before any
filesystem write: ambiguous or unresolved selector; a record that is neither a
request nor an adopted decision; a destination path that exists, is a symlink, or
resolves outside the configured checkout root; a repository that is not this
workroom's, by common Git directory
(`internal/mergeplan/mergeplan.go:351`); an existing branch not
asked for.

### A settled lane: three separate decisions

The adopted text made one rule out of three questions, so they are answered
separately.

**Local creation** makes a directory and a branch. It signs nothing and admits
nothing, so it refuses only on what it checks itself, listed above. When the
governing commitment is already settled by the existing word list, now read
through one shared vocabulary
(`internal/workroom/commitment_status.go:50`), it warns, names the
settling event, and proceeds only behind an explicit confirmation flag. It does
not refuse on the per checkout record alone: a value the actor can write is a
value the actor can unset, so refusing there stops only honest actors, and an
unreadable projection must not deadlock one.

**Authoring assistance** warns and never blocks. What it knows is the durable
record as last read, which may be stale or unreadable, and unknown is reported as
unknown, not as consent.

**Durable admission** is untouched by this design: the fold decides
admissibility from the durable record alone, neither a confirmation flag nor a
per checkout record is an input to it, and a settled commitment refuses exactly
the acts it refuses today. Unknown local evidence is never permission where the
fold requires proof, and a flag that silences a local warning cannot reach a
durable authority check, because the two never meet.

### The per checkout record

The record is a small JSON file in the checkout's own private Git directory,
which `apphost.ResolveGitDirs`
(`internal/apphost/config.go:420`) already separates from the
common directory, written through the existing atomic file and flock pattern
(`internal/apphost/config.go:346`). It holds the full canonical governing
identifier and this workroom's genesis, so a record copied from another
repository is detectable, and it is read as evidence, never permission.

The adopted text put this value in `git config --worktree gitseq.request`. That
is a live option, not a forbidden one. The earlier note at
`notes/2026-08-21-notes-and-decisions.md:238` is
about who signs: it rejects a clone wide `gitseq.actor` default because the
signature is the attribution, it keeps identity "scoped to the process or the
worktree", and its remark that there is no `git config gitseq.*` describes what
`gs` supported that day. Neither sentence governs an advisory governing record
key.

The JSON file wins on cost instead. Migration: worktree scope is read only once
the repository sets `extensions.worktreeConfig`
(`internal/app/app.go:384`), a repository wide change altering config
reading for every checkout and every actor, for one advisory value. Locking: the
atomic write and flock pair already exists and already holds per checkout state,
while `git config` offers no discipline this code shares. Maintenance: that
configuration is a scope the source documents as executable rather than merely
readable (`internal/app/app.go:386`), so anything kept there stays an
execution surface to reason about, while a private Git directory file is removed
with the checkout by the machinery that made it.

Trailer assistance is a commit message template naming the exact `Rests-On:`
line, and only the full canonical identifier is ever written
(`SKILL.md:440`). A hook is optional, off by default, installed
only with an explicit flag, only when no hook of that name exists and
`core.hooksPath` is unset or inside this repository. It is one
`prepare-commit-msg` script that appends one line from the record it was given,
execs nothing else, and never replaces a user hook. Nothing is load bearing on
it, because `gs merge` deliberately runs no commit hooks at all
(`docs/reference/gs/merge.md:239`).

## Layer 3: deterministic lane refs

Tooling maintains `refs/gitseq/lanes/<governing event hash>/<attempt>` pointing
at the lane tip, mirroring `refs/gitseq/merge-receipts/<key>`
(`internal/mergeplan/mergeplan.go:1075`). The hash is the
request's, or the adopted decision's for self initiated work, and the attempt
suffix is what gives each recut its own ref; one ref per request hash cannot,
because a recut under one request shares its hash. Every write goes through
`gitstore.UpdateRef` with an expected old value
(`internal/gitstore/gitstore.go:338`), so competing creation
loses cleanly, a retry at the same tip is a no op, and allocating an attempt is a
create against a missing ref. The ref survives checkout removal and branch
deletion, which is its point, and grants nothing: read back, it is `claimed`.

## Layer 4: validated display fields

Two different branches are involved, answering two different questions.
`target_ref` is the landing destination: where the request's artifact must land,
validated by `validBranchRef`
(`internal/workroom/landing.go:121`), resolved in the repository
at filing, and enforced by the merge. `branch` is the implementing checkout:
where the work happens before it lands anywhere. Both are documented today
(`docs/reference/gs/state.md:70`), and this very request carries
`target_ref=refs/heads/main` with `branch=request/lane-worktree-identity`.

The adopted request `branch` field is therefore kept, not dropped, and it is
validated. Request, promise, report and artifact kinds gain an optional `branch`,
checked by the same `validBranchRef` and admitted as a typed optional field of
the declared vocabulary (`internal/workroom/kinds.go:22`). It is
display and search only: it never substitutes for `target_ref`, never moves a
merge, never licenses a deletion and never corroborates an association.
Validation buys a storable, renderable string, not authority, and admission is
keyed by statement schema, so the same name on an older record stays opaque
prose. This advances the statement schema and the fold profile version
(`internal/workroom/schema.go:16`), replaying history once.

No worktree path field is added to any durable kind. A path is machine local and
not portable across the actors and repositories that read one log, and the
current surface deliberately keeps other checkouts' paths inside the resident
boundary (`internal/app/app.go:199`). The checkout label stays in the
ephemeral endpoint.

## Cleanup, duplicates, refless heads, scratch trees

One captured read serves the whole request. The checkout listing is taken once,
the bounded ref inventory read once, and the deadline and step budget opened
once; the association and this classification both read and spend from that
single capture, so they answer about one repository and the bound is on the
request rather than on each judgment separately. Reaching any bound, whether
the shared budget and deadline or one of the association's own, withholds the
whole response rather than the judgment that noticed it, which is what
discarding the whole batch's advice means. What they share is the observation
and the bound, never the conclusion: the classifier reads no grade
and the association reads no classification, because an unsigned trailer is not
something a deletion decision may stand on.

Advice stays read only and in one place. `gs merge` and request retirement print
the current advice and name the checkouts that became candidates; neither
computes its own answer, neither deletes anything, and removal stays a separate
deliberate act. A checkout is a candidate only when all hold: not current, not detached, not
dirty, not locked, not prunable; its resolved path is inside the configured
checkout root and is not a symlink pointing outside it; no unsettled or
`approved_not_landed` commitment names any head on it; and its tip is proved
incorporated into a witnessed target or was explicitly abandoned. The middle of
that list is already enforced
(`internal/app/worktree_landing.go:47`); containment and
symlink direction are new. Unknown protects.

### A pending decision protects its candidate

An artifact a live proposal cites is a pending decision candidate, and its
checkout stays protected while the parent request is unsettled, even when that
request is ordinarily stale or missing from a default actionable list.
Staleness is not settlement, and a filtered board view is not the record.
Disclosure #22184 is the example: note artifact 3c7fab9d was retired and its
branch and checkout deleted while proposal c64474ff rested directly on it and
the parent request still held a live promise. The work survived only because
commit 6c862934 outlived the deletion (planner assert 9d80e5c9).

The classifier therefore reads a proposal's structural `rests_on` edge to an
artifact, and never assumes prose naming a head is the only reference. Request
lifecycle, candidate retirement, decision adoption and checkout removal stay
four distinct facts: none is evidence for another, and none substitutes for
another.

Duplicate checkouts at one head are reported at inventory time and warned at
creation, with an explicit flag to proceed, not refused outright: a second
checkout at the target ref's own head is ordinary, and this repository has one
today. A refless head, a checkout whose `HEAD` is contained in no ref here, is
reported, never deleted, and protects its checkout. Mutation test scratch trees
follow the existing harness shape: created under the process temporary
directory, hooks emptied as the disposable clone already does
(`internal/mergeplan/mergeplan.go:1585`), removed by the
process that created them by recorded path only
(`cmd/gitseq-perf/main.go:833`). Nothing searches for
scratch trees it did not create.

## Three wording questions, answered

The S1 request asks that three questions it carries forward from message 4128
be settled here before its head lands. Each answer says which stage it binds.
None changes a semantic the adopted proposal fixed, and where an answer would
extend an adopted rule rather than read one, that is said plainly.

**Unreadable projection against selector and record eligibility (S2).** These
are two questions and the note already answers both. An eligibility refusal
fires on a determinate negative: a selector matching no event here or several,
a record that is neither a request nor an adopted decision, a request
addressed to somebody else. An unreadable projection yields no such answer. It
yields unknown, which layer 1 keeps distinct from unresolved and which never
proves a negative. So creation must not report an eligibility check as passed
when the projection could not be read, and must not report it as failed
either. It says the check was not established, warns, and creates. What it
creates grants nothing: the record is evidence and not permission, and durable
admission reads the durable record itself, so nothing an unreadable projection
let through can reach an authority check.

One case does not follow from the refusal list as written. A `#N` or a hash
fragment resolves against the projection, so with the projection unreadable
there is no canonical identifier to stamp and creation has nothing to write. A
full canonical identifier is classified without reading any log, so that case
proceeds under the paragraph above. Refusing a short selector for want of a
resolvable identifier is the only sensible outcome, but it adds to the adopted
refusal list rather than reads it, so it is named here as an S2 proposal item
and not slipped in as settled.

**New branch spelling against historical shorthand (S3).** The optional
`branch` field layer 4 adds is checked by `validBranchRef`, which expects a
full `refs/heads/...` value. Historical records carry the shorthand instead,
and the request this stage rests on carries the shorthand in its own `branch`
value. Both stay true because admission is keyed by statement schema: the
validated field exists only for records written under the new schema, and the
same name on an older record stays the opaque prose it always was. No
historical byte is rewritten and no old record becomes newly invalid. If
authoring ever expands a typed `request/<slug>` into the full ref, that is a
documented normalisation at the authoring boundary, never a widening of what
the validator accepts.

**Source isolation against operational reader rollback (S3).** Landing layer 4
by itself keeps its one replay attributable to one merge and makes the source
change one revert. It does not make the schema advance reversible. Once an
event is written under the new schema, a reader that does not know that schema
rules it ineffective rather than decoding it, and reverting the source does
not unwrite the event. So the isolation buys attribution and a clean source
revert, not operational rollback. S3 documents the compatibility and refusal
behaviour readers actually show and what would still have to happen to
activate the change, and its landing restarts and rolls out nothing.

## What S2 settled, and what it did not

Layers 2 and 3 are implemented. This section records the five choices the
adopted text left open, so a reader of the note is not left comparing it with
the source to find out what was decided. Layer 4 is S3's and nothing here
claims it: no durable kind gained a field, the statement schema did not
advance, and the fold profile version is untouched.

**The configured checkout root exists now.** It is `gitseq.checkoutRoot` in
the repository's own Git configuration, read through the same closed Git
environment as every other identity read. One value serves both directions:
`gs worktree` refuses a destination outside it, and the layer 1 containment
report protects a checkout whose resolved path is outside it. Unset is the
absence of a boundary rather than a second boundary. Protection keeps the
fallback it has always had, the directory holding the served checkout;
creation refuses nothing and reports the check as not established, because a
fallback nobody chose must not refuse an ordinary destination. A relative
value resolves against the repository top level, and containment compares
resolved paths by path elements.

This is a newly proposed default with a consequence worth naming. Configuring
a root narrows where checkouts may be created, and a root wider than the
derived fallback marks fewer checkouts as outside it, so it enlarges the set
cleanup advice offers. Both effects come from a value written into the
repository's own configuration, which is a scope a caller can already execute
code from during an ordinary read, so the boundary is unchanged.

**Destinations are also refused on their own terms.** Independently of any
root: a path that is not absolute-resolvable, a path whose final component is
a symbolic link, a path that already exists as anything but an empty
directory, a path inside this repository's own Git directory, and a path
inside a checkout this repository already has. Neither the destination nor the
branch has a default, because inventing a directory layout or a branch naming
convention would be the tool deciding something nobody wrote down.

**Reading a lane back is part of S2.** The adopted usable result is to find
the lane after its checkout is gone, so the attempt refs and the checkout
records are read back through layer 1's own captured read and layer 1's own
grader, not through a second answer beside it. The bounded ref inventory
covers `refs/gitseq/lanes/` under the existing 4,096-ref bound; each
checkout's record is one small file read from the private Git directory its
own `.git` entry names, starting no Git process; and the captured records join
the frontier, the refs and the listing in the association cache key. Read
back, both grade `claimed`, and promotion still needs a signed artifact naming
that exact commit. One further true fact follows: a head an attempt ref names
is reachable from a ref, so it is no longer a refless head.

**The commit hook is not implemented.** A linked checkout resolves its hooks
to the shared common directory, so installing a `prepare-commit-msg` hook "for
a checkout" installs it for every checkout of the repository, and the adopted
wording does not disclose that. The contract goes to the ratifier as a
clarification before anything is installed. The commit-message template is
separate, is adopted, and is shipped: an empty subject line, a blank line and
the exact `Rests-On:` line, written beside the record. Nothing depends on a
hook in any case, because `gs merge` commits through `commit-tree` and runs no
commit hooks at all.

**A short selector against an unreadable projection.** The note named refusing
one as an S2 proposal item rather than as settled. Nothing was added to the
adopted refusal list: a `#N` or a hash fragment is resolved by
`internal/eventref`, and with the log unreadable that resolver refuses for the
reason it has always refused. The message says the selector could not be
resolved, which is what happened, rather than claiming the record was
ineligible.

## Proof obligations

Each proof runs against a temporary repository built with `internal/testgit` and
a real Git binary, and each guard is exercised once with the guard removed.

| Obligation | Fixture |
|---|---|
| Branch movement and rename | Move and rename a lane branch under a captured snapshot; association remeasures, advice does not silently persist |
| Linked worktrees and recuts | Two linked checkouts of one repository, one current; two attempt refs under one governing hash, both live |
| Ambiguous and missing identity | A fragment matching two events, refused with both candidates and nothing created; a trailer naming no event here, `unresolved` and no refusal to act |
| Forged trailer | A commit whose trailer names a request addressed to another actor; `claimed`, never `corroborated`, never deletable |
| Forged author and trailer | The same commit also carrying the real performer's author name and email; still `claimed`, because no signed artifact names it |
| Corroboration is per commit | A signed artifact at one commit; that commit corroborated, later commits on its branch claimed |
| Pending decision candidate | A live proposal resting by `rests_on` on an artifact whose parent request is stale but unsettled: the checkout stays protected; in the same fixture a checkout with no live proposal, promise or approval is still offered as a candidate |
| Foreign workroom | A trailer and checkout record carrying another genesis; carried as typed, never resolved locally |
| Dirty, locked, used, symlink, out of scope paths | Creation refused before any write; classification protects |
| Retries and competing creation | Two concurrent lane ref writes; one wins the compare and swap, the loser retries into a fresh attempt |
| Settled lane | Local creation warned and named, created only with the confirmation flag; with that same flag present the fold refuses the durable act exactly as it does today |
| Unreadable projection | Creation and assistance report unknown and continue; no durable act is admitted on unknown |
| Merely stale lane, approved but unlanded | Staleness is recorded and is never a refusal; an approved unlanded head protects its checkout in every layer |
| Merge and non merge endings | Advice printed, nothing deleted, receipt unchanged; retirement prints the same advice |
| Hook scope | Installation refused when a hook exists or `core.hooksPath` points outside; the script appends one line and execs nothing |
| Read only inventory | The whole layer 1 path runs against a read only repository and writes nothing |

Positive controls, with contract values written independently of the
implementation, cover a corroborated association and a candidate checkout that
must appear, an ambiguous selector that must refuse, and a forged trailer that
must not promote.

## Boundaries

The kernel learns nothing about branches, worktrees or checkouts, and neither
does the fold: every Git derived observation stays in `internal/app`, joined to
durable rows only through the existing landing details shape. Layers 1 to 3 live
in the resident, `cmd/gs` and repo local metadata; layer 4 touches statement
field validation only. Historical branch names, historical bytes and existing
checkouts are untouched. "Lane" already names a work query lane in
`internal/statusview`, so new code says "checkout" and "governing record".
