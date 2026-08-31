---
title: gs merge-plan
summary: Explain an exact merge and its artifact succession without changing the repository or workroom.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8ca4b615d2b0ebceeff06f92e4af81305e1cea4b
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3eb96a9faebd16a9626fc17362bae4cd75e4b2c3
---

# `gs merge-plan`

Explains whether one ratified approval can merge its exact head and how every
live artifact covering that merge would be accounted for. It prints JSON and
makes no durable act. It does not change the governed checkout, its refs,
index, object database, worktree registration, Git configuration, gitseq
configuration, verified-frontier witness, or checkpoint state.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor who would sign the merge receipt. |
| `--checkout` | *(required)* | The checkout that would receive the merge. |
| `--candidate` | *(required)* | The full lowercase approved commit object ID. |
| `--approval` | *(required)* | The ratified approval report event. |
| `--server` | repository advertisement | The resident whose request ceiling the eventual succession must meet. `-` forces local-only admission preflight. |

It takes no positional arguments.

This minimal call deliberately names an unknown approval. It exits normally
with `allowed: false` and an `approval_refused` reason, which lets automation
inspect a refusal without parsing stderr.

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
gs init --repo "$REPO" --operator alice >/dev/null
HEAD_COMMIT=$(git -C "$REPO" rev-parse HEAD)
gs merge-plan --repo "$REPO" --as alice --checkout "$REPO" \
  --candidate "$HEAD_COMMIT" --approval unknown-approval >/dev/null
```

## Result

`mode` is `fresh`, `resume`, or `used`. A fresh plan reports the exact durable
frontier, approval, candidate head, implementer, target pre-head, candidate
artifacts, reviewed paths, canonical changed paths, and every live covering
artifact. Each covering artifact is classified as `reviewed candidate`,
`in-target predecessor`, `protected sibling`, or `abandoned`. The plan then
lists the proposed retirements, successor paths, and stable reason codes.
Before allowing a fresh plan, it also encodes the exact durable receipt,
successor, and retirement suffix through the same application request builder
that `merge` uses and checks the applicable local and resident admission
ceilings.

`allowed` says whether the exact preflight permits the action. A refusal stays
a normal structured result and names the exact failed check. Results are sorted
deterministically and bounded to 2 MiB; a larger result refuses with
`plan_output_too_large` instead of silently omitting accounting.

For a resumed merge, the command reads the immutable receipt already at the
checkout head and renders its sealed succession. It does not replan against a
newer world. A receipt already used in another checkout is `used` and refuses.

The prospective Git merge is staged only in a disposable clone. The governed
repository is read with optional locks disabled. `gs merge` consumes this same
typed plan, then verifies that the actual staged changed-path frontier and the
workroom frontier still equal what was planned before it moves `HEAD`.

## See also

- [`gs merge`](merge.md)
- [MCP `merge_plan`](../mcp/merge_plan.md)
