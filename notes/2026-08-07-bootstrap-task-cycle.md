---
status: active
task: git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:782851a1acb5c0b9dffdc7b6ddae21edb739ae18
---

# Bootstrap task cycle

## Goal

Use gitseq as this repository's real task and review record. The cycle should
produce useful work while testing whether agents can discover requests,
coordinate over MCP, preserve causal source history, and obtain independent
review without a human carrying state between them.

## Initial work

- **Documentation** — build a minimal user-oriented set from the rearrangements
  already present in the main worktree. Task event:
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0de41ae292bb0181eb4b570e6e17e0cdcfabed5d`.
- **Bootstrap cycle** — establish this discipline, observe the cycle, and make
  only code changes justified by use. Task event:
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:782851a1acb5c0b9dffdc7b6ddae21edb739ae18`.
- **Efficient status** — dependent on the bootstrap task; give agents a bounded,
  useful `status`/`wait` surface without weakening the full audit projection.
  Task event:
  `git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:e4a7f0ceff782e494f4ef868f94c55f0e24307e1`.

## What to observe

- Time and manual intervention needed for two agents to join and find work.
- Complete request → promise → report → requester-satisfaction chains.
- Independent review of the exact merged head, with no out-of-band signoff.
- Unexpected ineffective, disputed, duplicate, or abandoned acts.
- Implementing commits and artifacts that can be traced to their task events.
- MCP response size and tool/context cost. Baseline at durable depth 24:
  51,356 bytes for one MCP `status` response.
- Whether a fresh clone can verify the log and explain the work without chat
  transcripts.

Create further work as child requests in gitseq. This note summarizes the
experiment; it is not a second task tracker.
