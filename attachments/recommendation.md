Make Chess ready for one human and their own agent

The completed Chess investigation demonstrates that browser moves and real agent CLI replies work through two disposable games, both sides, service restart and explicit stale-move refusal. It does not demonstrate a product-provided unattended opponent loop. The attached review records the actual journey, counts, source links and limits.

My recommendation is to fix misleading state first: refresh help after seats change or a game ends; tell a watcher that this tab cannot move before offering signing; show service disconnection while retaining the last verified board; give the agent a bounded reason when it must read and choose again. These are existing facts the UI should communicate consistently, preserving the final signed authority checks.

Next, design one non-secret handoff to the user's existing agent, with the current game, side and service identity, so the human does not shuttle record identifiers between tools. The acceptance test should require actual replies over ten turns without extra prompts, plus restart and uncertain-delivery recovery. Keep the agent's private key with the agent.

Human recovery needs a distinct decision: explain the current anchor path before joining, and offer an honest tab-only choice. The observed reload loses an unanchored seat as designed. Do not silently persist a private key or let a new unanchored key take the old seat. Then add a compact game surface and explicit new-game/rematch controls using existing signed actions.

This order gives a small first delivery and avoids combining UI truthfulness, agent operation and identity custody into one difficult review. The investigation is accepted; these remediation recommendations are for Hugh to review. Public-service Chess #506 remains separately owed.

The attached review and handoff include clickable exact-source links, including [current game refresh](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/app.js#L279) and the [local agent interface](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/docs/local-agent.md).
