---
title: Lane identity, reconciled against current source
date: 2026-09-09
status: candidate
reconciles: notes/2026-08-27-lane-identity.md@6131249c (adopted by proposal cb755aa1, ratified 986a1ca2)
base: main d1ae6ade
---

# Lane identity, reconciled

## Problem at current main

The adopted design named a real gap and it is still open. Nothing joins a
checkout or a branch to the durable work that governs it by the commit's own
content. `refs/gitseq/lanes` holds nothing, `cmd/gs` has no `worktree`
subcommand (`cmd/gs/main.go:107`), and no `gitseq.*` Git config key is read
anywhere. Two deliveries since adoption narrow what is left, and one earlier
decision constrains how. Landing observations shipped a read only checkout
inventory with protection and deletion advice. The destination a request owes
became a validated durable field. And this repository already rejected
`git config gitseq.*` (`notes/2026-08-21-notes-and-decisions.md:238`), because a
clone wide default misattributes across the dozen checkouts running at once.

## The trailer distinction the old text missed

The adopted text said a commit hash seals a `Rests-On:` trailer, "so commit to
work item is already durable and unforgeable". Two different trailers wear that
one name.

A kernel event commit carries `Rests-On:` in a signed envelope
(`internal/intent/envelope.go:22`), parsed back at `:47` and compared byte for
byte with the signed intent at `internal/kernel/kernel.go:1535` and
`internal/kernel/checkpoint.go:826`. That trailer is unforgeable: a mismatch
refuses the event.

An implementing source commit carries `Rests-On:` as ordinary message text.
Nobody signs it, nothing verifies it, and `internal/gitstore/graph.go:46` reads
it only for display. Anyone who can write a commit can name any event. The hash
seals the bytes of a claim, never its truth.

## Trust boundary

Four things stay distinct: **claimed identity** (a source trailer, a per
checkout record, a lane ref, a branch name, a body path); **verified durable
evidence** (the request naming an addressee, the promise its performer signed,
the artifact naming an exact head, the ratified approval, the sealed receipt);
**local location** (a path, a checkout label, a lock, a presence lease); and
**actual permission** (what the fold admits, what the filesystem allows).

A claim is shown as claimed and never as governing until durable evidence
corroborates it. An uncorroborated claim never widens the deletable set and
never authorises a signature. A shared head, an expired lease, or a missing
claim never makes a tree removable. Branch controlled input retires nothing by
itself (`SKILL.md:539`). Every association is scoped to one workroom by genesis;
a value naming another genesis is carried as typed, never resolved locally.

## Layer 1: bounded reverse association, read only

The resident derives from Git a table of checkouts and branches, the durable
work each claims, and the durable work that corroborates it. It reuses, without
adding a competing store: the checkout inventory with its 8 second cache, 128
checkout cap, symlink resolution and basename only labels
(`internal/app/app.go:616`, `:619`, `:668`, `:672`, `:690`); the hardened
trailer scan with NUL framing and per object hash re verification
(`internal/gitstore/graph.go:41`, `:85`, `:118`); the bounded ref inventory and
the in process ancestry graph that answers unknown rather than false
(`internal/app/landing_graph.go:65`, `:150`); canonical resolution with typed
refusals and at most eight candidates (`internal/eventref/eventref.go:239`,
`:268`, `:300`); and the closed Git environment allowlist
(`internal/app/app.go:336`).

New: an association pass taking an explicit ref set and depth limit instead of
the fixed newest 81 (`internal/service/ui.go:60`), walking first parent lineage
from each branch tip until it meets the resolved target ref or a limit, and
grading each association.

- `claimed`: a trailer or checkout record names a resolvable event here.
- `corroborated`: that event is a request addressed to the commit author's
  actor, a promise that actor signed, or an adopted decision, and a live
  commitment or artifact ties it to this branch or head.
- `unresolved`: names no event here, or more than one; the typed refusal and its
  candidate list are carried verbatim.
- `foreign`: a canonical identifier of another genesis.

Association is a pure function of the captured Git snapshot and the projection
frontier. Ambiguity never picks a winner. Missing objects, shallow boundaries, a
foreign genesis, budget exhaustion and deadline expiry all yield unknown, and
unknown never proves a negative; the whole batch's advice is discarded on
exhaustion, as today. Bounds are the existing 65,536 step budget, 4,096
commitment cap and 3 second deadline (`internal/app/worktree_landing.go:11`,
`:13`, `internal/app/landing.go:35`), plus 4,096 refs, 256 branch tips, 512
commits per tip and 1 MiB per commit object. Results cache under the pair of
durable frontier and observed ref inventory digest, so ref movement at an
unchanged frontier invalidates the cache, as landing observations require. This
layer writes nothing, anywhere.

## Layer 2: guarded creation and stamping

`gs worktree <selector>` creates one checkout for one governing record. The
selector resolves through `internal/eventref`, so `#N`, an unambiguous fragment
and a canonical identifier all work, and an ambiguous or absent selector refuses
before anything is created. The record may be a request addressed to the acting
actor, or an adopted decision, which is how self initiated work is named without
inventing a self request; recognition reuses
`internal/reviewguard/binding.go:402` and `:436`, which already decide self
initiated binding and adopted decision from the durable record alone.

Refusals before any filesystem write: ambiguous or unresolved selector; a record
that is neither a request nor an adopted decision; a request whose commitment is
settled, by the existing word list at `internal/mergeplan/mergeplan.go:967`; a
destination path that exists, is a symlink, or resolves outside the configured
checkout root; a repository that is not this workroom's, by common Git directory
(`internal/mergeplan/mergeplan.go:351`); an existing branch not asked for.

**The per checkout record is not Git config.** The adopted text put it in
`git config --worktree gitseq.request`. The worktree scope answers half the
earlier objection: it still requires enabling repo wide
`extensions.worktreeConfig`, and it lands the value in a scope the source
documents as executable rather than merely readable (`internal/app/app.go:355`).
Instead the record is a small JSON file in the checkout's own private Git
directory, which `apphost.ResolveGitDirs` (`internal/apphost/config.go:420`)
already separates from the common directory for exactly this reason, written
through the existing atomic file and flock pattern
(`internal/apphost/config.go:346`). It holds the full canonical governing
identifier and this workroom's genesis, so a record copied from another
repository is detectable, and it is read as evidence, never permission.

Trailer assistance is a commit message template naming the exact `Rests-On:`
line, and only the full canonical identifier is ever written, matching
`SKILL.md:440`. A hook is optional, off by default, installed only with an
explicit flag, only when no hook of that name exists and `core.hooksPath` is
unset or inside this repository. It is one `prepare-commit-msg` script that
appends one line from the record it was given, execs nothing else, and never
replaces a user hook. Nothing here is load bearing on a hook, because `gs merge`
deliberately runs no commit hooks at all (`docs/reference/gs/merge.md:236`).

When the governing lane settles, the command warns, names the settling event and
requires an explicit confirmation flag. It does not refuse on the local record
alone: a value the actor can write is a value the actor can unset, so refusing
there stops only honest actors, and an unreadable projection must not deadlock
one.

## Layer 3: deterministic lane refs

Tooling maintains `refs/gitseq/lanes/<governing event hash>/<attempt>` pointing
at the lane tip, mirroring `refs/gitseq/merge-receipts/<key>`
(`internal/mergeplan/mergeplan.go:1084`). The hash is the request's, or the
adopted decision's for self initiated work. The attempt suffix is what gives
each recut its own ref; one ref per request hash cannot, because a recut under
one request shares its hash. Every write goes through `gitstore.UpdateRef` with
an expected old value (`internal/gitstore/gitstore.go:338`), so competing
creation loses cleanly and a retry at the same tip is a no op; allocating an
attempt is a create against a missing ref, retried on failure. Deletion keeps
the existing rule that an expected old value is mandatory (`:348`). The ref
survives checkout removal and branch deletion, which is its point, and it grants
nothing: read back, it is a `claimed` association.

## Layer 4: validated display fields

Requests already carry a validated branch, as `target_ref` under
`validBranchRef` (`internal/workroom/landing.go:121`, `:229`). No second
validated branch field is added to requests.

Promise, report and artifact kinds gain an optional `branch`, validated by the
same function, alongside the `commit` those kinds already declare
(`internal/workroom/kinds.go:174`). It is display and search only: it never
substitutes for `target_ref`, never moves a merge, never licenses a deletion.
Admission is keyed by statement schema in the established way, so the same name
on an older record stays opaque prose. This advances the statement schema and
the fold profile version (`internal/workroom/schema.go:16`), replaying history
once.

No worktree path field is added to any durable kind. A path is machine local, is
not portable across the actors and repositories that read one log, and the
current surface deliberately keeps other checkouts' paths inside the resident
boundary (`internal/app/app.go:178`). The checkout label stays in the ephemeral
endpoint, where it already is.

## Cleanup, duplicates, refless heads, scratch trees

Advice stays read only and in one place. `gs merge` and request retirement print
the current advice and name the checkouts that became candidates. Neither
computes its own answer and neither deletes anything; removal stays a separate
deliberate act.

A checkout is a candidate only when all hold: not current, not detached, not
dirty, not locked, not prunable; its resolved path is inside the configured
checkout root and is not a symlink pointing outside it; no unsettled or
`approved_not_landed` commitment names any head on it; and its tip is proved
incorporated into a witnessed target or was explicitly abandoned. The middle of
that list is already enforced (`internal/app/worktree_landing.go:46`, `:249`);
containment and symlink direction are new. Unknown protects.

Duplicate checkouts at one head are reported at inventory time and warned at
creation, with an explicit flag to proceed. They are not refused outright: a
second checkout at the target ref's own head is ordinary, and this repository
has one today. A refless head, a checkout whose `HEAD` is contained in no ref
here, is reported, never deleted, and protects its checkout rather than exposing
it. Mutation test scratch trees follow the existing harness shape: created under
the process temporary directory, hooks emptied as the disposable clone already
does (`internal/mergeplan/mergeplan.go:1563`), removed by the process that
created them by recorded path only (`cmd/gitseq-perf/main.go:833`, `:839`).
Nothing searches for scratch trees it did not create.

## Proof obligations

Each proof runs against a temporary repository built with `internal/testgit` and
a real Git binary, and each guard is exercised once with the guard removed.

| Obligation | Fixture |
|---|---|
| Branch movement and rename | Move and rename a lane branch under a captured snapshot; association remeasures, advice does not silently persist |
| Linked worktrees | Two linked checkouts of one repository, one current |
| Recuts | Two attempt refs under one governing hash, both live |
| Ambiguous identity | A fragment matching two events; typed refusal with both candidates, nothing created or signed |
| Missing identity | A trailer naming no event here; `unresolved`, no association, no refusal to act |
| Forged identity | A commit whose trailer names a request addressed to another actor; `claimed`, never `corroborated`, never deletable |
| Foreign workroom | A trailer and checkout record carrying another genesis; carried as typed, never resolved locally |
| Dirty, locked, used, symlink, out of scope paths | Creation refused before any write; classification protects |
| Retries and competing creation | Two concurrent lane ref writes; one wins the compare and swap, the loser retries into a fresh attempt |
| Settled lane | Warned and confirmable, not silently refused, never removable on that ground alone |
| Merely stale lane | Unchanged; staleness is recorded, never a refusal |
| Approved but unlanded | Protects the checkout in every layer |
| Merge and non merge endings | Advice printed, nothing deleted, receipt unchanged; retirement prints the same advice |
| Hook scope | Installation refused when a hook exists or `core.hooksPath` points outside; the script appends one line and execs nothing |
| Read only inventory | The whole layer 1 path runs against a read only repository and writes nothing |

Positive controls, with contract values written independently of the
implementation, cover a corroborated association that must appear, a candidate
checkout that must appear, an ambiguous selector that must refuse, and a forged
trailer that must not promote.

## Boundaries

The kernel learns nothing about branches, worktrees or checkouts, and neither
does the fold: every Git derived observation stays in `internal/app`, joined to
durable rows only through the existing landing details shape. Layers 1 to 3 live
in the resident, `cmd/gs` and repo local metadata; layer 4 touches statement
field validation only. Historical branch names, historical bytes and existing
checkouts are untouched. "Lane" already names a work query lane in
`internal/statusview`, so new code says "checkout" and "governing record", and
`refs/gitseq/lanes` stays a ref namespace name only.
