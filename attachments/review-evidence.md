# I6 exact-head review evidence

Candidate `3e3556e3dd26f8328f99828211115755033298b9` changes only the eight requested documentation paths. Each implementing commit rests on request19649. All behavior-source artifacts below were live and their exact cited path bytes equal current base39c7; no source artifact was republished. Existing unrelated architecture provenance remains visible, including ordinary staleness.

## Coverage

| Page | Reconciliation |
|---|---|
| [docs/concepts/work-loop.md](docs/concepts/work-loop.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Explicit result and optional promise; thirteen states, terminal field, report versus landing completion, inherited destinations/holds, current stale-claim attention, artifact audit, canonical citation admission, dated world staleness. |
| [docs/reference/gs/status.md](docs/reference/gs/status.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Preserves durable versus advisory status distinctions; replaces obsolete Work counts with actual Table/Graph populations and proposal count, and corrects dated world-staleness and succession explanations. |
| [docs/reference/gs/work.md](docs/reference/gs/work.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Five default relationship lanes plus explicit audit lane, optional false-versus-absent debt filter, closed debt exception and current Git observations outside the durable cursor. |
| [docs/reference/mcp/status.md](docs/reference/mcp/status.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Direct reporting-artifact next act, hold owner, closed transfer without a terminal value, bounded status omission versus work audit selection. |
| [docs/reference/mcp/work.md](docs/reference/mcp/work.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Exact query choices and omission policy; fold-selected approval/candidate versus latest review; receipt/terminal and approved-artifact carry transfer. |
| [docs/reference/gs/merge.md](docs/reference/gs/merge.md@3e3556e3dd26f8328f99828211115755033298b9:1) | State@3 held/unheld versus legacy authorization, exact destination/release signer and act-time checks, compatibility receipt warning, dated world staleness, unchanged carried/sibling/abandoned exact-path rules and reader-deployment boundary. |
| [SKILL.md](SKILL.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Keeps adopted discipline; aligns next actor and query triage with delivered behavior, retains instruction to honor holds despite the tool compatibility window, and corrects citation admission and exact cited-revision preview guidance. |
| [docs/reference/architecture.md](docs/reference/architecture.md@3e3556e3dd26f8328f99828211115755033298b9:1) | Reconciles layers 2, 5, 6 and 7 with delivered request/hold/status/query/UI behavior; removes future-policy contradictions and distinguishes actual claimed-stale attention from the separately commissioned recovery. No architecture contract changes. |

## Actual behavior sources

The links use the exact candidate head. Each source file at that head is byte-identical to its live behavior artifact revision listed beside it; this preserves usable previews under the record-head precedence rule. Directory evidence links a concrete file under the cited directory.

| Source | Basis |
|---|---|
| [fold: internal/workroom/fold.go](internal/workroom/fold.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:608be185aaba9343eba9175c04bf10a20a04b015 at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [landing: internal/workroom/landing.go](internal/workroom/landing.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:7452b69266324ba978fe1fd371defb3b658dca49 at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [authoring: internal/app/request_authoring.go](internal/app/request_authoring.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:cd7ea9e4bc9d97dd95133d999766029d1bd60cf6 at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [cli: cmd/gs/main.go](cmd/gs/main.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:14a05c918ecb152f54bf0eea4848339aba18fdb1 at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [mcp: cmd/gitseq-mcp/main.go](cmd/gitseq-mcp/main.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0f2c5ac05d9e834d7e824680eafa805e43a1c04d at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [service: internal/service/browser.go](internal/service/browser.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0a31c287af5b705b6b0991914cafd64d6ab4d39a at 39c7a0e74f4a828ba8223acbacc3f2c82eb58016 |
| [status: internal/statusview/view.go](internal/statusview/view.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:982bfe9e7df98bde8c6f8797112498fb300baf4a at 2348ebe66e226a889f08a1b90ccc3d6a45f437e4 |
| [actor: internal/statusview/actor.go](internal/statusview/actor.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f85c910b62d17846463092a668e7af6d19b20fb at 2348ebe66e226a889f08a1b90ccc3d6a45f437e4 |
| [query: internal/statusview/query.go](internal/statusview/query.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4db0902514c7bc1af75c364851f7da3c40cfa177 at 2348ebe66e226a889f08a1b90ccc3d6a45f437e4 |
| [viewlanding: internal/statusview/landing.go](internal/statusview/landing.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:adafb7b0046989609ff369efcac5acb605aa403a at 2348ebe66e226a889f08a1b90ccc3d6a45f437e4 |
| [git: internal/app/landing.go](internal/app/landing.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:4c2c9d0ef010bb7227472c4b8ada52a33f4723e5 at 2348ebe66e226a889f08a1b90ccc3d6a45f437e4 |
| [details: internal/app/landing_details.go](internal/app/landing_details.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:1dee44934842b9277a1125af2e9ea6d01f0ec786 at 7cd1c87702c20a9794b9b061dd0f769d94b9c3cb |
| [kernel: internal/kernel/kernel.go](internal/kernel/kernel.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:2198b8aaa2da6921f555c380d24385edaabcb787 at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74 |
| [preview: internal/service/preview.go](internal/service/preview.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:0438e5f5a6b2167feceb5a0c8646280a4227794c at b2ed92662464b9ca7670395fce26fe723fa069d1 |
| [rows: ui/src/lib/rows.ts](ui/src/lib/rows.ts@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6f5ca1c3b34c09a4a1a5f26ac366b94c748e3ca9 at 39c7a0e74f4a828ba8223acbacc3f2c82eb58016 |
| [list: ui/src/components/RequestList.tsx](ui/src/components/RequestList.tsx@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a3c6d28f602ea92883a8c4aa586c5b71f341b5db at 39c7a0e74f4a828ba8223acbacc3f2c82eb58016 |
| [mergeplan: internal/mergeplan/mergeplan.go](internal/mergeplan/mergeplan.go@3e3556e3dd26f8328f99828211115755033298b9:1) | git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:bd891443ff868623f2ad427b4a5becd32359e5d3 at 183211c3b5bff170171c5bfbe9bfbc3aacde08f6 |

## Checks and limits

`make build` passed for both CLI binaries in the owned worktree. The initial four documentation gates passed against an independent read-only checkout with a complete verified sequence at19657. They executed the documented command examples in disposable repositories; no witness was reset or existing service changed. A second run checks the exact final committed head after a small wording correction. Its complete result and log are attached on the primary artifact.

Each of the 17 selected source artifacts is live, describes no superseded world and has byte-identical source at its exact path in current base39c7. Link and frontmatter checks are part of the existing documentation gates. The remaining maintained docs were searched for the superseded status count, future hold proposal, old Work population labels, undated world-staleness refusal, restart instructions and obsolete terminal wording. The delivered shared landing-observations page and source-preview reference already describe the relevant current contracts and remain unchanged. Historical notes remain historical.

Current claimed-stale attention is documented honestly: without a live completion the promise survives but the lifecycle becomes stale and the query classifies it not_actionable. Planner has separately commissioned design recovery19656/replacement19659. The pending review-binding guard19651 and known other-destination publication classifier finding19530 are not pre-documented as delivered. The current no-Git-artifact lane destination gap remains explicitly identified in the merge reference.

Independent review must record Architecture, Security and Simplification conclusions for this exact head. The work changes no fold, schema, HTTP/CLI/MCP/UI implementation, hold policy, authority chain, production service or deployment. A held request still requires the authorized owner release under the agent discipline; the tool compatibility warning is descriptive evidence, not permission to bypass that instruction.

After ratified independent approval, seal all eight changed exact paths, verify succession and source/sequence/receipt publication and CI, then remove the owned worktree and local/remote feature branch. Coordinate overlap with Claude before either documentation head lands.

## Corrected review finding

Review19681 (00fe7845b651b48f7aaddea02d4f49544fa075ee), ratified19682, found the illustrative SKILL source link was parsed as a nonexistent repository path by root TestLocalMarkdownLinks. The corrected head removes that placeholder. The root check passes, and all four documentation gates pass again at this exact head against verified frontier19682. No other condition, authority, performer or destination changed.
