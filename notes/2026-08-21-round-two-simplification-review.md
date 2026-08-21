# Round-two codebase simplification review

This review describes `main` at exact commit
`921259571352f8a62a2276c29a28e6c2ad2169d0`. It covers `cmd`, `internal`,
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

## Ranked findings

### 1. Verify generated assets before a merge is sealed

**Evidence.** `cmd/gs/main.go:523-547` creates a tentative merge, plans
succession, and commits it without a repository verification seam. The
purpose-built `ui-check` target at `Makefile:26-33` rebuilds and compares the
embedded UI, but `.github/workflows/ci.yml:53-56` calls `make ui` instead and
only the generic final diff at lines 79-80 detects a mismatch. Commit
`88b51864` is the concrete failure: a merge left the committed browser bundle
matching neither merged source nor either parent.

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
spending the approval.

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
remains authoritative when the frontier advances after preflight.

### 3. Restore `residentclient` to a transport-only boundary

**Evidence.** The package contract at `internal/residentclient/client.go:1-3`
says commands choose when to use a resident while this package owns safe URLs
and byte transport. It nevertheless imports `internal/app`, `internal/kernel`,
and `internal/workroom` at lines 21-23. `Submit` at lines 302-313 chooses local
application acceptance versus HTTP submission, and `UndefinedKindWarning` at
lines 316-323 opens and interprets the Workroom vocabulary. The architecture
table assigns deliberate kernel/Workroom coupling to `internal/app` at
`docs/reference/architecture.md:617` and says new application meaning stays
above the lower boundaries at lines 627-635.

**Impact.** The successful HTTP-mechanism convergence has acquired a second
application coupling point. Workroom vocabulary, application snapshot, kernel
request, and local-fallback changes now ripple into a package whose reusable
job is bounded loopback transport.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Keep URL validation, bounded HTTP, response classification,
and a transport-neutral liveness probe in `residentclient`. Move submission
routing and undefined-kind interpretation into `internal/app` or the composing
commands. Preserve the asymmetric liveness rule and loopback-only SSRF guard.

### 4. Execute each Go test result once in CI

**Evidence.** `.github/workflows/ci.yml:46-56` runs `make test`, `make race`,
and the UI checks. `Makefile:5-13` shows that the first two targets exercise the
same Go package set, once plain and once under the race detector.
`spike/cmd/gitseq-report/main.go:217-235` launches another uncached `go test`
for each package containing an adversarial case, and
`.github/scripts/verify-preview-clone.sh:26-28` runs the complete plain suite
again in the fresh clone. The earlier single-stream implementation at
`e82d9fff` is not an ancestor of the surveyed head.

**Impact.** Every CI run executes all Go packages three times, plus a fourth
execution of the adversarial subset. This lengthens feedback and makes four
test invocations responsible for overlapping evidence.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Port the proven `e82d9fff` shape onto current main: produce
one uncached `go test -race -json ./...` stream, feed it to the adversarial
evidence projector, and make the preview clone prove vet and build without
rerunning tests. Keep `make test` as the fast local target.

### 5. Delete UI paths preserved only by their tests

**Evidence.** `GraphCommit` and `api.graph` at
`ui/src/lib/api.ts:237-246,310-321` have no production caller. The sole
production consumer of `buildThreadIndex` asks only for `.content` at
`ui/src/lib/spine.ts:61-66`, but `ui/src/lib/threads.ts:9-52` still constructs
a summary API and computes every summary. `threadChildren` at lines 72-74 is
reachable only through a barrel export and tests. The worktree client retains
and normalizes `WorktreeView` rows at `ui/src/lib/api.ts:248-264,322-325`,
while `ui/src/lib/store.ts:40` consumes only `local.repo`.

**Impact.** Retired screens still impose client types, response normalization,
per-projection summary work, fixtures, and tests. The suite protects behaviour
no rendered path can reach and makes the simplified two-screen UI harder to
change.

**Effort:** S. **Risk:** low. **Confidence:** high.

**Bounded fix.** Remove the unused graph client and types, summary computation,
wrapper exports, worktree-row normalization, and tests that exist solely for
them. Keep the server's `/v0/graph` and `/v0/worktrees` contracts outside this
UI-only child.

### 6. Make command-surface metadata runtime-owned

**Evidence.** `internal/docset/surface.go:28-31` explains that the documentation
gate parses implementation source because neither delivery surface exposes
importable metadata. CLI extraction begins at line 37 and the 461-line file
depends on particular switch, helper, closure, and literal shapes. The CLI has
a hand-written dispatch switch at `cmd/gs/main.go:75-114` and repeats the
command names in its usage string at lines 122-124. MCP has a hand-written tool
list at `cmd/gitseq-mcp/main.go:543-595` and repeats the names in dispatch from
line 692.

**Impact.** A surface change requires lockstep runtime, help/schema,
documentation, and extractor edits. A behaviour-preserving refactor can break
the documentation gate because its miniature Go interpreter no longer
recognizes the source shape.

**Effort:** L (several days). **Risk:** medium. **Confidence:** high.

**Bounded fix.** Introduce typed layer-7 command and tool descriptors that own
name, flags or schema, and handler together. Derive dispatch and help from the
registries, expose a stable manifest to the documentation gate, and delete the
source-shape extractor after parity tests cover the migration. No kernel or
Workroom package should learn CLI or MCP concepts.

### 7. Finish the citation migration and delete its exception subsystem

**Evidence.** `internal/docset/citation_baseline.txt:14-17` still contains four
branch-name citations across five pages. Its own header says these artifacts
can never flare. `BaselineEntry`, `CompareBaseline`, and `ParseBaseline` at
`internal/docset/docset.go:374-461`, together with
`internal/docset/baseline_test.go` and the baseline cases in
`internal/docset/citation_test.go`, exist to maintain those four exceptions.

**Impact.** Four known documentation defects sustain about two hundred lines
of parsing, comparison, generation, and tests, while five pages remain anchored
to names that normal path succession cannot update.

**Effort:** M. **Risk:** medium. **Confidence:** high.

**Bounded fix.** Re-check the five pages against current behaviour and re-anchor
them to live artifacts at exact stable paths through ordinary Gitseq
documentation work. When the four rows reach zero, delete the baseline
parser/comparator/generator and make the citation gate reject every fatal
citation directly.

## Comparison with the 2026-08-10 review

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
- CI deduplication does not survive in the surveyed history. The prior
  implementation exists at `e82d9fff` but is not an ancestor of the survey
  head, so finding 4 remains.
- The generated-embed merge gap also remains. `ui-check` can diagnose it, but
  nothing runs that check between Git's tentative merge and the sealed merge
  commit.

## Considered but not advanced

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
- `internal/custody` and `host/identity` are not dead. The architecture assigns
  them the second-application interpreter and public outside-application API,
  respectively.
- The repository-specific Markdown citation guard in `internal/app` is
  documented policy coupling shared by every authoring surface, not accidental
  layer drift.
- Large files were not findings by size alone. Splitting a cohesive file
  without deleting duplicated ownership would add package ceremony.
