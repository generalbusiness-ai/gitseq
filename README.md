# gitseq

## Why

Every document in your organization is a cache, and nothing
invalidates it. The spec, the quote, the onboarding doc, the
dashboard, the slide — each is a cached rendering of some
conversation-state, serving reads long after the underlying
discussion moved on. Organizations run entirely on stale caches with
no coherence protocol; the workaround is humans doing cache
invalidation by gossip ("wait, is this deck current?"), which is
exactly as reliable as it sounds.

So the lever is one of the two famous hard problems: cache
invalidation. Git solved the other one — naming things — with
content addressing. That is the exact complementarity: a hash
answers "is this the same bytes I saw before?"; it cannot answer "is
this still true?" Identity versus currency. Currency is only
definable relative to a clock — valid as of when, compared to what
now — and none of git's clocks is final or total across writers:
main's order is editorial and revisable, every branch is another
partial order, every rebase moves the hands. A sequenced log is a
monotonic clock with a journal attached. Add it to git and you have
both halves: immutable names for *what*, an immutable position for
*when, relative to everything else*.

The ladder is three rungs, each small, and the power is only in
their composition:

1. **Every statement gets a ticket number.** Signed at the door,
   final, the same number for every reader. Now "as of #4312" means
   one exact thing, forever — something no amount of merging can
   give you, because merge-order is written by participants, later,
   revisably.
2. **Statements point backward.** "Rests on #4290." "Replaces
   #4101." Now the organization's knowledge has a dependency graph
   pinned to the clock — not a wiki's link-soup, but edges with
   before/after facts attached. Which retirements *count* is decided
   by a referee every reader can re-run: the same rules, the same
   verdicts, no server to trust.
3. **Every artifact gets stamped with the ticket it was rendered
   at.** "Projected from #4312." Now *is this still true?* stops
   being a question you ask a person and becomes arithmetic: walk
   the log from the stamp to the head; if anything in the gap
   retires an ancestor of the stamp, the document knows it is wrong —
   and can say which section, because of which event, decided by
   whom.

Rung 1 is a small sequencing service. Rungs 2 and 3 are
commit-message trailers plus that re-runnable referee. The product
of the lever is **the document that knows when it's wrong** — and
the other demos are the same primitive at different scales. Honest
minutes are rung 1 plus the right to remain off the record: the
record takes only what is deliberately set down, numbered and
unarguable, and the room honestly forgets the rest — forgetting is
enforced, not accidental. The agent flight recorder is rungs 1–2
under adversarial conditions: enterprise agent security is the
demand that every action be attributable, ordered, and
tamper-evident by construction rather than by trust in a platform —
a signed total order wearing a security costume. A standards body is
all three rungs plus a ratification convention. One primitive, every
scale.

There are two components because coherence always takes two. A
hardware cache-coherence protocol needs a memory order (who wrote
first) and a snoop bus (tell the other caches *now* that their line
is dirty). The sequencer is the memory order; the nexus is the snoop
bus. Version numbers without invalidation broadcasts give you
correct-but-lazy staleness — you find out you are stale when you
check. The bell turns "you could find out" into "you get told,"
which is the difference between an audit capability and a coherent
system. Collaborative editors already run this protocol inside one
document's bytes; nothing runs it across the dependencies *between*
an organization's claims — from the quote to the pricing decision to
the meeting where it changed.

And the reason to build it now: this architecture was always sound
and always unaffordable — capture and re-rendering costs killed
every prior attempt. Ambient recording and LLMs collapsed those
costs, and agents supply the demand side: they multiply both the
volume of statements and the need for attribution. What remains is
small: a wire discipline and two thin services over an ordinary git
repository.

Ordinary is load-bearing: everything here is stock git. Clone the
whole room, verify every signature offline, fork it and continue
under your own authority — you leave with everything. The overlay
adds meaning; it never takes hostages.

Gitseq is that overlay, smallest-possible: a signed, totally ordered
durable log; deterministic application folds; and an amnesiac live
rendezvous. The first application is the workroom used to build
gitseq itself. The design is in
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
