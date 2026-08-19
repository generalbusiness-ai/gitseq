---
title: Performance evidence
summary: The versioned dependency fan-out measurement and its contract verdict.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f0047ba0e5d25ad1f9620bf1428a651f37e1a302
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:86288a0f149fa39592758bc97ab422b994f2dcb8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4da5ebd075a0a28941371add116b564cf9f4f7de
---

# Performance evidence

Performance measurements are evidence from one named workload and machine,
not limits or service-level guarantees. [Limits](limits.md) records the sizes
and counts that Gitseq actually refuses.

## Dependency fan-out

The `gitseq.performance/v2` contract gives dependency fan-out one explicit
axis at depth 1,000. Widths 1, 8, 16, 64, and 256 run as five consecutive case
blocks. The width-one block is their temporal denominator; it is removed from
the ordinary depth axis rather than measured twice under two names.

The full fan-out population is bounded and opt-in:

```text
make perf PERF_ARGS='run --tier fanout'
```

Every width gets five warmups and 100 recorded repetitions. Primary samples
finish before Trace2 and profiling reruns, so diagnostic work cannot split the
five-block quiet window. Each sample retains the fixture identity, exact head,
environment, raw latency, and both correctness digests. A digest mismatch is
an error and invalidates the sample.

For width `f`, evidence reports two signed quantities beside the width-one
median:

- relative increment: `median(T_f) / median(T_1) - 1`;
- absolute increment: `median(T_f) - median(T_1)` in milliseconds.

Negative increments stay negative. The cases are separate distributions;
their adjacency does not make individual samples pairs and does not cancel an
arbitrary load change.

Fan-out is a relative dimension. This axis measures the cost one extra causal
base adds, not the total cost of an append. The absolute cost of an append is
a separate target with a separate verdict, recorded below.

### The measured run

The current run used clean harness and candidate commit `7b5026f09da1086a13b4ab3472f9b84863e83632`,
contract digest `b0795bc71c9485210a842decce2fc932627a88072bbd3494a22098fcf66c7d45`,
fixture head `154e6fa1e0d05f8e074308c2618d555a8f84a998`, and fixture exact digest
`c3846ff0cf593e3166737a34db4649b5544751c9f99c8461c941a058f7e399d4`.
It ran on Darwin arm64, an 18-core Apple M5 Max with 64 GiB memory, Go 1.26.5,
and Git 2.50.1. All 500 primary samples had equal projected and trusted
correctness digests.

| Width | p50 | p95 | p99 | Maximum |
|---:|---:|---:|---:|---:|
| 1 | 497.331 ms | 1,106.639 ms | 3,615.586 ms | 6,077.966 ms |
| 8 | 451.447 ms | 469.212 ms | 483.286 ms | 486.542 ms |
| 16 | 445.251 ms | 458.616 ms | 472.200 ms | 473.492 ms |
| 64 | 445.613 ms | 460.090 ms | 482.367 ms | 488.723 ms |
| 256 | 444.809 ms | 450.182 ms | 477.759 ms | 492.071 ms |

### Cost per width

Each width is reported against two denominators, because the width-one block
did not hold still. The next section explains why. Read the last-quarter
column as the honest relative cost.

| Width | Median | ms vs full block | Relative vs full block | ms vs last quarter | Relative vs last quarter |
|---:|---:|---:|---:|---:|---:|
| 1 | 497.331 ms | 0 ms | 0% | +35.572 ms | +7.704% |
| 8 | 451.447 ms | -45.884 ms | -9.226% | -10.312 ms | -2.233% |
| 16 | 445.251 ms | -52.080 ms | -10.472% | -16.508 ms | -3.575% |
| 64 | 445.613 ms | -51.718 ms | -10.399% | -16.146 ms | -3.497% |
| 256 | 444.809 ms | -52.522 ms | -10.561% | -16.950 ms | -3.671% |

The full-block denominator is the width-one p50 of 497.331 ms over all 100 of
its recorded repetitions. The last-quarter denominator is the simple median of
461.759 ms over its final 25 repetitions. No value is clamped, no magnitude is
taken, and no sample is paired across cases.

### Why the run has two denominators

The width-one denominator was the run's only non-stationary block. Its simple
median fell from 497.471 ms across rounds 1–100 to 495.168 ms across rounds
18–100, 474.016 ms across rounds 51–100, and 461.759 ms across rounds 76–100.
Every sample above 600 ms occurred in its first 17 rounds, and no width above
one has any sample above 600 ms anywhere in the run. Five warmups therefore
did not settle the first measured block.

The consequence is that the full-block figures of -9.2 to -10.6 percent are
dominated by the width-one warm-up deficit, not by fan-out. Against width
one's own last-quarter median the later blocks sit between 2.233 and 3.671
percent below the denominator. Both release classifications survive either
way, but the signed magnitudes are bounded by this ordering and warm-up
artifact and must not be read as fan-out speedups.

The last-quarter comparison is the conservative one. It divides by the
smallest defensible denominator, so it reports the largest relative fan-out
cost the evidence supports. It also mixes statistics deliberately: the
numerators are the harness p50 over 100 repetitions and the denominator is a
simple median over 25. That mixture is disclosed rather than hidden, because
the alternative — the full-block denominator — is the one the run showed to be
unsettled.

### Contract verdict

The relative target permits no more than a 10-percent median increase at every
measured width, through 64 for PREVIEW and through 256 for FIRST-PRODUCTION.
Every clause of that contract is answered here, misses included.

| Clause | Result |
|---|---|
| PREVIEW: no width through 64 exceeds +10% | pass on both denominators; the worst width through 64 is width 8, at -2.233% against the last quarter and -9.226% against the full block |
| FIRST-PRODUCTION: no width through 256 exceeds +10% | pass on both denominators; width 256 adds nothing worse, at -3.671% and -10.561% |
| No intermediate nonlinear jump, even if the endpoint falls | pass; no width exceeds the limit, and the only increase between adjacent widths is 0.362 ms from 16 to 64 |
| Consecutive block at the contract-selected depth, widths 1, 8, 16, 64, 256 | pass |
| Width one measured exactly once, not duplicated in the depth axis | pass |
| Five warmups and 100 recorded repetitions at every width | pass as configured; the warmups did not settle width one, which is why two denominators are reported |
| One-base median, signed ratio and signed millisecond increment for every width | pass |
| No clamping, no magnitudes, no cross-case pairing | pass |
| Trusted-versus-projected digest equality on every sample | pass, as a validity precondition; equality admits the sample and is not evidence that the fold is correct |
| Raw samples, distributions, case order, environment and digests retained | miss on the raw samples; see the note on chain of custody below |
| Fixed one-base append within the separate 50 ms budget | miss, by about 9.2 to 9.9 times; parked request `8add6909`, see the next section |
| Denominator hazard disclosed rather than assumed away | pass |
| No cross-product claim at depth 50,000 or 500,000 | pass |

The fan-out dimension passes both release envelopes. The two misses are the
retention of the raw samples and the separate absolute append budget. Neither
is a fan-out result, and neither is repaired by this page.

### The separate absolute target

The fixed cost of a one-base append has its own budget of 50 ms, and this run
misses it badly. The width-one median is 497.331 ms over the full block and
461.759 ms over its last quarter, which is roughly 9.2 to 9.9 times the
budget. This is reported as evidence, not argued away.

That target belongs to parked request `8add6909`, whose Planner report remains
unratified. Work on it is now authorized separately, so this page neither
un-parks it nor claims it. The fan-out verdict above does not depend on it and
does not improve it.

The two targets interact in one direction worth naming. A relative limit
tightens in milliseconds when the fixed one-base cost falls, and it loosens
when that cost rises. At this run's denominators the 10-percent allowance is
worth 49.733 ms against the full block and 46.176 ms against the last quarter
— on its own, about the size of the entire 50 ms absolute budget. A regression
that inflated fixed and fan-out cost together would leave the ratio unchanged
and still pass. A passing fan-out ratio is therefore never a guarantee about
append latency overall.

### What this run does not establish

The axis measures one scale dimension independently at its contract-selected
depth of 1,000. A pass establishes the fan-out dimension at widths 64 and 256
at that depth. It is not an end-to-end result at depth 50,000 or 500,000, not
a cross-product of depth and fan-out, and not an overall latency pass. Any
such claim needs its own reviewed contract change and its own evidence.

### Evidence and chain of custody

The evidence files for this run have these SHA-256 digests:

- `evidence.json`: `47579f652d0b7ff30b6afb8469bd1a16a2eed7c463879ae46cc6fbe1acbd0c68`;
- `samples.jsonl`: `f7ec6a431026f4bcdfaf9f63da753294579f51baf09de6f0adc72c3a52386937`;
- `candidate.bench`: `ee5b332952f67ab5cdb02edcc6b701c24221ae1a4651514d0ff905aab0b2d972`.

All three digests, the 500-sample population, the per-width sample counts and
the four windowed width-one medians were independently recomputed from the raw
files during review of the lane that produced them, and that recomputation is
recorded durably. The contract digest above is reproducible today from
`performance/contract-v2.json` in this repository.

The raw files themselves are no longer available. `performance/.gitignore`
excludes `evidence/`, so they lived in the implementing worktree and were
removed with it after the merge. The digests still pin what was measured and
the recorded recomputation still stands, but a reader cannot re-derive these
distributions from this repository. Retaining the raw samples durably is
follow-up work, filed as request `0aa26097`.

If another complete consecutive-axis run changes either pass/fail
classification, the evidence is inconclusive and must be rerun under a quiet
window. The medians above do not replace the distributions.

## Preserved contracts

This lane changes measurement order and reporting only. It does not defer or
remove actor signing, sequencer admission, verified-ref compare-and-swap,
signature or bound verification, complete fold semantics, idempotency,
trusted-versus-projected equality, error behaviour, or atomic publication.
The harness exercises the ordinary submit path and fails the sample if the
trusted and projected digests differ.
