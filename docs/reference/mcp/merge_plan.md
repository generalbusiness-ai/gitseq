---
title: MCP merge plan
summary: Read the exact guarded merge plan without changing Git or the workroom.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8ca4b615d2b0ebceeff06f92e4af81305e1cea4b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8e2b7c8cd90d20d20bdf15065f8cf290bfebdac0
---

# `merge_plan`

Returns the same read-only typed result as [`gs merge-plan`](../gs/merge-plan.md).
The MCP adapter calls the shared evaluator directly; it does not infer the plan
from status rows or maintain a second set of merge rules.

## Arguments

| argument | required | meaning |
|---|---|---|
| `candidate` | required | The full lowercase approved commit object ID. |
| `approval` | required | The ratified approval report event. |
| `checkout` | optional | The checkout that would receive the merge. Defaults to `repo`. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose existing accessible key selects this call; defaults to startup `--actor`. |

The tool deliberately has no `authorization` argument. Structured merge
authorization belongs to the mutating `gs merge` boundary. An allowed result
therefore does not say that a required authorization exists or is valid, and
`gs merge` may still refuse the candidate on that separate check.

## Result

The structured result names the durable frontier, receipt mode, approval,
exact head and implementer; candidate artifacts and reviewed paths; changed
paths; every covering artifact and its classification; retirements and
successors; and stable allow or refusal reasons. The four classifications are
`reviewed candidate`, `in-target predecessor`, `protected sibling`, and
`abandoned`.

Before allowing a fresh plan, the evaluator encodes the exact durable receipt,
successor, and retirement suffix without structured authorization through the
same application request builder that merge uses. It checks the local admission
ceiling and, when this MCP room has a resident endpoint, the resident submission
ceiling. This proves the un-authorized suffix shape and size, not authority to
merge it.

The tool is read-only even on a cold workroom whose local verified-frontier or
checkpoint state could be repaired. It does not announce completion, reserve
the approval, append a durable act, or change repository-local acceleration
state. The deterministic result is bounded to 2 MiB and refuses with
`plan_output_too_large` when complete accounting would exceed that ceiling.

## See also

- [`gs merge-plan`](../gs/merge-plan.md)
- [`gs merge`](../gs/merge.md)
