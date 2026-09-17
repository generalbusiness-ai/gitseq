---
title: Decision authority
summary: How a decision becomes adopted, the two paths that confer that authority, and how implementation reaches it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4fdea36917504e1e3df66102c34d6901b7c1c153
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:732cf5a0a54d7443f05318908206b31d2c18800a
---

# Decision authority

Notes, whether feature discussions, position papers or decisions, are
ordinary Markdown files in git, and the workroom records only the relationships around
them. A published revision is an artifact statement at `path@commit`, like any
other. [Keep decision records](../how-to/keep-decision-records.md) runs the
ordinary path end to end.

A decision has two adoption paths, and implementation may rest on either.

## The ordinary path: propose and ratify

The ordinary path ratifies a proposal, never the artifact: nothing satisfies an
artifact. File a `propose` of one or two sentences, "adopt the decision
recorded at `notes/…` at commit `…`", resting on the artifact, and have an
actor holding `ratifier` ratify it.

## The authority-bearing request chain

The narrower path treats the governing request as the authority act itself. It
applies only when all four of these facts are in the durable record:

- the requester held, when they signed the governing request, the same
  authority that would have been required to ratify the adoption proposal;
- the request conditions explicitly commissioned the decision and authorized
  the work that follows it, rather than merely asking for research or a draft;
- the decision artifact was delivered through that request chain,
  independently approved, and merged so the request is satisfied; and
- the request chain and the merged decision artifact are effective, not stale
  and unretired when the follow-on work begins.

The independent reviewer confirms all four from the durable record before
approving work that rests on this chain. Ordinary staleness arising after the
follow-on work begins is recorded at merge under the existing rules; it does
not reopen adoption.

That chain is already the authority act. Do not restate it in a proposal and
ask the same authority holder to ratify it again. If any fact is missing, use
the ordinary path instead: a different required ratifier, an ordinary
participant as requester, a request that commissioned only advice, or a stale
or retired basis. A later change to the decision is a new decision and needs
authority again; a successor artifact does not inherit adoption merely by
occupying the same path.

## Order, because provenance is what the record is for

On the ordinary path, propose and ratify **before** requesting review, and
rest the review request on the ratified proposal as well as the artifact. On
the authority-bearing path, rest the decision's review request on the
governing request chain and the artifact. Either way the verdict rests on the
review request, the merge consumes the verdict, and the receipt and successor
artifact continue the chain. The verdict itself is ratified by the review
requester, and only by them, before the merge, as in any review.

The merge message is the one place the action log reaches readers without keys.
Write it in plain English from the log: who proposed, who ratified, who
reviewed, what was raised and how it was resolved, for a reader who will never
see an event id. The log stays the authority; the message is a render, and a
wrong render corrupts nothing.

## How implementation reaches the decision

Implementation reaches a decision by ordinary provenance, and the merged
artifact alone confers no authority.

Assigned work uses a request resting on the merged decision artifact and on
whichever adoption basis governs it: the ratified proposal, or the satisfied
authority-bearing request chain. The implementing commit rests on that
assigned request. Where the same actor would otherwise be both requester and
performer, no self-request is created: the commit rests directly on the merged
decision artifact and its adoption basis, and the artifact and review request
follow.

For example, Mara holds `ratifier` and files request `R`, whose conditions ask
for a retention decision and explicitly authorize its implementation. The
decision artifact `D` is delivered through `R`, independently approved and
merged, satisfying `R`. If Mara assigns the implementation to Pat, the new
request rests on both `R` and `D`, and Pat's commit rests on that new request.
If Mara implements it herself, she files no request to herself: her commit
rests on `R` and `D`, her artifact cites the same bases, and her review
request cites `R`, `D` and that artifact. Neither route needs a proposal that
merely repeats `R`.

If any governing basis becomes stale before implementation starts, the author
of the assigned request refiles it on current bases, and a self-initiating
actor obtains current adoption before committing.

## Revising versus replacing

Revising and replacing are different facts, and one sentence separates them:
amend in place while it is the same decision; when the decision changes, write
a new file and stamp the old one.

A revision edits the file at the same path, and the artifact chain at that
path is the published-revision history, so keep the revision narrative out of
the front matter: the chain already tells it. A replacement is a new file
whose front matter names its predecessor by path, plus a one-line stamp in the
old file saying what superseded it, in one commit, so a git reader with no keys
sees both directions. The old artifact's retirement stays merge-sealed like any
other retirement; front matter is branch-controlled input and retires nothing
by itself.

## See also

- [Keep decision records](../how-to/keep-decision-records.md)
- [`gs review`](../reference/gs/review.md) — how a verdict binds to an
  assigned, self-initiated or evidence-only implementation.
- [Agent practice](agent-practice.md), [The work loop](work-loop.md)
