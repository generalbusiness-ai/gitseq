---
date: 2026-08-24
status: draft/discussion — design note for running more than one workroom
  (independent genesis log) in one repository. Nothing here is built.
  Every file:line citation is verified against main at
  20722c1be8ed769bac86ae09b69baf228623a042.
---

# Multiple workrooms in one repository

One Git repository holds one workroom today. This note establishes where
that constraint actually lives, what would have to change to lift it, how
a command would pick a workroom, what happens to the resident, what the
application binding demands of the binary opening each log, and — most
importantly — what does *not* become possible: the fold's staleness
machinery stops at the log boundary, and no wiring inside one repository
changes that.

## 1. The constraint, precisely

The kernel and storage layers already support many logs in one
repository. Every durable structure is namespaced by genesis:

- the sequence ref is `refs/seq/<genesis>` (`internal/kernel/kernel.go:292`);
- the checkpoint ref is `refs/gitseq/checkpoints/<genesis>`
  (`internal/kernel/checkpoint.go:474`);
- `kernel.Create` writes only `Ref(commit)` for the new genesis and
  checks nothing about other sequences (`internal/kernel/kernel.go:302-332`);
- admission routes each submission by the *signed* intent's `Target`,
  resolving `Ref(targetOID)` and refusing "unknown target log"
  (`internal/kernel/kernel.go:422-432`), and verification re-checks that
  the decoded target equals the log's genesis
  (`internal/kernel/kernel.go:1385`, `:1428-1433`). A signed act is
  cryptographically bound to one log and cannot be replayed into a
  sibling log in the same repository;
- the interpreter binding is read from each log itself
  (`internal/app/host.go:68-70`), so two logs could be bound to two
  different applications — section 7 states what that demands of the
  binary that opens them.

The single-workroom rule is one application-layer fact: a repository has
exactly one metadata directory, `<commonDir>/gitseq`
(`internal/apphost/config.go:247`), holding exactly one `config.json`
(`internal/apphost/config.go:18`), whose schema names exactly one
`Genesis` (`internal/apphost/config.go:44`, required by `Validate` at
`:79`). `gs init` refuses when that file exists:

> `workroom already initialized` (`internal/app/app.go:383-384`)

and `AttachConfig` (`internal/app/app.go:500-545`) refuses to attach a
second log over it:

> `cannot attach over a writable workroom` (`internal/app/app.go:536`)
> `attached workroom genesis does not match --genesis` (`internal/app/app.go:539`)

Everything that opens a workspace goes through `LoadConfig(metaDir)`
(`internal/app/app.go:335-336`), so one file decides which single log
every command in the repository means.

## 2. What changes to let two workrooms co-exist

Only the `internal/apphost` configuration shape and the `internal/app`
open/init/attach paths, plus the surfaces that select a workspace. The
kernel, the workroom fold, and the projections need no change at all —
the fold never reads the config.

The per-repository state that must become per-workroom:

- **`config.json`** — either one file per genesis
  (`gitseq/<genesis>/config.json`) or a list inside one file with an
  optional default. Per-genesis files are simpler: `CreateConfig`'s
  existing create-exclusively discipline (`internal/apphost/config.go:147-154`)
  carries over unchanged, and two `gs init` races for *different*
  workrooms stop contending at all.
- **`resident.json`** — today a single advertisement file per repository
  (`internal/app/resident.go:28`), last writer wins (`:36-54`). See
  section 4.
- **Verified frontier** — already inside the config, so it follows the
  config split.

State that is already shared safely: actor key files under
`gitseq/actors/` are local signing custody, not application meaning
(`internal/apphost/config.go:20-27`); the durable roster that *admits* a
fingerprint is per-log, so the same key can be an actor in one workroom
and a stranger in the other. The sequencer key is generated per `gs init`
(`internal/app/app.go:394-397`) and named by the config, so per-genesis
configs give each workroom its own sequencer key with no extra work.

`gs init` changes from "refuse if the file exists" to "refuse if *this
genesis* is already configured"; `gs attach` likewise. Nothing else in
the durable protocol moves.

## 3. Selecting a workroom, and what absence or ambiguity means

Today selection is by repository only: every command takes `--repo`
(default `.`, `cmd/gs/main.go:140-145`) and the config supplies the one
genesis. The only command that names a genesis is `gs attach`
(`cmd/gs/main.go:2255-2264`), and there it is mandatory.

With several workrooms, every command that opens a workspace needs a
selection input — `--genesis`, resolved like Git resolves object names: a
unique prefix is accepted, an ambiguous prefix is refused naming the
candidates.

When the selection is absent:

- **One workroom configured:** act in it. This keeps every existing
  invocation, script, and skill working unchanged, and there is nothing
  to be wrong about.
- **Several workrooms configured:** refuse, listing the configured
  genesis ids. This is the right answer, not a stopgap. Acting appends a
  signed durable event to an append-only log; the signed intent's
  `Target` is set from whatever the CLI selected
  (`internal/app/app.go:1086`), and the kernel then faithfully appends to
  exactly that log — no later layer can catch a wrong guess, and nothing
  can be unappended. A guessed default ("the first one", "the one
  initialized earliest") turns every automation bug into a durable record
  in the wrong log, signed by a real actor. A *recorded* default — an
  explicit field in the repository configuration — is acceptable because
  it is itself a deliberate selection; silence plus plurality is not.

Read-only commands (`gs status`, `gs verify`) could in principle answer
for all workrooms at once, but starting with the same rule everywhere is
simpler and can be relaxed later without breaking anyone.

## 4. The resident

One resident process serves one workroom today, and the binding is
structural, not incidental. `gs serve` opens a single workspace
(`cmd/gs/main.go:2071`), builds a `service.Server` holding that one
`workspace *app.Workspace` (`internal/service/server.go:78-80`), claims
ownership at the per-genesis ref `refs/gitseq/resident/<genesis>`
(`internal/app/resident.go:86`, claimed at `:181`), and advertises
itself in the single `gitseq/resident.json`
(`internal/app/resident.go:28`, `:36-54`). Clients refuse an
advertisement naming a different genesis rather than act through it
(`internal/app/resident.go:60-66`), and the ownership claim itself
carries the genesis and is checked against the workspace
(`internal/app/resident.go:293-298`).

So with two workrooms, ownership already cannot collide — the claim refs
are distinct — but the advertisement does: the file is one path, last
writer wins, and the losing workroom's clients get `ResidentURL() =
false` and silently fall back to acting locally. That is a degraded mode,
not a failure, but it is invisible and slow.

Each workroom should get its own resident, and the advertisement should
become per-genesis (`gitseq/resident-<genesis>.json`, or a file per
genesis under a directory), with `gs serve` taking the same `--genesis`
selection as every other command, each resident on its own port. The
alternative — one process holding a workspace map and routing by genesis
— touches the whole `internal/service` surface (`Status`, wait cursors,
the nexus hub, the UI) for no capability the per-genesis claim ref does
not already provide. The trust posture does not distinguish the two
options anyway: every process inside the resident boundary can already
act as every actor key the repository holds
(`internal/service/server.go:39-40`), and both options keep that
boundary at the repository. Per-workroom residents are the change that
leaves the serve path's contract alone.

## 5. What does not become possible: `rests_on` across logs

The requester's framing was "`rests_on` cannot cross logs — it refuses."
That is imprecise in a way that matters. **A cross-log reference is not
refused; it is admitted and inert.**

Event ids are already globally qualified:
`git:<format>:<genesis>#git:<format>:<commit>`
(`internal/app/app.go:1317-1319`). Admission's only `rests_on` check,
`resolveReferences` (`internal/kernel/kernel.go:589-601`), classifies
each entry with `inLogEvent` (`:563-577`): an entry whose workroom half
is not *this* log's genesis returns `ok=false` and is skipped — carried
unchanged, exactly as the kernel contract states
(`internal/kernel/kernel.go:40-44`, and the layer-2 text in
`docs/reference/architecture.md`). Only an entry that *claims* a
position in the log being submitted to, and does not name one, draws
`ErrUnresolvedReference`.

So an author can put a foreign workroom's canonical event id straight
into `rests_on`, and the log keeps it. What the reference cannot do is
mean anything to the local fold:

- `f.byID` never matches it, so it counts for no basis constraint —
  `validateBasis` skips unknown references (`internal/workroom/fold.go:734`),
  and a promise resting only on a foreign request is refused as
  "dangling promise has no request" (`:753`);
- `ratify` and `supersede` target by exact id against local effective
  records (`internal/workroom/fold.go:853`, `:922`), so no cross-log
  retirement or ratification exists;
- the staleness pass consults only local indexes: the basis record via
  `f.byID` (`internal/workroom/fold.go:1636`), local retirement causes
  (`:1641`, backed by `retirementCauses` at `:292`, read at `:1479-1481`),
  and the local stale set (`:1659`). A foreign basis is never retired
  and never stale, so it never flares anything.

**What an author should write instead.** Two things together. Keep the
foreign canonical id in `rests_on` — it costs nothing, survives
verbatim, and is projected in `Provenance`
(`internal/workroom/fold.go:2017`), so a human or a tool reading the
record can follow it by hand. And file a local *bridge* statement — an
`assert`, or a `propose` that gets ratified when the dependency carries
authority — restating the foreign decision's content and naming the
foreign id in its body. Local work then rests on the bridge, which is a
real local basis: it counts, it can be superseded, and its supersession
flares every local dependent.

**What staleness coverage the substitution loses.** Inside one log, a
basis edge buys three automatic facts. When the basis is superseded,
every transitive dependent is marked stale in the same pass
(`internal/workroom/fold.go:1598-1690`); artifact-to-artifact edges
narrow that to `describes_superseded_world` (`:1667-1668`) with a cause
*date* (`causedAt`, projected as `WorldSupersededAt`,
`internal/workroom/fold.go:85-86`, `:122-128`); and the merge boundary
judges that dated fact as of the verdict
(`internal/workroom/fold.go:1089-1141`). Across the log boundary, none
of this runs — and the bridge only partially restores it:

- **The flare below the bridge is restored; the flare *into* the bridge
  is not.** If the foreign log supersedes the decision, nothing in the
  local log changes: no stale flag, no world flag, no
  `staleness-wave` row, no merge refusal. Some actor must *notice* the
  foreign supersession and supersede the local bridge by hand. The
  fold's guarantee — retire a basis and every dependent's row changes,
  with no one watching — becomes an operational duty with no mechanism
  checking it was performed. That is the concrete loss.
- **The date is wrong even when the duty is performed.** `causedAt`
  dates a moved world by the local supersession's log position. A bridge
  superseded on Tuesday, over a foreign decision that actually moved the
  previous Monday, dates every downstream
  `describes_superseded_world` fact to Tuesday. The merge boundary's
  rule — refuse a verdict that had already been shown the move, record
  one the world moved under afterwards — is judged against the wrong
  instant, in the direction that *admits* merges the reviewer would have
  been refused had the logs been one.

Both losses are inherent to independent logs, not defects of the bridge:
a fold is a deterministic function of one sequence, and making it read a
second sequence would make every projection depend on fetch state. The
bridge is the honest substitute; it should be filed knowing exactly which
two guarantees it does not carry.

## 6. Artifacts naming absent commits

**Finding: an artifact whose `commit` names nothing in the repository is
admitted today, stands effective, occupies its path, and participates in
succession.** `KindArtifact` is declared with only
`present("path", "commit")` (`internal/workroom/kinds.go:174`), and
`present` emits `FieldPresent` constraints alone (`:221-227`): the fold
checks that the field exists, never what it names. No admission or fold
code resolves the commit against Git; the fold only compares the string
to other strings — to an approval's head (`internal/workroom/fold.go:1213`)
and to a receipt's `merge_head` (`:1822`).

One correction to the requester's framing: it is not true that "no
reader can check" the commit. Any reader with the repository can
`rev-parse` it, and the CLI does exactly that on the paths that act on an
artifact: `gs review` requires a checkout standing at the artifact's
commit (`validateCheckout` at `cmd/gs/main.go:739`, resolving via
`canonicalCommit`'s `git rev-parse --verify <commit>^{commit}` at
`:990`), and `gs merge` resolves the candidate again (`:876`). The
precise fact is: *the log admits it and the fold never checks*, so a
fabricated pointer is refused only when — and if — someone tries to
review or merge it. Until then it is a live artifact like any other:
citable, occupying its path, a predecessor that succession must account
for.

Multiple workrooms sharpen this. Two workrooms in one repository share
one object database, so a commit filed in workroom A resolves in
workroom B — an artifact can silently point at the *other* workroom's
implementation history and nothing in the fold can tell. And an
attached read-only workroom (section 1) may legitimately hold artifacts
whose commits exist in the origin repository but not in this clone, so
"absent from the repository" is observer-relative.

**Should it change?** The fold must stay out of it: it is a
deterministic function of the log, and Git resolution would make
effectiveness depend on which clone folds. History must also keep
reading as it does — the same append-only reasoning that made
`ErrUnresolvedReference` gate submission only
(`internal/kernel/kernel.go:41-43`) applies unchanged. But a
*submission-time* check at the application boundary is available and
follows that precedent exactly: when the act's kind renders as an
artifact, resolve `body["commit"]` in the repository the sequencer
appends to, and refuse the submission when it names nothing. The
resident is the sequencer and holds the common object database, so the
check runs where merges already run their resolution. It closes the
window where a garbage pointer becomes durable, at the cost of refusing
an artifact filed before its commit is fetched — which is the same
ordering discipline `rests_on` already imposes on in-log references.
This is worth doing independently of multi-workroom; multi-workroom
raises its value because it adds the cross-workroom mis-pointing case.

What the check cannot promise: presence in the object database is not
reachability from any branch, and not presence in anyone else's clone.
It proves the author's sequencer could see the commit at filing time,
nothing more. Review at a named checkout remains the real verification.

## 7. The application binding, and what it demands of the binary

Section 1 counted the binding as already per-log: each log declares its
own, so two logs in one repository could be bound to two different
applications. This section states what that costs, because the first
draft of this note got part of it wrong.

**The binary opening a log must hold that log's application at its
exact fold version.** `selectHost` (`internal/app/host.go:68-84`)
refuses on two distinct grounds. A build that does not hold the
application at all:

> `repository is verifiable but uninterpretable: it is bound to
> application %q, which this build does not hold`
> (`internal/app/host.go:78`)

and a build that holds the application, but at a different fold
version:

> `repository is verifiable but uninterpretable: it is bound to %s fold
> %q, and this build holds %q` (`internal/app/host.go:81`)

A build holds the applications it was compiled with, and nothing
installs one at runtime (`internal/app/host.go:30-31`); today the
registry is a single entry (`heldHost`, `internal/app/host.go:44-49`).
So a chess-application log and a workroom-application log in one
repository leave exactly two shapes: one binary compiled to hold both
folds, or two binaries, each opening only its own log and refusing the
other's. This note states the constraint and stops. Which shape this
project ships is a decision for the repository owner, not for this
note, and nothing in the mechanics prefers either.

**The binding a log declares is mutable after the fact.**
`BindingInForce` (`internal/apphost/binding.go:134-198`) walks the log
forward — `WalkRevListMetadata` runs `git log --reverse`
(`internal/gitstore/gitstore.go:468`) — and overwrites the in-force
answer with each later qualifying binding record
(`internal/apphost/binding.go:188`): the bootstrap binding and a later
replacement are one rule, scan the log in order and keep the last
record that qualifies (`internal/apphost/binding.go:115-119`). So a
later binding governs every open that follows, and any build that does
not hold its application at its exact fold version refuses from that
moment — the refusal above, hard, not a degradation. Not every re-bind
strands someone: a record naming the same application and fold changes
only provenance, and a rollback to a fold an older build still holds
restores that build rather than excluding it
(`internal/app/host_test.go:315`, `:338`). What decides is whether a
reader holds what the new binding names. It binds pure readers too:
`selectHost` runs at every workspace open, so an actor who never
contends for the sequencer is refused the same way.

Here is where the first draft misread the code. Row 4 of the layers
table said the binding "does not move". That is true of where the
binding is read from — each log's own history — and false of what it
says. The comment the draft leaned on — "The answer is immutable, so
every operation on one workspace means the same thing however the log
moves under it" (`internal/app/host.go:87-88`) — is a statement about
the *workspace*: the selection is made once at open and never revisited
for that workspace, so a binding recorded afterwards cannot change what
an open workspace means (`internal/app/host.go:66-67`). It is not a
statement about the *log*, whose in-force binding moves whenever a new
record qualifies. The draft read the first as the second.

Initial agreement is a different problem, and it is not one. The code
enforces it and no convention is needed: a build that mismatches the
recorded binding cannot open the log at all. (One edge: `gs init`
records a binding only for a non-default application
(`internal/app/app.go:424-432`). A plain workroom log carries none,
and an absent binding permanently means workroom at whatever fold
version the reader ships (`internal/apphost/binding.go:32-34`,
`internal/app/host.go:73-74`) — only an explicit binding pins a
version.) **The coordination problem is upgrade, not initial
agreement:** re-binding a log to a new fold version strands every
not-yet-rebuilt binary at its next open, with no degraded mode to limp
along in.

**Who may re-bind: the initializing actor key alone.** The binding in
force is the last binding record signed by the actor key on the log's
first record (`internal/apphost/binding.go:115-116`; the key is
captured at `:158`), and a binding-shaped record signed by any other
key has no force (`bytes.Equal(signed.ActorKey, initializing)`,
`internal/apphost/binding.go:165`). Not the `ratifier` role, not
operator standing: binding authority sits below application roles,
because another application has no roster to consult
(`internal/apphost/binding.go:118-120`).

**What this note does not cover.** Designing an upgrade sequence — in
what order to advance builds' folds and record the re-binding, and how
to know beforehand which builds would be stranded at their next open —
is a separate piece of work, and it has not been done. Nothing here
designs it.

## 8. Affected layers

Against `docs/reference/architecture.md`:

| Layer | Effect |
|---|---|
| 1. Ordinary Git storage | No change. Refs are already per-genesis. |
| 2. Kernel | No change. It already hosts many sequences, routes by signed target, and carries foreign references unchanged. |
| 3. Nexus and live runtime | No contract change. Each resident carries its own hub; per-workroom residents multiply instances, not meanings. |
| 4. Application host binding | Contract change, confined to `internal/apphost`'s repository-configuration duty: the config shape ("the repository configuration a checkout needs to reopen its own log", per the package-boundary table) goes from one log to a set with selection. Where the binding is read from is per-log already (`internal/app/host.go:68-70`) and does not move; what it says can move after the fact — section 7 says who moves it and what a move refuses. |
| 5. Application profile (Workroom) | No change to the fold or vocabulary — unless the section-6 artifact check is adopted, which adds an application admission-side check at the `internal/app` boundary, not in the fold. |
| 6. Projections and queries | No change. Projections are per-workspace. |
| 7. CLI, MCP, connectors, UI | Surface change: `--genesis` selection on every workspace-opening command, per-genesis resident advertisement, `gs serve --genesis`. The MCP adapter binds one workspace at startup and needs the same selection. |

The contract changes live at layers 4 and 7. Under the review rule, the
head that implements them must update `architecture.md` (the `apphost`
row and the compatibility axes' "surface" row) in the same head and
re-anchor its artifact.

## Summary of corrections to the framing this note was requested under

- `internal/kernel/resident.go` does not exist; the resident lives in
  `internal/app/resident.go` (ownership ref at `:86`) and
  `internal/service/server.go`.
- "`rests_on` cannot cross logs — it refuses" is wrong in the mechanism:
  a foreign reference is admitted and carried unchanged
  (`internal/kernel/kernel.go:563-577`, `:589-601`); it is inert in the
  fold rather than refused at the door.
- "An artifact can name a commit no reader can check" overstates it:
  review and merge do check, at a named checkout
  (`cmd/gs/main.go:739`, `:876`, `:990`). The admitted-and-unchecked
  window is between filing and the first act that resolves it.
