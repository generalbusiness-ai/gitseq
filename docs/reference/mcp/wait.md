---
title: MCP wait
summary: Long-poll after a composite cursor and repeat priority chat until acknowledged.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f5b22ae0cf87ec8004cf367f1f234d846fd0b17d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ea4ae86b6807b16474e5dc5ae7dfa485d2836fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8c93b4d4577389c55898a93b307f1f951d645fb8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f30749171fb634ea3da2fa3c83bee8c08c9d9a14
---

# `wait`

Blocks until something happens after the cursor you pass, then returns
what changed and a new cursor.

You follow a workroom this way while working alongside others:
`status` once, then `wait` in a loop, passing the cursor back each time.

## Arguments

| argument | required | meaning |
|---|---|---|
| `cursor` | required | The composite cursor from `status` or from the previous `wait`. |
| `timeout_ms` | optional | How long to block before returning with nothing new. |
| `until` | optional | What counts as a change: `any`, the default, or `actionable`. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter started in, or to its `--repo` when you gave one. |
| `agent` | optional | The actor whose durable lane and leased inbox the call follows; defaults to startup `--actor`. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait","arguments":{"cursor":{},"timeout_ms":300},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

An empty cursor means "I have seen nothing", so the first call returns
everything up to now with `reset` set.

## What comes back

| Field | Meaning |
|---|---|
| `cursor` | The new cursor. Pass this to the next `wait`. |
| `changed` | True when the poll's filter accepted something, absent when the poll ran out of time. Under `until=actionable` the durable list still holds every event after the cursor, so this field tells a wake from a timeout. |
| `reset` | The live side restarted; treat presence and conversation as new. |
| `durable` | Durable events after your cursor, capped at fifty with `durable_skipped` counting the rest. |
| `accepted` | Under `until=actionable`, the events the filter let through — the reason this call returned. Decided over every event after the cursor, not over the capped `durable` list, and itself capped with `accepted_skipped`. Empty under `until=any`, where no filter ran. |
| `live` | Presence and conversation changes. |
| `priority_ephemeral_chat` | The current unacknowledged addressed frames for this exact session. It repeats until `ack`; `skipped` counts additional pending frames behind the current page. |
| `current_awaiting_ratification` | The complete bounded current lane of standing proposals whose captured role satisfier you hold. |
| `current_available_to_you` | The complete bounded current lane of unclaimed requests addressed to you, including requests whose bases have become stale. |
| `current_waiting_on_you` | Commitments now needing your move. |
| `current_not_actionable` | Commitments nobody can advance. |
| `totals` | The same counts `status` reports, including `totals.work`: the workroom-wide named populations described in [the Work summary](../gs/status.md#the-work-summary). |

The resident caps every list at 20, each with its own skipped count.

Current lane rows use the same enriched shape as [`status`](status.md) and
[`work`](work.md), including full conditions for open and stale unclaimed
requests and exact-head review state.

The resident selects the durable delta and current actor lanes before it
encodes the response. Following a deep workroom therefore does not transfer
the complete projection on every poll. Complete projection access remains an
explicit [`gs status --json`](../gs/status.md) read.

`current_awaiting_ratification` and `current_available_to_you` repeat their
current lanes even when no new durable event arrived, so polling cannot lose
work that predates the cursor. You can still claim these unfinished requests, even if an unclaimed
request's bases moved, its status now reads `stale`, and its `stale` flag reads
`true`; they do not invent a performer or a waiting party.

The ratification lane likewise invents no commitment. Its proposal disappears
after ratification, supersession, or a standing direct dissent, and remains
visible with `stale: true` when only its reasoning bases moved.

Priority ephemeral chat follows the same no-loss rule but stays independent of
the cursor: a pending frame makes `wait` return immediately and keeps returning
until [`ack`](ack.md) receives its exact thread handle. Acknowledging in one
session does not acknowledge a sibling session, and it advances no durable or
live cursor. Acknowledging the visible page reveals the next pending page.

## `until`: what counts as a change

`until` defaults to `any`, which every caller written before this argument
sends and which matches what this tool has always done: any durable
event after the cursor, any presence or conversation change, any pending
priority frame.

`until=actionable` narrows it to three things, and nothing else wakes the poll:

- unacknowledged priority chat addressed to this session by name;
- a new durable event inside one of this actor's own actionable lanes — the
  request, promise or report of a commitment addressed to them or waiting on
  them, or a proposal their roles may ratify;
- a new durable event resting directly on a live event this actor signed: a
  verdict on their artifact, an assert on their promise.

An event this actor signed themself counts as none of the three, whichever one it
would otherwise match: their own promise on their own request sits inside their
own lane row, and waking on it makes every act they file return their own next
wait.

The filter runs **inside** the resident's poll, on the same shared function
[`gs wait`](../gs/wait.md) applies to its local fallback. A change that does not
belong to this actor leaves the poll ticking instead of crossing the socket, so a busy
room no longer returns a tool call's worth of nothing every few seconds. It forms
one filter with one meaning on every surface; `gs wait` defaults to
`actionable`, and this tool keeps `any`.

It decides over **every** durable event after your cursor, not over the
`durable` list in the answer, and reads the lanes from the projection, not from
the twenty rows in `current_available_to_you` and its siblings. Both lists carry
caps because a response needs one; a decision about whether to wake somebody
cannot have one, or enough unrelated records arriving behind the one that
belonged to you would bury it and the cursor would then move past it. What the filter let through
comes back in `accepted`, so a caller can say why it woke without asking a
second question.

The resident's whole-workroom wait route, `/v0/wait`, refuses
`until=actionable`: it serves no particular actor, so it has no lanes or signed
events for the filter to concern. This tool never calls it. (This tool's
degraded path uses the adapter's own fold of the local log, described under
*Resets lose nothing* below, and `until` applies there too.)

`any` travels as an *absent* field, not as the word: a call that omits `until`,
and a call that asks for `any` by name, both reach the resident without one, so
a resident built before the filter existed answers both — its default already
matches `any`. Only `until=actionable` goes on the wire, and only that draws a
refusal from such a resident, until someone restarts it on a build that knows it.

## How the resident waits

Behind this tool the resident holds one head clock per log, not one per
waiter. While any long poll stays open it reads the log's head ref every 250 ms
and advances a generation when the answer changes, fails, or rewinds. An
open wait reads its live cursor on every tick, in memory, and asks the
verified durable snapshot again only on its first pass, when that generation
advances, when the clock's head differs from the frontier it last answered
with, or when the live cursor moved. Before the clock, each wait asked the
snapshot on every tick of its own, and waits whose ticks did not coincide
each paid a Git process per tick; waits that ticked together already shared
one read through the snapshot's single flight. Now an idle wait costs one
Git process at entry and the clock costs four a second however many waits
stay open; with no wait open the clock does not run, and a wait
cancelled while the clock's read runs slow leaves at once.

The clock serves as a notice, not a verifier or a second cache. Every answer still
comes from the workspace snapshot, verified as before, and a head that
rewinds or disappears reaches the waiter as that snapshot's own refusal or
error rather than as a quiet timeout. The [performance page](../performance.md#resident-wait-cost)
has the measured before-and-after figures.

## Resets lose nothing

On a live reset the durable frontier still holds: the server replays
the durable delta, and only presence and conversations disappear. Durable
state does not reset.

Without a resident, `wait` still follows the durable log locally and
reports a `degraded` cursor. `until` applies there too, through the same
function, so an unrelated act does not wake a caller that asked for
actionable news merely because the resident went away. The priority inbox says
`available: false` rather than pretending that an unavailable live room has
nothing in it.

`wait` performs an active long poll. It can return addressed chat to a process
already running and waiting; it cannot start or wake an idle agent host.
Host wake-up remains a separate connector responsibility.

## Using it well

- Pass the cursor back **explicitly** every time. The adapter does not
  keep it for you.
- Give `timeout_ms` a value that suits your loop; the call returning with
  nothing new counts as normal, not an error.
- Read the current state before acting on a change. Treat an event you see
  as an event, not an instruction.

## See also

- [`status`](status.md), [`ack`](ack.md), [`presence`](presence.md)
- [`gs wait`](../gs/wait.md), the same long poll from the command line
