---
date: 2026-08-05
status: design sketch — nothing built
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

A **log** is a git ref, `refs/seq/<log>`, that advances by fast-forward
only, one commit per event.

**Event = commit**, with this mapping:

- **First parent** = the previous event in the log. A `--first-parent`
  walk is the log in sequence order. Genesis event has no parents.
- **Additional parents** = intra-log causal parents ("this event rests
  on those"). Git's merge-parent machinery, reused as a causality DAG;
  no merge semantics implied. The full DAG is the causal graph; the
  first-parent spine is the sequence.
- **Cross-log causal parents** cannot be git parents (they may live in
  another repo). They are commit-message trailers:
  `Rests-On: <log-url>#<commit-hash>`. Opaque to gitseq.
- **Author** = the submitting actor, with the actor's signature carried
  in the envelope (see below). What the actor asserted.
- **Committer** = the sequencer, and the commit is **signed by the
  sequencer**. What the authority ordered, and when. The author/committer
  split that git already has is exactly the assertion/ordering
  distinction; no new field needed.
- **Commit message** = the event envelope: a small header (schema id,
  idempotency key, actor signature over the payload, arbitrary
  trailers), then nothing else. gitseq validates only shape and size.
- **Tree** = the payload: one blob at `event`, plus optional blobs
  under `attachments/`. Content-addressed sharing of repeated payloads
  comes free from git. The sequencer enforces a size ceiling and never
  reads the bytes.

**Verification is stock git**: walk the first-parent chain from the
signed head, check each commit is sequencer-signed, single-event-shaped,
and fast-forward-chained; check actor signatures if you care about a
given event. Anyone holding a clone can audit the whole log offline, or
fork it and continue under a different sequencer. Exit costs nothing.

Sequence number is the first-parent depth — derivable, so it is not
stored authoritatively; the sequencer MAY stamp `Seq:` as a convenience
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
  The sequencer never reads the payload blob. (A deployment MAY plug a
  pre-append hook here — the natural policy locus — but the hook sees
  the envelope only; base gitseq ships none.)
- **Ordering is server-side chaining**, not client CAS. Because events
  are pure appends with no shared mutable state, concurrent submissions
  never conflict; the sequencer chains them in arrival order and signs.
  This is the whole advantage over plain `git push`: no thundering
  rebase-retry under contention, and the write authority granted to a
  submitter narrows from "update this ref" to "append one well-formed
  event" — least authority, and history rewrite is structurally
  impossible for clients.
- **Idempotency**: client-supplied key in the envelope; bounded reply
  cache keyed by it; a replay returns the original commit hash, a
  different request under a used key is refused terminally.
- **Back-pressure is explicit**: at capacity the sequencer refuses
  before chaining, cheaply, and says so.
- The sequencer publishes its **signing key and a signed head**
  (`refs/seq/<log>` value, countersigned, timestamped) on demand —
  the Certificate Transparency "signed tree head" move — so a reader
  can detect a forked/rewound log by comparing heads out of band.

That is the entire sequencer. No queries, no projections, no schemas.

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
- The nexus is a read-side cache by construction: it can never disagree
  with the repo, only lag it.

Transport is boring on purpose: SSE or WebSocket beside the git smart
HTTP endpoint. A deployment without the nexus degrades to polling
`git fetch` — everything still works, just without the bell.

## What gitseq refuses to do

- No ontology. Event types, attestation levels (asserted / extracted /
  ratified), supersession — all envelope/payload conventions above.
- No mutation, ever. Corrections are new events; a log is never
  rewritten. Redaction-by-necessity is a fork under a new sequencer
  head, visibly.
- No projections. Derived state (indexes, renders, materialized views)
  lives in `refs/proj/*` or other repos, built by any tool, disposable,
  never consulted by the sequencer.
- No queries. `git log`, `git grep`, and layers above.
- No cross-log transactions or global views. One log, one order.
  Cross-log causality is reference-only (`Rests-On:` trailers);
  anything needing an order *across* logs must put its events in one
  log. Logs are cheap; make more.
- No identity system. Actors are keys; binding keys to people or
  agents is a layer above (possibly itself a log).

## Scale and failure shape

Per-log serialization caps a single log's throughput at one chaining
writer — which is the point; parallelism comes from having many logs,
and nothing in gitseq enumerates logs globally. Sequencer failover is
ref CAS at the storage layer (the new sequencer must prove it holds the
signing authority and the current head; a stale twin's push loses the
CAS). The repo itself can live on any git hosting; the sequencer just
needs exclusive push to `refs/seq/*` — enforceable with existing
per-ref permissions on every major forge, which means a v0 sequencer
can be **a bot account with a protected ref namespace**, and v0 nexus
a webhook-to-SSE relay. The whole thing is deployable today on
infrastructure everyone already runs.

## Prior art (nods, and deltas)

- **Certificate Transparency (RFC 6962)** — the closest spirit: a dumb
  append-only log, shape-only admission, signed heads, third-party
  verifiability. gitseq is CT generalized to arbitrary payloads on git
  plumbing, plus liveness.
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
  ordering countersignature, idempotency, and the bell.

## Open questions

1. **Payload ceiling and large artifacts** — attachments by hash
   reference (`Rests-On:`-style trailer to an object store or git-lfs)
   vs. inline blobs; where the ceiling sits.
2. **Compaction** — logs are forever by default; does an archival
   convention (seal log at N events, genesis of the successor carries
   the predecessor's head hash) belong in the base convention or above?
3. **Private logs** — encrypted payload blobs with envelope in the
   clear sequence fine (sequencer is shape-only by design); key
   distribution is a layer above; is that enough?
4. **Timestamp trust** — committer time is sequencer-asserted; is an
   RFC 3161 / roughtime countersignature worth a base-convention seat,
   or a layer above?
5. **The hook seam** — admitting an envelope-only pre-append hook
   reintroduces policy at the sequencer (the original motivation from
   the parent discussion) without breaking semantic-freedom; decide
   whether v0 ships the seam or stays hookless.
6. **Multi-parent limits** — git commits with very wide parent lists
   (dense causality) are legal but unusual; check tooling behavior at
   width, or cap causal parents per event and overflow into trailers.
