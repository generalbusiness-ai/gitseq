---
date: 2026-09-05
status: Review note for Hugh; recommendation, not an adopted interface change.
author: planner
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3bf2e77ae61216358c2f6b028c92b096d6c52abc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:22f18c10086439bd87fbd480607e8c630a7adc20
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3d41a355dcbb426ddcd97604031acaea144589e8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:5e43d944dac7f454e893c29c4f950d23c3ef61c0
---

# Keep running adapters compatible with their resident

A successful resident upgrade can leave an existing agent session unable to
inspect ordinary tasks. Replacing the executable on disk does not replace
the code already loaded by its MCP adapter process. I recommend one small
deployment check covering both processes, with a clear compatibility error
when their response contracts differ.

## Observed failure

During this monitoring session, the resident services were updated while
older MCP adapter processes remained running. At 03:26–03:27 UTC on 5 September,
the session's `inspect` tool returned `json: unknown field "stale_because"`
for the Chess review request below. The current `gs inspect` read that same
record successfully. A newly started MCP adapter also succeeded against the
same resident, returning the field and the complete inspection.

The fresh adapter's Go build metadata identifies clean source
`28c0298bc054383d37c2339be89720a4887c04cc`. The older process's exact source
revision was not measured. These controls establish a running-adapter
compatibility problem; they do not establish a general productivity saving.
The workaround was to use the current CLI for affected reads, while the
existing session continued to handle waits and signed actions.

The inspected immutable record was
`git:sha1:1b96687964cbba5a6089c526d7136b32ba9488a5#git:sha1:6a5916225725ddf4aac8cce7b6970a67ebaff9b7`.
Its lifecycle changed during delivery; both the error and the successful
controls concern decoding the inspection response, not its eventual outcome.

## Why it happens

At source `6bcd22b45394e0cc25fa7ebf41f8b92484b40a99`,
`internal/residentclient/client.go` deliberately rejects unknown fields in
typed responses. Only the bounded `/v0/identity` liveness response admits
future fields. That endpoint currently reports genesis and PID, not response
compatibility. MCP's protocol version and its fixed server version `0.1.0`
do not identify which resident response fields the loaded adapter understands.

An additive projection field can therefore break an old reader selectively:
an operation looks healthy until it reaches a record carrying that field.
The resulting decoder error gives an agent no direct upgrade instruction.
Repeated inspection or refiling the task cannot repair the loaded binary.

## Small remediation

1. Treat resident and adapter compatibility as one deployment check. Start or
   reconnect adapters through their owning harness, then exercise `wait` and
   a representative stale `inspect` through the actual agent connection.
   Checking a fresh CLI alone does not verify the long-running session.
2. Design a bounded response-contract identifier and diagnostic. Report the
   resident contract and the adapter's supported contract, with an explicit
   reconnect/upgrade instruction. Compare semantic compatibility, not source
   hashes: a documentation-only commit should not require a restart.
3. Preserve the present boundaries. Do not make every response decoder
   permissive, silently fall back from malformed responses, or kill unrelated
   adapters. A live but incompatible resident is still alive; compatibility
   must never turn into a vacancy claim or permission to take over its port.

One focused follow-on design should specify identifier ownership, the legacy
case, safe connection renewal, and the diagnostic before implementation.
Use the existing surface layer; add no task state or adoption shortcut.

Acceptance should run an older adapter against a response with the added
staleness field, a compatible pair, and the upgraded session connection.
Require a clear compatibility refusal or an explicitly supported decode,
followed by successful inspection after renewal. Malformed, oversized,
wrong-genesis and trailing responses must retain their refusal behavior;
liveness and ownership must remain unchanged. Record the measured commands
and loaded versions, without claiming elapsed time is active effort.
