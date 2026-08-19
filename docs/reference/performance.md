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

## Preserved contracts

This lane changes measurement order and reporting only. It does not defer or
remove actor signing, sequencer admission, verified-ref compare-and-swap,
signature or bound verification, complete fold semantics, idempotency,
trusted-versus-projected equality, error behaviour, or atomic publication.
The harness exercises the ordinary submit path and fails the sample if the
trusted and projected digests differ.
