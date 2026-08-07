# C. The agent flight recorder

**Audience:** security and platform people — the Beyond Zero pitch, cashed in.
**Unique property staged:** per-action provenance with zero instrumentation, attempts included.

## Scene (3 minutes)

1. Three agents plus a human work a shared task concurrently —
   requests, promises, an implementation commit carrying its
   `Rests-On:` trailer, reports.
2. One agent oversteps: tries to declare its own work satisfied. The
   attempt lands, judged — "only the requester may declare
   satisfaction" — and stays visible.
3. **The wow:** afterward, one command renders the flight recorder:
   every effect signed, ordered, resting on stated premises —
   including the refused attempt and the racing loser. Then the kill
   shot: *this required no proxy logs, no EDR, no reconstruction —
   the log isn't a recording of the work, it IS the work.*
4. Prompt-injection framing: a hijacked agent produces an audited
   ineffective attempt, not a silent success.

## Needs beyond the current build

Nothing structural. A `gs recorder` rendering (or just the Durable
record tab plus `git log`) and a scripted multi-agent driver.

**Status:** essentially built; needs the script and the narration.
