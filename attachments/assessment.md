# Bounded assessment: oversized attachments (request #22093)

Read-only. Source read at main `d70d8fe6a160a470a75cf613f0075b7695ef5c08` in the
worktree `oversized-attachment-disposition`; durable record from the resident at
`127.0.0.1:7777`. No tests run, nothing edited.

## 1. The ceiling

All four workrooms declare `payload_ceiling = 1,048,576` bytes (1 MiB) and
`object_format = sha1`. The ceiling covers the signed envelope, the inline payload and
every attachment together, not each separately.

| Role | Site |
|---|---|
| Enforcement | `internal/kernel/kernel.go:471` `validateRequestSize`, from `Submit` at `kernel.go:581` |
| Exported preflight | `internal/kernel/kernel.go:379` `ValidateRequestSize` |
| Genesis field | `internal/kernel/kernel.go:51`; set at `internal/app/app.go:814` |
| Reader re-check | `kernel.go:1507,1524,1545`; `internal/kernel/checkpoint.go:681,797,836,1325` |
| Documented | `docs/reference/limits.md:31` (table row), `:33-38` (prose) |

The accounting at `kernel.go:471-489` charges envelope, then payload, then each
attachment, refusing as soon as the running total would pass. Every refusal returns
the same string, `event exceeds genesis ceiling` — no attachment name, no size, no
ceiling value, no remedy. Unchanged since #7447 criticised it. A second, looser bound
sits at `internal/service/server.go:48`, `SubmissionRequestLimit = 2 MiB` on the
resident JSON body, checked at `server.go:906`. Go base64-encodes attachment bytes, so
1 MiB raw becomes about 1.4 MiB of JSON. The kernel ceiling binds on every path today,
but by only about 1.5x, and this cap is absent from `limits.md`.

## 2. Input surfaces

| Surface | Site | Behaviour at the ceiling |
|---|---|---|
| `gs state --evidence name=path` | flag `cmd/gs/main.go:507`; read `:532` -> `files()` `:3317`; act `:563` | Reads any file, **no size check**. Signs, submits, then the kernel refuses. |
| `gs batch` | `cmd/gs/main.go:2112` `kernel.ValidateRequestSize`; `:2116` resident cap | Refused before append, with batch position. Same opaque message. |
| MCP `state`, `evidence` object | schema `cmd/gitseq-mcp/main.go:732`; conversion `:1351-1360` | No size check. Signs, submits, kernel refuses. |
| Resident `/v0/act`, `evidence` map | `internal/service/ui.go:174,193-197,211` | Body capped at 2 MiB by `decode()` (`server.go:898`); otherwise unchecked. |
| Merge-plan signing | `internal/mergeplan/mergeplan.go:1361,1364` | Preflighted against both ceilings first. |

All five converge on `internal/app/app.go:1955` `signRequest`, whose comment calls it
the only place a submission is signed. It hashes the attachment map at `app.go:1961`
(`gitstore.HashPayloadTree`) and signs the tree OID at `:1966-1970`, with **no size
check and no conversion**. Attachment names are validated only by
`internal/gitstore/gitstore.go:27`, `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. So
#7447's "the path is genuinely reachable" still holds, and three of the four
author-facing surfaces still pay for signing before learning they are refused.

## 3. Measured demand

Method: walked the whole `refs/seq/<genesis>` in each repository with `git rev-list`,
read every `<commit>:attachments` tree with one batched `git cat-file --batch`, sized
every blob with `--batch-check`. "Event total" is the kernel quantity: signed envelope
+ `event` blob + all attachments (bytes). Driver and data: `work/measure.py`,
`work/measure.json`.

| Workroom | Depth | Events w/ attach | Attach | Per-attachment p50/p90/p99/max | Event total p50/p90/max | Max % of 1 MiB |
|---|---|---|---|---|---|---|
| gitseq | 22,109 | 384 | 1,269 | 1,222 / 9,796 / 60,230 / 176,067 | 8,641 / 38,956 / 196,373 | 18.7% |
| tailapp | 4,280 | 135 | 385 | 1,343 / 7,793 / 50,056 / 498,733 | 7,732 / 26,029 / 535,397 | **51.1%** |
| gitseq-chess | 843 | 11 | 41 | 2,371 / 7,884 / 43,938 / 59,586 | 11,636 / 46,464 / 86,436 | 8.2% |
| gitseq-inventory | 514 | 13 | 30 | 2,024 / 20,797 / 22,943 / 23,046 | 12,545 / 30,205 / 31,972 | 3.1% |
| **Combined** | 27,746 | **543** | **1,725** | 1,279 / 9,096 / 60,225 / 498,733 | max 535,397 | 51.1% |

Events by fraction of the ceiling, all four workrooms: **over 10%: 15; over 25%: 3;
over 50%: 1; over 90%: 0; over 100%: 0.** Attachments per event: mean 3.18, max 27 —
the oversize risk is many mid-sized files, not one big one, so aggregate accounting
matters. Largest ten attachments:

| Size | Workroom | Seq | Commit | Name | Event total |
|---|---|---|---|---|---|
| 498,733 | tailapp | 3923 | a897caf1ec10 | reference-input-manifest.json | 535,397 |
| 311,164 | tailapp | 3955 | a790a35d5830 | input-manifest.json | 367,649 |
| 176,067 | gitseq | 18730 | ab2de6667175 | w1-current-owner-audit-manifest.json | 186,016 |
| 145,236 | gitseq | 18959 | d8d3cdbf09ee | observations.json | 153,540 |
| 92,223 | gitseq | 19712 | 3ddba9956ba6 | source-provenance.json | 189,476 |
| 92,223 | gitseq | 19678 | 962a2a1be3be | source-provenance.json | 196,373 |
| 92,223 | gitseq | 19677 | e67e2a4d042d | source-provenance.json | 192,213 |
| 92,223 | gitseq | 19669 | f0718ce202b2 | source-provenance.json | 191,368 |
| 72,337 | gitseq | 20547 | 49d896c822f2 | cleanup-before.json | 77,151 |
| 63,142 | gitseq | 19937 | 346db088a81d | doc-gates.log | 136,225 |

**The trend is the finding.** #7447 measured this log at depth 7,439: 104 records with
attachments, largest event total 53,868 (5.1%), "a factor of nineteen of headroom".
Now in gitseq at depth 22,109: 384 records, largest 196,373 (18.7%), headroom 5.3x.
Across the fleet: 543 records, largest 535,397 (51.1%), **headroom 2.0x**. Within
gitseq the growth sits in the newest third (seq 14,740+): 260 of the 384
attachment-bearing events, p90 49,258 against p90 near 4,000 in the earlier two
thirds. The fleet's three largest events were written on 5-6 September 2026.

**Implication.** Nobody has hit the ceiling: zero refusals in 27,746 events, and no
request, document or code comment asks for a larger attachment. But #7447's second
fact — "nobody is near the ceiling" — no longer describes the world. One event is past
half, and the busiest workroom's headroom fell from 19x to 2x in about five weeks. On
that slope a refusal is plausible within the current working style, and it would land
on `gs state --evidence` or MCP `state`, which sign first and then report the opaque
message. **No refusals appear in any log:** `gitseq/.tmp/resident.log` is 318 bytes
and mentions no ceiling, the two "ceiling" hits in
`tailapp/.git/gitseq/builder-loop.log` are documentation text, and no other resident
logs exist under the four repositories or `~/Library/Logs`.

## 4. How a reader would see an inert descriptor today

#7447's "invisible on arrival" premise no longer holds. Attachment bytes are
still dropped from the folded record at `internal/workroom/fold.go:570`
(`record.Attachments = nil`; `:567` calls them transport material), but the resident
now serves them from git on demand.

- **Preview.** `internal/service/preview.go:108-120` reads `attachments/<name>`
  at the event commit and returns content plus a line window; `:113-116` lists sibling
  names with an `omitted` count; `:99,103` keep one request to one object; `:109`
  validates the name (`ValidPreviewPath`).
- **UI.** `ui/src/components/Preview.tsx:104-116,153,168-172` renders the
  evidence list, titles the pane and renders content (markdown as markdown);
  `RecordDetail.tsx:43-47,86` puts an `evidence` row on every record;
  `ui/src/lib/fileReferences.ts:16-21` resolves a bare token in a record's text to that
  record's attachment.
- **`gs inspect`.** No attachment listing; nothing under `cmd/gs` reads attachment bytes back, so the CLI reading path is unchanged from #7447.

A descriptor shipped as `evidence.json.locator` would therefore today be **listed and
openable in the UI and via `/v0/preview`, and invisible from the CLI**, rendered as
raw JSON with nothing saying the named bytes are not held. That, not invisibility, is
the honest-custody risk now.

## 5. Smallest useful option under each disposition

### A — locator required, no implicit custody (#5075 as written)

- Versioned descriptor written as an attachment named `<name>.locator`, legal
  under the existing name regex, so payload tree, `workroom` and fold are untouched.
  Fields: `version`, `name`, `size`, `digest` (`git:<object_format>:<hex>`, the typed
  form `app.go:1966-1968` already uses, from the existing `hashObject`), `locator`
  (opaque string), `custody: "none"`.
- Meaning to state in `limits.md`: the digest is a git blob OID under the
  workroom's object format; it binds, it does not retrieve. On **sha1** — all four
  workrooms — nothing re-reads the object, so collision detection does not apply and the
  binding is weaker than "content-addressed" suggests; sha256 is stronger. Gitseq stores
  no bytes and never fetches the locator.
- No locator supplied: refusal stays total, with B's improved message. Invalid
  or colliding names: refuse if `<name>.locator` fails the regex or is already present;
  no silent rename. Multiple attachments: convert largest-first until the whole request
  fits, charging each descriptor's own bytes, else refuse.
- Idempotency: capture the key **once** before the first `intent.Sign`
  (`app.go:1962-1967` mints a random key when none is given), then sign, measure with
  `kernel.ValidateRequestSize`, degrade, re-sign under the same key. Otherwise a retry
  mints a second key and retry safety is gone.
- Reading surface: preview and UI must say the bytes are not held; descriptors
  must never read as verified content.

Files: `internal/app/app.go`, a wrapper in `internal/gitstore`, `cmd/gs/main.go`
(`--evidence-locator`), `cmd/gitseq-mcp/main.go` (`evidence_locator`),
`internal/service/ui.go`, `internal/service/preview.go`,
`ui/src/components/Preview.tsx`, `docs/reference/limits.md`, `gs/state.md`,
`mcp/state.md`, `gs/verify.md:69` (it claims verification confirms payload sizes are
within the ceiling), `notes/2026-08-05-gitseq-design.md:113-115`. Tests and omission
controls: boundary conversion; refusal with no locator; aggregate multi-attachment
conversion; one-key-through-re-sign; a no-fetch control asserting no network call;
invalid and colliding names; unchanged behaviour below the ceiling. Each guard needs a
mutation that turns it red. Risks: a signed record that can outlive the bytes it
names, on sha1 workrooms where the digest binds weakly; three CLIs, one MCP schema and
one HTTP surface widened; and the re-sign step is the riskiest change in the most
safety-critical function in the codebase. Size: roughly 400-700 lines across ~13
files, plus docs.

### B — authorized narrowing keeping the hard ceiling

- Keep `validateRequestSize` exactly as it is. Replace the bare
  `event exceeds genesis ceiling` with a diagnostic naming the ceiling, the measured
  total, the envelope/payload/attachment split, the largest attachment by name, and the
  remedy (shrink, split across events, or cite a repository path).
- Add the preflight the batch path already uses to the single-act path, so
  `gs state --evidence`, MCP `state` and `/v0/act` refuse **before** signing — a call to
  the already-exported `kernel.ValidateRequestSize`.
- Correct `notes/2026-08-05-gitseq-design.md:113-115`, the only place in the
  repository claiming the degrade; state the hard bound and stamp the resolution. Add
  the resident 2 MiB cap and base64 expansion to `limits.md`, and correct
  `docs/reference/gs/verify.md:69`.
- Record the ratifier's actual abandonment of conversion in the durable log.

Files: `internal/kernel/kernel.go`, `cmd/gs/main.go`, `cmd/gitseq-mcp/main.go`,
`internal/service/ui.go`, `notes/2026-08-05-gitseq-design.md`,
`docs/reference/limits.md`, `docs/reference/gs/verify.md`. Tests and omission
controls: refusal message content at each boundary (envelope-only, payload-only,
single attachment, aggregate); refusal-before-signing on each of the three unchecked
surfaces, each with the preflight mutated out to prove the test is red; unchanged
admission below the ceiling; retry identity unaffected because nothing re-signs.
Risks: low — the residual one is that if demand keeps climbing at the observed rate
the hard refusal will be met sooner than the narrowing assumes, and reopening costs a
second decision. Size: roughly 150-250 lines plus docs.

## 6. #5075, and what its staleness means

**#5075 is live and unretired.** The projection shows no `retired` field, `ratified:
true`, and two `ratify` acts by `7fbc80f1ba06` (hugh), verdict `effective`, at
1786643537 and 1786643572. It adopted option 1 of `b72e4767`: an attachment may exceed
the ceiling only when the submitter supplies a locator hint; nothing stored
implicitly; the log never implies custody it does not have; implemented in
`internal/app` before signing; the no-locator case keeps today's refusal with a better
message. Option 2, rejected, anchored degraded blobs under a ref of the repository's
own.

**It is stale, and so is everything downstream.** #5075 is stale because `b72e4767`
(#3900), the request it answers, is **retired**. That propagates:
#20664 (retired), #20678, request #22093 itself and promise #22103 all carry
`stale_because` pointing at that same retired `b72e4767`, so the current-basis
replacement inherited the staleness it was filed to shed. Consequences for the
proposal. Do not rest it on `b72e4767`; it is retired, so that would need the
dead-basis override and would be a withdrawn pointer. Resting on #5075 is admitted but
records staleness. Either way SKILL.md:463-466 requires the ordinary
`propose`-and-ratify path, because the authority-bearing request chain fails its
fourth fact (the chain is stale). Rest the proposal on **current** bases: the live
artifacts for `internal/kernel`, `internal/app`, `internal/service` and `docs`, plus a
note carrying this measurement. Planner cannot ratify as hugh; ratifier-role holders
visible in the log are `7fbc80f1ba06` (hugh), `5f12e916d136` (codex) and
`a5d35aa7e479`.

**What changes from #5075 under B.** Its operative sentence — an attachment may exceed
the ceiling with a locator — is withdrawn. Everything else survives: nothing stored
implicitly, no implied custody, option 2 still rejected, and the better refusal
message, which #5075 already required and which has never shipped. B is less a
reversal than a finding that the locator half is not worth its surface, keeping the
half that is.

## 7. Recommendation

**Propose B, the authorized narrowing, as the single ratifiable outcome.** Zero
refusals in 27,746 events across four workrooms, and no user, request, document or
code comment asks for a larger attachment: A is a feature for a case nobody has met.
The two things that would have helped anyone in that log are both in B and neither
needs a locator — an actionable message, and refusing before signing on the three
surfaces that today sign first. A's central artefact is a signed record naming bytes
Gitseq does not hold, on sha1 workrooms where the digest binds weakly, shown by a
preview surface with no vocabulary for "not held": the implied-custody problem #5075
exists to avoid, reintroduced at the reading surface. And simplicity beats features —
B deletes a false claim and makes the documents agree, where A adds a schema, a flag,
an MCP field, an HTTP field and a re-sign step inside the one function that signs
everything.

The counterweight belongs in the proposal: **the headroom argument #7447 rested on has
largely gone** — 19x became 2x in five weeks, recently and concentratedly. B is right
for the world as measured, not one at four times this volume. So the narrowing should
carry, as conditions rather than a second outcome, that the refusal message be good
enough to make the next person's decision easy, and that `limits.md` record the
measured distribution so a future reader sees the slope.

Kept **outside** the one ratifiable outcome, as explanatory alternatives: disposition
A in full (section 5 records what was costed and declined); option 2 of `b72e4767`,
already rejected by #5075 and not reopened; raising `payload_ceiling`, which needs a
new workroom and is excluded by the request; making `gs inspect` read attachments
back, a separate reading-surface outcome needing its own request; and any wider
treatment of the resident 2 MiB cap beyond the one `limits.md` line B adds.
