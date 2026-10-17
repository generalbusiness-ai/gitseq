---
date: 2026-10-16
author: claude
status: candidate design; measured, not implemented, awaiting review
origin: request git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c4c2be71ea1a449f8f4819aa2897a2f26d501fcc
---

# One fold per signing command

The request asks that a signing command stop paying a cold fold of the whole
log each time it runs. This note records what each signing command costs
today, where the time goes, and why the outcome cannot be met inside the
current trust model. It proposes two steps. Step 1 needs no new format and
no new trust. Step 2 needs a decision before anyone builds it.

## Measurements

Binary: built from `3455950fc` (main `add427893` plus lanes 4 and 1, which
have since landed as main `b81a80cf0`). Machine: the workroom laptop, under
other agents' load (load average 8–10), so single runs vary by about 30 %.
Wall time and peak resident set size come from `/usr/bin/time -l`. Every run
used a fresh APFS clone, so no run warmed the next.

Repositories:

- **frozen**: the artroom replay template, depth 43,744, signed checkpoint
  at the head.
- **frozen, no checkpoint**: the same with `GITSEQ_CHECKPOINT=off`.
- **real**: an APFS clone of `~/play/artroom` taken at 16:2x on 2026-10-16,
  depth 45,092, checkpoint at depth 45,091 (one behind), resident
  advertisement and remote removed. I did not run fold commands in
  `~/play/artroom` itself: with the checkpoint one event behind the head,
  any local snapshot there publishes a new checkpoint ref and pointer, which
  is a write. The clone holds the same bytes.

Routes: **local** is `--server -`. **resident** is my own `gs serve` on
port 7805 over the clone, at the same head, so the fold preflight runs. The
real clone could not host one: `gs serve` refused because the live artroom
resident already holds that workroom. **dead** is `--server
http://127.0.0.1:1`, which skips the preflight and then fails to connect, so
it measures only what the client builds before sending.

Acts: `ratify` names an effective artifact and is refused before signing.
`state` is a promise with no request and an idempotency key. It is refused
by the preflight on the local route, and sent (and accepted as ineffective)
on the resident route with `--no-preflight`. `artifact` (one path) and
`review-request` succeed against a probe request, promise and commit made in
the clone beforehand. In the table, `state` is refused on every local and
dead route and accepted on the resident route.

| command | frozen local | frozen, no checkpoint, local | real local | frozen resident | frozen dead | real dead |
|---|---|---|---|---|---|---|
| `status --json` | 15.3 s, 1150 MB | 14.9 s, 984 MB | 20.0 s, 1203 MB | — | — | — |
| `ratify` (refused) | 14.4 s, 661 MB | 14.1 s, 402 MB | 19.7 s, 693 MB | 28.6 s, 650 MB | 13.8 s, 656 MB | 17.0 s, 712 MB |
| `state` | 22.1 s, 653 MB | 20.0 s, 415 MB | 38.8 s, 699 MB | 64.9 s, 1144 MB | 15.0 s, 558 MB | 17.6 s, 546 MB |
| `artifact` | 59.7 s, 1145 MB | 52.1 s, 1030 MB | 57.9 s, 1180 MB | 86.2 s, 1080 MB | — | — |
| `review-request` | 58.7 s, 1180 MB | 51.7 s, 1106 MB | 103.4 s, 1285 MB | 135.5 s, 1102 MB | — | — |

Raw results, wall-clock stack samples and CPU profiles are in the lane's
scratch directory (`lane5/runs/`).

Two facts stand out. The checkpoint saves nothing at this depth: every
command is as fast or faster with `GITSEQ_CHECKPOINT=off`. And the
expensive commands are expensive because they fold or scan the whole log
several times in one process.

## Where the time goes

One verified load of the log costs 3–5 s of CPU, whether from the
checkpoint or cold. One fold of 43,744 records costs 5.5–10 s, and 75–85 %
of that is `foldState.unsettledCommitmentEvents`, which recomputes staleness
and the whole commitment projection for each of the 188 merge receipts.
Rendering the projection costs about 0.2 s.

The checkpoint is found and used. It does not help because:

1. The kernel re-verifies every actor signature and payload hash in the
   checkpoint, and does so twice: once to authenticate the candidate, and
   again when it streams the events to the fold. That is by design: the
   kernel checkpoint must not let the sequencer key stand in for actor
   signatures.
2. The kernel checkpoint holds event material only, never projection state
   (architecture, layer 2, "Checkpoints and streamed reads"; the table entry
   for `internal/kernel`). So every process replays the fold from genesis.
3. The cold path reads objects through one `cat-file` batch and verifies each
   signature once, so it is no slower than the checkpoint path.

Each command then repeats that work. These are all the whole-log reads on
each path, from the stack samples:

- **Session or preflight snapshot**, `Workspace.Snapshot`: one verified load
  plus one fold. All commands. The step commands ask the resident first, but
  its projection is larger than the 64 MiB response limit, so they fall back
  to this local load every time.
- **Retry lookup**, `kernel.PriorActs`: a cold `scanHead` of the whole log
  with payloads, 6–14 s each, called once per question:
  `state` with a key (preflight); `artifact` three times
  (`refuseRetiredReplay`, `prospectiveChain`, `preflightAdmission`);
  `review-request` twice (`preflightAdmission`, then
  `acceptedRequestReplay` for the request it files).
- **Pre-signing admission**, `AdmitState` → `admissionWorld`: a private
  reader with no checkpoint, so a cold scan, plus a second full fold and a
  render. `state`, `artifact` and `review-request`. This is the fold
  `gs state --no-preflight` still pays.
- **Exact-replay checks**, `kernel.CheckReplay`: a cold scan without
  payloads, on the paths with a mutable precondition and on guarded
  retirements.
- **Chain preflight**, `scratchFold`: a second checkpoint load and full fold
  for any chain of two or more acts (not reached by the one-path artifact
  measured here).
- **Local submission**, `kernel.NewSubmitter`: its own checkpoint load
  (about 4 s, no fold). Local route only.
- **After the act**, `gs state` takes `Workspace.Snapshot` again to note dead
  bases. When no preflight ran (the resident route with `--no-preflight`),
  this is a further load and fold.

On the resident route, the client also waits 19–25 s for the resident's own
admission; that is the resident's cost, not a local fold.

## Why the outcome needs a decision

The fastest a signing command can be today is one verified load plus one
fold: about 14–20 s at this depth. A cold fold (`status` with the checkpoint
off) is 14.9 s. So removing every duplicate gets each command down to about
one cold fold, which is a large gain for `state`, `artifact` and
`review-request`, but it is not "much less than a cold fold".

To get below one fold per process, a process must start from fold state it
did not compute. There are three ways, and each needs review:

- **A persisted projection cache.** This is a new persistent format, and it
  changes the trust model. A local cache of fold state, even one signed with
  the sequencer key like the checkpoint, would let anyone holding the
  sequencer key decide fold outcomes without the actors' keys. Today the
  kernel re-verifies every actor signature on every load precisely so the
  sequencer key cannot do that. A persisted cache also outlives the binary
  that wrote it: the projection profile is advanced by hand, so a fold change
  that forgets to advance it would reuse a cache folded under other rules.
  Today that hazard ends with the process. If adopted, the cache must at
  least be keyed on the build's VCS revision as well as the profile, carry the
  verified head and depth it was folded at, and be accepted only after the
  kernel has verified the log up to that head.
- **The resident's projection.** The request rules this out, and rightly: it
  would make the resident's answer the authority for admission.
- **A faster fold.** Most of the fold is one function,
  `unsettledCommitmentEvents`, recomputed per merge receipt. Making it
  incremental would make every fold cheaper, cold or warm, with no new format
  and no new trust. It is fold work, which lane 4 and the 2026-09-04 fold
  simplification study already touch, and it changes no projection bytes if
  done correctly.

## Proposal

**Step 1: one verified world per process.** No new format, no new trust.
It changes one architecture contract sentence, so it needs review as a
contract change.

1. Answer the retry lookup and the exact-replay check from the workspace
   reader's verified dedup index (checkpoint plus verified suffix), at the
   head the snapshot verified. Read the payload of each matched event from
   Git and check it against the signed payload tree before returning it.
   This removes every cold `scanHead` from the signing paths.
2. Let the pre-signing half of admission use the reader's verified fold when
   that fold stands at exactly the head being judged, and its private reader
   otherwise. Let the authoritative half do the same only when the reader's
   fold is already at that exact head; it never takes a new reader snapshot.
   A fold at a head is the same projection whatever read produced it, and
   the rollback witness can only have refused a head that then has no
   snapshot, so the private path is still taken there. Give the private
   reader the checkpoint to consume but not to publish. This changes
   architecture layer 5, "Write-boundary guards", which now says the private
   world "shares nothing with reader snapshots, checkpoint cadence, or the
   rollback witness".
3. Seed the local submitter from the reader's verified log instead of a
   third load.
4. Leave `scratchFold` as a second fold for chains of two or more acts until
   the fold can be copied; record that cost.

Expected: `ratify` and refused `state` unchanged (one load, one fold);
`state` through a resident from 65 s to about 15–20 s; `artifact` and
`review-request` from 52–135 s to about 15–20 s for one path. No cold scan
remains when a checkpoint exists, which a test can pin by counting the
kernel's full scans across one command. Projections stay byte-identical,
because the fold and its inputs do not change.

**Step 2: below one fold.** Choose between the faster fold and a persisted
projection cache. I recommend the faster fold first, as its own request: it
needs no new trust, and it helps the resident's cold start and every reader
too. A persisted cache should be considered only if the fold cannot be made
small enough, and only with the safeguards listed above.

The faster fold is now its own request,
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d1b3d1b1d8a481ad0bda38d7b137764e0a3a28d3`,
filed on 2026-10-17. This note asks review only of step 1.

## Conditions, against this proposal

1. Measured above, with every local fold named. The "after" figures wait for
   an implementation.
2. Step 1 meets it: no full scan when a valid checkpoint exists.
3. Step 1 meets it: admission and preflight judge a log verified in this
   process at the head being signed against; the existing checkpoint
   authentication refuses a tampered checkpoint, and a test on the signing
   path should pin that.
4. Step 1 meets it, since no fold input changes.
5. To be shown at the implementation head.

The outcome, "much less than a cold fold", needs step 2.
