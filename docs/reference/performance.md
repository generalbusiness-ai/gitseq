---
title: Performance
summary: The scale envelopes this project measures itself against, and what it currently costs at them.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5fc8aa1aa18f8d947b919f7a6643b860aba62d17
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ee361d87ece2d3e98a336bfe937977b419285370
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6cfe666b556882128ff01e600b484142ac5efa9d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9627cf187f0a8002ea2c43861dd7cb208a09ce51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a6640f397fa62b5779ae6d2a8cd9811320055fd6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:37c3007c9d50d73c4012db8c49f03d55fb0aa7da
---

# Performance

[Limits](limits.md) records the sizes a call is refused for exceeding. This
page records something different: the scale the project intends to serve, and
what it measured itself costing at that scale. A limit is enforced. An
envelope is a claim about the future that measurement can falsify.

Every number here is labelled by how it was obtained. **Measured** means
observed at that scale. **Modeled** means computed from a per-record rate
observed at a smaller scale. **Not established** means no measurement on this
page decides it.

## Two envelopes

The envelopes were written down before any performance number was measured, so
that the targets could fail. Only the shape of real use informed them.

This repository is the grounding observation: 5,786 records in 12 days with 5
actors, at 482 records a day, under heavy multi-agent use, with dependency
fan-out averaging 1.78 bases per record and reaching 48.

**PREVIEW** is the scale a single enthusiast repository reaches in months: one
person and their agents sustaining about half this repository's burst rate for
six months, which is 240 records a day for 180 days, or about 43,000.

**FIRST-PRODUCTION** is a small team a year in: eight people each with agents,
about six times this repository's actor count.

| Dimension | PREVIEW | FIRST-PRODUCTION |
|---|---|---|
| Sequence depth | 50,000 | 500,000 |
| Actor count | 8 | 50 |
| Dependency fan-out | p99 8, max 64 | p99 16, max 256 |
| Checkpoint size | 128 MiB | 256 MiB |

The FIRST-PRODUCTION depth carries an explicit assumption. Six times the
observed 482 records a day, sustained for a year, is about 1.06 million
records. The envelope halves that to 500,000, assuming a team sustains roughly
half the duty cycle of a solo enthusiast's burst. That assumption is a
judgement, not a measurement, and the checkpoint-size verdict is the one that
changes at the undiscounted figure.

256 MiB is not a choice: it is the serialized checkpoint ceiling limits.md
enforces, so it is the largest value the envelope may name.

| Target | PREVIEW | FIRST-PRODUCTION | Why this number |
|---|---|---|---|
| Cold restart with a checkpoint | 10 s | 60 s | a restart is an operator-visible outage |
| Cold verify, no checkpoint | 5 min | 60 min | a deliberate full audit, nightly, not interactive |
| Warm status latency | 500 ms | 2 s | an interactive board refresh |
| Resident peak RSS | 1 GiB | 4 GiB | an enthusiast laptop; a small VM |
| Checkpoint blob on disk | 128 MiB | 256 MiB | the enforced ceiling |
| Max-fan-out record | 50 ms | 50 ms | one record must not stall the fold |
| Actor-count cost | +10% status | +10% status | actors must not dominate |

## Results

Measured on darwin/arm64, 18 cores, 64 GiB, Go 1.26.5, at main adda28c8, load
average 2.5 to 5.

| Target | Scale | Observation | Basis | Verdict |
|---|---|---|---|---|
| Cold verify | 50,000 | 12.80 s against 5 min | measured | pass |
| Cold verify | 500,000 | 128.7 s against 60 min | measured | pass |
| Cold restart, checkpoint, 255-record tail | depth 512 | 11.95 s against 10 s | measured | **miss** |
| Cold restart, checkpoint | 500,000 | tail cost carries; checkpoint-load cost unknown | **not established** | — |
| Warm status, bounded route | 50,000 | 15.7 ms against 500 ms | measured | pass |
| Warm status, bounded route | 500,000 | 103.3 ms against 2 s | measured | pass |
| Resident peak RSS | 50,000 | 937 MB against 1 GiB | measured | pass |
| Resident peak RSS | 500,000 | 9,183 MB against 4 GiB | measured | **miss** |
| Checkpoint blob | 50,000 | about 14.4 MB against 128 MiB | modeled, 287 B per record | pass |
| Checkpoint blob | 500,000 | about 143.5 MB against 256 MiB | modeled, 287 B per record | pass |
| Checkpoint blob | 1.06 million | about 304 MB against 256 MiB | modeled, 287 B per record | **miss** |
| Max-fan-out record | 50,000 | 774 ms against 50 ms | measured | **miss** |
| Actor-count cost | ~100 records, 1 vs 50 actors | 308.6 vs 303.0 ms, 7 repeats | measured | pass |

Supporting observations, all measured:

| Measurement | Scale | Result |
|---|---|---|
| Cold verify, this repository | depth 6,205 | 1.69 s; 6 git subprocesses; 43.8 MB peak RSS |
| Cold full audit, harness | depth 100 / 50,000 / 500,000 | 184.6 ms / 12.80 s / 128.7 s |
| Resident startup, no usable checkpoint | depth 50,000 / 500,000 | 12.83 s / 127.4 s |
| Checkpoint restart, tail 0 / 10 / 255 / 256 / 1000 | depth 257 / 267 / 512 / 513 / 1257 | 178.1 ms / 733.2 ms / 11.95 s / 11.93 s / 46.30 s |
| Cold audit at the restart depth | depth 512 | 0.30 s |
| Warm status, bounded route | depth 5,000 / 50,000 / 500,000 | 11.0 / 15.7 / 103.3 ms |
| Bounded response size | depth 5,000 / 50,000 / 500,000 | 726 / 729 / 732 B |
| Live resident, full versus bounded route | depth 6,205 | 11,978,253 B in 23 ms; 31,879 B in 14 ms |
| Checkpoint blob, v3 format | depth 6,205 | 1,781,504 B, or 287 B per record |
| Peak RSS, warm resident | depth 5,000 / 50,000 / 500,000 | 135 / 937 / 9,183 MB |
| Fan-out axis, incremental cost per base | depth 50,000, 1 to 256 bases | 0.22 ms per base |
| Append latency, one workroom | 311 records, 1 vs 256 bases | 764.0 vs 773.7 ms median |

Three rates are used for modeling and nothing else: the checkpoint blob at
287 B per record, the full projection response at 1,930 B per record, and a
cold audit at 0.25 ms per record. Every verdict resting on one of them is
marked modeled above.

The whole-projection route is not given a verdict. It is an explicit opt-in for
callers that want the entire projection and is uncapped by design — modeled at
about 96.5 MB at PREVIEW and 965 MB at FIRST-PRODUCTION. A caller asking for
everything gets everything, and no interactive budget applies to that request.
The 500 ms and 2 s targets belong to the route ordinary clients use.

## The checkpoint tail

This is the only place where the current implementation is slower than the
alternative it replaces, and it is a regression rather than a standing defect.

An earlier bounded request against tail cost was ratified complete on a maximum
1.2792 s tail restart. The cold-audit batching that merged afterwards made a
full audit roughly 250 times cheaper per record and left tail verification
untouched, which inverted the tradeoff between them.

At the same depth 512, a checkpointed restart with a 255-record tail takes
11.95 s while a full cold audit of the whole repository takes 0.30 s, both
reproducible to within 0.2 percent across repeated runs. A tail record costs
about 46 ms against 0.25 ms for an audited record, and the longest tail the
contract exercises, 1,000 records, takes 46.30 s.

Because a full audit costs 0.25 ms per record, a checkpoint only begins to pay
for itself near 47,000 records — modeled, and almost exactly the PREVIEW
envelope. Below that depth a checkpointed restart is slower than no checkpoint;
well above it the checkpoint should be a large win. Whether it is at
FIRST-PRODUCTION depth is not established, because no fixture places a
checkpoint near the head of a deep sequence.

## How the world moved under this page

All six bounded requests this work originally raised are closed in durable
history: two were cancelled as satisfied, one was superseded by a corrected
replacement, and the rest were satisfied. Between them they moved cold verify
from a miss to a pass, brought the checkpoint blob inside its ceiling, made both
envelope depths cheap enough to measure directly, introduced the bounded status
route, and halved the resident memory slope so PREVIEW fits in 937 MB.

The misses on this page are new findings measured after those merges, not
survivals from before them, and each has its own fresh bounded request: the
checkpoint-tail regression, resident memory at FIRST-PRODUCTION, the
max-fan-out target as written, and the missing near-head checkpoint fixture
that leaves FIRST-PRODUCTION restart undecided.

## Method

Measurements used the `internal/perfscenario` harness under the
`gitseq.performance/v1` contract, which carries depth, actor-count and
dependency-fan-out axes and reaches 500,000 records. Read-only measurements
against this repository supplied the real-world datapoints. Nothing was written
to this repository's log to produce these numbers.

Wall-clock seconds on a shared laptop are noisy, and this page measured that
rather than asserting it: an earlier harness run repeated at load average 20
returned every timing about three times larger than the same run near 3, while
returning peak-memory figures identical to within one percent. The two
conclusions carrying the most weight — the checkpoint-tail comparison and the
fan-out axis — are within-run comparisons at a single depth, so load cancels
out of both.

Payload mix matters in both directions, which is why the modeling rates come
from this repository rather than from fixtures. The harness fixture runs about
6.4 KB per record against this repository's 1,930 B, so fixture response sizes
overstate reality threefold. Checkpoints invert it: a fixture reports 11 B per
record because generated payloads compress far better than real ones, where the
old uncompressed format made the same fixture heavier than reality. A fixture is
the wrong instrument for either question.

## What this page is not

No invariant was weakened to reach any number here, and nothing is repaired on
this page. Every miss and the one undecided target carry a bounded
implementation request instead.
