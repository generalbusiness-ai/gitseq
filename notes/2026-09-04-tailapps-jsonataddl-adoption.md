---
date: 2026-09-04
status: >-
  candidate design. It adopts nothing. No dependency is added, no module is
  split, no application source is rewritten, and no binding is replaced until
  a ratified proposal and its own requests exist. Design only.
origin: >-
  request 5b3b0ddd from planner, "Reconcile Gitseq's four live JSONata/DDL
  design artifacts with Tailapps v0.1.3 and deliver the adoption design that
  makes github.com/generalbusiness-ai/tailapps/jsonataddl the basis for the
  inventory application. Assess chess separately and authorize it only if the
  published or deliberately extended plugin/capability boundary is
  appropriate. Design only; do not implement inventory or chess in this
  request."
bases: >-
  main 79e84008; Tailapps v0.1.3 read from the local Go module cache, tag
  commit e82a5b06775823062cda76859a57d22945dc09c6;
  notes/2026-08-26-jsonata-ddl-application-interface.md;
  notes/2026-08-26-jsonata-ddl-application-implementation.md;
  notes/2026-08-27-jsonata-ddl-stable-extensions.md;
  notes/2026-08-27-chess-jsonata-ddl-application.md;
  notes/2026-09-04-fold-simplification-study.md; docs/reference/architecture.md
  layers 4, 5 and 6
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3522d423cca34525233df614ddb508d35b3b0ddd
---

# Adopting the Tailapps JSONata-with-DDL core

Every claim about Tailapps code in this note cites `tailapps@v0.1.3/<path>:<line>`
against the module cache copy of the signed release. Every claim about Gitseq
code cites `path:line` at main 79e84008. Claims about the sibling application
repositories cite `gitseq-inventory@7cfdc271` and `gitseq-chess@0def23f7`.
Every number is reproducible by a command in section 6. Where I could not
verify something, section 7 says so.

## 0. Status, bases, and scope

### Status

This is a candidate design. It adopts nothing. The only durable output of
this request is this note.

### The pin

The analysis is pinned to Tailapps v0.1.3. The module cache records the tag's
VCS commit:

```json
{"Version":"v0.1.3","Time":"2026-08-31T21:13:31Z","Origin":{"VCS":"git","Hash":"e82a5b06775823062cda76859a57d22945dc09c6"}}
```

That hash begins `e82a5b0`, so the request's tag commit is confirmed from the
local cache.

The condition also asks whether later main-only changes alter the API. The
reviewer verified this against current Tailapps main `a79901f3` and reported
that it changes nothing in `jsonataddl`, in `go.mod` or in `go.sum` relative
to v0.1.3. The condition is therefore discharged: the pinned API is the
current API, and the two dependency pins this note relies on have not moved on
main. This note binds to v0.1.3 as read from the module cache.

### The public surface being adopted

Tailapps v0.1.3 exposes exactly one non-command Go package outside `internal`
that carries the core: `github.com/generalbusiness-ai/tailapps/jsonataddl`,
2709 non-test lines across nine files. Everything else in the module is
`internal`, `cmd/tailapp`, or the `tailapps/` application definitions with
their `embed.go`. The corpus runner is not exported: it lives in
`internal/profile` and is invoked as `go test ./internal/profile -run
TestConformanceCorpus`
(`tailapps@v0.1.3/jsonataddl/corpus/README.md:14`).

### What this note does not do

- It does not implement inventory or chess.
- It does not add a dependency, run `go get`, or touch any `go.mod`.
- It does not edit or delete the four live notes. Where this note supersedes
  a statement in one of them, section 1 names the statement and the
  replacement. Retiring the superseded note is a separate act by its author
  on the workroom board.
- It does not change `docs/reference/architecture.md`. Adoption would change
  a layer 5 contract, so the implementing head must update that page, but
  that is the implementing request's obligation, not this one's.
- It does not measure performance. No number in this note is a timing.
- It does not compile or execute Tailapps. Every behavioural claim is read
  from source.

---

## 1. Reconciliation with the four live notes

Each table names a statement, cites its location, gives a verdict, and where
the verdict is "superseded" names the replacing statement in this note.

Verdicts:

- **Stands.** The statement survives adoption unchanged.
- **Stands, tightened.** The statement survives and v0.1.3 enforces more than
  it required.
- **Superseded.** Adoption replaces the statement. The replacement is named.
- **Moot.** Adoption removes the question. Nothing replaces it.
- **Not available.** The statement describes something v0.1.3 does not have,
  and the statement remains a requirement on future work.

### 1.1 `notes/2026-08-26-jsonata-ddl-application-interface.md`

| Statement | Where | Verdict | Replacement or note |
|---|---|---|---|
| The smallest application is `application.sql` plus `folds/*.jsonata` | lines 27 to 33 | Stands | `SourceLayout` has exactly these three fields (`tailapps@v0.1.3/jsonataddl/dialect.go:47-54`) and the Tailapp dialect uses the same three values (`dialect.go:163-167`). The Gitseq dialect uses them too. |
| One `CREATE EVENT` per event kind, with one `CREATE FOLD` bound to each kind | lines 38 to 62, 123 to 126 | **Superseded** | v0.1.3 admits exactly one private event (`dialect.go:92-97`, `compile.go:255-260`), exactly one normalizer over the host event, and at least one analytic fold over the private event (`compile.go:78-80`, `compile.go:601-610`). Replacement: section 2.3. One host event carries the verified kernel record. One normalizer decodes it and emits one private event carrying a `kind` column. Folds consume the private event and branch on `kind`. |
| The fold input is `{meta, event, rows}` | lines 64 to 72 | Stands | `EvaluationInput` has exactly `Meta`, `Event`, `Rows` (`evaluate.go:24-28`). |
| The fold output is `{decision, facts, tables}` | lines 94 to 107 | Stands, tightened | `EvaluationResult` adds `Events` for the normalizer (`evaluate.go:41-46`). Decoding refuses unknown fields and trailing values (`evaluate.go:95-102`), requires `facts` to be an array (`evaluate.go:107-108`) and `tables` to be an object (`evaluate.go:112-113`). |
| The five-step contract: validate, read, evaluate, validate, apply atomically | lines 114 to 121 | Stands, with one boundary moved | Step 5 is not the core's. `Evaluate` returns a validated `TableChanges` plan and never executes it (`evaluate.go:30-37`, `evaluate.go:64-88`). The host applies it in its own transaction. Section 2.7. |
| `ONE` is an object, `OPTIONAL ONE` an object or null, `MANY` an array | lines 128 to 130 | Stands, tightened | Same three cardinalities (`application.go:45-51`). `MANY` additionally requires an explicit `LIMIT` within the dialect's `MaxManyRows` (`compile.go:527-532`) and a total `ORDER BY` ending in a declared unique key (`compile.go:561-563`, `compile.go:675-680`). |
| Tables are written only by folds; views are read-only; there are no undeclared reads or writes | lines 137 to 138 | Stands | Single-writer tables are enforced (`compile.go:625-636`), every table must have a declared writer (`compile.go:649-653`), and writes to undeclared tables are refused at evaluation (`evaluate.go:146-149`). |
| The expression language excludes `$now`, `$random`, `$eval` and equivalents | lines 140 to 143 | Stands | `ambientJSONataRE` rejects `$now`, `$millis`, `$random`, `$shuffle`, `$eval` lexically (`confine.go:18`, applied at `compile.go:429-431`). |
| **The application** declares limits for evaluation time, evaluator memory, recursion depth, read cardinality, and row changes | lines 143 to 147 | **Superseded** | Limits belong to the dialect, not the application (`dialect.go:139-151`), and every limit must be positive (`compile.go:94-101`). An application cannot narrow or widen them. Replacement: section 2.3 fixes the Gitseq dialect's eleven limits once, and any change to any of them changes the composed identity digest (`dialect.go:249-255`). |
| Declared limits include evaluator memory and evaluation time | lines 143 to 145 | **Not available** | `Limits` has no memory field and no step field (`dialect.go:139-151`). Wall time is a fixed 2000 ms process safety net (`compile.go:20-24`, `compile.go:444`) that is deliberately not a semantic bound (`evaluate.go:13-18`). Deterministic step and allocation bounds remain the open blocker `spike/jsonataddl/CORPUS.md:58-59` already named. Section 5, I4 and the go/no-go criteria. |
| `decision` distinguishes a business rejection from an execution failure | lines 149 to 153 | Stands | Only `effective` and `ineffective` are admitted (`evaluate.go:103-105`), and an ineffective decision may not carry events or row changes (`evaluate.go:190-192`). Every other failure is an error, never a partial result (`evaluate.go:63-66`). |
| Fold reads cannot observe a clock, randomness, the network, or later projection state | lines 159 to 163 | Stands, tightened | Ambient SQL is refused at compile (`compile.go:44`, `compile.go:576-578`), and at execution the default-deny authorizer denies every SQL function and every action other than SELECT and READ (`authorizer.go:33-51`). |
| The default read position is *n-1*; a read may declare an earlier event position | lines 165 to 168 | **Superseded** | `Read` has no position field (`application.go:53-62`). Worse, the technique the implementation note proposed cannot run: the default-deny authorizer denies any schema other than the empty string or `main` (`authorizer.go:38-40`), so a `temp`-schema shadow of the application tables is refused. Replacement: section 2.9. The Gitseq dialect declares no historical read. Historical reads become follow-on request I6, and are not a precondition of inventory adoption. |
| Application queries are ordinary read-only SQL that folds cannot call | lines 170 to 174 | Stands | The core has no query surface. The Gitseq host keeps its own query connection and its own query authorizer, separate from `ReadAuthorizer`. Section 2.6. |
| Native helper functions are outside this interface; a follow-up interface may admit them through an explicit allowlist | lines 181 to 188 | Stands as intent, **not available** as behaviour | v0.1.3 has a fixed nineteen-function allowlist compiled into the core (`confine.go:20-24`) and refuses user-defined functions outright (`confine.go:49-50`). No application-declared helper exists. Section 3 and follow-on request I7. |
| The query surface exposes event position, decision, facts, row versions, and row-to-event derivation | lines 190 to 193 | Moot for the core | The core declares no platform relations. These stay Gitseq host tables, written in the same transaction. Section 2.7. |
| This interface does not choose the JSONata implementation, SQL engine, DDL parser, storage format, or native-function ABI | lines 195 to 199 | **Superseded** | Adoption chooses all of them: `jsonata-go/jsonata/v206` (`compile.go:15`), `ncruces/go-sqlite3` and its `database/sql` driver (`compile.go:16-17`), and the core's regular-expression DDL profile (`compile.go:27-52`). Replacement: section 2.2 records those choices as the price of adoption, and section 5's go/no-go says what would make the price too high. |

### 1.2 `notes/2026-08-26-jsonata-ddl-application-implementation.md`

| Statement | Where | Verdict | Replacement or note |
|---|---|---|---|
| SQLite, one derived database per workroom and application profile | line 22 | Stands, narrowed | One database per application, not per workroom. The workroom gets none. Section 4. |
| The cgo-free `ncruces/go-sqlite3` binding, pinned to an exact version | lines 23 to 25 | Stands, and the pins already agree | Gitseq `go.mod:9` and Tailapps v0.1.3's `go.mod` both require `github.com/ncruces/go-sqlite3 v0.35.3`. The `jsonata-go/jsonata` pins also match exactly at `v0.0.0-20250709164031-599f35f32e5f` (Gitseq `go.mod:8`). Adoption introduces no version skew in either dependency today. |
| WAL mode, one writer connection, and a small read pool | line 26 | Stands, with one correction | The Tailapps host opens one connection total, `SetMaxOpenConns(1)` and `SetMaxIdleConns(1)`, with WAL and foreign keys on (`tailapps@v0.1.3/internal/projection/projection.go:232-234`). That is the writer. The read pool is a Gitseq host concern the core does not touch; the core neither opens nor owns a connection (`authorizer.go:9-12`). |
| The Go JSONata 2.0.6 implementation, pinned and **wrapped as a Gitseq language profile** | lines 27 to 29 | **Superseded** | The profile is no longer Gitseq's. It is the core's `core.jsonata` identity component, pinned as `jsonata-go-v206/bounded-2` (`identity.go:113`). Replacement: section 2.3. Gitseq consumes that value and does not define its own. |
| Ordinary same-origin HTTP between UI and resident; SQL is never the write interface | lines 30 to 31 | Stands | Host concern, untouched by the core. |
| The projection identity record contains genesis, application identity, fold version, schema digest, projected head and depth | lines 41 to 44 | Stands, extended | Add the composed runtime digest (`identity.go:93-96`), the compiled source revision (`application.go:130`, computed at `load.go:156-166`), the storage-schema digest and the export-contract digest (`application.go:132-133`). Section 2.4. |
| Reuse the database only when genesis, application, fold, and schema match | line 48 | **Superseded** | Replacement: section 2.4. The reuse key becomes genesis plus application name plus composed runtime digest plus source revision plus storage-schema digest. A continue activation additionally requires `ContinueCompatible` to pass (`application.go:208-226`), which permits added tables but refuses a changed or removed writable table shape. |
| A fold of event *n* runs in one `BEGIN IMMEDIATE` transaction; failure rolls back and leaves an explicit gap | lines 53 to 59 | Stands | The Tailapps host does the same, one delivery per transaction (`tailapps@v0.1.3/internal/projection/projection.go:1-4`, `projection.go:242`). Section 2.7 restates the Gitseq ordering. |
| Every writable table must have a primary key; `insert` and `upsert` carry a complete row, `delete` carries its complete primary key | lines 68 to 70 | Stands, enforced | An explicit primary key is required at compile (`compile.go:280-282`). Insert and upsert rows must be complete; delete rows are validated against the key columns only (`evaluate.go:165-172`, `evaluate.go:196-216`). |
| A small lexer recognizes only `CREATE EVENT` and `CREATE FOLD`; all other DDL is prepared by SQLite | lines 75 to 84 | **Superseded** | The core recognizes seven focused statements: `CREATE EVENT`, `CREATE TABLE`, `CREATE INDEX`, `CREATE VIEW`, `CREATE EXPORT`, `CREATE NORMALIZER`, `CREATE FOLD` (`compile.go:27-33`, seven regular expressions, dispatched at `compile.go:153-213`). Anything else is refused (`compile.go:211-212`). Replacement: section 2.5 lists the statements the Gitseq inventory application may use, and section 2.10 lists the two shipped view declarations that this profile refuses. |
| The first DDL profile admits primary keys, uniqueness, **foreign keys**, and checks | lines 86 to 88 | **Superseded** | Column-level `REFERENCES` is refused (`compile.go:49`, applied at `compile.go:338-340`), along with `DEFAULT`, `GENERATED`, `AUTOINCREMENT` and `COLLATE`. `CHECK` survives, because the raw statement text reaches SQLite (`compile.go:286-288`) and `CHECK` is not in the refused-clause set. Replacement: no foreign keys in a Gitseq JSONata-with-DDL application. Referential integrity is a fold obligation. |
| The first logical types are `TEXT`, `INTEGER`, `REAL`, `BLOB`, `BOOLEAN`, `JSON` | line 92 | Stands exactly | `values.go:26-33` and `compile.go:370-376`. |
| Integers are limited to JSON's exactly representable range | lines 94 to 95 | Stands exactly | `values.go:36-39`, enforced at `values.go:90-94`, with the decimal-string wrapper for values outside it (`values.go:43-55`). |
| Historical projection reads through a temporary schema that shadows the application tables | lines 104 to 123 | **Superseded** | Not implementable under the core's authorizer, as row 8 of section 1.1 says. Replacement: section 2.9 and follow-on request I6. |
| Application-query connections use `query_only` and an authorizer permitting the application and public platform relations | lines 133 to 138 | Stands | This is Gitseq host code. `ReadAuthorizer` is for fold reads only (`authorizer.go:14-21`). Gitseq keeps its own query authorizer. |
| Every query has bounds on SQL bytes, parameters, rows, bytes, and concurrent slots | lines 140 to 144 | Stands | Host concern. The core supplies none of these. |
| The Rust alternative and `rusqlite` | lines 150 to 175 | Moot | Adopting a Go module settles the language. If the Rust question returns, it returns as "should the core move", not as "should Gitseq's adapter move". |
| The Gitseq profile removes `$now`, `$millis`, `$random`, `$shuffle`, `$eval` | lines 184 to 186 | Stands exactly | `confine.go:18` names those five and no others. |
| The profile **adds only declared pure helper functions** | line 186 | **Not available** | The allowlist is nineteen names fixed in the core (`confine.go:20-24`), and an unrecognized name is refused (`confine.go:118-122`). There is no declaration mechanism. Section 3 and I7. |
| The adapter or a small maintained fork must add deterministic step and allocation bounds | lines 189 to 192 | Stands as an open requirement | v0.1.3 has not done it and says so (`compile.go:20-24`). This is the single most important unresolved condition in the whole adoption. Section 5 go/no-go. |
| Object key order is not semantic; any operation turning object iteration into an array must use canonical key order | lines 194 to 196 | Stands, resolved differently | The core removes the question by refusing the operations. Object wildcards and multiplication are refused lexically (`confine.go:159-185`), generated ranges are refused (`confine.go:125-153`), and `$keys`, `$each` and `$spread` are outside the allowlist. That matches the spike's answer at `spike/jsonataddl/CORPUS.md:30-39`, reached independently. |
| The evaluator version, disabled functions, limits, helper versions, and numeric rules all enter the fold identity | lines 196 to 197 | Stands, and becomes mechanical **for the dialect only** | `DialectComponent` hashes the complete canonical serialization of every dialect field, so no dialect change can ship under an unchanged digest (`dialect.go:206-255`). That is the whole of the mechanical part. The five core component values and the three host component values are hand-written strings (`identity.go:109-117`), so a semantic change inside the core or inside the host that does not update its string leaves the composed digest unchanged. Section 2.3 and section 6, item 21. |
| Resident and UI interface: metadata, query, submission, wait cursor | lines 199 to 232 | Stands | Host concern. |
| Three spikes should precede the platform build | lines 234 to 251 | Partly discharged | Spike one and spike two are recorded as passing in `spike/SPIKE-RESULTS.md` and `spike/querysandbox`. Spike three's compatibility and determinism half exists at `spike/jsonataddl/CORPUS.md`; its UI half exists at `spike/jsonataddl/inventory_ui.go`. Section 2.10 keeps the corpus and retires the rest. |

### 1.3 `notes/2026-08-27-jsonata-ddl-stable-extensions.md`

This note describes a mechanism that v0.1.3 does not contain in any form. Its
requirements survive; its grammar and its availability do not.

| Statement | Where | Verdict | Replacement or note |
|---|---|---|---|
| The minimal application needs no native extension | lines 17 to 19 | Stands, and is confirmed | The shipped inventory fold calls no function at all (`gitseq-inventory@7cfdc271/folds/inventory.jsonata`, eighteen lines, zero `$`-prefixed calls). |
| The six design rules: explicit, identified by meaning, no ambient authority, total within declared bounds, returns data not effects, failures retain meaning | lines 74 to 93 | Stand as requirements | None is implemented in v0.1.3. They become the acceptance conditions on follow-on request I7. |
| `CREATE FUNCTION alias USING 'capability@n' MAX CALLS ... MAX WORK ...` | lines 97 to 114 | **Not available** | No such statement exists in the dispatch (`compile.go:153-213`), and an unrecognized statement is refused (`compile.go:211-212`). |
| A fold allowlists aliases with `FUNCTIONS` and `CONTEXT` clauses | lines 138 to 148 | **Not available** | The normalizer tail admits only optional `WRITES` plus `EMITS`, and the fold tail admits only `WRITES` (`compile.go:36-37`, applied at `compile.go:450-476`). |
| The example fold declares `READ seats MANY MAX 2 ORDER BY color` | lines 143 to 144 | **Superseded** | The core's grammar is `MANY LIMIT n`, not `MANY MAX n` (`compile.go:34`), and the `ORDER BY` follows the `SELECT`, not the read header (`compile.go:40`, `compile.go:549-563`). Replacement: any future chess declaration uses `READ seats MANY LIMIT 2 AS SELECT ... ORDER BY color, game`, and the order must end in a declared unique key (`compile.go:675-680`). |
| The evaluator exposes the alias as `$chess_apply` | lines 150 to 153 | **Not available, and actively refused** | Any call whose name is outside the nineteen-function allowlist is a compile error (`confine.go:118-122`). |
| The first profile does not expose application functions to query connections, views, checks, or client SQL | lines 169 to 174 | Stands as a requirement | Also already true by construction: `validateAdmittedQuery` refuses every SQL function call in a read, view or export (`compile.go:585-587`). |
| The stable capability contract: types, bounds, contract digest, conformance corpus, fixed diagnostics | lines 176 to 199 | Stands as design | Unimplemented. I7. |
| Invocation and metering, event-local memoization, deterministic work units | lines 203 to 236 | Stands as design | Unimplemented. I7. |
| One internal provider interface with a JSONata adapter and a SQLite scalar-function adapter, the SQLite one registered only on the fold writer connection while an allowlisted fold runs | lines 238 to 252 | Stands, with precedent | The seat-and-release pattern this needs already exists in the Tailapps host: an atomic pointer holding the per-program authorizer, seated for the duration of one program's read plan and released after (`tailapps@v0.1.3/internal/projection/projection.go:72-89`, used at `projection.go:485-491`). A capability seat would use the same shape. |
| `meta.context.actor_identity` carries host-resolved identity at the exact position | lines 261 to 309 | **Partly available, and refused as a shortcut** | `EvaluationInput.Meta` is an untyped `map[string]any` the host fills freely (`evaluate.go:25`), and the core never validates it. A host could therefore put identity context there today with no core change. This note refuses that path. Undeclared context is not bounded, is not in any allowlist, does not enter the composed identity, and cannot be refused per fold. Replacement: section 3 and I7. Declared, bounded, identity-participating context needs a core change. |
| The base `meta` contract needs `position`, `event_id`, `actor`, `timestamp`, and ordered `rests_on` | lines 311 to 314 | **Not available as a contract** | `Dialect` has no field describing `meta`, so the meta shape is not part of `Canonical()` (`dialect.go:206-243`) and therefore not part of the identity digest. The Tailapps host supplies `position`, `event_id`, `event_type` by convention (`tailapps@v0.1.3/internal/projection/projection.go:499`). Replacement: section 2.3's input-contract table. Gitseq supplies `meta` as the empty object and puts every one of those five values in the `event` object instead, where four of them are declared envelope fields; the table gives the exact conversion for each. That table is the whole content of the `host.canonicalization` value, which is the only place v0.1.3 offers, and section 2.8 freezes it in a corpus because the digest cannot. Section 5 files I5 to move it into the dialect. |
| The security boundary: undeclared functions unresolvable, validation before and after, no raw arguments in diagnostics, rollback on any failure | lines 316 to 338 | Stands | Requirements on I7. |
| The eight admission and conformance gates | lines 340 to 359 | Stand | Gate 5 asks for two implementations agreeing byte-for-byte over a conformance corpus. That corpus now exists at `tailapps@v0.1.3/jsonataddl/corpus/`, seven case families under `v1/`. Its runner does not (`corpus/README.md:14`). Section 2.8 and I1. |
| The chess refactoring must land only after this mechanism exists; a chess-specific shortcut is not an acceptable substitute | lines 55 to 58, 361 to 364 | Stands, and decides section 3 | This is why the chess verdict is extend-first and not adopt. |

### 1.4 `notes/2026-08-27-chess-jsonata-ddl-application.md`

| Statement | Where | Verdict | Replacement or note |
|---|---|---|---|
| Three dependencies cannot honestly be rewritten in JSONata: position adjudication, SHA-256 comparison, exact-position identity | lines 25 to 30 | Stands, and is confirmed against the repo | Position adjudication is `game.engine.MoveStr` (`gitseq-chess@0def23f7/chess.go:417`) with outcome and method read from the engine (`chess.go:527-539`). Secret comparison uses `crypto/subtle` in the join fold (`chess.go:327`). Identity is the host's resolution over the same log (`chess.go:196`, queried at `chess.go:411`). |
| No chess-specific hook enters the resident, database, evaluator, query service, or kernel | lines 34 to 35 | Stands, and drives the verdict | Section 3. |
| The extension declarations block: `chess_initial`, `chess_apply`, `secret_matches`, `actor_identity` | lines 204 to 228 | **Not available** | As section 1.3 records. The four declarations do not compile under v0.1.3. |
| The numeric bounds in that block are design ceilings, not measured admission values | lines 230 to 234 | Stands | Nothing measured them. |
| The adapter never persists dependency-specific string forms such as Go enum names | lines 266 to 267 | Stands as a requirement, **and the current repository does not meet it** | `chess.go:531` stores `game.engine.Method().String()` directly into the projected `Method` field. Any refactor must map that to the stable lower-case vocabulary the note lists at lines 263 to 267. This is a finding about the chess repository, not a supersession of the note. |
| The relational projection: `games`, `seats`, `accepted_moves`, `board_squares`, `legal_moves` | lines 134 to 198 | Stands, with one bound to set deliberately | Replacing `board_squares` and `legal_moves` per move is a delete-plus-insert or upsert plan, and the total row changes in one evaluation are capped by the dialect's `MaxRowChanges` (`dialect.go:145`, enforced at `evaluate.go:187-189`; the Tailapp dialect sets it to 1024 at `dialect.go:199`). A chess dialect must set that bound against the worst-case legal-move count, not inherit Tailapp's. |
| The shared seat-match expression should live in one confined pure JSONata library fragment or be generated into the folds at package build time | lines 349 to 351 | **Superseded in part** | There is no include or fragment mechanism. Each program is one whole JSONata file named by `USING` (`compile.go:405-424`), and every source file must be bound by a declaration or the compile fails (`compile.go:232-236`). Replacement: generation at package build time is the only admitted option, and the generated bytes are covered by the source-set revision digest (`load.go:156-166`), so a generation change is visible in the application identity. |
| Chess is one fold stage today | `chess.go:193-224`, eight schemas dispatched in one switch | **Superseded by the same topology change as section 1.1** | Chess would need one normalizer over the host event and folds over one private event. That is a larger rewrite than the note assumed, and it is additional evidence for the extend-first verdict. |
| Compatibility and migration by differential replay of the native fold against the candidate fold | lines 383 to 404 | Stands | This is the same gate shape as section 2.8's corpus C. |
| The seven verification gates | lines 406 to 428 | Stand | Gates 4, 6 and 7 all depend on the capability seam that does not exist. |

---

## 2. Adoption design for inventory on the shared core

### 2.1 What the inventory application is today

`~/play/gitseq-inventory` exists. Its checked-out `main` is an empty root
commit, `8c19cc61b3c7ead18477b5f680ee98eb5424f224`, whose message says it
establishes an empty merge target. All eleven application files live on
`origin/main` at `7cfdc271503f490a4f80b533bc64ec27b3ce9a49`. Every citation
below reads that commit.

The application is a separate Go module, `github.com/generalbusiness-ai/gitseq-inventory`
(`gitseq-inventory@7cfdc271/go.mod:1`). It requires
`github.com/generalbusiness-ai/gitseq v0.0.0-20260827154243-61439ecd86d3`
(`go.mod:5`) and imports `github.com/generalbusiness-ai/gitseq/spike/jsonataddl`
(`application.go:11`). That import is the Gitseq-local duplicate the governing
request says must go.

Its source is two files: `application.sql`, with two `CREATE EVENT`, two
`CREATE TABLE` and two `CREATE FOLD` statements and no views or exports, and
`folds/inventory.jsonata`, eighteen lines that branch on `meta.event_type`.

### 2.2 What adoption costs and buys

Adoption replaces about 1656 lines of Gitseq-owned compiler, evaluator and
projection code with a dependency on 2709 lines nobody in this repository
reviews. The honest accounting is:

**Buys.** One implementation of the value codec instead of four boundaries
each doing their own (`values.go:14-21`). Execution-time default-deny read
authority instead of a compile-time text check (`authorizer.go:14-20`).
Mechanical identity for the dialect configuration, so no change to a layout,
envelope, topology, authority or limit value can ship under an unchanged
digest (`dialect.go:245-255`). A conformance corpus with frozen diagnostics.
`MANY` reads, views, exports and indexes, none of which the spike implements.

That identity claim is narrower than it first reads, and section 2.3 states
the limit. Only the dialect value is hashed. Core and host implementation
semantics still ride on hand-written component strings, and the `meta`
contract is not declared anywhere the digest can see. So the digest is a
reliable alarm for configuration drift and not for implementation drift.

**Costs.** The JSONata implementation, the SQL engine, the DDL grammar and
the SQLite driver stop being Gitseq's choice. A seventh compatibility axis
appears, exactly as `notes/2026-09-04-fold-simplification-study.md:1402-1405`
warns. The open determinism blocker moves from a spike note into a shipped
dependency. Every core release becomes a potential binding replacement.

The request settles the direction for inventory. The costs are why section 5
carries go/no-go criteria rather than a schedule.

### 2.3 Dialect and composed runtime identity

**The dialect.** Gitseq declares one dialect value, `GitseqRecord()`,
returning a `jsonataddl.Dialect` (`dialect.go:28-36`) with:

- `Identity`: name `gitseq-record`, version `1` (`dialect.go:40-43`).
- `Layout`: `application.sql`, `folds`, `.jsonata`. Identical to Tailapp's
  (`dialect.go:163-167`), which is what the interface note already specified.
- `HostEvent`: name `gitseq_record`, built with `NewEventContract`
  (`dialect.go:77-81`), with these six `TEXT` scalar fields: `id`, `schema`,
  `actor`, `position`, `timestamp`, `payload_digest`. `rests_on` and the
  decoded payload are deliberately not envelope fields, because envelope
  fields are only the names a read may parameterize on
  (`compile.go:637-647`), and neither a causal list nor an object is an
  equality key. They still reach the normalizer, through the `event` object
  described below.
- `PrivateEvent`: name `inventory_event`, `ExactlyOne: true`. The core
  refuses any other private-event policy (`compile.go:75-77`).
- `Topology`: `ExactlyOneNormalizer: true`, `AtLeastOneFold: true`,
  `FoldsMayEmitEvents: false`. The core refuses any other topology
  (`compile.go:78-80`), so this is not a choice.
- `Authority`: `NormalizerReads: ReadOwnTables`,
  `FoldReads: ReadOwnAndNormalizerTables`, `SingleWriterTables: true`. The
  core refuses non-single-writer authority (`compile.go:81-83`) and admits
  only these two visibility rules (`compile.go:84-93`).
- `Limits`: all eleven fields set deliberately (`dialect.go:139-151`). The
  inventory application is small, so the starting values should be smaller
  than Tailapp's, not copied from them. Every value enters the identity
  digest, so choosing them is a reviewed act.

**The normalizer's event object.** `HostEvent.Fields()` does not describe what
the normalizer receives. It controls exactly one thing: which names a read may
use as a `:event.<name>` parameter (`compile.go:637-647`, validated for
identifier shape only at `compile.go:102-106`). The core never validates
`EvaluationInput.Event` at all; the host supplies it and the core passes it
into the evaluation unexamined (`evaluate.go:26`, `evaluate.go:69-76`). Only
the scalar field names and types reach `Canonical()` (`dialect.go:219-222`),
so nothing below is mechanically bound to the identity digest by the core.

The Gitseq host therefore fixes the complete evaluation input itself. This is
the normative contract. Two implementations that agree with this table produce
byte-identical evaluation inputs for the same record, and two that disagree
anywhere in it are different runtimes even if both write
`host.canonicalization: gitseq-record/1`.

**`meta` is the empty object `{}`, for the normalizer and for the analytic
fold alike.** Not `null`, not absent. `EvaluationInput.Meta` carries no
`omitempty` tag (`evaluate.go:24-28`), so a nil map marshals as `"meta":null`
and an empty map as `"meta":{}`; the host supplies a non-nil empty map. The
same rule applies to `rows` for the normalizer, which declares no reads. This
application needs no metadata key: the normalizer decides from `event.schema`,
and the analytic fold branches on the private event's declared `kind` column
(section 2.10). Anything a future program needs must become a declared private
event column, where the core validates it on emission
(`evaluate.go:138-140`), not a new `meta` key. Adding a `meta` key is a
`host.canonicalization` value change and therefore a binding replacement.

**`event` for the normalizer** is exactly these eight members and no others.
The six scalars are the envelope fields the dialect declares, all JSON strings.
Every source below is `host.Record` at main 79e84008, built at
`host/host.go:794-804`.

| Member | Source | Conversion | Example |
|---|---|---|---|
| `id` | `host.Record.ID` (`host/host.go:210`) | Verbatim string, no transformation. The value is already canonical: `host.EventID` builds it as `"git:" + objectFormat + ":" + genesis + "#git:" + objectFormat + ":" + commit` (`host/identifier.go:41-43`). | `"git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3522d423cca34525233df614ddb508d35b3b0ddd"` |
| `schema` | `host.Record.Schema` (`host/host.go:220`), assigned from `event.Intent.Schema` (`host/host.go:799`) | Verbatim string, no transformation, no case folding. | `"gitseq-inventory/reservation-requested@0"` |
| `actor` | `host.Record.Actor` (`host/host.go:214`) | Verbatim string. The value is `intent.ActorFingerprint`, the lowercase hex encoding of `sha256` over the raw 32-byte Ed25519 public key (`internal/intent/intent.go:191-194`), always exactly 64 characters (`host/identifier.go:15`). | `"9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"` |
| `position` | The record's one-based index in `host.Log.Records` | Decimal string of that index, base 10, no padding, no separators, no sign. The first record is `"1"`. One-based, not zero-based, because that is what the oracle records in `gitseq_decisions.position` (`spike/jsonataddl/projection.go:161`), and Corpus C compares that column. | `"42"` |
| `timestamp` | `host.Record.Timestamp` (`host/host.go:227`) | Decimal string of the `int64`, base 10, no padding, no fraction, no suffix, with a leading `-` only when negative. The value is the sequencer's signed time **in Unix seconds**, stated verbatim at `host/host.go:224-226`: "Timestamp is the sequencer's signed time for this position, in Unix seconds. A deterministic fold judges expiry against this and never against the reader's clock." Seconds, never milliseconds and never nanoseconds. | `"1788134400"` |
| `payload_digest` | `host.Record.Payload` (`host/host.go:221`) | `"sha256:"` followed by the lowercase hex encoding of `sha256` over the payload bytes **exactly as signed and stored**. There is no re-canonicalization step and no canonicalization function to cite, because Gitseq applies none: `Payload` is assigned verbatim from `event.Payload` (`host/host.go:800`) and `host/host.go:219` states that "Schema and Payload are what the actor signed, unchanged." The signed bytes are the canonical bytes, and the actor's signature already binds them. The `sha256:` prefix with lowercase hex is the core's own digest spelling (`tailapps@v0.1.3/jsonataddl/load.go:168-171`). | `"sha256:a3f1..."`, 64 hex characters after the prefix |
| `rests_on` | `host.Record.RestsOn` (`host/host.go:223`) | JSON array of strings, in the exact order the kernel verified, with no sorting, no de-duplication and no normalisation, because order is meaning. An empty causal chain is `[]`, never `null`. | `["git:sha1:...#git:sha1:..."]` |
| `payload` | `host.Record.Payload` | The decoded payload object, decoded through `jsonataddl.DecodeCanonical` (`values.go:128`) so every number arrives as `json.Number` and matches what the validator expects on the way out. A payload that is not a JSON object, or that exceeds the host's payload ceiling, is an interpretation failure before evaluation, not a `null` payload the normalizer has to guess about. | `{"id": "r1", "sku": "A", "qty": 2}` |

**`event` for the analytic fold** is not this object. It is the private event
the normalizer emitted, whose columns the core declares and validates
(`compile.go:255-260`, `evaluate.go:138-140`). The table above governs the
normalizer's input only.

Because the core binds none of this, the whole table is what the
`host.canonicalization` component value `gitseq-record/1` names. Any change to
any cell, and any change to the `meta` rule above, is a component-value change
and therefore a binding replacement. That discipline is hand-applied, which is
exactly why section 5 files I5 to move the non-scalar host-event contract and
the `meta` contract into `Dialect`, where `Canonical()` would bind them
mechanically.

**Composed identity.** Gitseq composes exactly nine components
(`identity.go:21-31`). Five come from the core unchanged
(`jsonataddl.CoreComponents()`, `identity.go:109-117`). One is
`jsonataddl.DialectComponent(GitseqRecord())`, which is the dialect name and
version plus the SHA-256 of the complete canonical serialization
(`dialect.go:249-255`). Three are Gitseq's:

- `host.canonicalization`: `gitseq-record/1`. Its normative content is the
  input-contract table above, in full: the `meta: {}` rule, and the eight
  `event` members with the exact source field, conversion and encoding given
  for each. It names nothing that is not in that table, and the table is not
  a summary of something else. Neither the `meta` shape nor the non-scalar
  `event` members have a home in `Dialect`, which is why they live in this
  component value and why section 5 files I5 to fix that upstream.
- `host.orchestration`: `one-record-txn/1`. One verified record, one
  transaction, in the order section 2.7 gives.
- `host.projection`: `gitseq-query-values/1`. The externally observable query
  value conversion.

`ComposeIdentity` validates that the set is exactly the nine required keys,
each once, non-empty, with no reserved characters (`identity.go:47-74`). The
digest is SHA-256 over the sorted `key=value` descriptor, prefixed
`jsonata-ddl-runtime:sha256:` (`identity.go:78-96`).

**Where the digest is recorded.** In two places.

1. In the host binding, as `host.Application.FoldVersion`. That field is
   layer 4 vocabulary (`docs/reference/architecture.md:411-422`) and is what
   selects an interpreter. Today inventory records
   `FoldVersion: "jsonata-v206-sqlite-spike@0"`
   (`gitseq-inventory@7cfdc271/application.go:17`). It becomes the composed
   digest string. Changing any of the nine components is therefore a binding
   replacement, which architecture.md:444-452 already requires to carry
   equivalence evidence.
2. In the projection identity record, beside the compiled source revision
   (`application.go:130`), the storage-schema digest (`application.go:132`)
   and the export-contract digest (`application.go:133`).

The digest also seeds the source revision: `LoadApplication` hashes the
runtime profile string before the sorted source bytes (`load.go:156-166`).
So a component change reseeds every revision.

**What the digest does not cover.** One component of the nine is hashed from
its own content. `DialectComponent` serializes every semantic dialect field in
a fixed order and hashes it (`dialect.go:206-255`), so a changed limit, a
renamed envelope field or a different authority rule cannot hide. The other
eight are hand-written strings. `CoreComponents` returns five literals
(`identity.go:109-117`), and Gitseq writes three more. Nothing computes them
from the behaviour they name.

Three consequences follow, and the design must live with all three.

1. A core release that changes evaluator, grammar, codec or SQLite behaviour
   without editing its component literal produces the same composed digest
   and the same source revision. The projection would be reused across a
   semantic change. The core's own comment acknowledges the discipline this
   needs (`tailapps@v0.1.3/internal/profile/runtime.go:18-23`), and it is
   discipline, not a mechanism.
2. The same is true of Gitseq's three host components, and the exposure is
   larger there because `meta` and the non-scalar `event` fields are declared
   nowhere the core can see.
3. Therefore **every dependency pin move stays corpus-gated even when no
   component string moves.** The three corpora of section 2.8 are the only
   mechanical detector of implementation drift this design has. A pin move
   accompanied by an unchanged digest is not evidence of an unchanged
   meaning; it is the case the gate exists for. Section 5 records this as a
   standing rule rather than a per-release judgement.

### 2.4 Application handle lifecycle

The handle is immutable. `LoadApplication(files fs.FS, root, name string,
dialect Dialect, runtimeProfile string) (*Application, error)`
(`load.go:24`) compiles once and returns a handle whose every accessor
returns an independent copy (`application.go:136-190`). Evaluations of one
program serialize on that program's lock, so the handle is safe for
concurrent use (`application.go:76-91`).

Lifecycle:

1. **Compile at open.** The inventory binary embeds `application.sql` and
   `folds/`, and calls `LoadApplication` with `GitseqRecord()` and the
   composed digest. `LoadApplication` cleans the root and treats `"."` as
   empty (`load.go:34-37`), so the `prefixedFS` shim at
   `gitseq-inventory@7cfdc271/application.go:32-40` is deleted, not ported.
2. **Bind check.** The composed digest must equal the `FoldVersion` in the
   binding in force, or the host refuses to interpret. That is the existing
   layer 4 rule (`docs/reference/architecture.md:437-442`), not a new one.
3. **Projection reuse.** Reuse the database when genesis, application name,
   composed digest, source revision and storage-schema digest all match.
4. **Continue activation.** When the source revision moves but the operator
   asks to continue rather than rebuild, `ContinueCompatible(existing, next)`
   (`application.go:208-226`) decides. It permits added tables and refuses a
   removed or reshaped writable table. Added tables are created empty by the
   host, as its comment says.
5. **Discard on any other mismatch**, and replay from verified events. The
   database stays disposable derived state.
6. **Release at close.** No finalizer, no cache. A new binding gets a new
   handle.

### 2.5 Source loading and the application source set

The dialect's layout admits exactly two path shapes: the definition path
itself, and any path under the program root ending in the program suffix
(`load.go:84-89`). Path traversal, backslashes, absolute paths and
non-canonical paths are refused (`load.go:144-154`). Every file is size- and
UTF-8-checked against `MaxElementBytes`, and the total against
`MaxSourceBytes` (`load.go:115-124`). A program file not bound by any
declaration is a compile error (`compile.go:232-236`), so dead source cannot
ship.

`ValidateSource(dialect, name, content)` (`load.go:52`) is the single-element
check a submission path uses before it writes anything.

### 2.6 Host adapters

Four adapters. Each names the exact core interface it uses.

**Evaluation adapter.** Calls
`(*Application).Evaluate(programName string, input EvaluationInput)
(EvaluationResult, error)` (`evaluate.go:64`). It builds
`EvaluationInput{Meta, Event, Rows}` (`evaluate.go:24-28`) from the verified
record and the executed read plan. It must distinguish two failure classes.
`IsEvaluationTimeout(err)` (`evaluate.go:16`) reports the machine-time safety
net, which the core's own comment says a host must retry rather than record
as a deterministic gap. Every other error is a deterministic interpretation
failure that rolls the transaction back.

**Read-plan adapter.** Calls
`(*Application).ReadPlan(programName string) ([]Read, bool)`
(`application.go:184`). For each `Read` it binds `Read.Parameters` in order
from the event map, executes `Read.SQL`, and applies `Read.Cardinality`
(`application.go:45-51`): `One` demands exactly one row, `OptionalOne` at
most one, `Many` returns the array. The `LIMIT` is already appended to
`Read.SQL` for `Many` reads (`compile.go:565-567`), so the adapter must not
add a second one.

**Value codec adapter.** Three entry points, no Gitseq-local conversion:

- Fold-read input: `ReadRowValue(value any, declared LogicalType)`
  (`values.go:255`). The core's comment at `values.go:250-254` is explicit
  that this fold-input shape differs from the query shape on purpose and must
  not drift toward it without a runtime identity change.
- Projection write: `SQLiteBindValue(value any, logical LogicalType)`
  (`values.go:158`).
- Query read: `LogicalColumnValue(column SQLiteColumn, declared LogicalType)`
  (`values.go:216`), where `SQLiteColumn` (`values.go:204-210`) is
  driver-independent, so the Gitseq query layer converts once from its own
  driver rows.

`ValidateValue` (`values.go:74`), `DecodeCanonical` (`values.go:128`) and
`DecodeObject` (`values.go:140`) round out the surface. Diagnostics are
corpus-frozen, so Gitseq must not wrap them with different text where a
corpus case observes them.

**Default-deny authorizer adapter.** Calls
`(*Application).ReadAuthorizer(programName string) (Authorizer, bool)`
(`authorizer.go:21`), where `Authorizer` is a type alias for the pinned
driver's callback signature (`authorizer.go:12`). The allowlist derives from
the program's compiled read plan plus the compiled schema: only the named
relations and, through declared views, their base tables; table reads are
confined to declared columns; every other action is denied
(`authorizer.go:26-52`).

The seating discipline is the important part. The connection is opened once
with `SetAuthorizer` installed, plus `DBCONFIG_DEFENSIVE`,
`DBCONFIG_TRUSTED_SCHEMA` off and `DBCONFIG_ENABLE_LOAD_EXTENSION` off
(`tailapps@v0.1.3/internal/projection/projection.go:217-228`). Host
operations run with no deny function seated. The program's authorizer is
seated for the duration of its read plan and released after
(`projection.go:72-89`, `projection.go:485-491`). Gitseq mirrors this
exactly. Two consequences a reviewer should check: seating is per
connection, so the writer connection must not be shared with a concurrent
reader while a plan runs; and the application-query pool never seats a core
authorizer, because query authority is a different policy.

### 2.7 Persistence and transaction boundaries

The core never opens a connection, never executes a plan, and never writes
(`authorizer.go:9-12`, `evaluate.go:30-32`). Gitseq owns all of it.

One verified record is one transaction. In order:

1. `BeginTx` on the single writer connection.
2. Evaluate the normalizer. It declares no reads and writes no table, so no
   authorizer is seated and no `TableChanges` are applied at this step.
3. Apply the result mapping below. If the record is a skip, go to step 6.
4. Seat the analytic fold's authorizer, execute its read plan, release,
   evaluate the fold over the emitted private event, apply its validated
   `TableChanges`.
5. Write the Gitseq platform relations from the fold's result: one
   `gitseq_decisions` row, its `gitseq_facts` rows, row-to-event derivation,
   and the hidden per-table row versions the implementation note requires
   (lines 61 to 67).
6. Advance the interpreted frontier. Commit.

Any failure at any step rolls back the whole transaction and leaves the
interpreted frontier before the record, which is the explicit gap the
implementation note describes (lines 56 to 59). A timeout reported by
`IsEvaluationTimeout` rolls back and is retried, not recorded as a gap.

**Result mapping.** The core's `decision` word is a program-level result. The
Gitseq decision is an application-level fact about a record. They are not the
same thing, and the mapping between them has to be stated, because the
current implementation writes no decision row for a record it does not
interpret (`spike/jsonataddl/projection.go:510-516`) and exactly one row for a
record it does (`projection.go:551-566`). The mapping preserves that.

| Normalizer result | Meaning | What the host does |
|---|---|---|
| `effective`, no emitted private event | **Skip.** The record's schema is not an inventory schema. | No fold runs. No `gitseq_decisions` row. No `gitseq_facts` rows. Advance the frontier and commit. |
| `effective`, exactly one emitted private event | **Recognized.** | Run the single analytic fold. Its `decision` is the one `gitseq_decisions` row for this record, with `event_type` set from the host event's `schema` field, and its `facts` are that record's `gitseq_facts` rows in returned order. |
| `effective`, more than one emitted private event | Refused by the host. | Interpretation failure. Roll back. One record must not produce two decisions. |
| `ineffective` | Never produced by this application's normalizer. | Interpretation failure. Roll back. This keeps the normalizer's decision word out of `gitseq_decisions` entirely. |

Two things follow that a reviewer should check.

A skip must be spelled as `{"decision": "effective", "facts": [], "tables":
{}}` with no `events` key. That is the only spelling the core accepts for a
result that emits nothing: `facts` must be an array and `tables` an object
(`evaluate.go:107-113`), while `events` is optional (`evaluate.go:51`).
Spelling a skip `ineffective` would also be accepted by the core
(`evaluate.go:190-192`), and that is precisely the outcome this mapping
forbids, because it would invent an application-level refusal for a record
the application never claimed.

A recognized record whose payload is malformed is an execution failure, not
an ineffective decision. The normalizer projects the payload into the private
event's declared columns, and the core validates that row on emission
(`evaluate.go:138-140`, `evaluate.go:196-216`). A missing, undeclared or
wrongly typed column is an error from `Evaluate`, which rolls the transaction
back. That reproduces the oracle, where a failed event decode or validate
returns an error rather than a decision
(`spike/jsonataddl/projection.go:517-523`), and it honours the interface
note's rule that invalid input is never silently converted into an
ineffective event (interface note lines 149 to 153).

The connection is WAL, one writer, `SetMaxOpenConns(1)`. Application queries
use a separate read-only pool with Gitseq's own authorizer and its own
bounds. The database lives under the repository's private cache and is never
a Git artifact.

### 2.8 Differential corpus and replay gates

Three corpora. All three must be green at one exact head, run twice, before
the dependency pin moves or a binding is replaced.

**Corpus A: the Tailapps conformance corpus.** Source:
`tailapps@v0.1.3/jsonataddl/corpus/v1/`, seven case families:
`basic`, `misbehavior`, `projection-state`, `reject-ambient-function`,
`reject-bad-syntax`, `reject-multiple-writers`, `reject-unknown-type`
(`corpus/README.md:58-79`). Oracle: the frozen goldens shipped in the module.
Pass: byte-identical `EvaluationResult` JSON, byte-identical diagnostic text
for every error case, identical compiled identity for every `compile.outcome:
ok` case, and identical results across the `repeat: N` determinism assertions.
The complication is that the runner is unexported (`corpus/README.md:14`), so
Gitseq either writes its own runner over the published `manifest.json` format
or asks upstream to export one. Section 5, I1.

**Corpus B: the JSONata determinism corpus.** Source: the existing
`spike/jsonataddl/CORPUS.md`, `spike/jsonataddl/testdata/compatibility.json`
and `spike/jsonataddl/testdata/reference.js`. Oracle: live jsonata-js 2.0.6
executed through Node, located from the pinned Go module
(`spike/jsonataddl/CORPUS.md:3-8`). Pass: every deterministic case matches
the live reference, and every order-dependent or ambient expression the
corpus names is refused at compile by the core. This corpus is the one asset
in `spike/jsonataddl` worth keeping, because it is the evidence that
`core.jsonata` is honest, and because its own conclusion at
`CORPUS.md:58-59` is the open blocker.

**Corpus C: differential replay of the inventory log.** Oracle: the current
`spike/jsonataddl` implementation at main 79e84008, replaying the same
verified log. The comparison is over three things, and the result mapping in
section 2.7 is what makes each of them equal rather than merely similar.

1. **The `gitseq_decisions` rows**, compared as an ordered set of
   `(position, event_id, event_type, decision)` tuples. The mapping produces
   no row for a skipped record and exactly one row for a recognized record,
   which is what the oracle does at `spike/jsonataddl/projection.go:510-516`
   and `projection.go:551`. `event_type` is the record schema on both sides.
   So the row sets are directly comparable, including their absences.
2. **The `gitseq_facts` rows**, compared as `(event_id, ordinal, kind,
   fact_json)`. The single analytic fold returns the facts, in order, and the
   host writes them in that order, as the oracle does at
   `projection.go:554-566`.
3. **The final application table state** of `stock` and `reservations`, row
   for row.

Two exclusions are required, and they must be stated in the gate rather than
discovered during it. First, the runtime identity digest and therefore the
source revision differ by construction, so identity is asserted separately
and not compared. Second, program names differ, because the oracle has two
folds keyed by record schema and the candidate has one normalizer and one
fold, so nothing is compared per program. Neither exclusion touches the three
comparisons above.

**Input-contract freeze.** Corpus C also freezes the evaluation input, not
only the outcome, because the input-contract table in section 2.3 is the whole
normative content of `host.canonicalization: gitseq-record/1` and nothing in
the core checks it. The oracle cannot supply this golden: its `meta` carries
four keys and its `event` is the decoded payload alone
(`spike/jsonataddl/projection.go:528-532`), so there is nothing on the old
side to compare against. The freeze therefore takes fixtures, one per row of
the table plus one for `meta`:

- One golden holding the complete `EvaluationInput` JSON the host hands the
  normalizer for one fixture record, byte for byte, including `"meta":{}` and
  `"rows":{}` rather than `null`.
- One golden per `event` scalar, asserting the exact produced string against a
  fixture record with a known ID, schema, actor key, index, timestamp and
  payload: the verbatim `id` and `schema`, the 64-character lowercase `actor`,
  `"1"` for the first record's `position`, the unpadded Unix-seconds decimal
  `timestamp`, and the `sha256:` plus 64 lowercase hex `payload_digest`
  computed over the payload bytes as signed.
- One golden for `rests_on`, covering both the empty chain as `[]` and a
  two-element chain in kernel order, proving no sort and no de-duplication.
- One golden for `payload`, proving `DecodeCanonical` number preservation on
  an integer at the exactly representable bound.

A change to any of those bytes must fail the freeze and force a deliberate
`host.canonicalization` version bump, which is the only mechanism this design
has for that component, since the digest cannot detect it (section 2.3).

The gate is not "the tests pass". It is: all three corpora green, twice, at
the exact head that will be pinned, the input-contract freeze green, plus a
planted-defect run proving corpus C detects a deliberately wrong upsert. A
harness that cannot detect a planted defect is not a gate.

### 2.9 What is not carried across

**Historical reads at an earlier position.** The interface note's read-at-a-
position (lines 165 to 168) and the implementation note's temporary-schema
technique (lines 104 to 123) do not survive. `Read` has no position field
(`application.go:53-62`), and the authorizer denies any schema other than the
empty string or `main` (`authorizer.go:38-40`), which is exactly what a
`temp` shadow needs. The inventory application declares no historical read
and does not need one. This becomes upstream request I6, and it is not a
precondition of inventory adoption.

**Per-application limits.** Limits are per dialect. An application that needs
different bounds needs a different dialect, and therefore a different
identity.

**Foreign keys.** Column-level `REFERENCES` is refused (`compile.go:49`,
`compile.go:338-340`). Referential integrity becomes a fold obligation.

### 2.10 Source changes the shipped inventory application needs

The current `application.sql` does not compile under v0.1.3. Five changes:

1. **One private event and one normalizer.** Replace the two `CREATE EVENT`
   declarations with one `CREATE EVENT inventory_event (...)` carrying a
   `kind TEXT NOT NULL` column plus the union of the payload fields, and add
   one `CREATE NORMALIZER normalize ON gitseq_record USING
   'folds/normalize.jsonata' EMITS inventory_event`. The normalizer declares
   no `WRITES`, which the grammar permits (`compile.go:36`, applied at
   `compile.go:450-463`). It decides which kernel records are inventory
   records and, for every other record, emits nothing at all. Per section
   2.7's result mapping, that is a skip, not a refusal. This restructuring is
   required because the core admits exactly one private event
   (`compile.go:255-260`) and folds must consume it (`compile.go:464-467`).
2. **One analytic fold, not two.** This is the change that decides the shape
   of the rewrite, and the first draft of this note got it wrong. The current
   `receive_stock` and `reserve_stock` both declare `WRITES stock`
   (`gitseq-inventory@7cfdc271/application.sql:28`, `application.sql:34`).
   Moving both to `ON inventory_event` unchanged does not compile, because
   the core requires each table to have exactly one writing program and
   refuses a second writer by name:

   ```go
   if prior, exists := writers[tableName]; exists {
       return fmt.Errorf("table %q has multiple writers %q and %q", tableName, prior, program.Name)
   }
   ```

   That is `compile.go:630-632`, inside `validateTopology`, reached for every
   program's `Writes` at `compile.go:621-636`. The dialect cannot opt out:
   a non-single-writer authority policy is refused before compilation begins
   (`compile.go:81-83`), and the corpus freezes the diagnostic as its own
   `reject-multiple-writers` case
   (`tailapps@v0.1.3/jsonataddl/corpus/v1/reject-multiple-writers/`).

   So the two folds become one: `CREATE FOLD apply_inventory ON
   inventory_event READ stock_row OPTIONAL ONE AS SELECT sku, available FROM
   stock WHERE sku = :event.sku USING 'folds/inventory.jsonata' WRITES stock,
   reservations;`. It branches on `kind` instead of on `meta.event_type`.
   One fold over one private event also means one evaluation per recognized
   record, which is what makes the one-decision-per-record mapping in section
   2.7 true by construction rather than by convention.

   `:event.sku` is admitted because `sku` is a declared non-JSON column of
   the private event (`compile.go:611-616`, checked at `compile.go:637-647`),
   and the fold may read `stock` because it writes it, under either fold-read
   visibility rule (`compile.go:690-710`). With a normalizer that writes
   nothing, `ReadOwnTables` and `ReadOwnAndNormalizerTables` coincide for
   this application; section 2.3 declares the wider rule so that a second
   fold reading a normalizer table later would not need a new dialect
   identity.
3. **At least one `CREATE EXPORT`.** The core refuses an application with no
   export (`compile.go:877-879`). The shipped application has none. Add an
   export over the base tables. Exports may not read declared views
   (`compile.go:846-850`).
4. **No SQL functions and no qualified names in any view or export.**
   `validateAdmittedQuery` refuses any function call (`compile.go:585-587`)
   and any dotted name (`compile.go:589-591`). The two views in the spike
   fixture both fail: `spike/jsonataddl/inventory/application.sql:28-31` uses
   `sum(qty)`, and `application.sql:33-35` uses both `coalesce(...)` and the
   qualified `s.sku`. The shipped inventory application has no views, so this
   costs it nothing today, but any aggregate view must be materialized by a
   fold instead.
5. **Explicit row projection in the fold.** The current fold writes
   `"reservations": {"insert": [event]}`
   (`gitseq-inventory@7cfdc271/folds/inventory.jsonata:14`). Under the core
   that row is validated column by column against the declared table, and any
   column not declared is an error (`evaluate.go:196-209`). Once `event` is
   the private event and carries `kind`, the whole-object insert fails. The
   fold must name the three columns.

The source set therefore grows by exactly one file, `folds/normalize.jsonata`,
and every file must be bound by a declaration or the compile fails
(`compile.go:232-236`).

The eighteen-line fold is otherwise confinement-clean under v0.1.3. It uses
no `$`-prefixed calls, no lambda, no `*`, and no `..`, so it passes the
lexical check (`confine.go:26-34`) and the AST walk (`confine.go:36-106`)
unchanged. The normalizer must stay inside the same subset, which it can: it
compares `event.schema` against literals, projects named payload fields, and
returns either an empty result or one emitted row.

### 2.11 No Gitseq-local compiler or evaluator remains

**Stated plainly: after adoption, this repository contains no JSONata
compiler, no JSONata evaluator, no DDL parser, and no `jsonata-go`
dependency.**

**Deleted from Gitseq main:**

- `spike/jsonataddl/profile.go`, 407 lines. The whole compiler: the DDL
  regular expressions (`profile.go:52-61`), the limits (`profile.go:25-39`),
  and the fold-version constant `jsonata-v206-sqlite-spike@1`
  (`profile.go:45`).
- `spike/jsonataddl/projection.go`, 1012 lines. Replay, evaluation,
  validation, mutation application and the query surface.
- `spike/jsonataddl/history.go`, 237 lines. The historical-read technique
  that the core's authorizer refuses.
- `spike/jsonataddl/inventory_ui.go`, 463 lines, and
  `spike/cmd/jsonata-inventory`, `spike/cmd/jsonata-inventory-ui`.
- `spike/jsonataddl/inventory/`, the fixture application, now duplicated by
  the real one in `gitseq-inventory`.
- `github.com/jsonata-go/jsonata` from `go.mod:8`.

**Kept, and moved to the inventory repository as test fixtures:**

- `spike/jsonataddl/CORPUS.md`, `testdata/compatibility.json`,
  `testdata/reference.js` and `compatibility_test.go`. This is corpus B, and
  it is the reason to keep anything at all.
- `spike/jsonataddl/RECOVERY.md` and `recovery_test.go`. The crash-recovery
  sweep tests host behaviour, not core behaviour, so it follows the host
  adapter.

**Sequencing constraint.** `spike/jsonataddl` is an exported package, and
`gitseq-inventory` imports it cross-module at a pinned pseudo-version
(`gitseq-inventory@7cfdc271/application.go:11`). Deleting it from Gitseq main
does not break the pinned build, because the module cache copy is immutable,
but it does close the path forward. So the deletion must land after the
inventory `go.mod` has moved off it, not before.

That ordering is why section 5 splits the work across two repositories rather
than one request. The deletion also changes the surface compatibility axis
(`docs/reference/architecture.md:1577`) and the layer 5 contract, so the head
that removes the package is the head that updates
`docs/reference/architecture.md` and publishes its candidate artifact. The
two must not be separated: a contract-changing deletion whose architecture
update lands later leaves the page describing a package that no longer
exists. Section 5, I9.

### 2.12 Where the Gitseq host adapter lives

The adapter starts in the `gitseq-inventory` repository, as an internal
package of that module.

Reasons. Only inventory needs it today. Putting it in Gitseq main would put an
application interpreter's dependencies inside the repository whose layer 4
surfaces must stay free of any application profile
(`docs/reference/architecture.md:1618-1621`), and the inventory application
already lives in its own module for exactly that reason. Putting it in its own
module before a second consumer exists repeats the mistake the Tailapps README
already names: a module split before a second host is version-skew risk for no
benefit (`tailapps@v0.1.3/jsonataddl/README.md:16-21`).

That argument is a layer-boundary and simplicity argument. It is not the fold
study's. The fold study decided one narrower thing, that the **workroom fold**
must not depend on Tailapps or JSONata
(`notes/2026-09-04-fold-simplification-study.md:1252-1253`). It did not decide
that this repository may never depend on either, and this note does not claim
it did. Section 4 keeps that distinction.

The promotion trigger is explicit: when a second Gitseq application adopts
the core, the adapter moves to its own module in one mechanical split, and
the identity components move with it. Chess would be that second consumer,
and section 3 says chess is not ready.

Consequence for Gitseq main: it keeps no JSONata dependency and gains no
Tailapps dependency. The surface compatibility axis
(`docs/reference/architecture.md:1577`) still moves, because the exported
`spike/jsonataddl` package disappears.

### 2.13 The second-host module split

`tailapps@v0.1.3/jsonataddl/README.md:16-21` promises a dedicated module or
repository "when a second host adopts the core", and names Gitseq adoption as
one of the two triggers. Gitseq adoption is that trigger. Three options.

**Option A: leave `jsonataddl` a package in the Tailapps module.** Gitseq
then depends on the whole Tailapps module graph. v0.1.3's `go.mod` requires
the MCP Go SDK, the OTLP protobuf definitions, protobuf, grpc through the
gateway, and `golang.org/x/sys`, none of which the `jsonataddl` package
imports. The package itself imports only `jsonata-go/jsonata/v206`,
`ncruces/go-sqlite3` and its driver, plus the standard library. Option A
makes an inventory application carry an observability platform's dependency
graph. Rejected.

**Option B, recommended: a nested module at `tailapps/jsonataddl`.** A
`go.mod` inside the existing directory, requiring only `jsonata-go/jsonata`
and `ncruces/go-sqlite3`. The import path does not change, so no consumer
edits an import. Tailapps' main module requires it by version like any other
dependency. Releases are tagged `jsonataddl/vX.Y.Z`, which Go resolves for
nested modules.

**Option C: a separate repository.** Cleanest dependency story and the
cleanest review boundary. It changes the import path, which is a one-time
cost for two consumers today. It also splits the differential gate across two
repositories during the migration, which is the exact lock-step risk the
README cites.

**Recommendation: Option B now, Option C only if a third host appears or if
Tailapps and the core need different release cadences.** The trade-off worth
recording: Option B's import path is inside the Tailapps repository, so a
later move to Option C is a path change, not a directory move. Accepting
Option B is accepting that cost later.

**Ownership.**

- The Tailapps repository owns the core. Its maintainers cut releases.
- Gitseq is a consumer. Gitseq files upstream requests for core changes and
  does not fork. A fork would recreate the duplicate this request is removing.
- `gitseq-inventory/go.mod` pins an exact tagged version. Never a
  pseudo-version from main, because a pseudo-version is not a reviewed
  release and cannot be diffed against a signed tag.
- Every pin move is a reviewed act with the three corpora green, because a
  core release can change any of the five `CoreComponents` values
  (`identity.go:109-117`) and therefore the composed digest, and therefore
  every projection and every binding.
- Gitseq does not depend on the Tailapps main module at all, only on the
  nested core module.

---

## 3. Chess capability matrix

Columns: what chess needs, what v0.1.3 provides, the gap, and the remedy.
Chess citations are at `gitseq-chess@0def23f7`.

| Row | What chess needs | What v0.1.3 provides | Gap | Remedy |
|---|---|---|---|---|
| **Custom pure functions** | Position adjudication through the pinned rules engine: `game.engine.MoveStr(body.Move)` (`chess.go:417`), outcome and method (`chess.go:527-539`), legal-move enumeration over `ValidMoves()` (`chess.go:756-766`), and a constant-time SHA-256 secret comparison (`chess.go:327`). The chess note declares four capabilities for this (chess note lines 204 to 228). | A fixed nineteen-name allowlist compiled into the core (`confine.go:20-24`). User-defined functions are refused (`confine.go:49-50`). Any call outside the allowlist is a compile error (`confine.go:118-122`). | Total. There is no declaration, no registry, no alias, no per-fold allowlist and no invocation path. | **Extend Tailapps first.** The seam must be built in the core, under the six design rules of the stable-extensions note, before any chess source is written. |
| **Verified actor and host context** | Persistent identity resolved at the exact record position: `identity.Resolve(log)` (`chess.go:196`), `LookupAt(record.ID)` (`chess.go:411`), and the fail-closed seat allowlist requiring anchored, chess-scoped, `Witnessed` or `SelfSigned` vouching and `LiveLookup` or `InLog` verification (`chess.go:696-711`). | `EvaluationInput.Meta` is an untyped `map[string]any` the host fills (`evaluate.go:25`). `Dialect` has no field describing it, so it is absent from `Canonical()` (`dialect.go:206-243`) and from the identity digest. | Context can be delivered today, and that is the problem. It would be undeclared, unbounded, absent from identity, and impossible to refuse per fold. | **Extend Tailapps first.** Delivering identity through undeclared `Meta` is precisely the chess-specific shortcut the stable-extensions note forbids (lines 55 to 58). A declared, bounded `CREATE CONTEXT` that participates in the dialect canonical form is the remedy. |
| **Vocabulary and event-envelope differences** | Eight distinct schemas dispatched in one switch: `chess/create@0` through `chess/draw-accept@0` (`chess.go:29-36`, `chess.go:202-219`). Payloads are strict canonical JSON with a re-marshal byte-equality check (`chess.go:563`). `RestsOn` carries the accepted move-chain head and is load-bearing. | One host event and exactly one private event (`compile.go:255-260`). Private-event columns are validated on emission (`evaluate.go:138`). The host event's envelope is a flat list of typed scalars (`dialect.go:56-63`). | Expressible, but not as written. The eight schemas collapse into one private event with a `kind` column, and the switch moves into the normalizer's JSONata. `rests_on` cannot be an envelope field because envelope fields are read parameters (`compile.go:637-647`); it reaches the normalizer through `event` instead. | **Adopt as-is, with a rewrite.** No core change needed. But this is a larger rewrite than the chess note assumed, which is evidence for the overall verdict rather than a blocker on its own. |
| **Topology** | One stage. `Fold` both validates and projects, dispatching to eight `fold*` methods and then normalizing every game (`chess.go:193-224`). Refusals are emitted inline. | Exactly one normalizer plus at least one fold, folds may not emit (`compile.go:78-80`, `compile.go:601-610`). The core refuses any other topology, so it is not configurable. | Chess must split into a normalizer that decodes, authorizes and emits, and folds that project. The seat-match rule runs in the normalizer, because only the normalizer sees the verified actor. | **Adopt as-is, with a rewrite.** The split is real work and changes where refusals are decided, but the core admits it. |
| **Extension identity and digest** | A capability identifier that denotes exact behaviour, with a contract digest, so a bug fix that changes a result is a new identifier (stable-extensions note lines 176 to 199). | Mechanical identity exists for the dialect (`dialect.go:249-255`) and for the five core components (`identity.go:109-117`). There is no component for an extension registry, and `requiredComponents` is a closed set that refuses unknown keys (`identity.go:47-56`). | A capability set cannot enter the composed identity without a core change, because the component set is closed by design. | **Extend Tailapps first.** Adding a capability component to `requiredComponents` is the right shape, and it must be done upstream. A host cannot smuggle one in. |
| **Confinement** | The rules engine is trusted host code, not application code. It must be unreachable from application queries, views, checks and client SQL (stable-extensions note lines 169 to 174). | Fold reads run under a default-deny authorizer that denies every SQL function (`authorizer.go:33-51`), and compiled queries refuse function calls textually (`compile.go:585-587`). The seat-and-release pattern shows how a per-program capability would be scoped (`tailapps@v0.1.3/internal/projection/projection.go:72-89`). | The confinement machinery for a capability does not exist, but the pattern that would carry it does. | **Extend Tailapps first**, reusing the existing seat pattern rather than inventing a second one. |
| **Determinism** | Deterministic replay is the whole value. Chess already forbids network, clock, filesystem and randomness, and judges expiry against the signed `record.Timestamp`. | The core is deterministic by refusal: ambient functions rejected lexically (`confine.go:18`), object wildcards and multiplication rejected (`confine.go:159-185`), generated ranges rejected (`confine.go:125-153`), no lambdas (`confine.go:49-50`). Wall time is explicitly not a semantic bound (`evaluate.go:13-18`). | The core's own determinism is sound for JSONata. A capability provider's determinism is not the core's problem yet, because there are no providers. `spike/jsonataddl/CORPUS.md:58-59` still names step and allocation bounds as production blockers. | **Extend Tailapps first**, and the extension must carry the deterministic work metric the stable-extensions note requires (lines 225 to 229). Wall time cannot substitute. |
| **Resource bounds** | Per-position move generation is not fixed-cost work. `invalidMoveDetail` runs full move generation on every refused move to build a diagnostic (`chess.go:417-421`, `chess.go:756-766`), so an illegal-move spammer pays generation per record. `Decision` re-folds the entire prefix on a cache miss (`chess.go:851-870`), which is quadratic over the log. The rules state per game grows with move count. | Eleven dialect limits, all on the JSONata and source side (`dialect.go:139-151`). `MaxRowChanges` bounds one evaluation's row changes (`evaluate.go:187-189`). There is no work unit, no memory bound, and no per-capability budget. | The bounds chess needs are exactly the ones the core does not have. | **Extend Tailapps first.** A capability seam without a deterministic work metric would move the unbounded work from Go into an unmetered call, which is worse than today. |
| **Differential replay** | Native fold against candidate fold over the same verified logs, comparing decisions, refusal facts, seats, move chain, FEN, outcome, method, materializations, and tied-timestamp identity (chess note lines 383 to 404, gates at lines 406 to 428). | A host-neutral conformance corpus with frozen goldens and frozen diagnostics, seven case families (`corpus/README.md:58-79`), whose runner is unexported (`corpus/README.md:14`). | The corpus is the right mechanism and covers none of chess. Chess needs its own corpus, and it needs the runner. | **Extend Tailapps first** for the exported runner, then build the chess corpus as ordinary application work. |

### Conclusion: extend-first

Chess is **extend-first**, not adopt and not reject.

Reject is wrong. Four of the nine rows are adoptable today or need only
application rewriting: vocabulary, topology, and the parts of confinement and
determinism the core already enforces. The core is a plausible chess host.

Adopt is wrong for three independent reasons, any one of which is sufficient.

1. **The capability seam does not exist at all.** Not in reduced form, not
   behind a flag. Chess cannot express its rules engine, its secret
   comparison, or its identity context without it. This is not a gap that
   application work can close.
2. **The only path that works today is the forbidden one.** A host can put
   anything into `EvaluationInput.Meta` (`evaluate.go:25`) and the core will
   pass it through unexamined. That would deliver identity context this week
   and would be undeclared, unbounded, invisible to identity, and impossible
   to refuse per fold. The stable-extensions note names this exact shortcut
   and forbids it (lines 55 to 58, 361 to 364). This note refuses it too.
3. **The bounds chess needs are the bounds the core lacks.** Adding a
   capability call without a deterministic work metric converts today's
   visible unbounded Go work into invisible unbounded work inside an
   evaluation that claims to be bounded. That is a regression in the property
   the whole design exists to protect.

**No chess-specific shortcut enters the shared core.** The extension must be
designed against the stable-extensions note's six rules and its eight
admission gates, upstream, with chess as its first application and not its
specification. That is follow-on request I7, and it depends on nothing in
the inventory adoption except the module split, so it can proceed in
parallel.

One finding for the chess repository regardless of adoption:
`chess.go:531` stores the rules library's `Method().String()` directly into
the projection, which the chess note's own rule at lines 266 to 267 forbids.
That is worth fixing whether or not chess ever moves.

---

## 4. Workroom

The workroom stays independent of Tailapps and of JSONata.

`notes/2026-09-04-fold-simplification-study.md:1252-1253` concludes: "Do not
depend on Tailapps or on JSONata for the workroom fold." Its reasons are at
lines 1377 to 1415: the determinism evidence in this repository is negative
and the spike's own conclusion names open production blockers; the size
comparison is between an engine and a program and is therefore not a
comparison; the fold's difficulty is coupling between merge, succession,
staleness and authority, which a declarative surface does not remove; and
each layer-5 dependency is a compatibility axis that must be negotiated
forever (`docs/reference/architecture.md:1565-1577`).

This note treats that conclusion as given and adds nothing to it. The study
also says the rejection is not forever, and names the revisit point as its
stage S5 with a narrower question (lines 1412 to 1415). Nothing in the
inventory adoption changes that timing.

One consequence worth stating: because the host adapter lives in the
inventory repository (section 2.12), and because `spike/jsonataddl` and
`go.mod:8`'s `jsonata-go` requirement both go away (section 2.11), adopting
the core for inventory leaves Gitseq main with strictly fewer application-
interpreter dependencies than it has today, not more.

---

## 5. Follow-on requests

| Id | Scope | Depends on | Repository |
|---|---|---|---|
| **I1** | Upstream: split `jsonataddl` into a nested module at `tailapps/jsonataddl` with its own `go.mod` requiring only `jsonata-go/jsonata` and `ncruces/go-sqlite3`. Tag it independently. Export a corpus runner over the published `manifest.json` format so a second host can run the freeze. | This note ratified | Tailapps |
| **I2** | Gitseq: define `GitseqRecord()` and the three host identity components, with a pinned-descriptor test so no component moves silently. Publish the composed digest. | I1 | gitseq-inventory |
| **I3** | Gitseq: the four host adapters of section 2.6, the transaction boundary of section 2.7, and the projection identity and reuse rules of section 2.4. | I2 | gitseq-inventory |
| **I4** | Gitseq: the three corpora of section 2.8, including the input-contract freeze and the planted-defect proof for corpus C. This lands before I5, not after. | I3 | gitseq-inventory |
| **I5** | Upstream: add a declared `meta` contract and a declared non-scalar host-event contract to `Dialect`, so the whole input-contract table of section 2.3 enters `Canonical()` and therefore the identity digest, instead of hiding in a host component string and a frozen fixture. Also validate `EnvelopeField.Type` against the scalar claim the doc comment makes; `validateDialectPolicy` checks only the field name today (`compile.go:102-106`). | I1 | Tailapps |
| **I6** | Upstream: read-at-a-position, or an authorizer that admits a host-declared shadow schema. Only if a Gitseq application needs historical reads. Not a precondition for inventory. | I1 | Tailapps |
| **I7** | Upstream: the capability and context seam, against the stable-extensions note's six rules and eight admission gates, with a deterministic work metric and a new identity component. Chess is its first application, not its specification. | I1 | Tailapps |
| **I8** | Inventory adoption and binding. Rewrite the application source per section 2.10, move `go.mod` off `github.com/generalbusiness-ai/gitseq/spike/jsonataddl` onto the core module, and replace the host binding with the composed digest. Lands entirely in `gitseq-inventory`, under that repository's workroom and against that repository's target ref. Nothing in Gitseq main changes. | I4 | gitseq-inventory |
| **I9** | Gitseq-local delivery. Remove `spike/jsonataddl`, `spike/cmd/jsonata-inventory` and `spike/cmd/jsonata-inventory-ui`, drop `github.com/jsonata-go/jsonata` from `go.mod:8`, and update `docs/reference/architecture.md` for the changed layer 5 contract and the changed surface axis. All of it in **one exact head**, with that page's candidate artifact published in the same head. Lands in the Gitseq workroom against `main`. | I8 | gitseq |
| **I10** | Chess adoption design refresh and implementation. Blocked. | I7, I9 | gitseq-chess |

I8 and I9 are deliberately two deliveries and not one. They land in different
repositories, under different workrooms, against different target refs, so
they cannot be one head even if someone wanted them to be. The split also
gives the ordering constraint of section 2.11 a place to live: I9 removes a
package that I8 must already have stopped importing. Within I9 nothing is
split further. The deletion, the dependency drop and the architecture update
are one head, because the deletion is what changes the contract the page
states.

**Go on I1** if: this note is ratified. Nothing else. The split is mechanical
and is useful even if Gitseq never adopts.

**Go on I2 and I3** if all of:

- I1 has landed and the nested module builds with only the two direct
  dependencies.
- Both pins still agree: `jsonata-go/jsonata` at
  `v0.0.0-20250709164031-599f35f32e5f` and `ncruces/go-sqlite3` at `v0.35.3`.
  The reviewer confirmed both are unmoved on Tailapps main `a79901f3`. If
  either moves later, the version-skew analysis in section 2.13 must be redone
  before proceeding.

**Go on I8** if all of:

- Corpus A green, twice, at the exact head.
- Corpus B green, twice, at the exact head, with every order-dependent and
  ambient expression in `spike/jsonataddl/CORPUS.md` refused at compile by
  the core.
- Corpus C byte-identical over the inventory log on all three comparisons of
  section 2.8, twice, and failing when a defect is planted. In particular the
  `gitseq_decisions` row set must match including its absences: a skipped
  record produces no row on either side.
- The input-contract freeze of section 2.8 green: the complete normalizer
  `EvaluationInput` golden plus one golden per `event` scalar, per `rests_on`
  case, and for `payload`, all matching the table in section 2.3 byte for
  byte. An unfrozen input contract is a `host.canonicalization` value that
  means nothing.
- The composed digest is recorded in the binding and in the projection
  identity, and a deliberate change to any one of the nine components is
  shown to change it.

**Standing rule, not a gate.** Every dependency pin move is corpus-gated,
including a move where no component string changes. Eight of the nine
components are hand-written literals (section 2.3), so an unchanged digest is
not evidence of unchanged meaning. Running the three corpora is the only
mechanical check this design has for implementation drift, and skipping it
because "the digest did not move" is the specific mistake that reasoning
invites.

**No-go, and stop, if any of:**

- The core's `core.jsonata` component changes value without a corresponding
  determinism corpus run. That component is the only declared thing standing
  between a JSONata upgrade and a silent replay change, and it is a literal,
  so it can also fail to move when the evaluator does.
- Corpus C cannot be made to agree without excluding something other than the
  two exclusions named in section 2.8. A third exclusion means the folds are
  not equivalent, not that the harness needs tuning.
- Upstream declines I1 and the only path is depending on the whole Tailapps
  module. Carrying an observability platform's dependency graph into an
  inventory application is not worth the deduplication.
- Deterministic step and allocation bounds are still absent at the point
  where a production log, rather than a fixture log, would be interpreted.
  `spike/jsonataddl/CORPUS.md:58-59` calls these production blockers and this
  note does not overrule that.

---

## 6. How to verify this note

All commands are offline and read-only. Run them from the Gitseq worktree
root unless stated. `TA` is the module cache path.

```sh
# 0. Bases.
git merge-base HEAD origin/main          # 79e8400888a00a38ccdf96722eebcba2491a9780, the main this note is based on
TA="$(go env GOMODCACHE)/github.com/generalbusiness-ai/tailapps@v0.1.3"

# The pinned tag commit. Prints the v0.1.3 VCS hash e82a5b06775823...
cat "$(go env GOMODCACHE)/cache/download/github.com/generalbusiness-ai/tailapps/@v/v0.1.3.info"

# Gitseq does not depend on tailapps today.
grep -c tailapps go.mod go.sum

# 1. Core size and public surface.
ls "$TA"/jsonataddl/*.go | grep -v _test | xargs wc -l | tail -1     # 2709 total
find "$TA" -name '*.go' -not -path '*/internal/*' -not -name '*_test.go' | sort

# 2. The nineteen-function allowlist.
sed -n '20,24p' "$TA"/jsonataddl/confine.go | tr ',' '\n' | grep -c '"'   # 19

# 3. The nine required identity components.
sed -n '21,31p' "$TA"/jsonataddl/identity.go

# 4. The eleven dialect limits and the nine Tailapp envelope fields.
sed -n '139,151p' "$TA"/jsonataddl/dialect.go
sed -n '168,178p' "$TA"/jsonataddl/dialect.go

# 5. The seven corpus case families.
ls "$TA"/jsonataddl/corpus/v1

# 6. The topology, private-event and authority refusals.
sed -n '68,108p' "$TA"/jsonataddl/compile.go

# 7. The authorizer denies any schema other than "" or main.
sed -n '33,52p' "$TA"/jsonataddl/authorizer.go

# 8. Wall time is not a semantic bound.
sed -n '20,24p' "$TA"/jsonataddl/compile.go
sed -n '13,19p' "$TA"/jsonataddl/evaluate.go

# 9. REFERENCES, DEFAULT, GENERATED, AUTOINCREMENT, COLLATE are refused.
sed -n '49p' "$TA"/jsonataddl/compile.go
sed -n '337,340p' "$TA"/jsonataddl/compile.go

# 10. At least one export is required; exports cannot read views.
sed -n '843,881p' "$TA"/jsonataddl/compile.go

# 11. SQL functions and qualified names are refused in reads, views, exports.
sed -n '571,599p' "$TA"/jsonataddl/compile.go

# 11a. Single-writer tables. The refusal, and the corpus case that freezes it.
sed -n '621,636p' "$TA"/jsonataddl/compile.go
cat "$TA"/jsonataddl/corpus/v1/reject-multiple-writers/expected/diagnostic.txt

# 11b. The seven focused DDL statements the core recognizes.
sed -n '27,33p' "$TA"/jsonataddl/compile.go | wc -l                  # 7

# 12. The seat-and-release authorizer pattern in the Tailapps host.
sed -n '72,89p' "$TA"/internal/projection/projection.go
sed -n '477,500p' "$TA"/internal/projection/projection.go

# 13. Dependency pins agree.
grep -n 'jsonata-go/jsonata\|ncruces/go-sqlite3 ' go.mod
grep -n 'jsonata-go/jsonata\|ncruces/go-sqlite3 ' \
  "$(go env GOMODCACHE)/cache/download/github.com/generalbusiness-ai/tailapps/@v/v0.1.3.mod"

# 14. Gitseq-local duplicate size and deletion list.
find spike/jsonataddl -name '*.go' | xargs wc -l | tail -1        # 3716 total
git ls-files spike/jsonataddl | xargs wc -l | tail -1             # 4263 total
wc -l spike/jsonataddl/profile.go spike/jsonataddl/projection.go \
      spike/jsonataddl/history.go spike/jsonataddl/inventory_ui.go
# 407 + 1012 + 237 = 1656 lines of compiler, evaluator and history.

# 15. The determinism corpus and its own stated blocker.
sed -n '19,39p' spike/jsonataddl/CORPUS.md
sed -n '54,59p' spike/jsonataddl/CORPUS.md

# 16. The two spike views the core refuses.
sed -n '28,35p' spike/jsonataddl/inventory/application.sql

# 17. The fold study's conclusion on the workroom.
sed -n '1252,1253p' notes/2026-09-04-fold-simplification-study.md
sed -n '1377,1415p' notes/2026-09-04-fold-simplification-study.md

# 18. The inventory repository.
git -C ~/play/gitseq-inventory rev-parse HEAD origin/main
git -C ~/play/gitseq-inventory ls-files | wc -l                   # 0, empty checkout
git -C ~/play/gitseq-inventory ls-tree -r --name-only origin/main # 11 files
git -C ~/play/gitseq-inventory show origin/main:go.mod
git -C ~/play/gitseq-inventory show origin/main:application.go
git -C ~/play/gitseq-inventory show origin/main:application.sql
git -C ~/play/gitseq-inventory show origin/main:folds/inventory.jsonata

# 19. The chess repository.
git -C ~/play/gitseq-chess rev-parse HEAD
sed -n '28,54p'   ~/play/gitseq-chess/chess.go   # eight schemas, chess-fold@2
sed -n '193,224p' ~/play/gitseq-chess/chess.go   # one-stage fold
sed -n '410,422p' ~/play/gitseq-chess/chess.go   # MoveStr and the diagnostic cost
sed -n '517,540p' ~/play/gitseq-chess/chess.go   # Method().String() into the projection
sed -n '696,712p' ~/play/gitseq-chess/chess.go   # fail-closed seat allowlist
sed -n '851,870p' ~/play/gitseq-chess/chess.go   # prefix refold in Decision

# 20. The oracle's result mapping, which section 2.7 must reproduce:
#     an unbound schema advances the frontier and writes no decision row,
#     a bound schema writes exactly one, and a bad payload is a hard error.
sed -n '510,523p' spike/jsonataddl/projection.go
sed -n '551,566p' spike/jsonataddl/projection.go

# 20a. The input-contract table of section 2.3, against the real record type.
sed -n '206,228p' host/host.go            # host.Record: ID, Actor, Schema, Payload, RestsOn, Timestamp
sed -n '224,226p' host/host.go            # timestamp is Unix SECONDS, verbatim
sed -n '794,804p' host/host.go            # every field assigned verbatim from the kernel event
sed -n '41,43p'   host/identifier.go      # EventID: git:<fmt>:<genesis>#git:<fmt>:<commit>
sed -n '13,15p'   host/identifier.go      # actor fingerprint is 64 lowercase hex characters
sed -n '191,194p' internal/intent/intent.go  # ActorFingerprint = hex(sha256(public key))
sed -n '161p'     spike/jsonataddl/projection.go  # position is one-based: index + 1
sed -n '168,171p' "$TA"/jsonataddl/load.go       # the "sha256:" + lowercase hex spelling
sed -n '528,532p' spike/jsonataddl/projection.go # the oracle's four-key meta, which this design drops

# 21. Only the dialect component is content-hashed. The other eight are
#     hand-written literals, which is why every pin move stays corpus-gated.
sed -n '245,255p' "$TA"/jsonataddl/dialect.go        # DialectComponent hashes Canonical()
sed -n '109,117p' "$TA"/jsonataddl/identity.go       # five core literals
sed -n '24,38p'   "$TA"/internal/profile/runtime.go  # three host literals, and the discipline note

# 22. Regression gate for this note's own head.
git diff --check
make docs
```

---

## 7. Limits

Everything I could not verify.

1. **Nothing was executed.** Gitseq's `go.mod` does not require Tailapps, I
   added no dependency, and I ran no `go doc`, `go build` or `go test`
   against it. Every behavioural claim about the core is read from source.
   In particular I did not run the Tailapps conformance corpus, so I cannot
   say its goldens still pass at v0.1.3. I can only say the corpus exists and
   what its README says it covers.
2. **The corpus runner.** I confirmed it is invoked as
   `go test ./internal/profile -run TestConformanceCorpus`
   (`corpus/README.md:14`) and that `internal/profile` is not importable from
   outside the module. I did not read the runner, so I cannot describe how
   much work exporting it is. I1 must scope that upstream.
3. **No measurement.** No timing, no memory figure, no throughput claim
   appears in this note. The performance of the core relative to
   `spike/jsonataddl` is unknown.
4. **The inventory checkout is empty.** All inventory citations read
   `origin/main` at `7cfdc271503f490a4f80b533bc64ec27b3ce9a49` through
   `git show`. I did not confirm that this local `origin/main` matches the
   remote, because that needs a fetch.
5. **The chess survey.** Read at `0def23f727c82b2f51e29af05e1b9336cb5c1d80`
   with an untracked 16 MB build artifact present in the working tree. I
   spot-verified the load-bearing citations myself at that commit. I did not
   read `cmd/chess/web.go` or the test files in full, so the resource-bound
   row of section 3 reports the costs I found, not a complete inventory of
   them.
6. **jsonata-go determinism.** I did not independently test the evaluator. I
   rely on `spike/jsonataddl/CORPUS.md`, which is this repository's own
   evidence and whose conclusion at lines 58 to 59 is that deterministic step
   and allocation bounds and a complete safe-number contract remain
   production blockers.
7. **The `MaxRowChanges` claim for chess.** I state that a chess dialect must
   set the bound against the worst-case legal-move count. I did not compute
   that worst case, and I did not measure the largest `legal_moves` set the
   current chess code produces.
8. **Compile-behaviour claims about the inventory source.** I read the
   compiler and reasoned about what it would do with the shipped
   `application.sql` and `folds/inventory.jsonata`. I did not compile them
   against the core, because that needs a dependency this request may not
   add. Section 2.10's five required changes are derived by reading, not by
   running. A reviewer should treat them as the strongest claims in this note
   and the first to check once a scratch module exists. The review of the
   first draft found a real error of exactly this kind: two folds writing one
   table, which `compile.go:630-632` refuses. Reading is not compiling, and
   one such defect has already escaped.
9. **The result mapping is designed, not measured.** Section 2.7's mapping
   and section 2.8's three Corpus C comparisons are derived by reading the
   oracle at `spike/jsonataddl/projection.go:510-566` and the core at
   `evaluate.go:90-194`. I did not run either side, so I have not observed
   the two producing equal `gitseq_decisions` and `gitseq_facts` rows over a
   real log. That equality is the whole point of I4, and I4 exists precisely
   because this note cannot establish it.
10. **The input-contract table's example column is illustrative.** Every
    *source*, *conversion* and *encoding* cell in section 2.3's table is
    verified against `host.Record` and its construction at main 79e84008, and
    section 6, item 20a re-derives each one. The example strings are shapes
    written by hand, not values computed from a real record: I did not build a
    `host.Log` and print its first record. The first thing the input-contract
    freeze in section 2.8 must do is replace those examples with computed
    goldens, which is why the freeze asks for a fixture record rather than a
    table of literals.
11. **The `EnvelopeField.Type` gap.** I report that
    `validateDialectPolicy` checks the envelope field name but not its type
    (`compile.go:102-106`), while the doc comment describes a "typed scalar"
    envelope (`dialect.go:56-63`). I did not construct a dialect with a JSON
    envelope field to see what actually happens downstream, so I state the
    validation gap and not its consequence.
12. **Tailapps main was verified by the reviewer, not by me.** The claim that
    main `a79901f3` changes nothing in `jsonataddl`, `go.mod` or `go.sum`
    relative to v0.1.3 is the reviewer's, reported to me. I had no network
    access and did not reproduce it. Section 6 verifies the v0.1.3 side only.
