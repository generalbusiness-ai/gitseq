# A second application

2026-08-13. Design for review: what it takes to run an application other than
Workroom on the Gitseq kernel, decided small. The worked example is chess —
someone creates a game, someone else joins, they move, someone wins, many
games in one repository.

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
Workroom already does its governance bootstrap. `gs init --app <url>@<ref>`
resolves the ref and records a binding: the application's name, the source
URL, the resolved commit hash, and the application's fold-profile hash. The
binding is signed and permanent. A repository is a chess repository for life;
changing applications means creating a new repository. There is no namespace
machinery because there is nothing to disambiguate — the repository is the
instance, and games are entities inside its one log.

Repositories that predate this design have no binding record and cannot gain
one at genesis. The rule for them is explicit: absent a binding, the
application is Workroom at the profile the binary ships. Every new `gs init`
records a binding unconditionally, including for Workroom.

**The application is committed in Git.** An application is an ordinary Git
repository: a Go module that imports the Gitseq kernel and host packages and
ships its own binary. Its identity is a commit hash — the URL in the binding
is provenance, a hint about where to fetch; the hash is the name. This puts
applications on the same footing as everything else here: identity by
content-address, like the genesis, like event identifiers. It also makes
publishing a solved problem. Publishing with releases is tagging and
attaching binaries, which is what release tooling does. Publishing
source-only is pushing and doing nothing else; a consumer runs
`go install <module>@<tag>`. Installing is pointing at an existing repository
anywhere — any host, any transport — because the pinned commit, not the
location, is what the binding names. No registry, no package format, no
plugin loader: the registry is the Git hosting ecosystem and the package
format is a module.

**Install authorizes interpretation, never execution.** `gs init` does not
fetch, build, or run application code. Choosing which binary to run stays a
human act, exactly as for any software. What the binding buys is
verification: a host binary states which application and fold-profile hash it
embodies, checks that against the repository's binding, and on mismatch
reports the sequence as kernel-verifiable but application-uninterpretable —
the honest degraded state the architecture page already defines. No surface
invents meaning from field names or an old fold. The deeper guarantee is
unconditional and lives below the application: signatures, order, and payload
binding are kernel facts, so a wrong or malicious interpreter cannot forge
history, and anyone holding the genuine application at the pinned commit can
re-fold the log and recover the truth. Interpretation is replaceable; the
record is not.

Upgrades use the grammar the system already has: a successor binding
statement pins the new commit and fold-profile hash and supersedes the old
binding. The v1 authority rule is minimal — the initializing operator's key
may record it.

## The host change

`internal/app` is the deliberate coupling point today, and it joins the
kernel to exactly one interpreter, hardwired. It becomes a host that selects
one interpreter, once, at repository open, from the binding (or the legacy
default). There is no per-event routing and no interleaving of application
families in one log, so the fold of one application never pays for records of
another. Workroom becomes the first registered profile, and its existing
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
- **Vocabulary.** `create` (game parameters, color assignment), `join`
  (rests on the create; the first effective join seats the opponent), `move`
  (rests on the previous move or the join), `resign`, `draw-offer`,
  `draw-accept`.
- **Fold.** Deterministic and total, the same shape as the Workroom fold. An
  illegal move, or a move out of turn, is recorded but judged ineffective —
  the same append-then-judge pattern the Workroom fold uses for a malformed
  report. Checkmate and stalemate are computed by the fold, not asserted by a
  player, so the result is a projection fact and no event can lie about it.
- **Games.** A game is keyed by its create event's identifier. Many games run
  in one repository; turn order makes append contention negligible.
- **Identity.** Players are bare kernel actor keys. No roster, no names, no
  roles, no ratifier, and no import of `internal/workroom` anywhere. The
  nexus comes along for free for spectator chat, since live presence is
  application-independent; moves are durable, talk is ephemeral.

## Running it on the web

A chess deployment is one static binary (kernel, chess fold, embedded board
UI — the same shape as the resident today), a Git repository, and one
secret: the sequencer key. That is tier 2 of the ladder in
`notes/2026-08-07-deployment.md`, whose invariants this design inherits
unchanged — record authority is actor signatures, transport auth is
borrowed, and no tier ever asks an actor to surrender a private key to a
service.

The forge-primary storage shape from that note (R4) makes the deployment
nearly stateless: the container clones the game repository at boot, appends
locally, and pushes `refs/seq/*` advances back to the forge. Durability is
the forge plus attached clones, stated as replication, exactly as the
deployment note requires. A crashed container is the kernel's own failover
story. This shape fits commodity container hosts as they exist today, so
the deliverable is a recipe, not infrastructure: the application repository
ships a `Dockerfile` and a platform file, and "run this on the web" is
setting the sequencer-key secret and launching. The managed appliance
remains tier 3 — its seat is reserved by the deployment note, and nothing
here builds it.

The end state is not container operations. A gitseq application's runtime
shape — a tiny single-writer sequencer per domain, a static UI, git
storage — needs no full VM; it is closer to the platforms that deploy from
a repository push than to machines. So the destination is the deployment
note's tier-3 appliance wearing that ergonomics: a purpose-built
Render-or-Vercel-equivalent for gitseq applications, where deploying is
pointing at an application repository and a data repository and pressing
deploy, and a security domain is minted per repository in seconds (R5).
The nearest existing parallel is Cloudflare Artifacts — git-protocol
storage fronted by edge compute — but that ecosystem is TypeScript-first
and the fold is Go, so it is a reference for the shape, not a base to
build on. The tier-2 container recipe above is the interim that proves the
requirements the platform needs anyway; building the platform is scheduled
by adoption, not by this note.

Web play adds one component that does not exist yet: a browser signer.
Players are browsers, so actor keys live in the browser — WebCrypto
Ed25519, non-extractable, in local storage — signing canonical submission
intents over the same HTTP intake the deployment note already requires
(R10). There must be exactly one canonical-intent encoder, shared with the
Go implementation (compiled to wasm, or one implementation pinned by
cross-tests), because a second encoder that almost agrees is a fold
divergence wearing a different hat. Abuse of the open submit surface is
transport policy: kernel bounds and the admission profile rate-limit it,
and no identity system is invented to solve what a rate limit solves.

## Identity

Identity lives below the application, so no application reinvents it. The
kernel's contribution is already exactly right and does not grow: an actor
is a key, and the kernel proves who signed. Everything above that —
session-key minting, the attestation vocabulary, the anchor ladder, agent
credentials — is one shared vocabulary and verification library shipped
with the host and inherited by every application profile. Chess declares
nothing about identity: it names players as actor keys and gets anchoring,
display names, and agent credentials from the host layer. No application
defines its own attestation kind, and the kernel never learns what an
attestation is.

A minted browser key is sufficient to play, and must remain so: opening a
game link and moving is the adoption story, and it requires zero setup,
zero accounts, and zero prompts. Everything beyond that is an upgrade, not
a requirement.

Persistent identity is **anchoring as attestation evidence**, the pattern
the deployment note's R7 already defines for `github=<handle>`, with
stronger mechanics. A join or roster statement may carry an attestation: a
persistent root identity's signature over the actor key, the genesis, a
scope, and an expiry. The application layer verifies the attestation;
revocation is expiry plus a superseding statement — succession, the
grammar the system already runs on, so a revoked key is provable from the
log.

The first user-held-key anchor is Nostr, because its ecosystem is deployed
today: NIP-07 browser extensions hold the user's persistent key and sign
the attestation in one prompt, NIP-05 gives display names, and the
attestation is NIP-26-shaped — a delegation token with conditions and
expiry — so aligning with that ecosystem costs nothing. Nostr keys are
secp256k1, and that curve stays out of the kernel: Schnorr verification
happens in application evidence-checking only. GitHub signing keys remain
the parallel anchor for developer populations, as R7 states.

"Log in with GitHub"-style OIDC is the third anchor, and the
lowest-friction one, with an honestly weaker guarantee. In OIDC the user
authenticates at the provider and the provider hands the deployment a
short-lived signed token asserting the account name; the user holds no
signing key of their own — the provider does. The provider's token is
audience-bound and expiring, so it cannot itself be the attestation in the
log. Instead the deployment verifies the token and signs the binding —
`github:<handle>` to this actor key, scope, expiry — with an ordinary
actor key of its own, and the player's join statement carries that signed
binding as evidence. Witnessing invents no new kind of key: the deployment
is just an actor whose word a reader weighs. It does mean a witnessing
deployment holds this actor key alongside the sequencer key, and an OIDC
provider registration with its client secret besides; a deployment that
skips the OIDC anchor stays at one secret. The difference from the Nostr anchor is
who vouches: a Nostr attestation is self-signed by the user's root key and
verifiable offline by anyone forever; an OIDC attestation is witnessed —
the deployment's attestor says the provider said so — and is only as good
as that attestor's honesty at that moment. Both are the same statement
shape carrying evidence of stated strength: OIDC-witnessed below a
published GitHub signing key below a user-signed Nostr attestation. The
application displays which rung a binding sits on and invents no strength
it does not have.

The same statement shape is the agent-credential ladder: a human's
anchored identity attests an agent's Ed25519 actor key with scope and
expiry. An agent credential is only as strong as the anchor that minted
it: minted under a user-held key it is self-certifying; minted under an
OIDC-witnessed binding it carries the attestor's word, and the projection
says so. This is the dual-signature chain of custody that Block's Buzz
(July 2026, agents and humans as Nostr keypairs, agents carrying a second
owner-binding signature) ships for its workspace — expressed here in the
log itself, where the attestation, its scope, and its supersession are
signed, ordered, and provable, which is the part Buzz's public materials
leave unspecified. If Block documents their binding format, aligning is a
translation, not a redesign.

## Onboarding paths, enumerated for the friction trace

Every review of this design and its implementations must walk these paths
end to end and report the friction found: for each path, every step from
first contact to first effective act, the count of prompts, installs, and
copy-paste steps, every step that could be removed without weakening
record authority, and every step whose failure is silent. A path that
cannot be walked in the reviewer's head, step by step, is a finding.

- **Spectator.** Receive a game URL, open it, the board renders from the
  projection. No key, no prompt.
- **Anonymous player.** Receive a game URL, open it, a session key is
  minted silently, one tap joins (the first effective join seats the
  opponent), move. The budget is zero prompts before the first move.
- **Anchored player, OIDC.** The anonymous path, then "log in with
  GitHub": one redirect round-trip to the provider, no extension, no key
  material handled by the user. Lands a witnessed attestation signed by
  the deployment's attestor key. The budget is one redirect and zero new
  installs.
- **Anchored player, user-held key.** The anonymous path, then one
  deliberate "link identity" action costing one extension prompt (NIP-07)
  or one signing-key proof (GitHub signing key), landing a self-certifying
  attestation statement. Never required to play.
- **Agent credential.** A human with an anchored identity mints an agent
  keypair and its attestation, hands the agent the key material it needs,
  and the agent plays over CLI or MCP. The number of manual steps is the
  thing to measure.
- **Deployer.** Clone the application repository, set the sequencer-key
  secret, launch on a container host, share the URL. The budget is one
  secret and one command after clone. Enabling the OIDC anchor honestly
  costs more: a provider app registration, its client secret, and the
  deployment's witnessing actor key.
- **Self-hoster.** Install the binary, `gs init --app` against the pinned
  application commit, play on loopback. This is also the acceptance path.

## What this gives up

Mixed-application repositories, and in-place migration from one application
to another. Both are accepted costs. Interleaved families would make every
fold pay rent for records it cannot read, and fresh repositories are what
migration is for. Cross-references remain possible if ever wanted: event
identifiers embed the genesis, so they are globally unambiguous.

## The work, in order

1. Ratify this design. The binding record, the selection rule, and the
   legacy default change the architecture contract, so the implementing head
   updates `docs/reference/architecture.md` in the same change.
2. Init-time binding and host selection at open in `internal/app`, with
   Workroom as the registered default and its existing tests proving zero
   behavior change.
3. Whatever public-API surface the external importer needs, driven by
   actually importing it, not speculation.
4. The chess repository: vocabulary, rules-engine fold, per-game projection,
   and a minimal `chess` binary with create, join, move, board, and resign.
5. The web pieces, each its own head: the browser signer with the single
   shared canonical-intent encoder; the deploy recipe in the application
   repository; the attestation vocabulary with the anchor ladder
   (OIDC-witnessed, GitHub signing key, Nostr), OIDC first because it is
   the lowest-friction rung.
6. Acceptance, two variants: a fresh repository, `gs init --app` pointing
   at the chess repository at a pinned commit, two actor keys, a game
   played to checkmate, the fold — not a player — projecting the result;
   then the same game through two browsers against a container-hosted
   deployment whose repository of record is on a forge, one player
   anonymous and one anchored, with the onboarding paths above walked and
   their friction counts recorded as part of the review.
