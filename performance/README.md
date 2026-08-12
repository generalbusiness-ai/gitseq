# Performance evidence

This directory defines Gitseq's opt-in performance evidence lane. It does not
run from `make test` and it does not turn one machine's timing into a product
guarantee.

The versioned contract fixes the logical workload before a run: generator
version and seed, required log depths, checkpoint tails, complete-operation
scenarios, concurrent reader/writer pair counts, and retained measurements. A prepared fixture records both a
logical digest and the exact Git materialization used by every compared
binary. Each measured sample gets fresh writable state because checkpoint
reads may advance the checkpoint ref.

Run a bounded local smoke sample with:

```sh
make perf PERF_ARGS='run --tier smoke'
```

Compare two exact commits with repeated, alternating samples with:

```sh
make perf PERF_ARGS='compare --base main --candidate HEAD --tier standard'
```

The comparison command refuses dirty source states, keeps setup outside the
runs at least two rounds per revision, writes raw newline JSON and Go
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
process boundary. The raw trace, which can contain local paths and command
arguments, is summarized and deleted; retained JSON contains only process
count and cumulative duration. Fixture evidence omits its local path and its
depth-sized head map. CPU and memory profiles are also diagnostic reruns
because both Trace2 and profiling perturb timings.

The scheduled workflow has read-only repository permissions, bounded inputs,
no secrets, and finite artifact retention. Large fixture generation is cached
by contract digest, generator version, seed, object format, and shape; it is
never repeated for every sample.
