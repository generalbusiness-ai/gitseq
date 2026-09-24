---
title: Limits
summary: The sizes and counts a call may not exceed.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c4f0d4142bfa057005b09e59bc0a3462980842b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:617a0446bf89ef5ce8ccff6d095052d602d1dfc7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b2c6b2a03e3c03af9a20985a40f85e09f31ee417
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:54826d805556c6dd81ccc460bf4c5ce80abb4e5b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b1c98a63e3a0cfa3c4638086a2551d82fe78e14b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7d6f6997c01a89e509dec03f68fc6ba4fb4125fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:52966895e59050b9a39308e6069ddb9ae7bd0c2e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f51aec8a375e1a1bcc04b9bc6dcbe04f640c397d
---

# Limits

The write path enforces these bounds before the sequence ref moves, and
readers enforce them again. A refused act leaves nothing behind.

Everything on this page describes a refusal, with one exception: the dated
headroom measurement kept beside the ceiling it measures against, marked as
evidence, which refuses nothing. Nothing on this page makes a performance
commitment, and no measurement makes something here more or less binding. The
scale envelopes and the measured latency, memory and checkpoint sizes live on
the [performance evidence](performance.md) page, where they serve as evidence
about one workload and one machine.

## Signed intent

| Limit | Value |
|---|---|
| Any actor-controlled string in the signed intent | 32 KiB |
| Causal references in one intent (`rests_on`) | 4,096 |
| Signed envelope plus inline payload and attachments | the workroom's genesis `payload_ceiling` |

`gs init --payload-ceiling` fixes `payload_ceiling` when it creates the
workroom, and the value defaults to 1 MiB. Nobody can change it afterwards
without creating a new workroom, because every reader validates against the
value recorded in genesis.

Readers enforce the same envelope ceiling explicitly, so the write path
cannot admit a commit that a parser would later reject.

Gitseq refuses an act over the ceiling. It stores nothing by reference,
converts no attachment, and accepts no external locator. The refusal
names the ceiling, the measured total, the split across envelope, payload
and attachments, and the largest attachment by name, so an author can see
what to shrink.

The application applies that measurement where it signs, so the refusal
arrives before a signature exists and before an act travels anywhere. One
boundary covers every surface: `gs state`, `gs batch`, merge planning, MCP
`state` and the resident's `/v0/act`. It measures against the bound that
genesis records, so it holds on every workroom, including one attached
to a fetched sequence whose local configuration carries no copy of it. No
signature means no act under that retry key, so an author may file a refused
act again under the same `--idempotency-key`.

## Resident request body

| Limit | Value |
|---|---|
| Any JSON request body the resident decodes | 2 MiB |

This limit serves as the resident's transport bound, separate from the
genesis ceiling; the resident enforces it before parsing the body. It covers
every endpoint that `decode` decodes, including `/v0/submit` and the
browser's `/v0/act`. The resident applies more than this one body bound:
`/v0/preview` caps its own body at 8 KiB.

JSON carries attachment bytes base64 encoded, so 1 MiB of attachments
travels as about 1.4 MiB of body. On a workroom at the default 1 MiB
ceiling the genesis ceiling binds first, by about 1.5x. A workroom created
with a larger `payload_ceiling` can meet this cap first, and its refusal
says so in different words.

Two write paths meet this cap differently, and the difference deserves
attention. `gs batch` and merge planning measure it themselves before they
change anything else, so they refuse a receipt they cannot send before the
Git merge rather than discovering it after. A single act does not: the client
sends it, and the resident's own cap answers. Gitseq chose that asymmetry
deliberately for the paths that must not half-finish, and it means a single
act learns about this bound one round trip later than a batch does.

## Measured attachment use

This section gives evidence about headroom. Gitseq enforces nothing here, and
nothing here counts as demand evidence.

An independent census on 2026-09-09 (assert `0a048f5d`) measured every one of
the 27,845 non-genesis events at four pinned workroom heads, records with and
without attachments alike. A second run at the same saved heads produced
identical room measurements. All four workrooms record a ceiling of 1,048,576
bytes.

555 events carry 1,772 attachments. By nearest-rank percentile an attachment
measures 1,279 bytes at the median, 9,145 at the 90th, 60,232 at the 99th, and
498,733 at the largest. Largest complete event per room:

| Workroom | Largest event | Headroom against its ceiling |
|---|---|---|
| Gitseq | 196,373 bytes | 5.34 times |
| Tailapp | 535,397 bytes | 1.96 times |
| Chess | 86,436 bytes | 12.13 times |
| Inventory | 31,972 bytes | 32.80 times |

The largest record carrying no attachment at all measures 74,031 bytes, in
Gitseq. Keep the rooms distinct: the fleet maximum belongs to Tailapp, and it
does not describe Gitseq's headroom.

What this cannot say. It measures the bytes of accepted records. Refused
admission creates no event, so accepted history cannot show how many attempts
met refusal, or whether any did. The search of the retained logs found no
refusal evidence, and that search had a limited scope. Nobody asking for a
larger attachment would appear here either. Read these numbers as the size of
what authors have written, and nothing more.

## Concurrent submissions

| Limit | Value |
|---|---|
| Submissions inside the sequencer at once | 32 |

The count includes the submission holding the sequencer lock, so 32 means
one in progress and 31 waiting. Over that bound the sequencer refuses a
submission rather than queueing it, and the refusal says `sequencer at capacity`.

The sequencer takes that refusal first: before it parses the signed intent,
before any admission hook runs, before it writes the payload tree, and before
it chains anything onto the sequence ref. A submission refused for
capacity therefore costs almost nothing and leaves no object behind,
which makes it a signal a caller can act on rather than a late
failure after the system has already spent the work.

The same `ErrBackPressure` sentinel also reports one other condition: a
submission that exhausted its retry limit while chaining under
contention. Both mean "overload, try again later", and for that reason they
share a sentinel — `errors.Is` separates overload from a malformed or
unauthorized submission, the distinction a caller needs. But only the
capacity refusal comes free. A retry exhaustion has already decoded
the intent and written objects, so do not read the paragraph above as a
guarantee about every `ErrBackPressure`. Rotation keeps its own
unnamed exhaustion error and falls outside this.

The bound belongs to Gitseq's resident. A program embedding the kernel
directly may leave it unset, which means an unbounded queue; that marks the
embedding opt-out, not a posture Gitseq takes.

## Published paths

`gs publish` applies separate bounds before it queues a new batch of
publication facts:

| Limit | Value |
|---|---|
| Tracked `.gitseq` file | 64 KiB and valid UTF-8 |
| Watch globs | 64 |
| Branch ref | 1,024 bytes, valid UTF-8, with no control bytes |
| Governing basis | 1,024 bytes, valid UTF-8, with no control bytes |
| Published path | 4,096 bytes, valid UTF-8, with no control bytes |
| Watched paths in one published head | 256 |
| Attempts before gitseq abandons an entry | 3 |
| Batches in one actor's outbox | 16 |

A bad ref, basis, or configuration refuses the command. `gs publish` reports
one bad path while the other watched paths publish; exceeding the 256-path
ceiling refuses the whole new batch before any act enters the queue and before
an outbox file exists. The repository-private outbox and frontier each have a
4 MiB bound, applied before reading the file rather than after. See
[`gs publish`](gs/publish.md) for the derivation and reconciliation rules.

## Genesis sequencer key

Genesis pins exactly one canonical `ssh-ed25519` sequencer public key:
the key type, one ASCII space, and the base64 wire key. No options, no
principals, no comment, no additional lines. Creation and auditor
decoding apply the same validation before the value can become an
OpenSSH allowed-signers entry.

An operator can rotate that key **in band**. A rotation takes the form of a
reserved, empty-tree commit, signed by the current sequencer key, that names
exactly one canonical successor. The successor becomes current only after that
commit: Gitseq refuses later commits signed under the retired key, and full
and incremental audits both carry the current key forward as they walk
the sequence. Rotation commits increase the sequence depth but do not count as
application events.

### What rotation does not recover

Rotation limits damage; it does not restore authority that has already
gone.

- A lost current private key cannot sign its successor, so recovery
  requires an out-of-band continuation.
- Whoever holds a compromised current key can rotate to another key
  before the legitimate operator does. The append-only history shows that
  rotation, but the kernel cannot decide which competing custodian acted
  legitimately, and it cannot undo events the compromised key already
  signed.

## Projection responses

Bounds apply to `gs status` and to the MCP `status` and `wait` digests.
Every list keeps the newest 20 entries and reports its own omitted count,
so a shortened list reads as "20 of 500" rather than as a bare count.
The `gs status` view normalizes user-controlled text to one line and caps it
at 240 bytes. The resident selects MCP status and wait views before
encoding them; it does not move the complete projection merely to discard it
in the adapter. Use `gs status --all` or `gs status --json` when you need the
whole projection; neither has a cap.

Bounds also apply to addressed ephemeral chat before signing and indexing:

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

The priority view returns the oldest 20 pending frames. `skipped` counts the
additional pending frames hidden behind that page, not lost frames.
Acknowledging visible handles reveals the next page. When a recipient,
reference, frame, or byte limit has filled, the resident refuses publication
before it changes the room. Expiry, departure, acknowledgement, and
conversation forgetting release the corresponding capacity. Only sessions
that registered the current versioned inbox protocol consume pending-frame
capacity. Conversation and inbox state remain process-local and do not
persist.

## Live attention

Every completed MCP tool call carries a bounded `live_attention` adjunct
when the resident can answer for it, including tool-specific error
results where the attention read itself succeeded. It stays advisory
throughout: it creates no ownership, promise, authority, completion, or
durable read receipt, and a client that ignores it entirely loses
nothing but awareness. No resident yields `available: false`, and a
failed attention read never fails the durable operation it rides beside.

| Limit | Value |
|---|---|
| Event identifiers one call asks about | 32, from the tool input and its result combined |
| Actors reported for those events | 16, with the remainder counted rather than dropped |
| Frames in the adjunct | the 20-frame priority page above, with pending and omitted counts |

The adjunct matches actors by exact equality on canonical event identifiers
the caller already holds. It does no prefix matching and draws no inference
about what relates to what: a guess about relatedness would amount to the
adapter asserting a relationship nobody stated, the one thing an
observation must not do.

Each row carries the full durable fingerprint, never a prefix, because a
truncated identity invites the reader to match it against another
truncation. The adjunct filters out a caller's own sessions before it
aggregates actors, so one person working from two windows reads as one actor
rather than two people. The resident observes `activity_changed_at`, and the
value moves only when status, focus, or note changes — a
heartbeat renewal leaves it alone, so an old timestamp means an old
decision rather than a quiet client.

Addressed frames repeat in the adjunct until the recipient explicitly
acknowledges them, because reading does not acknowledge. Acknowledgement
works per leased session, so one session's acknowledgement never clears
another's.

## Restart and the checkpoint

| Limit | Value |
|---|---|
| Checkpoint refresh cadence | every 256 accepted events after the last successful write |
| Serialized checkpoint blob | 256 MiB |

The [performance evidence](performance.md) page measures the serialized size
an actual checkpoint reaches at 50,000 and 500,000 records. That measurement
serves for comparison against this ceiling; it does not set it.

A successful checkpoint therefore leaves at most 255 sequence commits for
full delta verification, though persistent storage or signing failures
make the tail larger. Restart cost grows linearly with total history for the
local metadata proof and linearly with the tail for commit-signature and
payload reads. That describes the shape of the cost, not a bound on it:
Gitseq refuses no restart for taking too long, and the
[performance evidence](performance.md) page measures the seconds it actually
takes. Rotations make the exception to that shortcut: a rotation inside the
cached prefix still costs a signature check, because Gitseq derives the key
that authenticates the checkpoint through them.

The repository-private pointer at
`.git/gitseq/checkpoints/<genesis>.json` has a 4 KiB bound, and Gitseq writes
it by atomic replacement. Gitseq does not trust it by itself: it only selects
a Git checkpoint object whose shape, kernel identity, sequence position,
payload bindings, sequencer-key lineage, and signature Gitseq verifies before
using its cached prefix. It carries no application projection or fold-profile
key, so the authenticated event prefix remains reusable when the host changes
folds. The corresponding local ref serves as the object's garbage-collection
root. The pointer exists separately as application-owned, process-independent
state: it can recover selection after ref loss, while the ref can repair
pointer loss.

## Local view

The resident caps the commit graph it serves at 80 commits and marks the
response truncated. Nothing in the browser draws that graph any more: a change
removed the surface it fed. The resident still serves `/v0/graph`, and `ui/`
still carries an `api.graph` fetcher for it, but nothing calls that fetcher.
The limit therefore bounds what the endpoint returns, not what any page shows.

## What has no limit

Nothing bounds the depth of the sequence, how many actors a repository holds
custody for, or how many events one act may transitively rest on. Cold audit
cost grows with depth; the resident checkpoint exists to amortize that cost,
and [`gs verify`](gs/verify.md) deliberately does not use the checkpoint.

Because none of those has a bound, the scale envelopes impose no bounds
either. PREVIEW and FIRST-PRODUCTION name measured workloads, on the
[performance evidence](performance.md) page. Exceeding an envelope refuses
nothing, and staying inside one promises nothing about latency or memory. A
production commitment would take a different form: it would need a
concrete ordinary proposal and its adoption, and it would appear on this page
only if the write path actually enforced it.
