---
title: gs wait
summary: Block until something this actor can act on changes, then print it as gs work --next does.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:afb09e38c640e12c625f0fb3cb2069f670ebaedf
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6ea4ae86b6807b16474e5dc5ae7dfa485d2836fe
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:8c93b4d4577389c55898a93b307f1f951d645fb8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49dc369e791419e55f10810aec9bfc13f7d2cf56
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:f5b22ae0cf87ec8004cf367f1f234d846fd0b17d
---

# `gs wait`

Blocks until something you can act on has changed, or a deadline passes,
and prints what changed the way [`gs work --next`](work.md#next-what-to-type)
prints it, with the exact command each row owes.

One invocation makes one wake. [`gs status`](status.md) takes a snapshot and
`gs work --next` fires one shot, so following a workroom from a shell used to
mean a sleep loop around one of them, paying a whole verification or a whole
status fetch each time round to find out that nothing had happened — and
never able to tell "something happened" from "something happened for me".
This command runs the long poll the MCP [`wait`](../mcp/wait.md) tool uses,
and keeps the loop, the presence lease and the cursor here.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | | The actor whose lanes the command follows. Required; falls back to `GITSEQ_ACTOR`. |
| `--server` | | The resident to long-poll. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local watch below; the command honours an explicit loopback URL as given. |
| `--timeout` | `10m` | How long to wait before giving up. |
| `--until` | `actionable` | What counts as a wake: `actionable`, or `any`. |
| `--cursor-file` | per actor under `.git/gitseq` | Where the command keeps this actor's cursor between calls. |

A malformed invocation — an unknown `--until`, a non-positive timeout, a
positional argument — prints the flags and one worked example
and exits non-zero, before it opens or dials anything.

## Exit status

| status | meaning |
|---|---|
| `0` | Something changed. The command prints it on standard output. |
| `3` | The deadline passed with nothing new. The command prints nothing. |
| other | The command failed, and says why on standard error. |

A loop branches on the status and never has to parse the output:

```text
while :; do
  gs wait --as bot --timeout 10m
  case $? in
    0) ;;      # act on what it printed
    3) ;;      # nothing yet; go round again
    *) break;; # something is wrong
  esac
done
```

## What counts as a wake

`--until actionable`, the default, wakes on exactly three things.

- **Unacknowledged priority chat.** A frame addressed to you by name. It
  repeats until [`ack`](../mcp/ack.md) receives its thread handle.
- **A new durable event inside one of your own actionable lanes**: the
  request, promise or report of a commitment now standing in what someone
  addressed to you or what waits on you, or a proposal now standing in your
  ratification lane. The filter decides "a lane changed" this way without
  keeping the previous answer: a lane row waits for your move, and a new event
  inside that row moved it.
- **A new durable event resting directly on an event you signed** that
  remains unretired: a verdict on your artifact, an assert on your promise. Such an
  event need not create a lane row at all, and it carries exactly the news a poll
  watching only lanes would lose.

Nothing else wakes it. Somebody else's request to somebody else moves the
frontier and does not concern you; presence and conversation churn makes no
durable news. `--until any` drops the filter and wakes on any durable or live
change after the cursor, as the MCP tool does by default.

Work *leaving* your lanes does not wake it either, by construction: nothing
here looks at a row's previous state. The retirement of a request addressed to you,
and a reassignment of it away from you, both pass in silence — the row simply
drops out of your lanes on the next answer. A merge receipt closing your
commitment deserves a note: the closure itself wakes nothing, but the
receipt normally rests on artifacts you signed, and rule three covers that, so it
usually does wake you. When you need to know what has gone rather than what has
arrived, read [`gs work --next`](work.md); a wait serves work arriving.

The resident, the MCP tool and this command share one filter function, so
`actionable` means the same thing on every surface. With `--server` the filter
runs **at the resident**, inside its poll: a change that does not concern you
leaves the poll ticking rather than crossing the socket for you to discard.

It decides over **every** event after your cursor, not over the delta the
answer prints. A response stops at fifty events; a decision about whether
to wake somebody does not, or fifty unrelated acts arriving behind the one
request that concerned you would bury it — and the cursor would then move past it,
so no later wait could report it either.

A resident built before the filter existed decodes the request strictly and
refuses the field by name. The command sends `--until any` as an *absent*
field rather than as the word, so it keeps working against such a resident —
its default already reads `any` — and `--until actionable` says which side
lags and names both repairs: restart the resident on this build, or ask for
`any`. Such a resident also returns no verdict of its own, so an unfiltered
wait reads the wake out of the delta it received: a frontier that moved, an
event after the cursor, a live change, a reset, or a pending frame. A rollout
in either order therefore never leaves this command with an error nobody can
act on, nor waiting out its deadline in silence.

## The cursor

Each call resumes from the last one. The command writes the cursor after every
poll, whether or not it woke. That stays safe because the filter judged every
event after the cursor, not just the printed ones: the filter has already seen
and decided an event it declined, so not reconsidering it next time loses
nothing. The command writes the file atomically, so a call interrupted
mid-write leaves a readable one behind.

A wake writes its cursor only after the wake has printed. If the rendering
fails, the cursor stays where it stood and the next call sees the same news
again, rather than losing the news for good.

The default file, `wait-cursor-<fingerprint>.json`, lives under the repository's
`gitseq` directory in the common Git directory, so every worktree of one
checkout resumes the same cursor. `--cursor-file` names another. A missing or
unreadable file, or one from another workroom, resumes from nothing, which
costs one replay and never a wrong answer.

## The presence session

`/v0/actor-wait` answers only a session, so this command opens one: the same
`POST /v0/presence` the MCP adapter sends, with this actor's name, a lease of
one minute, and the activity status `waiting` — what the session actually
does. The resident opens the actor's key from this checkout, so `gs wait`
needs `--as` and the local key just as the adapter does. The command renews
the lease between polls, and the session departs on every exit path,
including an interrupt, so a stopped wait does not sit in the room's presence
list until its lease expires.

This session stands on its own. An actor who also has an MCP adapter attached
will show **two** live sessions in the room while `gs wait` runs, and the
room's presence list will say `waiting` for this one. That gives the honest
picture — two processes attend — and it clears when the command exits.

The credential stays in this process. The command never prints it, never logs
it and never writes it to the cursor file.

Presence opened this way still counts as [advisory session
attention](../../concepts/agent-practice.md): it carries no promise, claim,
report or completion signal.

## Priority chat, and its one limit

A frame addressed to you by name wakes the wait and prints with it. Know two
things before relying on it.

The inbox belongs to the session, and the session dies with the command, so
`gs wait` sees only the frames that arrive **while one of its polls stays open**.
A frame sent between two invocations reaches the session open at the
time, not the next one.

No `gs ack` exists: acknowledging a thread takes an [MCP `ack`](../mcp/ack.md)
call, and it acknowledges the session that made it. From the CLI you therefore
read a frame once, in the wake that carried it, and it never repeats — because
the session that received it has gone.

## When the resident does not answer

Three things a resident can say do not count as answers, and each has an
obvious repair, so this command performs it inside your `--timeout` rather than
failing:

- **Its wait budget has filled up** (`429`; the resident holds at most 64 long polls
  across both wait routes). The command pauses briefly and asks again, because
  the refusal asks for that.
- **Your session lapsed** — the resident restarted and threw away the
  credential it minted. The command says so on standard error, opens another
  session, and carries on. Three lapses in one invocation mean a resident that
  will not keep a session, and the command reports that rather than looping on it.
- **It stopped answering at all.** After two unanswered polls the command says
  so on standard error and spends what remains of the deadline on the local
  watch below, as the MCP adapter does with the same failure.

## Without a resident

With no resident — `--server -`, nothing advertised, or one that stopped
answering mid-wait — the command says so on standard error and watches the
local sequence ref every two seconds. A ref that has not moved costs one cheap
Git call; a ref that has moved buys one verified local audit, and the command
applies the same filter to the same answer. This path keeps working
across a resident restart, when an agent most needs it.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"

# Nothing is owed to bot yet, so this waits and then says so with status 3.
gs wait --repo "$REPO" --as bot --server - --timeout 3s || echo "nothing new: exit $?"

# Now alice asks bot for something. The next call wakes on it, resuming from
# the cursor the first call left behind.
gs state --repo "$REPO" --as alice --kind request --text 'Add a changelog' \
  --body to=@bot --body conditions='the page is written' \
  --body no_git_artifact=true --rests-on "$SEED" >/dev/null
gs wait --repo "$REPO" --as bot --server - --timeout 30s
```

The wake prints the reason it woke and then what to do about it: under
`--until actionable`, one line per event the filter accepted; under
`--until any`, one line per durable event after the cursor, up to the
fifty-event cap with a count of the rest. Then one line per unacknowledged
priority frame, and then this actor's work in the exact shape
`gs work --next` prints it.

```text
# effective request by alice git:sha1:…#git:sha1:… — Add a changelog

# Next acts for bot: 1 rows on this page, 1 matching, 0 remaining.

# open available_to_you git:sha1:…#git:sha1:…
#   Add a changelog
gs promise --as bot git:sha1:…#git:sha1:…
#   or decline: gs state --as bot --kind assert --rests-on git:sha1:…#git:sha1:… --text '<why you decline>', and ask alice to retire it
```

## Cost

A quiet workroom costs one open connection and nothing else. The resident
holds **one** head clock per log, not one per waiter: it reads the head ref
four times a second while any poll stays open, and each waiter reads the verified
snapshot again only when that clock moves.

A poll under `--until actionable` keeps ticking through changes it declines,
and a change it has judged counts as a change it has seen: the poll moves its
own baseline past it, so it neither re-judges nor re-reads the same one on the
next tick. Without that, one presence announcement — which every `gs wait`
makes on its own arrival — kept a new Git process starting every 250 ms for the
rest of the poll.

The resident caps one poll at 30 seconds, so the loop here runs client-side:
one `gs wait` makes one wake however long it waits, rather than one call every
half minute. A wake then costs one bounded work query and one projection read,
the same as one `gs work --next`.

## See also

- [`gs work`](work.md), [`gs status`](status.md), [`gs promise`](promise.md)
- [MCP `wait`](../mcp/wait.md), [MCP `ack`](../mcp/ack.md)
- [The work loop](../../concepts/work-loop.md)
