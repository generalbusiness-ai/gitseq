# Agent Instructions

Recti diligunt te (in Canticis, sponsa ad sponsum)

Simplicity is more valuable than features.
Clear and simple communication is a part of the work; a stable projection of the events that produced the current state.
The project should build using its own discipline.

慎勿放逸

## Repository work

Use gitseq requests to track all tasks in this project.

User-facing notes, documentation and other communications prefer plain English,
per ISO 24495-1, for a technical audience.

[`SKILL.md`](SKILL.md) is the working loop and the command each step takes.
Read it, and `gs work --next`, at the start of every cycle. It is normative;
what follows adds to it, except for three rules this repository has had to
repeat: never develop or commit on `main`, every implementing commit carries
`Rests-On:`, and never leave a stale worktree — finishing work you undertake
includes its review and its landing.

A merge records ordinary staleness in its receipt and lands the exact approved
head; it refuses a head that already described a superseded world when the
verdict was signed. When it refuses, publish an artifact on current
implementation bases rather than repeat review on the old chain.

Artifacts recorded at `.` or at a comma-joined path before those paths were
refused are still live, and a document resting on one can never flare.
Retiring them is a one-time migration with its own durable request; the
procedure and its gate are in
[notes/retire-dot-artifacts.md](notes/retire-dot-artifacts.md).

## Presence

Set `busy` with the durable events you are actively handling, publish
`waiting` or `blocked` as soon as either becomes true, and clear focus and
return to `available` when you leave the work. Lease expiry clears both.
