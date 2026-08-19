---
title: Deploy a resident
summary: Run the local service, wait for it to be ready, and understand what the loopback boundary trusts.
rests_on:
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
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" &
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
! gs serve --repo "$REPO" --listen 0.0.0.0:9999
```

The refusal is deliberate. The service is a **trusted local custodian**
for several actors at once: it holds their signing keys and signs on
behalf of whichever session asks.

That makes a **session identifier a credential**. Present one and the
service signs with that session's actor key — ephemeral frames through
`/v0/say` and durable events through `/v0/act` — and will end that
session's lease on request. So session identifiers are never published.
Presence and the change stream name each session by an opaque minted
`session:` handle instead, drawn from system randomness with no
derivation in either direction. A live session cannot be rebound to a
different actor.

What remains trusted is the loopback boundary itself, and it is worth
being exact about how much that carries. Anything that can reach the
listening port can announce a session for any actor the repository holds
custody for and then act as that actor — not only ephemeral speech.

Two layers are worth separating, because conflating them overstates the
damage. Possession of a session makes the custodian produce a **genuinely
actor-signed** event: no later reader can tell it from one the actor
intended, because cryptography answers who holds the key and not who
meant it. What the event then *means* is judged separately — the fold
reads already-decoded records, checks no signatures, and can rule a
perfectly signed act ineffective on its merits. The boundary buys an
attacker authentic authorship, not automatic force.

There is no authentication below that line, by design. On a machine with
untrusted local users or processes, that boundary is the whole of the
protection, and it protects the durable record as well as the
conversation.

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
