Review note for Hugh — make idle workroom checks smaller without hiding work

Recommendation: commission one bounded design for a compact workroom wait response. Keep current obligations and genuine changes prominent; summarize completed presence churn and give stable, inspectable references to historical audit rows. Preserve the existing full history view. This changes communication, not commitment states or authority.

Evidence from this session

The first resumed sweep at 18:23 UTC followed the last recorded 15:27 sweep; I do not claim monitoring during that gap. Gitseq, Tailapps and Chess each returned 87 Hugh presence registrations and 87 expirations. Each live array serialized to 33,322 UTF-8 bytes using compact JSON. Tailapps and Chess had no new durable events, addressed requests or priority chat. Inventory had no such presence churn and its whole structured response was 683 bytes. The cause of the short-lived Hugh sessions is not yet established.

The next ten-minute sweep at 18:33 repeated five registrations and five expirations in each of those three rooms. Tailapps and Chess were still durably unchanged. Their responses were 6,086 and 6,216 compact JSON bytes: each included 1,916 bytes of presence history, plus 3,425 and 3,498 bytes of non-actionable commitment detail. Gitseq repeated 20 historical rows occupying 11,332 bytes, with another 109 skipped. These are serialized response measurements, not token charges, compute time or measured delivery-time savings.

The historical lane matters. Chess includes an unresolved stale item 6, which is viable and awaiting a governing decision; it must remain visible. Tailapps includes cancelled source-delivery chains whose outcomes were previously audited through their landed successors. Gitseq's default list is dominated by old cancelled/reneged rows. Showing less repetitive prose must never become a reason to retire, ignore or silently classify any of them as completed.

Current code deliberately repeats these lanes regardless of the cursor: internal/statusview/actor.go. cmd/gitseq-mcp/digest_test.go explicitly tests that stale, reneged and cancelled rows remain visible. That is an intentional protection against dropped work, not an accidental loop to delete.

Small proposed scope

1. Preserve the repeated current open/review/ratification obligations, unresolved stale assignments, changed closures and unacknowledged addressed chat. For unchanged historical audit rows, investigate compact event/status/reason references with complete counts and working pagination, avoiding repeated full conditions and provenance text. Keep the full rows available through existing work/inspect operations. No new audited/completed lifecycle flag or second task registry.
2. Summarize presence changes into useful current actor/status/focus information. Investigate why the same human repeatedly registers and expires; do not assume the actor or client is at fault. Keep detailed session events available when debugging leases. Current busy/waiting/blocked focus and real lease expiry must remain honest.

Acceptance should compare actual four-room responses over an idle interval and a mixed active interval. An old unresolved stale request, a newly cancelled task needing viability review, a review awaiting a verdict, a held landing, an addressed chat frame and a resident/cursor reset must all remain discoverable. Demonstrate reduced response bytes with unchanged obligations and complete audit access; do not claim saved task time from payload size alone.

This recommendation is separate from the closed 20-delivery trial and does not alter its sample or findings. It is advice for Hugh to review, not adoption or an implementation assignment. The existing review-evidence and workflow notes remain separate; this proposal targets the recurring communication cost visible while doing those reviews.
