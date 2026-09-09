# Kernel trust-guard mutation matrix

This note records two mutation passes over the same fifteen kernel trust
boundaries. Each pass ran against its own exact head, and neither speaks for the
other. The current table is first; the original experiment follows it unchanged
in substance.

## Current recipes at e8278a6d (2026-09-07)

Every one of the fifteen rows below was re-mutated at head `e8278a6d`. No row was
carried over from the 2026-08-08 table by inspection alone. Each mutation was
applied by itself in a detached disposable worktree, one row at a time and never
in parallel. For each row the named test failed with a behavioural assertion at
the named guard, and the same test then passed with the mutation reverted. A
compile failure was never counted as a caught mutation. Toolchain: Go 1.27.0 on
darwin/arm64.

| Trust boundary | Current seam (file:line, function) | Build-valid mutation | Isolating test | Result |
|---|---|---|---|---|
| cold sequencer signature | `internal/kernel/kernel.go:1219`, `scanHeadInto` | Ignore the `gitstore.VerifySSHSignature` error | `TestVerifierRejectsWrongSequencerSignatureOnColdAudit` | Mutant failed: `wrong sequencer cold-audit error = <nil>`. Control passed. |
| resident-delta sequencer signature | `internal/kernel/kernel.go:1413`, `(*deltaScan).accept` | Ignore the `gitstore.VerifySSHSignature` error | `TestReaderRejectsWrongSequencerDeltaWithoutMutatingVerifiedCache` | Mutant failed: `reader accepted a delta signed by a non-descriptor sequencer`. Control passed. |
| named genesis | `internal/kernel/kernel.go:1275`, `validateSequenceBounds` | Disable only the first-commit/genesis comparison | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` | Mutant failed: `wrong genesis boundary error = <nil>`. Control passed. |
| named head | `internal/kernel/kernel.go:1278`, `validateSequenceBounds` | Disable only the last-commit/head comparison | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` | Mutant failed: `wrong head boundary error = <nil>`. Control passed. |
| parentless genesis | `internal/kernel/kernel.go:1298`, `validateChainParents` at index 0 | Disable only the genesis parent-count condition | `TestChainParentGuardsPinGenesisAndEventsIndependently` | Mutant failed: `parented genesis error = <nil>`. Control passed. |
| single-parent chain | `internal/kernel/kernel.go:1303`, `validateChainParents` | Disable the exact-one/exact-prior condition shared by the cold scan and the delta scanner | `TestChainParentGuardsPinGenesisAndEventsIndependently`; concrete control `TestVerifierRejectsSequencerSignedMergeEvent` | Both mutants failed: `merge event error = <nil>`. Both controls passed. |
| actor signature | `internal/intent/intent.go:166`, `Verify` | Keep the signature inputs live but bypass the Ed25519 result | `TestVerifierRejectsSequencerSignedEventWithInvalidActorSignature` | Mutant failed: `invalid actor signature error = <nil>`. Control passed. |
| target log | `internal/kernel/kernel.go:1532`, `loadCommit`, deciding on the comparison in `verifySignedTarget` at `internal/kernel/kernel.go:1576` | Disable only the decoded-target/genesis comparison | `TestVerifierRejectsIntentReboundToAnotherLog` | Mutant failed: `verifier accepted an intent rebound into another log`. Control passed. |
| causal trailers | `internal/kernel/kernel.go:1535`, `loadCommit` | Disable only signed-rests-on/envelope-trailer equality | `TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity` | Mutant failed: `altered causal trailer error = <nil>`. Control passed. |
| signed payload tree | `internal/kernel/kernel.go:1542`, `loadCommit` | Disable only actual-tree/signed-tree equality | `TestVerifierRejectsCommitTreeDifferentFromSignedPayloadTree` | Mutant failed: `substituted tree error = <nil>`. Control passed. |
| payload shape | `internal/gitstore/audit.go:437`, `(*AuditBatch).PayloadTree` root switch | Accept the otherwise-invalid payload-tree switch default | `TestVerifierRejectsExtraPayloadTreeEntry` | Mutant failed: `extra payload entry error = <nil>`. Control passed. |
| attachment name | `internal/gitstore/audit.go:458`, `(*AuditBatch).PayloadTree` attachments loop | Accept `attachments/` paths without applying the attachment-name grammar | `TestVerifierRejectsInvalidAttachmentName` | Mutant failed: `invalid attachment name error = <nil>`. Control passed. |
| dedup conflict | `internal/kernel/kernel.go:1565`, `dedupPrior` | Keep the prior event live but bypass signed-envelope inequality | `TestVerifierRejectsExternallySequencedDedupConflict` | Mutant failed with the wrong refusal: `... duplicates idempotent event ...` instead of an idempotency conflict. Control passed. |
| verifier ceiling | `internal/kernel/kernel.go:1545`, `loadCommit` | Stop charging the signed envelope against the remaining payload ceiling | `TestVerifierAppliesCeilingToEnvelopeAndPayloadTogether` | Mutant failed: `combined envelope and payload verification error = <nil>`. Control passed. |
| submit aggregate ceiling | `internal/kernel/kernel.go:483`, `validateRequestSize` | Keep attachment sizes live but stop accumulating them | `TestSubmitChargesPayloadAndAllAttachmentsToOneCeiling` | Mutant failed: `combined payload and attachments error = <nil>`. Control passed. |

### What changed since the historical table

Five rows name a different call site or a different file than the 2026-08-08
table did. The behaviour each row claims is unchanged.

- Cold sequencer signature: `VerifySSHCommit` in `scanHead` is now
  `gitstore.VerifySSHSignature` in `scanHeadInto`. `scanHead` remains only as a
  thin wrapper around it.
- Resident-delta sequencer signature: `VerifySSHCommit` in `scanAfter` is now
  `gitstore.VerifySSHSignature` in the delta scanner's `accept` method.
  `scanAfter` still exists, but it delegates every per-commit check to that
  scanner, which the checkpoint path shares.
- Actor signature: the Ed25519 result now lives in `internal/intent`, not in the
  kernel package.
- Payload shape and attachment name: both guards now live in
  `internal/gitstore/audit.go`, not in the kernel package. The kernel reaches
  them through `AuditBatch.PayloadTree`.

The remaining ten rows kept the guard the older table described; only their file
and line moved.

### One row worth reading twice

The dedup-conflict row fails differently from the rest. With signed-envelope
inequality bypassed, a conflicting second commit under the same idempotency key
is read as an ordinary replay, so the scan still refuses the log, but as a
duplicate rather than as a conflict. The test pins that distinction by requiring
the idempotency-conflict error, and it fails. The boundary is proved by the
identity of the refusal, not by the mere presence of one.

## Historical experiment at ede3289 (2026-08-08)

The table below records build-valid mutations made against exact head `ede3289`
while repairing request `27e563c5`. Each mutation was applied alone in a detached
disposable worktree. The named focused test failed; the unmutated positive
control passed. A compile failure was never counted as a caught mutation. Those
runs happened at `ede3289` and nowhere else; the current table above is the
record for `e8278a6d`.

| Trust boundary | Build-valid mutation | Isolating test |
|---|---|---|
| cold sequencer signature | Ignore the `VerifySSHCommit` error in `scanHead` | `TestVerifierRejectsWrongSequencerSignatureOnColdAudit` |
| resident-delta sequencer signature | Ignore the `VerifySSHCommit` error in `scanAfter` | `TestReaderRejectsWrongSequencerDeltaWithoutMutatingVerifiedCache` |
| named genesis | Disable only the first-commit/genesis comparison | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` |
| named head | Disable only the last-commit/head comparison | `TestSequenceBoundsPinNamedGenesisAndHeadIndependently` |
| parentless genesis | Disable only the genesis parent-count condition | `TestChainParentGuardsPinGenesisAndEventsIndependently` |
| single-parent chain | Disable the exact-one/exact-prior condition shared by cold and delta scans | `TestChainParentGuardsPinGenesisAndEventsIndependently`; concrete control `TestVerifierRejectsSequencerSignedMergeEvent` |
| actor signature | Keep signature inputs live but bypass the Ed25519 result | `TestVerifierRejectsSequencerSignedEventWithInvalidActorSignature` |
| target log | Disable only the decoded-target/genesis comparison | `TestVerifierRejectsIntentReboundToAnotherLog` |
| causal trailers | Disable only signed-rests-on/envelope-trailer equality | `TestVerifierRejectsAlteredCausalTrailersWithFreshIdentity` |
| signed payload tree | Disable only actual-tree/signed-tree equality | `TestVerifierRejectsCommitTreeDifferentFromSignedPayloadTree` |
| payload shape | Accept the otherwise-invalid payload-tree switch default | `TestVerifierRejectsExtraPayloadTreeEntry` |
| attachment name | Accept `attachments/` paths without applying the attachment-name grammar | `TestVerifierRejectsInvalidAttachmentName` |
| dedup conflict | Keep the prior event live but bypass signed-envelope inequality | `TestVerifierRejectsExternallySequencedDedupConflict` |
| verifier ceiling | Stop charging the signed envelope against the remaining payload ceiling | `TestVerifierAppliesCeilingToEnvelopeAndPayloadTogether` |
| submit aggregate ceiling | Keep attachment sizes live but stop accumulating them | `TestSubmitChargesPayloadAndAllAttachmentsToOneCeiling` |

The mutation pass found one pre-existing false positive. With cold sequencer
signature verification disabled, `TestInjectedGenesisSignerCannotVerifyASequence`
still passed because another rejection remained in its deliberately malformed
descriptor fixture. It therefore did not prove the sequencer-signature line.
`TestVerifierRejectsWrongSequencerSignatureOnColdAudit` uses an ordinary valid
genesis and actor envelope, changing only the commit signer; it fails with that
guard removed.

The altered-trailer repair follows the same rule. Its hostile commit uses a
fresh dedup identity that never appeared earlier in the log. With trailer
equality removed there is no duplicate event available to refuse the commit,
so the test can fail only at the causal-trailer boundary it names.
