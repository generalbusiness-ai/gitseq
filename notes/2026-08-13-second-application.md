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

## Three decisions

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
5. Acceptance: a fresh repository, `gs init --app` pointing at the chess
   repository at a pinned commit, two actor keys, a game played to
   checkmate, and the fold — not a player — projecting the result.
