# Performance baseline

The first current smoke baseline was recorded on 2026-08-11 at clean harness
commit `77f2883f7eb8f9a49b5755b3ed8f2d84623d2772`, with contract digest
`272c8c057d9c8fbf3f72f1b7d0c92e376900fd322d83bdb2b5f08f3b82d03969`.
It contains one primary sample for each of 18 bounded smoke cases on an Apple
M5 Max with Darwin arm64, Go 1.26.5, and Git 2.50.1. One sample is enough to
classify response-size hypotheses and preserve a first observation, but not
enough to claim latency percentiles.

| Earlier hypothesis | Required evidence | Current classification |
| --- | --- | --- |
| Exact-head status at or below 500 ms | Complete cold and warm status samples at a named depth | **Not yet measurable.** Depth-100 cold was 4.37 s and warm was 16.0 ms, but one sample cannot establish a representative exact-head distribution. |
| A tail of at most 10 events at or below 2 s | Checkpoint restart with tail 0, 1, and 10 | **Met in this smoke observation.** The three samples were 283 ms, 322 ms, and 694 ms. Repeat the standard population before treating this as stable evidence. |
| Default bounded output below 32 KiB | Encoded default response bytes | **Met in every bounded summary smoke case.** The largest default status summary was 8,142 bytes. Full snapshots and wait responses are intentionally larger and are not this hypothesis. |
| Warm depth-20,000 summary below 64 KiB | Complete warm summary response bytes | **Not yet measurable.** Smoke covers depth 100, not 20,000. |

The smoke run also exercised one, four, and sixteen concurrent reader/writer
pairs. Those are retained workload observations, not service-level objectives.
The existing size tests remain correctness tripwires; they are not substituted
for retained performance evidence, and historical point timings are not
silently promoted into guarantees.
