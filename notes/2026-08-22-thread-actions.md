# Acting from the thread view

Design revision, re-evaluated on 2026-09-04. The adopted design basis is
proposal `4ab5ddfc1f4b04de6378b3eca3a6ea2a81f8de1f`, ratified at
`e73f82947475e1d74ac4f45f02285907f0aab9e7`, for the exact historical note at
head `10d8dd3205da0cdf325ea34a5b191da3ae9e0fc6`. This revision preserves that
interaction model and updates it for the current fold, browser authority
helpers, guarded review path, and target-aware landing design. Request
`d920e756` authorizes delivery of this note only. It authorizes no UI change.

The target-aware landing note in
[`2026-09-04-landing-obligation.md`](2026-09-04-landing-obligation.md) is the
governing design for future landing states. This note lands before that
design's I1 and I5 implementation steps. A later request to implement thread
actions should depend on I5, so it consumes the final projected states and UI
target fields instead of building a temporary second interpretation of
`awaiting-merge`.

## The problem

The thread is where an operator learns that work needs an answer, but the
browser distributes semantic actions across row toolbars and composer routes.
The composer can send temporary talk and several ordinary Workroom records,
but it does not make the current actor's available durable choices clear in
one place. A person can read an open request and still miss how to accept it,
report it, record why they cannot take it, or continue an approved landing.

The browser already receives the fold-projected statements, commitments,
decisions, provenance, reviews, and roster. It must show only what those inputs
say. A durable write remains subject to the signing boundary when it arrives.
A guarded review, release, or Git merge also remains subject to its dedicated
CLI or MCP boundary.

## The proposed experience

Add a fixed **Your move** section between the thread spine and its composer.
It lists the actions or instructions waiting on the current actor in this
thread and names the record each item concerns. It remains visible when there
is no item and says, “No durable action is available to you in this thread.” A
blank area is not an explanation.

There are three deliberately different interaction shapes:

- A reasoned state action opens the composer in a named mode. It shows the
  action, direct bases, known body fields, and applicable request conditions
  before the actor writes and sends text. The composer gains an explicit
  `assert` mode for **Decline** and **Blocked**; it submits a normal `state`
  record of that kind, just as the existing modes submit their stated kind.
- A ratification has no text. **Accept**, **Agree**, and a request's direct
  proposal ratification use a direct-act control that calls `doAct` with
  `act: ratify` and the target event. It is not routed through the composer.
  The control uses the existing `mayRatify` check for the current roster and
  the target's projected satisfier; `signingRefusal` remains the final check.
- A guarded operation is an instruction with exact records, not a browser
  write. Review verdicts point to `gs review` or the MCP review tool, releases
  point to the target-aware guarded release path, and landings point to
  `gs merge`. An instruction is never passed to `doAct` or the composer.

Escape closes a composer mode. A successful write redraws from the projection
and recomputes the section. The section replaces semantic row shortcuts; rows
keep navigation, detail, disclosure, and citation controls.

## Browser actions and exact records

The selector reads direct projected facts only. State actions are offered only
to a current participant. Ratification actions additionally require an
effective, unretired, not-currently-ratified target whose projected satisfier
and current roster permit this viewer. These are calls to the browser's
existing authority helpers, not a new authority model. The signing boundary
repeats the check because a session, role, target, or decision may move after
rendering.

| Situation projected in this thread | Item | Durable effect |
|---|---|---|
| An effective open request addressed to the viewer, with no live promise and no one eligible direct proposal | **Accept** | A promise resting on that request. |
| An effective open request addressed to the viewer, with no live promise | **Done** | One direct report resting on the request. The composer shows the request conditions and does not create a hidden promise. |
| An effective open request addressed to the viewer, with no live promise | **Decline** | An assertion resting on the request that gives the reason and asks its requester or a ratifier to retire it. |
| An effective open request addressed to the viewer that directly rests on exactly one eligible proposal | **Agree** or **Disagree** | A direct ratification of that proposal, or a dissent resting on it. This preserves the proposal-first path instead of also offering request acceptance. |
| The viewer holds a live promise on an ordinary request | **Done** | A report resting on that promise. |
| The viewer holds a live promise | **Blocked** | An assertion resting on that promise. |
| The viewer authored an ordinary live record | **Withdraw** | A supersession of that record. Roster and other governance records stay excluded because their retirement rule is not ordinary authorship. |
| An effective report whose projected satisfier and current roster permit the viewer | **Accept** or **Needs work** | A direct ratification of the report, or a dissent resting on it. Gating both choices on the report's satisfier is a presentation choice, not an authority rule for dissent. |
| An effective proposal whose projected satisfier and current roster permit the viewer | **Agree** or **Disagree** | A direct ratification of the proposal, or a dissent resting on it. |
| An artifact | **Propose adoption** or **Request review** | A proposal rests on that exact artifact. A review request rests on the artifact and, when one direct citable ratified proposal exists, is prefilled with that proposal too. Neither action is gated on whether that proposal exists. |

An eligible direct proposal is effective, unretired, not currently ratified,
ratifiable by this viewer, and a direct basis of the request. If there is no
such proposal, **Accept** remains available. If exactly one exists, **Agree**
or **Disagree** replaces **Accept**. Several eligible direct proposals do not
authorize the browser to choose between them; the section explains the
ambiguity and offers neither path until the records are made unambiguous.

The direct **Done** rule follows the current work loop: an addressee may report
directly only when they hold no live promise on that request. Once a promise
exists, its report is the sole completion route. **Decline** has the same
no-live-promise gate. A person who has accepted work must use **Blocked** or
report against their promise rather than purport to decline it.

An assigned review request is the exception to generic **Done**. The browser
may offer the reviewer **Accept** to publish a promise. Once the review is in
flight it shows the assigned head, artifact set, conditions, and an instruction
to use `gs review` or the MCP review tool. It does not offer a generic report
that could be mistaken for a verdict.

The browser never uses an unbounded set of proposals to prefill a review
request. It uses `RecordIndex.citableProposals` and its
`CITED_PROPOSAL_LIMIT` of one: the newest direct, effective, unretired,
ratified proposal that rests on the artifact, ordered by fold sequence with an
event-id tie-break. It displays that exact basis before sending. The prefill
does not decide whether **Propose adoption** or **Request review** is offered.
This is a bounded citation rule, not an inference that the browser can
otherwise make an artifact adopted.

Review verdicts are not browser-signed generic reports. Admission refuses a
report whose `body.verdict` or `body.status` is `approved` or
`changes-requested` unless the guarded review path supplies `review_path` and
the other exact review evidence. Lack of a browser checkout is not the reason:
the MCP review tool can obtain the head from the artifact without one. The
boundary is the guarded admission contract.

## Target-aware landing items

Today, phase-one `gs merge --authorization` can bind an exact candidate,
approval, implementation request, and measured target head. New work carries
that structured guard under `SKILL.md`, but the current projection has no
machine-readable hold or release state. The browser must not parse prose to
invent one or offer a generic report as authorization.

The thread-actions UI implementation follows landing-obligation I5. It reads
the I1/I4 projection rather than translating request prose into authority.
The three landing states add these items:

| Projected state | Waiting actor | Item |
|---|---|---|
| `awaiting-review` | performer | **Request review** opens the bounded composer route when no live review request already covers the reporting artifact. An assigned reviewer sees the guarded-review instruction described above. |
| `awaiting-authorization` | hold owner | **Release in the guarded path** shows the exact request, candidate, approval, target repository, target ref, and measured target head. It is an instruction, not a generic browser report. The performer may get **Request release**, a normal no-artifact request addressed to the hold owner with those projected bindings prefilled. |
| `awaiting-landing` | performer | **Merge with `gs`** shows the exact candidate, ratified approval, target, and release plus its ratification when the request was held. It is an instruction, not a direct act. |

Other viewers see whom the row is waiting on, not an action for themselves.
The selector does not parse phrases such as “do not merge” or invent a hold
from prose. It reads the projected `landing`, `hold_owner`, target, approval,
and waiting actor established by I1 and I4.

Until I1 lands, the current fold profile has one `awaiting-merge` state. This
note does not ask for a temporary browser implementation against it. I1's
legacy rule will map an existing row to `awaiting-review` when no ratified
approval names its reporting artifact and to `awaiting-landing` when one does.
I5 then gives the selector the final state vocabulary. This ordering avoids a
second compatibility branch in the UI.

The merge instruction never claims that the displayed head may still land.
`gs merge-plan` and `gs merge` recheck the exact approval, artifact lineage,
target head, target-aware authorization, and staleness. `gs merge` also holds
the repository-wide merge transaction lock through Git mutation, durable
succession, receipt sealing, and cleanup. The browser neither copies those
checks nor acquires that lock.

## Boundaries and inputs

This is a layer 7 presentation design over layer 5's projection and layer 6's
typed views; the existing contracts are in
[the architecture reference](../docs/reference/architecture.md). It adds no
workroom kind, lifecycle, endpoint, adoption relation, server selection rule,
or merge authority.

The selector receives the projection, record index, viewer identity, current
session state, and roster. After I5 it also receives the projected landing
state, target, waiting actor, reporting artifact, exact review relation, and
release relation. For a report ratification it reads the unique originating
requester from the projected commitment row, only while that requester is a
live participant. It may use the bounded direct-proposal citation described
above.

It must not infer an artifact's adopted status, an indirect basis for a new
act, a past ratification as the active ratification, a hold from prose, or that
a displayed guarded operation will still be admitted. Ordinary staleness does
not mean retirement and does not by itself hide an action: the admission path
records permitted stale bases. A retired or ineffective target is withheld.
`describes_superseded_world` is shown where projected but is not turned into a
new browser authority rule; the guarded review and merge paths decide what it
means for their operations.

The direct-act control and every composer route go through the existing write
boundaries. The selector may show a known refusal early by calling those same
authority helpers; it does not reimplement fold authority. It withholds state
actions from a viewer who is not a current participant, while preserving the
fold's narrow exception that a departed author may supersede their own
ordinary record.

The broader state-route courtesy repair has now landed on main at
`ad8793636a22985cc63eaecc16f6dbb61a6ec128`. The shared
`isLiveParticipant` predicate gates all ordinary state-composer routes, and
`signingRefusal` remains the final boundary while own-author **Withdraw** stays
available to a departed actor. This design builds on that result; it does not
reopen or claim to replace it.

## Implementation shape

Extract a pure thread-action selector from `semanticActions` into a library
module that does not render React elements or close over callbacks. It returns
small descriptions: label, subject event, action shape (`composer`,
`direct-act`, or `instruction`), state mode where applicable, direct bases,
known body fields, exact guarded-operation records, and a display-only reason.

`Thread.tsx` renders those descriptions in **Your move**. Its composer accepts
the existing modes plus `assert`; a direct-act description calls the existing
`doAct` path; an instruction renders exact identifiers and the named CLI or MCP
path but signs nothing. `Toolbar.tsx` keeps `RowToolbar` and `ToolbarButton` for
navigation and detail controls, with no semantic Workroom actions.

The selector preserves fold sequence order when it must choose from projected
records. It exposes every selected basis before a composer send. It does not
need a general **Note** action: ordinary thread talk remains available, and a
durable assertion is available only where this design gives it a specific
record and reason.

## Tests that must accompany implementation

- Pure selector cases cover every browser-action row; inactive, ineffective,
  stale, and retired targets; a withdrawn active ratification; a departed
  participant; ambiguous direct proposals; and a roster record that must not
  offer **Withdraw**.
- Direct ratification tests prove that report acceptance, proposal agreement,
  and a request's direct proposal use `doAct` and never the composer. They
  cover a target whose standing, satisfier, or current roster denies the
  viewer, and a target already carrying an active ratification.
- A direct-report browser test shows request conditions and submits one
  `report` resting on the request, with no promise. A live promise removes
  direct reporting and makes **Done** cite that promise. A decline test proves
  the no-live-promise gate and the assertion's retirement request.
- Assert-mode tests cover **Decline** and **Blocked**. They prove the right
  direct basis and kind rather than mislabelling either action as a dissent.
- The artifact flow test proves that **Propose adoption** and **Request review**
  remain offered with and without a direct citable ratified proposal. When one
  exists, **Request review** is prefilled with the artifact plus exactly that
  one proposal; the test fails if the citation limit or deterministic order is
  removed.
- Review-assignment tests prove that a generic browser report shaped as a
  verdict is never offered and remains refused without the guarded
  `review_path` evidence.
- Landing-state tests cover `awaiting-review`, `awaiting-authorization`, and
  `awaiting-landing`, including the projected waiting actor, held and unheld
  requests, exact target fields, and the I1 legacy mapping. They prove that a
  guarded review, release, or merge is rendered only as an instruction and is
  never passed to the composer or `doAct`.
- All action tests show exact citations before signing and prove that the final
  signing boundary refuses a target or authority that moved after rendering.
  Guarded-operation tests instead prove that the browser hands exact records
  to the dedicated boundary and does not claim the operation succeeded.

## Decision maintained

Keep one visible, projection-derived **Your move** section in the thread view.
It centralizes existing action choices, adds direct reporting and declining
only in forms the fold admits, makes ratification an explicit direct act, and
represents guarded review, release, and landing as exact instructions. It
changes no Workroom semantics. Its UI implementation follows landing-obligation
I5 and requires a separate durable request and fresh exact-head review.
