# A second application: chess

2026-08-13. This note designs the second application to run on the gitseq
kernel: a complete chess service. Someone creates a game, someone else
joins, they move, someone wins, and many games live in one repository.
The design is small on purpose, and this note is the specification the
implementation work will be queued from.

## Start here: running a game

Alice wants to play chess with Bob.

1. Alice gets the chess program: she clones its repository from wherever
   it lives and builds it, or downloads a release. One binary.
2. She runs `chess init` in a fresh folder, then `chess serve`. It prints
   a local address. Opening it shows a lobby.
3. She creates a game. The app gives her an invitation link. She sends it
   to Bob.
4. Bob opens the link and taps **join**. No account, no sign-in, nothing
   to install. His browser quietly makes a signing key for him, and his
   join seats him as the opponent.
5. They play. Each move is a small signed record. Their friend Carol opens
   the watch link, sees the board move in real time, and chats with them
   in the side panel.
6. When checkmate comes, the application — not either player — declares
   the result.

Or Alice plays alone: she asks an agent to play against her. She creates a
game and hands her agent the invitation link; the agent joins with its own
signing key and plays its own moves. To the game an agent is just another
player — same vocabulary, same rules, same log — and every move it makes
is signed with its key, so the history always shows exactly who (or what)
played what.

Everything durable about that game now lives in an ordinary Git
repository. Push it to any host and the complete, verifiable history of
every game travels with it. Anyone with a copy can replay it and get
exactly the same games, moves, and results.

## What gitseq provides

Gitseq keeps a tamper-evident log of signed events inside an ordinary Git
repository. Its core — the **kernel** — proves exactly two things: who
signed each event, and in what order the events were accepted. It assigns
no meaning to them.

Meaning comes from an **application**. An application defines its kinds of
event (for chess: create, join, move, resign...) and a deterministic
interpreter — the **fold** — that replays the log from the beginning and
judges every event. An illegal move is recorded but judged ineffective: it
sits in the history forever, changing nothing. Replaying the same log with
the same application always produces the same state, on any machine, with
no network and no clock — every time-dependent judgement uses the
sequencer's signed timestamps inside the log, never the reader's clock.

The first application is Workroom, which gitseq's own developers use to
build gitseq. Chess is the second, and it exists partly to prove the seam
is real: if building chess requires touching the kernel, that is a finding
about the kernel, not a task for chess.

## The core decisions

**One repository, one application, chosen at init.** A repository declares
its application once, in the first records of its log, and that choice is
permanent: a chess repository is a chess repository for life, and moving
to a different application means starting a fresh repository. Games are
just entries inside the one log, so there is no namespace machinery —
nothing needs disambiguating.

The declaration — the **binding** — records the application's name, its
source commit as a format-qualified object id (`git:sha1:<commit>`, never
a bare hash), the URL it came from (as provenance, not as authority), and
the version hash of its fold. A binding is honored only at the head of the
log, or as a later replacement signed by the key that initialized the
repository; anything binding-shaped anywhere else has no effect. Every
program that opens a repository reads the binding first, then selects its
interpreter, then folds — never the other way around, so no program ever
interprets a log with the wrong application and repairs afterwards.
Because a chess program must read bindings without knowing anything about
Workroom, the binding's small vocabulary belongs to the host layer between
the kernel and the applications; it gets its own row in the architecture
reference when implemented.

Repositories created before this design have no binding. For them the rule
is fixed: no binding means Workroom, at the version the binary ships, and
the binding authority is the key that appears as operator in the log's
opening records. That authority sits below applications and is not revoked
by application-level role changes — a chess program has no roster to
consult. In practice this grants nothing new: the same key has held every
power since the first event. It is written here so it is a decision, not a
later discovery.

**An application is a Git repository, and installing it is clone-and-run.**
A gitseq application is an ordinary repository: a Go module that imports
the gitseq kernel and ships one binary. Its identity is a commit. To
install it, clone or fork it — from any host, over any transport — and run
what it builds. The fetch happens in git, where you chose the URL and can
read what arrived. The build and run are the same visible acts of trust as
for any software. Then `chess init` is purely local: the binary already
knows its own name, source commit, and fold version (Go stamps them in at
build time), and it writes the binding from what it is. No flags, no
network, nothing to copy by hand. A fork is honestly a different build,
and the binding records the fork's exact commit — which is exactly what
the log should say.

Publishing costs nothing beyond pushing. Tag releases and attach binaries
if you like; or publish source only and let consumers `go install` it. No
registry, no package format, no plugin system: the registry is Git
hosting, the package format is a module.

**Installing never executes anything.** Recording a binding runs no code,
and no gitseq command fetches, builds, or runs application code as a side
effect. The binding is there for verification: a binary states which
application and fold version it embodies, checks the repository's binding,
and on mismatch reports the log as *verifiable but not interpretable* —
readable proof of who signed what, with an honest refusal to guess what it
means. That honesty rests on something unconditional: signatures and order
are kernel facts, so even a wrong or malicious interpreter cannot forge
history, and anyone who obtains the genuine application at the pinned
commit can re-fold the log and recover the truth. Interpretation is
replaceable; the record is not.

**Upgrades are replacements, priced honestly.** Upgrading the application
means a new binding — signed by the initializing key, pinning the new
commit and fold version, replacing the old binding. A fold-version change
invalidates every reader's cached state by construction, so every program
then re-verifies and re-folds the whole log. For a long-lived repository
that is a real operational event, and it is scheduled like one.

## The host change in gitseq

Today `internal/app` joins the kernel to exactly one interpreter,
hardwired. It becomes a host: read the binding, select one interpreter,
once, at open — then fold. No per-event routing, no mixing of applications
in one log, so one application's fold never pays for another's records.
Workroom becomes the first registered profile, and its existing tests
prove the selection change alters no Workroom behavior. This is the single
boundary where one-application becomes many; the kernel does not change.

## Chess, the application

Chess is the right second application because its fold contains a real
rules engine rather than bookkeeping, and because who-may-act comes from
game state (whose turn it is) rather than from any roster.

- **Its own repository, from day one.** Chess is not a package inside the
  gitseq tree; it is a separate module that imports gitseq from outside.
  That makes it the first honest test of gitseq's public API — a boundary
  the in-tree tests can only gesture at.
- **Vocabulary.** `create` (game parameters, color assignment, and an
  optional invitation — an invited opponent's key, or the hash of a join
  secret), `join` (names the game; carries the join secret when the game
  demands one; the first join the fold accepts seats the opponent),
  `move`, `resign`, `draw-offer`, `draw-accept`, and `anchor` (links a
  player's session key to a persistent identity; see Identity). A game
  created without an invitation is explicitly open to all: anyone may
  seat, and the creator has chosen that. The invitation exists because
  the log is public and an open seat can be sniped by whoever submits
  first — no rate limit fixes a race that one request wins. The
  invitation link carries the join secret in its URL fragment, so "the
  link is the invitation" stays true and costs the invitee nothing; a
  watch link simply omits it.
- **The fold judges everything.** Illegal move, wrong turn, second join:
  recorded, ineffective, and the interface says so. Checkmate and
  stalemate are computed by the fold, so the result is a fact of the
  replay and no event can lie about it. No network calls in the fold, and
  all expiry judged on log-internal time — the determinism rules above.
- **Games and seats.** A game is identified by its create event. Many
  games share one repository; chess is turn-based, so contention is
  negligible. A seat belongs to the player's anchored identity if they
  have one, otherwise to their session key. That rule is what makes
  recovery work: an anchored player whose browser lost its key — cleared
  storage, a new device — makes a fresh session key, anchors it to the
  same identity, and sits back down. An unanchored seat is honestly bound
  to a key that can be lost, and the interface says that too.
- **Players are keys.** No roster, no names, no roles, no import of
  anything from Workroom.

## The complete application

The goal is a finished chess service, not a demo. Its features divide
along gitseq's own seams, which is much of why it is worth building:

- **Durable acts** are the game: create, join, move, resign, draw,
  anchor. This is what replays identically forever from any clone.
- **Ephemeral presence** is who is here right now: players and watchers
  hold short-lived leases on the game's live channel, so the watcher list
  is live and honest, and presence frames carry motion hints — a piece
  being dragged, a move being submitted — so the board animates smoothly
  instead of jumping when the record lands.
- **Ephemeral messaging** is the chat panel: one live conversation per
  game, which dies with the session by contract and never pretends to be
  a record.

The truth discipline is strict. The board renders only what the fold has
judged. Presence may preview motion, but a move lands only when the
durable record carries it, and a refused act is surfaced, not swallowed —
the loser of a join race is told the seat was taken, and a refused move
snaps back with the reason. A freshly started server is still replaying
the log, so "the board may be behind" is a normal, named state on screen —
never a stale board presented as current.

The architecture mirrors gitseq's own service, one layer at a time:

- **One binary** ships kernel, chess fold, projections, HTTP service, and
  the embedded browser UI. Local play and web play are the same binary.
- **Read path.** Bounded projections: the games list (open, playing,
  finished) and each game's state at a cursor. Plus one query that keeps
  the browser honest: legal destinations for a selected piece, answered
  by the fold's own engine. The browser holds no rules engine — it
  proposes, the fold disposes. There is exactly one implementation of
  chess in the system.
- **Write path.** The browser signs each act with its session key and
  POSTs it to the submit endpoint. The signer is small TypeScript over
  WebCrypto; its encoding is pinned by cross-tests against the Go
  implementation, and the kernel's byte-exact refusal of non-canonical
  input is the backstop that makes any near-miss loud rather than
  dangerous. Nothing heavy rides the join path.
- **Live path.** One event stream per game carries durable appends and
  live frames, each labeled with which world it belongs to, under two
  separate cursors. The server joins them for transport; the client never
  confuses them.
- **Agents are players.** The chess binary also exposes its acts over an
  MCP adapter — list games, show the board, query legal destinations,
  move, resign — so a conversational agent can play without a browser,
  exactly as gitseq's own service does for its workroom. The agent brings
  its own chess judgment; the fold judges legality identically for every
  player, and an agent's illegal move is refused the same way a human's
  is. If the agent's key was minted under a person's anchor (see
  Identity), the log also shows whose agent it is; either way its moves
  are its own signatures, distinguishable from its owner's forever.
- **Keys in the browser.** The session key is WebCrypto Ed25519,
  non-extractable, stored in IndexedDB — the wording matters, because an
  extractable key in ordinary web storage is one script injection away
  from theft. Browsers may evict script-writable storage (Safari does
  after seven days without a visit), so the app asks for persistent
  storage *lazily* — after the first move has landed, or at anchor time —
  because that request can itself prompt in some browsers and the join
  path stays at zero prompts. The interface names the eviction hazard
  honestly; the durable fix is anchoring, which also carries a seat to a
  second device. Watching needs no key at all; joining, chatting, or
  appearing in the watcher list mints one silently.

## Running it on the web

A public deployment is the same binary, a Git repository, and secrets that
are counted honestly below. It follows the tier-2 ladder in
`notes/2026-08-07-deployment.md`, and adopts — or explicitly amends — that
note's three tier-2 invariants rather than inheriting them by reference:

- **One writer, enforced.** Exactly one process holds the writer lease
  per repository; a process without the lease refuses durable writes and
  says so. Rolling deploys briefly run two containers as a matter of
  course, so the lease is a blocker, not a nicety.
- **Acknowledge after the push.** When the repository of record lives on
  a forge (GitHub or similar), the forge ref is the authority. A move is
  acknowledged to the player only after the log advance has been pushed
  fast-forward there; a rejected push means no acknowledgment, and the
  move is resubmitted against the true head. No player is ever told
  "accepted" about a move that could be orphaned.
- **The submit endpoint is open, by design.** The deployment note
  requires networked submissions to carry capability tokens fronted by an
  identity provider. A public game server amends that scope deliberately:
  anyone may submit, because zero-setup play is the point, and record
  authority (the signature on every act) never depended on transport
  authorization anyway. The capability chain remains the rule for
  deployments that need authorization; a public application profile opts
  out on purpose and says so. Volume abuse is answered by kernel bounds
  and rate limits; seat-sniping is answered by the invitation, because it
  is a race, not a load problem.

The shape: the container clones the game repository at boot, appends under
the lease, and pushes each advance. Durability is the forge plus any other
clones, stated plainly as "how many copies, refreshed how often". The
deliverable is a recipe — a `Dockerfile` and a platform file in the chess
repository — and deploying is: create the data repository on the forge,
set two secrets (the sequencer's signing key, and the credential that lets
the container push to the forge), launch, share the URL. Enabling "log in
with GitHub" adds a provider app registration, its client secret, and a
witnessing key whose public half is recorded in the log at setup so those
identity bindings outlive the deployment (see Identity). The login
round-trip returns to the game's own origin, because a cross-origin
callback would strand the browser's key in an unreachable storage
partition.

The end state is not container operations. This runtime — a tiny
single-writer sequencer, a static UI, git storage — wants a purpose-built
platform with deploy-from-repository ergonomics: point it at an
application repository and a data repository, press deploy, get a domain
in seconds. The nearest existing parallel is Cloudflare Artifacts
(git-protocol storage fronted by edge compute), but that ecosystem is
TypeScript-first and the fold is Go, so it is a reference for the shape,
not a base. The container recipe is the interim that proves what the
platform will need; building the platform is scheduled by adoption, not by
this note.

## Identity

Identity lives below the application, so no application reinvents it. The
kernel already has exactly the right amount: an actor is a key, and the
kernel proves who signed. Everything above that — minting session keys,
the attestation vocabulary, the anchor ladder, agent credentials — is one
shared library and vocabulary in the host layer, inherited by every
application. Chess names `anchor` in its vocabulary and defines nothing
else about identity.

A browser-minted key is enough to play, always. Opening a link and moving
requires zero setup, zero accounts, zero prompts. Everything beyond that
is an upgrade, never a requirement.

The upgrade is an **anchor**: a statement linking the session key to a
persistent identity, carrying that identity's endorsement of the key, the
repository, a scope, and an expiry. Verification happens in the
application layer; revocation is expiry plus a superseding statement, so a
revoked key is provable from the log alone.

Anchors differ on two independent axes, and the interface shows both:

- **Who vouches.** *Self-signed* — the user's own root key signed the
  endorsement — is stronger than *witnessed* — the deployment's key says
  a provider said so.
- **How it verifies.** A signature carried in the log verifies offline,
  forever. A claim needing a live third-party lookup verifies only while
  that third party cooperates, and may answer differently later.

Three anchors, on that grid:

- **Nostr** (self-signed, verifies in-log — strongest on both axes). A
  NIP-07 browser extension holds the user's persistent key and signs the
  endorsement — typically two extension prompts on first use, and only
  for users who already run one; for everyone else this path honestly
  begins with installing an extension and backing up a key, which is why
  it is not the first rung built. The endorsement is shaped like a Nostr
  delegation (NIP-26), so aligning with that ecosystem costs nothing. Its
  secp256k1 signatures are verified in application code only; that curve
  never enters the kernel.
- **"Log in with GitHub"** (witnessed, verifies in-log — the
  lowest-friction rung). One redirect round-trip, one first-time consent
  screen at the provider, no extension, no key handling. The provider's
  token is short-lived and cannot itself be the record, so the deployment
  verifies it and signs the binding with its own ordinary key — a witness
  whose word readers weigh like any actor's, and whose public key is
  recorded in the log so bindings outlive the deployment. Whoever holds
  that key could mint false bindings; that risk is inherent to
  witnessing, and sequencing bounds it, since every binding occupies a
  signed position in the log.
- **A published forge signing key** (self-signed, but verified by live
  lookup — weakest on verifiability, whatever its vouching strength). The
  check fetches the user's published keys from the forge at verification
  time; the user can delete the key later and retroactively unverify
  their own history. Like the OIDC check, it runs outside the fold and
  its result is signed into the log. The browser cannot produce this
  proof at all — it takes the user's own SSH tooling at a terminal — so
  this anchor is offered on the command line only.

The same statement shape mints **agent credentials**: a person's anchored
identity endorses an agent's signing key, with scope and expiry. An agent
credential is exactly as strong as the anchor that minted it, on both
axes, and the interface says which. (Block's Buzz workspace ships the same
dual-signature idea — human-owned agents signing their own work — on
Nostr; here the endorsement, its scope, and its revocation live in the
signed log itself, so they are provable later rather than taken on faith.)

## Onboarding paths, with budgets

Every review of this design and its implementations walks these paths end
to end and reports the friction found: each step from first contact to
first effective act; every prompt, install, redirect, and copy-paste;
every step removable without weakening record authority; every step whose
failure is silent. A path that cannot be walked step by step in the
reviewer's head is a finding, and so is a path without a budget.

- **Spectator.** Receive a watch URL, open it, see the board. Budget: two
  steps, no key, no prompt. A cold or catching-up server is named on
  screen, never silent. Entering the watcher list or chat mints a session
  key silently — still zero prompts.
- **Anonymous player.** Receive an invitation URL, open it, tap join,
  move. Budget: zero prompts before the first move — persistent-storage
  permission is deliberately deferred until after the first move so this
  stays true in every browser. A lost join race is reported. The key can
  be evicted with the browser's storage; the interface says so and offers
  anchoring, never requires it.
- **Anchored player, GitHub login.** The anonymous path, then one
  redirect round-trip with one first-time consent screen. Zero installs,
  zero copy-paste.
- **Anchored player, Nostr.** The anonymous path, then two extension
  prompts — for users who already run a NIP-07 extension. For anyone
  else, honestly: an extension install and a key backup first. Never
  required to play.
- **Agent credential.** An anchored person mints an agent key, endorses
  it, and hands the agent its key and the repository. Budget: three
  manual steps, measured at acceptance.
- **Alice asks an agent to play.** For an agent that already has its key
  and MCP access to the service: paste the invitation link. Budget: one
  step. First-time setup is the agent-credential path above, plus
  pointing the agent's MCP configuration at the deployment — counted
  when walked, and the walk must include an agent that has never seen
  the service before.
- **Deployer.** Create the data repository on a forge; clone the chess
  repository; set two secrets (sequencer key, forge push credential);
  launch; share the URL. Budget: one repository, two secrets, one launch
  command. GitHub login adds a provider registration, its client secret,
  and the witnessing key — three more setup steps.
- **Self-hoster.** Clone (or fork) the chess repository, build, `chess
  init`, `chess serve`, play on loopback. Budget: two commands after
  clone. This is the acceptance path, and it is the whole install story.

## What this gives up

Mixed-application repositories, and in-place migration between
applications: both deliberately. Mixing would make every fold pay rent for
records it cannot read, and fresh repositories are what migration is for.
Cross-references stay possible — event identifiers embed their log's
genesis, so they are globally unambiguous. And a creator who declines an
invitation accepts an open, enumerable, snipeable game; the vocabulary
records that as a choice, not a defect.

## The work, in order

Once this design is ratified and merged, each item below is queued as its
own implementation request.

1. The contract change: the host-level binding vocabulary with its new
   row in `docs/reference/architecture.md`, the read-binding/select/fold
   order, and the legacy no-binding-means-Workroom rule — one head.
2. Init-time self-binding and host selection at open in `internal/app`,
   with Workroom as the registered default and its existing tests proving
   zero behavior change.
3. Whatever public-API surface the external importer needs, driven by
   actually importing it, not speculation.
4. The chess repository: vocabulary (invitation and anchor included),
   rules-engine fold on log-internal time, per-game projections, the
   legal-destination query, and a minimal binary with init, serve,
   create, join, move, board, and resign — plus the MCP adapter exposing
   the same acts so agents can play.
5. The chess UI, embedded in the same binary: lobby, game view, board and
   pieces from the durable fold, presence-animated moves, watcher list,
   and the single per-game chat — the complete-application section above
   is its specification.
6. The web deployment: writer lease, acknowledge-after-push, the open
   submit intake as amended above, the deploy recipe, and the browser
   signer with parity tests against the Go encoder.
7. The identity layer in the host: attestation vocabulary, the two-axis
   display, GitHub-login witnessing with the log-anchored witness key,
   and the Nostr anchor.
8. Acceptance, two variants: a fresh repository, clone-and-run, two
   keys — one seat played by an agent over the MCP adapter — and a game
   played to checkmate with the fold projecting the result;
   then the same game through two browsers against a container-hosted
   deployment whose repository of record is on a forge — one player
   anonymous, one anchored, seat recovery exercised after a deliberately
   cleared browser store — with the onboarding paths above walked and
   their friction counts recorded as part of the review.

---

Provenance: this design rests on the ratified decision that the fold stays
pure and total and unknown kinds stay opaque,
`git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b15de2f8788788a1afe970d6d077f7843862ebf2`.
