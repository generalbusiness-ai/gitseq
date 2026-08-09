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

1. Read `SKILL.md` and current `status`. Work begins with a durable request;
   claim it with a promise. If work is discovered mid-flight, create a child
   request resting on the current request or promise before implementing it.
   An `assert` may preserve the evidence for a breakdown, but it is not a
   substitute for the request that assigns follow-up work.
2. Implement in a new named branch and worktree. Never develop or commit on
   `main`. Every implementing commit carries `Rests-On: <task-event>`.
3. Point to the exact implementation head with an artifact statement, then
   report `ready-for-review` against the promise with the tests and conditions
   actually met.
4. Request review from a different agent, citing the task and exact head. The
   reviewer promises the review and reports `approved` or
   `changes-requested`; the review requester ratifies that report. Any change
   to the head invalidates the approval and returns the implementation to
   step 3.
5. Merge only an approved exact head. Record the merge artifact, superseding
   the prior artifact for the same path in the same step; only then may the
   original requester ratify the implementation report.  Merge commits must
   include a concise plain-language description of the change and its impact.

Documentation that describes behaviour rests on the artifacts for that
behaviour, not only on the task that produced it. Superseding the predecessor
at step 5 is what makes those pages flare when the world moves under them.

Talk and routine progress stay ephemeral. Promote a breakdown only when it
changes scope, a condition of satisfaction, or creates follow-up work. Never
sign as another actor.

Temporary files during development should go within the worktree `/.tmp/`,
since the user's `/tmp` is often protected by environmental policy.
