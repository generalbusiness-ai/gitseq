# Per-file summary of the diff over main a6ed642f

## New

| File | What it is |
|---|---|
| `internal/eventref/eventref.go` | The resolver's whole vocabulary: `Room.Form` classifies one typed string into canonical, external, number, hash or opaque without reading the log; `Membership` indexes the events one projection holds; `Set` adds the sequence and hash indexes and resolves a number or a fragment; `Refusal` renders an ambiguity with bounded `#N identifier` candidates, or a no-match with where to look. |
| `internal/eventref/resolver.go` | `Resolver`, one per act: it reads the event set at most once and only when a typed reference needs it, keeps the resolution sentences, and `BasisNotes` produces the three pre-signing citation sentences both surfaces show. |
| `internal/eventref/eventref_test.go` | Unit controls: both object formats, actor fingerprints, the `#N` boundaries, one event matched by prefix and suffix at once, bounded ambiguity, no match, a foreign identifier preserved, the three basis notes, the read-at-most-once and read-only-when-needed properties, and a refusal when the event set cannot be read. |
| `cmd/gs/eventref.go` | The command line's helpers: `newResolver` (lazy, from `roomOf` and `snapshotWithProgress`), `newResolverFrom` (over a snapshot the command already folded), `showResolved` (standard error, before signing), `resolveRefs` (rewrites single and repeatable references in place), `discloseBases`, plus `noteDeadRestsOn` moved here unchanged. |
| `cmd/gs/event_reference_test.go` | The command-line controls, driven through the installed binary so the two streams and the exit status are real: both object formats, record-number boundaries, ambiguity, a blob prefix, cross-workroom, targets, batch, and the pre-signing basis disclosure. |
| `cmd/gitseq-mcp/eventref.go` | `eventReferenceInputs`, the whole per-tool inventory in one table; `resolveToolReferences` rewrites the call's own arguments and returns the pre-signing notes; `annotateReferences` puts both into the result. |
| `cmd/gitseq-mcp/event_reference_test.go` | The adapter controls and the command-line/adapter parity test, which builds `gs` and compares what each surface signed and what each said. |

## Changed

| File | What changed |
|---|---|
| `cmd/gs/main.go` | Resolution added at eleven command boundaries (`state`, `supersede`, `ratify`, `reassign-if-unclaimed`, `review`, `merge`, `merge-plan`, `inspect`, `provenance`, `batch`, `publish`); the filing verbs disclose their bases before signing; `chainCitations` and `isBatchLabel` keep intra-batch labels out of that disclosure and `batchLabelPrefix` gives the marker one spelling; `noteDeadRestsOn` moved to `eventref.go` unchanged; the `inspect` refusal now names the accepted forms. |
| `cmd/gitseq-mcp/main.go` | `dispatch` resolves every tool call's references and annotates the result, with the previous body renamed `dispatchResolved`; the local `resolves` helper replaced by the shared `eventref.Membership`; `projectionNotes` otherwise unchanged, including `unresolved_rests_on` and `dead_rests_on`. |
| `cmd/gitseq-mcp/review_test.go` | One acknowledgment control used a bare head commit hash as a stand-in for "not an event". A bare event hash now resolves, so the control names the artifact instead, which is a real event and still not head news. |
| `SKILL.md` | The two citation bullets: a short form may now be typed and is resolved before signing; `Rests-On:` trailers still take the full identifier. |
| `docs/reference/event-identifiers.md` | The accepted input forms, the searched population, the refusals, the growth property, the cross-workroom rule, the basis disclosure table, the widened where-it-is-used list, and the non-identifier table. |
| `docs/reference/architecture.md` | Layer 7 gains **Event reference input** and **Basis disclosure**; the MCP bullet names its one input table; `internal/eventref` joins the package-boundary table. |
| `docs/reference/gs/*.md`, `docs/reference/mcp/*.md`, `docs/how-to/end-to-end.md` | Per-command and per-tool guidance: paste what the display shows, and what is never resolved. |


## Changed again after the independent review

| Defect | Change |
|---|---|
| 1 | `publishCommand` resolves and discloses `--basis` before `runPublication` reads the remote frontier; `docs/reference/gs/publish.md` and the inventory say so; the audit method is recorded. |
| 2 | `chainCitations` keeps `$label` out of the batch disclosure; `batchLabelPrefix` and `isBatchLabel` give the marker one spelling, reused by `resolveLabel`. |
| 3 | `Membership.Unresolved` restored to main's exact shape — no dedup, no empty-string skip — so `unresolved_rests_on` is byte-identical to main, with a test that pins it. |
| 4 | The `Resolver` doc comment now says what is true: a read command handed a canonical identifier reads nothing, and a filing verb pays for the set once because it asks for it. |
| 5 | The cross-room number case files an in-range `#3` and asserts the signed identifier is this room's record at position 3. |
| 6 | The pasted ten-page paragraph is gone; `event-identifiers.md#typing-one-at-a-boundary` is the one statement and every page names its own inputs in a sentence or two. The read-only pages (`gs inspect`, `gs provenance`, `gs merge-plan`, MCP `inspect`, MCP `merge_plan`) no longer speak of signing or appending, and say they record nothing. |
| 7 | `provenanceCommand` uses `newResolverFrom(workspace, snapshot)`; `roomOf` is the one place the room is built. |
| 8 | `gs review`'s exclusion from `discloseBases` is stated in the inventory, the architecture contract and `event-identifiers.md`, with the reason: the review guard already judges every citation and refuses what does not stand. |


## Changed again after the second review (verdict 633a7890)

| Defect | Change |
|---|---|
| 1 | `eventref.EventBodyFields` names the six body fields a consumer reads as one durable event, with the consumer for each in the doc comment; `Resolver.Body` rewrites exactly those, through the same per-act resolver, on `gs state --body`, every `gs batch` entry body, `gs reassign-if-unclaimed --body`, and the MCP `state` and `reassign_if_unclaimed` bodies. `authorizes_candidate`, the receipt's Git heads, the JSON plan documents and all other prose stay untouched. The inventory now states the six with file:line consumers and says plainly that its earlier claim was wrong and why. |
| 2 | `dispatch` wraps a failed call in `referenceDisclosure`, which carries the resolutions and the basis notes, wraps the original error and renders them into `Error()`; the transport emits the refusal as the first content block and each sentence as its own block after it. `refusalText` keeps the reason itself unchanged. |
| 3 | The duplicated paragraphs in `gs/review.md` and `gs/supersede.md` are gone. "reads and records nothing" corrected to "records nothing" in `gs/provenance.md`, `gs/merge-plan.md`, `mcp/inspect.md` and `mcp/merge_plan.md` — these commands read; recording is what they do not do. |
