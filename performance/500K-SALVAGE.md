# 500k memory salvage measurements

These measurements decide which parts of the stranded 500k-memory work are
worth keeping. They are point observations, not latency percentiles or service
objectives.

The run used an Apple M5 Max with 18 logical CPUs and 64 GiB of memory, Darwin
25.6.0 arm64, Go 1.26.5, and Git 2.50.1. Every sample was a fresh worker
process over the linear, one-actor, fan-out-one fixture at depth 100,000. The
fixture used generator `gitseq.performance-fixture.v2`, seed 632, logical
digest `550b626c3bc05a6977431f9d7738296773c56d29512bc010f4ac8c875a5e6901`,
and exact digest
`07968ba07f9454dfaca888fc68a021d5282f54fd5a946fac8c428a332135ef26`.

Each comparison used two alternating rounds. A part counts as measurable only
when its candidate and base allocated-byte ranges do not overlap. The
decode-time string pool also had to reduce median allocated bytes by at least
5% without making median latency, peak RSS, or steady memory more than 5%
worse.

| Part and exact comparison | Scenario | Base allocated bytes | Candidate allocated bytes | Median change | Base peak RSS | Candidate peak RSS | Median change |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Borrow complete checkpoint chunks, `b87afa1e` to `da53996b` | `submit_ack` | 9,192,322,040–9,192,328,280 | 8,550,400,360–8,550,454,688 | -6.98% | 1,669,070,848–1,698,168,832 | 1,376,911,360–1,393,852,416 | -17.71% |
| Unmarshal and stream the canonical comparison, `b87afa1e` to schema-only `577a1917` | `cold_status` | 6,620,318,560–6,621,318,736 | 3,756,670,904–3,757,140,176 | -43.26% | 559,824,896–571,588,608 | 535,937,024–549,027,840 | -4.11% |
| Reuse repeated unescaped state text during decode, `18708744` to `704dc7be` | `cold_status` | 3,756,656,568–3,756,976,296 | 3,184,198,440–3,184,297,984 | -15.24% | 528,711,680–574,308,352 | 536,395,776–562,692,096 | -0.36% |

The allocation ranges separate for all three parts, so all three are kept.
The checkpoint change also reduced allocation count by 0.42%. The schema
change reduced allocation count by 3.51% and garbage collections from 98 to
69–70. The final string-pool implementation did not add per-record
allocations: its two samples recorded 13,777,087 and 13,777,131 allocations,
against 13,777,618 and 13,777,775 for its exact parent.

Median latency changed by -0.39% for checkpoint borrowing, +0.03% for schema
decoding, and -1.85% for decode-time pooling. Median steady memory changed by
-0.09%, +0.03%, and +0.01%, respectively. Every `cold_status` sample produced
the same correctness and trusted digest. Every `submit_ack` sample's trusted
digest matched its correctness digest; the action-specific digest differs
between processes because each run creates a fresh signed action.

The checkpoint comparison uses `submit_ack` deliberately. `cold_status` uses
the streamed checkpoint builder and does not execute the changed batch
`appendEvents` path. Schema decoding and text pooling use `cold_status`, where
the full verified history is decoded and retained. Setup and profiling are
outside the measured operation.

## Combined 500k integration

The contract `memory` tier compared base `b87afa1e` with combined code head
`79741ac2`. It passed all 20 primary samples at depths 100, 1,000, 10,000,
100,000, and 500,000. The clean harness checkout was also `79741ac2`.

At depth 500,000, the two base samples allocated 33,549,453,056 and
33,550,331,784 bytes. The two candidate samples allocated 16,310,004,608 and
16,311,203,184 bytes, a 51.38% reduction in the median. Candidate and base
produced the same correctness and trusted digest in every sample.

Median latency changed by -0.06%, from 133.14 to 133.06 seconds. Median peak
RSS changed by +1.40%, from 2,667,421,696 to 2,704,654,336 bytes, and median
steady memory changed by +0.003%, from 1,365,771,552 to 1,365,817,516 bytes.
Those remain inside the 5% regression guard. The pinned `benchstat` tool was
not installed, so the evidence records that comparison as unavailable rather
than as zero.

The retained run contains exactly the required
[evidence document](retained/2026-08-21-500k-salvage/evidence.json),
[raw samples](retained/2026-08-21-500k-salvage/samples.jsonl), and
[candidate benchmark data](retained/2026-08-21-500k-salvage/candidate.bench).
