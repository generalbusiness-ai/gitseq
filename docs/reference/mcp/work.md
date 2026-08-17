---
title: MCP work
summary: Page through the configured actor's durable work through a bounded resident-side selection.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
---

# `work`

Queries the configured actor's durable work. Selection happens at the
resident, before any transfer: the tool calls `POST /v0/work-query` and
never fetches the complete `/v0/status` projection.

## Arguments

| argument | required | meaning |
|---|---|---|
| `lanes` | optional | Typed relationship lanes: `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`. Default is all four. |
| `statuses` | optional | Lifecycle statuses to include: `open`, `promised`, `reported`, `satisfied`, `stale`, `cancelled`, `reneged`, `withdrawn`. An unknown status is an error, not a guess. |
| `stale` | optional | One staleness policy: `include` (default), `only`, or `exclude`. |
| `limit` | optional | Page size, 1 to 50. Default 20. |
| `cursor` | optional | The opaque continuation from a previous page. |
| `repo` | optional | The repository whose workroom this call acts in. |

Filters are finite, typed choices, not an expression language.

## What comes back

The default page includes current `open`, `promised`, and `reported`
commitments — including open, unclaimed requests addressed to the
configured actor — plus stale commitments in **every** lifecycle state.
Settled non-stale history requires an explicit status filter.

Every response gives the exact durable frontier, the matching total,
the returned count, the preceding count, the remaining count, and a
next cursor only when more remain. A cursor is bound to its exact head
and filters, so a moved head is an explicit refusal: restart the query
to read the new world rather than mixing two projections.

If the resident is unavailable, the tool makes the same bounded
selection from a verified local snapshot and marks the response
`degraded`. A resident rejection or oversized response is surfaced,
not hidden by fallback. The adapter caps work responses at 256 KiB.

## See also

- [`inspect`](inspect.md)
- [`status`](status.md)
