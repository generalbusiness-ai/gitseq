---
title: gs supersession-plan
summary: Build complete bounded gs batch input for retiring every live artifact at one exact path.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
---

# `gs supersession-plan`

Builds the `supersede` acts for every live artifact at one exact path. The JSON
form is input for [`gs batch`](batch.md):

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
ARTIFACT=$(gs state --repo "$REPO" --as alice --kind artifact \
  --text 'the docs stand here' --body path=docs \
  --body commit="$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")" \
  --rests-on "$SEED" | sed -n 's/.*"event": *"\([^"]*\)".*/\1/p')

gs supersession-plan --repo "$REPO" --path docs \
  --text 'Retire the docs pointer; its behaviour moved.' \
  --idempotency-prefix retire-docs- --json > "$REPO/retire-docs.json"
grep -q "$ARTIFACT" "$REPO/retire-docs.json"
gs batch --repo "$REPO" --as alice "$REPO/retire-docs.json"
```

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--path` | *(required)* | The exact live artifact path to retire. |
| `--text` | *(required)* | Plain-language reason copied into every act. |
| `--idempotency-prefix` | `supersede-` | Prefix joined to each target event. |
| `--limit` | `50` | Maximum complete plan size, 1 to 50. |
| `--json` | `false` | Emit the `gs batch` JSON array instead of a human summary. |

The command never prints a partial plan. If more live artifacts match than the
limit holds, it exits non-zero before writing output and reports both counts.
This makes a redirected file bounded without letting truncation look complete.

Paths are exact strings. The command always selects `live`: a retired artifact
has already been withdrawn and must not receive another supersession act.

## See also

- [`gs artifacts`](artifacts.md), [`gs batch`](batch.md),
  [`gs supersede`](supersede.md)
