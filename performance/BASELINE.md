# Performance baseline

The first current smoke baseline was refreshed on 2026-08-11 at clean harness
commit `83772d0f3e45d3c234120fddb04c49f76fea85df`, with contract digest
`272c8c057d9c8fbf3f72f1b7d0c92e376900fd322d83bdb2b5f08f3b82d03969`.
It contains one primary sample for each of 18 bounded smoke cases on an Apple
M5 Max with Darwin arm64, Go 1.26.5, and Git 2.50.1. One sample is enough to
classify response-size hypotheses and preserve a first observation, but not
enough to claim latency percentiles.

| Earlier hypothesis | Required evidence | Current classification |
| --- | --- | --- |
| Exact-head status at or below 500 ms | Complete cold and warm status samples at a named depth | **Not yet measurable.** Depth-100 cold was 4.44 s and warm was 8.2 ms, but one sample cannot establish a representative exact-head distribution. |
| A tail of at most 10 events at or below 2 s | Checkpoint restart with tail 0, 1, and 10 | **Met in this smoke observation.** The three samples were 287 ms, 330 ms, and 716 ms. Repeat the standard population before treating this as stable evidence. |
| Default bounded output below 32 KiB | Encoded default response bytes | **Met in every bounded summary smoke case.** The largest default status summary was 8,142 bytes. Full snapshots and wait responses are intentionally larger and are not this hypothesis. |
| Warm depth-20,000 summary below 64 KiB | Complete warm summary response bytes | **Not yet measurable.** Smoke covers depth 100, not 20,000. |

The smoke run also exercised one, four, and sixteen concurrent reader/writer
pairs. Its soak reached the 60-second ceiling after 662 of the possible 1,000
operations. Those are retained workload observations, not service-level objectives.
The existing size tests remain correctness tripwires; they are not substituted
for retained performance evidence, and historical point timings are not
silently promoted into guarantees.

## Resident memory by sequence depth

On 2026-08-19, the reliably cold `cold_status` scenario measured the linear,
one-actor, fan-out-one fixture at 500, 5,000, and 50,000 records. Each sample
used a fresh process and writable materialization with the checkpoint ref and
local checkpoint pointer removed. The fixture's exact digest was
`694dc12f795d3ef25b8f487e2b5cce64ed860c9e146a9930ee0e0fd0b23fdbdf`.
The machine was an Apple M5 Max running Darwin arm64, Go 1.26.5, and Git 2.50.1.

| Records | Base `394fcd56` peak RSS | This change peak RSS |
| ---: | ---: | ---: |
| 500 | 50,593,792 B | 50,511,872 B |
| 5,000 | 221,888,512 B | 134,119,424 B |
| 50,000 | 1,919,893,504 B | 934,969,344 B |

Across the full hundredfold depth range, observed peak-RSS slope fell from
37,764 to 17,868 bytes per record. The two final unprofiled 50,000-record
candidate runs peaked at 934,969,344 and 931,643,392 bytes. Both remained below the
1-GiB bound (1,073,741,824 bytes), with matching trusted and projected
correctness digests. These are retained point measurements of one workload and
machine, not a general latency or capacity guarantee.
