---
date: 2026-08-09
status: draft — not yet reviewed or adopted
origin: a design conversation with hugh on 2026-08-09, held outside
  the workroom and summarized here. No signed frames exist to carry
  as evidence; this note is the author's faithful summary and stands
  on its own reasoning, not on borrowed authority.
---

# Connectors

Work does not begin in the workroom. It begins in a GitHub issue, a
Slack thread, a Jira ticket, a SARIF report from a scanner. A
connector is what lets a workroom exchange work with one of those
systems while keeping the two properties that make the record worth
having: every durable act is attributable to a key, and nothing in
the log claims more authority than someone actually granted.

This note describes the shape. GitHub issues and pull requests are
the first instance; Slack, Discord, Jira, Salesforce, Microsoft
Sentinel and SARIF-based tools are the same shape with different
vocabularies. The note does not specify a wire format or a
configuration schema — those follow once the shape is agreed.

Connectors live outside the core. The design note already places
them there: "approval/workflow vocabularies, artifact conventions,
promotion rituals, executors and connectors: all here or higher"
(`notes/2026-08-05-gitseq-design.md:418`@2aa63e78). Nothing in this
note asks the kernel or the workroom fold to learn what GitHub is.

## The refusal: no symmetric sync

The obvious framing is bidirectional sync: mirror the foreign
system into the log, mirror the log back out, reconcile. Reject it.

Symmetric sync requires conflict resolution, and conflict
resolution over an append-only signed log has no honest
implementation. There is no merge in this substrate — corrections
are new events, and mutation is refused outright. A sync engine that
wants to reconcile two divergent states must either forge
signatures or invent a merge semantics the substrate does not have.

What the framing is actually reaching for is traceability in both
directions and status flowing both ways. Two asymmetric operations
deliver that, and they never need to reconcile with each other:

- **Observe** — foreign to room. Append-only, at-least-once,
  idempotent. An observation is never a merge; it is a new event.
- **Render** — room to foreign. A projection onto a mutable surface
  the connector owns, stamped with the frontier it rendered.

Inbound only appends. Outbound only overwrites a surface that
nothing reads back. There is no conflict, so there is nothing to
resolve.

The rendering direction is not a consolation prize. It is the
project's central claim applied to somebody else's user interface: a
pull request carrying one comment that reads *as of #4312 — STALE:
the artifact this rested on was superseded by #4400* is the document
that knows when it is wrong, sitting where people already look.

### Commands, the third thing

Renderings are continuous and idempotent, and deserve no ceremony.
Discrete side effects — close this issue, post this text, set this
field — are different, and they should go through the work loop like
any other work. The connector is requested, promises, acts on the
foreign system, and reports with the resulting object as evidence.

Delivery failure then appears in the room as an unkept promise
rather than as a silent error in a log nobody reads. The connector's
own reliability becomes visible in the same terms as everyone
else's, which is the point of having the loop at all.

### Never read back your own writing

A connector that ingests its own renderings will loop. Suppression
by tagging is fragile. Suppression by structure is not: a connector
owns specific surfaces — one comment it edits in place, one field it
sets — and ingests only what it does not own.

## Identity: one connector, principals as data

A connector is a single rostered actor of kind `service`
(`spike/internal/app/app.go:427`@0a66e85), holding one key. Foreign
principals are carried as data in the act body, never as separate
identities.

The alternative — one gitseq identity per GitHub account — is worse
than unmanageable. It is dishonest. Minting a key per foreign user
means either the connector holds everyone's keys, so the signature
reads `alice` when the connector signed, or real key distribution to
people who never asked for one. The first is attribution theatre and
is strictly worse than the connector speaking plainly in its own
voice. The second does not survive contact with a hundred-person
repository.

So the connector attests: *I observed that foo@bar filed this.* The
trust chain is legible and short. The room trusts the connector's
fidelity; the connector trusts the foreign system's authentication
of its own users. A reader who distrusts either can see exactly
which link to doubt, because the signature names the connector and
the body names the principal.

This costs something real. Every act from the connector carries the
same fingerprint, so a projection that renders actors alone will
show one identity requesting everything and waiting on itself. The
fix belongs in the projection, not in the identity: where an act
carries `on_behalf_of`, the rendering shows *github-connector for
foo@bar*. The signature stays with the key; the display shows the
principal. This is a change to the status projection and the UI, not
to the fold's notion of who acted.

### Aliases

A person who is already a room actor may also have a foreign
account. Binding the two — `github:alice` to alice's fingerprint —
is useful: it lets projections attribute observed acts to a known
member, and it lets a charter treat those observations differently.

The binding is a durable roster claim and must be ratified like any
other authority-adjacent fact. Two limits hold regardless:

1. An alias never lets the connector sign as alice. The signature is
   always the connector's. If an act must be alice's own, alice acts
   in the room.
2. An alias is a credential-equivalence claim, and it inherits the
   security of the foreign account. See the hazards below.

## The charter

A connector's mapping — which foreign events mean what, and what
the connector may do about them — is room policy, not a connector
feature. It belongs in the log, stated durably and ratified before
the connector acts on it.

The charter declares:

- which foreign system and scope the connector watches, and which
  room genesis it binds to;
- its actor key;
- the mapping from foreign events to acts, including which are
  durable;
- the doorstep: whose foreign actions may cause durable acts at all;
- any delegated authority, stated explicitly.

This follows the substrate's own rule that the rules are governed by
the moves they govern: fold definitions live in the practice's own
log. It has a consequence worth wanting. Change the mapping by
superseding the charter, and everything projected under the old
mapping flares stale. Revocation is not a config change nobody
notices; it is a visible event with visible downstream effects.

### The charter is what makes automation legitimate

The interesting case is the fully automatic one: a pull request
becomes a request the moment it opens, and an approving review
ratifies. Call it the dark-factory end of the spectrum. It is a
common way to run a process and the design must support it.

It is better founded than it first appears, because of who holds the
requester position. A report is ratified by the requester
(`SKILL.md:51`@61153471). If the connector filed the request from
the pull request, the connector *is* the requester, and it is
already the authorized ratifier for reports against that request. No
new authority is needed and nothing is forged; the loop closes with
the rules the workroom already has.

What is not automatic is the meaning. An approving review is not
inherently a ratification. It becomes one through a durable,
ratified delegation that says so:

> On repository `o/r`, an approving review by a member of team X
> satisfies requests I filed from pull requests, and
> `github-connector` may enact this.

Ratified in the log, revocable by supersession. For assertions and
proposals rather than reports, the connector additionally needs the
`ratifier` role, granted and revoked through the existing role
machinery (`spike/internal/app/app.go:456` and `:495`@0a66e85).

The same connector binary then serves the whole spectrum. One room's
charter says a human converts every issue into a request; another's
closes the loop without a person in it. The difference is a ratified
document, not a fork of the code.

### Say the containment cost out loud

A connector that may only observe can lie visibly but cannot decide
anything. That containment weakens exactly as far as the charter
delegates, and it should be stated rather than discovered.

The trade is still favourable. The delegation is written down,
dated, attributed, and revocable, and every act enacted under it is
traceable to it. The equivalent power in an ordinary webhook
automation exists too — it is simply invisible, held in a
configuration screen nobody audits.

## What is durable and what is not

Forge traffic is two different things and they deserve different
treatment.

**Chatter** — comments, review remarks, "looks good", "please
rebase" — is high volume, frequently edited or deleted, and mostly
worth forgetting. It is ephemeral, exactly as
`SKILL.md:126`@61153471 already says. When something in it
crystallizes, a room actor promotes it with the relevant quotes as
evidence.

**Transitions** — pull request opened, approved, merged, issue
closed — are acts in the forge's own speech-act vocabulary. They are
low volume, structurally load-bearing, and they are precisely what
the room would have stated durably had the work happened in-room.
They are durable.

`SKILL.md:126` currently says to treat forge threads as ephemeral
without drawing this line. Adopting this note implies amending it.
That amendment is follow-up work, not part of this note.

A default GitHub mapping, which a charter may override:

| Foreign event | Treatment |
|---|---|
| Issue or review comment | Ephemeral frame |
| Issue opened | Ephemeral frame; a room actor states the request, unless the charter automates it |
| Pull request opened | Durable request when the charter says so, otherwise ephemeral |
| Pull request merged at commit C | Durable, citing C — the traceability anchor |
| Review approved | Ratification only under charter delegation; otherwise ephemeral |
| Labels, milestones, assignment | Rendered outbound only |

The volume argument that justifies keeping chatter out does not
reach transitions. A busy repository produces tens of pull requests
a day and thousands of comments.

## Identifiers and traceability

Generalize the rule the skill already states for pull requests: cite
the immutable thing, hint the mutable locator. A pull request is
cited by its head commit hash with the URL as a hint. A SARIF
finding is cited by its fingerprint and the tool run by its digest.
A Slack message is cited by channel and timestamp. A Jira issue key
is stable even though its content is not.

One rule is absolute: `rests_on` contains gitseq events and nothing
else. A durable act never rests on a foreign identifier, because a
basis must be immutable and signed, and foreign identifiers are
neither. Foreign references live in the body and in evidence.

Outbound, the canonical event ID is long but the surfaces tolerate
it: put the full `git:<fmt>:<genesis>#git:<fmt>:<commit>` in a
machine-readable footer, and a short form in human prose.

A connector must never present admission order as foreign
chronology. Webhooks arrive out of order, and importing an existing
issue's history lands as one burst at the tail. Carry the foreign
timestamp as data; the sequence position means what it always means,
which is when the room heard about it.

## Where it plugs in

Pluggable here does not mean a plugin interface in the core process.
It means a separate process, holding its own key, talking to the
same public surface every other actor uses. The seams already exist:

- `POST /v0/submit` (`spike/internal/service/server.go:157`@0a66e85)
  accepts a fully client-signed submission. The core never holds the
  connector's key. This is the boundary, and it needs no new code.
- `POST /v0/wait` provides the composite cursor the render loop
  follows.
- The pre-append allowlist
  (`spike/internal/app/app.go:734`@0a66e85) admits the connector's
  key; roles constrain what its acts can do.
- Idempotency is already the right shape. The dedup key is
  target, actor fingerprint, namespace, and key
  (`spike/internal/intent/intent.go:154`@0a66e85). A connector sets
  its own namespace and uses the foreign delivery identifier as the
  key, and at-least-once webhook redelivery collapses to a replay
  result. The hardest part of any sync system is load-bearing here
  already.

Two consequences follow.

**A connector keeps no database.** Its correspondence between
foreign objects and events is a fold over durable acts carrying
source and external identifiers, and its deduplication ledger is the
log. It is stateless modulo the log: restartable, auditable, and
reconstructible by any reader. A connector that needs private state
to be correct has a design problem.

**Shared code is a library, not a framework.** Cursor handling,
idempotency key derivation, the correspondence fold, frontier
stamping, and surface ownership are worth writing once. None of it
belongs in the core process.

The core listens on loopback only, so the connector is the sole
internet-facing component. Webhook signature verification, replay
windows, and delivery deduplication are its responsibility and its
alone.

## Hazards

**The doorstep.** On a public repository anyone may open a pull
request. If pull-request-opened mints a durable request, then anyone
on the internet can write to the durable log. This is the real
attack surface of automatic mapping, more than anything outbound.
The charter must gate durable effects by principal — collaborators,
organization members, aliased actors — and the connector must
enforce that gate before submitting, not after.

**Alias escalation.** If the charter lets alias-backed observations
trigger ratification, then compromising alice's GitHub account is
escalation into the room. Scope deliberately: an alias may be enough
to attribute an observation while not being enough to close a
commitment.

**Untrusted content reaching agents.** Foreign text is attacker-
influenced. It is quoted material, never instruction, and agents
reading it must treat it as data. This deserves a line in
`SKILL.md`.

**Evidence carries secrets.** Attachments are permanent and travel
with every clone. A raw webhook payload from a private system may
carry addresses, private repository content, or tokens. The
repository-per-security-domain rule applies unchanged: do not attach
material whose read policy differs from the room's.

**Deletion.** Someone deletes a comment for a reason, and gitseq
cannot unsay. An edit or deletion is handled by superseding the
observation with a new one that records what happened. That is
correct — the record retains that a claim was made and withdrawn,
which is tamper-evidence against foreign-side rewriting — and it is
also why chatter stays ephemeral by default. Keeping the high-volume
personal traffic out of the durable log is what keeps this from
becoming a liability.

## What the core still owes

One gap blocks the ephemeral half of the design. Durable acts can be
signed by the client and submitted over `/v0/submit`, but ephemeral
frames cannot: `handleSay` resolves the session's actor and reads
its private key from server-side custody
(`spike/internal/service/server.go:229`@0a66e85). A connector that
wants to publish foreign chatter as frames must therefore hand its
key to `gs`, which destroys the custody property that makes
`/v0/submit` the right boundary.

Since chatter-as-ephemeral is the default inbound path, this is on
the critical path. It needs a frame submission that accepts a signed
envelope and verifies it against the roster — consistent with the
collaboration profile's rule that everything the nexus carries is
enveloped and signed.

A second, smaller constraint: state bodies are flat string maps
(`spike/internal/workroom/schema.go:52`@0a66e85). That is enough for
identifiers, and structured foreign payloads belong in attachments
anyway. It should be a stated rule so each connector does not
rediscover it.

## Lineage

This note rests on the layering in
`notes/2026-08-05-gitseq-design.md`, which places connectors above
the kernel and the collaboration profile, and on the forge policy in
`SKILL.md`, which it refines rather than replaces. Code claims cite
`path@commit` directly rather than resting on directory-level
artifacts, several of which currently have more than one live head
for the same path.

## Open questions

1. **Charter form.** Is the charter a `propose` ratified once, or a
   governance kind of its own? A proposal reuses existing machinery;
   a distinct kind makes the delegation legible to a reader who does
   not know to look for it.
2. **Reopening.** A pull request closed unmerged is the connector
   superseding its own request, which cleanly releases any promise.
   A reopened pull request cannot unsupersede. A new request resting
   on the retired one is probably right, but it needs deciding.
3. **Backfill.** Importing an existing repository's open issues
   produces a burst of observations at the tail with foreign
   timestamps spanning years. Is that acceptable as-is, or does
   backfill need a distinct treatment so readers do not mistake it
   for a sudden flurry of work?
4. **Aliases and the roster.** Does an alias extend the roster kind,
   or is it a separate ratified assertion? The fold keys actors by
   fingerprint either way.
5. **Which system is second?** GitHub proves the forge shape. Slack
   would prove the chatter-only shape, SARIF the machine-observation
   shape with no conversation at all. The second connector is what
   tells us whether the library boundary is drawn in the right
   place.
