---
title: Limits
summary: The sizes and counts a call is refused for exceeding.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8aa25919999f625d17a15302e3a535cd6c0012c9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:416d72476ccd31f44ab7c56de98ac3a0709c4a04
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:48bd5acfe51abd4146197a48b0f7674f5676cc5c
---

# Limits

These bounds are enforced on the write path, before the sequence ref
moves, and again by readers. A refused act leaves nothing behind.

## Signed intent

| Limit | Value |
|---|---|
| Any actor-controlled string in the signed intent | 32 KiB |
| Causal references in one intent (`rests_on`) | 4,096 |
| Signed envelope plus inline payload and attachments | the workroom's genesis `payload_ceiling` |

`payload_ceiling` is fixed when the workroom is created, by
`gs init --payload-ceiling`, and defaults to 1 MiB. It cannot be changed
afterwards without creating a new workroom, because every reader
validates against the value recorded in genesis.

Readers enforce the same envelope ceiling explicitly, so the write path
cannot admit a commit that a parser would later reject.

## Concurrent submissions

| Limit | Value |
|---|---|
| Submissions inside the sequencer at once | 32 |

The count includes the submission holding the sequencer lock, so 32 means
one in progress and 31 waiting. Over that bound a submission is refused
rather than queued, and the refusal says `sequencer at capacity`.

That refusal is taken first: before the signed intent is parsed, before
any admission hook runs, before the payload tree is written, and before
anything is chained onto the sequence ref. A submission refused for
capacity therefore costs almost nothing and leaves no object behind,
which is what makes it a signal a caller can act on rather than a late
failure after the work is already spent.

The same `ErrBackPressure` sentinel also reports one other condition: a
submission that exhausted its retry limit while chaining under
contention. Both mean "overload, try again later", and that is why they
share a sentinel — `errors.Is` separates overload from a malformed or
unauthorized submission, which is the distinction a caller needs. But
only the capacity refusal is free. A retry exhaustion has already decoded
the intent and written objects, so do not read the paragraph above as a
guarantee about every `ErrBackPressure`. Rotation keeps its own
unnamed exhaustion error and is not part of this.

The bound belongs to Gitseq's resident. A program embedding the kernel
directly may leave it unset, which means an unbounded queue; that is the
embedding opt-out, not a posture Gitseq takes.

## Genesis sequencer key

Genesis pins exactly one canonical `ssh-ed25519` sequencer public key:
the key type, one ASCII space, and the base64 wire key. No options, no
principals, no comment, no additional lines. Creation and auditor
decoding apply the same validation before the value can become an
OpenSSH allowed-signers entry.

That key can be rotated **in band**. A rotation is a reserved,
empty-tree commit signed by the current sequencer key that names exactly
one canonical successor. The successor becomes current only after that
commit: later commits signed under the retired key are refused, and full
and incremental audits both carry the current key forward as they walk
the sequence. Rotation commits increase the sequence depth but are not
application events.

### What rotation does not recover

Rotation limits damage; it does not restore authority that is already
gone.

- A lost current private key cannot sign its successor, so recovery
  requires an out-of-band continuation.
- Whoever holds a compromised current key can rotate to another key
  before the legitimate operator does. The append-only history shows that
  rotation, but the kernel cannot decide which competing custodian was
  legitimate, and it cannot undo events the compromised key already
  signed.

## Projection responses

`gs status` and the MCP `status` and `wait` digests are all bounded.
Every list keeps the newest 20 entries and reports its own omitted count,
so a shortened list reads as "20 of 500" rather than as a bare count.
User-controlled text in the `gs status` view is normalized to one line
and capped at 240 bytes. Use `gs status --all` or `gs status --json` when
you need the whole projection; neither is capped.

Addressed ephemeral chat is also bounded before it is signed and indexed:

| Limit | Value |
|---|---|
| Authored chat text | 16 KiB of valid UTF-8 |
| `about`, reply, or acknowledgement handle | 256 bytes |
| Signed recipient fingerprints | 32 unique recipients |
| Signed frame payload | 20 KiB |
| Current priority inbox page | 20 frames per leased session |
| Pending addressed frames | 256 per inbox-capable leased session |
| Acknowledgement batch | 20 exact thread handles |
| Live sessions | 256 per resident; 16 per actor |
| Retained conversations | 4,096 frames and 8 MiB of payload per resident |

The priority view returns the oldest 20 pending frames. `skipped` is the count
of additional pending frames hidden behind that page, not a count of lost
frames. Acknowledging visible handles reveals the next page. Publication is
refused before it changes the room when a recipient, reference, frame, or byte
limit is full. Expiry, departure, acknowledgement, and conversation forgetting
release the corresponding capacity. Only sessions that registered the current
versioned inbox protocol consume pending-frame capacity. Conversation and
inbox state remain process-local and are not durable.

## Live attention

Every completed MCP tool call carries a bounded `live_attention` adjunct
when the resident can answer for it, including tool-specific error
results where the attention read itself succeeded. It is advisory
throughout: it creates no ownership, promise, authority, completion, or
durable read receipt, and a client that ignores it entirely loses
nothing but awareness. No resident yields `available: false`, and a
failed attention read never fails the durable operation it rides beside.

| Limit | Value |
|---|---|
| Event identifiers one call asks about | 32, from the tool input and its result combined |
| Actors reported for those events | 16, with the remainder counted rather than dropped |
| Frames in the adjunct | the 20-frame priority page above, with pending and omitted counts |

Actors are matched by exact equality on canonical event identifiers the
caller already holds. There is no prefix matching and no inference about
what relates to what: a guess about relatedness would be the adapter
asserting a relationship nobody stated, which is the one thing an
observation must not do.

Each row carries the full durable fingerprint, never a prefix, because a
truncated identity invites the reader to match it against another
truncation. A caller's own sessions are filtered out before actors are
aggregated, so one person working from two windows reads as one actor
rather than two people. `activity_changed_at` is observed by the
resident and moves only when status, focus, or note changes — a
heartbeat renewal leaves it alone, so an old timestamp means an old
decision rather than a quiet client.

Addressed frames repeat in the adjunct until the recipient explicitly
acknowledges them, because reading is not acknowledging. Acknowledgement
is per leased session, so one session's acknowledgement never clears
another's.

## Restart and the checkpoint

| Limit | Value |
|---|---|
| Checkpoint refresh cadence | every 256 accepted events after the last successful write |
| Serialized checkpoint blob | 256 MiB |

A successful checkpoint therefore leaves at most 255 sequence commits for
full delta verification, though persistent storage or signing failures
make the tail larger. Restart is linear in total history for the local
metadata proof and linear in the tail for commit-signature and payload
reads. Rotations are the exception to that shortcut: a rotation inside
the cached prefix still costs a signature check, because the key the
checkpoint is authenticated under is derived through them.

The repository-private pointer at
`.git/gitseq/checkpoints/<genesis>.json` is bounded to 4 KiB and written by
atomic replacement. It is not trusted by itself: it only selects a Git
checkpoint object whose shape, profile, sequence position, payload bindings,
and sequencer signature are verified before its cached prefix is used.

## Local view

The browser's commit graph is a newest-80 window and says when it is
truncated: the resident caps the graph it serves at 80 commits and marks
the response truncated, and the view then says "Showing the newest 80
commits." The event railway beside it is not windowed that way; it folds
lanes when it runs out of room.

## What is not limited

There is no bound on the depth of the sequence, on how many actors a
repository holds custody for, or on how many events one act may
transitively rest on. Cold audit cost grows with depth; that is what the
resident checkpoint exists to amortize, and what
[`gs verify`](gs/verify.md) deliberately does not use.
