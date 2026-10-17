---
date: 2026-10-16
updated: 2026-10-17
author: claude
status: implemented; outcome accepted by the requester on 2026-10-17
origin: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c4c2be71ea1a449f8f4819aa2897a2f26d501fcc
---

# One verified load per signing command

The request asked that a signing command (`gs ratify`, `state`, `artifact`,
`review-request` and the rest) stop paying a cold fold of the whole log each
time it runs. On 2026-10-16 those commands took 14–135 s each at artroom
depth.

Two changes have landed:

- **The faster fold**, request
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d1b3d1b1d8a481ad0bda38d7b137764e0a3a28d3`,
  landed as main `68fa76b3`. It removes the per-receipt recomputation that was
  75–85 % of each fold.
- **One verified world per command**, request
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:555f239cfad9d38d46016fb118161539235d8011`,
  landed as main `0e3b02a1` and was approved in
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f96945df2637ac549e89ddb7a98ef470981be08f`.
  Each signing command now verifies the log once and reuses that read for
  replay lookups and admission.

Every signing command measured now takes 7–10 s and makes no cold scan when
a checkpoint exists.

The requester settled the two open questions on 2026-10-17, from hugh's
decisions:

- **Condition 2.** One verified load per command meets it, as long as every
  actor signature is still verified:
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:637f7ac660a019cce750bc61080077fee2a678f7`.
- **The outcome.** It is delivered, measured against the baseline the request
  recorded:
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:345e6930a7e4da65f8e1762d09f622044e5795e1`.

The remaining cost is that one verified load. Going lower would mean trusting
the checkpoint in place of actor signatures, which the request keeps.

## Results

### Method

**Before.** Single runs of binary `3455950f` (main `add42789` plus the two
lanes that landed as main `b81a80cf`), on 2026-10-16. The load average was
8–10.

**After.** Binary `0e3b02a1` (`vcs.modified=false`), on 2026-10-17. Each value
is the median of three runs, and the spread within a row is at most 5 %. The
load average was 1.7–3.6.

The frozen local and frozen resident rows come from the step 1 lane's own
runs, of binary `8803f894`. Its tree is identical to `0e3b02a1`, and a
cross-check with `0e3b02a1` itself agreed within 1 %. The `status` row in the
frozen local table is from `0e3b02a1`.

**Load control.** Part of the gain comes from the lower load. To show how
much, the old binary `3455950f` was run once per case in the same session as
the after runs. It gives a before figure at equal load, in the column
"`3455950f` at today's load".

**Timing.** Wall time and peak resident set size come from
`/usr/bin/time -l`. Every run used a fresh APFS clone.

**Cold scans.** A cold scan is a `git rev-list` call with no base. The tables
count them in the measured process; on the resident route, only the client's
are counted.

**Repositories:**

- **frozen**: the artroom replay template at depth 43,744
  (head `b3a68ebe`), with a signed checkpoint at the head.
- **real**: APFS clones of `~/play/artroom`, with the resident advertisement
  and the remote removed. The before clone was at depth 45,092; the after
  clone at depth 49,211, head `c792bbb7`, with its checkpoint 9 events behind.
  No fold command ran in `~/play/artroom` itself, because a local snapshot
  there publishes a checkpoint, which is a write.

**Acts** (each was refused or accepted the same way before and after):

- `ratify` names a retired artifact and is refused before signing ("retired
  statement cannot be ratified").
- `state` is a promise with no request:
  - on the local route the preflight refuses it;
  - on the resident route it is sent with `--no-preflight` and recorded as an
    ineffective act.
- `artifact` (one path) and `review-request` are accepted, against a probe
  request, promise and commit made in the clone beforehand.

**Routes.** **local** is `--server -`. **resident** is a `gs serve` of the
same binary over the clone.

### Real checkout clone, local route

| command | outcome | before, `3455950f` | `3455950f` at today's load | after, `0e3b02a1` | cold scans before → after |
|---|---|---|---|---|---|
| `status --json` | accepted | 20.0 s, 1203 MB | 15.5 s | 9.2 s, 1229 MB | 0 → 0 |
| `ratify` | refused | 19.7 s, 693 MB | 15.2 s | 9.0 s, 690 MB | 0 → 0 |
| `state` | refused | 38.8 s, 699 MB | 22.1 s | 9.3 s, 692 MB | 1 → 0 |
| `artifact` | accepted | 57.9 s, 1180 MB | 55.1 s | 9.8 s, 707 MB | 4 → 0 |
| `review-request` | accepted | 103.4 s, 1285 MB | 54.6 s | 9.8 s, 718 MB | 4 → 0 |

The real clone cannot host a resident of its own. `gs serve` refuses there
because the live artroom resident already holds that workroom, before and
after, so there is no resident route for the real clone.

### Frozen copy, local route

| command | outcome | before, `3455950f` | `3455950f` at today's load | after | cold scans before → after |
|---|---|---|---|---|---|
| `status --json` | accepted | 15.3 s, 1150 MB | 12.6 s | 7.4 s, 1072 MB | 0 → 0 |
| `ratify` | refused | 14.4 s, 661 MB | 12.1 s | 7.3 s, 626 MB | 0 → 0 |
| `state` | refused | 22.1 s, 653 MB | 18.3 s | 7.3 s, 622 MB | 1 → 0 |
| `artifact` | accepted | 59.7 s, 1145 MB | 47.7 s | 8.7 s, 637 MB | 4 → 0 |
| `review-request` | accepted | 58.7 s, 1180 MB | 48.5 s | 8.6 s, 651 MB | 4 → 0 |

### Frozen copy, resident route

| command | outcome | before, `3455950f` | after | client cold scans before → after |
|---|---|---|---|---|
| `ratify` | refused | 28.6 s, 650 MB | 7.2 s, 624 MB | 0 → 0 |
| `state` | accepted, ineffective | 64.9 s, 1144 MB | 7.7 s, 640 MB | 1 → 0 |
| `artifact` | accepted | 86.2 s, 1080 MB | 8.5 s, 854 MB | 4 → 0 |
| `review-request` | accepted | 135.5 s, 1102 MB | 8.2 s, 866 MB | 4 → 0 |

Peak memory on the resident route is higher than on the local route. That is
because the command now streams the resident's 83 MB projection as well as
making its one local verified read.

### Frozen copy, no checkpoint (`GITSEQ_CHECKPOINT=off`), local route

| command | outcome | before, `3455950f` | after, `0e3b02a1` | cold scans before → after |
|---|---|---|---|---|
| `status --json` | accepted | 14.9 s, 984 MB | 9.2 s, 880 MB | 1 → 1 |
| `ratify` | refused | 14.1 s, 402 MB | 9.1 s, 390 MB | 1 → 1 |
| `state` | refused | 20.0 s, 415 MB | 9.0 s, 387 MB | 2 → 1 |
| `artifact` | accepted | 52.1 s, 1030 MB | 9.8 s, 538 MB | 6 → 1 |
| `review-request` | accepted | 51.7 s, 1106 MB | 9.7 s, 506 MB | 6 → 1 |

Without a checkpoint, the one remaining cold scan is the single verified
load. Before, the checkpoint saved nothing at this depth. Now it saves about
1–2 s per command.

### The request's own reference

The request recorded a single run of a refused `gs ratify --server`: 54.1 s
at head `89c4603b` and 55.5 s on main, on the frozen copy with no checkpoint.
That is one reference figure, not a median, and it is not part of the tables
above. The same command now takes 7.2 s on the resident route with a
checkpoint, and 9.1 s locally without one.

### Raw evidence

Kept in the workroom laptop's session scratch directory, under
`noteafter/` (after runs, controls, scripts and `table.md`), `lane8/runs2/`
(step 1 lane runs `h8803r1`–`h8803r3`) and `lane5/runs/` (before runs).

## Conditions

| # | condition | result |
|---|---|---|
| 1 | Wall time and peak RSS of ratify, state, artifact and review-request, on the real checkout and a frozen copy, before and after, naming every local fold on each path | Met. The tables above give before and after. The whole-log reads on each path before the change are listed under "Where the time went". After the change there is one per command, plus the two limits below. The real checkout has no resident route, because the live resident holds it. |
| 2 | No full verification of the log at artroom depth when a valid checkpoint exists, with a test that counts full scans | Met, as clarified in `637f7ac6`: one verified load per command and no cold scan. `TestSigningCommandsVerifyTheLogOnce` counts the reads and goes red if a second one returns. |
| 3 | Admission and preflight judge a log verified in this process at the head being signed against; no resident projection substitutes; a tampered checkpoint is refused | Met. Admission reuses a fold only where both the session fold and the session reader stand at exactly that head, and uses a private reader otherwise. `TestSigningCommandRefusesATamperedCheckpoint` pins the refusal. |
| 4 | Projections byte-identical to a cold fold at the same frontier | Met. Each lane compared its projection with a cold fold at the same frontier on artroom data and found them byte-identical. Step 1 checked after four kinds of accepted act on the frozen copy. |
| 5 | `go test ./...` passes | Met. The full suite passed on main `69018ecd`, which includes the faster fold, and at the step 1 head, whose tree is identical to main `0e3b02a1`. Hosted CI run 39057970464 passed on `0e3b02a1`. |

## Limits

These are recorded in the architecture page, layers 2 and 5:

- A chain of two or more acts makes a second read, for its scratch fold. The
  fold has no copy operation, and the checkpoint material a reader keeps lacks
  the signed intent (actor, `rests_on`, schema) that a record is folded from.
- In a long-lived process, a submitter seeded from the reader makes one full
  read each time a checkpoint falls due. This keeps the checkpoint cadence at
  one write per interval.

## Where the time went (historical, 2026-10-16, binary `3455950f`)

This is the analysis the two changes were built from. It describes the code
before them.

One verified load of the log cost 3–5 s of CPU, whether it came from the
checkpoint or a cold read. One fold of 43,744 records cost 5.5–10 s. Of that,
75–85 % was `foldState.unsettledCommitmentEvents`, which recomputed staleness
and the whole commitment projection for each of the 188 merge receipts. The
faster fold removed this.

The checkpoint did not help, for three reasons:

- the kernel re-verifies every actor signature and payload hash in it, by
  design;
- the checkpoint holds event material only, so every process replayed the
  fold from genesis;
- the cold path was no slower.

Each command then repeated the work. These were the whole-log reads on each
path. Step 1 removed all but the first:

- **Session or preflight snapshot** (`Workspace.Snapshot`): one verified load
  and one fold, in every command. The resident's projection was over the
  64 MiB response limit, so the step commands always fell back to this.
- **Retry lookup** (`kernel.PriorActs`): a cold `scanHead` with payloads,
  6–14 s each. `state` made one, `artifact` three and `review-request` two.
- **Pre-signing admission** (`AdmitState` → `admissionWorld`): a private
  reader with no checkpoint, so a cold scan, then a second fold and a render.
- **Exact-replay checks** (`kernel.CheckReplay`): a cold scan without
  payloads.
- **Chain preflight** (`scratchFold`): a second load and fold for chains.
  This one remains, as a limit.
- **Local submission** (`kernel.NewSubmitter`): its own checkpoint load.
- **After the act**: `gs state` took a second snapshot when no preflight had
  run.

The note considered three ways to get below one fold per process:

- **A persisted projection cache.** Rejected. It would let the sequencer key
  decide fold outcomes without the actors' keys, and it would outlive the
  binary that wrote it.
- **The resident's projection.** Ruled out by the request.
- **A faster fold.** Chosen; this became request `d1b3d1b1`.
