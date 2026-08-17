# gitseq

A simple layer over git, and the result is a strong multi-agent workroom.
Use it to accelerate software development, strengthen review cycles, or
as a platform for other collaborative applications where the fundamental
data structure is a log of immutable signed transactions.

Blog:
[Coordination and Traceability: Not Two Problems](https://generalbusiness.ai/blog/2026-08-09-gitseq/)

Intro walkthrough:
[![gitseq demo introduction](gitseq.png)](https://youtu.be/LwVhU3mNXnM)

## Why

Suppose we want to build a Kanban board. Tasks on the board need attributes
such as status, assignee, description, and so on.  

A traditional application might store those attributes as columns in a database.
Updates replace their previous values, while workflow rules live elsewhere in
the application.

With gitseq, there's a different way.  If I want to change the status of a 
task, I just write a _log entry_ with a different status (or a correction,
revision, request, claiming or assigning a task, or whatever). We track the
**acts** as immutable events.  Each is signed by its author, admitted to one
verifiable order, and connected to the task and its history through strong
references.

The current state of the task becomes a projection of that immutable log.
It can be recalculated at any time, independently verified, and traced back
through every act that produced it.

The kernel is simple: _ordinary Git storage_ plus a _signed sequencer_.
An act’s signed payload can cite any immutable Git object, such as a blob,
tree, commit, tag, or another gitseq event.  Above this, applications define
their own object types, actions, projections, and rules for deciding which
acts take effect.

Alongside the durable sequence, the resident service hosts the _nexus_
for live, ephemeral coordination: presence, activity and focus, and signed
conversation. Actors using MCP or the browser UI can see which live
participants are focused on particular events and exchange messages.

The first application is the workroom being used to build gitseq itself.
It uses the _language-action perspective_ to describe acts such as requests,
promises, reports, agreement, disagreement, and conditions of satisfaction;
these acts become a lightweight framework for getting things done.

In practice, it's a great tool for working with two or more agents.  Each 
gets a name, role, and a strong identity.  You can chat about a request, then
formalize it, and the agents will work together until it's satisfied - leaving
a full audit trail along the way.

The second application? A [chess game](notes/2026-08-13-second-application.md).

## Getting Started

Follow [Getting started](docs/getting-started.md) for first-time
initialization.  Heads-up: in this preview the one-time [`gs init`](docs/reference/gs/init.md)
(to initialize gitseq in a git repo) is a bit of a chore.

To run a local server (including its web UI):
```
make build
./bin/gs serve --repo /path/to/repo --listen 127.0.0.1:0
```

Give your agents the [SKILL.md](SKILL.md), and prompt:
```
Using the gitseq MCP, check for work items and prioritize
appropriately to keep progressing.  Dispatch tasks to max 3
subagents.  Continue checking every 10 minutes indefinitely.
```

> **Technical preview.** The repository is usable for local workrooms and
> offline audit, but it is not yet a hardened multi-tenant service.

## The Kernel

The kernel sequencer is very simple:

* A series of events produce a log. The events are stored and linked in
  git, under a ref `refs/seq/<genesis-oid>` which points at the head of
  the log.
  ```
  refs/seq/id → eventₙ → eventₙ₋₁ → … → genesis
  ```

* Each event is a commit with an ordinary git tree: an `event` blob
  and optional blobs under `attachments/`.  The event is signed by the
  actor producing it; the sequencer admits it, creates and signs a
  commit, to produce an authoritative sequence.

* Git is the log store: `git hash-object` and `git mktree` assemble
  the event, `git commit-tree` links and signsit ; `git update-ref`
  atomically advances the sequence head.

* There's no working tree, staging area, branch checkout, merge, or
  ordinary `git commit` involved in the sequence itself.

It's just a signed, content-addressed log store, with an authoritative
order across concurrent submissions from cryptographically-identified
actors.

## The Nexus

(todo)

## The Applications

(todo)

## Documentation

Go 1.26 and Git with SSH signing support are required. The complete UI gate
also uses Node.js 24 and npm.

```sh
make test
make vet
make build
```

[`docs/`](docs/README.md) is the user documentation set. Start at
[why gitseq exists](docs/why.md) for the idea, or go straight to
[one path end to end](docs/how-to/end-to-end.md) — initialize a workroom,
do a piece of work, review it at an exact head, and audit it from a fresh
clone. There are concept pages for how it behaves, recipes for common
tasks, and a reference page for every `gs` subcommand and every MCP tool.

Each page names the durable acts that govern the behaviour it describes,
so a page flares when its behaviour moves. Four gates enforce that, and
`make test` runs them; see [Anchoring](docs/anchoring.md).

Clone the public repository and run the complete local gate:

```sh
git clone https://github.com/generalbusiness-ai/gitseq.git
cd gitseq
npm ci --prefix ui
make vet test race build ui spike
git diff --exit-code
```

This technical preview is distributed under the [MIT License](LICENSE).
Read the [security policy](SECURITY.md) before using it with sensitive
material; vulnerability reports go through GitHub's private advisory channel,
not a public issue or workroom.

The shipping Go module lives at the repository root. `cmd/gs` and
`cmd/gitseq-mcp` build the two user-facing binaries, while `internal/` holds
the kernel, workroom profile, and services. `spike/` is deliberately narrower:
it keeps the adversarial CLI, report generator, forge fixture, and six-case
evidence that preceded the technical preview.
