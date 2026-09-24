---
title: Event identifiers
summary: The one canonical name that every citation of a durable event uses.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:191ece9ae6bdc7636c4bc5c219e6af3aefb489ba
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:265b14724281203aac18927aa37ecc96dfc92523
---

# Event identifiers

Every durable event has exactly one canonical identifier:

```text
git:<object-format>:<genesis>#git:<object-format>:<event-commit>
```

For example:

```text
git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cc7eb5a129850990652c553adffbfd2f4f726f83
```

The part before `#` names the **workroom**, by the genesis hash. The part
after names the **event**, by its commit in `refs/seq/<genesis>`. Both
carry the object format, so a sha256 repository leaves no ambiguity.

## Where it appears

- `--rests-on` on `gs state`, `gs supersede` and `gs reassign-if-unclaimed`
- the positional target of `gs ratify`, `gs supersede` and
  `gs reassign-if-unclaimed`
- the argument to `gs inspect` and `gs provenance`
- `--artifact`, `--promise`, `--ack-head-news`, `--implementation` and
  `--self-initiated` on `gs review`
- `--approval` and `--authorization` on `gs merge`, `--approval` on
  `gs merge-plan`
- `target`, `retirement` and `rests_on` in a `gs batch` file
- a handful of **body fields** a consumer reads as one event: `artifact`,
  `authorizes_request`, `authorizes_approval`, `merge_approval`,
  `merge_authorization` and `merge_authorization_ratification`, wherever someone
  writes a body — `gs state --body`, a `gs batch` entry, the MCP `state`
  and `reassign_if_unclaimed` tools
- `event`, `target`, `old_request`, `approval`, `promise`, `artifacts`,
  `ack_head_news`, `implementations`, `self_initiated` and `rests_on` in
  the MCP tools
- `Rests-On:` trailers on ordinary implementing commits

The trailers form the exception to the next section: a commit message
offers no boundary for anything to resolve at, so a trailer carries the full
identifier and nothing shorter.

## The record stores only the whole identifier

A signed act only ever carries the canonical identifier. A record number
or a piece of a hash offers a way of typing one at a command line or a
tool call; it never offers a way of storing one. A fragment unique today
can collide as the log grows, so a stored fragment would make a citation
that changes meaning by itself.

`gs status` shortens identifiers for reading — `git:sha1…02aa808`. You
cannot type back an ellipsis-truncated display like that: it lacks its
middle. The next section describes what you can type back.

## Typing one at a boundary

Every page in this reference that says an input takes a **short reference**
means this section, and nothing beyond it. `gs` and the MCP tools accept three
forms wherever they take an event reference, and resolve the short two to the
full identifier **before** they sign anything:

| You type | It means |
|---|---|
| `git:sha1:<genesis>#git:sha1:<event>` | the identifier itself, used as typed |
| `#417` | the record number the displays print beside every event |
| `d54de200`, `a810640a6` | an unambiguous prefix **or** suffix of one event hash |

`#N` names the `sequence` every projected record carries: the founding seed
takes `#1` and each later record adds one. `gs status`, `gs work` and the
reading view print that number. The boundary refuses `#0` and any number
past the last record, and anything after the `#` other than a plain
number.

The boundary matches a hash reference against the **event hashes of this
workroom's verified durable records** — the part after the second
`git:sha1:` — and against nothing else. The workroom's own genesis does not
count among those records, so it has no number and no short form; cite it by
its canonical identifier. The boundary does not search Git's object
database: a hexadecimal fragment can name a blob, a tree or an ordinary
commit, and none of those names an event. Lowercase hexadecimal only, and no
longer than one object name in this repository's format.

Whatever you type, the command signs the full identifier, and it names
that identifier on standard error before it signs:

```text
gs: resolved #417 -> git:sha1:5d2622…#git:sha1:d54de2…
```

Standard output still carries the new event's identifier and nothing
else, so a shell capturing it sees no change. The MCP tools return the same
sentences in a `resolved` field of the result.

### When it refuses

The boundary **refuses** a short reference that names no event, or more
than one, and appends nothing. An ambiguous one lists the events it matched, as
`#N` and identifier, up to a bounded number:

```text
gs: event reference "a8" matches 3 events here; name one exactly
  #13520 git:sha1:5d2622…#git:sha1:d54de2…
  #13804 git:sha1:5d2622…#git:sha1:a8c4f1…
  #14002 git:sha1:5d2622…#git:sha1:1fa800…
```

This refuses human input; it differs from the fold's rule that admits in
silence a signed citation resolving to nothing. The two never meet: the
first happens before signing, the second governs what someone has already
signed.

A short reference can go stale, and only in the safe direction. The log
only ever grows, so a record number keeps naming the record it named, and
a hash fragment gains matches but never loses the one it had: a fragment
unique today becomes ambiguous tomorrow and the boundary refuses it; it
never comes to name a different event in silence. That explains why you
may type a fragment but should never store one.

### Numbers and fragments never cross a workroom

The boundary reads both short forms against the selected workroom and no
other. Two workrooms both have a `#17`, and the two have no relation. To
cite another workroom's event, write its canonical identifier: the boundary
keeps it exactly as typed, and says so rather than resolving it.

## What the substrate does with a signed citation

The rules below govern what someone has already signed, and they remain
unchanged. They differ from the boundary refusals above, which govern what
a person types.

The sequencer **refuses** an identifier for this workroom that names no
event in it before sequencing it, whatever the kind. The identifier asserts
that the event sits at a position in this log, and the substrate can settle
that one claim without knowing what any kind means, so a mistyped
or invented one comes back as a refusal naming the reference rather than
as a record nobody can repair. The gate applies to new records only:
identifiers that dangle in existing history stay in it, and still read.

The substrate still **accepts** a citation it cannot resolve at all.
Another workroom's identifier, or a string other than an identifier,
asserts nothing about this log. `assert`, `artifact` and `propose` have
no required edge either, so the substrate simply keeps such a basis and the
act records as effective. The loss stays silent: the fold marks an
artifact whose bases all fail to resolve as `unable to flare`, because
`supersede` needs a target it can resolve and so nothing could ever make
that act stale.

Events with a required edge apply stricter rules, and `ratify` the
strictest of all — it refuses any citation other than its target. See
[The record](../concepts/record.md#recorded-does-not-mean-effective).

## Getting one

Every durable `gs` command that appends prints the identifier of what it
appended, and nothing else, so you can capture it directly:

```text
EVENT=$(gs state --repo . --as alice --kind assert --text 'a claim' --rests-on "$SEED")
```

`gs init` prints the genesis hash and the seed event. The seed, the one
act that rests on nothing, serves as the usual first basis in a new
workroom:

```text
git:sha1:<genesis>#git:sha1:$(git -C <repo> rev-parse refs/seq/<genesis>)
```

`gs status --json` includes the full identifier of every event.

## What a filing surface tells you about its bases

Before `gs state`, `gs supersede`, `gs reassign-if-unclaimed`,
`gs publish`, `gs batch` or the MCP `state`, `supersede` and
`reassign_if_unclaimed` tools sign anything, they say what each basis
means here. Three cases earn a sentence and the ordinary one earns
silence:

| The basis | What you hear | What happens |
|---|---|---|
| An event this workroom holds | nothing | admitted |
| This workroom's own genesis | nothing | admitted |
| A string that names no identifier | a warning that nothing resolves it | admitted, connected to nothing |
| This workroom's identifier naming no event | a warning naming the claim | **refused** by the sequencer |
| Another workroom's identifier | a note that this room cannot verify it | admitted as an external citation |

The genesis forms the second quiet row for a reason. `git:sha1:<genesis>#git:sha1:<genesis>`
forms the root of the sequence, so the sequencer resolves it, but it does not
count as an application record: the fold projects nothing for it, it has no
record number, and no short reference reaches it. By resting on it, an act says
it bears on the founding of this workroom, and the sequencer admits it in
silence like any other resolvable basis.

Two references fall outside this table. A `$label` in a `gs batch` file
names an act the same chain has yet to mint, so nothing exists yet to
describe and the surface says nothing. And `gs review` builds its citations
through the review guard, which judges every one of them against the
projection and refuses what does not stand; a weaker description beside
that judgement would only teach readers to skip it.

The warnings describe; they refuse nothing and grant nothing. The one
refusal in the table belongs to the sequencer, and it already existed.
After the act lands, a citation already retired, stale, refused or itself
a supersession earns its own note. The MCP tools return the
same sentences in a `basis_notes` field.

## Body fields, and why only some of them

A statement's `body` forms an open map: the fold reads a few keys and carries the
rest as application prose. A boundary that resolved every value that looked
like a number would rewrite prose, so it resolves a named list instead, and
that list comes from the code that reads those fields:

| Field | Read by | As |
|---|---|---|
| `artifact` | the review gate, and the rule that an approved report rests on its artifact | one artifact event |
| `authorizes_request` | the landing-hold release | the held request |
| `authorizes_approval` | the landing-hold release | the approval it covers |
| `merge_approval` | the merge receipt | the approval sealed |
| `merge_authorization` | the merge receipt | the release or authorization carried |
| `merge_authorization_ratification` | the merge receipt | that authorization's ratification |

The boundary leaves everything else in a body exactly as typed, including the
fields that sit right beside these: `authorizes_candidate`, `merge_candidate`,
`merge_head` and `merge_target_pre_head` name ordinary Git commits, `target_ref`
and `merge_target_ref` name refs, `to` and `hold_owner` name principals, and
`merge_retirements` and `merge_successors` hold JSON documents rather than one
identifier. A field naming no event, or more than one, refuses and names the
field that carried it.

## Related identifiers other than event identifiers

The boundary reads none of these as an event reference by inference. Where
one runs short enough to form part of an event hash here, the boundary
searches it against this room's events like any other fragment and refuses
it when it names none; it carries anything else through exactly as typed.

| Looks like | Means |
|---|---|
| A 40- or 64-character hex string | An ordinary git commit. Artifacts cite these in `body.commit`, and `gs merge --candidate` names one. |
| A 64-character hex string in `actor` | An actor fingerprint. |
| `session:` followed by hex | An opaque presence handle. It grants nothing and stays ephemeral. |
| `$label` in a `gs batch` file | A name for an act the batch has yet to mint. |
| A pull request URL | A hint. Never rest a durable act on a bare URL; cite the head commit. |
