---
title: Performance evidence
summary: Measured dependency fan-out, append cost and resident-memory evidence.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f0047ba0e5d25ad1f9620bf1428a651f37e1a302
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:86288a0f149fa39592758bc97ab422b994f2dcb8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f1569302953f2b46ed91f78414538b5b80454768
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f3a67e0c4d3a06c97c1bf8fa08250af6a77e3977
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9736b0cd2d853282ebb5c6f2993a160daad26238
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b99410af38eab88094ff208ff668f8b557021461
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1303d36457ad43404647dcf18fdbc729bb19931e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66fbe1aba3d99c83b3844dd85a19babd205ddd97
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fe48f17b6128732fcc5f072bc128fe72541d5c40
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
an error and invalidates the sample; equality is a precondition for admitting
a sample, not evidence that the fold is correct.

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

The run records both `harness_commit` and `candidate_commit` as
`f71b4d73c46b5df1ba2c755571fc8ffdf4455275`: the harness and the measured
subject are the same commit. It also records contract digest
`b0795bc71c9485210a842decce2fc932627a88072bbd3494a22098fcf66c7d45`, fixture
head `4fad5f2940e43d4436d5fbe84fb5973271e606da`, and fixture exact digest
`d15f714d71d27b391bf63199c8ac9baaee81ddd032a2069738059c9b226b869e` — one
fixture across all samples. It ran on Darwin arm64, an 18-core Apple M5 Max
with 64 GiB memory, Go 1.26.5, and Git 2.50.1, against a clean worktree. The
commit that publishes this page is a descendant of the measured commit,
because the retained files did not exist when the run started.

The run produced 505 records: 100 primary samples at each width, plus one
round-zero diagnostic per width that never enters a distribution. All 500
primary samples had equal projected and trusted correctness digests.

Everything below is derived from the raw samples. They are tracked, so the
figures on this page can be checked rather than taken on trust:

| File | SHA-256 |
|---|---|
| [`evidence.json`](../../performance/retained/fanout-20260819-f71b4d73/evidence.json) | `61a53e96c9bf31da07d7e37722d5bd2225f4f5ea9dad5a8bbf2e30acf9254a7b` |
| [`samples.jsonl`](../../performance/retained/fanout-20260819-f71b4d73/samples.jsonl) | `21c74a93ff7ec928297b4fb25196244d1c61529b60b8028abe27f3d1f8ff3209` |
| [`candidate.bench`](../../performance/retained/fanout-20260819-f71b4d73/candidate.bench) | `f5078a0e1263305c3615359c7f558c9ed12df414c3b5fe4da74afd28aa9e93e6` |

`samples.jsonl` holds every raw record. `evidence.json` is the harness evidence
document, and it retains the harness's own latency distributions and fan-out
axis summary as well as the contract, environment and digests.
`candidate.bench` is the 500 primary samples in Go benchmark format. No
separate derived file is retained: the robustness calculations this page makes,
which the harness does not compute, are recomputable from `samples.jsonl`.

| Width | p50 | p95 | p99 | Maximum |
|---:|---:|---:|---:|---:|
| 1 | 471.761 ms | 1,674.197 ms | 2,924.528 ms | 3,192.573 ms |
| 8 | 464.469 ms | 507.728 ms | 558.976 ms | 598.181 ms |
| 16 | 439.562 ms | 457.319 ms | 481.686 ms | 483.073 ms |
| 64 | 443.188 ms | 475.025 ms | 488.418 ms | 498.886 ms |
| 256 | 442.514 ms | 457.511 ms | 462.791 ms | 465.051 ms |

Width one's tail is conspicuous. The next section reports what is known about
it, and what is not.

### Cost per width

Against the harness p50 of the whole width-one block, 471.761 ms:

| Width | p50 | Absolute increment | Relative increment |
|---:|---:|---:|---:|
| 1 | 471.761 ms | 0 ms | 0% |
| 8 | 464.469 ms | -7.292 ms | -1.546% |
| 16 | 439.562 ms | -32.199 ms | -6.825% |
| 64 | 443.188 ms | -28.573 ms | -6.057% |
| 256 | 442.514 ms | -29.246 ms | -6.199% |

No value is clamped, no magnitude is taken, and no sample is paired across
cases. Every width from 8 upward sits below the one-base median, so the
measured incremental cost of extra causal bases is not positive at any width
this run reached.

Between adjacent widths the only increase is 16 to 64, at **+3.626 ms**. The
other steps fall: 1 to 8 by -7.292 ms, 8 to 16 by -24.907 ms, and 64 to 256 by
-0.673 ms. The largest step in absolute terms is therefore the fall from 8 to
16, not the rise from 16 to 64.

### Width one moved during the run, and what follows from it

Two facts about how this run was conducted belong here, because neither can be
recovered from the samples.

First, it is a second attempt. An earlier invocation of the same tier was
suspended by machine sleep 48 samples into the width-one block. Sample records
carry no wall-clock timestamp, so a suspend that a run spans cannot be found
afterwards in the data; repairing one width and keeping the rest would have
meant trusting samples that might have straddled the gap. That run was
discarded whole and the tier rerun as a single invocation, with sleep
inhibited. No sample here comes from it.

Second, the quiet window did not hold. The run waited for a quiet machine and
got one for its first sixty seconds. Then a sibling lane started two test
processes; machine load climbed from 4.08 to a peak of 14.53 and stayed high
for about three minutes before clearing. Width one is the block that was running
during that window.

Width one has 36 samples above 600 ms, all in rounds 46 to 93. No other width
has a single one; the largest sample anywhere outside width one is 598.181 ms,
at width 8. Splitting each block into four consecutive quarters of
25 samples and taking the median of each shows how much each block moved
within itself:

| Width | Rounds 1–25 | 26–50 | 51–75 | 76–100 | Range, as % of that width's p50 |
|---:|---:|---:|---:|---:|---:|
| 1 | 448.474 ms | 463.870 ms | 622.700 ms | 735.251 ms | 60.789% |
| 8 | 496.527 ms | 490.666 ms | 456.593 ms | 440.960 ms | 11.964% |
| 16 | 438.284 ms | 437.842 ms | 440.368 ms | 443.047 ms | 1.184% |
| 64 | 441.718 ms | 450.787 ms | 442.804 ms | 440.404 ms | 2.343% |
| 256 | 442.162 ms | 443.913 ms | 442.939 ms | 440.548 ms | 0.760% |

Width one climbs across its four quarters. Width eight, which runs next, falls
across its own. Widths 16, 64 and 256 stay within 2.4%.

What caused the two moving blocks to move is not something this run can settle.
Machine load was observed only at twenty-second intervals, and the latency
records carry no wall-clock timestamp, so no sample can be aligned to the
machine's state when it was taken. The quarter table is reported as what was
observed, and the attribution is left open.

A block that moves this much within itself is the in-block variation the
governing decision said adjacency reduces but cannot remove. It matters because
width one is the sole denominator of every relative figure above. So the honest
question is not which denominator is right, but whether the verdict depends on
the choice. It does not:

| Width-one denominator | Value | Worst width | Worst relative | PREVIEW | FIRST-PRODUCTION |
|---|---:|---:|---:|---|---|
| Full block, p50 | 471.761 ms | 8 | -1.546% | pass | pass |
| Full block, simple median | 474.324 ms | 8 | -2.078% | pass | pass |
| Rounds 1–25, its lowest quarter | 448.474 ms | 8 | +3.566% | pass | pass |
| Rounds 76–100, its highest quarter | 735.251 ms | 8 | -36.829% | pass | pass |

Both envelopes pass on all four. The worst case anywhere is width 8 at
+3.566% against the lowest width-one quarter, well inside the 10-percent
limit.

Across the four tested denominators, the largest observed relative increment is
+3.566% at width 8. Both classifications pass, but the magnitudes remain load-
and order-sensitive.

### Contract verdict

The relative target permits no more than a 10-percent median increase at every
measured width, through 64 for PREVIEW and through 256 for FIRST-PRODUCTION.
Every clause of that contract is answered here, misses included.

| Clause | Result |
|---|---|
| PREVIEW: no width through 64 exceeds +10% | pass, on all four denominators; worst is width 8 at +3.566% |
| FIRST-PRODUCTION: no width through 256 exceeds +10% | pass, on all four denominators; width 256 is never the worst width |
| No intermediate nonlinear jump, even if the endpoint falls | pass; the only increase between adjacent widths is +3.626 ms from 16 to 64 |
| Consecutive block at the contract-selected depth, widths 1, 8, 16, 64, 256 | pass; one invocation, contract case order |
| Width one measured exactly once, not duplicated in the depth axis | pass |
| Five warmups and 100 recorded repetitions at every width | pass; 100 primary samples per width, diagnostics excluded |
| One-base median, signed ratio and signed millisecond increment for every width | pass |
| No clamping, no magnitudes, no cross-case pairing | pass |
| Trusted-versus-projected digest equality on every sample | pass, as a validity precondition; 500 of 500 equal |
| Raw samples, distributions, case order, environment and digests retained | pass; tracked under `performance/retained/` |
| Load disclosed, not assumed away | pass; the restart, the observed load range and the per-quarter movement of every block are set out above, with attribution left open |
| Fixed one-base append within the separate 50 ms budget | miss, by about nine times; parked request `8add6909`, see below |
| No cross-product claim at depth 50,000 or 500,000 | pass |

The fan-out dimension passes both release envelopes. The one miss is the
separate absolute append budget, which is not a fan-out result and is not
repaired by this page.

The contract treats the evidence as inconclusive if another complete
consecutive-axis run changes either classification. This is the second complete
campaign on this axis, and PREVIEW and FIRST-PRODUCTION were pass in both, so
neither classification moved and the rule did not trigger. The earlier
campaign's raw files no longer exist — they were written to untracked working
space and deleted with their worktree — which is why this run is tracked and
why none of its numbers appear on this page.

### The separate absolute target

The fixed cost of a one-base append has its own budget of 50 ms, and this run
misses it badly. The lowest one-base figure the run produced is the
first-quarter median of 448.474 ms, about nine times the budget; the
full-block p50 of 471.761 ms is worse. This is reported as evidence, not
argued away.

That target belongs to parked request `8add6909`, whose Planner report remains
unratified. Work on it is authorized separately, so this page neither un-parks
it nor claims it. The fan-out verdict does not depend on it and does not
improve it.

The two targets interact in one direction worth naming. A relative limit
tightens in milliseconds when the fixed one-base cost falls, and it loosens
when that cost rises. At this run's denominators the 10-percent allowance is
worth between 44.847 and 73.525 ms — on its own, at or above the entire 50 ms
absolute budget. A regression that inflated fixed and fan-out cost together
would leave the ratio unchanged and still pass. A passing fan-out ratio is
therefore never a guarantee about append latency overall.

### What this run does not establish

The axis measures one scale dimension independently at its contract-selected
depth of 1,000. A pass establishes the fan-out dimension at widths 64 and 256
at that depth. It is not an end-to-end result at depth 50,000 or 500,000, not
a cross-product of depth and fan-out, and not an overall latency pass. Any
such claim needs its own reviewed contract change and its own evidence.

## One-base append fixed cost

The one-base budget is measured separately from the fan-out verdict above. A
full alternating comparison started at `2026-08-19T21:16:08Z` with exact base
`12105a304e0ee0e66d9d3075a011364b40e24fc4` and measured candidate
`e08e36e2bbdf6f3d7ba104a20654a0f5aea84684`. The candidate computes the
actor-signed payload-tree identity in memory. Kernel admission remains the sole
durable writer and still reconstructs and checks that exact identity before
sequencer signing, signature verification and verified-ref compare-and-swap.

The comparison used the `fanout` tier so the fixed saving was checked across
the whole dependency-width axis: five warmups and 100 recorded samples per
revision at widths 1, 8, 16, 64 and 256. Base and candidate samples alternated
within each case. Setup stayed outside the measured acknowledgement. The run
recorded 1,000 primary samples plus five candidate diagnostics and completed
with harness outcome `pass`. That outcome means the run was internally valid;
it is not the verdict against the 50 ms target. Alternation limits temporal
drift between revisions, but the samples remain separate distributions; it
does not pair samples or cancel arbitrary machine load or interference.

| Fan-out | Base p50 | Candidate p50 | Change |
|---:|---:|---:|---:|
| 1 | 446.833 ms | 429.809 ms | -17.023 ms; -3.810% |
| 8 | 444.337 ms | 427.481 ms | -16.856 ms; -3.793% |
| 16 | 442.258 ms | 426.352 ms | -15.906 ms; -3.597% |
| 64 | 444.468 ms | 429.231 ms | -15.237 ms; -3.428% |
| 256 | 437.813 ms | 421.655 ms | -16.158 ms; -3.691% |

The candidate's PREVIEW-through-64 and FIRST-PRODUCTION-through-256 fan-out
verdicts both remain `pass`; the fixed-cost change did not trade the existing
relative fan-out result for its latency reduction.

At width one, the base p95, p99 and maximum were 461.827 ms, 488.626 ms and
495.595 ms. The candidate values were 455.927 ms, 470.668 ms and 472.557 ms.
The candidate p50 is still 8.60 times the 50 ms budget, so the absolute target
remains an honest **miss**. The change removes a measured fixed cost; it does
not claim to solve the much larger cold verification and publication costs.

A separate same-fixture Trace2 diagnostic explains the fixed reduction without
turning diagnostic latency into a distribution. The exact base started 21 Git
root processes and recorded 293.938 ms of cumulative Git-process duration. The
measured candidate started 19 and recorded 272.402 ms. The two removed
processes are the application-side `hash-object` and `mktree`; kernel admission
still performs the one authoritative payload-tree write. The retained
candidate diagnostics also report 19 Git root processes at every width.

The comparison used contract digest
`b0795bc71c9485210a842decce2fc932627a88072bbd3494a22098fcf66c7d45`,
fixture head `9dc8f7ae7251183f7f1f2ea8114fd5ed84ab1db0`, fixture logical digest
`7b3512ee2c7fb3ac95ce6dc01edec89b20194b6dd1f44eaec45a7ce1684a4159`,
and fixture exact digest
`24d7353240b0393d98a8da5a9b29c46bc19b75a6ff5f878d4be32c1d356d2932`.
It ran on Darwin arm64, an 18-core Apple M5 Max with 64 GiB memory, Go 1.26.5
and Git 2.50.1, from a clean worktree. Every primary sample had equal projected
and trusted correctness digests. The pinned `benchstat` tool was unavailable;
the table uses the harness's retained nearest-rank distributions directly.

| File | SHA-256 |
|---|---|
| [`evidence.json`](../../performance/retained/append-fixed-20260819-e08e36e2/evidence.json) | `cc2bbd4c4f4ff4c329f177ebbac98480412eea748f55dd5b3e727f37a96a24c8` |
| [`samples.jsonl`](../../performance/retained/append-fixed-20260819-e08e36e2/samples.jsonl) | `abfe3e5c010d3f0210cb780bfdad59e6c117200cd6af3f4aa6de0d9cffaf9e0e` |
| [`candidate.bench`](../../performance/retained/append-fixed-20260819-e08e36e2/candidate.bench) | `b1135c758bded12d0da4c2ab44e0b58a9c33568e7d52fe3027da4d0cfb058b75` |

The evidence document and raw sample file retain both exact revisions; the
benchmark-format file retains the 500 candidate primary samples. Base bench
output, profiles and traces are not retained. The publishing head is a
descendant of the measured candidate because this page, its precise measured
artifact basis and the retained evidence did not exist when sampling began.

## 500,000-record resident memory

The bounded memory tier measures one linear, one-actor `cold_status` rebuild at
every contract depth through 500,000 records:

```text
make perf PERF_ARGS='run --tier memory'
```

It runs two fresh-process primary samples per depth, with no warmup. Diagnostic
reruns start only after every primary sample and do not enter the ranges below.
The harness outcome `pass` means the run completed with valid fixtures and
matching correctness digests; the separate target verdict comes from comparing
peak resident memory with the 4 GiB FIRST-PRODUCTION envelope.

The measured harness and candidate were both exact commit
`08b7c72c7cf32ade5288093b0a9acb3833cf7bb0`. The run started at
`2026-08-20T05:37:06Z` on Darwin arm64, an 18-core Apple M5 Max with 64 GiB
memory, Go 1.26.5 and Git 2.50.1, from a clean worktree.

| Depth | Peak RSS range | Steady memory range | Cold-status latency range |
|---:|---:|---:|---:|
| 100 | 0.157 GiB | 0.067 GiB | 0.21 s |
| 1,000 | 0.158–0.159 GiB | 0.075 GiB | 0.48 s |
| 10,000 | 0.220 GiB | 0.100 GiB | 2.95–3.09 s |
| 100,000 | 0.607–0.630 GiB | 0.292 GiB | 27.01–27.15 s |
| 500,000 | 2.500–2.523 GiB | 1.246 GiB | 133.97–134.17 s |

The worst 500,000-record peak is 2,709,110,784 bytes. That is 36.9 percent
below 4 GiB, so the measured FIRST-PRODUCTION resident-memory target **passes**.
The range is observed, not extrapolated. All ten primary samples and all five
diagnostics had equal projected and independently folded trusted digests. The
500,000-record fixture head is
`5bdaab68803394118d82130bcfa14d15dcbc7ccf`; every sample records fixture exact
digest `062b953f5b460861c64a08eedb02245545c8f75ab0cf8f4b22bb1a1b80265999`.

| File | SHA-256 |
|---|---|
| [`evidence.json`](../../performance/retained/resident-memory-20260820-08b7c72c/evidence.json) | `e074957c17a2a29ad95f9514e23316bedb9a269d990aadacea6a75d298a59f52` |
| [`samples.jsonl`](../../performance/retained/resident-memory-20260820-08b7c72c/samples.jsonl) | `c50f5e585696acdcd4879e4a11a892d9eec7d6af0d41fefb4e984b83322aac65` |
| [`candidate.bench`](../../performance/retained/resident-memory-20260820-08b7c72c/candidate.bench) | `cdd3a75eac1984c79a0b323c1723f8ce1978da275bcfc1090604e4ef6721ec57` |

The kernel now verifies and transfers a full rebuild without retaining a second
depth-sized event slice. The application folds that provisional stream into a
private folder and publishes only after complete verification and folding.
The Workroom folder also shares repeated immutable identifiers, vocabulary and
state strings while preserving projection-mutation isolation. The projection
itself and the kernel's idempotency index still grow with the information they
must answer, so this is a measured bound for the named workload, not a claim of
constant memory.

After recording measured usage, the worker builds a separate trusted projection
to validate the digest. That later two-projection diagnostic is deliberately
outside `peak_rss_bytes` and `steady_memory_bytes`: a serving resident keeps one
verified application projection, while the harness keeps two only to check the
first one. The publishing commit is a descendant of the measured commit because
this page and the retained evidence did not exist when sampling began.

## Preserved contracts

The earlier fan-out lane changes measurement order and reporting only. This
fixed-cost lane changes request construction inside the Workroom application
profile: it computes the signed payload-tree identity without publishing the
tree before admission. It does not change the kernel contract or move write
authority out of the kernel.

The actor still signs the same target, schema, payload-tree identity, bases and
idempotency fields. The sequencer still admits the request, enforces bounds,
writes the payload tree once, checks that the written identity equals the
signed identity, signs and verifies the event, and advances the verified ref by
compare-and-swap. Signature failures, bound failures and CAS failures retain
their existing behaviour. Publication remains atomic, and the complete fold,
idempotency, projected-versus-trusted equality and application error semantics
are unchanged. The harness exercises that ordinary submit path and refuses a
sample when the two correctness digests differ.
