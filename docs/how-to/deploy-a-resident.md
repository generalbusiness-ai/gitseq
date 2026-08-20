---
title: Deploy a resident
summary: Run the local service, wait for it to be ready, and understand what the loopback boundary trusts.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:209b923336260e75192deb934037c8a4c6fcb64e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7802fc152c5d66eae7f651783d24fab7ae477605
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:507fc7fe7ef7b5f998311bce5786b03d39d573ac
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:48bd5acfe51abd4146197a48b0f7674f5676cc5c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:68701f489d614b758b26439d335e7f0ec5307544
---

# Deploy a resident

The resident service sequences concurrent appends, holds presence and
ephemeral conversation, notifies readers when something moves, and serves
the browser view. Everything durable works without it; nothing ephemeral
does.

## Start one

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null

PORT="${PORT:-7777}"
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" \
  --acknowledge-trusted-processes &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT
```

Then open `http://127.0.0.1:$PORT` for the live view.

Serving publishes the address it bound inside the repository, together
with the genesis it holds, so clients find the service by naming the
repository rather than by being told a URL. Use `--listen 127.0.0.1:0`
when you are serving several repositories at once and do not want to
allocate ports by hand.

## Wait for it properly

Starting the process is not the same as being able to talk to it. Poll
for a real answer:

```sh
for _ in $(seq 40); do
  if gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null
```

## Submit through it

Durable subcommands that take `--server` submit through the resident
instead of writing to the local log directly. Both land in the same
sequence; going through the resident is what makes concurrent appends
safe.

```sh
GENESIS=$(gs status --repo "$REPO" --json \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p' | head -1)
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

gs state --repo "$REPO" --server "http://127.0.0.1:$PORT" --as alice \
  --kind assert --text 'submitted through the resident' --rests-on "$SEED"
```

## Loopback only, and why

```sh
! gs serve --repo "$REPO" --listen 0.0.0.0:9999 \
  --acknowledge-trusted-processes
```

The refusal is deliberate. The service is a **trusted local custodian** for
several actors at once: it holds their signing keys and signs on behalf of
trusted processes. The required `--acknowledge-trusted-processes` flag records
that deliberate operating choice for each invocation. Without it, serving
stops before the repository is opened or an address is published.

The listener host is resolved and every result must be loopback. Each HTTP
mutation separately rejects a Host that is not wholly loopback, then enforces
same-origin browser provenance and JSON content type before routing or
decoding. These guards narrow accidental exposure; they are not shared-host
authentication.

The resident, not the client, mints each private credential from 256 bits of
system randomness. It binds the credential to one repository and actor.
Renewal, acts, speech, inbox operations and departure use it in JSON bodies;
expiry, departure, revocation or process restart invalidates it. Presence and
the change stream show only a separate random `session:` handle. The handle is
display-only and grants no authority.

The browser keeps its credential only in memory. The MCP adapter keeps one
private credential per repository and consumes it internally; ordinary tool
results, including `whoami`, do not return it. Credentials do not appear in
status, logs, diagnostics, durable events, URLs, queries or referrers.

What remains trusted is the operating-system boundary itself. Any process
running as the account that owns the resident can reach loopback and may read
the repository's actor keys or invoke local `gs` commands directly. Such a
process can act as any actor whose key the application can open.

Two layers are worth separating, because conflating them overstates the
damage. Possession of a session makes the custodian produce a **genuinely
actor-signed** event: no later reader can tell it from one the actor
intended, because cryptography answers who holds the key and not who
meant it. What the event then *means* is judged separately — the fold
reads already-decoded records, checks no signatures, and can rule a
perfectly signed act ineffective on its merits. The boundary buys an
attacker authentic authorship, not automatic force.

There is no authentication below that line, by design. A multi-user service,
container boundary shared with untrusted workloads, or remote listener is not
a supported deployment.

## Supported deployment checklist

Run the resident under one dedicated OS account whose processes you trust.
Protect the repository's **common Git directory**, not only one checkout: it
contains actor and sequencer keys, the sequence refs, resident claim,
checkpoint selectors and linked-worktree metadata. Give other accounts no
read or write access to that directory.

Apply the same rule to every path that can copy or expose it:

- backups and volume snapshots;
- developer shells, editors, debuggers, profilers and crash reporters;
- synchronisation tools and cloud-drive clients; and
- copied repositories, worktrees, archives and temporary migration paths.

Encrypt and access-control backups and snapshots. Test recovery into an
equally trusted account. Do not restore a repository carrying live actor keys
onto a less trusted host merely to inspect it; use a read-only attachment or a
copy without private custody instead.

If the account, host, backup, snapshot or copied common directory may have
been exposed, stop the resident, revoke access to the host, preserve the
durable log for audit, and treat every actor and sequencer key in that custody
set as compromised. Recover from a known-good protected copy when possible.
Rotate the sequencer key through the verified in-band rotation procedure and
replace or retire affected actor keys and capabilities according to the
application's governance. Restarting the resident only revokes live
credentials; it does not rotate durable keys or undo signed acts.

## One service per repository

Exactly one runs, and this is now enforced. A second `gs serve` on a
repository that is already served refuses before it serves anything, and
names the address already holding it.

Two services on different ports against the same repository is the case
being prevented. The durable log stays correct either way — appends are
compare-and-swap on the git ref and retry on contention — but presence and
ephemeral conversation are per-process, so the two would form separate
rooms whose participants cannot see each other and are never told.

Ownership is a claim at the ref `refs/gitseq/resident/<genesis>`, holding
a small record of the address being served and a fresh random nonce. It is
taken with a git ref update carrying the expected old value, the same
compare-and-swap the durable log's own appends use, so exactly one of any
number of simultaneous starts wins it. The claim is an ordinary shared ref
in the repository's common directory, so every path alias, symlink and
linked worktree of one repository contends for the same claim, and two
different repositories never contend at all.

The advertisement at `.git/gitseq/resident.json` is endpoint metadata, not
authority. Only a process that already holds the claim writes it.

A service that stops normally releases the claim. A service that is killed
does not, and that is not a wedge: the next start reads the claim, asks the
address it names whether it is still serving, and takes it over when
nothing is listening there. Only a refused connection frees a claim.
Anything else — a timeout, a port that accepts and never answers, an
unparseable answer, an answer naming another workroom — leaves the claim
alone and refuses, because starting beside a resident that is really alive
is worse than not starting at all.

That asymmetry has one operational cost. If the address in a claim is
reused by an unrelated program that accepts connections, or is firewalled
so probes time out, `gs serve` will keep refusing. The refusal says what to
do:

```sh
git update-ref -d refs/gitseq/resident/<genesis>
```

Check that no `gs serve` is actually running against the repository first.
Removing the claim while a service holds it is how you get the two rooms.

This is coordination between cooperating services, not a security boundary.
Any local process that can write the repository can write the claim, and it
already has the durable log in reach.

## Restart

The resident and no-server `gs` commands share an application-owned selector
under `.git/gitseq/checkpoints/<genesis>.json`. It names a signed checkpoint
object. The local ref `refs/gitseq/checkpoints/<genesis>` names the same object:
the ref keeps it reachable to Git garbage collection and can repair a missing
or damaged selector, while the selector can recover from an unavailable or
rewritten ref. Both are selectors, not proof. The object contains the original
actor-signed events at one fully audited sequence head and is signed by the
sequencer key **current at that head**. On restart gitseq checks its checkpoint
schema, object format, genesis and exact head, proves the commit sequence from
genesis to that head from local metadata, and re-reads sequencer signatures and
payload objects only for events after the frontier. The checkpoint contains no
folded state and is not keyed by the application profile: a fold change reuses
the authenticated events and rebuilds the separately profile-gated projection.

Because the sequencer key can be rotated in band, deriving the right key
is part of the check rather than an assumption. Every rotation inside the
cached prefix is read from its own sequence commit and verified under the
preceding key, and the key that walk arrives at must be the one that
signed the checkpoint. Cached application events skip the sequencer
signature read; cached rotations do not.

A missing, malformed, mismatched, oversized or non-descendant checkpoint
is only a cache miss: gitseq performs the ordinary full audit and, if it
holds sequencer custody, replaces the checkpoint.

A writing process refreshes the reachability ref before the local selector
every 256 accepted events after its last successful write. A selector write
failure therefore cannot leave the new checkpoint object unreferenced. A
successful checkpoint usually leaves at most 255 commits for full delta
verification. Persistent storage or signing failures make that tail larger.

To force the next process to audit from genesis, stop any resident for the
repository and run `gs checkpoint-clear --repo <path>`. This removes the local
selector and rewinds the checkpoint ref to genesis; the next ordinary read
rebuilds both after its cold audit. To keep checkpoints disabled for a command
or process, set `GITSEQ_CHECKPOINT=off`. The off switch neither reads nor writes
the selectors. It does not turn [`gs verify`](../reference/gs/verify.md) into a
different operation: `verify` is always cold.

Checkpoint refs are local. `attach` does not fetch them, the documented
sequence push does not publish them, and `gs verify` never consults them.

## Stop it

```sh
kill "$SERVER"
trap - EXIT
```

Interrupt and terminate both count as being told to stop: the service
withdraws its advertisement and exits reporting success, so an ordinary
shutdown does not read as a fault in a supervisor's logs. Only a hard
kill leaves the record behind, and that costs a client one refused
connection before it falls back to acting locally.

## See also

- [`gs serve`](../reference/gs/serve.md)
- [Components](../concepts/components.md)
