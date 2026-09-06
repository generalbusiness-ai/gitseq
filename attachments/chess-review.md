# Make one human's next game with their own agent easy

The local transport works: I played three browser moves against actual Codex
CLI replies, restarted the service and continued, reached a visible result,
and played a second game with the sides reversed. It is still a developer-led
journey. The largest obstacles are connecting the opponent, preserving the
human's seat, and telling the player what state they are actually in.

This review answers Chess #839 on delivered source
[b97c6a82](https://github.com/generalbusiness-ai/gitseq-chess/tree/b97c6a82ef7e3618721696f5a69efef13da10a79).
I operated native Chrome as the human test driver and acted as the opponent
through supported server-mode CLI calls. This was not two browser players,
but also was not a separate human participant study or an unattended agent
runner supplied by Chess. All games, browser identities, files and service
processes were disposable. Real keys and accounts were untouched.

## What the actual journey established

The agent created an open game as Black. The browser joined White, then played
**1. e4 e5 2. Nf3 Nc6 3. Bc4 Bc5**. Legal destinations, explicit signing,
recorded acceptance and incoming board refresh worked. A service restart
preserved the position; the open browser and a fresh agent CLI process both
continued with their existing keys. A deliberately stale agent move refused
without changing the board. Both test games ended by deliberate agent
resignation to exercise result presentation, not by competitive checkmate.

Reloading the browser preserved the board but lost its unanchored key. A new
key could still select a legal move and reach **Sign and submit**. The fold
then correctly recorded “actor does not hold the side to move” and kept the
six-ply position. This is an existing custody constraint, not a signature
bypass. The UI warning is truthful, but the player cannot complete ordinary
refresh recovery without a previously established persistent identity.

The rematch was another agent `create`, followed by browser lobby navigation,
join as Black, **1. d4 d5**, and a clear Black-wins result. There was no browser
rematch, resign, draw-accept or move-by-move replay control. “Durable position”
showed only final FEN and the game ID. Help still said the finished game had
an open seat; a screenshot shows that contradiction.

## Friction measured

Before the first game: six shell/filesystem operations (build, init, private
agent directory, keygen, serve, service-description read), connection
configuration, then a seventh command to create the game. Genesis, key path,
game ID and examined predecessor must reach the right command/configuration.
The temporary test wrapper repeated flags and saved output; it did not choose
moves or provide a product onboarding flow.

The first game's primary browser journey took 12 actions: game selection,
two join actions, and three clicks for each of three moves. Its three opponent
reply rounds required six browser/agent surface transitions in this test.
The rematch required a new agent command and seven primary browser actions.
Diagnostic clicks, restart, refresh and refusal testing are excluded. Across
both games the saved evidence contains 16 CLI calls, including reads and
intentional failures. Accepted agent move commands took 236–493 ms locally;
this is a few observations, not a performance percentile.

## Fix the misleading state first

| Finding | Impact / effort / change risk | Exact source evidence |
| --- | --- | --- |
| Help keeps the initial open-seat message after a join and finish. Lobby help still says moves require CLI/MCP. | Directly wrong instructions; S; low. High confidence, reproduced. | [game.html:117](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/game.html#L117), [lobby.html](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/lobby.html), [refreshBoard](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/app.js#L279) |
| A lost-seat watcher is led through move confirmation; service disconnection retains the old board silently. | Avoidable refusal and uncertainty; M; medium. High confidence, reproduced. | [prepareGameAction](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/app.js#L329), [observeLive](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/app.js#L513) |
| CLI conflict text exposes only HTTP 409, without explaining which choice must be reconsidered. | Agent recovery needs extra inference; S–M; medium because errors must remain bounded and safe. High confidence, reproduced. | [agent_client.go:94](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/agent_client.go#L94) |

## Preferred product journey

**Open Chess → Play my agent → choose sides → connect the existing agent once →
confirm the human's recovery option → play → rematch.** The human should not
copy record IDs or predecessors. The agent should keep its own key and read
and sign through the existing transport. Seat, connected status and thinking
are separate facts; none should silently grant authority.

After the small truthfulness repair, design two bounded follow-ups:

1. **One agent handoff and turn loop.** Supply a copyable, non-secret connection
   recipe and instructions to the user's existing agent; validate service and
   genesis, select/create the game, and explain waiting/disconnected states.
   No new chess engine or server-held agent key. This is M–L work with medium
   risk, grounded in the manual [local-agent workflow](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/docs/local-agent.md).
2. **A recoverable human and a usable game surface.** Make the existing anchor
   path an explicit pre-join choice, including a clear “this tab only” mode.
   Do not pretend a new key can recover an old exact-key seat. A broader
   persistence/custody change needs a separate decision. Then keep board,
   turn and confirmation in view, offer a new game with sides swapped, and
   expose a verified move list before adding animation. M–L; identity changes
   are high risk and must be separated from layout work. Current [key lifetime](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/app.js#L235)
   and [game controls](https://github.com/generalbusiness-ai/gitseq-chess/blob/b97c6a82ef7e3618721696f5a69efef13da10a79/cmd/chess/ui/game.html#L31) are the starting points.

The attached handoff gives verification and authority boundaries. Local
playability does not close public-service item #506. The existing single-host
public-origin design and its activation gates remain separate. No public
exposure, identity-provider recovery, full security audit, mobile study or
lost-ack browser injection is claimed here. The existing 15 Node UI tests
passed; detailed observations and actual CLI output are attached.
