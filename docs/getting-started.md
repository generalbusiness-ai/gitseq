# Getting started

One path, end to end: install, initialize, join over MCP, do a piece of
work, verify it, and audit it from a fresh clone. Every command here is
meant to be run in order.

The example uses two participants — `alice`, a human operator, and
`bot`, an agent. Substitute your own names.

## 1. Install

Requires **Go 1.26** and **Git with SSH signing support**.

```sh
git clone <this-repo> gitseq
cd gitseq
make test
make vet
make build
```

`make build` produces two binaries in `bin/`: `gs`, the command line and
resident service, and `gitseq-mcp`, the adapter that lets an agent join
over MCP. Put them on your path, because the rest of this page runs them
from inside a *different* repository:

```sh
export PATH="$PWD/bin:$PATH"
command -v gs        # should print .../bin/gs
```

If you would rather not change `PATH`, substitute the absolute path to
`bin/gs` and `bin/gitseq-mcp` wherever they appear below.

## 2. Initialize a workroom

A workroom is an overlay on an ordinary git repository — your own, not
this one. It adds refs under `refs/seq/*` and private state under
`.git/gitseq`; it never touches your branches or working tree.

```sh
cd /path/to/your/repo
gs init --repo . --operator alice
gs actor-add --repo . --as alice --name bot --kind agent
gs actors --repo .
```

`init` prints the **genesis** hash. Keep it: everyone who attaches to
this workroom later needs it.

`kind` (`human`, `agent`, or `service`) describes what a principal *is*
and grants no authority. Authority comes from roles, which are separate
and durable. The operator can ratify; nobody else can until granted:

```sh
gs role-grant --repo . --as alice --actor bot --role ratifier
```

An agent holding a ratifier grant may ratify. Kind is not an authority
test.

Now start the resident service:

```sh
gs serve --repo . --listen 127.0.0.1:7777
```

Open <http://127.0.0.1:7777> for the live view. The service refuses
non-loopback addresses on purpose: it is a trusted local custodian for
several actors on one machine, not an authenticated remote server. Run
exactly one against a repository — see [Reference](reference.md#one-service-per-repository).

## 3. Join over MCP

Run one adapter process per client session, configured for exactly one
actor. It signs everything that session does as that actor.

```sh
gitseq-mcp --repo /path/to/your/repo --actor bot --server http://127.0.0.1:7777
```

Point your MCP client at that command. It exposes eight tools —
`whoami`, `presence`, `status`, `wait`, `say`, `state`, `ratify`, and
`supersede` — described in [`SKILL.md`](../SKILL.md), which is the
normative contract for agents working in a workroom. Read it before
acting as one.

> **Client compatibility.** This adapter currently implements only the
> stateless MCP `2026-07-28` shape: no `initialize` handshake, a
> `server/discover` probe, and per-request protocol metadata. Clients
> built against `2025-11-25` or earlier open with `initialize` and
> **cannot attach to this build** — the specification classes that
> combination as a failure with no client-side fallback. If your client
> reports an unsupported protocol version, this is why.

## 4. Do a piece of work

Durable acts can be submitted from the command line or by an agent
through MCP; both land in the same log. The unit of work is a
**request**, claimed by a **promise**, closed by a **report** that the
*requester* ratifies. You never declare your own work complete.

Ask for something:

```sh
gs state --repo . --server http://127.0.0.1:7777 --as alice \
  --kind request --text 'Build the projector' \
  --body to=@bot --body conditions='all tests pass'
```

Every durable command prints the **event identifier** that later acts
cite:

```
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

Copy it whole. Never retype a shortened form — a citation that does not
resolve is accepted silently.

`bot` claims it, then reports when done, each citing the previous event:

```sh
gs state --repo . --as bot --kind promise \
  --text 'I will build the projector' \
  --rests-on '<request-event>'

gs state --repo . --as bot --kind report \
  --text 'Projector built; make test and make vet pass' \
  --rests-on '<promise-event>'
```

`alice` — the requester, and only the requester — confers force:

```sh
gs ratify --repo . --as alice '<report-event>'
```

Attempts that exceed your authority are not errors. They are recorded
and marked ineffective, visibly and permanently.

### Tie the work to the code

Work that changes files must be bridged to the decisions that motivated
it, or staleness tracking cannot see it. Give the implementing commit a
trailer:

```
Rests-On: <request-event>
```

Then point at the exact commit:

```sh
gs state --repo . --as bot --kind artifact \
  --text 'Projector implementation' \
  --body path=cmd/projector --body commit=<full-commit-sha> \
  --rests-on '<request-event>'
```

The governing event must exist *before* you make the commit — otherwise
you have to amend the trailer in afterwards, which changes the hash.

### Guard review and merge

After the reviewer has promised the exact-head review, use the workflow helper
to sign the verdict. It checks the artifact event, derives the review request
from the promise, and refuses a dirty or advanced checkout before submitting
the report:

```sh
gs review --repo . --as reviewer --checkout ../project-review \
  --artifact '<artifact-event>' --promise '<review-promise-event>' \
  --verdict approved --text 'APPROVED; make test and make vet pass'
```

The review requester ratifies that report. Then merge the immutable approved
commit, not the branch that happens to point at it:

```sh
gs merge --repo . --checkout . --candidate '<full-commit-sha>' \
  --approval '<ratified-review-report-event>'
```

Independent tests and Git-plumbing probes remain part of the reviewer's
evidence. These commands enforce the handoff boundaries: the checkout matched
the artifact when the verdict was signed, and the merge candidate matches the
still-live ratified approval. After the merge, publish its artifact and ratify
the original implementation report as described by the repository discipline.

## 5. Verify

```sh
gs status --repo .
gs verify --repo .
```

`status` projects the current state: commitments and who they wait on,
artifacts and whether they have gone stale, and attempts that were
judged ineffective. `verify` checks every signature and the integrity of
the sequence.

An artifact turns **stale** when something it rests on is retired.
That is the whole point: the document finds out it is wrong, and can say
which event made it so.

## 6. Audit from a fresh clone

The strongest check is that a stranger with nothing but a clone can
confirm the record — no service, no chat logs, no trust in you.

The sequence lives in `refs/seq/*`, and git neither pushes nor fetches
those by default. Both halves have to be arranged, or the audit fails on
a ref that was never published.

First publish it from the repository that holds the workroom. Do this
whenever you want others to see new events — it is the step that makes
the record shared rather than local:

```sh
git push origin 'refs/seq/*:refs/seq/*'
```

The refspec deliberately has no leading `+`. A sequence only ever
advances, so publishing is always a fast-forward, and a push that git
refuses is telling you the remote holds something your copy does not.
Forcing it would overwrite published history — in a record whose whole
purpose is that positions are final, that is the one thing you must not
be able to do by habit.

Then, as the auditor, clone and attach. `attach` adds the matching fetch
rule and pulls the sequence down. The rule is deliberately non-forcing:
later attaches and ordinary fetches accept only initial or fast-forward
sequence refs. If an older build installed a forced sequence rule, `attach`
replaces it before fetching. A remote that has rewound is rejected without
moving the auditor's last observed sequence head.

```sh
git clone <your-repo> /tmp/audit
gs attach --repo /tmp/audit --remote origin --genesis '<genesis>'
gs status --repo /tmp/audit
gs provenance --repo /tmp/audit '<artifact-event>'
```

If `attach` reports `Needed a single revision` for a `refs/seq/...` ref,
the sequence was never published. Run the push above in the workroom's
repository, then rerun `attach` in the clone you already have — the
clone itself is fine, only the refs were missing.

`provenance` walks back from any event through everything it rests on.
Attached clones are read-only unless local actor custody and a sequencer
endpoint are configured. Delete `.git/gitseq` and the extra
`refs/seq/*` fetch rule and you are left with an ordinary repository —
you can always leave with everything.

## Where to go next

- [Reference](reference.md) — commands, tools, and identifiers.
- [`SKILL.md`](../SKILL.md) — the normative contract for agents.
- [`notes/`](../notes/) — design and implementation notes, including the
  [design document](../notes/2026-08-05-gitseq-design.md).
