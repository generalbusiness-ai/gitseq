# Reference

Commands, tools, and identifiers. For the ordered walkthrough, start
with [Getting started](getting-started.md).

## Event identifiers

Every durable act has one canonical identifier:

```
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

This is the form used by `--rests-on`, by `ratify` and `supersede`
targets, by `provenance`, and by `Rests-On:` commit trailers. Always copy
it whole, from the emitted event rather than from a display that
abbreviates it.

What happens to a citation that resolves to nothing depends on where it
sits. The fold enforces the commitment chain: a `promise` naming a
request that does not exist is judged ineffective as a dangling promise,
and a `report` resting on that promise is ineffective in turn, so an
unearned approval cannot carry force. It does **not** enforce evidential
`rests_on`: a dangling basis on an `assert` or `artifact` is recorded as
effective. That division is deliberate — the chain is machinery the fold
owns, while `rests_on` is a claim about meaning, and a substrate with no
ontology cannot check it. The practical consequence is that a mistyped
citation on a claim is accepted silently, while the same mistake on a
commitment is caught.

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

## MCP

```sh
gitseq-mcp --repo <path> --actor <name> --server http://127.0.0.1:7777
```

One process per client session, one actor per process. The adapter signs
every act as that actor and holds a leased, session-bound presence.

**Protocol era.** The adapter is dual-era: it serves the stateless
`2026-07-28` shape and the `initialize` handshake of `2025-11-25` and
earlier. Era is a property of the connection, selected by how the client
opens and settled once.

| Client opens with | Adapter answers |
|---|---|
| per-request `_meta` at `2026-07-28` | modern envelope with `resultType` and cache directives; `server/discover` reports `supportedVersions: ["2026-07-28"]` |
| per-request `_meta` at a version it does not serve | `-32022` with `supported` and `requested`, so the client can retry |
| `initialize` naming a revision it speaks | that same revision, echoed |
| `initialize` naming one it does not | `2025-11-25`, which the client may refuse |
| `initialize` missing `protocolVersion`, `capabilities`, or `clientInfo.name`/`.version` | `-32602`; the era stays undetermined and the client may open again |

Once settled, the era does not move. `initialize` after modern traffic is
refused, a second `initialize` is refused rather than renegotiating the
version mid-stream, and `server/discover` is unavailable on a legacy
connection since that revision cannot interpret its reply. A refused
opening never disturbs a session that is already working, and legacy
results carry neither the modern envelope nor its cache directives.

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
