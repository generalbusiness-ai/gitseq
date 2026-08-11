---
title: gs serve
summary: Run the resident service: sequencing, presence, change notification, and the browser view.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:404f16bcf0df9bf1052cba27800143ef29a9a57d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:845e3cc9af4e2a888ceece1a72d5b31d9ba72fe1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9999be33277b1a209e3366ef9f9d6c6075f069a0
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
| `--listen` | `127.0.0.1:7777` | A loopback address to bind. Port `0` takes any free port. |

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

## Publishing the address

`serve` binds first, then publishes the address it actually bound inside
the repository, then prints `gitseq workroom http://…` to standard error.
So the banner names a port that is really open, a failed start announces
nothing, and `--listen 127.0.0.1:0` is usable: the kernel picks the port
and clients read it from the repository rather than being told it. That
is what you want when several repositories are served at once.

The advertisement carries the genesis of the workroom being served, and a
client refuses one whose genesis does not match, so an act can never be
posted to a service holding a different log.

Publication is **not a lock**. The last service to start wins the
advertisement, which at least pulls new clients into one room. Stopping
withdraws it, unless a later service has taken it over — that service is
still serving, and removing its record would send clients into degraded
mode for nothing.

Interrupt and terminate both count as being told to stop, so Ctrl-C and
an ordinary supervisor shutdown both withdraw the advertisement and both
exit reporting success. Only a hard kill leaves a record behind, and that
costs a client one refused connection before it acts locally instead.

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

Run exactly one. Nothing enforces it — there is no lock, and publishing
the address is not one. Two services on different ports against
the same repository is the case to avoid: the durable log stays correct,
because appends are compare-and-swap on the git ref and retry, but
presence and conversation are per-process, so the two form separate rooms
whose participants never see each other and are never told.

## Refusals

| Situation | Message |
|---|---|
| A non-loopback `--listen` | `--listen must name a loopback address; the resident service is a trusted local multi-actor custodian` |
| A read-only attachment | `cannot serve a read-only attachment` |
| The port is taken | The bind error, before anything is published or announced. |

## Restart

The resident keeps a signed checkpoint under
`refs/gitseq/checkpoints/<genesis>` and refreshes it every 256 accepted
events after its last successful write, so restart re-audits only the
tail. The checkpoint is signed by the sequencer key current at its head,
and any key rotation inside the cached prefix is re-read from its own
sequence commit and checked under the preceding key, so a rotated log
still restarts from cache. A missing or mismatched checkpoint is only a
cache miss: it does the full audit instead. Checkpoint refs are local,
never fetched by `attach`, never published, and never consulted by
[`gs verify`](verify.md).

Expired presence leases are swept, so a session that goes away without
departing does not linger in presence forever.

## See also

- [Deploy a resident](../../how-to/deploy-a-resident.md)
- [Components](../../concepts/components.md)
