# Governing tests by gitseq: the "changed assumptions" flare

2026-08-18. **Status: draft — future direction, not queued for implementation.**
Citations re-verified against main at `5a9529b3` on 2026-08-19. Captured while
thinking about the chess design. The idea is to let gitseq's provenance flare —
the signal that fires when a basis is deliberately superseded — apply to
*tests*, so that a test which still passes can be marked *potentially stale in
intent* when the out-of-unit behavior it rests on was changed on purpose.

## The idea

A test is a claim about behavior. gitseq's core move is to rest a claim on the
artifact for the behavior it depends on, so that superseding the behavior flares
the claim: "the world under you moved — re-check." Apply that to tests: rest a
test (as an artifact) on the artifacts for the code it exercises, and a
deliberate supersession of that code flares the test.

The signal is **orthogonal to pass/fail**. CI answers "does this test pass
against current code?" It cannot answer "is this still-passing test still
*meaningful*?" — a test that stays green while the behavior it was written to
pin was intentionally changed, or that has quietly stopped exercising the seam
it claims to cover. That gap — **intent drift under green** — is what the flare
fills, and only that. It is a *review prompt*, not a gate.

## Where it pays off, and where it does not

For an ordinary unit test, the test and the unit live in one package and move
together; resting the test on its own package's artifact adds a flare that
mostly fires on the same change that already updated the test. Little gained.

The value is in the **other** tests: the ones whose subject is a call graph
*outside* the test's own unit — "sociable" tests in Fowler's taxonomy (after Jay
Fields), integration and component tests, and coverage-intent tests that pin a
seam owned elsewhere. There a deliberate change in a *different* file can leave
the test green while silently changing what it means. That is the population
worth governing.

No single software-engineering tradition names this. The adjacent ones each
supply a piece and then aim it elsewhere:

- **Regression Test Selection / Test Impact Analysis** (Rothermel & Harrold;
  Ekstazi; STARTS; Google/Microsoft TIA) computes exactly the test→out-of-unit
  dependency edges we want — but uses them to decide which tests to *re-run*,
  and re-running answers pass/fail, the correctness oracle CI already owns. RTS
  equates "affected" with "must execute"; to it, a passing affected test is a
  closed case.
- **Mutation testing** (DeMillo/Lipton/Sayward; Jia & Harman) is the rigorous
  form of "green proves nothing about coverage" — but it is a measurement you
  run at a point in time, not a provenance signal that fires when a dependency
  is deliberately superseded.
- **Change impact analysis** (Bohner & Arnold) is the general "when X changes,
  what should a human re-examine" framing, and **traceability** (DO-178C, ISO
  26262) keeps test→subject edges — but both are usually aimed at requirements
  and correctness, not at test *intent*.

The un-named axis is **affected → re-judge**, as distinct from **affected →
re-run**. gitseq can express it because it already separates *staleness*
(provenance: a basis was deliberately superseded, advisory) from *failure* (the
fold refuses). Most test frameworks have no vocabulary for a claim that is
simultaneously *green* and *stale*. Working names for the idea:
**provenance-based test staleness**, or **intent-impact analysis**.

## Concrete illustrations from this codebase

The selection rule: a test whose subject-assumption is owned by *another file*,
where a deliberate change there leaves the test green. Two of the four below
were live on the board the day this note was written, which is the strongest
part of the argument — the flare would have fired on real, recent, deliberate
changes. A third example has since moved too, and checking it turned up the
sharpest distinction in this note — between a stale *recipe* and lost
*coverage*; it is recorded inside the example it belongs to, not added as a new
one.

### 1. `public_surface_test.go` — purest cross-boundary (fired the wrong way)

`TestPublicRepositorySurface` (public_surface_test.go:19–115) asserts that
`README.md`, `docs/getting-started.md`, `SECURITY.md`,
`.github/workflows/ci.yml` and `.github/scripts/verify-preview-clone.sh`
contain specific fragments, and that the first two do *not* contain others. Its
entire subject is other files' content; it rests wholly on assumptions it does
not own.

Live: the disclosure policy changed — vulnerability reports moved off GitHub's
private advisory channel to the maintainer directly (`27ad16c2`) — and the
`strings.Contains` fixtures that pinned the old wording went **red**. That is
the *brittle* failure mode: the match breaks on any reword and cannot
distinguish "the policy changed" from "a typo was fixed." The repair took two
commits — `27ad16c2` restated the policy and re-pinned it, then `a1680d71`
generalized the fixture to an email-shaped regexp and a whitespace-normalized
compare so the check survives a reword. Today the test carries both a required
list ("Report vulnerabilities privately and directly to the maintainer") and a
forbidden list (`security/advisories/new`, "private advisory channel", any
email address).

The provenance version rests the test on the `SECURITY.md` artifact and flares
**advisory** — "the surface you pin was deliberately superseded; re-derive your
required fragments" — instead of forcing a red-CI scramble for what was really a
*re-judge the policy* event. This is "affected → re-run" (today) versus
"affected → re-judge" (the flare), caught in the act.

### 2. `BenchmarkCheckpointRestartAtDepth20000` — coverage-intent (the contract went, the test stayed)

internal/kernel/kernel_test.go:1042–1084. Intent: "at a realistic depth, restart
reuses the authenticated prefix with a bounded delta." It rests on assumptions
owned by `internal/kernel/checkpoint.go`: the hardcoded `20000` assumes
`checkpointInterval = 256` (checkpoint.go:30) so that 20000 is "many checkpoint
cycles"; line 1081 asserts `Verification.Events == 20000` and `fullScans == 0`
(full prefix reuse); and, as written, it drove everything through
`CheckpointProfile: "benchmark-fold@1"`, assuming **Profile gates checkpoint
eligibility.**

That last assumption is now gone. The schema/fold split (see
[`2026-08-18-schema-fold-split.md`](2026-08-18-schema-fold-split.md)) merged as
`394fcd56` and deliberately decoupled the kernel checkpoint from `Profile`,
which now "exists only to decode checkpoint@1 and checkpoint@2" and is refused
in a current kernel checkpoint (checkpoint.go:67–69, 266–267).

The prediction here was that the benchmark would stay green while quietly
losing its subject. What actually happened is more interesting, and worth
recording as the honest outcome rather than smoothing over. The Profile
coupling was a *struct field*, so the split could not leave it alone: the same
merge rewrote the benchmark's `Options{… CheckpointProfile: "benchmark-fold@1"}`
to `Options{… CheckpointEnabled: true}` and its
`CheckpointOptions{Profile: options.CheckpointProfile, …}` to
`CheckpointOptions{Enabled: true, …}`. The compiler forced the edit and green
was restored by making it.

The intent still drifted, and nothing asked about it. The benchmark was written
to characterize restart under a policy where a fold profile decided checkpoint
eligibility; that policy no longer exists, the edit that kept it compiling was
mechanical, and no step in the process posed the question the flare poses —
"your depth constant, your `fullScans == 0` and your eligibility assumption
were written against the old policy; are they still the right things to
assert?" So the example survives with its lesson sharpened: a compile-visible
coupling gets a mechanical rewrite, which restores green without restoring
meaning. Compile-invisible couplings — the next two examples — do not even get
that much.

### 3. `TestVerifierRejectsWrongSequencerSignatureOnColdAudit` — the seam moved, the coverage held

internal/kernel/kernel_test.go:648. It appends a commit signed with the *wrong*
sequencer key and asserts `Verify(...)` errors with a message containing
"sequencer signature." Per the kernel mutation matrix
([`2026-08-08-kernel-mutation-matrix.md`](2026-08-08-kernel-mutation-matrix.md)),
its guard-intent is "catch a mutation that ignores the `VerifySSHCommit` error
in `scanHead`." It rests on two out-of-unit assumptions in `kernel.go`: that
cold-audit verification happens *at that seam*, and that the error string still
contains the substring it matches on.

The seam has since moved. `scanHead` (kernel.go:876) no longer calls
`store.VerifySSHCommit` at all; it calls `gitstore.VerifySSHSignature` on an
already-audited commit (kernel.go:930) and wraps the failure as
`"commit %s sequencer signature: %w"`. The substring survived the move, so the
test is green and CI has had nothing to say.

The tempting conclusion is that the test has gone toothless. It has not, and
checking rather than assuming is what makes this the most useful example in the
note. Applying the equivalent current mutation — ignore the
`gitstore.VerifySSHSignature` error at kernel.go:930 — makes the test **fail**,
at kernel_test.go:656, with `error = nil`. The guard still bites at the moved
seam and still enforces wrong-signer rejection. Behavioral coverage is intact.

What went stale is the *recorded mutation recipe*, not the coverage. The matrix
still names "ignore the `VerifySSHCommit` error in `scanHead`", and that
sentence can no longer be applied as written, because the call it names is not
there any more. Someone reaching for the matrix to re-check this guard has to
re-derive the mutation before they can run it.

Those are two different kinds of staleness, and separating them is the point:

- **The claim** (this test rejects a wrong sequencer signature on cold audit)
  is still true, still enforced, and needs nothing.
- **The recipe** (the named mutation that proves the claim is enforced) refers
  to a call site that no longer exists, and needs re-deriving.

The flare belongs on the second, and that refines the idea rather than
confirming it: what needed re-judging here was not the test but the *record of
how to test the test*. Rest the matrix entry on the verification-seam artifact,
and moving that seam says "the mutation you recorded names a call that moved;
re-derive it and re-run." That is a small, bounded, unambiguous action — and,
run here, it returned a clean confirmation rather than a defect. Which is the
honest measure of what this signal is worth: not that it catches broken tests
CI missed, but that it tells you *which* of a hundred recorded assumptions is
worth spending five minutes re-checking, instead of leaving that judgement to
whoever happens to read the file next. The string coupling remains the sharp
edge underneath — reword away from the substring and the test goes red for no
semantic reason — but on this occasion the coupling held and the guard held
with it.

### 4. The merge-gate contract — semantic-contract class

The forgiving-staleness change (merge `23c4c759`) narrowed the merge gate from
"refuse any stale approval" to "refuse only world-stale." Tests asserting the
old rule go red (loud, fine). But a test that only exercises the *unchanged*
branch — "a fresh approval merges" — stays green while its sibling assumption
("stale ⇒ refused") silently no longer holds, so it now under-characterizes the
real gate. Rest such tests on the staleness-semantics artifact (`workroom` fold
+ `validateMerge` at cmd/gs/main.go:860) and superseding that semantics flares
them: "the contract you were written against changed; re-derive what you
assert."

## The hard part

Granularity is the whole game. Precise per-behavior edges give signal; coarse
per-package edges flare every test on every change, and a flare that carries no
information trains people to ignore the flares that do — the same mutex hazard
`AGENTS.md` warns about for the `.`-path artifact. Tests also have an execution
oracle CI already uses, so the marginal value is only the intent-drift slice —
which is why this is for a curated set of contract / coverage-intent /
cross-boundary tests, not the general suite.

Automation would help it scale: derive the test→code edges from the call graph
rather than hand-declaring them. But gitseq keys artifacts by exact path, which
pushes toward coarse (package-level) edges, and coarse brings back the noise.
Fine-grained-and-automatic is the unsolved part, and the thing that would decide
whether this is ever worth more than a curated pilot.

## If pursued

Start with the mutation-matrix tests as the pilot: small, high-value, and each
flare has an unambiguous action (re-run the mutation). The two live examples
above are the argument to keep: `public_surface` shows today's tools too loud
(red for a policy reword), the checkpoint benchmark shows them too quiet (green
restored by a mechanical edit after a deliberate contract change, with nobody
asked whether the assertion still characterizes anything). The flare is the one
signal that fits both — advisory where red is too loud, present where green is
too quiet.

The verifier-seam case sets the expectation for what a pilot would actually
feel like. Its seam moved, its recorded mutation stopped being applicable, and
the re-check it prompts came back clean: the guard still bites. Most flares
will end that way. A signal whose usual answer is "still fine" is worth having
only if it is cheap and specific, which is the argument for starting with the
mutation matrix — a small set where each flare names one recipe to re-derive
and one mutation to re-run — and against ever pointing this at the general
suite.
