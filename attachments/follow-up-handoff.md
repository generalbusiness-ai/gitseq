# Bounded follow-ups from actual Chess play

Planned against b97c6a82ef7e3618721696f5a69efef13da10a79, 2026-09-06.
These are recommendations, not implementation assignments. File a durable
request for each accepted outcome, promise it, and use a request/<slug>
worktree. Preserve exact-head artifacts, independent architecture/security/
simplification review, ratification, sealed merge, push and cleanup.

Before implementation, compare `git diff --stat b97c6a82..HEAD -- cmd/chess
README.md docs`; inspect drift in the named symbols. This repository is Go
1.26.7 with embedded HTML/CSS and plain JavaScript, no package-manager UI
build. Match existing inert DOM writes and `checkedGameDraft`; use the shared
host identity and Chess encoders. Existing tests are `go test ./...`,
`go test -race ./...`, `go vet ./...`, and
`node --test cmd/chess/app_ui_test.cjs`. Build is `go build -o <temporary-path>
./cmd/chess`. Existing exact-source Node tests passed in this review; full
race/vet were part of the separately delivered transport validation.

## 1. Tell the player the current state — implementation request, M

Scope: cmd/chess/ui/app.js, game.html, lobby.html, app.css,
cmd/chess/app_ui_test.cjs, cmd/chess/main_test.go, agent_client.go,
service_actions.go, their existing refusal tests, and the existing bounded
read composition only if needed to expose the already computed seat role.
Architecture layers 6–7; no change to layer 5 seat rules or layers 1–4.

Current facts: `refreshBoard` updates status, seats and last move but never the
server-rendered `.join-instruction`. `observeLive` catches outages and retries
without a player-facing connection state. `prepareGameAction` creates a new
key and prepares a move even when it cannot hold the side to move. The
server/fold correctly refuses the signed act later. Do not weaken that check.

Steps:
1. Derive help and action availability from the same current game projection.
   Eliminate the independent initial-only open-seat instruction. Replace the
   lobby's false CLI-only claim with the real browser move flow. Add a test
   sequence open → joined → finished without document reload.
2. Expose a bounded, advisory current-session seat result from the existing
   host/Projection.SeatFor path; do not compare only raw fingerprints where
   anchors and scope apply, and do not derive authority from presence. Render
   “You are watching. This tab cannot move White's pieces” before presenting
   a move confirmation. Distinguish no key, unanchored key and an anchored
   identity. Keep the final signed fold refusal authoritative under races.
3. Keep the last verified board visible during disconnection, with a clear
   “Connection lost. Reconnecting…” label; clear that label only after a
   successful bounded read. Do not say “agent disconnected” when only the
   service connection is known. Do not automatically re-sign pending actions.
4. Make CLI preparation conflicts actionable using a closed bounded reason
   code/message, preserving existing generic handling for unknown/unsafe
   responses. A changed position says to read it and choose again. An
   uncertain submit still says retry the exact saved action. Never expose
   arbitrary server response text or secrets.

Verify after each relevant step with focused Go tests and the Node command;
all must exit 0. Add refusal tests by the existing web_actions/agent_refusal
patterns, then full race/vet. Actual Chrome acceptance: join and finish without
reload; help never offers an occupied seat; reload an unanchored game and see
watcher guidance before signing; stop/restart the disposable service and see
honest connection state with unchanged board and no automatic mutation.

Stop and request a scope decision if this needs new seat authority, player
key persistence, a copied identity resolver, or public exposure. These are
separate outcomes. Maintenance: help and controls must follow refreshed
projection state, not template-time state.

## 2. Connect the user's existing agent once — design request, M

Scope the design to docs/local-agent.md, the existing agent_commands.go/
agent_client.go interface, and the lobby/game front door. Do not implement a
new engine or agent framework during this design. Existing server mode
already supports a private agent key, pinned canonical genesis, typed moves,
explicit predecessor and restart-safe retry; the missing product path is
getting a user's agent onto that connection and keeping its task alive.

Specify one copyable non-secret handoff: selected loopback service, expected
genesis, game/side intent and explicit existing key selection by the agent.
Never put private keys, invitations or local custody files in a shareable URL.
The human should not copy predecessors. Define how the agent discovers a
human move, reads it, chooses its reply and resumes its exact pending action
when outcome is unknown. Prefer existing MCP/CLI capabilities and a bounded
read/wait primitive if the current interface lacks one; avoid busy polling.

Acceptance for the later implementation request: one human handoff starts an
actual configured agent; at least ten reply turns need no extra prompt or
manual identifier copy; disconnect and restart retain the same key and game;
unknown delivery replays exact signed intent; observable opponent activity is
advisory and never grants a seat. Record setup commands and handoffs before
and after. Validate in native Chrome plus a separately configured actual
agent. If the environment cannot demonstrate unattended continuation, report
that limitation rather than presenting a scripted reply engine as the result.

Stop if the design needs server custody of the agent key, delegation to play
the human side, remote/public transport or a new identity scheme. Those need
separate durable authority. This design can proceed alongside step 1.

## 3. Recover the human, then make repeat play comfortable — split requests

First a recovery design (M, high risk), scoped to the existing browser identity
presentation and documented anchor flow. Current `createSessionKey` generates
a non-exportable memory-only key; full navigation, reload and tab closure lose
it. The observed seat loss is intentional. Offer a deliberate pre-join choice:
“Use a recoverable identity” through existing host anchoring, or “Play in this
tab only; closing or reloading ends access.” Test a disposable configured
provider/root identity through reload, expiry, revocation and scope refusal.
The new key must recover only an already authorized anchored seat. If neither
existing provider can support the intended default setup, report the gap and
request a custody decision. Do not silently persist/export a private key or
claim anchoring restores an already lost unanchored seat.

Then a separate game-surface implementation (M, medium risk): compact the
heading/identity details so the board, turn and confirmation fit together;
make Black's orientation explicit or offer flip; replace raw-result casing
with “You won — your agent resigned.” Reuse typed create/resign/draw builders
for explicit game controls; scope any browser action-contract additions in
architecture and tests. A rematch is a new game with explicit sides and player
consent, not mutation of the finished game. A verified move list must identify
its game and immutable positions; do not reconstruct another chess rules
engine in JavaScript. Prioritize new-game/rematch over decorative animation.

Acceptance: keyboard and normal desktop/zoomed browser can see the complete
board and act without repeated page scrolling; changing game does not
silently imply the old tab key survived; result has a clear next-game action;
rematch swaps sides only when chosen; replay leaves the final durable game
unchanged. Full existing tests plus native human/agent acceptance must pass.
Recovery and layout should not become one oversized implementation lane.

## Considered and rejected

Do not replace signature checks, permit a new unanchored key to take an old
seat, infer agent thinking from a seated public key, or label the functioning
local transport broken. Do not treat public hosting as a prerequisite for the
local fixes. The adopted public-host shape, destination/custody choices and
activation gates belong to the separate still-owed #506 commitment.
