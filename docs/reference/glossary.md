---
title: Glossary
summary: The vocabulary of a workroom, in one place.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:57e4bc379b4f3539155eb83b13c359567e436aff
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6918582b884b2f82fa7ab64242f40d12de845c39
---

# Glossary

**Act.** Anything an actor does that lands in the sequence: a statement,
a ratification, a supersession.

**Actor.** A principal with a signing key. Everything it does is signed
with that key.

**Artifact.** A statement citing implementation truth as `path@commit`.
The durable pointer from the record to the code.

**Attach.** Adding a non-forcing `refs/seq/*` fetch rule to a clone and
pulling the sequence down, so the clone can read and verify the workroom.

**Basis.** An event named in another act's `rests_on`.

**Custody.** Holding an actor's private key. The resident service holds
custody for every actor whose key is in the repository it serves.

**Decision.** The fold's verdict on one act: effective, or not, with a
reason. Decisions are history and do not move.

**Degraded.** The state the MCP adapter reports when the resident service
is unreachable: durable tools still work against the local log, and the
live cursor says so.

**Durable.** Permanent, ordered, attributed, and visible to everyone —
including acts that turn out to carry no force. The opposite of
ephemeral.

**Ephemeral.** Sequenced and signed, but forgotten when everyone leaves
and their presence leases expire. Not secret: any participant can keep a
copy.

**Effective.** A verdict, not a permission: the act satisfied the fold's
rules at the moment it was appended. An effective act may still confer
nothing now, if something it depends on has since been retired.

**Event identifier.** The canonical name of one durable act. See
[Event identifiers](event-identifiers.md).

**Evidence.** Signed frames or files embedded in a durable act, so a
stranger can verify a promotion after the conversation is forgotten.

**Flare.** What a document does when something it rests on is retired. It
means *re-check this*, not *this is wrong*.

**Fold.** The deterministic function from the event sequence to a
projection. A library that readers run, never a service whose reading is
authoritative.

**Genesis.** The hash naming one workroom, fixed when it is created. It
pins the object format, the payload ceiling and the sequencer key.

**Kind.** Two unrelated senses. On a **statement**, the speech act:
`request`, `promise`, `report`, `artifact`, and so on. On a **principal**,
`human`, `agent` or `service` — which confers no authority.

**Nexus.** The amnesiac side of the resident service: presence, ephemeral
conversation, and change notification.

**Payload ceiling.** The genesis-fixed bound on a signed envelope plus its
inline payload and attachments. See [Limits](limits.md).

**Presence.** Who is in the room now, by leased session. Per-process, and
does not survive a restart.

**Projection.** What the fold produces: commitments, artifacts, actors,
decisions and provenance, as of one sequence head.

**Promotion.** Turning ephemeral conversation into a durable act, with
the selected signed frames embedded as evidence.

**Provenance.** The transitive walk back from an event through everything
it rests on.

**Ratify.** To confer force on a target you hold the authority for. The
requester ratifies a report; a ratifier ratifies assertions, proposals
and governance.

**Reneging.** Superseding your own promise. Visible forever; honourable
when early.

**Resident.** The local service started by `gs serve`.

**Role.** A durable, revocable authority grant: `participant`,
`operator`, `ratifier`, or a name a room's practice defines.

**Sequencer.** What admits signed events into one final order and signs
the sequence commits.

**Session.** One connection's ephemeral identity. A session identifier is
a credential and is never published; presence names sessions by opaque
`session:` handles instead.

**Stale.** Marked because a basis was retired, transitively. `STALE —
describes a superseded world` narrows it: the retired ancestor was itself
an artifact.

**Succession not recorded.** A warning that an artifact follows a
still-live artifact for the identical path — a probable forgotten
supersession.

**Supersede.** To retire an act, propagating staleness to everything
resting on it. Preferred to contradiction.

**Unable to flare.** A warning that an act cites nothing resolvable, so
nothing could ever make it stale. Its silence is not currency.

**Workroom.** One sequence, its actors, and the practice they share. It
lives in an ordinary git repository as `refs/seq/<genesis>`.
