# Keep viable stale assignments on their existing commitments

Review note for Hugh, following the instructions review #18630. I recommend a
bounded policy clarification: ordinary inherited staleness should trigger a
check of the assignment's current viability. When its author has explicitly
confirmed unchanged conditions and authority, keep the existing request and
commitment. Reserve replacement for a material change or an unresolved basis.
This is a recommendation for adoption, not a rule already in force.

The instructions review deliberately left this policy question open. SKILL's
discipline 11 requires an author to replace a stale request even when the
requirements have not changed. The adopted correction rule preserves an
unchanged live commitment. Applying both mechanically can create replacement
work solely to remove a status label, or leave viable work outside an actor's
ordinary open-request inbox.

The current Tailapps release provides a concrete example. Its input-contract
implementation landed at c57f34b2, passed main CI, and completed owned checkout
cleanup. The historical decision note, sealed receipt and current published
input-contract artifacts then showed ordinary inherited staleness. Inspection
of the current input-contract and identity artifacts reported no
`describes_superseded_world` fact.

Hugh's gate-2 release request #3785, b9d6cd89, explicitly confirmed the current
source and unchanged release conditions, destination, decision and performer.
It still inherited staleness from its receipt basis. Builder's promise
9f5158a0 was effective, and the release candidate 1c096394 and Checker's promised
review e6a8348f proceeded without a replacement assignment. Checker #3790,
db87db21, confirmed current release authority and found a substantive defect:
the proposed v0.2.0 archive's README still installed v0.1.2. That correction
remains owed on the same release promise. No version is yet published; this
example establishes successful intake and review, not completed release or a
measured efficiency saving.

The proposed rule should make these distinctions explicit:

- For an effective, unretired assigned request, inspect the actual staleness
  causes and current successors. The request author confirms the same outcome,
  conditions, performer, destination, hold ownership and governing decision.
  Preserve that confirmation in the existing durable record. An already
  explicit current commissioning statement needs no duplicate confirmation.
- If those facts are unchanged and the work remains feasible, retain the
  request and any live promise. Keep the recorded staleness visible. A stale
  lane should lead the actor to this check; it should not hide the obligation.
- If scope, performer, destination or authority changed, or a necessary basis
  has no adequate successor, use the existing replacement, transfer or decision
  procedure. Retired requests gain no force from this exception.

This would not relax exact-head review, approval ratification, held releases,
current target checks, artifact succession or superseded-world refusals. It
would not waive the four fresh authority facts required when self-initiated
work starts through an authority-bearing request chain. It adds no lifecycle
state, adoption shortcut or automatic authority inference.

If adopted, put the rule in one SKILL section and link intake and corrections
to it. Verify an unchanged stale assignment, a changed hold owner, a withdrawn
decision, an unavailable performer and an obsolete source basis against that
text. The existing broader UI review can use those cases when explaining stale
work. Until adoption, continue handling individual cases through explicit
current commissioning and evidence; do not silently rewrite the general rule.
