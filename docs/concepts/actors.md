---
title: Actors and authority
summary: Who may do what in a workroom, why kind is not authority, and how a grant stops conferring.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:b9b714309ab6aa17154b96083c9d7fc054a9218d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cb605f5622c1aa47d1b98dddaaba4f9fb164a343
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cae4cb65017feffac75c4cba88dccda021a640de
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# Actors and authority

## Principals

An **actor** is a principal with a signing key. Everything that actor
does is signed with it, so authorship is not a claim anyone has to
believe.

Each actor has a **kind** — `human`, `agent`, or `service`. Kind
describes what a principal is. It confers nothing. An agent holding a
`ratifier` grant may ratify; a human without one may not. Reading kind as
an authority test is the common mistake, and it is wrong in the direction
that matters: it invites you to trust an act because a person made it.

## Roles

Authority comes from **roles**, granted durably and revocably:

- `participant` — membership itself, the thing every other role rests on.
- `operator` — the founding authority; it carries `ratifier` with it.
- `ratifier` — may confer force on statements and proposals.
- other named roles, as a room's practice requires.

`gs actors` prints the roles a principal holds **right now**. That is a
different question from whether some past act was effective, and the two
are answered by different parts of the projection.

## How a grant confers, and stops

A grant is a `roster` statement. Whether it currently confers depends on
what kind of grant it is.

A **membership grant** is live when the grant statement is live and at
least one effective ratification of it is live. It does not rest on a
membership, because it *is* the membership.

A **non-membership grant** — `ratifier`, or any named role — needs those
two things *and* the membership it named as its **first** basis to still
be live. The fold looks at the first basis and nowhere else. A grant that
lists a dangling basis first and the membership second is judged
effective and confers nothing.

The **genesis seed** is the one exception to the ratification
requirement, because there is no earlier ratifier to give one. It is also
non-retirable: otherwise one supersession could remove the authority from
which every later governance act descends.

An authority grant cannot be authored or ratified by its beneficiary.
Another actor with the required live authority must write and ratify it.
This keeps a principal from preparing a hidden spare grant for itself.
Ratifying an `operator` grant specifically requires a current `operator`;
a plain `ratifier` cannot mint an operator for someone else.

Two consequences worth holding on to:

- The ratification condition is a **disjunction**. One grant may be
  ratified several times, and any surviving ratification keeps the role.
  Retiring one of two changes nothing.
- Revoking a grant retires the role it named **and everything derived
  from it**. Revoking `operator` takes a principal from
  `[operator, participant, ratifier]` to `[participant]`, because
  `ratifier` was riding on `operator` and had no grant of its own.

Retiring the membership removes membership and, with it, every
non-membership role that named it. One supersede, and the principal is
no longer a participant — but it stays listed. A retired principal
is left on the roster with `retired: true` and no roles, because the
events it signed are permanent and a reader has to be able to tell a
retired principal from a live one. Measured: superseding a membership
takes a principal from `[participant]` to retired with no roles, and
superseding *that supersession* returns them to `[participant]`.

Membership also bounds durable authorship. The genesis seed is the only
state whose author need not already be live. Every later state from a
non-member is ineffective, even when the repository still holds that
principal's signing key: custody of a key is evidence of who signed, not a
standing permission to keep speaking. Removal does not rewrite verdicts
earned while the principal was admitted, and restoring membership gives
force only to later states.

A retired principal cannot ratify a report, including one on a request they
made while admitted. They may still supersede an earlier act they authored.
That is deliberately narrow cleanup: it lets someone withdraw their own
record, but neither author a new effective state, ratify a report, nor
supersede another actor's act.

A merge receipt is the one thing that otherwise reaches across authorship: it
lets the actor who merged an approved head retire the predecessors that head
republished. That reach ends at departure too. A receipt is authority the
merge exercised, not a capability the signer keeps, so a principal who is no
longer live cannot retire another author's artifact by citing one — however
complete the receipt, the plan and the successor still are.

Governance retirement and restoration use the authority of the target,
not ordinary authorship of an old event. A `ratifier` may change ordinary
membership and ratifier grants. An `operator` grant, or a membership that
carries a live or dormant operator grant, requires a current `operator`.
The same check runs again when a supersession is superseded, so someone
cannot revive authority they no longer hold.

## Effectiveness and authority are different questions

For every act other than a grant, effectiveness is settled once, when the
act is appended, and stays settled. A grant can satisfy every rule, be
judged effective, be ratified — and confer nothing right now, because a
basis it depends on has since been retired.

So for grants, do not read the verdict as the answer. `gs actors` answers
the current question, and answers it only for the moment you ask.

The JSON projection keeps the evidence too. `role_sources` lists live
grant statements, `dormant_role_sources` lists directly live grants whose
membership basis is inactive, and `retired_role_sources` lists retired
grants. Use `gs status --json` when auditing whether a revocation left a
grant that could confer again after a later governance change.

## Custody

An actor's private key lives under `.git/gitseq/actors/`. The resident
service can open every actor key in the repository it serves, so running it
at all is what draws the trusted-process boundary. Inside that boundary it
mints a private random credential for one repository-and-actor lease and signs
on that actor's behalf when the credential is used. The credential is not
authentication against a malicious process running as the same OS account;
that process may read the key or invoke local `gs` directly. That is why
serving is loopback-only, and why starting the service is itself the decision
to accept the boundary — see
[Deploy a resident](../how-to/deploy-a-resident.md).

## See also

- [`gs actors`](../reference/gs/actors.md),
  [`gs role-grant`](../reference/gs/role-grant.md),
  [`gs role-revoke`](../reference/gs/role-revoke.md)
- [The work loop](work-loop.md)
