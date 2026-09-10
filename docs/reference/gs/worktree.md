---
title: gs worktree
summary: Create one checkout for one governing record, stamped with the record it is for and with an attempt ref that outlives it.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7e086435a146763884518c84d649fc15cbcbb9a8
---

# `gs worktree`

Creates one checkout for one governing record and stamps it, so that the work
done in it can say which durable record it is for and so that the lane can be
found again after the checkout is gone.

It signs nothing and reads no private key. `--as` names the acting actor only
so that "a request addressed to me" is a question this command can ask.

Five things are made, and each of them is a claim rather than evidence:

| What | Where |
|---|---|
| the checkout | the `--path` you name |
| the branch | `refs/heads/<--branch>`, created at `--start` |
| the record | `<the checkout's own Git directory>/gitseq/checkout.json` |
| the commit-message template | `<the same directory>/gitseq/rests-on` |
| the attempt ref | `refs/gitseq/lanes/<governing event hash>/<attempt>` |

None of them widens what may be deleted, authorises a signature or moves a
merge. Durable admission reads the durable record alone: a settled commitment
refuses exactly the acts it refuses today, whatever this command was told.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom. |
| `--as` | | The acting actor. Required; falls back to `GITSEQ_ACTOR`. Read for the addressee check and nothing else. |
| `--path` | | The destination directory for the new checkout. Required. |
| `--branch` | | The branch to create. Required. |
| `--start` | `HEAD` | The revision the new branch starts at. |
| `--settled-ok` | `false` | Create even though the governing commitment has already settled. |
| `--duplicate-ok` | `false` | Create even though another checkout of this repository is already at this head. |

Neither the destination nor the branch has a default. Inventing a directory
layout or a branch naming convention here would be this command deciding
something nobody wrote down.

## The governing record

The selector resolves through the same boundary every other `gs` command uses,
so `#N`, an unambiguous hash fragment and a full canonical identifier all work,
and what is stamped is always the full canonical identifier. A `Rests-On:`
trailer takes the full identifier only: a commit message is not a boundary
anything resolves at.

Two shapes of record govern a checkout:

- a **request addressed to the acting actor**, which is assigned work;
- an **adopted decision** — a ratified proposal, or a satisfied
  authority-bearing request — which is how self-initiated work is named without
  inventing a self request.

The assigned shape is tried first. A request addressed to you whose commitment
has closed is a dead lane whatever else it may also serve as, and that is what
`--settled-ok` is for.

## The eight checks

Every check reports one of four words, and each word means exactly one thing
for creation.

| word | what happened | does creation continue? |
|---|---|---|
| `passed` | the check looked and found nothing wrong | yes |
| `refused` | a determinate negative | no |
| `warned` | a determinate negative the design says must not refuse outright, and its confirmation flag was given | yes |
| `not established` | the check could not look | yes |

`not established` is the important one. An unreadable durable record set
yields no answer about the record kind, the addressee or the settlement — and
unknown is not a negative and is not a consent. Those three checks say they
could not be established, and creation proceeds on a full canonical
identifier, because what it makes grants nothing. A `#N` or a hash fragment
against an unreadable log is refused earlier, by the resolver, for the reason
it has always refused: the durable event set could not be read, so there is no
identifier to stamp.

The four determinate refusals need no durable record at all and still fire:

- the **governing record** is a canonical identifier of another workroom, or is
  not a canonical identifier;
- the **destination** does not resolve, is already a symbolic link, or already
  exists as anything but an empty directory;
- the **repository** is wrong: the destination is inside this repository's own
  Git directory, or inside a checkout it already has;
- the **branch** already exists. Creation makes a branch rather than taking one
  over.

## The checkout root

`gitseq.checkoutRoot`, in the repository's own Git configuration, names the one
directory this repository expects its checkouts to live under:

```text
git config gitseq.checkoutRoot /path/to/checkouts
```

One value answers two questions. Creation refuses a destination outside it, and
the cleanup advice at `GET /v0/worktrees` protects a checkout whose resolved
path is outside it.

Unset is the ordinary case, and it is the absence of a boundary rather than a
second boundary. The checkout-root check then reports `not established`,
because there is nothing configured for a destination to be outside of, and
creation proceeds. Cleanup advice keeps the fallback it has always had, the
directory holding the served checkout: a root that is too narrow can only
protect a checkout that did not need it. That fallback is never treated as
though somebody had chosen it, so it never refuses a creation.

A relative value resolves against the repository's top level. Containment
compares resolved paths by path elements, so a symbolic link cannot carry a
destination out of the root while its name says otherwise, and `/a/bc` is not
inside `/a/b`.

Setting a wider root marks fewer checkouts as outside it, so it can enlarge
the set of checkouts cleanup advice offers. That sits inside this repository's
existing trust boundary and does not widen it: a caller who can write this
repository's configuration can already run a program of their choosing during
an ordinary read.

## Attempt refs

`refs/gitseq/lanes/<governing event hash>/<attempt>` points at the lane tip and
mirrors `refs/gitseq/merge-receipts/<key>`: an ordinary shared ref in the
repository's common directory, outside the durable event log, that no fold
reads.

The attempt suffix is what gives each recut its own ref. One ref per governing
hash could not, because a recut under one request shares that request's hash.

Allocation is a create against a missing ref: the compare-and-swap carries an
empty expected old value, which is Git's "this ref must not exist". An existing
attempt is never reused, whatever it points at, so a second checkout for one
record takes attempt 2. Competing creation loses cleanly and the loser takes
the next number; a refusal that leaves the ref still free is transient and
keeps its number. Allocation walks at most eight attempts and then refuses
rather than waiting.

Nothing here deletes anything. Attempts accumulate, the ref survives the
checkout being removed and the branch deleted, and removal stays a separate
deliberate act.

Read back, an attempt ref is `claimed`. `GET /v0/worktrees` carries the
attempts and the checkout records beside the branch associations, graded by the
same rule: promotion still needs a signed artifact statement naming that exact
commit. See [landing observations](../landing-observations.md).

## Trailer assistance

The template file holds an empty subject line, a blank line and the exact
trailer, which is what makes the line a trailer rather than the last sentence
of a paragraph:

```text

Rests-On: git:sha1:<genesis>#git:sha1:<event>
```

Point `git commit -t` at it, or copy the line. The adopted design also
describes an optional commit hook that appends the same line; it is not part of
this command, and nothing depends on one — `gs merge` deliberately runs no
commit hooks at all.

## Example

```sh
REPO="$(mktemp -d)/project"
git init -q -b main "$REPO"
git -C "$REPO" commit -q --allow-empty -m "seed"
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
gs actor-add --repo "$REPO" --as alice --name bot --kind agent >/dev/null

REQUEST=$(gs state --repo "$REPO" --as alice --kind request \
  --text 'Reconcile the identity layers' --body to=@bot \
  --body conditions='an approved head lands' \
  --body target_ref=refs/heads/main --rests-on "$SEED")

ROOT="$(dirname "$REPO")"
git -C "$REPO" config gitseq.checkoutRoot "$ROOT"
gs worktree --repo "$REPO" --as bot --path "$ROOT/work" \
  --branch request/identity "$REQUEST"

git -C "$REPO" for-each-ref --format='%(refname) %(objectname)' refs/gitseq/lanes/
cat "$(git -C "$ROOT/work" rev-parse --absolute-git-dir)/gitseq/rests-on"
```

The checks go to standard error, one line each, and what was made goes to
standard output. A creation that fails part-way still names what it made:
nothing here removes anything.

## See also

- [Landing observations](../landing-observations.md), for the read-only
  checkout association and cleanup advice these refs and records are read back
  through
- [`gs merge`](merge.md), [`gs state`](state.md), [`gs work`](work.md)
- [Event identifiers](../event-identifiers.md)
