# JSONata and DDL application spike

This spike loads one application profile from exactly two application files:
[`inventory/application.sql`](inventory/application.sql) declares events,
tables, named fold reads and write authority; and
[`inventory/folds/inventory.jsonata`](inventory/folds/inventory.jsonata)
defines the fold.

It demonstrates a deliberately small end-to-end path:

- `host.Open` verifies an application-bound Git sequence and recovers its
  immutable payloads from the Git object database;
- each known event is schema-checked and reads the n-1 SQLite projection
  through a dedicated read-only connection. The admitted fold-read language
  is only a named-column equality lookup by the table's complete primary key;
  a default-deny authorizer closes over the declared application tables and
  columns, and SQLite length, SQL, column, expression, compound-select,
  variable and VDBE-op limits bound the surface;
- the canonical `{meta,event,rows}` JSON encoding is capped at 24 KiB before
  the pinned JSONata 2.0.6 evaluator sees it;
- validated inserts and upserts, decisions, facts and the interpreted frontier
  commit atomically into a file-backed SQLite WAL database; and
- a read-only, authorizer-limited SQL connection returns bounded rows together
  with the exact verified and interpreted frontiers.

Run the focused proof with `go test ./spike/jsonataddl`. Given a repository
already bound to the fixture's application identity, the small query program
is runnable as:

```text
go run ./spike/cmd/jsonata-inventory -repo REPOSITORY -database NEW.sqlite \
  -sql 'SELECT sku, available FROM stock ORDER BY sku'
```

The projection database is disposable and the command refuses to overwrite
one. The Git sequence remains the source of truth.

The fold-read authority is not a blacklist. Mutable connection functions such
as `changes()`, `total_changes()` and `last_insert_rowid()`, PRAGMAs, physical
or platform relations, recursion, and every SQL function are unreachable even
if another spelling appears. A blacklist cannot establish that property as
SQLite adds functions and aliases.

The exact inventory fixture cannot turn an arbitrarily small event into a
large read result: its only read returns the event's own `sku` key plus one
integer. It can still duplicate that key between `event` and `rows`, so the
24 KiB cap applies to the assembled encoding rather than to either input in
isolation. A future profile with a small lookup key and a large non-key column
could expand much further, but that profile is outside this closed spike.

## Deliberate omissions

This is not the resident or the general application platform. It does not
implement a general SQL parser, `MANY` or historical reads, typed version
relations, projection-cache reuse or suffix replay, crash failpoints, HTTP/UI
or event submission, query concurrency slots, a general query-work budget, or
the complete JSONata compatibility corpus. The evaluator wrapper excludes the
known ambient functions and bounds depth, ranges, encoded input and output,
but it does not yet provide deterministic evaluation-step and allocation
bounds or settle all map-order and numeric edge cases. Those omitted contracts
must be resolved before this profile can interpret production history.
