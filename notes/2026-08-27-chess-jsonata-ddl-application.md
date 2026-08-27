---
date: 2026-08-27
status: >-
  draft application-refactoring design. It depends on the stable extension
  mechanism proposed beside it and does not authorize implementation, binding
  replacement, or migration of existing chess repositories.
applies_to: https://github.com/generalbusiness-ai/gitseq-chess
requires: notes/2026-08-27-jsonata-ddl-stable-extensions.md
compatible_with: notes/2026-08-26-jsonata-ddl-application-implementation.md
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c478cb7d7dc48c0b22acc8a95733c84676449dab
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c88eb77d46112d9cc64a2fbf581baa343b3ea19b
---

# Refactoring chess onto the JSONata-with-DDL platform

The native chess application is a useful test of whether the proposed
application platform can carry a real event-sourced product rather than only
the inventory example. Its fold already has the right outer shape: Gitseq
verifies and orders opaque signed records; the application validates chess
events, reads prior game state, judges each event, and produces a deterministic
projection with no network, clock, filesystem, randomness, or mutable cache.

Most of that judgment can move into event declarations, tables, declared SQL
reads, and JSONata. Three dependencies cannot honestly be rewritten there:

- chess position calculation and adjudication;
- SHA-256 comparison for a secret invitation; and
- host identity resolution at the exact event position.

The first two use stable scalar functions. Identity arrives through stable host
context. The prerequisite design is
[`2026-08-27-jsonata-ddl-stable-extensions.md`](2026-08-27-jsonata-ddl-stable-extensions.md).
No chess-specific hook enters the resident, database, evaluator, query service,
or sequencing kernel.

This design preserves the implementation choices in the companion platform
note: one disposable SQLite projection per repository and application, one
writer transaction per event, the pinned Go JSONata profile, ordinary declared
reads, atomic typed row changes, bounded read-only SQL, and same-origin UI
submission through the resident. Chess adds an application package and trusted
function providers; it does not select another database, sidecar, native ABI,
storage format, or transport.

## Decision boundary and prerequisites

This draft does not decide to replace the native chess fold, adopt the proposed
application package, change an existing repository binding, migrate a
projection, or retire a compatibility surface. It also leaves final table and
column spelling, measured capability bounds, compatibility-view shapes, and
the mechanism for binding replacement to later ratified decisions backed by
implementation evidence. It deliberately does not revisit the first
implementation's choices of SQLite, the pinned Go JSONata evaluator, the
resident process boundary, Git-backed authoritative storage, disposable
projection storage, or same-origin HTTP transport.

Implementation has a strict prerequisite order. First establish and test the
reusable extension registry and host-context seam. Then admit the chess rules,
secret comparison, and identity-context providers through that seam. Only
after those prerequisites pass may work begin on the chess schema, JSONata
folds, differential replay, or replacement of native read surfaces. The chess
refactor depends on the extension mechanism; it must not define that mechanism
through application-specific exceptions.

This shape still carries assumptions that the three platform spikes can move.
Spike two established the current bounded query sandbox while exposing the
cost of fully materializing result rows; it did not change the driver or narrow
the SQL surface. Spike one may still change the driver, historical-view
technique, or atomic-frontier recovery. Spike three may still narrow the
JSONata profile, query surface, schema-discovery contract, or frontier-wait
behavior. Such a result requires this note to recheck its SQLite views,
historical queries, evaluator expressions, pagination, UI wait loop, and
differential harness before implementation. The durable chess judgments,
exact-event identity rules, pure bounded capability contracts, and dependency
order are load-bearing requirements rather than claims that those unsettled
implementation assumptions have already been proved.

## Refactoring boundary

The durable application package becomes approximately:

```text
chess/
  application.sql
  folds/
    create.jsonata
    create-named.jsonata
    name.jsonata
    join.jsonata
    move.jsonata
    resign.jsonata
    draw-offer.jsonata
    draw-accept.jsonata
  ui/
    ... static application assets ...
```

The application package declares the existing chess event families. It does
not absorb host identity events; unknown and host event kinds remain opaque to
the chess application exactly as they are today.

The generic resident replaces the current chess-specific replay, decision
lookup, board endpoints, and bounded game listing. Key custody, signing,
idempotency, append, the CLI or MCP presentation, and process-local presence and
chat are outside the fold. They may remain native clients of the generic event
submission and SQL query services. The live room stays nondurable and cannot
become an input to replay.

## Exact behavior before structure

The first refactor must preserve actual durable judgments, not infer a new
ruleset from prose. In particular, the currently pinned chess library
automatically ends a game for checkmate, stalemate, fivefold repetition, the
seventy-five-move rule, and insufficient material. Threefold repetition and the
fifty-move rule require a claim, and the current chess vocabulary has no claim
event. The current README describes repetition and the fifty-move rule too
broadly.

The refactor should therefore preserve fivefold and seventy-five-move automatic
draws and correct the documentation. Adding claim events for threefold or
fifty-move draws is a separate product change with separate event schemas and a
new fold version. It must not be smuggled into a representation refactor.

The new SQL projection and application package are a new bound fold even where
every old event judgment is preserved. Existing repositories bound to
`chess-fold@2` are not silently reinterpreted. Coexistence, explicit binding
replacement, and any projection migration use the host's binding rules in
force when implementation begins; this note neither assumes nor changes them.

## Relational projection

The following logical tables separate authority, game state, history, and
query materialization. Exact column spelling can change during implementation,
but their responsibilities should not be recombined.

### `games`

One row per effective create:

- game event identifier, name, created timestamp, and creator actor;
- admission mode: open, invited actor, or secret digest;
- open, playing, or finished status;
- current side to move, FEN, last accepted move-chain event, last UCI move, and
  accepted ply count;
- outcome and stable method vocabulary;
- pending draw-offer event and offering side; and
- the bounded canonical rules state consumed and returned by the chess
  capability.

The rules state is application state, not a serialized Go object. Its canonical
format belongs to `gitseq.chess.position@1`. At minimum it carries the current
position and the history summary required for exact repetition adjudication.
It cannot contain pointers, library enums, map-order-dependent bytes, caches,
or an implementation's private encoding. The capability contract fixes its
encoding and maximum size so another conforming implementation can replay the
same row-change stream.

### `seats`

At most two rows per game, keyed by game and color:

- the exact actor key fingerprint that originally sat;
- optional persistent identity scheme and stable subject;
- whether the seat has committed its late identity upgrade; and
- the event that established the seat.

Provider handle is not stored as authority. Identity equality is scheme plus
stable subject. Keeping seats separate makes the rule that one persistent
identity cannot occupy both colors a database-visible invariant rather than
hidden fields on a game object.

### `accepted_moves`

One row per effective move, keyed by game and ply, containing event identifier,
actor, UCI move, prior chain event, and resulting FEN. This is the durable SQL
history applications and users can query. Ineffective move attempts remain in
the platform decision and fact relations, not in this table.

### `board_squares` and `legal_moves`

These are bounded materializations of the current effective position.
`board_squares` has at most 32 occupied-square rows, or a fixed 64 rows if that
proves simpler for queries. `legal_moves` is keyed by game and canonical UCI
move and includes source, destination, and optional promotion piece.

The chess function returns both sets with every accepted position. JSONata
replaces them atomically with the game row. The UI and agents then use ordinary
SQL; application-query connections never call the chess function and the
browser never carries a second rules engine.

Views provide lobby, board, current legal destinations, and per-game refusal
summaries. The platform's decision, fact, event-position, row-version, and
derivation relations replace the native bounded refusal tail and the expensive
prefix refold currently used to recover old decisions.

Every writable table has an explicit primary key. There are no triggers,
generated keys, database time defaults, or application-query writes, matching
the first implementation design.

## Extension declarations

The package declares two scalar capabilities and one host context:

```sql
CREATE FUNCTION chess_initial
  USING 'gitseq.chess.initial-position@1'
  MAX CALLS 1
  MAX OUTPUT BYTES 4194304
  MAX WORK 2000000;

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

CREATE CONTEXT actor_identity
  USING 'gitseq.identity-at-event@0'
  MAX OUTPUT BYTES 1024;
```

The numeric bounds above are design ceilings, not measured admission values.
Implementation must replace them with proved limits that cover every legal game
under the selected automatic-draw rules, the current 4 KiB invitation-secret
limit, and the platform's total event budget. If the capability cannot prove a
finite complete state bound, it is not ready for the stable registry.

`chess_initial` returns the canonical starting rules state, FEN, pieces, legal
moves, turn, and no outcome. `chess_apply` takes the prior canonical state and a
lowercase UCI move. Its normal result is one of:

```json
{
  "legal": false,
  "reason": "promotion-piece-required",
  "details": { "from": "b7", "to": "a8" }
}
```

or:

```json
{
  "legal": true,
  "state": {},
  "fen": "...",
  "turn": "black",
  "outcome": null,
  "method": null,
  "pieces": [],
  "legal_moves": []
}
```

Finished positions use stable lower-case application values such as
`white-wins`, `black-wins`, `draw`, `checkmate`, `stalemate`,
`fivefold-repetition`, `seventy-five-move-rule`, and
`insufficient-material`. The adapter never persists dependency-specific string
forms such as Go enum names or a library's error text.

An illegal move is an ordinary bounded result. Invalid rules state, a malformed
provider result, exhausted work, or provider failure is an execution failure
that rolls back the event transaction. It is not reported as an illegal move.

`secret_matches` accepts the stored lowercase SHA-256 digest and the submitted
UTF-8 secret and returns one boolean. The implementation performs a
constant-time digest comparison. It returns no digest or diagnostic containing
the candidate secret. Changing a join event to carry the digest instead would
make the digest already visible on the create into the bearer secret, so the
existing raw-secret event semantics remain.

`actor_identity` is resolved by host vocabulary at the exact chess event. The
JSONata folds inspect its scheme, subject, and scope. They never call identity
code or read identity event payloads themselves.

## Fold design

Each fold reads only rows named in its declaration, calls only its allowlisted
functions, and writes one atomic result.

### Create and create-named

The two event schemas preserve the current wire shapes. The folds require no
`rests_on`, validate white or black creator color, validate exactly one
invitation form when present, and validate the optional display name. They call
`chess_initial` only after those cheap checks succeed.

An effective create inserts the game, creator seat, initial board, and initial
legal moves. Legal-move views hide them until the opponent seat exists. The
creator seat captures the actor's persistent identity only when
`meta.context.actor_identity` is anchored and scoped to `chess` or this game.

### Name

The name fold reads one game. It requires the create as its sole causal basis,
the exact creator actor, an empty current name, and one trimmed line of at most
256 bytes. It updates only the name. No extension function is needed.

### Join

The join fold reads the game and existing seats. It requires the create as its
sole causal basis, an unoccupied opponent seat, and an actor other than the
creator. An invited-actor game compares exact fingerprints. A secret game calls
`secret_matches` only after those structural conditions succeed. An open game
needs no function.

The candidate seat captures the event actor and eligible identity context. The
fold refuses a persistent-identity collision with the creator seat. An
effective join inserts the second seat and changes the game to playing, with
the join event as the first accepted move-chain head.

### Move

The move fold reads one game and exactly two seats. Before calling
`chess_apply`, JSONata checks:

- the game exists and is playing;
- `meta.rests_on` contains exactly the current accepted chain event;
- the actor holds exactly one seat under the rules below; and
- that seat is the side to move.

An anchored seat matches only a currently anchored actor with the same scheme
and stable subject in chess scope. An unanchored seat matches its exact original
actor. If that original actor now has a valid identity, an otherwise effective
act may upgrade the seat, unless that identity collides with the other seat.
The exact actor of the opposing seat can never borrow that seat's identity.

Only after these checks does JSONata call `chess_apply`. A legal result commits
any late seat upgrade, updates the game and rules state, inserts one accepted
move, and replaces board and legal-move rows. An illegal result is ineffective
and commits no seat upgrade. This order preserves the current mutation-witness
behavior: an illegal move cannot smuggle a later recovery identity into a seat.

Moving rather than accepting the other side's pending offer clears that offer.
The chess result sets automatic outcome and method; there is no separate result
event.

### Resign, draw-offer, and draw-accept

These folds read the game and seats and use the same seat-match expression as
move. That expression should live in one confined pure JSONata library fragment
or be generated into the folds at package build time; it should not become a
native helper because it is application policy, not a difficult algorithm.

Resign and draw-offer require the current move-chain head. Draw-offer also
requires no pending offer. Draw-accept requires the exact pending offer as both
payload field and sole causal basis, and only the other seated side may accept.
Only an effective act commits a late identity upgrade. Resignation and accepted
draw clear the queryable legal-move view by finishing the game.

## Queries and UI

The existing bounded application reads become parameterized SQL with an
expected frontier where pagination requires it:

- lobby and game pages query views over `games` and `seats`;
- board reads query `games` plus `board_squares`;
- legal destinations query `legal_moves` by game and source square;
- decisions and refusal facts query the platform relations by event or game;
  and
- stable game pagination orders by create position and uses the prior expected
  frontier, as required by the companion implementation note.

The static UI receives typed rows and exact frontier. It submits event type,
payload, session credential, and idempotency key to the generic resident. It
does not open SQLite, call functions, hold a durable private key on the server,
or decide chess legality. The existing wait cursor remains the invalidation
channel.

Process-local presence, signed chat, motion hints, and ephemeral browser keys
may continue beside this UI. Their role preview queries durable seats at one
frontier, remains optimistic about a later append's expiry, and never changes a
durable table. This refactor does not turn live state into SQL history.

## Compatibility and migration

The implementation should first run the native Go fold and the candidate
DDL/JSONata fold over the same verified logs and compare:

- every chess event's effective or ineffective decision;
- stable refusal fact kind and parameters;
- seats and late identity upgrades;
- accepted move chain, UCI history, FEN, turn, outcome, and method;
- board-square and legal-move materializations; and
- exact-event identity behavior at tied timestamps.

Public JSON does not need to retain the native Go struct layout. Compatibility
views or client adapters can preserve the current lobby, board, and MCP result
shapes while the relational schema becomes the internal source.

No migration should copy the current in-memory engine object or trust the
current projection as authority. A new projection is rebuilt from verified
events and the bound application package, as the platform design requires.
Binding replacement, if authorized separately, must prove that the old and new
folds agree over the repository's exact history before the new binding takes
force.

## Verification gates

Before implementation can replace the native fold, the candidate must pass:

1. every current chess fold, invitation, identity, mutation-witness,
   pagination, CLI, MCP, HTTP, and external-host integration test through the
   generic platform or a compatibility adapter;
2. differential replay over generated legal games, illegal moves, stale causal
   chains, lost join races, promotions, mate, stalemate, fivefold repetition,
   seventy-five-move draws, and insufficient material;
3. exact tests proving that failed moves, draw offers, and draw acceptances do
   not commit late identity upgrades;
4. helper conformance across starting position, UCI parsing, castling, en
   passant, promotion, repetition equivalence, move clocks, outcomes, canonical
   state encoding, output order, and every bound;
5. SQL tests proving board and legal queries invoke no helper and return the
   exact projected frontier;
6. crash tests proving a helper or evaluator failure leaves all game, seat,
   move, board, legal, decision, fact, derivation, and frontier writes rolled
   back together; and
7. security tests proving application queries cannot call either scalar
   function, diagnostics do not copy invitation secrets, malformed capability
   output cannot become an ineffective event, and identity context fails closed.

Implementation should proceed in dependency order: stabilize the extension
registry and identity context; admit and test the three chess capabilities;
write the chess schema and folds behind differential replay; replace read
surfaces with SQL and compatibility adapters; then consider binding replacement
or retirement of the native fold. The platform extension is reusable work. The
chess package is its first application, not its specification.
