---
date: 2026-08-26
status: draft interface specification. Nothing here selects an evaluator,
  database, storage layout, or host language. Those belong in a follow-up
  implementation-design note.
origin: Hugh's application-platform discussion: make the smallest Gitseq
  application read as JSONata with DDL and expose its projection through SQL.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b15de2f8788788a1afe970d6d077f7843862ebf2
---

# JSONata with DDL: the application interface

The kernel verifies and sequences opaque events. An application gives those
events meaning by declaring their schemas, folding them into typed relations,
and exposing those relations for read-only SQL queries.

The smallest useful application is two files:

```text
inventory/
  application.sql
  folds/reserve.jsonata
```

`application.sql` contains ordinary table and view DDL plus two small
extensions: event schemas and fold bindings. For example:

```sql
CREATE EVENT reservation_requested (
  id   TEXT NOT NULL,
  sku  TEXT NOT NULL,
  qty  INTEGER NOT NULL CHECK (qty > 0)
);

CREATE TABLE stock (
  sku       TEXT PRIMARY KEY,
  available INTEGER NOT NULL CHECK (available >= 0)
);

CREATE TABLE reservations (
  id  TEXT PRIMARY KEY,
  sku TEXT NOT NULL,
  qty INTEGER NOT NULL
);

CREATE FOLD reserve
  ON reservation_requested
  READ stock_row OPTIONAL ONE AS
    SELECT sku, available FROM stock WHERE sku = :event.sku
  USING 'folds/reserve.jsonata'
  WRITES stock, reservations;
```

The fold is a JSONata expression over one fixed input:

```json
{
  "meta": { "position": 42, "event_id": "...", "actor": "..." },
  "event": { "id": "r1", "sku": "A", "qty": 2 },
  "rows": { "stock_row": { "sku": "A", "available": 5 } }
}
```

For example, `reserve.jsonata` can be:

```jsonata
(
  $stock := rows.stock_row;
  $enough := $exists($stock) and $stock.available >= event.qty;
  {
    "decision": $enough ? "effective" : "ineffective",
    "facts": $enough ? [] : [{ "kind": "insufficient-stock" }],
    "tables": $enough ? {
      "stock": { "upsert": [{
        "sku": event.sku,
        "available": $stock.available - event.qty
      }] },
      "reservations": { "insert": [event] }
    } : {}
  }
)
```

It returns one fixed output shape:

```json
{
  "decision": "effective",
  "facts": [],
  "tables": {
    "stock": { "upsert": [{ "sku": "A", "available": 3 }] },
    "reservations": {
      "insert": [{ "id": "r1", "sku": "A", "qty": 2 }]
    }
  }
}
```

That is the whole minimal interface: typed event in, declared reads, JSONata
transition, typed row changes out, SQL projection available for query.

## Contract

For event position *n*, the application platform:

1. validates the payload against its `CREATE EVENT` declaration;
2. runs the fold's named reads against the projection after position *n - 1*;
3. evaluates the bound fold once with `{meta, event, rows}`;
4. validates the result and every row change against the fold declaration and
   table DDL; and
5. applies the accepted row changes atomically at position *n*.

In `rows`, `ONE` is an object, `OPTIONAL ONE` is an object or `null`, and
`MANY` is an array. In `tables`, each declared table may contain `insert`,
`upsert`, or `delete` arrays of rows.

The event schema, read names and cardinalities, JSONata program, writable
tables, and output schemas are part of the bound application identity. There
are no undeclared reads or writes. Tables are written only by folds; views are
read-only.

`decision` distinguishes a business rejection from an execution failure. A
valid fold may return `ineffective` and facts explaining why. Invalid input,
invalid fold output, a DDL constraint violation, exhaustion of a declared
bound, or evaluator failure stops interpretation; it is not silently converted
into an ineffective event.

## Reads and queries

There are two SQL surfaces.

**Fold reads** are deterministic inputs to replay. They are named,
parameterized `SELECT` statements with declared cardinality (`ONE`, `OPTIONAL
ONE`, or `MANY`). `MANY` has an explicit order and bound. They cannot observe a
clock, randomness, the network, mutable environment, or projection state later
than the event being folded.

The default read position is *n - 1*. A read may instead declare an earlier
event position. Such a read observes versioned projection state at that
position, so feedback and lineage do not depend on whatever happens to be
materialized now.

**Application queries** are ordinary read-only SQL over a completed
projection: tables, joins, aggregates, and views. Every result names the exact
event frontier it represents. Query breadth does not affect replay because
application queries cannot be called by a fold unless separately declared as
fold reads.

The SQL database is disposable derived state. The authoritative inputs are the
verified events and the bound application; the authoritative fold result is
the ordered logical row-change stream. A conforming platform can rebuild the
same projection from them.

## Small extensions

JSONata should cover the common case. An application may also declare named
helper functions implemented in another language. Each helper has typed
arguments and result, a version included in the application identity, and the
same purity, bounds, and deterministic-failure contract as the fold. A fold can
call only helpers it declares.

The query surface also exposes event position, decision, emitted facts, row
versions, and row-to-event derivation. Their physical storage is unspecified;
their meaning is not. This makes the feedback path inspectable rather than an
implicit dependency on database state.

## Not specified here

This interface does not choose the JSONata implementation, SQL engine, DDL
parser, storage format, cache strategy, process boundary, native-function ABI,
or wire protocol. The first implementation design should make those choices
without enlarging the two-file minimal application.
