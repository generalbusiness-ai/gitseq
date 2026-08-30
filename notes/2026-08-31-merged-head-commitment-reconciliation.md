---
date: 2026-08-31
status: candidate design; no implementation is authorized by this note
origin: planner's current-basis request to stop merged implementation commitments
  remaining open until an operator notices them
---

# Reconcile implementation commitments already incorporated into main

Git reachability and Workroom satisfaction answer different questions.
Reachability says that Git can find one commit from another. Satisfaction says
that the requested work was reported by its performer and accepted through the
request's authority path. The board currently loses the first fact and must not
invent the second.

Add a repository-aware reconciliation view that finds unresolved commitments
whose implementation evidence is already present in the configured integration
head. Surface those rows as attention immediately after a merge and in normal
status reads. Keep the existing closure rule: the performer files a report and
the requester ratifies it. This drains rows without letting a Git ancestry test
declare somebody else's conditions satisfied.

This design intentionally does not add an eleventh commitment status, a new
durable kind, or automatic artifact retirement.

## Decision

The three earlier candidates are not safe or sufficient as stated.

1. **Do not let a merge receipt close every commitment on an ancestor.** A
   receipt is signed by the implementer whose exact head was approved. The fold
   deliberately bounds that receipt's cross-author authority to the reviewed
   artifact tree. Letting the signer name arbitrary ancestor commitments or
   retire artifacts at unchanged paths would turn one approval into authority
   over unrelated work. The fold also has no application repository from which
   to verify the claimed ancestry.
2. **Do not project `satisfied` from reachability alone.** A promise's `head`
   field is an advisory checkout hint. Even a reporting artifact proves only
   the commit and paths its performer named. An ancestor may be partial work,
   may have entered main through a different decision, or may fail conditions
   that are not reducible to file bytes. A byte-identical recut has the same
   limit. Satisfaction remains a signed authority decision, not a repository
   heuristic.
3. **Do not rely on a publication discipline alone.** Avoiding artifacts at
   unchanged paths reduces noise but does not expose or close an existing
   promise. The eleven 2026-08-30 repairs demonstrate that omission.

Choose a fourth shape: **machine-detected, authority-preserving
reconciliation**. Repository reachability and content equality produce
advisory evidence. The current lifecycle acts turn that evidence into a
closure. Existing rows become visible automatically, but they do not become
satisfied automatically.

## What the detector says

The detector reads one explicit integration ref, resolving it to an exact
commit for each result. The repository default is `refs/heads/main`; callers
may name another full ref. Output always includes both `integration_ref` and
`integration_head`, so a later ref move cannot silently change what an earlier
result claimed.

It considers commitments in `promised`, `stale`, `reported`, and
`awaiting-merge`. Terminal rows are excluded. Each candidate has one of these
evidence classes:

| Evidence | Meaning | May it close the row? |
| --- | --- | --- |
| `reported-head-reachable` | A live or stale reporting artifact names commit `H`, and `H` is an ancestor of the resolved integration head. | No. It supports a performer report. |
| `reported-paths-equal` | The reporting artifacts' exact paths have equal Git object kind and object ID at their reported head and the integration head. This is the bounded byte-identical-recut case. | No. It supports a performer report and must not be called reachability. |
| `head-hint-reachable` | A promise or explicit report body carries advisory head `H`, and `H` is an ancestor of the integration head, but no reporting artifact proves it. | No. It is a prompt to investigate, not completion evidence. |

`reported-paths-equal` requires every reporting artifact belonging to that
completion to match. A missing path, directory/file kind change, submodule
change, unreadable object, invalid commit, or partial match produces no equality
claim. Exact path strings are used; there is no cleaning, prefix inference, or
glob expansion.

The detector does not parse request conditions, infer that two requests are the
same, or search commit messages for ticket numbers. It does not treat a tree
hash match as proof that tests, deployment, review, or another non-file
condition was met.

## Where the result appears

Add `merge_reconciliation` beside, not inside, the durable Workroom projection.
It is a repository-aware host view over the projection and ordinary Git
storage. Each bounded row contains:

- request, promise and completion event identifiers already projected by the
  fold;
- current commitment status and ordinary staleness;
- performer and requester fingerprints;
- evidence class and the exact reported or hinted head;
- exact artifact paths used for a content comparison;
- integration ref and resolved head; and
- `next_actor`: the performer until a report exists, then the requester.

The normal `gs status`, `gs work`, MCP `status`, MCP `work`, and browser work
board show a count and bounded rows. The resident recomputes the host view when
either the durable frontier or integration ref moves. The fold and its
checkpoints remain independent of the application repository.

After `gs merge` updates the integration ref, it runs the same detector against
the new exact head and prints any newly qualifying unresolved rows. This is a
warning after the merge, not another merge gate and not a durable receipt
field. A warning failure must not make an already completed Git merge look as
though it did not happen.

## How a row closes

The sanctioned closure is the ordinary non-merge route already understood by
the fold:

1. The performer reads the original request conditions and the detector's
   evidence.
2. If those conditions are in fact met, the performer files an explicit
   `report` resting on the promise, or on the request when there was no promise.
   The report states the exact integration ref and resolved head it checked and
   why the incorporated implementation satisfies the conditions.
3. If that basis is stale, the performer uses the existing dead-basis override.
   The override records that the stale basis was seen; it does not make it
   current.
4. The original requester ratifies the report. The fold then projects the
   commitment as `satisfied`, retaining its independent stale flag.

This is the same shape planner used on 2026-08-30: performer report on the stale
promise with a dead-basis override, then requester ratification. It is the
sanctioned interim and permanent drain because it preserves both signatures
that matter. A generic ratifier does not substitute for the originating
requester when accepting a report.

The report is not automatic. Only the performer can truthfully say the
conditions were met, and only the requester can accept that report. If either
actor is unavailable, the row stays visible. The requester or a ratifier may
retire the request under the ordinary rules, but that records cancellation or
withdrawal, not satisfaction.

## A guarded convenience command

Add `gs reconcile-merged` as a thin command over those existing acts. With no
write flag it lists bounded candidates. With `--request <event> --report` it:

- requires the current actor to be the projected performer;
- resolves and prints the exact integration head again;
- repeats the request conditions and evidence class;
- refuses a partial path match or a moved integration head;
- requires `--text` describing why all conditions are met;
- requires `--allow-dead-basis` when the promise or request is stale; and
- appends one ordinary report with an idempotency key derived from request,
  promise, integration ref and resolved head.

It never ratifies. The requester uses the existing `ratify` command after
reading the report. There is no `--all --report`: bulk inference is the failure
this design is avoiding.

The MCP adapter exposes the same read and one-row report operation, including
per-call actor and repository selectors under the existing selector contract.
The browser offers the normal report and ratify actions only when the durable
projection says the signed-in actor may take them.

## Existing rows and rollout

The first implementation computes reconciliation candidates for the current
projection immediately. Therefore all still-live historical rows with readable
Git evidence appear without a migration event. They do **not** close
automatically. Their performers and requesters drain them through the steps
above.

Rows already closed by the seven ratified reports and four explicit request
retirements on 2026-08-30 remain terminal history and are not rewritten. This
design does not reinterpret old events or change their lifecycle labels.

Roll out in three bounded steps:

1. implement and test the repository-aware detector plus CLI read view;
2. compose bounded results into status, work, MCP and browser attention; and
3. add the one-row reporting convenience and post-merge warning.

Each implementation request must rest on the ratified adoption of this note
and identify the architecture layers it changes.

## Architecture

Layer 1 supplies Git reachability and exact object identity. It assigns no
Workroom meaning. Layers 2 through 4 are unchanged. The layer-5 Workroom fold
continues to own commitment status, report authority and ratification; it
receives no integration ref and does not read the application repository. The
layer-6 durable projection remains repository-independent. Layer 7 composes
Git evidence with that projection for CLI, resident, MCP and browser use,
renders the same bounded advisory rows, and uses existing write actions.

The implementation must update `docs/reference/architecture.md` because it
adds a repository-aware host projection outside the pure fold. It must also
update the work-loop and CLI/MCP documentation so users and agents understand
that incorporation evidence is not satisfaction.

## Security and failure posture

- Merger-authored receipt fields confer no new authority. A forged ancestry or
  equality claim cannot close a row or retire an artifact.
- Git revisions are resolved to object IDs before comparison. Symbolic refs,
  abbreviated hashes, replace refs and working-tree contents are not evidence.
- Missing and malformed objects fail closed for the affected row. Other rows
  remain readable.
- Result counts and rows use the existing status/work bounds. Path and error
  text use the existing output sanitation rules.
- Repository reads are local and read-only. The detector does not fetch or
  trust a remote ref implicitly.
- A moved integration ref invalidates a pending write preflight. The reporting
  command recomputes before append and signs the exact head it observed.
- Content equality never crosses an exact artifact path and never grants
  retirement authority at that path.

## Simplification

One derived view and one thin convenience command reuse the existing report,
ratification, staleness and commitment rules. Adding a durable `included`
kind, a new lifecycle status, automatic request acceptance, or merge-wide
artifact retirement would duplicate those rules while weakening their
authority boundary. Leave them out.
