# Reference

Commands, tools, and identifiers. For the ordered walkthrough, start
with [Getting started](getting-started.md).

## Glossary

An actor performs an act; admitted, it becomes an event; the fold reads it
back as a statement.

| Term | Meaning |
|---|---|
| **event** | One admitted, signed record at a final position in the durable log. |
| **act** | Something an actor does, such as requesting, promising, reporting, ratifying, or superseding. |
| **statement** | The fold's projected reading of an event. |
| **kind** | The named speech or governance classification carried by a statement, such as `request` or `assert`. |
| **request** | The unit of work: an actor asks another actor to act under stated conditions. |
| **promise** | An actor's voluntary undertaking to fulfil a request. |
| **report** | The promisor's claim that the promise's conditions have been met. |
| **commitment** | The tracked request–promise–report aggregate and its current lifecycle. |
| **ticket** | The short, one-based position used to refer to an event in one workroom. |
| **note** | The everyday UI label for an `assert` statement. |

The projection's existing `acts` JSON key for ratify and supersede events is
retained for wire compatibility. Renaming that key is follow-up work, not part
of this vocabulary change.

## Event identifiers

Every durable event has one canonical identifier:

```
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

This is the form used by `--rests-on`, by `ratify` and `supersede`
targets, by `provenance`, and by `Rests-On:` commit trailers. Always copy
it whole, from the emitted event rather than from a display that
abbreviates it.

How much of `rests_on` the fold checks depends on the event, and the rules
differ enough to be worth stating one at a time.

| Event | What must resolve | Surplus bases |
|---|---|---|
| `assert`, `artifact`, `propose`, and other statements | nothing | carried unchecked |
| `promise` | one basis must be an effective `request` | carried unchecked |
| `report` | one basis must be an effective `promise` | carried unchecked |
| `supersede` | the target, and it must be the **first** basis | carried unchecked |
| `ratify` | the target, and it must be the **only** basis | **refused** |

The required edges are what make the commitment chain hold: a promise
citing a request that does not exist is ineffective, and a report on
that promise is ineffective in turn, so an unearned approval cannot
carry force.

**For events that have a required edge**, that edge is necessary and
not sufficient — the fold also checks **who signed**, which the table
does not show. A promise citing a perfectly effective request is still
ineffective when its author is not the performer the request named;
measured, the reason reads `promise actor is not the requested
performer`. A report needs the promisor's own signature in the same
way, `ratify` needs authority over that particular target, and
`supersede` is likewise constrained. For those events, satisfying the
citation rule earns a hearing, not force.

That qualification matters: `assert`, `artifact`, `propose` and the
other ordinary statements require no resolving citation at all, so
there is nothing for a citation to be insufficient *for*. The row that
says "nothing" means it.

Authority grants then need a paragraph of their own, for a different
reason. For every other event, effectiveness is settled once, when the
event is appended, and stays settled. A grant can satisfy every rule, be
judged effective, be ratified — and still confer nothing, for as long
as the conditions below do not hold. Which conditions apply depends on
what kind of grant it is, and the two kinds are not the same shape:

- A **membership grant** — the one that makes someone a participant —
  is live when the **grant statement** is live and **at least one**
  effective ratification of it is live. It does not rest on a
  membership, because it *is* the membership.
- A **non-membership authority grant** — `ratifier`, `reviewer`, any
  named role — needs both of those *and* the **membership basis it
  named** to be live. That basis is its first citation, and the fold
  looks nowhere else.
- The **genesis seed** is the sole exception to the ratification
  requirement, and confers without one, because there is no prior
  ratifier to give it.

Reading the membership condition as universal is the easy error, and it
is wrong in the direction that matters: it invites you to look for a
basis under an ordinary membership grant, find none, and conclude the
grant is defective. Measured: retire the genesis seed after Bob has
joined, and Alice — seeded — disappears from the roster while Bob
remains `[participant]`, because his own membership grant never
depended on hers.

The ratification condition is a disjunction, not a single named act.
One roster statement may be ratified more than once, and any surviving
ratification keeps the role. Measured: grant Alice `reviewer`, have a
second ratifier ratify the same grant, retire the first ratification
and Alice is still `[participant, reviewer]`; retire the second and
`reviewer` goes. Writing "its ratification" invites the reader to
retire the one they can see and conclude the role is gone.

A grant can therefore fail to confer in two ways, and they are
different questions asked at different times. Both of the following
concern the **membership basis**, so both are about non-membership
authority grants only — a membership grant has no basis to get wrong.

**Citation order, at the moment of appending.** A `roster` statement
conferring a non-membership role takes its **first** basis as the
membership it rests on, and looks nowhere else:

| grant's `rests_on` | verdict | role active |
|---|---|---|
| membership first | effective | yes |
| membership first, surplus dangling basis | effective | yes |
| dangling first, membership second | **effective** | **no** |

**Liveness, at the moment of asking.** Current authority for such a
grant also requires that both it and the membership it rests on are
still live. Two distinct retirements, with different blast radii:

- Retiring the explicit grant — what `gs role-revoke` does — retires
  the role it named **and every role derived from it**. Measured:
  revoking `operator` takes a principal from
  `[operator, participant, ratifier]` to `[participant]`, because
  `ratifier` was riding on `operator` and had no grant of its own. The
  ordinary `ratifier` revocation looks like it retires one role only —
  `[participant, ratifier]` to `[participant]` — but that is because
  `ratifier` has nothing derived from it, not because revocation is
  narrow. Retiring ratifications can also end a grant, but only by
  exhausting them: because the ratification condition is a disjunction,
  the role survives until **every** live effective ratification of that
  grant is retired. Retiring one of two changes nothing.
- Retiring the **membership** removes membership itself, and with it
  every non-membership role that named that membership as its basis.
  One supersede, and the principal is no longer a participant.

**Liveness is reversible, and a verdict is not.** Retirement can itself
be retired, and authority comes back. Measured: superseding a
membership takes a principal from `[participant]` to absent from the
roster entirely, and superseding *that supersession* returns them to
`[participant]`. So "this grant confers nothing" is never a permanent
fact about the grant — it is a statement about right now, and the same
grant may confer tomorrow without anyone appending a new one. Decisions
are history and do not move; authority is current and does.

So for grants, do not read the verdict as the answer. Whether an event was
effective and whether an authority is live now are two questions, asked
at two different times, and only the first appears in the decisions
list. `gs actors` answers the second, and answers it only for the
moment you ask.

This is narrower than "a record can be recorded without effect", which
is true of the whole log and is what the verdicts exist to say. What is
peculiar to grants is that the verdict does not carry the answer.

Everywhere else a mistyped identifier is simply kept. A dangling basis
on an assert or artifact records as effective, and so does a surplus
dangling basis on a promise, report or supersede that already has its
required edge. `ratify` is the single operation strict enough to reject an
extra citation outright.

The division is deliberate. Those edges are machinery the fold owns;
`rests_on` otherwise asserts that one thing bears on another, which is a
claim about meaning, and a substrate with no ontology cannot check it.
The practical rule for an author does not vary with the table: copy
identifiers whole, from the emitted event.

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

The kernel can rotate that key in-band. A rotation is a reserved, empty-tree
commit signed by the current sequencer key that names one canonical successor
key. The successor becomes current only after that commit: later commits under
the retired key are refused, and full and incremental audits both carry the
current key forward as they walk the sequence. Rotation commits increase the
sequence depth but are not application events.

Rotation limits damage; it does not recover authority that is already gone. A
lost current private key cannot sign its successor, so recovery requires an
out-of-band continuation. Whoever holds a compromised current key can rotate
to another key before the legitimate operator does. The append-only history
shows that rotation, but the kernel cannot decide which competing custodian was
legitimate or undo events the compromised key already signed.

### Speaking

| Command | Purpose |
|---|---|
| `gs state --as <actor> --kind <kind> --text <text>` | Append a durable utterance. |
| `gs review --as <actor> --checkout <path> --artifact <event> --promise <event> --verdict <approved\|changes-requested> --text <text>` | Check the exact artifact checkout, then sign a review report. |
| `gs merge --checkout <path> --candidate <full-commit> --approval <event>` | Merge only the exact head named by a live ratified approval. |
| `gs ratify --as <actor> <event>` | Confer force, if you hold the authority for that target. |
| `gs supersede --as <actor> --text <reason> <event>` | Retire an event and propagate staleness. |
| `gs batch --as <actor> <file>` | Append an ordered chain of acts, loading and verifying the log once. |

`state` also takes `--rests-on <event>`, `--body key=value`, and
`--evidence name=path`, each repeatable, plus `--idempotency-key` for
safe retries.

### Chains of acts

Each ordinary durable subcommand loads and verifies the whole log before it
appends, so a chain filed one command at a time pays that cost once per act.
`gs batch` pays it once for the chain: it opens the workroom, verifies the log,
and then appends every act against that one resident frontier.

The input is a JSON array of acts, read from a file or from standard input when
the argument is `-` or absent. Each entry carries an optional `label`, a `verb`
of `state`, `ratify`, or `supersede`, and that verb's usual fields: `kind`,
`text`, `body`, `rests_on`, `target`, and `idempotency_key`.

```json
[
  {"label": "req", "verb": "state", "kind": "request", "text": "do the thing",
   "body": {"to": "@worker", "conditions": "tests green"},
   "rests_on": ["git:sha1:<genesis>#git:sha1:<event>"],
   "idempotency_key": "thing-request"},
  {"label": "promise", "verb": "state", "kind": "promise", "text": "I will",
   "rests_on": ["$req"], "idempotency_key": "thing-promise"}
]
```

A later act may cite an earlier act of the same batch as `$label` in `rests_on`
or `target`, and the label resolves to the event identifier minted for that act.
The whole file is parsed and every reference checked before the first append, so
a malformed entry or an unknown or forward label lands nothing. The array must
be the whole input: anything after it other than whitespace, including a stray
`]` or a second value, is refused before the first append.

The batch is not atomic. Events are commits on `refs/seq/<genesis>`, and the
kernel owns the whole write for each one: envelope and actor signature checks,
the payload ceiling, the admission hook, the dedup index, sequencer signing, and
the compare-and-swap that publishes the commit. Building a chain of commits
outside that path so the ref could move once would mean repeating those checks
where the kernel cannot enforce them. Per-act idempotency keys carry the
recovery instead: rerunning the same file replays the prefix that already
landed, without duplicating it, and continues from the first act that did not.
Acts given no idempotency key are not resumable and land afresh.

`gs batch` prints a JSON report naming, for every act in the chain, its
position, its label, the event it minted, and its outcome: `landed`,
`replayed`, `failed`, or `skipped`. A failure adds a typed `error` and exits
nonzero, so the report says exactly which acts landed and which did not.

`--server` forwards the same signed requests to the resident sequencer one at a
time. That server holds the single verified frontier, and batch semantics stay
per-act exactly as they are locally.

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
| `gs status` | Project a newest-first bounded current/actionable view. Every list is capped at 20 and reports its exact omitted count; request text is capped at 240 bytes. |
| `gs status --all` | Render the complete human-readable commitment, artifact, and attempt tables. |
| `gs status --json` | Emit the complete `Snapshot` JSON without the bounded human view. |
| `gs verify` | Check every signature and the sequence integrity. |
| `gs provenance <event>` | Walk back through everything an event rests on. |

An explicit `gs status --server http://127.0.0.1:7777` asks the resident's
bounded `/v0/status-summary` endpoint for the default view. The CLI accepts
only an HTTP loopback URL, follows no redirects, limits the response to 64
KiB and the request to two seconds, and checks that the returned genesis,
head, depth, and cursor still match the selected local workroom. A refusal,
timeout, oversized response, stale head, or moving head is named on stderr
and falls back to the verified local path. `--all` and `--json` use the full
`/v0/status` response and retain the complete modes.

Local status consumes a sequencer-signed checkpoint and verifies its
descendant tail. If no checkpoint is usable, it performs the ordinary full
audit and prints a progress line after one second rather than appearing to
hang. `gs verify` never uses the checkpoint shortcut: it always audits the
whole sequence.

Resident snapshots are immutable borrowed views. In-process consumers may
receive maps and slices owned by the workspace cache and must not mutate them;
JSON and MCP adapters only serialize those values.

The browser's Work drawer also reads local worktree state. That endpoint names
the served checkout's own absolute path, so a reader can tell which repository
the page is showing, and otherwise emits only checkout basenames, branch/HEAD,
and explicit clean, dirty, detached, bare, locked, prunable, or unavailable
state; it never enters the durable projection. Disclosing the served path is
safe because `gs serve` refuses any listen address that is not loopback: the
browser reading it is already on the host it names. A checkout associated only through an ordinary commit's
`Rests-On:` trailer is visibly marked **unverified trailer**, because trailer
text is not an actor-signed workroom statement. The railway is a newest-80
window and says when it is truncated, so older trailer associations may be
absent without being mistaken for durable evidence.

### Exact-head workflow guards

`review` is the enforced verdict boundary. The named artifact must be effective
and must not be retired; the named promise must be effective, not retired, and
owned by the reviewer; and its originating request is copied from the durable
graph rather than typed again. The checkout must belong to the same repository,
be clean (including no untracked files), and have the artifact's full commit ID
checked out. The resulting report names that immutable head and rests on the
promise, request, and artifact. Running tests, a built CLI, or Git-plumbing
experiments remains review evidence: those probes help a reviewer reach a
verdict, while `review` guards the state at which the verdict is signed.

Staleness does not stop a review. Being retired and being stale are two
different facts, and the projection keeps them apart: retired means this act
was superseded, stale means something underneath it was. A stale artifact still
names the commit it always named, and whether the movement matters to that
commit is the reviewer's question. So `review` goes ahead and records what had
moved: the verdict body carries `stale=true` and a `staleness` field naming
which of the artifact, promise and request are stale, whether the movement was
in the world they describe, and the retired events that caused it. The signed
report then says plainly that the world had moved and the reviewer signed
anyway.

After the review requester ratifies an approved report, `merge` enforces the
other boundary, and it keeps the strict reading that `review` gives up. It
refuses an ineffective, unratified, retired, or stale approval or artifact; a
non-approval verdict; a candidate other than the report and artifact's exact
head; a dirty target checkout; and a checkout from another repository. The
latitude belongs where a reviewer is present to exercise it. A refused merge
leaves a signed approval standing and asks only that the record be brought up
to date. It passes the approved full object ID to `git merge --no-ff`, never a
branch name, so advancing the reviewed branch cannot retarget the merge.

Recording the result is separate, and the workroom discipline separates
retiring from publishing: every live artifact covering what the merge changed
is retired, and a successor is published at the one path the area keeps using.
A path can carry several live artifacts, which the projection counts and
reports as *succession not recorded* until each is retired; a path with no
live artifact yet needs no supersession.
`git diff --name-only <merge>^1 <merge>` lists the files a merge brought in.
Paths are compared as exact strings — the projection keys artifacts by the
path field with no normalising, prefix matching, or globbing — so a new
artifact reaches a predecessor only when the two strings are identical. Reuse
the exact path an area already uses instead of renaming it, and never
comma-join paths: `AGENTS.md,SKILL.md` is one string that no real artifact can
equal in either direction.

Two cases would otherwise be ambiguous. When a directory and something inside
it are both live over one changed file, the wider path wins: the successor
goes to the directory, and the narrower artifact is retired by a bare
`gs supersede` naming the surviving path, which is never published at again.
When a merge renames or deletes a tracked file with a live artifact at its old
path, that artifact is retired the same way; a rename publishes a first
artifact at the new path, and a deletion publishes nothing. `gs supersede`
rests on its target by itself, and `decideSupersede` admits it only from the
target's own author or from an actor holding `ratifier` — an operator holds
that role by implication — so retiring another actor's artifact needs a
ratifier.

A merge artifact at `.` is the failure this rule
exists to prevent: a path every merge rewrites retires every act anchored to
it on every merge, whatever changed. The whole-repository pointer has no
successor and needs none — `git rev-parse main` answers which commit main
carries, and the live artifact at a path answers it for that area.

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
by the sequencer key current at that head. On restart, gitseq checks the
checkpoint's object format, genesis, exact head, and fold-profile version. One
local first-parent metadata enumeration then proves the exact commit sequence
from genesis through the named head. Every cached event must occupy its claimed
commit and match that commit's actor envelope, causal trailers, and tree; its
actor signature, payload ceiling, dedup key, and payload-tree bytes are checked
again. Every rotation in the cached prefix is read from its exact sequence
commit and its signature is checked under the preceding current key. The key
derived through those verified rotations must then sign the checkpoint itself.
Cached application-event commits avoid sequencer-signature and payload-object
reads; cached rotations still require signature checks, and every commit after
the checkpoint frontier receives the ordinary full verification. A missing,
malformed, mismatched, oversized, or non-descendant checkpoint is only a cache
miss: gitseq performs the ordinary full audit and, when it holds sequencer
custody, replaces the checkpoint.

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

Run **one** service per repository. Nothing enforces this: there is no
lock. Two services on different ports against the same repository is the
case to avoid. The durable log stays correct — appends are
compare-and-swap on the git ref and retry on contention — but presence
and ephemeral conversation are per-process, so the two form separate
rooms whose participants cannot see each other and are never told.

Serving binds before it publishes, so the address written into the
repository is the one actually being served and a failed start announces
nothing. Publication is not a lock either: the last service to start wins
the advertisement, which at least pulls new clients into one room.
Stopping withdraws the advertisement unless a later service has taken it
over. Interrupt and terminate both count as stopping, so Ctrl-C and an
ordinary supervisor shutdown both clean up and both exit reporting
success. Only a hard kill leaves a record behind, and that costs a client
one refused connection before it acts locally instead.

`--listen 127.0.0.1:0` takes any free port, which is what you want when
several repositories are served at once: clients read the port from the
repository rather than being told it.

#### What loopback still trusts

The service is a local custodian for several actors at once. It holds
their signing keys and will sign on behalf of whichever session asks, so
a **session identifier is a credential**: present one, and the service
signs with that session's actor key — ephemeral frames through
`/v0/say`, and durable events through `/v0/act` — and will end that
session's lease on request.

Session identifiers are therefore never published. Presence and the
change stream name each session by an opaque minted `session:` handle
instead — drawn from system randomness, with no derivation from the
identifier in either direction — which is stable enough to follow a
renewal or notice a departure and grants nothing. A live session cannot be rebound to a different actor.

What remains trusted is the loopback boundary itself, and it is worth
being exact about how much it carries. Anything that can reach the
listening port can announce a session for any actor the repository holds
custody for, and then act as that actor — **not only ephemeral speech**.
`/v0/act` resolves the session the same way `/v0/say` does, selects that
actor's custodial key, and appends a durable event to the log.

Two layers are worth separating here, because conflating them overstates
what an attacker gets. Possession of a session makes the custodian
produce a **genuinely actor-signed** event: the kernel authenticates that
signature and retains the event, and no later reader can tell it from one
the actor intended, because cryptography answers who holds the key and
not who meant it. What the event then *means* is judged separately — the
profile fold reads already-decoded records, checks no signatures at all,
and can rule a perfectly signed event ineffective or disputed on its
merits. So the boundary buys an attacker authentic authorship, not
automatic force.

There is no authentication below that line, by design — this is a
trusted local multi-actor custodian, not a remotely authenticated
server, which is why it refuses non-loopback listeners. On a machine
with untrusted local users or untrusted local processes, that boundary
is the whole of the protection, and it protects the durable record as
well as the conversation.

## MCP

```sh
gitseq-mcp --actor <name> [--repo <path>]
```

One process per client session, one actor per process. The adapter signs
every act as that actor and holds a leased, session-bound presence.

**Which workroom.** The repository is a parameter of the call, not of the
installation. A call with no `repo` acts in the adapter's working
directory, or in `--repo` when that was given; any call may name another
repository instead. A directory with no workroom, or one where the actor
is not configured, fails that call and says so — it does not stop the
adapter, which is installed once and pointed at many repositories. Linked
worktrees of one repository are one workroom, not several.

**Finding the service.** The adapter reads the resident address that
`serve` published in the target repository, and uses it only when the
genesis recorded with it matches that workroom, so an act cannot be
posted to a service holding a different log. An address that stops
answering is forgotten and looked up again on the next call, so a service
started or moved later is picked up without reconnecting the client.

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

`whoami` returns the effective actor identity and capped roles at an exact
durable frontier. A current loopback resident is labeled
`resident_statusview_current`; the client refuses redirects and guards the
answer with a two-second, 64 KiB, strict-JSON boundary plus matching local
genesis and head checks. A local fallback sets `degraded: true` and names the
actual verified path: `verified_signed_checkpoint_tail`,
`verified_incremental_tail`, or `verified_cold_full_audit`. The response never
includes the local actor key path, and omitted non-semantic roles are counted.

**Degraded operation.** If the resident service is down, the durable
tools keep working directly against the local log and report a
`degraded` live cursor. `say` and `presence` fail rather than pretend —
ephemeral state does not survive, and the adapter will not imply it did.

## What lives where

| Path | Contents |
|---|---|
| `cmd/` | Shipping `gs` and `gitseq-mcp` commands. |
| `docs/` | User documentation. |
| `internal/` | Production kernel, workroom profile, nexus, and service packages. |
| `SKILL.md` | Normative contract for agents in a workroom. |
| `notes/` | Dated design and implementation notes. |
| `spike/` | Adversarial CLI, report generator, forge fixture, and six-case evidence. |
| `ui/` | Browser projection source; `make ui` regenerates the committed build. |
| `internal/service/uidist/` | Committed browser build served at the listen address; `make ui-check` rebuilds and verifies it matches `ui/`. |
