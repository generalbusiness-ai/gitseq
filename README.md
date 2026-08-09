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
**sets it down**: a signed event at one final position, carrying the
evidence and the prior events it rests on. How the fold reads those events —
a request, a decision, a claim — belongs to the room's practice, not
to the substrate: gitseq has no ontology.

The mechanism is three small rungs, and the power is only in their
composition:

1. **Every event gets a ticket number.** Signed at the door, final,
   the same number for every reader. "As of #4312" means one exact
   thing, forever — something no amount of merging can give you,
   because merge-order is written by participants, later, revisably.
2. **Events point backward.** "Rests on #4290." "Replaces #4101." The
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
[`notes/2026-08-05-gitseq-design.md`](notes/2026-08-05-gitseq-design.md),
the dogfood plan in
[`notes/2026-08-06-bootstrap.md`](notes/2026-08-06-bootstrap.md), the
agent contract in [`SKILL.md`](SKILL.md), and the demo cases in
[`notes/2026-08-06-demos/`](notes/2026-08-06-demos/README.md).

## Documentation

Go 1.26 and Git with SSH signing support are required.

```sh
make test
make vet
make build
```

[`docs/getting-started.md`](docs/getting-started.md) then takes a new
user along one path end to end: initialize a workroom, join over MCP, do
a piece of work, verify it, and audit it from a fresh clone.
[`docs/reference.md`](docs/reference.md) covers the commands, the MCP
tools, and the constraints worth knowing before deploying anything.

The implementation currently grows out of the adversarial module under
`spike/`: the proven kernel remains intact while the workroom profile,
resident service, CLI, MCP adapter, and demo live alongside it.
