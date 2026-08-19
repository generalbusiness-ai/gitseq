---
title: Getting started
summary: Build the binaries, create a workroom in a repository you already have, and take the first path into the documentation set.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:34c5f09e2f5bc4e4fa5acb7404ae9b7df4808e52
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a3cd3c438a2a5eaac579ddc22ccccde367a49177
---

# Getting started

Requires **Go 1.26** and **Git with SSH signing support**.

```text
git clone https://github.com/generalbusiness-ai/gitseq.git
cd gitseq
make test
make vet
make build
```

`make build` puts `gs` and `gitseq-mcp` in `bin/`. Put them on your path:

```text
export PATH="$PWD/bin:$PATH"
```

## Create a workroom

A workroom is an overlay on a repository you already have — yours, not
gitseq's. Point `gs init` at that repository and name yourself as the
operator:

```text
gs init --repo /path/to/your/repo --operator "$(whoami)"
```

The same command against a scratch repository, so the example on this page
is one that runs:

```sh
REPO="$(mktemp -d)/project"
git init -q "$REPO"
gs init --repo "$REPO" --operator alice
```

`init` prints three things: the **genesis** hash, the operator's
fingerprint, and the seed event. Keep the genesis — everyone who attaches
later needs it.

### What it creates, and what it leaves alone

Init writes a `gitseq/` directory inside the repository's **git common
directory**: a config file, a sequencer key, and your operator key under
`actors/`. In an ordinary checkout that is `.git/gitseq/`. In a linked
worktree `.git` is a file rather than a directory, and in a bare
repository there is no `.git` at all, so derive the location rather than
assuming it:

```text
git rev-parse --git-common-dir
```

In the repository itself, init creates a parentless commit over an empty
tree — the genesis — and then appends one event, the roster statement that
makes you the operator. A single ref, `refs/seq/<genesis>`, points at the
tip.

Two keys are made, and they sign different things. The **sequencer key**
signs the genesis commit and the sequence itself. Your **operator key**
signs your acts, starting with that roster statement and continuing with
every grant and statement you make afterwards.

It is safe to run inside a repository you are working in. It writes no
branch, moves no HEAD, and touches neither the index nor the working tree:
the genesis tree is built with `git mktree`, which never reads the index.
Nothing leaves the machine until you push that ref deliberately.

Two things it is not.

It is **not idempotent**. A second run against the same repository stops
with `workroom already initialized` and changes nothing. That is a refusal,
not a repair — there is no rebuild path.

It has **no default identity**. `--operator` is required, or
`GITSEQ_ACTOR` in the environment. The operator seeded here signs the
grants and statements they make, so who it is has to be a choice someone
made. Every actor admitted later signs their own acts with their own key. The `operator` role carries `ratifier` with it.

To undo it, remove that directory and delete the one ref:

```text
rm -rf "$(git rev-parse --git-common-dir)/gitseq"
git update-ref -d refs/seq/<genesis>
```

## Where next

[Do a piece of work, end to end](how-to/end-to-end.md) walks one complete
path — an empty directory to an audited record in a fresh clone — and the
[documentation map](README.md) lays out every other route: how-to guides
for a task you already have, concepts for why the system behaves as it
does, and a reference page for every command and tool.
