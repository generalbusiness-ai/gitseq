# Witnessing gitseq heads in an external transparency log

2026-08-18. **Status: draft — captured for later prioritization, not queued
for implementation.** This note records an idea raised while planning the
chess auth work. It is deliberately not a specification; it is enough to
prioritize against later.

## What this is, and what it is not

This is about publishing a signed *checkpoint of the gitseq head* to an
external, append-only transparency log — sigstore/rekor is the concrete
example — so that "at time T, this repository's head was X" becomes an
independent, tamper-evident public fact.

It is **not** the in-log witnessing already sketched in
[`2026-08-13-second-application.md`](2026-08-13-second-application.md). That
note's "witness key" endorses an OIDC / GitHub-login *identity binding* from
inside the log, as part of onboarding. This note is the opposite direction:
an *outbound* attestation of the whole log's head to infrastructure outside
it. The two are complementary and independent — one is about who an actor
is; this one is about whether the log itself has been rewritten.

It is also **not** pluggable actor signing. A companion proposal — abstracting
the actor `Signer`/`Verifier` so sigstore could substitute for ssh-ed25519 —
was considered and **declined**, because keyless, short-lived sigstore
identities fight gitseq's core property: a stable actor id whose signature
stock `git verify-commit` validates offline, forever. Sigstore appears in
*this* note only as the transparency log for head checkpoints, never as the
signing primitive for events.

## The problem it addresses

gitseq's durable compare-and-swap on the sequence ref gives an authoritative
order for everything that goes *through* it. What it cannot catch by itself
is an actor with write access to the git repository going *around* it: a
force-moved ref, a forked history, or a rollback to an earlier head. The
append-only log verifies internally — every event is a signed commit — but a
second, divergent, equally-well-signed history is also internally valid. Only
an outside observer who remembers the previous head can tell the two apart.

A transparency-log witness is exactly that outside observer, made durable and
public. It directly complements two open security items:

- **P1 — multiple residents for one repository** (`85fe10f1`). Two residents
  racing on one repo is the local version of the same hazard; a witness is
  the global version of the answer.
- The **"authoritative order across concurrent submissions"** claim itself.
  Today that claim rests on the operator not rewriting the ref. A witness
  turns a silent rewrite into a detectable event.

## Shape (sketch, not a spec)

- **Attestation payload:** something like `{genesis, head, depth, timestamp}`,
  signed. The head and depth are the load-bearing fields; genesis scopes it to
  one repository; the timestamp orders witnesses.
- **Submission:** periodically publish that attestation to the transparency
  log — cadence to be decided (per-merge, or time-based, or both). This is
  out-of-band and asynchronous.
- **Verification:** a `gs verify --witness`-style check that, given a head,
  confirms a consistent witness chain exists in the log and that the current
  head descends from every witnessed head (no rollback, no fork).

## Why it fits gitseq cleanly

- **Additive.** It does not touch event signing or the internal verification
  of the log. Offline verification of the log stays exactly as it is today.
- **Clean dependency boundary.** The online transparency log is required only
  for the *fork-detection guarantee*, never for the base offline
  verifiability of the history. This is the crucial difference from the
  declined pluggable-signing idea, which would have put an online, evolving
  dependency on the *critical* verification path. Here the new dependency
  buys a new, optional guarantee and nothing weaker replaces an old one.
- **Zero hot-path cost.** Witnessing is periodic and asynchronous; the write
  path (~10 signed commits/second, dominated by `commit-tree -S`) is
  untouched.

## Open questions to settle before implementation

- **Cadence policy.** Per-merge is precise but chatty; time-based is cheap but
  leaves a detection window. Probably per-merge on `main` plus a heartbeat.
- **What key signs the attestation?** A deployment/witnessing key, not an
  actor key — this is the deployment asserting a fact about its own log, not
  an actor making a durable claim inside it.
- **Availability and retention** of the external log, and graceful behaviour
  when it is unreachable (witnessing must fail open for writes, never block a
  merge on an external service).
- **Operational meaning of a detected divergence.** A witness that disagrees
  with the current head is an alarm; the runbook for that alarm is part of the
  feature, not an afterthought.
- **Privacy.** A public transparency log leaks head hashes, depth, and
  timing. For a public project that is fine; for a private deployment it may
  need a private/permissioned witness instead of a public one.

## Where it belongs

With the security backlog (next to `85fe10f1` P1 and `f85f197b` P2), not with
the chess onboarding story — the two only share the word "witness." It needs
no dependency on any signing refactor. When prioritized, it starts with its
own durable request and a small spike: publish one witness, verify one head
against it, decide the cadence, then specify.
