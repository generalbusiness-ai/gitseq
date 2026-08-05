---
date: 2026-08-05
status: design sketch — nothing built. Reworked the same day after
  external review; this repo's own first-parent history is the first
  (hand-run) log, genesis = the draft as reviewed.
origin: spinoff from the woo "engine over log" discussion (Beyond Zero →
  orchestration locus → event-log engine). This note is deliberately
  self-contained and woo-free.
---

# gitseq — git extended with a sequencer and a liveness nexus

## Intent

An append-only log of small, signed, causally-linked events is the
substrate for a class of systems (artifact-under-feedback-control
engines, audit-grade coordination, multi-agent orchestration). The
substrate needs exactly three capabilities:

1. **Substrate** — durable, content-addressed, causal-parented,
   tamper-evident, verifiable and forkable offline.
2. **Sequencing under concurrent writers** — a total order per stream,
   assigned by an authority at append time, not reconstructed socially
   after the fact by merging.
3. **Liveness** — a bell: subscribers learn of appends promptly, and
   can see who else is currently listening.

Git already provides (1) better than anything purpose-built, and it
provides ubiquity and exit for free. It lacks (2) — concurrent `git
push` serializes by non-fast-forward rejection and client-side
rebase-retry, i.e. merge-as-serialization — and it lacks (3) entirely
(pull-based, dead storage between fetches).

gitseq is the smallest thing that adds (2) and (3) to stock git
**without adding any semantics**. It never interprets payloads. All
meaning — ontologies, attestation ladders, supersession, projections,
policy content — lives in layers above, as event content and signature
conventions. gitseq is small, dumb, and composable: a wire discipline
plus two thin services over an ordinary git repository that any stock
git client can clone, verify, and walk away with.

## The log convention (no software required)

**A log's identity is its genesis commit hash.** The ref that carries
it — `refs/seq/<log>`, advancing by fast-forward only, one commit per
event — is a locator, not an identity: refs get renamed, repos get
mirrored, forks share history. URLs likewise. Wherever this note names
a log, it names the genesis hash; refs and URLs are hints.

**Genesis is the trust anchor.** The genesis commit (no parents)
declares in its envelope the sequencer's public key and the
deployment constants (payload size ceiling, causal-parent width cap).
The log carries its own root of trust — identity-equals-genesis-hash
covers the key — so "sequencer-signed, per whom?" has an in-band
answer, and no out-of-band PKI sneaks back in through a "the
sequencer publishes its key on demand" side door.

**Key rotation is the only event type the base convention knows.** A
rotation event is a special envelope signed by the current key,
naming its successor; verification walks the spine tracking the
current key. Nothing else earns a reserved type.

**Event = commit**, with this mapping:

- **First parent** = the previous event in the log. A `--first-parent`
  walk is the log in sequence order. Genesis has no parents.
- **Additional parents** = intra-log causal parents ("this event rests
  on those"). Git's merge-parent machinery, reused as a causality DAG;
  no merge semantics implied. The full DAG is the causal graph; the
  first-parent spine is the sequence. One rendering caveat: causal
  parents are already ancestors via the spine, so every event that has
  them is a merge commit whose extra parents are "redundant" — legal
  git, but some tooling renders such merges oddly and `git show` will
  look like merge noise. Deployments cap parent width (a genesis
  constant) and overflow into `Rests-On:` trailers, which work
  intra-log too.
- **Cross-log causal parents** cannot be git parents (they may live in
  another repo). They are `Rests-On:` trailers (see below). Opaque to
  gitseq.
- **Author** = the submitting actor, with the actor's signature carried
  in the envelope (see below). What the actor asserted.
- **Committer** = the sequencer, and the commit is **signed by the
  sequencer**. What the authority ordered, and when. The
  author/committer split that git already has is exactly the
  assertion/ordering distinction; no new field needed. And git carries
  exactly one signature slot per commit (`gpgsig`) — this design
  spends it on the sequencer, which is why the actor's signature rides
  in the envelope rather than as a second commit signature. The
  allocation is forced by git, and lands exactly right.
- **Commit message** = the event envelope: a small header (schema id,
  idempotency key, actor signature over the payload, arbitrary
  trailers), then nothing else. gitseq validates only shape and size.
- **Tree** = the payload: one blob at `event`, plus optional blobs
  under `attachments/`. Content-addressed sharing of repeated payloads
  comes free from git. The sequencer enforces the genesis-declared
  size ceiling and never reads the bytes.

**Two trailers are load-bearing** — what an event rests on, and what
an artifact was rendered from. Both are opaque strings to gitseq:

- `Rests-On: <genesis-hash>#<event-hash>` — a causal parent beyond
  git-parent reach: in another log, or past the parent-width cap. An
  optional `Rests-On-Hint: <url>` may ride along as a locator; the
  URL may rot, the hashes don't.
- `Projected-From: <genesis-hash>#<event-hash>` — stamped on a
  projection commit (in `refs/proj/*` or anywhere) naming the event
  it was rendered from. This one line makes staleness computable for
  the layers above — compare the named event's depth to the current
  head, walk the gap — without gitseq building or consulting any
  projection itself.

**Verification is stock git, in three grades** — and a reader should
know which one they hold:

- **Full audit**: walk the first-parent chain from head to genesis,
  tracking the current sequencer key through rotation events; check
  each commit is signed by the then-current key, single-event-shaped,
  and fast-forward-chained; check actor signatures for the events you
  care about. O(history), offline, from any clone.
- **Incremental audit**: a **checkpoint** is (event hash, depth,
  sequencer signature, optionally witness cosignatures). A head you
  verified yesterday is a checkpoint; today you verify only the
  delta. Audits become O(new events), and a shallow clone from a
  trusted checkpoint is sound for anyone who trusts its signatures —
  which is most practical readers.
- **Witnessed head**: sequencer signatures alone cannot rule out
  equivocation — showing different heads to different audiences. That
  takes **witnesses**: third parties that fetch, verify the delta,
  check fast-forward consistency against the last head they saw, and
  countersign (the transparency.dev cosigning pattern; CT's hard
  lesson is that "compare heads out of band" is exactly where
  split-view attacks live). A witness is a stock git client plus a
  signature. v0 ships no witness software, but the cosignature format
  sits in the base convention's verification model, because a
  reader's guarantee is materially different with and without one.

Anyone holding a clone can audit offline, or fork the log and
continue under a different sequencer. Exit costs nothing. Sequence
number is first-parent depth — derivable, so not stored
authoritatively; the sequencer MAY stamp `Seq:` as a convenience
trailer but the chain is the truth.

## The sequencer (service 1)

One sequencer per log; a sequencer process may host many logs. It is
the only writer of `refs/seq/*`. Its whole contract:

```
submit(log, envelope, payload-tree, causal-parents, idempotency-key)
  → { commit-hash, head }   | replay of prior reply for same key
  | refused(shape | size | unsigned | unknown-log | back-pressure)
```

- **Admission is shape-only.** Well-formed envelope, size ceiling,
  actor signature present and valid, causal parents resolvable in-log.
  The sequencer never reads the payload blob. A deployment MAY plug a
  **pre-append hook** here — the natural policy locus. The hook sees
  the envelope only (schema id, actor key, trailers), never the
  payload, so policy like "a ratification event is refused unless the
  actor key is a recognized ratifier" is expressible without
  breaching semantic-freedom. **v0 ships the seam and zero hooks** —
  a hookless sequencer forces every serious deployment to fork it,
  which is how semantic-free substrates die.
- **Ordering is server-side chaining**, not client CAS. Because events
  are pure appends with no shared mutable state, concurrent submissions
  never conflict; the sequencer chains them in arrival order and signs.
  This is the whole advantage over plain `git push`: no thundering
  rebase-retry under contention, and the write authority granted to a
  submitter narrows from "update this ref" to "append one well-formed
  event" — least authority, and history rewrite is structurally
  impossible for clients.
- **Idempotency**: client-supplied key in the envelope — hence in the
  commit message, hence in the log. **The log is the dedup index**:
  the authoritative record of used keys is a projection of the log
  itself, rebuildable by any successor on restart or failover. The
  in-memory reply cache is an optimization over it, and eviction can
  never cause a duplicate append — eviction loses the cache, not the
  index. A replay returns the original commit hash; a different
  request under a used key is refused terminally.
- **Back-pressure is explicit**: at capacity the sequencer refuses
  before chaining, cheaply, and says so.
- The sequencer serves, on demand, a **checkpoint** of each log it
  hosts — (head hash, depth, sequencer signature) — the Certificate
  Transparency signed-tree-head move, feeding the verification grades
  above and giving witnesses something to countersign. Its key needs
  no publication channel: it is in genesis, rotated in-band.

That is the entire sequencer. No queries, no projections, no schemas —
and no durable state of its own: head, dedup index, and current key
are all reconstructible from the log. The sequencer is **stateless
but for its private key**, which is the strongest thing one can say
about its failover.

## The nexus (service 2)

The bell and the roster. Equally dumb:

```
subscribe(log, from)  → stream of new commits (or head notices; client fetches)
presence(log)         → current leased subscriber set
```

- Fanout is **at-least-once, resumable by sequence**: a subscriber
  reconnecting with `from=<hash|depth>` replays the gap from the repo
  itself — the log *is* the durable queue, so the nexus holds no
  durable state at all and can be killed and restarted freely.
- **Presence is leased and ephemeral, and is not written to the log.**
  Only events enter the log; who is currently listening is a liveness
  fact, not a historical one. (A layer above may choose to *observe*
  presence changes into a log as events; gitseq does not.)
- The nexus also fans out **checkpoints and witness cosignatures** —
  cheap, cacheable, and like everything else it serves, independently
  verifiable: it is a distribution point, never a trust point.
- The nexus is a read-side cache by construction: it can never disagree
  with the repo, only lag it.

Transport is boring on purpose: SSE or WebSocket beside the git smart
HTTP endpoint. A deployment without the nexus degrades to polling
`git fetch` — everything still works, just without the bell.

## What gitseq refuses to do

- No ontology. Event types, attestation levels (asserted / extracted /
  ratified), supersession — all envelope/payload conventions above.
  (Rotation is the sole reserved type, and it is about the log's own
  key, not the payloads.)
- No mutation, ever. Corrections are new events; a log is never
  rewritten. Redaction-by-necessity is a fork, and a fork is a precise
  object here: same genesis, divergent sequencer signatures after the
  fork point — detectable by any reader holding both heads, and
  attributable to the keys that signed each.
- No projections. Derived state (indexes, renders, materialized views)
  lives in `refs/proj/*` or other repos, built by any tool, disposable,
  never consulted by the sequencer. The one concession is the
  `Projected-From:` trailer above — a convention so layers above can
  compare, not machinery.
- No queries. `git log`, `git grep`, and layers above.
- No cross-log transactions or global views. One log, one order.
  Cross-log causality is reference-only (`Rests-On:` trailers);
  anything needing an order *across* logs must put its events in one
  log. Logs are cheap; make more.
- No identity system. Actors are keys; binding keys to people or
  agents is a layer above (possibly itself a log). The sequencer's own
  key is the one exception, and it is anchored in genesis.

## Scale and failure shape

Per-log serialization caps a single log's throughput at one chaining
writer — which is the point; parallelism comes from having many logs,
and nothing in gitseq enumerates logs globally.

**Failover separates two questions** that a ref CAS alone conflates.
*Storage*: the new sequencer must win the compare-and-swap on
`refs/seq/<log>` at the current head; a stale twin's push loses the
race. *Authority*: it must hold the currently-rotated-to key, or
append a rotation event signed by it. Winning the CAS without the key
yields commits every reader refuses; holding the key without the CAS
yields a visible fork. Both properties are checkable from the log
alone.

**Compaction is a checkpoint made durable.** Logs are forever by
default; to seal one, declare a final checkpoint and start a
successor whose genesis carries the predecessor's genesis hash and
sealed head. A chain of sealed logs remains one verifiable identity —
readers walk across the seam the same way they walk the spine, and
`Rests-On:` references into sealed history keep resolving.

The repo itself can live on any git hosting; the sequencer just
needs exclusive push to `refs/seq/*` — enforceable with existing
per-ref permissions on every major forge, which means a v0 sequencer
can be **a bot account with a protected ref namespace**, and v0 nexus
a webhook-to-SSE relay. The whole thing is deployable today on
infrastructure everyone already runs.

## Prior art (nods, and deltas)

- **Certificate Transparency (RFC 6962)** — the closest spirit: a dumb
  append-only log, shape-only admission, signed heads, third-party
  verifiability. gitseq is CT generalized to arbitrary payloads on git
  plumbing, plus liveness; the witness convention is the
  transparency.dev cosigning pattern, adopted rather than reinvented.
- **Secure Scuttlebutt / Nostr** — signed append-only events, but
  per-author feeds with gossip; no multi-writer total order per topic,
  which is the one thing the engine needs most.
- **Radicle** — p2p git with its own gossip layer; heavier, and
  collaboration objects carry semantics. gitseq stays semantic-free.
- **Kafka et al.** — the right ordering/liveness shape, but opaque
  storage: no content addressing, no offline verification, no fork/exit,
  and durable retention is the operator's problem rather than a clone.
- **Plain git server with protected refs** — the degenerate v0 (see
  above); gitseq's deltas are server-side chaining under contention,
  ordering countersignature, an in-band trust anchor, idempotency,
  and the bell.

## Open questions

1. **Payload ceiling and large artifacts** — attachments by hash
   reference (`Rests-On:`-style trailer to an object store or git-lfs)
   vs. inline blobs; where the genesis-declared ceiling should sit.
2. **Private logs** — encrypted payload blobs with envelope in the
   clear sequence fine (sequencer is shape-only by design); key
   distribution is a layer above; is that enough?
3. **Timestamp trust** — committer time is sequencer-asserted. A
   witness cosignature already carries an independent observation
   time; is that enough, or is an RFC 3161 / roughtime
   countersignature worth a base-convention seat?
4. **Trailers-only causality** — causal parents as git parents buy
   ancestry-at-a-glance but cost merge-rendering noise and a width
   cap, and `Rests-On:` already expresses the same edge intra-log.
   Should trailers be the *only* mechanism, freeing extra parents
   entirely? A real simplification; worth a spike before v0 freezes
   the convention.
5. **Checkpoint cadence and witness sets** — how often heads are
   checkpointed and how many cosignatures a reader demands are
   deployment policy; does the base convention need vocabulary for
   stating that policy, or does it live wholly above?

Resolved since the first draft, by review: log identity (genesis
hash), the trust anchor and key rotation, fork detection (witness
cosigning), incremental verification (checkpoints, which also absorb
compaction), idempotency state (the log is the index), and the hook
seam (ship it, hookless). The sections above absorb all of it; the
shape did not change.
