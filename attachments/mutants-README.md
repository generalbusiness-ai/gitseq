# Omission-sensitive controls

Fourteen mutants at the final head, one at a time, in a disposable copy taken
with `git archive HEAD` (never in the request worktree), each restored before
the next. `<name>.diff` is the applied change and `<name>.out` is the focused
test output with its exit code and the exact command that produced it.

| # | Mutant | Site | Named test that failed | Exit |
|---|---|---|---|---|
| 01 | The resolver stores what was typed instead of the canonical identifier | `internal/eventref/resolver.go`, `Resolver.One` returns `selector` | `TestShortEventReferencesResolveInBothObjectFormats` — all eight number/prefix/suffix/whole-hash cases in both formats, on the signed `rests_on` | 1 |
| 02 | The ambiguity check is removed, first match wins | `internal/eventref/eventref.go`, `Set.resolveHash` returns `matched[0]` | `TestAmbiguousHashReferenceRefusesAndNamesCandidates` — the act was admitted | 1 |
| 03 | The room identity is dropped from the resolver | `internal/eventref/eventref.go`, `Room.prefix` loses the genesis | `TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped`, and the short-prefix cases of `TestShortEventReferencesResolveInBothObjectFormats` | 1 |
| 04 | The standard-error echo is removed | `cmd/gs/eventref.go`, `showResolved` prints nothing | `TestShortEventReferencesResolveInBothObjectFormats`, `TestTargetsAcceptShortReferencesAndSignCanonicalIdentifiers` | 1 |
| 05 | The pre-signing basis disclosure is removed | `cmd/gs/eventref.go`, `discloseBases` prints nothing | `TestFilingDisclosesItsBasesBeforeSigning` — malformed, absent-local and external all fell silent | 1 |
| 06 | The external-citation distinction is removed | `internal/eventref/resolver.go`, `BasisNotes` reports an external identifier as an absent local one | `TestFilingDisclosesItsBasesBeforeSigning/external_identifier_is_named_as_external_and_admitted`, `TestAnotherWorkroomsEventIsNeverResolvedAndIsPreservedAsTyped` | 1 |
| 07 | Suffix matching is removed, prefixes only | `internal/eventref/eventref.go`, `Set.resolveHash` drops `HasSuffix` | `TestShortEventReferencesResolveInBothObjectFormats/*/suffix` and `internal/eventref` `TestSuffixResolves` | 1 |
| 08 | The prefix/suffix dedup is removed, one event counted twice | `internal/eventref/eventref.go`, `Set.resolveHash` appends once per match instead of once per event | `TestShortEventReferencesResolveInBothObjectFormats/*/whole_hash` (a whole hash is both) and `internal/eventref` `TestPrefixAndSuffixOfOneEventIsOneMatch` | 1 |
| 09 | A refusal is swallowed and the reference carried through | `cmd/gs/eventref.go`, `resolveRefs` continues instead of returning the error | `TestRecordNumberBoundariesRefuseWithoutAppending`, `TestAmbiguousHashReferenceRefusesAndNamesCandidates`, `TestHashOfAnUnrelatedGitObjectNamesNoEvent` — each act was admitted | 1 |
| 10 | One tool is dropped from the adapter's input inventory | `cmd/gitseq-mcp/eventref.go`, `ratify` removed from `eventReferenceInputs` | `cmd/gitseq-mcp` `TestToolsResolveWhatTheDisplaysShow` — the signed target was `"#22"` | 1 |
| 11 | `gs publish --basis` bypasses the resolver | `cmd/gs/main.go`, `publishCommand` calls `runPublication` directly | `TestPublishResolvesItsGoverningBasis` — the published fact signed `[#2]` | 1 |
| 12 | Batch labels are warned about again | `cmd/gs/main.go`, `chainCitations` stops filtering labels | `TestChainCitationsKeepsEverythingButLabels`, `TestBatchLabelsEarnNoBasisWarning` | 1 |
| 13 | A recognized typed body field bypasses the resolver | `internal/eventref/resolver.go`, `Resolver.Body` iterates an empty list | all three surfaces: `cmd/gs` `TestRecognizedBodyFieldsResolveAndSignCanonicalIdentifiers` (signed `body.authorizes_request` = `"#3"`), `TestBatchEntryBodiesResolveTheirRecognizedFields` (`"#4"`), `cmd/gitseq-mcp` `TestStateToolResolvesRecognizedBodyFields` | 1 |
| 14 | The MCP refusal path drops the disclosure | `cmd/gitseq-mcp/main.go`, `dispatch` returns the bare error | `cmd/gitseq-mcp` `TestStateToolDisclosesItsBases` — "the refusal dropped the disclosure" | 1 |

Note on mutant 03. Cross-room resolution is structurally impossible here: the
searched set is one workroom's own verified projection, so no mutation of the
resolver can make a number or a fragment reach another room's log. The mutation
therefore targets the one place the room's identity enters the resolver, and the
controls that fire show it is load-bearing in both directions.

Note on 07 and 08. The first run of these two named two packages in one quoted
word, so `go test` failed to resolve the path and exited 1 without running
anything. An aborted run is not a measurement, and a red exit code from a setup
failure looks exactly like a killed mutant. The driver now word-splits the
package list, and the outputs recorded here are from the re-run, which names the
failing assertions.

Every output was checked for a real named failure rather than a red exit code:
each records between one and twelve `--- FAIL` lines and none records a setup
or build problem.

The copy was verified byte-identical to the head after the last restore and then
removed, before any gate ran.
