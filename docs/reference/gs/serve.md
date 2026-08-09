---
title: gs serve
summary: Run the resident service: sequencing, presence, change notification, and the browser view.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:75eab177526d3a45b70df77ac650932e1203a79f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2fa5182bb85a8347c55bcf229d53b104dde600a7
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a9d3606442131e4bc700d1310451657bd4eac438
---

# `gs serve`

Runs the local service. It sequences concurrent appends, holds presence
and ephemeral conversation, notifies readers when the record moves, and
serves the browser view at its listen address.

It runs in the foreground until stopped.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--listen` | `127.0.0.1:7777` | A loopback address to bind. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null

PORT="${PORT:-7777}"
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT

for _ in $(seq 40); do
  gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null 2>&1 && break
  sleep 0.25
done
gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null
kill "$SERVER"
trap - EXIT
```

## The banner is not readiness

`serve` prints `gitseq workroom http://…` to standard error **before** it
binds, so a failed start still announces an address. Check for the bind
error, and poll for a real answer as above rather than trusting the
banner.

## Loopback only

```sh
! gs serve --repo "$REPO" --listen 0.0.0.0:9999
```

The refusal is by design. The service is a trusted local custodian for
several actors: it holds their signing keys and signs on behalf of
whichever session asks.

That makes a **session identifier a credential**. Present one and the
service signs with that session's actor key — ephemeral frames through
`/v0/say` and durable events through `/v0/act` — and will end that
session's lease on request. Session identifiers are therefore never
published; presence and the change stream name sessions by opaque minted
`session:` handles instead, and a live session cannot be rebound to
another actor.

Anything that can reach the port can act as any actor this repository
holds custody for. What that buys an attacker is authentic authorship,
not automatic force: the fold still judges the act on its merits and can
rule it ineffective. On a machine with untrusted local users, that
boundary is the whole of the protection.

## One per repository

Run exactly one. Nothing enforces it — there is no lock, and only port
contention stops a second one. Two services on different ports against
the same repository is the case to avoid: the durable log stays correct,
because appends are compare-and-swap on the git ref and retry, but
presence and conversation are per-process, so the two form separate rooms
whose participants never see each other and are never told.

## Refusals

| Situation | Message |
|---|---|
| A non-loopback `--listen` | `--listen must name a loopback address; the resident service is a trusted local multi-actor custodian` |
| A read-only attachment | `cannot serve a read-only attachment` |
| The port is taken | The bind error, after the banner has already printed. |

## Restart

The resident keeps a signed checkpoint under
`refs/gitseq/checkpoints/<genesis>` and refreshes it every 256 accepted
events after its last successful write, so restart re-audits only the
tail. A missing or mismatched checkpoint is only a cache miss: it does
the full audit instead. Checkpoint refs are local, never fetched by
`attach`, never published, and never consulted by
[`gs verify`](verify.md).

Expired presence leases are swept, so a session that goes away without
departing does not linger in presence forever.

## See also

- [Deploy a resident](../../how-to/deploy-a-resident.md)
- [Components](../../concepts/components.md)
