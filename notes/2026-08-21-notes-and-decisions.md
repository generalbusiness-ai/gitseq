---
date: 2026-08-21
status: draft/discussion — design basis for a feature request. Nothing
  here is built. Statements marked **exists** cite what main carries.
origin: hugh's 2026-08-21 discussion of ADR use cases for the workroom,
  and the visibility problem when only part of a team has adopted
  gitseq. Builds on an earlier agent note proposing that an ADR is an
  ordinary Markdown file and gitseq records the relationships around it.
---

# Notes and decisions

A decision record should be an easy fit for gitseq. What makes it hard
is not the model but the seam: the file is checked in and visible to
everyone; the acts around it live in the workroom and are visible only
to people who have adopted gitseq. For one developer that is fine. For a
team, the traceability exists only if everyone is in, and the cost of
getting everyone in is exactly the cost we are trying to avoid.

This note keeps the model an earlier note got right and adds three
pieces of work: an adoption process built from mechanisms this
repository already exercises daily; a publication step that files the
acts without anyone typing a commit hash; and a replacement link that a
merge seals. The three carry different risk, so they are split and
ordered, and the rest of this note is organised under that split.

## The model, unchanged

A decision is an ordinary Markdown file in git. Gitseq never learns
what an ADR is. It records:

```text
artifact(path, commit)      an immutable identity for one revision
propose → ratify            the decision is accepted, and is now a basis
Rests-On: <artifact>        an implementation commit depends on it
successor at the same path  a revision
```

Acceptance is a `propose` that rests on the note's artifact, and the
proposal is what gets ratified. There is no ratifying the artifact
itself: `KindArtifact` is declared with `SatisfierNone`
(`internal/workroom/kinds.go:174`), and `decideRatify` rules any kind
whose satisfier names nobody "statement kind is not ratifiable". The
live log holds three attempts to ratify an artifact; all three are
ineffective, with that exact reason.

The trap worth recording is the argument "nothing new is asked of the
fold". A design that needs `ratify(artifact)` asks plenty: it needs
`KindArtifact` to gain a satisfier, which is a governed Workroom
contract change and belongs in `docs/reference/architecture.md`. Either
write that change and own it, or use the proposal. This note uses the
proposal: one or two sentences — "adopt the decision recorded at
`notes/…` at commit `…`" — carrying the authority the file cannot carry
itself, at the cost of one extra act per accepted decision.

## Notes and decisions are one process

The same mechanism covers everything in `notes/` — feature discussions,
position papers, ideas — not only records that end in a decision. The
workroom records the same fact for all of them: this path at this
commit is something you can rest on. A decision is a note whose
adoption proposal somebody ratified, so a position paper that becomes a
decision does not move directories. Keep one directory or two for human
navigation; the tooling does not care.

```text
push, then publish → artifact   a published revision
propose → ratify   → accepted   it is now a basis
Rests-On:          → implementation depends on it
publish again      → successor  a revision; propose again, or not
supersedes:        → replacement candidate; the merge seals it
```

Drop the revision narrative from `status:` front matter ("tenth wave",
"revised after adversarial review"): the artifact chain is that
history, and `gs status` at the path renders it. The index is a
projection too — `gs status` filtered to the directory — never a file
every decision rewrites.

## Three parts

This is three pieces of work with different risk. Bundling them would
let the risky parts ride on the safe one, so each gets its own request
and review.

**Part one — adoption.** An ordinary Markdown file, an exact-head
artifact, an independent review, requester ratification, a merge, and a
separate ratified proposal carrying the authority. Every mechanism
exists and is exercised daily in this repository. This ships first and
needs none of the rest.

**Part two — publication and reconciliation.** How an artifact gets
filed without a person typing a commit hash. This touches identity,
failure modes and idempotency.

**Part three — replacement and merge semantics.** Making a supersession
link hold. This changes merge succession itself, the deepest layer of
the three, and waits until parts one and two have shown what the shape
needs to be.

## Part one: adoption

Three surfaces were considered for the action log — who proposed, who
reviewed, what was raised, how it was resolved, who ratified. Git notes
are not fetched by default, not shown on GitHub, and not read by
anyone; anything there is derivable. A checked-in index or status block
is rewritten by every decision, the `.`-mutex problem from `AGENTS.md`
under another name. The merge commit message wins: `gs merge --text` is
already required (**exists**: `docs/reference/gs/merge.md`), it travels
everywhere for free — `git log`, GitHub, Jira commit links — and it is
the one place the action log reaches people without keys.

So the skill's job is to say what goes in that text: plain English for
people who will never see an event id, written from the action log by
whoever runs the merge, usually an agent. The log stays the authority;
the message is a render, and a wrong render corrupts nothing.

The cycle, step by step. Actors: **author**, **reviewer** (keyed),
**ratifier** (operator or holder of the role; may be the reviewer),
**merger** (whoever runs `gs merge`; often the ratifier). Any of them
may be agents.

| step | who | does | what they see | what a non-adopter sees |
|---|---|---|---|---|
| 1 | author | commits the note on a branch, pushes, publishes | `gs status` shows the artifact at the path | a branch with one Markdown file |
| 2 | author | review request resting on the artifact, `to=@reviewer` | board row: open request, waiting on reviewer | (tier 1) an ADR-PR opened by the connector |
| 3 | reviewer | promise, then `gs review` → `approved` or `changes-requested` | board row: verdict | (tier 1) a PR comment rendered from the verdict |
| 4 | the review requester, and only them | `gs ratify` the verdict | `gs merge` will now accept it | nothing yet |
| 5 | author | `propose` adopting the decision, resting on the artifact | board row: an unratified proposal | nothing yet |
| 6 | ratifier | `gs ratify` the proposal | the decision now carries authority | nothing yet |
| 7 | merger | `gs merge --candidate <head> --approval <verdict> --text "<summary>"` | receipt; the artifact retired and republished at the merge commit; flares on anything that rested on the draft | the merge commit on `main` whose message is the summary; (tier 1) the PR closed as merged with a final comment |
| 8 | merger | delete the worktree, push | `main` advanced | the same |

Step 4 is the review requester and nobody else. `KindReport` is declared
with `SatisfierOriginatingRequester`, so a ratify from an author who did
not request the review, or from someone merely holding `ratifier`, is
ineffective. This note said "author or ratifier" until a reviewer
checked; the same class of error as `ratify(artifact)`, made twice
because after correcting one kind's satisfier I did not check the
others.

Steps 5 and 6 are the adoption proposal, and their position matters.
They are filed before the merge, on the draft artifact, because the
proposal records that *this text* was adopted. The merge then retires
that artifact and republishes at the merge commit, and the proposal
stays ratified: it named a revision, and that revision is what was
accepted.

Implementation must reach both. Assigned implementation rests on its
request, and that request's chain has to reach the ratified proposal and
the live post-merge artifact — the request cites them, so the chain is
complete without the commit naming either. Self-initiated implementation
rests directly on the ratified proposal, per `AGENTS.md`, and should
cite the live artifact too. Resting on the post-merge artifact alone, as
an earlier draft of this note said, drops the proposal — which is the
only thing carrying the authority the proposal exists to carry.

The decision's PR is not the code PR. The decision merges first;
implementation rests on the merged artifact. Otherwise the artifact
cannot exist until the code lands, and `Rests-On:` points at nothing
during implementation. The exception is the replacement loop in part
three, which is a second decision cycle, not a code PR carrying a
decision.

For a team on GitHub, the connector's render direction (**exists**:
`docs/concepts/connectors.md`) can open a PR from the note and comment
the verdict and the merge. Render is never observed back, so the PR
body never becomes authority. Non-adopters' GitHub approvals arrive as
observations and a keyed member ratifies: one keyed person per
decision, unaffordable at code-PR volume and cheap at decision volume —
which is why decisions are the right wedge for partial adoption. The
connector work is a follow-up, not part of part one.

## Part two: publication and reconciliation

Publication is an explicit act after a successful push. A pre-push hook
cannot be the thing that records: it runs before the remote accepts, so
a rejected push leaves an artifact claiming a publication that never
happened, and a non-blocking failure leaves a silent omission that an
identical retry — same commit, same idempotency key — never repairs.
Both failures are invisible, and an invisible wrong record is worse
than a missing one. So `gs publish`, or a push wrapper that runs it,
reads what the remote now has and writes through a durable outbox that
a later run reconciles. A hook may remind; it must not record.

`post-commit` is out for a different reason: a draft reworked five
times would earn five artifacts, each succeeding the last — signed
noise that teaches people to ignore the chain. One artifact per
published revision, never per commit.

For each published head, `gs publish`:

1. Lists the files changed since the remote's previous head that match
   a `watch` glob.
2. Files `artifact(path, head)` for each, with idempotency key
   `artifact:<path>@<head>`. A re-publish is a no-op; a force-push
   makes a real successor.
3. Rests the artifact on the promise when one exists, and on nothing
   otherwise. It never copies the commit's `Rests-On:` trailers: those
   name what the *commit* rests on, and for assigned work that is the
   request. A reporting artifact must rest on the promise, or the
   commitment strands and never closes — a defect this repository has
   already recorded once. A note with no commitment behind it has no
   promise, and an artifact resting on nothing is correct.
4. Signs as the identity resolved for this process, and fails closed
   when none is resolvable.

Identity is where a convenient default misattributes. `git config
gitseq.actor` would be shared by every linked worktree of a clone, and
this repository routinely has a dozen worktrees with different agents
working in them at once: a clone-wide default signs one agent's
publication as another's, silently and unrecoverably, because the
signature is the attribution. Identity stays scoped to the process or
the worktree — explicit `--as`, or `GITSEQ_ACTOR` — and refusing to
publish is recoverable where misattributing is not.

What the repository owns is tracked and tiny. `gs` today takes
`--repo`, `--as` and `--server` per call plus `GITSEQ_ACTOR`; there is
no tracked config and no `git config gitseq.*`. Add one file:

```text
# .gitseq
watch notes/**.md
watch docs/decisions/**.md
```

No kinds, no statuses, no per-path rules. Which paths are watched is a
property of the repository, reviewed like code and visible to
non-adopters; who is publishing is a property of the process, never
committed.

The reminder hook is optional, records nothing, and its installer has
to be careful in ways the first draft was not. It prints one line when a
push contains watched paths that have not been published. That is all it
does; if it is absent, missing or broken, nothing is lost but the
reminder.

Installing it means writing an executable file into someone's
repository, so:

- **Never clobber.** If a `pre-push` hook exists, append or refuse and
  say which; do not overwrite work the operator did not put there.
- **Shebang and mode.** Write `#!/bin/sh` and `0755`. A hook without
  either is silently never run, which is the failure mode this whole
  note is about.
- **`core.hooksPath` may be shared.** It can point outside the
  repository, at a directory several repositories use. Installing there
  affects all of them, so detect it and refuse rather than surprise
  someone.
- **No ambient executable resolution.** Write the absolute path of the
  `gs` that is installing, not the bare word `gs`. Resolving through
  `PATH` at push time means whatever binary happens to be first runs
  with the operator's repository and identity.

The bounded rules the publisher needs are part of part two's
specification, not decoration: what to do with refs and paths carrying
unusual bytes, renames and deletions, tags, force pushes, malformed
YAML, an act count past a ceiling, a partially-applied batch, and acts
the fold ruled ineffective. Each of those is a way the publisher can
record something untrue or nothing at all, and each needs a stated
answer before it is built.

Agents should use the publish step too, and not for convenience. Three
breakdowns of this exact shape are already on this log. A review request
cited its report as `…#git:sha1:REPORTLABEL`, a literal placeholder in
place of an event id, and had to be superseded (`git:sha1:5d26…#git:sha1:f3be69cda9eff1fa52ac6b144f29a6e21660ed25`). A report
carried the same kind of placeholder where an artifact id belonged
(`git:sha1:5d26…#git:sha1:71213ea92e1387c841819d607e72ef9dcdd4f708`). And an artifact
(`git:sha1:5d26…#git:sha1:c654df30f3a14cedaa78cf0721fea45a7ab4165e`)
rested on the request instead of the promise, which stranded its
commitment until a separate closure report repaired it. None of the three is possible for a tool that reads the sha
from git and the promise from the log. An agent typing
`gs state --body commit=…` by hand is the defect surface, and the
evidence is that the hand-typed field is the one that goes wrong.

## Part three: replacement and merge semantics

Two cases, distinguished by meaning, and the skill states the rule in
one sentence: amend in place while it is the same decision; when the
decision changes, write a new file and stamp the old one.

**Revision** — same decision, better words, or a draft being refined:
edit the file at the same path. `git log -- path` is the revision
history; the artifact chain is the published-revision history. They
differ only by commits that were never pushed, which is correct.

**Replacement** — the decision changed, including when implementation
showed it was wrong: a new file declares its predecessor by path, and
the old file gets a one-line stamp, in one commit.

```yaml
# notes/2026-09-01-a2a-revised.md
---
title: Invoke idle agents over A2A, revised
supersedes: notes/2026-08-21-a2a.md
---
```

```yaml
# notes/2026-08-21-a2a.md
---
status: superseded by notes/2026-09-01-a2a-revised.md
---
```

A git reader opens the old file and sees it was superseded and by what
— the convention ADR readers already expect. The reasoning is not
rewritten; only the header moves.

`supersedes:` is the only front-matter key the publish step reads. From
it and the diff, in this order:

- **N** = `artifact(new path, head)`, resting on the promise when one
  exists and otherwise on nothing — never on the old artifact. If it
  rested on the old artifact, the next step would retire its basis and
  N would be stale from birth.
- Then at the old path, by what the diff shows:
  - old file **modified** (the stamp): **S** = `artifact(old path,
    head)`, `rests_on = [N]`. Publishing at a path retires nothing:
    only `gs merge` or an explicit `gs supersede` retires, so on a
    branch S and the old artifact are both live and nothing flares —
    this repository has carried three live artifacts at one path at
    once. The link holds only once a merge seals it, and current merge
    succession does not preserve candidate-to-successor provenance, so
    the map from old to new must be carried explicitly into the merge
    rather than inferred from same-path publication. That merge change
    is what part three exists for.
  - old file **deleted**: still merge-sealed, never a bare supersede at
    branch time. Deletion is the case this note got wrong twice: a
    `supersede` driven by `supersedes:` front matter would let anyone
    who can push a branch irreversibly retire anyone's decision, before
    any review has read it. Front matter is branch-controlled input, and
    supersession is not reversible. So deletion carries the same signed
    replacement map into the merge as modification does, and the merge
    performs the retirement or refuses.
  - old file **untouched** while `supersedes:` is present: file S at
    `(old path, head)` anyway — the statement is still true; the file
    did not change in that commit — and warn that the git-visible stamp
    is missing.

Cross-author replacement is where this gets genuinely hard, and this
note defers the algorithm rather than gesturing at one. `gs merge`
retires only paths its signed artifact set covers, so replacing another
actor's decision needs the review to co-sign both N and S, and needs a
signed replacement map the receipt can validate. Neither exists today,
and specifying them badly is worse than saying they are unspecified.
Part three is that work; until it is done, replacing a decision authored
by someone else requires that actor or a `ratifier` to act, which is a
limitation to state rather than to engineer around.

One consequence of the stamp-over-deletion preference. Any actor can
state a successor artifact at a path, but supersession is admitted only
from the old artifact's author or a `ratifier`. Modifying
the old file lets anyone replace anyone's decision through ordinary
review; deleting it needs the operator. And paths live in files, ids in
the log: the file never names an event id, so there is no follow-up
commit and no mistyped hash; the publish step resolves the path to the
live artifact at publish time. That is the anchoring problem
(`docs/anchoring.md`) solved in the direction it wants to go.

When implementation forces a replacement: the implementer files a child
request resting on their own promise, the replacement goes through its
own ratify-and-merge, and the implementation is re-based to rest on N.
The implementation review then names N. That loop catches "we built it
and learned the decision was wrong" without silently rewriting the
record.

## Three tiers of adoption

| tier | who | needs | what a non-adopter sees |
|---|---|---|---|
| 0 | one developer | `gs`, a clone-local workroom, the publish step | the file, `Rests-On:` trailers, the merge message |
| 1 | a team on GitHub | a shared resident, the connector render | an ADR-PR with the verdict and merge commented |
| 2 | a team on Jira or Linear | the same render contract, another target | commit-linked summaries (tier 0), until someone asks |

The publish step is portable between tiers; the log is not. A
clone-local log cannot be merged into a team log. What saves the solo
developer's history is that every artifact statement is re-derivable
from git — `path + commit` — so "we adopted gitseq as a team" is a
replay script over `git log -- notes/`, not a migration.

## What to build

In the order of the split.

**Part one, now.** A "Notes and decisions" section in `SKILL.md`: the
lifecycle table, the one-sentence path rule, what goes in `--text`, and
"rest on the merged artifact". This belongs in the skill, not
`AGENTS.md`, which governs development of gitseq itself. No code.

**Part two, its own request and review.** The `.gitseq` parser, `gs
publish` with the derivation above and the durable outbox, and the
reminder shim. Tests: one artifact per published head; idempotent
re-publish; force-push makes a successor; the artifact rests on the
promise when one exists and never on the commit trailers; publish
refuses without a resolvable identity; a failed write is reconciled by
the next run.

**Part three, deferred** until parts one and two are real: the
`supersedes:` derivation and the merge succession change that seals it.
Also deferred until someone misses them: the connector render for
decision-PRs, a `gs decision replace` helper that writes both
front-matter lines, and any tier-2 target.

## Open questions

- When a note rests on another note, where does the publish step learn
  that: the commit's `Rests-On:` trailer or the note's front matter?
  The trailer keeps one source, but reading it must stay distinct from
  the request edge the publish step must never copy; front matter is
  what a human would write. Leaning to trailer only.
- Whether `watch` globs should exclude by default anything not under
  the listed paths or whether a second verb is needed. Leaning to no
  second verb.
