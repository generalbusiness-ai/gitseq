---
title: gs publish
summary: Record what a Git remote already accepted, as publication facts that are not artifacts.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:13c9a0a2861529a51aacfc20bf721135f951fe25
---

# `gs publish`

`gs publish` records one durable fact for each watched file that changed at a
branch head the remote has already accepted. Run it after a successful push. It
never pushes Git refs and is not a pre-push hook.

## It records no artifacts

This is the part to read first, because the command's name suggests otherwise.

Merge succession already owns source paths. `gs merge` lands one artifact at
every path its first-parent diff changed and retires the predecessors that
cover them. If publication also minted an artifact per push at a source path,
every push would add an accounting row the merger did not create — and often
cannot lawfully retire, because it belongs to another actor. The merge would
then either strand or drag another actor's pointer with it.

So publication mints no artifacts at all. What it records is an **app-validated
publication assert**:

- kind `assert`;
- body `publication_path`, `publication_head`, `publication_remote`, and
  `publication_ref`;
- body `publication_artifact` when the fact rests on an artifact; and
- text naming the remote, the accepted head, and the path.

`assert` is a defined kind with no required fields and no basis constraint, so
no governed rule reads those four fields. This command validates them itself,
at its own boundary, before anything is signed — hence *app-validated* rather
than fold-governed.

The body deliberately does not use `path` and `commit`. Those are the artifact
schema's field names, and other code in this repository keys on them, so a
publication fact that borrowed them would look like an artifact to a reader
that never asked what kind it was. Asserts never enter the artifact map, so
merge succession and its left-live accounting are untouched by every fact this
command records.

## Flags

| flag | default | meaning |
|---|---|---|
| `--repo` | `.` | The repository holding the workroom and the ordinary branch. |
| `--as` | *(required, or `GITSEQ_ACTOR`)* | The actor signing each fact. |
| `--remote` | `origin` | A configured Git remote whose accepted head is published. |
| `--ref` | current branch | The full `refs/heads/...` branch ref. Tag refs are ignored. |
| `--basis` | *(required)* | The event that governs publication in this repository. |
| `--server` | *(the repository's advertisement)* | Where the durable acts are submitted. |

It takes no positional arguments.

`--basis` is a flag rather than a line in the tracked configuration on purpose.
The configuration is read out of the head a remote accepted, so a pushed commit
would otherwise choose the durable citation of an act signed by whoever runs
this command next. Which event governs publication here is the operator's
answer, given in the operator's own process.

`--server` behaves exactly as it does for every other authoring command:
empty takes the address the repository advertises, `-` forces the local
verified fold, and an advertisement that is present and cannot be trusted
refuses the command before a signing key is read or anything is queued.

Identity is process-scoped. Pass `--as`, or set `GITSEQ_ACTOR` for that
process. There is no repository or clone-wide Git configuration fallback, and
the command writes nothing when neither source resolves.

## Example

```sh
REPO="$(mktemp -d)/project"
REMOTE="$(mktemp -d)/remote.git"
git init -q -b main "$REPO"
git init -q --bare "$REMOTE"
git -C "$REPO" remote add origin "$REMOTE"
mkdir -p "$REPO/notes"
printf 'watch notes/**.md\n' > "$REPO/.gitseq"
printf '# One decision\n' > "$REPO/notes/one.md"
git -C "$REPO" add .gitseq notes/one.md
git -C "$REPO" commit -q -m 'Publish one decision'
GENESIS=$(gs init --repo "$REPO" --operator alice \
  | sed -n 's/.*"genesis": *"\([^"]*\)".*/\1/p')
SEED="git:sha1:$GENESIS#git:sha1:$(git -C "$REPO" rev-parse "refs/seq/$GENESIS")"
BASIS=$(gs state --repo "$REPO" --as alice --kind propose \
  --text 'Publish watched notes after every accepted push' --rests-on "$SEED")

git -C "$REPO" push -q origin HEAD:main
gs publish --repo "$REPO" --as alice --remote origin \
  --ref refs/heads/main --basis "$BASIS"
```

## Tracked configuration

The exact published head must contain a tracked UTF-8 `.gitseq`. It may contain
blank lines, comments beginning with `#`, and `watch` lines only:

```text
watch notes/**.md
watch docs/decisions/**.md
```

`*` matches within a path component, `**` may cross `/`, and `?` matches one
non-`/` byte. Character classes are deliberately not a second syntax. The file
may hold at most 64 patterns and is bounded at 64 KiB. It carries no actor,
kind, status, or per-path publication rules.

## Path shapes, and the `..` decision

Path matching across this repository is pure string comparison with no
normalisation. The fold's own path coverage and this command's neighbours in
merge succession all compare prefixes byte for byte, so `notes/../one.md`
neither covers nor is covered by `one.md`.

Admitting such a path would create a **shadow chain**: two live facts about one
file that no succession rule can ever relate, where retiring either leaves the
other standing as though it were current. So a path or a watch glob with a `.`
component, a `..` component, an empty component, or a leading or trailing `/`
is refused. Git's own diff and `ls-tree` output never contains one, so this
costs an ordinary repository nothing and closes the door on a crafted head.

Paths carrying a comma are refused for a neighbouring reason: no artifact
successor could ever stand at such a path, so the fact could never be
reconciled with merge succession.

## Which paths publish

On the first observation of a remote branch, every watched file present at its
head is eligible. Later runs compare that head with the repository's last
fully reconciled head for that exact remote and ref. The frontier is shared
across actors, so changing who makes the next push does not republish
unchanged files.

Added and modified paths publish. A rename publishes only its new path. A
deletion publishes nothing, because retirement is sealed by `gs merge`. A
force-pushed head is compared with the earlier observed head even when neither
is an ancestor of the other.

A path whose current fact already names this exact accepted head is suppressed.
That is what makes an interrupted run safe to repeat: the diff can be non-empty
and every path in it already published, which is exactly what a lost frontier
write leaves behind.

## Bases, succession, and withdrawal

Each fact rests on one of two things, in this order:

1. **An artifact standing at the exact watched path and the exact accepted
   head.** A live unsettled candidate counts — what is being cited is the
   pointer, not its settlement. A retired artifact does not, because nothing
   may rest on a withdrawn pointer. Ties break on the event identifier, so two
   runs over one log choose the same artifact.
2. **The governing publication basis** given as `--basis`. The fact then says
   in its own text that no artifact stood at this path and head, so a reader is
   never left to infer that from a missing field.

A subsequent fact at a path also rests on its same-path predecessor **and
supersedes it**, so exactly one fact per remote and path stands live.

When a path leaves the repository's watch globs, its final fact is
**bare-retired**: superseded with no successor named, because there is none.

## One wire per path, one author end to end

A path's publication facts form one wire: each fact rests on the one before it
and retires it. That wire is continued by a single author from beginning to
end.

So a run refuses **the whole derived batch** — before a byte is queued, an act
appended, or the shared frontier moved — when any changed watched path already
carries a live publication fact authored by a different actor. Paths in the
same batch that would have published cleanly do not publish; nothing partial
survives the refusal. The message names the paths and says what to do:

> The same publisher must continue the chain, or a ratifier must terminally
> retire an orphan before a new actor begins a fresh chain.

Holding `ratifier` is not an exception, and a publisher holding it is refused
in exactly the same way. A ratifier may lawfully retire another actor's fact,
but doing it inside publication would end one author's wire and begin
another's in a single unreviewed step, and leave the two chains related by
nothing a reader could follow.

The earlier behaviour was to record the gap as a debt: rest the new fact on
the foreign predecessor, skip the supersession the fold would refuse, and
report the retirement as owed. That manufactured standing retirement
obligations nobody in the run could lawfully close — the same accounting this
command already refuses to create by minting no artifacts. A foreign
predecessor is not a debt to record while proceeding. It is a reason to stop.

Two things are unaffected. A path nobody has published carries no wire, so any
actor may begin one. And a wire ends where its final fact is retired, so once a
terminal retirement has landed, the next actor starts fresh rather than
continuing someone else's chain.

## Orphans belong to a ratifier

Two kinds of orphan can outlive the actor that made them, and neither is this
command's to clear:

- a publication fact whose author has since been retired, or which belongs to
  an actor other than the one running the command; and
- an unresolved outbox file left behind by an actor who can no longer sign.

An actor holding `ratifier` clears both — the facts with `gs supersede`, the
outbox file by deleting it from `.git/gitseq/publication-outbox/`. Retiring an
orphan fact is a deliberate, separate act, and it is what unblocks a path whose
publisher has changed. The command reports an orphan outbox file it steps over
rather than blocking on it; an orphan *fact* at a path this run would publish
refuses the batch, as above.

## Reconciliation, and how a failure ends

Before the first durable submission, the complete derived batch is fsynced in
an actor-specific outbox under the repository's private `.git/gitseq` state.
One cross-process advisory lock — the host layer's own, released by the
operating system on process death — serialises those outboxes with the
repository-wide remote/ref frontier across linked worktrees.

Each queued entry owes up to two acts, and they are **two separately durable
phases**:

1. the successor `assert`; and
2. the supersession of the same-path predecessor it succeeds, where there is
   one.

An entry is settled only after the sequencer that accepted it says so, phase by
phase. A submission through a resident is verified through that same resident,
and never against the local fold: a durable write that answered itself out of a
different frontier could record as settled an act whose verdict this process
cannot see. The resident's answer must also be about the event that was asked
for, and each phase is verified against **its own exact event** — a retirement
completes only when that supersession is returned as effective.

Recording a phase is not completing it. A recovered entry whose successor is
already visible still owes its retirement and resumes there: visibility is not
completion, and a run that stopped at the successor would leave two live facts
at one path. Until every phase an entry owes is effective, the entry stays
pending, its batch is kept, and the shared frontier does not advance past that
head.

Every failure has a bounded end:

| what happened | what the run does |
|---|---|
| Transport or visibility failure, in either phase | The entry stays pending and the run exits non-zero. It is retried on later runs, at most 3 times in all; one run costs one attempt however many phases it drives. |
| Retried past the attempt ceiling | The entry is **abandoned** with the reason it kept failing for. |
| The fold ruled either act ineffective | The entry is **abandoned** immediately. Its idempotency key is spent, so no replay could reach a different verdict. |
| A changed watched path carries another actor's live fact | The run refuses the whole batch. Nothing is queued, appended, or advanced. |
| Another live actor holds a queue on this same remote and ref | The run refuses. |
| A retired or unknown actor holds such a queue | The run reports the orphan outbox file and continues. |

An abandoned entry stops holding its batch, and the shared frontier advances
past that head. Holding it back would re-derive the same refused act on every
later run for ever, which is the wedge this design exists to remove. Nothing is
presented as published that was not: an entry whose retirement never became
effective is reported **abandoned**, never landed or replayed, so partial
succession is never shown as a publication. The command exits non-zero.
Republishing an abandoned path is a deliberate act afterwards, not something a
later run does by itself.

## Refusals

A bad ref, basis, or tracked configuration refuses the whole command. One bad
path is reported while the other watched paths publish. More than 256 otherwise
valid watched paths refuses the whole new batch before any act is queued, and
before an outbox file exists at all. A changed watched path already carrying
another actor's live publication fact refuses the whole new batch the same way.

Any refusal, pending act, or abandoned act exits non-zero even when other paths
published successfully.

## Output

The JSON result names the remote, ref, governing basis, previous observed head,
current remote head, and each landed, replayed, withdrawn, pending, or
abandoned path, with the artifact each fact rested on where there was one and
the supersession that retired its predecessor where there was one. `landed` and
`replayed` mean every phase the entry owed is effective. `refused` and
`warnings` preserve per-path and per-orphan diagnostics.

See also [`gs state`](state.md), [`gs artifacts`](artifacts.md),
[`gs supersede`](supersede.md), and [`gs merge`](merge.md).
