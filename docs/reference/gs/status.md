---
title: gs status
summary: Project the current state of the workroom.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:57e4bc379b4f3539155eb83b13c359567e436aff
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cccadaa785ee972d3154690bb4ad262d1dcd9633
---

# `gs status`

Runs the fold over the whole sequence and prints the projection:
commitments and who they wait on, artifacts and their staleness, and the
acts that took no force.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--json` | `false` | Print the full projection as JSON instead of a summary. |
| `--server` | | Read from a resident service, which adds live presence and conversations. |

`--server` replaces the local read entirely: the response comes from the
service, and `--json` does not apply to it.

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
gs status --repo "$REPO" --json | head -5
```

## Reading the output

**Requests and commitments.** One row per request, with its status, the
requester, the assignment, and who it is waiting on. An unclaimed request
shows `addressed to … — unclaimed` rather than inventing a debt against
someone who has not promised anything.

**Artifacts.** One row per artifact statement, with its state and any
notes:

| State | Meaning |
|---|---|
| `current` | Nothing it rests on has been retired. |
| `STALE` | A basis was retired. Re-check the thing it describes. |
| `STALE — describes a superseded world` | The retired ancestor was itself an artifact, so the implementation has been replaced. |

| Note | Meaning |
|---|---|
| `unable to flare` | It cites nothing resolvable, so nothing could ever make it stale. Its silence is not currency. |
| `succession not recorded` | An earlier artifact for the identical path is still live — a probable forgotten supersession. |

The summary under the table reports both the number of rows and the
number of supersessions **actually owed**. Those differ: one forgotten
retirement at a long-lived path repeats on every later link of the chain,
so the row count overstates how many situations there are to fix.

**Attempts.** Every act judged other than effective, with the reason.
This section is the record's honesty about what was tried. It is not a
list of bugs.

## `--json`

The full projection: `decisions`, `acts`, `statements`, `commitments`,
`artifacts`, `actors`, `provenance`, and the counts. Use it when you need
whole event identifiers, which the summary abbreviates for reading.

## Local worktrees

The browser view served by a resident also reports local checkout state —
basenames, branch and HEAD, and explicit clean, dirty, detached, bare,
locked, prunable or unavailable state. That is never part of the durable
projection. A checkout associated with a commitment only through a
commit's `Rests-On:` trailer is marked **unverified trailer**, because
trailer text is not an actor-signed statement.

## See also

- [`gs provenance`](provenance.md), [`gs verify`](verify.md)
- [Staleness](../../concepts/staleness.md)
