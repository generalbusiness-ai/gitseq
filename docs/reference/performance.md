---
title: Performance evidence
summary: The versioned dependency fan-out measurement and its current result.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ae6af9093ba17d3c2cfe46ca05c02af9f26627e7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4beace94c4c82b4f7cb0e3336813ba98b5c49a9c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c6ad16380d83dcc1dad691b5bcea49176276c7dc
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

### Current measured run

The current run used clean harness and candidate commit `7b5026f09da1086a13b4ab3472f9b84863e83632`,
contract digest `b0795bc71c9485210a842decce2fc932627a88072bbd3494a22098fcf66c7d45`,
fixture head `154e6fa1e0d05f8e074308c2618d555a8f84a998`, and fixture exact digest
`c3846ff0cf593e3166737a34db4649b5544751c9f99c8461c941a058f7e399d4`.
It ran on Darwin arm64, an 18-core Apple M5 Max with 64 GiB memory, Go 1.26.5,
and Git 2.50.1. All 500 primary samples had equal projected and trusted
correctness digests.

| Width | p50 | p95 | p99 | Maximum | Signed change from width 1 |
|---:|---:|---:|---:|---:|---:|
| 1 | 497.331 ms | 1,106.639 ms | 3,615.586 ms | 6,077.966 ms | 0 ms; 0% |
| 8 | 451.447 ms | 469.212 ms | 483.286 ms | 486.542 ms | -45.884 ms; -9.226% |
| 16 | 445.251 ms | 458.616 ms | 472.200 ms | 473.492 ms | -52.080 ms; -10.472% |
| 64 | 445.613 ms | 460.090 ms | 482.367 ms | 488.723 ms | -51.718 ms; -10.399% |
| 256 | 444.809 ms | 450.182 ms | 477.759 ms | 492.071 ms | -52.522 ms; -10.561% |

The relative target permits no more than a 10-percent median increase at
every measured width through 64 for PREVIEW and through 256 for
FIRST-PRODUCTION. This run passes both. The width-one median itself is still
an honest miss against the separate 50 ms fixed append budget; a passing
fan-out ratio is not an overall append-latency guarantee.

The evidence files for this run have these SHA-256 digests:

- `evidence.json`: `47579f652d0b7ff30b6afb8469bd1a16a2eed7c463879ae46cc6fbe1acbd0c68`;
- `samples.jsonl`: `f7ec6a431026f4bcdfaf9f63da753294579f51baf09de6f0adc72c3a52386937`;
- `candidate.bench`: `ee5b332952f67ab5cdb02edcc6b701c24221ae1a4651514d0ff905aab0b2d972`.

If another complete consecutive-axis run changes either pass/fail
classification, the evidence is inconclusive and must be rerun under a quiet
window. The raw distributions must not be replaced by only these medians.

## Preserved contracts

This lane changes measurement order and reporting only. It does not defer or
remove actor signing, sequencer admission, verified-ref compare-and-swap,
signature or bound verification, complete fold semantics, idempotency,
trusted-versus-projected equality, error behaviour, or atomic publication.
The harness exercises the ordinary submit path and fails the sample if the
trusted and projected digests differ.
