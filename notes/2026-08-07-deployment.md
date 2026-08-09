---
date: 2026-08-07
status: draft/discussion — direction set by the operator in the
  2026-08-07 deployment discussion and promoted here: the availability
  posture is accepted, the Action-sequencer tier is worth a spike, and
  the container line is the main road, so its requirements run all the
  way to the managed appliance. Supersedes one sentence of the design
  note (per-ref forge permissions; see "the accepted availability
  posture").
origin: deployment discussion, 2026-08-07, outside the workroom;
  promoted by request.
---

# gitseq deployment — from one laptop to a managed appliance

## What "deployment" means here

The read path is already deployed. `git push 'refs/seq/*:refs/seq/*'`,
clone, `gs attach`, verify offline: any number of readers, on any
hosting, today. Sequence refs are namespaced by genesis
(`refs/seq/<genesis>`), so two workrooms overlaid on one shared repo
cannot collide. Nothing below concerns reading.

Two properties of that path are easy to overstate, and the rest of
this note depends on stating them narrowly.

**Publishing is not discovery.** Git does not fetch `refs/seq/*` in
either direction by default, which is a property this project relies
on — the overlay stays invisible to ordinary git use, and removing it
costs nothing. The cost is the other half: an ordinary `git clone`
does *not* bring the record down. A reader gets the workroom only by
running `gs attach`, which installs the fetch rule. So the record is
**publicly reachable to anyone who knows to ask**, not discoverable by
cloning, and no deployment may promise the second while providing the
first. Anything that needs to be found without instructions — the
genesis hash, the attach invocation — belongs in the repository's
ordinary content, where a clone does bring it down.

**Retention is a policy, not a property.** A published sequence
survives exactly as long as some host keeps the objects and some ref
keeps them reachable. Forge GC, repository deletion, an account
lapsing, or a retention policy nobody read will all take it, and
signatures do not prevent any of that — they let a reader detect a
*forged* history, not recover a *deleted* one. Durability therefore
requires stating who holds copies and how often they refresh:
publication to the forge is one copy, and one copy is not durability.
The minimum any tier may claim is what its stated replication
provides; a deployment with a single published copy and no attached
auditor should claim availability, not durability.

The deployment problem is the write path, and it is exactly two
questions:

1. **Transport** — how does a signed submission intent travel from an
   actor's machine to the process holding the sequencer key?
2. **Onboarding** — how does a new actor come to hold a keypair and an
   effective roster statement?

Everything else — the nexus, the UI, the MCP adapter — follows one of
those two or is deliberately absent at a given tier. The nexus in
particular is the *easy* part: it is amnesiac by contract, its loss is
the stated behavior, and the MCP adapter already degrades honestly
(durable tools keep working, `say` and `presence` refuse). A tier
where liveness lags durability is coherent, not compromised.

**The invariant across every tier** is the separation of two
credential planes:

- **Record authority** — actor Ed25519 signatures over canonical
  intents, judged by the roster fold. This never changes shape, at any
  tier. It is why exit stays free.
- **Transport auth** — who may reach the submit surface at all. This
  is *borrowed* from whatever the environment already runs: the OS
  account at tier 0, forge write access at tier 1, the organization's
  IdP behind the capability chain at tiers 2–3.

A deployment tier is an answer to "whose transport auth do we borrow."
No tier ever makes transport auth a substitute for record authority,
and no tier ever asks an actor to surrender a private key to a
service.

## The accepted availability posture

The design note claims the sequencer gets exclusive push to
`refs/seq/*` via "per-ref permissions on every major forge." That is
an overclaim and this note retires it. GitHub rulesets and branch
protections govern branches and tags; an arbitrary ref namespace like
`refs/seq/*` is writable by anyone holding repo write access, and the
same is true or configuration-dependent on other mainstream forges.
Gerrit-style per-ref ACLs are the exception, not the rule.

The posture, accepted deliberately:

- **Integrity does not depend on the forge.** Every sequenced commit
  is sequencer-signed; `attach` and subsequent fetches are non-forcing
  and refuse rewinds; a forged or rewound published copy is detected
  by every reader and accepted by none.
- **Availability does depend on the forge.** A writer with repo access
  can clobber or rewind the published `refs/seq/*`. That is vandalism
  — visible, attributable through forge audit logs, and recoverable
  by the key-holder re-pushing the true head. "Recoverable" is doing
  real work in that sentence and only holds if somebody still *has*
  the true head: an attached auditor's clone, or at tier 1 the
  control-repository mirror described with spike case 6. Recovery is a
  deliberate act by a party who holds a copy, never something the
  vandalised repository can do for itself. With no copy anywhere, this
  is detection, not recovery. It is a denial-of-service surface, not a
  trust surface.
- **The forge ACL keeps exactly one security job**: repository read
  access defines the security domain, as the design note already
  states.

Deployments that want forge-enforced append-only can carry the
sequence under `refs/heads/seq/<genesis>` and protect it with a
branch ruleset plus a bypass list for the sequencer principal. That is
an optional hardening with real costs (tooling divergence, a second
ref form in the wild) and this note does not adopt it; it records that
the option exists and that declining it costs availability only.

## The tier ladder

| tier | sequencer key lives | transport auth borrowed | liveness |
|---|---|---|---|
| 0 — resident (today) | operator's machine, `.git/gitseq` | the OS account | full, loopback only |
| 1 — forge-carried | forge secret store (Actions) | forge write access (org SSO, 2FA) | none; degraded contract |
| 2 — container | deployment secret | capability chain fronted by the org IdP | full, per domain |
| 3 — managed appliance | KMS | capability chain fronted by the customer IdP | full, multi-tenant |

Same kernel at every rung. The sequencer core stays a library with two
thin intakes — a drain loop (tier 1) and an authenticated HTTP surface
(tiers 0, 2, 3) — and divergence between those intakes is the failure
mode that would break the ladder. That is a standing requirement, not
an aspiration (R10 below).

Tier 1 and tier 2 are not stations on one line; they are two answers
for two audiences. Tier 1 exists because "add a workflow to the repo
you already have" is an adoption story with zero new infrastructure.
Tier 2–3 is the main road, and its requirements are the bulk of this
note. The general remote-submit contract is the tier-2 surface; the
tier-1 intake below is scoped to the Action and is not proposed as a
general transport.

## Tier 1 — the sequencer as a GitHub Action

One workflow file and one secret turn a GitHub repository into a
multi-user workroom with no server anywhere.

**Control-plane isolation comes first.** The obvious arrangement —
one repository holding both the inbox branches and the workflow that
drains them — hands the sequencer key to every submitter, and the
tier collapses. Two mechanisms do it. A `push`-triggered workflow runs
the workflow file *as it exists on the pushed ref*, so an inbox branch
carrying its own `.github/workflows/` runs attacker-authored steps
with the repository's secrets in scope. And repository secrets are
available to workflows on any branch of the same repository, so a
submitter with write access can also push an ordinary branch that
prints the sequencer key. Either path is a full key compromise by
someone the design intends to trust only for transport.

So the control plane is isolated from the submission plane, and the
isolation is structural rather than procedural:

- The workflow and the sequencer secret live in a **separate
  repository** to which no submitter has write access. It reaches the
  inbox repository as a reader.
- Submitters have write access to the **submission repository only**,
  and a ruleset there restricts them to creating refs under
  `refs/heads/inbox/**` — no workflow files, no other refs.
- The drain runs on a **schedule**, never on a `push` trigger in the
  repository submitters can write to. An earlier draft also allowed
  `repository_dispatch`, which does not survive the isolation it was
  supposed to sit behind: creating that event requires Contents-write
  on the repository receiving it, so any submitter able to trigger the
  drain would hold write access to the control repository — the exact
  access this whole section exists to deny. Schedule-only costs
  latency, not safety. A deployment that wants submitter-driven
  triggering must name a trusted intermediary — a GitHub App or webhook
  relay holding the control-repo credential on the submitter's behalf —
  and that intermediary's authority and executable-code boundary is
  then part of what spike case 7 has to test, not an assumption
  underneath it.
- If a deployment insists on one repository, the secret must sit
  behind an **environment with required reviewers**, the workflow
  must be pinned to the default branch, and the default branch must be
  ruleset-protected. This is weaker — it depends on configuration
  staying correct — and the note records it as the fallback, not the
  recommendation.

Whether each of these holds is a claim about GitHub's behaviour, not
about our code, so spike case 7 below exists to turn it into evidence
before anyone relies on it.

**Intake.** A submitter writes the submission as git objects and
pushes a single branch into the submission repository:

```
refs/heads/inbox/<genesis-hex>/<namespace>/<actor-fingerprint>/<key-digest>
```

Every component of that name is load-bearing, and the earlier draft's
`inbox/<actor-fingerprint>/<idempotency-key>` was wrong in two ways.
It omitted the target, so one repository serving two logs — or two
namespaces within one — silently collided two unrelated submissions
onto one ref, and the drain could not tell which log a candidate was
for. And it interpolated the idempotency key raw, which is arbitrary
submitter text and frequently not a legal ref component:
`git check-ref-format` rejects space, `~ ^ : ? * [ \`, ASCII control
characters, `..`, `@{`, a leading or trailing `/`, a trailing `.` and
a trailing `.lock`. A submitter whose key contains any of them cannot
push at all, and one whose key contains `/` silently reshapes the
namespace.

Every rejection above is measured against `git check-ref-format` on
git 2.50.1, not recalled: each of those keys is refused raw.

An earlier draft encoded the key in base32hex, on the grounds that the
alphabet is ref-safe and the encoding reversible. Legality was the
wrong bar, and testing it with `check-ref-format` alone was a test that
could not fail: a name can be well-formed and still unwritable. A
300-character component passes `check-ref-format` and then fails
`update-ref` with `Unable to create '…​.lock': File name too long` —
measured, not recalled. Encoding made this worse rather than better,
since a 300-byte key expands to 480 base32hex characters. A submitter
with a long but perfectly ordinary key could push nothing at all.

`<key-digest>` is therefore a **fixed-width digest**: the first 32 hex
characters of SHA-256 over the tuple (genesis, namespace, actor
fingerprint, idempotency key). Fixed width means no key length can
make a ref unwritable, and 128 bits is far past collision concern for
a per-actor queue.

Calling that encoding "unambiguous" and stopping there was a gap of
exactly the kind this note has already been caught on once — naming a
property instead of specifying it, as "compare-and-swap push" did for
the claim step. A digest binds an identity only if every party
computes it over the same bytes, and the obvious constructions do not.
Plain concatenation is ambiguous: `("ab", "c")` and `("a", "bc")`
collide. A delimiter scheme fails as soon as a key contains the
delimiter, and the whole point of this field is that keys are
arbitrary submitter text. Unicode normalisation and encoding choices
diverge silently between a submitter and a drain written against
different libraries.

So the construction is **byte-exact and stated**:

```
digest_input = LP(genesis) ‖ LP(namespace) ‖ LP(fingerprint) ‖ LP(key)
LP(x)        = uint64be(len(bytes(x))) ‖ bytes(x)
```

Length-prefixing makes the encoding injective — no shift of bytes
between adjacent fields can produce the same input — so the four-part
identity is what the digest actually commits to. It is also stronger
than the kernel's own `DedupKey`, which joins its components with NUL
separators and is unambiguous only because NUL is a forbidden byte;
length-prefixing needs no such constraint to hold.

`bytes(x)` needs saying precisely, because the previous draft named the
wrong bytes. It claimed the key was hashed as "the exact bytes the
submitter sent, with no normalisation", and the drain never sees those:
the signed intent carries `IdempotencyKey` as a canonical-CBOR text
string, so what reaches both the sequencer and the drain is the
**decoded** string, not the transport octets or the submitter's
original JSON. So `bytes(x)` is the **UTF-8 encoding of the canonical
decoded intent text**. That is still a byte-exact rule and still
normalisation-free — nobody case-folds or NFC-normalises anything — but
it is anchored where the value actually exists, which is the only place
a submitter and a drain can be made to agree.

Admissibility comes with it, and is not ours to relax: the intent
validator already **requires the key to be non-empty** and **rejects
CR, LF and NUL**. An empty key is therefore not a valid candidate
intent at all, and the spike case below has to say which of the two
things it is testing — the standalone encoder, or a candidate the
kernel would admit.

Case 9 has to test this rather than assume it: adversarial
boundary-shift tuples whose fields differ only in where one field ends
and the next begins must yield different digests, and an independently
written producer and drain must agree on every case. A digest
computed twice by the same code proves nothing. `<genesis-hex>` and
`<actor-fingerprint>` are already hex, and `<namespace>` is restricted
to `[a-z0-9-]{1,32}` at publication time, so every component is
bounded and the whole ref is under 200 characters.

The digest is not reversible, and does not need to be: the candidate
commit carries the canonical intent, key included. That turns a
weakness into the check the reversible form lacked — **the drain
recomputes the digest from the candidate's own intent and refuses any
candidate whose ref name does not match**, so a submitter cannot park a
candidate under a digest belonging to someone else's identity. That
refusal has a terminal record like every other, the `wrong-ref` line
below; a refusal with no record is the failure this note's ledger
section exists to forbid. The record is not the same thing as a proof,
and the ledger section says exactly how far it goes: this is the one
refusal whose grounds rest on refs the drain then deletes, so a reader
can check what the ref should have been but neither what the listing
held nor which candidate any listed ref pointed at.
The four components together are the deduplication identity, and it is
exactly the identity the log dedups on; a divergence between the two is
what lets a replay through, and the recomputation is what keeps them
from diverging.

The branch points at one commit: tree = the payload tree (event blob
plus attachments), message = the canonical intent and the actor
signature, **parents = none** — an *unsequenced candidate*, structurally
the envelope the sequencer would admit. Parentlessness is required, not
incidental, and the ledger's parent-role rules below depend on it: a
candidate carrying a parent is refused at intake with reason `shape`,
and is the one refused candidate the ledger records without parenting,
for the reason given with the bijection. Inbox branches live under `refs/heads/` because
rulesets are guaranteed to apply to branches and stock forge UI makes
the queue visible. Transport auth is repo write access — the org's
existing membership, SSO, and 2FA, borrowed whole. Record authority is
unchanged: the intent signature is checked at admission exactly as at
every other tier; a stolen forge credential without an actor key can
spam the inbox but cannot speak as anyone.

**The Action is a drain loop, not a per-event handler.** A concurrency
group serializes runs, and GitHub coalesces queued triggers (at most
one pending run per group; newer pending runs replace older). So a
correct sequencer run must list *all* inbox branches and drain them in
one pass — deterministic order: lexicographic by branch name within a
run — validating shape and signature, chaining each admitted event
onto `refs/seq/<genesis>`, pushing the advanced head, deleting each
drained branch. Coalescing then costs nothing: whichever run survives
drains everything. Admission remains shape-only; the Action runs no
fold.

**Key custody.** The sequencer key is an Actions secret in the
control-plane repository. Stated trust trade: the forge now holds
storage *and* ordering authority, so a forge compromise is a key
compromise. Equivocation remains witness-detectable and history
remains actor-signed, so the blast radius is ordering authority, not
authorship. Organizations already trusting the forge with deploy keys
are making this trade today; those that will not are tier-2 customers.

**Every submission ends in a durable terminal record.** The earlier
draft said "silent disappearance is a bug, never an outcome" and then
specified a protocol that could produce one. Two ways. A refusal
marker deleted after a TTL leaves a submitter who polled late unable
to distinguish *refused* from *never arrived* — the record of the
outcome expires while the question does not. And a drain that deletes
the inbox branch before the advanced `refs/seq` push has landed loses
accepted work with no trace anywhere: the candidate is gone, the
sequence never moved, and nothing says so.

The corrected protocol makes the sequence push the commit point and
never destroys evidence ahead of it. Three earlier drafts of this
section were wrong in the same way — each named a property and left the
object graph implicit — so this one states what objects exist.

**Prebuild, then claim.** Before touching any ref, the run builds the
sequence commits it intends to publish, chaining admitted events onto
the current published head `P`. That yields an exact expected head `E`.
The chain may be empty — a batch in which every submission is refused
admits nothing, so `E` = `P`. `E` is always defined for a drain that
acquires a claim, which is what keeps the reconciliation rows below
performable in that case.

Prebuilding is not an optimisation; without it `E` is not knowable. An
earlier draft claimed a later run could recompute the published head
from "the parent plus the candidates", and it cannot: `SignedCommit`
sets `GIT_AUTHOR_DATE` and `GIT_COMMITTER_DATE` from `time.Now()`, so
replaying identical inputs a second later yields a different commit id.
Reconstruction from insufficient inputs was a promise the
implementation cannot keep. `E` has to be recorded, which means it has
to exist first.

**One claim per genesis, and it is a singleton.** The claim ref is
`refs/heads/claimed/<genesis-hex>` — no batch digest in the name. A
digest-suffixed ref is not a lock: two runs that each observe no claim
can atomically create two *different* refs when their inbox listings
differ, which is exactly what happens when a submission arrives between
the two listings. "Refuse while any claim is live" is check-then-act,
not compare-and-swap, and the note claimed structural exclusion.
Contending on one fixed name with an **absent-value lease** is what
actually makes it structural: both runs push the same ref, one lease
holds, the loser skips.

**The claim's parents are what retain the objects, not its tree.** The
claim commit is created with the prebuilt head `E` and every candidate
commit as **parents**, and carries `P`, the observed mirror head `M`,
`E` and the candidate identities in its message.

An earlier draft said the claim's *tree* kept them reachable. That is
false, and measured so: a tree entry naming a commit is a gitlink, and
`rev-list` does not traverse into it — with a candidate embedded as a
`160000` entry and only the claim referenced, the candidate is **not
reached**. A tree can carry an id as bytes; it cannot retain the object.
Since the same atomic push deletes the inbox refs, that draft would
have stranded precisely the objects recovery needs. With parent
topology the check passes: all of `E` and both candidates are reached
by `rev-list`, and all three survive `git gc --prune=now` with only the
claim ref present.

**The batch ledger is a branch, and it is what actually retains the
objects.** An earlier draft put the retained record in
`refs/notes/gitseq-batches`. Measured, that fails exactly where it was
needed: after deleting the claim and running `git gc --prune=now`, the
notes ref survives and the recorded `E` is **pruned**. A note stores an
identifier; identifiers are not reachability, so "recover from the
candidates the note records" was impossible in the one state the record
existed for. It also had no protection story — GitHub rulesets target
branches and tags, not arbitrary ref namespaces, so nothing documented
could stop a submitter deleting it.

Both problems have one fix. The record is an **append-only ledger
branch**, `refs/heads/gitseq/batches/<genesis-hex>`, and each batch
appends a commit whose parents are the previous ledger head, the profile
bundle it ran, the prebuilt `E` when the batch admitted something, and
**every retainable candidate the drain considered — admitted and refused
alike**. The refused ones are not an afterthought: a refusal is
a verdict about a candidate that continues to exist, and it is only
retained here, since the drain deletes its inbox ref. "Retainable"
carries one exclusion, stated with the bijection below: a candidate
refused for carrying a parent cannot be parented without breaking the
ancestry bound the ledger walk rests on. Measured: with the
claim deleted and `gc --prune=now` run, `E` and the candidates survive,
held by the ledger alone. Being a branch, it is covered by the same
ruleset mechanism that protects everything else, and being append-only
it is never deleted — not on release, not on recovery. Each entry is
signed with the sequencer key **in force at that entry's own `base`**,
which is what lets an auditor tell a ledger entry from a commit shaped
like one; which key that is, and what happens across a rotation, is the
signing-epoch rule below. What that protection does and does not reach
is stated below, with the ledger walk.

**The root is a computed object, not a marked batch.** A previous draft
said two incompatible things — that the first batch entry omits its
predecessor slot and carries `root=true`, and that the root is a
deterministic object whose id is known before any batch runs. Those
cannot be the same commit, and the descriptor has no field to settle
it. One construction, and it is the second: the ledger root is an
**empty batchless commit computed from the genesis hash alone**.

It is specified as **bytes**, not as a recipe, because a recipe is what
two implementations disagree about. The commit object body is exactly:

```
tree <empty-tree-oid>
author gitseq <gitseq@invalid> 0 +0000
committer gitseq <gitseq@invalid> 0 +0000

gitseq-ledger-root <genesis-hex>
```

Every line ends with one LF; there is no `parent` line and no `gpgsig`
header; one blank line separates the header from the message, and the
message ends with exactly one LF and nothing after it. The OID is the
object hash over `commit <len>\0` followed by those bytes.
`<empty-tree-oid>` is `4b825dc642cb6eb9a060e54bf8d69288fbee4904` in a
SHA-1 workroom and
`6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321` in a
SHA-256 one, and both are creatable in a repository that has never held
an empty tree (`git hash-object -t tree -w --stdin </dev/null`).
Equivalently, for anyone who would rather run Git than serialize:
`git commit-tree <empty-tree> -m "gitseq-ledger-root <genesis-hex>"`
with that identity and both dates `1970-01-01T00:00:00Z` — `-m` supplies
the single trailing newline.

Fixed identity and fixed dates are what make it computable rather than
merely reproducible-in-principle: `SignedCommit` bakes `time.Now()` into
its dates, which is why `E` has to be recorded, and this object avoids
that by having no clock in it at all. Measured, and measured the way
that can fail: the byte string above was hashed by an implementation
that is not Git and the result equals what `git commit-tree` produces in
a freshly initialised repository — for this workroom's genesis,
`28ed9b9f6570a694e79b3a3d39265010dacdfd06`. Two independently
initialised repositories, given only the genesis hash, produce that same
OID, and recomputing in either yields it again. The round-15 reviewer
reproduced the same SHA-1 value independently.

**Two SHA-256 figures did not agree, and the resolution is now measured
rather than guessed.** Neither figure is this workroom's root: this
workroom is SHA-1 and its root is `28ed9b9f…` above. Both are probe
artefacts, and naming what each one hashed *is* the resolution. The
reviewer measured
`366791a7b12de05b466f3d8f03a258e7b065228ce625afa5d5c77979eec6363d`
against
`3eef49be30980102b9164c4fc75b4c09ebecd6259b2a721cfd837a3b34aed495`, and
an earlier draft left a plausible story about the difference standing in
a note that is supposed to be measured. The reviewer's retained probe
repository settled it. Their object carries these exact headers and a
message whose `<genesis-hex>` is sixty-four ASCII `1` bytes — a
placeholder standing in for a SHA-256 name. The other value pairs the
SHA-256 empty tree with this workroom's real 40-hex genesis, which is a
hybrid describing no actual workroom, since a genuine SHA-256 workroom's
`<genesis-hex>` is 64 hex characters. Both reproduce exactly and both
reproduce twice over — from an implementation that is not Git, and from
`git commit-tree` in a fresh `--object-format=sha256` repository on git
2.50.1. Four figures, four agreements. The inputs differed. Git did not,
and neither did the construction.

The lesson survives the resolution intact, which is why it stays. Two
careful parties working from the same prose hashed different bytes, and
neither could tell from the numbers alone which input had diverged — it
took a retained artefact rather than a better argument. So this note
quotes the derivation, and quotes worked values only with the genesis
and object format that produced them, rather than a table of bare
constants: **a root OID quoted without its genesis and its object format
means nothing.**

That construction needs no descriptor field, so **existing genesis-v0
workrooms get it for free** — the root is derivable from a hash they
already have, and adopting it is arithmetic rather than migration. The
first batch entry is an ordinary entry whose predecessor is that root.
Every *batch* entry has exactly one ledger predecessor, so there is no
special-case arity, and the root has no predecessor and no payload
because it records no batch — the one place "entry" in this section
means the root as well as the entries above it.

**The first-parent walk proves shape, not history, and the previous
draft claimed otherwise.** That draft had the auditor reject a forged
root because the walk "never terminates at the computed root". It does
terminate. Measured: with `L` the current ledger head and `R` the
computed root, an entry `F` built with `R` as its **first** parent and
`L` as a side parent is a legitimate fast-forward — `L` is an ancestor
of `F`, so a stock receiver accepts it as an ordinary append — and
`rev-list --first-parent F` is exactly `F, R`. It terminates at the
known root while omitting every batch from `L` backwards. The claim was
false in this note's recurring way: an assertion about a mechanism,
written without running it.

What excludes that entry is a **bounded check on the side parents**, not
a property of the walk. `F` needs `L` somewhere in its ancestry or the
push is not a fast-forward — measured: the same entry without the side
parent is rejected `non-fast-forward` by a stock receiver. So the whole
question is whether any side-parent slot can hold, or lead to, a ledger
entry. Under the roles below none can: the profile bundle and every
candidate are **parentless**, and `E` lies on the sequence, which never
reaches a ledger entry. Reachability of `L` from `F` then implies `L` is
on `F`'s **first-parent** chain — which is the property the walk needs
and the forge's own test does not give, since Git's fast-forward check
is reachability through any parent, first or not.

That bound is load-bearing and easy to lose. Measured: hiding `L` behind
a candidate-shaped commit `X`, whose parent is `L` and which sits in a
candidate slot of `F`, restores both the fast-forward and the clean
two-step walk. The parentless-candidate rule is the only thing rejecting
`X`, and relaxing it reopens the hole entirely. A tree cannot smuggle
`L` in instead — measured, a parentless candidate whose tree names `L`
as a gitlink leaves `L` outside the ancestry, the push is rejected
`non-fast-forward`, and `rev-list` does not reach `L`, which is the same
gitlink finding that killed the claim-tree draft, working in our favour
this time.

**The unbounded reachability scan stays retired, but not for the reason
the previous draft gave.** It is retired because the per-entry role
checks are bounded and are run anyway, not because a second root
"cannot be named" — that argument was the same "cannot happen" shape
this note has been caught on before. Checking `E`'s position costs the
sequence walk an auditor already performs; checking the other side
parents is O(parents) with no traversal at all.

**There is a residue, and the note states it rather than closing it.**
None of this survives a *force* update of the ledger branch. A principal
able to rewind `refs/heads/gitseq/batches/<genesis-hex>` can replace it
with a short branch whose object graph is internally consistent, leaving
nothing in the graph to detect. That is the same class as the sequence
vandalism the availability posture already accepts, and it arrives by
the same door as the key: the drain principal writes this branch, so
"a forge compromise is a key compromise" covers it, and spike case 7
proves only that a *submitter* cannot do it. The check that does not
depend on the forge is **first-parent containment**: an auditor who
fetched before holds a tip `L` and requires `L` to appear on the new
tip's first-parent chain. Measured, that check separates both forgeries
above from a genuine successor of `L`, where a plain ancestry test
accepts all three. Ruleset protection is what keeps the ledger
append-only in the repository, and that is forge configuration rather
than a property of the object graph.

Away from the forge it is the reader's job, and the reader has to be
told to do it, because two defaults are against them. **A stock clone
tracks branches with a forcing refspec** — `+refs/heads/*` — so an
ordinary fetch adopts a rewound ledger without a word: measured, the
auditor's `origin/ledger` moved silently back onto a short forged
branch. Configuring the refspec without the `+` rejects that same
rewind, which is the posture section's integrity claim carried over to a
second ref. **But a non-forcing fetch is not enough here**, and this is
the difference between the ledger and the sequence rather than an
oversight in either. Sequence commits have one parent, so "not a rewind"
and "not a truncation" are the same condition. Ledger entries are
merges, so a truncating entry is a fast-forward — measured, the
non-forcing fetch that rejected the rewind accepted the truncation
without comment. First-parent containment is exactly the part no fetch
setting gives, and an auditor tracking this branch must run it as its
own step.

**Parent roles are positional for speed and discriminated by content for
correctness.** Entries fix their parents in order — **ledger
predecessor, then the profile bundle, then `E` when there is one, then
the candidates** — so a first-parent walk is the ledger walk. `E` is
absent for an all-refused batch, so position alone cannot identify the
later slots and content has to.

"Validated by type and content" was the previous draft's entire rule,
and it discriminates nothing: every one of these is a commit, and a
commit message is attacker-authored text. Measured — a commit whose
message is `batch base=… head=… profile=…`, authored by anyone with
`commit-tree`, parses as an entry. So each position gets a rule an
auditor can run, and each rule ends at a **trust anchor** rather than at
a shape:

- **Ledger predecessor** — either the computed root, recognised by OID
  equality against the constant derived above, or a commit that verifies
  under **the sequencer key in force at that commit's own `base`**,
  whose tree is the empty tree and whose message parses as the entry
  grammar below. That key is not the same as the one anchored in the
  genesis descriptor once the sequencer has rotated, and the
  signing-epoch rule below is what says which it is. The root is the
  only ledger object recognised without a signature, and the only one
  that needs to be, because its bytes are fixed — it is not a batch
  entry and has none of a batch entry's parents. Measured: real sequenced commits
  in this workroom carry a `gpgsig` header and an attacker's
  `commit-tree` output does not, so the discriminator is visible in the
  object before any key is consulted — and the key is what settles it.
- **Profile bundle** — a **parentless** commit whose id equals the
  admission profile in force at `base`, resolved from the sequence by
  the selection rule specified below and never read out of the entry.
  The anchoring is the idea R1 uses for the capability issuer key — a
  value fixed by a log's own governance rather than by whoever is using
  it — except that the log doing the anchoring here is the workroom's
  own sequence, so no second config log is involved. The auditor
  resolves the id for itself and requires the header's `profile=` to
  match, so a drain naming another bundle, however well-shaped, is
  detected. It is the anchor the previous draft lacked: that draft
  trusted the entry to name its own rules and then called re-running
  them a check. Every entry that records a batch parents its profile,
  the bootstrap one included, so this slot never varies in arity — the
  root is the exception that proves it, being parentless because it
  records no batch and therefore ran no rules.
- **`E`** — a commit on the sequence's first-parent chain, equal to the
  entry's `head` field, present only when the batch admitted something.
  Its signature needs no separate rule here: the sequence walk the
  auditor already runs verifies each commit on that chain under the key
  in force for what follows its parent, so being on the chain is the
  check.
- **Candidates** — **parentless** commits, not sequencer-signed, one per
  ordinary payload line. Parentlessness is what keeps the side parents
  from reaching the ledger, and it costs a submitter nothing: a
  candidate is a standalone envelope, and chaining an admitted event
  onto `P` is the sequencer's job. Note what this rule deliberately does
  *not* require — a candidate's own actor signature need not verify,
  because `unsigned` is a refusal reason. Signature validity is the
  verdict; being a candidate is the position.

A drain that reorders parents to disguise a role is detected by these
rules, where a positional convention alone would take the label at its
word.

**Which key signs a ledger entry, and it is not simply the genesis
key.** The predecessor rule ends at the sequencer key, and an earlier
draft named that key as the one "anchored in the genesis descriptor".
That sentence is correct only until the sequencer key rotates, and the
governing design makes rotation the kernel's sole reserved event type,
with a full audit that tracks the key through rotations. Two obvious
readings both fail: a checker consulting only the genesis key rejects
every entry a rotated sequencer wrote, and a checker accepting any key
reachable from genesis through the rotation chain accepts an entry
written with a key retired long before. Neither is authentication. So
the epoch is defined, and defined so that an auditor derives it from the
chain rather than being told it.

**`keyat(H)`** is the sequencer key in force for commits appended
*after* sequence commit `H`: the genesis descriptor's sequencer public
key, updated by walking the first-parent chain from genesis to `H`
**inclusive** and replacing the current key at each rotation event with
the successor that event names. That is the rotation walk the design
already requires of a full audit; the ledger adds no second walk and no
second anchor.

"After `H`" is the whole content of the definition and the place a
plausible variant goes wrong. A sequence commit `C` verifies under
`keyat(parent(C))`, not `keyat(C)`, which is why the rotation event
itself verifies under the key it retires: a rotation is signed by the
outgoing key, and its own update applies only to what follows it. The
variant that applies the update at the rotation rather than after it
rejects the rotation commit under its own successor's key, which is the
one commit in the chain that cannot verify that way.

**A ledger entry must verify under `keyat(base)`** — the key in force at
the entry's own `base`, which is the head the drain read before it built
anything. Not `keyat(head)`, and not the genesis key. `base` is already
checked against the predecessor's `head` rather than trusted, so the
epoch is pinned by the same step that pins the chain, and an auditor who
has walked the sequence once holds `keyat` for every position for free.

**The rotation-batch boundary is one entry wide, and it is stated rather
than left to arithmetic.** A batch whose admitted events include a
rotation would otherwise straddle two epochs: the commits before the
rotation belong to the old key, the commits after it to the new one, and
the single entry recording all of them belongs to neither by
construction. The rule that removes the ambiguity is a restriction on
the drain. **A rotation is the last admitted event of its batch**: a
drain that admits one admits nothing further in that pass and leaves the
rest of the inbox to the next. Then every sequence commit in that batch,
up to and including the rotation, is signed by `keyat(base)` — the
outgoing key, which is also what signs the rotation event itself, since
a rotation is signed by the key it retires — and so is the ledger entry.
The next entry's `base` is that batch's `head`, which is the rotation
commit or later, so its `keyat(base)` is the incoming key. The boundary
falls between two entries and never inside one. It costs one batch of
latency per rotation and buys a rule with no case analysis in it.

**The kernel has the rotation walk now, and this note has to say so
rather than repeat what it measured before.** An earlier revision
measured a kernel that verified every sequence commit against the
genesis descriptor's key and nothing else, and a spike README that
listed key rotation among the things outside it. Both have moved.
Measured on the current tree: the kernel has its reserved rotation event
(`gitseq-rotation-v0`), `Rotate` signs the rotation commit with the
**outgoing** key and refuses a successor equal to the current key, and
the scans verify each commit under the key currently in force and adopt
the successor **only after** the rotation commit they have just
verified. That is `keyat` as defined above, in code, agreeing on the one
point the two plausible readings disagree about. There is no operator
command for it: rotation is a kernel entry point, so custody hand-over
is still a procedure somebody writes rather than one a deployment runs.

What remains unexercised is narrower, and worth naming exactly.
Measured: no commit on this workroom's first-parent chain carries the
rotation marker, and `gs verify` audits the sequence whole under the
genesis key — depth 1455 at that reading, where the root-sentinel
measurement further down reads 1450, because the sequence grows while
the note is being written and a depth is a reading rather than a
constant. So `keyat(H)` here is still the genesis key for every `H`, and
nothing in this workroom exercises the epoch rule. Nothing enforces it
on a ledger entry either, because there is no ledger and no drain: a
sequence commit under a stale key is refused by the kernel's own scan,
and a ledger entry under a stale key is still refused by nothing that
exists.

**One atomic push, four kinds of update:** create the claim, delete
every inbox ref being drained — one deletion per ref, so an N-candidate
batch pushes more than four updates and never fewer kinds — advance the
ledger branch, and carry an explicit **no-op refspec**
`P:refs/seq/<genesis>` alongside the lease on it.

That last one is not decoration. A `--force-with-lease` on a ref the
push does not otherwise update **does not assert anything** — measured:
with the remote sequence ref advanced past the observed `P`, an atomic
claim-plus-delete carrying the lease but no refspec for that ref
*succeeded*, created the claim and deleted the inbox. Adding the no-op
`P:refs/seq/<genesis>` refspec made the same stale state reject every
update in the push. A lease binds a ref only if the push is going to
update it, so the refspec is what turns the stale-`P` premise into a
refusal.

Per-candidate claims were the previous draft's mistake and they do not
survive contact with the reconciliation model. Renaming each inbox ref
to a `claimed` ref leaves it pointing at the *submitter-authored*
candidate commit, which cannot record a drain run's parent or batch —
the later text simply assumed a claim commit that step 1 never created.
Worse, an N-candidate drain would leave N claims against one published
batch head, so the early claims would observe a head later than their
own and be classified as anomalies by the very table meant to protect
them. One batch, one claim, one head.

Per-ref leases are what make two concurrent runs safe — exactly one
claim succeeds and the loser skips the batch rather than
double-draining it — but leases alone are not enough, because
`--force-with-lease` is evaluated per ref while the push as a whole is
not all-or-nothing. Measured against a local bare `receive-pack`, with
`claimed` already held by another run: the **non-atomic** push *deleted
`inbox` and rejected `claimed` as stale*, leaving the candidate
reachable from no ref at all and nothing recording its loss — the
precise failure the durable-terminal-record rule exists to forbid,
reintroduced by the step meant to enforce it. The **same command with
`--atomic`** rejected both updates and left `inbox` intact. Atomicity
here is the property, not an optimisation, and since it requires server
support the drain must **verify the capability and refuse** rather than
fall back — a silent fallback to per-ref semantics is this same data
loss wearing a success exit code.

Claims live under a ruleset that permits only the drain principal to
create or delete them. A submitter who could delete a claim could erase
the only record that a batch was in flight.

**Then publish, mirror, and only then release.** Push `refs/seq` to
`E`; fast-forward the control mirror to `E`; verify both refs read `E`
exactly; delete the claim last. An earlier draft had this step "record
any refusals in the ledger entry", which cannot be done: that entry was
written and pushed in the claim transaction above, and a pushed commit
takes no further writes. The refusals are already in it, which is the
point of writing the ledger before publishing rather than after — the
`P`/`P` row recovers from an entry that exists precisely because it was
never deferred to a step a crash can skip.

**The state machine.** A run starts only from a quiescent workroom —
no outstanding claim, no unaccounted ledger entry, and published = mirror
= `P`. That is a **precondition, not an invariant**, and the difference
matters: it is observed by reading two refs in two repositories with no
atomicity between them, so it can be stale the moment it is read. The
claim acquisition therefore binds what it can — the atomic push carries
an explicit lease on `refs/seq` expecting `P`, so a submission-side move
between the read and the claim loses the lease rather than proceeding on
a stale premise. The mirror lives in the other repository and **cannot**
be included in that atomicity; a mirror that moved in the same window
is caught after the claim by re-reading both refs, and classified as an
anomaly. Saying "invariant" without stating those checks was the
overclaim.

Reconciliation then uses the claim's recorded `P`, `M` and `E`:

The table has three inputs, not two: the two refs **and the ledger**.
A previous draft left the ledger out and put its consequences in prose
below, which is how "`E`/`E`/none = proceed" ended up false — with an
unaccounted ledger entry it is not complete, and `E` is not even
defined when no claim is live. "Accounted" below means the ledger's
newest entry has its events in the sequence.

| published | mirror | claim | ledger | meaning | action |
|---|---|---|---|---|---|
| `P` | `P` | live | — | crashed before publishing | **publish the recorded `E`** — do not rebuild it |
| `E` | `P` | live | — | crashed between publish and mirror | fast-forward mirror to `E`, verify, release |
| `E` | `E` | live | — | crashed after mirror, before release | verify both, release |
| equal | equal | none | accounted | quiescent | proceed |
| equal | equal | none | **unaccounted** | claim lost before publishing | recover from the ledger's retained candidates |
| **divergent** | | none | any | rewind or partial loss with nothing to explain it | **refuse**, ask for manual reconciliation |
| anything else | | any | any | **anomaly** | refuse, raise, move nothing |

The fifth row is the state that a deleted claim produces and the one
that used to read as quiescence: the inbox refs are already gone and
the two refs agree, so nothing about them is wrong — only the ledger
knows a batch went missing, and only the ledger still holds the objects
to recover it from. The sixth is divergence with no claim, which the
drain must never resolve by inference about which ref is right.

The first row said "re-drain; idempotency makes it exact", which
contradicts this note's own finding two sections up: re-signing later
changes the timestamps and yields a different commit id, so a re-drain
produces `E'`, not `E`. Recovery **republishes the recorded `E`**,
whose objects the ledger retains whether or not the claim survived.
Idempotency stops a duplicate *event*; it does not make a rebuilt head
equal the original.

The third row is the one the previous draft omitted, and omitting it
meant an ordinary crash at the commonest interruption point matched no
case at all. The anomaly row is what catches a rewind performed inside
the window: a vandal resetting `refs/seq` to the stale mirror produces
published = mirror, which looks quiet, and is caught only because the
claim says `E` went out.

**With no claim present**, two checks run, and the second is the one a
previous draft was missing. If published and mirror diverge with no
claim to explain it, the drain **refuses and asks for manual
reconciliation** — it must not infer which ref is right.

But the dangerous claim-loss state is the quiet one. Destroy the claim
at `P`/`P`, before publication: the inbox refs are already gone, the two
refs agree, and a rule that only inspects them classifies the workroom
as complete. The batch would vanish with no log event, no recorded
refusal, no inbox and no claim — the exact silent loss this protocol
exists to forbid, reintroduced by the state machine meant to enforce
it. A
ruleset restricting deletion does not close it, because the drain
principal must be able to delete claims, so an implementation bug or a
privileged mistake stays in scope.

That is why the ledger branch is advanced in the same atomic push and
never deleted. The second check is: **for every ledger entry, either
its events are in the sequence or its claim is live.** An entry whose
batch is absent with no claim outstanding is a lost batch — refuse,
name it, and recover from the candidates, which the ledger still holds
as commit parents rather than merely naming. Claim loss is therefore
tested at *every* live row, `P`/`P` most of all, not only where the
refs diverge.

Multiple outstanding claims are excluded by the singleton claim ref and
its absent-value lease, not by a check-then-act rule: two runs
contending on one fixed name cannot both win, whatever their inbox
listings differ by.

**Refusals and recovery.** A refused submission's reason (`shape |
size | unsigned | unknown-log`) is recorded in the **same ledger
entry** as the drain that refused it, keyed by the candidate's object
id — which every line carries — and additionally by the four-part
identity on the lines that reached one.

The entry's payload is one line per submission in the batch, in its
commit message body, so no tree walk is needed to read it:

```
batch base=<P> head=<E> profile=<commit id of the admission profile bundle>
identity=<four-part identity> candidate=<commit> result=admitted  event=<event commit> ref=<len>:<bytes>
identity=<four-part identity> candidate=<commit> result=refused   reason=<reason> ref=<len>:<bytes>
identity=<four-part identity> candidate=<commit> result=wrong-ref ref=<len>:<bytes>
identity=none                 candidate=<commit> result=refused   reason=shape ref=<len>:<bytes>
candidate=<commit> result=refused reason=shape unretained=parented ref=<len>:<bytes>
candidate=<commit> result=noncanonical-ref ref=<len>:<bytes>
candidate=<commit> result=extra-ref ref=<len>:<bytes>
```

The first four forms are **ordinary lines**: each names a candidate the
entry parents, and each consumes exactly one candidate parent. The last
three **consume no candidate parent**, for the reasons given with the
bijection below, and none is a payload line for it — the parented form
because the entry holds no such candidate at all, the two extra-ref
forms because the candidate they name already has its own judged line,
ordinary wherever the candidate has one.

**Extra-ref form** unbackticked names the pair of them below; `extra-ref`
backticked is one of the two, and the note keeps the distinction because
the two are not interchangeable. They differ in exactly one thing, and
the difference is what the drain was in a position to know.
`noncanonical-ref` states that these bytes are not the canonical ref of
the candidate they pointed at, which is a claim; `extra-ref` states only
that another ref pointed there and was not the judged one, which is not.
The rule joining them is set out with the classification order below: a
claim is recorded only where the drain computed the canonical ref, or
established that none exists, from a candidate the ledger goes on to
retain.

`candidate=` is present on every form, which is what lets the
submitter's lookup below key on it. `identity=` appears on the ordinary
lines only, and there it is filled wherever the canonical intent decoded
into the four fields and is `none` wherever it did not. The three
parent-free forms carry no `identity=` at all, and that is a consequence
of what each one records rather than an omission: the parented verdict
is reached before anything is decoded, and the two extra-ref forms are
records about ref bytes rather than about a submission, so neither is
entitled to name an identity even where the drain holds one. In
particular a parented candidate gets the same line whether or not its
intent would have decoded, which is what makes that class total
— an earlier draft required an identity on it and so described no line
at all for a candidate that was both parented and undecodable, a
combination the same draft said exists.

**The grammar has to be total over what a submitter can push, and an
earlier draft's was not.** It keyed every refusal on the four-part
identity while requiring a malformed intent to yield one — and an intent
that does not decode has no identity to key on, so the line the case
demanded could not be formed. It also gave every candidate a canonical
ref, while the drain refuses a candidate whose ref does not match the
digest recomputed from its own intent; a candidate pushed *only* under a
wrong ref then had no ordinary line, nothing parented it, and the object
the refusal was about was not retained — a second unretained class in a
note that says there is one. Both gaps close with a stated **order of
classification**, run once per distinct candidate object after the
deduplication rule below. Classification ends at the first step that
refuses, so every candidate reaches exactly one verdict and the order is
what decides which:

1. **Parentlessness.** A candidate carrying a parent is refused `shape`
   with `unretained=parented`, on the parent-free form above. It is
   checked first because it is the one check that decides whether the
   entry may parent the object at all, and that answer must not depend
   on anything else being well-formed. Nothing is decoded before that
   verdict, so the line names the candidate by object id and carries no
   identity — which is what makes the class total, since a parented
   candidate whose intent never decodes needs no second form. Its
   judged ref is the least of its refs in byte order, there being no
   computed canonical ref to prefer, and **each remaining ref at it is
   an `extra-ref` record rather than a `noncanonical-ref` one**. That
   is the one place the two differ, and it is forced: classification
   stopped before decode, so the drain holds no canonical ref for this
   candidate, and a parented candidate whose intent *would* have
   decoded has one all the same — the batch may even have pushed it. If
   it did, and some wrong ref happened to sort lower, calling the
   submitter's true canonical ref noncanonical would be a false
   statement, made in the one class whose object the ledger does not
   retain and about which therefore nobody could check it. `extra-ref`
   records the bytes and claims nothing further.
2. **Decode.** The canonical intent is decoded into genesis, namespace,
   actor fingerprint and idempotency key. A candidate whose intent does
   not decode that far is refused `shape` and its line carries
   `identity=none`. It is parentless, so the entry parents it like any
   other refusal, and step 3 is skipped — there is nothing to recompute
   a digest from. Having no canonical ref, it takes the same judged ref
   as the class above: the least of its refs in byte order. Each
   remaining ref is a `noncanonical-ref` record, and here that outcome
   is truthful where it was not above: the drain reached this verdict
   *by* decoding, so it knows no canonical ref exists, and the entry
   parents the candidate, so an auditor holding only the ledger reaches
   the same conclusion from the retained object.
3. **Canonical ref.** The canonical ref is recomputed from the decoded
   intent. If the batch pushed that ref at this candidate, it produces
   the ordinary line and every other ref at the same candidate produces
   a `noncanonical-ref` record. If the batch did not, the candidate is
   refused `wrong-ref`, its ordinary line records the least of its refs
   in byte order, and the rest produce `noncanonical-ref` records as
   before. The candidate is retained in both cases, so both uses of the
   outcome are recomputable in the same way step 2's is.
4. **Admission** — `size`, `unsigned`, `unknown-log`, and any remaining
   `shape` verdict, judged against the profile in force at `base`.

**One rule covers all four, and stating it is better than remembering
which step is which.** Every ref at a candidate other than the judged
one becomes its own record; that record carries `noncanonical-ref`
wherever the entry parents the candidate, and `extra-ref` wherever it
does not. The two conditions coincide because they have the same cause:
step 1 is the only step that ends before decode, and it is the only step
whose object the entry does not parent. So the outcome an auditor sees
is exactly the outcome they can check — a `noncanonical-ref` record is
always about a retained object, from which the canonical ref is either
recomputable or provably absent, and `extra-ref` is what the drain
writes when it is in no position to say. An earlier draft used
`noncanonical-ref` for all four steps, which made the entry state
something false about a decodable parented candidate pushed under its
own canonical ref plus a lower-sorting wrong one: the rule below defines
that canonical ref as canonical, and the record called it noncanonical.

`wrong-ref` is the record this note already promised and never gave:
refusing a candidate whose ref name does not match its own digest is
what stops a submitter parking a candidate under someone else's
identity, and a refusal is a verdict about an object the ledger retains.
It is a `result` and not a `reason`, exactly as `noncanonical-ref` is,
because it is a fact about an inbox ref rather than about admission —
the four-reason taxonomy is shared with the tier-2 surface, which has no
refs to get wrong. So every *parentless* candidate the drain considered
has exactly one ordinary line and exactly one parent, and `parented`
remains the only class whose object the ledger does not hold and the
only class with no ordinary line.

The header line carries the values replay needs and the entry would
otherwise only *refer* to. `profile` is the **commit id of the
admission profile bundle**, and that commit is an additional parent of
the ledger entry, so the rules an auditor must re-run are retained by
the same edge that retains the candidates.

A previous draft made it the blob id and parented the blob. That is
impossible and I should have tried it before writing it: **Git commit
parents must be commits**, and `commit-tree -p <blob>` fails with `is
not a valid 'commit' object`. Measured both ways — the blob parent is
rejected outright, and a profile *commit* whose tree carries the blob
does retain it: with every other ref deleted, reflogs expired and
`gc --prune=now` run, the blob survives held by the ledger alone.

**A rules blob is not executable on its own**, which is the second-order
problem the blob edge would have hidden rather than solved. Rules need
whatever reads them. So an activated profile is a **bundle commit**: a
declarative ruleset plus the version of the interpreter contract it is
written against, in one parentless commit, with no reference outside its
own tree. The bootstrap profile specified further down is the reason
this sentence says *activated*: it carries no ruleset of its own,
because it denotes the admission the genesis descriptor already defines,
and it takes its contract version from that descriptor rather than from
a tree.

The previous draft then said an auditor holding that commit can execute
it. That is false, and calling the pair "self-contained" papered over
the same gap the blob edge had. A version string is not an interpreter;
it is a name for one, kept by somebody else. What the bundle retains is
every input the rules depend on. What it does not retain is the thing
that runs them. Two honest ways out, and this note takes the second:

- **Retain a content-addressed executable closure** — the interpreter
  and its dependencies as blobs in the bundle's own tree, so replay
  needs nothing but the commit. This is the strong form; it costs a
  build target and an architecture choice per bundle, and it is recorded
  here as available and not adopted.
- **Name the dependency and carry the obligation openly.** The bundle
  declares a contract version; the project keeps an interpreter for
  every published contract version available and reproducible, and
  specifies the contract closely enough that an independent
  implementation is possible. That is an **availability obligation on a
  party outside the record**, of exactly the kind this note opens by
  refusing to disguise: it holds while someone keeps their end, and no
  object graph enforces it.

So the replay claim narrows to what is true. **An auditor holding the
bundle and any interpreter implementing contract version V reproduces
the verdict; an auditor holding the bundle alone reproduces nothing and
must say which of the two it is.** The contract version is what an
interpreter promises to keep supporting, so a profile stays runnable
when the drain's code moves on — and a profile referring to files
outside its tree would have moved a second missing dependency behind an
object id rather than removing it.

Naming them here is not redundancy. An earlier draft said replay runs
against "the genesis descriptor and the recorded `P`" and left both to
be found elsewhere — but `P` was recorded only in the *claim*, which is
deleted on release and can then be pruned, and an all-refused first
batch has no admitted event to derive `P` from and omits `E` = `P` as a
parent. So the note named the inputs to a replay while specifying a
topology that discards them: the same failure as the batch note and the
refused candidates, a third time, and the reason the header exists.

**`base` is bound by the topology, not asserted.** Writing `P` into a
header the drain also authors proves nothing: for an admitted batch the
sequence constrains it, since `P` is the parent of the batch's first
event, but an all-refused batch has no event and omits `E` = `P`, so
any ancestor could be written there and replayed against — which is
precisely the case the field was added for. So the entry takes its
**predecessor's `head` as its `base`**, and an auditor checks that
rather than trusting the value: walk one step back along the ledger,
read that entry's `head`, and require equality. The ledger is
append-only and totally ordered, so this chains.

`head` is written explicitly for the same reason `base` is checked
rather than trusted. An earlier draft left `E` to be derived from the
topology, which works for an admitted batch — `E` is the last sequence
commit among the parents — and fails for an all-refused one, which has
no `E` parent at all, so there is nothing to derive it from. Recording
it makes the chain readable in one pass for both, and it is still not
merely trusted: for an admitted batch it must equal the sequence parent,
and for an all-refused batch it must equal `base`.

**The root carries no header line, so its `base` and `head` are derived
by an explicit parser rule rather than read.** The root's message is
`gitseq-ledger-root <genesis-hex>` and nothing else; the previous draft
told the auditor to read the predecessor's `head` and separately
asserted that the root's head is the genesis commit, without saying how
a parser gets from one to the other. The sentinel rule is: **when the
first-parent walk reaches the root OID it stops, and the root's virtual
`base` and `head` are both the object named by the `<genesis-hex>` in
its own message.** The root has no payload lines and no candidate
parents, so nothing else needs deriving, and no descriptor field anchors
the chain.

That object is the sequence's own first commit, and it is now measured
rather than asserted: in this workroom `5d2622748872b7e2dec3fe5c59e4be73a35e0bc8`
is a commit, has zero parents, and is the last entry of
`rev-list --first-parent refs/seq/5d2622748872b7e2dec3fe5c59e4be73a35e0bc8`
over a 1450-commit sequence. It also carries the genesis descriptor in
its own message, so the single object the root names is the same object
a replay needs for the `size` ceiling. The auditor checks the first real
batch's `base` against that object directly — resolve it, require zero
parents, require it to be the root of the sequence's first-parent chain
— rather than against a value the ledger asserts about itself.

`entries` is gone. It was a count the drain authors over evidence the
drain also authors, so it could only ever disagree with the
parent-to-line bijection that already detects truncation and padding —
adding a way to manufacture a fault without adding a way to detect one.

Every submission the drain considered appears exactly once. That is
what makes the submitter's lookup below a search over a branch rather
than an address lookup on a note.

For that to be checkable rather than merely asserted, the entry and its
parents stand in a **bijection**, and each half is independently
falsifiable by an auditor holding only the ledger branch and the
sequence — with three exceptions, each named where it arises: the
recomputability bullet, where the auditor also needs an interpreter the
record does not contain; the unretained-candidate class in the first
bullet, whose object the record deliberately does not hold; and the
`wrong-ref` bullet, half of whose verdict is a fact about the drain's
inbox listing — what it held, and which candidate each listed ref
resolved to — rather than about any retained object.

The three are not the same kind of exception, and treating them as one
is how the third came to be understated. The first two are checks an
auditor can still complete by obtaining something that continues to
exist — an interpreter for the recorded contract version, or the
candidate the submitter built and still holds. The third is a check
**no party can complete**, because what it would rest on was a set of
refs the drain deleted, and a deleted ref leaves no object behind for
anyone to produce. The bijection itself is unaffected either way:

- every candidate parent of the entry has exactly one payload line, and
  every payload line's `candidate=` field names exactly one candidate
  parent by its Git object ID. The `candidate` field is what makes this
  mechanical: the four-part identity is a digest over the submitter's
  intent, **not** a Git object ID, so an entry carrying only identities
  states the pairing without encoding it. A line whose `candidate` is
  not a parent, or a parent with no line, is a detected fault. Line
  order is not significant and carries no meaning; the pairing is by
  hash alone.

  **Two refs may name one commit, and that has to be resolved before
  the bijection is even stateable.** Anyone with inbox write access can
  point a second ref at a candidate. The drain then sees two
  submissions while Git stores one parent — `commit-tree` reports
  `duplicate parent ... ignored`. Neither reading of the old rule
  survived that: two payload lines broke "a repeated `candidate` is a
  fault", and one line broke "every submission appears exactly once".
  So candidates are **deduplicated by object id before admission**, the
  batch considers each distinct object once, and the entry carries one
  line judging that object — an ordinary line for every class but the
  parented one. What happens to the *extra* refs is the subject of the
  next paragraph, and it is not collapse.

  **There are no valid aliases, and the previous draft contradicted
  itself by having both.** It said the canonical intake ref is
  completely determined by genesis, namespace, candidate actor and the
  recomputed key digest — and then required a canonical-plus-alias form
  to collapse into one normal line. Both cannot hold: if the path is
  determined, every *second* ref to the same candidate is by
  construction noncanonical. There was no alias grammar to make one
  legitimate, so "valid alias" named nothing. That is a statement about
  the candidate and not about the drain's knowledge of it: at most one
  ref at a candidate can be the canonical one, and which one that is may
  be a question the drain never asked.

  So the rule is the strict one. Each candidate whose intent decodes has
  exactly one canonical ref, recomputed from the candidate itself. Where
  the batch pushed that ref, it produces the ordinary line; where it did
  not, the ordinary line is the `wrong-ref` refusal of the
  classification order above. A candidate whose intent does not decode
  has no canonical ref at all, and its judged ref is the least of its
  refs in byte order instead.

  **A parented candidate is where those two sentences must not be run
  together, and an earlier draft ran them.** It is refused before
  anything is decoded, so the *drain* holds no canonical ref for it — but
  the first sentence is about the object and not about the drain, so if
  its intent would have decoded then a canonical ref exists and one of
  the pushed refs may be it. Its judged ref is the least in byte order,
  that being the only rule a verdict reached before decode can apply.
  Every case still has exactly one judged line per distinct candidate,
  which is what keeps the pairing below stateable.

  **Every other ref pointing at that candidate is a separate terminal
  record**, carrying the outcome the classification order assigns:
  `noncanonical-ref` where the entry parents the candidate, so the claim
  rests on a retained object, and `extra-ref` for the parented class,
  where it would rest on nothing. The draft that wrote
  `noncanonical-ref` in both places labelled a true canonical ref false
  whenever a parented candidate was pushed under it alongside a
  lower-sorting wrong one, and did so in the one class where no auditor
  could contradict the label. Under either outcome the judged submission
  reaches its own outcome untouched — a stranger pointing at your
  candidate cannot alter your record, add lines to it, or make it look
  malformed.

  **And that record does not name who created the ref, because Git
  cannot say.** A ref stores a name and a target, nothing else. Probed
  independently: two actors updated differently-named refs to the same
  third party's candidate, and `for-each-ref` exposed only the names,
  the target, and the *victim's* object author — with no reflog on
  either, and a fresh clone carrying less still. Attribution would need
  authenticated transport evidence the object graph does not hold, so
  the entry records the claimed namespace and ref bytes as **untrusted
  input** and attributes them to nobody. An earlier draft said the
  outcome is recorded "against the actor who created it", which was
  unimplementable, and inventing an attribution from a ref name would
  be worse than omitting one.

  A comma-joined ref list would also not be injective. Comma and `=`
  are legal bytes in a Git ref name, so a submitter can craft a name
  containing the delimiters and forge an extra entry or hide one. Every
  ref is therefore written **length-prefixed** — a byte length then the
  bytes — so no byte inside a name can be read as structure. That
  matters more under this rule, not less: an extra-ref line of either
  outcome exists precisely to record bytes nobody vouches for, and a
  hostile name must survive round-tripping without becoming grammar.
  Delimiter-bearing names are a case to test, not a case to forbid,
  because forbidding them is a rule the drain enforces and the auditor
  cannot check.

  One line per ref, not a list per candidate, is what makes the pairing
  survive this. The judged line carries the single ref the candidate is
  judged under — the canonical one where it was pushed, the least of the
  others in byte order in every other case, including the two classes
  the drain judges with no canonical ref in hand; each remaining ref is
  its own record. A `noncanonical-ref` or `extra-ref` line names the
  candidate it pointed at but is **not** a payload line for the
  bijection — it adds no candidate parent and consumes none, so the
  parent-to-line correspondence is still one-to-one over ordinary lines
  alone.

  **A candidate that carries a parent takes the same treatment, and it
  has to, because the two halves of this note otherwise disagree.**
  Intake refuses such a candidate with reason `shape`, and a refusal is
  a verdict about an object the ledger is supposed to retain — but
  parenting it is exactly the attack the side-parent bound exists to
  stop, since a commit with a parent in a candidate slot is how `L`
  gets hidden. Retention and the bound cannot both have it. The bound
  wins, because everything the ledger walk proves rests on it: the
  refusal is recorded as a line of outcome
  `result=refused reason=shape unretained=parented`, which names the
  candidate by object id and nothing else about it, **adds no parent
  and consumes none**, and is excluded from the bijection exactly as
  the two extra-ref forms are. Naming the object and no identity is
  what makes the line writable for every candidate in the class,
  including one whose intent would not have decoded — the verdict is
  reached before any decoding, so nothing about the payload can
  withhold the record.

  The treatment is the same in every respect but one, and the exception
  falls out of the same missing object: extra refs at a parented
  candidate are `extra-ref` records rather than `noncanonical-ref` ones,
  because there is no retained candidate to recompute a canonical ref
  from and no decode was performed to compare against.

  **What that costs is stated rather than hidden.** This is the one
  refusal class whose object the ledger does not retain, and the
  classification order above is what keeps it the only one — the
  undecodable-intent and `wrong-ref` classes are parentless candidates,
  so the entry parents them and both objects survive. An auditor
  therefore cannot re-derive this reason from the record alone and takes
  the drain's word for it. It is not unfalsifiable: the submitter still
  holds the candidate they built, the reason is visible in the object's
  own parent line, and producing it either confirms the drain or
  convicts it. That holds whether or not the intent decodes, since the
  reason rests on the commit's parent line and not on its payload. So
  the whole check moves from the auditor to the disputing party for this
  class, which is the only one whose object is missing — and it *can*
  move there, because parenthood is intrinsic to the commit and does not
  perish. The `wrong-ref` class below is **not** a second instance of
  that, and an earlier draft said it was: half of its verdict moves
  nowhere, because what would settle it was never an object. The note
  says which classes those are rather than letting "every candidate is
  retained" stand while one is not.
- an `admitted` line's `event` commit lies on the `P..E` sequence;
- a `refused` line's `reason` is **recomputable**, in the precise sense
  below, except for the `unretained=parented` class above, whose object
  the ledger does not hold and whose reason is therefore the drain's
  word until the submitter produces the candidate. An `identity=none`
  line is recomputable like any other `shape` refusal, since failing to
  decode is a property of the retained object;
- a `wrong-ref` line is recomputable in the half that rests on an
  object, and in the other half it is recomputable by nobody. The
  auditor recomputes the canonical ref from the retained candidate and
  requires the recorded ref to differ from it; that half is mechanical,
  and one further check goes with it. If the canonical ref appeared on
  a line **at that candidate** — as an ordinary line's ref, or as a
  `noncanonical-ref` record — the entry would contradict its own
  verdict, so the auditor requires the recomputed canonical ref to
  appear on no line at that candidate.

  **That check is narrower than an earlier draft claimed it was, and
  the gap between the two claims is the residue.** The draft said a
  drain wanting a false `wrong-ref` must therefore suppress the
  canonical ref from the entry entirely rather than misfile it. It need
  not. It can record those same ref bytes as an extra-ref record of
  either outcome against a *different* real candidate in the same
  batch, and every
  check above still passes: at the refused candidate the canonical ref
  appears on no line, and at the candidate it was moved to the record
  says only that these bytes are not *that* candidate's canonical ref,
  which is true. The entry stays internally consistent because nothing
  in it is checked against anything outside it — the refs are deleted,
  and a line's `candidate=` field is the drain's own assertion of the
  pairing, with no retained object binding a ref to the target it
  actually had.

  **What makes the reassignment unreachable is that it is a legitimate
  input, byte for byte.** A ref bearing one identity's canonical name,
  pointed at another submitter's candidate, is exactly the cross-actor
  case above — an input this note requires the drain to accept, record,
  and attribute to nobody. So the misfiled record is not merely hard to
  detect; it is indistinguishable in principle from a record the drain
  is obliged to write, and tightening the check would mean refusing an
  input the note has already decided to allow. The check is kept for
  what it does catch, a drain that misfiles at the same candidate, and
  the claim that it forces total suppression is withdrawn.

  **And where those bytes actually were is what no party can settle.**
  Whether the canonical ref was in the listing the drain read, and
  which candidate any listed ref pointed at, are facts about refs
  the drain then deleted in the acquisition push, and a ref is not an
  object: nothing retained records it, and nothing pushed afterwards
  restores it. So this half is a **drain assertion**, and it is the one
  clause in the entry that no reader, auditor or submitter can convict
  or acquit after the fact. An earlier draft said the submitter settles
  it by producing the canonical ref they pushed, and called that the
  same move as producing a parented candidate. It is not the same move.
  Parenthood is intrinsic to the candidate commit, so the submitter
  holds that evidence for as long as they hold the object; ref presence
  at drain time is extrinsic and perishable, and a ref pushed today says
  nothing about a listing taken then. Settling it would need
  authenticated transport evidence, which is exactly what this note
  already declines to invent for `noncanonical-ref` attribution, and for
  the same reason: the object graph does not hold it and no party to
  the dispute can be asked to vouch for it.

  **Retaining the listing was considered and does not fix it**, which is
  worth recording so the obvious repair is not proposed a fourth time.
  The drain could write the refs it observed into the entry — it already
  does, one line per ref — but the entry is authored and signed by the
  drain, so a drain willing to omit a ref from its verdict is willing to
  omit it from its listing, and equally willing to write it against the
  wrong candidate there as well: a listing with the same author as the
  verdict cannot contradict the verdict. The record grows without
  gaining a witness. Evidence from the other side does not close it
  either: a signed push certificate is produced by the submitter, need
  not be retained by the receiver, and in any case proves that a push
  was made rather than that the ref was still there when the drain
  listed — another writer with inbox access can delete a ref between the
  two. A listing that settled this would have to be signed by the forge,
  at drain time, and bind each ref to the object it resolved to, since a
  bare list of names would leave the reassignment untouched. No
  mainstream forge offers either. So this half stays an assertion, and
  the note says so rather than adding a mechanism that looks like proof.

  What that costs is bounded, and stating the bound is the point of not
  pretending the check exists. The candidate is retained and its refusal
  is recorded, so nothing disappears and no work is destroyed: the
  submitter re-pushes the same object under its canonical ref and it
  reaches admission on a later batch, judged on its merits. The
  reassignment costs the candidate it is moved to nothing, and for a
  reason already settled above: an extra-ref record of either outcome
  attributes to nobody, adds no parent, consumes none, and cannot alter
  the outcome the judged submission at that candidate reaches. What it
  buys the drain is only the concealment, not a second victim. An honest
  refusal and a false one are indistinguishable in any single entry, and
  differ only in that the false one has to be repeated to keep having
  effect — each repetition another signed entry attributable to the
  drain. That is a pattern a reader may notice, not a proof a reader can
  check, and this note does not promote the one into the other.

Recomputability needs its inputs named, because they are not all in the
candidate. `shape` and `unsigned` are candidate-local: the auditor
reaches them from the object alone. `size` is not — it is a verdict
against the **genesis descriptor's payload ceiling** — and
`unknown-log` is not, being a verdict about the target this candidate
names. So the rule is that the auditor re-runs admission against the
retained candidate **together with the genesis descriptor and the
recorded `P`**, both of which this topology already makes durably
reachable through the entry's own header and parents, and **against the
admission profile bundle the governance record puts in force at `base`**
— which the entry must name and parent, and which the auditor resolves
for itself, by the selection rule specified below, rather than reading
out of the entry.

That last pin matters and is not pedantry. Admission rules change. With
no version recorded, an auditor running today's rules against a batch
refused under yesterday's can reach a different verdict while the drain
and the auditor are each correct for their own era — a disagreement the
record would report as a fault. The entry therefore names and parents
the profile bundle commit it ran, and the auditor reproduces *that
ruleset* — retained, not a label pointing at rules someone else is
obliged to keep. The interpreter for its contract version remains
exactly such a label, under the stated obligation above, and the
difference between the two is the difference between a fault and an
inability.

Any batch whose parents and lines fail to pair, or whose refusal
reasons do not reproduce under the recorded profile, is a detected
fault — which is what "without trusting the drain" has to mean if it is
to mean anything.

An auditor that cannot obtain an interpreter for the recorded contract
version, though, reports **`profile-unavailable`** and
reconciles nothing. It must not silently fall back to the current
interpreter, and it must not report a fault: both would charge the drain
for a dependency the record never held, and the second would turn a
missing tool into an accusation. The bootstrap profile specified below
takes no exemption from this: its contract version is the genesis
descriptor's `gitseq-genesis-v0`, so the same outcome is reachable for
it, and the only difference is that nobody expects that particular
interpreter to become unobtainable.

**"Without trusting the drain" means something narrower than it sounds,
and the bound belongs next to the claim.** The phrase holds over the
pairing, over the refusal reasons under the recorded profile, and over
every fact an entry states about an object the ledger retains. It does
**not** reach what an entry states about refs that no longer exist:
whether a `wrong-ref` candidate's canonical ref was in the drain's
listing, and, for every recorded ref, which candidate it actually
pointed at. An auditor must report neither a fault nor a pass on either
clause. The second is the wider of the two and was the later to be
seen: because no retained object binds a ref to its target, a false
`wrong-ref` needs no suppression at all — the same bytes recorded
against another candidate leave every check passing. Nor does the
phrase reach an `unretained=parented` reason unless the submitter
produces the candidate. Everything else in an entry is checkable
against retained objects, which is the property this topology was built
for. Those are the residue, and naming them is what keeps the rest of
the sentence worth making.

**"In force at `base`" was a name and not a mechanism, and this is the
mechanism.** Three passages above now lean on the phrase — the profile
parent role, the recomputability rule, and the `profile-unavailable`
outcome — and none of them said what statement puts a bundle in force,
who may make it, or how an auditor picks exactly one at an arbitrary
base. When this section was first written the gap was total, and
measured so: the fold had no admission-profile machinery of any kind, a
statement of a kind it did not know was recorded but could not be
ratified, and "put in force by a ratified statement in the sequence"
named an act that could not happen.

**That has changed under the note, and the section is now part
description.** Measured on the current tree: `admission-profile` is a
declared kind, in the governance render class with a `role:ratifier`
satisfier, so it is stated by anyone and put in force only by a
ratifier; and `SelectAdmissionProfile` resolves the profile in force
over a fold prefix by the rule this section specifies, falling back to
the bootstrap profile computed from the genesis hash. What is still
absent is everything downstream of that resolution: no drain, no ledger
entry carrying `profile=`, no auditor lookup. So the section is a
description of the governance half and a contract for the rest, and it
diverges from the shipped kind in one place, stated with the field it
concerns below. The last paragraph of this section says which parts were
measured, which are obligations, and which now disagree.

**The activation statement.** The governance kind
`admission-profile`, with two required body fields:

```
bundle   = <object id of the profile bundle commit>
genesis  = <genesis hex of the log this profile governs>
```

`genesis` is present for the reason the inbox ref name carries a genesis
component: one workroom may govern more than one log, and a profile that
does not name its target silently governs both.

**There is no `contract` field, and an earlier draft's was inert.** That
draft required the activation to name the interpreter contract version
the bundle declares, and then specified a lookup that takes the version
from the bundle tree and never reads the activation. Two sources with no
rule joining them: a ratified activation could say one version while its
bundle said another, selection would still put it in force, and replay
would silently use the bundle's — a required governance field that no
party reads and no check can fail. The field was a value the activator
authors over evidence the bundle already carries, which is exactly why
`entries` is gone from the ledger header: a duplicate of something the
record already holds adds a way to manufacture a disagreement without
adding a way to detect one. So the **bundle's `contract` blob is the
sole source for an activated profile**, the genesis descriptor is the
sole source for the bootstrap profile, and neither is the activation.
The fold should require `bundle` and `genesis` and read nothing else;
any further field an activation happens to carry is inert, and no
auditor may take a contract version from one. A ratifier who wants to
know which contract they are putting in force resolves the bundle and
reads it there, which they must do in any case — the rules themselves
are only in the bundle.

**The shipped kind disagrees, and the note records the disagreement
rather than settling it silently.** Measured on the current tree, the
`admission-profile` kind requires three body fields — `bundle`,
`contract` and `genesis` — and the resolver reports the activation's
`contract` value as the profile's contract version, which is then what
decides interpreter availability. That is the shape this passage argues
against, shipped: two sources for one version, joined by no rule, with
the activation winning. One of the two has to move. This note does not
move, because the argument above is unchanged by the code existing — the
field is authored by the activator over evidence the bundle already
carries, which is the same defect that removed `entries` from the ledger
header. Reconciling it is a change to the kind, and it is small: drop
`contract` from the required fields and take the version from the bundle
tree. Until that happens, the spike's stray-version-field run below has
a live subject rather than a hypothetical one, and no auditor built to
this note may read a contract version out of an activation.

**What the fold has to do, and it is three registrations rather than a
new judgement.** Add `admission-profile` to the known kinds; give it the
governance render class and the ratifier satisfier, so ratification is
admitted exactly as it is for `roster`, `infra-key` and `seal`; require
the body fields at validation. Nothing else. The fold learns no
admission semantics — it does not read the bundle, does not fetch it,
and does not know what a contract version is. Selection is a read over
the projection the fold already produces, which is why this needs no new
machinery and why an auditor can run it. All three registrations are now
in the tree; only the required-field list differs from what this note
specifies, as stated above.

**Who may activate.** Anyone may state one; it is a draft like every
other governance statement. It is in force only once ratified by an
actor holding the `ratifier` role — the authority the fold already
computes, which this design extends by nothing. Measured on the current
fold: a ratification by an actor with no roster grant is ineffective,
"actor lacks ratifier role".

**Selection at an arbitrary base, which is the part an auditor has to be
able to run.** The profile in force at sequence commit `B` is the bundle
named by the **last** `admission-profile` statement that lies at or
before `B` on the sequence, names this log's genesis, is effective, is
ratified at or before `B`, and is not retired at or before `B`. "At or
before `B`" means position on the sequence's first-parent chain, which
is the order the fold consumes. That order is total, so "the last" names
exactly one statement or none.

It is resolvable because the fold is already a pure function of a log
prefix: an auditor truncates the sequence at `B`, folds it, and reads
the newest live activation out of the resulting projection. That read is
now in the tree as `SelectAdmissionProfile`, and measured on it, on the
real kind rather than on the stand-in an earlier revision had to use: at
a prefix cut before the ratification the statement is not in force; one
record later it is; with a second activation ratified, the newer one is
selected; once that newer one is retired, the older one returns to
force; and a stale activation stays in force. That retirement case is
the right answer rather than a special case — supersession selects a
predecessor instead of leaving a hole.

**A batch runs under exactly one profile, and that falls out of
resolving at `base`.** A batch may itself admit an activation statement,
or the ratification that puts one in force: the vocabulary is
self-hosting, exactly as the roster is. That does not split the batch.
The profile is resolved once, at `base`, before any candidate is judged,
so a batch that admits an activation is judged entirely under the
profile that preceded it, and the next batch — whose `base` is at or
after the ratification — is judged under the new one. Unlike the
rotation boundary above, this needs no restriction on what a drain may
admit, because the profile is consulted once per batch while the signing
key is used throughout it. So the second parent slot holds exactly one
parentless profile commit per entry however much governance moves during
the batch, which is what the side-parent ancestry bound requires of that
position.

**Staleness does not deselect.** The fold also flares a statement
`stale` when something it rests on is retired. A stale activation stays
in force. Staleness propagates from bases the drain never sees, so
letting it deselect would make admission depend on distant governance
no party to the batch can observe, and the fold itself keeps stale
statements effective. Retirement is the deselecting act; staleness is a
signal to a reader.

**The bootstrap profile, because every workroom starts without one.**
The first batch of a genesis-v0 workroom has its `base` at or near the
genesis commit, where the prefix contains no activation at all, and no
amount of grammar makes that case go away. So where no activation is
selected, the profile in force is the **bootstrap profile**: the empty
parentless commit computed from the genesis hash alone, by exactly the
byte rule the ledger root uses, with the message

```
gitseq-admission-profile-v0 <genesis-hex>
```

Its tree is the empty tree, so it declares no rules of its own. It
denotes the admission defined by the genesis descriptor — the shape and
signature checks, the payload ceiling, the target check — and **its
contract version is the descriptor's own**, `gitseq-genesis-v0`.
Measured: that string is the kernel's literal genesis marker, and the
descriptor's version field is validated to be exactly 0, so the version
is read out of an object the log already carries rather than assigned
here.

That matters more than it looks, because the tempting version of this
rule is wrong. "Replay a bootstrap batch under the kernel's own
admission" would mean *today's* kernel, which is an unversioned label
for code that changes — the exact hazard the profile pin exists to
remove, and this note calls that kind of disagreement a fault two
sections up. So the bootstrap profile takes no exemption. Replaying it
needs an implementation of `gitseq-genesis-v0` admission, under the same
availability obligation as any other contract version, and
`profile-unavailable` is possible for it in principle. What is true, and
all that is true, is that this is the one contract the project cannot
drop while any log exists, so in practice the interpreter is the one the
auditor is already running. Possible-and-not-expected is a different
claim from cannot-happen, and this note has been caught on the
difference before.

The object itself is measured the way the root was measured: the byte
string hashed by an implementation that is not Git equals
`git commit-tree`'s output, and for this workroom's
genesis both give `6ad9570a5b4f824304c855a25511001e126f6a3c` — distinct
from the ledger root `28ed9b9f…` because only the message differs, which
is the same lesson the SHA-256 disagreement taught: the bytes decide.
A third implementation now agrees: the fold's own bootstrap
computation, written against this byte rule rather than against these
figures, returns the same id for this workroom's genesis.
It is parentless with an empty tree, so it fills the profile parent
role without an exception, and it is neither the root OID nor
sequencer-signed, so it is refused in the predecessor slot without one
either. A drain running with no activation in force writes that object
and parents it like any other profile — one `commit-tree` over the empty
tree — so the profile slot is never empty and the parent grammar needs
no arity case for it.

The bootstrap profile is also what keeps this design from degrading into
a hole in a workroom that has activated nothing, which is every workroom
until someone does. Every batch there resolves to it, any auditor can
compute it from the genesis hash, and replaying those batches needs only
a `gitseq-genesis-v0` implementation — which is the kernel the auditor
is running anyway. What that buys today is still an **oracle and not a
run**: the object, its id and the resolution that returns it all exist
now, while everything that would consume them — the drain, the entry's
`profile=` header, the auditor's contract lookup — is still design. So
the spike cases below have a computable oracle and a working resolver,
and no batch to point either at.

**The bundle tree.** An activated bundle is a parentless commit whose
tree holds exactly two entries, both regular blobs at mode `100644`, and
nothing else — no subtrees, no symlinks, and no gitlinks, since a
gitlink would move a dependency behind an object id without retaining
it, which is the failure that killed the claim-tree draft:

```
contract   one line: "gitseq-admission-contract <version>\n"
rules      the declarative ruleset; opaque bytes to this grammar
```

`<version>` matches `[A-Za-z0-9.-]{1,32}`. The bundle commit's message
is unconstrained, deliberately: the bundle is identified by object id
equality against the resolved activation, so its message carries no
weight and must not be given any.

This grammar describes the adopted form. The retained-closure option
recorded above as available and not adopted would put interpreter blobs
in this same tree, so taking that option means amending this grammar
rather than smuggling extra entries past it — which is what a verifier
written to "two entries and nothing else" would otherwise reject.

A bundle whose tree does not match this grammar is a **fault against the
activation, not against the drain** — the ratifiers put an ill-formed
bundle in force, and the drain ran what governance told it to run. That
is the same distinction `profile-unavailable` draws, and it matters for
the same reason: charging the drain for another party's error makes the
ledger a bad witness.

**Contract-version lookup, and the one result that is neither pass nor
fault.** An auditor reconciling an entry:

1. resolves the profile in force at the entry's `base` by the selection
   rule above, and requires the entry's `profile=` header to equal it. A
   mismatch is a fault against the drain — this is the check that
   catches a drain naming its own rules;
2. takes the contract version from the genesis descriptor if the
   resolved profile is the bootstrap profile, and from the bundle
   tree's `contract` blob otherwise — one step with two sources, not
   two rules, and the activation statement is neither of them;
3. obtains an interpreter for that version and, with one, replays and
   reports a verdict;
4. with none obtainable, reports **`profile-unavailable`** for that
   entry and reconciles nothing — neither a fault nor a pass.

The fallbacks that must not happen are now two rather than one. Running
the *current* interpreter was always forbidden. Running
`gitseq-genesis-v0` admission is the new one, and it is the more
tempting of the two, because that interpreter is always to hand — an
implementation that reaches for it when the recorded version's
interpreter is missing converts "I cannot check this" into "I checked it
under rules nobody activated", which reads as a pass. The bootstrap
profile does not license it: bootstrap means the genesis-v0 contract
specifically, not whichever contract is cheapest to reach.

**What is implemented here, what is an obligation, and where the two
disagree.** This paragraph was a list of absences when it was written,
and half of it has since become a list of code; keeping the old wording
would have been the cheapest way for this note to start lying.

Implemented, measured on the current tree: the `admission-profile` kind,
declared with the governance render class and the ratifier satisfier, so
anyone may state one and only a ratifier puts it in force; the selection
read at an arbitrary prefix, including the return to an older activation
when the newer is retired and the rule that a stale activation stays in
force; `profile-unavailable` as a typed outcome distinct from both a
pass and a fault; and the bootstrap profile's bytes and object id, which
the fold computes from the genesis hash and which agree with
`git commit-tree`. Also measured, unchanged from before: that a
non-ratifier's ratification is ineffective, and the genesis marker and
the descriptor's version field the bootstrap contract is read from.

Still obligations, stated here as the contract an implementation must
meet: the bundle grammar; every contract-version lookup; the auditor
that performs it; and — underneath all of them — the tier-1 drain that
would resolve a profile at `base` and write the entry naming it, which
does not exist in any form. So no sentence in this note may say a batch
has ever run under a profile.

Disagreeing, and named above where the field is specified: the shipped
kind requires a `contract` body field and its resolver reports that
value as the contract version, where this note requires the version to
come from the bundle tree and an activation's own version field to be
inert. The note holds its position; the kind is what should move.

An earlier draft wrote refusals to `refs/notes/gitseq-refusals`,
justified as "notes are retained, fetchable, and do not expire." That
justification answers the wrong half. Retention is not the exposure
here — a refusal reason is text, reachable from its own notes ref, so
nothing prunes it. **Protection is.** Rulesets target branches and
tags, so exactly as with the batch note above, nothing documented could
stop a submitter deleting the ref that records their own refusal. A
submitter who deletes their refusal record moves their submission from
*refused* to *in flight* in the three-state decision below, which is
the one transition the protocol must not permit them to author.

So refusals go where the retained batch record already goes: the
append-only, ruleset-protected ledger branch. This removes a mechanism
rather than adding one.

A drain that refuses every submission still appends its ledger entry,
parented on the previous ledger head, **the profile bundle** — a batch
entry parents the profile it ran, per the roles above, and an
all-refused batch ran one like any other — **and every refused
candidate**. An earlier draft gave it the previous head alone,
reasoning that an all-refused batch has no candidates. That was wrong,
and wrong in the way this document has now been wrong three times:
*refused* is a verdict **about** a candidate, not the absence of one.
Every refused inbox branch still points at a submitter-authored commit.
Measured: with a sole-parent ledger entry, the inbox ref deleted and
`gc --prune=now` run, the refused candidate is pruned; with that
candidate as an additional parent, it survives. The refusal *line*
survives either way, which is exactly what made the error easy to
miss — the record still reads correctly while the object it describes
is gone.

That mattered because of a claim made two paragraphs above: that the
payload lets an auditor reconcile a batch **without trusting the
drain**. Under the sole-parent rule an auditor could not recompute the
identity, recheck the refusal reason, or test the one-line-per-
submission property at all; they could only read the drain's own
summary of what it had refused. The mechanism removed the evidence for
the property the mechanism was there to provide.

`E` is unaffected and is still `P`, and `E` is *not* added as a parent:
with nothing admitted it is already reachable from `refs/seq/<genesis>`,
which is where `P` is retained in any case, so the edge would be
redundant. "Already reachable" needs saying exactly, because the
computed root changed what the ledger reaches. Any entry that admitted
something parents `E`, and `E`'s ancestry is the sequence, so from that
entry onwards the ledger does hold the sequence — which is what the
earlier measurement above found. An entry that admitted nothing adds no
such edge, and the root names the genesis object in its message rather
than parenting it, so a ledger whose entries have all been all-refused
holds no sequence object at all. That is not a defect to repair: `P` is
retained by `refs/seq/<genesis>`, and the ledger's job is to retain what
nothing else does — the candidates and the profile. An earlier draft
said an all-refused batch has no `E`, and that broke the state machine
rather than simplifying it: the claim records `P`, `M` and `E`, and the
`P`/`P`/live row instructs a recovering run to publish the recorded
`E`. With no `E` recorded, a crash after acquiring an all-refused claim
reaches a row whose action is impossible to perform.

Defining `E` = `P` keeps every existing row correct without a second
transaction to specify. The recovery action stays "publish the recorded
`E`", which for this batch is a no-op fast-forward to where the
sequence already is — exactly right, because nothing was admitted. The
ledger commit still omits `E`, because `E` = `P` is already reachable
from the sequence ref in the sense set out above and needs no retaining
here. It does **not** omit candidate parents: the
refused candidates are exactly what it must retain, as above.

A claim older than the recovery window is **never deleted**; it is
reconciled by the table above. Nothing is removed on a timer.

The submitter's protocol is a decision over three retained states: the
idempotency identity appears in the log (admitted), a ledger entry
records a refusal for it (refused, reason attached), or neither and an
inbox or claim ref still exists (in flight). The key into the **ledger**
is the candidate's own object id, which every line in the grammar
carries and which the submitter holds in every case, having built the
object; the idempotency identity is the key into the **log**, and it
exists exactly where the intent decoded. Keying the ledger on the object
id is what makes the decision performable for the two classes that have
no identity to key on — the undecodable intent and the parented
candidate — and it is why every line carries `candidate=` while only the
ordinary ones carry `identity=`. Every one of the three states is
durable *and protected against the submitter*, so "silent disappearance
is a bug" is a property of the protocol rather than an assurance about
it.

**What a terminal record settles is bounded, and the bound belongs
here.** A record says that the submission reached a verdict, which
candidate the drain says it was about, under the ref bytes the drain
says it was judged under, and — for every class but two — a reason an
auditor can recompute from the retained object. The two exceptions are
the ones named with the bijection: an `unretained=parented` reason,
which only the submitter's own candidate can confirm or refute, and the
ref half of a `wrong-ref` verdict — what the listing held, and where any
recorded ref actually pointed — which nothing can. So a `wrong-ref`
record tells a submitter that they were refused and under which ref; it
does not tell them, and cannot be made to tell them, that the refusal
was earned. "No
submission disappears" is the claim this protocol makes good on. "Every
recorded reason is provable" is not, and the note does not say it.

**Latency, and what schedule-only costs.** "Seconds to a minute" was
written when a push triggered the drain. Removing that trigger for
isolation invalidated the number, and leaving it standing would have
been an overcorrection paid for in a claim elsewhere in the note —
worth stating plainly, because a repair that quietly breaks a
neighbouring claim is a defect the repair introduced.

The honest posture is **zero to five minutes, plus unbounded delay**.
GitHub's shortest schedule interval is five minutes, scheduled runs
are delayed under high load and queued runs may be dropped outright,
and the workflow executes only from the default branch. So a
submission waits up to one interval in the ordinary case and
arbitrarily longer in the bad one, and the drain must be idempotent
against a dropped run rather than assume every tick fires — which the
claim-and-recover protocol above already provides, since nothing is
deleted on a timer and a stale claim is re-drained.

Two operational conditions follow and belong in the runbook rather
than in a footnote. Scheduled workflows on a **public** repository are
disabled automatically after 60 days without activity, so a quiet
workroom stops draining silently; the control repository therefore
needs either private visibility or a liveness check that notices the
disable and reactivates. And because a submitter cannot observe the
schedule, the submitter-facing contract is the three retained terminal
states, not a time bound.

None of this touches the durable-acts bar, and it still does not
pretend toward the interactive one: there is no nexus at this tier,
`say` and `presence` refuse per the degraded contract, and the
workroom is asynchronous by construction. A tier whose liveness lags
its durability is coherent — but only if the lag is stated.

**Spike, pass/fail** (house rules: executable, adversarial, evidence
projected to a results file):

1. **Race** — two submitters push inbox branches concurrently; both
   events admitted in one total order; actor-scoped dedup holds under
   a replayed inbox branch.
2. **Backlog drain, and separately, run coalescing.** The original
   case asserted that N burst-pushed branches produce ≤ N runs and
   exactly N admissions — which schedule-only makes vacuous, since
   inbox pushes no longer trigger anything and one scheduled run
   trivially satisfies "≤ N runs". It proved backlog draining while
   claiming to prove coalescing, so it is split.
   **(2a) Backlog drain**: N branches accumulated between ticks are
   all admitted by the next run, exactly once each, in one total
   order. **(2b) Coalescing**: this needs **three** overlapping runs,
   not two. A concurrency group holds one running and one pending run,
   so two runs only ever demonstrate queueing — the pending one is
   never displaced, and nothing is replaced. Replacement is visible
   only with a third: one run still executing, a second sitting
   pending, and a third arriving to evict the second. The case passes
   when the evicted run is cancelled, one survivor remains, and that
   survivor drains every candidate including the evicted run's. The
   two-run version was this list's second cannot-fail case, and it was
   introduced by the previous repair rather than inherited.
3. **Token authority** — the workflow's `GITHUB_TOKEN` (contents:
   write) can create and fast-forward `refs/seq/<genesis>`; if a
   forge policy forbids it, the deploy-key fallback is documented as
   the spike's finding, not discovered by a user.
4. **Refusal** — a malformed intent and an oversized payload each
   yield a refusal recorded in the drain's ledger entry with the right
   reason and no sequence append; the entry is still readable long
   after any plausible TTL, and the submitter's own credential cannot
   delete it. That last clause is the one worth executing: a refusal
   record the refused party can remove is not a record. The malformed
   intent here is one that still **decodes** into the four identity
   fields and fails a later check, so both lines carry identities; the
   intent that does not decode that far is a hostile run below, because
   it exercises a different line form.

   Run with those two as the *only* submissions, this is also the
   all-refused batch, so it carries that case's assertions: the claim
   records `E` = `P`; the ledger entry is parented on the previous
   ledger head, **the profile bundle** and **both refused candidates**;
   and killing the run after claim acquisition lands on the `P`/`P`/live
   row, whose recovery publishes the recorded `E` as a no-op and
   releases.
   Asserting `E` is recorded and equal to `P` is the point — an
   all-refused batch that records no `E` reaches a row it cannot
   perform, which is how this rule was got wrong the first time.

   The retention assertion must be run the only way that can fail.
   **Release the claim first**, then assert both inbox refs are already
   **absent** — the acquisition transaction deleted them, so a case
   that deletes them here is describing a state it should be checking —
   then `git reflog expire --expire=now --all`, then
   `git gc --prune=now`, then require `git cat-file -e` to **succeed**
   for both refused candidates and the auditor to recompute both
   reasons from the retained objects under the profile bundle — the one
   the entry parents, checked against the one the selection rule
   resolves at `base`, since resolving it from the entry alone is
   trusting the drain to name its own rules.

   The same case carries the alias assertions, since aliasing is a
   submitter-controlled input and this is the case a submitter drives.
   Run it two ways. **Canonical only**: one candidate under its
   canonical ref, producing one ordinary line with one candidate
   parent. That is the positive control, and without it the hostile
   runs below pass for a drain that silently drops everything, which
   looks identical from the outside. **Canonical plus a second ref**:
   the same candidate also pointed at by another ref, which must
   produce the canonical line *unchanged* plus a separate
   `noncanonical-ref` record naming the ref bytes and attributing them
   to nobody. The earlier version of this case required the two to
   collapse into one line, which was the contradiction with canonical
   validation and is retired with it.

   Six hostile runs go with them, because each closes a rule that is
   otherwise only stated. The first two concern extra refs pointed at
   one candidate. A **cross-actor ref**: a second submitter
   points a ref in their own namespace at the first submitter's
   candidate, and the case must show the canonical submission reaching
   its own outcome untouched while the extra ref becomes its own
   `noncanonical-ref` record. The case must **not** assert who created
   that ref — asserting it would encode the attribution Git cannot
   supply, and a case that demands the impossible will be made to pass
   by inventing it. And a **delimiter-bearing ref name**, carrying a
   comma and an `=`, which must round-trip through the length-prefixed
   encoding and be un-forgeable into an extra or missing entry. The
   forged-root run that used to sit here has moved to case 11, whose
   subject it actually is: a submitter cannot write the ledger branch at
   all, so a ledger forgery is not a case a submitter drives.

   A third hostile run covers the **parented candidate**, which is a
   submitter input and the one refusal class the ledger records without
   retaining. The submitter pushes a candidate carrying a parent; the
   entry must carry an `unretained=parented` line naming it; that line
   must consume no candidate parent, so the bijection over ordinary
   lines still holds; and after release, reflog expiry and
   `gc --prune=now`, `git cat-file -e` on that candidate must **fail**
   while it succeeds for every retained one. Asserting the failure is
   the point. A drain that parents it anyway passes every other
   assertion in this case and silently reopens the side-parent hole
   case 11 exists to close.

   That run has three mutations. The first two are the two candidates
   the class has to cover: one carrying a parent and an intent that
   decodes into the four identity fields, and one carrying a parent and
   an intent that does not decode at all. Both must produce the same
   line, and that line must carry **no `identity=` field** in either
   run. The pair is the assertion, not either run alone: a drain that
   emits an identity for the decodable one has decoded before deciding
   retention, which is the order this classification forbids; and a
   drain that emits nothing for the undecodable one has left a candidate
   with no terminal record, which is the hole this class was written to
   close. Run the undecodable candidate under two refs as well, and
   require the extra ref to become its own `extra-ref` record rather
   than a second refusal line.

   A **third mutation** carries the truthfulness of that record, and it
   is the one the pair above does not reach. Push the **decodable**
   parented candidate under **two** refs: its own canonical ref `R`,
   recomputed by the case and not read out of the entry, and a wrong ref
   `S` constructed so that `S` sorts **before** `R` in byte order. The
   judged line must record `S`, since no canonical ref was computed; the
   record for `R` must carry outcome `extra-ref`; and the entry must
   contain **no line at that candidate with outcome
   `noncanonical-ref`**. That last assertion is the whole run. Under the
   earlier rule the drain was *required* to write `noncanonical-ref` at
   `R`, so a drain still implementing it passes every other assertion in
   this case and every assertion in the two runs above, while stating
   that the submitter's true canonical ref is not canonical — in the one
   class whose object is pruned, so no auditor can contradict it. The
   sort order is what makes the run fail rather than pass by luck: with
   `R` least, the judged line would carry `R` and the false record would
   never be written, which is why the case must construct `S` to sort
   lower rather than take whatever two names come to hand.

   The **submitter** is the party who checks it, for the same reason the
   parented reason is theirs to settle: they hold the candidate, so they
   recompute `R` and read the entry keyed on the candidate's object id.
   The case therefore runs this check from the submitter's side, after
   the prune that removes the object from everyone else's reach.

   A fourth covers the **undecodable intent**, which is the input the
   identity-keyed grammar could not describe at all. The submitter
   pushes a candidate whose canonical intent does not decode into the
   four identity fields. The entry must carry one ordinary line for it
   with `identity=none`, that line must consume a candidate parent, and
   after release, reflog expiry and `gc --prune=now` the candidate must
   still be present — the refusal is about an object, and the object is
   retained whether or not it could be named. The run must also show
   that no ref-digest verdict was recorded against it, since there is
   nothing to recompute a digest from; a drain that reports `wrong-ref`
   here has invented an identity it could not read.

   A fifth covers the **candidate pushed only under a wrong ref**. The
   submitter pushes a well-formed candidate under a ref whose digest
   does not match the one recomputed from the candidate's own intent,
   and pushes no other ref at it. The entry must carry one ordinary
   `wrong-ref` line naming the candidate and the ref bytes, that line
   must consume a candidate parent, the candidate must survive the
   prune, and the auditor must reproduce **the half of the verdict that
   rests on an object**: recompute the canonical ref from the retained
   candidate, require the recorded ref to differ from it, and require
   the recomputed canonical ref to appear on no line at that candidate.
   A drain that records this as a `noncanonical-ref` record instead
   keeps the line count right and loses the object, which is the failure
   this run exists to catch, and the run must assert the survival rather
   than the line to catch it.

   The run must **not** assert that the auditor established the
   canonical ref was absent from the listing, and must not assert that
   any recorded ref pointed where the entry says it pointed. The
   acquisition transaction deleted the inbox refs, nothing retained
   records what was there or what each name resolved to, and a case that
   asserts either will be made to pass by trusting the entry it was
   written to check — which would be this list's fourth cannot-fail
   case, and the one an implementer is likeliest to add in good faith.

   A sixth hostile run pins that as a limit rather than a pass, the way
   case 11 pins the force-update residue. It has three instrumentations
   and needs all three, because the first two together suggested a
   boundary the third shows is not there.

   **Total suppression.** The submitter pushes candidate `C` under its
   canonical ref *and* a wrong one; the drain, instrumented to lie,
   records only the wrong one as a `wrong-ref` refusal and drops the
   canonical ref from the entry. A ledger-only auditor must find **no
   fault**, because there is none in the record to find, and the case
   passes on that finding.

   **Misfiling at the same candidate.** The same drain instrumented to
   record `C`'s canonical ref as a `noncanonical-ref` record *at `C`*
   must be **caught**, since the recomputed canonical ref then appears
   at a candidate the entry refused for lacking it. That is the
   at-candidate check working, and it is the only lie it catches.

   **Cross-candidate reassignment**, which is the run that was missing.
   The batch carries a second, unrelated, well-formed candidate `D`
   pushed by another submitter under `D`'s own canonical ref. The drain
   records the `wrong-ref` refusal at `C` as before, and records `C`'s
   canonical ref as a `noncanonical-ref` record **at `D`**. Every check
   must pass and the auditor must find **no fault**: at `C` the
   recomputed canonical ref appears on no line, and at `D` the recorded
   bytes genuinely are not `D`'s canonical ref, which the auditor
   confirms by recomputing from `D`'s retained object. `D`'s own
   ordinary line must be unchanged, which is the assertion that keeps
   the reassignment from being mistaken for damage to `D`. The case
   passes on the no-fault finding, exactly as the first does.

   Running the second and third together is what makes either mean
   something. Alone, the second reads as though the at-candidate check
   forces a drain into total suppression; the third is the counterexample
   in executable form, and an implementer who writes the check believing
   it does more than it does will be contradicted by a run rather than by
   a paragraph. The three also stand at different distances from
   proof: the first two are about a ref the entry does or does not
   mention, the third about a binding between a ref and a target that
   the record never held. A case that asserted a fault in the third
   would demand a check no auditor can perform, and would be made to
   pass by having the auditor trust the drain's own `candidate=` field —
   this list's fifth cannot-fail case, and a close relative of the
   fourth.

   The replay assertion needs a resolver to exercise before its
   interpreter negative means anything: three positive runs, a
   wrong-profile rejection and a stray-field run, because a resolver
   that returns the same bundle for every base passes any
   single-activation test. All five construct the sequence prefix and
   not just the ledger, since the profile is resolved from the
   sequence.

   **First batch, no activation**: a batch whose base has no activation
   in its prefix resolves to the bootstrap profile, whose id the case
   computes from the genesis hash independently of the drain, and both
   refusal reasons reproduce under a `gitseq-genesis-v0` interpreter and
   no other. It must assert the contract version it used, not merely
   that the reasons reproduced: a run that reproduces them without
   naming a version is indistinguishable from the forbidden fallback.
   It is the positive control for the rest, and an earlier draft also
   called it the run that works today, which it is not. Its **oracle**
   is computable today: the bootstrap profile's object id follows from
   the genesis hash, needs no fold change, and this note computed it.
   The **run** is not. A batch that resolves to that profile needs a
   drain, an entry carrying `profile=`, and an auditor performing the
   contract-version lookup, and none of the three exist — so every
   assertion in this run after the object id is design-only, the
   reproduction of the two refusal reasons included.
   **After one activation**: with a bundle activated and ratified
   before the batch's base, the entry's `profile=` must be that bundle
   and not the bootstrap one — the run that fails against a resolver
   still returning the default. **Across two activations**: with a
   second bundle activated and ratified between two batches, the earlier
   entry must still resolve to the first bundle and the later to the
   second, from one repository at one time. That is the run that fails
   against any resolver reading the current head instead of the entry's
   base, which is the implementation written first by default.
   **Wrong profile named**: an entry whose `profile=` names a
   well-formed bundle that is not the resolved one is a fault against
   the drain — without it, a resolver may be computed and then never
   compared. **Stray version field**: an activation carrying an extra
   `contract=` field that disagrees with its own bundle's `contract`
   blob must change nothing — the activation is still effective, the
   version the auditor reports and replays under is the bundle's, and
   no fault is raised against anyone. This is the run that fails
   against an implementation that put the version back in the
   activation body — no longer a hypothetical, since the shipped
   `admission-profile` kind requires that field and its resolver reports
   it. Until the kind drops the field, this run is expected to fail, and
   that failure is the finding rather than a defect in the run.

   **Negative, missing interpreter**: with a bundle activated whose
   contract version no obtainable interpreter implements, the auditor
   reports `profile-unavailable` and reconciles nothing. The run fails
   if the auditor reports a fault against the drain; fails if it quietly
   runs the current interpreter and passes; and fails if it falls back
   to the bootstrap profile's kernel admission and passes. The third is
   new with the bootstrap rule and is the likeliest of the three,
   because bootstrap is always computable and looks like a definition
   rather than a fallback.

   Every run in this block is still blocked, and what blocks it has
   narrowed. The `admission-profile` kind and the selection read now
   exist, so the resolver these runs need is real; what none of them can
   reach is the drain, the entry header and the auditor's
   contract-version lookup, and every run asserts something about an
   entry. That is stated so the case is not marked green on the strength
   of a resolver that runs — the first one included, which is the
   reading the previous draft invited by calling it the run that works
   today.

   The claim release is not incidental ordering, it is the whole
   validity of the case. The claim commit already parents `E` and every
   candidate, so while it is live it retains exactly what the ledger is
   supposed to retain — measured, a deliberately broken ledger missing
   its refused parent keeps that object alive for as long as the claim
   exists and loses it only on release. A case that prunes before
   releasing therefore passes against a ledger that retains nothing,
   which is a stricter version of the same false green that made the
   sole-parent rule look correct. For the same reason the case must
   **enumerate the refs that exist at prune time** and assert the
   ledger is the only one reaching the candidates, rather than assume
   it: the claim, the mirror and the sequence are all live objects in
   the real topology, and any of them can silently stand in.
5. **Fresh-clone audit** — an auditor with nothing but clone +
   `attach` verifies everything the Action sequenced.
6. **Vandalism detection, and recovery from a named replica.** The
   original case promised the next Action run would re-push the true
   head. It cannot. The hosted runner is ephemeral, so a fresh run
   starts with no memory of the frontier, and the only copy it can
   read is the vandalised one — it has no way to tell a legitimate
   rewind from an attack, and no way to recover commits the vandal
   deleted. The posture section's availability claim was resting on a
   recovery step that does not exist.
   So the sequence's survival is made explicit rather than assumed.
   The control repository holds a **`refs/seq-mirror/<genesis>`
   replica**, ruleset-protected against force-update and writable only
   by the drain's principal, fast-forward-only.

   Introducing a second durable copy is not free, and the previous
   draft stopped at naming it — acquiring a trusted component to fix a
   recovery claim, and leaving its update protocol unstated, which is
   the same failure this note has now made three times. Two refs in
   two repositories cannot be advanced atomically, so the order and
   every partial outcome have to be written down.

   **Order: publish, then mirror, then release.** The claim is held
   across both, and its commit records the parent head the run read
   and the candidates it is admitting — so the head the run *should*
   have published is computable from the claim alone. Publishing
   `refs/seq` is still the commit point; the mirror never moves first,
   which removes the failure where a crash after a mirror advance
   makes ordinary interruption look like vandalism.

   A run that finds an outstanding claim reconciles before doing
   anything else, using **the state table in the protocol section** —
   which is the single source of truth for these states. This case
   restated a three-row version of it that was both incomplete and, in
   the recovery row, contradicted by the note's own timestamp finding.
   Two copies of a state machine is one copy too many; the table is
   normative and this case exercises it.

   The mirror is evidence, not an oracle. Before it is advanced or
   used for comparison it is verified like any other input —
   sequencer-signed, and a descendant of what the run last recorded —
   because a mirror trusted blindly moves the vandalism target rather
   than removing it.

   Recovery stays a **deliberate operator act**: the key-holder or an
   attached auditor re-publishes from the mirror or their own attached
   clone, because overwriting a published sequence should not be
   automatic. The case must exercise **every row of the state table**,
   not a representative sample, and with **N candidates rather than
   one**, since the batch claim exists precisely because N and 1
   behave differently. Specifically: a saboteur force-move is detected
   on the next tick; attached auditors refuse the rewind; the mirror
   still holds the true frontier; each crash point reconciles to its
   own row; a rewind to the stale mirror *between publish and mirror*
   is still detected, because the claim records `E`; a **deliberately
   destroyed claim at every live row, including `P`/`P` where the refs
   agree**, is caught by the ledger branch entry rather than mistaken
   for quiescence; and recovery restores the frontier by republishing
   the recorded `E`, not a rebuilt one. Every tuple the table does not
   name must refuse without moving either durable ref — that is the
   assertion, not merely that the named rows work. Absent the mirror,
   this tier's honest claim is detection only, and the availability
   posture must say so.
7. **Malicious workflow** — the threat case the isolation exists for,
   and it must be run before tier 1 is offered to anyone. A submitter
   holding only inbox write access attempts, and must fail at, each
   of: pushing an inbox branch carrying `.github/workflows/`; pushing
   any ref outside `refs/heads/inbox/**`; **deleting or force-updating
   a `refs/heads/claimed/*` ref**; **deleting or force-updating the
   ledger branch `refs/heads/gitseq/batches/*`**; causing any workflow
   run whose file it authored; and reading the sequencer secret from a
   run it triggered. The two deletion attempts are called out rather
   than left to "any ref outside inbox", because they are the ones
   that destroy evidence.

   **A positive control comes first, and without it the case proves
   nothing.** Six refusals all pass trivially against a dead token, an
   expired credential, or a principal with no write access at all — so
   the case would go green while testing nothing about ref isolation.
   So: first prove the exact submitter credential *can* create an
   ordinary allowed inbox ref, then **snapshot every durable ref**,
   then run the six forbidden operations with that same unchanged
   credential, then verify every durable ref is byte-identical to
   *that snapshot*. The baseline is deliberately the post-control
   state, not the pre-case state: the positive control necessarily
   created a ref, so comparing against the pre-case state would report
   a difference the case itself caused. A pass is one success,
   followed by six refusals, with nothing moved after the snapshot.
8. **Crash at each commit point** — and it must assert mirror
   convergence before claim release, or it passes while exercising
   the obsolete release-after-publish behaviour. A run is killed at
   each of the three points and the next run must reach the row of
   the state table that matches: before the sequence push
   (`published=P, mirror=P`, republish the **recorded** `E` and assert
   the resulting head equals it byte for byte, not merely that one
   admission happened — otherwise the case passes while recomputing a
   different head and never exercises the invariant); between
   publish and mirror (`E, P`, fast-forward the mirror, verify, then
   release); after the mirror and before release (`E, E`, verify both,
   release). No path deletes a candidate that was never sequenced,
   and no path releases a claim while the two durable refs disagree.
9. **Ref-name adversary** — and this case must **create the refs**,
   not merely call `check-ref-format`. Checking well-formedness alone
   is a test that cannot fail for the reason it was written: a
   300-character component is well-formed and still unwritable. So:
   idempotency keys containing space, `..`, `~`, `^`, `:`, `?`, `*`,
   `[`, `\`, a leading and trailing `/`, a trailing `.lock`, `@{`, a
   non-ASCII string, and keys at 1, 255, 300 and 64Ki bytes each yield
   a ref that is actually created and actually pushed; two submissions
   differing only in target genesis, or only in namespace, or only in
   actor, land on different refs; and a candidate whose ref name does
   not match the digest recomputed from its own intent is refused
   `wrong-ref` on an ordinary line that parents it, not on a
   `noncanonical-ref` record that parents nothing — the refusal and the
   retention are one assertion here, since a refusal whose object is
   pruned is the failure case 4 tests from the other side.
   Two sub-cases must not be conflated, because they exercise
   different subjects. The **encoder** is tested over any byte string,
   including the empty one. A **candidate** is tested only over keys
   the kernel would admit — non-empty, no CR, LF or NUL — since an
   inadmissible key never becomes an intent and a "candidate" built
   from one is a fiction. Feeding the empty string to the encoder and
   calling the result a valid submission would be this list's third
   cannot-fail case.
10. **Racing claims and non-atomic receiver.** Two runs must race with
    **different batch digests and disjoint candidate sets** — the case
    a digest-suffixed claim ref would have let both win. An
    already-occupied identical claim ref cannot expose it. Exactly one
    run may hold the singleton claim. Then: a receiver advertising no
    atomic-push
    capability must make the drain refuse to run rather than fall back
    to per-ref updates. Paired with the occupied-claim case: with
    `claimed` already held, the push must leave `inbox` intact and the
    candidate drainable. This is the case that was missing, and it is
    the one that fails destructively. The occupied-claim behaviour was
    measured against a local bare `receive-pack` while the protocol
    section was written; as with case 11, that measurement binds Git and
    not the drain, so the case exists to hold the real drain to what a
    scratch repository showed, and case 10 stays unverified until it
    does.
11. **Ledger grammar, parent roles, and truncation.** The drain writes
    this branch, so every run here uses the drain's own credential; a
    submitter cannot reach it, which is case 7's subject and not this
    one's.

    **The impostor set.** A commit-shaped object is offered in each of
    the four positions and each must be rejected for its own stated
    reason: an entry-shaped but unsigned commit as **predecessor**; a
    well-formed bundle commit that is not the one the selection rule
    resolves at `base` as **profile**; a commit not on the sequence's
    first-parent chain as **`E`**; and a commit carrying a parent as a
    **candidate**. A positive control comes first — a genuine entry with
    all four roles correctly filled must verify — or the four rejections
    pass against a verifier that rejects everything. The profile
    impostor needs a sequence prefix behind it, not just a ledger: its
    oracle is what the selection rule resolves at that base, and against
    a workroom with no activation the resolved value is the bootstrap
    profile, so the impostor for that run is any well-formed bundle that
    is not it.

    **The signing epoch, which is the part no shape check reaches.**
    Every run here is written against a sequence carrying one rotation,
    because a sequence with none cannot separate the three rules that
    disagree. **Positive, pre-rotation**: an entry whose `base` precedes
    the rotation, signed by the outgoing key, verifies. **Positive,
    post-rotation**: an entry whose `base` is the rotation commit or
    later, signed by the incoming key, verifies — the run that fails
    against a checker consulting only the genesis descriptor's key,
    which is what this note said before this revision and what the
    kernel does today. **Negative, stale key**: an entry whose `base` is
    after the rotation but which verifies only under the outgoing key is
    rejected — the run that fails against a checker accepting any key
    reachable through the rotation chain, which is the natural
    over-correction. **Negative, premature key**: an entry whose `base`
    precedes the rotation but which verifies only under the incoming key
    is rejected — the mirror of the last, and the run an implementation
    resolving `keyat(head)` instead of `keyat(base)` gets wrong.
    **The boundary batch**: the batch whose last admitted event is the
    rotation has its entry signed by the outgoing key, and the next
    entry's `base` equals that batch's `head`, so the next entry is the
    incoming key's; a drain that admits any event after a rotation in
    the same batch must refuse, since that is the shape the boundary
    rule forbids.

    The kernel now has the rotation event, so this block no longer needs
    a synthetic sequence: a run can rotate a scratch workroom for real
    and take its epochs from the kernel's own walk. What it still lacks
    is a ledger to sign an entry into, so every assertion here remains
    about an object no implementation writes. Recording that is the
    point: a case written against machinery that does not exist must be
    visible as such, not counted as coverage.

    **The truncation topology**, which replaces the forged-root run that
    used to live in case 4. With `L` the ledger head and `R` the
    computed root, build `F` with `R` as first parent and `L` as a side
    parent, and assert first that it *is* accepted by a stock receiver
    and that its first-parent walk *does* terminate at `R` — the case
    starts by reproducing the failure, or it will be written against the
    property it is meant to test. Then: the side-parent role check
    rejects `F` because `L` is a ledger entry in a non-predecessor slot;
    the same entry with `L` omitted is rejected by the receiver as
    `non-fast-forward`; and the variant hiding `L` behind a
    candidate-shaped commit is rejected by the parentless-candidate rule
    and by nothing else, which is the run to keep if only one survives.
    A fourth run pins a Git behaviour we depend on rather than a rule we
    wrote: a parentless candidate whose *tree* names `L` as a gitlink
    must still leave the push `non-fast-forward`. Nothing of ours
    rejects that one, so if Git ever traverses gitlinks for reachability
    this is the run that notices.

    **And the residue is asserted as a limit, not as a pass.** Force the
    branch to a short forged ledger with no reference to `L` anywhere.
    An auditor holding only the resulting repository must find no fault,
    because there is none to find; an auditor holding the previous tip
    `L` must reject it by first-parent containment. A case that claims
    the object graph detects this is the false green to avoid, and it is
    the one the previous draft wrote.

    The reader-side defaults are asserted too, since the limit is only
    honest if the reader's tooling is what the note says it is: a stock
    clone's forcing refspec adopts the rewind silently; a refspec without
    the `+` rejects it; and that same non-forcing refspec still accepts
    the truncating fast-forward, which is why containment is a separate
    step and not a fetch setting.

**No case in this list has been executed, because the tier-1 drain does
not exist.** Several of the Git and forge behaviours the cases rest on
were measured in scratch repositories while the note was written, and
each such measurement is marked where it occurs; a measured premise is
not a passed case. What follows classifies why each case cannot run
today, since the reasons are not the same and the difference decides
what has to be built first.

Cases 3, 7 and 10 are the genuinely unverified *mechanisms*; 3 and 7 are
claims about the forge rather than about our code. Tier 1 is not an
offered deployment until 7 and 10 pass.

Cases 4 and 11 are unverifiable for a further reason again, and the
difference matters. The mechanisms cases 3, 7 and 10 test exist and have
not been run. Case 4's profile block and case 11's signing-epoch block
test mechanisms that are only **partly** built: the pieces they resolve
against — the `admission-profile` kind and selection read, the kernel's
rotation event and its epoch walk — have landed since this note was
first written, and the pieces they assert about have not, because both
blocks need the drain and the ledger. They stay specified here as the
contract the remaining implementations must meet. An earlier draft
exempted case 4's bootstrap-profile run as one that works today; it does
not, and its oracle and its resolver being real does not make its
assertions runnable. So neither case may be reported as passing on the
strength of an oracle, a resolver, or a Git behaviour a scratch
repository showed: not case 4's bootstrap-profile run, and not case 11's
parent-role and truncation runs. Case 11's topology assertions
were measured in a scratch repository while this section was written —
the forged fast-forward, the two rejections, the candidate-shaped hiding
place, and the containment check that separates all of them from a
genuine successor — and the case exists to hold the real drain and
auditor to what a scratch repository showed Git actually does.

Two of these cases were rewritten because they could pass without
exercising their subject — case 9 checked ref legality with
`check-ref-format` and never created a ref, and case 2 asserted a run
bound that schedule-only makes trivially true. That is worth recording
as a standing hazard rather than two fixed bugs: a spike case states a
pass condition, and a pass condition can drift into being satisfiable
by something other than the mechanism it names, particularly after a
change elsewhere in the design. Every case here should be re-read
against that question whenever the trigger, transport or naming
scheme changes, and the honest test is always the same one — would
this case fail if its subject were removed?

## Tiers 2 and 3 — requirements to the managed appliance

Containerizing the resident service is nearly free — the sequencer is
stateless but for its key, the nexus is stateless, period. The
container is not the work. The work is everything `serve` currently
declines to be: reachable, multi-tenant, and honest about it. The
bootstrap deferred the capability chain "until the first stranger
arrives." A networked deployment *is* the first stranger; these
requirements are mostly that clause coming due.

**R1 — Networked submit activates the capability chain.** The
loopback-only bind is correct today because there is no network auth
design *in the code*; there is one in the design note. Tier 2
implements it: the nexus issues short-lived capabilities (actor key,
coordinate, expiry, claims), the sequencer verifies them offline
against the issuer key anchored in the profile's config log, and the
services stay coupled through the token, never a runtime call.
Fronting the nexus with the organization's IdP (OIDC) is the borrowed
transport auth; no new trust design is invented at this tier, and
none is permitted to be.

**R2 — One writer per domain, enforced and honest.** Today nothing
stops a second `serve` against the same repository; the durable log
survives (ref CAS), but presence and ephemera silently split into two
rooms. At tier 2 this graduates from footnote to blocker: restarts
and replicas make the accident the default. Requirement: a per-domain
writer lease; a process that does not hold the lease refuses durable
writes and says so; and the ready banner prints only after a
successful bind *and* lease (repairing the known banner-before-bind
defect while we are in that code).

**R3 — Key custody at the deployment boundary.** The sequencer key
comes from the platform's secret store (tier 2) or a KMS (tier 3),
never from a file baked into an image. Rotation is in-band by design —
the kernel's sole reserved event type — and the kernel now implements
it: measured, `Rotate` appends a rotation event signed by the outgoing
key, and every scan carries the key forward from that event onwards.
That closes the first half of this requirement and leaves the second
open. There is no operator surface for it — no command, no runbook, no
tested hand-over — so the old key's retirement and the new key's custody
are not yet one auditable act, and no deployment can rotate without
someone writing that act first. What rests on rotation, the ledger
signing epoch above, is written against a walk that now exists and stays
untested in this workroom, which has never rotated.

The service custodies exactly one kind of key: the sequencer's. Actor
keys stay at the edge — each participant's MCP adapter runs on their
own machine, holding their own keys, pointed at the deployment URL.
An appliance that asks users to upload actor keys has failed this
note.

**R4 — Storage topology is a named choice.** Two supported shapes:
*appliance-primary* (bare repositories on a volume; the appliance
serves reads over smart HTTP with per-repo authorization; optional
push-mirror to a customer forge keeps exit free), and *forge-primary*
(the forge stays the repository of record; the appliance holds a
clone and pushes sequence advances, inheriting the availability
posture above). A deployment declares which one it is; the
documentation stops implying both at once. Either shape schedules
`git gc` — refused-after-objects submissions and CAS losers leave
garbage by design, and "a deployment just has to actually run it"
becomes a shipped default, not a note.

**R5 — Tenancy is repository-per-domain, and domains stay cheap.** A
security domain is a repo with an ACL; minting one is an API call
that completes in seconds, or "repos are cheap" has been broken by
packaging. The nexus index must never join coordinates across domains
it serves — the domain-scoping rule becomes a tested property of the
multi-tenant build, including the metadata surface: who may query a
coordinate learns that something is anchored there; nobody outside
the domain learns even that.

**R6 — Scale by sharding domains, never by replicating a log.** One
log has one writer; that is the point, not the bottleneck. The
appliance scales horizontally by assigning domains to processes
(consistent routing at the ingress by genesis or domain name), one
writer lease per domain (R2), no replica ever sharing a log. Failover
is the kernel's own story — rebuild head and dedup index from the
log, win the CAS, hold the key — and a k8s pod replacement is just
that story run quickly. Interactive latency (p99 submit→notice,
100–200 ms) applies at this tier because the nexus is present; it is
the number the deployment is sized against.

**R7 — Onboarding binds three things, and only at the edges.** A
joining participant: authenticates to the IdP (transport), generates
a keypair locally — a `gs join`-shaped flow that never moves a
private key — and receives an effective roster statement under the
workroom's own governance (record authority; ratification stays with
whoever holds it, not with the platform). The identity-binding
convention is application vocabulary: the roster statement may carry
evidence such as `github=<handle>`, mechanically checkable when the
actor key is also published as that account's signing key. The
kernel learns nothing of any of this.

**R8 — Operability is part of the contract.** Exposed per domain:
sequence head and depth, submit→notice latency, lease holder, gc and
checkpoint recency. Backup is `git clone` **plus `gs attach`** — a
clone alone does not fetch `refs/seq/*` and so is not a backup of the
record; restore is the failover story (R6); disaster recovery is the
key plus any *attached* clone. Every tier states its replication
explicitly — how many independent holders of `refs/seq/<genesis>`,
refreshed how often — because that number, and not the signature
chain, is what the durability claim rests on. Checkpoints are
published on a stated cadence so incremental audit (and later,
witnesses) have something to hold; an unwitnessed checkpoint held only
by the host that wrote it bounds nothing, since the same host can
discard it.

**R9 — "Safe" is a checkable claim, or it is marketing.** The
appliance's pitch is precisely that its customers need not trust it:
any attached clone verifies offline; removing the overlay leaves an
ordinary repository (exit costs nothing); checkpoints published to a
holder other than the host bound what a misbehaving host could
rewrite; witness cosignatures — already kernel vocabulary — are the
roadmap item that closes equivocation. Tier 3 marketing may not claim
more than the verifier checks, and three of those claims are narrower
than they read: verification requires `attach`, the bound requires a
checkpoint the host cannot discard, and equivocation is *detectable*
by a reader who holds two views, not *prevented* — which is precisely
what the witness work is for and why R9 only reserves its seat.

**R10 — One kernel, thin intakes.** The Action drain loop and the
HTTP submit surface stay adapters over the same admission library —
same shape checks, same signature checks, same refusal taxonomy. The
admission profile does not break this and is not allowed to: it is an
*input* to that library, resolved by one rule from one sequence at one
base, so two tiers reading the same log at the same base resolve the
same profile. A divergence between what tier 1 and tier 2 admit is a
kernel bug by definition, and a divergence in what they resolve is the
same bug wearing a profile.

## What this note does not decide

- Payload encryption (confidentiality from the operator) — future
  profile, unchanged.
- Witness software — the cosignature vocabulary ships before any
  witness does; R9 only reserves its seat.
- Forge certification beyond the existing Gitea lane — the compose
  profile is where a chosen forge's behavior gets certified per
  deployment, and this note adds GitHub Actions behavior to the list
  of things certified there, not to the kernel.
- A durable nexus — never, at any tier. Amnesia is a feature the
  ladder preserves.
- Tier-3 build order — the appliance's requirements are stated so
  tier-2 choices cannot foreclose them; nothing here schedules its
  construction.
