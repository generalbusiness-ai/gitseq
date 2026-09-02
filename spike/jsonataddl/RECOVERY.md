# Crash recovery and the historical view: spike findings

This page records what the crash sweep and the historical-view exercise in
this spike actually establish, point by point, including the limits of the
crash model. The tests are `TestCrashSweepRecoversAtomicFrontier`,
`TestCrashSweepRecordsGapAtomically`, and the `TestHistorical*` tests.

## Crash model

A recording VFS wraps the operating-system VFS under the projection writer
and captures every durable mutation in order: WAL and main-database writes,
the creation-window rollback-journal writes, truncations, and file
deletions. Because the on-disk state changes only at those mutations, the
sweep replays the first k mutations onto empty files for every k — plus a
torn variant that applies half of the next write — and opens each image
cold. This reproduces the exact bytes a killed process leaves at every
possible instant, including the instants between two WAL frame writes of a
single commit.

The model is process death: completed writes persist, in order. It does not
model an operating-system or power crash, which can lose or reorder writes
that were never synced.

## Interruption points

Every image must recover to exactly one clean prefix projection — a byte-level
logical match of every table, hidden version relation, decision, and fact —
with the recovered frontier naming that prefix. The sweep verified 398 images
over 201 mutations for the clean replay and 204 images over 103 mutations for
the gap replay.

| Interruption point | Result |
| --- | --- |
| Mid-transaction, before the commit flush | **Atomic.** The writer defers every durable write to commit, so these instants leave no trace; each equals an already-verified write boundary. |
| After WAL data-frame writes, before the commit frame — including a torn commit frame | **Atomic.** Recovery discards frames past the last valid commit; the frontier and all row changes revert together to the previous event. |
| After the commit frame, before the frontier advance | **Structurally absent.** The frontier row is updated inside the same transaction as the row changes, so no such instant exists. The mutation proof below shows the sweep detects this point if the transaction is ever split. |
| During checkpoint — backfill page writes, torn main-database pages, WAL truncation; exercised mid-replay via `wal_checkpoint(TRUNCATE)` and at close | **Atomic.** WAL replay repairs every partial backfill state. |
| During initialization, before the single initialization commit | **Recoverable by discard.** The frontier relation is absent, an unambiguous rebuild signal for a disposable projection, and `Build` refuses to reuse an existing path. This required a driver change: initialization previously ran one autocommit transaction per DDL statement, and a crash inside that window left a partial schema carrying a plausible frontier row. Initialization is now one transaction. |
| After the last event's commit, before the completion marker | **Consistent, but `complete` is 0.** The completion flag commits in its own transaction after the last event. A crash between them leaves a fully interpreted projection not marked complete. Benign — the frontier equals the verified depth and every row matches — but it is a separate commit, recorded here as a design fact rather than folded into the atomicity claim. |
| After a failed fold's rollback, before the gap metadata commits | **Atomic state, lagging metadata.** The failed transaction leaves no trace at any instant. Until the gap update's own commit, the image is the clean prefix with the gap merely undiscovered; replaying the suffix re-derives it. |
| Operating-system or power crash (unsynced writes lost or reordered) | **Untestable at this driver seam.** The recording VFS observes completed writes in order; it cannot lose or reorder what SQLite handed to the OS. Under WAL with `synchronous=NORMAL`, power loss can lose recently committed transactions — a durability bound, not an atomicity defect — and sector-level tearing beyond the half-write variant is unmodeled. Proving power-loss behaviour needs a sync-aware fault model or hardware-level testing. |

## Mutation proofs

Two seam mutations confirm the tests fail where it matters, with the tree
restored byte-identical (verified by `git hash-object`) and green afterwards.

1. Frontier advance moved outside the fold transaction (commit rows first,
   update `gitseq_frontier` in a second transaction). The sweep fails at the
   image between the two commits:

   `recovery_test.go: crash image before op 57 (torn=false): recovered state
   does not match the clean prefix projection at its recovered frontier 1`

   — the recovered dump shows the event's row changes and decision present
   while the frontier still names the previous position.

2. Replaced row versions never closed in the version relation. The
   historical exercise fails at the first replaced row:

   `history_test.go: historical read at position 3 diverges from the clean
   prefix projection ... got: [["ink" 5] ["ink" 3]] want: [["ink" 3]]`

## Historical view

The exercise follows the note's temporary-schema design. At every
interpreted position, the unchanged application SQL — base tables, and a
view nested over another view — returns exactly what a clean projection
built from only that prefix returns through the ordinary current-table path.
Deletes are covered: a cancelled reservation is visible precisely from its
insert position to the position before its delete.

Adversarial name-resolution facts, tested rather than assumed:

- Unqualified names, including names inside re-created nested views, resolve
  to the temporary shadow.
- Schema-qualified names (`main.stock`, `main.sku_summary`) escape the
  shadow by SQLite's resolution rules. The historical authorizer refuses
  them, so they error instead of silently answering with current rows.
- The historical authorizer must admit reads of the hidden version relations,
  because the shadow views are defined over them and SQLite presents both
  routes as the same authorizer action. A direct read-only query against a
  version relation therefore succeeds on a historical connection. Hiding it
  would need statement vetting; the current-read connection still refuses
  every `__gitseq_` name.

## Boundary

Everything above was reached inside the projection driver: version relations,
the single-transaction initialization, the temporary-schema shadow, and the
crash sweep's VFS seam. No change to the sequencing kernel, the host log
format, or the two-file application was needed, so the note's stop rule —
failure here may change the driver or narrow the SQL surface, never teach the
kernel about the database — was not triggered.
