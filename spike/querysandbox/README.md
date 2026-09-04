# Application-query sandbox spike

This isolated spike tests the query boundary proposed in
[`notes/2026-08-26-jsonata-ddl-application-implementation.md`](../../notes/2026-08-26-jsonata-ddl-application-implementation.md).
It uses the pinned `ncruces/go-sqlite3` binding directly. It is not wired into
the resident, the application profile, or the sequencing kernel.

Architecture judgment: this changes none of the seven production layers in
`docs/reference/architecture.md`. It is an experiment beside them under
`spike/`; in particular it neither imports into nor teaches database meaning to
the kernel. The architecture reference therefore needs no change in this head.

The result supports the proposed binding and a deliberately narrow SQL read
surface. The engine authorizer is a default-deny allowlist: only `SELECT`,
recursive query control, reads from named public relations, and eight named
aggregate/null functions pass. An unknown future authorizer action therefore
denies.

## What enforces each refusal

| Refusal class | Primary enforcement | Other enforcement in the spike |
| --- | --- | --- |
| Writes | SQLite authorizer | Read-only open, `query_only`, and prepared-statement `ReadOnly` check |
| DDL | SQLite authorizer | Read-only open, `query_only`, and prepared-statement `ReadOnly` check |
| PRAGMA | SQLite authorizer | None. Setup PRAGMAs run before the authorizer, so no application PRAGMA exception is needed. |
| `ATTACH` and `DETACH` | SQLite authorizer | `SQLITE_LIMIT_ATTACHED` is zero and the connection is read-only. |
| Extension loading | SQLite authorizer rejects `load_extension` because it is not an allowed function | `SQLITE_DBCONFIG_ENABLE_LOAD_EXTENSION` is false. |
| Unsafe functions | SQLite authorizer function allowlist | Nothing else. Adding a safe function requires changing this allowlist. |

The tests execute representative statements from every class and require an
intended refusal rather than a deadline cancellation. They separately walk
every authorizer action currently exposed by the pinned binding. A future or
unknown action is denied by the same default branch. The mutation proof
requires SQLite's specific authorization-denied result for a PRAGMA, then opens
a connection with authorizer installation actually disabled: the PRAGMA is
admitted, rather than merely returning some unrelated error. It opens a third
connection with the guard restored and checks the assertion again.

## Work, result, and concurrency bounds

SQLite runtime limits bound SQL bytes, value bytes, columns, expression depth,
compound selects, prepared opcodes, function arguments, attachments,
variables, and worker threads. A 5 ms context deadline reaches SQLite's
progress callback through `SetInterrupt`; the expensive recursive-query proof
is interrupted. The cancellation mutation opens the same sandbox with the
deadline decision disabled, observes the named cancellation assertion fail,
then restores the decision and checks it again.

The host wrapper returns no more than 32 complete rows or 8 KiB of SQLite value
data and marks a clipped result as truncated. These two result caps are Go
wrapper checks, not SQLite authorizer decisions or full process-memory quotas.

Authorization, result-bound, and reader-concurrency tests use the spike's
unexported options seam to disable only the internal wall-clock deadline. This
keeps their semantic assertions independent of scheduler delay while leaving
the public `Open` path and its 5 ms production default unchanged. A repeated
regression poisons the unused test deadline with a one-nanosecond value, so
accidentally re-enabling it fails deterministically instead of depending on a
duration threshold. Cancellation behavior and its mutation proof remain
separate tests with controlled deadlines.

In WAL mode, a query reader completes while a separate `BEGIN IMMEDIATE` fold
transaction is open. It sees the last committed value, never the writer's
uncommitted value, and sees the new value after commit. The test uses one
serialized application-query connection; a production pool and concurrent
slot limit remain unproved. A reader can still pin a WAL snapshot until its
deadline and delay checkpoint progress for that period.

## What this does not close

- The allowlist proves connection authority, not that every permitted query is
  cheap. The deadline is elapsed-time cancellation, not a deterministic
  SQLite step budget. `SQLITE_LIMIT_VDBE_OP` limits prepared program size, not
  executions of a small loop.
- `Query` materializes a complete row before applying `MaxResultBytes`. A row
  containing four 60 KiB values measured 264,352 bytes, or 32.3 times the
  8 KiB cap, before rejection. Under the existing 32-column and 64 KiB value
  limits, the dominant SQLite value-byte term can reach roughly 2 MiB. The
  smaller Go slice, string, allocator, and JSON-encoding overhead sits beside
  that term, so the cap is not a memory quota.
- The spike exposes relations by exact name. It does not design per-actor or
  row-level query authority for a mutually untrusted remote deployment.
- It proves that the SQL `load_extension` entry point is blocked before a file
  is opened. It does not ship a native extension fixture and attempt a
  successful dynamic load in a deliberately weakened process.
- A single serialized reader proves writer/reader coexistence, not read-pool
  sizing, admission fairness, checkpoint policy, or HTTP cancellation.

These gaps narrow the production contract; they do not require database
concepts in the sequencing kernel or a larger minimal application.

Run the focused proof with:

```text
go test ./spike/querysandbox -count=1
```
