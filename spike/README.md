# gitseq implementation and adversarial spike

This began as a disposable executable test of the contracts in
`../2026-08-05-gitseq-design.md`. The proven kernel remains the adversarial
fixture; the bootstrap now grows beside it as `internal/workroom`,
`internal/app`, `internal/service`, `cmd/gs`, and `cmd/gitseq-mcp`.

The kernel commands are one-shot processes: every invocation is a cold
failover, so repository state and signing keys are the only durable truth.
The nexus is the sole stateful component because its amnesia is part of the
collaboration-profile contract. The resident workroom service exposes it and
the durable sequencer over localhost HTTP; the stdio MCP process remains a
thin per-actor adapter.

Run the fast evidence lane:

```sh
go test ./...
go test -race ./...
```

Run the intent mutation fuzzer for a bounded interval:

```sh
go test ./internal/intent -fuzz=FuzzDecode -fuzztime=10s
```

`go run ./cmd/gitseq-report` runs the named adversarial cases, refreshes the
tracked stable projection in `SPIKE-RESULTS.md`, and writes machine-specific
JSON, timings, and a detailed Markdown report under ignored `.spike/`.

The optional forge lane is described in `FORGE.md` and uses the `forge`
Docker Compose profile. It is separate because pulling and booting a forge
must not slow the kernel correctness loop.

## What this spike fixes—and does not

The mechanism selected deterministic fixed-array CBOR, Ed25519 actor intents,
Git SSH signatures for sequencer commits, single-parent `refs/seq/*` logs, and
a nexus-assigned per-conversation counter. The counter proves that ordering is
cheap; it does not prove that total ephemeral order belongs in the minimal
profile.

Actor keys are separated by name, not by isolation. Every principal's private
key is a file in one local directory that any process running as the same user
can read, so the log proves which key signed an event and not which instance
held it. Concurrent instances therefore take distinct identities by discipline;
real separation would need separate operating-system users or machines, and
this spike does not provide it.

The custody case uses an acyclic offer → accept → settle saga. Competing
completed settlements produce a typed `disputed` state and a decision for
every event; choosing a winner remains application policy, not kernel behavior.
The forge profile is also not certified by the normal test lane; only the
smart-HTTP repository boundary is.

The resident now keeps a local sequencer-signed Git checkpoint for fast cold
restart. It contains original actor-signed events rather than a projected
authority answer. Recovery proves the exact first-parent commit sequence with
one local metadata enumeration, binds every cached envelope and tree back to
its actual commit, rechecks actor signatures and cached payload bytes, rebuilds
the profile fold, and performs full commit verification on the descendant
tail. Any checkpoint failure falls back to the ordinary full audit. Writes
replace the whole bounded snapshot; failed writes retry on the next accepted
event instead of silently postponing another 256 events. Restart acceleration
is tail-dependent rather than a fixed multiplier; the depth-1,000 benchmark
with a 232-event tail measured 3.71x on an Apple M5 Max. Witness
cosignatures and configurable cadence policy, key rotation, production multi-domain watch
and frontier retrieval, capability token semantics, and throughput/latency
targets remain outside this spike. The pre-append hook is present, but ships
with no capability policy.
