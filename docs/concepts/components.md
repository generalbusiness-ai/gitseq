---
title: Components
summary: The CLI, the resident service, the MCP adapter, the browser view, and the repository underneath.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7755a1195e83805be2a8fa5023c70f609891ec40
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:94cadf30855bd467e8b29a4529297c63eac4cb7b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fd0680effdbc154f7f17a8f801bed602f20e3717
---

# Components

## The repository underneath

The workroom is an overlay on an ordinary git repository.

| Where | What |
|---|---|
| `refs/seq/<genesis>` | The sequence. One commit per durable event. |
| `.git/gitseq/` | Local configuration and actor keys. Not shared. |
| `.git/gitseq/checkpoints/<genesis>.json` | The application-owned selector for a sequencer-signed verification checkpoint. It survives process restarts and is never published. |
| `refs/gitseq/checkpoints/<genesis>` | The reachability and recovery anchor for the same local signed checkpoint object. It keeps `git gc` from pruning the object and is never published. |

Git ignores `refs/seq/*` in both directions, so publishing and fetching
the sequence are deliberate acts. Your branches, tags and working tree
are untouched.

Artifacts never live in the workroom. They are files, commits and
branches, exactly as always; the workroom carries the why.

## This repository

| Path | Contents |
|---|---|
| `cmd/` | The shipping `gs` and `gitseq-mcp` commands. |
| `internal/` | The kernel, the workroom profile, the nexus, and the service. |
| `docs/` | This documentation set. |
| `SKILL.md` | The normative contract for an agent in a workroom. |
| `notes/` | Dated design notes, not maintained. |
| `spike/` | The adversarial CLI, the report generator, and the six-case evidence. |
| `ui/` | The browser projection source. |
| `internal/service/uidist/` | The committed browser build the resident serves. |

The resident serves the committed build, not `ui/src`, so changes to the
browser source and its committed output must travel together.

## `gs`

One binary, sixteen subcommands: create a workroom, add principals, grant
and revoke roles, append durable acts one at a time or as a chain, guard
review and merge, project status, walk provenance, verify signatures, run
the resident service, and attach a clone.

Durable subcommands that accept `--server` submit through a resident
sequencer when given one and write straight to the local log when not.
Both land in the same sequence.

See [the `gs` reference](../reference/gs/).

## The resident service

`gs serve` runs a local process that does three things the CLI cannot:

- **Sequencing under contention.** Concurrent appends are compare-and-swap
  on the git ref and retry, so several actors can write at once.
- **Presence and ephemeral conversation** — the amnesiac nexus. This
  includes bounded per-session addressed inboxes and acknowledgements. The
  service resolves Workroom names to fingerprints before the nexus signs and
  retains the conversation for every current matching lease. Only leases that
  registered the inbox protocol receive pending inbox references. This state
  is per-process and does not survive.
- **Change notification.** Long-poll `wait` returns when something moves,
  rather than making every reader poll.

It also serves the browser view at its listen address, and keeps a signed
checkpoint so restart does not re-audit the whole log.

It publishes the address it bound inside the repository it serves, so
clients find it by naming the repository rather than by being told a URL.

It binds loopback addresses only. It is a trusted local custodian for
several actors on one machine, not an authenticated remote server. Run
exactly one per repository. See
[Deploy a resident](../how-to/deploy-a-resident.md).

## The MCP adapter

`gitseq-mcp` is one process per client session, one actor per process. It
signs everything that session does as that actor and holds a leased,
session-bound presence.

The repository is a parameter of each call, not of the installation.
Register the command once; a call acts in the adapter's working directory
unless it names another repository. The resident service is read from the
repository being acted in.

It is dual-era: it serves the stateless `2026-07-28` shape and the
`initialize` handshake of `2025-11-25` and earlier. Era is a property of
the connection, chosen by how the client opens and settled once.

If the resident service is down, the durable tools keep working against
the local log and report a `degraded` live cursor. Priority chat is marked
unavailable; `say`, `ack`, and `presence` fail rather than pretend, because
ephemeral state genuinely does not survive.

See [the MCP reference](../reference/mcp/) and
[Configure an agent](../how-to/configure-an-agent.md).

## The browser view

The resident serves a live projection at its listen address, as two
screens and no more. The first is a list of open requests, one single
line each: its state, the one actor it waits on, how long since anything
in its thread moved, its title, and its log number. Priority order is the
default — needing attention, then unclaimed, then waiting on a human,
oldest first inside each — and clicking a column header re-sorts, clicks
again to reverse, and a third time returns to priority order. A sort
reorders the rows that are there; nothing on this screen decides which
rows exist. One number heads the list and equals the rows beneath it, and
the two quiet lines below it — work resting on reasoning that has moved,
and stale requests no longer in flight — each open to exactly the rows they
count.

Clicking a row opens the second screen, the thread. It draws the
commitment spine as a vertical rail: the request, the promise, the report
or artifact, the latest review verdict, and whether the approved head
reached the mainline, with any live blocker branching off beside the row
it concerns. A station nobody has reached yet is still a row, hollow, and
names who owes it. Everything else in the thread sits behind counted
expanders that open to exactly the records they counted, so what is
elided is stated rather than silent.

The merge station reads two sources, because they can disagree. The fold
knows whether a commitment closed; only Git knows whether the code
landed, and work that shipped and stayed open is invisible on every other
surface. That question is asked of Git when the row is drawn, never
stored as a field somebody types. Its three answers are kept apart:
landed, absent, and could-not-be-determined. A check that fails must not
read as a negative.

Ordinary staleness stays quiet — it is counted below the list and never
colours a row — while world-staleness and retirement stay loud, because
those are what a merge will actually refuse.

The resident also reads local worktree state, which is *not* part of the
durable projection. That endpoint names the served checkout's own
absolute path, so a reader can tell which repository the page is showing,
and otherwise emits only checkout basenames, branch and HEAD, and
explicit clean, dirty, detached, bare, locked, prunable or unavailable
state. Naming the served path is safe because the service binds loopback
addresses only: whoever is reading the page is already on the host it
names.

## Choosing a path in

| You are | Start with |
|---|---|
| A person at a terminal | `gs` |
| An agent in an MCP client | `gitseq-mcp`, plus [`SKILL.md`](../../SKILL.md) |
| An auditor with a clone | `gs attach`, then `gs verify` |
| Watching work in progress | the browser view |
