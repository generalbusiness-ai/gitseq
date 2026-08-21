---
title: gs serve
summary: Run the resident service: sequencing, presence, change notification, and the browser view.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:25101623b92c3e17c4634c6a6e2dc5c48ab7abbe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:209b923336260e75192deb934037c8a4c6fcb64e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7802fc152c5d66eae7f651783d24fab7ae477605
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:507fc7fe7ef7b5f998311bce5786b03d39d573ac
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:48bd5acfe51abd4146197a48b0f7674f5676cc5c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bc5ca55fb4a4e67e2395903519f2103a92930268
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fd0680effdbc154f7f17a8f801bed602f20e3717
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:05dccd875ac20804b78e3de4dcf80dbe25835a44
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3991ed3d5f102a963671e45cfb1fa5aef0d3d5fd
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
| `--otel-endpoint` | empty | An OTLP/HTTP collector URL to send traces and metrics to. Empty disables observation entirely; nothing is collected and no exporter is started. Gitseq never discovers a collector on its own. |
| `--profile-listen` | empty | A second loopback address serving Go pprof endpoints. Empty starts no profiler. |

## Observation

Both observation flags are off by default, and off means absent rather
than quiet: with no `--otel-endpoint` the service records no
measurements, so there is no sampling decision to explain and no
collector to trust.

Given an endpoint, `serve` reports how long its own operations take and
how much work they covered, labelled with a closed vocabulary — the
operation, a coarse path such as `cache` or `cold`, and an outcome such
as `ok` or `timeout`. HTTP measurements carry the registered route
template, never the request path, so an identifier in a URL does not
become a metric label. Go runtime metrics are included.

`--profile-listen` is separate on purpose. Profiling is a debugging
session, not steady-state observation, so it binds its own loopback
address and stops when you stop passing the flag. The address is checked
to be loopback and refused otherwise, as `--listen` is. Treat anyone who
can reach that port as able to read the process's memory profile.

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

`serve` validates the acknowledgement and listener first, binds, then
publishes the address it actually bound inside the repository. It prints
`gitseq workroom http://…` and the trusted-process boundary to standard error.
The full and summary resident status responses repeat that boundary as
`trust_boundary`, and the browser displays it before actor selection.
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

The refusal is by design. `--listen` resolves its host and accepts it only
when every returned address is loopback. Each mutation also checks its HTTP
`Host` before routing, then applies the same-origin, fetch-site and JSON
content-type guards before it decodes input or changes state. There is no
permissive CORS route.

The service is a trusted local custodian for several actors: it holds their
signing keys and signs on behalf of whichever trusted process asks. Its
posture is trusted processes only: every process inside this resident boundary
can act as every actor key this application can open. Starting the service is
the decision to accept that boundary, and it prints the same sentence next to
its address on every successful start. Loopback binding limits who can reach
the service; it is not a shared-host authentication system, and it does not
separate the actors from one another inside the boundary.

When a browser tab or MCP adapter first joins, the resident mints a private
credential from 256 bits of system randomness and binds it to exactly that
repository and actor. The client cannot choose it. Renewals, speech, durable
acts, inbox operations and departure require it. Expiry, departure,
revocation or resident restart invalidates it; an adapter reconnecting after a
restart receives a new one. Presence and the change stream expose only a
separate random `session:` handle for display. The handle grants nothing and
cannot be substituted for the credential.

Credentials stay in client process memory. They are absent from status,
presence, MCP tool results, logs, diagnostics, durable events, URL paths,
queries, referrers and returned URL errors. Departure sends the credential in
a JSON body to a fixed route.

These measures stop accidental disclosure and cross-session replay. They do
not protect against a malicious process running as the same OS account: that
process can reach the loopback port and can often read the repository and its
actor keys directly or invoke local `gs` commands. Such a process can obtain
authentic actor signatures. The fold still judges the resulting acts and may
rule them ineffective, but cryptography cannot recover the operator's intent.

## Connection limits

Loopback does not make a stalled client harmless. Both the resident listener
and the optional profiler allow five seconds for request headers, ten seconds
for the complete request, forty seconds for a response, and sixty seconds for
an idle connection. Request headers are capped at 64 KiB. Resident JSON
decoding also stops after 2 MiB. The forty-second response window includes the
resident's bounded long poll, whose accepted timeout is at most thirty
seconds.

## One per repository

Exactly one runs, and serving enforces it. Two services on different ports
against the same repository is the case being prevented: the durable log
stays correct, because appends are compare-and-swap on the git ref and
retry, but presence and conversation are per-process, so the two would form
separate rooms whose participants never see each other and are never told.

Ownership is a claim at `refs/gitseq/resident/<genesis>`, taken with a git
ref update carrying the expected old value. Serving binds the listener
first, so the claim carries the address actually served including a port
the kernel chose, then contests ownership, and only then serves. Binding is
not what authorizes serving; holding the claim is. A start that does not
win the claim closes its listener and exits non-zero without answering
anything.

The claim is a shared ref in the repository's common directory, so path
aliases, symlinks and linked worktrees all reach the same one, and separate
repositories never contend.

A normal stop releases the claim. A killed process leaves it behind, and
the next start recovers it: it asks the claimed address whether it is still
serving, and takes the claim only when nothing is listening. Every other
answer leaves the claim alone and refuses.

The advertisement at `.git/gitseq/resident.json` remains what clients read
to find the address. It is metadata, written only by the process that holds
the claim, and it confers no ownership of its own.

## Refusals

Every hostname in `--listen`, including `localhost`, is resolved through the
host resolver. Resolution must succeed, return at least one address, and
return only loopback addresses. A host that fails to resolve, or resolves to
both loopback and non-loopback addresses, is refused.

| Situation | Message |
|---|---|
| A literal non-loopback `--listen` | `--listen must name a loopback address; the resident service is a trusted local multi-actor custodian` |
| A `--listen` hostname fails to resolve or any result is non-loopback | `--listen must resolve only to loopback addresses; the resident service is a trusted local multi-actor custodian` |
| A read-only attachment | `cannot serve a read-only attachment` |
| The port is taken | The bind error, before anything is claimed, published or announced. |
| Another service holds the repository | `refusing to serve: another service already holds this repository's workroom and is answering (<url>)` |
| The holder cannot be shown to be gone | `refusing to serve: the service holding this repository could not be shown to be gone, so its claim is left alone (<url>); if you are certain no service is running, remove the claim with git update-ref -d refs/gitseq/resident/<genesis>` |
| The claim cannot be read | `refusing to serve: the ownership claim at refs/gitseq/resident/<genesis> (<object>) cannot be read as a claim: <reason>; …` — a damaged claim is never treated as a vacancy. |

## Restart

The resident and ordinary local `gs` commands share an application-owned
checkpoint selector at `.git/gitseq/checkpoints/<genesis>.json`. It names a
signed checkpoint object and is refreshed every 256 accepted events after the
last successful write, so a new process re-audits only the tail. The ref
`refs/gitseq/checkpoints/<genesis>` points to the same object, keeps it reachable
to `git gc`, and repairs a missing or damaged local selector. The selector also
lets gitseq recover if that local ref was unavailable or rewritten. Neither
selector is trusted. The checkpoint is signed by the sequencer key current at its head,
and any key rotation inside the cached prefix is re-read from its own
sequence commit and checked under the preceding key, so a rotated log
still restarts from cache. A missing or mismatched checkpoint is only a
cache miss: it does the full audit instead. Checkpoint refs are local,
never fetched by `attach`, never published, and never consulted by
[`gs verify`](verify.md).

For a deliberate cold restart, stop the resident and run
`gs checkpoint-clear --repo <path>` before starting it again. This clears both
persistent selectors. `GITSEQ_CHECKPOINT=off gs serve ...` keeps checkpoint
loading and publication disabled for that resident process.

Expired presence leases are swept, so a session that goes away without
departing does not linger in presence forever.

## Local worktrees

The browser view served by a resident also reports local checkout state.
It names the served checkout's own absolute path, so a reader can tell
which repository the page is showing, and otherwise emits only checkout
basenames, branch and HEAD, and explicit clean, dirty, detached, bare,
locked, prunable or unavailable state. Disclosing the served path is safe
because [`gs serve`](serve.md) refuses any listen address that is not
loopback: whoever is reading the page is already on the host it names.

None of that is part of the durable projection. A checkout associated
with a commitment only through a commit's `Rests-On:` trailer is marked
**local** in the view, and that marking is the whole of the claim: a
trailer is ordinary commit text, not an actor-signed statement, so the
association is local evidence and nothing the log will vouch for.

## See also

- [Deploy a resident](../../how-to/deploy-a-resident.md)
- [Components](../../concepts/components.md)
