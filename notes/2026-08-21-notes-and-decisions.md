---
date: 2026-08-21
status: draft/discussion — design basis for a feature request. Nothing
  here is built. Sections marked **exists** cite what main carries.
origin: hugh's 2026-08-21 discussion of ADR use cases for the workroom,
  and the visibility problem when only some of a team has adopted
  gitseq. Builds on an earlier agent note proposing that an ADR is an
  ordinary Markdown file and gitseq records the relationships around it.
---

# Notes and decisions

A decision record should be an easy fit for gitseq. What makes it hard
is not the model but the seam: the file is checked in and visible to
everyone; the acts around it live in the workroom and are visible only
to people who have adopted gitseq. For one developer that is fine. For a
team it means the traceability exists only if everyone is in, and the
cost of getting everyone in is exactly the cost we are trying to avoid.

This note records a design that keeps the model an earlier note got
right, and adds three things: a hook that files the acts so nobody
types them, a replacement link that holds by construction, and a rule
that the derived story lands where people already look.

## The model, unchanged

A decision is an ordinary Markdown file in git. Gitseq never learns
what an ADR is. It records:

```text
artifact(path, commit)     an immutable identity for one revision
ratify(artifact)           the decision is accepted, and is now a basis
Rests-On: <artifact>       an implementation commit depends on it
successor at the same path a revision
```

No second identifier, no status field the log reads, no artifact id
inside the file. A gitseq-aware reader goes `HEAD:path → artifact(path,
commit) → lineage → everything resting on it`. A reader without gitseq
has `git log -- path` and the file.

This is already the discipline in `AGENTS.md`: self-initiated work
"rests directly on the motivating ratified decision". A decision record
is that ratified decision given a path. Nothing new is being asked of
the fold.

## Notes and decisions are one process

The same mechanism applies to everything in `notes/` — feature
discussions, position papers, ideas — not only to records that end in a
decision. The workroom records the same fact for all of them: *this path
at this commit is something you can rest on*. The only difference
between a note and a decision is whether anyone ratified the artifact,
and ratification is already a separate act, not a property of the file.

```text
push        → artifact            a published revision        (hook)
ratify      → accepted            it is now a basis           (a person or agent with the role)
Rests-On    → implementation depends on it
push again  → successor artifact  a revision; ratify again, or not
supersedes: → replacement         forward link, by construction
```

So an ADR is a note somebody ratified, and a position paper that turns
into a decision does not move directories. Keep one directory or two
for human navigation; the hook does not care.

What to drop from the current `notes/` convention: the revision
narrative in `status:` ("tenth wave", "revised after adversarial
review"). The artifact chain is that history, and `gs status` at the
path renders it.

## Where the derived story lands

Three surfaces were considered for the action log — who proposed, who
reviewed, what was raised, how it was resolved, who ratified:

- **Git notes.** Not fetched by default, not shown on GitHub, not read
  by anyone. Anything that would go there is derivable. Rejected.
- **A checked-in index or status block.** Every decision rewrites it,
  which is the `.`-mutex problem from `AGENTS.md` under another name.
  Rejected.
- **The merge commit message.** `gs merge --text` is already required
  (**exists**: `docs/reference/gs/merge.md`). It travels everywhere for
  free — `git log`, GitHub, Jira commit links — and it is the one place
  the action log reaches people without keys. Chosen.

So the skill's job is to say what goes in that text: plain English for
people who will never see an event id, written from the action log by
whoever runs the merge, which is usually an agent. The log stays the
authority; the message is a render, and a render that turns out wrong
corrupts nothing.

For a team on GitHub the connector's existing *render* direction
(**exists**: `docs/concepts/connectors.md`) can open a PR from the note
and comment the verdict and the merge. Render is never observed back, so
the PR body never becomes authority. Non-adopters' GitHub approvals
arrive as observations and a keyed member ratifies. That costs one keyed
person per decision, which is unaffordable at code-PR volume and cheap at
decision volume — which is why decisions are the right wedge for partial
adoption. The connector work is a follow-up, not part of the first cut.

## Three tiers of adoption

| tier | who | needs | what a non-adopter sees |
|---|---|---|---|
| 0 | one developer | `gs`, a clone-local workroom, the hook | the file, `Rests-On:` trailers, the merge message |
| 1 | a team on GitHub | a shared resident, the connector render | an ADR-PR with the verdict and merge commented |
| 2 | a team on Jira or Linear | the same render contract, another target | commit-linked summaries (tier 0), until someone asks |

The hook is portable between tiers; the log is not. A clone-local log
cannot be merged into a team log. What saves the solo developer's
history is that every artifact statement is re-derivable from git —
`path + commit` — so "we adopted gitseq as a team" is a replay script
over `git log -- notes/`, not a migration.

## Configuration

None of this exists yet. `gs` takes `--repo`, `--as` and `--server` per
call plus `GITSEQ_ACTOR`; there is no tracked config and no `git config
gitseq.*`. Two files, split by who owns the fact:

| fact | where | why |
|---|---|---|
| which paths the hook watches | tracked `.gitseq` at the repo root | a property of the repo, reviewed like code, visible to non-adopters |
| who I am, where I write | `git config gitseq.actor`, `gitseq.server` | per clone, per person, never committed |

The tracked file is declarative and tiny:

```text
# .gitseq
watch notes/**.md
watch docs/decisions/**.md
```

No kinds, no statuses, no per-path rules. `gs hook install` refuses to
run without a `.gitseq`, so nobody gets a silent no-op hook. The existing
flags stay; `git config` only supplies defaults.

## The hook

`gs hook install` writes one line into `.git/hooks/pre-push`, honouring
`core.hooksPath`:

```text
exec gs hook run pre-push "$@"
```

The shim is dumb so that upgrading `gs` upgrades the behaviour. Hooks
live in the common dir, so one install covers every worktree; an agent's
`request/<slug>` worktree is hooked without doing anything.

`pre-push`, not `post-commit`. A draft reworked five times would get
five artifacts from `post-commit`, each succeeding the last: signed
noise that teaches people to ignore the chain. `pre-push` fires once per
head you meant others to see.

`gs hook run pre-push` reads git's stdin (`local-ref local-sha
remote-ref remote-sha`) and for each pushed head:

1. Lists the files changed in `remote-sha..local-sha` (or `local-sha
   --not --remotes` for a new branch) that match a `watch` glob.
2. Files `artifact(path, head)` for each, with idempotency key
   `artifact:<path>@<head>`. A re-push is a no-op; a force-push makes a
   real successor. One artifact per published revision, never per
   commit.
3. Takes `rests_on` from the commit's `Rests-On:` trailers. The commit
   message is already where that discipline lives; the hook copies it,
   so the artifact cannot rest on something the commit did not name.
4. Signs as `gitseq.actor` and writes through `gitseq.server` or
   locally — the same resolver as every other command.
5. Never blocks a push. A missing actor, an unreachable server, a fold
   refusal: print one line and exit 0. `--strict` is opt-in for a team
   that wants the push refused.

Agents should install it too, and not for convenience. The two
breakdowns this repository has already recorded — a hash transcribed by
hand into an artifact, and an artifact resting on the request instead of
the promise — are both things a hook that reads the sha and the trailers
from git cannot do. An agent typing `gs state --body commit=…` by hand
is the defect surface.

## Revision and replacement

Two cases, distinguished by meaning, and the skill states the rule in
one sentence: *amend in place while it is the same decision; when the
decision changes, write a new file and stamp the old one.*

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

A git reader opens the old file and sees it was superseded and by what —
the convention ADR readers already expect. The decision's reasoning is
not rewritten; only the header moves.

`supersedes:` is the only front-matter key the hook reads. From it and
the diff, in this order:

- **N** = `artifact(new path, head)`, `rests_on` from the commit
  trailers. N does *not* rest on the old artifact. If it did, the next
  step would retire its basis and N would be stale from birth.
- Then at the old path, by what the diff shows:
  - old file **modified** (the stamp): **S** = `artifact(old path,
    head)`, `rests_on = [N]`. Same-path succession retires the old
    artifact, and its lineage now points forward to N. Everything
    resting on the old decision flares once, at S, and the flare names
    the replacement.
  - old file **deleted**: a bare `gs supersede` of the old artifact,
    resting on N.

The link holds because N is filed first and S names it; there is nothing
to check afterwards. The one thing that is a check is the mirror: if
`supersedes:` is present and the old file is untouched, the hook files S
at `(old path, head)` anyway — the statement is still true, the file just
did not change in that commit — and warns that the git-visible stamp is
missing. The gitseq link is by construction; the stamp is a courtesy the
hook nags about.

Two consequences:

- The stamp beats deletion for authority reasons. Any actor can state a
  successor artifact at a path, but a bare supersede is admitted only
  from the old artifact's author or a `ratifier`. Modifying the old file
  lets anyone replace anyone's decision through ordinary review;
  deleting it needs the operator.
- Paths in files, ids in the log. The file never names an event id, so
  there is no follow-up commit and no mistyped hash; the hook resolves
  the path to the live artifact at push time. That is the anchoring
  causal problem (`docs/anchoring.md`) solved in the direction it wants
  to go.

When implementation forces a replacement: the implementer files a child
request resting on their own promise, the replacement goes through its
own ratify-and-merge, and the implementation is re-based to rest on N.
The implementation review then names N. That is the loop that catches
"we built it and learned the decision was wrong" without silently
rewriting the record.

## The merge, step by step

Actors: **author**, **reviewer** (keyed), **ratifier** (operator or
holder of the role; may be the reviewer), **merger** (whoever runs `gs
merge`; often the ratifier). Any of them may be agents.

| step | who | does | what they see | what a non-adopter sees |
|---|---|---|---|---|
| 1 | author | commits the note on a branch, pushes | the hook files the artifact; `gs status` shows it at the path | a branch with one Markdown file |
| 2 | author | review request resting on the artifact, `to=@reviewer` | board row: open request, waiting on reviewer | (tier 1) an ADR-PR opened by the connector |
| 3 | reviewer | promise, then `gs review` → `approved` or `changes-requested` | board row: verdict | (tier 1) a PR comment rendered from the verdict |
| 4 | author or ratifier | `gs ratify` the verdict | `gs merge` will now accept it | nothing yet |
| 5 | merger | `gs merge --candidate <head> --approval <verdict> --text "<summary>"` | receipt; the artifact retired and republished at the merge commit; flares on anything that rested on the draft | the merge commit on `main` whose message is the summary; (tier 1) the PR closed as merged with a final comment |
| 6 | merger | delete the worktree, push | `main` advanced | the same |

Implementation should rest on the *post-merge* artifact, not the draft
one; otherwise it flares at step 5. This is the one place a careful
person would guess wrong, so the skill says it plainly.

Whether the decision's PR is the code PR: no. The decision merges first;
implementation rests on the merged artifact. Otherwise the artifact
cannot exist until the code lands and `Rests-On:` points at nothing
during implementation. The exception is the replacement loop above,
which is a second decision cycle, not a code PR carrying a decision.

## Scaling

Nothing new. The exact-path rule in `AGENTS.md` gives one artifact per
note path, succession at the same path, and an index that is a
projection (`gs status` filtered to the directory) rather than a file
every decision rewrites.

## What to build

One request, because the parts are not useful apart:

1. `.gitseq` parser and `git config gitseq.*` defaults in `cmd/gs`.
2. `gs hook install` and `gs hook run pre-push`, with the derivation
   above. Tests: one artifact per pushed head; idempotent re-push;
   force-push succeeds; `supersedes:` with stamp, with deletion, and
   with the stamp missing; `Rests-On:` trailers copied; never blocks
   without `--strict`.
3. A "Notes and decisions" section in `SKILL.md`: the lifecycle table,
   the one-sentence path rule, what goes in `--text`, and "rest on the
   merged artifact". This belongs in the skill, not `AGENTS.md`, which
   governs development of gitseq itself.

Deferred until someone misses them: the connector render for
decision-PRs, a `gs decision replace` helper that writes both front
matter lines, and any tier-2 target.

## Open questions

- Whether the hook should also copy `Rests-On:` from the *note's* front
  matter when a note rests on another note, or whether the commit
  trailer is the only place. The trailer keeps one source; front matter
  is what a human would write. Leaning to trailer only.
- Whether `watch` globs should exclude by default anything not under the
  listed paths or whether a second verb is needed. Leaning to no second
  verb.
