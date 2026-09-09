---
title: Landing observations
summary: Keep durable delivery evidence separate from current target and worktree facts.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0581948abafe7fda01c7e4bcafaae5337297c601
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:65e932f9ddd81331c355d7c87def2de9210300ef
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c1912b37f7f0668c7512f9281c6513d2043f69f6
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a51bf9c28f8fc0c4b0669a80d10d3e7ed9f698e0
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aa1fb7103f0466394a55535fcd34687358e7a08e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0d43258e62f3d48b8a226c084d693237cee1ec5b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:22cb07e91e19956f4ad81ba0b2ba1f09e74ee1ad
---

# Landing observations

A sealed receipt proves that an approved head was delivered. Git can separately
show whether the target still contains that landing. Moving or deleting a ref
changes the observation; it does not reopen the commitment the receipt closed.

## Durable fields and receipt evidence

Status, work and worktree commitment rows share these fields. Inspection retains
the raw `commitment` block and adds the same shared shape under `landing`.

| Field | Meaning |
|---|---|
| `target_repo`, `target_ref` | The fold's resolved destination. Empty fields do not by themselves establish that an older request explicitly chose no Git artifact. |
| `legacy` | The request's destination was read from its older history. |
| `hold_owner`, `release` | The hold owner and effective release event, if present. |
| `approval`, `candidate` | The ratified approval and exact candidate head. |
| `latest_resolution`, `terminal` | Nonterminal evidence and the existing closure reason. |
| `approved_not_landed` | The selected approved artifact has no recorded landing to its target. Delivery includes every eligible reporting artifact named by the exact-head approval, even when the retirement cut is empty or carries that artifact. |
| `landing_receipt` | The existing validated receipt selected by the fold's merge index and matched to the commitment's target. |
| `merge_head`, `receipt_legacy` | The witnessed receipt's source merge head and whether both target fields were absent. |
| `merge_hold_warning` | True only when that receipt explicitly sealed `merge_hold_warning=true`. |

An ordinary assertion containing `merge_head` is not a receipt witness. Missing
legacy fields do not imply a hold warning. A released held landing and a landing
that used the compatibility window remain distinguishable by their actual
receipt evidence. The human CLI and MCP summaries also flag shown warnings.

The witness and commitment closure use the same destination-matched delivery.
Retirement authority remains separate from delivery: carrying an approved
reporting artifact does not leave its commitment awaiting landing. The correction
advances the fold profile to `workroom-fold@24`, rebuilding older projection
caches from verified records. Signed checkpoints still contain authenticated
transport records; their format does not change.

## Current Git observations

The nested `git` object is advisory. It includes `measured_at`, the observed
`target_head`, nullable `ref_incorporated`, and one of these states:

| State | Meaning |
|---|---|
| `incorporated` | The observed target contains the receipt's `merge_head`. |
| `landed-then-removed` | The target exists, and its ancestry no longer contains that merge. |
| `target_gone` | A complete local ref inventory contains no such target. |
| `unknown` | The repository, supplied merge head, objects or bounded ancestry read could not settle the answer. A missing witnessed merge head has an explicit reason and leaves both ancestry booleans null; it does not prove that no receipt exists. |

`remote`, `remote_ref`, `remote_head` and nullable `remote_contains` describe
the configured remote's **local tracking observation**. No network fetch runs.
The remote selection matches the local repository display: `origin`, otherwise
the first configured name in lexical order. Its local fetch refspec must map
the exact target unambiguously into `refs/remotes/`. Unsupported, negative or
ambiguous mappings yield unknown. Credentials and remote URLs are not copied
into these observations. Both contains fields use JSON `null` for unknown.

One batch captures ref object IDs and uses one bounded object/ancestry read for
all selected rows. Ordinary ref movement at an unchanged workroom frontier is
remeasured. Missing objects, shallow boundaries and traversal limits cannot
prove absence. Targets in another repository are unknown in this local read.

The bounded status views count all `approved_not_landed` rows. Their
`landing_targets` list shows the newest witnessed receipt per target, capped at
20 with `landing_targets_omitted`. The complete `gs status --json` snapshot and
`--all` tables remain durable exports, without these local observations.

Work accepts an optional `approved_not_landed` boolean (false differs from
absence) and an exact `target_ref` filter. The explicit `approved_not_landed`
lane selects the actor as performer or hold owner, without changing the row's
waiting party. The existing five lanes remain the default. Approved delivery
debt is not hidden as ordinary closed staleness. Query cursors bind the durable
frontier and filters; Git observations can change between pages.

## Worktree cleanup advice

`GET /v0/worktrees` retains the local checkout list and adds `deletable`, a list
of checkout labels. Each checkout carries a `classification`, reason, up to
20 mapped `rows` and `rows_omitted`. Unsettled rows rank first. The `row`,
`approved`, `landed_into` and `remote_contains` fields summarize its primary
mapping conservatively: an approval must name a candidate on the branch, and
`landed_into` names a receipt for its exact tip.

Classification examines every named head on each commitment, including older
artifacts and ancestors of the branch tip. Any unsettled commitment or
`approved_not_landed` row protects it. A clean branch is deletable only when
its current tip is proved incorporated into a witnessed target, or that exact
tip was explicitly abandoned. Current, detached, non-clean and target
checkouts stay protected. Unknown evidence protects rather than enlarges the
deletable set; an unmapped checkout is not deletable.

The ordinary checkout listing keeps its existing eight-second cache. The
classification refreshes attached branch tips from current refs before making
its decision. Cleanliness is still an advisory observation: W1 or the actor
doing cleanup must recheck the checkout and its current heads before deletion.
This endpoint performs no deletion. If the durable snapshot is unavailable,
it retains the local listing with unknown classification and an empty
`deletable` list.

Limits are explicit: 128 measured status/work rows; 4,096 inventoried refs,
4,096 object IDs, 256 graph tips, 20,000 graph nodes and 300,000 aggregate
ancestor visits; a three-second Git inspection deadline. Worktree classification
also caps its commitment input at 4,096. One shared budget allows 65,536
inspection steps across statements, direct provenance associations, receipt
joins, object collection, graph traversal, membership joins and ranked output
selection. Each association is charged before visiting it. A single statement
pass builds the head/branch index and joins only selected receipts. Checkout
membership propagates once through the captured graph. Rows with the same
checkout match ranks share their newest 20 row indexes and an exact total; a
bounded merge selects each checkout's newest rows by rank. This preserves
distinct promises, every named head, tie order and the exact omitted count
without expanding the checkout-by-commitment product. Object inputs are
deduplicated before allocating the Git batch. Cancellation or exhaustion discards the entire batch's deletion
advice and clears partial classifications to `unknown`, preserving every
potential unsettled obligation. Output collectors have byte ceilings.
Crossing a bound reports unknown or omitted rows; it never proves a negative.

## See also

- [`gs work`](gs/work.md), [`gs status`](gs/status.md), [`gs inspect`](gs/inspect.md)
- [Architecture layers](architecture.md)

## Browser presentation

Table and Graph show the projected target and delivery state. The target uses
its short branch name and a legacy badge where applicable; the full repository
and ref remain available. Source closure and approved-artifact audit have separate labels. The artifact
landing audit counts commitments, including historical lifecycles, retaining
words such as cancelled or satisfied. Its rows can overlap completed or closed
populations. A carried label requires the exact approval artifact in the
selected receipt’s fold-verified `merge_left_live` accounting; raw receipt
text and source closure alone cannot supply it. A later approved artifact
without that accounting remains visible as having no recorded landing. The
waiting actor remains the one projected by the fold; an independent publication
request keeps its own lifecycle.

Opening a row or graph card keeps its particular promise or report selected in
the thread. The thread reads `/v0/inspect` for that lifecycle and refreshes every
ten seconds. Its sealed landing station uses `landing_receipt`; a separate
Git-now station can show incorporation, removal, a missing target, unknown
ancestry or an unavailable read. Those observations do not rewrite delivery
history. A compatibility hold warning appears only when the actual receipt
carries it. Late responses for a different event, target, candidate or receipt
are discarded.

The request form requires the result explicitly. For a named branch, the
service resolves and records the repository and current head at filing. The
browser sends only the chosen branch ref, with no default branch or supplied
repository or commit measurement. A hold needs its owner. Asking
for a hold only in text produces a warning, without creating authority. Retrying
an unchanged request keeps the same input and key; changing the result creates
a new intent.
