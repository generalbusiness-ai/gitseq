# Resident wait cost: the shared head clock

This page records the before and after measurements for the shared per-log
head clock in `internal/service`. Before it, every open `/v0/wait` and
`/v0/actor-wait` ticked its own 250 ms clock and asked the verified snapshot
on every tick, and the snapshot's currency check is one `git rev-parse`
process. After it, one clock per log reads the head ref every 250 ms while
any wait is open, and a waiter asks the snapshot again only on its first
pass, when that clock advances, or when its live cursor moves.

These are small-depth warm figures from one machine (`darwin/arm64`,
`go1.27.0`, see the JSON). They say nothing about depth 20,000 or about
physical resources, and the poll timeout of a timeout scenario is the poll
timeout, not a latency.

## How the figures were taken

`internal/service/headwatch_evidence_test.go` is the opt-in lane:

```sh
GITSEQ_HEAD_WAIT_EVIDENCE=/tmp/head-wait.json \
  go test ./internal/service -run '^TestHeadWaitEvidence$' -count=1
```

It builds a fresh repository at depths 1, 31 and 300 and, at each depth,
runs one, eight aligned, eight staggered (23 ms apart) and thirty-two
staggered waiters against an unchanged frontier for 1.1 s, three repeats
each, plus eight staggered waiters idling through a 5 s poll once. It then
measures eight open waits woken by one append from a second workspace on the
same repository (external durable change), eight open waits woken by one
presence announcement (live-only change), and eight readers opening a fresh
workspace together (cold read). Git processes are counted only inside each
measurement window, through the resident's own observer.

The before run compiled the same harness against main `1fba3d55` in a
detached worktree ([head-wait-before.json](head-wait-before.json)); the after
run is the working tree of the commit that carries this page
([head-wait-after.json](head-wait-after.json)).

## Results

Medians of the three repeats; the idle scenario ran once. "ref reads" counts
`git rev-parse` processes in the window.

| depth | scenario | waiters | ref reads before | ref reads after | wake or read ms before | wake or read ms after |
|---|---|---|---|---|---|---|
| 1 | cold-read/eight-readers | 8 | 0 | 0 | 150 mean, 281 slowest | 132 mean, 283 slowest |
| 1 | timeout/eight-aligned | 8 | 5 | 6 | timeout | timeout |
| 1 | timeout/eight-staggered | 8 | 40 | 12 | timeout | timeout |
| 1 | timeout/eight-staggered-idle-5s | 8 | 160 | 28 | timeout | timeout |
| 1 | timeout/one | 1 | 5 | 6 | timeout | timeout |
| 1 | timeout/thirty-two-staggered | 32 | 76 | 40 | timeout | timeout |
| 1 | wake/external-durable | 8 | 4 | 6 | 374 | 396 |
| 1 | wake/live-only | 8 | 2 | 4 | 66 | 78 |
| 31 | cold-read/eight-readers | 8 | 0 | 0 | 146 mean, 279 slowest | 146 mean, 281 slowest |
| 31 | timeout/eight-aligned | 8 | 5 | 6 | timeout | timeout |
| 31 | timeout/eight-staggered | 8 | 40 | 12 | timeout | timeout |
| 31 | timeout/eight-staggered-idle-5s | 8 | 160 | 29 | timeout | timeout |
| 31 | timeout/one | 1 | 5 | 6 | timeout | timeout |
| 31 | timeout/thirty-two-staggered | 32 | 76 | 40 | timeout | timeout |
| 31 | wake/external-durable | 8 | 4 | 6 | 375 | 395 |
| 31 | wake/live-only | 8 | 2 | 4 | 66 | 71 |
| 300 | cold-read/eight-readers | 8 | 0 | 0 | 178 mean, 322 slowest | 165 mean, 306 slowest |
| 300 | timeout/eight-aligned | 8 | 5 | 6 | timeout | timeout |
| 300 | timeout/eight-staggered | 8 | 40 | 13 | timeout | timeout |
| 300 | timeout/eight-staggered-idle-5s | 8 | 160 | 29 | timeout | timeout |
| 300 | timeout/one | 1 | 5 | 6 | timeout | timeout |
| 300 | timeout/thirty-two-staggered | 32 | 76 | 39 | timeout | timeout |
| 300 | wake/external-durable | 8 | 4 | 6 | 382 | 403 |
| 300 | wake/live-only | 8 | 2 | 4 | 61 | 80 |

## Reading them

- **Idle waiters.** Eight staggered waiters sitting through a 5 s poll cost
  160 ref reads before and 28 after: eight for their own first snapshots,
  twenty for the clock's ticks, and one baseline read when the clock
  started. Thirty-two staggered waiters over 1.1 s fell from 76 to about 40,
  which is thirty-two first reads plus the clock. The per-waiter cost is now
  one read at entry; the per-second cost is the clock's four, however many
  waits are open.
- **Aligned waiters** were already coalesced by the single flight and are
  unchanged apart from the clock's baseline read (5 to 6).
- **External durable change** wakes in about 390 ms after against about 375
  before: the clock notices within one 250 ms tick and broadcasts, and the
  waiter re-reads the snapshot on that broadcast. Without the broadcast the
  waiter's own tick stacked on the clock's and the figure was about 640 ms;
  that version was measured and rejected.
- **Live-only change** wakes in 60 to 80 ms either way, on the waiter's
  in-memory tick.
- **Cold read** is unchanged: one audit serves eight readers.

## What this run does not establish

It does not measure depth 20,000, resident memory, or behaviour under a
rewound or invalid head beyond the correctness tests in
`headwatch_test.go`, which show a rewind refused as a verified-frontier
rollback and a deleted ref surfaced as the snapshot's own error, both within
one clock tick.
