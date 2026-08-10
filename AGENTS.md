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
5. Merge only an approved exact head. Record the merge artifact, superseding
   the prior artifact for the same path in the same step; only then may the
   original requester of assigned work ratify the implementation report.
   Self-initiated work has no report to ratify; the ratified review approval
   authorizes the merge and the merge artifact closes it. Merge commits must
   include a concise plain-language description of the change and its impact.
6. After a worktree is merged, delete it.
7. After a change to main, ensure that it is pushed to origin.

Documentation that describes behaviour rests on the artifacts for that
behaviour, not only on the request that produced it. Superseding the predecessor
at step 5 is what makes those pages flare when the world moves under them.

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
