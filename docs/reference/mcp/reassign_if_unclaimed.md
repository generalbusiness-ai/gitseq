---
title: MCP reassign_if_unclaimed
summary: Guardedly retire an unclaimed request and publish its replacement.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a27668b9112717eafde2516d16387d8d50858e87
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
---

# `reassign_if_unclaimed`

Retires one request and publishes a replacement only while the old request has
no admitted promise or direct completion. The two durable acts carry signed
Workroom guards; the fold remains the final authority even when the adapter
talks to a resident running different code.

The guard is about claims, not freshness: a request that went stale because a
basis under it was retired can still be reassigned, because nobody has claimed
or completed it.

## Arguments

| argument | required | meaning |
|---|---|---|
| `old_request` | required | The exact request event read as unclaimed. |
| `to` | required | The replacement addressee: name, `@name`, or fingerprint. |
| `text` | required | The replacement request text. |
| `conditions` | required | Observable conditions of satisfaction. |
| `body` | optional | String map of further replacement-request fields. The replacement is a new request and states its own result here: `target_ref`, `target=inherit`, or `no_git_artifact=true`. `to` and `conditions` are written over whatever this map says. |
| `retirement_text` | optional | Why the old request is retired. |
| `rests_on` | optional | Additional current bases for the replacement request. |
| `idempotency_key` | required | Stable base key used to derive resumable keys for both acts. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose existing accessible key signs both guarded acts; defaults to startup `--actor`. |

## Example

```json
{
  "name": "reassign_if_unclaimed",
  "arguments": {
    "old_request": "git:sha1:<genesis>#git:sha1:<event>",
    "to": "@second-agent",
    "text": "Check the release",
    "conditions": "the release is checked",
    "body": {"no_git_artifact": "true"},
    "rests_on": ["git:sha1:<genesis>#git:sha1:<current-basis>"],
    "idempotency_key": "release-check-reassignment"
  }
}
```

The replacement request is signed as `workroom/reassign-if-unclaimed@1`, under
which the fold reads its stated result exactly as it reads a
`workroom/state@3` request. Nothing is inherited from the retired request: a
replacement that states no result is refused before either act is appended, and
a replacement of a legacy request must say in its own words what it owes.

The result contains `retirement` and `request` submission results. Unrelated
durable traffic does not refuse the pair. A promise or direct completion before
the retirement, or between the retirement and replacement, does. If only the
retirement lands, the error names it; an exact retry replays that act before
continuing. The replacement is authored on the same path as
[`state`](state.md#request-authoring-what-a-request-owes), so its retry is
answered from the log before any ref is read: it replays even after the branch
its `target_ref` named has gone, while a reused key naming a different branch
is refused rather than answered with the accepted replacement.

Use the ordinary [`supersede`](supersede.md) tool when a requester knowingly
withdraws work that has already been promised.
