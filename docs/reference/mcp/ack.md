---
title: MCP ack
summary: Remove exact addressed-chat handles from this leased session's priority inbox.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:aea9521daff999b6b5f6a1ec97f85994cdfea4aa
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
---

# `ack`

Acknowledges priority ephemeral chat for this exact adapter session. It removes
the named frames from subsequent `status` and `wait` answers.

## Arguments

| argument | required | meaning |
|---|---|---|
| `threads` | required | Up to 20 exact `<conversation>:<sequence>` handles from `priority_ephemeral_chat`. |
| `repo` | optional | The repository whose workroom this call acts in. |

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
