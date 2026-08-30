---
title: MCP reassign_if_unclaimed
summary: Guardedly retire an unclaimed request and publish its replacement.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a27668b9112717eafde2516d16387d8d50858e87
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
---

# `reassign_if_unclaimed`

Retires one fresh request and publishes a replacement only while the old
request has no admitted promise or direct completion. The two durable acts
carry signed Workroom guards; the fold remains the final authority even when
the adapter talks to a resident running different code.

## Arguments

| argument | required | meaning |
|---|---|---|
| `old_request` | required | The exact request event read as unclaimed. |
| `to` | required | The replacement addressee: name, `@name`, or fingerprint. |
| `text` | required | The replacement request text. |
| `conditions` | required | Observable conditions of satisfaction. |
| `retirement_text` | optional | Why the old request is retired. |
| `rests_on` | optional | Additional current bases for the replacement request. |
| `idempotency_key` | required | Stable base key used to derive resumable keys for both acts. |
| `repo` | optional | The repository whose workroom this call acts in. |

## Example

```json
{
  "name": "reassign_if_unclaimed",
  "arguments": {
    "old_request": "git:sha1:<genesis>#git:sha1:<event>",
    "to": "@second-agent",
    "text": "Check the release",
    "conditions": "the release is checked",
    "rests_on": ["git:sha1:<genesis>#git:sha1:<current-basis>"],
    "idempotency_key": "release-check-reassignment"
  }
}
```

The result contains `retirement` and `request` submission results. Unrelated
durable traffic does not refuse the pair. A promise or direct completion before
the retirement, or between the retirement and replacement, does. If only the
retirement lands, the error names it; an exact retry replays that act before
continuing.

Use the ordinary [`supersede`](supersede.md) tool when a requester knowingly
withdraws work that has already been promised.
