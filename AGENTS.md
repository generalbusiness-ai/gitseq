# Agent Instructions

Recti diligunt te (in Canticis, sponsa ad sponsum)

Simplicity is more valuable than features.
Clear and simple communication is a part of the work; a stable projection of the events that produced the current state.
The project should build using its own discipline.

慎勿放逸

## Repository work

Use gitseq requests instead of GitHub issues.

User-facing notes, documentation and other communications prefer plain English,
per ISO 24495-1, for a technical audience.

1. Read `SKILL.md` and current `status`. Work assigned between actors begins
   with a durable request; claim it with a promise. If work is discovered
   mid-flight, create a child request resting on the current request or
   promise before implementing it. An `assert` may preserve the evidence for
   a breakdown, but it is not a substitute for the request that assigns
   follow-up work.

   Self-initiated work — requester and performer the same actor — carries no
   self-request, self-promise, or self-report: the implementing commit rests
   directly on the motivating ratified decision, and the durable filing is
   the artifact plus the review request only. A commitment loop between one
   actor and itself keeps no promise the log needs. The tradeoff is that this
   path shows no in-flight commitment row, so nobody can see from the board
   that the work is underway.
2. Implement in a new `request/<slug>` branch and worktree unless the request
   records a better prefix. Never develop or commit on `main`. Every
   implementing commit carries `Rests-On:` — the request event for assigned
   work, the motivating ratified decision for self-initiated work.
3. Point to the exact implementation head with an artifact statement. For
   assigned work, then report `ready-for-review` against the promise with the
   tests and conditions actually met; self-initiated work proceeds straight
   to review.
4. Request review from a different agent, citing the exact head and what
   governs it — the request for assigned work, the motivating decision and
   the artifact for self-initiated work. The reviewer promises the review
   and reports `approved` or
   `changes-requested`; the review requester ratifies that report. Any change
   to the head invalidates the approval and returns the implementation to
   step 3.

   Before approval, every implementation review records three conclusions:

   - **Architecture:** name the affected layers from
     `docs/reference/architecture.md` and state whether the exact head
     preserves or changes their contract. A contract change must update that
     page in the same head and publish its candidate artifact at that head;
     otherwise request changes.
   - **Security:** examine the affected trust and authority boundaries,
     untrusted inputs, signatures, secrets, bounds, and failure modes. State
     the result and request changes for any unresolved security defect.
   - **Simplification:** identify any opportunity to asimplify, without
     weakening the conditions of satisfaction. Request changes to cut the fluff.
5. Merge only an approved exact head. In the same step, retire every live
   artifact that covers what the merge changed and publish a successor at the
   path each area keeps using, by the rules below. Only then may the original
   requester of assigned work ratify the implementation report. Self-initiated
   work has no report to ratify; the ratified review approval authorizes the
   merge and the merge artifact closes it. Merge commits must include a
   concise plain-language description of the change and its impact.
6. After a worktree is merged, delete it.
7. After a change to main, ensure that it is pushed to origin.

Documentation that describes behaviour rests on the artifacts for that
behaviour, not only on the request that produced it. Superseding the live
predecessors at step 5 is what makes those pages flare when the world moves
under them.

Never record a merge artifact at `.`. A path that every merge rewrites is a
global mutex: everything anchored to it flares whenever anyone merges
anything, and a flare carrying no information teaches people to ignore the
flares that do. `git diff --name-only <merge>^1 <merge>` lists the files the
merge changed; a merge that changed nothing in an area publishes and
supersedes nothing there.

Paths match as exact strings. The projection keys artifacts by the path field
alone: no normalising, no prefix matching, no globbing. `internal/workroom`
and `internal/workroom/fold.go` are unrelated paths to it, so an artifact at
one never reaches a predecessor at the other, and the old artifact — with
everything resting on it — stays silent. The paths are therefore not free to
choose.

Retiring and publishing are two separate decisions, and keeping them apart is
what makes the choice deterministic. Retire every live artifact that covers
what the merge changed, at its exact string, found in `gs status`. Publish a
successor only at the path the area will keep using:

Live means not retired. Stale is a different fact and does not do this job: it
says a basis underneath the artifact moved, not that the pointer was withdrawn,
so a stale artifact still occupies its path and still counts as a predecessor
the successor must retire. The two read alike on a status page — both mean "not
the current one" to someone looking for what is current — which is exactly why
they are easy to conflate, and conflating them leaves predecessors standing
while the report says the path is clear.

- One live path covers the change: publish the successor at that exact
  string.
- Two live paths cover the same changed file, a directory and something
  inside it. The wider path wins: publish the successor at the directory, and
  retire the narrower artifact with a bare `gs supersede` whose reason names
  the surviving path. Nothing is published at the narrower string again, so
  the area settles on one granularity instead of carrying both for ever.
- No live artifact covers the change: this is a first artifact, with no
  predecessor to retire. Pick the granularity a reader would cite, a package
  directory or a document, and keep that string stable, because the next
  merge in that area must match it.
- The merge renamed or deleted a tracked file that has a live artifact at its
  old path: retire that artifact with a bare `gs supersede` naming the merge
  commit, and never publish at the old string again. A rename then publishes
  a first artifact at the new path. A deletion publishes nothing, and the
  flare tells whoever rested on the old artifact to re-anchor on wherever the
  behaviour now lives, or to drop the basis if it is gone.

A bare supersession is how a path is retired with no successor. `gs supersede`
rests on its target by itself, and the fold admits it only from the artifact's
own author or from an actor holding `ratifier`. If another actor recorded the
artifact you have to retire, ask an actor with that role; never sign as them.

One path per artifact. A comma-joined pseudo-path such as `AGENTS.md,SKILL.md`
is one string that no real predecessor and no real successor can ever equal,
so it flares nothing and nothing flares it. Record two artifacts instead.

Nothing replaces the whole-repository pointer. What main carries now is a
question for git, `git rev-parse main`; per area it is the live artifact at
that path, which names the merge commit that last changed it. A single record
that every merge must rewrite would be the same mutex under another name,
whatever we called it.

The artifacts already recorded at `.` are a silent omission while they stay
live: no merge will ever supersede at `.` again, so a document resting on one
can never flare however far the code moves underneath it. Retiring them is a
one-time migration with its own durable request, and its procedure, its
preconditions and the gate that proves it finished are in
[notes/retire-dot-artifacts.md](notes/retire-dot-artifacts.md).
Merge artifacts recorded at comma-joined pseudo-paths have the same defect and
take the same repair.

Talk and routine progress stay ephemeral. Promote a breakdown only when it
changes scope, a condition of satisfaction, or creates follow-up work. Never
sign as another actor.

## Leased activity

Presence status and focus are advisory, session-bound attention. They are not
a promise, claim, report, authorization, or completion signal. Set `busy` with
the durable events you are actively handling; publish `waiting` or `blocked`
immediately when either becomes true; clear focus and return to `available`
when leaving the work. Lease expiry clears both automatically.

Keep routine failed tests and exploratory dead ends ephemeral. If a blockage
is material or must survive the session, record an `assert` resting on the
promise. If repair work is needed, create a child request before implementing
it. Supersede your promise only when you are withdrawing it.
