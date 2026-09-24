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

The guard concerns claims, not freshness: a request that went stale through
the retirement of a basis under it still admits reassignment, because nobody
has claimed or completed it.

## Arguments

| argument | required | meaning |
|---|---|---|
| `old_request` | required | The exact request event read as unclaimed. |
| `to` | required | The replacement addressee: name, `@name`, or fingerprint. |
| `text` | required | The replacement request text. |
| `conditions` | required | Observable conditions of satisfaction. |
| `body` | optional | String map of further replacement-request fields. The replacement counts as a new request and states its own result here: `target_ref`, `target=inherit`, or `no_git_artifact=true`. `to` and `conditions` overwrite whatever this map says. |
| `retirement_text` | optional | The reason for retiring the old request. |
| `rests_on` | optional | Additional current bases for the replacement request. |
| `idempotency_key` | required | Stable base key used to derive resumable keys for both acts. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose existing accessible key signs both guarded acts; defaults to startup `--actor`. |

`old_request`, `rests_on` and the recognized event fields of `body` take a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier. The tool resolves both acts of the pair against one
verified event set before it signs either, and the result names what it
resolved.

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

The tool signs the replacement request as `workroom/reassign-if-unclaimed@1`,
under which the fold reads its stated result exactly as it reads a
`workroom/state@3` request. The replacement inherits nothing from the retired
request: a replacement of a legacy request must say in its own words what it
owes.

The pair consists of two acts in order, so a refusal the replacement earns
after the retirement has landed would leave the old request withdrawn with
nobody asked to do the work. Before appending the retirement, the tool
therefore checks everything about the replacement that the call's stated
values can settle: a missing `to`
or `conditions`, an address current custody does not hold — a performer
since retired included — a reserved admission field, a missing or
doubled result, a `target_ref` outside `refs/heads/`, one naming a ref that
does not resolve here, a supplied `target_repo` or `target_head`, and an
`idempotency_key` already spent on some other act. Each of those refuses with
nothing appended and the old request still open. Nobody can know the guard
on the old request — no admitted promise, no direct completion, no prior
retirement — at that point, so its check runs at each act's append, against
the frontier that act actually joins.

The result contains `retirement` and `request` submission results. Unrelated
durable traffic does not refuse the pair. A promise or direct completion before
the retirement, or between the retirement and replacement, does. If only the
retirement lands, the error names it; an exact retry replays that act before
continuing. The tool authors the replacement on the same path as
[`state`](state.md#request-authoring-what-a-request-owes), so the log answers
its retry before anything reads a ref, and the preflight answers it the same
way: a key already holding a replacement counts as a retry only when the whole
call — old request, words, bases and body — rebuilds to that accepted act, in
which case it replays even after the branch its `target_ref` named has gone or
the performer it named has left the roster. The tool refuses a reused key that
names a different old request, a different branch, or any other change as a
reused key before any retirement, rather than answering it with the accepted
replacement or letting it withdraw a second request in its name.

Use the ordinary [`supersede`](supersede.md) tool when a requester knowingly
withdraws work that someone has already promised.
