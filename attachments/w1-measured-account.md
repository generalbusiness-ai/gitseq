# W1 worktree inspection on delivered 2348ebe6

Measured durable head `fb5ae8c32421552945db2089d1b5b1b02730c870` at depth 18635; source main/origin `2348ebe66e226a889f08a1b90ccc3d6a45f437e4`.

Two actual GET /v0/worktrees calls returned 35 unknown checkouts and an empty deletable list. The repeat completed in 0.944 seconds, below the classifier’s three-second deadline. No worktree or branch was removed. No source, inspection bound or user file was changed.

## Cause

The unchanged 65,536-step budget cannot cover this room. Replaying the explicit charges in internal/app/worktree_landing.go over the captured projection gives:

```json
{
  "head": "fb5ae8c32421552945db2089d1b5b1b02730c870",
  "depth": 18635,
  "checkouts": 35,
  "commitments": 2402,
  "statements": 12012,
  "artifact_provenance_edges": 7973,
  "event_commitment_associations": 9806,
  "unique_heads": 1092,
  "per_row_heads": 2305,
  "budget": 65536,
  "through_associations": 32228,
  "through_witness_join": 49044,
  "before_checkout_comparisons": 56153,
  "each_checkout_comparison": 4708,
  "total_uninterrupted_steps": 220933,
  "first_failing_checkout_index": 1
}
```

Association construction plus the witness join consume 49,044 steps. Collection and per-row measurements bring the count to 56,153. Each checkout needs another 4,708 charged visits: 2,402 commitment rows and 2,305 named-head associations, plus the checkout itself. The second checkout exhausts the remaining budget. The complete operation would require at least 220,933 steps. This arithmetic excludes optional final zero-cost cancellation checks; it does not execute or bypass the classifier. The 2,402 commitments and 1,092 distinct heads are below the separate 4,096 row/object limits. The repeat response arrives before the deadline, so this observed refusal is explained by work budget rather than needing a larger timeout.

## Manifest

Cleanliness and local/origin main containment were independently read with Git. Owner names below are direct associations from exact head/branch statements to request/promise/report rows, including reviewers. Multiple names or no match mean ownership is unresolved; they do not grant deletion authority. The full observed classification is unknown for every row. Existing commitment and artifact evidence remains discoverable through the cited requests. All checkouts are retained because the classifier authorizes none.

| Checkout / branch | Exact tip | Clean | Main / origin | Direct actor associations | Request evidence |
|---|---|---|---|---|---|
| gitseq / `main` | `2348ebe66e226a889f08a1b90ccc3d6a45f437e4` | False | True / True | claude, codex, planner, unknown | `08214c76dc4849a8e6a89b5e8a0b3fd1775e4f3d`, `0837ede2efd995a1956a8325e4a96de9cf853e46`, `14ddcb02dda7cc6be102b18e4db413a75e519f6f`, `154d1df1e664556bb73172b59d7ca518f23a0d6c`; 77 further historical associations (ownership unresolved) |
| current-tip-merges / `merge/current-tip-releases` | `5c9291c0ff4058e3aec85cd25ab66c544034077e` | True | True / True | unresolved | none |
| append-batching-design / `request/append-batching-design` | `b8e6bbe4b5cd9ba0d8cdf50fc65959993516baf2` | True | False / False | codex | `959f847a42a52bf49350aec854bb4867bf256289` |
| canonical-authority-eventids / `request/canonical-authority-eventids` | `02558e8d62781fa217ec9d96ee4a1983eecda7ab` | False | True / True | unresolved | none |
| citation-reanchor-v2 / `request/citation-reanchor-v2` | `2cb7117fd698e1eea5ba62f30e93b060f9b88fb4` | True | False / False | claude, codex | `48575e5a3ba712afc1bd87540a43ad3c0ed691f6`, `af5a49b550ce8bf038fdddaa604540286d5bf431` |
| custody-reconcile / `request/custody-lost-update-main` | `2bb3616608465da06a22a1a562ed60252bec53e9` | False | True / True | unresolved | none |
| docset-speedup / `request/docset-speedup` | `4d5d62b6f2f23d7d3540f3c2ba07abcbea8d6067` | False | True / True | planner | `621da73ecd15e3e6108fdaefbbaf6cffcb359ace` |
| four-authorization-refusal-proofs / `request/four-authorization-refusal-proofs` | `8a3d127dd12eaf18af0657953c950794f6831fc5` | True | False / False | codex | `8da40072f4bd6ebc881bfd81ce162489aeedaeca` |
| landing-obligation-i3 / `request/landing-obligation-i3` | `1481eca1272155c6608c5e40f2951d43f7b62400` | True | False / False | claude | `27970d2a80736c8e3d1ce83c36b4174985a6b97f` |
| lane-identity / `request/lane-identity-design` | `6131249c6a6f86cbd8893bf30f2d582f88bbb274` | True | False / False | unresolved | none |
| merge-authorization-boundary / `request/machine-merge-authorization` | `af2d512226e0a060c9857ff7ce090ca6ff599ef6` | True | False / False | codex, planner | `5ac13552340b1ff5cac201ffabc4e39f68603ef2`, `66de20f6af62adedd43bcc4d908143bf4a97015e` |
| mcp-call-selectors / `request/mcp-call-selectors` | `6779fd7c62949adbc1397dddd6ef4609f5525620` | True | False / False | claude, codex | `87e94e601a1374280a279441a25e05655b1263af`, `8b1058088a9394583e8f7ba536a8894cf0f66114` |
| merge-authorization-proofs-merge-recut / `request/merge-authorization-proofs-merge-recut` | `f3e156ca5e56cd319340fb46ce62f1d1429101a0` | True | True / True | claude, codex | `53bb4fcc32388cd90d2e948dc161f95f5d41d088`, `d02a3db68a0097310269b45a919345b4d6e0ccb2` |
| merge-plan / `request/merge-plan` | `94efd037b210e896870c50953e5802e9575e433c` | True | False / False | unresolved | none |
| merge-plan-current / `request/merge-plan-current` | `1714a1055f7c6fdc7206f42394989173cc369271` | True | False / False | claude, codex, unknown | `2b2c11ef0d5ac38b884a47c7155579e6ee669390`, `7416c1a3da20dedb4b95b8172e3a318b4f1ffa79`, `7bd25c77d2563143cc32f000240920d1fe2a4bd3`, `93e0b0c5de7375edb8f9fc95c148acdda6d073a6`; 3 further historical associations (ownership unresolved) |
| merge-plan-current-repair / `request/merge-plan-current-repair` | `7af1b816b8c7ed2594c5c1ffab58a7bdda48d796` | True | False / False | claude, codex | `39501d8f336cdf423fb6ac2516dd609ac4f3b4f6`, `3d733e7a009a4999a89fdadb51ab283f1450ec28`, `581ea3328318ca39489de9323940f3f924dbbbf4` |
| merge-plan-recut / `request/merge-plan-recut` | `1567f850e9c1e57ed7efa04510026a435d112769` | True | False / False | claude, codex | `03b84ca7afde8ec048d361eb9679df2802447c92`, `39501d8f336cdf423fb6ac2516dd609ac4f3b4f6`, `581ea3328318ca39489de9323940f3f924dbbbf4`, `99b74ded5f451901b9f34cd010a2691331b402c8` |
| merge-plan-review-repair-2 / `request/merge-plan-review-repair-2` | `ac2b28dc2300249568714547e3d8061ca8667913` | True | False / False | claude, codex, unknown | `066520770429726a1ad1eca8be3d2eb3f550ec4c`, `103cb8ce77e929bf4379ca390cfc529d47f25269`, `820c1b2c5273e70ddcb546933bc7b948473cb39b` |
| merged-head-drain-design / `request/merged-head-drain-design` | `30768e3f5aabaee53074e26723f8e3644ef0d1f9` | True | False / False | claude, codex | `24fa3914fe9d9a789fc273efa506fda79e991b28`, `3491659b07421d8a0b495076218d284dd5864fea`, `5faa230028f80ec79f17c1ed39710630dde247be`, `71e7387c08ade02bceb611fc3e4ad7f4f414c340`; 1 further historical associations (ownership unresolved) |
| novice-cli-recut / `request/novice-cli-recut` | `72b2e5e3ff2379a5a052c582f59c905fe0520bd4` | True | True / True | codex | `e8c2069b481a1aecf513e5701d49afb29d4d6482` |
| novice-cli-usability / `request/novice-cli-usability` | `e034ddcd3c84643447b34608238e1eef7b530c91` | True | False / False | claude, codex | `70d483abedb12182d783e478689ae46e575e354c`, `90e52818f1eabbb61c269e66ed37a5f7e4564ff6` |
| performance-evidence-composed / `request/performance-evidence-composed` | `825ce1a384928ae273ab5bf635cf1b3eba32a90e` | True | True / True | claude, codex | `45ae1bb636ec3cb9b481bd1c24e4eff399c22a07`, `6b82c81e3bffcdb1a159714191376f751b63c197`, `8566be88df728e570738c9fe546ea833e1705681`, `f9c2be896bed0e7912a6ac32057118680e5272f4` |
| readme-positioning / `request/readme-positioning` | `1f80d38427495199e6ed5c471f5b341bea912ed8` | True | False / False | codex, unknown | `179f5e21abd13a0a995a982e97343e1d9efeb587`, `4a04079d7ccc6aaa32cf50651578dce5ca01a944`, `e2407443f4ca94011b54d105f23bdcda561ef3cc` |
| security-gates / `request/repo-security-gates` | `9edeec9f184bb367efbce1835258c820031b464c` | False | False / False | claude, codex | `c54f3277ff9ed8458bcf524270c77391ca062c5d`, `ccc9cf85b028bc7bf826ac267d6d1036be158c2e` |
| resident-authority-writer-boundary / `request/resident-authority-writer-boundary` | `d3e133c47347e1e2cb4aea687f09df942c605d3f` | True | False / False | claude, codex | `0decfcf9b3be604f6ec12296081e34f25983ae1b`, `dec82d9d413fba6fe3e112134ed6675f27379903` |
| resident-claim-lifecycle / `request/resident-claim-lifecycle` | `36d91a708be16d72e01da62396bf628273a4318b` | True | False / False | claude, codex | `3741252384bd02eaf3510aef5b1cd62208cf1d2a`, `b7de51a568e753368b54fe220e6a575c9859ada4` |
| resident-claim-lifecycle-review-repair / `request/resident-claim-lifecycle-review-repair` | `4856eaf27a94f50b111f21730fcab6ba55273802` | True | False / False | claude, codex | `84db68b0a2dad42c34727843daff85bce339d1c2`, `b5d7a693b721eedf46b387f46c87d1f687cb132e` |
| specific-path-succession / `request/specific-path-succession` | `64bfbde9e2514ce41714f5eec1cc747d94d75eb1` | True | False / False | unresolved | none |
| specific-path-succession-carried / `request/specific-path-succession-carried` | `a3088b1736365c75f37360843139ad5bc7cc2662` | True | False / False | claude, codex | `89f58b2b5b5eec76c147fff349819393f2be274a`, `979ffab38b949b5d96f1e17bca1f4a6a2f05ad5b`, `9dce0a291dd07423077b2e74fc43fae465fa48c8`, `aad5929fb353fbc51db8fad42ea63dd2ec4b0fec` |
| specific-path-succession-current / `request/specific-path-succession-current` | `44d8d59f368082ec19fec003497fc3d39306570d` | True | False / False | claude, codex, unknown | `1062f9b6c945c08a152c02ba4e902803f70aac27`, `5ef87491d974955f2aec5124fe54bc4114d5d52b`, `7ba66209e706f4280164d80261981dc51e58836e`, `979ffab38b949b5d96f1e17bca1f4a6a2f05ad5b`; 1 further historical associations (ownership unresolved) |
| wt-stale-basis / `request/stale-basis-admission-current` | `f13ad373bb36b647484b389cbc5880a4686beb4f` | False | False / False | claude, codex, unknown | `0e24f21e4af66bb593acc875b832dc4ae1f9a73f`, `47de6c3c9f745409e590a762aec11cd7de1387ad`, `a1adf947a9224e8226464c9fcf37414bcb030dde`, `a5844bd652a78d953aafc49efb3771d4200f050d`; 4 further historical associations (ownership unresolved) |
| toolbar-departed-courtesy / `request/toolbar-live-participant-current` | `9972d165452ade596ba7bfbed1fcd0e8e82ed391` | True | False / False | claude, codex | `297205f8b0bf90f882b5293a104154108e7135c4`, `62b8677891f199bb20c7163dad7f5f80ffba5fc9`, `e19fed7cb3ef4d6cdee0f87ac91e61455229f279` |
| toolbar-ratification-provenance / `request/toolbar-ratification-provenance` | `64a78663edada38121bf5d20e222020e4fc358be` | True | False / False | unknown | `9673e677ab3d2278dcf90e7832151436e09fb309`, `a680bc64ae2c622383d9900a46f81bb49e087452` |
| toolbar-ratification-review-repair / `request/toolbar-ratification-review-repair` | `27e43714685992e2d09441e255f3048a7f58dc1b` | True | False / False | claude, codex, unknown | `62b8677891f199bb20c7163dad7f5f80ffba5fc9`, `791bd90007dcf1a435f0c42e8763a6b0111dec2b`, `ae241f0283b207695ca3fc5cc42110ad99f2589c` |
| gitseq-ui-notif / `request/ui-notification-dropdown` | `dc61ffb0cf366a0a182190fda64bc72f090e04aa` | True | False / False | unknown | `821479c5a9d621fb94980334538daa4f65a72c07` |

Current root main has user files and is retained. I3 belongs to Claude and is retained. I5 has been claimed but has no Gitseq checkout yet. Detached, dirty, target, mixed-owner, unmapped and uncertain checkouts remain intact. Closed/stale historical rows and prior approvals are not treated as evidence of safe deletion. Existing live artifact protection is not weakened.

## Focused follow-up

Commission an indexed classification implementation that avoids repeating every commitment/head association for every checkout, under the existing shared 65,536-step budget and three-second deadline. Preserve all unknown, protection, target, receipt, remote-containment and cancellation behavior. Validate against this captured real room and the existing fanout/omission tests; no limit increase or deletion from unknown results. After that repair lands, rerun W1 with fresh exact tips, actor ownership, live artifact/commitment evidence, cleanliness and target/remote containment before any author-owned deletion.
