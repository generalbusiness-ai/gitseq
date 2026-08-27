---
title: Use gitseq in a repository you already have
summary: What gitseq touches inside an existing clone, how it behaves across worktrees, what push and pull do with the log, what a central resident changes, and what becomes permanent.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:9141779ce5de63132cdbfd0498ef22730e280d1f
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:44a260205b00e793c8350419a71438dc599d2cbc
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0baadffc9ea51e7d3f31d7a1ea4a6ae210322c4c
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4b09c0e250a2eea6d310236fdd4077662785c06d
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4288e38f53f8e0df705089e0cec337aa26a39084
---

<!-- The rests_on identifiers above are the live behaviour artifacts, as of
     main de274aac, at the paths this page describes: cmd/gs (the `init`,
     `attach` and `--server` resolution behaviour), internal/app (workroom
     layout, resident advertisement), internal/apphost (git-directory
     resolution and the meta directory), internal/kernel (the compare-and-swap
     append), and internal/residentclient (loopback validation). -->

# Use gitseq in a repository you already have

You have a real project in git. You are curious about gitseq, and you
would rather not find out the hard way what it does to a repository other
people depend on.

This page answers the five questions people actually ask before trying
it, in the order they ask them. Every command runs; `make test` executes
them against scratch repositories built from nothing.

Everything here works with no service and no network. That is the
baseline, not a restricted mode: a workroom is a set of git refs and a
directory of local files, and a lone laptop with no remote is a complete
gitseq installation. Sharing and a resident service are additions you
opt into, and each has its own section below.

## Is it safe to run gitseq in my existing clone?

Yes, and the reason is narrow enough to check.

`gs init` writes in two places. In the repository it creates a parentless
commit over an empty tree — the **genesis** — and appends one event, the
roster statement naming you the operator. Those objects are reachable
from one ref, `refs/seq/<genesis>`, and folding that event also writes a
signed checkpoint at `refs/gitseq/checkpoints/<genesis>`. Beside them it
creates a `gitseq/` directory inside the repository's **git common
directory** — normally `.git/gitseq` — holding a config file, the
sequencer key, and your operator key under `actors/`.

It creates no branch, moves no `HEAD`, and rewrites nothing you track.
It cannot: every git command the object store runs is invoked as
`git --no-replace-objects --git-dir <common dir> …`, with no working tree
attached, so the index and your files are not addressable from there. The
genesis tree is built with `git mktree`, which never reads the index.

Prove it on a scratch repository. First, a repository with a commit in
it, and a record of its refs and its tracked tree:

```sh
WORK="$(mktemp -d)"
REPO="$WORK/project"
git init -q "$REPO"
printf 'print("hello")\n' > "$REPO/app.py"
git -C "$REPO" add app.py
git -C "$REPO" commit -q -m 'Initial commit'

BASE=$(git -C "$REPO" branch --show-current)
BEFORE=$(git -C "$REPO" rev-parse HEAD)
git -C "$REPO" for-each-ref --format='%(refname)' > "$WORK/refs-before"
git -C "$REPO" ls-tree -r --name-only HEAD > "$WORK/tree-before"
```

Now initialize the workroom, and compare:

```sh
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')

git -C "$REPO" for-each-ref --format='%(refname)' > "$WORK/refs-after"
echo 'refs that are new:'
comm -13 "$WORK/refs-before" "$WORK/refs-after"

git -C "$REPO" ls-tree -r --name-only HEAD > "$WORK/tree-after"
cmp "$WORK/tree-before" "$WORK/tree-after"
test "$BEFORE" = "$(git -C "$REPO" rev-parse HEAD)"
test "$BASE" = "$(git -C "$REPO" branch --show-current)"
git -C "$REPO" diff --quiet HEAD
git -C "$REPO" status --porcelain=v1 --untracked-files=all
```

The two new refs are the sequence and its checkpoint. The tracked tree is
byte-identical, `HEAD` and the branch are where you left them, the working
tree is clean, and `git status` — asked about untracked files as well —
prints nothing, because everything gitseq wrote is inside the git
directory and so outside the working tree entirely.

The local files. None of them is tracked, and none needs a `.gitignore`
entry, because git does not look inside its own directory:

```sh
META="$(git -C "$REPO" rev-parse --path-format=absolute --git-common-dir)/gitseq"
ls "$META"
```

`config.json` records the genesis, the object format and the roster;
`sequencer` and `actors/alice.key` are private keys, written mode `0600`.
Nothing here leaves the machine until you push the ref yourself.

Two things `gs init` is not. It is **not idempotent** — a second run stops
with `workroom already initialized` and changes nothing. And it has **no
default identity**: `--operator` is required, or `GITSEQ_ACTOR` in the
environment.

### Removing it again

Removal is deleting the directory and the refs. Nothing else was
touched, so nothing else has to be undone:

```sh
set -e
COPY="$WORK/copy"
cp -R "$REPO" "$COPY"
COMMON_DIR="$(git -C "$COPY" rev-parse --path-format=absolute --git-common-dir)"
# The safeguard here is the two comparisons below, not find. find is not a
# safeguard at all: it would empty a wrong directory as thoroughly as the
# right one. What makes this safe is that the resolved path must equal the
# one location it is allowed to be, so a substitution returning anything
# else stops the block instead of deleting. `set -e` above is what makes it
# stop: without it a failed comparison only prints nothing and the deletion
# below runs regardless, which is why it is in the block you paste rather
# than left to your shell. Both sides are resolved with
# pwd -P first, because git reports an absolute path with symlinks already
# followed while $COPY may still contain one: on macOS a temporary directory
# under /tmp or /var is reached through exactly such a link, and comparing
# the two spellings would fail on every run.
COPY_DIR="$(cd "$COPY" && pwd -P)"
test "$COMMON_DIR" = "$COPY_DIR/.git"
GITSEQ_DIR="$COMMON_DIR/gitseq"
test "$GITSEQ_DIR" = "$COPY_DIR/.git/gitseq"
test -d "$GITSEQ_DIR"
find "$GITSEQ_DIR" -mindepth 1 -depth -type f -exec unlink {} \;
find "$GITSEQ_DIR" -mindepth 1 -depth -type d -exec rmdir {} +
rmdir "$GITSEQ_DIR"
for ref in $(git -C "$COPY" for-each-ref --format='%(refname)' 'refs/seq/*' 'refs/gitseq/*'); do
  git -C "$COPY" update-ref -d "$ref"
done
git -C "$COPY" for-each-ref --format='%(refname)'
git -C "$COPY" log --oneline -1
```

An ordinary repository, with its history intact. Note that this discards
the record: the log is unrecoverable once its ref is gone and the objects
are collected. Push it first if you want to keep it.

## Can I use it from several checkouts or worktrees of one repo?

Linked worktrees share one workroom. Separate clones do not.

A linked worktree has its own git directory but the same **common**
directory, and the common one is what gitseq resolves. Every command
asks git for both — `git rev-parse --absolute-git-dir --git-common-dir` —
and derives the meta directory from the common one, so every worktree of
one repository opens the same config, the same keys, and the same refs.
There is exactly one log, whichever checkout you are standing in.

```sh
git -C "$REPO" worktree add -q "$WORK/feature" -b feature
git -C "$WORK/feature" rev-parse --path-format=absolute --git-common-dir
gs actors --repo "$WORK/feature"
gs state --repo "$WORK/feature" --as alice --kind assert \
  --text 'Filed from the linked worktree'
test "$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")" \
   = "$(git -C "$WORK/feature" rev-parse "refs/seq/$GENESIS")"
```

The worktree reports the main repository's common directory, sees the
same roster, and an act filed from it advances the one ref both share.

### Two writers at once

Concurrent appends do not corrupt the log, and the mechanism is worth
naming precisely rather than trusting. Each append builds its commit on
the head it just read and then advances the ref with
`git update-ref <ref> <new> <old>` — a compare-and-swap against the exact
old value. If the ref moved in between, the update fails, the appender
re-reads the head and builds again, up to thirty-two attempts by default.
Exhausting them returns back-pressure and appends nothing. So a race
costs a retry, never a lost or overwritten event.

What that buys is a log that stays consistent under contention. It is not
a claim that parallel work is otherwise safe. Two agents editing one
checkout still overwrite each other's files, and a head assembled from
interleaved writes is not reviewable. This project's own rule is
therefore **one writer per checkout**: each actor works in its own
worktree and nobody else commits there. gitseq does not enforce that —
it is a convention, and the thing it protects is the working tree, not
the record.

If you want several actors writing at once on one machine, give them one
resident service to sequence through; see below.

## What happens when I push and pull?

Git ignores `refs/seq/*` on both sides by default. That is the single
most important fact in this section, and it cuts both ways: nothing
escapes by accident, and nothing arrives by accident either.

```sh
ORIGIN="$WORK/origin.git"
git init -q --bare "$ORIGIN"
git -C "$REPO" remote add origin "$ORIGIN"
git -C "$REPO" push -q origin "$BASE"
echo 'on the remote after an ordinary branch push:'
git --git-dir="$ORIGIN" for-each-ref --format='%(refname)'
```

Only the branch. A default `git push` sends the refs its refspec names,
and no default refspec names `refs/seq/*`. Colleagues who pull from this
remote get your code and nothing else.

To share the log, push it explicitly:

```sh
git -C "$REPO" push -q origin 'refs/seq/*:refs/seq/*'
git --git-dir="$ORIGIN" for-each-ref --format='%(refname)'
```

The refspec has no leading `+`. A sequence only ever advances, so
publishing is always a fast-forward; see below for what a refusal means.

A colleague who clones this remote and does nothing else gets the code
and no log:

```sh
git clone -q "$ORIGIN" "$WORK/colleague"
echo 'refs in a default clone:'
git -C "$WORK/colleague" for-each-ref --format='%(refname)'
git -C "$WORK/colleague" config --get-all remote.origin.fetch
```

`refs/seq/*` is absent, and the fetch configuration is the stock
`+refs/heads/*:refs/remotes/origin/*`. To that colleague the repository
is an ordinary one. Nothing they do will break the record, and nothing
they do requires them to know it exists.

Someone who does want the log configures the refspec once. `gs attach`
does it for them and then verifies what it fetched:

```sh
gs attach --repo "$WORK/colleague" --remote origin --genesis "$GENESIS"
git -C "$WORK/colleague" config --get-all remote.origin.fetch
gs status --repo "$WORK/colleague"
```

The rule it adds — `refs/seq/*:refs/seq/*`, again with no `+` — makes
ordinary `git fetch` keep the sequence up to date from then on, and
refuse anything that is not a fast-forward.

### What a refused push means

For an append-only log, "non-fast-forward" has one meaning: the remote
holds events you do not. Either you are simply behind — fetch, and push
again — or two holders appended to the same log independently and the two
lines have diverged.

The second case is what to avoid, because gitseq has no merge for it. Two
copies of one workroom, each appending locally, produce two valid
sequences that are not ancestors of each other. Git refuses both the push
and the fetch, correctly, and neither side loses anything; but nothing
reconciles them afterwards, and an attached clone, which records the last
frontier it verified, refuses a sibling sequence rather than choosing one.

```sh
LAPTOP="$WORK/laptop"
cp -R "$REPO" "$LAPTOP"
gs state --repo "$LAPTOP" --as alice --kind assert \
  --text 'Appended on the second machine' >/dev/null
git -C "$LAPTOP" push -q origin 'refs/seq/*:refs/seq/*'

gs state --repo "$REPO" --as alice --kind assert \
  --text 'Appended here, unaware of the other machine' >/dev/null
if git -C "$REPO" push origin 'refs/seq/*:refs/seq/*'; then
  echo 'unexpected: the diverged push was accepted'
else
  echo 'refused, as it should be'
fi
```

Git says `! [rejected] … (fetch first)`, and a subsequent fetch says
`! [rejected] … (non-fast-forward)` because the remote line is not an
ancestor of the local one either. The way out of that state is a
judgement call about which line is the record, not a command.

The way to stay out of it is to have one place where appends happen: one
machine, or one resident service. That is the next question.

## There's a central gitseq service for this repository — what changes for me?

Less than you would expect, and the change is in your favour.

`gs serve` runs a **resident** — a local, loopback-only process that
sequences appends, holds presence and ephemeral conversation, notifies
readers when the record moves, and serves the browser view. It takes an
ownership claim at `refs/gitseq/resident/<genesis>`, an ordinary shared
ref in the common directory, so exactly one process serves one
repository however many worktrees or path aliases it is reached by. It
then advertises the address it bound in `resident.json`, beside the
config in the meta directory.

**That advertisement is the default for every `gs` command that can reach
a resident at all** — durable acts and reads alike. You do not pass
`--server` to use the resident your checkout is already running; you pass
it to refuse one. There are three cases and all three are worth knowing:

| `--server` | what happens |
|---|---|
| omitted | The address the repository advertises, after loopback validation. No `resident.json` at all means the local fold — which is why every command earlier on this page worked with no service running. |
| a URL | Used as given, after the same loopback validation. |
| `-` | Forces the local verified fold, even when a resident is advertised. `-` is not a URL, so no advertisement can collide with it. |

Loopback validation is strict: `http` only, no credentials, no query or
fragment, no path, and a host that is `localhost` or a loopback IP. A
resident is a trusted local custodian for the actor keys on one machine,
not an authenticated remote server, and a URL that could reach off the
machine is refused before anything is sent.

### An advertisement `gs` will not act on

`resident.json` is an ordinary file inside the git directory. Any local
process that can write the repository can write it, nothing signs it, and
`gs` treats it as untrusted input accordingly. Exactly one reading of it
counts as *no resident*: the file is not there. Every other way it can be
wrong **refuses the command**, names which way, and offers `--server -`
for anyone who meant to act locally. The refusal happens before the
command reads a signing key and before it appends anything, so a refused
act leaves nothing behind — not in the log, and not in key custody either.

There are three refusal messages, and which one you get says how far the
command got.

A record that is present and cannot be trusted never gets as far as an
address:

```text
gs: this repository advertises a resident that cannot be trusted: the record at /path/to/.git/gitseq/resident.json is not a resident record: unexpected end of JSON input; pass --server - to act locally instead
```

The clause after the path is the failure that applies. `cannot be read:
… permission denied` for a record whose permissions deny it; `is larger
than the 8192 bytes a resident record may be` for an oversized one; `is
not a resident record: …` for anything that is not JSON or is cut short;
`advertises no address` for an empty `url`; and `names workroom "…", not
"…"` for a record a different workroom left behind. That last one matters
most, because acting through it would append to somebody else's log.

A record that reads cleanly but names an address off this machine is
refused by loopback validation, quoting the address:

```text
gs: this repository advertises "http://example.com", which is not usable: resident service must name a loopback address; pass --server - to act locally instead
```

And a record that is trustworthy in every way, whose resident is simply
not running, is refused in the terms that matter — that nothing was
appended:

```text
gs: no resident is listening at http://127.0.0.1:1, so nothing was appended: start one with `gs serve`, or pass --server - to fold this act locally: Post "http://127.0.0.1:1/v0/submit": dial tcp 127.0.0.1:1: connect: connection refused
```

Only the last of the three is a dead resident rather than an untrustworthy
record, and only the last is one a read works around: `gs status` against
an unreachable address names the failed request, says `performing verified
local fallback`, and answers from the verified local read. A durable act
never falls back. Under the first two, nothing proceeds — reads included.

Check the whole set on a scratch repository. Each record below is written
in turn, the append is expected to refuse, and the log's depth at the end
proves that none of them got through:

```sh
TAMPER="$WORK/tamper"
git init -q "$TAMPER"
git -C "$TAMPER" commit -q --allow-empty -m 'Initial commit'
TGENESIS=$(gs init --repo "$TAMPER" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
TMETA="$(git -C "$TAMPER" rev-parse --path-format=absolute --git-common-dir)/gitseq"
RECORD="$TMETA/resident.json"

depth() { gs verify --repo "$TAMPER" | sed -n 's/.*"Depth": *\([0-9]*\).*/\1/p'; }
refuses() {
  if gs state --repo "$TAMPER" --as alice --kind assert \
      --text 'must never be appended' > "$WORK/refusal" 2>&1; then
    echo "unexpected: appended under $1"
    exit 1
  fi
  echo "--- $1"
  cat "$WORK/refusal"
}
SETTLED=$(depth)

FOREIGN=0000000000000000000000000000000000000000
PADDING=$(head -c 9000 /dev/zero | tr '\0' 'a')

printf 'not a resident record\n' > "$RECORD"
refuses 'not JSON'
printf '{"url":"http://127.0.0.1:7777","gene' > "$RECORD"
refuses 'truncated'
printf '{"url":"","genesis":"%s"}\n' "$TGENESIS" > "$RECORD"
refuses 'no address'
printf '{"url":"http://127.0.0.1:7777","genesis":"%s"}\n' "$FOREIGN" > "$RECORD"
refuses 'another workroom'
printf '{"url":"http://127.0.0.1:7777","genesis":"%s","pad":"%s"}\n' \
  "$TGENESIS" "$PADDING" > "$RECORD"
refuses 'oversized'
printf '{"url":"http://example.com","genesis":"%s"}\n' "$TGENESIS" > "$RECORD"
refuses 'not loopback'
printf '{"url":"http://127.0.0.1:1","genesis":"%s"}\n' "$TGENESIS" > "$RECORD"
refuses 'nobody listening'

chmod 000 "$RECORD"
if cat "$RECORD" > /dev/null 2>&1; then
  echo '--- unreadable: skipped, this process can read a mode-000 file'
else
  refuses 'unreadable'
fi
chmod 600 "$RECORD"

test "$(depth)" = "$SETTLED"
```

Every record the run exercised was refused, and the depth is exactly
where it started: not one of them appended an event. The unreadable case
is the one conditional: it runs only where mode 000 actually blocks
reading, and a process that can read the file anyway — root, for one —
skips it rather than stage a refusal the environment cannot produce.

The way out is either of the two the messages name. Ask for the local
fold on purpose, or delete the record — deleting it is what turns the
refusal back into ordinary local operation, because absence is the only
reading that means no resident:

```sh
gs state --repo "$TAMPER" --as alice --kind assert \
  --text 'Local fold, asked for deliberately' --server -
unlink "$RECORD"
gs state --repo "$TAMPER" --as alice --kind assert \
  --text 'Local fold, because nothing is advertised'
test "$(depth)" = "$((SETTLED + 2))"
```

The [MCP adapter refuses the same way](../reference/gs/serve.md): a
durable call through it stops with the same reason, and a read still
answers from the verified local fold. It refuses the call rather than the
attachment, so a long-lived session survives a record it can repair, and
deleting the record is the whole recovery there too.

Now the ordinary case. Start a resident and keep working exactly as
before:

```sh
PORT="${PORT:-7777}"
gs serve --repo "$REPO" --listen "127.0.0.1:$PORT" &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true' EXIT
ready=""
for _ in $(seq 40); do
  kill -0 "$SERVER" 2>/dev/null || break
  if gs status --repo "$REPO" --server "http://127.0.0.1:$PORT" 2>&1 >/dev/null \
      | grep -q 'performing verified local fallback'; then
    sleep 0.25
  else
    ready="yes"
    break
  fi
done
[ -n "$ready" ] || { echo "no resident on port $PORT — is it in use?" >&2; exit 1; }

cat "$META/resident.json"
gs state --repo "$REPO" --as alice --kind assert \
  --text 'Appended through the resident, with no flag'
gs state --repo "$REPO" --as alice --kind assert \
  --text 'Appended by the local fold, deliberately' --server -
```

Both land in the same sequence. What the resident changes is the cost.
A local append is a fresh process that has to read and verify the log's
state before it can chain onto the head; the resident is one long-lived
process holding that state already, so an append through it is close to
the compare-and-swap alone. On a short log the difference is not worth
noticing. On a long one it is the difference between a command you wait
for and one you do not, which is why the advertisement became the
default. [Performance evidence](../reference/performance.md) carries the
measured numbers.

What it does not change: the log, the keys, the refs, and the fact that
`--server -` always gets you the local path back. If the resident is
down, pass `--server -` or delete its advertisement, and you are on the
baseline this page started from.

## What stays local, and what becomes permanent and shared?

Three categories, and the boundaries are sharp.

**Ephemeral.** Presence, focus, chat, acknowledgements, and the
[live attention](../reference/live-attention.md) adjunct on every tool
result. These live in the resident's
process memory and are gone when it stops. They are advisory: no promise,
claim, report or authorization is ever ephemeral. When the resident is
down, `say`, `ack` and `presence` fail rather than pretend, and the
durable tools keep working.

**Durable and local.** Every act you record — a request, a promise, an
artifact, a verdict, a merge receipt — is signed with your actor key and
appended to `refs/seq/<genesis>` in your own repository. It is permanent
there the moment it is appended: positions in the sequence are final,
there is no edit and no delete, and retracting something means recording
a further act that supersedes it. But it is still only yours. Nothing has
been shared.

**Durable and shared.** Sharing is the explicit push from the section
above. After it, the events are on the remote for anyone with the fetch
refspec, and they carry your actor fingerprint and your signature
forever. Assume anything you append will be read by whoever ends up with
the repository.

**Keys and custody stay local, always.** They live in the meta directory
inside the git directory, never in the tracked tree, and they are never
pushed:

```sh
ls "$META/actors"
git -C "$REPO" status --porcelain=v1 --untracked-files=all
echo "tracked files mentioning gitseq: $(git -C "$REPO" ls-files '*gitseq*' | wc -l)"
gs verify --repo "$REPO"
```

`gs verify` is the full audit: every actor signature, every sequencer
signature, and the integrity of the sequence, reported as genesis, head,
depth and event count. Run it from any clone that has attached; it is the
answer to "can I check this myself", and it needs no service and no
trust in whoever gave you the repository.

## See also

- [Getting started](../getting-started.md) — build the binaries, create a
  workroom, and put agents in it.
- [Publish and audit](publish-and-audit.md) — sharing and verification in
  full, including what a first-contact audit cannot prove.
- [Deploy a resident](deploy-a-resident.md) — running the service so it
  outlives a shell, and the boundary it asks you to accept.
- [Components](../concepts/components.md) — the CLI, the resident, the
  adapter, and what each is responsible for.
- [`gs init`](../reference/gs/init.md),
  [`gs attach`](../reference/gs/attach.md),
  [`gs serve`](../reference/gs/serve.md),
  [`gs verify`](../reference/gs/verify.md)
