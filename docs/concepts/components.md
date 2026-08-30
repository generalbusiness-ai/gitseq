---
title: Components
summary: The CLI, the resident service, the MCP adapter, the browser view, and the repository underneath.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2ef0bb48f6842c8f43f9aaacb6bed75584a77e48
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1a5bb9becc97d3ae601879a02b19923a2194811e
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
| `host/live/` | The public, application-neutral live runtime. |
| `internal/` | The kernel, the workroom profile, and the service composition. |
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

Durable subcommands that accept `--server` submit through the resident
the repository advertises, write straight to the local log when nothing is
advertised, and take an explicit URL or `-` (always local) to override. Both
paths land in the same sequence.

See [the `gs` reference](../reference/gs/).

## The resident service

`gs serve` runs a local process that does three things the CLI cannot:

- **Sequencing under contention.** Concurrent appends are compare-and-swap
  on the git ref and retry, so several actors can write at once.
- **Presence and ephemeral conversation** — the amnesiac nexus. This
  includes bounded per-session addressed inboxes and acknowledgements. The
  service resolves Workroom names to fingerprints before preparing the exact
  message bytes. The actor signs outside `host/live`; the runtime verifies the
  signature, orders the frame, and retains the conversation for every current
  matching lease. Only leases that registered the inbox protocol receive
  pending inbox references. This state is per-process and does not survive.
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
and otherwise emits only checkout basenames, branch and HEAD, explicit
clean, dirty, detached, bare, locked, prunable or unavailable state, and
that repository's own remote when there is one safe to link. Naming the
served path is safe because the service binds loopback addresses only:
whoever is reading the page is already on the host it names.

The remote is what lets the browser turn the path into a link to where
the repository lives. It is read from the repository's own configuration
only, bounded in size and count, and admitted by an allowlist: `http` and
`https` only, and never a URL carrying userinfo, a query or a fragment,
any of which can carry a credential. Anything refused is absent from the
response, and the page shows the path as plain text. See
[`gs serve`](../reference/gs/serve.md).

## Choosing a path in

| You are | Start with |
|---|---|
| A person at a terminal | `gs` |
| An agent in an MCP client | `gitseq-mcp`, plus [`SKILL.md`](../../SKILL.md) |
| An auditor with a clone | `gs attach`, then `gs verify` |
| Watching work in progress | the browser view |
