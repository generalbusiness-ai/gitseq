# Performance evidence

This directory defines Gitseq's opt-in performance evidence lane. It does not
run from `make test` and it does not turn one machine's timing into a product
guarantee.

The [500k memory salvage measurements](500K-SALVAGE.md) record the isolated
keep-or-drop evidence for the current checkpoint and decode reductions.

The versioned contract fixes the logical workload before a run: generator
version and seed, required log depths through 500,000 records, actor counts,
dependency fan-outs, checkpoint tails, complete-operation scenarios,
concurrent reader/writer pair counts, and retained measurements. A prepared fixture records both a
logical digest and the exact Git materialization used by every compared
binary. Each measured sample gets fresh writable state because checkpoint
reads may advance the checkpoint ref.

Contract v2 gives dependency fan-out its own ordered axis at depth 1,000. Its
widths 1, 8, 16, 64, and 256 run as one consecutive block; the width-one case
is the temporal denominator and is not duplicated in the ordinary depth axis.
Evidence retains the five separate distributions and reports the signed ratio
and signed millisecond increment from the width-one median. The contract's
10-percent relative limit yields separate PREVIEW-through-64 and
FIRST-PRODUCTION-through-256 verdicts. It does not claim that individual
samples are paired or that a ratio removes arbitrary load changes.

Run a bounded local smoke sample with:

```sh
make perf PERF_ARGS='run --tier smoke'
```

Run only the full-population consecutive fan-out block (five warmups and 100
recorded repetitions at every width) with:

```sh
make perf PERF_ARGS='run --tier fanout'
```

Run the bounded resident-memory axis with two fresh-process `cold_status`
samples at each contract depth through 500,000 records with:

```sh
make perf PERF_ARGS='run --tier memory'
```

This tier keeps the depth range consecutive and excludes the other scenarios,
actor counts, projection shapes, and checkpoint cases. It exists so the 500k
resident bound can be measured without running the much larger full contract.

## Retained runs

A run writes into `evidence/`, which is untracked working space and is deleted
with the worktree that produced it. That is the right default for exploratory
runs and the wrong one for a run a page publishes a verdict from: the first
fan-out campaign was lost exactly that way, leaving a reference page citing
distributions nobody could consult.

So a run whose numbers reach a page is copied into `retained/<run-id>/` before
review, and that copy is exactly three files:

- `evidence.json` — the harness evidence document, which carries the contract,
  the environment, the fixture and contract digests, the exact head, and the
  harness's own latency distributions and axis summary;
- `samples.jsonl` — every raw sample, one record per line;
- `candidate.bench` — the primary samples in Go benchmark format.

No separate derived file is retained. The harness's own summaries travel inside
`evidence.json`, and any further statistic a page computes for itself is
recomputable from `samples.jsonl`, so a fourth file holding those numbers would
only be one more thing that can drift away from the samples it describes.
Anything a reader needs to know about how a run was conducted belongs in the
page that publishes the verdict, where it is reviewed, rather than in a note
beside the data.

Profiles, traces and fixtures stay out. The first two are diagnostic reruns
rather than primary evidence, and fixtures are large and reproducible from the
contract.

Retention stays a deliberate decision about one run, not an automatic
consequence of running the harness. The ignore rules above are unchanged, which
means copying the files in is not enough on its own: `*.bench` is ignored, so
an ordinary `git add` of the copied directory silently leaves `candidate.bench`
untracked. Add that one file explicitly:

```text
mkdir -p retained/<run-id>
cp evidence/evidence.json evidence/samples.jsonl retained/<run-id>/
cp evidence/candidate.bench retained/<run-id>/
git add retained/<run-id>/evidence.json retained/<run-id>/samples.jsonl
git add -f retained/<run-id>/candidate.bench
```

Then check that the directory holds the three files and nothing else, because
the ignore rules will not tell you what they dropped:

```text
git ls-files retained/<run-id>
```

Compare two exact commits with repeated, alternating samples with:

```sh
make perf PERF_ARGS='compare --base main --candidate HEAD --tier standard'
```

The comparison command refuses dirty source states, keeps setup outside the
measured operation, runs at least two rounds per revision, writes raw newline JSON and Go
benchmark-format files, and
runs `benchstat` when the pinned tool is available. Baseline acceptance is a
reviewed source change: the command refuses automatic `--accept-baseline`
writes so a run cannot silently bless its own numbers.

## What the words mean

- **Cold** means a fresh process and application instance. The runner does not
  claim to flush the operating system's filesystem cache.
- **Warm** means the same resident has already completed the corresponding
  verified read.
- **Setup** includes fixture restoration and process preparation. It is kept
  and reported, but excluded from the operation latency.
- **Bounded soak** stops at the first of the contract's operation or elapsed
  time ceilings and records the number of operations actually completed.
- **Not available** is different from zero. Platform counters that cannot be
  read are recorded with a reason.
- p95 and p99 are emitted only when the contract's minimum sample population
  makes them meaningful. Smaller runs retain every raw sample and the maximum.

Git child-process evidence comes from `GIT_TRACE2_EVENT` in a separate
diagnostic pass. That observes all inherited Git commands without adding a
production hook or pretending `internal/gitstore.Store.run` is the only
process boundary. Each diagnostic result carries the process count and
cumulative duration required by the contract. The raw trace, which can contain
local paths and command arguments, is summarized and deleted. Fixture evidence omits its local path and its
depth-sized head map. CPU and memory profiles are also diagnostic reruns
because both Trace2 and profiling perturb timings.

The scheduled workflow has read-only repository permissions, bounded inputs,
no secrets, and finite artifact retention. Large fixture generation writes the
synthetic signed commits as one Git pack, then uses ordinary verification when
a sample runs. Fixtures are cached by contract digest, generator version,
seed, object format, shape, and actor count; they are never repeated for every
sample.
