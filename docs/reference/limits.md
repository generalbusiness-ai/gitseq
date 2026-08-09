---
title: Limits
summary: The sizes and counts a call is refused for exceeding.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1539075831e59cbc39fefdd6a4e800ba2c150208
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

## Projection responses

The MCP `status` and `wait` digests are bounded. Every list is capped at
20 entries, each with its own skipped count on the wire and in the
summary line, so a shortened list reads as "20 of 500" rather than as a
bare count. Use `gs status --json` when you need the whole projection.

## Restart and the checkpoint

| Limit | Value |
|---|---|
| Checkpoint refresh cadence | every 256 accepted events after the last successful write |
| Serialized checkpoint blob | 256 MiB |

A successful checkpoint therefore leaves at most 255 sequence commits for
full delta verification, though persistent storage or signing failures
make the tail larger. Restart is linear in total history for the local
metadata proof and linear in the tail for commit-signature and payload
reads.

## Local view

The browser's event railway is a newest-80 window and says when it is
truncated.

## What is not limited

There is no bound on the depth of the sequence, on how many actors a
repository holds custody for, or on how many events one act may
transitively rest on. Cold audit cost grows with depth; that is what the
resident checkpoint exists to amortize, and what
[`gs verify`](gs/verify.md) deliberately does not use.
