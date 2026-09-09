---
title: Performance evidence
summary: Measured dependency fan-out, append cost and resident-memory evidence.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2a097018a3e48d083684443824d3f864755be8f1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:94310c5a430a5f66bf0fd097c93900e1dc5715ec
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:56c0e84d7115c2695fb92a8715268d42aedba8f7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7d6f6997c01a89e509dec03f68fc6ba4fb4125fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c4f0d4142bfa057005b09e59bc0a3462980842b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:e0ffb053668e086b2121921df6c4a5820ff26bbb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6e088c6df49336e6df8c2e045c0bc3c0a3448af5
---

# Performance evidence

Performance measurements are evidence from one named workload and machine,
not limits or service-level guarantees. [Limits](limits.md) records the sizes
and counts that Gitseq actually refuses.

## Scale envelope targets

This section is version 1 of the scale envelope table, published 2026-09-09. A
later version replaces it whole at this path. It states what each envelope asks
for, where the number came from, and what has actually been measured. It commits
Gitseq to nothing.

Two envelopes are named. PREVIEW is 50,000 events with 8 actors.
FIRST-PRODUCTION is 500,000 events with 50 actors.

Every cell carries one of three tags:

- **planning target**: a number someone chose as the thing to aim at. It is not
  enforced anywhere, and missing it refuses nothing.
- **measured**: an observation from a named run on named hardware. It is
  evidence about that workload and that machine, and nothing more.
- **limit**: a bound the code actually enforces. Exceeding it is refused.
  [Limits](limits.md) is the page that governs these.

Most measured cells below come from the envelope campaign, and each of those is
the pair of primary samples that campaign recorded, reported in the residual
dimensions section further down. Two samples are not a distribution, so no
percentile is available for any of them. The fan-out cells and the one-actor
memory cells come instead from the lanes already published below. Each cell
carries its number and its tag and nothing else; where every number comes from
is set out once, below the table, rather than in a column beside it.

| Row | PREVIEW, 50,000 events and 8 actors | FIRST-PRODUCTION, 500,000 events and 50 actors |
|---|---|---|
| Sequence depth | 50,000, planning target | 500,000, planning target |
| Application-event count | 50,000, planning target | 500,000, planning target |
| Actor count | 8, planning target; joint-cell cold status 12.647 s and 12.659 s, measured | 50, planning target; joint-cell cold status 126.148 s and 126.471 s, measured |
| Dependency fan-out | width 64 at +10 percent median, planning target, machine-encoded; pass, worst +3.566 percent, measured | width 256, same limit, planning target, machine-encoded; pass, measured |
| Checkpoint size | 256 MiB serialized blob, limit; 2,161,188 bytes at tail 255, 0.81 percent of it, measured | 256 MiB, limit; 21,934,525 bytes at tail 255, 8.17 percent of it, measured |
| Cold verify, no checkpoint | 5 minutes, planning target; 12.979 s and 13.001 s, measured | 60 minutes, planning target; 129.259 s and 128.810 s, measured |
| Cold restart, tail 255 | 10 seconds, planning target; 7.243 s and 7.211 s, measured | 60 seconds, planning target; 69.417 s and 69.320 s, measured |
| Warm status | no adopted target; 17.589 ms and 18.155 ms, measured | no adopted target; 150.307 ms and 164.122 ms, measured |
| Memory, peak resident, at the envelope actor count | 1 GiB, planning target; 396,640,256 and 387,366,912 bytes, measured | 4 GiB, planning target; 3,547,348,992 and 3,053,731,840 bytes, measured |
| Memory, peak resident, one actor | 934,969,344 and 931,643,392 bytes, measured | 2,709,110,784 bytes worst in the memory lane and 3,098,345,472 and 3,126,509,568 bytes in this campaign, measured |

### The hardware

Every measured figure on this page was taken on one machine: an 18-core Apple
M5 Max with 64 GiB of memory, running Darwin arm64, with Git 2.50.1 (Apple
Git-155). The head and the Go toolchain differ by source, because the table
carries results from earlier lanes as well as from this campaign. Each source
states its own, and the retained `evidence.json` for each run carries the
`environment` field these lines come from:

| Source | Head | Go |
| --- | --- | --- |
| This campaign: cold verify, cold restart, warm status, checkpoint bytes, actor-count cost, and the memory figures marked "in this campaign" | `0b689fe27affcae3eba5c496eee4713b257dce40` | 1.27.0 |
| Dependency fan-out lane, carried | `f71b4d73c46b5df1ba2c755571fc8ffdf4455275` | 1.26.5 |
| Resident-memory lane, carried, including the 2,709,110,784-byte worst peak | `08b7c72c7cf32ade5288093b0a9acb3833cf7bb0` | 1.26.5 |
| Fixed-append lane, carried | `e08e36e2bbdf6f3d7ba104a20654a0f5aea84684` | 1.26.5 |
| Head-wait run, carried | its own before-and-after pair, recorded in [HEAD-WAIT.md](../../performance/HEAD-WAIT.md) | 1.27.0 |

This campaign ran as one invocation at its head. Machine load during its window
is reported with the samples. A different machine will produce different
seconds, and a different toolchain may too: no figure here is comparable across
two rows of that table.

### Why each target is what it is

- **Sequence depth.** 50,000 is the depth the contract's own checkpoint case
  already names below the top of the axis, and it is the depth at which the
  first cold-audit and resident-memory misses were argued. 500,000 is the top of
  the contract depth axis and the largest fixture the harness builds. They sit
  one decade apart so a cost shape shows between them.
- **Application-event count.** The synthetic workload writes one application
  event per record, so this row repeats the depth by construction. A real
  workroom also carries sequencer-key rotation commits, which raise depth
  without raising the application-event count.
- **Actor count.** 8 is a working room of people and agents. 50 is the largest
  value the contract's actor axis already names, so the envelope reuses an axis
  value rather than inventing a number.
- **Dependency fan-out.** 64 and 256 are the contract's own
  `preview_max_width` and `first_production_max_width`. The target is relative
  because this axis measures what one extra causal base costs against the
  one-base median, not what an append costs in total.
- **Checkpoint size.** 256 MiB is not a target anyone chose here. It is an
  enforced refusal, and it bounds the serialized blob because a reader loads the
  whole blob before it trusts any of it.
- **Cold verify.** A workroom with no usable checkpoint verifies everything, so
  this row is the worst restart a host can face. The 5 minute and 60 minute
  figures are the ones a retired request argued against; they were never
  adopted.
- **Cold restart.** A near-head restart is the ordinary cost a running resident
  pays. Tail 255 is the largest tail a successful checkpoint leaves, which
  follows from the 256-event refresh cadence in [Limits](limits.md), so it is
  the tail worth measuring. The 10 second and 60 second figures come from a
  retired artifact statement's own text.
- **Warm status.** Warm reads are the interactive path, so this row is what a
  person waits for. No number has been adopted for it.
- **Memory.** 1 GiB and 4 GiB are the figures the retired resident-memory
  requests were argued against, and 4 GiB is the figure the memory lane below
  reports a measured pass against. Neither is enforced anywhere.

### The envelopes against the tracked contracts

The two envelopes are older than the contract files, and the contracts have only
ever encoded part of them.

`performance/contract-v2.json` already names most of the shape. Its `depths`
reach 500,000. Its `checkpoint_cases` carry a tail-255 case at both 50,000 and
500,000, which is where the PREVIEW depth enters the contract at all, because
50,000 is not on the depth axis. Its `actor_counts` are 1, 8 and 50. Its
`dependency_fanout_axis` is the one place either envelope appears as a
machine-checked target: a 10 percent relative limit, `preview_max_width` 64 and
`first_production_max_width` 256. Everything else in v2 is a workload
description, not a target.

`performance/contract-v3.json` bumps `schema_version` to
`gitseq.performance/v3`, adds three things, and changes nothing else. It adds
`envelope_cases`, which names the two joint cells 50,000 with 8 actors and
500,000 with 50 actors, because separate axes do not prove a joint envelope and
the case matrix otherwise keeps the scale axes independent at the smallest
depth. It adds a `checkpoint_bytes` metric, because the serialized checkpoint
size had no metric at all and so could not be compared with the 256 MiB limit.
It lengthens the worker timeouts for `cold_status`, `warm_status`,
`honest_fallback` and `checkpoint_restart`, because v2's ceilings were sized
for the depths v2 reached with those scenarios. A warm sample pays a full cold
read in setup, measured at about 126 seconds at 500,000 records, so v2's
120-second `warm_status` ceiling would abort the first case of the run.
Contract v2 is unchanged and stays the default, so every retained run still
compares against the file it was measured with.

Neither contract adopts a latency figure of any kind, apart from the fan-out
relative limit. Neither carries a memory ceiling. Neither names a checkpoint
byte target; the 256 MiB figure is a limit, enforced by the kernel and recorded
in [Limits](limits.md), not a target this page sets. That is the whole reason
the seconds and the gigabytes in the table above are tagged as planning targets:
they exist in prose and in the conditions of requests that are now retired, and
nowhere else.

Where the table's numbers come from, row by row. The depths are
`contract-v3.json` `envelope_cases` with `contract-v2.json` `checkpoint_cases`
and `depths`. The application-event count repeats the depth because the fixture
writes one application event per sequence record, so the two counts coincide
for this workload. The actor counts are v2's `actor_counts` and v3's
`envelope_cases`. The fan-out limit is v2's `dependency_fanout_axis`, measured
by the fan-out lane below at depth 1,000 with one actor; the only enforced
bound on causal references is 4,096 in one intent, on the
[Limits](limits.md) page. The checkpoint-size limit is that page's "Restart and
the checkpoint", enforced in the kernel. The cold-verify figures appear in the
conditions of retired request `a56bea44` and the cold-restart figures in the
text of retired artifact statement `e2b15773`, in no contract file either way.
Warm status has no earlier figure but the smoke hypothesis of 500 ms for
exact-head status, still classified not yet measurable in
`performance/BASELINE.md`. The one-actor memory figures are the published ones,
`performance/BASELINE.md` at 50,000 and the 500,000-record resident memory lane
below; neither is an envelope cell, because both are one actor.

Full verification is a different scenario again, with no target of any kind. The
checkpoint restart and the cold verify peaked between 1,807,319,040 and
2,250,342,400 bytes at 50,000 records, and between 16,204,529,664 and
18,966,216,704 bytes at 500,000. Those figures are new on this page, and the
section below says why they are neither a pass nor a miss.

One earlier statement already reads as a cold-verify pass at both envelopes, and
it is worth saying why this page does not carry it forward. The act that
withdrew the cold-audit request records that a cold audit of this workroom takes
3.79 seconds with 6 Git subprocesses, against 242.3 seconds and 11,581
subprocesses when the request was filed, and it says both envelopes pass. The
improvement is real and is not questioned here. The envelope claim is a
different thing: that audit was of this workroom's own history, a few thousand
records, not of 50,000 or 500,000. A result at one depth is not a result at ten
or a hundred times it. So the cold verify rows above come from measurements
taken at each envelope depth, and nothing here inherits that pass.

### The 10-second and 60-second restart targets

The 10-second PREVIEW and 60-second FIRST-PRODUCTION checkpoint-restart figures
appear in the text of one retired artifact statement and in no tracked contract
file. They are historical planning targets. This page publishes them so a reader
can see what was aimed at, and it does not treat a measurement above them as a
failed commitment or a measurement below them as a passed one.

The same holds for the 5-minute and 60-minute cold-verify figures and the 1 GiB
and 4 GiB memory figures. Turning any of them into a production commitment, and
any change to a governed limit on the [Limits](limits.md) page, needs a concrete
ordinary proposal and its adoption first. Nothing on this page does that, and no
measurement below reaches for it.

### Chronology

This table was not published before the campaign timed anything. The harness
head `0b689fe2` was committed at 11:50Z on 2026-09-09 and reviewed; the
fixtures were prepared at 12:32Z; the campaign ran from 12:39:59Z to 13:30:57Z
at that head. The table above and the campaign's
[classification rules](../../performance/retained/envelope-20260909-0b689fe2/classification-rules.md)
were drafted in the author's working notes between 12:35Z and 12:49Z, while the
run was in progress, and were first committed durably with the results at
13:50Z. Neither is a pre-registration, and this page does not claim one.

What was fixed before the run is narrower, and it is what the classifications
rest on. Every target in the table comes from a tracked document or a durable
statement that predates the run, and the previous two subsections say which:
the depths, actor counts and fan-out widths from the versioned workload
contract; the 256 MiB checkpoint ceiling from [Limits](limits.md); the 1-GiB
memory bound and the 500 ms exact-head status hypothesis from
`performance/BASELINE.md`; the 4 GiB figure from the retired resident-memory
requests and the memory lane below; the 5-minute and 60-minute cold-verify
figures from the conditions of retired request `a56bea44`; the 10-second and
60-second restart figures from retired artifact statement `e2b15773`. The
harness commit fixed the ten cases and the sample count before any of them ran. The agreement bands come from the pair spreads of
runs already published on this page, not from this campaign's numbers. No
target and no band was changed after a result was read, and no classification
was chosen to suit a result.

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

That target belongs to request `8add6909`. Its report was ratified on
2026-08-19, and that report parks the work: no reduction is authorized until a
ratifier adopts a successor to the retired proposal `3db9488e`. This page
neither claims that work nor improves it. The fan-out verdict does not depend
on it.

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

## Resident wait cost

Every open long poll on the resident used to tick its own 250 ms clock and
ask the verified snapshot on each tick: one `git rev-parse` process per tick
for each waiter whose ticks did not coincide, while waiters ticking together
already shared one read through the snapshot's single flight. One head clock
per log now does that read while any wait is open, and a waiter asks the
snapshot again only on its first pass, when the clock advances or its head
differs from the last answer, or when its live cursor moves. The
[measured run](../../performance/HEAD-WAIT.md) puts eight idle staggered
waiters over a 5 s poll at 28 ref reads against 160 before, and leaves
external-change wake latency at about one clock tick. Depths 1, 31 and 300,
one machine, warm fixtures, before and after runs taken one after the other;
not a claim about deep logs.

## Residual dimensions

Five dimensions of the two envelopes had no measurement on this page: cold
restart from an authenticated near-head checkpoint, cold verify with no
checkpoint, warm-status latency, serialized checkpoint bytes, and actor-count
cost. One bounded invocation of the `envelope` tier measures them at both
envelope depths.

```text
make perf PERF_ARGS='run --contract performance/contract-v3.json --tier envelope'
```

### How to read these results

Four things hold for every dimension below, so they are stated once here and
not repeated in each one.

- **Two primary samples and one diagnostic rerun per case.** The diagnostic
  starts only after every primary sample and enters no range or spread on this
  page; it is reported beside the pair so a reader can see whether the pair was
  a fluke.
- **No percentile.** The contract emits p95 only at 20 samples and p99 only at
  100, so at this population **both are unavailable**. No table below has a
  percentile column and none should be read as having one. Two samples are a
  pair, not a distribution.
- **Agreement bands.** Each dimension's pair is checked against a band recorded
  in the campaign's retained
  [classification rules](../../performance/retained/envelope-20260909-0b689fe2/classification-rules.md),
  which also give each band's source. A pair outside its band is published
  inconclusive rather than measured. Each subsection names its own band.
- **Planning target is not commitment.** Every figure these results are held
  against is a planning target or a historical figure, not an adopted
  commitment, except the 256 MiB checkpoint ceiling, which is an enforced
  limit. So a number under its figure is measured capability and not a
  commitment met, and a number over it is a miss against that figure and not a
  broken commitment. Adopting any of them, or changing a governed limit, needs
  a concrete ordinary proposal first, and nothing here does that.

One admission rule is shared too: a sample counts only if its projected and
trusted correctness digests were equal. That is a precondition for admitting a
sample, not evidence that the fold is correct.

### The measured envelope run

The run records harness and candidate as the same exact commit,
`0b689fe27affcae3eba5c496eee4713b257dce40`, and contract digest
`07b230aefd87f10d01f7e4c3976ef001251df27b52946efaab54036523c79d1f`. It started
at `2026-09-09T12:39:59Z`. It used three prepared fixtures, all linear: one
actor at depth 500,000 with exact digest
`19a20fe89a19433ae571f8fd4c10cd652e42197baa527b6cb7e7f9463cd8f298`, 8 actors at
depth 50,000 with exact digest
`a466e80b7683cbf5251f88023d419588c8c22c5461c992e107089c5276aae395`, and 50
actors at depth 500,000 with exact digest
`282d00e564fd0bb8ee063b2b809dca6ff9d2be5a88dbf36f26db0b52447929eb`. It ran on
the machine named above, from a clean worktree, as a single invocation with
sleep inhibited.

That harness head is fixed: it is what the samples were taken at, so it cannot
change. One correction found in review therefore lands in the later publishing
commit instead. The lane entry now refuses a tier that selects no cases under
the contract it was given, before it creates any output, so a request for the
envelope tier against contract v2 is an error rather than a zero-sample
campaign reporting a pass. The guard reads the selected case list at the
command boundary and touches no measured path, no scenario and no contract, so
the run above stands exactly as it was measured.

Only three fixtures were prepared, so the 50,000-record one-actor cases are
materialized from the 500,000-record one-actor fixture at depth 50,000. They
carry that fixture's exact digest and their own fixture head. The four fixture
heads the samples record are
`587ce2caff5718b584aa19d936e03fb860bd4a50` at 500,000 with one actor,
`7f2a2d4f25d078a2c9ad9630a52d586648193d39` at 50,000 with one actor,
`cddb45a0881c0c56d10374e6cb6f8acdbec9fb44` at 50,000 with 8 actors, and
`777f4814748886e76c48e5257260a6296671350d` at 500,000 with 50 actors.

The tier ran ten cases in contract order, on the population described above.
This section reports every raw observation and the pair.

Machine load was sampled once a minute for the whole window. The 51 one-minute
load averages ran from 2.67 to 4.40 on 18 logical CPUs, so no case window
reached the campaign's quiet threshold of 6.00, let alone its spike threshold of
12.00. That load was not idle: other work on the machine held the one-minute
average between about 3 and 4 throughout, and the numbers below were taken with
that load present. Load is disclosed rather than assumed away, for the reason
the fan-out lane records above, and the same sibling-load caveat applies in
kind. The fan-out lane's own quiet window broke when a sibling lane started, and
nothing prevents that here either. What can be said is what was observed: the
one-minute average never exceeded 4.40 during this run.

One thing about the correctness check is worth stating exactly, because it is
not the same for all ten cases. Every sample folds a second trusted projection
after its measured window, and any sample whose trusted fold fails is an error
rather than a slow result. The checkpoint restart and the cold verify also
return their own projection from the measured operation, so two digests exist
and are compared, and a mismatch fails the sample. The cold reads and the warm
reads return an encoded response rather than a projection, so no second digest
exists for them and the recorded correctness digest is the trusted one. For
those six cases the trusted fold shows that the scratch copy verifies; the
comparison of projected against trusted is not an independent check there, and
this page does not present it as one.

| File | SHA-256 |
|---|---|
| [`evidence.json`](../../performance/retained/envelope-20260909-0b689fe2/evidence.json) | `9be6e415a25f156828f6ffb90f2a555300cde7069b4eb19b4eb094d09e2d4de0` |
| [`samples.jsonl`](../../performance/retained/envelope-20260909-0b689fe2/samples.jsonl) | `994de559510bc7dd347be5035663a7f137332cea388ed77b0a7b45696acf6f0f` |
| [`candidate.bench`](../../performance/retained/envelope-20260909-0b689fe2/candidate.bench) | `8937f849bcff11ec03f781ed0d9bce80d0002d4eeaa4e2ba4c649169f4f07632` |
| [`load.log`](../../performance/retained/envelope-20260909-0b689fe2/load.log) | `49436ca4596e170abc878832f9040460332a2e0d6afc49cbae77bdfb391bd561` |
| [`classification-rules.md`](../../performance/retained/envelope-20260909-0b689fe2/classification-rules.md) | `2e4a6b42ca8d1f7896f85a1da644460ce0f314713fff2bbacb736c039c84b7f2` |

### Cold restart from an authenticated near-head checkpoint

**Measured.**

Cases `checkpoint_restart/shape-linear/depth-050000/tail-0255` and
`checkpoint_restart/shape-linear/depth-500000/tail-0255`.

| Depth | Primary samples | Diagnostic | Pair spread | Snapshot source | Checkpoint bytes |
|---:|---|---:|---:|---|---:|
| 50,000 | 7.243 s and 7.211 s | 7.259 s | 0.44 percent | `verified_signed_checkpoint_tail` | 2,161,188 |
| 500,000 | 69.417 s and 69.320 s | 69.784 s | 0.14 percent | `verified_signed_checkpoint_tail` | 21,934,525 |

Band: 15 percent. Both pairs sit well inside it.

This dimension adds one admission rule of its own: a sample counts only if it
restored from the checkpoint. The snapshot source column is that check.
`verified_signed_checkpoint_tail` means the sample measured the restart it
claims to measure, and any other value means it measured something else.

At 50,000 records the measurement is under the historical planning figure of 10
seconds, and improves on the 19.171-second average the earlier run recorded at
head `b391c918`.

At 500,000 records the measurement is over the historical planning figure of 60
seconds, by 15.7 and 15.5 percent, so this is a **miss** against that figure. It
improves on the 72.986-second average the earlier run recorded at head
`b391c918`, and the improvement does not make it a pass. A bounded
implementation child is to be filed on this evidence.

### Cold verify with no checkpoint

**Measured.**

Cases `honest_fallback/shape-linear/depth-050000` and
`honest_fallback/shape-linear/depth-500000`.

| Depth | Primary samples | Diagnostic | Pair spread | Snapshot source |
|---:|---|---:|---:|---|
| 50,000 | 12.979 s and 13.001 s | 12.994 s | 0.17 percent | `verified_cold_full_audit` |
| 500,000 | 129.259 s and 128.810 s | 129.310 s | 0.35 percent | `verified_cold_full_audit` |

Band: 10 percent. Both pairs sit well inside it. The historical 5-minute and
60-minute planning figures are far above both measurements.

The fixture materialization removes the checkpoint ref and the local checkpoint
pointer for this scenario, so the sample has nothing to restore from and
verifies the whole history. Its `checkpoint_bytes` is zero, and that zero is the
fact itself rather than a missing reading. Like the checkpoint restart, this
case returns its own projection, so its projected and trusted digests are
compared against each other and a mismatch fails the sample.

This is not the same measurement as the cold read reported in the resident
memory lane above, which folds a full history for a different scenario. The two
are reported separately and neither is restated as the other.

### Memory during full verification

**Measured. Outside the scope of the adopted memory outcome. No target exists.**

The checkpoint restart and the cold verify return the whole projection to the
caller, and they are much heavier in memory than a status read at the same
depth.

| Depth | Case | Peak resident, primary samples | Steady memory | Response bytes |
|---:|---|---|---|---:|
| 50,000 | checkpoint restart | 2.096 GiB and 1.683 GiB | 0.742 GiB and 0.741 GiB | 321,858,214 |
| 50,000 | cold verify | 1.736 GiB and 1.993 GiB | 0.693 GiB and 0.742 GiB | 321,858,214 |
| 500,000 | checkpoint restart | 16.027 GiB and 17.664 GiB | 6.863 GiB and 6.617 GiB | 3,247,800,825 |
| 500,000 | cold verify | 15.502 GiB and 15.092 GiB | 6.617 GiB and 6.617 GiB | 3,247,800,825 |

This is the same scenario shape the earlier checkpoint run recorded at
24,161,976,320 bytes of peak resident memory. These figures are lower than that
one, and they are still about five to six times the peak of a status read at the
same depth.

The 4 GiB figure does not reach these rows. It is scoped to resident status
reads: that is the shape of the adopted memory outcome `a9047812`, which is
satisfied, and it is the shape of the "500,000-record resident memory" lane
above and of "Resident memory by sequence depth" in `performance/BASELINE.md`.
None of those measured a full-verification restore that returns the whole
projection to its caller. So these four rows are neither a pass nor a miss.
They are a measured cost with no target over it, published so that the cost is
visible. A production commitment about full-verification memory would need a
concrete ordinary proposal and its adoption first, and this page does not make
one.

### Warm-status latency

**Measured.**

Cases `warm_status/shape-linear/depth-050000` and
`warm_status/shape-linear/depth-500000`.

| Depth | Primary samples | Diagnostic | Pair spread | Setup, outside the measured operation |
|---:|---|---:|---:|---|
| 50,000 | 17.589 ms and 18.155 ms | 18.468 ms | 3.22 percent | 12.904 s and 12.892 s |
| 500,000 | 150.307 ms and 164.122 ms | 163.103 ms | 9.19 percent | 126.656 s and 126.407 s |

Band: 50 percent, the one exception to the bands' source. Both pairs sit
inside it. It is wide because a warm read is milliseconds and fixed jitter is a
large fraction of one, and no published pair existed for this scenario at any
depth, so it is a judgement rather than a figure derived from evidence.

The setup column is the part that matters most here. One warm sample pays a full
cold read first, inside setup and outside the measured operation, and that first
read costs about 12.9 seconds at 50,000 records and about 126.5 seconds at
500,000. So a warm figure of 164 milliseconds is not the cost of the first read.
It is the cost of the reads that follow it, once the same resident has completed
the corresponding verified read. Warm does not mean any operating-system cache
state. The setup cost is also why this case needed a longer worker timeout in
contract v3 than contract v2 allowed.

Both depths are under the smoke-baseline planning figure of 500 milliseconds
for exact-head status. `performance/BASELINE.md` still classifies exact-head
status as not yet measurable, and two samples do not change that: they cannot
establish an interactive latency distribution.

### Serialized checkpoint bytes

**Measured.**

Read from the two `checkpoint_restart` cases above. This is a size, not a
timing, so machine load does not bear on it.

| Depth | Checkpoint bytes | As MiB | Share of the 256 MiB limit |
|---:|---:|---:|---:|
| 50,000 | 2,161,188 | 2.06 | 0.81 percent |
| 500,000 | 21,934,525 | 20.92 | 8.17 percent |

Both samples of each case reported the same size, which is what the campaign
required before publishing a size at all: two different sizes would mean the two
samples restored different objects.

The figure is the serialized size of the checkpoint object the sample actually
restored, read from the object store rather than re-encoded, so nothing
allocates it to measure it. The 256 MiB ceiling it is compared against is a
governing limit on the [Limits](limits.md) page. This section measures against
that limit and does not propose changing it.

Every other scenario in the tier records zero checkpoint bytes. For the cold
verify that zero means it restored no checkpoint.

### Actor-count cost

**Measured.**

Four cases, two pairs. At 50,000: `cold_status/shape-linear/depth-050000` and
`cold_status/shape-linear/depth-050000/actors-008`. At 500,000:
`cold_status/shape-linear/depth-500000` and
`cold_status/shape-linear/depth-500000/actors-050`.

| Depth and actors | One actor | Envelope actors | Difference of the two-sample averages |
|---|---|---|---|
| 50,000, 8 actors | 12.746 s and 12.733 s; diagnostic 12.775 s | 12.647 s and 12.659 s; diagnostic 12.691 s | -0.087 s, or -0.68 percent |
| 500,000, 50 actors | 126.360 s and 126.194 s; diagnostic 126.978 s | 126.148 s and 126.471 s; diagnostic 126.918 s | +0.032 s, or +0.03 percent |

Band: 10 percent. Every pair spread is at or under 0.26 percent, well inside
it. At 500,000 records the difference between the two cells
is 0.032 s, smaller than the spread within either pair, so it is not a
measurable cost. At 50,000 records the 8-actor cell is 0.087 s faster than the
one-actor cell. That difference is larger than either pair's own spread, but it
runs the wrong way for an actor-count cost.

The one-actor and joint cells necessarily use different fixtures, because actor
count is a fixture parameter. At 50,000 they also differ in preparation, because
the one-actor cell reads the 500,000-record one-actor fixture at depth 50,000
while the joint cell reads the fixture prepared with 8 actors at 50,000. So this
run measures no actor-count cost on a cold status read at either envelope, and
the 50,000 difference is not evidence of a saving either. That is a statement
about these four cases and this workload. It is not a claim that actor count is
free in general, and it says nothing about restart, verify or warm latency at 8
or 50 actors.

The two comparisons are not equally controlled, and the difference matters. At
50,000 the one-actor read and the joint cell are emitted together and run
adjacent, so little machine time separates them. At 500,000 the one-actor read
is the ordinary depth-axis case and runs first in the tier, while the joint cell
runs last, so the primary samples of eight other cases separate them. The
reconstructed separation is about 13 minutes between the last one-actor primary
sample and the first 50-actor primary sample. That reconstruction is
approximate and shorter than the clock, because it adds each sample's setup and
latency and neither field carries process spawn or fixture copy time: the summed
figure for the whole run is about 32 minutes against a load log covering about
51 minutes. An increment computed across that gap carries whatever the machine
did in between, so the 500,000 difference is reported with its separation stated
rather than as a controlled comparison.

Peak resident memory for the same four cases is 0.448 and 0.454 GiB at 50,000
with one actor, 0.369 and 0.361 GiB at 50,000 with 8 actors, 2.886 and 2.912 GiB
at 500,000 with one actor, and 3.304 and 2.844 GiB at 500,000 with 50 actors.
The worst 50-actor peak is 3,547,348,992 bytes, which is 17.4 percent below
4 GiB. Those last two pairs are the first resident-memory figures on this page
taken at an envelope actor count, so the FIRST-PRODUCTION resident-memory result
is re-observed here at this head and at 50 actors. It is re-observed, not
restated: the memory lane above keeps its own one-actor numbers, and neither set
replaces the other.

The one-actor pair at 500,000 is worth naming on its own. Its peaks of
3,098,345,472 and 3,126,509,568 bytes are 14.4 and 15.4 percent above the worst
one-actor peak the memory lane published, and still 27.9 and 27.2 percent below
4 GiB. So the one-actor peak at this head sits above the lane's published range
and inside the planning figure.

### How to re-derive these figures

Every measurement in this section comes from the retained files above.
`performance/retained/envelope-20260909-0b689fe2/samples.jsonl` holds one JSON
record per sample, and
`performance/retained/envelope-20260909-0b689fe2/evidence.json` holds the head,
the contract digest, the environment and the harness's own summaries. The
machine-load figures for this campaign, the 51 samples and the range across the
window, are read from
`performance/retained/envelope-20260909-0b689fe2/load.log`: one line a minute,
carrying the one, five and fifteen minute load averages and a count of the
build and harness processes running at that minute. The agreement bands each
scenario is judged against, and the rules that decide measured, modeled,
unavailable or inconclusive, are in
`performance/retained/envelope-20260909-0b689fe2/classification-rules.md`. That
file records the bands and admission checks the author applied and where each
band came from, so a later reader can hold the published numbers against them.
It is not a pre-registration: it was written during the run and first committed
with the results, and it says so in its own first lines. A nested metric in a sample row is either a plain
number or an object with a `value` field, so a reader has to accept both:

```text
jq -r 'select(.position==1) | [.case, .round, .result.latency_ns,
  (.result.peak_rss_bytes | if type=="object" then .value else . end),
  .result.checkpoint_bytes] | @tsv' samples.jsonl
```

Rows with `position` 1 are the primary samples; `position` 0 is the diagnostic
rerun that enters no range here. Seconds are `latency_ns` divided by
1,000,000,000, and a share of the 256 MiB limit is `checkpoint_bytes` divided by
268,435,456.

### The historical checkpoint measurements

Two earlier durable statements measured near-head checkpoint restart at these
depths. Both are history here, and neither is a current-head result.

Artifact statement `e2b15773`, filed under request 6231, measured at exact head
`b391c918` against `performance/contract-v1.json`. Two samples at 50,000
recorded 19.616 and 18.727 seconds, an average of 19.171 seconds against a
10-second target, a miss. Two at 500,000 recorded 73.761 and 72.211 seconds, an
average of 72.986 seconds against a 60-second target, a miss by 21.6 percent.
All four reported `verified_signed_checkpoint_tail` and matching digests. It is
not a current-head result: the contract has moved from v1 through v2 to v3, the
harness and scenario packages have changed many times since, and its own
24,161,976,320-byte peak resident figure belongs to the full-verification shape
discussed above rather than to the status reads the memory lane bounds.

Artifact statement `7994ba10`, filed under request 6292, measured checkpoint-tail
batching at exact head `e5ce5aab` on `internal/kernel`, recording PREVIEW at
5.904 to 5.976 seconds against a cold 12.442 to 12.654 seconds with equal
digests, and a reviewer reproducing 500,000 below 60 seconds. It is not a
current-head result either: the checkpoint chunk and cache handling in
`internal/kernel` has been reworked since, the verification path changed again
when causal trailers began comparing as ordered elements, and the reviewer's
500,000 figure is a reported observation with no retained distribution behind it.

A historical measurement does not become a current-head result because the idea
it measured still exists. Both are cited for what was aimed at and what was seen
then. The current figures in this section are the ones taken at the head named
above.

### What the joint cells prove

Exactly two joint cells were measured: 50,000 records with 8 actors, and 500,000
records with 50 actors, both as cold reads. Every other joint claim about these
envelopes is unmeasured, and is stated here as unmeasured.

In particular, no measurement here combines depth with dependency fan-out. The
fan-out lane above is measured at depth 1,000 with one actor, and it stays a
result about that depth. The checkpoint restart, the cold verify and the warm
read at each envelope depth are one-actor cases; they are not joint results with
the envelope's actor count. The two cold reads that are joint say nothing about
restart, verify or warm latency at 8 or 50 actors.

A claimed joint verdict needs its own joint case. Separate axes do not prove a
combined envelope, and this section does not claim one.

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
