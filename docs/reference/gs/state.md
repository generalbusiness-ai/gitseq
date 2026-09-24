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

`gs state` serves as the general-purpose durable command. `ratify` and
`supersede` remain the only two acts it cannot make.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The signing actor. |
| `--kind` | *(required)* | The speech act, from the room's declared vocabulary: `assert`, `propose`, `request`, `promise`, `report`, `dissent`, `artifact`, or a governance kind. |
| `--text` | *(required, or `--text-file`)* | The statement itself, in plain language, typed on the command line. |
| `--text-file` | | The statement, read from this file instead. Give one of the two, never both; the contents become the statement text as written, apart from trailing whitespace. |
| `--body` | | `key=value`, repeatable. Structured fields. |
| `--rests-on` | | An event reference, repeatable. What this act bears on. |
| `--evidence` | | `name=path`, repeatable. Files embedded as attachments. |
| `--allow-dead-basis` | `false` | Rest on a retired basis anyway. Asking for it signs `dead_basis_override=true`: testimony that you saw it, not a repair of it. A merely stale basis needs no flag; see below. Citing an effective supersession, or a record the fold refused, stays advisory. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; `gs state` honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; `gs state` refuses a value other than a positive duration before signing anything. |
| `--idempotency-key` | *(random)* | A stable key, so a retry lands once. |
| `--no-preflight` | `false` | File the act without asking the fold what it would decide first. See [Refused before signing](#refused-before-signing). |

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' \
  --body to=@bot --body conditions='CHANGELOG.md exists' \
  --body target_ref=refs/heads/main --rests-on "$SEED")

PROMISE=$(gs state --repo "$REPO" --as bot --kind promise \
  --text 'I will add it' --rests-on "$REQUEST")

printf '# Starting\n\nThe first entry will describe the request itself.\n' > note.md
gs state --repo "$REPO" --as bot --kind assert \
  --text-file note.md --rests-on "$PROMISE"
```

## Refused before signing

Before signing anything, this command asks the fold what it would decide about
the act as written, and refuses unless the answer says effective:

```text
gs: the fold would rule this act ineffective: dangling promise has no request
fix: rest the promise on the request it claims: --rests-on <request-event>
file it as written with --no-preflight
```

The reason comes from the fold, word for word; this boundary adds only the line
beneath it, and a reason nobody has written a line for prints alone. Nothing
has happened when it prints: the command has read no signing key and built no
act, and the log stands where it stood.

It gives advice, not authority. No rule lives here — the check folds the
prospective record with the same fold the sequencer runs, over the projection
this checkout last verified — and the fold judges the act again at sequencing,
against the world it actually joins. Only that second judgement
counts. `--no-preflight` skips the check and files the act exactly as written,
for the deliberate filing of a shape the fold refuses.

The command leaves four cases to the fold, with no refusal here:

| case | why |
|---|---|
| this process cannot fold the workroom | no world exists to judge against |
| `--server` names a resident standing anywhere but where this checkout stands | the act joins that resident's frontier, not this one |
| the act carries an `--idempotency-key` this actor already holds | it counts as a retry: the sequencer replays the accepted event, or refuses the key as reused, and a judgement against today's world would refuse a recovery. The check decides this per act, so in a chain it stands down for that act alone — see [`gs batch`](batch.md#refused-before-signing) |
| the builder cannot build the act's body at all — an undefined kind, a request stating no result, a reserved field | the builder refuses it at signing, in its own words |

This check does not judge everything the builder refuses. A retirement
documentation still cites, a report's basis and an unratifiable target get their
judgement after the command reads a key, as always.

A malformed invocation gets a different answer, and an earlier one: an undefined
flag, a missing required flag, a subject given as a flag where a positional
argument belongs, or an event reference that names nothing here prints this
command's flags and one worked example, and exits non-zero with nothing touched.

## Writing the statement to a file

A report or an assert often holds formatted text: headings, tables, quoted
findings, code. Passing that through `--text` on a command line means shell
quoting, escaped newlines and truncated pastes, and the workroom then holds a
mangled statement. Write it to a file and name it with `--text-file`, as the
example above does. `--text` still serves the one-line statement that
needs no file.

The file's contents become the statement text exactly as written, apart from
trailing whitespace, which the command trims. Signing and display of the text
do not change.

The two flags exclude each other, judged by presence rather than value, and every
statement needs one of them. The command refuses each of these before signing,
and the message names the flag:

| what you gave | refusal |
|---|---|
| both `--text` and `--text-file`, even with one of them empty | `--text and --text-file cannot both be given` |
| neither | `--text or --text-file is required` |
| an empty or whitespace-only file | `--text-file <path> is empty` |
| an unreadable path | `--text-file <path>: <the read error>` |

## Body fields the fold reads

Most of `body` stays free-form and means whatever the room's practice says.
A few fields matter structurally, because the room's declared vocabulary
requires them — read `status.durable.vocabulary.definitions` for the
catalog in force rather than trusting this list as complete:

| Kind | Required body | Meaning |
|---|---|---|
| `request` | `conditions` | What would count as satisfaction. |
| `request` | `to` | The performer: a configured name, `@name`, or fingerprint. The signed event stores the fingerprint, and it must identify a live roster actor. |
| `artifact` | `path`, `commit` | Implementation truth as `path@commit`. |

For an artifact, `commit` must resolve in `--repo` and must already give the
full canonical commit object ID. The command refuses a branch, tag, symbolic
name, uppercase ID, or abbreviated hash before signing or appending the statement.

Implementation requests, promises and reports may also carry `branch` and
`head` (or `commit`) as advisory hints, so a local tool can associate a
checkout. They claim nothing about the cleanliness or currency of that checkout;
the `artifact` serves as the durable pointer.

## Request authoring: what a request owes

Every request states its result, and the command refuses a request that states
none before appending anything. Exactly three ways exist to say it, and a
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

`target_repo` and `target_head` do **not** come from the caller. This command fills
`target_repo` with this workroom's genesis identifier and resolves
`target_head` from `target_ref` at filing, so the stored measurement matches what
this repository actually held. The command refuses either one if supplied, because
a hand-written head can only give a guess or a measurement taken somewhere else.
It also differs from a release report's `target_pre_head`, which records
the signer's own measurement and gets checked separately.

Refused before any durable append, with the frontier unchanged:

| body | refusal |
|---|---|
| no choice at all | `request states no result: name a target, inherit one, or state no_git_artifact` |
| two choices | `request states more than one result` |
| `target_ref` outside `refs/heads/` | `body.target_ref must name a branch under refs/heads/` |
| `target_ref` naming no existing ref | `body.target_ref: refs/heads/x does not resolve in <repo>` |
| `target_repo` or `target_head` supplied | `body.<field> is resolved at filing and cannot be supplied` |

The destination belongs to the request, so nothing edits it in place:
retargeting takes a new request superseding the old one. A ref that moves after
filing changes nothing durable — `target_head` records the measurement at filing,
and the release and the merge each re-measure.

### Retrying a request

The command reads `target_head` from the ref at filing, so a retry cannot take
the same measurement twice. The log answers an exact retry under an
`--idempotency-key` already accepted instead: the command recovers the accepted
act before reading any ref, rebuilds the request in the form that act took, and
returns the original event with nothing appended. The command reads no ref on
that path, so the retry still replays after the branch it named has moved or
someone has deleted it outright.

Rebuilding it in the form that act took means under that act's own schema, on the
measurement it stated. That makes an existing workroom retryable at
all. Every request filed before the landing obligation existed stands in the
log as `workroom/state@2` and states no result; re-signing one as
`workroom/state@3` would refuse it for stating none — the one answer a
caller who already owns the act must never get. So an exact retry of an
accepted `state@2` request replays that act, and the rules that applied when
its actor signed it judge the reproduction, rather than today's.

The key alone buys nothing. Only the act stated again byte for byte replays. A
reused key over any different intent — different words, body, bases,
attachments, a different `target_ref`, or a result the accepted legacy request
never stated — falls through to an ordinary fresh filing, which measures the
ref as it stands now; the command then refuses the reused key with `idempotency key
reused with different intent`, or, when that different destination does not
resolve or the act states no result, refuses it for that instead. The command
never answers a reused key naming a different branch with the request filed
against the old one. It refuses a fresh key naming a ref that does not resolve,
or stating no result at all, as it would any first filing.

The command signs requests filed fresh under this rule as `workroom/state@3`. Records
already in the log under `workroom/state@2` or earlier keep their old reading
exactly: the same field names there count as opaque body text, and a legacy
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
| `gs reassign-if-unclaimed` and MCP `reassign_if_unclaimed` | `workroom/reassign-if-unclaimed@1` | whatever `--body` / `body` states; the replacement makes a new request and restates its own result rather than inheriting the retired one's |
| `gs review` | *(none)* | Files a verdict report on an existing review commitment; it produces no request. |
| `gs merge` | *(none)* | Files artifacts, asserts and supersessions in its succession batch; it produces no request. The performer files the authorization request that releases a hold with `gs state`, as `no_git_artifact=true`. |

## Authorization and release reports

A report carrying `authorizes_request` acts as an authorization — the release of a
held landing, or the phase-one authorization of a legacy one. When it also
carries `target_ref`, this command resolves that ref in `--repo` before signing
anything and refuses the act unless the ref stands at the report's
`target_pre_head`, which must give a full canonical commit object ID. Stating
`remeasure=disjoint-paths` relaxes the comparison to requiring that
`target_pre_head` stand as an ancestor of where the ref stands now.

The check lives here because a signer measures a destination and then signs; a
force-push in between makes the signature describe a world that has moved.
[`gs merge`](merge.md) resolves the same ref again immediately before it moves
`HEAD`, so it takes the reading at both act times rather than trusting it once.
The MCP `state` tool and the resident's `/v0/act` endpoint apply the same
reading, so which surface files the report changes nothing.

The check judges a genuinely new report only. An exact retry under an
`--idempotency-key` already accepted — same actor, same words, same body, same
bases — returns the original event, appends nothing, and skips a second
measurement, so a lost response stays recoverable after the ref moves. The
key alone does not do this: the command refuses the same key over any different
act as a reused key, and measures a report filed under a fresh key against the
ref as it stands now.

## Retired bases and stale bases

A **retired** basis marks withdrawn ground: nothing stands there any more, so
the command refuses to rest on it. `--allow-dead-basis` provides the escape,
signing `dead_basis_override=true` on the act.

A **stale** basis still stands exactly where it stood; only something
underneath it moved. The boundary admits that act and writes what moved
into `body.stale_bases` — the same one-line note
[`gs merge`](merge.md) puts in a receipt, naming the stale basis, whether it
describes a superseded world, and the retired acts beneath it. This applies the
merge rule at the write boundary: refuse the retired one, land the
stale one and record it. The boundary checks the note as well as writing it: at
sequencing, the boundary computes the note again from the world the act
would join and refuses any act whose signed `body.stale_bases` differs, or
that carries the field at all on fresh ground. If your world moved between
signing and sequencing, re-run the command to sign the current note.

A basis the fold **refused** counts as neither: nobody withdrew anything and nothing
underneath it can move. The boundary admits the act, and the command notes the
citation on standard error as `already dead (ineffective)`, the same way it
notes a retired, stale or superseding one, so an author sees at filing time
that part of what the act rests on never took force. The landed statement,
and its artifact row if it has one, then carry `ineffective_bases` in the
projection. See [staleness](../../concepts/staleness.md#ineffective-bases).

## Reserved fields you cannot write

The admission boundary reserves five body keys, and `gs state` refuses a
plain call that supplies any of them:

| Field | Belongs to | Ask for it with |
|---|---|---|
| `review_path` | The guarded review path | [`gs review`](review.md) |
| `head_news_acknowledged` | The guarded review path | [`gs review`](review.md) |
| `review_frontier` | The guarded review path | [`gs review`](review.md) |
| `dead_basis_override` | The dead-basis escape | `--allow-dead-basis` |
| `stale_bases` | The recorded staleness note | *(nothing; the boundary writes it)* |

The refusal names the field and quotes back the value you sent, so the command
refuses a `review_path` of `x` as `body.review_path="x" is a reserved
admission field and cannot be supplied by this write`. It happens before
signing, so nothing reaches the log.

`gs review` stamps the first three onto the verdict it builds, so
a hand-written call has no reason to carry them. `dead_basis_override`
differs: it records a deliberate escape, and you ask for that
escape with `--allow-dead-basis`, which signs the field for you. The command
refuses it when set by hand because a reserved field means the same thing wherever
it appears, and a caller that writes it directly claims an
authorisation the boundary never granted. `stale_bases` has no flag at all:
it carries the boundary's own testimony about what the act rested on, and an
author who could write it could make an act look freshly grounded. Refusing
it here protects only this command, so the sequencing boundary recomputes the
note and refuses any signed value other than exactly its own: hand-signing
one gains nothing.

## Citing

`--body` values count as application prose, with one exception this boundary
knows: the fields a consumer reads as exactly one durable event —
`artifact`, `authorizes_request`, `authorizes_approval`, `merge_approval`,
`merge_authorization` and `merge_authorization_ratification` — take a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) and
resolve with the rest of the act. `authorizes_candidate` names a Git commit
and the command carries every other key exactly as typed. See
[Body fields](../event-identifiers.md#body-fields-and-why-only-some-of-them).

An act says what it bears on with `--rests-on`. It takes a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier, resolved against this workroom's verified events and
named on standard error before the command signs anything. The signed act always
carries the full canonical identifier.

Before signing the act, the command says what each basis means here. A
basis naming a live event of this workroom earns silence. The other three
cases each earn one line on standard error:

| The basis | What the command tells you |
|---|---|
| This workroom's own genesis | nothing: the sequencer resolves it, though the fold projects no record for it |
| A string that names no identifier | `warning: ... is not an event identifier` — the act will rest on nothing that can flare it |
| This workroom's identifier naming no event | `warning: ... names no event in this workroom` — the sequencer refuses the act, and this says why before it does |
| Another workroom's identifier | `note: ... is another workroom's event` — admitted as an external citation this room cannot verify |

The warnings describe; they refuse nothing and grant nothing. Only the
sequencer's own rule, unchanged, refuses.

A statement with an empty `rests_on` almost always signals a mistake. The
boundary accepts it, and then nothing can ever make it stale; the fold marks
artifacts in that state `unable to flare`.

Required edges, by kind:

- every local filing surface checks a request-lifecycle draft before signing:
  `body.conditions` must exist, and `body.to` must resolve to a configured
  actor. The signed event stores that actor's fingerprint. This applies to
  declared request-lifecycle kinds as well as the starter `request` kind, and
  `gs batch` checks each request draft before signing or appending that act.
  An error names the failing body field. The fold remains authoritative if the
  log or active vocabulary moves after this local check;
- a `promise` needs one effective `request` as a basis, **and** the
  performer that request named must sign it;
- a `report` needs one effective `promise` as a basis, signed by
  the promisor. Before appending anything, filing checks the active
  vocabulary for exactly one effective promise-lifecycle basis and checks that
  its promisor signed the report. An error tells the caller which rule the
  draft violates; the fold remains authoritative if the log moves meanwhile.

When these lifecycle edges do not match, the CLI keeps the precise reason and
adds the recovery: a promise uses exactly one live request in `--rests-on`; a
report uses the one live promise the reporter made, or the request directly
only when that reporter made no promise. Report preflight adds that guidance
to its refusal before append. If the fold records an ineffective act, the
human status and inspection views add it beside the fold's unchanged verdict,
so a terse reason such as `dangling promise has no request` becomes actionable at
the terminal.

An artifact can report assigned implementation work without changing the
governed artifact schema. It qualifies when the promisor signed it, it
names a commit, and its bases contain exactly one effective promise: the
promise it fulfils. Other artifacts retain their ordinary meaning.

The command carries anything else in `rests_on` unchecked.

## Evidence

`--evidence name=path` embeds a file as an attachment, so anyone can verify a
promotion from conversation after the conversation has gone. Select
honestly and summarize faithfully; embedded bytes count against the
[payload ceiling](../limits.md).

## Retrying safely

Pass `--idempotency-key` with a stable value. A replayed submission
reports that the act already landed rather than appending a second one.
If you see a replay report, your act already sits in the log — do not submit a
variant.

## Local or through the resident

With no resident advertised and no `--server` given, the command writes the act
straight to the local sequence. An advertised resident takes it by default,
and `--server -` always writes locally. Submitting to a resident sequencer
makes concurrent appends from several actors safe. Both land in the
same sequence.

The projection comes from the same place that sequences the act. With a
resident, a command reads the projection it resolves its references against —
and, for the step commands, judges its act against — from that resident when it
stands at this checkout's head, under the same workroom and fold profile;
the command names anything else on standard error and answers it by verifying
the durable log locally. What that accepts and what it still checks appears in
[the architecture](../architecture.md). The fold preflight below, and the
admission that builds and signs the act, read the local verified log either
way.

## How long to wait for the resident

A submission gives the resident ten seconds to answer. That suffices for an
already warm resident, and matches the value every command used before
the wait became settable at all.

Raise it with `--deadline`, or with `GITSEQ_SUBMIT_DEADLINE` for every command
one instance runs; the flag wins where the caller gives both. Raise it when the
resident has just started and still verifies, when the workroom's log has grown
large enough that folding it takes longer than the wait, or when the act itself
runs large. The command refuses a value other than a positive duration before
signing anything, naming what it would accept.

An expired deadline does not mean a refusal. A refused act definitely did not land; an
expired wait means the resident may have sequenced the act and taken too long to
say so. `--idempotency-key` exists for that: submit again with the same key
and the answer says whether the act already sits in the log. Without a key, a
second submission appends a second act, so look for the first one before
retrying.

## See also

- [`gs ratify`](ratify.md), [`gs supersede`](supersede.md)
- [The work loop](../../concepts/work-loop.md)
