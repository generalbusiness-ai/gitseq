---
date: 2026-09-04
status: audit. Records what the shipped web UI does and does not do for
  docs/how-to/keep-decision-records.md, as of refs/heads/main at
  e8b33ca5923ebed108aa86f816763a46e893fee5, and states the one gap this
  delivery closes.
origin: request 5d262274...3d1ff642, "revive and deliver the decision-records
  web UI loop from #10894 and #11575 to refs/heads/main". Heads
  b60f3a406590c823bdac2ef51c70a6e21a467232 and
  3fd75543f80c42b400fdd446e1c5b4524e39e200 are evidence only.
---

# The decision-records loop in the browser: what is there, what is not

## Why this note exists

Two earlier heads built a web UI for the decision-records loop. The second of
them, `3fd75543`, ended in a corrected changes-requested verdict on two
defects, not in approval. Neither head was merged.

Since then the same work landed on `main` piecemeal, through later requests.
This note walks every step of `docs/how-to/keep-decision-records.md` against
the committed UI at `e8b33ca5` and says, for each step, whether the browser can
do it, whether it is outside the browser on purpose, or whether it is missing.

The short answer: both reviewed defects are repaired on `main`, every step of
the page except one is either present or deliberately outside the browser, and
one step is genuinely incomplete. That step is 5, revising an adopted decision.
This delivery closes it.

## How to read the evidence

Line numbers marked `main` are at `e8b33ca5`. Line numbers marked `head` are at
the head this delivery produces, and appear only where this delivery changed a
file. Every "present" row names a test that asserts the behaviour at the
altitude a person experiences it: a mounted component and its rendered output,
or the bytes that reach `/v0/act`.

## The seven steps

### Setup: join the workroom and choose an identity

**Present.** `ui/src/App.tsx:304` renders `JoinGate` until an actor is chosen,
and `ui/src/lib/session.ts` holds the choice and the presence lease. Creating a
workroom (`gs init`) and adding actors (`gs actor-add`) stay outside the
browser: they are custody operations on keys, and the page has no key store of
its own.

Test: `ui/test/publish-chain.test.mjs` answers the gate by storing the actor
before mounting, then drives the real page against a real resident, so the
session path is exercised rather than stubbed.

### 1. Write the decision on a branch, then publish an artifact

**Writing the file and committing it is outside the browser.** The page has no
checkout. The how-to says so of the commit hash as well: "Today you copy the
commit hash yourself".

**Publishing the artifact is present.** `ui/src/components/TopBar.tsx` offers
the control, `ui/src/components/Publish.tsx` is the dialog, and
`ui/src/App.tsx:205` is the boundary that signs. The record it writes is
`act: state, kind: artifact` with `body.path` and `body.commit`
(`ui/src/App.tsx:222`).

Test: `ui/test/publish-chain.test.mjs` builds `gs`, runs a real resident,
mounts the real `App`, submits the dialog over HTTP, and reads the record back
out of the fold's own projection, including the fold's verdict on it.

### 2. Propose adoption, and ratify the proposal

**Present, both halves.**

The proposal: `ui/src/components/Toolbar.tsx:165` offers "propose adoption" on
an artifact row, prefilled with the artifact as its only citation and with the
path in its text. It is offered without any adoption test, which is deliberate
and documented at `docs/reference/architecture.md:1552` (main): the fold
projects no adoption relation, so the browser may not gate on one.

The ratification: `ui/src/components/Toolbar.tsx:142` offers "agree" on a
proposal row, gated by `mayRatify`
(`ui/src/lib/authority.ts:200`), and submits `act: ratify` through
`ui/src/App.tsx:138`, which asks `signingRefusal`
(`ui/src/lib/authority.ts:293`) again at the moment of signing.

The page's rule that an artifact cannot be ratified is preserved by
construction: no affordance ever names an artifact as a ratification target.

Tests: `ui/test/before-signing.test.mjs:154` for who is offered the
ratification, on rendered output; `ui/test/signing-standing.test.mjs:399` for
what reaches `/v0/act`; `ui/test/decision-records.test.mjs:154` for the row
offering both acts and gating neither.

### 3. Ask for a review, promise it, sign a verdict, ratify the verdict

**Request: present.** `ui/src/components/Toolbar.tsx:173` offers "request
review" on an artifact row. It carries `body.artifact` and `body.head` copied
from the artifact's own `body.commit` (`Toolbar.tsx:170-171`), so neither is
retyped. The composer supplies the two fields a request cannot be filed
without: `body.to` from a roster select and `body.conditions` from a text field
(`ui/src/components/Thread.tsx:577-578`, `:690`, `:704` on main).

**Promise: present.** `ui/src/components/Toolbar.tsx:137` offers "accept" on a
request addressed to the viewer, routing to a `promise` resting on the request.

**Verdict: outside the browser by design.** `gs review` signs only from a clean
checkout sitting on the artifact's exact commit. The browser has no checkout,
so it cannot make the statement the verdict is. This is the correct boundary,
not a gap.

**Ratifying the verdict: present.** `ui/src/components/Toolbar.tsx:185` offers
"accept" on a reported commitment, and `mayRatify` is asked with the
commitment's requester as the `originating-requester` candidate. The how-to's
rule that only the review requester may ratify a verdict is therefore enforced
from the fold's own published satisfier rather than restated here.

Test: `ui/test/before-signing.test.mjs:237` mounts the pane and proves both
directions of the `originating-requester` rule, including a departed requester.

### 4. Merge

**Outside the browser by design.** `gs merge` lands a commit, retires
predecessor artifacts and publishes successors, from a checkout. The browser
has none.

### 5. Revise the decision

**Publishing the revision artifact: present**, by the same path as step 1.

**Requesting the review: incomplete on main.** This is the gap.

The how-to's revision request rests on two records: the new artifact, and the
proposal that already adopted the decision. That proposal rests on the
*earlier* artifact at the same path, so no projected citation edge reaches it
from the revision. `index.citableProposals`
(`ui/src/lib/records.ts:96`, main) walks direct provenance only, and
`ui/test/decision-records.test.mjs:124` holds it to that on purpose: reaching
across a shared path would be the browser asserting that a decision at a path
stays adopted, which is the one relation
`docs/reference/architecture.md:1537` (main) forbids it to invent.

The prefill was therefore correct and the composer had no other way to name a
citation. `ui/src/components/Thread.tsx:546` (main) reads `bases` from the
route and from nowhere else. A revision review request filed from the browser
would rest on the artifact alone, and the chain from the merge back to the
adoption would be broken.

**Closed by this delivery.** The operator names the record; the browser does
not derive it. See "What this delivery adds" below.

### 6. Replace the decision with a new one

**Present.**

Both artifacts can be published, and the stamped predecessor can rest on the
replacement. `ui/src/components/Publish.tsx:117-121` renders a checkbox
offering the record on screen as a basis, and `ui/src/App.tsx:228` turns it
into `rests_on`. `ui/src/App.tsx:244` supplies that record: publishing the
replacement first leaves the page on its thread (`App.tsx:198-203` waits for
the projection to carry the record before opening it), which is where the
offered basis comes from.

The fresh adoption is step 2 again, on the replacement artifact.

Before this delivery nothing proved the checkbox reached `rests_on`. The
dialog's focus contract was tested against a real fold; the basis was not. This
delivery extends `ui/test/publish-chain.test.mjs` to publish both artifacts of
a replacement and read the edge back out of the fold's provenance.

### 7. Read the record and follow the chain

**Present.** `ui/src/components/RecordDetail.tsx` shows every field the
projection holds for a record, including its path, its commit, the fold's
verdict and reason, and its flags. Lines 115 and 120 render "rests on" and
"rested on by" as bounded lists of buttons, each of which opens the thread of
the record it names, so a reader walks the chain in both directions from any
record.

`git log` remains what a colleague without gitseq reads, and that is unchanged
by anything here.

## The two confirmed defects, and where they are repaired

### A. Authority read from the live vocabulary

The fold captures a kind's definition once, when the statement is admitted
(`internal/workroom/fold.go:702`), and decides a ratification from the
satisfier on that captured definition (`internal/workroom/fold.go:1033`). The
reviewed head instead looked the kind up in the current vocabulary, which
disagrees with the fold in both directions as soon as a kind is redefined.

Repaired on main. The fold now publishes the captured value per statement:
`satisfierOf` at `internal/workroom/fold.go:2835`, projected at
`internal/workroom/fold.go:2930`, typed into the browser's projection at
`ui/src/lib/api.ts:33`. `mayRatify` reads that field and nothing else
(`ui/src/lib/authority.ts:217`), and refuses when it is absent. The rule is
stated at `docs/reference/architecture.md:966`, the page that had previously
stated the defective rule.

Pinned by: `ui/test/before-signing.test.mjs:154`. It is an evolved-vocabulary
fixture in the strict sense: the admitted satisfier and the live satisfier are
separate knobs, and the test drives all four combinations, including the two
where they differ. It asserts on the rendered control, not on a helper's return
value.

### B. Citations signed but never shown

The reviewed head passed the row's resolved citations into `rests_on` and
rendered none of them. The operator signed causal references they were never
shown. Its tests asserted the array a spy caught.

Repaired on main. `ui/src/components/Thread.tsx:668` (main) renders every
element of `bases` as a list with a ticket, a kind, the record's first line and
its whole identifier, above the send control, with two honest labels because a
withdrawal names a target rather than a basis.

Pinned by: `ui/test/before-signing.test.mjs:265`. It mounts the composer, reads
`[data-citations]` in the composer's own region rather than document-wide, and
checks that the send control is already usable at the moment the disclosure is
readable. It covers the two-citation case, where hiding the surplus would be
the same defect with a smaller number.

## The four recheck items

### Architecture ownership

Three layers are involved, per `docs/reference/architecture.md`.

- Layer 5, the workroom fold and its projection, owns admitted-time authority.
  It publishes the captured satisfier; the browser does not reconstruct it.
- Layer 6, the service, owns the projection surface and the `/v0/act` write
  boundary. Nothing in this lane widens what the browser may write: every
  durable act it files is `state`, `ratify` or `supersede` through the existing
  route.
- Layer 7, the browser, owns presentation and affordance selection, bounded by
  "what the browser may derive" at `docs/reference/architecture.md:1527`
  (main).

The head this delivery produces preserves the layer 5 and layer 6 contracts
exactly: no Go file changes. It extends one layer 7 contract, the citation
rule, and updates `docs/reference/architecture.md` in the same head to state
it.

### Deterministic bounded proposal selection

`index.citableProposals` (`ui/src/lib/records.ts:96`, main) filters candidates
by projected fields only: the citation edge, the kind, the fold's `ratified`
and `retired` flags, and the fold's verdict. It orders by the fold's sequence,
descending, with the event identifier as tie-break, so the result never depends
on the order the projection happened to serialise. It is bounded by
`CITED_PROPOSAL_LIMIT`, which is one, because only one proposal can be the
current one and citing the rest creates the ambiguity the bound exists to
remove.

Evidence: `ui/test/decision-records.test.mjs:59` shuffles the serialisation and
gets the same answer; `:83` proves the bound holds at 500 candidates and keeps
the newest; `:99` proves each projected filter; `:124` proves the shared-path
case is excluded.

### Actual-authority affordances

Every affordance that submits or opens a durable act is gated on a fact the
fold published.

- The three that submit `ratify` directly ("ratify yes", "agree", the "accept"
  on a report) go through `mayRatify`, which reads `statement.satisfier` and
  the projected roster. Not the live vocabulary.
- The seven that open the composer for a `state` record are gated on
  `isLiveParticipant` (`ui/src/lib/authority.ts:67`), which is the fold's
  `hasActor` question, the participant role rather than a roster entry.
- "withdraw" is gated on authorship and excluded for roster governance, which
  is the fold's own order in `decideSupersede`.
- Publishing is gated on `publishRefusal` (`ui/src/lib/authority.ts:162`) at
  the control, at the dialog's submit, and again at the signing boundary.

Every one of these is a courtesy; `signingRefusal` and `publishRefusal` at the
signing boundaries are the guarantee, because a composer outlives the authority
that opened it.

Evidence: `ui/test/signing-boundary.test.mjs` for the rule,
`ui/test/signing-standing.test.mjs` and `ui/test/publish-authority.test.mjs`
for whether the code that signs actually asks it, each with a positive control
so a zero-call assertion cannot pass vacuously.

### Dialog focus, Escape and restoration

`ui/src/lib/modalFocus.ts` implements the four behaviours `aria-modal="true"`
promises: initial focus on the first focusable control, Tab and Shift+Tab
contained, Escape dismissing when the caller has somewhere to dismiss to, and
focus restored to the opener on close. The control ring is read at each key
press rather than captured at open, so a control that becomes enabled joins it.
`ui/src/components/Publish.tsx:60` uses it.

Evidence: `ui/test/publish-chain.test.mjs` asserts all four against a live
jsdom document, naming both endpoints of the ring rather than computing them,
and covers the case where enabling submit moves the wrap target.

One honest exception: `JoinGate` in `ui/src/App.tsx:310` declares
`aria-modal="true"` and does not use `useModalFocus`. It focuses its first
identity and nothing else. Escape would have nowhere to dismiss to, since the
page cannot be used without an identity, but containment and restoration are
unenforced. This is out of scope for this request, which is about the decision
records loop, and it is recorded here rather than left unsaid.

## What this delivery adds

One facility, and the tests and the contract sentence for it.

**A citation the operator names.** `ui/src/components/Thread.tsx:773-800`
(head) adds a field to the composer where the operator names one more record to
cite, by the ticket number the screen already shows or by the whole event
identifier a record's detail offers to copy. The browser resolves that name
against the projection and no further
(`ui/src/components/Thread.tsx:573`, head): a name the projection does not
carry is refused where the operator can still fix it, rather than filed as a
reference to nothing.

What is added joins the same disclosed list that already shows what a reply
will sign, carries a control to remove it again, and counts against one bound
for the whole list (`COMPOSED_CITATION_LIMIT`, `ui/src/lib/records.ts:73`,
head), so an operator's additions can no more outrun the reader than a prefill
can. A withdrawal takes no operator citation, because it names the record it
retires in its target and files no causal references at all.

This is not the browser deriving a relation. The operator says which record.
`docs/reference/architecture.md` states that boundary in the same head, as a
third consequence of the adoption rule it already carries.

**Two tests.** `ui/test/composed-citations.test.mjs` drives the real composer
in a DOM and reads the bytes that reach `/v0/act`, covering the revision case
end to end, resolution by ticket and by identifier, refusal of an unresolvable
name, refusal of a duplicate, the bound, removal, and the withdrawal exclusion.
`ui/test/publish-chain.test.mjs` gains the second artifact of a replacement, so
the publish dialog's basis checkbox is proved to reach the fold's provenance
rather than merely to render.

## What is still absent, and deliberately so

- Signing a verdict and merging stay outside the browser, because both need a
  checkout at an exact commit.
- The workroom seals no "this decision replaced that one" fact. The link lives
  in the two files and in the chain of records, as the how-to says at its end.
- The browser gates nothing on adoption, because the fold projects no adoption.
  Changing that is a layer 5 change: a fold rule, a projected field and a
  profile version.
