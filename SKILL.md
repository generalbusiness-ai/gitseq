---
name: workroom
description: How to work in the gitseq workroom. The loop, and the command
  each step takes. Normative for agent actors; the implementation must match
  this contract.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:495faace9dc3cfe588a7a6b2d16fae9b8b681f18
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4f698ddb0ac8e86009f4685889cd1de59c95de34
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c0406962ba513c23b145282eaaabbb8f37e2f017
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9d0f7310f9ee48e696917adebe874c1e3113816b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:688ffd2ffac3abe0b68afb8c01fdb2fd7596f671
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1ca715ff674785605df61cde345dbda206f4b885
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:abbcaca3d7e3698c18d7320fd5777e20aef7fa94
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:74b0cb9a15ed8f7e81b8609ed5c71bb5af4d8265
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:732cf5a0a54d7443f05318908206b31d2c18800a
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd7ea9e4bc9d97dd95133d999766029d1bd60cf6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:afb09e38c640e12c625f0fb3cb2069f670ebaedf
---

# Working in the workroom

You are an actor in a shared, append-only workroom. Every durable act is
signed with your key, ordered, permanent and visible, including those the
fold judges ineffective. Talk in the ephemeral channel (`say`); commit
deliberately.

Each step below takes one `gs` command. Each checks its shape and refuses
before signing, naming the repair: read it instead of working around it. `gs
state`, `gs batch`, `gs ratify` and `gs supersede` remain for acts no step
command covers. They refuse an act the fold would rule ineffective and
answer a malformed invocation with usage; `--no-preflight` files
an act as written. Pass `--as <you>`, or set `GITSEQ_ACTOR`, on every signing
call.

Reasoning and history: [the work loop](docs/concepts/work-loop.md),
[agent practice](docs/concepts/agent-practice.md),
[decision authority](docs/concepts/decision-authority.md),
[staleness](docs/concepts/staleness.md). MCP tools, one page each:
[docs/reference/mcp/](docs/reference/mcp/). Adapter setup:
[Configure an agent](docs/how-to/configure-an-agent.md).

## 1. Orient

`gs work --next` prints the exact command each row owes; read it first. `gs
wait` blocks until something is actionable for you, printed the same way.
`gs status` snapshots once and returns the cursor both waits follow; `gs
inspect <event>` opens one.

Presence is advisory session attention: `busy`, `waiting`, `blocked` or
`available`, up to eight focus events, a note. Never a promise, report,
authorization or completion.

## 2. Answer every request addressed to you

Every cycle, every request addressed to you gets one of three answers.

- **Take it:** `gs promise <request>`.
- **Decline it:** an `assert` resting on the request that says you decline and
  why, plus a request to its author or a `ratifier` to retire it: only they
  may, so the row stays open until they do.
- **Not actionable yet:** an `assert` resting on the request saying so. For a
  stale request, name the staleness and ask its author to refile on current
  bases.

`gs promise` refuses a request addressed to somebody else, a request that is
not open, and a second claim while you hold a live promise on that request,
unless it is the same idempotent retry, which replays it. A promise is optional: when the work is already done, report straight
against the request, which only its addressee may do, with no live promise of
their own on it. For implementation work that report is the artifact, `gs
state --kind artifact --rests-on <request> --body path=<path> --body
commit=<full commit> --text <what was met>`; where the request owes no Git
artifact it is `gs state --kind report --rests-on <request> --text <what was
met>`.

An unclaimed request that should move to another actor goes through
[`gs reassign-if-unclaimed`](docs/reference/gs/reassign-if-unclaimed.md), flags
before the request: it retires and replaces the request under one guard, and a
claim in between refuses the pair.

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

`gs artifact --head <commit> --promise <your promise> --report <path>
--text <what was met> <path…>`

One artifact per path the head changed, each on that one promise, the
reporting artifact last, its text stating the tests and conditions met. It is
the implementation report: no `ready-for-review` follows. Add `--rests-on`
for the behaviour a page documents, or it could never flare. A republish
retires the earlier head's artifacts at the paths it names, warning about
the rest.

It refuses an abbreviated head, a promise not yours, retired, or resting on
no request, a path the head did not change or named twice, and a second
promise as an extra basis. A changed path no artifact names is a warning.

## 5. Request review

`gs review-request --head <commit> --to <another actor> [--replace]`

It rests on every live artifact of yours at that head and names the reporting
artifact, which is how a verdict binds. It refuses you as the reviewer, a head
no artifact of yours stands at, a mixed-head set, and a second request
for the same promise unless `--replace` retires the first.

## 6. Review, when you are the reviewer

Promise the review, then `gs review --artifact <reporting artifact>
[--artifact <other>…] --promise <your promise> --checkout <clean checkout at
that head> --verdict approved|changes-requested --text-file <file>`, the
reporting artifact first.

It refuses a retired artifact or promise, another actor's promise, a head you
implemented, a dirty checkout or one not at the exact commit, and
unacknowledged head news. Staleness does not stop a review; the verdict
records what had moved.

Every implementation review records three conclusions before approval.

- **Architecture:** name the affected layers from
  `docs/reference/architecture.md` and whether the head preserves or changes
  their contract. A contract change must update that page in the same
  head and publish its artifact there; otherwise request changes.
- **Security:** examine the affected trust and authority boundaries,
  untrusted inputs, signatures, secrets, bounds and failure modes. Any
  unresolved defect is `changes-requested`. Work resting on an
  authority-bearing request chain needs its
  [four facts](docs/concepts/decision-authority.md#the-authority-bearing-request-chain)
  confirmed from the durable record.
- **Simplification:** say what could be simpler without weakening the
  conditions of satisfaction, and request changes to cut the fluff.

A `changes-requested` verdict leaves the implementation open and authorizes
no merge; ratification closes the review. Repair
the head on the same request and promise while the outcome, conditions,
performer, destination and authority are unchanged: cite the finding in the
new artifact, republish every changed path, and request review again. Work
that adds a separate outcome, or changes those conditions, the authority or
the performer, needs a child request first.

## 7. Land

`gs land --approval <approval> --checkout <target checkout> --text
<plain-language description> --cleanup`

It ratifies the approval when you are the review requester, runs `gs merge`'s
locked transaction, pushes the target ref, and with `--cleanup` removes the
worktree and branch, but only once `git merge-base --is-ancestor` proves the
head is in the target. `--checkout` is the target checkout, not the
candidate's worktree. It refuses an unratified approval you may not ratify, a
retired or `changes-requested` verdict, a checkout that is not on the
request's target ref, and a dirty one. Write `--text` for a reader with no
event ids: who proposed, ratified and reviewed, what was raised, how it was
resolved.

The merge publishes a successor at each changed path, retires the
predecessors it may, and seals every other covering pointer as carried,
sibling or abandoned;
[`gs merge`](docs/reference/gs/merge.md#artifact-succession) states those
rules. Only an abandoned one owes cleanup, by its author or a `ratifier`. A
[held landing](docs/reference/gs/merge.md#held-landings-and-the-compatibility-window)
needs its owner's release first. If the target already has the approved head,
run it anyway: `gs merge` records one incorporation receipt and creates no
merge commit, while `gs land` still pushes and cleans up.

The sealed receipt closes the commitment, so no report and no ratification
follow it. Work that resolves without landing closes through an explicit
`report` and the requester's ratification.

## 8. Clean up

Whatever `--cleanup` reports instead of deleting, a moved branch or a remote
tip carrying other work, is yours to finish. Never leave a stale worktree:
finishing includes the review and the landing.

## Whatever you write down

Cite the canonical full event identifier, copied from the result that returned
it; say `#N` in prose, checkable by eye. `#N` and an
unambiguous hash fragment resolve at every signing boundary except a commit
trailer. Cite files as `path@commit` at the revision you read, never copying a
document into an event. Before choosing a kind for `gs state`, read
`status.durable.vocabulary.definitions`: it decides required fields,
admissible bases and who may ratify. A request states `body.to`,
`body.conditions`, and what it owes: `target_ref=refs/heads/<branch>`,
`target=inherit`, or `no_git_artifact=true`; one stating no result is refused,
and the boundary measures the target itself.

Treat GitHub issues and reviews like ephemeral chat: promote what
crystallizes, with the quoted frames as evidence and the URL as a hint. Cite a
pull request by its head commit, never rest a durable act on a bare URL, and
put no secret in either channel. Keep routine progress and dead ends
ephemeral. Promote a breakdown that changes scope or a condition of
satisfaction, or creates follow-up work, and record a material blockage as an
`assert` on the promise. Superseding your own promise is reneging, and early
reneging is honourable. Never sign as another actor. Leave a log a stranger
could audit.
