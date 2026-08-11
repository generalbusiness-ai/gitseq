---
title: Components
summary: The CLI, the resident service, the MCP adapter, the browser view, and the repository underneath.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d97eed896404f401dae2439928e31c6a0290ed47
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b2edd696b01e4ce953cf31194eb1a3dbb67e9b56
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:30206869d55828c9a4eb7d3c16d3cb71fe0cac8d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a383b4db5b97c20dae3e36463f2e0760904d9204
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d0fd7f5227adc05a6a42883aadd765dad0a89098
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d87af410275ef5dffdd11cdd5b9a2a3b5a62b45
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:63106fd8b893add378c21856065812b9f130f8a1
---

# Components

## The repository underneath

The workroom is an overlay on an ordinary git repository.

| Where | What |
|---|---|
| `refs/seq/<genesis>` | The sequence. One commit per durable event. |
| `.git/gitseq/` | Local configuration and actor keys. Not shared. |
| `refs/gitseq/checkpoints/<genesis>` | A resident's local restart cache. Never published. |

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

The resident serves the committed build, not `ui/src`, so the two can
drift. `make ui-check` rebuilds and fails if the committed files differ.

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
  routes them to live sessions that registered the inbox protocol. This state
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

The resident serves a live projection at its listen address: the Work
board, the event railway, actors, and the artifacts with their staleness
marks.

It also reads local worktree state, which is *not* part of the durable
projection. That endpoint names the served checkout's own absolute path,
so a reader can tell which repository the page is showing, and otherwise
emits only checkout basenames, branch and HEAD, and explicit clean,
dirty, detached, bare, locked, prunable or unavailable state. Naming the
served path is safe because the service binds loopback addresses only:
whoever is reading the page is already on the host it names. The railway
is a newest-80 window and says so when it is truncated, so an older
association can be absent without that meaning anything.

## Choosing a path in

| You are | Start with |
|---|---|
| A person at a terminal | `gs` |
| An agent in an MCP client | `gitseq-mcp`, plus [`SKILL.md`](../../SKILL.md) |
| An auditor with a clone | `gs attach`, then `gs verify` |
| Watching work in progress | the browser view |
