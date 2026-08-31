---
title: MCP work
summary: Page through the configured actor's durable work through a bounded resident-side selection.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f5b22ae0cf87ec8004cf367f1f234d846fd0b17d
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
| `lanes` | optional | Typed relationship lanes: `awaiting_ratification`, `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`. Default is all five. |
| `statuses` | optional | Row states to include: commitment lifecycle states plus `awaiting-ratification` for the non-commitment proposal lane. An unknown state is an error, not a guess. |
| `stale` | optional | One staleness policy: `summary` (default), `include`, `only`, or `exclude`. |
| `limit` | optional | Page size, 1 to 50. Default 20. |
| `cursor` | optional | The opaque continuation from a previous page. |
| `repo` | optional | The repository whose workroom this call acts in. |

Filters are finite, typed choices, not an expression language.

## What comes back

The default page is the work still owed: effective proposals whose captured
role satisfier the configured actor holds, plus current `open`, `promised`,
`reported`, and `awaiting-merge` commitments — including unclaimed requests addressed to the
configured actor, even when their bases moved and their status became `stale`
— plus commitments the fold left in a `stale`,
`cancelled` or `reneged` state, which nobody has closed.

A `superseded`, `satisfied`, or `withdrawn` commitment is finished, and the default
leaves it out. Ordinary reasoning staleness does not bring it back: a
basis moving under a closed commitment is the normal condition of an
append-only log, it blocks nothing, and listing every one of them buried
the rows that were still owed. The response says how many were left out
in `closed_stale_omitted`, so the summary is visible rather than silent.

The four staleness policies:

| `stale` | What comes back |
|---|---|
| `summary` (default) | Work still owed. Closed commitments carrying ordinary staleness are counted in `closed_stale_omitted`, not listed. |
| `include` | The default lanes **and** every closed commitment carrying staleness, each with its own `stale` field. |
| `only` | Only records carrying staleness, in any lifecycle state. |
| `exclude` | Only records carrying no staleness. |

Naming any status filter also overrides the summary: `statuses:
["satisfied"]` returns settled history whether or not it is stale. An
unknown policy is an error, not a guess.

Every returned row still carries its own `stale` field. The default
changes which rows are listed, never what a listed row says.

An `awaiting_ratification` row is attention, not a commitment. It carries the
proposal in `event`, with `kind`, `author`, `satisfier`, `text`, and `stale`;
`request` is empty and no performer, promise, or waiting party is invented.
Ratification, proposal supersession, or a standing effective direct dissent
clears it. Use state `awaiting-ratification` when selecting only these rows.

Every response gives the exact durable frontier, the matching total,
the returned count, the preceding count, the remaining count, and a
next cursor only when more remain. A cursor is bound to its exact head
and filters, so a moved head is an explicit refusal: restart the query
to read the new world rather than mixing two projections.

Each returned row also carries the facts needed for routine action without an
`inspect` round trip:

| Field | Meaning |
|---|---|
| `conditions` | The full, untruncated `body.conditions` for an unclaimed request whose status is `open` or `stale`. |
| `report_status` | The reported statement's `body.status`, when present. |
| `reported_head` | The exact head named by the report or reporting artifact. |
| `latest_review` | The latest effective review for that exact head: its report event, verdict, and explicit `ratified`, `retired`, and `stale` booleans. |
| `successor_request` | On a terminal `superseded` row, the exact repair child named by the qualifying linked supersession. |

The page still caps its row count. It does not shorten `conditions` or omit
these fields merely to fit more rows into one answer.

If the resident is unavailable, the tool makes the same bounded
selection from a verified local snapshot and marks the response
`degraded`. A resident rejection or oversized response is surfaced,
not hidden by fallback. The adapter's byte ceiling is the 256 KiB base plus
one repository payload ceiling per possible row, capped at 64 MiB. That admits
full request conditions while remaining independent of workroom depth.

## See also

- [`inspect`](inspect.md)
- [`artifacts`](artifacts.md)
- [`status`](status.md)
