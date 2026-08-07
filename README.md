# gitseq

Conversation that can become a verifiable shared record — without
turning every conversation into paperwork.

## Why

Every document in your organization is a cache, and nothing
invalidates it. The spec, the quote, the onboarding doc, the
dashboard, the slide — each is a cached rendering of some
conversation-state, serving reads long after the discussion moved
on. The workaround is humans doing cache invalidation by gossip
("wait, is this deck current?"), which is exactly as reliable as it
sounds. Agents make it worse: more is said, more is done, and it
matters more who stands behind each act.

Git already solved half of this — naming things — with content
addressing. A hash answers "is this the same bytes I saw before?";
it cannot answer "is this still true?" Currency needs a clock, and
git deliberately has no final one: main's order is editorial and
revisable, every branch is another partial order, every rebase moves
the hands. That is the right design for source code and the wrong
one for commitments. Gitseq adds the missing half — a sequenced log,
a monotonic clock with a journal attached, carried in ordinary git
refs. Immutable names for *what*; one final position for *when,
relative to everything else*.

On that clock, gitseq separates conversation from commitment. People
and agents talk freely in a workroom; the chatter is ephemeral and
honestly forgotten when the room empties — forgetting is enforced,
not accidental. When something matters, a participant deliberately
**sets it down**: a signed act at one final position, carrying the
evidence and the prior acts it rests on. What the acts are called —
a request, a decision, a claim — belongs to the room's practice, not
to the substrate: gitseq has no ontology.

The mechanism is three small rungs, and the power is only in their
composition:

1. **Every act gets a ticket number.** Signed at the door, final,
   the same number for every reader. "As of #4312" means one exact
   thing, forever — something no amount of merging can give you,
   because merge-order is written by participants, later, revisably.
2. **Acts point backward.** "Rests on #4290." "Replaces #4101." The
   organization's knowledge becomes a dependency graph pinned to the
   clock — not a wiki's link-soup, but edges with before/after facts
   attached.
3. **Every artifact is stamped with the ticket it was rendered at.**
   "Projected from #4312." Now *is this still true?* stops being a
   question you ask a person and becomes arithmetic: walk the log
   from the stamp to the head; if anything in the gap retires an
   ancestor of the stamp, the document knows it is wrong — and can
   say which section, because of which event, decided by whom.

So the record can answer the ordinary questions:

- What did we agree to?
- Who is waiting on whom?
- What evidence supports this claim?
- Was this adopted, disputed, withdrawn, replaced?
- Is this document still current — and if not, what made it stale?

The answers are projections, not decrees from a trusted server.
Every reader replays the same deterministic fold over the signed
record and reaches the same verdicts; invalid and unauthorized
attempts remain visible without gaining force.

The product of the lever is **the document that knows when it is
wrong**. The same primitive at other scales yields honest meeting
records (the record takes what was set down; the room forgets the
rest), agent flight recorders (every action attributable, ordered,
and tamper-evident by construction — a signed total order wearing a
security costume), and ratified standards. These are applications
reading one small substrate, not features built into it.

There are two components because coherence always takes two: a
memory order (who wrote first) and a snoop bus (tell the other
caches *now* that their line is dirty). The sequencer is the memory
order; the nexus is the snoop bus. Version numbers alone give you
correct-but-lazy staleness — you find out when you check. The bell
turns "you could find out" into "you get told," which is the
difference between an audit capability and a coherent system.
Collaborative editors already run this protocol inside one
document's bytes; nothing runs it across the dependencies *between*
an organization's claims — from the quote to the pricing decision to
the meeting where it changed.

This is practical now because models have made it cheap to extract
and re-render the few statements worth keeping, while agents have
made attribution and coordination urgent. The architecture was
always sound and always unaffordable; the costs collapsed. What
remains is small.

## What ships

Two thin services over an ordinary git repository: a **sequencer**
that admits signed events into one final order, and an amnesiac
**nexus** for presence, ephemeral conversation, and immediate change
notification. Folds — the deterministic readings that give events
meaning — belong to applications and to readers; gitseq defines the
record, never its interpretation, and never runs a fold on your
behalf.

The durable layer is stock git. Clone the whole room, verify every
signature offline, fork it, and continue under your own authority —
you leave with everything. The overlay adds meaning; it never takes
hostages.

The first application is the workroom being used to build gitseq
itself. The design is in
[`2026-08-05-gitseq-design.md`](2026-08-05-gitseq-design.md), the
dogfood plan in [`BOOTSTRAP.md`](BOOTSTRAP.md), the agent contract
in [`SKILL.md`](SKILL.md), and the demo cases in
[`demos/`](demos/README.md).

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
./bin/gs actor-add --repo . --as hugh --name codex --kind agent
./bin/gs role-grant --repo . --as hugh --actor codex --role ratifier
./bin/gs actors --repo .
./bin/gs serve --repo . --listen 127.0.0.1:7777
```

Actor `kind` (`human`, `agent`, or `service`) describes the principal; it
never grants authority. Roles are independent, durable authority grants. A role
grant is ratified through the workroom fold and `role-revoke` retires its
explicit grant, so an agent may be a ratifier without ceasing to be an agent.
“Capability” is reserved for the profile's short-lived nexus-signed bearer
tokens and their orthogonal claims.

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
