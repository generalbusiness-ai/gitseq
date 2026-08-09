# gitseq spike results

This tracked file is the stable projection of the six adversarial cases. Run-specific tool versions, timings, and JSON evidence are regenerated under ignored `.spike/`.

Overall: **pass**

| Case | Result | Evidence |
|---|---|---|
| 1. Concurrent retry and failover | pass | `TestActualProcessExitRecoversFromGitAlone` (pass)<br>`TestConcurrentCASProducesOneLinearChain` (pass)<br>`TestCrashBoundariesRecoverFromLog` (pass)<br>`TestCreateSubmitReplayVerifyObjectFormats` (pass) |
| 2. Rebinding attacks | pass | `TestIdempotencyConflict` (pass)<br>`TestSignedBindingFieldsCannotBeSwapped` (pass)<br>`TestSizeCeilingAndEnvelopeOnlyAdmissionHook` (pass)<br>`TestVerifierRejectsRebindingAndTrailerMutation` (pass) |
| 3. Nexus crash with live ephemera | pass | `TestCrashChangesGenerationAndOldCursorResets` (pass)<br>`TestNexusDoesNotTouchGit` (pass)<br>`TestRetainedFramesVerifyWithoutHub` (pass)<br>`TestSelfAssertedNexusKeyIsNotTrust` (pass) |
| 4. Unauthorized fetch across a domain | pass | `TestRepositoryIsTheHTTPReadBoundaryEvenForKnownOID` (pass) |
| 5. Snapshot/watch race | pass | `TestSnapshotWatchBarrierCannotMissTransition` (pass) |
| 6. Conflicting multi-log custody transition | pass | `TestMultipleCompletedSettlementsBecomeDisputed` (pass)<br>`TestThreeStepSagaAcrossSecurityDomains` (pass) |

## Findings

1. Cold processes rebuild head and idempotency from Git; a CAS loser retries into one signed chain.
2. Actor intent binds target, payload tree, causal trailers and idempotency identity.
3. A new nexus generation resets live state; retained participant copies remain independently attestable.
4. Repository-scoped smart-HTTP authorization denies fetch-by-known-hash across domains.
5. The snapshot cursor and state share one barrier; the next transition appears strictly after that cursor.
6. The saga branch leaves competing settlements unorderable but total: every event projects as disputed. An asset-owned log excludes that dispute by construction — evidence for the entity-log default.

Regenerate with `make spike` from the repository root.
