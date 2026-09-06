Source: docs/reference/gs/merge.md at 80d4fbf79fb41f4eb675000a5d09a4df4ec9ba74, lines 487-505.

treats stale artifacts as live until they are retired and deduplicates work
across changed files. It retires covered predecessors in the target's world,
publishes their successors, and seals why every other covered candidate stayed
live.

| Situation | Enforced result |
|---|---|
| One live exact path covers an added or modified file, or a rename destination | One successor is published at that exact changed string. In-target predecessors at that string are retired; other candidates are accounted for and stay live. |
| A directory and something inside it both cover a landed path | The exact changed path receives the successor. An in-target wider pointer stays live as carried, with no cleanup obligation; outside-target candidates are siblings or abandoned. |
| A rename source or deleted file is removed | Its exact old path is retired with no successor there. Because removal changes covering directories, in-target directory pointers may be retired and the widest directory successor published. |
| An in-target wider pointer covers a landed destination | It stays live and `Gitseq-Left-Live` records it as carried. It creates no cleanup obligation. |
| A non-target candidate has an unsettled commitment naming its head or reaching its artifact | It stays live and `Gitseq-Left-Live` records it as a sibling with the protecting commitment. |
| A non-target candidate has no unsettled commitment | It stays live and `Gitseq-Left-Live` records it as abandoned. Its author or a `ratifier` owes the bare supersession. |
| Testimony names a settled, mismatched, or unknown commitment | The receipt remains effective and grants no extra authority. The testimony is unverified and the successor keeps its succession warning. |
| An artifact appears after the sealed snapshot | It is outside the plan and the successor warns that succession was not recorded. A later merge at the path accounts for it. |
| No live artifact covers an added or modified file | A first artifact is published at the changed file path. |
| A file is renamed | Its exact old path is retired without a successor there. The destination receives a first artifact or the successor for the live path already covering it. |
| A file is deleted | Its exact old path is retired with no successor. A live covering directory still receives its successor because the directory changed. |
| A successor rests on the predecessor the same merge retires | The successor stays current. The work stood on what it replaces, and the merge that publishes one withdraws the other in the same act, so that withdrawal is not news arriving underneath it. Only artifacts that merge actually published — at its merge head, at a path it declared — read it that way; any other record citing the receipt goes stale as usual. |
