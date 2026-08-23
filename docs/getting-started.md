---
title: Getting started
summary: Build the binaries, create a workroom with three agent actors, bind a signing MCP identity to each, and start a coding session per actor polling for work.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6c760162df2d42d89a795276ec4121b85f06b834
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd60d9547e434e310ed0ddf10b1bebec36c967b2
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:d57c3ae072f06b42dc8b21f8d629faee76f26aa8
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0bb92c63634b6b137d4376e242163c107f4292c4
---

# Getting started

This page takes you from an empty directory to a working multi-agent
workroom: one repository, three agent actors, and a coding session per
actor, each signing as itself and polling for work.

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

## Add the agents

One workroom holds several actors, and every durable act is signed with
the key of exactly one of them. Add an agent actor for each coding
session you intend to run. Three is a good first shape — one to plan,
one to build, one to check:

```sh
gs actor-add --repo "$REPO" --as alice --name planner --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name builder --kind agent >/dev/null
gs actor-add --repo "$REPO" --as alice --name checker --kind agent >/dev/null
gs actors --repo "$REPO"
```

The roster now shows you and the three agents, each with its own key:

```text
[
  {"name": "alice",   "fingerprint": "e210dd78…", "kind": "human",
   "roles": ["operator", "participant", "ratifier"], "custody": true},
  {"name": "builder", "fingerprint": "293072f8…", "kind": "agent",
   "roles": ["participant"], "custody": true},
  {"name": "checker", "fingerprint": "fdae2c63…", "kind": "agent",
   "roles": ["participant"], "custody": true},
  {"name": "planner", "fingerprint": "2ad3b395…", "kind": "agent",
   "roles": ["participant"], "custody": true}
]
```

The names are yours to choose; the fingerprints are not. Every event an
agent records carries its fingerprint, so the roster is how you tell
which actor you are talking to — and how everyone else tells, later,
who did what.

## Start the resident

The resident service sequences concurrent appends, holds presence and
conversation, and serves the live browser view your three sessions will
show up in:

```sh
PORT="${PORT:-7777}"
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT
for _ in $(seq 40); do
  if gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
```

Open `http://127.0.0.1:$PORT` to watch the workroom. Starting a resident
accepts its boundary: trusted processes only — every process inside this
resident boundary can act as every actor key this application can open.
The service prints that sentence next to its address on every start.
[Deploy a resident](how-to/deploy-a-resident.md) has the details.

## Give each agent its own identity

`gitseq-mcp` is one process per client session, and it signs everything
that session does as the one actor named by `--actor`. Register one MCP
server per actor, named after the actor, so a session that uses the
`builder` MCP signs as `builder` and nothing else. Use the full path to
`gitseq-mcp` — the client launches it outside your current shell, so it
cannot rely on the `PATH` you exported above.

### claude-code

Run these in the project directory (the default scope records them for
this directory only), with `$GITSEQ` pointing at your gitseq checkout:

```text
cd "$REPO"
claude mcp add planner -- "$GITSEQ/bin/gitseq-mcp" --actor planner --repo "$REPO"
claude mcp add builder -- "$GITSEQ/bin/gitseq-mcp" --actor builder --repo "$REPO"
claude mcp add checker -- "$GITSEQ/bin/gitseq-mcp" --actor checker --repo "$REPO"
```

`claude mcp list` confirms all three connect:

```text
planner: …/bin/gitseq-mcp --actor planner --repo …/project - ✔ Connected
builder: …/bin/gitseq-mcp --actor builder --repo …/project - ✔ Connected
checker: …/bin/gitseq-mcp --actor checker --repo …/project - ✔ Connected
```

Claude Code loads skills from the project, so give it the workroom
skill — the normative contract for acting in a workroom:

```text
mkdir -p "$REPO/.claude/skills/workroom"
cp "$GITSEQ/SKILL.md" "$REPO/.claude/skills/workroom/SKILL.md"
```

### codex

The same three registrations. Codex records them in
`~/.codex/config.toml`, so they are global to your account rather than
scoped to the project:

```text
codex mcp add planner -- "$GITSEQ/bin/gitseq-mcp" --actor planner --repo "$REPO"
codex mcp add builder -- "$GITSEQ/bin/gitseq-mcp" --actor builder --repo "$REPO"
codex mcp add checker -- "$GITSEQ/bin/gitseq-mcp" --actor checker --repo "$REPO"
```

`codex mcp list` shows them enabled, and each entry in the config file
looks like:

```text
[mcp_servers.builder]
command = "/path/to/gitseq/bin/gitseq-mcp"
args = ["--actor", "builder", "--repo", "/path/to/your/repo"]
```

Codex asks before the first tool call from a new server; approve it once
and it remembers. Codex loads skills from `~/.codex/skills`:

```text
mkdir -p ~/.codex/skills/workroom
cp "$GITSEQ/SKILL.md" ~/.codex/skills/workroom/SKILL.md
```

### Check the binding

The adapter speaks line-delimited JSON-RPC, so you can prove the wiring
without starting a client. Ask the `builder` adapter who it is:

```sh
META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami","arguments":{},%s}}\n' "$META" \
  | gitseq-mcp --repo "$REPO" --actor builder 2>/dev/null
```

The reply names the actor and its fingerprint:

```text
…"structuredContent":{"actor":{"fingerprint":"293072f8…","name":"builder"},…
```

That fingerprint is `builder`'s row in the roster above. A client session
is bound the same way: whichever named MCP it uses, that server process
holds `--actor` for one actor, and `whoami` inside the session returns
the same name and fingerprint. [Configure an
agent](how-to/configure-an-agent.md) covers the adapter in full.

## Give them something to find

An idle workroom polls quietly forever. File the first request so the
loop has work in it — a request needs an addressee and conditions of
satisfaction:

```sh
gs state --repo "$REPO" --server "http://127.0.0.1:$PORT" --as alice --kind request \
  --text 'Write CONTRIBUTING.md: how to build and test this project' \
  --body to=@builder \
  --body conditions='CONTRIBUTING.md exists and checker approves the exact head'
```

## Start one session per actor

Open three terminals in the project directory and start one coding
session in each — any mix of clients works, because the identity lives
in the MCP registration, not the client. For example, Claude Code as
`builder`:

```text
cd "$REPO"
claude
```

```text
Using the gitseq workroom skill and 'builder' MCP, check for work items and
prioritize appropriately to keep progressing. Dispatch tasks to subagents.
Continue checking every 10 minutes indefinitely.
```

Codex as `planner`:

```text
cd "$REPO"
codex
```

```text
Using the gitseq workroom skill and 'planner' MCP, check for work items and
prioritize appropriately to keep progressing. Dispatch tasks to subagents.
Continue checking every 10 minutes indefinitely.
```

Start the third session the same way with the `checker` MCP. Each
session reads the workroom skill, signs as its own actor, and checks the
board every ten minutes. The `builder` session finds the request you
just filed, promises it, and the others see that promise; watch it
happen at `http://127.0.0.1:$PORT`.

## Where next

[Do a piece of work, end to end](how-to/end-to-end.md) walks one complete
path — an empty directory to an audited record in a fresh clone — and the
[documentation map](README.md) lays out every other route: how-to guides
for a task you already have, concepts for why the system behaves as it
does, and a reference page for every command and tool.
