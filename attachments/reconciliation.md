# S2 reconciliation: layers 2 and 3 against current source

Read-only. Nothing in `/Users/hughpyle/play/gitseq` was modified.

Bases as read:

- Current main `869702241e4d28c9b8599e8caccaf3d01a2ca4d9` (S1 landed at this commit).
- Adopted design as it now stands at main: `notes/2026-09-09-lane-identity-reconciled.md`
  (383 lines) and `notes/2026-09-09-lane-identity-changes-for-adoption.md` (311 lines).
- Historical adopted text: `3f817c3b86481339934fe1ebfe57f88b54f7c25d`.
- Every `path:line` below was re-derived at main `86970224`, not copied from the notes.

**What S1 changed in the two notes.** The adoption note changed only its reference
notation (Markdown links to inline `path:line` spans) and refreshed drifted line
numbers. The reconciled note did the same, and added two substantive sections: the
one-captured-read paragraph under "Cleanup, duplicates, refless heads, scratch trees"
(`notes/2026-09-09-lane-identity-reconciled.md:242-252`) and the whole
"Three wording questions, answered" section (`:295-344`). The first of those three
questions is S2's and is quoted in section 3 below. No adopted semantic moved.

---

## 1. The adopted requirements for layers 2 and 3

References are to `notes/2026-09-09-lane-identity-reconciled.md` at main `86970224`
unless prefixed `changes:`, which means
`notes/2026-09-09-lane-identity-changes-for-adoption.md`.

### 1a. The ten adopted differences, and which bind S2

| # | Difference | Note | Binds S2? |
|---|---|---|---|
| A1 | A source commit trailer is evidence, not authority | `changes:25-48` | **Yes**, as an invariant. Everything S2 creates — record, lane ref, branch alias — is claimed identity. None may promote a claim, widen the deletable set or authorise a signature. |
| A2 | The per-checkout record is a private JSON file, not `git config --worktree` | `changes:50-76` | **Yes**, directly. This *is* half of layer 2. |
| A3 | A settled lane is three decisions, not one | `changes:78-95` | **Yes**, directly. Warn + confirmation flag on local creation; warn-never-block on authoring; fold untouched. |
| A4 | The commit hook is opt-in and narrow, and nothing depends on it | `changes:97-113` | **Yes**, directly. |
| A5 | Cleanup is advised, never performed by merge or retirement | `changes:115-129` | **Yes**, as a prohibition. S2 adds no deletion path and must carry a no-deletion control. |
| A6 | Lane refs keyed by governing record **and attempt** | `changes:131-144` | **Yes**, directly. This *is* layer 3. |
| A7 | A lane ref names the lane in one direction only | `changes:146-154` | **Yes**, directly. Read back, a lane ref grades `claimed`; tip→record stays trailer + corroboration. |
| A8 | No `body.worktree` field is added | `changes:156-170` | **Yes**, as a prohibition. S2 adds no durable field, no schema advance. |
| A9 | The request `branch` field is kept and validated | `changes:172-196` | **No.** S3 only. S2's artifact must not claim it (`changes:305-309`). |
| A10 | Duplicate checkouts are warned, not refused | `changes:198-209` | **Yes**, directly, at creation. S1 already delivered the inventory-time report (`WorktreeView.DuplicateHead`, `internal/app/app.go:577`). |

Eight of ten bind S2. A9 is S3's. A5 and A8 bind only as prohibitions.

Part B of the adoption note "changes no policy and needs no adoption"
(`changes:211`). Three of its items nonetheless constrain S2's implementation and
are carried into section 2: item 3, trailers take the full canonical identifier only
(`changes:225-227`); item 8, two settled word lists exist and reading unifies on
mergeplan's (`changes:242-249`); item 10, `gs worktree` uses the bounded Git
environment for every read it makes about identity (`changes:253-257`).

### 1b. Enumerated requirements S2 owes

Requirements, one per row. Anything in the notes that argues *why* rather than states
*what* is listed as commentary at the end and is not a condition of satisfaction.

**Layer 2 — command and refusals**

| R | Requirement | Note line |
|---|---|---|
| R1 | `gs worktree <selector>` creates one checkout for one governing record | `:120` |
| R2 | The selector resolves through `internal/eventref`, so `#N`, an unambiguous fragment and a canonical identifier all work | `:121-122` |
| R3 | The eligible record is a request addressed to the acting actor, **or** an adopted decision (self-initiated work, named without inventing a self request) | `:122-125` |
| R4a | Refuse, before any filesystem write: an ambiguous or unresolved selector | `:125-127` |
| R4b | Refuse: a record that is neither a request nor an adopted decision | `:126-127` |
| R4c | Refuse: a destination path that exists, is a symlink, or resolves outside the configured checkout root | `:127-128` |
| R4d | Refuse: a repository that is not this workroom's, judged by common Git directory | `:128-130` |
| R4e | Refuse: an existing branch not asked for | `:130-131` |
| R5 | Local creation makes a directory and a branch; it signs nothing and admits nothing, so it refuses only on what it checks itself | `:138-140` |
| R6 | A settled governing commitment warns, names the settling event, and proceeds only behind an explicit confirmation flag; settlement is judged by the **existing** word list at `internal/mergeplan/mergeplan.go:967` | `:140-143` |
| R7 | Creation must **not** refuse on the per-checkout record alone: a value the actor can write is one the actor can unset, and an unreadable projection must not deadlock an actor | `:143-145` |
| R8 | Authoring assistance warns and never blocks; what it knows is the durable record as last read, and unknown is reported as unknown, not as consent | `:147-150` |
| R9 | Durable admission is untouched: neither the confirmation flag nor the per-checkout record is an input to the fold, and a settled commitment refuses exactly the acts it refuses today | `:152-156` |
| R10 | Duplicate checkouts at one head are warned at creation with an explicit flag to proceed, never refused outright | `:283-286` |

**Layer 2 — the record and authoring assistance**

| R | Requirement | Note line |
|---|---|---|
| R11 | The record is a small JSON file in the checkout's **own private Git directory**, which `apphost.ResolveGitDirs` already separates from the common directory | `:160-163` |
| R12 | It is written through the existing atomic-file and flock pattern | `:163-164` |
| R13 | It holds the full canonical governing identifier **and this workroom's genesis**, so a record copied from another repository is detectable | `:164-166` |
| R14 | It is read as evidence, never as permission | `:166` |
| R15 | Trailer assistance is a commit-message template naming the exact `Rests-On:` line, and only the full canonical identifier is ever written | `:188-190` |
| R16 | The hook is optional, off by default, installed only with an explicit flag, only when no hook of that name exists, and only when `core.hooksPath` is unset or inside this repository | `:190-193` |
| R17 | It is one `prepare-commit-msg` script that appends one line from the record it was given, execs nothing else, and never replaces a user hook | `:193-194` |
| R18 | Nothing is load-bearing on the hook (`gs merge` deliberately runs no commit hooks) | `:194-196` |

**Layer 3 — lane refs**

| R | Requirement | Note line |
|---|---|---|
| R19 | Tooling maintains `refs/gitseq/lanes/<governing event hash>/<attempt>` pointing at the lane tip, mirroring `refs/gitseq/merge-receipts/<key>` | `:200-202` |
| R20 | The hash is the request's, or the adopted decision's for self-initiated work | `:202-203` |
| R21 | The attempt suffix is what gives each recut its own ref; one ref per request hash cannot | `:203-205` |
| R22 | Every write goes through `gitstore.UpdateRef` with an expected old value | `:205-207` |
| R23 | Competing creation loses cleanly | `:207-208` |
| R24 | A retry at the same tip is a no-op | `:208` |
| R25 | Allocating an attempt is a create against a missing ref | `:208-209` |
| R26 | The ref survives checkout removal and branch deletion | `:209-210` |
| R27 | The ref grants nothing: read back, it is `claimed` | `:210` |

**Cross-cutting**

| R | Requirement | Note line |
|---|---|---|
| R28 | Unreadable projection: an eligibility check that could not be established is reported as **not established**, warned, and creation proceeds — reported neither as passed nor as failed | `:302-313` |
| R29 | A `#N` or hash-fragment selector against an unreadable projection has no canonical identifier to stamp; refusing it is proposed as an **S2 proposal item**, explicitly *not* settled by the adopted text | `:315-322` |
| R30 | Nothing S2 adds deletes anything; removal stays a separate deliberate act | `:254-257` |
| R31 | The kernel and the fold learn nothing; layers 2–3 live in the resident, `cmd/gs` and repo-local metadata; new code says "checkout" and "governing record", never "lane" for a code identifier | `:377-383` |
| R32 | Every proof runs against a temporary repository built with `internal/testgit` and a real Git binary, and each guard is exercised once with the guard removed | `:348-350` |
| R33 | Scratch trees follow the existing harness shape: under the process temporary directory, hooks emptied, removed by the process that created them by recorded path only | `:288-293` |
| R34 | S2's artifact must not claim layer 4 | `changes:305-309` |

**Commentary, not requirements.** The trailer-distinction essay (`:36-47`); the trust
boundary restatement (`:49-63`); the whole A2 cost argument for JSON over `git config`
(`:168-186` and `changes:62-76`) — it justifies R11/R12 and adds no separate condition;
the note's own referencing convention (`:11-24`); the layer-1 recap (`:65-116`), which
S1 delivered.

---

## 2. Current source map, at main `86970224`

### 2a. Command boundary

| Thing | Path:line | Note |
|---|---|---|
| `switch os.Args[1]` dispatch | `cmd/gs/main.go:107` | The request's `:107` at the old main is **still** `:107` here. Add `case "worktree": err = worktreeCommand(ctx, os.Args[2:])`. |
| `usage()` command list | `cmd/gs/main.go:181` | A single `Fprintln` naming every command. Must gain `worktree`. |
| Shared `flags(name, arguments)` helper | `cmd/gs/main.go:246` | Registers `--repo` on every command. `--repo` is therefore a mandatory row in the new doc page's Flags table. |
| `signingActor` / `residentclient.ResolveActor` | `cmd/gs/main.go:59-68` | How `--as` becomes an actor name. `gs worktree` needs the acting actor for R3's addressee check but reads **no private key**: it signs nothing (R5). |
| Ambient-environment Git helper | `cmd/gs/main.go:3282` (`func git`) | Inherits the ambient environment. Part B item 10 forbids using it for identity reads. |
| Bounded Git constructor | `internal/app/app.go:405` (`repositoryLocalGit`), env at `internal/app/app.go:357` | The bounded path R31 and Part B item 10 require. The note cites `internal/app/app.go:346`, which is inside the explanatory comment; the constructor is at `:405`. |

> **Hard gate, easy to miss.** `internal/docset/surface.go:38` (`CLISurface`) parses
> **`cmd/gs/main.go` only**, and `collectFlags` (`internal/docset/surface.go:208`)
> returns `func %s not found` when the named command function is absent from that file.
> So `worktreeCommand` **must be defined in `cmd/gs/main.go`**, and every flag must be
> registered on a local variable literally named `set`
> (`internal/docset/surface.go:241`). A `cmd/gs/worktree.go` would fail the docset gate
> with an extractor error, not a documentation error.

### 2b. Checkout Git directories, atomic write and locking

| Thing | Path:line | Note |
|---|---|---|
| `apphost.ResolveGitDirs(ctx, repo) (gitDir, commonDir, err)` | `internal/apphost/config.go:420` | The request's `:420` is exact at this main. |
| `apphost.MetaDir(commonDir)` | `internal/apphost/config.go:438` | `<commonDir>/gitseq`. Repository-wide, **not** the record's home. |
| `apphost.WriteFileAtomically(path, data)` | `internal/apphost/atomic_file.go:16` | Unique temp in the same directory, then `os.Rename`. The atomic half of R12. |
| `apphost.WithMetaLock[T](metaDir, lockFile, fn)` | `internal/apphost/config.go:346` | The flock half of R12. "The one advisory-lock primitive in this repository" — a second helper would be a second answer. |
| `validateLockFile` | `internal/apphost/config.go:445` | The lock name must be a bare file name: no separator, no `..`. |
| `lockMetaFile` | `internal/apphost/config.go:369` | `os.OpenFile(filepath.Join(metaDir, lockFile), O_CREATE|O_RDWR, 0o600)`. **The directory must already exist** — this call does not create it. |
| Existing `WithMetaLock` callers | `cmd/gs/publication.go:238`, `cmd/gs/main.go:736` | Both lock on `workspace.MetaDir` with their own bare lock-file name. The precedent for adding a third name. |
| `Workspace.GitDir` / `.CommonDir` / `.MetaDir` | set at `internal/app/app.go:868` | Already carried on every open workspace. |

**Where the record lands.** R11 says "the checkout's own private Git directory".
For a linked worktree that is `<commonDir>/worktrees/<name>/`; for the served checkout
it is the common dir itself. The implementer must call `ResolveGitDirs` **against the
newly created checkout path** after `git worktree add`, not reuse `workspace.GitDir`
(which is the *invoking* checkout's). The record directory must be created with
`MkdirAll` before `WithMetaLock` is called against it, and the lock file name must
pass `validateLockFile`.

### 2c. Ref update with expected-old-value CAS, and retry prior art

| Thing | Path:line | Note |
|---|---|---|
| `gitstore.Store.UpdateRef(ctx, ref, newOID, oldOID)` | `internal/gitstore/gitstore.go:338` | `git update-ref <ref> <new> <old>`. The request's cited `:338` is exact. |
| Create-against-missing idiom | `internal/kernel/kernel.go:352` | `UpdateRef(ctx, Ref(commit), commit, "")` — an **empty** `oldOID` is Git's "this ref must not exist". This is R25's mechanism, already in use. |
| `gitstore.Store.DeleteRef` | `internal/gitstore/gitstore.go:348` | Refuses deletion without an expected old value. Relevant only as a boundary S2 does not cross (R30). |
| `gitstore.Store.RefValue(ctx, ref)` | `internal/gitstore/gitstore.go:360` | Reports absence separately from failure — the read half of R24 (retry at the same tip is a no-op). |
| Bounded CAS retry loop | `internal/kernel/kernel.go:627-658`, `:784-790` | `maxRetries` default 32; on CAS failure, re-read the head and `continue` only if it actually moved, otherwise return the error. Do not invent a second loop shape. |
| Contention-not-error CAS, with a bounded attempt count | `internal/app/resident.go:470-488`, bound at `internal/app/resident.go:188` (`claimAttempts = 8`) | The closest prior art to R23/R25: a JSON record carrying `Genesis`, a CAS whose failure is *contention* rather than an error, and a bounded attempt count that refuses rather than spinning. Model attempt allocation on this. |
| Reject-a-foreign-record precedent | `internal/app/resident.go:495` (`readResidentClaim`) | "a claim naming a workroom other than the one whose ref it sits at is corruption rather than an incumbent". Exactly the shape R13's genesis check needs. |
| Ref namespace siblings | `internal/mergeplan/mergeplan.go:1085` (`ReceiptRef`), `internal/app/resident.go:176` (`ResidentRef`), `internal/kernel/checkpoint.go:474` (`CheckpointRef`) | R19's "mirroring" target. A `LaneRef(governingHash, attempt)` helper belongs beside one of these. |
| `gitstore` environment | `internal/gitstore/gitstore.go:60` (`run` uses `os.Environ()`), `:95` (`storeGitArguments` pins `--git-dir`) | The store is **not** environment-bounded, but it does pin `--git-dir`, so ambient `GIT_DIR` cannot redirect it. `Store.Repo` is the **common dir** (`internal/app/app.go:868`), which is why a lane ref written through it survives checkout removal (R26) for free. |

### 2d. Selector resolution

| Thing | Path:line | Note |
|---|---|---|
| `eventref.Room.Form(selector)` | `internal/eventref/eventref.go:94` | Classifies `FormOpaque` / `FormCanonical` / `FormExternal` / `FormNumber` / `FormHash` **without reading the log**. This is the mechanism behind R29. |
| `eventref.Set.Resolve` / `.Resolvable` | `internal/eventref/eventref.go:239`, `:232` | Resolution and "does this workroom hold it". Both need the projection. |
| `eventref.Refusal` (`Detail`, `Candidates`, `Omitted`) | `internal/eventref/eventref.go:300`, cap `MaxCandidates = 8` at `:32` | R4a's typed refusal and its candidate list. Carry it verbatim, as the association already does. |
| `Resolver.One` | `internal/eventref/resolver.go:55` | `FormCanonical` returns unchanged **without calling the loader**; `FormNumber`/`FormHash` force `Set()` and surface the loader's error as `read the durable event set to resolve %q`. |
| `newResolver` / `newResolverFrom` / `showResolved` / `resolveRefs` | `cmd/gs/eventref.go:17`, `:31`, `:48`, `:63` | The one boundary shape every `gs` command uses. `gs worktree` uses it too; `showResolved` before any write is what makes a mis-resolution correctable. |

### 2e. Eligibility inputs

| Thing | Path:line | Note |
|---|---|---|
| `workroom.Statement` (`Kind`, `Body`, `Retired`, `Stale`, `Ratified`) | `internal/workroom/fold.go:38` | `Body["to"]` is the addressee as filed. |
| `workroom.Commitment` (`Status`, `Stale`, `AddressedTo`, `Performer`, `ApprovedNotLanded`) | `internal/workroom/fold.go:127` | **`Status` is the settlement field. `Stale` is a separate bool.** See section 4. |
| `reviewguard.AdoptedDecision(projection, decision) error` | `internal/reviewguard/binding.go:447` | Returns nil for a standing **ratified proposal** or a standing **satisfied request**. R3's second limb. Already exported and already used by S1 (`internal/app/association.go:614`). |
| `reviewguard.OwnedEdge(projection, artifact)` | `internal/reviewguard/binding.go:234` | Not needed for creation; it is layer 1's corroboration edge. |
| Settled word list (allow-list of *unsettled* words) | `internal/mergeplan/mergeplan.go:967` (`unsettledCommitment`) | R6's list. Unexported. See the import-direction warning below. |
| Settled word list (deny-list of *settled* words) | `internal/app/worktree_landing.go:56` (`settledCommitment`) | Layer 7's fail-closed list, used by `protectsWorktree` (`:47`). |
| `Workspace.ResolveActor(name)` | `internal/app/app.go:1150` | Name to `apphost.Actor` with `.Fingerprint`, which is what `body.to` holds. |

> **Import direction warning.** `internal/mergeplan` imports `internal/app`
> (`internal/mergeplan/mergeplan.go:23`), so `internal/app` **cannot** import
> `internal/mergeplan`. Part B item 8 says "Reading is unified on the first", meaning
> mergeplan's `unsettledCommitment`. If S2's settled-lane read lands in `internal/app`
> it must either duplicate a third copy of the word list — which the Simplification
> conclusion should reject — or the word list moves down into `internal/workroom` and
> both existing sites read it from there. Recommendation: move it to
> `internal/workroom`, exported, keeping the two *defaults* distinct (mergeplan's
> unknown-means-settled for warnings; app's unknown-means-protected for deletion), so
> one vocabulary serves three readers. Alternatively put the settled read in `cmd/gs`,
> which already imports both (`cmd/gs/main.go:28`). This is an architecture decision
> the implementer must make explicitly and the reviewer must judge.

### 2f. S1's shared reporting — consume, do not recompute

The S2 request says "Use the shared S1 reporting rather than recomputing a competing
answer." The consumable surface:

| Thing | Path:line |
|---|---|
| `Workspace.CaptureCheckouts(ctx) (*CheckoutRead, error)` | `internal/app/checkout_read.go:65` |
| `Workspace.captureAround(ctx, views)` — one ref inventory, one 3s deadline, one 65,536-step budget | `internal/app/checkout_read.go:79` |
| `CheckoutRead.Close()` / `.Exhausted()` | `internal/app/checkout_read.go:52`, `:60` |
| `Workspace.AdviseCheckouts(read, snapshot) ([]string, AssociationTable)` — the **one** response-disposition step | `internal/app/checkout_read.go:119` |
| `Workspace.ClassifyCheckouts(read, projection)` | `internal/app/worktree_landing.go:286` |
| `Workspace.AssociateCheckouts(read, snapshot)` | `internal/app/association.go:149` |
| `AssociationTable` / `AssociationRow` / `AssociationClaim` | `internal/app/association.go:105`, `:90`, `:60` |
| Grades `claimed` / `corroborated` / `unresolved` / `foreign` / `unknown` | `internal/app/association.go:32-51` |
| `WorktreeView` (`DuplicateHead`, `ReflessHead`, `OutsideRoot`, `SymlinkedPath`, `Classification`, `Governing`, `Grade`, `PendingDecision`) | `internal/app/app.go:554-597` |
| `Workspace.LocalWorktrees(ctx)` with its 8s cache, `withinRoot`, `isSymbolicLink` | `internal/app/app.go:673`, `:841`, `:833` |
| Bounded ref inventory (`refs/heads/`, `refs/remotes/` only, 4096 cap) | `internal/app/landing_graph.go:68`, limit at `:13` |
| `gitstore.Store.Lineage`, `LineageTipLimit=256`, `LineageCommitLimit=512` | `internal/gitstore/lineage.go:71`, `:17-18` |
| Endpoint, including the unreadable-projection continuation | `internal/service/ui.go:136`, continuation at `:152-165` |

**Concretely, what S2 consumes.** The duplicate-head warning (R10) reads
`WorktreeView.DuplicateHead` off `LocalWorktrees`, not a fresh `git worktree list`.
The destination-containment refusal (R4c) reuses `withinRoot` and `isSymbolicLink`
rather than a second containment rule. The settled-lane warning (R6) reads
`Commitment.Status` off the same `Snapshot` the resolver used. Nothing in `gs worktree`
re-derives an association grade.

### 2g. The checkout root: an unresolved gap S1 disclosed

`internal/app/app.go:743-752` is explicit:

> "The derivation takes no configuration because there is none to take: the design
> note names a 'configured checkout root', and no such key exists in this source. A
> root that is too narrow can only protect a checkout that did not need it, never
> expose one that did, so the conservative direction is the one taken until the key
> exists."

The root is `filepath.Dir(selected)` — the directory holding the served checkout
(`internal/app/app.go:752`). That asymmetry is safe for S1 (a narrow root
over-protects) and **is not safe for S2**: R4c refuses a destination outside the root,
so a narrow root over-*refuses*, which blocks ordinary use. See section 6, Q1.

### 2h. Documentation gates S2 must satisfy

| Gate | Path:line | What it demands of S2 |
|---|---|---|
| CLI surface completeness | `internal/docset/surface_test.go:25` | `docs/reference/gs/worktree.md` must exist and its `## Flags` table must name **exactly** `--repo` plus every flag `worktreeCommand` registers on `set`. |
| Reference pages run something | `internal/docset/examples_test.go:51` | The page needs an `sh` block containing the literal string `gs worktree`, executed against a scratch workroom built from nothing. |
| Documented commands run | `internal/docset/examples_test.go:33` | Every `sh` block on the page actually runs. A hook example must `mkdir -p` its hooks directory first — `testgit.Isolate` replaces Git's template dir (`internal/testgit/environment.go:47-49`), so scratch repositories start **without** a hooks directory. |
| No empty basis | `internal/docset/basis_test.go:16` | Front matter needs `title`, `summary` and `rests_on` naming canonical event ids — S2's own behaviour artifacts, not just the request (docs rest on behaviour artifacts). |
| Flare | `internal/docset/flare_test.go:18`, `:94` | Retiring one act must flare exactly its pages and no act may flare most of the set. |
| Architecture contract | `docs/reference/architecture.md:1693` (layer-6 paragraph), `:1763-1857` (layer-7 association and cleanup paragraphs), and specifically `:1854` | `:1854` currently reads "What this does not add: no `gs worktree` command, no per-checkout record, no `refs/gitseq/lanes` ref, and no `branch` field on any durable kind." **S2 changes a stated contract and must rewrite this paragraph in the same head**, publishing its candidate artifact at `docs/reference/architecture.md` (AGENTS.md step 4). |
| Landing observations | `docs/reference/landing-observations.md:87-89` | The worktrees section. Only touched if S2 changes what `/v0/worktrees` reports (see Q4). |

---

## 3. Eligibility versus unreadable projection

The reconciled note settles this, and S1 wrote the settlement into the note. Quoted in
full from `notes/2026-09-09-lane-identity-reconciled.md:302-322`:

> **Unreadable projection against selector and record eligibility (S2).** These are two
> questions and the note already answers both. An eligibility refusal fires on a
> determinate negative: a selector matching no event here or several, a record that is
> neither a request nor an adopted decision, a request addressed to somebody else. An
> unreadable projection yields no such answer. It yields unknown, which layer 1 keeps
> distinct from unresolved and which never proves a negative. So creation must not
> report an eligibility check as passed when the projection could not be read, and must
> not report it as failed either. It says the check was not established, warns, and
> creates. What it creates grants nothing: the record is evidence and not permission,
> and durable admission reads the durable record itself, so nothing an unreadable
> projection let through can reach an authority check.
>
> One case does not follow from the refusal list as written. A `#N` or a hash fragment
> resolves against the projection, so with the projection unreadable there is no
> canonical identifier to stamp and creation has nothing to write. A full canonical
> identifier is classified without reading any log, so that case proceeds under the
> paragraph above. Refusing a short selector for want of a resolvable identifier is the
> only sensible outcome, but it adds to the adopted refusal list rather than reads it,
> so it is named here as an S2 proposal item and not slipped in as settled.

### 3a. The reconciliation, stated precisely

The two rules do not conflict because they answer about **different classes of
predicate**. An eligibility refusal fires on a *determinate negative* — a fact the
command established and that came back false. An unreadable projection produces no
determinate value at all; it produces `unknown`, which S1 already keeps as a distinct
grade (`internal/app/association.go:48-50`) and which "never proves a negative".

So each check has **three** outcomes, not two: `passed`, `refused`, `not established`.
The rule is: only `refused` stops creation, and only for checks whose input the command
actually read.

**Determinate refusals — these fire whenever their input is available, and each is a
hard refusal before any filesystem write:**

| Check | Input | When determinate |
|---|---|---|
| R4a ambiguous selector | `eventref.Set.Resolve` returned a `*Refusal` with candidates | Projection readable |
| R4a unresolved selector | `Resolve` succeeded but `Set.Resolvable` is false, or `Resolve` returned "names no event" | Projection readable |
| R4b wrong record kind | `Statement.Kind` is neither `request` nor a record for which `reviewguard.AdoptedDecision` returns nil | Projection readable |
| R4b wrong addressee | `Statement.Body["to"]` / `Commitment.AddressedTo` is not the acting actor's fingerprint, and the record is not an adopted decision | Projection readable |
| R4c destination exists / is a symlink / is outside the checkout root | The filesystem and `withinRoot` | **Always.** No projection needed. |
| R4d wrong repository | `apphost.ResolveGitDirs` common-directory comparison, as `internal/mergeplan/mergeplan.go:351` does; plus the record/genesis check | **Always.** No projection needed. |
| R4e unwanted existing branch | `git show-ref` / `RefValue` on `refs/heads/<name>` | **Always.** No projection needed. |
| Selector is `FormExternal` (another genesis) | `eventref.Room.Form` | **Always.** No projection needed — this is a determinate negative even with no log. |

Note the shape: **four of the eight determinate refusals need no projection at all.**
An unreadable projection therefore disables the selector and record-eligibility checks
only; every filesystem and repository guard still fires. That is what makes R28 safe.

**"Not established" warnings that still permit creation:**

| Check | What is said |
|---|---|
| Record kind, when the projection could not be read | "not established: could not read the durable record set" — never "passed", never "refused" |
| Addressee, likewise | same |
| Adopted-decision status, likewise | same |
| Settled-lane status (R6), likewise | "not established" — and note this means the confirmation flag is **not** required, because a settlement was never named. It also must not be reported as "not settled". |
| Duplicate head (R10), when the checkout listing could not be read | "not established" |

The output distinction must be visible in the command's own text, not only in an exit
code. Three words, one per check: `passed`, `refused`, `not established`. A test should
assert the literal absence of the word `passed` (and of anything that reads as consent)
on every check whose input was unavailable.

### 3b. What a `#N`-or-prefix selector does when the projection cannot be read

The mechanism is already in the source and needs no new classifier:

- `eventref.Room.Form` (`internal/eventref/eventref.go:94`) classifies without touching
  the log.
- `Resolver.One` (`internal/eventref/resolver.go:55-60`) returns a `FormCanonical`
  selector **unchanged, without calling the loader**. So a canonical selector needs no
  projection to yield an identifier to stamp.
- For `FormNumber` and `FormHash`, `Resolver.One` forces `Set()` and, on loader
  failure, returns `read the durable event set to resolve %q: %w`
  (`internal/eventref/resolver.go:63`).

So:

- **Canonical selector, unreadable projection**: proceed. There is a full identifier to
  write into the record and into `refs/gitseq/lanes/<hash>/<attempt>`. Every
  projection-dependent eligibility check reports "not established". This is R28's own
  case and needs no new rule.
- **`#N` or a hash fragment, unreadable projection**: there is **nothing to stamp**.
  The command cannot write a record, cannot name a lane ref, and cannot produce a
  trailer template, because a `Rests-On:` trailer takes the full canonical identifier
  only (`SKILL.md:440-441`, Part B item 3). Refusing is the only coherent outcome — but
  **the note says explicitly this is a proposal item, not settled**.

**Therefore: this refusal must not be implemented as settled adopted behaviour.** It
is a new entry on the adopted refusal list (R4). Two lawful paths for the implementer,
and only these two:

1. Carry it as an S2 proposal item, get it adopted (or at least an explicit disposition
   from Hugh or planner via the governing request's refiling), and then implement the
   refusal. This is the note's own instruction.
2. Implement it as a refusal *of the resolution step*, not of eligibility — i.e. let
   `Resolver.One`'s existing error propagate untouched, so the command fails for the
   ordinary, already-adopted reason "this selector could not be resolved", exactly as
   every other `gs` command already fails on the same input. Nothing new is added to
   the refusal list; the pre-existing resolver contract does the work.

Path 2 is the smaller change and is arguably already adopted, since R2 says the
selector resolves *through* `internal/eventref` and that package's refusal is part of
what R2 adopts. It is also honest: the message says the selector could not be resolved,
not that the record was ineligible. **Recommend path 2, and say so explicitly in the
implementation artifact so the reviewer can judge it.** Do not silently ship path 1's
behaviour under path 2's justification. See Q2.

---

## 4. The settled-lane rule

R6: "When the governing commitment is already settled by the existing word list
(`internal/mergeplan/mergeplan.go:967`) it warns, names the settling event, and proceeds
only behind an explicit confirmation flag" (`:140-143`).

### 4a. Which projection field distinguishes settled from merely stale

`workroom.Commitment` (`internal/workroom/fold.go:127`) carries both, as two separate
fields:

- **`Status string`** (`internal/workroom/fold.go:134`) is the settlement field.
- **`Stale bool`** (`internal/workroom/fold.go:136`) is a separate qualifier and is
  **not** a settlement.

The two word lists in the source, both reading `Status`:

```
internal/mergeplan/mergeplan.go:967   unsettledCommitment(status)
    unsettled: open, promised, reported, awaiting-review,
               awaiting-authorization, awaiting-landing, stale
    default: settled            <- unknown word means SETTLED

internal/app/worktree_landing.go:56   settledCommitment(status)
    settled: satisfied, abandoned, superseded, withdrawn, cancelled, reneged
    default: unsettled          <- unknown word means UNSETTLED (protects)
```

`"stale"` appears **inside the unsettled list** at
`internal/mergeplan/mergeplan.go:969`, and the comment at
`internal/app/worktree_landing.go:53-55` states the rule directly: "Staleness is not
settlement — a stale request is a request whose reasoning moved, and its work is still
owed." S1 pinned it with an assertion: `if settledCommitment("stale") { t.Fatal(...) }`
(`internal/app/checkout_reporting_test.go:247-249`).

**So, concretely:** `gs worktree` warns when `!unsettledCommitment(commitment.Status)`.
It does **not** warn on `Stale`, and it does not warn on `Status == "stale"`. A merely
stale governing record creates without a confirmation flag, and its staleness may be
*reported* (R8's warn-never-block) but must never gate creation.

Note the asymmetric defaults and pick deliberately. Part B item 8 (`changes:242-249`)
says reading unifies on mergeplan's list, where an unrecognised status word counts as
settled and therefore **warns**. That is the safe direction here, because the
consequence is a warning and a flag, not a refusal — the opposite direction from
deletion, where unknown must protect. Keep both defaults; do not "fix" one to match the
other.

### 4b. "Names its settlement"

R6 requires the warning to *name the settling event*, not merely say "settled".
The commitment carries the candidates:

- `Commitment.Report` (`internal/workroom/fold.go:133`) — the closing report.
- `Commitment.Terminal` and `Commitment.LandingReceipt` — used in S1's fixture at
  `internal/app/checkout_reporting_test.go:225`.
- `Commitment.SuccessorRequest` (`:135`) for a superseded commitment.

The implementer should name whichever of these is populated, plus the `Status` word
itself, and should have a test assert that the settling **event id** appears in the
warning text — not just the word "settled". An omission mutant that drops the event id
while keeping the word must turn that test red.

### 4c. "Only explicit local confirmation permits creation"

"Local" is doing real work in that sentence. A3 (`changes:78-95`) splits three
decisions and the flag reaches exactly one of them:

1. **Local creation** — the flag is read here, and only here. It permits a directory
   and a branch.
2. **Authoring assistance** — never blocks, so it has nothing for the flag to unblock
   (R8).
3. **Durable admission** — the fold decides from the durable record alone. The flag is
   never signed, never a body field, never a `rests_on`, never an input to admission
   (R9).

The proof obligation the note names (`:363`) is a *pair*: with the confirmation flag
present, local creation succeeds **and the fold refuses the durable act exactly as it
does today**. One fixture, two assertions. Passing only the first half proves nothing
about the boundary.

### 4d. What duplicate creation implies for CAS attempt numbering

R10 (`:283-286`) and A10 (`changes:198-209`): a second checkout at one head is warned,
not refused, and proceeds behind an explicit flag. R21 (`:203-205`): the attempt suffix
is what gives each recut its own ref.

These meet at attempt allocation, and the consequence is a rule that must be written
down explicitly because it is easy to get backwards:

- **Attempt allocation is a create against a missing ref** (R25) —
  `UpdateRef(ctx, laneRef(hash, n), tip, "")` with an *empty* expected old value, as
  `internal/kernel/kernel.go:352` already does. Not a read-then-write.
- **Allocation walks upward until a create succeeds.** Start at attempt 1; on refusal,
  try 2, and so on, bounded — model the bound on `claimAttempts = 8`
  (`internal/app/resident.go:188`), which "refuses rather than looping". A ref that
  keeps being taken under us is contention, and spinning trades a clear refusal for an
  unbounded wait.
- **A permitted duplicate gets a fresh attempt, never a reuse.** If the confirmation
  flag lets a second checkout be created for a governing record that already has
  `.../1`, the second checkout allocates `.../2`. Reusing `.../1` would make one ref
  name two lanes and would break R21's recut guarantee.
- **The confirmation flag does not change the CAS.** It permits the *checkout*; the ref
  allocator behaves identically with or without it. If a test can distinguish the CAS
  path by the flag, the flag has leaked into a place A3 says it must not reach.
- **A retry at the same tip is a no-op** (R24). Read the ref with `RefValue`
  (`internal/gitstore/gitstore.go:360`); if it already equals the intended tip, do
  nothing and report the existing attempt. This is what makes an interrupted
  `gs worktree` re-runnable, and it must not allocate a new attempt.
- **Losing the CAS is contention, not an error** (R23) —
  `internal/app/resident.go:465-469` states the reasoning: Git refuses for one reason
  that matters, and parsing Git's prose would be the alternative. A genuinely broken
  store exhausts the attempts and then refuses, the same fail-closed outcome by a
  slower route.
- **No lane ref is ever deleted by S2** (R30, A5). Attempts accumulate. Both attempt
  refs of one governing hash stay live, which is exactly the note's linked-worktree
  proof obligation (`:354`).

One ref-shape hazard: `refs/gitseq/lanes/<hash>` and `refs/gitseq/lanes/<hash>/1`
cannot both exist (Git directory/file conflict). Since S2 only ever writes the
`/<attempt>` form this never arises — but the ref-name builder should refuse a bare
`<hash>` form outright rather than leaving the invariant implicit.

---

## 5. Test surface

### 5a. What S1 already has, and what S2 reuses

`internal/app/association_test.go:21-148` is the reusable core:

| Helper | Path:line | S2 use |
|---|---|---|
| `associationFixture` / `newAssociationFixture` | `internal/app/association_test.go:21`, `:30` | One temporary repo, real Git, real workroom, one human and one agent actor. The base for every S2 fixture. |
| `f.git(...)` → `landingTestGit` | `internal/app/association_test.go:50`, `internal/app/landing_test.go:63` | Raw Git in the fixture repo. |
| `f.commit(msg)` / `f.commitAs(name, email, msg)` | `:58`, `:67` | Empty commits with exact messages and chosen author idents. `commitAs` is the forgery lever. |
| `f.assign(key)` → `(request, promise)` | `:81` | Files a request addressed to the agent with `target_ref`, plus the agent's promise. Directly supplies R3's positive case. |
| `f.artifact(key, path, commit, bases...)` | `:93` | The signed statement that promotes a claim. |
| `f.snapshot()` | `:100` | The `Snapshot` the resolver and eligibility read. |
| `f.inventory()` | `internal/app/checkout_reporting_test.go:14` | `LocalWorktrees` with the 8-second cache defeated — S2 fixtures change the world faster than the cache. |
| `f.view(views, name)` | `internal/app/checkout_reporting_test.go:28` | Pick a checkout by label. |
| `restsOn(id)` | `internal/app/association_test.go:148` | Builds the exact trailer text. |
| `treeDigest(t, root)` | `internal/app/association_test.go:568` | Path + mode + size + content hash of a whole tree. The no-write / no-deletion oracle. |
| `testRepo` / `testRepoOnMain` | `internal/app/app_test.go:26`, `:38` | Bare repository construction. |
| `internal/testgit` | `internal/testgit/environment.go:50` (`Isolate`) | Excludes system, global and command-scoped Git config; replaces the template dir, so scratch repos start with **no hooks directory**. |
| Linked-worktree patterns | `internal/app/checkout_reporting_test.go:51-52`, `:105-118`, `:174`, `:189` | Every state S2 must refuse into: dirty, locked, symlinked entry, outside-root, detached, refless. Reuse verbatim. |
| `TestAStaleGoverningRecordProtectsAndASettledOneDoesNot` | `internal/app/checkout_reporting_test.go:214` | The stale-versus-settled projection shape. S2's warning test is the same projection, a different assertion. |

Two facts about the fixture environment the implementer must know before writing the
hook tests:

1. **Scratch repositories have no `hooks` directory.** `testgit.Isolate` points
   `GIT_TEMPLATE_DIR` at a template containing only a `config` file
   (`internal/testgit/environment.go:82`), and the doc comment says so at
   `internal/testgit/environment.go:47-49`. The installer must `MkdirAll`; the
   "never replaces an existing hook" fixture must create the hook itself.
2. **A linked worktree's hooks resolve to the *common* `.git/hooks`.** Verified
   empirically: `git -C <linked-wt> rev-parse --git-path hooks` returns
   `<repo>/.git/hooks`. See Q3 — this is a design gap, not just a fixture detail.

S2 needs **one new fixture helper** beyond these: a real `git worktree add` performed by
`gs worktree` itself, plus `ResolveGitDirs` against the created path to find the record.
Everything else composes from the list above.

### 5b. The controls, with fixture shape and omission mutant

Each row: the control the request names, the fixture that proves it, and the single
change to production code ("the mutant") whose survival would mean the control proves
nothing. R32 requires each guard to be exercised once with the guard removed.

**1. Copy / foreign-record claims**

| | |
|---|---|
| Fixture | Two temporary workrooms, A and B. Create a checkout in A so a real record exists. Copy A's record JSON byte-for-byte into a checkout's private Git directory in B. Read it back in B. Also: pass a `FormExternal` canonical identifier of B's genesis to `gs worktree` in A. |
| Assertion | The copied record reads as `foreign` / not-of-this-workroom and confers nothing; the foreign selector is refused before any filesystem write and `treeDigest` of A is unchanged. |
| Mutant | Delete the genesis comparison in the record reader (the analogue of `internal/app/resident.go:495`). The copied record then reads as this workroom's own. |
| Positive control | A record written by this workroom in this workroom reads back correctly, so the check is not simply always-refusing. |

**2. Path containment and symlinks**

| | |
|---|---|
| Fixture | Build on `internal/app/checkout_reporting_test.go:99-118`. Ask `gs worktree` to create at: (a) a path that already exists; (b) a path whose final component is a symlink; (c) a path under `t.TempDir()` outside the derived checkout root; (d) a path reached through a symlinked parent. |
| Assertion | Each refuses **before any filesystem write** — assert `treeDigest` of the repository *and* of the destination's parent are byte-identical before and after, not merely that the command exited non-zero. |
| Mutant | Replace `withinRoot` (`internal/app/app.go:841`) with a `strings.HasPrefix` comparison. `/a/bc` then passes as inside `/a/b` — the exact defect the existing comment names. |
| Positive control | An ordinary sibling path inside the root creates successfully. |

**3. Missing or ambiguous selectors**

| | |
|---|---|
| Fixture | A hash fragment matching two events (S1's shape at `internal/app/association_test.go:287`); a `#N` past the end of the log; a canonical identifier of this genesis naming no event here. |
| Assertion | Refused before any write, with the typed refusal detail and **both** candidates named (`eventref.Refusal.Candidates`, capped at `MaxCandidates = 8`). `treeDigest` unchanged. |
| Mutant | Make the ambiguity path pick `Candidates[0]`. A checkout then gets created for one of two records. |
| Positive control | An unambiguous fragment of the same length resolves and creates. |

**4. Wrong record kind and wrong addressee**

| | |
|---|---|
| Fixture | (a) A selector naming an artifact statement; (b) a request addressed to a *third* actor while `--as agent`; (c) a ratified proposal (self-initiated, must be **accepted**); (d) a satisfied request (also accepted, per `reviewguard.AdoptedDecision`). |
| Assertion | (a) and (b) refuse before any write; (c) and (d) create. |
| Mutant | Drop the `AddressedTo` comparison. (b) then creates. Separately: drop the `AdoptedDecision` limb; (c) then refuses, which would silently forbid all self-initiated work — a false negative a "does it refuse?" test would never catch. |

**5. Temporary Git retries and CAS**

| | |
|---|---|
| Fixture | An injected `UpdateRef` seam that fails the first N calls with a transient ref-lock error, then succeeds. Assert the ref reaches the intended tip and the attempt number did **not** advance for a transient failure. Separately: call the allocator twice at the same tip and assert the second is a no-op returning the same attempt (R24). |
| Assertion | Bounded: exhausting the attempt budget refuses rather than looping. Assert a wall-clock or call-count bound, per `internal/app/resident.go:184-188`. |
| Mutant | Remove the `oldOID` argument (pass `""` on an update, or the ref name with no expected value). A lost update then goes unnoticed. Second mutant: make the retry loop unbounded; the exhaustion test hangs and must be caught by the test timeout, so prefer a call-count assertion over a timeout. |

**6. Concurrent attempts**

| | |
|---|---|
| Fixture | The note's own obligation (`:362`): two concurrent lane-ref writes for one governing hash. Two goroutines (or two child processes, which is stronger given flock is per open description — `internal/apphost/config.go:343-345`) each allocating an attempt. |
| Assertion | Exactly one wins each attempt number; the loser retries into a **fresh** attempt; both refs are live afterwards and point at different tips; no attempt number is reused. |
| Mutant | Turn the create-against-missing (`oldOID == ""`) into an unconditional update. Both writers then "succeed" and one silently overwrites the other; the assertion that both refs are live and distinct goes red. |
| Positive control | A single sequential allocation yields attempt 1, so the test is not passing merely because everything refuses. |

**7. Settled versus merely stale**

| | |
|---|---|
| Fixture | The projection shape from `internal/app/checkout_reporting_test.go:221-232`, three cases: `Status: "stale", Stale: true`; `Status: "satisfied"`; `Status: "some-future-status"`. |
| Assertion | Stale → creates with no confirmation flag and no settlement warning. Satisfied → refuses without the flag, creates with it, and the warning text contains the settling **event id**. Unknown word → warns (mergeplan's default), does not refuse outright with the flag present. Plus the pair from `:363`: with the flag present the **fold still refuses the durable act** exactly as today. |
| Mutant | Add `"stale"` to the settled side of whichever word list the command reads. Every stale lane then demands a flag. Second mutant: drop the event id from the warning while keeping the word "settled" — the "names its settlement" assertion must go red. |

**8. Cleanup / no-deletion invariants**

| | |
|---|---|
| Fixture | `treeDigest` (`internal/app/association_test.go:568`) around every S2 code path: a refused creation, a successful creation followed by a re-run, a lane-ref allocation, a hook install refused. Also run the whole path against a repository whose files are mode `0o555`, mirroring `TestTheAssociationPassWritesNothing` (`internal/app/association_test.go:542`). |
| Assertion | A refused creation writes nothing anywhere. A successful creation writes **only** the new checkout, its branch, its record and its lane ref — enumerate the expected delta rather than asserting "some change". Nothing S2 does removes a directory, a branch or a ref. |
| Mutant | Add a cleanup-on-failure `os.RemoveAll` to the creation path. The refused-creation digest test goes red if the destination's parent was pre-populated. |

**9. Hook behaviour**

| | |
|---|---|
| Fixture | Four cases in one temporary repository. (a) Default: no flag, assert **no hook file exists** afterwards. (b) Flag, no existing hook: `MkdirAll` the hooks directory first, install, assert exactly one `prepare-commit-msg` appears and contains exactly one appended `Rests-On:` line naming the full canonical identifier. (c) Flag, hook of that name already present with distinctive content: refuse, assert the file's bytes are **unchanged**. (d) Flag, `core.hooksPath` set to a directory outside the repository: refuse, assert nothing written there. |
| Assertion | The installed script execs nothing else — assert against the script text (no `$(`, no backtick, no `exec`, no `eval`, no interpreter beyond the shebang), and then run a real `git commit` in the checkout and assert the message gained exactly one trailer line and nothing else. Plus R18: `gs merge` still runs no commit hooks. |
| Mutant | Change the "hook exists" check from `os.Lstat` to `os.Stat` on a *symlinked* existing hook — the installer then writes through the link. Second mutant: relax the `core.hooksPath` containment to a prefix comparison; the outside path then passes. Third mutant: have the script interpolate the record's contents unquoted, and prove it with a record whose identifier field contains shell metacharacters. |
| Note | The `core.hooksPath`-inside-this-repository test must use `withinRoot`-style element comparison, not a string prefix, for the same reason as control 2. |

**10. Trailer template assistance**

| | |
|---|---|
| Fixture | A record whose selector was typed as `#N`; assert the template names the **full canonical identifier** (`SKILL.md:440-441`, Part B item 3). A workroom whose projection is unreadable plus a canonical selector; assert the template is still produced and the eligibility line says "not established". |
| Mutant | Write the selector as typed into the template. The `#N` fixture then produces a trailer no `Rests-On:` reader can resolve. |

**11. Unreadable projection**

| | |
|---|---|
| Fixture | The note's obligation (`:364`). Break the projection read — the most faithful seam is the one the endpoint already exercises (`internal/service/ui.go:152-165`, `Snapshot` returning an error). Then: (a) canonical selector → creates, each projection-dependent check reads **not established**, no durable act is admitted; (b) `#N` selector → the resolver's own refusal, nothing created. |
| Assertion | Assert on the literal text: for every check whose input was unavailable, the word `passed` does not appear and neither does anything reading as consent. |
| Mutant | Report an unavailable check as `passed`. The text assertion goes red. Second mutant: report it as `refused`; the creation-proceeds assertion goes red. Both directions need a mutant, because the note forbids both. |

**12. Duplicate creation**

| | |
|---|---|
| Fixture | The shape at `internal/app/checkout_reporting_test.go:43-85`. Create one checkout for a governing record; create a second for the same record without the flag (warn + refuse), then with the flag (creates). |
| Assertion | The second checkout allocates attempt **2**, not 1; attempt 1 still points at its original tip; both refs live. |
| Mutant | Make allocation reuse the highest existing attempt when the duplicate flag is set. The two-live-refs assertion goes red. |

**13. Refs survive checkout removal and branch deletion**

| | |
|---|---|
| Fixture | Create a checkout, allocate its lane ref, then `git worktree remove` it and `git branch -D` the branch. Read the lane ref back. |
| Assertion | The ref still exists and still names the tip; read back through the association it grades `claimed` and nothing more (R27, A7); the record file is gone with the checkout (A2's maintenance argument). |
| Mutant | Write the lane ref through a per-checkout git dir instead of `Store.Repo` (the common dir, `internal/app/app.go:868`). Removal then takes the ref with it. |

**14. Linked worktrees and recuts** (note obligation `:354`)

| | |
|---|---|
| Fixture | Two linked checkouts of one repository, one current; two attempt refs under one governing hash, both live. |
| Assertion | Both refs live and distinct; the current checkout is not confused with the new one. |
| Mutant | Resolve the record's home from `workspace.GitDir` (the *invoking* checkout's private dir) rather than from `ResolveGitDirs` against the created path. The second creation then overwrites the first checkout's record. |

### 5c. Gates

Per the request: the applicable normal Go, vet, race, UI and docset gates, plus the
adopted real-Git fixtures above. Concretely at the exact head: `gofmt -l`, `go vet ./...`,
`go build ./...`, `git diff --check`, `go test -race ./internal/app/... ./internal/gitstore/...`,
`go test ./internal/docset/...` (the four documentation gates), and `go test ./...` in
full — named-package lists have missed cross-package breakage before. `make ui-check`
only if any UI source changes; S2 should touch none, and if `/v0/worktrees` gains a
field (Q4) the TypeScript wire types must mirror it. No new broad test framework, and
no repeated whole-suite runs beyond the final gate.

---

## 6. Risks and open questions

Short, specific, and each one a thing to raise rather than decide.

**Q1 — The "configured checkout root" does not exist.** `internal/app/app.go:743-752`
records that S1 looked for the key the design names and found none, deriving the root as
`filepath.Dir(served checkout)` and stating that a too-narrow root is safe *for
protection*. R4c uses that same root for a **refusal**, where too-narrow is not safe: it
would refuse ordinary destinations. Does S2 (a) introduce the configuration key the
design has always named, (b) keep S1's derived root and accept the refusal it implies,
or (c) use a different root for creation than for protection? Option (c) means two roots
and should probably be rejected on Simplification grounds. **Needs planner or Hugh.**

**Q2 — The `#N`-with-unreadable-projection refusal is an unadopted proposal item.**
The note names it as such at `:319-322` and refuses to slip it in as settled. Section 3b
recommends letting the existing `eventref` resolver refusal do the work, which adds
nothing to the adopted refusal list. **Confirm that reading with planner before
implementation**, and have the refiled request either adopt the new refusal or record
that the resolver's own contract covers it.

**Q3 — A per-checkout hook is not possible; the hook is repository-wide.** Verified: a
linked worktree's hooks resolve to `$GIT_COMMON_DIR/hooks`, shared by every checkout of
the repository. So installing `prepare-commit-msg` "for a checkout" installs it for
`main` and every other worktree. R17 says the script "appends one line from the record
it was given" — a baked-in record path would then be wrong in every other checkout. The
only coherent reading is that the script resolves the record at run time from
`$(git rev-parse --absolute-git-dir)/…` and appends nothing when no record is there, so
it degrades to a no-op outside the stamped checkout. That reading also means the flag's
blast radius is repository-wide, which R16's "off by default, explicit flag" language
does not disclose. **The adopted text does not settle this. Raise it, and disclose the
blast radius in the artifact whichever way it is settled.** The alternative — per-worktree
`core.hooksPath` — needs `extensions.worktreeConfig`, which A2 rejected on exactly the
migration cost this would reintroduce.

**Q4 — Does S2 owe reading lane refs and records back into layer 1?** The reconciled
note's layer-1 `claimed` definition (`:83-84`) says "a trailer, **a lane ref or a
checkout record** names a resolvable event here". S1 shipped trailers only, correctly —
the other two did not exist. The bounded ref inventory reads only `refs/heads/` and
`refs/remotes/` (`internal/app/landing_graph.go:70`), so lane refs are invisible to the
association as it stands. But S2's stated usable result is "Create a stamped checkout for
one governing record, **and find its lane again after the checkout is gone**"
(`changes:286`), which requires *some* read path. Is that read path (a) a listing mode
of `gs worktree` itself, or (b) an extension of the S1 association and the `/v0/worktrees`
contract? Option (b) changes a layer-7 contract and a documented endpoint and pulls in
`docs/reference/landing-observations.md`; option (a) keeps S1's shape untouched but
risks being the "competing answer" the request forbids. **Needs an explicit disposition
in the refiled request.**

**Q5 — Where the settled word list lives.** `internal/mergeplan` imports `internal/app`
(`internal/mergeplan/mergeplan.go:23`), so `internal/app` cannot read
`unsettledCommitment` where Part B item 8 says reading should unify. Section 2e names
three options. A third copy of the word list would be a Simplification finding waiting to
happen. **Implementer decides; reviewer must judge it explicitly under Architecture.**

**Q6 — `workroom-fold@24`, not `@23`.** `internal/workroom/schema.go:16` reads
`ProfileVersion = "workroom-fold@24"` at main `86970224`, while the adoption note's
Part B item 5 (`changes:230`) and Part C (`changes:299-302`) both say "currently
`workroom-fold@23`". Harmless for S2, which advances no schema (A8, R31), but S3's
disclosed cost is now stated against a stale number. **Worth telling planner so S3's
request is not filed on it.**

**Q7 — What "wrong repository" means when the command is what creates the checkout.**
R4d cites the common-Git-directory comparison at `internal/mergeplan/mergeplan.go:351`,
which compares an *existing* checkout to the workroom. `gs worktree` creates the
checkout, so `git worktree add` from `--repo` makes it belong to that repository by
construction. The refusal that actually bites is a genesis mismatch — the record's
workroom versus this repository's — or `--repo` naming a repository that is not a
workroom at all, which `app.Open` already refuses. **Suggest the implementation artifact
state which of these R4d is understood to mean**, so a reviewer is not left checking a
condition that cannot fire.

**Q8 — Attempt-count bound and its refusal.** R25 says allocation is a create against a
missing ref, and R23 says competing creation loses cleanly, but the adopted text sets no
bound on how many attempts allocation walks through before it gives up. Section 4d
recommends borrowing `claimAttempts = 8` (`internal/app/resident.go:188`) and its
refuse-rather-than-spin reasoning. **A number chosen by the implementer, so name it and
its reason in the artifact rather than leaving it as an accident of the loop.**
