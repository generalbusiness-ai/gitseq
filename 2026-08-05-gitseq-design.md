---
date: 2026-08-05, revised 2026-08-06
status: draft/discussion, moving forward — nothing built. This repo's
  own first-parent history is the first (hand-run) log;
  refs/seq/design carries it.
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
3. **Liveness** — a bell and a meeting-place: subscribers learn of
   appends promptly, and actors declare availability at coordinates
   and find each other there.

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
git client can clone, verify, and walk away with. The base is meant to
feel finished: two services, two ref namespaces, two load-bearing
trailers, two hook seams, one lease. Growth pressure lands in layers
above, or it doesn't land.

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
  size ceiling and never reads the bytes. Above the ceiling, an
  attachment is a content-addressed hash reference with an optional
  locator hint — the hash is in the log, storage is a hint, same
  split as `Rests-On-Hint:` below.

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

### One structure, two retention classes

`refs/seq/<log>` is durable: kept forever, checkpointed, witnessable.
`refs/eph/<log>` is **ephemeral**: same genesis identity, same
chaining, same sequencer signature, same causal parents and trailers —
retained only while any participant holds a lease (see the nexus),
then garbage-collected after a grace window. Ephemerality is a
retention property, not a structure property: a conversation is
ordered, causal, and about things; it is simply forgotten when
everyone has gone. Ephemeral logs take no checkpoints and no
witnesses — machinery for permanence has nothing to attach to — and
they live in the same repository, so a service crash forgets nothing
early; only lapsed leases do.

Two rules keep the boundary honest:

- **References across the boundary degrade gracefully.** Ephemeral
  events `Rests-On:` durable ones freely. A durable event citing an
  ephemeral hash holds a reference that degrades, once the
  conversation is forgotten, from *resolvable* → *attestable* (any
  participant who kept a clone can prove what it was) → a bare
  *commitment* (the hash still pins exactly what would have to be
  produced). Content addressing is what makes citing a forgotten
  conversation honest rather than broken.
- **Ephemeral ≠ private.** While it lives, the log is
  sequencer-signed and cloneable by every participant; "forgotten"
  means the infrastructure stops retaining, not that forgetting is
  enforced. Anyone may remember, as with any conversation.
  Deniability is cryptography in a layer above; the base refuses to
  imply it.

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

## Identity, address, discovery

The three things a conversation identifier usually conflates, held
separate:

- **Identity** is the genesis hash. Always, for everything: a room, a
  working group, a mug with sequenced custody, a whisper, an actor.
  *Everything addressable is a log; every log's identity is its
  genesis.*
- **Address** is an **anchor set**: the coordinates a log is at or
  about. An ephemeral genesis declares its anchors — `[room-genesis,
  actor-A, actor-B, mug-genesis]` — and the nexus indexes them.
  Address is contextual and non-unique; identity never depends on it.
  Two parallel whispers between the same pair are simply two logs
  sharing anchors, and group membership change never re-keys a
  conversation — the failure modes of dyad-identifier messaging
  (one eternal thread per pair; identity breaks when the member set
  changes) don't arise, because identity was never the member set.
- **Discovery** is the rendezvous index, live-only: who is present at
  a coordinate, which ephemeral logs are anchored there. It resolves
  only while leases hold it up; afterward, citations persist as
  hashes.

A **coordinate** is an opaque string; by convention a genesis hash or
`<genesis>#<event>`, but nothing stops other schemes. Actors worth
addressing durably are logs too: an **identity log** whose genesis is
the actor's stable identity and whose events bind and rotate keys —
the sequencer's own in-band rotation machinery, one level up. Bare
keys may act; a genesis addresses.

## The sequencer (service 1)

One sequencer per log; a sequencer process may host many logs. It is
the only writer of `refs/seq/*` and `refs/eph/*`. Its whole contract:

```
create(genesis)                        → new log (durable or ephemeral)
submit(log, envelope, payload-tree, causal-parents,
       idempotency-key [, lease])
  → { commit-hash, head }   | replay of prior reply for same key
  | refused(shape | size | unsigned | unknown-log | no-lease | back-pressure)
checkpoint(log)                        → (head, depth, signature)
```

- **Admission is shape-only.** Well-formed envelope, size ceiling,
  actor signature present and valid, causal parents resolvable in-log.
  The sequencer never reads the payload blob. A **lease** (see the
  nexus) may accompany a submit and is envelope-visible — actor key,
  coordinates, expiry — verified offline against the nexus's
  signature, never by calling the nexus. For an **ephemeral** log a
  live lease at the log's own coordinate is required by definition:
  the participant set *is* the lease set. For a **durable** log, lease
  requirements are deployment policy, never base — offline-composed
  and batch appends are the git ethos and stay first-class.
- A deployment MAY plug a **pre-append hook** — the policy locus. The
  hook sees the envelope and the lease (schema id, actor key,
  trailers, coordinates), never the payload, so policy like "a
  ratification event is refused unless the actor key is a recognized
  ratifier" is expressible without breaching semantic-freedom.
  **v0 ships the seam and zero hooks** — a hookless sequencer forces
  every serious deployment to fork it, which is how semantic-free
  substrates die.
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
  never cause a duplicate append. A replay returns the original
  commit hash; a different request under a used key is refused
  terminally.
- **Back-pressure is explicit**: at capacity the sequencer refuses
  before chaining, cheaply, and says so.
- **Logs are cheap is a performance requirement, not a slogan.**
  Applications mint logs promiscuously — a room, a mug, a meeting, a
  whisper — so `create()` is one cheap operation and per-log overhead
  is near zero. The other number that matters is **p99 submit→bell
  latency** (one chain plus one fanout): interactive presence needs
  ~100–200 ms, and per-log serialization is nowhere near the
  constraint at that scale.
- The sequencer serves, on demand, a **checkpoint** of each durable
  log it hosts — the Certificate Transparency signed-tree-head move,
  feeding the verification grades above and giving witnesses
  something to countersign. Its key needs no publication channel: it
  is in genesis, rotated in-band.

That is the entire sequencer. No queries, no projections, no schemas —
and no durable state of its own: head, dedup index, and current key
are all reconstructible from the log. The sequencer is **stateless
but for its private key**, which is the strongest thing one can say
about its failover.

## The nexus (service 2)

The bell, the roster, and the meeting-place. Its whole contract:

```
announce(signed {actor, coordinates[], visibility, annotation, ttl})
                                → { leases[], refusals[] }
renew(lease) / release(lease)
presence(coordinate)            → visible announcements there
anchored(coordinate)            → live ephemeral logs anchored there
subscribe({logs | coordinates}, from)
                                → stream: commits (or head notices),
                                  lease transitions, new-anchor notices
```

- **A presence announcement is a signed assertion** under the same
  envelope discipline as an event: "I am available for interaction at
  these coordinates." The nexus answers with a **lease** per
  coordinate — partial grants are normal (admitted to the room,
  refused at a doorstep). The lease is a short-lived token signed by
  the nexus binding actor key, coordinate, expiry, and visibility.
  The sequencer verifies it offline; the two services couple through
  a token, never a runtime call.
- **Subscription and presence are different facts.** Subscribing is
  transport (a lurker, a monitor agent, a mirror); announcing is
  availability; a durable roster, where a practice wants one, is a
  fold of entry/exit events that some layer above chose to log. One
  visibility bit inside the announcement covers "present for writing,
  not advertised."
- **The nexus is a roster and an index, never a message channel.**
  Presence is leased, ephemeral, and never written to any log by the
  base. The annotation blob is small mutable status ("typing", a
  cursor) — anything conversational is a log (usually an ephemeral
  one), sequenced like everything else. No transient pub/sub exists
  to become a second, unauditable channel.
- **Retention of `refs/eph/*` is lease-driven**: when the last lease
  on a log's coordinate lapses and the grace window passes, the ref
  is dropped and its objects become prunable. Ephemera are chained in
  the repo like everything else, so nexus restart forgets nothing
  early.
- Fanout is **at-least-once, resumable by sequence**: a subscriber
  reconnecting with `from=<hash|depth>` replays the gap from the repo
  itself — the log *is* the durable queue, so the nexus holds no
  state that outlives its leases and can be killed and restarted
  freely.
- The nexus also fans out **checkpoints and witness cosignatures** —
  cheap, cacheable, and like everything else it serves, independently
  verifiable: it is a distribution point, never a trust point.
- **Coordinate policy is the nexus's hook seam** — the doorstep. Who
  may announce at a coordinate, and who may see what is anchored
  there, is a pluggable envelope-only predicate, symmetric with the
  sequencer's pre-append hook: two seams, same shape, signed inputs
  only, zero hooks shipped. Blocking someone is refusing their lease
  at your coordinate.
- **What the index leaks, it leaks knowingly.** Anchoring a whisper
  at [A, B] tells anyone who may query those coordinates that the
  conversation exists, and the operator sees the index. Visibility
  bounds who can query; private coordinates are a layer above. The
  base states its metadata surface rather than pretending it away.

Transport is boring on purpose: SSE or WebSocket beside the git smart
HTTP endpoint. A deployment without the nexus degrades to polling
`git fetch` for durable logs — everything still works, just without
the bell; ephemeral logs and rendezvous need the nexus by nature.

## Application patterns (conventions above, not machinery)

Testing the base against two hypothetical-but-complete architectures —
a live interaction room, and a whole standards organization
(discussion, meetings, drafts, reviews, ratified artifacts) — bent
nothing in the shape, and precipitated three patterns that belong to
the layers above. Recorded here so applications converge; none of
this is machinery:

- **Admission ≠ validity; validity is a fold.** The sequencer admits
  shape; whether an admitted event is *effective* ("take the mug" when
  the mug is gone; "ratify" from a non-chair) is decided by a
  deterministic, total, versioned fold that every replayer runs to
  the same answer. Ineffective events stay in history as what they
  are: attempts, audit-relevant. Hooks are economics — rate, size,
  doorsteps — never correctness; a stateful hook consulting a fold is
  how semantic-freedom dies. Fold definitions are themselves logged
  (in the practice's own log), so the rules are governed by the moves
  they govern.
- **An entity with sequenced state is a log.** Custody of a mug that
  moves between rooms double-spends if rooms own it; give the mug a
  log and custody has one total order, while rooms merely reference
  it. Identity = genesis, here too.
- **Promotion copies content inward.** What an ephemeral conversation
  decided enters a durable log as an event carrying the payload that
  matters, citing the ephemeral hashes it rests on — references that
  degrade gracefully, by design. Durable logs hold acts; chatter
  swirls around them and is ratified inward or forgotten.

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
- No enforced forgetting. Ephemeral GC drops the infrastructure copy;
  it proves nothing about clones, and the base never implies
  deniability.
- No message channel. The nexus carries rosters, indexes, and
  fanout of sequenced commits; conversation that isn't sequenced
  isn't carried.
- No fold execution. Validity is computed by readers; the base never
  runs an application fold, consults one, or stores one's output.
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
- No identity system. Actors are keys; binding keys to durable actor
  identities is the identity-log convention above — a convention,
  not a registry. The sequencer's own key is the one exception, and
  it is anchored in genesis.

## Scale and failure shape

Per-log serialization caps a single log's throughput at one chaining
writer — which is the point; parallelism comes from having many logs,
and nothing in gitseq enumerates logs globally. Applications fold
several logs at once (a room client follows the room plus the object
logs of whatever is present), so the nexus multiplexes many logs and
coordinates over one connection, and `create()` is cheap enough to
mint a log per whisper.

**Failover separates two questions** that a ref CAS alone conflates.
*Storage*: the new sequencer must win the compare-and-swap on
`refs/seq/<log>` at the current head; a stale twin's push loses the
race. *Authority*: it must hold the currently-rotated-to key, or
append a rotation event signed by it. Winning the CAS without the key
yields commits every reader refuses; holding the key without the CAS
yields a visible fork. Both properties are checkable from the log
alone. The nexus's failure shape is simpler: its only state is its
lease set, which expires by construction; ephemeral retention is
repo-backed, so nothing is forgotten early by a crash.

**Compaction is a checkpoint made durable.** Logs are forever by
default; to seal one, declare a final checkpoint and start a
successor whose genesis carries the predecessor's genesis hash and
sealed head. A chain of sealed logs remains one verifiable identity —
readers walk across the seam the same way they walk the spine, and
`Rests-On:` references into sealed history keep resolving. (Meeting
capture uses this constantly: one log per meeting, sealed at
adjournment.)

The repo itself can live on any git hosting; the sequencer just
needs exclusive push to its ref namespaces — enforceable with existing
per-ref permissions on every major forge, which means a v0 sequencer
can be **a bot account with a protected ref namespace**, and a v0
nexus a webhook-to-SSE relay grown a lease table (in-memory,
expiring, rebuildable). The whole thing is deployable today on
infrastructure everyone already runs.

## Prior art (nods, and deltas)

- **Certificate Transparency (RFC 6962)** — the closest spirit: a dumb
  append-only log, shape-only admission, signed heads, third-party
  verifiability. gitseq is CT generalized to arbitrary payloads on git
  plumbing, plus liveness; the witness convention is the
  transparency.dev cosigning pattern, adopted rather than reinvented.
  "Validity is a fold" is CT's other lesson — the log records,
  readers judge — promoted here to the sanctioned application
  pattern.
- **Secure Scuttlebutt / Nostr** — signed append-only events, but
  per-author feeds with gossip; no multi-writer total order per topic,
  which is the one thing the engine needs most.
- **Signal / XMPP dyad threads** — conversation identity as the
  sorted identity pair: deterministic rendezvous, paid for with one
  eternal thread per pair and the group-membership identity problem.
  The identity/address/discovery split is the correction: genesis
  identifies, anchors address, the index discovers.
- **Radicle** — p2p git with its own gossip layer; heavier, and
  collaboration objects carry semantics. gitseq stays semantic-free.
- **Kafka et al.** — the right ordering/liveness shape, but opaque
  storage: no content addressing, no offline verification, no fork/exit,
  and durable retention is the operator's problem rather than a clone.
- **Plain git server with protected refs** — the degenerate v0 (see
  above); gitseq's deltas are server-side chaining under contention,
  ordering countersignature, an in-band trust anchor, idempotency,
  leases, and the bell.

## Open questions

1. **Private logs** — encrypted payload blobs with envelope in the
   clear sequence fine (sequencer is shape-only by design); key
   distribution is a layer above; is that enough? The rendezvous
   index's metadata surface sharpens this: private *coordinates* are
   a related but distinct problem.
2. **Timestamp trust** — committer time is sequencer-asserted. A
   witness cosignature already carries an independent observation
   time; is that enough, or is an RFC 3161 / roughtime
   countersignature worth a base-convention seat?
3. **Trailers-only causality** — causal parents as git parents buy
   ancestry-at-a-glance but cost merge-rendering noise and a width
   cap, and `Rests-On:` already expresses the same edge intra-log.
   The standards-organization test raises the urgency: synthesis
   events resting on a dozen claims are *normal* there, so the width
   cap would be hit constantly. Spike before v0 freezes the
   convention.
4. **Checkpoint cadence and witness sets** — deployment policy (rooms
   want frequent cheap checkpoints for fast joins; an organization
   wants witness cosignatures at ratification moments); does the base
   need vocabulary for stating the policy, or does it live wholly
   above?
5. **Grace-window semantics** — a fixed genesis constant, or
   renewable by a former participant within the window ("wait, keep
   that")?
6. **Whole-log promotion** — sealing an ephemeral log into
   `refs/seq/*` with history intact ("this conversation turned out to
   be the design review"). Attractive, but it makes retention class
   mutable and needs a rule for whose act ratifies the promotion.
7. **Anchor mutation** — genesis-frozen aboutness plus lease-based
   participation keeps membership out of identity *and* address; but
   "this conversation drifted, re-anchor it" is a real pattern that
   would need an anchor event.
8. **Follow-the-place** — should `subscribe(coordinate)` on a durable
   log's genesis also carry that log's commits, collapsing the two
   subscription kinds into one?
9. **Knock visibility** — is a refused doorstep lease distinguishable
   from absence (a social fact) or not (a privacy fact)? A values
   choice; probably a per-coordinate policy bit, not a convention.

Resolved in the first review pass: log identity (genesis hash), the
trust anchor and key rotation, fork detection (witness cosigning),
incremental verification (checkpoints, which also absorb compaction),
idempotency state (the log is the index), and the hook seam (ship it,
hookless).

Resolved in the second wave, by testing against two full application
architectures (interaction room; standards organization) and the
presence discussion: ephemerality as a retention class (`refs/eph`,
lease-scoped, repo-backed); presence as signed announcement answered
by leases, split from subscription; leases as admission credential
(definitional for ephemera, policy for durable logs); the
identity/address/discovery split (genesis / anchor set / rendezvous
index); actor identity logs; attachments (inline to the ceiling,
content-addressed hash + locator hint beyond); the two symmetric
envelope-only hook seams. The base grew by exactly one noun — the
lease — and one ref namespace; everything else landed above.
