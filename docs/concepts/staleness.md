---
title: Staleness
summary: What a flare means, what it does not cover, and one known gap.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:fcf3a656a218276298c194b8e48fa6f70d7b8dde
---

# Staleness

## What a flare means

Retiring an act propagates staleness to everything resting on it,
transitively. A statement or artifact whose basis has been retired is
marked **stale**.

A flare means **re-check this**. It does not mean *this is wrong*. The
world moved under a document; someone has to look and decide whether the
prose still holds.

## Retired is not stale

The projection keeps two facts apart, and it is worth holding them apart
too. **Retired** means this act was itself superseded. **Stale** means
something underneath it was.

The difference decides what happens next. A retired artifact names
nothing anyone is proposing. A stale one still names the commit it always
named, so [`gs review`](../reference/gs/review.md) will still review it
and will record in the verdict what had moved.
[`gs merge`](../reference/gs/merge.md) refuses both, because no reviewer
is present at that step to weigh the difference.

`gs status` marks them separately:

| Mark | Meaning |
|---|---|
| `succeeded` | This artifact was superseded and the act named where the behaviour went. |
| `retired` | This act was superseded and nothing was named in its place. |
| `stale` | Something this rests on was retired. |
| `stale`, noted `describes a superseded world` | The retired ancestor was itself an artifact, so the implementation it described has been replaced. |

The last narrows `stale`. It is the one that usually means real work. The
full tables under `gs status --all` write these as `SUCCEEDED — replaced
at the same path`, `RETIRED — withdrawn with no successor`, `STALE` and
`STALE — describes a superseded world`.

## Replaced is not condemned

Every merge withdraws the artifact that stood at the branch head and
publishes one at the same path for the head that landed. If that
withdrawal flared, every completed loop would flare on the act that
completed it: the approval, the report and the commitment would all be
told to re-check reasoning that had just been acted on. A flare carrying
no information teaches people to ignore the flares that do.

So the fold reads a retirement for what its own act rested on.

**Succeeded.** The supersession rests on an artifact standing at the same
path, or at a directory covering it. The pointer moved and the log says
where. Nothing resting on the retired artifact goes stale from that act.

**Condemned.** The supersession names no covering artifact — a bare
`gs supersede`. The behaviour was deleted, or the claim was never true.
Everything resting on it goes stale, exactly as before.

The signal is what the retiring act rested on, and nothing else. The
tempting shortcut is structural — call a retirement succeeded whenever
some later live artifact happens to stand at the same path — and it fails
on the case that matters most: an artifact retired *because it was wrong*
would be quietly rescued by the next unrelated publication in that tree,
and everything resting on the false claim would stop flaring. A successor
is something the retiring actor states and signs. A bystander cannot
supply one afterwards.

One link is followed, not a chain. If the successor is itself retired
later, its own retirement carries the same link, so a reader is handed on
rather than left guessing.

### What succession does not quiet

Succession answers the reasoning that stood on the artifact. It does not
answer a page that *describes* it. A document resting on an implementation
artifact still flares when that artifact is superseded, and still reads
`describes a superseded world`, because the behaviour it explains has
changed and the prose has to be re-read against it. That is the whole
reason the merge step retires the live artifacts covering what it changed.

The rule is the edge, not the act: artifact-to-artifact provenance always
carries the flare; every other `rests_on` edge is quieted by succession.

## Two marks about practice

Alongside staleness, the projection reports two situations that are
warnings about how the record is being kept rather than verdicts on any
act.

**`unable to flare`.** An artifact that cites nothing — or cites only
events the log does not contain — can never be made stale by anything,
because `supersede` needs a target it can resolve. Its silence is not
currency, and the projection says so rather than letting it pass as
current. One resolvable basis is enough to escape: it is a handle a
future supersession can take hold of.

**`succession not recorded`.** An artifact that follows a still-live
artifact for the identical path is a probable forgotten supersession. The
act stays effective; the warning is a to-do. Recording the succession
clears it. Paths are compared as exact strings, because path is a free
body field and guessing which spellings mean the same tree would be
guesswork.

`gs status --all` reports both the number of rows affected and the number
of supersessions actually owed, because one forgotten retirement at a
long-lived path repeats on every later link of that chain. The row count
overstates how many situations there are to act on; the owed count is the
work.

## What staleness does not cover

**It is not a correctness check.** Nothing verifies that a page's prose
matches the code it names. The record tells you when to look, not what
you will find.

**It is only as good as the anchoring.** A document that names no
governing act never flares. That is why `unable to flare` exists, and why
this documentation set has a test for it.

**It does not follow paths it was not told about.** Staleness travels
along `rests_on`, not along file paths or imports. Work that never
recorded an artifact is invisible to it.

**Ordinary commit trailers are not durable evidence.** A commitment
associated with a checkout only through a `Rests-On:` trailer rests on
nothing the fold can verify, because trailer text is not an actor-signed
statement. How a reader is warned about that is a presentation question;
see [`gs serve`](../reference/gs/serve.md).

## The known gap

**Staleness does not propagate through ineffective bases.** If a page is
anchored to an act that was judged ineffective, retiring that act's own
bases will not flare the page: the chain is broken at the ineffective
link, so the page goes quiet rather than stale.

This is open work, carried by the lying-by-omission request. Until it
closes, the practical defence is to anchor pages to acts you have
confirmed are effective and live — which `gs status` and
[`gs provenance`](../reference/gs/provenance.md) both show.

## See also

- [Anchoring](../anchoring.md) — how the pages in this set name their
  acts.
- [`gs supersede`](../reference/gs/supersede.md)
- [`gs status`](../reference/gs/status.md)
