# JSONata and DDL application spike

This spike loads one application profile from exactly two application files:
[`inventory/application.sql`](inventory/application.sql) declares events,
tables, named fold reads and write authority; and
[`inventory/folds/inventory.jsonata`](inventory/folds/inventory.jsonata)
defines the fold.

It demonstrates a deliberately small end-to-end path:

- `host.Open` verifies an application-bound Git sequence and recovers its
  immutable payloads from the Git object database;
- each known event is schema-checked, reads the n-1 SQLite projection through
  its named `OPTIONAL ONE` query, and runs the pinned JSONata 2.0.6 profile;
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

## Deliberate omissions

This is not the resident or the general application platform. It does not
implement a general SQL parser, `MANY` or historical reads, typed version
relations, projection-cache reuse or suffix replay, crash failpoints, HTTP/UI
or event submission, query concurrency slots, SQLite work/opcode limits, or
the complete JSONata compatibility corpus. The evaluator wrapper excludes the
known ambient functions and bounds depth, ranges, input and output, but it does
not yet provide deterministic evaluation-step and allocation bounds or settle
all map-order and numeric edge cases. Those omitted contracts must be resolved
before this profile can interpret production history.
