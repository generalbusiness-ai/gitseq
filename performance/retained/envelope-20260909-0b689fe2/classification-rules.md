# Classification rules for the envelope campaign

Written during the run (2026-09-09, 12:35Z to 12:49Z) and committed with the
results; not a pre-registration.

These rules decide, for each of the ten cases the `envelope` tier runs, whether
the result is published as measured, modeled, unavailable or inconclusive.

The bands below come from the pair spreads of runs already published on the
performance page, not from this campaign's numbers, and none of them was
changed after a result was read. That is what this file can honestly claim.
What it cannot claim is priority: the campaign started at 12:39:59Z, this file
was still being drafted at 12:49Z, and it was first committed durably with the
results. A rule chosen after seeing a number is not a rule. If a rule below
turns out to be wrong, change it in the open and say that it changed, rather
than reclassifying one case to suit its result.

Head under measurement: `0b689fe27affcae3eba5c496eee4713b257dce40`. Contract:
`performance/contract-v3.json`. Population: two primary samples and one
diagnostic rerun per case.

## The four classes

- **Measured.** The number was observed in this campaign, at this head, under
  the admission and quiet rules below.
- **Modeled.** The number was computed from observations of other cases rather
  than observed for this cell. A modeled figure names its inputs and the
  arithmetic. Nothing in this campaign should be modeled; the entry exists so
  that an interpolated figure, if anyone publishes one, cannot be printed as if
  it were seen.
- **Unavailable.** No usable observation exists. The reason is stated.
- **Inconclusive.** Observations exist but do not support the claim, because
  there were too few of them, because they disagree, or because the machine was
  not quiet.

A classification is not a verdict. Measured says the number is real. Whether
that number is above or below a planning target is a separate sentence, and no
target moves to change one.

## Admission: what makes a sample exist at all

The harness enforces these. A sample that fails one is an error, not a slow or
odd result, and it never enters a range.

1. **Trusted fold, and digest equality where two digests exist.** Every sample
   folds a second trusted projection after its measured window, and a sample
   whose trusted fold fails is an error. Two of the four scenarios here also
   return their own projection from the measured operation, so a real comparison
   happens: `checkpoint_restart` and `honest_fallback` fail on a mismatch.
   `cold_status` and `warm_status` return an encoded response rather than a
   projection, so their recorded correctness digest is copied from the trusted
   one and the comparison is trivially equal. For those six cases, cite the
   trusted fold, not an independent digest comparison. Equality is in any case a
   precondition for admitting a sample, never evidence that the fold is right.
2. **Worker completion inside the contract timeout.** Contract v3 allows 900
   seconds for `cold_status`, `honest_fallback` and `checkpoint_restart`, and
   600 for `warm_status`. A timeout kills the worker and fails the sample.
3. **Declared shape.** The worker's reported actor count and dependency fan-out
   must equal what the case asked for.
4. **Snapshot source.** A `checkpoint_restart` sample must report
   `verified_signed_checkpoint_tail`. An `honest_fallback` sample must report
   `verified_cold_full_audit`. A sample that reports anything else measured a
   different operation and is not admitted, whatever its latency.
5. **Checkpoint object present.** For the two `checkpoint_restart` cases, the
   lane reads the restored checkpoint object's size from the fixture. If that
   object has been garbage-collected out of a cached fixture, the read fails and
   the sample fails with it. It never records a zero.

The harness outcome line, `pass` or `error`, says whether the run was internally
valid. It is not a verdict against any target, and it is not a classification.

## Quiet window

The machine has 18 logical CPUs. The campaign samples the one-minute load
average once a minute for the whole run.

- **Quiet:** every load sample inside a case's window is at or below 6.00.
- **Busy but usable:** some sample inside the window is above 6.00 and none is
  above 12.00. The case can still be measured, and the page states the observed
  range for that case.
- **Spike:** any load sample inside the window is above 12.00. The case is
  **inconclusive**, and the observations are published with the load that
  accompanied them.

Those thresholds are one third and two thirds of the CPU count. They follow
from the machine, not from this campaign's load samples. The fan-out lane
records what a spike does: load climbed from 4.08 to 14.53 during one block,
and that block's own median moved 60.789 percent within itself.

**Window reconstruction.** Sample records carry no wall-clock timestamp, and the
lane logs no per-case progress. Only the run's `started_at` is recorded. So a
case window is reconstructed by adding each sample's `setup_ns` plus
`latency_ns` in emission order, starting from `started_at`. The reconstruction
drifts, because process spawn and scratch-copy time are not in either field, so
a load sample within one minute of a reconstructed boundary counts against both
adjacent cases. The page states that the alignment is reconstructed and
approximate, in the same terms the fan-out lane used for its own alignment gap.

## Pair agreement at n equals 2

With two samples there is no distribution, so the only internal check available
is whether the two agree. Spread is `(max - min) / min`.

| Scenario | Band | Why this band |
|---|---:|---|
| `cold_status`, `honest_fallback` | 10 percent | The published memory lane's repeated cold reads on a quiet machine differed by 0.15 percent at 500,000 records, 0.5 percent at 100,000 and 4.7 percent at 10,000. Ten percent is generous against that. |
| `checkpoint_restart` | 15 percent | The historical tail-255 pairs differed by 4.7 percent at 50,000 and 2.1 percent at 500,000. Fifteen percent is generous against that, and this scenario adds checkpoint load to tail verification. |
| `warm_status` | 50 percent | A warm read is milliseconds, so fixed jitter is a large fraction of it. No published pair exists for this scenario at any depth, so this band is a judgment rather than a figure derived from evidence, and the page says so. |

A pair inside its band and inside a quiet or busy-but-usable window is
**measured**. A pair outside its band is **inconclusive**, whatever the load.

## The ten cases

| # | Case | Measured when | Inconclusive when | Unavailable when |
|---:|---|---|---|---|
| 1 | `cold_status/shape-linear/depth-500000` | Both samples admitted, spread at or under 10 percent, no load sample above 12.00 in the window | One sample admitted; or spread over 10 percent; or a spike in the window | Neither sample admitted, for a timeout at 900 s, a trusted-fold failure, a worker error, or a missing fixture |
| 2 | `warm_status/shape-linear/depth-500000` | Both admitted, spread at or under 50 percent, no spike | One admitted; or spread over 50 percent; or a spike | Neither admitted. The 600 s ceiling covers the operation and its setup, and setup here is a full cold read of about 134 s, so a timeout is a real risk and is reported as one |
| 3 | `checkpoint_restart/shape-linear/depth-050000/tail-0255` | Both admitted with `verified_signed_checkpoint_tail`, spread at or under 15 percent, no spike | One admitted; or spread over 15 percent; or a spike | Neither admitted; or the 49,745 checkpoint object was collected out of the cached fixture and the size read failed |
| 4 | `checkpoint_restart/shape-linear/depth-500000/tail-0255` | As case 3, at the 499,745 checkpoint | As case 3 | As case 3, at the 499,745 checkpoint |
| 5 | `honest_fallback/shape-linear/depth-500000` | Both admitted with `verified_cold_full_audit` and `checkpoint_bytes` zero, spread at or under 10 percent, no spike | One admitted; or spread over 10 percent; or a spike | Neither admitted. At 900 s the ceiling is about six times the measured 500,000-record cold read, so a timeout here means something changed, not that the ceiling is tight |
| 6 | `cold_status/shape-linear/depth-050000` | As case 1, at 50,000 | As case 1 | As case 1 |
| 7 | `cold_status/shape-linear/depth-050000/actors-008` | As case 1, at 50,000 with 8 actors. The worker's reported actor count must be 8 | As case 1 | As case 1; or the 8-actor 50,000-record fixture is missing |
| 8 | `warm_status/shape-linear/depth-050000` | As case 2, at 50,000 | As case 2 | As case 2 |
| 9 | `honest_fallback/shape-linear/depth-050000` | As case 5, at 50,000 | As case 5 | As case 5 |
| 10 | `cold_status/shape-linear/depth-500000/actors-050` | As case 1, at 500,000 with 50 actors. The worker's reported actor count must be 50 | As case 1 | As case 1; or the 50-actor 500,000-record fixture is missing |

## Derived figures

Three numbers on the page are computed rather than observed. Each takes its
class from its inputs.

- **Checkpoint bytes as a share of the 256 MiB limit**, from cases 3 and 4.
  Measured when its case is measured. This is a size, not a timing, so the quiet
  rule does not apply to it: load cannot change how many bytes an object holds.
  Both samples of a case must report the same size; different sizes mean the two
  samples restored different objects, which is a defect to investigate rather
  than a spread to average.
- **Actor-count increment at 50,000**, from cases 6 and 7. Measured only when
  both are measured. The two run adjacent, because the envelope cell emits them
  together, so little machine time separates them.
- **Actor-count increment at 500,000**, from cases 1 and 10. Measured only when
  both are measured, and published with the reconstructed separation between the
  two windows stated beside it. Case 1 runs first in the tier and case 10 runs
  last, so the whole tier lies between them. If a spike falls anywhere between
  the two windows, the increment is **inconclusive** even when both cases are
  themselves measured, because the pair is not a controlled comparison across
  that gap.

## What "insufficient samples" means here

Two primary samples per case is the memory tier's population, chosen so one
bounded invocation can reach both envelope depths. It buys less than a
distribution, and the page must not spend more than it bought.

- **Percentiles are unavailable, always.** The contract emits p95 only at 20
  samples and p99 only at 100. At two, neither exists. Write "unavailable", not
  a value, and not a silence.
- **No mean is published as a summary.** Publish both raw observations. A
  two-sample mean reads like a statistic and is only an average of two numbers.
  Where an average is unavoidable, name it as the average of the two samples and
  print both.
- **A range is not a bound.** Two samples give an observed pair, not a worst
  case. The page says "observed", as the memory lane already does.
- **One usable sample is not a measurement.** It is a single observation. Report
  it as one, classify the case inconclusive, and say what happened to the other
  sample.
- **A case with no usable sample is unavailable, and the reason is named.** Not
  available is different from zero, which is the rule the README already states
  for platform counters and which applies here in the same way.
- **Two samples cannot settle a claim about variability.** Nothing on the page
  may say a figure is stable, typical or representative on this evidence.

## Remedy for an inconclusive case

Rerun the whole tier at the same head, in one invocation, in a quieter window.
The tier has no per-case selection, so there is no way to rerun one case alone,
and a case reran on its own would not be the same run anyway.

If the second complete run also fails the quiet rule or the pair rule, publish
the case inconclusive and report both runs' observations. Do not discard the
unfavourable run and publish the favourable one. Do not merge samples from two
invocations into one range: they are separate runs, and the fan-out lane's
reason for discarding a disturbed campaign whole applies here too.
