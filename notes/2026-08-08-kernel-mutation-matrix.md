# Kernel trust-guard mutation matrix

This matrix records build-valid mutations made against exact head `ede3289`
while repairing request `27e563c5`. Each mutation was applied alone in a
detached disposable worktree. The named focused test failed; the unmutated
positive control passed. A compile failure was never counted as a caught
mutation.

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
