# Previous-frontier readers during a rebuild

Status: adopted: declined, 2026-09-09, proposal 7493bb29.

Request e855a8ca (#20301), the case-C child of #20008, continued by the
current-basis successor 4fe7a689 (#22186). The source references below carry
no revision, so each one opens at this note's own exact commit. Those files
were re-read against main `d1ae6ade996af7167f22a2170dfaa6a200e004e1`, the
parent of this commit, and hold the same bytes at both. This note replaces
proposal 0b8a9ff5 (#20195), whose ancestry check and header-copy argument did
not survive the review recorded in the request, and corrects its own first
draft (7628c518) after the planner's finding 01386505 (#20633). The decision
it explains is already adopted. It authorises no source change.

## The question

A first-time reader (a browser tab with no retained status, a CLI or MCP
call) that arrives while the resident is verifying durable history sees
progress and nothing else. #20008 delivered the already-open-browser case:
a page that holds a status keeps showing it, qualified, while the resident
reports a rebuild under the same fold profile. Case C asked whether the
resident could offer a first-time reader a previously verified frontier
during that window, under an explicit read contract. The answer, adopted on
2026-09-09, is no.

## What the source says

**When "rebuild running" is true.** `/v0/rebuild` reports
`Workspace.RebuildProgress`
([server.go:570](internal/service/server.go:570),
[app.go:2387](internal/app/app.go:2387)),
and `running` is the kernel tracker's `Started`, which "becomes true only
when checkpoint lookup has fallen back to a full audit"
([kernel.go:797](internal/kernel/kernel.go:797));
"a checkpoint hit or incremental read leaves the fresh tracker unstarted"
([kernel.go:869](internal/kernel/kernel.go:869)).
So the rebuild notice, and the whole case-C window, is the full cold audit
and nothing else. A long incremental tail is verified without a running
report.

**When a retained snapshot exists.** `snapshotWithSource` holds
`snapshotMu` for the whole read
([app.go:2508](internal/app/app.go:2508)).
It keeps `snapshotCache`, `snapshotFolder` and `snapshotProfile` from the
previous publication and replaces them only after a complete successful
audit
([app.go:2629](internal/app/app.go:2629),
[app.go:2643](internal/app/app.go:2643)).
The full audit runs in exactly these situations:

1. First read of a process: `w.reader == nil`, no cache. After a restart
   the process holds nothing from before; what it verifies is the signed
   checkpoint or, failing that, the whole log.
2. Cache present under the same profile, but the verified tail does not
   contain the cached head (`start < 0`): the reader is replaced and a full
   audit runs
   ([app.go:2575](internal/app/app.go:2575),
   [app.go:2584](internal/app/app.go:2584)).
   This is the rewind or replaced-ref case. The retained snapshot describes
   a head that is no longer on the ref.
3. Profile differs from the cache's: the cold path is taken and the old
   projection is never re-published
   ([app.go:2584](internal/app/app.go:2584)).
   The parent request forbids exposing it.

When the cache head is the verified base of the new tail (`start >= 0`)
the read is incremental, the tracker never starts, and no rebuild is
reported
([app.go:2575](internal/app/app.go:2575)).
The act path extends the cache in place only when the cache head equals the
commit's base
([app.go:2316](internal/app/app.go:2316)).

There is a fourth situation, and an earlier draft of this note wrongly
said there was none. The kernel reader advances before the witness is
persisted: `rememberVerifiedFrontier` runs after the load
([app.go:2568](internal/app/app.go:2568)),
so a storage failure there leaves the reader at the new head B while the
retained snapshot and persisted witness stay at A. If the ref is then moved
back to A and a valid C is appended on it, and no signed checkpoint is
selectable, the next read is a full cold audit that reports `Started` while
a same-profile retained A, an ancestor of C, is still held; the audit then
publishes C.

That is not an argument, it is a measurement. The planner ran a paired
isolated probe at exact `7628c518bb2fffab2597f0084f220f4dc642981c`, the
first draft of this note, and filed it as finding 01386505 (#20633) on
2026-09-07. Both arms reproduced: with both checkpoint selectors absent the
reader reported `Started` during a cold full audit while holding
same-profile A, and with a valid signed checkpoint available the same
fixture correctly gave no cold progress. The probe read the retained
snapshot inside the real kernel progress gate, before publication, and
changed no live ref, witness, key or process. That evidence stands as
filed and is not re-run here.

So ancestry does not imply an incremental read, and "a retained ancestor
cannot occur during a cold audit" is false. What remains true is narrower,
and it is what the decision below rests on: in that state the retained
snapshot is not the verified base of the audit in flight (the reader was
replaced; the audit has no base), the endpoint cannot know before
verification finishes that C will verify, and Git ancestry confers no
verification of the history between A and C.

**Immutability.** `Folder.Projection()`
([fold.go:536](internal/workroom/fold.go:536))
runs `foldState.project()`, which builds a new `Projection` with fresh
slices and maps on every call
([fold.go:3169](internal/workroom/fold.go:3169)).
A `Snapshot` value therefore does not alias the folder's later appends; the
strings and record-level slices it shares are never mutated after the
record is folded. The earlier proposal's worry that a copy of the struct
header shares mutable slices with the folder is not what the source does,
and neither is the request's warning that copying the header proves
immutability: what proves it is that projection output is built per call.
Any design below still publishes a snapshot under one owner and one atomic
pointer, so the guarantee is stated, not inferred.

**Consumers and their checks.** The CLI compares the resident's frontier
with the local ref and falls back to a local verified read on mismatch; the
MCP adapter refuses an orientation whose frontier is not the local head
read before and after the call. The browser tracks whether the page is
showing a status and under which profile
([store.ts:45](ui/src/lib/store.ts:45)),
sets both from whatever `/v0/status` and `/v0/wait` return
([store.ts:47](ui/src/lib/store.ts:47)),
and, while a wait is outstanding, probes `/v0/rebuild` and drops a retained
status whose profile differs from the answering process's
([store.ts:78](ui/src/lib/store.ts:78),
[store.ts:91](ui/src/lib/store.ts:91)).
A reader with no status renders the rebuild notice
([RebuildNotice.tsx:13](ui/src/components/RebuildNotice.tsx:13)).
The browser is the only consumer that could use a historical surface; the
CLI and MCP adapters would discard it by their own contracts.

## What follows

Put the findings together. The window in which a first-time reader sees
a rebuild is the full cold audit. During a full cold audit the process
holds either no earlier snapshot (restart), a snapshot for a head the ref
no longer has (rewind), a snapshot under a profile this binary does not
interpret (forbidden), or, after a witness-persistence failure followed by
a rewind onto the retained head, a same-profile snapshot that is an
ancestor of the history being audited. Only the last could be offered as
"a verified frontier of this history from before I began", and offering it
would rest on Git ancestry plus a verification that has not yet happened.
That is the check the earlier proposal relied on, and the request is right
that it settles nothing by itself: ancestry says the retained head is in
the history; it does not say the history verifies, and the endpoint would
be answering before the kernel knows. Whether that state is worth serving
is a question of contract, benefit and cost, not of possibility.

So the remaining opportunity is not a mechanical exposure of state the
resident already has. It would need one of two new commitments:

- **Persist the last published projection across restarts** as an
  unsigned local file beside the checkpoint, and serve it while the next
  process audits from cold. The parent forbids serving a profile-invalid
  projection, and an invalid or missing checkpoint is the usual reason a
  restart audits from cold at all; the case it would serve is a valid,
  same-profile checkpoint that the process nevertheless re-audits, which
  today does not happen (a valid checkpoint loads as a tail without a
  running report). This adds a second, unsigned source of truth to serve a
  case the source does not produce.
- **Report incremental tail verification as a rebuild** and serve the
  verified base during it. The base is verified by construction (it is the
  reader's own verified head, not an ancestry inference), same-profile by
  construction, and immutable under the publication rule above. The window
  is the tail's verification time after an idle period, which has not
  been measured in this workroom; adopting it would need a timing envelope
  first. It changes the meaning of `/v0/rebuild` for every reader,
  including the qualifier the parent just delivered, to cover a window the
  notice was not designed for.

Neither is small, and neither gives a first-time reader anything during the
cold audit the case was named for.

## The adopted decision

**The previous-frontier surface is declined.** Hugh ratified proposal
7493bb29 (#20657) on 2026-09-09, in act 0d8b8ac2 (#22163). The grounds are
the verification contract and the cost, not impossibility. Every state in
which the resident could offer a first-time reader an earlier frontier
during a cold audit is one where it holds nothing it has verified as the
base of the history being audited, so it would be answering on Git ancestry
ahead of the kernel. The one bounded alternative below refuses exactly those
states, so it serves nothing during a cold audit at all, and the states it
would serve, long tail verifications, are unmeasured.

Four things are settled by that adoption.

1. A first-time reader during a full cold audit sees verification progress
   and nothing else.
2. A browser that was already open keeps the status it holds, qualified,
   while the running process reports a rebuild under the same fold profile.
   That is what #20008 delivered.
3. That browser drops the retained status when the running process names a
   different fold profile.
4. Nothing is added. There is no previous-frontier endpoint, no read
   contract for earlier state, no second verifier, and no persistence of
   unsigned projection state.

That is the complete, honest experience. The final guidance is this note and
a short paragraph in the reading-view reference. No source changes.

## The alternative that was rejected

This is the bounded design put to the ratifier beside the decline, kept here
so the reasoning survives with the refusal. It was written to be adopted
as-is or refused, and it was refused. Nothing in rules 1 to 7 is proposed,
planned or in flight.

1. One read-only endpoint, `GET /v0/previous`. `/v0/status`, `/v0/wait`,
   orientation, admission and every write path keep their current-frontier
   contract unchanged.
2. The workspace publishes, under `snapshotMu` at the moment a snapshot is
   published (both the audit path and `acceptSnapshot`), an immutable
   `retained` record into an `atomic.Pointer`: the `Snapshot` value, the
   profile, the source, and the wall time. The projection inside it is
   the per-call output of `project()` and is never appended to. That is
   the ownership rule: one writer, at publication, under the same lock
   that orders publications; readers take the pointer and never the lock.
3. The endpoint answers only while a verified read is in flight
   (`flight != nil` and not done), the retained profile equals
   `Workspace.Profile()`, and the retained head equals the reader's
   verified head at the start of that flight (recorded by the flight when
   it begins; it is the base the kernel is extending, not an ancestry
   guess). Otherwise it answers 404 with the rebuild report. A cold audit
   after restart (no retained record), a rewind (retained head is not the
   flight's base), a profile change (profile differs), the fourth
   situation above (the reader was replaced, so the flight has no verified
   base and the retained head cannot equal it) and a process with no
   flight in progress all answer 404. The retained record and the reader
   are kept separate on purpose: the record is written only at
   publication, after the witness is persisted, so a failure between the
   kernel's advance and publication leaves the record at the last
   published frontier and the gate closed.
4. **Unverifiable current tail.** While the flight runs, the base is
   served with `previous: true`; the moment the flight ends, whether with
   a new snapshot or with a verification error, the endpoint answers 404.
   A failed tail therefore ends historical display rather than prolonging
   it: the page shows the failure the wait returns, and nothing labelled
   as history survives it. That was the explicit choice put to the
   ratifier; the other way, showing the old base after a failed
   verification, would present a frontier of a history the process had
   just refused to extend.
5. The answer is a distinct shape (`{previous: true, frontier, published,
   profile, status}`), never `service.Status`, so no existing client can
   mistake it for a current answer, and `DurableChanged` and the head
   clock are untouched. The browser shows it under the same qualifier
   wording the parent delivered, with "from before the resident began
   verifying" and no act affordance.
6. Cost is bounded: one pointer swap per publication, no copy beyond the
   projection the publication already built, one unlocked read per probe.
   Only the browser consumes it; the CLI and MCP adapters keep their local
   checks and never read it.
7. The kernel tracker is not changed: `running` keeps meaning a full cold
   audit. The endpoint's own gate (a flight in progress with a verified
   base) is the only new condition, so the rebuild notice and the
   qualifier delivered by #20008 keep their meaning.

Had the alternative been adopted, delivery on the same commitment would
have included server tests for a first-time reader during a tail flight,
same-profile forward rebuild, invalid profile, rewind, verification failure
mid-flight, missing cache after restart, publication ownership under
concurrent act and audit, cancellation of a probing client, and bounded
polling; browser tests for the first-time page; positive and omission
controls for each gate in rule 3 and the 404 in rule 4; and exact-head
previews. None of that work exists, because the alternative was refused.

## How the decision was taken

The proposal filed with this note carried one outcome only, the decline,
because a ratification carries a target and no text and cannot select
between two designs. Declining that proposal would have left case C as it
stood. Hugh ratified it instead, so the decline is the adopted position and
case C is closed. Adopting the alternative in rules 1 to 7 would now need a
distinct proposal of its own, and none is filed.
