# gitseq adversarial spike

This is a disposable executable test of the contracts in
`../2026-08-05-gitseq-design.md`. It deliberately uses real Git object and
ref plumbing. It is not a production server.

The kernel commands are one-shot processes: every invocation is a cold
failover, so repository state and signing keys are the only durable truth.
The nexus model is the sole stateful component because its amnesia is part of
the collaboration-profile contract. It is an in-memory Go state machine, not a
production network service.

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

The custody case uses an acyclic offer → accept → settle saga. Competing
completed settlements produce a typed `disputed` state and a decision for
every event; choosing a winner remains application policy, not kernel behavior.
The forge profile is also not certified by the normal test lane; only the
smart-HTTP repository boundary is.

Checkpoint/witness formats, key rotation, a network watch service, frontier
retrieval, capability token semantics, and throughput/latency targets remain
outside this spike. The pre-append hook is present, but ships with no policy.
