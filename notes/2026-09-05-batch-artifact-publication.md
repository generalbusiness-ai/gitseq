---
date: 2026-09-05
author: planner
status: Review note for Hugh; observation and recommendation, no contract change
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3bf2e77ae61216358c2f6b028c92b096d6c52abc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3a2fd4ce262098627671b192b55273a32ef3477f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:353c62b8f6252d4e1f1af79289b413c08d9d24e4
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:eee526a8c0e58757800a4bce73373af5ef3d4872
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:c70da49677139d59fbbedb6624affe16c017fbeb
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:29d3e40a2a006aa9dc02339976a7e51d26f39165
---

# Publish an artifact set in one batch

Use the existing `gs batch` command when publishing several artifacts from
one actor. Keep the same exact paths, bases and independent review. This is an
immediate tool-use improvement under the delivery-efficiency trial, requiring
no new lifecycle state or implementation.

Two Gitseq publications during this session illustrate the opportunity:

| Candidate and method | Artifacts | First event, UTC | Last event, UTC | Interval |
| --- | ---: | --- | --- | ---: |
| I1 `a0741f48`, separate commands | 33 | #17706, 00:29:38 | #17740, 00:41:27 | 709 seconds |
| Graph plan `8266c475`, one batch | 19 | #17752, 01:01:42 | #17770, 01:02:11 | 29 seconds |

All times are 5 September 2026. The canonical endpoint events appear above.
Claude confirmed that the I1 set used 33 separate `gs state` invocations and
the graph set used one batch. The [batch contract](../docs/reference/gs/batch.md)
explains the mechanism: a batch opens and verifies the log once; separate
commands repeat that work. I checked that the cited batch documentation is
an ancestor of main and its blob equals main at `3f4c4969`.

These are first-to-last durable-event intervals, excluding startup before the
first event. The sets differ in size and content and were not controlled
timing experiments. They measure neither active effort nor total delivery
time, and support no percentage speedup claim.

Prepare one JSON file with stable per-act idempotency keys and labels for
references within the set. Check every `landed`, `replayed`, `failed` and
`skipped` result before requesting review. Batch is not atomic: retry the
unchanged file to resume an incomplete prefix. Keep the guarded `gs review`
command separate; a batch of ordinary statements is no substitute for its
checks.

For subsequent multi-artifact publications, record command count, artifact
count, first-to-last interval and any partial failure. Retain the existing
trial's substantive-review and delivery-defect measures. If this becomes a
documented default, put one example in the publishing instructions rather
than adding another approval or reporting step.
