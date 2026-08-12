---
title: MCP inspect
summary: Read one exact canonical durable event with its decision, commitment chain, and bounded context.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:66b6cb0b770fe88808130a195babf79fe1ea7746
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
