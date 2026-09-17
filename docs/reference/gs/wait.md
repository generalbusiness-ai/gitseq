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

One invocation is one wake. [`gs status`](status.md) is a snapshot and
`gs work --next` is one shot, so following a workroom from a shell used to
mean a sleep loop around one of them, paying a whole verification or a whole
status fetch each time round to find out that nothing had happened — and
never able to tell "something happened" from "something happened for me".
This is the long poll the MCP [`wait`](../mcp/wait.md) tool uses, with the
loop, the presence lease and the cursor kept here.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | | The actor whose lanes are followed. Required; falls back to `GITSEQ_ACTOR`. |
| `--server` | | The resident to long-poll. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local watch below; an explicit loopback URL is honoured as given. |
| `--timeout` | `10m` | How long to wait before giving up. |
| `--until` | `actionable` | What counts as a wake: `actionable`, or `any`. |
| `--cursor-file` | per actor under `.git/gitseq` | Where this actor's cursor is kept between calls. |

A malformed invocation — an unknown `--until`, a timeout that is not
positive, a positional argument — prints the flags and one worked example
and exits non-zero, before anything is opened or dialled.

## Exit status

| status | meaning |
|---|---|
| `0` | Something changed. It is on standard output. |
| `3` | The deadline passed with nothing new. Nothing is printed. |
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
  request, promise or report of a commitment now standing in what is
  addressed to you or what waits on you, or a proposal now standing in your
  ratification lane. This is how "a lane changed" is decided without keeping
  the previous answer: a lane row is yours to move, and a new event inside
  that row is what moved it.
- **A new durable event resting directly on an event you signed** that is
  not retired: a verdict on your artifact, an assert on your promise. Such an
  event need not create a lane row at all, and it is exactly the news a poll
  watching only lanes would lose.

Nothing else wakes it. Somebody else's request to somebody else moves the
frontier and is not yours; presence and conversation churn is not durable
news. `--until any` drops the filter and wakes on any durable or live change
after the cursor, which is what the MCP tool does by default.

The filter is one function shared by the resident, the MCP tool and this
command, so `actionable` means the same thing on every surface. With
`--server` it is applied **at the resident**, inside its poll: a change that
is not yours leaves the poll ticking rather than crossing the socket for you
to discard.

A resident built before the filter existed decodes the request strictly and
refuses the field by name. `--until any` is sent as an *absent* field rather
than as the word, so it keeps working against such a resident — its default is
already `any` — and `--until actionable` says which side is behind and names
both repairs: restart the resident on this build, or ask for `any`. Such a
resident also returns no verdict of its own, so an unfiltered wait reads the
wake out of the delta it was sent: a frontier that moved, an event after the
cursor, a live change, a reset, or a pending frame. A rollout in either order
therefore never leaves this command with an error nobody can act on, nor
waiting out its deadline in silence.

## The cursor

Each call resumes from the last one. The cursor is written after every poll,
whether or not it woke, so events the filter declined are not reconsidered
next time; and it is written atomically, so a call interrupted mid-write
leaves a readable file behind.

The default file is `wait-cursor-<fingerprint>.json` under the repository's
`gitseq` directory in the common Git directory, so every worktree of one
checkout resumes the same cursor. `--cursor-file` names another. A file that
is missing, unreadable or from another workroom resumes from nothing, which
costs one replay and never a wrong answer.

## The presence session

`/v0/actor-wait` answers only a session, so this command opens one: the same
`POST /v0/presence` the MCP adapter sends, with this actor's name and a lease
of one minute. The resident opens the actor's key from this checkout, which
is why `gs wait` needs `--as` and the local key just as the adapter does. The
lease is renewed between polls, and the session departs on every exit path,
including an interrupt, so a stopped wait does not sit in the room's presence
list until its lease expires.

The credential stays in this process. It is never printed, never logged and
never written to the cursor file.

Presence opened this way is still [advisory session
attention](../../concepts/agent-practice.md): it is not a promise, a claim, a
report or a completion signal.

## Without a resident

With no resident — `--server -`, or nothing advertised — the command says so
on standard error and watches the local sequence ref every two seconds. A ref
that has not moved costs one cheap Git call; a ref that has moved buys one
verified local audit, and the same filter is applied to the same answer. This
is the path that keeps working across a resident restart, which is when an
agent most needs it.

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

The wake prints a header saying what moved, one line per durable event after
the cursor, one line per unacknowledged priority frame, and then this actor's
work in the exact shape `gs work --next` prints it:

```text
# gs wait woke at depth 4: 1 durable events after your cursor, 0 live changes, 0 unacknowledged priority chat frames.
# effective request by alice git:sha1:…#git:sha1:… — Add a changelog

# Next acts for bot: 1 rows on this page, 1 matching, 0 remaining.

# open available_to_you git:sha1:…#git:sha1:…
#   Add a changelog
gs promise --as bot git:sha1:…#git:sha1:…
#   or decline: gs state --as bot --kind assert --rests-on git:sha1:…#git:sha1:… --text '<why you decline>', and ask alice to retire it
```

## Cost

A quiet workroom costs one open connection and nothing else: the resident
holds one head clock for every waiter on a log, reads that ref four times a
second, and reads the verified snapshot again only when it moves. The
resident caps one poll at 30 seconds, so the loop here is client-side: one
`gs wait` is one wake however long it waits, rather than one call every half
minute. A wake then costs one bounded work query and one projection read, the
same as one `gs work --next`.

## See also

- [`gs work`](work.md), [`gs status`](status.md), [`gs promise`](promise.md)
- [MCP `wait`](../mcp/wait.md), [MCP `ack`](../mcp/ack.md)
- [The work loop](../../concepts/work-loop.md)
