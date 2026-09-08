# Test names per required control

Twenty-nine tests over three packages. The command-line tests drive the
installed `gs` binary so the two streams and the exit status are real; the
adapter tests drive the real tool dispatcher through the package's own signed
fixtures.

## Deliverable A

| Required control | Test |
|---|---|
| SHA-1 and SHA-256 rooms | `cmd/gs` `TestShortEventReferencesResolveInBothObjectFormats` (subtests `sha1`, `sha256`); `internal/eventref` `TestFormReadsBothObjectFormats` |
| `#1`, `#depth` | `cmd/gs` `TestRecordNumberBoundariesRefuseWithoutAppending` (first, last); `internal/eventref` `TestNumberBoundaries` |
| `#0`, `#depth+1`, non-numeric `#x` | `cmd/gs` `TestRecordNumberBoundariesRefuseWithoutAppending` (zero, beyond, non numeric, bare, padded); `internal/eventref` `TestNumberBoundaries`; `cmd/gitseq-mcp` `TestReferenceRefusalsAppendNothing` |
| Prefix collision between two events, ambiguous refusal listing both | `cmd/gs` `TestAmbiguousHashReferenceRefusesAndNamesCandidates`; `internal/eventref` `TestAmbiguityRefusesAndBoundsItsCandidates` |
| Prefix matching an unrelated blob but no event | `cmd/gs` `TestHashOfAnUnrelatedGitObjectNamesNoEvent` |
| Another room's event as `#N` or a short hash: no match | `cmd/gs` `TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped` |
| Another room's event as a canonical id: preserved external | same test; `cmd/gitseq-mcp` `TestReferenceRefusalsAppendNothing`; `internal/eventref` `TestForeignCanonicalIdentifierIsPreserved` |
| Batch entries using `#N` and short hashes, `$labels` still working | `cmd/gs` `TestBatchResolvesShortReferencesAndKeepsItsLabels` |
| CLI and MCP resolving the same selectors to the same ids | `cmd/gitseq-mcp` `TestCommandLineAndAdapterResolveTheSameReferences` |
| Assertions on the actual signed canonical payload | every test above that files: the assertion reads `Projection.Provenance[event]` and `Projection.Acts[].Target`, both decoded from the signed record, never the command's echo |
| No append after each refusal | `TestRecordNumberBoundariesRefuseWithoutAppending`, `TestAmbiguousHashReferenceRefusesAndNamesCandidates`, `TestHashOfAnUnrelatedGitObjectNamesNoEvent`, `TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped`, `TestBatchResolvesShortReferencesAndKeepsItsLabels`, `cmd/gitseq-mcp` `TestReferenceRefusalsAppendNothing` — each compares depth before and after |
| Deduplicating one event matched by prefix and suffix | `internal/eventref` `TestPrefixAndSuffixOfOneEventIsOneMatch`; `cmd/gs` `TestShortEventReferencesResolveInBothObjectFormats/whole_hash`, where the whole hash is both |
| Suffixes are first-class | `internal/eventref` `TestSuffixResolves`; `TestShortEventReferencesResolveInBothObjectFormats/suffix` |
| Targets and read selectors | `cmd/gs` `TestTargetsAcceptShortReferencesAndSignCanonicalIdentifiers`; `cmd/gitseq-mcp` `TestToolsResolveWhatTheDisplaysShow` (state, ratify, inspect) |
| The resolution is shown, and standard output is not corrupted | `TestShortEventReferencesResolveInBothObjectFormats` asserts one line on standard output and the sentence on standard error |
| Fingerprints, heads, handles and prose are not reinterpreted | `internal/eventref` `TestActorFingerprintIsNeverResolvedToAnEvent`, `TestFormReadsBothObjectFormats` (prose, batch label, session handle, uppercase) |
| One frontier, read once, and only when needed | `internal/eventref` `TestResolverReadsTheEventSetAtMostOnceAndOnlyWhenNeeded`, `TestResolverRefusesWhenTheEventSetCannotBeRead` |

## Deliverable B

| Required control | Test |
|---|---|
| Malformed basis warns and the act lands | `cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/malformed_basis_warns_and_lands`; `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases` |
| Canonical unknown local basis refuses, no append | `cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/local_identifier_naming_no_event_is_warned_then_refused`; `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases` — both assert the kernel's own text and unchanged depth |
| Valid quiet control | `cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/live_basis_is_quiet` (empty standard error) and `.../the_genesis_basis_is_quiet`; `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases` (no `basis_notes`); `internal/eventref` `TestGenesisCitationIsAdmittedQuietlyAndHasNoShortForm` |
| External unresolved distinction | `cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/external_identifier_is_named_as_external_and_admitted`; `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases`; `internal/eventref` `TestBasisNotesDistinguishTheThreeCases` |
| stderr/stdout and MCP-result agreement | `cmd/gitseq-mcp` `TestCommandLineAndAdapterResolveTheSameReferences` compares the exact standard-error line with the adapter's `basis_notes` entry, and the signed citations of both surfaces |
| The MCP disclosure already on main is preserved | `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases` asserts `projected.unresolved_rests_on` beside the new field; `TestProjectionNotes*` in `main_test.go` unchanged |
| The ineffective-support and dead-basis disclosure is preserved | `cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/a_retired_basis_still_earns_its_own_note_after_the_act_lands`; existing `internal/app` and `internal/workroom` dead-basis tests unchanged |

## New controls added after the independent review

| Defect | Test |
|---|---|
| `gs publish --basis` reached a signed `rests_on` unresolved and undisclosed | `cmd/gs` `TestPublishResolvesItsGoverningBasis` — a `#N` basis resolves and the published fact signs the canonical id; `#0` refuses with the depth and the remote frontier both unchanged |
| `gs batch` warned about every `$label` on the ordinary path | `cmd/gs` `TestBatchLabelsEarnNoBasisWarning` (and its second half: a real unresolvable basis beside a label still warns), `TestChainCitationsKeepsEverythingButLabels`, `internal/eventref` `TestBasisNotesCallALabelWhatItIs` |
| `unresolved_rests_on` changed shape | `internal/eventref` `TestUnresolvedMirrorsTheCitationsAsWritten` — duplicates kept, empty string kept, order preserved |
| the cross-room number case proved only out-of-range | `cmd/gs` `TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped` now files `#3`, in range in both rooms, and asserts the signed id is this room's record at position 3 |
| the parity test compared whole streams | `cmd/gitseq-mcp` `TestCommandLineAndAdapterResolveTheSameReferences` compares the disclosure lines through `basisNoteLines`, so a slow cold fold's progress line cannot turn it red; proved non-vacuous by removing the command line's disclosure in a disposable copy and watching it fail |

## One thing the first full run caught

The pre-signing warning was first written against the fold's membership answer
— "does the projection hold a record for this identifier" — and that is not the
predicate the sequencer refuses on. The workroom's own genesis,
`git:<fmt>:<genesis>#git:<fmt>:<genesis>`, is the root of the sequence and is in
the log the kernel resolves against, but it is not an application record, so the
fold projects no decision for it. Four existing `cmd/gs` tests failed on the
false alarm that produced: `TestStateRefusesUndefinedKindsAndListsTheVocabulary`,
`TestStateRefusesADeadRestOnBasisUntilTheOverrideSignsIt` (which names it "the
living genesis basis" in so many words),
`TestBatchProcessReadsItsFileAndReportsFailures/positional_file`, and — for a
different reason, the deliberately changed refusal text —
`TestInspectRefusesAnEventTheLogDoesNotHold`.

`eventref.Set.Resolvable` now answers the kernel's question and
`Membership.Has` the fold's; they differ by exactly the genesis, and the
projection notes the MCP adapter already made still read from the fold's, so
`unresolved_rests_on` is unchanged. Three controls pin it:
`internal/eventref` `TestGenesisCitationIsAdmittedQuietlyAndHasNoShortForm`,
`cmd/gs` `TestFilingDisclosesItsBasesBeforeSigning/the_genesis_basis_is_quiet`,
and the four repaired tests above.


## A second thing, caught by the reviewer

`gs publish --basis` is signed straight into the `rests_on` of every
publication fact, and it was the one event-reference input the first pass
missed. No mutant catches an input that was never wired up — a mutation can
only break code that exists — so the guard is the audit, and the audit is now
recorded in `input-inventory.md` as a method rather than a conclusion: list
every `app.Act{`, trace every `RestsOn`/`Target`/`Retirement` back to its
source, then classify every flag and every tool-schema key. Re-run at this head
it finds nothing else.
