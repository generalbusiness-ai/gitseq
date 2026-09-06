# Make installed residents reproducible

Review for Hugh: build a long-running resident from clean, exact source and keep a small deployment record beside its versioned binary. This would make a future repair start with known code instead of a source-provenance investigation. It adds no workroom state or extra approval stage.

The current Tailapp incident makes the gap concrete. The running executable identifies revision 5d84ac71, Go 1.27.0 and vcs.modified=true. A one-line inbox query correction now has actual-driver and disposable-resident evidence, but current Tailapp source also changes the runtime/input contract. Builder's [recovery recommendation](https://github.com/generalbusiness-ai/tailapps/blob/94baf5a979e11f35333e1340c4d42947e65abddd/docs/reference/resident-upgrade.md) therefore correctly requires accounting for the deployed source before a compatible backport. The dirty marker does not tell us whether source changed or only an unrelated untracked report existed.

The [checkout-upgrade script](https://github.com/generalbusiness-ai/tailapps/blob/a3b29b3becdd0c298f5e200a9f6f279a1db18b71/scripts/upgrade-resident-macos.sh) stages a binary, names it with timestamp and revision, switches the executable link, and checks service health. It does not require a clean build input or save its complete identity. That script is unchanged from the deployed revision. The current [build script](https://github.com/generalbusiness-ai/tailapps/blob/a3b29b3becdd0c298f5e200a9f6f279a1db18b71/scripts/build.sh) already disables the local Go workspace and makes module files read-only; preserve those improvements.

The smallest useful change is:

1. Build a resident upgrade from an isolated clean checkout of the exact reviewed source commit, or install an already verified release asset. Leave the operator's working files where they are. Validate clean inputs before and after building; refuse unexplained modifications before changing the executable link.
2. Alongside the versioned binary, record its SHA-256, exact source commit/tree, actual Go build metadata and active runtime identity. Use that one record for the upgrade result and later diagnostics. Source revision and runtime compatibility remain different facts.
3. Keep the existing link switch, service ownership, rollback and data-preservation rules. A binary hash proves identity, not permission to reset applications or compatibility with stored projections.

Acceptance should show that a dirty source input is refused without a link/process change, a clean exact-head build produces a verifiable deployment record, and a non-default runtime is retained in the recorded result. Check that operator files and application data remain untouched. No new migration or release tag is needed for this improvement.

This is a recommendation for future upgrades, not proof that the existing dirty binary is compatible with a clean historical checkout. It does not repair today's daemon. The immediate recovery still needs its own source and data checks. No raw telemetry was read for this review, and no measured time or cost saving is claimed. This is separate from the existing resident/adapter response-compatibility recommendation and outside the closed 20-delivery sample.
