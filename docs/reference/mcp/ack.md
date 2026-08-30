---
title: MCP ack
summary: Remove exact addressed-chat handles from this leased session's priority inbox.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cadb3875bb56fc359f4b96b167a35d13b29d8dda
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:430562cb8828b03180359324f47bedc1708c3330
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ad2e2daabd99b310687e7640b55ab7eae1c677d
---

# `ack`

Acknowledges priority ephemeral chat for this exact adapter session. It removes
the named frames from subsequent `status` and `wait` answers.

## Arguments

| argument | required | meaning |
|---|---|---|
| `threads` | required | Up to 20 exact `<conversation>:<sequence>` handles from `priority_ephemeral_chat`. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The actor whose existing accessible key selects this session; defaults to startup `--actor`. |

A well-formed handle that was already acknowledged, expired, or never belonged
to this session is harmless. A malformed handle is refused. Acknowledging in
one live session does not alter another session's inbox. The result reports
how many pending frames were actually removed. When more frames are pending
behind the current page, acknowledging visible handles reveals the next page.

## What it does not mean

`ack` is leased local attention. It is not a durable read receipt, a promise,
an approval, a ratification, or evidence that the message was acted on. It
advances neither the durable sequence nor the live room cursor.

Inbox state disappears with the session lease or resident process. If the
resident is unavailable, `ack` fails rather than pretending it changed a live
inbox that cannot be reached.

## See also

- [`status`](status.md), [`wait`](wait.md), [`say`](say.md)
