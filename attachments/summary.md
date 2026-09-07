# Mutation sweep evidence — gitseq request #20743

Exact head: `e8278a6dacd14b08bbbcd1276fc193edd6401f53`
Toolchain: `go version go1.27.0 darwin/arm64`
Method: one disposable detached worktree (`git worktree add --detach ... e8278a6d`),
one mutation applied at a time by python3 string replacement asserting exactly one
occurrence, `git diff` captured, focused test run, then `git checkout -- .` and the
same focused test run unmutated as the control. Runs were sequential, never parallel.
The disposable worktree was removed before any gate ran.

Every one of the 15 rows was re-mutated at this head. None was merely mapped.

| # | Trust boundary | Seam at e8278a6d (file:line) | Build-valid mutation | Isolating test | Mutant | Control |
|---|---|---|---|---|---|---|
| 01 | cold sequencer signature | internal/kernel/kernel.go:1219 (`scanHeadInto`) | `gitstore.VerifySSHSignature(commit, log.sequencerPublicKey); err != nil` -> `err != nil && false` | `TestVerifierRejectsWrongSequencerSignatureOnColdAudit` | FAIL: `wrong sequencer cold-audit error = <nil>` (exit 1) | PASS (exit 0) |
| 02 | resident-delta sequencer signature | internal/kernel/kernel.go:1413 (`(*deltaScan).accept`) | `gitstore.VerifySSHSignature(audited, s.base.sequencerPublicKey); err != nil` -> `err != nil && false` | `TestReaderRejectsWrongSequencerDeltaWithoutMutatingVerifiedCache` | FAIL: `reader accepted a delta signed by a non-descriptor sequencer` (exit 1) | PASS (exit 0) |
| 03 | named genesis | internal/kernel/kernel.go:1275 (`validateSequenceBounds`) | `commits[0] != genesis` -> `(commits[0] != genesis && false)` | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` | FAIL: `wrong genesis boundary error = <nil>` (exit 1) | PASS (exit 0) |
| 04 | named head | internal/kernel/kernel.go:1278 (`validateSequenceBounds`) | `commits[len(commits)-1] != head` -> `... && false` | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` | FAIL: `wrong head boundary error = <nil>` (exit 1) | PASS (exit 0) |
| 05 | parentless genesis | internal/kernel/kernel.go:1298 (`validateChainParents`, index 0) | `len(parents) != 0` -> `len(parents) != 0 && false` | `TestChainParentGuardsPinGenesisAndEventsIndependently` | FAIL: `parented genesis error = <nil>` (exit 1) | PASS (exit 0) |
| 06 | single-parent chain | internal/kernel/kernel.go:1303 (`validateChainParents`) | `len(parents) != 1 \|\| parents[0] != prior` -> `(...) && false` | `TestChainParentGuardsPinGenesisAndEventsIndependently`; concrete control `TestVerifierRejectsSequencerSignedMergeEvent` | FAIL (both): `merge event error = <nil>` (exit 1) | PASS both (exit 0) |
| 07 | actor signature | internal/intent/intent.go:166 (`Verify`) | `!ed25519.Verify(...)` -> `!ed25519.Verify(...) && false` (inputs stay live) | `TestVerifierRejectsSequencerSignedEventWithInvalidActorSignature` | FAIL: `invalid actor signature error = <nil>` (exit 1) | PASS (exit 0) |
| 08 | target log | internal/kernel/kernel.go:1532 (`loadCommit`; comparison in `verifySignedTarget`, kernel.go:1576) | `if !targetMatches` -> `if !targetMatches && false` | `TestVerifierRejectsIntentReboundToAnotherLog` | FAIL: `verifier accepted an intent rebound into another log` (exit 1) | PASS (exit 0) |
| 09 | causal trailers | internal/kernel/kernel.go:1535 (`loadCommit`) | `!intent.EqualRefs(decoded.RestsOn, trailers)` -> `... && false` | `TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity` | FAIL: `altered causal trailer error = <nil>` (exit 1) | PASS (exit 0) |
| 10 | signed payload tree | internal/kernel/kernel.go:1542 (`loadCommit`) | `commit.Tree != treeOID` -> `commit.Tree != treeOID && false` | `TestVerifierRejectsCommitTreeDifferentFromSignedPayloadTree` | FAIL: `substituted tree error = <nil>` (exit 1) | PASS (exit 0) |
| 11 | payload shape | internal/gitstore/audit.go:437 (`(*AuditBatch).PayloadTree`, root switch default) | delete the `default:` refusal so an unknown root entry is accepted | `TestVerifierRejectsExtraPayloadTreeEntry` | FAIL: `extra payload entry error = <nil>` (exit 1) | PASS (exit 0) |
| 12 | attachment name | internal/gitstore/audit.go:458 (`(*AuditBatch).PayloadTree`, attachments loop) | `!isBlobMode(entry.mode) \|\| !attachmentName.MatchString(entry.name)` -> `!isBlobMode(entry.mode)` | `TestVerifierRejectsInvalidAttachmentName` | FAIL: `invalid attachment name error = <nil>` (exit 1) | PASS (exit 0) |
| 13 | dedup conflict | internal/kernel/kernel.go:1565 (`dedupPrior`) | `!prior.Signed.Equal(signed)` -> `... && false` | `TestVerifierRejectsExternallySequencedDedupConflict` | FAIL: `external dedup conflict error = commit 99d9bac6... duplicates idempotent event 0f469d62...` (exit 1) | PASS (exit 0) |
| 14 | verifier ceiling | internal/kernel/kernel.go:1545 (`loadCommit`) | `remaining := desc.PayloadCeiling - uint64(len(message))` -> `remaining := desc.PayloadCeiling` | `TestVerifierAppliesCeilingToEnvelopeAndPayloadTogether` | FAIL: `combined envelope and payload verification error = <nil>` (exit 1) | PASS (exit 0) |
| 15 | submit aggregate ceiling | internal/kernel/kernel.go:483 (`validateRequestSize`) | `eventSize += size` -> `eventSize += 0 * size` (per-attachment size stays live) | `TestSubmitChargesPayloadAndAllAttachmentsToOneCeiling` | FAIL: `combined payload and attachments error = <nil>` (exit 1) | PASS (exit 0) |

## Recipes that changed since the historical ede3289 table

- Row 01: `VerifySSHCommit` in `scanHead` is now `gitstore.VerifySSHSignature` in
  `scanHeadInto`. `scanHead` survives only as a thin wrapper (kernel.go:1153).
- Row 02: `VerifySSHCommit` in `scanAfter` is now `gitstore.VerifySSHSignature`
  inside the delta scanner method `(*deltaScan).accept`. `scanAfter` (kernel.go:1313)
  delegates the per-commit check to that scanner, which `scanListedAfterMode` shares.
- Rows 11 and 12 live in `internal/gitstore/audit.go`, not in the kernel package.
- Row 07 lives in `internal/intent/intent.go`, not in the kernel package.
- Rows 03, 04, 05, 06, 08, 09, 10, 13, 14 and 15 kept their described guard; only
  their exact file and line moved.

## Nuances worth stating

- Row 13 is a wrong-error failure, not an accepted-log failure. With signed-envelope
  inequality bypassed, `dedupPrior` reports the second commit as an idempotent
  duplicate instead of a conflict, so the scan still refuses the log but at the wrong
  classification. The test pins the distinction by requiring `ErrIdempotencyConflict`,
  and it fails. This is the named boundary, judged by the error identity.
- No row was left unproven. No row's mutation was silently tolerated by its test.
- No compile failure was counted anywhere: every mutant built and every control passed.

## Files

Per row: `<nn>-<slug>.diff`, `<nn>-<slug>-mutant.txt`, `<nn>-<slug>-control.txt`.
Each test output file ends with an `exit_code=` line.
Gate output: `gates-go-test.txt`, `gates-docset.txt`.
