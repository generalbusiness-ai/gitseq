# Round-two codebase simplification review

This review describes `main` at exact commit
`9f2f7b4f04e71de38e0250907157a6eb0b780ba7`. It covers `cmd`, `internal`,
`host`, `ui`, and the documentation tooling. It is a read-only survey: this
head changes no source code.

The review looked for duplicate ownership of one rule, code no reachable
behaviour needs, configuration or ceremony standing in for a decision, and
meaning crossing the layer contracts in `docs/reference/architecture.md`.
The findings below are deliberately a short ranked list. They are proposals
for separate implementation requests, not authority to change the code.

I did not audit third-party dependencies, production traffic, deployment
configuration outside this repository, or cryptographic primitives. I also
did not treat file size, repeated projection scans, or test seams as defects
without evidence that they duplicate ownership or reachable work.

All seven findings were re-checked against `main` at the commit above. Six
remain as originally diagnosed. Finding 4 moved: its implementation has been
repaired on `request/ci-suite-dedup` at `073d68c7`, but that head is not an
ancestor of `main` and its ready-for-review report `eb0741b6` has not yet
produced an effective verdict. The finding therefore remains current, but the
next action is review and landing rather than another implementation.

## Ranked findings

### 1. Verify generated assets before a merge is sealed

**Evidence.** `cmd/gs/main.go:485-547` creates a tentative merge, plans
succession, and commits it without a repository verification seam. The
purpose-built `ui-check` target at `Makefile:26-33` rebuilds and compares the
embedded UI, but `.github/workflows/ci.yml:53-56` calls `make ui` instead and
only the generic final diff at lines 79-80 detects a mismatch. Commit
`88b51864` records the concrete failure: its parent `a3f416ec` is the merge
that left the committed browser bundle matching neither merged source nor
either parent.

**Impact.** An approved source head can still produce a broken merge commit
when Git combines generated output. Repair then needs an extra main commit and
another round of artifact succession. The existing local gate also has no
shared caller, so the same failure is reported late and generically in CI.

**Effort:** M (about a day). **Risk:** medium. **Confidence:** high.

**Bounded fix.** Make the UI check build into a temporary output directory and
compare without modifying the checkout. Add one argv-based repository verifier
to `gs merge`, run it after the tentative merge and before `git commit`, and
configure this repository to call the same check from CI and the documented
pre-merge path. A failed verifier must abort the tentative merge without
spending the approval. The verifier command must come from trusted application
or repository policy, never from inherited or repository-local Git
configuration. This preserves the current layer contracts.

### 2. Reuse Workroom lifecycle admission rules during authoring

**Evidence.** `internal/app/app.go:741-777` says that
`normalizeRequestShape` mirrors the fold while independently resolving the
active lifecycle, required fields, and requested actor. `validateReportBasis`
at lines 780-825 separately rebuilds lifecycle, decision, and statement
indexes to enforce promise and report rules. The vocabulary already declares
request fields and promise/report basis counts at
`internal/workroom/kinds.go:163-171`; the authoritative fold enforces those
counts and actor relationships again at `internal/workroom/fold.go:538-570`
and traverses the generic constraints at lines 577-610.

**Impact.** One vocabulary or lifecycle change has to keep an application
preflight and the authoritative fold aligned by hand. Until both are changed,
an authoring surface can refuse an act the fold would admit, or sign an act the
fold will reject.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Add a pure Workroom candidate validator over an active
vocabulary and an immutable effective-state view. Let `internal/app` retain
human address resolution, then delegate field, basis, and lifecycle-actor
checks to that validator. Adapt the fold's indexes to the same rule; the fold
must remain the definition and `internal/app` its consumer, never the reverse.
The fold remains authoritative when the frontier advances after preflight.
The implementation changes the `internal/workroom` and `internal/app`
boundary rows in `docs/reference/architecture.md`; both changes and their
candidate artifacts belong in the same head. Convergence removes duplicated
policy but also removes crude defence in depth, so mutation tests must prove a
preflight defect cannot broaden what the fold admits.

### 3. Restore `residentclient` to a transport-only boundary

**Evidence.** The package contract at `internal/residentclient/client.go:1-3`
says commands choose when to use a resident while this package owns safe URLs
and byte transport. It nevertheless imports `internal/app`, `internal/kernel`,
and `internal/workroom` at lines 21-23. `Submit` at lines 302-313 chooses
local application acceptance versus HTTP submission, and
`UndefinedKindWarning` at lines 316-323 opens and interprets the Workroom
vocabulary. The architecture table assigns deliberate kernel/Workroom
coupling to `internal/app` and says new application meaning stays above the
lower boundaries.

**Impact.** The successful HTTP-mechanism convergence has acquired a second
application coupling point. Workroom vocabulary, application snapshot, kernel
request, and local-fallback changes now ripple into a package whose reusable
job is bounded loopback transport.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Keep URL validation, bounded HTTP, response classification,
and a transport-neutral liveness probe in `residentclient`. Move submission
routing and undefined-kind interpretation into `internal/app` or the composing
commands. Preserve the asymmetric liveness rule and loopback-only SSRF guard,
and require every caller that reaches the network to do so through
`residentclient.PostJSON`. Add an `internal/residentclient` row to the
architecture boundary table in the same head; the missing row is part of the
current ownership gap.

### 4. Execute each Go test result once in CI

**Evidence.** `.github/workflows/ci.yml:46-59` runs `make test`, `make race`,
and `make spike`. `Makefile:5-13` shows that the first two targets exercise
the same Go package set, once plain and once under the race detector.
`spike/cmd/gitseq-report/main.go:217-245` launches another uncached `go test`
for each package containing an adversarial case, and
`.github/scripts/verify-preview-clone.sh:26-28` runs the complete plain suite
again in the fresh clone. The earlier single-stream head `e82d9fff` is not an
ancestor of `main`; it received a ratified changes-requested verdict because
it removed unique forbidden-path coverage from `layout_test.go`.

**Impact.** Every CI run executes all Go packages three times, plus a fourth
execution of the adversarial subset. Changes-requested report `b88d4573`
measured 174 seconds for the single race run, so overlapping full-suite
invocations materially lengthen feedback while dividing responsibility for
the same evidence.

**Effort:** S to review and land. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Review and land corrected head `073d68c7` on
`request/ci-suite-dedup`. Its ready-for-review report `eb0741b6` states that
one uncached `go test -race -json ./...` stream drives the adversarial evidence
projector, the preview clone retains vet, build, and topology checks without
rerunning tests, and the four forbidden shipping paths are restored. Re-check
those claims and current-main mergeability during the exact-head review; do
not reopen the implementation by porting the changes-requested `e82d9fff`
head.

### 5. Delete unreachable UI paths

**Evidence.** `GraphCommit` and `api.graph` at
`ui/src/lib/api.ts:237-246,310-321` have no caller in `ui/src` or `ui/test`.
The sole production consumer of `buildThreadIndex` asks only for `.content`
at `ui/src/lib/spine.ts:61-66`, but `ui/src/lib/threads.ts:9-52` still
constructs a summary API and computes every summary. `threadChildren` at
lines 72-74 is reached only through the barrel export at
`ui/src/lib/store.ts:3`; the interaction test imports `buildThreadIndex`,
not `threadChildren`. The worktree client retains and normalizes
`WorktreeView` rows at `ui/src/lib/api.ts:248-264,322-325`, while
`ui/src/lib/store.ts:40` consumes only `local.repo`.

**Impact.** Retired screens still impose client types, response normalization,
per-projection summary work, fixtures, and tests. The suite protects adjacent
index machinery no rendered path needs and makes the simplified two-screen UI
harder to change.

**Effort:** S. **Risk:** low. **Confidence:** high.

**Bounded fix.** Remove the unused graph client and types, summary computation,
wrapper exports, worktree-row normalization, and tests that exist solely for
them. Keep the server's `/v0/graph` and `/v0/worktrees` contracts outside this
UI-only child. This preserves the architecture contracts.

### 6. Make command-surface metadata runtime-owned

**Evidence.** `internal/docset/surface.go:28-31` explains the independence
property the documentation gate protects: the command surface is read from
the implementation rather than from a hand-maintained list that could be
missed by the same edit that forgot the documentation. CLI extraction begins
at line 37 and the 461-line file depends on particular switch, helper,
closure, and literal shapes. The CLI has a hand-written dispatch switch at
`cmd/gs/main.go:75-114` and repeats the command names in its usage string at
lines 122-124. MCP has a hand-written tool list at
`cmd/gitseq-mcp/main.go:543-595` and repeats the names in dispatch from line
692.

**Impact.** A surface change requires lockstep runtime, help/schema,
documentation, and extractor edits. A behaviour-preserving refactor can break
the documentation gate because its miniature Go interpreter no longer
recognizes the source shape.

**Effort:** L (several days). **Risk:** medium. **Confidence:** high.

**Bounded fix.** Introduce typed layer-7 command and tool descriptors that own
name, flags or schema, and handler together. Derive runtime dispatch and help
from those registries, then expose the same runtime-owned data as a stable
manifest to the documentation gate. Because the registry drives behaviour,
the manifest is not a separately maintained self-report and preserves the
gate's independence property. Delete the source-shape extractor only after
parity tests cover the migration. Add the new surface-metadata ownership to
the architecture boundary table and update the layer-7 section in the same
head. No kernel or Workroom package should learn CLI or MCP concepts.

### 7. Finish the citation migration and delete its exception subsystem

**Evidence.** `internal/docset/citation_baseline.txt:14-17` still contains four
branch-name citations across five pages. The reason field in each data row
says the cited artifact can never flare. `BaselineEntry`, `CompareBaseline`,
and `ParseBaseline` at `internal/docset/docset.go:374-461`, the gate and
caller at `internal/docset/anchor_test.go:62-94`,
`internal/docset/baseline_test.go`, and the baseline cases in
`internal/docset/citation_test.go:121-190` exist to maintain those four
exceptions.

**Impact.** Four known documentation defects sustain about 260 lines of
parsing, comparison, generation, and tests, while five pages remain anchored
to names that normal path succession cannot update.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Re-check the five pages against current behaviour and
re-anchor them to live artifacts at exact stable paths through ordinary Gitseq
documentation work. When the four rows reach zero, delete the baseline
parser, comparator, generator, gate-specific caller, and their tests, and make
the citation gate reject every fatal citation directly. This preserves the
architecture contracts.

## Comparison with the 2026-08-10 review

This comparison is partial: no canonical 2026-08-10 review document was found
in the repository, so it names only themes that could be reconstructed and
re-checked from the durable record and current tree.

- Roster convergence is resolved. `internal/app/app.go:501` delegates actor
  kind classification to `workroom.IsActorKind`, and line 673 uses the same
  vocabulary for authority roles.
- Cache and checkpoint ownership are unified. `internal/app/app.go:1243`
  constructs the one kernel reader from host-owned checkpoint options, while
  `internal/kernel/checkpoint.go:47` keeps projection identity outside the
  kernel checkpoint.
- Resident HTTP mechanism convergence is resolved: `cmd/gs`,
  `cmd/gitseq-github`, and `cmd/gitseq-mcp` use `internal/residentclient`.
  Finding 3 is later semantic creep across that successful boundary.
- The UI screen reduction is merged. Finding 5 is the smaller set of client
  and test remnants it left behind.
- TypeScript/Go wire parity now has a bounded gate at
  `internal/wireparity/parity_test.go:1-57`. It checks field-name parity for
  six explicitly selected Workroom wire types; that closes the earlier absence
  claim without pretending to cover every type or semantic constraint.
- Kernel scan-path Git process amplification is resolved. Cold scans use one
  `RevList` and one long-lived `OpenAuditBatch`
  (`internal/kernel/kernel.go:1018,1037,1051-1089`), while incremental scans
  stream through one metadata process (`internal/kernel/kernel.go:1173` and
  `internal/gitstore/gitstore.go:454-503`).
- CI deduplication is implemented and repaired at `073d68c7`, but is not an
  ancestor of the surveyed main and still awaits an effective exact-head
  verdict and merge. Finding 4 records that current state.
- The generated-embed merge gap also remains. `ui-check` can diagnose it, but
  nothing runs that check between Git's tentative merge and the sealed merge
  commit.

## Considered but not advanced

- Production-dead Go declarations remain. `SelectAdmissionProfile` and
  `ResolveAdmissionProfile` in `internal/workroom/admission_profile.go` have
  no production caller outside their internal pair, while `Rotate` and
  `VerifyContinuation` in `internal/kernel/kernel.go` have no non-test
  caller. Their exported or security-sensitive contracts require a focused
  compatibility review before deletion; finding 5 does not imply the Go side
  is clean.
- The aliases and forwarding helpers in `cmd/gitseq-mcp/digest.go:15-33,56-57`
  include several production-dead names. Their deletion is safe but too small
  to rank beside the seven ownership findings; it can ride with nearby MCP
  cleanup.
- Focus normalization repeats at `ui/src/lib/session.ts:68-78` and
  `ui/src/lib/interaction.ts:228-268`. One helper would be clearer, but the
  current expressions are pure, bounded, and identical; it is a suitable
  follow-up inside finding 5 rather than a separate request.
- `internal/perfscenario/fixture_writer.go` deliberately duplicates low-level
  construction so 500,000-event fixture setup stays bounded. Replacing it with
  production submission would invalidate the benchmark.
- `internal/statusview` deliberately repeats some projection scans instead of
  building event-sized indexes for capped twenty-row responses.
- The rebuild test gates in `internal/app` are used by deterministic
  cross-package concurrency tests; removing them would weaken atomic-publication
  evidence.
- `internal/custody` and `host/identity` are not dead. The architecture
  assigns them the second-application interpreter and public
  outside-application API, respectively.
- The repository-specific Markdown citation guard in `internal/app` is
  documented policy coupling shared by every authoring surface, not accidental
  layer drift.
- Large files were not findings by size alone. Splitting a cohesive file
  without deleting duplicated ownership would add package ceremony.
