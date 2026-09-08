# Event-reference input inventory

Every input of `cmd/gs` and `cmd/gitseq-mcp` that carries a durable event
reference, how it is handled, and what is deliberately left alone.

## Resolved (canonical, `#N`, or an unambiguous event-hash prefix/suffix)

### cmd/gs

| Command | Input | Site |
|---|---|---|
| `state` | `--rests-on` (repeatable) | `stateCommand`, before `submitAct` |
| `supersede` | positional target, `--rests-on` | `supersedeCommand` |
| `ratify` | positional target | `ratifyCommand` |
| `reassign-if-unclaimed` | positional old request, `--rests-on` | `reassignIfUnclaimedCommand`, before the guarded pair |
| `review` | `--artifact` (repeatable), `--promise`, `--ack-head-news` (repeatable), `--implementation` (repeatable), `--self-initiated` | `reviewCommandWithValidator`, before `reviewguard.CheckCitations` |
| `merge` | `--approval`, `--authorization` | `mergeCommand`, before the merge lock |
| `merge-plan` | `--approval` | `mergePlanCommand` |
| `inspect` | positional event | `inspectCommand`, before the resident is asked |
| `provenance` | positional event | `provenanceCommand`, from the snapshot it already folded |
| `batch` | every entry's `target`, `retirement`, `rests_on` | `batchCommand`, before `checkBatch` and the first append |
| `publish` | `--basis` | `publishCommand`, before `runPublication` reads the remote frontier |

### cmd/gitseq-mcp

One table, `eventReferenceInputs`, read by `dispatch` before any tool runs.

| Tool | Inputs |
|---|---|
| `inspect` | `event` |
| `merge_plan` | `approval` |
| `state` | `rests_on` |
| `review` | `promise`, `self_initiated`, `artifacts`, `ack_head_news`, `implementations` |
| `ratify` | `target` |
| `supersede` | `target`, `rests_on` |
| `reassign_if_unclaimed` | `old_request`, `rests_on` |

## Not resolved, and why

| Input | Why |
|---|---|
| `gs merge --candidate`, `merge_plan` `candidate` | an ordinary Git commit, not an event |
| artifact `body.commit`, approval `body.head` | Git commits |
| `gs review --checkout`, `gs merge --checkout` | filesystem paths |
| `gs attach --genesis`, `--remote` | a genesis hash and a Git remote |
| request `body.to` | an actor name, `@name` or fingerprint |
| `work` `target_ref`, request `body.target_ref` | Git refs |
| `say` `about`, `re`, `conversation`; `ack` `threads` | ephemeral handles; not durable, and outside adopted d54de200, which names `rests_on`, `target` and the read verbs |
| `$label` in a `gs batch` file | names an act the batch has yet to mint; it passes the resolver untouched and is kept out of the basis disclosure, because it is the one reference in a batch file that is always correct |
| `gs publish --remote`, `--ref`, and the accepted head | ordinary Git |
| body values outside the recognized list below | free-form application fields; reinterpreting them would resolve prose |
| `authorizes_candidate`, `merge_candidate`, `merge_head`, `merge_target_pre_head` | Git commits |
| `merge_retirements`, `merge_successors`, `merge_changed_paths`, `merge_left_live` | JSON documents, not one identifier, composed by the merge path from canonical values |
| `admitted_by` | written by the GitHub connector and read back by nothing |
| `basis` in a kind definition | constrains kinds, not events |
| reserved body fields `review_frontier`, `head_news_acknowledged` | stamped by the review guard from resolved input, never caller input |
| `Rests-On:` commit trailers | a commit message is no boundary anything resolves at |
| resident HTTP routes (`/v0/*`) | program-to-program; callers already hold canonical identifiers |

### Recognized body fields, which are resolved

Six body fields are read by a consumer as exactly one durable event, so they
are resolved with the rest of the act on every surface that writes a body —
`gs state --body`, a `gs batch` entry, the MCP `state` and
`reassign_if_unclaimed` tools:

| Field | Consumer | Read as |
|---|---|---|
| `artifact` | `internal/statusview/reviews.go:150,158`, `internal/statusview/query.go:431,562`, `internal/app/app.go:1797` | the artifact a request names, and the one an approved report must rest on |
| `authorizes_request` | `internal/workroom/landing.go:470` | the held landing request a release lifts |
| `authorizes_approval` | `internal/workroom/landing.go:515` | the approval that release covers |
| `merge_approval` | `internal/workroom/fold.go:1612,3885`, `internal/mergeplan/mergeplan.go:1433` | the approval a receipt was sealed on |
| `merge_authorization` | `cmd/gs/succession.go:173` | the release or authorization it carried |
| `merge_authorization_ratification` | `cmd/gs/succession.go:176` | that authorization's ratification |

An earlier version of this inventory claimed no caller-supplied body field held
an event identifier. That was wrong, and the way it was wrong is worth keeping:
it was derived from what the fields are called rather than from what reads
them. `body.target` does take only the literal `inherit`, and `body.artifact`
on a guarded approval is stamped by the review path — but `--body artifact=`,
`--body authorizes_request=` and `--body merge_approval=` are all writable by
hand, and the fold and the projection read every one of them as an event.

## Where the basis disclosure runs, and where it deliberately does not

`gs state`, `gs supersede`, `gs reassign-if-unclaimed`, `gs publish`,
`gs batch`, and the MCP `state`, `supersede` and `reassign_if_unclaimed` tools
describe their citations before signing. Two things are outside it on purpose.

**Batch labels.** A `$label` names an act the same chain has yet to mint, and
`runBatch` replaces it with that act's real identifier before signing. It is
not a citation the disclosure can say anything true about, so `chainCitations`
filters it out. Saying "not an event identifier" about it would put a false
alarm on the ordinary path, about the one reference in a batch file that is
always correct.

**`gs review` and the MCP `review` tool.** Their citations are resolved by the
shared resolver like every other input, but they are not passed to
`discloseBases`. `reviewguard.CheckCitations` and the three confirming reads
already judge every one of them against the projection — live, standing at the
exact head, the primary first — and refuse what does not stand. A weaker
sentence beside a stronger refusal is exactly the kind of note readers learn to
skip.

## How this inventory was re-derived

Not by memory. Every `app.Act{` construction in `cmd/gs` and `cmd/gitseq-mcp`
was listed, and every `RestsOn`, `Target` and `Retirement` value traced back to
where it came from; then every `set.String`/`set.Var`/`set.Bool` in `cmd/gs`
and every key of every MCP `inputSchema` was classified. The body fields were
re-derived the same way and from the other end: every `body["..."]` and
`Body[...]` **read** in `internal` and `cmd` was enumerated — 79 distinct keys
— and each classified by what its consumer does with the value, which is how
the six above were found and how `authorizes_candidate` was kept out. That is how
`gs publish --basis` was found: it is the only event reference that reaches a
signed `rests_on` without passing through a flag whose name says "event".
Nothing else in either binary reaches `RestsOn`, `Target` or a citation list
from caller input; `successionActs` and `publicationRetireAct` derive theirs
from the merge plan and the projection, both already canonical.

## Guarantees

- One resolver, `internal/eventref`, used by both binaries.
- One verified event set per act, read at most once and only when a typed
  reference needs it; a canonical identifier reaches the resident untouched.
- The searched population is `Projection.Decisions` — one row per durable
  record, so ratify and supersede resolve too — never Git's object database.
- The signed payload carries only full canonical identifiers.
- Ambiguity and no match refuse with bounded candidates and append nothing.
- A canonical identifier of another genesis is preserved exactly as typed.
