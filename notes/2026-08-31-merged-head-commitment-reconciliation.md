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
head. Qualify the existing commitment rows immediately after a merge and in
normal status reads. Keep the existing closure rule: the performer files a
report and the requester ratifies it. This drains rows without letting a Git
ancestry test declare somebody else's conditions satisfied.

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
reconciliation**. Repository reachability produces advisory evidence. The
current lifecycle acts turn that evidence into a
closure. Existing rows become visible automatically, but they do not become
satisfied automatically.

This is an extension of existing landing evidence, not a second ancestry
system. `internal/gitstore` already exposes `Landings`; the resident already
serves `/v0/landed`; thread presentation already marks landed heads; and the
review gate already resolves exact reviewed commits. The detector reuses those
interfaces and their object-verification rules, then joins the result to
commitment rows. The problem here is that the join is absent, not that Gitseq
cannot tell whether a commit landed.

## What the detector says

The detector reads one explicit integration ref and peels it with
`git rev-parse --verify <ref>^{commit}`. Only the resulting full object ID is
reported or signed; abbreviated hashes, unpeeled tags and symbolic names are
not evidence by themselves. Standing status and work views use
`refs/heads/main` until Gitseq has a structured integration-target setting;
callers may name another full ref explicitly. The post-merge warning is
different: it examines the checked-out target branch immediately after that
merge, so merging into a non-main branch cannot accidentally report main.
Output always includes both `integration_ref` and `integration_head`, so a
later ref move cannot silently change what an earlier result claimed.

It considers commitments in `promised`, `stale`, `reported`, and
`awaiting-merge`. Terminal rows are excluded. Each candidate has one of these
evidence classes:

| Evidence | Meaning | May it close the row? |
| --- | --- | --- |
| `reported-head-reachable` | A live or stale reporting artifact names commit `H`, and `H` is an ancestor of the resolved integration head. | No. It supports a performer report. |
| `head-hint-reachable` | A promise or explicit report body carries advisory head `H`, and `H` is an ancestor of the integration head, but no reporting artifact proves it. | No. It is a prompt to investigate, not completion evidence. |

The detector does not parse request conditions, infer that two requests are the

## Where the result appears

Add an `incorporated` qualifier to the existing repository-aware host rendering
of a commitment row, not a new top-level `merge_reconciliation` collection.
The fold remains unchanged; layer 7 joins ordinary Git evidence to the row it
already places in `waiting_on_you`, `you_are_waiting_on`, or another existing
lane. The qualifier never changes the lane, waiting party, status, count or
action ownership. Each bounded qualifier contains:

- request, promise and completion event identifiers already projected by the
  fold;
- current commitment status and ordinary staleness;
- performer and requester fingerprints;
- evidence class and the exact reported or hinted head;
- integration ref and resolved full commit ID.

The normal `gs status`, `gs work`, MCP `status`, MCP `work`, and browser work
board show the qualifier on their already bounded rows. They do not add a
second count. The resident recomputes the host view when either the durable
frontier or integration ref moves. The fold and its checkpoints remain
independent of the application repository. Waiting-party wording stays the
wording of the underlying lane; incorporation evidence is an explanation, not
a reassignment.

After `gs merge` updates the integration ref, it runs the same detector against
the new exact head and prints any newly qualifying unresolved rows. This is a
warning after the merge, not another merge gate and not a durable receipt
field. A warning failure must not make an already completed Git merge look as
though it did not happen. The warning prints at most the existing work-row cap
and states the omitted count, so one merge cannot produce unbounded output.

## How a row closes

The sanctioned closure is the ordinary non-merge route already understood by
the fold:

1. The performer reads the original request conditions and the detector's
   evidence.
2. If those conditions are in fact met, the performer files an explicit
   `report` resting on the promise, or on the request when there was no promise.
   The report's visible signed text states the exact integration ref, resolved
   full commit ID and why the incorporated implementation satisfies the
   conditions. The convenience adds no `body.head`, `body.commit`,
   `body.verdict`, `body.status`, or other invented body field.
3. If that basis is stale, the performer uses the existing dead-basis override.
   The override records that the stale basis was seen; it does not make it
   current.
4. The original requester ratifies the report. The fold then projects the
   commitment as `satisfied`, retaining its independent stale flag.

When a commitment already has an earlier completion artifact or report, a
later effective report becomes its latest completion for presentation and
ratification; the earlier completion remains immutable history. The
implementation must test that replacement explicitly so reconciliation never
leaves a repaired report hidden behind the completion it was meant to
supersede.

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
- refuses a moved integration head;
- requires `--text` describing why all conditions are met;
- requires `--allow-dead-basis` when the promise or request is stale; and
- appends one ordinary report with an idempotency key derived from request,
  promise, integration ref and resolved head.

The command composes the integration ref, full resolved commit ID and supplied
reason into the visible report text before signing. It uses only the existing
report body shape and ordinary projection rules.

It never ratifies. The requester uses the existing `ratify` command after
reading the report. There is no `--all --report`: bulk inference is the failure
this design is avoiding.

The MCP adapter exposes the qualifier through existing `status` and `work`
reads, including per-call actor and repository selectors under the existing
selector contract. It adds no reconciliation write operation. Agents use the
existing `state` tool to file the ordinary report. The browser likewise uses
its normal report and ratify actions only when the durable projection says the
signed-in actor may take them.

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

1. extend the existing `Landings`, `/v0/landed`, thread landed-marker and
   review-gate head classifiers into the repository-aware detector, and add
   the CLI read view;
2. compose the bounded qualifier into existing status, work, MCP and browser
   rows; and
3. add the CLI one-row reporting convenience and post-merge warning.

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

- Merger-authored receipt fields confer no new authority. A forged ancestry
  claim cannot close a row or retire an artifact.
- Git revisions are peeled with `git rev-parse --verify <ref>^{commit}` and
  recorded as full object IDs before comparison. Symbolic refs, abbreviated
  hashes, replace refs and working-tree contents are not evidence.
- Missing and malformed objects fail closed for the affected row. Other rows
  remain readable.
- Result counts and rows use the existing status/work bounds. Path and error
  text use the existing output sanitation rules.
- Repository reads are local and read-only. The detector does not fetch or
  trust a remote ref implicitly.
- A moved integration ref invalidates a pending write preflight. The reporting
  command recomputes before append and signs the exact head it observed.

## Simplification

One qualifier and one thin CLI convenience reuse the existing landing,
report, ratification, staleness and commitment rules. There is no second row
collection, content-equality engine, MCP write method, durable `included` kind,
new lifecycle status, automatic request acceptance, or merge-wide artifact
retirement. Leave them out.
