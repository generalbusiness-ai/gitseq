---
title: gitseq documentation
summary: Map of the documentation set, and how to choose a starting point.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:3e11a0d9e8061998f3e1b95f41242d7da5be20d2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# gitseq documentation

gitseq is an overlay on an ordinary git repository. It gives every
deliberate act a final position in one sequence, signed by whoever made
it. A tracked artifact can name the acts it rests on, so the projection
marks it for re-checking when one of those premises moves.

Read in this order if you are new:

1. **[Why gitseq exists](why.md)** — the problem, and the shape of the
   answer. Ten minutes, no commands.
2. **[Getting started](getting-started.md)** — build the two binaries and
   create a workroom, which everything below assumes you have.
3. **[Do a piece of work, end to end](how-to/end-to-end.md)** — one path
   from an empty directory to an audited record in a fresh clone. Every
   command runs.
4. **Concepts**, in any order, when you want to know why something
   behaves as it does.
5. **Reference**, when you know what you want and need the exact
   spelling.

## Concepts

| Page | What it settles |
|---|---|
| [The record](concepts/record.md) | What an event is, what the fold does, why a recorded act may carry no force. |
| [Actors and authority](concepts/actors.md) | Who may do what, and why kind is not authority. |
| [The work loop](concepts/work-loop.md) | How a promise becomes an exact artifact report, how an independently approved merge closes it, and when explicit reports still apply. |
| [Staleness](concepts/staleness.md) | What a flare means, what it does not cover, and one known gap. |
| [Connectors](concepts/connectors.md) | How work enters from GitHub, what a charter does, and what it deliberately does not. |
| [Components](concepts/components.md) | The CLI, the resident service, the MCP adapter, the browser view, and the repository underneath. |

## Recipes

| Page | Task |
|---|---|
| [End to end](how-to/end-to-end.md) | One complete path, from nothing to an audited record. |
| [Run a work loop](how-to/run-a-work-loop.md) | Request through review, merge, and ratification. |
| [Use it in a repository you already have](how-to/use-in-a-cloned-repo.md) | What it touches in an existing clone, across worktrees, and on push. |
| [Keep decision records](how-to/keep-decision-records.md) | Adopt a decision file, review and merge it, then revise or replace it. |
| [Publish and audit](how-to/publish-and-audit.md) | Share the sequence; verify it from a clone you did not create. |
| [Deploy a resident](how-to/deploy-a-resident.md) | Run the local service, and what it trusts. |
| [Configure an agent](how-to/configure-an-agent.md) | Attach an MCP client and check that it can act. |
| [Connect GitHub](how-to/connect-github.md) | Bring selected issues into a workroom and publish an exact candidate as a pull request. |

## Reference

- [Architecture layers](reference/architecture.md) — the boundary between
  the semantic-free kernel and replaceable application profiles.
- [`gs` subcommands](reference/gs/) — one page each:
  [init](reference/gs/init.md),
  [actor-add](reference/gs/actor-add.md),
  [role-grant](reference/gs/role-grant.md),
  [role-revoke](reference/gs/role-revoke.md),
  [actors](reference/gs/actors.md),
  [state](reference/gs/state.md),
  [batch](reference/gs/batch.md),
  [publish](reference/gs/publish.md),
  [review](reference/gs/review.md),
  [merge](reference/gs/merge.md),
  [merge-plan](reference/gs/merge-plan.md),
  [ratify](reference/gs/ratify.md),
  [supersede](reference/gs/supersede.md),
  [reassign-if-unclaimed](reference/gs/reassign-if-unclaimed.md),
  [status](reference/gs/status.md),
  [work](reference/gs/work.md),
  [artifacts](reference/gs/artifacts.md),
  [inspect](reference/gs/inspect.md),
  [reviews](reference/gs/reviews.md),
  [supersession-plan](reference/gs/supersession-plan.md),
  [staleness-wave](reference/gs/staleness-wave.md),
  [provenance](reference/gs/provenance.md),
  [verify](reference/gs/verify.md),
  [checkpoint-clear](reference/gs/checkpoint-clear.md),
  [serve](reference/gs/serve.md),
  [attach](reference/gs/attach.md).
- MCP reference pages currently in this documentation set:
  [whoami](reference/mcp/whoami.md),
  [presence](reference/mcp/presence.md),
  [status](reference/mcp/status.md),
  [wait](reference/mcp/wait.md),
  [say](reference/mcp/say.md),
  [ack](reference/mcp/ack.md),
  [state](reference/mcp/state.md),
  [ratify](reference/mcp/ratify.md),
  [supersede](reference/mcp/supersede.md),
  [reassign_if_unclaimed](reference/mcp/reassign_if_unclaimed.md),
  [work](reference/mcp/work.md),
  [artifacts](reference/mcp/artifacts.md),
  [inspect](reference/mcp/inspect.md),
  [merge plan](reference/mcp/merge_plan.md).
- [Live attention](reference/live-attention.md) — the advisory adjunct
  every MCP tool result carries.
- [Event identifiers](reference/event-identifiers.md) — the one name
  everything else cites.
- [Limits](reference/limits.md) — sizes and counts a call is refused for
  exceeding.
- [Performance evidence](reference/performance.md) — the versioned fan-out
  measurement contract and its current result.
- [Glossary](reference/glossary.md) — the vocabulary, in one place.

## How these pages stay honest

Every page here names the durable acts that govern the behaviour it
describes, and ships with its own artifact statement resting on them. So
when the behaviour moves, the page — that page, not the set — flares. The
convention, and the four gates that enforce it, are described in
[Anchoring](anchoring.md).

## Not user documentation

- [`SKILL.md`](../SKILL.md) is the normative contract for an agent
  working in a workroom. If you are configuring an agent, that is what
  the agent reads.
- [`notes/`](../notes/) holds dated design notes. They record thinking at
  a point in time and are not maintained.
