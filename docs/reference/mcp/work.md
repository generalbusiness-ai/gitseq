---
title: MCP work
summary: Page through the configured actor's durable work through a bounded resident-side selection.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:30206869d55828c9a4eb7d3c16d3cb71fe0cac8d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d0fd7f5227adc05a6a42883aadd765dad0a89098
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d87af410275ef5dffdd11cdd5b9a2a3b5a62b45
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
