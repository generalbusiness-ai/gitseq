---
title: gs state
summary: Append a durable, attributed utterance.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:720a506647f095d95a079b667b2e9c6cc8dc8084
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:829bcd4d9952d4beb5ee8e3667a3f2aa9a1fab42
---

# `gs state`

Appends one durable statement, signed as the named actor, and prints its
event identifier and nothing else.

This is the general-purpose durable command. `ratify` and `supersede` are
the only two acts it cannot make.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The signing actor. |
| `--kind` | *(required)* | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `--text` | *(required)* | The statement itself, in plain language. |
| `--body` | | `key=value`, repeatable. Structured fields. |
| `--rests-on` | | An event identifier, repeatable. What this act bears on. |
| `--evidence` | | `name=path`, repeatable. Files embedded as attachments. |
| `--allow-dead-basis` | `false` | Rest on a retired basis anyway. Asking for it signs `dead_basis_override=true`: testimony that you saw it, not a repair of it. A merely stale basis needs no flag; see below. Citing an effective supersession stays advisory. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' \
  --body to=@bot --body conditions='CHANGELOG.md exists' \
  --body no_git_artifact=true --rests-on "$SEED")

gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST"
```

## Body fields the fold reads

Most of `body` is free-form and means whatever the room's practice says.
A few fields are structural, because the room's declared vocabulary
requires them — read `status.durable.vocabulary.definitions` for the
catalog in force rather than trusting this list to be complete:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer: a configured name, `@name`, or fingerprint. The signed event stores the fingerprint, and it must identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

For an artifact, `commit` must resolve in `--repo` and must already be the
full canonical commit object ID. A branch, tag, symbolic name, uppercase ID,
or abbreviated hash is refused before the statement is signed or appended.

Implementation requests, promises and reports may also carry `branch` and
`head` (or `commit`) as advisory hints, so a local tool can associate a
checkout. They claim nothing about that checkout being clean or current;
the `artifact` is the durable pointer.

## Request authoring: what a request owes

Every request states its result, and a request that states none is refused
before anything is appended. There are exactly three ways to say it, and a
request must use exactly one:

| body | meaning |
|---|---|
| `target_ref=refs/heads/<branch>` | The request owes a Git artifact landed into that branch of this workroom's own repository. |
| `target=inherit` | The same obligation, with the destination taken from the nearest ancestor request that named one. |
| `no_git_artifact=true` | The request owes no Git artifact: a review, a decision, a design conversation, an operation. |

A landing request may also carry `landing=held`, which says the landing waits
for an exact release, and `hold_owner=@name` (or a fingerprint) naming the one
actor who may sign it. Children inherit the hold with the target, and a child
may not rename an owner it merely inherited.

`target_repo` and `target_head` are **not** caller input. This command fills
`target_repo` with this workroom's genesis identifier and resolves
`target_head` from `target_ref` at filing, so the stored measurement is the one
this repository actually held. Supplying either is refused, because a
hand-written head is either a guess or a measurement taken somewhere else. It
is also a different field from a release report's `target_pre_head`, which is
the signer's own measurement and is checked separately.

Refused before any durable append, with the frontier unchanged:

| body | refusal |
|---|---|
| no choice at all | `request states no result: name a target, inherit one, or state no_git_artifact` |
| two choices | `request states more than one result` |
| `target_ref` outside `refs/heads/` | `body.target_ref must name a branch under refs/heads/` |
| `target_ref` naming no existing ref | `body.target_ref: refs/heads/x does not resolve in <repo>` |
| `target_repo` or `target_head` supplied | `body.<field> is resolved at filing and cannot be supplied` |

The destination is part of the request, so nothing edits it in place:
retargeting is a new request superseding the old one. A ref that moves after
filing changes nothing durable — `target_head` is the measurement at filing,
and the release and the merge each re-measure.

Requests filed under this rule are signed as `workroom/state@3`. Records
already in the log under `workroom/state@2` or earlier keep their old reading
exactly: the same field names there are opaque body text, and a legacy
commitment that ever carried a reporting artifact still reads as owing
`refs/heads/main`, flagged `legacy`.

### Which surfaces produce requests

Every producer states a truthful choice; none falls back to a guessed target.

| producer | schema signed | choice it emits |
|---|---|---|
| `gs state --kind request` (and any declared request-lifecycle kind) | `workroom/state@3` | whatever the caller's `--body` states |
| `gs batch` entries of `verb: state`, `kind: request` | `workroom/state@3` | whatever the entry's `body` states |
| MCP `state` tool | `workroom/state@3` | whatever the call's `body` states |
| Resident `POST /v0/act` with `act: state`, `kind: request` | `workroom/state@3` | whatever the request's `body` states |
| `gs reassign-if-unclaimed` and MCP `reassign_if_unclaimed` | `workroom/reassign-if-unclaimed@1` | whatever `--body` / `body` states; the replacement is a new request and restates its own result rather than inheriting the retired one's |
| `gs review` | *(none)* | Files a verdict report on an existing review commitment; it produces no request. |
| `gs merge` | *(none)* | Files artifacts, asserts and supersessions in its succession batch; it produces no request. The authorization request that releases a hold is filed by the performer with `gs state`, as `no_git_artifact=true`. |

## Authorization and release reports

A report carrying `authorizes_request` is an authorization — the release of a
held landing, or the phase-one authorization of a legacy one. When it also
carries `target_ref`, this command resolves that ref in `--repo` before signing
anything and refuses the act unless the ref is at the report's
`target_pre_head`, which must be a full canonical commit object ID. Stating
`remeasure=disjoint-paths` relaxes the comparison to requiring
`target_pre_head` to be an ancestor of where the ref stands now.

The check is here because a signer measures a destination and then signs; a
force-push in between makes the signature describe a world that has moved.
[`gs merge`](merge.md) resolves the same ref again immediately before it moves
`HEAD`, so the reading is taken at both act times rather than trusted once.
The MCP `state` tool and the resident's `/v0/act` endpoint apply the same
reading, so which surface files the report changes nothing.

It is judged for a genuinely new report only. An exact retry under an
`--idempotency-key` already accepted — same actor, same words, same body, same
bases — returns the original event, appends nothing, and is not measured a
second time, so a lost response is still recoverable after the ref moves. The
key alone does not do this: the same key over any different act is refused as a
reused key, and a report filed under a fresh key is measured against the ref as
it stands now.

## Retired bases and stale bases

A **retired** basis is withdrawn ground: nothing stands there any more, so
resting on it is refused. The escape is `--allow-dead-basis`, which signs
`dead_basis_override=true` on the act.

A **stale** basis still stands exactly where it stood; only something
underneath it moved. That act is admitted, and the boundary writes what moved
into `body.stale_bases` — the same one-line note
[`gs merge`](merge.md) puts in a receipt, naming the stale basis, whether it
describes a superseded world, and the retired acts beneath it. This is the
merge rule applied at the write boundary: refuse the retired one, land the
stale one and record it. The note is checked as well as written: when the act
is sequenced, the boundary computes the note again from the world the act
would join and refuses any act whose signed `body.stale_bases` differs, or
that carries the field at all on fresh ground. If your world moved between
signing and sequencing, re-run the command to sign the current note.

## Reserved fields you cannot write

Five body keys are reserved for the admission boundary, and a plain
`gs state` call is refused if it supplies any of them:

| Field | Belongs to | Ask for it with |
|---|---|---|
| `review_path` | The guarded review path | [`gs review`](review.md) |
| `head_news_acknowledged` | The guarded review path | [`gs review`](review.md) |
| `review_frontier` | The guarded review path | [`gs review`](review.md) |
| `dead_basis_override` | The dead-basis escape | `--allow-dead-basis` |
| `stale_bases` | The recorded staleness note | *(nothing; the boundary writes it)* |

The refusal names the field and quotes back the value you sent, so a
`review_path` of `x` is refused as `body.review_path="x" is a reserved
admission field and cannot be supplied by this write`. It happens before
signing, so nothing reaches the log.

The first three are stamped by `gs review` onto the verdict it builds, so
a hand-written call has no reason to carry them. `dead_basis_override` is
different: it records a deliberate escape, and the way to ask for that
escape is `--allow-dead-basis`, which signs the field for you. Setting it
by hand is refused because a reserved field means the same thing wherever
it appears, and a caller that writes it directly is claiming an
authorisation the boundary never granted. `stale_bases` has no flag at all:
it is the boundary's own testimony about what the act rested on, and an
author who could write it could make an act look freshly grounded. Refusing
it here protects only this command, so the sequencing boundary recomputes the
note and refuses any signed value that is not exactly its own: hand-signing
one gains nothing.

## Citing

`--rests-on` is how an act says what it bears on. Copy identifiers whole
from the emitted event — see
[Event identifiers](../event-identifiers.md).

A statement with an empty `rests_on` is almost always wrong. It is
accepted, and then nothing can ever make it stale; the fold marks
artifacts in that state `unable to flare`.

Required edges, by kind:

- every local filing surface checks a request-lifecycle draft before signing:
  `body.conditions` must be present, and `body.to` must resolve to a configured
  actor. The signed event stores that actor's fingerprint. This applies to
  declared request-lifecycle kinds as well as the starter `request` kind, and
  `gs batch` checks each request draft before that act is signed or appended.
  An error names the failing body field. The fold remains authoritative if the
  log or active vocabulary moves after this local check;
- a `promise` needs one basis that is an effective `request`, **and** the
  signer must be the performer that request named;
- a `report` needs one basis that is an effective `promise`, signed by
  the promisor. Before anything is appended, filing checks the active
  vocabulary for exactly one effective promise-lifecycle basis and checks that
  its promisor is the report signer. An error tells the caller which rule the
  draft violates; the fold remains authoritative if the log moves meanwhile.

When these lifecycle edges do not match, the CLI keeps the precise reason and
adds the recovery: a promise uses exactly one live request in `--rests-on`; a
report uses the one live promise the reporter made, or the request directly
only when that reporter made no promise. Report preflight adds that guidance
to its refusal before append. If the fold records an ineffective act, the
human status and inspection views add it beside the fold's unchanged verdict,
so a terse reason such as `dangling promise has no request` is actionable at
the terminal.

An artifact can report assigned implementation work without changing the
governed artifact schema. It qualifies when its signer is the promisor, it
names a commit, and its bases contain exactly one effective promise: the
promise it fulfils. Other artifacts retain their ordinary meaning.

Anything else in `rests_on` is carried unchecked.

## Evidence

`--evidence name=path` embeds a file as an attachment, so a promotion
from conversation can be verified after the conversation is gone. Select
honestly and summarize faithfully; embedded bytes count against the
[payload ceiling](../limits.md).

## Retrying safely

Pass `--idempotency-key` with a stable value. A replayed submission
reports that the act already landed rather than appending a second one.
If you see a replay report, your act is in the log — do not submit a
variant.

## Local or through the resident

With no resident advertised and no `--server` given, the act is written
straight to the local sequence. An advertised resident takes it by default,
and `--server -` always writes locally. Submitting to a resident sequencer
is what makes concurrent appends from several actors safe. Both land in the
same sequence.

## See also

- [`gs ratify`](ratify.md), [`gs supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
