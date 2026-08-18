---
title: gitseq documentation
summary: Map of the documentation set, and how to choose a starting point.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bbe37f00315605cfc6d6306cc9d815650a7589d8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8fed27446d2441119f676fd3fb0b3cbb3a038ec8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcdbd7cb5b058a09c59b4540d5157f46116026bc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b749bdeade8afec544344727fb77b5c89348e705
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6adcbc51bb03c6c0b0f8ba6601f23194b033f4b9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f79617aa07d856b3773f60aa2ae464d41c34fbe5
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4eeb3acf8ba29c41c1076d8eb54dadb37463de51
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# gitseq documentation

gitseq is an overlay on an ordinary git repository. It gives every
deliberate act a final position in one sequence, signed by whoever made
it, so that a document can find out when the thing it describes has
moved.

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
| [Publish and audit](how-to/publish-and-audit.md) | Share the sequence; verify it from a clone you did not create. |
| [Deploy a resident](how-to/deploy-a-resident.md) | Run the local service, and what it trusts. |
| [Configure an agent](how-to/configure-an-agent.md) | Attach an MCP client and check that it can act. |

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
  [review](reference/gs/review.md),
  [merge](reference/gs/merge.md),
  [ratify](reference/gs/ratify.md),
  [supersede](reference/gs/supersede.md),
  [status](reference/gs/status.md),
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
  [work](reference/mcp/work.md),
  [inspect](reference/mcp/inspect.md).
- [Live attention](reference/live-attention.md) — the advisory adjunct
  every MCP tool result carries.
- [Event identifiers](reference/event-identifiers.md) — the one name
  everything else cites.
- [Limits](reference/limits.md) — sizes and counts a call is refused for
  exceeding.
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
