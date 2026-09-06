Review for Hugh: explain a delivery once; keep its path records short

Recommendation: use one concise account of the final candidate in its implementation-reporting artifact. Keep the other required path artifacts brief and specific to their path. Put the complete artifact manifest and detailed test evidence alongside the review handoff. This changes our writing practice; it needs no new lifecycle or authority rule.

I compared three recent exact candidates in the durable histories. Chess fc3fa1f5 has 14 published path artifacts whose text begins with the identical 2,102-character delivery account. That prefix alone repeats 27,326 characters beyond its first copy. Tailapp 62c0d8f4 has 12 artifacts sharing a 1,543-character account, repeating 16,973 characters beyond the first. These are exact common-prefix measurements of event text, not estimates of time, token charges or Git storage. The Chess publication is still in progress, so the count describes the observed set, not a promised final manifest.

Gitseq 21b7e742 already provides a useful contrasting practice: one full reporting artifact and 51 much shorter path-specific descriptions. Its full report is still 11,487 characters and recounts three implementation rounds. A reviewer should first see what the final head does, the tests actually run on it, what remains unproved, and which earlier finding each correction satisfies. The immutable earlier reports already preserve the full history.

The practical handoff would contain:

- A short request title naming the outcome and exact candidate, so it remains readable in the table and graph.
- One reporting artifact explaining the final behavior, scope, validation and limitations. It cites the relevant review findings without reproducing every previous round.
- Each companion artifact at its real exact changed path, with a short statement of that path's change and the bases required by its meaning.
- An explicit manifest of every artifact the reviewer must examine, plus actual logs or fixtures for consequential claims. No same-head discovery or scope expansion is implied.

Keep the existing path and provenance rules. Do not replace the path records with a whole-repository or comma-joined artifact. Do not add dependencies merely to make the prose easier to find: that would alter staleness propagation. This recommendation removes repeated narration, not the evidence needed to test a claim.

There is a current handoff trap to respect. Several artifacts resting on one promise can change which one the projection selects as Commitment.Report. Publish the intended reporting artifact after its companions, then inspect the actual selected report before asking for review and naming the primary artifact. For combined deliveries, inspect each commitment. This is a present-process precaution, not a replacement for the already-adopted review-binding runtime guard. The recent Inventory primary/report mismatch is additional evidence that merely choosing a plausible first artifact is insufficient.

Try this writing pattern on the next delivery and assess whether an unfamiliar reviewer can find the outcome, selected report, complete scope and supporting evidence without reading repeated accounts. No extra approval form, mandatory matrix, new task state or retrospective rewriting is proposed. This is advice for Hugh to review, not an adopted workflow change.

Evidence: gitseq-artifact-prose-repetition-evidence-20260906.json lists all observed canonical event IDs, exact paths, candidate heads and character counts. The full local extraction remains at /tmp/gitseq-artifact-prose-repetition-study-20260906.json. Related existing work: Gitseq #18901 addresses evidence specificity; adopted review-binding proposal #18073 addresses report identity. This note addresses repeated delivery prose and the immediate handoff practice.
