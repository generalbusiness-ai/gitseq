---
title: Limits
summary: The sizes and counts a call is refused for exceeding.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d97eed896404f401dae2439928e31c6a0290ed47
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5644516fe30fcb0920b688e19e2ef185d18240f1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:30206869d55828c9a4eb7d3c16d3cb71fe0cac8d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a383b4db5b97c20dae3e36463f2e0760904d9204
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d0fd7f5227adc05a6a42883aadd765dad0a89098
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d87af410275ef5dffdd11cdd5b9a2a3b5a62b45
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:63106fd8b893add378c21856065812b9f130f8a1
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

## Local view

The browser's event railway is a newest-80 window and says when it is
truncated.

## What is not limited

There is no bound on the depth of the sequence, on how many actors a
repository holds custody for, or on how many events one act may
transitively rest on. Cold audit cost grows with depth; that is what the
resident checkpoint exists to amortize, and what
[`gs verify`](gs/verify.md) deliberately does not use.
