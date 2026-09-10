# Changes needing adoption, and changes needing none

Against the adopted 101 line design `notes/2026-08-27-lane-identity.md@6131249c`
(proposal cb755aa1, ratified 986a1ca2), reconciled at main
`d1ae6ade996af7167f22a2170dfaa6a200e004e1`.

Part A is the material list. It is written so it can go to Hugh as exactly one
revised ordinary proposal. Part B changes no policy and needs no adoption. Part
C is the recommended stage split.

Source references are `path:line` references, which the reading view resolves
against this note's own exact head, which is why none pins a revision. None is
a Markdown link: the local-links gate resolves a link target as a path
relative to this file and stats it, so a `path:line` target fails that gate
while the same text in an inline code span both passes it and opens the
preview. The lines were read at main
`d1ae6ade996af7167f22a2170dfaa6a200e004e1`. Where a line has since moved, the
reference names its position at this head rather than the position it was read
at, because a reference that opened somewhere else would be worse than none.
The superseded design is absent from this head, so it is quoted by line and
cited by revision rather than referenced.

## Part A: material differences, for one revised proposal

### A1. A source commit trailer is evidence, not authority

**Old (lines 39 to 41):** "Implementing commits already carry `Rests-On:`
naming the governing request event. The commit hash seals that trailer, so commit
to work item is already durable and unforgeable."

**Proposed:** The kernel's own event envelope trailer is unforgeable, because it
is compared byte for byte with the signed intent
(`internal/kernel/kernel.go:1535`). An implementing source
commit's `Rests-On:` is ordinary message text that nobody signs and nothing
verifies. The hash seals the bytes of a claim, never its truth: anyone who can
write a commit can name any event. An association derived from a source trailer
is `claimed` until a signed artifact statement names that exact commit and the
existing owned edge ties its signing actor to the governing record
(`internal/reviewguard/binding.go:234`). A `claimed` association
never widens the deletable set and never authorises a signature.

**Reason:** As written, the adopted sentence turns arbitrary commit text into
signing and filesystem authority. This is the single security correction the
request asks for, and everything else in Part A follows from it. Corroboration
reads only the actor an event signature names: a Git author ident is committer
controlled (`internal/gitstore/graph.go:36`), so copying
a real performer's ident alongside a forged trailer promotes nothing, and no
second actor identity system is introduced.

### A2. The per checkout record is not Git config

**Old (lines 54 to 56):** "writes `git config --worktree
gitseq.request=<full canonical event id>`. Worktree identity is inherently
local, so worktree-local config is its correct home."

**Proposed:** The record is a small JSON file in the checkout's own private Git
directory, written through the existing atomic file and flock pattern
(`internal/apphost/config.go:346`,
`internal/apphost/config.go:420`). It holds the full canonical governing
identifier and this workroom's genesis.

**Reason:** Cost, not prohibition. The earlier note at
`notes/2026-08-21-notes-and-decisions.md:238` is
about who signs: it rejects a clone wide `gitseq.actor` default because the
signature is the attribution, and it explicitly keeps identity scoped to the
process or the worktree. It does not forbid an advisory governing record key,
and this design does not claim it does. The comparison is made on three costs
instead. Migration: `--worktree` scope is read only once the repository sets
`extensions.worktreeConfig` (`internal/app/app.go:366`), a
repository wide change for one advisory value. Locking: the atomic write and
flock pair already exists and already holds per checkout state, while `git
config` offers no discipline this code shares. Maintenance: that configuration
is a scope the source documents as executable rather than merely readable
(`internal/app/app.go:369`), while a file in the private Git directory is
removed with the checkout by the machinery that made it, and
`apphost.ResolveGitDirs` already separates that directory from the common one.

### A3. A settled lane is three decisions, not one

**Old (lines 59 to 61):** "Any `gs` command run inside the worktree knows its
governing lane and refuses durable acts once that lane settles, enforcing the
one-writer rule and preventing work on dead lanes."

**Proposed:** Local creation warns, names the settling event and proceeds behind
an explicit confirmation flag. Authoring assistance warns and never blocks, and
reports unknown as unknown. Durable admission is unchanged: the fold decides
from the durable record alone, no flag and no local record is an input to it,
and a settled commitment refuses exactly the acts it refuses today.

**Reason:** A value the actor can write is a value the actor can unset, so a
refusal keyed on it stops honest actors and stops nobody else, and an unreadable
projection must not deadlock an actor mid work. The one writer rule is a durable
rule and is not enforceable from a local file. Keeping the three apart is what
stops a confirmation flag from ever reading as permission: it silences a local
warning and never reaches an authority check.

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
(`docs/reference/gs/merge.md:239`), precisely because a hostile
`pre-commit` hook can retarget `HEAD` (`cmd/gs/main.go:946`).

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
(`docs/reference/landing-observations.md:89`) and
states that the actor doing cleanup must recheck before deleting.

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
boundary and publishes basenames only (`internal/app/app.go:188`,
`internal/app/app.go:751`). Recording paths durably would put one machine's
directory layout in the permanent record and disclose it to every reader.

### A9. The request `branch` field is kept and validated, beside `target_ref`

**Old (lines 79 to 80):** "`body.branch` is promoted from advisory prose to a
validated field on request and promise kinds."

**Proposed:** The adopted promotion is kept. Request, promise, report and
artifact kinds gain an optional `branch`, validated by the same `validBranchRef`
(`internal/workroom/landing.go:121`) and admitted as a typed
optional field of the declared vocabulary
(`internal/workroom/kinds.go:22`). It is display and search only:
it never substitutes for `target_ref`, never moves a merge, never licenses a
deletion and never corroborates an association.

**Reason:** `target_ref` and `branch` answer different questions and are not two
answers to one. `target_ref` is the landing destination the merge enforces;
`branch` is the implementing checkout the work happens on before it lands
anywhere. Both are already documented
(`docs/reference/gs/state.md:70`,
`docs/reference/gs/state.md:83`), and this very request carries
`target_ref=refs/heads/main` with `branch=request/lane-worktree-identity`. So
`target_ref` does not deliver layer 4's request field, and the adopted field is
kept rather than dropped. What changes is only that validation buys a storable,
renderable string and grants no authority: an advisory `branch` on a request
that names a checkout nobody can verify must not become an input to any
decision.

### A10. Duplicate checkouts are warned, not refused

**Old (lines 91 to 92):** "Duplicate checkouts at one head become detectable at
creation time and can be refused."

**Proposed:** Reported at inventory time, warned at creation, proceeding behind
an explicit flag.

**Reason:** A second checkout at the target ref's own head is ordinary, and this
repository has one today (`main` and `request/lane-worktree-identity` both sat
at `d1ae6ade` when this branch was cut). A flat refusal would block ordinary
recut and review checkouts.

## Part B: pure reconciliation, no adoption needed

1. **Bounded scan and cache invalidation.** The old text says the resident
   "parses the trailer at each tip's lineage" with no bound. The conditions
   require bounds, and current source supplies the shape: the 65,536 step
   budget, 4,096 commitment cap and 3 second deadline
   (`internal/app/worktree_landing.go:12`,
   `internal/app/landing.go:45`), plus a cache keyed on the
   durable frontier and the observed ref inventory digest.
2. **Self initiated work.** Recognition already exists at
   `internal/reviewguard/binding.go:408`
   (`resolveSelfInitiated`) and `internal/reviewguard/binding.go:447`
   (`adoptedDecision`). Reusing it is not a policy change; it is how AGENTS.md
   step 1 already reads.
3. **Trailers take the full canonical identifier only.**
   `SKILL.md:440` already says so. Trailer assistance resolves the
   selector at the tool boundary and writes only the resolved identifier.
4. **One workroom scoping.** `internal/eventref` already refuses to search Git's
   object database and already treats another genesis as a carried citation.
5. **Schema and fold version cost.** Adding a validated `branch` to request,
   promise, report and artifact is a statement schema advance and a bump of
   `ProfileVersion` (`internal/workroom/schema.go:16`, currently
   `workroom-fold@23`), which rejects the existing projection cache and replays
   history once. Older records keep their old reading. This is a disclosed cost,
   not a changed policy.
6. **Naming.** "Lane" already names a work query lane
   (`internal/statusview/query.go:23`). New code says "checkout"
   and "governing record"; `refs/gitseq/lanes` stays a ref namespace string.
7. **No new periodic job.** The old text hoped for "a periodic hygiene gate ...
   every planner tick". The existing endpoint already answers it under an
   8 second cache; the gate is a read, and it writes nothing.
8. **Two settled word lists exist.**
   `internal/mergeplan/mergeplan.go:967`
   (`unsettledCommitment`) and
   `internal/app/worktree_landing.go:47`
   (`protectsWorktree`) express the same idea with different defaults. Reading
   is unified on the first; the fail closed protect on unknown stays where it
   belongs, on deletion, and is deliberately not carried into any refusal to
   sign, which would deadlock.
9. **Containment, symlink direction and refless heads.** Protective additions
   the conditions name. They only ever move a checkout from candidate to
   protected.
10. **Command environment.** `internal/app` runs Git under a closed environment
    allowlist (`internal/app/app.go:346`), while `cmd/gs`'s helper
    inherits the ambient environment (`cmd/gs/main.go:3282`). A
    new `gs worktree` uses the bounded path for every read it makes about
    identity.
11. **Correction to the brief.** `mergeplan.Result.Mode` values are `fresh`,
    `used` and `resume`; there is no `complete` mode. No design consequence.
12. **Scratch trees.** The existing patterns are the disposable clone with hooks
    emptied (`internal/mergeplan/mergeplan.go:1594`) and the
    perf harness's own add and remove pair
    (`cmd/gitseq-perf/main.go:833`,
    `cmd/gitseq-perf/main.go:839`). Reusing them is not new policy.
13. **A pending decision protects its candidate.** An artifact a live proposal
    cites is protected while its parent request is unsettled, even when that
    request is stale or missing from a default actionable list, and the
    classifier reads the proposal's structural `rests_on` edge rather than
    assuming prose is the only reference. Request lifecycle, candidate
    retirement, decision adoption and checkout removal stay four distinct
    facts. Disclosure #22184 and planner assert 9d80e5c9 supply the worked
    example, and the conditions already require protected active and approved
    heads and safe cleanup advice, so this adds no policy. The proof set gains
    a positive control for genuinely abandoned work, so the safeguard cannot
    make cleanup impossible.

## Part C: recommended stage split

Three stages, not six. Each is a child request under the parent, filed and
assigned before anything it covers lands, with its own review and its own merge.
The parent promise stays open until the last one lands.

| Stage | Content | Usable result |
|---|---|---|
| S1 | This design note and the architecture layer 6 and 7 contract paragraphs it changes, published at the same head; layer 1 read only association with its grading, bounds and cache key; the cleanup, duplicate, refless head, containment and pending decision reporting that reads the same table | Ask which durable record each checkout and branch claims, which claims are corroborated, and which checkouts are candidates. Writes nothing, anywhere |
| S2 | Layers 2 and 3 together: lane refs with attempt allocation and compare and swap, `gs worktree`, the per checkout record, the commit message template, the settled lane warning, the optional narrow hook behind its flag | Create a stamped checkout for one governing record, and find its lane again after the checkout is gone |
| S3 | Layer 4: the validated optional `branch` on request, promise, report and artifact, with its schema advance and fold profile bump | Filter and render work by implementing checkout |

Why three and not six. Layers 2 and 3 are one slice because creation is what
allocates an attempt ref and writes the record: landing the ref allocator alone
ships a namespace nothing writes and nothing reads, provable only against a test
that is also the only caller. The cleanup extensions are one slice with layer 1
because they are the same read, the same bounds and the same cache, adding
reporting fields rather than a mechanism. Splitting either pair buys a review
cycle and a merge window and no usable result.

S3 is separate for a deployment reason, not a layering one. It is the only stage
that advances the statement schema and bumps the fold profile version
(`internal/workroom/schema.go:16`), which rejects every reader's
projection cache and replays history once. Landing it alone keeps that replay
attributable to one merge, makes reverting it one revert, and keeps it out of
the same restart as a resident behaviour change. It depends on nothing in S1 or
S2 and may land in any order beside them.

**What a partial landing must not claim.** Each stage's artifact states what it
implemented and what it did not. S1 must not claim that a `gs worktree` command,
a per checkout record or any `refs/gitseq/lanes` ref exists, or that layers 2, 3
and 4 are delivered; S2 must not claim layer 4; S3 must not claim any layer 1 to
3 behaviour. No stage landing closes the parent promise, and no ratification of
one may be read as retiring the four layer outcome. The reviewer of each stage
should be asked to confirm exactly that in the architecture conclusion.
