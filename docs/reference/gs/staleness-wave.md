---
title: gs staleness-wave
summary: Measure the whole-log ordinary-staleness wave rooted at one exact artifact path.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a1055e9d1a044c420c25d249f91c79988cfcda4d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0d87b56bb5146f67931203a41039e3d511ce503e
---

# `gs staleness-wave`

Measures how far ordinary staleness from every artifact at one exact path
reaches through the effective record. It prints four whole-log counts in a
fixed-size answer:

- all fold decisions and how many records the wave reaches;
- all live artifacts away from the selected path and how many the wave reaches.

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice >/dev/null
gs staleness-wave --repo "$REPO" --path docs
gs staleness-wave --repo "$REPO" --path docs --json
```

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--path` | *(required)* | Exact artifact path whose causal wave is measured. |
| `--json` | `false` | Emit the summary as JSON. |

The walk follows every basis of every effective record. It skips only the edge
from an effective supersession to its own target, because retiring a record
does not make the supersession stale at the instant it lands. Retired artifacts
remain seeds: retirement withdraws a pointer but does not erase history.

This is a measurement, not a gate. Re-anchoring current documentation does not
remove the completed requests, promises and reports that honestly descend from
the old path, so the reached counts are not expected to return to zero.

## See also

- [`gs artifacts`](artifacts.md), [`gs supersession-plan`](supersession-plan.md),
  [Staleness](../../concepts/staleness.md)
