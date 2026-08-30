---
title: MCP review
summary: File a guarded review verdict against an exact head.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:db34afe2f1c6b4033d1d0bdbce0c4d7278bcb94d
---

# `review`

Files one durable verdict through the same guarded pipeline as
[`gs review`](../gs/review.md). Discovery of head news, acknowledgment
validation, canonical encoding, and act construction live in
`internal/reviewguard`, so both surfaces sign the same body shape and
cannot drift. The tool holds no working tree: the reviewed head comes from
the durable artifact row rather than from a checkout, and everything else —
independence, standing, head news, the frontier binding — judges exactly
as the command line does.

The verdict is a report whose body carries `verdict` and the reserved
guard fields. A generic [`state`](state.md) call cannot write that shape:
admission refuses it and names this tool.

## Arguments

| argument | required | meaning |
|---|---|---|
| `artifacts` | required | Array of artifact events standing at the reviewed head; the first is the primary the verdict names. Each must be effective, not retired, and stand at the exact head. |
| `promise` | required | The reviewer's own promise to review. |
| `verdict` | required | `approved` or `changes-requested`. |
| `text` | required | The review itself. Acknowledgment means seen; judgment lives here in words. |
| `ack_head_news` | optional | Array of event identifiers. Durable statements sequenced after the review request that name this head or lane are head news: the call refuses until you acknowledge exactly that set, once each. News the verdict already cites counts once. Every acknowledged event becomes a citation of the verdict. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. |
| `agent` | optional | The reviewer whose existing accessible key signs this verdict; defaults to startup `--actor`. |

## Head news

Anything sequenced after the review request that carries the reviewed
object ID in a structured `head` or `commit` field, or rests directly on
the request, the promise, a cited artifact, or another effective artifact
standing at that head, is news to the reviewer. Matched records are shown
whatever force they have — ineffective, retired, and undefined-kind rows
included — because the guard directs attention; it does not judge.

Missing, duplicate, or extraneous acknowledgments refuse with the full set
named. The signed body records `head_news_acknowledged` as the canonical
sequence-ordered array and `review_frontier` as the frontier the final
read observed; if the workroom moves before the event lands, admission
refuses at sequencing time and rerunning exposes what arrived.

## Capacity

Promise, request, artifacts, and acknowledgments must fit inside the
causal-reference bound every signed act carries. Overflow refuses without
truncation.

## See also

- [`gs review`](../gs/review.md)
- [`state`](state.md), [`supersede`](supersede.md)
