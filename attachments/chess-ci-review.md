# Give Chess deliveries a repeatable CI check

I recommend one small CI workflow for Chess: run its existing Go tests with race detection, vet and build on each pull request and main push, with the Go version from go.mod and a Node version supporting the existing WebCrypto tests. Put the exact head and check result link in the single delivery account recommended in #19016. This adds a repeatable check, not a task state or approval stage.

The current Chess main, `7ba6463a6e4d2ce2f7e2be87464ac44ad1f4f519`, contains no `.github` workflow. GitHub's latest main-branch run is Dependency Graph #33554756997, for older head `0def23f7`; it does not establish test coverage of current main. History already acknowledged this absence in Chess #511. Earlier #306 fixed a different problem by wiring the Node assertions into the Go suite; those tests now run, and that completed work should stay closed.

The new transport at `fc3fa1f5` passed my independent full race suite, vet, Linux build and two omission controls. That is useful local evidence, recorded in Chess #777 and approved in #778. It does not create a persistent automatic check for the next change. The [Chess README](https://github.com/generalbusiness-ai/gitseq-chess/blob/7ba6463a6e4d2ce2f7e2be87464ac44ad1f4f519/README.md#L336) names the existing test entry point.

Gitseq's [CI workflow](https://github.com/generalbusiness-ai/gitseq/blob/183211c3b5bff170171c5bfbe9bfbc3aacde08f6/.github/workflows/ci.yml) provides a useful pattern: run the suite once and derive its evidence from that captured run. Tailapp's [workflow](https://github.com/generalbusiness-ai/tailapps/blob/a3b29b3becdd0c298f5e200a9f6f279a1db18b71/.github/workflows/ci.yml) also gives deliveries an inspectable automated check. Reuse that practice without introducing a shared multi-repository framework.

Keep the limits clear. A passing workflow does not replace independent source/security review, exact-head approval, sealed delivery or real browser acceptance. Establish the workflow with one passing candidate and one deliberately failing assertion so the failure path is proved. Then point reviewers to that same run instead of requiring them to reconstruct routine check provenance from prose. No elapsed-effort saving is claimed here.

This is a recommendation for Hugh to review. It does not reopen Chess #306, assert that current source is defective, or authorize public deployment.
