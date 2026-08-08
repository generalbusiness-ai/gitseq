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

Genesis pins exactly one canonical `ssh-ed25519` sequencer public key: the key
type, one ASCII space, and the base64 wire key, with no options, principals,
comments, or additional lines. Creation and auditor decoding apply the same
validation before the value can become an OpenSSH allowed-signers entry.

### Speaking

| Command | Purpose |
|---|---|
| `gs state --as <actor> --kind <kind> --text <text>` | Append a durable utterance. |
| `gs ratify --as <actor> <event>` | Confer force, if you hold the authority for that target. |
| `gs supersede --as <actor> --text <reason> <event>` | Retire an act and propagate staleness. |

`state` also takes `--rests-on <event>`, `--body key=value`, and
`--evidence name=path`, each repeatable, plus `--idempotency-key` for
safe retries.

Every actor-controlled string in the signed intent is limited to 32 KiB, and
one intent may cite at most 4,096 causal references. The complete signed commit
envelope plus inline payload and attachment contents must fit the workroom's
genesis `payload_ceiling`; oversize input is refused before the sequence ref
moves. Readers enforce the same envelope ceiling explicitly, so the write path
cannot admit a commit that later fails only because of a parser line limit.

For implementation requests, promises, and reports, `body.branch` is an
optional branch hint and `body.head` (or `body.commit`) is an optional exact
ordinary-Git head hint. The Work drawer uses these signed durable fields only
to associate local checkouts with a commitment; they do not claim the checkout
is clean or current. An `artifact` remains the durable statement that points
to implementation truth as `path@commit`.

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

Resident snapshots are immutable borrowed views. In-process consumers may
receive maps and slices owned by the workspace cache and must not mutate them;
JSON and MCP adapters only serialize those values.

The browser's Work drawer also reads local worktree state. That endpoint emits
only checkout basenames, branch/HEAD, and explicit clean, dirty, detached,
bare, locked, prunable, or unavailable state; it never enters the durable
projection. A checkout associated only through an ordinary commit's
`Rests-On:` trailer is visibly marked **unverified trailer**, because trailer
text is not an actor-signed workroom statement. The railway is a newest-80
window and says when it is truncated, so older trailer associations may be
absent without being mistaken for durable evidence.

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

The resident may also maintain a local
`refs/gitseq/checkpoints/<genesis>` ref. Its parentless commit contains the
original actor-signed events at one fully audited sequence head and is signed
by that log's sequencer key. On restart, gitseq checks the checkpoint's object
format, genesis, exact head, fold-profile version, and commit signature. One
local first-parent metadata enumeration then proves the exact commit sequence
from genesis through the named head. Every cached event must occupy its claimed
commit and match that commit's actor envelope, causal trailers, and tree; its
actor signature, payload ceiling, dedup key, and payload-tree bytes are checked
again. Only events after the checkpoint frontier require sequencer-signature
and payload-object reads. A missing, malformed, mismatched, oversized, or
non-descendant checkpoint is only a cache miss: gitseq performs the ordinary
full audit and, when it holds sequencer custody, replaces the checkpoint.

A writing resident refreshes the ref every 256 accepted events after its last
successful write. A failed write does not advance that cadence and is retried
on the next accepted event. Consequently a successful checkpoint leaves at
most 255 sequence commits for full delta verification, but persistent storage
or signing failures can make the tail larger. Each write serializes the whole
cached event prefix; the canonical JSON blob is capped at 256 MiB. Restart is
therefore linear in total history for the local metadata proof and linear in
the tail for expensive commit-signature and payload reads. Speedup depends on
both depth and tail and has no fixed multiplier. On an Apple M5 Max, the
checked-in `BenchmarkCheckpointRestartAtDepth1000` measured a 768-event
checkpoint plus 232-event tail at 20.29 seconds and 36.1 MB allocated, versus
75.31 seconds and 99.7 MB for a cold audit: 3.71x for that exact depth and
tail. Reproduce it with `go test ./internal/kernel -run '^$' -bench
'^BenchmarkCheckpointRestartAtDepth1000$' -benchtime=1x -count=1` from
`spike/`. `gs verify` remains an explicit full audit and never consults this
resident cache. Checkpoint refs are local implementation artifacts; `attach`
does not fetch them and the documented sequence push does not publish them.

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
