---
title: gs promise
summary: Claim one request addressed to you, once, with the request as its only basis.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs promise`

Files a promise on one request: the act that shows the board the work is
in flight.

A promise has one shape and the fold reads it strictly. It rests on
exactly one request, it is signed by the actor that request is addressed
to, and one commitment takes one closure. `gs state --kind promise` will
let you get each of those wrong; this command checks them first and
refuses with the repair named.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor accepting the request. |
| `--text` | *(names the request)* | The promise text. The default quotes the request it accepts. |
| `--branch` | | The branch this work will run on, recorded as advisory `body.branch`. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; an explicit loopback URL is honoured as given. |

It takes one positional argument, the request, after the flags:
`gs promise [flags] <request>`. The request accepts a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier, and what is signed is always the full identifier.

The idempotency key is derived from your fingerprint and the request, so
running the same claim twice replays the promise you already made instead
of filing a second one.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
git -C "$REPO" commit -q --allow-empty -m 'Initial commit'
BASE=$(git -C "$REPO" branch --show-current)
gs init --repo "$REPO" --operator alice >/dev/null
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Add a changelog' --body to=@bot --body conditions='CHANGELOG.md exists' \
  --body target_ref="refs/heads/$BASE")

gs promise --repo "$REPO" --as bot --branch request/changelog "$REQUEST"
```

## What it checks before signing

Each of these is refused before anything is appended, and each refusal
names what to do instead:

| what is wrong | the refusal names |
|---|---|
| the event is not a request | what it actually is, and `gs inspect` |
| the request is retired | its author, and `gs work --next` |
| the request is addressed to someone else | the addressee, and `gs reassign-if-unclaimed` |
| you already hold a live promise on it | that promise, `gs artifact` to report it, and `gs supersede` to withdraw it |
| the request is closed, withdrawn or reneged | its lifecycle status, and `gs inspect` |

A **stale** request is not refused. Staleness means a basis under the
request was retired: the reasoning moved, the conditions did not, and only
the request's author can replace it. The promise is filed and a warning on
standard error names the staleness and the repair — ask the author to
refile once the conditions, your availability and the governing decisions
are confirmed unchanged.

## Declining is not silence

A request addressed to you gets an answer every working cycle: a promise,
an explicit decline, or an assert saying why it is not yet actionable. The
fold admits a request's retirement only from its author or a `ratifier`,
never from you, so declining is an `assert` resting on the request plus a
request to the author to retire it:

```text
gs state --kind assert --rests-on <request> --text '<why you decline>'
```

The row stays open until the author retires it. That is their pending
retirement, not your neglect.

## What it produces

A `promise` statement resting on the request, carrying `body.branch` when
`--branch` was given. The commitment moves to `promised` and waits on you.
The event identifier is printed on standard output and nothing else is.

## See also

- [`gs artifact`](artifact.md), [`gs work`](work.md), [`gs state`](state.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
