# Gitseq sequence recovery, 6 September 2026

Notes source is approved and merged at b2ed92662464b9ca7670395fce26fe723fa069d1, pushed to origin with its sealed receipt. Source content is unaffected. Canonical workroom writes are blocked by a fork created during Codex's overlapping sequence push and artifact cleanup.

## Evidence and preservation

The remote and common parent are a9ade32ac1973cdb6eddd201e43a19e8a09aa26a. Both following commits have that same parent:

- b5c32b72aa86b510b05a73b9d065ca22ad2672d1: first cleanup supersession, returned effective and was saved as the locally verified frontier at depth 19193.
- cd0e24abe956993f1389a095fd80a7d99763f1f8: subsequent cleanup supersession admitted after the local ref had regressed. The stored verified frontier correctly refuses this sibling.

Both commits and their payload objects are preserved in refs/archive/notes-delivery-fork-20260906/{verified,admitted-sibling} and /tmp/notes-delivery-fork-20260906.bundle. The verified bundle names the shared parent as its prerequisite, already present on origin. No signing key or custody file is in this evidence.

The local remote.origin.fetch maps refs/seq/*:refs/seq/*. An isolated bare Git reproduction at /tmp/reproduce-seq-push-tracking.py confirms that successful git push updates that mapped tracking destination back to the advertised snapshot even if another writer advanced it during the push. The local sequence push and cleanup overlapped. Reproduction output is /tmp/notes-push-tracking-repro.json.

## Proposed recovery requiring operator authorization

1. Verify the live ref is still cd0e24abe956993f1389a095fd80a7d99763f1f8, the stored verified frontier is b5c32b72aa86b510b05a73b9d065ca22ad2672d1 at 19193, the remote is still their parent, and both archives and bundle verify. Stop if any premise changed.
2. Coordinate an explicit pause of every Gitseq sequencer write, direct CLI mutation, sequence fetch and overlapping push. Planner/Codex are paused; do not proceed if any other writer remains active or an incompatible stored witness is identified. Then stop only the owned Gitseq reader/sequencer currently PID 2339 on 127.0.0.1:7777, verifying PID, loaded executable and arguments immediately beforehand. Other workroom residents and the real Tailapp telemetry daemon are outside this action. Before restoring the authoritative ref or restarting admission, replace the unsafe origin fetch mapping with refs/seq/*:refs/remotes/origin/seq/* and verify that no configured fetch destination maps onto refs/seq/*. Preserve the heads mapping and remote URL. This closes the old origin hazard as well as providing a safe publication destination.
3. Restore the canonical local sequence ref to its previously verified b5c32b72 chain using exact compare-and-swap from cd0e24ab. Keep both archive refs. Do not edit, lower or erase the stored verified frontier, custody, signed events, or sealed Notes receipt.
4. Restart that same Gitseq build and verify the sequence against the unchanged stored witness. Reconnect the adapter. Re-submit the exact original own cleanup intent/idempotency key for the archived sibling through the sequencer so its outcome is admitted as a descendant of b5c32b72. Preserve the original sibling as incident evidence; do not pretend its event ID belonged to the canonical continuation.
5. File a durable incident account and follow-up request against the sealed delivery for prevention of publication overwriting the authority ref. Resume own cleanup, obtain author/ratifier retirement of the four deleted predecessors, verify the exact Notes merge, and complete delivery housekeeping.
6. Publish through the corrected origin mapping, whose sequence tracking destination is refs/remotes/origin/seq/*, never the live refs/seq/*. The corrected disposable control has proved this preserves a concurrent advance. Push source, full continuation and receipt, and verify remote ancestry. No force push is needed because origin remains the common ancestor.

Acceptance: canonical log continues the stored verified b5c32b72 frontier; both intended cleanup outcomes are effective once; old sibling evidence remains retained; no witness reset, actor impersonation, custody change, signed-event editing, source modification or force push; Notes successor verification passes.

## Additional verification

The corrected reproduction and separate-tracking positive control both pass. The hook fails on any unsuccessful advance and clears Git quarantine variables. /tmp/notes-known-verified-checkpoints.json records the bounded local checkpoint survey: the canonical checkout remembers b5c32b72 at 19193; the inactive Notes browser clone remembers an ancestor at 19084. All listed Gitseq adapters use the canonical common Git directory. Unknown remote or transient in-memory observations are not claimed surveyed; independent operator review must identify any further frontier. /tmp/four-room-frontier-integrity-20260906.json checks each room against its own saved checkpoint; only Gitseq fails.

## Exact executable and restart

The loaded PID2339 executable is /Users/hughpyle/play/gitseq/bin/gs, SHA-256 0b8aeeec3b31a3aefbd0714e7a1d96286f1cd94bf7746748c5f9e6ae7dc978b6. A byte-identical copy is preserved at /tmp/gitseq-fork-recovery-bin/gs-0b8aeeec3b31a3aefbd0714e7a1d96286f1cd94bf7746748c5f9e6ae7dc978b6. Build metadata is recorded in /tmp/notes-fork-original-binary-build-info.txt: Go1.27.0, darwin/arm64, source183211c3, vcs.modified=true. This is the same existing binary, not a claimed clean rebuild.

Restart command after approval and gates:

```sh
/tmp/gitseq-fork-recovery-bin/gs-0b8aeeec3b31a3aefbd0714e7a1d96286f1cd94bf7746748c5f9e6ae7dc978b6 serve --repo /Users/hughpyle/play/gitseq --listen 127.0.0.1:7777
```

Output goes to /tmp/gitseq-fork-recovery-resident-20260906.log. Before any admission, verify the saved witness is unchanged and current ref equals b5c32b72. Abort on incompatible witness, changed PID/binary, failed mapping check, changed canonical/remote head, failed CAS or failed sequence audit. Independent Planner review ran both corrected publication fixtures, verified the preservation bundle and checked the 36 accessible registered worktrees share the same b5c32b72 checkpoint; one historical prunable scratch checkout was unavailable. Its adapter witnessed b5 and refused cd0. That review supports the proposal and does not authorize fork selection.
