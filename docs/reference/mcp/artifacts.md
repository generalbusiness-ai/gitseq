---
title: MCP artifacts
summary: Page through live artifact bases at exact path strings.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3e42417eb5b568cbd099571f99ef18dc10cf7ee5
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9627cf187f0a8002ea2c43861dd7cb208a09ce51
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
`stale`, `retired`, and `describes_superseded_world` flags. `retired` is false
for every returned row; carrying it explicitly prevents callers from having to
infer that fact from an omitted field.

The response also gives the exact frontier, sorted requested paths, matching
total, returned count, preceding count, remaining count, and a next cursor when
more remain. The cursor is bound to the exact durable head and path set. If the
head moves, start again rather than mixing artifact bases from two worlds.

The resident selects and caps the rows before encoding. If it is unavailable,
the adapter applies the same exact-path selection to a verified local snapshot
and marks the response `degraded`.

## See also

- [`work`](work.md)
- [`inspect`](inspect.md)
- [`state`](state.md)
