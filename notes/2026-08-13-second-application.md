# A second application

2026-08-13. Design for review: what it takes to run an application other than
Workroom on the Gitseq kernel, decided small. The worked example is chess —
someone creates a game, someone else joins, they move, someone wins, many
games in one repository. Revised against review f9d9f9b7, which approved the
spine and requested changes where stated costs were lower than real costs;
every finding is resolved in place below.

The architecture contract already promises this is possible: the kernel
proves who signed opaque bytes and where they stand, and an application
interpreter decides what those bytes mean. The ratified fold-purity decision
(`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b15de2f8788788a1afe970d6d077f7843862ebf2`)
keeps unknown kinds opaque for exactly this reason. Nothing exercises the
promise yet. Chess is the first test that the seam is real: if building it
requires touching `internal/kernel`, that is a finding, not a task.

## The core decisions

**One repository, one application, bound at init.** A repository declares its
application once, in the bootstrap records at the head of the sequence, where
Workroom already does its governance bootstrap. The binding records the
application's name, its source as a format-qualified object id
(`git:sha1:<commit>`, never a bare hash), the provenance URL, and the
application's fold-profile hash. The binding is signed and permanent: a
repository is a chess repository for life, and changing applications means
creating a new repository. There is no namespace machinery because there is
nothing to disambiguate — the repository is the instance, and games are
entities inside its one log.

The binding is positional and host-level. A binding is effective only in the
bootstrap prefix, or as a supersession of an already-effective binding signed
by the initializing operator's key; anything binding-shaped anywhere else is
ineffective. Every host must read it before selecting an interpreter —
read-binding, then select, then fold, in that order, so no host ever folds
with the wrong interpreter and repairs afterwards. Because a chess host must
read the binding without holding Workroom, the binding vocabulary cannot live
in `workroom/*`: it is a small host-level schema family between the kernel
and the application profiles, and the implementing head adds its row to the
architecture layer table.

Repositories that predate this design have no binding record and cannot gain
one at genesis. The rule for them is explicit: absent a binding, the
application is Workroom at the profile the binary ships, and the binding
authority is the genesis bootstrap operator's key per the log. That authority
is host-level and is not revoked by application-layer roster changes — a
chess host has no roster to consult — which grants no new power in practice
(the same key held operator and ratifier from the first event) but is stated
here so it is a decision rather than a discovery.

**The application is committed in Git, and install is clone-and-run.** An
application is an ordinary Git repository: a Go module that imports the
Gitseq kernel and host packages and ships its own binary. Its identity is a
commit — the URL is provenance, a hint about where to fetch; the pinned
object id is the name. Installing is cloning (or forking) that repository
from anywhere and running what it builds. The fetch happens in git, where
the operator chose the URL and can read what arrived; the run is the same
visible trust act as running any software; and init is then purely local:
the binary embodies its own name, source commit, and fold-profile hash (Go
stamps module and VCS identity at build time), so `chess init` records the
binding from what the binary is. No `--app` flag, no ref resolution at init,
no copy-paste of a 40-hex hash, no network dependency on the bootstrap path.
A fork is honestly a different build — the binding records the fork's exact
commit, which is precisely what the log should say.

Publishing follows for free. Publishing with releases is tagging and
attaching binaries, which is what release tooling does. Publishing
source-only is pushing and doing nothing else; a consumer runs
`go install <module>@<tag>`. No registry, no package format, no plugin
loader: the registry is the Git hosting ecosystem and the package format is
a module.

**Install authorizes interpretation, never execution.** Recording a binding
executes nothing, and no gitseq command ever fetches, builds, or runs
application code as a side effect. What the binding buys is verification: a
host binary states which application and fold-profile hash it embodies,
checks that against the repository's binding, and on mismatch reports the
sequence as kernel-verifiable but application-uninterpretable — the honest
degraded state the architecture page already defines. The deeper guarantee is
unconditional and lives below the application: signatures, order, and payload
binding are kernel facts, so a wrong or malicious interpreter cannot forge
history, and anyone holding the genuine application at the pinned commit can
re-fold the log and recover the truth. Interpretation is replaceable; the
record is not.

Upgrades use the grammar the system already has: a successor binding
statement, signed by the initializing operator's key, pins the new commit and
fold-profile hash and supersedes the old binding. An upgrade is not free and
this note prices it: a fold-profile change invalidates every reader's
checkpoint by construction, so every host performs a full verified re-fold of
the log — a real operational event for a long-lived repository, scheduled
like one.

## The host change

`internal/app` is the deliberate coupling point today, and it joins the
kernel to exactly one interpreter, hardwired. It becomes a host that reads
the binding, selects one interpreter, once, at repository open, and only
then folds. There is no per-event routing and no interleaving of application
families in one log, so the fold of one application never pays for records
of another. Workroom becomes the first registered profile, and its existing
tests prove the selection change alters no Workroom behavior. This is the
single boundary at which the singular becomes plural; the kernel does not
change.

## Chess

Chess is the right second application because its fold has a real rules
engine rather than bookkeeping, and its authority comes from application
state rather than from a roster.

- **Its own repository, from day one.** Not a package in this tree that
  might someday move out. A separate module that imports Gitseq from outside
  is the only honest test of the public kernel and host API, which is the
  real deliverable hiding inside this project. `public_surface_test.go` and
  `layout_test.go` gesture at that boundary from within; a foreign importer
  proves it.
- **Vocabulary.** `create` (game parameters, color assignment, and an
  optional invitation: an invited-opponent key, or the hash of a join
  secret), `join` (rests on the create; carries the join secret when the
  create demands one; the first effective join seats the opponent), `move`
  (rests on the previous move or the join), `resign`, `draw-offer`,
  `draw-accept`, and `anchor` (a post-hoc attestation binding the player's
  session key to a persistent identity; defined under Identity below). A
  create with no invitation is explicitly open-to-all: anyone may seat, the
  game is enumerable, and the creator has chosen that. The invitation
  exists because the projection is public and open join is otherwise
  snipeable — a rate limit cannot fix a race that one request wins. The
  shareable game link carries the join secret in its fragment, so
  "the link is the invitation" stays true and costs the invitee nothing;
  a spectator link simply omits it.
- **Fold.** Deterministic and total, the same shape as the Workroom fold. An
  illegal move, or a move out of turn, is recorded but judged ineffective —
  the same append-then-judge pattern the Workroom fold uses for a malformed
  report. Checkmate and stalemate are computed by the fold, not asserted by
  a player, so the result is a projection fact and no event can lie about
  it. Every time-dependent judgement — attestation expiry included — is
  evaluated against log-internal time, the sequencer-signed timestamp of the
  position being judged, never the reader's clock; and no network request
  ever happens inside the fold. Two readers folding the same log get the
  same projection, always.
- **Games and seats.** A game is keyed by its create event's identifier.
  Many games run in one repository; turn order makes append contention
  negligible. A seat belongs to the player's anchored identity when an
  anchor exists, else to the session key. That rule is what makes recovery
  work: an anchored player whose browser key is lost — evicted storage, a
  new device — mints a fresh session key, anchors it to the same persistent
  identity, and resumes their seat. An unanchored player's seat is honestly
  bound to a key that can be lost, and the interface says so.
- **Identity.** Players are bare kernel actor keys. No roster, no names, no
  roles, no ratifier, and no import of `internal/workroom` anywhere.

## The complete application

The goal is a fully complete chess application, not a demo: multiple games
tracked in one repository; creating, joining, and watching; and within a
game, the board and pieces and a single live chat. Its surfaces divide
exactly along the system's own seams, which is the point of building it:

- **Durable acts** are the game: create, join, move, resign, draw, anchor.
  This is what replays identically forever from any clone.
- **Ephemeral presence** is who is here and what is in motion: players and
  watchers hold leases on the game's nexus coordinate, so the watcher list
  is live and honest; presence frames carry motion hints (a piece being
  dragged, a move being submitted) so the board animates in realtime rather
  than jumping on projection refresh.
- **Ephemeral messaging** is the chat panel: one live conversation per game
  over nexus `say`, dying with the session by contract, never pretending to
  be a record.

The truth discipline is inherited from the architecture page and is strict:
the board renders only what the fold has judged. Presence may *preview*
motion, but a move lands on the board only when the durable projection
carries it, and an ineffective act is surfaced, not swallowed — the loser of
a join race is told the seat was taken, and a move the fold refused snaps
back with the reason. Chat and presence never claim durability. A cold or
degraded projection is named on screen — under forge-primary storage a
freshly started server re-folds at boot, so "the board may be behind" is a
normal state with a visible name, never a silently stale board presented as
current.

The software architecture mirrors the resident, one layer at a time:

- **One binary.** The chess repository ships kernel, chess fold, projection,
  HTTP service, and the embedded static UI in one binary — the same
  composition `internal/service` uses today. Local play and web play are the
  same binary with the same routes.
- **Read path.** Bounded projections: the games list (open, in play,
  finished) and the game state at a cursor. Plus one query that keeps the
  browser honest: legal destinations for a selected piece, answered by the
  fold's own engine. The UI holds no rules engine — it proposes, the fold
  disposes. There is exactly one implementation of chess in the system, and
  the review's observation stands as the design rule: the second-encoder
  hazard was never the intent bytes (the kernel already refuses
  non-canonical bytes byte-for-byte at submit), it was a second rules
  engine, and the cure is to never write one.
- **Write path.** The browser signs canonical submission intents with its
  session key and POSTs them to the submit intake the deployment note
  already requires (R10). The signer is small TypeScript over WebCrypto; its
  encoding is pinned by cross-tests against the Go implementation (the
  `internal/wireparity` pattern), and the kernel's byte-identical refusal is
  the backstop that makes an almost-agreeing encoder loud rather than
  dangerous. No wasm requirement: nothing multi-megabyte rides the
  zero-prompt path.
- **Live path.** One event stream per game (SSE or WebSocket) carrying
  durable appends and nexus frames under their two distinct cursors — the
  durable/live separation the architecture page requires. The server joins
  them for transport; the client never confuses them, because the stream
  labels which world each event belongs to.
- **Keys in the browser.** The session key is WebCrypto Ed25519,
  non-extractable, in IndexedDB — the only storage that holds structured
  keys, and the wording matters because the extractable-key-in-localStorage
  reading is an XSS away from key theft. The page requests persistent
  storage (`navigator.storage.persist()`), and the interface names the
  eviction hazard honestly: script-writable storage can be evicted (Safari
  does so after seven days without interaction), so a long correspondence
  game on an unanchored key can lose its seat, and the durable fix is the
  anchor, which also carries the seat to a second device. Viewing is
  keyless; joining, chatting, or appearing in the watcher list mints the
  session key silently — still zero prompts.

## Running it on the web

A chess deployment is one binary, a Git repository, and one kernel secret:
the sequencer key. That is tier 2 of the ladder in
`notes/2026-08-07-deployment.md`, and this design adopts its invariants
explicitly rather than by reference — including the two the review found
omitted:

- **R2's writer lease, adopted.** Exactly one process holds the writer
  lease per repository; a process without the lease refuses durable writes
  and says so. Rolling deploys make overlap the default case, not the edge
  case, so the lease is a blocker for the web deployment, exactly as the
  deployment note rates it.
- **Acknowledgment follows the push.** Under forge-primary storage the
  forge ref is the authority, and a local compare-and-swap win is not
  custody. A move is acknowledged to the player only after the sequence
  advance has been pushed fast-forward to the forge; a rejected push means
  the move was not acknowledged and is resubmitted against the true head.
  No player is ever told "accepted" about a move that can be orphaned.

With those stated, the shape stays as before: the container clones the game
repository at boot, appends under the lease, pushes advances, and durability
is the forge plus attached clones, stated as replication. The deliverable is
a recipe — a `Dockerfile` and a platform file in the application repository —
and "run this on the web" is setting the sequencer-key secret and launching.
The OIDC anchor, when enabled, honestly costs more: a provider app
registration, its client secret, and the deployment's witnessing actor key,
whose public half is anchored in the repository's log at setup (the
deployment note's R1 issuer pattern) so that bindings remain verifiable
after the deployment rotates or dies. The OIDC callback is pinned to the
game's own origin, because a cross-origin round-trip would strand the
browser key in an unreachable storage partition.

The end state is not container operations. A gitseq application's runtime
shape — a tiny single-writer sequencer per domain, a static UI, git
storage — needs no full VM; it is closer to the platforms that deploy from
a repository push than to machines. So the destination is the deployment
note's tier-3 appliance wearing that ergonomics: a purpose-built
deploy-from-repository platform for gitseq applications, where deploying is
pointing at an application repository and a data repository, and a security
domain is minted per repository in seconds (R5). The nearest existing
parallel is Cloudflare Artifacts — git-protocol storage fronted by edge
compute — but that ecosystem is TypeScript-first and the fold is Go, so it
is a reference for the shape, not a base to build on. The tier-2 recipe is
the interim that proves the requirements the platform needs anyway; building
the platform is scheduled by adoption, not by this note.

Abuse of the open submit surface divides into two problems with two answers:
volume is transport policy (kernel bounds and admission-profile rate
limits), and seat-sniping is not — it is solved by the invitation in the
create vocabulary, because no rate limit fixes a race that one request wins.

## Identity

Identity lives below the application, so no application reinvents it. The
kernel's contribution is already exactly right and does not grow: an actor
is a key, and the kernel proves who signed. Everything above that —
session-key minting, the attestation vocabulary, the anchor ladder, agent
credentials — is one shared vocabulary and verification library shipped
with the host and inherited by every application profile. Chess declares
nothing about identity beyond naming `anchor` in its vocabulary; the
statement shape, verification, and display all come from the host layer.

A minted browser key is sufficient to play, and must remain so: opening a
game link and moving is the adoption story, and it requires zero setup,
zero accounts, and zero prompts. Everything beyond that is an upgrade, not
a requirement.

Persistent identity is **anchoring as attestation evidence**: a statement
carrying a persistent root identity's signature over the actor key, the
genesis, a scope, and an expiry. It lands either as evidence on the join or
as a free-standing `anchor` statement afterwards — the post-hoc path is the
normal one, since play-first-anchor-later is the ordering the adoption
story demands. The application layer verifies attestations; expiry is
judged against log-internal time (see the fold rules above); revocation is
expiry plus a superseding statement, so a revoked key is provable from the
log.

Anchors differ on two independent axes, and the display shows both rather
than collapsing them into one rung:

- **Who vouches.** Self-signed (the user's own root key made the
  attestation) is stronger than witnessed (the deployment's actor key says
  a provider said so).
- **How it verifies.** An in-log signature verifies offline, forever. A
  claim that needs a live third-party lookup verifies only while the third
  party cooperates, and can change its answer later.

The Nostr anchor is self-signed and in-log verifiable — the strongest on
both axes. NIP-07 browser extensions hold the user's persistent key and
sign the attestation (typically two prompts on first use — the extensions
prompt separately for the public key and the signature — and only for users
who already run one; for everyone else this path honestly begins with an
extension install and a key backup, which is why it is not the first rung
built). The attestation is NIP-26-shaped, so aligning with that ecosystem
costs nothing; the secp256k1 curve stays out of the kernel, verified in
application evidence-checking only.

The OIDC anchor — "log in with GitHub" — is witnessed but in-log
verifiable, and it is the lowest-friction rung: one redirect round-trip,
one first-use consent screen at the provider, no extension, no key material
in the user's hands. The provider's token is short-lived and audience-bound
so it cannot itself be the attestation; the deployment verifies it and
signs the binding with its own ordinary actor key, whose word a reader
weighs like any actor's, and whose public key is anchored in the log so the
binding outlives the deployment. The standing capability this creates —
whoever holds that key can mint a binding for any handle — is inherent to
witnessing; sequencing bounds it, since a minted binding still occupies a
signed position.

A forge signing key (the published SSH/GPG key on a GitHub profile) is
self-signed but *not* in-log verifiable — checking it means fetching the
handle's published keys from a third party, at that moment, and the user
can delete the key and retroactively unverify their own history. It is
therefore the weakest rung on verifiability despite its vouching strength,
the display says so, and the verification is factored like OIDC's: checked
outside the fold, with the checked result signed into the log. The browser
cannot walk this path at all (the proof requires a terminal round-trip
with the user's SSH tooling), so it is offered on the CLI path only.

The same statement shape is the agent-credential ladder: a human's
anchored identity attests an agent's Ed25519 actor key with scope and
expiry. An agent credential is only as strong as the anchor that minted
it, on both axes, and the projection says so. This is the dual-signature
chain of custody that Block's Buzz ships for its workspace — expressed
here in the log itself, where the attestation, its scope, and its
supersession are signed, ordered, and provable, which is the part Buzz's
public materials leave unspecified.

## Onboarding paths, enumerated for the friction trace

Every review of this design and its implementations must walk these paths
end to end and report the friction found: for each path, every step from
first contact to first effective act, the count of prompts, installs,
redirects, and copy-paste steps, every step that could be removed without
weakening record authority, and every step whose failure is silent. A path
that cannot be walked in the reviewer's head, step by step, is a finding.
Every path carries a budget; a path without one is a finding too.

- **Spectator.** Receive a game URL, open it, the board renders. Budget:
  two steps to first view, no key, no prompt. The projection's cold or
  behind state is named on screen, never silent. Appearing in the watcher
  list or chatting silently mints a session key — still zero prompts.
- **Anonymous player.** Receive an invitation URL, open it, tap join, move.
  Budget: zero prompts before the first move. The join secret rides the
  URL fragment, so the invitation costs the invitee nothing. A lost join
  race is reported on screen. The key custody and eviction rules above
  apply, and the interface offers anchoring as the durable fix without
  ever requiring it.
- **Anchored player, OIDC.** The anonymous path, then "log in with
  GitHub". Budget: one redirect round-trip, one first-use provider consent
  screen, zero installs, zero copy-paste. Lands a witnessed attestation.
  The callback returns to the game origin.
- **Anchored player, Nostr.** The anonymous path, then "link identity".
  Budget for a user already running a NIP-07 extension: two extension
  prompts, zero installs. For anyone else the path honestly begins with an
  extension install and key backup, and the interface says so instead of
  pretending. Never required to play. (The forge signing-key anchor is CLI
  only; it has no browser budget because it has no browser path.)
- **Agent credential.** A human with an anchored identity mints an agent
  keypair, attests it, and configures the agent with its key and the
  repository. Budget: three manual steps, measured at acceptance.
- **Deployer.** Clone the application repository, set the sequencer-key
  secret, launch, share the URL. Budget: one secret and one command after
  clone. Enabling the OIDC anchor adds a provider registration, its client
  secret, and the witnessing actor key — three more setup steps, disclosed
  here and counted when walked.
- **Self-hoster.** Clone (or fork) the application repository, build and
  run: `chess init`, play on loopback. Budget: two commands after clone.
  This is also the acceptance path, and it is the clone-and-run install
  story in its entirety.

## What this gives up

Mixed-application repositories, and in-place migration from one application
to another. Both are accepted costs. Interleaved families would make every
fold pay rent for records it cannot read, and fresh repositories are what
migration is for. Cross-references remain possible if ever wanted: event
identifiers embed the genesis, so they are globally unambiguous. A creator
who declines an invitation accepts an open, enumerable, snipeable game;
that is a choice the vocabulary records, not a defect.

## The work, in order

Once this design is ratified and merged, each numbered item below is queued
as its own implementation request.

1. The implementing head for the contract change: the host-level binding
   vocabulary and its new row in `docs/reference/architecture.md`, the
   read-binding/select/fold order, and the legacy default — updated in the
   same head that lands them.
2. Init-time self-binding and host selection at open in `internal/app`,
   with Workroom as the registered default and its existing tests proving
   zero behavior change.
3. Whatever public-API surface the external importer needs, driven by
   actually importing it, not speculation.
4. The chess repository: vocabulary (invitation and anchor included),
   rules-engine fold with log-internal time, per-game projection, legal-
   destination query, and a minimal `chess` binary with init, create, join,
   move, board, and resign.
5. The chess UI, embedded in the same binary: lobby, game view, board and
   pieces from the durable fold, presence-animated moves, watcher list, and
   the single per-game chat — the complete-application section above is its
   specification.
6. The web deployment: writer lease, acknowledge-after-push, the deploy
   recipe, and the browser signer with parity tests against the Go encoder.
7. The identity layer in the host: attestation vocabulary, the two-axis
   display, OIDC witnessing with the anchored attestor key, and the Nostr
   anchor.
8. Acceptance, two variants: a fresh repository, clone-and-run, two actor
   keys, a game played to checkmate, the fold — not a player — projecting
   the result; then the same game through two browsers against a
   container-hosted deployment whose repository of record is on a forge,
   one player anonymous and one anchored, exercising seat recovery after a
   deliberately cleared browser store, with the onboarding paths above
   walked and their friction counts recorded as part of the review.
