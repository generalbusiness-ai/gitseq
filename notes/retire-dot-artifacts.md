# Retire the whole-repository artifacts

2026-08-21. **Status: runbook — a one-time migration, not part of the ordinary
work loop.** `AGENTS.md` states the rule this enforces — never record an
artifact at `.` — and points here. Everything below is the procedure for
clearing the artifacts recorded at `.` before that rule existed. It lives here
so that the file every agent reads at the start of every session does not carry
a runbook that runs once. The numbered steps below are this runbook's own
steps, not the numbered work loop in `AGENTS.md`.

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

2. Wait for a quiet wave. Retirement propagates ordinary staleness into every
   review chain that rests on one of these artifacts. `gs review` and `gs merge`
   record that reasoning movement rather than refusing it. Artifact provenance
   is narrower: an approval or artifact that describes a superseded world still
   cannot merge and needs a current behaviour anchor, not another review of the
   same chain. A verdict signed during the retirement wave creates avoidable
   repair even where it can eventually merge, so wait until the first verdicts
   and approved out-of-main heads are settled.
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

   jq -r '.projection as $p
     | ([$p.statements[]
          | select(.describes_superseded_world // false)
          | .event] | map({key: ., value: true}) | from_entries) as $world
     | [$p.reviews[]?
           | select(.verdict == "approved" and (.ratified // false))
           | select((($world[.report] // false) or
                     ($world[(.artifact // "")] // false)) | not)
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

   Only an approval that could still be acted on counts. `gs merge` records
   ordinary reasoning staleness, but refuses a retired approval or an approval
   or primary artifact that describes a superseded world. That head is waiting
   for a current behaviour anchor, not for a fresh verdict on the same chain.
   Counting those makes the precondition unreachable, which is the one
   failure a gate on an irreversible step cannot have: it would either block
   for ever or teach the operator to run the migration anyway. Measured
   against the live log, dropping them removes the world-stale approval of head
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
