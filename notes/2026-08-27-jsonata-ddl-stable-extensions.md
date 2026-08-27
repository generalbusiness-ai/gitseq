---
date: 2026-08-27
status: >-
  draft prerequisite interface design. It extends the optional native-helper
  seam left open by the JSONata-with-DDL application interface. It does not
  authorize implementation or change the first implementation's database,
  evaluator, resident, storage, or transport choices.
extends: notes/2026-08-26-jsonata-ddl-application-interface.md
compatible_with: notes/2026-08-26-jsonata-ddl-application-implementation.md
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c478cb7d7dc48c0b22acc8a95733c84676449dab
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c88eb77d46112d9cc64a2fbf581baa343b3ea19b
---

# Stable extensions for JSONata-with-DDL applications

The minimal JSONata-with-DDL application needs no native extension. Typed
events, declared SQL reads, JSONata transitions, and typed row changes remain
the default and complete interface. Some applications nevertheless depend on
algorithms that should not be rewritten in JSONata: chess rules, cryptographic
digests, image metadata decoders, or similarly bounded domain calculations.
They need a narrow function seam without gaining ambient authority.

This note defines that seam. An application may name a stable scalar function
capability in `application.sql`, allow selected folds to call it, and receive a
typed value. The resident supplies an implementation it already trusts. The
application package cannot supply, download, load, or select native code.

A second, narrower extension supplies host context that cannot honestly be a
scalar function. The first such context is the persistent identity, if any,
resolved for the actor at the exact event position. Context providers are
host-owned; applications cannot install them.

These additions do not change the choices in the companion implementation
note. The first resident still embeds the pinned JSONata evaluator and SQLite,
owns one projection writer, uses declared SQL reads, applies row changes in one
transaction, and serves read-only SQL. A SQLite custom function is a useful
adapter for a scalar capability, but SQLite's extension ABI is not the stable
application contract.

## Decision boundary and prerequisites

This draft does not decide that the extension profile should be adopted, which
capabilities should enter the stable registry, their final identifiers or
bounds, or whether a later provider should run in process, in WASM, or behind a
sidecar. Those choices need a later ratified decision and their own evidence.
It also leaves the first implementation's database, evaluator, resident,
storage, and transport choices unchanged: SQLite remains the disposable
projection, the pinned Go JSONata profile remains the evaluator, the resident
owns replay and the single writer, Git remains authoritative storage, and
same-origin HTTP remains the UI transport.

The dependency order is deliberate. The reusable extension registry,
allowlisting, metering, failure boundary, and host-context seam must exist and
pass their admission gates before chess capability providers are admitted.
Only then may the chess package and differential refactor depend on those
providers. A chess-specific shortcut is not an acceptable substitute for the
prerequisite seam.

The three platform spikes are evidence for the companion implementation, not
completed premises of this design. Spike two found a bounded-query
materialization cost that must remain explicit, but did not change the driver
or narrow the SQL surface. Spike one may still change the SQLite driver or the
historical-read and atomic-frontier design. Spike three may still narrow the
JSONata profile or application-query and frontier-wait surface. If either does,
the SQLite adapter, connection-authorizer assumptions, evaluator binding, and
host-context delivery described here must be rechecked. The stable semantic
requirements—explicit capabilities, no ambient authority, deterministic
bounds, typed results, fixed failures, and host-owned context—do not depend on
those implementation choices.

## Design rules

Every extension obeys six rules.

1. **It is explicit.** The application declares the capability, and each fold
   separately names the aliases it may use. An unlisted function is absent.
2. **It is identified by meaning.** A capability identifier denotes exact
   behavior, types, limits, and failure vocabulary. It is not a Go symbol,
   shared-library name, SQLite registration name, or package-manager range.
3. **It has no ambient authority.** A scalar function sees only its arguments.
   It cannot read SQL, the event log, a clock, randomness, files, the network,
   environment variables, mutable globals, or another invocation's state.
4. **It is total within declared bounds.** The provider has deterministic
   input, output, work, memory, and recursion bounds. A host that cannot enforce
   them cannot admit the capability.
5. **It returns data, not effects.** A function cannot emit a decision, fact,
   event, query, or row change. JSONata remains the only producer of the fold's
   output object, and the platform remains the only projection writer.
6. **Its failures retain their meaning.** A domain refusal is an ordinary
   return value. A malformed argument, invalid provider result, unavailable
   provider, exhausted bound, panic, or evaluator failure is an interpretation
   failure and never becomes an ineffective event.

## Application declaration

`CREATE FUNCTION` is a focused application-DDL extension, like `CREATE EVENT`
and `CREATE FOLD`. It does not ask SQLite to load an extension. For example:

```sql
CREATE FUNCTION chess_apply
  USING 'gitseq.chess.position@1'
  MAX CALLS 1
  MAX INPUT BYTES 4194304
  MAX OUTPUT BYTES 4194304
  MAX WORK 2000000;

CREATE FUNCTION secret_matches
  USING 'gitseq.sha256-matches-utf8@1'
  MAX CALLS 1
  MAX INPUT BYTES 8192
  MAX OUTPUT BYTES 32
  MAX WORK 16384;
```

The exact grammar should follow the existing small lexer: recognize this one
statement family, extract its fields, and reject everything else. It does not
justify a general SQL parser. Capability-level maxima come from the provider's
stable contract. An application may narrow them but cannot widen them.

The companion implementation note correctly describes the baseline lexer as
recognizing only `CREATE EVENT` and `CREATE FOLD`. An implementation that
admits this optional extension profile adds exactly `CREATE FUNCTION` and the
`CREATE CONTEXT` family below to that explicit allowlist. Ordinary DDL still
goes through SQLite's scratch database, and applications that declare no
extension remain unchanged. This is an additive compiler admission rule, not a
different parser, database, evaluator, resident boundary, or application
binding.

The application-local name is an alias. Renaming the alias without changing the
capability does not change the provider's meaning, although changing the bound
application package still changes its package digest. A semantic change to the
provider requires a new capability identifier and a new application fold
version.

A fold allowlists the aliases it can call:

```sql
CREATE FOLD move
  ON chess_move
  READ game OPTIONAL ONE AS
    SELECT * FROM games WHERE id = :event.game
  READ seats MANY MAX 2 ORDER BY color AS
    SELECT * FROM seats WHERE game = :event.game ORDER BY color
  USING 'folds/move.jsonata'
  FUNCTIONS chess_apply
  CONTEXT actor_identity
  WRITES games, seats, accepted_moves, board_squares, legal_moves;
```

The JSONata evaluator exposes the alias as `$chess_apply`. The function receives
only the values passed by the expression and returns one value in the
capability's declared logical type. The JSONata program can therefore perform
cheap business checks before calling expensive native code:

```jsonata
(
  $eligible := $exists(rows.game) and rows.game.status = "playing";
  $judged := $eligible ? $chess_apply(rows.game.rules_state, event.move) : null;
  /* JSONata alone returns decision, facts, and tables. */
)
```

This preserves the original five-step fold contract. The helper call occurs
inside the one evaluation of step 3; it does not add a second transition or a
write phase. Fold reads stay ordinary declared reads over state at position
*n - 1*.

The first profile does not expose application functions to application-query
connections, views, indexes, generated columns, defaults, checks, triggers, or
arbitrary SQL submitted by a client. If a query needs a derived value, the fold
materializes that value in a table. This keeps replay dependencies out of the
query surface and prevents a read client from turning an expensive helper into
an unbounded compute service.

## Stable capability contract

The resident has a registry keyed by capability identifier. Each entry fixes:

- argument count, logical types, null behavior, and maximum encoded sizes;
- result type, canonical encoding, result schema, and maximum encoded size;
- domain-result vocabulary, including which values mean a normal refusal;
- deterministic work, memory, recursion, and collection limits;
- a contract digest and conformance corpus;
- the functions and versions on which this capability itself depends, normally
  none; and
- a fixed diagnostic vocabulary containing no argument, provider, transport,
  or library-controlled text.

The application declaration must agree with that registry entry. A provider
registered under the wrong signature or contract digest is not a substitute.
If the named capability is missing or incompatible, the resident refuses the
application as uninterpretable before replay; it does not skip affected events.

Two implementations may use the same identifier only when they produce the
same canonical result for every admitted input, consume work according to the
same metric, and pass the same conformance corpus. A bug fix that changes a
result is a semantic change, even if it appears to make an implementation more
correct. It needs a new capability identifier and application fold version.

The capability contract, alias, narrowed limits, and folds that allow it are
part of the application package digest identified by the existing application
binding. No new host-binding field is needed.

## Invocation and metering

Arguments cross the boundary as the platform's logical values: null, boolean,
safe integer, finite real, text, blob, JSON array, or JSON object. JSON objects
have no semantic key order. A capability that serializes an object or turns its
keys into a sequence uses the profile's canonical key order. Blobs use the
platform's tagged representation outside SQL.

Before invocation the adapter validates argument count, type, nullability, and
encoded size. After invocation it validates the canonical result against the
registered result schema and output bound before JSONata can observe it.

Function calls are event-local. The platform may memoize a call by capability
identifier and canonical arguments for the duration of one event, but no cache
is authoritative and no cache survives as provider state. Repeated evaluation
of the same call must not change its answer. The declared call count and work
limit apply to logical invocations after such event-local deduplication, so SQL
or evaluator optimization cannot turn identical pure calls into a different
replay judgment.

Work is a deterministic capability-defined unit, not elapsed time. The first
implementation may also interrupt on a wall-clock safety deadline, as the
companion implementation note already requires. Such an interruption leaves an
interpretation gap and applies no row changes; it never becomes a business
refusal. A later replay may resume at that event under the same bound profile.

The platform records the event, alias, capability identifier, argument digest,
result digest, encoded sizes, work consumed, and success or fixed failure code
in its disposable invocation relation. It does not record raw arguments or
results there. Raw inputs may contain invitation secrets or other sensitive
values already governed by the event and application tables; diagnostics must
not make extra copies.

## SQLite and JSONata adapters

The stable registry is above both the database and evaluator. In the first Go
resident, one internal provider interface can have two confined adapters:

- a JSONata function binding used by the fold evaluator; and
- where an internal implementation benefits from it, a SQLite scalar-function
  binding with the same types and result validation.

The SQLite binding is registered only on the fold writer connection while an
allowlisted fold runs. The authorizer permits only that fold's aliases. The
application-query pool never registers them. SQLite's deterministic or
innocuous flags are useful defense in depth and optimization hints, not proof
of purity, totality, confinement, or bounds. Extension loading remains
disabled, as required by the companion implementation note.

No application package supplies a shared library, Go plugin, WASM module, FFI
path, sidecar address, or download URL. Providers are compiled into the
resident or installed by an administrator into a separately trusted registry
before the application is opened. Whether a future provider runs in process,
through a native ABI, in WASM, or behind a sidecar is an implementation choice;
the logical contract and failure behavior above do not change.

## Host context

Some deterministic inputs cannot be scalar functions because their honest
input is verified host history. Persistent identity is the first case. Making
`identity_at(actor, position)` look like a scalar function would either hide a
log read or pass an unbounded log through an argument. Both break the function
contract.

The application instead declares a host-owned context capability:

```sql
CREATE CONTEXT actor_identity
  USING 'gitseq.identity-at-event@0'
  MAX OUTPUT BYTES 1024;
```

A fold lists `CONTEXT actor_identity`. The platform then places the bounded
result at `meta.context.actor_identity` before declared reads and JSONata
evaluation. For the first capability the value is either:

```json
{ "anchored": false }
```

or:

```json
{
  "anchored": true,
  "scheme": "github",
  "subject": "4242",
  "handle": "alice",
  "scope": "chess:game-id",
  "anchor_event": "...",
  "vouching": "witnessed",
  "verification": "in-log"
}
```

The host resolves the event's actor at that event's exact verified position and
signed timestamp. Position decides anchor and revocation order; timestamp
decides expiry. Handle is display text and never identity equality. An
unanchored actor is a complete ordinary result, not a failure.

Context capabilities are versioned and bounded like scalar capabilities, but
the first profile admits only providers shipped as host vocabulary. Third-party
applications cannot install context code or ask it to read arbitrary host
state. A context failure stops interpretation at the event without row
changes.

The base `meta` contract also needs the signed fields applications already use:
`position`, `event_id`, `actor`, `timestamp`, and ordered `rests_on`. These are
verified event metadata, not extensions. The chess application needs all five.

## Security boundary

Application authors control function arguments, event values, fold SQL, and
JSONata expressions. They do not control provider code or registration.
Providers are nevertheless trusted code in the resident and require the same
review as a database driver or cryptographic library.

The adapter must:

- make undeclared functions unresolvable in both JSONata and SQLite;
- reject aliases that shadow built-ins, SQL functions, platform relations, or
  another application declaration;
- validate before and after every call;
- catch provider panics or equivalent language failures at the boundary where
  the implementation permits it, converting them to a fixed execution failure;
- never repeat raw arguments, results, or provider error text in durable facts,
  logs, HTTP responses, or evaluator messages;
- account for function work inside the event's total evaluation budget; and
- roll back the complete event transaction on any extension failure.

An in-process provider can still crash or corrupt its host despite this logical
contract. This note does not claim otherwise. Moving untrusted providers across
a process or WASM boundary is future hardening, not permission for applications
to load code today.

## Admission and conformance gates

Before the first stable extension is admitted, tests must prove:

1. a function absent from a fold's allowlist is unavailable even when another
   fold in the application uses it;
2. application-query SQL, views, checks, and schema DDL cannot call it;
3. missing providers, signature mismatches, malformed results, fixed failures,
   panics, and every exhausted bound apply no row changes and leave the
   interpreted frontier before the event;
4. normal domain refusals remain ordinary values that JSONata may turn into an
   ineffective decision and facts;
5. two supported implementations, when they exist, produce byte-identical
   canonical outputs over the conformance corpus;
6. diagnostics and the invocation relation contain no raw arguments, including
   a known invitation secret;
7. identity context observes anchor, delegation, revocation, scope, and expiry
   at exact record order when timestamps tie; and
8. removing or changing an extension provider makes the application visibly
   uninterpretable rather than partially replayed.

The chess refactoring is the first demanding conformance case. It should not be
implemented on an application-specific escape hatch; it should land only after
this mechanism can express its rules engine, invitation digest, and exact-event
identity context without widening their authority.
