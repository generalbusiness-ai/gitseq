---
title: gs serve
summary: Run the resident service: sequencing, presence, change notification, and the browser view.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1a5bb9becc97d3ae601879a02b19923a2194811e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
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
| `--otel-endpoint` | empty | An OTLP/HTTP collector URL to send traces and metrics to. Empty disables observation entirely; the service collects nothing and starts no exporter. Gitseq never discovers a collector on its own. |
| `--profile-listen` | empty | A second loopback address serving Go pprof endpoints. Empty starts no profiler. |

Gitseq removed `--acknowledge-trusted-processes`; a script still passing it
now fails at flag parsing and should drop it.

## Observation

Both observation flags default to off, and off means absent rather
than quiet: with no `--otel-endpoint` the service records no
measurements, so no sampling decision needs explaining and no
collector needs trusting.

Given an endpoint, `serve` reports how long its own operations take and
how much work they covered, labelled with a closed vocabulary — the
operation, a coarse path such as `cache` or `cold`, and an outcome such
as `ok` or `timeout`. HTTP measurements carry the registered route
template, never the request path, so an identifier in a URL does not
become a metric label. The measurements include Go runtime metrics.

`--profile-listen` stands apart on purpose. Profiling serves a debugging
session, not steady-state observation, so it binds its own loopback
address and stops when you stop passing the flag. `serve` checks that the
address names loopback and refuses it otherwise, as it does for `--listen`.
Treat anyone who can reach that port as able to read the process's memory
profile.

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

`serve` validates the listener first, binds, then publishes the address it
actually bound inside the repository. It prints `gitseq workroom http://…`
and the trusted-process boundary to standard error.
The full and summary resident status responses repeat that boundary as
`trust_boundary`, and the browser displays it before actor selection.
The full status and the rebuild report also carry `profile`, the fold
profile this process interprets with; a page that kept a status across a
restart keeps it only while both name the same profile.
So the banner names a port that really stands open, a failed start announces
nothing, and `--listen 127.0.0.1:0` works: the kernel picks the port
and clients read it from the repository rather than hearing it from the
service. You want that when you serve several repositories at once.

The advertisement carries the genesis of the served workroom, and
`gs` refuses one whose genesis does not match rather than reading it as no
resident at all, so `gs` can never post an act to a service holding a
different log and can never quietly fold it here instead.

Publication does **not act as a lock**. The last service to start wins the
advertisement, which at least pulls new clients into one room. Stopping
withdraws it, unless a later service has taken it over — that service
still serves, and removing its record would send clients into degraded
mode for nothing.

Interrupt and terminate both count as an instruction to stop, so Ctrl-C and
an ordinary supervisor shutdown both withdraw the advertisement and both
exit reporting success. Only a hard kill leaves a record behind, and that
record still reads as a good one: the service has gone, not the
record. So it costs a client one refused connection, and the two surfaces
answer that differently. Reads fall back to the verified local fold as
before. `gs` refuses the durable act and names the way out, either starting
the resident again or passing `--server -`. `cmd/gitseq-mcp` folds the act
into the local fold and marks the result `degraded`, as it does
for any resident that stops answering.

Two limits deserve naming now that `gs` uses the advertisement by
default. A resident that accepts an act and then stalls past the client
deadline leaves the outcome unknown, and a retry mints a fresh idempotency
key that can append twice; that held before, but only for somebody who
deliberately passed `--server`. And a published record that gitseq cannot
trust stops durable acts in the repository until somebody repairs or
removes it: `gs` refuses the command, and `cmd/gitseq-mcp` refuses the one
call, leaving its attachment and its session intact so the repair alone
completes the recovery. Both fail closed on purpose. The one way past it:
ask for the local fold deliberately with `gs --server -`, which reads no
record at all. Neither surface refuses a read: a read answers through the
resident when one answers and from the verified local fold when none does.

Gitseq refuses to trust a record that it cannot read, that exceeds the
8 KiB limit on a record, that does not parse as a record at all, that
carries no address, that names another workroom, or that carries an
address other than a bare `http` loopback origin.
`internal/residentclient` owns the clause naming which of those applies,
so the two surfaces cannot drift into separate accounts of the same six
failures; each adds its own way out, the part that honestly
differs. `gs` offers `--server -`. The adapter has no flags of its own, so
it says to repair or remove the record, or to fold that one act locally on
purpose with `gs` and `--server -`. Each refusal happens before the caller
reads a signing key or appends anything.

Both surfaces judge the record on every durable act, not once per session.
An adapter that found a good resident an hour ago still refuses the next act
if someone has rewritten the record since, and when a resident stops
answering it reads the record once more before folding locally. A rewrite
landing mid-call refuses the local fallback only when the adapter cannot
trust that re-read; a record that someone removed, or replaced with one that
still reads and names this workroom, leaves the transport loss an honest
reason to fold locally, marked `degraded`. Only a record missing entirely
counts as absence, and a repository with none acts locally exactly as it did
before residents existed.

## Loopback only

```sh
! gs serve --repo "$REPO" --listen 0.0.0.0:9999
```

The design intends this refusal. `--listen` resolves its host and accepts it
only when every returned address names loopback. Each mutation also checks
its HTTP `Host` before routing, then applies the same-origin, fetch-site and
JSON content-type guards before it decodes input or changes state. No
permissive CORS route exists.

Every response also carries the browser policy stated once in
`internal/service`: a `Content-Security-Policy` that admits only the
service's own origin for scripts, styles, fonts, images and fetches and
denies framing with `frame-ancestors 'none'`, `X-Frame-Options: DENY` for
older agents, `X-Content-Type-Options: nosniff`, and
`Referrer-Policy: no-referrer`. The embedded UI loads under that policy by
construction, so the browser, rather than hope, refuses a page that embeds
the board, a script injected beside it, or a mislabelled asset. The style
rule refuses style attributes and elements written into markup and any
stylesheet from another origin; it does not govern styles a script sets
through the CSSOM, and the UI applies its few inline styles that way.

The service acts as a trusted local custodian for several actors: it holds
their signing keys and signs on behalf of whichever trusted process asks. Its
posture reads "trusted processes only: every process inside this resident
boundary can act as every actor key this application can open." Starting the service
means deciding to accept that boundary, and it prints the same sentence next
to its address on every successful start. Loopback binding limits who can
reach the service; it provides no shared-host authentication system, and it
does not separate the actors from one another inside the boundary.

When a browser tab or MCP adapter first joins, the resident mints a private
credential from 256 bits of system randomness and binds it to exactly that
repository and actor. The client cannot choose it. Renewals, speech, durable
acts, inbox operations and departure require it. Expiry, departure,
revocation or resident restart invalidates it; an adapter reconnecting after a
restart receives a new one. Presence and the change stream expose only a
separate random `session:` handle for display. The handle grants nothing, and
no client can substitute it for the credential.

Credentials stay in client process memory. They never appear in status,
presence, MCP tool results, logs, diagnostics, durable events, URL paths,
queries, referrers or returned URL errors. Departure sends the credential in
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
an idle connection. Both cap request headers at 64 KiB. Resident JSON
decoding also stops after 2 MiB.

The resident's `/v0/status` route forms the one response exception. A cold
status request can start or join a full verified rebuild, whose duration
depends on the durable log and can exceed forty seconds. That route clears
the response deadline so the connection remains attached to the shared
rebuild instead of failing while the same work continues in the resident.
The read, header-size, and idle bounds still apply. Every other resident
route keeps the forty-second response deadline, including the bounded long
poll, whose accepted timeout cannot exceed thirty seconds. Profiler routes
have no exception.

## One per repository

Exactly one runs, and serving enforces it. Serving prevents two services on
different ports against the same repository: the durable log stays correct,
because appends compare-and-swap the git ref and retry, but presence and
conversation live per process, so the two would form separate rooms whose
participants never see each other and never learn of it.

Ownership takes the form of a claim at `refs/gitseq/resident/<genesis>`,
taken with a git ref update carrying the expected old value. Serving first
performs a read-only liveness preflight, so it can name precisely an
incumbent already bound to the requested port. It then binds the listener,
so the claim carries the address actually served including a port the kernel
chose. Startup may spend a preflight proof that the old address refused a
connection only on one compare-and-swap against that exact claim object; if
the ref moved, startup discards the proof and re-reads and probes normally.
The proof authorizes only that one exact CAS attempt; neither it nor the
bound listener authorizes serving. Holding the post-bind claim does. A start
that does not win closes its listener and exits non-zero without answering
anything.

The claim lives as a shared ref in the repository's common directory, so
path aliases, symlinks and linked worktrees all reach the same one, and
separate repositories never contend.

A normal SIGINT or SIGTERM stop closes the listener, releases this process's
exact claim, and withdraws its advertisement before exit. Compare-and-swap
guards claim release, and the process retries it through brief ref-lock
contention; it reports a cleanup failure instead of discarding it. It never
removes a successor's claim.

A crash or SIGKILL can still leave a claim behind. The next start actively
probes the claimed loopback address with a two-second bound. It uses the
resident's small `/v0/identity` status response instead of the full projection,
so a cold durable-log audit cannot make a healthy service look dead. A refused
connection proves that nothing listens: startup replaces the exact stale
claim by compare-and-swap and logs the recovery. A valid response naming this
workroom proves that a service answers; startup refuses and, when the
service returned it, names its PID. Timeouts, malformed responses, a different
workroom, and addresses outside the loopback HTTP boundary count as ambiguous.
They leave the claim alone and refuse.

The advertisement at `.git/gitseq/resident.json` remains what clients read
to find the address. It carries metadata, written only by the process that
holds the claim, and it confers no ownership of its own.

## Refusals

`gs serve` resolves every hostname in `--listen`, including `localhost`,
through the host resolver. Resolution must succeed, return at least one
address, and return only loopback addresses. It refuses a host that fails to
resolve, or resolves to both loopback and non-loopback addresses.

| Situation | Message |
|---|---|
| A literal non-loopback `--listen` | `--listen must name a loopback address; the resident service is a trusted local multi-actor custodian` |
| A `--listen` hostname fails to resolve or any result falls outside loopback | `--listen must resolve only to loopback addresses; the resident service is a trusted local multi-actor custodian` |
| A read-only attachment | `cannot serve a read-only attachment` |
| Something else holds the port | The bind error, before anything gets claimed, published or announced. |
| Another service holds the repository | `refusing to serve: another service already holds this repository's workroom and is answering (<url>, pid <pid>)` — `gs serve` offers no claim-deletion hint while a service answers. It omits the PID when the answering service did not return one. |
| The holder refuses a loopback connection | Startup reclaims the exact stale claim automatically and logs `reclaimed stale resident claim after <url> refused the liveness probe`. |
| Nothing can show the holder has gone | `refusing to serve: the service holding this repository could not be shown to be gone, so its claim is left alone (<url>); last-resort override: first prove no service is answering; deleting a live service's claim can start a second resident and race the log; only then remove the claim with git update-ref -d refs/gitseq/resident/<genesis>` |
| The claim cannot load | `refusing to serve: the ownership claim at refs/gitseq/resident/<genesis> (<object>) cannot be read as a claim: <reason>; …` — `gs serve` never treats a damaged claim as a vacancy, and the same last-resort race warning accompanies the manual override. |

## Restart

The resident and ordinary local `gs` commands share an application-owned
checkpoint selector at `.git/gitseq/checkpoints/<genesis>.json`. It names a
signed checkpoint object, and gitseq refreshes it every 256 accepted events
after the last successful write, so a new process re-audits only the tail.
The ref `refs/gitseq/checkpoints/<genesis>` points to the same object, keeps
it reachable to `git gc`, and repairs a missing or damaged local selector.
The selector also lets gitseq recover if that local ref went missing or
someone rewrote it. Gitseq trusts neither selector. The sequencer key current
at its head signs the checkpoint, and gitseq re-reads any key rotation inside
the cached prefix from its own sequence commit and checks it under the
preceding key, so a rotated log still restarts from cache. A missing or
mismatched checkpoint counts only as a cache miss: it does the full audit
instead. Checkpoint refs stay local: `attach` never fetches them, gitseq
never publishes them, and [`gs verify`](verify.md) never consults them.

For a deliberate cold restart, stop the resident and run
`gs checkpoint-clear --repo <path>` before starting it again. This clears both
persistent selectors. `GITSEQ_CHECKPOINT=off gs serve ...` keeps checkpoint
loading and publication disabled for that resident process.

The resident sweeps expired presence leases, so a session that goes away
without departing does not linger in presence forever.

## Local worktrees

A resident serves local checkout state at `/v0/worktrees`. It names the
served checkout's own absolute path, so a reader can tell which repository
the page shows, and otherwise emits only checkout basenames, branch
and HEAD, explicit clean, dirty, detached, bare, locked, prunable or
unavailable state, and — when one exists that it will link — that
repository's own remote. Of that, the browser reads the served path and
the remote: the per-checkout rows it once displayed have gone, so the rest
bounds what the endpoint discloses rather than what any page shows.
Disclosing the served path stays safe because [`gs serve`](serve.md) refuses
any non-loopback listen address: whoever reads the page already sits on the
host it names.

The remote comes from the repository you pointed this resident at, and
from that repository's own configuration. Those make two bounds, not one,
and neither implies the other. `git config --local` bounds the scope, so
no outer configuration scope can name a remote the repository never
configured. It does not bound which repository Git resolves: `GIT_DIR`,
`GIT_COMMON_DIR`, `GIT_WORK_TREE` and their family redirect that before
any scope rule applies, and a strictly local read of the wrong repository
still reads the wrong repository. So the endpoint strips them from the
environment of every Git command behind it. The endpoint bounds both the
bytes read and the number of remotes kept, and a repository past either
bound reports no remote rather than a partial answer.

A reader can predict which remote: `origin` when one exists,
otherwise the first remote name alphabetically. The endpoint then admits
that single choice only if it renders safely as a hyperlink. The rule works
as an allowlist — it admits `http` and `https` and refuses everything else,
so it refuses a scheme nobody has thought of by leaving it unlisted rather
than admitting it for want of a rule against it. The endpoint declines a URL
carrying userinfo, a query or a fragment outright rather than stripping it,
because any of the three can carry a credential and refusing keeps it out
of the response body altogether. Anything refused simply goes missing: the
endpoint omits the field, and the page shows the path with no link.

None of that belongs to the durable projection. A checkout associated
with a commitment only through a commit's `Rests-On:` trailer carries the
mark **local** in the view, and that marking makes the whole of the claim: a
trailer holds ordinary commit text, not an actor-signed statement, so the
association counts as local evidence and nothing the log will vouch for.

## See also

- [Deploy a resident](../../how-to/deploy-a-resident.md)
- [Components](../../concepts/components.md)
