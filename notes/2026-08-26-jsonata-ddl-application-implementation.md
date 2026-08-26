---
date: 2026-08-26
status: draft companion implementation design. It binds the first choices but
  does not authorize implementation.
companion_to: notes/2026-08-26-jsonata-ddl-application-interface.md@2511990b01d852db12700c7ee5aa21c5da292efb
origin: Hugh's request to validate an embedded database against the application
  SQL and account for the UI and resident-service boundary.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:264cbdd6dc30aad217eaf6c5af2ec14eb39eadd8
---

# First implementation of the JSONata-with-DDL platform

The first implementation should keep the two-file application unchanged. The
resident embeds the evaluator and database, owns one projection writer, and
serves bounded SQL queries to the application UI.

The choices are:

- **SQLite**, one derived database per workroom and application profile;
- for the current Go resident, the cgo-free
  [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) binding,
  pinned to an exact module version;
- SQLite WAL mode, one writer connection, and a small read pool;
- the native Go JSONata 2.0.6 implementation in
  [`jsonata-go/jsonata`](https://github.com/jsonata-go/jsonata), pinned and
  wrapped as a Gitseq language profile; and
- ordinary same-origin HTTP between an optional application UI and the
  resident. SQL is never the write interface.

DuckDB remains a possible analytical consumer, not the primary projection
store. Its embedded and analytical strengths are attractive, but Gitseq's
ordinary workload is many small ordered transactions with concurrent UI reads.
SQLite's WAL model directly supports one writer alongside readers; DuckDB
describes its concurrency as optimized for read-intensive analytical work.

## Database ownership

The database is a disposable application projection under the repository's
private cache, never a Git artifact or source of truth. Its identity record
contains the genesis, application identity, fold version, schema digest, and
exact projected head and depth.

On open, the resident:

1. reuses the database only when genesis, application, fold, and schema match;
2. replays a verified suffix when its recorded head is an ancestor of the
   verified head; and
3. discards and rebuilds it for any other mismatch.

A fold of event *n* runs in one `BEGIN IMMEDIATE` transaction: execute the
declared reads, evaluate JSONata, validate its result, apply current and
versioned row changes, record the decision and facts, advance the projection
frontier, and commit. Query readers see either the complete projection before
*n* or the complete projection after it. A fold or constraint failure rolls
back and leaves an explicit gap between the verified and interpreted
frontiers.

The physical database contains:

- application tables, indexes, and views under their declared names;
- platform relations for events, decisions, facts, and derivations;
- one hidden typed version relation for each writable application table; and
- the projection identity and frontier record.

Every writable table must have a primary key. `insert` and `upsert` carry a
complete row; `delete` carries its complete primary key. This makes row
identity, versioning, replay, and lineage unambiguous. Database-generated keys,
triggers, and nondeterministic defaults are not in the first profile.

## Compiling `application.sql`

The platform does not need a general SQL parser.

A small lexer separates statements while respecting SQL strings, quoted
identifiers, and comments. It recognizes only `CREATE EVENT` and `CREATE FOLD`.
All allowed ordinary DDL is prepared by SQLite itself in a scratch database.
`CREATE EVENT` is checked through the same SQLite column grammar, but recorded
in the event catalog rather than created as an application table. The focused
fold grammar extracts its event, named reads and cardinalities, JSONata path,
and writable tables; SQLite prepares every declared read against the completed
scratch schema and confirms that it is read-only.

The first DDL profile admits `CREATE TABLE`, `CREATE INDEX`, and `CREATE VIEW`,
with primary keys, uniqueness, foreign keys, and checks. It refuses `ALTER`,
triggers, virtual tables, attached databases, PRAGMAs, and extension loading.
Schema changes produce a new profile and a rebuild rather than an in-place
migration.

The first logical types are `TEXT`, `INTEGER`, `REAL`, `BLOB`, `BOOLEAN`, and
`JSON`. The platform validates JSONata values before binding them; SQLite
constraints are a second check. Integers are limited to JSON's exactly
representable range. Applications represent money as minor-unit integers and
timestamps as an explicitly checked `TEXT` or `INTEGER` convention; decimal
and temporal domain types can follow after their cross-language semantics are
settled.

This is enough for the interface note's inventory application. The main SQL
friction is not tables, joins, views, constraints, or atomic changes; it is
historical projection reads, which SQLite does not provide natively.

## Historical reads without a SQL dialect

Each hidden version relation repeats its table's typed columns and adds the
opening position, optional closing position, source event, and operation. The
writer closes the old version and opens the new one in the same transaction as
the current table change.

For a fold read at an earlier position, the platform uses a dedicated
read-only connection. In its temporary schema it creates:

1. a one-row selected-position relation;
2. a temporary view under each application table name, selecting the row
   version visible at that position; and
3. the application's declared views again in the temporary schema.

SQLite resolves the temporary names before the current application tables, so
the declared `SELECT` runs unchanged against the past projection. Current
fold reads use the ordinary tables. This needs an adversarial spike for nested
views and name resolution, but it avoids inventing `FOR SYSTEM_POSITION` or
rewriting arbitrary SQL.

## SQLite binding and query safety

The binding choice is deliberate. `ncruces/go-sqlite3` is cgo-free, supports
`database/sql`, and exposes the native connection controls the resident needs:
statement read-only inspection, an authorizer, connection limits, and
context-driven interruption. The more established pure-Go `modernc.org/sqlite`
is a reasonable fallback for trusted internal SQL, but its current driver
documentation says it exposes no authorizer.

Connections enable foreign keys and defensive settings and disable trusted
schema behavior and extension loading. Application-query connections also use
`query_only`, but do not rely on it alone. Their authorizer permits reads from
the application and public platform relations and denies writes, DDL, PRAGMAs,
`ATTACH`, `DETACH`, and unapproved functions.

Every query has bounds on SQL bytes, parameters, SQLite expression and opcode
limits, execution work, elapsed safety time, returned rows, returned bytes,
and concurrent query slots. Request cancellation interrupts SQLite. The work
bound determines a reproducible refusal; wall time is only an operational
circuit breaker and cannot turn an event ineffective.

WAL lets bounded readers proceed beside the fold writer, but long readers can
delay checkpointing. The query deadline and result bounds therefore protect
both HTTP capacity and database maintenance.

### Rust alternative

Rust has the more established SQLite binding. [`rusqlite`](https://github.com/rusqlite/rusqlite)
can bundle a pinned SQLite and directly exposes the authorizer, progress
handler, interruption, and runtime limits. It would be the first choice if the
resident were written in Rust. SQLx and Diesel add machinery this synchronous,
single-writer projection does not need, while making the low-level sandbox
controls less direct.

It is not a smaller choice for the present Go resident. Using `rusqlite` would
require either a resident rewrite, a native ABI with ownership and callbacks
crossing into Go, or a sidecar that no longer embeds the database in the
resident. If a native toolchain becomes acceptable, a direct Go SQLite binding
is the smaller comparison. The first implementation should therefore keep the
database behind a narrow internal interface, spike `ncruces/go-sqlite3`, and
change the Go binding if that spike fails rather than introduce Rust only as a
database adapter.

Rust also does not yet provide a more established combined SQLite-and-JSONata
stack. Stedi's `jsonata-rs` still describes itself as incomplete and alpha.
The newer [`jsonata-core`](https://github.com/txjmb/jsonata-core) reports full
reference-suite coverage and useful evaluation bounds, but its first published
versions appeared in 2026 and its host-extension surface still needs the same
profile and determinism validation as the Go evaluator. It is a worthwhile
language-platform spike, not yet a reason to move the resident boundary.

## JSONata profile

The application binds a Gitseq JSONata language version, never "latest". The
first candidate is the Go port's 2.0.6 profile. Before it is admitted, a
compatibility suite must establish the subset it actually implements against
the JSONata 2.0.6 reference; unsupported expressions are profile-load errors.

The Gitseq profile removes `$now`, `$millis`, `$random`, `$shuffle`, `$eval`,
and every ambient or dynamic-code facility. It adds only declared pure helper
functions. Input size, AST depth, recursion, ranges, intermediate collections,
evaluation steps, and output bytes have deterministic limits. The upstream Go
package already supplies depth, range, and wall-time controls; the adapter or a
small maintained fork must add deterministic step and allocation bounds and
must settle map iteration and numeric behavior. Wall-clock cancellation alone
is not a replay contract.

JSON numbers use finite IEEE-754 values and safe integers. Object key order is
not semantic, and any operation that turns object iteration into an array must
use the profile's canonical key order. The evaluator version, disabled
functions, limits, helper versions, and numeric rules all enter the fold
identity.

## Resident and UI interface

An application may add static UI assets, served by the resident under the same
origin. The UI reads projections and submits events; it neither opens the
database nor calls application helpers directly.

The generic service additions are small:

- application metadata: identity, fold version, event schemas, relational
  schema, views, and current verified and interpreted frontiers;
- a query call accepting SQL, bound parameters, and an optional expected
  frontier, and returning the exact frontier, typed columns, rows, and a
  truncation marker; and
- an event-submission call accepting an event type, payload, session
  credential, and idempotency key, returning its event identifier, decision,
  and resulting frontier.

The existing wait cursor is the invalidation channel. When the durable frontier
moves, an application UI re-runs the queries it cares about. The first version
does not need SQL subscriptions or row-diff streaming. A client that pages with
`LIMIT` and `OFFSET` supplies the prior expected frontier; if it moved, the
resident refuses the continuation and the client starts again.

Typed query results use `null` for SQL null, JSON booleans and safe numbers,
strings for text, structured JSON for declared JSON, and tagged base64 for
blobs. This contract, plus schema introspection, is enough to generate forms,
typed clients, table views, and agent-facing application guidance without
copying the fold into the UI.

The current resident is loopback-only and explicitly trusts local processes.
This design preserves that posture. A remote or mutually untrusted deployment
would require authentication, per-actor query authority or protected views,
and a different key-custody design; it is not unlocked merely by adding a SQL
endpoint.

## Validation before implementation

Three spikes should precede the platform build:

1. **SQL:** compile the inventory example, replay inserts/upserts/deletes,
   enforce constraints, query current and historical tables and nested views,
   and prove atomic frontier recovery after a simulated crash.
2. **Sandbox:** prove the application-query connection refuses every write,
   DDL, PRAGMA, attachment, extension, and unsafe function; cancels expensive
   queries; bounds results; and permits a reader while the fold transaction is
   open.
3. **JSONata and UI:** run the reference compatibility and determinism corpus,
   then serve a tiny inventory UI that discovers its schema, submits an event,
   waits for the frontier, and re-runs a SQL query.

Failure of the historical-view or query-sandbox spike is grounds to change the
driver or narrow the SQL surface. It is not grounds to add database concepts to
the sequencing kernel or enlarge the minimal application.

## Primary references

- [SQLite WAL and concurrency](https://www.sqlite.org/wal.html)
- [SQLite authorizer](https://www.sqlite.org/c3ref/set_authorizer.html),
  [runtime limits](https://www.sqlite.org/c3ref/limit.html), and
  [query interruption](https://www.sqlite.org/c3ref/progress_handler.html)
- [`ncruces/go-sqlite3` package](https://pkg.go.dev/github.com/ncruces/go-sqlite3)
  and [`database/sql` driver](https://pkg.go.dev/github.com/ncruces/go-sqlite3/driver)
- [`modernc.org/sqlite` package](https://pkg.go.dev/modernc.org/sqlite)
- [`rusqlite`](https://github.com/rusqlite/rusqlite) and its
  [connection controls](https://docs.rs/rusqlite/latest/rusqlite/struct.Connection.html)
- [Stedi `jsonata-rs`](https://github.com/Stedi/jsonata-rs) and
  [`jsonata-core`](https://github.com/txjmb/jsonata-core)
- [DuckDB concurrency](https://duckdb.org/docs/current/connect/concurrency)
- [Go JSONata 2.0.6 API](https://pkg.go.dev/github.com/jsonata-go/jsonata/v206)
  and [JSONata release history](https://github.com/jsonata-js/jsonata/releases)
