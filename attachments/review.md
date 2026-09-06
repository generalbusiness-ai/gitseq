# Reserve verdict for reviews

Ordinary completion reports should state the result in their text and omit `body.verdict`. That field already means “this report is a review.” Using it for `done` or `completed` creates an unnecessary review record and an “unresolved” independence label.

The current example is Tailapp #3917: a recovery-preparation report with `verdict: done`. Its requester accepted it in #3918, but inspection also calls it a review with unresolved independence. It reports preparation; it does not judge an implementation artifact. The same pattern occurs in my Gitseq #17318 and Tailapp #3154 coordination reports: each completed an assignment and was ratified, yet each is also an unresolved review. These are three checked examples across two workrooms, not a claim about every completion report. The historical satisfied tasks remain complete and should not be revived because of that label.

The [projection rule](fold-excerpt.go) treats every effective report with a nonempty verdict as a review. The [existing test](fold_test-excerpt.go) explicitly says that a report without a verdict is ordinary completion. The [detail renderer](RecordDetail-excerpt.tsx) displays the resulting review and independence fields. Current API inspections and exact source references are in [evidence.json](evidence.json). I have checked the rendering code; this note does not claim a new browser test.

The smallest improvement is an authoring clarification alongside the existing report example in SKILL.md or its linked tool guide: “For ordinary completion, put the outcome in text and omit verdict. For an implementation review, use gs review.” Show one minimal report example. Remove `verdict: done/completed` from any ordinary-report template found during that edit. Keep the current report/requester-ratification closure and the exact-head review rules.

This needs no new event kind, workflow state, approval stage or historical cleanup. Do not retire or refile already accepted reports to tidy the display. A later UI review may improve historical labels, but that is unnecessary for preventing new noise now. Verify the documentation example against the existing no-verdict test and confirm a genuine approved report still requires its normal independent review conditions.

Recommendation for Hugh: include this small clarification in the instructions simplification work. It is distinct from the earlier request-staleness and completion-preflight recommendations. I will use the existing ordinary-report form in my own future completion filings.
