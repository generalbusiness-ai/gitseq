# gitseq

Gitseq is a smallest-possible collaboration substrate over ordinary Git: a
signed, totally ordered durable log; deterministic application folds; and an
amnesiac live rendezvous. The first application is the workroom used to build
gitseq itself.

The design is in [`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md),
the dogfood plan in [`BOOTSTRAP.md`](BOOTSTRAP.md), and the agent contract in
[`SKILL.md`](SKILL.md).

## Build and verify

Go 1.26 and Git with SSH signing support are required.

```sh
make test
make vet
make build
```

The implementation currently grows out of the adversarial module under
`spike/`: the proven kernel remains intact while the workroom profile,
resident service, CLI, MCP adapter, and demo live alongside it.

## Attach a workroom to a repository

```sh
./bin/gs init --repo . --operator hugh
./bin/gs actor-add --repo . --as hugh --name codex --role agent
./bin/gs actors --repo .
./bin/gs serve --repo . --listen 127.0.0.1:7777
```

Open <http://127.0.0.1:7777> for the live projection. Durable commands may
submit directly or through the resident sequencer:

```sh
./bin/gs state --repo . --server http://127.0.0.1:7777 --as hugh \
  --kind request --text 'Build the projector' \
  --body to=@codex --body conditions='all tests pass' \
  --rests-on '<basis-event>'
./bin/gs status --repo .
./bin/gs verify --repo .
```

`state` accepts repeatable `--rests-on`, `--body key=value`, and
`--evidence name=path` flags. It prints the durable event identifier used by
later `ratify`, `supersede`, provenance, and source-commit `Rests-On:`
trailers.

The resident service deliberately refuses non-loopback listeners: it is a
trusted local multi-actor custodian, not a remotely authenticated server.
Request performers may be written as `codex`, `@codex`, or the actor
fingerprint; the signed event always contains the fingerprint.

## MCP

Run one custodial stdio process per client session, configured for exactly one
actor:

```sh
./bin/gitseq-mcp --repo . --actor codex --server http://127.0.0.1:7777
```

The adapter implements the stateless MCP `2026-07-28` shape: no initialize
handshake or protocol session, `server/discover`, cacheable `tools/list`, and
the eight tools in `SKILL.md`. `status` returns a composite cursor; pass it
back to `wait` explicitly. Presence is leased and session-bound. If the
resident service is down, durable tools continue directly against the local
sequencer and report a `degraded` live cursor; ephemeral tools remain
unavailable.

The canonical event identifier used by tools and `Rests-On:` trailers is
`git:<object-format>:<genesis>#git:<object-format>:<event-commit>`.

## Offline audit after a normal clone

A normal clone does not fetch custom refs. Attach them explicitly, then verify
without the service:

```sh
./bin/gs attach --repo /path/to/clone --remote origin --genesis '<genesis>'
./bin/gs status --repo /path/to/clone
./bin/gs provenance --repo /path/to/clone '<artifact-event>'
```

Attached clones are intentionally read-only unless local actor custody and a
sequencer endpoint are configured. Removing `.git/gitseq` and the extra
`refs/seq/*` fetch rule leaves an ordinary repository.
