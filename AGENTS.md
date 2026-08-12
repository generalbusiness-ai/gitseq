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

The artifacts already recorded at `.` stay true as history: each says main
carried that commit at that moment, and retiring one does not erase it — the
log is append-only, so every one of them stays readable afterwards. But while
they are live they are a silent omission. No merge will ever supersede at `.`
again, so a document resting on one of them can never flare, however far the
code moves underneath it. Retire them once, and re-anchor the documentation
that flares. This is work like any other: it begins with its own durable
request, and the repairs in step 5 are ordinary implementing work.

Retiring the set is not a small event. These artifacts sit on the causal spine
of the log: one measurement while this was written found 1360 records of 1667
descended from one of them, so the retirement flares four fifths of the log at
once. Nearly all of
that is history that needs nothing, and the gate in step 6 is what says when
the work is actually over.

1. List them — every artifact statement whose path is exactly `.` and which
   is not already retired:

   ```sh
   gs status --repo . --json | jq -r '.projection.artifacts[]
     | select(.path == "." and (.retired | not))
     | .event'
   ```

2. Wait for a quiet wave. Retirement propagates staleness into every review
   chain that rests on one of these artifacts, and `gs merge` refuses a stale
   approval and a stale artifact, so a head approved before the migration
   cannot be merged after it without a fresh review. `gs review` is the softer
   gate — it proceeds and records the staleness in the verdict — but a verdict
   signed over a moved world is a worse thing to hand a merger than a wait.
   Run the migration when no review request is still waiting for its first
   verdict and no approved head is still out of `main`:

   ```sh
   mkdir -p .tmp
   gs status --repo . --json > .tmp/gs-status.json

   jq '.projection as $p
     | ([$p.artifacts[] | select(.retired | not) | .event]
        | map({key: ., value: true}) | from_entries) as $live
     | ([$p.reviews[]? | .report] | map({key: ., value: true}) | from_entries) as $effective
     | ([$p.commitments[]
          | select((.report // null) != null and ($effective[.report] // false))
          | .request] | map({key: ., value: true}) | from_entries) as $settled
     | ([$p.statements[]
          | select((.retired | not) and .kind == "request")
          | select((.body.artifact // "") != "")]) as $named
     | {awaiting: [$named[] | select(($settled[.event] // false) | not)] | length,
        unresolved: [$named[] | select((($live[.body.artifact] // false) | not))] | length,
        named: ($named | length)}' .tmp/gs-status.json

   jq -r '[.projection.reviews[]?
           | select(.verdict == "approved" and (.ratified // false))
           | select((.stale // false) | not)
           | select((.retired // false) | not)
           | .head] | unique | .[]' .tmp/gs-status.json |
     while read -r commit; do
       git merge-base --is-ancestor "$commit" main 2>/dev/null ||
         echo "still out of main: $commit"
     done
   ```

   Either canonical verdict settles a review. Waiting cannot turn a
   `changes-requested` into an approval, so counting only approvals leaves
   every closed-with-changes review in the total for ever.

   Only an effective verdict on this request's own chain settles it. Two
   things follow, and an earlier version got both wrong. The count reads
   `projection.reviews`, which the fold populates from reports it judged
   effective, rather than filtering `projection.statements` on `kind ==
   "report"`; statements includes what the fold refused, so a malformed or
   unauthorized report carrying `body.verdict: approved` was being counted
   as a verdict. The live log holds three such reports — one of them
   `08c337e9`, ruled `report has no promise` — and the old form admitted all
   three. And settlement is keyed by the request event through
   `projection.commitments`, not by the artifact name, because several
   requests can name one artifact and a verdict on one of them was settling
   the others. Measured against the live log, the two faults together hid
   five review requests that no effective verdict binds. A report the fold
   refused must not be able to release an irreversible migration.

   The count fails closed on a reference it cannot resolve. What makes a
   request a review request is that it names an artifact at all, not that the
   name resolves — so a request carrying an unexpanded `<artifact-event>`
   placeholder, or naming a head that was never merged, is counted as awaiting
   rather than dropped. An earlier version required the name to resolve
   through the live-artifact map, which meant a malformed review request left
   the total silently: it once reported six awaiting when at least seven were.
   A gate that can declare quiet while work is outstanding is worse than no
   gate, because the migration it releases is the one thing that cannot be
   undone by waiting longer. `unresolved` reports how many named references
   did not resolve, so the failure is visible as a number rather than as an
   absence — a non-zero value there is something to look at before trusting
   `awaiting`.

   Only an approval that could still be acted on counts. `gs merge` refuses a
   stale or retired approval, so a head held up by one is not waiting for a
   merger — it is waiting for a fresh review, and no amount of waiting moves
   it. Counting those makes the precondition unreachable, which is the one
   failure a gate on an irreversible step cannot have: it would either block
   for ever or teach the operator to run the migration anyway. Measured
   against the live log, dropping them removes the stale approval of head
   bc469129 and keeps the genuinely outstanding 00b7070d.

   A live artifact is not a merge test. The historical merges predate this
   retirement discipline and left their approved artifacts live, so the live
   set and the unmerged set are exactly the population this migration is about.
   Git is the authority: ask whether the approved head is already an ancestor
   of `main`. A commit this clone does not have counts as out of `main` —
   fetch it before trusting the silence. The head comes from the review record
   itself, which carries the exact commit the verdict was signed over; going
   through the artifact's `commit` field asked a second question whose answer
   only happened to agree.
   Never scrape forty hex characters out of event text. Every event id in this
   log contains the genesis commit, event ids are themselves commits, and
   `git cat-file -e` will confirm all of them.

   One measurement while this was written read `awaiting: 7`, and no approved
   commit was out of `main`. Both can reach zero: the open reviews close as
   their reviewers report, and the approved heads are already merged.
3. Retire them in one run, as `hugh`. More than one actor recorded these
   artifacts, and the fold admits a supersession only from the target's own
   author or from an actor holding `ratifier`. `codex` and `claude` are bare
   participants, so each can retire only its own; `hugh` is the operator, and
   an operator holds `ratifier` by implication, so `hugh` is the one actor who
   can retire the whole set. Anyone else running this step will watch every
   supersession outside their own authorship be judged ineffective.

   Generate the acts from the same selector rather than writing them out, so
   the file still covers the set on the day it is run — the set grows with
   every merge that records at `.`:

   ```sh
   gs status --repo . --json | jq '[.projection.artifacts[]
     | select(.path == "." and (.retired | not))
     | {verb: "supersede", target: .event,
        text: "Retire the whole-repository artifact; succession moves to the per-path artifacts.",
        idempotency_key: ("retire-dot-" + .event)}]' > .tmp/retire-dot.json
   gs batch --repo . --as hugh .tmp/retire-dot.json
   ```

   `gs batch` reads the whole file before anything lands and appends the acts
   in one process against one verified frontier, so the flares arrive as one
   run. It is not atomic: the acts land one at a time and the run stops at the
   first failure. The idempotency keys carry the recovery — rerun the same
   file and it replays what already landed, then continues. A supersede act
   needs no `rests_on`: `gs` puts the target first in the record's bases for
   you, which is what the fold requires.
4. Re-run `gs status --repo .` and read the flares. Do not treat the count as
   a work queue. Most of it is history: requests, promises and reports whose
   bases moved after the act was over. A stale record of a past act is honest
   and needs nothing.
5. Re-anchor the current documentation, which is the part that does need work:
   every live artifact whose chain of artifact statements still reaches `.`.
   Re-check its prose against what main now carries, correct what has moved,
   and state it again resting on the live artifacts at the exact paths whose
   behaviour it describes. The gate in step 6 counts exactly these and nothing
   else.
6. Prove it finished. The migration is done when nothing live sits at `.` and
   no live artifact still reaches a `.` artifact along artifact provenance —
   the anchor a document follows to say which behaviour it describes:

   ```sh
   gs status --repo . --json | jq -e '
     .projection as $p
     | reduce ($p.artifacts[]) as $a ({};
         . as $seen
         | if $a.path == "." then . + {($a.event): true}
           elif ((($p.provenance[$a.event]) // []) | any($seen[.] // false))
           then . + {($a.event): true}
           else . end)
     | . as $anchored
     | {live: [$p.artifacts[]
         | select(.path == "." and (.retired | not))] | length,
        anchored: [$p.artifacts[]
         | select((.retired | not) and .path != "."
                  and $anchored[.event])] | length}
     | ., (.live == 0 and .anchored == 0)'
   ```

   It prints the two counts and then the verdict, and `jq -e` exits non-zero
   while either count is above zero, so the closing report can be gated on it.
   A retired artifact still carries the anchor to whatever was stated on top of
   it, so the walk does not stop at one; only the final count is restricted to
   live artifacts. The same measurement read `live: 37, anchored: 17`, and all
   seventeen are documentation pages — `docs/why.md`,
   `docs/concepts/record.md`, `docs/reference/gs/status.md` and the rest of
   that set. That is the step 5 list, and it reaches zero when each page has
   been restated.

   The wave is a larger number, and it is deliberately not the gate. The fold
   propagates staleness along every `Rests-On` hop of every effective
   statement, not only along artifact provenance, so the retirement touches far
   more than seventeen live artifacts. Measure it, and do not gate on it:

   ```sh
   gs status --repo . --json | jq '
     def closure($children; $seeds):
       {frontier: $seeds, seen: ($seeds | map({key: ., value: true}) | from_entries)}
       | until(.frontier == [];
           . as $s
           | ([$s.frontier[] | ($children[.] // [])[]] | unique
              | map(select($s.seen[.] | not))) as $next
           | {frontier: $next,
              seen: ($s.seen + ($next | map({key: ., value: true}) | from_entries))})
       | .seen;
     .projection as $p
     | ([$p.decisions[] | select(.verdict == "effective") | .event]
        | map({key: ., value: true}) | from_entries) as $effective
     | ([$p.acts[] | select(.type == "supersede" and .verdict == "effective")
         | {key: .event, value: .target}] | from_entries) as $retires
     | ([$p.provenance | to_entries[] | select($effective[.key]) | .key as $e
         | (.value // [])[] | select(. != $retires[$e]) | {basis: ., dep: $e}]
        | group_by(.basis)
        | map({key: .[0].basis, value: (map(.dep) | unique)}) | from_entries) as $carries
     | [$p.artifacts[] | select(.path == ".") | .event] as $dot
     | closure($carries; $dot) as $reached
     | {records: ($p.decisions | length), reached: ($reached | length),
        live_artifacts: ([$p.artifacts[]
          | select((.retired | not) and .path != ".")] | length),
        reaching: ([$p.artifacts[]
          | select((.retired | not) and .path != "."
                   and $reached[.event])] | length)}'
   ```

   It follows the same edges the fold does — every basis of every effective
   record, skipping only the edge from a supersession to its own target — and
   counts only live artifacts at the end. The same measurement read 150 of 186
   live artifacts reaching `.`, out of 1360 of 1667 records reached in all.

   That number cannot be driven to zero, and gating on it would be asking for
   something no work can deliver. The `.` artifacts sit on the causal spine:
   four fifths of every record ever appended descends from one, through the
   requests, promises and reports that carried the work along. A replacement
   artifact rests on the request that authorised it, that request rests on the
   report that asked for it, and the chain runs back onto the spine — so
   re-anchoring adds to this count instead of reducing it. What it measures is
   history: finished acts whose bases moved afterwards, which is honest and
   needs nothing. What step 5 can finish is the anchor, and the anchor is what
   the gate counts. Report both numbers in the closing report, so nobody has to
   guess which one was proved.

The one-time cost is that wave of flares, most of it history left honestly
stale. The cost of not paying it is a set of documents that can never flare at
all. Merge artifacts already recorded at comma-joined pseudo-paths have the
same defect and take the same repair, under their own request.

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
