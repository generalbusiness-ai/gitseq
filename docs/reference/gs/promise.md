---
title: gs promise
summary: Claim one request addressed to you, once, with the request as its only basis.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2d135abad0f792f493ac3124c7025d4ff0076d5c
---

# `gs promise`

Files a promise on one request: the act that shows the board the work now
in flight.

A promise has one shape and the fold reads it strictly. It rests on
exactly one request, the actor that request addresses signs it, and one
commitment takes one closure. `gs state --kind promise` will
let you get each of those wrong; this command checks them first and
refuses with the repair named.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor accepting the request. |
| `--text` | *(names the request)* | The promise text. The default quotes the request it accepts. |
| `--branch` | | The branch this work will run on, recorded as advisory `body.branch`. |
| `--server` | | Submit through a resident sequencer instead of writing locally. Default: the resident URL this repository publishes (see `gs serve`); `-` forces the local fold; the command honours an explicit loopback URL as given. |
| `--deadline` | `10s`, or `GITSEQ_SUBMIT_DEADLINE` | How long the resident has to answer each submission. Raise it for a cold or loaded resident, or one folding a large log; the command refuses any value other than a positive duration before it signs anything. |

It takes one positional argument, the request, after the flags:
`gs promise [flags] <request>`. The request accepts a
[short reference](../event-identifiers.md#typing-one-at-a-boundary) as well as
the canonical identifier, and the command always signs the full identifier.

The command derives the idempotency key from your fingerprint and the request,
so running the same claim twice replays the promise you already made — same
event identifier, nothing appended — instead of filing a second one. A promise
filed some other way, by hand or by another surface, does not count as this
command's act: the command refuses to claim again over one of those.

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

The command refuses each of these before it appends anything, and each
refusal names what to do instead:

| what went wrong | the refusal names |
|---|---|
| the event names no request | its actual kind, and `gs inspect` |
| someone has retired the request | its author, and `gs work --next` |
| the request addresses someone else | the addressee, and `gs reassign-if-unclaimed` |
| you already hold a live promise on it | that promise, `gs artifact` to report it, and `gs supersede` to withdraw it |
| the request has closed or its author has withdrawn it | its lifecycle status, and `gs inspect` |

The command does not refuse a **reneged** request either. A withdrawn promise
stays visible forever, and it locks no door: the fold admits a fresh
promise on the request, and the new claim carries the promise it follows
in its key, so it neither replays the withdrawn one nor collides with it.

The command does not refuse a **stale** request. Staleness means someone
retired a basis under the request: the reasoning moved, the conditions did
not, and only the request's author can replace it. The command files the
promise, and a warning on standard error names the staleness and the repair —
ask the author to refile once the conditions, your availability and the
governing decisions check out unchanged.

## Declining differs from silence

Declining takes an `assert` resting on the request, plus asking its author to
retire it — the fold admits that retirement only from the author or a
`ratifier`, never from you, so the row stays open until they act:

```text
gs state --kind assert --rests-on <request> --text '<why you decline>'
```

## What it produces

A `promise` statement resting on the request, carrying `body.branch` when
you gave `--branch`. The commitment moves to `promised` and waits on you.
The command prints the event identifier on standard output and nothing else.

## See also

- [`gs artifact`](artifact.md), [`gs work`](work.md), [`gs state`](state.md)
- [Run a work loop](../../how-to/run-a-work-loop.md)
