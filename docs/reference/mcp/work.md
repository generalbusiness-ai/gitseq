---
title: MCP work
summary: Page through the selected actor's durable work through a bounded resident-side selection.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0f2c5ac05d9e834d7e824680eafa805e43a1c04d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0a31c287af5b705b6b0991914cafd64d6ab4d39a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f85c910b62d17846463092a668e7af6d19b20fb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:adafb7b0046989609ff369efcac5acb605aa403a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c2c9d0ef010bb7227472c4b8ada52a33f4723e5
---

# `work`

Queries the selected actor's durable work. Selection happens at the
resident, before any transfer: the tool calls `POST /v0/work-query` and
never fetches the complete `/v0/status` projection.

## Arguments

| argument | required | meaning |
|---|---|---|
| `lanes` | optional | Typed relationship lanes: `awaiting_ratification`, `available_to_you`, `waiting_on_you`, `you_are_waiting_on`, `not_actionable`. Defaults to those five; `approved_not_landed` serves as an additional explicit audit lane. |
| `statuses` | optional | Row states to include: commitment lifecycle states plus `awaiting-ratification` for the non-commitment proposal lane. An unknown state draws an error, not a guess. |
| `target_ref` | optional | Exact destination filter, such as `refs/heads/release`. |
| `approved_not_landed` | optional | Boolean delivery-debt filter; false differs from absence. |
| `stale` | optional | One staleness policy: `summary` (default), `include`, `only`, or `exclude`. |
| `limit` | optional | Page size, 1 to 50. Default 20. |
| `cursor` | optional | The opaque continuation from a previous page. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose durable work the tool selects; defaults to startup `--actor`. The process must already have access to the actor's key. |

Filters offer finite, typed choices, not an expression language.

The additional explicit lane `approved_not_landed` selects the actor as
performer or hold owner without changing the waiting party. Legacy satisfied
rows with delivery debt remain visible. The five existing lanes stay the
default. Each row carries the shared [landing evidence and Git observations](../landing-observations.md),
including a compatibility warning only when its witnessed receipt sealed one.

## What comes back

The default page shows the work still owed: effective proposals whose captured
role satisfier the configured actor holds, plus current `open`, `promised`,
`reported`, `awaiting-review`, `awaiting-authorization`, and `awaiting-landing`
commitments — including unclaimed requests addressed to the
selected actor, even when their bases moved and their status became `stale`
— plus commitments the fold left in a `stale`,
`cancelled` or `reneged` state, which nobody has closed.

A `superseded`, `satisfied`, `withdrawn`, or `abandoned` commitment counts as finished, and the default
leaves it out unless it carries approved-artifact landing debt. Ordinary
reasoning staleness alone does not bring it back: a
basis moving under a closed commitment reflects the normal condition of an
append-only log, it blocks nothing, and listing every one of them buried
the rows still owed. The response says how many it left out
in `closed_stale_omitted`, so the summary stays visible rather than silent.

The four staleness policies:

| `stale` | What comes back |
|---|---|
| `summary` (default) | Work still owed. The page counts closed commitments carrying only ordinary staleness and no landing debt in `closed_stale_omitted` rather than listing them. |
| `include` | The default lanes **and** every closed commitment carrying staleness, each with its own `stale` field. |
| `only` | Only records carrying staleness, in any lifecycle state. |
| `exclude` | Only records carrying no staleness. |

Naming any status filter also overrides the summary: `statuses:
["satisfied"]` returns settled history whether or not it has gone stale. An
unknown policy draws an error, not a guess.

Every returned row still carries its own `stale` field. The default
changes which rows the page lists, never what a listed row says.

An `awaiting_ratification` row signals attention, not a commitment. It carries
the proposal in `event`, with `kind`, `author`, `satisfier`, `text`, and
`stale`; `request` stays empty and the tool invents no performer, promise, or
waiting party.
Ratification, proposal supersession, or a standing effective direct dissent
clears it. Use state `awaiting-ratification` when selecting only these rows.

Every response gives the exact durable frontier, the matching total,
the returned count, the preceding count, the remaining count, and a
next cursor only when more remain. A cursor binds to its exact head
and filters, so a moved head draws an explicit refusal: restart the query
to read the new world rather than mixing two projections. The cursor does not
freeze current Git observations between pages.

Each returned row also carries the facts needed for routine action without an
`inspect` round trip:

| Field | Meaning |
|---|---|
| `conditions` | The full, untruncated `body.conditions` for an unclaimed request whose status reads `open` or `stale`. |
| `report_status` | The reported statement's `body.status`, when present. |
| `reported_head` | The exact head named by the report or reporting artifact. |
| `approval`, `candidate` | The fold-selected ratified approval and candidate, separate from the latest review and current Git incorporation. |
| `landing_receipt`, `terminal` | The validated receipt witness and closure reason; see the shared landing fields above. |
| `latest_review` | The latest effective review for that exact head: its report event, verdict, and explicit `ratified`, `retired`, and `stale` booleans. |
| `successor_request` | On a closed `superseded` row, the exact successor carrying a rejected repair or an approved artifact. |

The page still caps its row count. It does not shorten `conditions` or omit
these fields merely to fit more rows into one answer.

Without an available resident, the tool makes the same bounded selection from
a verified local snapshot and marks the response `degraded`. The tool surfaces
a resident rejection or oversized response rather than hiding it by fallback.
The adapter's byte ceiling equals the 256 KiB base plus one repository payload
ceiling per possible row, capped at 64 MiB. That admits full request
conditions while remaining independent of workroom depth.

## See also

- [`inspect`](inspect.md)
- [`artifacts`](artifacts.md)
- [`status`](status.md)
