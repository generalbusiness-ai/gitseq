---
title: Staleness
summary: What a flare means, what it does not cover, and one known gap.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:49d2d3d82ebba3ffec1a0c343d3ecba17f96c3f2
---

# Staleness

## What a flare means

Retiring an act propagates staleness to everything resting on it,
transitively. The projection marks a statement or artifact whose basis
someone has retired as **stale**.

A flare means **re-check this**. It does not declare *this wrong*. The
world moved under a document; someone has to look and decide whether the
prose still holds.

## Retired differs from stale

The projection keeps two facts apart, and holding them apart yourself pays
off too. **Retired** means a later act superseded this act itself. **Stale**
means a later act superseded something underneath it.

The difference decides what happens next. A retired artifact names
nothing anyone proposes. A stale one still names the commit it always
named, so [`gs review`](../reference/gs/review.md) will still review it
and will record in the verdict what had moved.
[`gs merge`](../reference/gs/merge.md) refuses the retired one and lands
the stale one. A withdrawn pointer proposes nothing, while the head an
approval named stays immutable and still names the commit the reviewer
signed for, so `merge` writes ordinary staleness into the merge receipt
rather than refusing it. The narrower `describes a superseded world` fact
in the table below makes the exception: `merge` refuses that, because
something has replaced the behaviour the record describes.

The write boundary makes the same distinction about what a new act may
rest on. The boundary refuses an act resting on a retired basis, because
nothing stands there any more; the escape takes an explicit, signed step
(see [`gs state`](../reference/gs/state.md)). The boundary admits an act
resting on a merely stale basis, and writes what had moved into the act's
`body.stale_bases`, in the same line a merge receipt would carry. The
boundary records the staleness rather than arguing with it, and it still
counts as a flare: someone has to look. The note belongs to the boundary in
both halves: the boundary writes it before signing, and at sequencing
computes the note again and refuses any act whose signed `stale_bases` does
not match it exactly, so nobody can sign a staleness story of their own.

The guarded [`gs reassign-if-unclaimed`](../reference/gs/reassign-if-unclaimed.md)
reads staleness the same way. It protects one statement — nobody has
claimed or completed this request — and a basis moving under the request
leaves that statement exactly as true as before, so a stale unclaimed
request can still receive a new owner.

`gs status` marks them separately:

| Mark | Meaning |
|---|---|
| `succeeded` | A later act superseded this artifact and named where the behaviour went. |
| `retired` | A later act superseded this one and named nothing in its place. |
| `stale` | A later act retired something this rests on. |
| `stale`, noted `describes a superseded world` | The retired ancestor itself recorded an artifact, so something has replaced the implementation it described. |

The last narrows `stale`, and usually means real work. The
full tables under `gs status --all` write these as `SUCCEEDED — replaced
at the same path`, `RETIRED — withdrawn with no successor`, `STALE` and
`STALE — describes a superseded world`.

## Replaced does not mean condemned

Every merge withdraws the artifact that stood at the branch head and
publishes one at the same path for the head that landed. If that
withdrawal flared, every completed loop would flare on the act that
completed it: the fold would tell the approval, the report and the
commitment to re-check reasoning someone had just acted on. A flare carrying
no information teaches people to ignore the flares that do.

So the fold reads a retirement for what its own act rested on.

**Succeeded.** The supersession rests on an artifact standing at the same
path, or at a directory covering it. The pointer moved and the log says
where. Nothing resting on the retired artifact goes stale from that act.

**Condemned.** The supersession names no covering artifact — a bare
`gs supersede`. Someone deleted the behaviour, or the claim never held.
Everything resting on it goes stale, exactly as before.

The signal comes from what the retiring act rested on, and nothing else. The
tempting shortcut looks structural — call a retirement succeeded whenever
some later live artifact happens to stand at the same path — and it fails
on the case that matters most: the next unrelated publication in that tree
would quietly rescue an artifact retired *because its claim proved false*,
and everything resting on the false claim would stop flaring. The retiring
actor states and signs a successor. A bystander cannot
supply one afterwards.

The fold follows the link to its end. If something later replaces the
successor itself, its own retirement carries the next link, and the chain
still answers for everything that stood on the first artifact. If an act
instead retires any successor in the chain with no successor, the behaviour
stands condemned after all: everything that stood on the predecessor flares
then, exactly as if someone had withdrawn its own basis, so a finished loop
cannot look current after someone has found its replacement wrong.

### What succession does not quiet

Succession answers the reasoning that stood on the artifact. It does not
answer a page that *describes* it. A document resting on an implementation
artifact still flares when a later act supersedes that artifact, and still
reads `describes a superseded world`, because the behaviour it explains has
changed and someone has to re-read the prose against it. For exactly that
reason the merge step retires the live artifacts covering what it changed.

The rule follows the edge, not the act: artifact-to-artifact provenance
always carries the flare; succession quiets every other `rests_on` edge.

### A merge checkpoints what it publishes

A merge receipt often stands on reasoning that had already moved. The receipt
records that and keeps it. Its successor does not inherit it: on the one edge
from a receipt to an artifact that same merge published, the merge settled
causes already active when the receipt sealed, so the successor starts
fresh. The receipt stays stale, and only the successor starts the new
current epoch — and that makes a merged implementation a basis worth
resting on.

The checkpoint travels one edge and no further. A record that merely cites a
receipt keeps nothing. Nor does an artifact by another author, or one standing
at a commit or a path the receipt never declared. A cause that arose after the
receipt, a planned retirement whose successor a later act condemned, and
retirement of the receipt itself all still flare the successor, and
`describes a superseded world` stays untouched throughout.

## Two marks about practice

Alongside staleness, the projection reports two situations that warn
about how people keep the record rather than judging any act.

**`unable to flare`.** An artifact that cites nothing — or cites only
events the log does not contain — can never go stale, whatever happens,
because `supersede` needs a target it can resolve. Its silence does not mean
currency, and the projection says so rather than letting it pass as
current. One resolvable basis suffices to escape: it gives a future
supersession a handle to take hold of.

**`succession not recorded`.** An artifact that follows a still-live
artifact for the identical path probably marks a forgotten supersession.
The act stays effective; the warning serves as a to-do. Recording the
succession clears it. The fold compares paths as exact strings, because
path counts as a free body field and deciding which spellings mean the same
tree would come down to guesswork.

`gs status --all` reports both the number of rows affected and the number
of supersessions actually owed, because one forgotten retirement at a
long-lived path repeats on every later link of that chain. The row count
overstates how many situations need action; the owed count measures the
work.

## What staleness does not cover

**It does not check correctness.** Nothing verifies that a page's prose
matches the code it names. The record tells you when to look, not what
you will find.

**It works only as well as the anchoring.** A document that names no
governing act never flares. For that reason `unable to flare` exists, and
this documentation set has a test for it.

**It does not follow paths nobody told it about.** Staleness travels
along `rests_on`, not along file paths or imports. It cannot see work that
never recorded an artifact.

**Ordinary commit trailers do not count as durable evidence.** A commitment
associated with a checkout only through a `Rests-On:` trailer rests on
nothing the fold can verify, because trailer text carries no actor
signature. How to warn a reader about that remains a presentation question;
see [`gs serve`](../reference/gs/serve.md).

## Ineffective bases

**Staleness does not propagate through ineffective bases.** If a page
anchors to an act the fold judged ineffective, retiring that act's own
bases will not flare the page: the chain breaks at the ineffective
link, so the page goes quiet rather than stale. That follows the governed
rule, and the rule has not changed: a refused record carries no authority
and no staleness, and nothing under it can reach anything above it.

Instead, the fold says so where the citation happens. An
effective statement or artifact that rests on a refused record carries `ineffective_bases`
in the projection, naming the refused citations directly; the artifact row
in `gs status --all` notes `rests on ineffective support`; and filing an
act on such a citation earns the same note the other dead bases earn, on
standard error from `gs state` and under `dead_rests_on` from the MCP
`state` tool, classified `ineffective`. The note stays advisory: the
boundary admits the act, asks for no override, and records no staleness,
because none exists. The disclosure applies directly and stops there: a
record resting on that record sees a live basis, and reads the disclosure
from its row.

The practical defence has not changed: anchor pages to acts you have
confirmed as effective and live, which `gs status` and
[`gs provenance`](../reference/gs/provenance.md) both show.

## See also

- [Anchoring](../anchoring.md) — how the pages in this set name their
  acts.
- [`gs supersede`](../reference/gs/supersede.md)
- [`gs status`](../reference/gs/status.md)
