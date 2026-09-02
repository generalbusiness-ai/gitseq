---
date: 2026-09-02
status: candidate design; no implementation is authorized by this note
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:959f847a42a52bf49350aec854bb4867bf256289
---

# Batch the durable append plumbing

Gitseq pays for process boundaries, not only for the work Git does inside
them. At current main `ad8793636a22985cc63eaecc16f6dbb61a6ec128`, one
ordinary warm resident append starts 15 Git processes. Three write objects,
one advances the sequence ref, and eleven read or verify state. A cold direct
append starts 24. The path is not minimal.

This note proposes two bounded changes. Write all payload objects through one
Git ingestion process, and reuse one verified read session while passing exact
heads between the kernel operations that already agree on them. The signed
intent, sequencer signature, application admission, replay decision, and the
compare-and-swap of `refs/seq/<genesis>` stay where they are.

The expected common warm cost is at most eight Git process launches: two
object writers, one ref update, and at most five reads. A cold append or a
checkpoint-refresh append should cost no more than fourteen. These are design
targets, not measured results. An implementation request must treat failure to
meet them as a failed performance condition rather than quietly revising the
numbers.

## Measurement

The measurement counted root Git processes rather than elapsed time. A PATH
shim recorded the complete argument vector and then executed `/usr/bin/git`.
The measured program therefore ran the real Git selected on Hugh's machine,
Git 2.50.1, and no command name was inferred from tracing or source inspection.
Git's own `ssh-keygen` children are outside this count because the question is
the number of Git process launches.

The shim was:

```sh
#!/bin/sh
set -eu
: "${GITSEQ_GIT_SHIM_LOG:?GITSEQ_GIT_SHIM_LOG is required}"
if [ -n "${GIT_TRACE2_EVENT:-}" ]; then
  {
    printf '%s' "$PPID"
    for argument do
      printf '\t%s' "$argument"
    done
    printf '\n'
  } >> "$GITSEQ_GIT_SHIM_LOG"
fi
exec /usr/bin/git "$@"
```

`GIT_TRACE2_EVENT` was set only around the operation under measurement. That
is the same measurement window the existing performance worker uses for its
Git-process counter. Fixture creation, workspace opening, and correctness
verification after the timed operation were outside the window.

The cold sample used the existing `submit_ack` worker at depth 1,000, fan-out
one, on the deterministic linear fixture generated from seed 632. Its fixture
head was `ce2e98bc22967372e22386753754f0c27179301f`, logical digest was
`7b3512ee2c7fb3ac95ce6dc01edec89b20194b6dd1f44eaec45a7ce1684a4159`,
and exact digest was
`615aee82f915f8ba1926af531101f18dc0c28b13d29ca259a65bb0896004edfe`.
The worker reported the same 24 Git processes as the PATH log and equal
projected and trusted correctness digests.

The warm sample used one `app.Workspace`, completed two ordinary assertions to
warm its reader, admission, submitter, and projection caches, and enabled the
shim only for a third assertion. It deliberately used a shallow fixture: the
claim is launch count, and the history walk is already one batch process at
any positive delta. No checkpoint was due during the measured append.

| Boundary | Object writes | Ref updates | Reads | Total |
|---|---:|---:|---:|---:|
| cold direct `Workspace.Act`, depth 1,000 | 6 | 2 | 16 | 24 |
| warm resident `Workspace.Act` | 3 | 1 | 11 | 15 |
| one-shot local `gs state`, including cold pre/post projection | 9 | 3 | 48 | 60 |

The warm command distribution was exact:

| Git command | Launches | Classification |
|---|---:|---|
| `hash-object -w --stdin` | 1 | object write |
| `mktree` | 1 | object write |
| signed `commit-tree` | 1 | object write |
| `update-ref refs/seq/... <new> <old>` | 1 | ref update |
| `rev-parse` | 6 | read |
| `show` | 2 | read |
| `log` | 1 | read |
| `cat-file --batch` | 1 | read |
| `verify-commit` | 1 | read and signature verification |

The cold sample added one checkpoint publication and cold verification work.
Its exact distribution was two `hash-object`, two `mktree`, two
`commit-tree`, two `update-ref`, nine `rev-parse`, two `rev-list`, two
`cat-file --batch`, one `show`, and two `verify-commit` launches.

The 60-process CLI number is useful context but is not the acceptance boundary
for this design. It includes repeated process-lifetime setup and projection;
conflating it with the sequencer append would hide whether the proposed write
and read changes worked.

## One Git ingestion for one payload tree

Replace `WritePayloadTree`'s one-process-per-blob calls and separate tree
builder with a bounded object batch:

1. Validate attachment names and the existing total payload ceiling before
   starting Git.
2. Serialize the event blob, attachment blobs, attachment tree, and root tree
   in memory. Compute every object ID with the existing SHA-1 and SHA-256
   object framing functions. The root ID must still equal the payload-tree ID
   in the actor-signed intent.
3. Encode those non-delta objects as one small Git pack and stream it to one
   hermetic `git unpack-objects -q --strict --max-input-size=<bound>` process.
   Do not use `-r`: its best-effort recovery is the opposite of this failure
   boundary. Git, rather than Gitseq, continues to install the objects in the
   repository and honour its object-store durability configuration.
4. Ask the shared read session to batch-check every expected object ID, type,
   and size before `commit-tree` can use the root. A missing or mismatched
   object refuses the append.

This turns the ordinary payload tree from two Git launches into one. With
attachments, it turns `attachment count + 3` payload-object launches into one.
The signed `commit-tree` remains a separate process because Git must create the
sequencer-signed commit, and `verify-commit` remains separate because a
successful signing command is not a substitute for auditing the resulting
commit.

`unpack-objects` is preferable to writing loose-object paths in Go. It keeps
Git's repository-format handling, object collision checks, permissions, and
configured fsync behaviour at the storage boundary. It is also preferable to
one tiny retained pack per append, which would exchange process count for an
ever-growing pack count. A failed ingestion may leave unreachable objects,
exactly as the current sequence of `hash-object` and `mktree` calls may; it may
not move a ref.

Checkpoint object publication should use the same batch primitive. Its signed
checkpoint commit and checkpoint ref update remain separate from the sequence
commit. Combining the checkpoint ref and authoritative sequence ref in one
`update-ref --stdin` transaction would make an optional cache-ref race refuse
an otherwise valid durable append. The common append already updates only one
authoritative ref, so there is no useful ref batch there.

## One verified read session

The warm path launches eleven reads because three verified consumers repeat
immutable discovery, head reads, and short history scans. Reduce launches
without making one layer trust another layer's application meaning:

- A `gitstore.ReadSession` owns one hermetic `cat-file --batch-command`
  process for its lifetime. It serializes requests behind a mutex, bounds every
  response, and fails closed if framing, output, or process lifetime is wrong.
  It carries object bytes only; it knows no Workroom kinds or authority rules.
- Walk first parents through that batch session instead of starting `rev-list`
  and then `cat-file`. Collect the verified delta and reverse it before folding,
  preserving the current oldest-first event order. The walk must refuse a
  missing parent, a repeated object, a non-descendant base, or any existing
  object, signature, payload, or size failure.
- Verify the repository object format and genesis descriptor when the kernel
  target is opened, then retain that immutable result in the kernel session.
  Do not accept either value from application configuration without that
  verification. Reopening creates and verifies a new session.
- Add an exact-head reader operation. The caller supplies the object ID already
  read from `refs/seq/<genesis>`; the reader authenticates that history without
  reading the ref again. The kernel submit loop reads the sequence ref once per
  attempt, advances its verified cache to that exact head, runs replay and
  application admission against that same head, creates and verifies the new
  commit, and finally uses the unchanged `update-ref <new> <old>` CAS.

The exact-head handoff is a kernel fact, not an application shortcut. A ref
advance after the read still loses the final CAS and enters the existing retry
loop. Application admission is rerun against the newly verified head on that
retry. The application never supplies verified bytes, a dedup result, or an
authority answer to the kernel.

The shared batch process is an optimization only. EOF, cancellation, malformed
framing, or an unexpected child exit discards the session. The operation then
returns an error; it must not silently retry a partly consumed protocol inside
an append. A later top-level operation may open a fresh session and perform the
ordinary verified read.

## Expected process budget

For a common warm append with no checkpoint due:

- object writes fall from three to two: one `unpack-objects` payload batch and
  one signed `commit-tree`;
- ref updates stay at one: the authoritative sequence CAS;
- reads fall from eleven to at most five: the two ref observations that select
  signing and sequencing positions, one signature verification, and no more
  than two cold-start or session checks.

That is an upper bound of eight launches, a reduction of at least seven from
the measured warm path. After the read session is established, the expected
steady result is seven because `cat-file --batch-command` remains alive rather
than launching per append.

A cold append or checkpoint refresh has more legitimate work. Its target is
fourteen or fewer launches, down from the measured 24: one payload ingestion,
one checkpoint ingestion when due, two signed commits, two ref updates, two
signature verifications, and at most six discovery or verification reads.
Attachment count must not change either target.

## Preserved contracts

**Signed authority.** The actor signs the same intent fields and payload-tree
identity. Application preflight remains advisory; the post-dedup hook judges
the exact verified head the kernel will attempt to extend. The sequencer still
signs the event commit, and the ordinary verifier still verifies that signature
before the ref can move. No application kind or role enters `gitstore`.

**Replay.** Deduplication still runs over a completely verified log before
application admission or commit creation. An exact retry returns its original
commit, and a conflicting reuse is refused. Batched payload objects are
content-addressed and may already exist; their presence never decides replay.

**Atomicity.** The only authoritative publication remains
`update-ref refs/seq/<genesis> <new> <old>`. All payload objects and the signed
commit exist and verify before that CAS. A CAS loss changes no visible sequence
and repeats verification and admission at the new head. Checkpoint publication
remains independent and cannot veto the sequence CAS.

**Durability.** Git still installs objects and refs. The design does not write
inside `.git/objects` itself, weaken repository configuration, defer the
authoritative ref update, acknowledge before the CAS returns, or add a memory-
only write queue. A process crash before the CAS can leave unreachable objects
but no event; a crash after the CAS leaves the same durable event the current
path leaves.

**Bounds and failures.** Existing envelope, payload, attachment, reference,
queue, retry, and checkpoint bounds remain. Pack construction is bounded by the
already admitted payload plus fixed framing overhead. The pack writer must not
accept deltas, external paths, filters, or caller-selected object types.

## Implementation and test order

This proposal authorizes nothing by itself. If ratified, implementation should
be split so object ingestion and read-session changes can be reviewed and
measured independently.

1. Add table tests for non-delta pack framing in SHA-1 and SHA-256 repositories.
   Compare every produced object ID and final tree ID with the current writer,
   including zero, one, and many attachments and maximum admitted sizes.
2. Add corruption, truncated-input, child-exit, cancellation, invalid-name, and
   missing-object tests. At every failure point assert that the sequence ref is
   unchanged. Retain the existing after-object, after-commit, before-CAS, CAS-
   loss, and before-reply replay tests.
3. Replace payload and checkpoint object writes with the batch primitive. Use
   a PATH-shim regression to require fixed object-write launch count regardless
   of attachment count.
4. Add the bounded read session and parent walk. Differentially compare cold,
   checkpoint-tail, incremental, non-descendant, malformed-history, rotation,
   and replacement-object cases with the present scanner.
5. Add exact-head admission tests with a forced ref advance before admission,
   before commit, and before CAS. Each race must either append against the head
   it judged or retry and judge the new head; it must never publish a stale
   authority decision.
6. Measure the same cold and warm operations with the same PATH shim. Require
   no more than eight common warm launches and fourteen cold or checkpoint
   launches, with object-write count independent of attachment count and equal
   projected and trusted digests.
7. Run the full suite, race suite for the affected packages, vet, both object
   formats, and the performance correctness harness. The implementing head must
   update the affected layer-one storage and layer-two kernel contracts in
   `docs/reference/architecture.md` and publish its candidate artifact.

The simplification condition is strict: one object-batch primitive and one
read-session primitive, both below application meaning. Do not add a general
transaction framework, a second storage backend, a write-behind queue, or an
application-specific fast path.
