---
date: 2026-08-09
status: draft — four review rounds by codex (reports b8c98009,
  d92ae2a5, 0ad30949, f6598244, all ratified). Every design finding
  is resolved and all 15 citations verified; round four required the
  artifact to rest on the code it describes rather than only cite it,
  which this revision does. Awaits final review
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
(`notes/2026-08-05-gitseq-design.md:418`@b4a4454). Nothing in this
note asks the kernel or the workroom fold to learn what GitHub is.

## The refusal: no symmetric sync

The obvious framing is bidirectional sync: mirror the foreign
system into the log, mirror the log back out, reconcile. Reject it.

Reconciliation itself is not dishonest: a corrective act citing the
observations it revises is a perfectly good move, and the log is
built for exactly that. What has no honest implementation is
*automatic* reconciliation. There is no merge in this substrate —
corrections are new events, and mutation is refused outright — so an
engine asked to make two divergent states agree without a
participant deciding must either forge signatures or invent a merge
semantics the substrate does not have.

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

Commands have a crash boundary that renderings do not, and the note
must not skate over it. The side effect can succeed and the
connector can die before reporting. Retrying then duplicates the
comment; leaving the promise open hides an action that already
happened. Neither the work loop nor the kernel's idempotency helps
here, because both protect the *room* side of the boundary and the
damage is on the foreign side.

A command specification must therefore say, per system, which of
these it uses:

1. **A foreign idempotency key**, where the system offers one. Best
   case: the retry is the foreign system's problem.
2. **A correlation marker plus readback**, where it does not. The
   connector writes a marker it can recognise — usually the
   commanding event ID in the posted content — and before retrying,
   searches for its own marker. Recovery is a read, not a guess.
3. **Durable intent before effect**: state the attempt, act, then
   state the outcome, so a reader can see the ambiguous window
   rather than having it silently resolved.

The third is not a recovery mechanism, and the note would be
cheating to present it as one. It makes the ambiguity visible; it
does not tell a restarted connector whether the effect happened. So
it needs a terminal rule:

> A connector may retry a command automatically only where a foreign
> idempotency key or a readback proof establishes what already
> happened. Where neither is available, it must not retry. It
> reports the outcome as unknown and leaves reconciliation to a
> person.

Which means, in practice, that shapes 1 and 2 are the requirement
for any command a room wants handled automatically, and shape 3 is
what honesty looks like for the rest. A connector that silently
retries into an unknown state has traded a visible gap for an
invisible duplicate, which is the wrong direction for this project.

The prohibition on reading back applies to connector-owned
renderings. It does not extend to commands, where readback is the
recovery mechanism.

### Never read back your own writing

A connector that ingests its own renderings will loop. Suppression
by tagging is fragile. Suppression by structure is not: a connector
owns specific surfaces — one comment it edits in place, one field it
sets — and ingests only what it does not own.

## Identity: one connector, principals as data

A connector is a single rostered actor of kind `service`
(`internal/app/app.go:428`@b4a4454), holding one key. Foreign
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
log.

Staleness does not propagate by good intentions. It propagates along
`rests_on`, so the charter only flares its consequences if the acts
enacted under it actually cite it. That is a requirement, not a
hope — but it cannot be a blanket one, because not every act may
choose its own bases.

The rule has to be stated in two parts:

> **Root statements** a connector originates — its observations,
> the requests it files, the artifacts it cites — rest on the live
> charter event, and, where the act depends on them, on the live
> alias claim and the live doorstep admission it relied upon.
>
> **Constrained acts** inherit the basis transitively. A `ratify`
> must rest on exactly its target and nothing else
> (`internal/workroom/fold.go:353`@b4a4454), so it cannot cite the
> charter directly. Its legitimacy comes from the chain it closes:
> a connector may only ratify automatically where the target report
> and its originating request already carry the live charter and the
> relevant admission claims.

With those edges in place, superseding the charter flares the root
statements, and the fold's staleness propagation carries the flare
through the report and ratification chain that rests on them.
Without them the charter is decoration.

The two-part shape is not an inconvenience to route around. It is
the fold refusing to let a ratification quietly acquire bases that
would change what it means, and the connector design has to fit that
rather than wish it away.

### What the charter does not do

The charter is a statement of policy that an honest connector
follows. It is not an enforcement mechanism, and this note would be
lying if it implied otherwise.

The pre-append hook checks only that the submitting key is on the
static allowlist (`internal/app/app.go:734`@b4a4454). The fold
does not read charter bodies and does not know what a charter is. So
an allowlisted connector whose key is stolen can state anything a
connector may state, including requests it was never chartered to
file, and the charter will not stop it. What the charter gives is
detection and attribution after the fact, not prevention.

Whether that should change — an admission rule at the profile
boundary that refuses acts not resting on a live charter — is an
open question below, not something this note settles.

### The charter is what makes automation legitimate

The interesting case is the fully automatic one: a pull request
becomes a request the moment it opens, and an approving review
ratifies. Call it the dark-factory end of the spectrum. It is a
common way to run a process and the design must support it.

It is better founded than it first appears, because of who holds the
requester position. A report is ratified by the requester
(`SKILL.md:51`@b4a4454). If the connector filed the request from
the pull request, the connector *is* the requester, and it is
already the authorized ratifier for reports against that request. No
role grant is needed and nothing is forged; the loop closes with the
rules the workroom already has.

**This is the whole of the automatic design, and the boundary is
deliberate.** A connector may ratify reports against requests it
filed itself. It may not ratify anything else.

What is not automatic is the meaning. An approving review is not
inherently a ratification. It becomes one through a durable,
ratified delegation that says so:

> On repository `o/r`, an approving review by a member of team X
> satisfies requests I filed from pull requests, and
> `github-connector` may enact this.

Ratified in the log, revocable by supersession.

The temptation is to go further and grant the connector the
`ratifier` role so it can ratify assertions and proposals too.
Resist it, because that role does not have the shape the charter
implies. It is granted room-wide through the existing role machinery
(`internal/app/app.go:456` and `:495`@b4a4454) and carries no
repository, team, or event-kind scope. A connector holding it can
ratify anything in the room that a ratifier can ratify, charter or
no charter. Granting it and then describing the result as narrow
charter delegation would be false.

Scoped ratification — authority bounded to a class of acts — is a
mechanism the profile does not have. Naming it as missing is more
useful than pretending the charter supplies it.

The same connector binary then serves the whole spectrum. One room's
charter says a human converts every issue into a request; another's
closes the loop without a person in it. The difference is a ratified
document, not a fork of the code.

### Say the containment cost out loud

A connector that only observes can lie visibly but cannot decide
anything. Automatic mapping gives up part of that, and the amount
given up should be stated rather than discovered.

Stated precisely: the containment that remains is *mechanical* only
where the fold enforces it. The fold enforces that a report is
ratified by its requester, so a connector's reach over reports is
genuinely bounded to requests it filed. Everything else the charter
says — which principals pass the doorstep, which events map to what
— is honoured by an honest connector and by nothing else. A stolen
connector key is bounded by the roles the fold knows about, not by
the charter's prose.

The trade is still favourable, but for a narrower reason than
"delegation is bounded". It is favourable because the delegation is
written down, dated, attributed, revocable, and cited by the acts
made under it, so both compliance and violation are legible after
the fact. The equivalent power in an ordinary webhook automation
exists too — it is simply invisible, held in a configuration screen
nobody audits.

## What is durable and what is not

Forge traffic is two different things and they deserve different
treatment.

**Chatter** — comments, review remarks, "looks good", "please
rebase" — is high volume, frequently edited or deleted, and mostly
worth forgetting. It is ephemeral, exactly as
`SKILL.md:126`@b4a4454 already says. When something in it
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

- `POST /v0/submit` (`internal/service/server.go:172`@b4a4454)
  accepts a fully client-signed submission. The core never holds the
  connector's key. This is the boundary, and it needs no new code.
- `POST /v0/wait` provides the composite cursor the render loop
  follows.
- The pre-append allowlist
  (`internal/app/app.go:734`@b4a4454) admits the connector's
  key; roles constrain what its acts can do.
- Idempotency is already the right shape. The dedup key is
  target, actor fingerprint, namespace, and key
  (`internal/intent/intent.go:154`@b4a4454). A connector sets
  its own namespace and uses the foreign delivery identifier as the
  key, and at-least-once webhook redelivery collapses to a replay
  result. The hardest part of any sync system is load-bearing here
  already.

Two consequences follow.

**A connector keeps no database, on one condition.** Its
correspondence between foreign objects and events is a fold over
durable acts carrying source and external identifiers, and its
inbound deduplication ledger is the log. It is stateless modulo the
log: restartable, auditable, and reconstructible by any reader.

The condition is the command boundary above. Statelessness survives
only where every foreign effect is either idempotent under a key the
connector can recompute, or discoverable by reading back a marker
the connector wrote. Where neither holds, the correlation must
become durable — stated in the log before the effect — rather than
hidden in local state. A connector that needs *private* state to be
correct has a design problem; a connector that needs *durable*
correlation is simply being honest about an ambiguous boundary.

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

The gate is adequate against strangers, which is what it is for. It
is not adequate against a compromised connector, because nothing
below the connector checks it. An act admitted this way should cite
the doorstep claim it relied on, so a reader can see which
admission was asserted and audit it later; whether the profile
should also *refuse* acts that cite no live charter is the open
question below.

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
(`internal/service/server.go:234`@b4a4454). A connector that
wants to publish foreign chatter as frames must therefore hand its
key to `gs`, which destroys the custody property that makes
`/v0/submit` the right boundary.

Since chatter-as-ephemeral is the default inbound path, this is on
the critical path. "Accept a signed envelope" is not a sufficient
specification, because of what the actor signature currently covers.

The actor signs a body containing the nexus generation, the
conversation identifier, the nexus-assigned sequence number, and the
previous frame's hash (`internal/nexus/nexus.go:360`@b4a4454),
and those last two are assigned at publish time
(`:438`@b4a4454). A remote signer cannot know them in advance, and
under concurrent speakers they change between deciding to speak and
being ordered. Some protocol has to close that gap. Two shapes are
available:

1. **Reserve then sign.** The nexus issues a position and previous
   hash, the client signs that exact body, and the reservation
   expires if unused. Preserves the current frame format and its
   verifier; adds a round trip and a reservation to manage. This is
   the only one of the two compatible with the format as it stands
   today.
2. **Sign the payload, let the nexus order it.** The client signs a
   detached body that binds the actor, conversation, and payload but
   not the position; the nexus verifies it against the roster,
   assigns the order, and co-signs. Single round trip; changes what
   the actor signature attests, so both the frame format and
   `VerifyFrame` move with it.

The second is analogous to how durable submission already works,
where the actor signs an intent without a position and the sequencer
commits it.

Both sketches share a gap worth naming, because it is easy to miss.
The conversation identifier is inside the signed body, and it is not
always known to the client: when no conversation is anchored at the
subject yet, the nexus mints one
(`internal/nexus/nexus.go:402`@b4a4454). A remote signer speaking
first into a fresh subject therefore cannot sign the conversation
identity any more than it can sign the sequence. So the reservation
must carry and hold the conversation identity together with the
slot, or the detached protocol needs a separate signed
conversation-create step that the client can then reference.

Whichever is chosen, the design must also say how a session lease
binds the key, how roster membership is checked at publish time, and
how replay and stale-tip races behave.

A second, smaller constraint: state bodies are flat string maps
(`internal/workroom/schema.go:52`@b4a4454). That is enough for
identifiers, and structured foreign payloads belong in attachments
anyway. It should be a stated rule so each connector does not
rediscover it.

## Lineage

This note rests on the layering in
`notes/2026-08-05-gitseq-design.md`, which places connectors above
the kernel and the collaboration profile, and on the forge policy in
`SKILL.md`, which it refines rather than replaces. Code claims cite
`path@commit` so a reader can check them, and the artifact for this
note also **rests on** the artifacts owning that code, because a
citation is not a causal edge. Exact citations let a reader verify
the prose; only `rests_on` makes the prose flare when the code moves
under it. This note argues that staleness must follow causal edges,
so its own provenance has to be built that way — an earlier revision
cited the code without resting on it, and would have gone quietly
wrong if the fold's ratification rule had changed.

Every citation reads at `b4a4454`, the commit that merged the
then-current main into this branch. The citations were re-derived
there, and the cited source is unchanged since; pinning to a commit
the note does not itself sit in keeps it from citing its own
revision. Rounds one and two were reviewed against `0a66e85`, before
the shipping module was promoted out of `spike/` to the repository
root; the line numbers survived that rename unchanged, and only the
path prefix moved. The promotion left no artifact at the new paths,
so this note's bases include a narrow current-root artifact stated
for exactly the files it cites — a provenance anchor, not a claim of
authorship over that code.

## Open questions

1. **Charter form.** Is the charter a `propose` ratified once, or a
   governance kind of its own? A proposal reuses existing machinery;
   a distinct kind makes the delegation legible to a reader who does
   not know to look for it.
2. **Should the charter be enforced, not merely followed?** As
   written it is policy an honest connector obeys. A profile-level
   admission rule could refuse a connector act that does not rest on
   a live charter, which would turn the doorstep from prose into
   mechanism. The cost is a hook that consults durable state, which
   is close to the boundary the kernel deliberately keeps clear of
   correctness. Worth deciding explicitly rather than inheriting.
3. **Scoped ratification.** The `ratifier` role is room-wide.
   Automatic forge approval only ever wants "reports on requests
   this actor filed against repository `o/r`". The requester-owned
   case covers today's design without a role, but any wider
   automation needs bounded authority the profile does not have. Is
   that a missing profile mechanism, or a sign that wider automation
   should not exist?
4. **Reopening.** A pull request closed unmerged is the connector
   superseding its own request, which cleanly releases any promise.
   A reopened pull request cannot unsupersede. A new request resting
   on the retired one is probably right, but it needs deciding.
5. **Backfill.** Importing an existing repository's open issues
   produces a burst of observations at the tail with foreign
   timestamps spanning years. Is that acceptable as-is, or does
   backfill need a distinct treatment so readers do not mistake it
   for a sudden flurry of work?
6. **Aliases and the roster.** Does an alias extend the roster kind,
   or is it a separate ratified assertion? The fold keys actors by
   fingerprint either way.
7. **Which system is second?** GitHub proves the forge shape. Slack
   would prove the chatter-only shape, SARIF the machine-observation
   shape with no conversation at all. The second connector is what
   tells us whether the library boundary is drawn in the right
   place.
