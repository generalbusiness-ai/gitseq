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
   with a durable request. Claim it with a promise to show the work is in
   flight, or, when it is already done, report straight against the request:
   the promise is how the board sees work underway, not a toll on finishing.
   Only the addressee may report directly, and not while their own promise on
   that request is live — one commitment takes one closure. Each working cycle,
   every request addressed to you gets an answer: a promise, an explicit
   decline, or an `assert` resting on the request saying why it is not yet
   actionable. Only the request's author or a `ratifier` may retire a request,
   so a decline is an `assert` stating the refusal and its reason, plus asking
   the author to retire the request; the row stays open until they do. Answer a
   stale request addressed to you the same way — name the staleness and ask the
   author to refile on current bases, confirming its conditions, the addressee's
   availability, and the governing decisions are unchanged. If work is discovered
   mid-flight, create a child request resting on the current request or promise
   before implementing it. An `assert` may preserve the evidence for a
   breakdown, but it is not a substitute for the request that assigns follow-up
   work. Self-initiated work — requester and performer the same actor — carries
   no self-request, self-promise, or self-report: the implementing commit rests
   directly on the motivating ratified decision, and the durable filing is the
   artifact plus the review request only. A commitment loop between one actor
   and itself keeps no promise the log needs. The tradeoff is that this path
   shows no in-flight commitment row, so nobody can see from the board that
   work is underway.
2. Implement in a new `request/<slug>` branch and worktree unless the request
   records a better prefix. Never develop or commit on `main`. Every
   implementing commit carries `Rests-On:` — the request event for assigned
   work, the motivating ratified decision for self-initiated work.
3. Point to the exact implementation head with an artifact statement. For
   assigned work, that artifact rests on the promise — or on the request itself
   when you made no promise — and states the tests and conditions actually met:
   it is the implementation report, so do not file a duplicate
   `ready-for-review` report. Self-initiated work proceeds straight
   to review.
4. Request review from a different agent, citing the exact head and its
   reporting artifact plus what governs it — the request for assigned work, or
   the motivating decision for self-initiated work. The reviewer promises the
   review and reports `approved` or `changes-requested`; the review requester
   ratifies that report. Any change to the head invalidates the approval and
   returns the implementation to step 3. File the verdict with `gs review`; a
   verdict filed by hand must rest on the artifact it names. Before approval,
   every implementation review records three conclusions:

   - **Architecture:** name the affected layers from
     `docs/reference/architecture.md` and say whether the exact head preserves
     or changes their contract. A contract change must update that page in the
     same head and publish its candidate artifact there; otherwise request
     changes.
   - **Security:** examine the affected trust and authority boundaries,
     untrusted inputs, signatures, secrets, bounds, and failure modes. State
     the result and request changes for any unresolved security defect.
   - **Simplification:** identify any opportunity to simplify, without
     weakening the conditions of satisfaction. Request changes to cut the
     fluff.
5. Merge only an approved exact head. In the same step, publish each added,
   modified, or renamed destination at its exact changed path and retire the
   live in-target predecessors at that exact string. A pointer wider than a
   landed destination is a separate wire: keep an in-target pointer live and
   seal it as carried, with no cleanup obligation. Seal an outside-target
   candidate as a sibling when an unsettled commitment protects it, or as
   abandoned otherwise. Removing a rename source or deleted file also changes
   its covering directories, so the plan may retire in-target directory
   pointers and publish the widest directory successor. Other live candidates
   stay sealed and unretired. Only an abandoned classification prompts cleanup,
   and the candidate's author or a `ratifier`, not the merge, retires it.
   `SKILL.md` states those succession rules in full
   and [`docs/reference/gs/merge.md`](docs/reference/gs/merge.md) tabulates
   what `gs merge` enforces. Three of them bite hardest. Paths match as exact
   strings, with no normalising, prefixes or globs, so the paths are not free
   to choose. Never record an artifact at `.`, a path every merge rewrites, so
   that everything anchored to it flares over changes it has nothing to do
   with; and never at a comma-joined pseudo-path such as `AGENTS.md,SKILL.md`,
   one string no real predecessor or successor can ever equal, so it flares
   nothing and nothing flares it. Live means not retired — stale is a different
   fact, and a stale artifact still occupies its path and is still a
   predecessor to retire. A bare `gs supersede` retires a path with no
   successor, and the fold admits it only from the artifact's own author or an
   actor holding `ratifier`, so ask that actor when the artifact is not yours.
   The sealed merge receipt is the original requester's pre-authorized
   acceptance and closes an assigned implementation commitment, so no
   implementation ratification follows it; self-initiated work has no
   commitment to close. The review approval is separate and must be explicitly
   ratified before merge. Work that resolves without a merge still closes
   through an explicit report and requester ratification, or through
   supersession. Merge commits must include a concise plain-language
   description of the change and its impact.
6. After a worktree is merged, delete it.
7. After a change to main, ensure that it is pushed to origin.

Documentation that describes behaviour rests on the artifacts for that
behaviour, not only on the request that produced it. Superseding the live
predecessors at step 5 is what makes those pages flare when the world moves
under them. Ordinary staleness says the reasoning moved, not that the immutable
reviewed head changed, and `gs merge` records it in the receipt. The narrower
`describes_superseded_world` fact crosses a direct retired-artifact edge and
then follows artifact-to-artifact provenance only. A merge judges it as of the
verdict: one the reviewer had already been shown refuses, and one the world
moved under after they signed is recorded, because their head has not changed
and they had no chance to see the move. A flagged artifact the fold cannot date
refuses. When it refuses, publish an artifact on current implementation bases
rather than repeat review on the old chain.

Artifacts recorded at `.` or at a comma-joined path before those rules existed
are still live, and a document resting on one can never flare. Retiring them is
a one-time migration with its own durable request; the procedure, its
preconditions and the gate that proves it finished are in
[notes/retire-dot-artifacts.md](notes/retire-dot-artifacts.md).

Talk and routine progress stay ephemeral. Promote a breakdown only when it
changes scope, a condition of satisfaction, or creates follow-up work. Never
sign as another actor.

## Leased activity

Presence status and focus are advisory, session-bound attention. They are not a
promise, claim, report, authorization, or completion signal. Set `busy` with
the durable events you are actively handling; publish `waiting` or `blocked`
immediately when either becomes true; clear focus and return to `available`
when leaving the work. Lease expiry clears both automatically.

Keep routine failed tests and exploratory dead ends ephemeral. If a blockage is
material or must survive the session, record an `assert` resting on the
promise. If repair work is needed, create a child request before implementing
it. Supersede your promise only when you are withdrawing it.
