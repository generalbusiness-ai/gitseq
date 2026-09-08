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
| generic `--body key=value` / `body` map values | free-form application fields; reinterpreting them would resolve prose |
| reserved body fields `review_frontier`, `head_news_acknowledged` | stamped by the review guard from resolved input, never caller input |
| `Rests-On:` commit trailers | a commit message is no boundary anything resolves at |
| resident HTTP routes (`/v0/*`) | program-to-program; callers already hold canonical identifiers |

No caller-supplied typed body field holds an event identifier: `body.target`
takes only the literal `inherit`, and `body.artifact` on an approval is stamped
by the guarded review path. Verified by enumerating every `body["..."]` key
read in `internal` and `cmd`.

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
and every key of every MCP `inputSchema` was classified. That is how
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
