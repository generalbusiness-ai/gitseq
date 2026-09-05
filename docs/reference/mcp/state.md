---
title: MCP state
summary: Append a durable attributed utterance.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:ccfbba8ebd13ea7f0a38159275f5b87b8c396c93
---

# `state`

Appends one durable statement, signed as this session's actor. It is the
MCP counterpart of [`gs state`](../gs/state.md), and everything it
appends is permanent. It runs the same filing-time checks, including the
authorization target re-resolution `gs state` describes: a report carrying
`authorizes_request` and `target_ref` is refused unless the ref still holds
its `target_pre_head`. As there, an exact retry under an `idempotency_key`
already accepted replays the original event and is not measured again, while
the same key over a different act is refused and a fresh key is measured
against the ref as it stands now.

## Arguments

| argument | required | meaning |
|---|---|---|
| `kind` | required | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `text` | required | The statement, in plain language. |
| `rests_on` | required | Array of event identifiers. What this act bears on. |
| `body` | optional | String map of structured fields. |
| `evidence` | optional | String map of `name` to content, embedded as attachments. |
| `allow_dead_basis` | optional | Rest on a retired basis anyway, signing `dead_basis_override=true`. Testimony that you saw it, not a repair of it. A merely stale basis needs no argument; see below. |
| `idempotency_key` | optional | A stable key, so a retry lands once. |
| `repo` | optional | The repository whose workroom this call acts in. Defaults to the directory the adapter was started in, or to its `--repo` when one was given. |
| `agent` | optional | The actor whose existing accessible key signs this statement; defaults to startup `--actor`. |

`rests_on` is required by the schema — an act citing nothing is almost
always a mistake, and requiring the field makes that a decision rather
than an omission. It may still be an empty array, and then nothing can
ever make the act stale.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
PORT="${PORT:-7777}"
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'

printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"state","arguments":{"kind":"request","text":"Add a changelog","body":{"to":"@bot","conditions":"CHANGELOG.md exists"},"rests_on":["%s"]},%s}}\n' "$SEED" "$META" \
  | gitseq-mcp --repo "$REPO" --actor alice 2>/dev/null
```

## Body fields the fold reads

Read `status.durable.vocabulary.definitions` before choosing a kind: that
catalog is the source of truth for required fields, basis constraints and
ratification authority. The structural fields you will meet most are:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer, as a name, `@name`, or fingerprint. The signed event stores the fingerprint, and the fold requires it to identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

Implementation requests, promises and reports may carry `branch` and
`head` as advisory checkout hints. They claim nothing about that checkout
being clean or current.

An artifact reports assigned implementation work when its signer is the
promisor, it names the exact implementation commit, and its bases contain
exactly one effective promise: the promise it fulfils. This adds no required
artifact field and does not change ordinary artifacts.

## Retired bases and stale bases

A **retired** basis is withdrawn ground: nothing stands there any more, so
resting on it is refused. The escape is `allow_dead_basis`, which signs
`dead_basis_override=true` on the act.

A **stale** basis still stands exactly where it stood; only something
underneath it moved. That act is admitted, and the boundary writes what moved
into `body.stale_bases` — the same one-line note a merge receipt carries,
naming the stale basis, whether it describes a superseded world, and the
retired acts beneath it. This is the merge rule applied at the write
boundary: refuse the retired one, land the stale one and record it. The note
is checked as well as written: when the act is sequenced, the boundary
computes the note again from the world the act would join and refuses any act
whose signed `body.stale_bases` differs, or that carries the field at all on
fresh ground. If your world moved between signing and sequencing, call the
tool again to sign the current note.

## Reserved fields you cannot write

Five body keys are reserved for the admission boundary, and a plain
`state` call is refused if it supplies any of them:

| Field | Belongs to | Ask for it with |
|---|---|---|
| `review_path` | The guarded review path | [`review`](review.md) |
| `head_news_acknowledged` | The guarded review path | [`review`](review.md) |
| `review_frontier` | The guarded review path | [`review`](review.md) |
| `dead_basis_override` | The dead-basis escape | `allow_dead_basis` |
| `stale_bases` | The recorded staleness note | *(nothing; the boundary writes it)* |

The refusal names the field and quotes back the value you sent, so a
`review_path` of `x` is refused as `body.review_path="x" is a reserved
admission field and cannot be supplied by this write`. It happens before
signing, so nothing reaches the log.

The first three are stamped by the [`review`](review.md) tool onto the
verdict it builds, so a hand-written call has no reason to carry them.
`dead_basis_override` is different: it records a deliberate escape, and
the way to ask for that escape is the `allow_dead_basis` argument, which
signs the field for you. Setting it by hand is refused because a reserved
field means the same thing wherever it appears, and a caller that writes
it directly is claiming an authorisation the boundary never granted.
`stale_bases` has no argument at all: it is the boundary's own testimony
about what the act rested on, and a caller who could write it could make an
act look freshly grounded. Refusing it here protects only this tool, so the
sequencing boundary recomputes the note and refuses any signed value that is
not exactly its own: hand-signing one gains nothing.

## Evidence

`evidence` is a map of name to content, embedded as attachments in the
signed payload. This is how a promotion from ephemeral conversation stays
verifiable after the conversation is gone. Embedded bytes count against
the [payload ceiling](../limits.md).

## Idempotency

Pass `idempotency_key` when you might retry. A replay reports that your
act already landed; do not then submit a variant.

## What the fold made of it

A successful append tells you the act landed. It does not tell you the
act became what you meant, and those are different questions. A report
whose body sets `status` instead of `verdict` reads as a review to every
human and is no review to the fold. An approval that does not cite the
artifact cannot authorise the merge it was written to authorise. A
citation naming no event in this workroom is skipped in silence. All
three return a record, are ruled effective, and move the commitment.

So the result carries a `projected` object saying how the fold read the
act. [`ratify`](ratify.md) and [`supersede`](supersede.md) carry it too,
since a target naming nothing is the cheapest of these mistakes to make
and the quietest to survive:

| Field | Says |
|---|---|
| `verdict`, `reason` | The fold's ruling. `verdict` is always present, including `effective`; `reason` accompanies rulings that explain a refusal or dispute. |
| `unresolved_rests_on` | Citations naming no event in this workroom. |
| `unresolved_target` | For [`ratify`](ratify.md) and [`supersede`](supersede.md): a target naming no event here. |
| `review` | For a report: whether it became a review, and which artifact it judges. |

**These notes describe; they do not refuse.** Unknown body keys are still
accepted — the reserved fields above are the one closed exception, and
they are refused by the admission boundary rather than by these notes. Refusing them would catch `status` today and narrow a
deliberately open structure for good — the body map is open so a room can
carry vocabulary this implementation never anticipated, and a validator
that rejects what it does not recognise takes that away to fix one
spelling mistake. Describing the reading catches the whole family,
including the shapes nobody has hit yet, at the cost of not stopping any
of them.

**The notes are a report, not a guarantee.** They say what the fold made
of the act at that moment. A later supersession can change any of it, and
reading a summary is weaker than querying the projection. Treat a clean
`projected` as the absence of the known traps, not the presence of
correctness.

## Without a resident

`state` keeps working against the local log and marks the result
`degraded`. The act is real and permanent either way; what you lose is
the sequencer's coordination with other writers.

## See also

- [`ratify`](ratify.md), [`supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
