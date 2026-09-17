---
name: workroom
description: How to work in the gitseq workroom: the loop, and the one command
  each step takes. Normative for agent actors; the implementation must match
  this contract.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a3addbc47ccc245903a2fa424f1812df033bb081
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f5f87da91587e6702baf7f94c266d67a1e872047
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:11e8d1637ee2234cd85c7541c67df26a5fac005b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:457979e61b36a99dcc6beced07f6dca2bdddd0bd
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:688ffd2ffac3abe0b68afb8c01fdb2fd7596f671
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7afa1830c258297c82196b2fecff70ea47ae954b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fc74e676c77a9e23d65975b5cd6772de27773c66
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd7ea9e4bc9d97dd95133d999766029d1bd60cf6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177
---

# Working in the workroom

You are an actor in a shared, append-only workroom. Every durable act is
signed with your key, ordered, permanent, and visible to everyone — including
the acts the fold judges ineffective. Talk in the ephemeral channel (`say`);
commit deliberately.

Each step below takes one `gs` command. Every one of them checks its shape and
refuses before signing, naming the repair, so read the refusal instead of
working around it. `gs state`, `gs ratify` and `gs supersede` remain for the
acts no step command covers, and they refuse an act the fold would rule
ineffective. Pass `--as <you>`, or set `GITSEQ_ACTOR`, on every call.

The reasoning is elsewhere: [the work loop](docs/concepts/work-loop.md),
[agent practice](docs/concepts/agent-practice.md),
[decision authority](docs/concepts/decision-authority.md),
[staleness](docs/concepts/staleness.md). The MCP tools are one page each
under [docs/reference/mcp/](docs/reference/mcp/), and registering the adapter
is in [Configure an agent](docs/how-to/configure-an-agent.md).

## 1. Orient

`gs work --next` prints, for each row you own, the exact command that row
owes. Read it first every cycle. `gs status` gives one snapshot and a cursor,
`gs wait` follows it, and `gs inspect <event>` opens one item.

Presence — `busy`, `waiting`, `blocked`, `available`, up to eight focus
events, a short note — is advisory session attention. It is never a promise,
claim, report, authorization, or completion.

## 2. Answer every request addressed to you

Every cycle, every request addressed to you gets one of three answers.

- **Take it:** `gs promise <request>`.
- **Decline it:** an `assert` resting on the request that says you decline and
  why, plus a request to its author or a `ratifier` to retire it. Only they
  may, so the row stays open until they do.
- **Not actionable yet:** an `assert` resting on the request saying so. When
  the request is stale, name the staleness and ask its author to refile on
  current bases.

`gs promise` refuses a request addressed to somebody else, a retired request,
and a second claim over a promise it did not file; a repeat of its own claim
replays that promise instead of adding one. A promise is optional: when the
work is already done, report straight against the request, which only its
addressee may do, and only with no live promise of their own on it.

Work you begin yourself, as both requester and performer, files no
self-request and no self-promise. Rest the implementing commit on the
motivating adopted decision, publish the artifact, and go straight to review.
No row then shows the work in flight; that is what the shortcut costs.

## 3. Implement on a worktree

Work on a new `request/<slug>` branch in its own worktree, unless the request
records a better prefix. Never commit on the target branch. The target is the
request's `target_ref` — `main` unless a request or governing decision names
another. Base and recut the work on it.

Every implementing commit carries `Rests-On:` with the full canonical event
identifier: the request for assigned work, the adopted decision for work you
began yourself. The trailer takes the full identifier only, because a commit
message is no boundary anything resolves at. Everywhere else, `#N` or an
unambiguous fragment resolves before signing.

## 4. Publish the artifact set

`gs artifact --head <full commit> --promise <your promise> --report <path>
--text <what was met> <path…>`

One artifact per path the head changed, each resting on that one promise, the
reporting artifact published last. Its text states the tests and conditions
actually met. It is the implementation report, so file no separate
`ready-for-review`. Add `--rests-on` for the artifacts of any behaviour the
head documents: a page resting only on the request that asked for it can
never flare.

The command refuses an abbreviated head, a promise that is not yours or is
retired, a path the head did not change, and a second promise as an extra
basis. A changed path you named nothing for is a warning.

## 5. Request review

`gs review-request --head <commit> --to <another actor> [--replace]`

It rests on every live artifact of yours at that head and names the reporting
artifact, which is how the verdict binds to the lane. It refuses you as the
reviewer, a head no artifact of yours stands at, artifacts at mixed heads,
and a second request for the same promise unless `--replace` retires the
first in the same run.

## 6. Review, when you are the reviewer

Promise the review, then `gs review --artifact <reporting artifact first>
--artifact <every other one you read> --promise <your promise> --checkout
<clean checkout at that head> --verdict approved|changes-requested
--text-file <file>`.

It refuses a retired artifact or promise, another actor's promise, a head you
implemented, a dirty checkout, a checkout not at the exact commit, and
unacknowledged head news. Staleness does not stop a review; the verdict
records what had moved.

Before approval, every implementation review records three conclusions.

- **Architecture:** name the affected layers from
  `docs/reference/architecture.md` and say whether the head preserves or
  changes their contract. A contract change must update that page in the same
  head and publish its artifact there; otherwise request changes.
- **Security:** examine the affected trust and authority boundaries,
  untrusted inputs, signatures, secrets, bounds and failure modes. Any
  unresolved defect is `changes-requested`. Work resting on an
  authority-bearing request chain needs its
  [four facts](docs/concepts/decision-authority.md#the-authority-bearing-request-chain)
  confirmed from the durable record.
- **Simplification:** say what could be simpler without weakening the
  conditions of satisfaction, and request changes to cut the fluff.

A `changes-requested` verdict closes nothing and authorizes no merge. Repair
the head on the same request and promise while the outcome, conditions,
performer, destination and authority are unchanged: cite the finding in the
new artifact, republish every changed path at the new head, and request
review again. File a child request first for work that adds a separate
outcome or changes those conditions, the authority, or the performer.

## 7. Land

`gs land --approval <approval> --checkout <target checkout> --text
<plain-language description> --cleanup`

It ratifies the approval when you are the review requester, previews the
merge, runs `gs merge`'s locked transaction, pushes the target ref, and with
`--cleanup` removes the worktree and deletes the branch — only once
`git merge-base --is-ancestor` proves the head is in the target. `--checkout`
is the target checkout, not the candidate's worktree. It refuses an
unratified approval you may not ratify, a `changes-requested` verdict, a
checkout that is not on the request's target ref, and a dirty one. Write
`--text` for a reader who will never see an event id: who proposed, who
ratified, who reviewed, what was raised, how it was resolved.

The merge publishes a successor at each changed path and retires the
predecessors it may;
[`gs merge`](docs/reference/gs/merge.md#artifact-succession) states those
rules and seals every other covering pointer as carried, sibling or
abandoned. Only an abandoned one owes cleanup, by its author or a
`ratifier`. A [held landing](docs/reference/gs/merge.md#held-landings-and-the-compatibility-window)
needs its owner's release first. If the target already contains the approved
head, run the same command anyway: it records one incorporation receipt and
changes nothing in Git.

The sealed receipt closes the commitment, so no report and no ratification
follow it. Work that resolves without landing closes through an explicit
`report` and the requester's ratification.

## 8. Clean up

`--cleanup` deletes the merged worktree and branch; anything it reports
instead of deleting is yours to finish. Never leave a stale worktree:
finishing the work includes its review and its landing.

## Whatever you write down

Cite the canonical full event identifier, copied from the result that
returned it; say `#N` in prose, where a reader can check it by eye. Cite
files as `path@commit` at the exact revision you read, and never copy a
document into an event. Before choosing a kind for `gs state`, read
`status.durable.vocabulary.definitions`: that governed catalog decides
required fields, admissible bases, and who may ratify. A request states
`body.to`, `body.conditions`, and what it owes —
`target_ref=refs/heads/<branch>`, `target=inherit`, or
`no_git_artifact=true`; one that states no result is refused, and the filing
boundary measures the target itself.

Treat GitHub issues and reviews like ephemeral chat: participate freely, and
promote what crystallizes with the quoted frames as evidence and the URL as a
hint. Cite a pull request that matters durably by its head commit. Never rest
a durable act on a bare URL, and put no secret in either channel. Keep
routine progress and dead ends ephemeral; promote a breakdown that changes
scope or a condition of satisfaction, or that creates follow-up work, and
record a material blockage as an `assert` on the promise.
Superseding your own promise is reneging, and early reneging is honourable.
Never sign as another actor. Leave a log a stranger could audit and
understand.
