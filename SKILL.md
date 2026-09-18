---
name: workroom
description: How to work in the gitseq workroom. The loop, and the command
  each step takes. Normative for agent actors; the implementation must match
  this contract.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:af34036cc682b70c25b342e4d1ae151bf189aec1
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c291f07e1368a6555263d4924aebc51eb8b37b4c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:abbcaca3d7e3698c18d7320fd5777e20aef7fa94
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f63f8396801b661b79141281c65394151cce6068
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0da3aeabc822e15d50a4aed159aa28bb8043a99b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:861051cb9ef2e871bfb353d5e8b31866e26dfcd3
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a4470198fb447513e9a6deffe185de3b2a87525f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:dfff38330e26e43eb766db96d7dde605b2afb972
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8f5434f1c274cda75b68d3e272a1c50877c63734
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd7ea9e4bc9d97dd95133d999766029d1bd60cf6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
---

# Working in the workroom

You are an actor in a shared, append-only workroom. It exists to coordinate
work between people and agents, and it is a tool for that and nothing more.

Every durable act is signed with your key, ordered, permanent and visible,
including those judged ineffective. Talk in the ephemeral channel (`say`);
commit deliberately.

Each step below takes one `gs` command. Each checks its shape and refuses
before signing, naming the repair: read the refusal instead of working around
it, and see that command's page under [docs/reference/gs/](docs/reference/gs/)
for what it checks. [`gs state`](docs/reference/gs/state.md),
[`gs batch`](docs/reference/gs/batch.md),
[`gs ratify`](docs/reference/gs/ratify.md) and
[`gs supersede`](docs/reference/gs/supersede.md) remain for acts no step
command covers, each with `--no-preflight` for an act the preflight would
turn away. Pass `--as <you>`, or set `GITSEQ_ACTOR`, on every signing call.

Reasoning and history: [the work loop](docs/concepts/work-loop.md),
[agent practice](docs/concepts/agent-practice.md),
[decision authority](docs/concepts/decision-authority.md),
[staleness](docs/concepts/staleness.md). MCP tools, one page each:
[docs/reference/mcp/](docs/reference/mcp/). Adapter setup:
[Configure an agent](docs/how-to/configure-an-agent.md).

## 1. Orient

`gs work --next` prints the exact command each row owes; read it first. `gs
wait` blocks until something is actionable for you, printed the same way,
[`gs status`](docs/reference/gs/status.md) snapshots once and returns the
cursor both waits follow, and `gs inspect <event>` opens one row.

Presence is advisory session attention: `busy`, `waiting`, `blocked` or
`available`, up to eight focus events, a note. Never a promise, report,
authorization or completion.

## 2. Answer every request addressed to you

Every cycle, every request addressed to you gets one of three answers.

- **Take it:** [`gs promise <request>`](docs/reference/gs/promise.md).
- **Decline it:** an `assert` resting on the request that says you decline and
  why, plus a request to its author or a `ratifier` to retire it: only they
  may, so the row stays open until they do.
- **Not actionable yet:** an `assert` resting on the request saying so. For a
  stale request, name the staleness and ask its author to refile on current
  bases.

A promise is optional. When the work is already done, report straight against
the request, which only its addressee may do, with no live promise of their
own on it: for implementation work that report is the artifact, and where the
request owes no Git artifact it is a `report` resting on the request.

An unclaimed request that should move to another actor goes through
[`gs reassign-if-unclaimed`](docs/reference/gs/reassign-if-unclaimed.md),
flags before the request: it retires and replaces the request under one guard,
and a claim in between refuses the pair.

Work you begin yourself, as both requester and performer, files no
self-request and no self-promise: rest the implementing commit on the
motivating adopted decision, publish the artifact, and go straight to review.
No row then shows the work in flight.

## 3. Implement on a worktree

Work on a new `request/<slug>` branch and worktree, unless the request records
a better prefix. Never commit on the target branch: the request's
`target_ref`, `main` unless a request or governing decision names another.
Base and recut the work on it.

Every implementing commit carries `Rests-On:` with the full canonical event
identifier: the request for assigned work, the adopted decision for work you
began yourself.

## 4. Publish the artifact set

[`gs artifact`](docs/reference/gs/artifact.md) `--head <commit> --promise
<your promise> --report <path> --text <what was met> <path…>`

One artifact per path the head changed, each on that one promise, the
reporting artifact last, its text stating the tests and conditions met. It is
the implementation report: no `ready-for-review` follows. Add `--rests-on` for
the behaviour a page documents, or it could never flare. A republish retires
the earlier head's artifacts at the paths it names, and warns about the rest.

## 5. Request review

[`gs review-request`](docs/reference/gs/review-request.md) `--head <commit>
--to <another actor> [--replace]`

It rests on every live artifact of yours at that head and names the reporting
artifact, which is how a verdict binds.

## 6. Review, when you are the reviewer

Promise the review, then [`gs review`](docs/reference/gs/review.md)
`--artifact <reporting artifact> [--artifact <other>…] --promise <your
promise> --checkout <clean checkout at that head> --verdict
approved|changes-requested --text-file <file>`, the reporting artifact first.

Staleness does not stop a review; the verdict records what had moved.

Every implementation review records three conclusions before approval.

- **Architecture:** name the affected layers from
  `docs/reference/architecture.md` and whether the head preserves or changes
  their contract. A contract change must update that page in the same head and
  publish its artifact there; otherwise request changes.
- **Security:** examine the affected trust and authority boundaries,
  untrusted inputs, signatures, secrets, bounds and failure modes. Any
  unresolved defect is `changes-requested`. Work resting on an
  authority-bearing request chain needs its
  [four facts](docs/concepts/decision-authority.md#the-authority-bearing-request-chain)
  confirmed from the durable record.
- **Simplification:** say what could be simpler without weakening the
  conditions of satisfaction, and request changes to cut the fluff.

Check the evidence rather than the claim. A test that passes is not a guard
that holds: break each guard the head adds and watch a named test go red. A
mutant that does not compile has proved nothing.

A `changes-requested` verdict leaves the implementation open and authorizes no
merge; ratification closes the review. Repair the head on the same request and
promise while the outcome, conditions, performer, destination and authority are
unchanged: cite the finding in the new artifact, republish every changed path,
and request review again. Work that adds a separate outcome, or changes those
conditions, the authority or the performer, needs a child request first.

## 7. Land

[`gs land`](docs/reference/gs/land.md) `--approval <approval> --checkout
<target checkout> --text <plain-language description> --cleanup`

It ratifies the approval when you are the review requester, runs `gs merge`'s
locked transaction, pushes the target ref, and with `--cleanup` removes the
worktree and branch, but only once `git merge-base --is-ancestor` proves the
head is in the target. `--checkout` is the target checkout, not the
candidate's worktree. Write `--text` for a reader with no event ids: who
proposed, ratified and reviewed, what was raised, how it was resolved.

The merge publishes a successor at each changed path and seals every other
covering pointer as carried, sibling or abandoned; only an abandoned one owes
cleanup, by its author or a `ratifier`.
[`gs merge`](docs/reference/gs/merge.md#artifact-succession) states those rules
and what a
[held landing](docs/reference/gs/merge.md#held-landings-and-the-compatibility-window)
needs. If the target already has the approved head, run it anyway: one
incorporation receipt is recorded, no merge commit is made, and `gs land`
still pushes and cleans up.

The sealed receipt closes the commitment, so no report and no ratification
follow it. Work that resolves without landing closes through an explicit
`report` and the requester's ratification, or through supersession.

## 8. Clean up

Whatever `--cleanup` reports instead of deleting, a moved branch or a remote
tip carrying other work, is yours to finish. Never leave a stale worktree:
finishing includes the review and the landing.

## Whatever you write down

Cite the canonical full event identifier, copied from the result that returned
it; say `#N` in prose, checkable by eye. `#N` and an unambiguous hash fragment
resolve at every signing boundary except a commit trailer. Cite files as
`path@commit` at the revision you read, never copying a document into an event.

Spend the attention on the work, not the bookkeeping, and let the boundary
tell you which is which. A stale basis is admitted and recorded, so re-anchor
and carry on rather than stall over it; a retired one refuses, so repoint it
before you file. Between two the boundary would both admit, cite the one you
can defend and move on, saying in the text what you were unsure about.

Before choosing a kind for `gs state`, read `durable.vocabulary.definitions`
in the status response: it decides required fields,
admissible bases and who may ratify. A request states `body.to`,
`body.conditions`, and what it owes: `target_ref=refs/heads/<branch>`,
`target=inherit`, or `no_git_artifact=true`; one stating no result is refused,
and the boundary measures the target itself.

Treat GitHub issues and reviews like ephemeral chat: promote what
crystallizes, quoting the frames as evidence. Cite a pull request by its head
commit, never rest a durable act on a bare URL, and put no secret in either
channel.

Keep routine progress and dead ends ephemeral. Promote a breakdown that
changes scope or a condition of satisfaction, or creates follow-up work, and
record a material blockage as an `assert` on the promise. Report what you
actually ran: a gate you did not run, or ran and did not read, is not evidence,
and a number from a killed run is not a measurement. Superseding your own
promise is reneging, and early reneging is honourable. Never sign as another
actor. Leave a log a stranger could audit.
