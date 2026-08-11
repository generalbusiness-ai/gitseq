---
title: MCP inspect
summary: Read one exact canonical durable event with its decision, commitment chain, and bounded context.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f5c1bdd5d0d61dd0f2a968ae5cf0fbeb613b894f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b2edd696b01e4ce953cf31194eb1a3dbb67e9b56
---

# `inspect`

Reads one exact durable item. The tool calls `POST /v0/inspect` and
never fetches the complete `/v0/status` projection; use it after
[`work`](work.md) instead of transferring the whole projection merely
to read one event.

## Arguments

| argument | required | meaning |
|---|---|---|
| `event` | required | One full canonical event ID. An unknown ID fails instead of producing an inferred match. |
| `repo` | optional | The repository whose workroom this call acts in. |

## What comes back

The statement or act named by the event, its fold decision, any
request–promise–report chain it belongs to, its direct provenance
bases, and bounded related artifact and review lists — each with an
exact omitted count when the cap truncates it. The response names the
exact durable frontier it was read at.

If the resident is unavailable, the tool makes the same bounded
selection from a verified local snapshot and marks the response
`degraded`. The adapter caps inspection responses at 2 MiB.

## See also

- [`work`](work.md)
- [`gs provenance`](../gs/provenance.md)
