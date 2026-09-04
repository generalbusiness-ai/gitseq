---
title: gs status
summary: Project the current state of the workroom, bounded by default.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9936cbb28db1642a5cdabd2f787fb881fb33dbf2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:35a8c246effe4f81fe54aac7ebd260f8fb3888d4
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
---

# `gs status`

Runs the fold over the sequence and prints what is current and
actionable: commitments and who they wait on, artifacts and their
staleness, and the acts that took no force.

The default view is **bounded**. Everything is still there — `--all` and
`--json` render it — but a workroom accumulates satisfied commitments and
retired artifacts forever, so the default answers "what now" rather than
"what ever".

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--all` | `false` | Render the complete commitment, artifact and attempt tables instead of the bounded view. |
| `--json` | `false` | Emit the complete snapshot as JSON, with no human view. |
| `--server` | | Read from a resident service instead of folding locally, falling back to the local read if that fails. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

`--all` and `--json` are mutually exclusive; asking for both is refused.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='it exists' \
  --rests-on "$SEED" >/dev/null

gs status --repo "$REPO"
gs status --repo "$REPO" --all | head -5
gs status --repo "$REPO" --json | head -5
```

## The bounded view

The header names the frontier, its depth, and where the answer came
from — `verified local`, `resident summary`, or `verified local
fallback`. Then a line of totals, and six sections:

| Section | What is in it |
|---|---|
| Actionable commitments | Commitments still in flight: `open`, `promised`, `reported`, `awaiting-review`, `awaiting-authorization`, `awaiting-landing`. |
| Needs attention | Live commitments in any other state — `stale`, `reneged`, `cancelled`. Terminal `superseded` rows stay in history. |
| Current artifacts | Artifacts that are neither retired nor stale. |
| Stale artifacts | Artifacts that were retired, and artifacts a retirement reached. |
| Dissents | Standing objections, each naming the act it is recorded against. |
| Non-effective attempts | Acts judged ineffective or disputed, with the reason. |

Superseded, satisfied, withdrawn, and abandoned commitments are finished, and are counted in the
totals rather than listed.

`awaiting-review`, `awaiting-authorization` and `awaiting-landing` name an
artifact-backed completion at its three stages. The artifact's satisfier is
`none`, so requester ratification never closes one. `awaiting-review` waits on
the performer, who must obtain an independent approval of the exact head.
`awaiting-authorization` waits on the hold owner named by a held request.
`awaiting-landing` waits on the performer, who signs the merge into the ref the
request named.

`abandoned` names an approved head a supersession deliberately dropped rather
than carrying into a successor. It is terminal, and distinct from `cancelled`:
it says work that had been approved was given up, not that the request was
withdrawn.

`superseded` names a request whose work explicitly moved to a successor: a
rejected implementation parent whose required repair was transferred to one
qualifying child, or a request whose approved head was carried into a successor
that rests on it. Its JSON row carries `successor_request`; it is terminal
history, not cancelled work and not a satisfied implementation. A supersession
that dropped an approved head instead of carrying it reads `abandoned`.

## Two kinds of staleness

Both are called staleness, and only one of them is a reason to stop.

**Ordinary reasoning staleness** means a basis under a record was
retired. The reasoning that led to the record moved; the record itself
did not. It blocks nothing: a merge may still land the exact head an
approval named, and it records the movement in its receipt.

**A superseded world** is narrower. It means the retired ancestor was
itself an artifact, so the behaviour the record describes has been
replaced. An approval carrying it cannot merge, and the work needs a
fresh artifact on current bases rather than another review of the same
chain.

The difference matters here because the first one is ordinary. In a
workroom of any age most closed commitments and most artifacts carry it,
so a mark on every row would fire almost everywhere and tell a reader
nothing about which row to pick — and a warning that fires everywhere
teaches people to ignore the one that does not.

So the default view **counts ordinary staleness and marks no row with
it**:

- Commitment rows carry their lifecycle status and no stale mark. The
  totals line carries the fact per lane instead: `reported 27 (24
  stale)` says how many of that lane's commitments rest on something
  retired.
- A superseded, satisfied, or withdrawn commitment stays out of the lists whether or
  not it is stale. It is finished, and a basis moving under it afterwards
  does not reopen it. The totals still count it.
- A commitment that was never reported has no outcome to preserve, so
  the fold gives it the status `stale` outright. That is unfinished work,
  it appears under "Needs attention", and the word is not repeated.

The loud facts stay on their own rows. A retired artifact reads
`retired`, and an artifact describing a superseded world says so in its
notes, wherever either occurs. The totals line counts each of them
separately from ordinary staleness:

```
Artifacts: 55 current, 121 stale, 1037 retired, 388 describing a superseded world.
```

Nothing is lost. [`--all`](#all) prints a `qualifiers` column with
`stale` on every commitment that carries it, and `--json` carries the
`stale` field on every record. The bounded summary the resident serves
keeps its per-row `stale` field too — only the rendered page is quiet.

## Why this disagrees with the Work drawer

The browser UI's Work counts and these totals describe different
populations on purpose, and neither is a rounding of the other.

`gs status` reports commitments by lifecycle status, and reports
artifacts on their own line. It never adds the two together, because an
artifact is not a commitment and a total mixing them matches nothing you
can act on.

The Work drawer answers a different question — what a reader should look
at — so it derives `active`, `closed` and `attention` from those same
commitments, and shows artifacts needing attention beside them rather
than inside them. `attention` deliberately overlaps `active` and
`closed`, because staleness and dispute are qualifiers sitting on top of
a lifecycle status: most of what needs attention is already counted
somewhere else. Adding the drawer's three figures together therefore
exceeds the number of commitments, which is correct and is why they are
not presented as a partition.

So a number here and a number there can differ while both are right. If
you are reconciling them, compare like with like: the drawer's `total`
is the commitment count, and everything else is a different cut of it.

Each list keeps the **newest 20** entries and says exactly how many older
ones it omitted — "Showing 20 of 500; 480 older omitted" — so a shortened
list never reads as a complete one. Request text is normalized to one
line and capped at 240 bytes. The exact numbers are in
[Limits](../limits.md).

An open unclaimed request names the actor it is addressed to and shows as
`addressed to NAME — unclaimed`, rather than inventing a debt against
someone who has not promised anything.

Artifact rows carry a state and any notes:

| State | Meaning |
|---|---|
| `current` | It stands, and nothing under it has been retired. |
| `succeeded` | It was superseded, and the act that superseded it rests on an artifact covering the same path. The pointer moved; the log says where. |
| `retired` | It was superseded and no successor was named. The pointer was withdrawn and there is nowhere to follow it to. |
| `stale` | A basis was retired. Re-check the thing it describes. |

All three non-current states are listed under stale artifacts, because to
a reader looking for what is current they mean the same thing: not this
one. They are named apart because a replaced pointer, a withdrawn one and
a moved world call for different work.

Succeeded and retired are the same act read for different content, and
the difference is what the act rested on. `gs supersede` naming a
successor at the same path — which is what every merge does — says the
behaviour moved there. A bare `gs supersede` says the behaviour is gone,
or the claim was never true. Only the second makes the reasoning above it
flare, so a finished loop no longer flares itself on the merge that
finished it. In the `--all` tables these read `SUCCEEDED — replaced at
the same path` and `RETIRED — withdrawn with no successor`.

| Note | Meaning |
|---|---|
| `describes a superseded world` | The retired ancestor was itself an artifact, so the implementation has been replaced. |
| `unable to flare` | It cites nothing resolvable, so nothing could ever make it stale. Its silence is not currency. |
| `succession not recorded` | An earlier artifact for the identical path is still live — a probable forgotten supersession. |

## `--all`

The complete human-readable tables: every commitment, every artifact,
every non-effective attempt, with no cap. The artifact summary under that
table reports both the number of rows and the number of supersessions
**actually owed**. Those differ: one forgotten retirement at a long-lived
path repeats on every later link of the chain, so the row count
overstates how many situations there are to fix.

## `--json`

The complete snapshot: the `genesis`, `head` and `depth`, the whole
`projection` — `decisions`, `acts`, `statements`, `commitments`,
`artifacts`, `actors`, `provenance` and the counts — and the
`vocabulary` in force. Use it when you need whole event identifiers,
which both human views abbreviate for reading.

## Selecting instead of dumping

`--json` prints the complete snapshot, which is the right answer when you
need whole event identifiers and the wrong one when you need a subset. A
subset has its own commands, and each of them makes its selection before
anything is rendered:

| Question | Command |
|---|---|
| Which artifacts sit at this exact path, and in which state? | [`gs artifacts --path <p> --state live\|retired\|succeeded\|all`](artifacts.md) |
| Which artifacts still anchor to that path, however many hops away? | [`gs artifacts --reaches <p>`](artifacts.md) |
| What does one actor still owe, or wait on? | [`gs work --as <actor>`](work.md) |
| What is this one event, and what does it rest on? | [`gs inspect <event>`](inspect.md) |
| Is the review queue quiet enough to run an irreversible step? | [`gs reviews --branch main`](reviews.md) |

Each answers in a bounded page and says what it left out, and each prints
the same JSON shape the matching MCP tool returns, so a caller learns one
shape rather than one per surface.

## `--server`

`--server http://127.0.0.1:7777` asks a resident service for the answer
instead of folding the log here. The URL must be an HTTP **loopback**
address with no credentials, path, query or fragment; anything else is
refused outright. With the flag omitted, `gs status` asks the resident this
repository publishes and folds locally only when nothing is advertised;
`--server -` always folds locally.

Nothing advertised means the record is not there. A record that is there and
cannot be trusted — unreadable, larger than 8 KiB, not a record, carrying no
address, naming another workroom, or carrying an address that is not a bare
loopback origin — is refused with the reason and with `--server -` named as
the way out. That is the same refusal every `gs` command makes, reads
included, so a repository whose advertisement has been tampered with does
not answer some questions and refuse others.

The default view is read from the resident's bounded summary endpoint.
That read is deliberately narrow: no redirects are followed, the response
is limited to 64 KiB and the request to two seconds, and the returned
genesis, head, depth and cursor must still match the workroom selected
here. `--all` and `--json` use the resident's full response instead, with
a larger limit and a longer deadline.

A refusal, a timeout, an oversized response, a stale head, or a head that
moves while the answer is being read is **named on standard error** and
the command then does the verified local read instead. The header says
`verified local fallback`, so a fallback answer is never presented as a
resident one.

This fallback belongs to reading, and only after the resident has been
asked and the request or the response failed. It is not what happens when
the advertisement itself cannot be trusted. An unreadable, oversized,
unparseable or addressless `resident.json`, or one naming another
workroom, refuses here as it does everywhere, before any request is made
— see [`gs serve`](serve.md). Durable writes never fall back at all.

## Cost

The local read tries the application-owned checkpoint selector under
`.git/gitseq` and the local Git reachability ref, verifies the
sequencer-signed checkpoint object they name, and verifies the tail that
descends from it. A resident restart and a no-server `gs` process use this same
path. If no checkpoint is usable it performs the
ordinary full audit, and prints a progress line after one second rather
than appearing to hang. [`gs verify`](verify.md) never takes the
checkpoint shortcut: it always audits the whole sequence.

`gs checkpoint-clear --repo <path>` removes the application selector and
rewinds the checkpoint ref to genesis. The next new process performs a cold
audit and rebuilds them. Stop a resident before clearing if the resident itself
must restart cold, because this command cannot erase another process's verified
memory. Set `GITSEQ_CHECKPOINT=off` to disable checkpoint loading and writing
for one command or process without changing the persistent selectors.


## See also

- [`gs work`](work.md), [`gs artifacts`](artifacts.md), [`gs inspect`](inspect.md), [`gs reviews`](reviews.md)
- [`gs provenance`](provenance.md), [`gs verify`](verify.md)
- [Staleness](../../concepts/staleness.md)
