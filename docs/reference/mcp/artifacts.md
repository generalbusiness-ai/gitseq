---
title: MCP artifacts
summary: Page through live artifact bases at exact path strings.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
---

# `artifacts`

Finds live artifact statements at exact path strings without fetching the full
workroom projection. Use it before filing an act that must rest on the current
behavior at one or more maintained paths.

## Arguments

| argument | required | meaning |
|---|---|---|
| `paths` | required | One to 20 exact artifact path strings. No cleaning, prefix matching, or globbing occurs. |
| `limit` | optional | Page size, 1 to 50. Default 20. |
| `cursor` | optional | The opaque continuation from a previous page. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose existing accessible key selects this call; defaults to startup `--actor`. |

## What comes back

The `artifacts` array contains only live, non-retired artifacts at the exact
requested paths. Every row gives the event, path, commit, and explicit
`stale`, `retired`, and `describes_superseded_world` flags. Every returned row
carries `retired` as false; carrying it explicitly prevents callers from having
to infer that fact from an omitted field.

A stale row also gives `stale_because`, naming the nearest retired basis that
actually propagated to it, and, for an artifact basis, `stale_because_path`.
The explanation looks through at most four cause edges. If the cause lies
farther away, the row sets `stale_because_truncated` to true and invents no
nearer cause. Non-stale rows omit all three fields.

The response also gives the exact frontier, sorted requested paths, matching
total, returned count, preceding count, remaining count, and a next cursor when
more remain. The cursor binds to the exact durable head and path set. If the
head moves, start again rather than mixing artifact bases from two worlds.

The resident selects and caps the rows before encoding. If it does not answer,
the adapter applies the same exact-path selection to a verified local snapshot
and marks the response `degraded`.

## See also

- [`work`](work.md)
- [`inspect`](inspect.md)
- [`state`](state.md)
