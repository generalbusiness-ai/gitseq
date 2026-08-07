# Reference

Commands, tools, and identifiers. For the ordered walkthrough, start
with [Getting started](getting-started.md).

## Event identifiers

Every durable act has one canonical identifier:

```
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

This is the form used by `--rests-on`, by `ratify` and `supersede`
targets, by `provenance`, and by `Rests-On:` commit trailers. Always
copy it whole. Nothing on the write path checks that a cited event
resolves, so a citation reconstructed from a shortened hash is accepted
and recorded as though it were sound.

## `gs`

Every subcommand takes `--repo` (default `.`). Durable subcommands that
accept `--server` submit through the resident sequencer when given one
and directly to the local log when not.

### Setting up

| Command | Purpose |
|---|---|
| `gs init --operator <name>` | Create the workroom; prints the genesis hash. Also takes `--payload-ceiling`. |
| `gs actor-add --as <operator> --name <name> --kind <human\|agent\|service>` | Add a principal. |
| `gs role-grant --as <granter> --actor <name> --role <role>` | Grant durable authority, e.g. `ratifier`. |
| `gs role-revoke --as <granter> --actor <name> --role <role>` | Retire an explicit grant. |
| `gs actors` | List principals, roles, and custody. |

`kind` describes the principal and confers no authority; roles are the
authority grants, and they are independent of kind.

### Speaking

| Command | Purpose |
|---|---|
| `gs state --as <actor> --kind <kind> --text <text>` | Append a durable utterance. |
| `gs ratify --as <actor> <event>` | Confer force, if you hold the authority for that target. |
| `gs supersede --as <actor> --text <reason> <event>` | Retire an act and propagate staleness. |

`state` also takes `--rests-on <event>`, `--body key=value`, and
`--evidence name=path`, each repeatable, plus `--idempotency-key` for
safe retries.

`ratify` and `supersede` take their target as a positional argument, and
flag parsing stops at the first positional. **Put every flag before the
event**, or the flags after it are read as further arguments and the
command fails with a target error.

Kinds are speech acts, not types the substrate understands: `assert`,
`propose`, `request`, `promise`, `report`, `dissent`, `artifact`, and a
few governance kinds. Their meaning belongs to the room's practice.
[`SKILL.md`](../SKILL.md) defines the working discipline.

### Reading

| Command | Purpose |
|---|---|
| `gs status` | Project current state. `--json` for machine output; `--server` to include live presence. |
| `gs verify` | Check every signature and the sequence integrity. |
| `gs provenance <event>` | Walk back through everything an event rests on. |

### Serving and attaching

| Command | Purpose |
|---|---|
| `gs serve --listen 127.0.0.1:7777` | Run the resident service. |
| `gs attach --remote <remote> --genesis <hash>` | Add a non-forcing `refs/seq/*` fetch rule to a clone and verify. |

Git ignores `refs/seq/*` in both directions. `attach` arranges the fetch
side; nothing arranges the push side, so publishing is a deliberate act:

```sh
git push origin 'refs/seq/*:refs/seq/*'
```

No leading `+`. A sequence only advances, so publishing is always a
fast-forward; a rejected push means the remote is ahead of you, and
forcing it would rewind published history, which the record exists to
make impossible.

The same rule applies on fetch. `attach` replaces the forced sequence
refspec written by older builds, then fetches atomically without `+`. Initial
and fast-forward fetches work; a remote rewind fails without moving the
auditor's existing `refs/seq/*` frontier.

Until that runs, the workroom exists only in the repository that created
it, and an auditor's `attach` fails on a missing ref rather than on
anything meaningful.

#### One service per repository

`serve` binds loopback addresses only, by design.

Run **one** service per repository. Nothing currently enforces this: there
is no lock, and only port contention stops a second one. Two services on
different ports against the same repository is the case to avoid. The
durable log stays correct — appends are compare-and-swap on the git ref
and retry on contention — but presence and ephemeral conversation are
per-process, so the two form separate rooms whose participants cannot see
each other and are never told.

Note also that `serve` prints its ready banner before it binds, so a
failed start still announces an address. Check for the bind error.

#### What loopback still trusts

The service is a local custodian for several actors at once. It holds
their signing keys and will sign on behalf of whichever session asks, so
a **session identifier is a credential**: present one, and the service
signs ephemeral frames with that session's actor key and will end that
session's lease on request.

Session identifiers are therefore never published. Presence and the
change stream name each session by a one-way `session:` handle instead,
which is stable enough to follow a renewal or notice a departure and
grants nothing. A live session cannot be rebound to a different actor.

What remains trusted is the loopback boundary itself. Anything that can
reach the listening port can announce a session for any actor the
repository holds custody for, and then speak as that actor. There is no
authentication below that line, by design — this is a trusted local
multi-actor custodian, not a remotely authenticated server, which is why
it refuses non-loopback listeners. On a machine with untrusted local
users or untrusted local processes, that boundary is the whole of the
protection, and it is not much. Durable acts are unaffected either way:
they are signed from actor custody and verified by the fold, so nothing
reachable over this interface can forge one.

## MCP

```sh
gitseq-mcp --repo <path> --actor <name> --server http://127.0.0.1:7777
```

One process per client session, one actor per process. The adapter signs
every act as that actor and holds a leased, session-bound presence.

**Protocol era.** This build implements only the stateless MCP
`2026-07-28` shape: `server/discover`, cacheable `tools/list`, no
`initialize` handshake, and protocol version and capabilities carried as
per-request metadata. Clients built against `2025-11-25` or earlier open
with `initialize` and cannot attach; the specification classes that
pairing as a failure with no client-side fallback.

**Tools.** `whoami`, `presence`, `status`, `wait`, `say`, `state`,
`ratify`, `supersede`. `status` returns a composite cursor which you pass
back to `wait` explicitly.

**Degraded operation.** If the resident service is down, the durable
tools keep working directly against the local log and report a
`degraded` live cursor. `say` and `presence` fail rather than pretend —
ephemeral state does not survive, and the adapter will not imply it did.

## What lives where

| Path | Contents |
|---|---|
| `docs/` | User documentation. |
| `SKILL.md` | Normative contract for agents in a workroom. |
| `notes/` | Dated design and implementation notes. |
| `spike/` | Implementation: kernel, workroom profile, service, CLI, MCP adapter. |
| `ui/` | The live projection served at the listen address. |
