# Performance baseline

The maintained lane has no accepted current baseline yet. Its first clean,
exact-head smoke run will classify the earlier hypotheses below without
turning one machine's timing into a product guarantee.

| Earlier hypothesis | Required evidence | Current classification |
| --- | --- | --- |
| Exact-head status at or below 500 ms | Complete cold and warm status samples at a named depth | **Not yet measured.** |
| A tail of at most 10 events at or below 2 s | Checkpoint restart with tail 0, 1, and 10 | **Not yet measured.** |
| Default bounded output below 32 KiB | Encoded default response bytes | **Not yet measured.** |
| Warm depth-20,000 summary below 64 KiB | Complete warm summary response bytes | **Not yet measured.** |

The existing size tests remain correctness tripwires. They are not substituted
for retained performance evidence, and historical point timings are not
silently promoted into guarantees.
