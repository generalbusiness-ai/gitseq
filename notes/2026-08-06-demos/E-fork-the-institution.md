# E. Fork the institution

**Audience:** standards bodies, DAOs, anyone governed. Exit rights.
**Unique property staged:** offline verification and forkability with stock git.

## Scene (30 seconds — a coda, not a standalone)

1. At the end of demo A or C: `git clone` the whole workroom.
2. `gs verify` offline — every signature, every chain, no server.
3. Fork it: continue under a different sequencer key; both forks
   verifiable, the divergence attributable to the keys that signed
   each side.
4. **The wow is one sentence:** "If you don't like how this room is
   governed, you leave with everything — history, proofs, and all."

## Needs beyond the current build

`gs attach`/`verify` already do the clone-and-audit; the fork step
needs a `gs fork` convenience (new sequencer key, continuation
genesis citing the predecessor) — small, and the continuation
machinery exists in the kernel.

**Status:** verification half built; fork convenience not.
