---
title: MCP whoami
summary: Show the configured durable actor and selected workroom without disclosing the resident credential.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:68fce5ef1c832368d54c3de12bc37471afbda627
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:092bd6cbe60056cd85ba626ea22807c1e97cc376
---

# `whoami`

Reports which actor this call selected and what the roster currently says
about that actor in the selected workroom.

Call it first in a new session and after changing a selector. Durable acts are
signed as the selected actor, permanently.

## Arguments

| argument | required | meaning |
|---|---|---|
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |
| `agent` | optional | The actor whose existing accessible key this call selects; defaults to startup `--actor`. |

Every tool takes both selectors. `repo` chooses a workroom and `agent` chooses
an existing accessible signing key in it. A selector never creates a key or
grants identity. An unavailable repository, inaccessible key, or actor absent
from the effective roster refuses instead of falling back to the startup
defaults. Linked worktrees of one repository are one workroom, not several.

This is a custody boundary only when keys are access-gated secrets. The current
development key model derives keys from actor names, so it does not yet enforce
that separation; the selector must not be mistaken for a new trust grant.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
call() { printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s,%s}}\n' "$1" "$2" "$META"; }

call whoami '{}' | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## What comes back

| Field | Meaning |
|---|---|
| `actor` | The configured identity: name and fingerprint only. The key path is never returned. |
| `repo` | The repository this call acted in, as its git common directory. |
| `genesis` | The genesis hash of that repository's workroom. |
| `durable` | What the roster says now: name, fingerprint, kind, membership event, and capped roles. Which of these the adapter checks itself is stated [below](#what-the-adapter-checks-and-what-it-repeats). |
| `frontier` | The exact durable frontier the answer is anchored to: genesis, head, depth. |
| `source` | The path that produced the answer: the resident's bounded orientation, or one of the locally verified fallbacks. |
| `degraded` | `true` when the resident could not be used and a verified local fallback answered. |
| `protocol` | The protocol version the adapter serves. |

`repo` and `genesis` are worth reading before you act. One adapter serves
whatever repository a call names, so they are the answer to "which
workroom am I about to speak in".

`actor` is local and `durable` is the record. They can disagree: a
repository can hold a key for a principal whose membership has been
retired. Prefer `durable` when they disagree. Neither field grants
authority: see [below](#what-the-adapter-checks-and-what-it-repeats).

The answer is anchored to an exact durable frontier. A current loopback
resident is labeled `resident_statusview_current`; the client refuses
redirects and guards the answer with a two-second, 64 KiB, strict-JSON
boundary plus matching local genesis and head checks. When the resident
cannot be used, a local fallback sets `degraded: true` and names the
verified path it actually took: `verified_signed_checkpoint_tail`,
`verified_incremental_tail`, or `verified_cold_full_audit`. The response
never includes the local actor key path or the resident-minted credential.
That credential is private adapter state, scoped to one repository and actor,
and is replaced after resident restart. No MCP tool returns it.

## What the adapter checks and what it repeats

When `degraded` is `false`, `durable` came from the resident. The adapter
fetched it during this call, over loopback, and checked these things against
its own copy of the durable log before answering:

- `frontier.genesis` and `frontier.head` equal the workroom's genesis and the
  head the adapter read immediately before and after the fetch, so the answer
  describes the frontier the adapter can see, and the head did not move while
  the resident was consulted;
- the projection version is the one this adapter understands;
- `durable.fingerprint` is the selected actor's fingerprint;
- `durable.name`, `durable.kind` and `durable.membership_event` are present,
  `durable.roles` names `participant` and holds at most twenty entries, the
  skipped-roles count is not negative, and `frontier.depth` is not negative;
- the response arrived within two seconds, under 64 KiB, as strict JSON with
  no unknown fields, and without following a redirect.

Everything else in `durable` is repeated from the resident, not re-derived
from the log: the text of `name` and `kind`, which membership event is named,
which roles appear beside `participant`, the skipped-roles count, and
`frontier.depth`. A process on this account that answers on the resident's
listen address can supply any contents that pass the checks above, and the
answer still says `degraded: false` with `source:
resident_statusview_current`. That is the documented
[host posture](../architecture.md#host-posture): every process running as
the operator account is trusted to speak for the resident, and the adapter
does not add verification of the resident's contents on top of that trust.

When `degraded` is `true`, the adapter built `durable` itself from a locally
verified snapshot of the log, so every field is derived from verified
evidence. The `source` label names the verification path taken. The
fallback verifies more about the contents than the resident path does; the
resident path verifies that the answer is anchored to the frontier the
adapter can see.

On neither path does the answer confer authority. `durable` is a description
of the roster for orientation. Every durable act you file is signed with the
selected key and judged again at sequencing against the signed log, so a
name or role displayed here cannot grant what the log does not record.

## See also

- [`gs actors`](../gs/actors.md)
- [Actors and authority](../../concepts/actors.md)
