# Captured worktree classification shape

`worktree-captured.json.gz` is a reduced, anonymized projection and immutable
Git graph from the Gitseq workroom at depth 18,635, sequence head
`fb5ae8c32421552945db2089d1b5b1b02730c870` (5 September 2026).

It retains the classifier's inputs: 2,402 commitment rows, 12,012 statements,
35 checkouts, all direct provenance associations, selected receipt fields,
and a graph of 1,248 commits. Statement order, shared requests, distinct
promises, duplicate heads, branches, receipt links, statuses and Git ancestry
are preserved. Event names, object IDs, repository names, branch names and
checkout labels are consistently replaced. Text, actor identities, paths,
signatures and attachments are omitted. No keys or local configuration are
included. The fixture does not claim that the replacement IDs are actual Git
objects or that its synthetic events would pass the workroom fold.

The original endpoint returned 35 unknown classifications because its total
inspection budget expired. The regression runs the production input index,
ref refresh, receipt measurements, membership join and output selection under
the unchanged 65,536-step budget. It compares every result against the original
exhaustive matching rules with a larger test-only budget, including primary
fields, distinct promise rows, ranking, exact omission counts and protection.
Git object loading itself is separately covered by repository-backed tests and
the actual endpoint check recorded with the implementation artifact.

Compression only keeps this structural fixture small. It is ordinary JSON
inside a deterministic gzip stream and can be inspected with `gzip -dc`.
