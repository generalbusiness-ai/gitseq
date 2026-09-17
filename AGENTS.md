# Agent Instructions

Recti diligunt te (in Canticis, sponsa ad sponsum)

Simplicity is more valuable than features.
Clear and simple communication is a part of the work; a stable projection of the events that produced the current state.
The project should build using its own discipline.

慎勿放逸

## Repository work

Use gitseq requests to track all tasks in this project.

User-facing notes, documentation and other communications prefer plain English,
per ISO 24495-1, for a technical audience.

`SKILL.md` is the working loop and the command each step takes; read it, and
`gs work --next`, at the start of every cycle. The steps below are that loop
as it applies here.

1. **Orient and answer.** `gs work --next`. Every request addressed to you
   gets a `gs promise <request>`, an `assert` resting on it that declines, or
   an `assert` saying why it is not yet actionable. A decline also asks the
   request's author or a `ratifier` to retire it; a stale request asks its
   author to refile on current bases.
2. **Implement** on a new `request/<slug>` branch and worktree, unless the
   request records a better prefix. Never develop or commit on `main`. Every
   implementing commit carries `Rests-On:` — the request for assigned work,
   the motivating adopted decision for work you began yourself.
3. **Publish the artifact set** with `gs artifact`: one artifact per changed
   path at the exact head, the reporting artifact last, stating the tests and
   conditions actually met. It is the implementation report, so file no
   duplicate `ready-for-review`. Documentation also rests on the artifacts for
   the behaviour it describes, not only on the request that produced it.
4. **Ask a different agent for review** with `gs review-request`. The reviewer
   promises it and files `gs review`. Before approval, every implementation
   review records three conclusions:

   - **Architecture:** name the affected layers from
     `docs/reference/architecture.md` and say whether the exact head preserves
     or changes their contract. A contract change must update that page in the
     same head and publish its artifact there; otherwise request changes.
   - **Security:** examine the affected trust and authority boundaries,
     untrusted inputs, signatures, secrets, bounds and failure modes. Request
     changes for any unresolved security defect.
   - **Simplification:** identify any opportunity to simplify without
     weakening the conditions of satisfaction. Request changes to cut the
     fluff.

   Any change to the head invalidates the approval and returns the work to
   step 3.
5. **Land** the approved head with `gs land --approval <approval> --checkout
   <main checkout> --text <description> --cleanup`. It ratifies the approval
   as review requester, merges with succession, pushes `main` to origin, and
   deletes the worktree and branch once Git proves the head landed. Write the
   description in plain language: the change and its impact. Work that
   resolves without landing closes through an explicit report and requester
   ratification, or through supersession.

A merge records ordinary staleness in its receipt and lands the exact approved
head; it refuses a head that already described a superseded world when the
verdict was signed. When it refuses, publish an artifact on current
implementation bases rather than repeat review on the old chain.

Artifacts recorded at `.` or at a comma-joined path before those paths were
refused are still live, and a document resting on one can never flare.
Retiring them is a one-time migration with its own durable request; the
procedure and its gate are in
[notes/retire-dot-artifacts.md](notes/retire-dot-artifacts.md).

Talk and routine progress stay ephemeral. Promote a breakdown only when it
changes scope, a condition of satisfaction, or creates follow-up work. Never
sign as another actor.

## Worktree discipline

All feature work must be done on its own worktree.

When work is completed and merged, its original worktree and branch must be
deleted. `gs land --cleanup` does it; anything it reports instead of deleting
is yours to finish.

NEVER leave stale worktrees lying around.  You are responsible for finishing
work that you undertake, including all necessary reviews, until completion.

## Leased activity

Presence status and focus are advisory, session-bound attention. They are not a
promise, claim, report, authorization, or completion signal. Set `busy` with
the durable events you are actively handling; publish `waiting` or `blocked`
immediately when either becomes true; clear focus and return to `available`
when leaving the work. Lease expiry clears both automatically.

Keep routine failed tests and exploratory dead ends ephemeral. Record a
material or session-surviving blockage as an `assert` resting on the promise.
A correction that meets an existing condition stays on the live commitment;
file a child request before work that adds a separate outcome or changes the
conditions, authority or performer. Supersede your promise only when you are
withdrawing it.
