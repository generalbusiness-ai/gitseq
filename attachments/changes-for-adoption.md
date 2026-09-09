# Changes needing adoption, and changes needing none

Against the adopted 101 line design `notes/2026-08-27-lane-identity.md@6131249c`
(proposal cb755aa1, ratified 986a1ca2), reconciled at main `d1ae6ade`.

Part A is the material list. It is written so it can go to Hugh as exactly one
revised ordinary proposal. Part B changes no policy and needs no adoption. Part
C is the recommended stage split.

## Part A: material differences, for one revised proposal

### A1. A source commit trailer is evidence, not authority

**Old (lines 39 to 41):** "Implementing commits already carry `Rests-On:`
naming the governing request event. The commit hash seals that trailer, so commit →
work-item is already durable and unforgeable."

**Proposed:** The kernel's own event envelope trailer is unforgeable, because it
is compared byte for byte with the signed intent
(`internal/kernel/kernel.go:1535`). An implementing source commit's `Rests-On:`
is ordinary message text that nobody signs and nothing verifies. The hash seals
the bytes of a claim, never its truth: anyone who can write a commit can name
any event. An association derived from a source trailer is `claimed` until
durable evidence corroborates it, and a `claimed` association never widens the
deletable set and never authorises a signature.

**Reason:** As written, the adopted sentence turns arbitrary commit text into
signing and filesystem authority. This is the single security correction the
request asks for, and everything else in Part A follows from it.

### A2. The per checkout record is not Git config

**Old (lines 54 to 56):** "writes `git config --worktree
gitseq.request=<full canonical event id>`. Worktree identity is inherently
local, so worktree-local config is its correct home."

**Proposed:** The record is a small JSON file in the checkout's own private Git
directory, written through the existing atomic file and flock pattern
(`internal/apphost/config.go:346`, `:420`). It holds the full canonical
governing identifier and this workroom's genesis.

**Reason:** Three facts. This repository already decided there is "no tracked
config and no `git config gitseq.*`"
(`notes/2026-08-21-notes-and-decisions.md:238`), because a clone wide default
misattributes. The `--worktree` scope answers only half of that: it requires
enabling repo wide `extensions.worktreeConfig`, which changes config reading for
every checkout. And repository and worktree config is a scope the source
documents as executable rather than merely readable
(`internal/app/app.go:355`), so putting identity there puts it in the worst
available place. `apphost.ResolveGitDirs` already separates the per checkout Git
directory from the common one for exactly this purpose.

### A3. A settled lane warns and asks; it does not refuse on the local record

**Old (lines 59 to 61):** "Any `gs` command run inside the worktree knows its
governing lane and refuses durable acts once that lane settles, enforcing the
one-writer rule and preventing work on dead lanes."

**Proposed:** The command and the authoring boundaries warn, name the settling
event and require an explicit confirmation flag. Refusal stays with the fold,
which already decides admissibility from the durable record.

**Reason:** A value the actor can write is a value the actor can unset, so a
refusal keyed on it stops honest actors and stops nobody else. And an unreadable
projection must not deadlock an actor mid work. The one writer rule is a durable
rule; it is not enforceable from a local file.

### A4. The commit hook is opt in and narrow, and nothing depends on it

**Old (lines 62 to 63):** "A commit hook appends the correct `Rests-On:`
trailer automatically, removing the transcription errors already present in the
log."

**Proposed:** The default assistance is a commit message template naming the
exact trailer line. A hook installs only with an explicit flag, only when no
hook of that name exists and `core.hooksPath` is unset or inside this
repository. It is one `prepare-commit-msg` script that appends one line and
execs nothing else, and it never replaces a user hook.

**Reason:** Hooks are user property, and installing over one is a filesystem
mutation with no durable authority behind it. Nothing may be load bearing on a
hook in any case: `gs merge` deliberately runs no commit hooks at all
(`docs/reference/gs/merge.md:236`), precisely because a hostile `pre-commit`
hook can retarget `HEAD` (`cmd/gs/main.go:946`).

### A5. Cleanup is advised, never performed by merge or retirement

**Old (lines 64 to 66 and 86 to 88):** "Merge receipts and request retirements
can *compute* the worktree that has become removable" and "cleanup can be
prompted, or performed, at every lane ending".

**Proposed:** Merge and retirement print the current read only advice and name
the candidate checkouts. Neither performs a removal. Removal stays a separate
deliberate act by a person or by W1.

**Reason:** `gs merge` already holds a lock and a compare and swap window on the
one irreversible act in the system; adding filesystem mutation widens that blast
radius for no gain. The read only endpoint already computes the answer
(`docs/reference/landing-observations.md:85`) and states that the actor doing
cleanup must recheck before deleting.

### A6. Lane refs are keyed by governing record and attempt

**Old (lines 72 to 75):** "`refs/gitseq/lanes/<request-event-hash>` pointing at
the lane tip ... gives every recut its own ref without anyone choosing a name."

**Proposed:** `refs/gitseq/lanes/<governing event hash>/<attempt>`, where the
governing record is the request or, for self initiated work, the adopted
decision, and the attempt is allocated by a create against a missing ref.

**Reason:** The adopted text is internally inconsistent. One ref per request
hash cannot give every recut its own ref, because a recut under one request
shares that hash. The attempt suffix is what makes the recut proof possible. The
governing record widening is what makes self initiated work nameable without
inventing a self request, which AGENTS.md step 1 forbids.

### A7. A lane ref names the lane in one direction, not two

**Old (line 73):** "The ref names the lane exactly, in both directions."

**Proposed:** The ref answers governing record to tip. Tip to governing record
stays the trailer plus durable corroboration, because a ref can be written by
anyone with repository write access and can point anywhere.

**Reason:** Same boundary as A1. A ref is claimed identity, not evidence.

### A8. No `body.worktree` field is added

**Old (lines 79 to 80):** "`body.branch` is promoted ... joined by
`body.worktree`."

**Proposed:** No worktree path field is added to any durable kind. The checkout
label stays in the ephemeral endpoint where it already lives.

**Reason:** This is the one adopted sub item the reconciled design drops
outright, so it needs an explicit decision. A filesystem path is machine local
and is not portable across the actors and repositories that read one log; the
current surface deliberately keeps other checkouts' paths inside the resident
boundary and publishes basenames only (`internal/app/app.go:178`, `:690`).
Recording paths durably would put one machine's directory layout in the
permanent record and disclose it to every reader.

### A9. The validated branch field on requests already exists as `target_ref`

**Old (lines 79 to 80):** "`body.branch` is promoted from advisory prose to a
validated field on request and promise kinds."

**Proposed:** Requests keep `target_ref`, already validated by `validBranchRef`
(`internal/workroom/landing.go:121`). The optional validated `branch` is added
to promise, report and artifact kinds only, and is display and search only.

**Reason:** Two validated branch names on a request would be two answers to one
question, and `target_ref` is the one the merge already enforces. Layer 4's
stated purpose, "these serve rendering and search", is met without touching
requests.

### A10. Duplicate checkouts are warned, not refused

**Old (lines 91 to 92):** "Duplicate checkouts at one head become detectable at
creation time and can be refused."

**Proposed:** Reported at inventory time, warned at creation, proceeding behind
an explicit flag.

**Reason:** A second checkout at the target ref's own head is ordinary, and this
repository has one today (`main` and `request/lane-worktree-identity` both sit
at `d1ae6ade`). A flat refusal would block ordinary recut and review checkouts.

## Part B: pure reconciliation, no adoption needed

1. **Bounded scan and cache invalidation.** The old text says the resident
   "parses the trailer at each tip's lineage" with no bound. The conditions
   require bounds, and current source supplies the shape: the 65,536 step
   budget, 4,096 commitment cap and 3 second deadline
   (`internal/app/worktree_landing.go:11`, `:13`), plus a cache keyed on the
   durable frontier and the observed ref inventory digest.
2. **Self initiated work.** Recognition already exists at
   `internal/reviewguard/binding.go:402` (`resolveSelfInitiated`) and `:436`
   (`adoptedDecision`). Reusing it is not a policy change; it is how AGENTS.md
   step 1 already reads.
3. **Trailers take the full canonical identifier only.** `SKILL.md:440` already
   says so. Trailer assistance resolves the selector at the tool boundary and
   writes only the resolved identifier.
4. **One workroom scoping.** `internal/eventref` already refuses to search Git's
   object database and already treats another genesis as a carried citation.
5. **Schema and fold version cost.** Adding a validated `branch` to promise,
   report and artifact is a statement schema advance and a bump of
   `ProfileVersion` (`internal/workroom/schema.go:16`, currently
   `workroom-fold@23`), which rejects the existing projection cache and replays
   history once. Older records keep their old reading. This is a disclosed cost,
   not a changed policy.
6. **Naming.** "Lane" already names a work query lane
   (`internal/statusview/query.go:23`). New code says "checkout" and "governing
   record"; `refs/gitseq/lanes` stays a ref namespace string.
7. **No new periodic job.** The old text hoped for "a periodic hygiene gate ...
   every planner tick". The existing endpoint already answers it under an
   8 second cache; the gate is a read, and it writes nothing.
8. **Two settled word lists exist.** `internal/mergeplan/mergeplan.go:967`
   (`unsettledCommitment`) and `internal/app/worktree_landing.go:46`
   (`protectsWorktree`) express the same idea with different defaults. Reading
   is unified on the first; the fail closed protect on unknown stays where it
   belongs, on deletion, and is deliberately not carried into any refusal to
   sign, which would deadlock.
9. **Containment, symlink direction and refless heads.** Protective additions
   the conditions name. They only ever move a checkout from candidate to
   protected.
10. **Command environment.** `internal/app` runs Git under a closed environment
    allowlist (`internal/app/app.go:336`), while `cmd/gs`'s helper inherits the
    ambient environment (`cmd/gs/main.go:3282`). A new `gs worktree` uses the
    bounded path for every read it makes about identity.
11. **Correction to the brief.** `mergeplan.Result.Mode` values are `fresh`,
    `used` and `resume`; there is no `complete` mode. No design consequence.
12. **Scratch trees.** The existing patterns are the disposable clone with hooks
    emptied (`internal/mergeplan/mergeplan.go:1563`) and the perf harness's own
    add and remove pair (`cmd/gitseq-perf/main.go:833`, `:839`). Reusing them is
    not new policy.

## Part C: recommended stage split

Each stage is a child request under the parent, with its own review and its own
merge. The parent promise stays open until the last one lands.

| Stage | Content | Depends on |
|---|---|---|
| S0 | This design note plus the architecture layer 6 and 7 contract paragraphs it changes, published at the same head | none |
| S1 | Layer 1: read only association, grading, bounds, cache key; new fields on the existing worktree rows | S0 |
| S2 | Layer 3: lane refs, attempt allocation, compare and swap, survival across checkout removal | S0 |
| S3 | Layer 2: `gs worktree`, the per checkout record, the commit message template, the settled lane warning, the optional narrow hook behind its flag | S1, S2 |
| S4 | Layer 4: validated optional `branch` on promise, report and artifact; schema advance and fold profile bump. Lands alone, because it is the only stage that touches the fold | S0 |
| S5 | Cleanup extensions: duplicate checkout reporting, refless head reporting, containment and symlink checks, merge and retirement advice printing, scratch tree discipline | S1 |

**What an S0 design only landing must not claim.** It must not claim that any of
the four layers is implemented; that a `gs worktree` command, a `gitseq` per
checkout record, or any `refs/gitseq/lanes` ref exists; that cleanup candidates,
duplicate checkouts or refless heads are computed by anything new; or that the
adopted outcome is delivered. Its artifact statement says documentation only,
with no gates beyond the documentation set. It must not close the parent
promise, and no ratification of it may be read as retiring the four layer
outcome. The reviewer should be asked to confirm exactly that in the
architecture conclusion.
