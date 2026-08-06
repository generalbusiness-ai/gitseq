---
date: 2026-08-05, revised 2026-08-06 (seventh wave)
status: draft/discussion, moving forward — kernel spiked; bootstrap
  plan ratified and review-repaired (BOOTSTRAP.md). This repo's own first-parent history
  is the first (hand-run) log; refs/seq/design carries it.
  Restructured after adversarial review (kernel / collaboration
  profile / application profiles); the six-case adversarial spike
  under spike/ passes against real git (stable evidence projection in
  spike/SPIKE-RESULTS.md).
origin: spinoff from the woo "engine over log" discussion (Beyond Zero →
  orchestration locus → event-log engine). This note is deliberately
  self-contained and woo-free.
---

# gitseq — a sequencing kernel on git, with profiles

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

The design is layered so that each layer can *finish*:

- The **kernel** is capability (2) on git, and nothing else: CT-style
  sequenced admission over stock git storage. It knows nothing of
  coordinates, presence, or conversations. A kernel-only deployment
  is a useful transparency log.
- The **collaboration profile** is capability (3): coordinates,
  rendezvous, capabilities, presence, ephemeral conversation, and the
  security-domain rule. It has its own service (the nexus) and its
  own trust point, named as such.
- **Application profiles** carry meaning: identity, folds,
  projections, vocabularies. Never machinery.

Minimality here means independent responsibilities and complete
contracts — not a small count of nouns. The kernel never interprets
payloads; all meaning lives above, as event content and signature
conventions. Growth pressure lands in a profile, or it doesn't land.

## The kernel

### Identity and trust anchor

**A log's identity is its genesis commit hash.** The ref that carries
it — `refs/seq/<log>`, advancing by fast-forward only, one commit per
event — is a locator, not an identity: refs get renamed, repos get
mirrored, forks share history. URLs likewise. Wherever this note names
a log, it names the genesis hash; refs and URLs are hints.

**Genesis is the trust anchor.** The genesis commit (no parents)
declares in its envelope the sequencer's public key and the
deployment constants (payload size ceiling). The log carries its own
root of trust — identity-equals-genesis-hash covers the key — so
"sequencer-signed, per whom?" has an in-band answer, and no
out-of-band PKI sneaks back in.

**Key rotation is the only event type the kernel knows.** A rotation
event is a special envelope signed by the current key, naming its
successor; verification walks the spine tracking the current key.
Nothing else earns a reserved type.

The rule generalizing all of this: **every independent ordering
domain is a log** — a place where conflicts must be decided gets one
durable order. Not everything addressable is a log; immutable
artifacts, coordinates, and snapshots are content or convention, and
need no sequencer.

### Event = commit

- **Parent** = the previous event in the log, and nothing else. Every
  commit has exactly one parent (genesis has none); the chain *is*
  the sequence, `git log` renders it cleanly, and there are no merge
  commits, ever.
- **All causality is trailers.** `Rests-On:` expresses "this event
  rests on that one" — intra-log and cross-log alike, one mechanism,
  no width cap, no second representation. (The earlier design spent
  git's extra parents on intra-log causality; review retired it —
  two representations of one concept, merge-rendering noise, and a
  width policy, for reachability that folds never used.) The causal
  references live **inside the signed intent** (authoritative) and
  are repeated as commit-message trailers — a *derived projection
  for stock-git tooling* (`git log --grep`), legal under the design's
  own derived-copy rules because the verifier enforces equality
  between the two. Trust the intent, grep the trailer.
- **Author** = the submitting actor. What the actor asserted, carried
  as the signed submission intent (below).
- **Committer** = the sequencer, and the commit is **signed by the
  sequencer** (`gpgsig` — git's one signature slot, spent on the
  ordering authority). What the authority ordered, and when.
- **Commit message** = the event envelope: the submission intent
  verbatim, the actor's signature over it, and sequencer-added
  trailers. The kernel validates shape and size only.
- **Tree** = the payload: one blob at `event`, plus optional blobs
  under `attachments/`. Content-addressed sharing comes free from
  git. Above the genesis-declared ceiling, an attachment is a
  content-addressed hash reference with an optional locator hint —
  the hash binds, storage is a hint.

### The signed submission intent

The actor does not sign the payload; the actor signs a **canonical
submission intent** that binds everything the event claims:

```
intent {
  intent_version
  target:        genesis hash of the log this event is for
  schema:        application schema id + envelope version
  payload:       tree hash (event blob + attachment manifest)
  rests_on:      ordered causal references (genesis#event ...)
  idempotency:   key + namespace
  capability:    claim material presented, if any (by hash)
}
```

Signature is over a canonical encoding with a domain-separation tag;
the kernel spec fixes the encoding, hash and signature algorithm
identifiers, and duplicate-field rejection. These are kernel
material, not application semantics.

Why this shape is load-bearing: with a payload-only signature, a
sequencer (or any replayer) could bind a validly-signed payload to a
*different log*, different causal claims, or a different idempotency
key, and every "actor signature valid" check would still pass. The
intent pins the binding; the verifier checks intent ↔ commit
consistency (the chain's genesis matches `target`, the tree hash
matches `payload`, the trailers match `rests_on`). The sequencer can
still refuse, delay, or reorder relative to other submissions — that
is its job — but it cannot alter what an event *claims* without
breaking the actor's signature.

### The sequencer (the kernel's service)

One sequencer per log; a sequencer process may host many logs. It is
the only writer of `refs/seq/*`. Its whole contract:

```
create(genesis)                          → new log
submit(intent, signature, payload-tree)  → { commit-hash, head }
                                         | replay of prior reply (same
                                           idempotency key)
                                         | refused(shape | size | unsigned
                                           | unknown-log | back-pressure)
checkpoint(log)                          → (head, depth, signature)
watch(log, from)                         → head notices
```

- **Admission is shape-only**: well-formed intent, valid actor
  signature, size ceiling, `rests_on` references resolvable where
  in-log. The sequencer never reads the payload blob. A deployment
  MAY plug a **pre-append hook** — envelope and capability material
  visible, payload never — and **v0 ships the seam with zero hooks**:
  a hookless sequencer forces every serious deployment to fork it,
  which is how semantic-free substrates die.
- **Ordering is server-side chaining**, not client CAS. Pure appends
  never conflict; the sequencer chains arrivals and signs. No
  thundering rebase-retry, and a submitter's authority narrows from
  "update this ref" to "append one well-formed event."
- **The log is the dedup index**: idempotency keys live in the
  envelope, hence in the log; the authoritative used-key record is a
  projection of the log, rebuildable by any successor. The in-memory
  reply cache is an optimization; eviction can never cause a
  duplicate append. **The dedup identity is actor-scoped** —
  (target, actor key, namespace, key) — so no actor can burn or
  squat another actor's idempotency keys; a replay must present the
  same signed intent, and the same key under a different intent is
  refused terminally.
- **Back-pressure is explicit**: at capacity, refuse before chaining,
  cheaply, and say so.
- **Logs are cheap is a performance requirement**: applications mint
  logs promiscuously (a room, a mug, a case); `create()` is one cheap
  operation, per-log overhead near zero. The service number that
  matters is p99 submit→notice latency — one chain plus one fanout;
  interactive use needs ~100–200 ms.
- `watch` is the kernel's minimal bell: per-log head notices, no
  coordinates, no presence. The collaboration profile builds on it.

The sequencer holds no durable state of its own: head, dedup index,
and current key are all reconstructible from the log. It is
**stateless but for its private key** — the strongest thing one can
say about its failover. Failover separates two questions: *storage*
(win the ref CAS at the current head; a stale twin loses) and
*authority* (hold the rotated-to key, or append a rotation signed by
it). Winning the CAS without the key yields commits every reader
refuses; the key without the CAS yields a visible fork. Both are
checkable from the log alone.

### Verification

Three grades, and a reader should know which one they hold:

- **Full audit**: walk the chain head→genesis, tracking the sequencer
  key through rotations; check each commit sequencer-signed,
  single-event-shaped, fast-forward-chained; check the submission
  intents you care about. O(history), offline, from any clone.
- **Incremental audit**: a **checkpoint** is (event hash, depth,
  sequencer signature, optionally witness cosignatures). Yesterday's
  verified head is a checkpoint; verify only the delta. A shallow
  clone from a trusted checkpoint is sound for anyone who trusts its
  signatures — most practical readers.
- **Witnessed head**: sequencer signatures alone cannot rule out
  equivocation — different heads shown to different audiences. That
  takes **witnesses**: third parties that fetch, verify the delta,
  check consistency against the last head they saw, and countersign
  (the transparency.dev cosigning pattern). v0 ships no witness
  software, but the cosignature format is kernel vocabulary, because
  a reader's guarantee differs materially with and without one.

Honesty about tooling: stock git *stores, transfers, and traverses*
all of this; **a small gitseq verifier is a kernel deliverable** —
envelope and intent-binding checks, the rotation walk, checkpoints,
witness cosignatures. It is small, but it exists, and pretending
`git log` alone verifies would overclaim.

**Continuation, not compaction.** Logs are forever by default. To
seal one: declare a final checkpoint and start a successor whose
genesis names the predecessor's genesis and sealed head. By the
kernel's own identity rule the successor is a *different log*; the
pair is a verifiable **continuation** (a stream family), and
`Rests-On:` references into sealed history keep resolving. It is not
"the same log," and the design stops claiming so.

Anyone holding a clone can audit offline, or fork a log and continue
under a different sequencer. Exit costs nothing. Sequence number is
chain depth — derivable, never stored authoritatively.

## The collaboration profile

Everything live: finding each other, talking, and the boundaries of
who sees what. Its service is the **nexus**, and the profile states
plainly what the kernel never needed: **the nexus is this profile's
trust point.** It issues capabilities; where its issuer key is
anchored, how it rotates, which sequencers accept it, and whose
clock governs expiry are profile contract items, not afterthoughts.
(Anchoring convention: the profile deployment has its own config log —
a kernel log — whose genesis anchors the nexus issuer key and rotates
it in-band, same machinery as sequencer keys.)

### Coordinates, rendezvous, capabilities

A **coordinate** is an opaque string; by convention a genesis hash or
`<genesis>#<event>`. The nexus indexes two live facts per coordinate:
who is available there, and which ephemeral conversations are
anchored there.

The profile has one credential: a **capability** — a short-lived
token signed by the nexus, binding an actor key, a coordinate, an
expiry, and a set of independent **claims**:

- `discover` — may query presence and anchors at the coordinate
- `read` — may subscribe to what the coordinate carries
- `append` — may submit to logs the coordinate governs (presented to
  the sequencer, verified offline against the nexus issuer key — the
  services couple through a token, never a runtime call)
- `advertise` — appears in the visible roster

The claims are orthogonal because the states are real: visible but
read-only; authorized but offline; silently observing; present on
three devices. One format, independent claims — not three lease
types, and not one blob doing three jobs. Retention is deliberately
*not* a claim: see ephemera below.

**Announcements**: "I am available at these coordinates" is a signed
assertion under the same envelope discipline as an event; the nexus
answers with capabilities per coordinate, partial grants normal
(admitted to the room, refused at a doorstep). **Subscription and
presence stay different facts**: subscribing is transport (lurker,
monitor, mirror); announcing is availability; a durable roster, where
a practice wants one, is a fold of entry/exit events some layer above
chose to log.

**Coordinate policy is the profile's hook seam** — the doorstep. Who
may announce, who may discover: a pluggable envelope-only predicate,
symmetric in shape with the kernel's pre-append hook, zero hooks
shipped. Blocking someone is refusing their capability at your
coordinate.

### Snapshots, cursors, frontiers

Learned from woo's presence races, stated as contract:

- Every discovery query (`presence`, `anchored`) returns a
  **snapshot plus a cursor**; `subscribe` resumes from the cursor.
  Query-then-subscribe without the cursor can permanently miss a
  transition that lands between the two calls; the contract makes
  that impossible rather than unlikely.
- A multiplexed subscription over many logs cannot use one scalar
  position. Its cursor is a **frontier**: a set of (genesis, head)
  pairs, opaque to carry, exact to resume.
- The frontier is also the profile's **multi-log observation model**:
  a client view following a room plus the object logs of what's
  present names its state as a frontier; retrieval is
  causal-closure-best-effort with *explicitly reported* unavailable
  logs (absence is a stated fact, never silent). Cross-log
  transitions choose between an **acyclic saga** — offer → accept →
  settle, each later step resting on the earlier one's hash;
  content addressing makes a mutual-reference cycle impossible, so
  "each rests on the other" is not an expressible shape — and
  putting the whole transition in one authority log. An application
  choice the profile names but does not make.

### Ephemeral conversation (amnesiac by design)

Ephemerality is a retention property, not a structure property — a
conversation is enveloped, signed, causal, and *about* things — but
the infrastructure keeps **no durable copy at all**:

- An ephemeral conversation lives in nexus memory and in
  participants' clients. It never touches the repository; the kernel
  does not know it exists. Its identity is still a genesis hash (of
  its genesis envelope); its anchors still address it; `Rests-On:`
  still cites it.
- **Durability lives only at the edges.** Every participant keeps a
  copy exactly as long as they care to. "Forgotten when everyone went
  away" is literal: the service is amnesiac, memory belongs to
  participants. A nexus crash drops live conversations like a
  dropped call — that is the contract, not a failure to engineer
  around.
- **References degrade gracefully.** A durable event citing an
  ephemeral hash holds a reference that degrades from *resolvable*
  (conversation live) → *attestable* (a participant kept a copy and
  can prove what it was) → a bare *commitment* (the hash pins what
  would have to be produced). Content addressing makes citing a
  forgotten conversation honest rather than broken.
- **Ephemeral ≠ private.** While it lives, every participant can copy
  it; forgetting is an infrastructure default, not an enforcement.
  Deniability is cryptography in a layer above; the base refuses to
  imply it.
- Promotion needs no machinery: a participant who kept the content
  submits what mattered into a durable log, citing the ephemeral
  hashes it rests on. Durable logs hold acts; chatter swirls around
  them and is ratified inward or forgotten.
- Open (spike-level, not architecture): whether the nexus assigns a
  per-conversation order (a router with a counter, cheaply signed) or
  ephemera are causal-only with client-rendered arrival order.

### Security domains: repository-per-domain

Read authorization adopts the only boundary git actually has:

- **The unit of read authorization is the repository.** A security
  domain is a repo; membership in the domain is fetch access,
  enforced by ordinary forge/hosting ACLs. Per-ref read isolation
  inside a shared object database is unsound (objects fetch by hash
  regardless of ref advertisement; delta bases leak) and the design
  does not pretend otherwise.
- Cross-domain references are `Rests-On:` hashes: they leak an
  existence-commitment and nothing else — already the documented
  posture for degraded references. Multi-domain folds are
  multi-remote fetches, which git does natively.
- **Repos are cheap** joins "logs are cheap" as a requirement: a
  security domain must cost a directory, not a provisioning ticket.
- The nexus is **domain-scoped**: its rendezvous index must not join
  coordinates across domains it serves. And within a domain, the
  `advertise`/`discover` bits are **courtesy, not confinement** —
  real confidentiality is domain membership; the visibility flag is
  politeness. The profile states its metadata surface: the operator
  sees the index; anchoring a whisper at [A, B] tells anyone who may
  query those coordinates that the conversation exists.
- Payload encryption within a domain is a future profile
  (confidentiality from the operator); it is no longer load-bearing
  for the collaboration range.

## Application profiles (conventions, never machinery)

Recorded so applications converge; the base runs none of it:

- **Admission ≠ validity; validity is a fold.** The kernel admits
  shape; whether an admitted event is *effective* ("take the mug"
  when the mug is gone; "ratify" from a non-chair) is decided by a
  deterministic, total, versioned fold every replayer runs to the
  same answer. **Total means every event receives a decision** —
  including system-level ambiguity, which is a typed outcome
  (`disputed`), never an error: a fold that throws gives late joiners
  no projection at all, while `disputed` is a rendering. Ineffective
  events remain in history as what they are: attempts,
  audit-relevant. Hooks are economics — rate, size, doorsteps —
  never correctness; a stateful hook consulting a fold is how
  semantic-freedom dies. Fold definitions are themselves logged in
  the practice's own log, so the rules are governed by the moves they
  govern.
- **An entity with sequenced state is a log.** Custody of a mug that
  moves between rooms double-spends if rooms own it; give the mug a
  log and custody has one total order, rooms merely reference it.
  *Spike-backed*: the custody case ran the saga alternative (events
  in the parties' logs) and found that two completed sagas are
  unorderable — ambiguity survives and must be `disputed` by policy.
  The entity-own-log pattern excludes that ambiguity by construction;
  the saga is the exception you choose knowingly, paying for it with
  a dispute state.
- **Identity logs.** Actors worth addressing durably are logs: genesis
  is the stable identity, events bind and rotate keys, the fold
  answers "which keys speak for this actor now." Bare keys may act; a
  genesis addresses. (The kernel's rotation machinery, one level up —
  as a convention.)
- **Projection provenance.** A projection commit (in `refs/proj/*` or
  anywhere) stamps `Projected-From:` naming the frontier it rendered —
  staleness becomes computable by comparing frontiers. Profile
  vocabulary, not kernel: the substrate never consults projections.
- Approval/workflow vocabularies, artifact conventions, promotion
  rituals, executors and connectors: all here or higher. Gitseq
  coordinates executors; it never becomes one.

## What gitseq refuses to do

- No ontology. Event types, attestation ladders, supersession — all
  above. (Rotation is the kernel's sole reserved type, and it is
  about the log's own key.)
- No mutation, ever. Corrections are new events. Redaction-by-necessity
  is a fork, and a fork is precise here: same genesis, divergent
  sequencer signatures after the fork point — detectable by any
  reader holding both heads, attributable to the signing keys.
- No enforced forgetting. Ephemeral amnesia drops the service copy;
  it proves nothing about participants' copies, and the base never
  implies deniability.
- No unstructured channel. Everything the nexus carries is enveloped
  and signed; ephemeral conversations are logs in every respect
  except durability. There is no raw pub/sub to become a second,
  unauditable path.
- No fold execution. Validity is computed by readers; the base never
  runs an application fold, consults one, or stores one's output.
- No queries. `git log`, `git grep`, and layers above.
- No cross-log transactions or global views. One log, one order;
  cross-log causality is reference-only. Logs are cheap; make more.
- No identity system in the kernel. Actors are keys; durable actor
  identity is the identity-log convention. The sequencer's key is the
  one exception, anchored in genesis; the nexus issuer key is the
  collaboration profile's exception, anchored in its config log.

## Deployment shape

The repo lives on any git hosting; the sequencer needs exclusive push
to `refs/seq/*` — per-ref permissions on every major forge — so a v0
sequencer is **a bot account with a protected ref namespace**, and a
v0 nexus a webhook-to-SSE relay grown a capability table (in-memory,
expiring; its loss drops conversations and un-issued capabilities,
which is the stated contract). A security domain is a repo; minting
one is `git init` plus an ACL. One operational note: CAS losers and
refused-after-objects submissions leave unreferenced objects in the
object database — ordinary `git gc` collects them; a deployment just
has to actually run it. The whole thing deploys today on
infrastructure everyone already runs.

## Prior art (nods, and deltas)

- **Certificate Transparency (RFC 6962)** — the closest spirit: dumb
  append-only log, shape-only admission, signed heads, third-party
  verifiability; the witness convention is transparency.dev cosigning,
  adopted not reinvented. "Validity is a fold" is CT's other lesson —
  the log records, readers judge.
- **Secure Scuttlebutt / Nostr** — signed append-only events, but
  per-author feeds with gossip; no multi-writer total order per
  topic, the one thing coordination needs most.
- **Signal / XMPP dyad threads** — conversation identity as the
  sorted identity pair: deterministic rendezvous, paid for with one
  eternal thread per pair and the group-membership identity problem.
  Identity/address/discovery is the correction: genesis identifies,
  anchors address, the index discovers.
- **Radicle** — p2p git with its own gossip; heavier, and
  collaboration objects carry semantics.
- **Kafka et al.** — the right ordering/liveness shape, opaque
  storage: no content addressing, no offline verification, no
  fork/exit.
- **Plain git server with protected refs** — the degenerate kernel;
  gitseq's deltas are server-side chaining under contention, the
  signed submission intent, ordering countersignature, an in-band
  trust anchor, idempotency, and the bell.

## Next act: the adversarial spike

Before another design wave, one executable spike against the kernel
spec, then the profile. Six cases, each pass/fail:

1. **Concurrent retry and failover** — two submitters racing, a
   sequencer killed mid-chain, a successor rebuilding head + dedup
   index from the log alone.
2. **Rebinding attacks** — replay a signed intent into another log;
   alter causal trailers; swap idempotency keys; every variant must
   break the actor signature or the intent↔commit binding check.
3. **Nexus crash with live ephemera** — conversations drop, nothing
   durable leaks, participants' copies still attest; verify the
   contract *is* the behavior.
4. **Unauthorized fetch across a domain** — a forge-ACL test plus a
   fetch-by-hash probe, confirming the domain boundary holds where
   claimed and leaks only existence-commitments where stated.
5. **Snapshot/watch race** — a presence transition landing between
   query and subscribe must be impossible to miss under the cursor
   contract.
6. **Conflicting multi-log custody transition** — the mug dropped in
   one room and taken in another, saga-style; folds on both sides
   converge, the attempt that lost stays visible as an attempt.

Cases 1–2 exercise the kernel; 3–6 the collaboration profile. What
these six teach about the true minimal boundary outranks any further
hypothetical application architecture.

## Open questions

1. **Ephemeral ordering** — nexus counter vs. causal-only with
   client-rendered arrival. The spike showed the counter is cheap to
   implement; it deliberately did *not* establish that total
   ephemeral order belongs in the minimal profile, so the question
   stays open on merit, not feasibility.
2. **Timestamp trust** — committer time is sequencer-asserted; a
   witness cosignature carries an independent observation time — is
   that enough, or does RFC 3161 / roughtime earn a kernel seat?
3. **Checkpoint cadence and witness-set vocabulary** — deployment
   policy; does the kernel need vocabulary for *stating* the policy,
   or does it live wholly above?
4. **Knock visibility** — is a refused doorstep capability
   distinguishable from absence? A values choice; a per-coordinate
   policy bit in the profile, not a convention.
5. **Payload-encryption profile** — confidentiality from the domain
   operator; future, explicitly not load-bearing now.

Resolution ledger. First pass: log identity (genesis), trust anchor
and rotation, fork detection (witnesses), incremental verification
(checkpoints), idempotency state (the log is the index), the hook
seam (ship it, hookless). Second wave: ephemerality as retention
class; presence split from subscription; leases as admission;
identity/address/discovery; attachments. Third wave (adversarial
review): the **signed submission intent** replaces payload-only
signatures (kernel-critical — payload signatures allowed rebinding);
**kernel / collaboration / application layering** with each layer's
trust points named (the nexus *is* the profile's trust point; the
"never a trust point" claim is retired); **trailers-only causality**
(single-parent chains, no merge commits, width caps gone);
**amnesiac ephemera** (no `refs/eph`, no service durability, edges
remember — retiring the second-wave repo-backed claim, the retain
claim, and the grace-window question); **capability with orthogonal
claims** (discover/read/append/advertise) replaces the
three-jobs-in-one lease; **repository-per-security-domain** as the
read-authorization rule, visibility demoted to courtesy;
**snapshot+cursor and frontier contracts** for races woo paid for;
`Projected-From:` demoted to the projection profile; continuation
replaces "compaction as same identity"; "everything addressable is a
log" corrected to "every independent ordering domain is a log"; the
numerical-symmetry rhetoric dropped — minimality is independent
responsibilities and complete contracts; and the small gitseq
verifier admitted as a kernel deliverable. Fourth wave (the
executable spike, `spike/`): all six adversarial cases pass against
real git plumbing, sha1 and sha256, including actual process-death
failover and a real smart-HTTP domain-boundary probe with a positive
control. Decisions the spike made and this note now ratifies:
**canonical intent encoding** is core-deterministic fixed-array CBOR
with a domain-separation tag and Ed25519 actor signatures, decode ∘
encode proven bijective by fuzzing (7.3M executions); sequencer
commits carry git SSH signatures; **dedup identity is actor-scoped**;
**Rests-On trailers are a verifier-checked derived projection** of
the signed intent; **fold totality** means every event gets a
decision, with `disputed` as a typed outcome rather than an error
(the original custody spike errored on ambiguity — the finding that
changed the contract; Seq 5 aligned the executable with `disputed`);
and the entity-own-log custody pattern is spike-backed — the saga
alternative demonstrably leaves settlement ambiguity to policy.
Stable evidence: `spike/SPIKE-RESULTS.md`; per-run JSON, timings, and
detailed output regenerate under ignored `spike/.spike/`. The
retained-ephemera result (frames verify only against an externally
anchored nexus key) confirms the profile's config-log anchor
decision. Sixth wave: the **bootstrap plan** ([BOOTSTRAP.md]
(BOOTSTRAP.md)) — the project moves onto its own substrate (the
workroom), which is also the demonstration: multiple agents with
live discussion, decisions, and real work (stage 1), a staleness-
flaring status projection (stage 2). Ratified there: the workroom
log taxonomy; the continuation-migration of this hand-run log as the
first production act; the minimal-but-real MCP identity chain
(custodial Ed25519 per actor, OS-transport session binding, roster
as a fold, static allowlist → nexus capability as an audited
transition, roles enforced by the fold so ineffective attempts stay
visible); and the agent usage contract ([SKILL.md](SKILL.md)).
Seventh wave (bootstrap review): the plan reshaped fold-first — the
application profile (schemas, effectiveness rules, work-item
lifecycle, deterministic projector with golden fixtures) is stage 1,
before any service code; a **continuation gate** (the genesis
descriptor has no continuation fields yet — build and audit a
candidate successor on a scratch copy before sealing the real
hand-run log); the custody claim corrected to its real strength
(accidental-handling prevention, not isolation from a tool-capable
same-account agent) and the stdio topology corrected (one server per
session, configured for an actor); a **composite cursor** contract
(durable frontier + resettable live position, subscribe-before-
snapshot, dedupe by depth); the provenance chain closed (`gs attach`
refspecs, promotion **embeds** signed frames, and the artifact
bridge: decision event → source commit `Rests-On:` trailer →
artifact-reference act); one golden work session as acceptance story;
the MCP layer targets the stateless MCP spec only; and this note's
saga sentence repaired — mutual reference is a hash cycle, which
content addressing makes inexpressible; the saga is acyclic
offer → accept → settle, as the spike always had it.
