---
title: MCP state
summary: Append a durable attributed utterance.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
---

# `state`

Appends one durable statement, signed as this session's actor. It serves as
the MCP counterpart of [`gs state`](../gs/state.md), and everything it
appends stays permanent. It runs the same filing-time checks, including the
authorization target re-resolution `gs state` describes: the tool refuses a
report carrying `authorizes_request` and `target_ref` unless the ref still
holds its `target_pre_head`. As there, an exact retry under an
`idempotency_key` already accepted replays the original event without
measuring it again, while the tool refuses the same key over a different act
and measures a fresh key against the ref as it stands now.

## Arguments

| argument | required | meaning |
|---|---|---|
| `kind` | required | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `text` | required | The statement, in plain language. |
| `rests_on` | required | Array of event references. What this act bears on. |
| `body` | optional | String map of structured fields. |
| `evidence` | optional | String map of `name` to content, embedded as attachments. |
| `allow_dead_basis` | optional | Rest on a retired basis anyway, signing `dead_basis_override=true`. Testimony that you saw it, not a repair of it. A merely stale basis needs no argument; see below. Citing a record the fold refused stays advisory. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter started in, or to its `--repo` when you gave one. |
| `agent` | optional | The actor whose existing accessible key signs this statement; defaults to startup `--actor`. |

`rests_on` takes a [short reference](../event-identifiers.md#typing-one-at-a-boundary)
as well as the canonical identifier, and so do the `body` fields a consumer
reads as one durable event — `artifact`, `authorizes_request`,
`authorizes_approval` and the three `merge_` receipt bindings. The tool carries
every other body value exactly as given. The result names what it resolved in a
`resolved` field; on a refusal the same sentences arrive as content blocks
beside the reason.

The schema requires `rests_on` — an act citing nothing almost always signals
a mistake, and requiring the field makes that a decision rather
than an omission. It may still hold an empty array, and then nothing can
ever make the act stale.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"state","arguments":{"kind":"request","text":"Add a changelog","body":{"to":"@bot","conditions":"CHANGELOG.md exists","target_ref":"refs/heads/main"},"rests_on":["%s"]},%s}}\n' "$SEED" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## Request authoring: what a request owes

A request states its result, in `body`, exactly as it does through
[`gs state`](../gs/state.md#request-authoring-what-a-request-owes), and the same
refusals apply before the tool appends anything: `target_ref=refs/heads/<branch>`,
`target=inherit`, or `no_git_artifact=true`, exactly one of them, optionally
with `landing=held` and `hold_owner`. This adapter fills `target_repo` and
resolves `target_head` from the ref at filing; it refuses a call that supplies
either. The adapter signs requests as `workroom/state@3`.

The adapter answers an exact retry under an `idempotency_key` already accepted
from the log before it reads any ref, so the retry replays its original event
even after someone moved or deleted the branch it named. The adapter rebuilds
it under the accepted act's own schema, so a request this workroom accepted
before the landing obligation existed — a `workroom/state@2` record stating no
result — replays; the adapter does not re-sign it as `state@3` and refuse it
for stating none. The adapter refuses a reused key that names a different
`target_ref`, states a result the accepted legacy request never stated, or
changes anything else the caller sent, rather than answering it with the
accepted request; it also refuses a fresh key naming a ref that does not
resolve, or stating no result at all.

You can read the stored schema and the folded result through
[`inspect`](inspect.md) and the commitment rows in [`work`](work.md) and
[`status`](status.md): `target_repo`, `target_ref`, `target_head`, and the
`inherited`, `held`, `hold_owner` and `legacy` facts.

## Body fields the fold reads

Read `status.durable.vocabulary.definitions` before choosing a kind: that
catalog serves as the source of truth for required fields, basis constraints
and ratification authority. You will meet these structural fields most:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer, as a name, `@name`, or fingerprint. The signed event stores the fingerprint, and the fold requires it to identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

Implementation requests, promises and reports may carry `branch` and
`head` as advisory checkout hints. They claim nothing about the
cleanliness or currency of that checkout.

An artifact reports assigned implementation work when the promisor signed
it, it names the exact implementation commit, and its bases contain
exactly one effective promise: the promise it fulfils. This adds no required
artifact field and does not change ordinary artifacts.

## Retired bases and stale bases

A **retired** basis marks withdrawn ground: nothing stands there any more, so
the boundary refuses an act resting on it. The escape: `allow_dead_basis`,
which signs `dead_basis_override=true` on the act.

A **stale** basis still stands exactly where it stood; only something
underneath it moved. The boundary admits that act and writes what moved
into `body.stale_bases` — the same one-line note a merge receipt carries,
naming the stale basis, whether it describes a superseded world, and the
retired acts beneath it. This applies the merge rule at the write
boundary: refuse the retired one, land the stale one and record it. The
boundary checks the note as well as writing it: at sequencing, it
computes the note again from the world the act would join and refuses any act
whose signed `body.stale_bases` differs, or that carries the field at all on
fresh ground. If your world moved between signing and sequencing, call the
tool again to sign the current note.

A basis the fold **refused** counts as neither: nobody withdrew anything and
nothing underneath it can move. The boundary admits the act, and the result's
`dead_rests_on` note classifies the citation `ineffective`, beside `retired`,
`stale` and `supersede`, so a caller sees at filing time that part of what the
act rests on never took force. The landed statement, and its artifact row when
it records an artifact, then carry `ineffective_bases` in the projection. See
[staleness](../../concepts/staleness.md#ineffective-bases).

## Reserved fields you cannot write

The admission boundary reserves five body keys, and it refuses a plain
`state` call that supplies any of them:

| Field | Belongs to | Ask for it with |
|---|---|---|
| `review_path` | The guarded review path | [`review`](review.md) |
| `head_news_acknowledged` | The guarded review path | [`review`](review.md) |
| `review_frontier` | The guarded review path | [`review`](review.md) |
| `dead_basis_override` | The dead-basis escape | `allow_dead_basis` |
| `stale_bases` | The recorded staleness note | *(nothing; the boundary writes it)* |

The refusal names the field and quotes back the value you sent, so the tool
refuses a `review_path` of `x` with `body.review_path="x" is a reserved
admission field and cannot be supplied by this write`. The refusal happens
before signing, so nothing reaches the log.

The [`review`](review.md) tool stamps the first three onto the
verdict it builds, so a hand-written call has no reason to carry them.
`dead_basis_override` differs: it records a deliberate escape, and
you ask for that escape with the `allow_dead_basis` argument, which
signs the field for you. The boundary refuses it set by hand because a
reserved field means the same thing wherever it appears, and a caller that
writes it directly claims an authorisation the boundary never granted.
`stale_bases` has no argument at all: it records the boundary's own testimony
about what the act rested on, and a caller who could write it could make an
act look freshly grounded. Refusing it here protects only this tool, so the
sequencing boundary recomputes the note and refuses any signed value that does
not match its own exactly: hand-signing one gains nothing.

## Evidence

`evidence` maps name to content, embedded as attachments in the
signed payload. This keeps a promotion from ephemeral conversation
verifiable after the conversation has gone. Embedded bytes count against
the [payload ceiling](../limits.md).

## Idempotency

Pass `idempotency_key` when you might retry. A replay reports that your
act already landed; do not then submit a variant.

## What your bases mean here, before it signs

Before signing the act, the call says what each `rests_on` value means in
this workroom, in a `basis_notes` field of the result: the same sentences
[`gs state`](../gs/state.md#citing) writes to standard error. A basis naming
a live event of this workroom earns silence. The other three each earn one
line:

| The basis | What the call tells you |
|---|---|
| A string that names no identifier | `warning: ... is not an event identifier` — the act will rest on nothing that can flare it |
| This workroom's identifier naming no event | `warning: ... names no event in this workroom` — the sequencer refuses the act, and this says why |
| Another workroom's identifier | `note: ... is another workroom's event` — admitted as an external citation this room cannot verify |

The warnings describe; they refuse nothing and grant nothing. Only the
sequencer's own rule, unchanged, refuses. [`supersede`](supersede.md) and
[`reassign_if_unclaimed`](reassign_if_unclaimed.md) carry the same field for
the same reason.

## What the fold made of it

A successful append tells you the act landed. It does not tell you the
act became what you meant, and those pose different questions. Admission
refuses two shapes outright, before anything lands: a citation that uses
this workroom's identifier but names no event here (the sequencer's own
rule, which the warning above announces), and a report whose `verdict`
or `status` says `approved` or `changes-requested`, which makes it a review
verdict that you must file with the [`review`](review.md) tool so the
guarded path can bind it. Other mistakes land, and the result only describes
them: admission accepts a foreign identifier as a citation this room cannot
verify, and a report with some other `status` word stays an ordinary report,
not a review. Historical records written before these rules can carry any of
those shapes, and the fold reads them as they stand, including an approval
that does not cite the artifact it judges, which authorises no merge.

So the result carries a `projected` object saying how the fold read the
act. [`ratify`](ratify.md) and [`supersede`](supersede.md) carry it too,
since of these mistakes, a target naming nothing costs least to make
and survives most quietly:

| Field | Says |
|---|---|
| `verdict`, `reason` | The fold's ruling. `verdict` always appears, including `effective`; `reason` accompanies rulings that explain a refusal or dispute. |
| `unresolved_rests_on` | Citations naming no event in this workroom. |
| `unresolved_target` | For [`ratify`](ratify.md) and [`supersede`](supersede.md): a target naming no event here. |
| `review` | For a report: whether it became a review, and which artifact it judges. |

**These notes describe; they do not refuse.** Admission still accepts unknown
body keys — the reserved review fields form the one closed exception, and
admission refuses those, not these notes. The body map stays open so a room
can carry vocabulary this implementation never anticipated, and a
validator that rejects what it does not recognise would take that away to
fix one spelling mistake. Describing the reading catches the whole family,
including the shapes nobody has hit yet, at the cost of not stopping any
of them.

**The notes report; they do not guarantee.** They say what the fold made
of the act at that moment. A later supersession can change any of it, and
reading a summary gives weaker evidence than querying the projection. Treat a
clean `projected` as the absence of the known traps, not the presence of
correctness.

## Without a resident

`state` keeps working against the local log and marks the result
`degraded`. The act stays real and permanent either way; you lose
the sequencer's coordination with other writers.

## See also

- [`ratify`](ratify.md), [`supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
