---
title: Two S2 clarifications for adoption
date: 2026-09-10
status: candidate
clarifies: notes/2026-09-09-lane-identity-reconciled.md (adopted by proposal 7a71886b, ratified ba18cd1a)
base: main 869702241e4d28c9b8599e8caccaf3d01a2ca4d9
prepared_under: request 6eee19d1 (b4247e09), promise f605b80a
---

# Two S2 clarifications

Stage two of the adopted lane and worktree identity design reaches two places
where the adopted text does not settle what to build. One the design itself
flags as unfinished. The other is a fact about Git that the adopted wording does
not disclose. Both need a decision before the stage can honestly claim to
implement what was adopted.

Everything else in stage two is an ordinary implementation choice and is not
asked about here.

## Matter one: a short selector when the projection cannot be read

### What is already settled

Creation checks whether the record a lane names is one that can govern work. An
unreadable projection yields unknown, not false, so the adopted rule is that
creation reports the check as *not established*, warns, and proceeds. What it
creates grants nothing: the record is evidence, and durable admission reads the
durable record itself.

### What is not settled

The adopted note names this case as unfinished, in its own words:

> Refusing a short selector for want of a resolvable identifier is the only
> sensible outcome, but it adds to the adopted refusal list rather than reads
> it, so it is named here as an S2 proposal item and not slipped in as settled.

A full canonical identifier is classified without reading any log, so it
proceeds under the warn-and-create rule. A `#N` or a hash fragment resolves
*against* the projection, so when the projection cannot be read there is no
identifier to write into the record at all. The command has nothing to stamp.

This is a refusal that the adopted list does not contain. Reading it in would be
inventing a rule and calling it an interpretation, which is why the design
refused to do that and why it is here.

### Recommendation

Adopt the refusal, in these words:

> A selector that must be resolved against the projection — a display index or
> a hash fragment — is refused with the existing event reference resolution
> error when no full canonical identifier can be obtained. This is a refusal of
> the selector, not a judgement about the record it might have named. A full
> canonical identifier is not refused: it is classified without reading any
> log, and its projection-dependent eligibility is reported as not established
> under the rule above.

The refusal reuses the error the resolver already returns, so the message a user
sees is one this repository already emits, and no new error text or refusal
category enters the surface.

### Alternatives, and what they cost

**Create with no record.** Write the checkout and leave the identity blank for
the user to fill in later. It costs the property that makes the whole stage
worth having: a checkout with no record is exactly the unattributed checkout the
design set out to remove, and nothing later forces it to be filled in.

**Block until the projection is readable.** Honest, and it makes the tool
useless in the situation people most want it — a workroom whose log is being
repaired is when you most need a checkout to repair it from.

**Resolve short selectors from a stale cache.** It turns an unknown into a
confident wrong answer, which is the thing the unreadable-projection rule exists
to prevent.

## Matter two: the optional commit hook

### What the adopted text says

> A hook is optional, off by default, installed only with an explicit flag, only
> when no hook of that name exists and `core.hooksPath` is unset or inside this
> repository. It is one `prepare-commit-msg` script that appends one line from
> the record it was given, execs nothing else, and never replaces a user hook.

### What it does not disclose

By default a linked checkout's hooks are not its own: Git resolves them through
the shared common directory, so a hook installed from any checkout runs for
every checkout in the repository. Observed:

```text
$ cd wt && git rev-parse --git-path hooks/prepare-commit-msg
/tmp/probe/main/.git/hooks/prepare-commit-msg
$ git rev-parse --git-dir
/tmp/probe/main/.git/worktrees/wt
```

"Installed only with an explicit flag" reads as though the flag bounds what is
touched. By default it does not: the flag bounds who asks, and the effect is
repository-wide.

That default is not the whole picture, and the earlier draft of this note was
wrong to say hooks are shared full stop. A **relative** `core.hooksPath` is
resolved per checkout, so each checkout runs its own file:

```text
$ git -C main   config core.hooksPath   →  .githooks
main checkout   ran main/.githooks/prepare-commit-msg
linked checkout ran wt/.githooks/prepare-commit-msg
```

So a repository that already sets a relative `hooksPath` has per-checkout hooks
today. gitseq must not set it: it is the user's configuration and the adopted
text is right to refuse to touch it. It only means the disclosure has to say
"by default", not "always".

The second half of the adopted sentence is the sharper problem. A shared script
cannot append "one line from the record it was given", because it was given one
checkout's record and runs for all of them; taken literally it would stamp every
checkout with whichever lane installed it. A wrong trailer is worse than none,
because the association reads a trailer as a claim.

### What is possible inside the existing no-exec contract

Nothing needs relaxing, and nothing needs a JSON parser. Git sets `GIT_DIR` per
invocation, and stage two already writes the adopted trailer assistance as a
plain file beside the record: `$GIT_DIR/gitseq/rests-on`, holding the exact
`Rests-On:` line derived from `checkout.json`. A shared script reads that file
with shell builtins alone — no helper, no `gs` call, no Git read, no parsing of
the record itself.

Observed, from one shared hook, with a different record in each checkout, full
canonical identifiers throughout:

```text
main checkout   Rests-On: git:sha1:5d26227488…0bc8#git:sha1:944ebe0d…38327160
linked checkout Rests-On: git:sha1:5d26227488…0bc8#git:sha1:06d28558…82327e23
```

One wrinkle: in the *main* checkout `GIT_DIR` is unset in the hook environment
and the working directory is the top of the tree, so the script falls back to
`.git`. Both cases are shown above.

### Recommendation

Keep the adopted outcome. Replace the second sentence with:

> It is one `prepare-commit-msg` script that reads the invoking checkout's own
> trailer file — `$GIT_DIR/gitseq/rests-on`, with `GIT_DIR` naming that checkout
> and `.git` the fallback for the main one — and appends the exact `Rests-On:`
> line it holds. It uses shell builtins only and execs nothing. It appends
> nothing when the file is absent, unreadable, or does not hold a full canonical
> identifier, and nothing when the message already carries that line.

And add, to the sentence before it:

> By default, installation is repository-wide: a linked checkout's hooks resolve
> through the shared common directory, so installing from one checkout changes
> how commits are made in every checkout. A repository that already sets a
> relative `core.hooksPath` has per-checkout hooks instead; gitseq does not set
> or change that setting either way. The flag bounds who asks, not what it
> touches, and the command says so before it writes.

This is a change to what was adopted, not a reading of it: the adopted sentence
says the script is *given* a record and the recommendation says it *finds* a
file. That is why it is here.

### The controls that go with it

- **Wrong checkout.** Two checkouts, two records, one shared hook: each commit
  carries its own lane. Shown above.
- **Missing file.** The commit succeeds, carries no trailer, prints nothing. The
  unguarded first draft printed a shell redirection error on every commit in
  every checkout without one — precisely the repository-wide harm this section is
  about — so the wording tests readability before reading.
- **Malformed or foreign.** A line that is not a full canonical `Rests-On:`
  identifier for this genesis is ignored rather than appended.
- **Already stamped.** Amending twice leaves exactly one trailer.
- **Existing hook and hooksPath.** Installation refuses when a
  `prepare-commit-msg` hook exists, and when `core.hooksPath` points outside
  this repository. Unchanged from the adopted text.
- **Authority.** The trailer is evidence. Nothing the hook writes widens the
  deletable set or authorises a signature.

No `extensions.worktreeConfig` migration is proposed: nothing here writes Git
configuration.

### Alternatives, and what they cost

**Template only, defer the hook.** Ship the template, which is adopted and
already written, and defer the hook. What is deferred is the adopted
optional-hook outcome itself, so this is a deferral for Hugh to decide and not a
delivery of stage two unchanged. Its cost is the adopted convenience: the
template must be selected per commit, so the trailer stays something a person
can forget, which is the failure the hook exists to prevent.

**Per checkout via `core.hooksPath`.** Genuinely per-checkout, as shown above,
and it costs the user's own configuration: gitseq would be taking over a setting
it does not own. The adopted text already refuses this.

**Bake the installing checkout's record into the script.** The literal reading.
It costs correctness: every other checkout gets a wrong trailer.

## What is being asked

Two decisions:

1. Adopt the short-selector refusal, in the wording above.
2. Adopt the hook wording above — the by-default repository-wide disclosure and
   the run-time read of the checkout's own trailer file — or defer the optional
   hook and take the template alone.

Neither changes stage two's performer or destination, and the first changes
nothing else either. The second is not neutral: deferring the hook defers an
outcome the design adopted, so stage two would deliver less than was adopted
until Hugh decides. Stage two as implemented has the hook removed on that basis
and does not claim the adopted outcome complete.

Three things are *not* being asked, because planner has already placed them
inside stage two as ordinary implementation choices: one configured checkout
root serving both creation containment and the existing protection, the lane and
record readback delivered through the shared captured read, and where the shared
settled-status logic lives.
