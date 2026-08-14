# Pane Rehydration

Rehydration is how a client comes to hold a pane's content. The daemon owns the
pane: it runs the shell, feeds a VT emulator with every byte the shell produces,
and keeps that emulator for the pane's whole life. A client owns a second VT
emulator per pane and is expected to arrive at the same picture.

This document states the contract that every route into a pane must satisfy, and
records where the implementations differ.

## The two sources a client can be filled from

**The snapshot.** `PTY.GetTerminalState` (`internal/session/session.go`) serializes
the daemon emulator's visible grid, cursor position, DEC modes, kitty keyboard
stack, the alternate-screen flag and up to 1000 scrollback rows. The client
applies it in `OS.restoreTerminalContent` (`internal/app/session.go`). It is a
snapshot of *now*: applying it is idempotent and carries no history.

**The stream.** Every PTY keeps a 64KB ring of the bytes it has produced
(`PTY.appendToBuffer`) and a monotonic `outputSeq` counting every byte ever
produced. `PTY.Subscribe(clientID, fromSeq)` replays the ring from `fromSeq`
onward and then streams live. `PTY.Unsubscribe` returns the position the client
reached, which the daemon parks in `connState.ptyResume`. The stream is history:
applying it advances the emulator, so applying the same bytes twice paints them
twice.

The two are not interchangeable and they are not additive. That is the whole
subject.

## The invariant

For every route, once the route has completed and the pane is quiet:

1. **Grid.** The client emulator's visible cells equal the daemon emulator's
   visible cells, for the same size.
2. **Scrollback.** The client emulator's scrollback lines are a suffix of the
   daemon's, and every line they share is equal. A client may hold less history
   than the daemon; it may never hold history the daemon does not have, and it
   may never hold a line the daemon does not have at that offset.
3. **Cursor.** The client's cursor is at the daemon's cursor.
4. **Modes.** Alternate-screen flag, DEC modes and the kitty keyboard stack match.
5. **No duplication.** Content the pane produced once appears once.

Invariant 5 is not implied by 1-4 read loosely; it is the one the known bugs all
broke, so it is stated separately.

## The routes

Seven routes reach a pane. They collapse into exactly two client-side
mechanisms.

**M1 - `primePaneFromDaemon`.** Snapshot, then subscribe with whatever
`ptyResume` the daemon still holds for this pane.

**M2 - `RestoreTerminalStates` then `SetupPTYOutputHandlers`.** Snapshot for
every window in the session, then subscribe the current workspace's panes. Every
route that reaches M2 has been through `handleDetach`, which clears `ptyResume`
whole, so every subscribe here resumes from 0 and is answered with the entire
ring.

| Route | Client emulator | Mechanism | Resume position | What the client is sent |
|---|---|---|---|---|
| First attach | fresh | M2 | 0 | snapshot + whole ring |
| Reattach after detach | fresh | M2 | 0 | snapshot + whole ring |
| Session switch | fresh | M2 | 0 | snapshot + whole ring |
| Daemon restart with restore | fresh | M2 | 0 | snapshot + whole ring (ring is empty for a respawned shell) |
| Workspace switch | **surviving** | M1 | last seen | snapshot + bytes produced while hidden |
| Pane created by another client | fresh | M1 | 0 (never subscribed) | snapshot + whole ring |
| Second client attaching to a live session | fresh | M2 | 0 | snapshot + whole ring |

The maintainer's expectation was that the invariant is the same for all routes
and only the implementations differ. That is half right. The invariant is the
same. But the implementations do not merely differ in spelling: **both
mechanisms apply the snapshot and the stream to the same emulator**, and the
stream they apply is history the snapshot has already accounted for. The routes
differ only in how much history gets double-applied.

## Why the snapshot and the stream cannot both be applied

The snapshot is the daemon emulator's state after consuming bytes `0..S`. The
ring replay hands the client bytes `R..S` for some `R <= S`. Writing those bytes
into an emulator that already holds the state at `S` re-runs output the client
already has.

The failure is not cosmetic overdraw, because the snapshot restores cells
without restoring the cursor. `restoreTerminalContent` calls `SetCell` in a loop
and never applies `state.CursorX`/`state.CursorY`. So after the blit the client's
cursor is wherever its own emulator last left it, and the replayed bytes are
written from there. On a fresh emulator that is (0,0); on a surviving one it is
the position the pane had when it was hidden.

Three shapes follow:

- `R == S` (pane idle while hidden): nothing is replayed, the blit is the whole
  answer, and the cursor is left stale. Grid holds, cursor does not.
- `R < S` (pane produced while hidden): the delta is painted a second time, from
  a stale cursor. This is the stacked-prompt symptom, and it survives the resume
  fix because that fix only shrank the replay from the whole ring to the delta.
- `R == 0` with a non-empty ring (every M2 route): the whole ring is replayed
  over the blit. The replay ends at `S`, so the *final* grid tends to come out
  right by accident, but the scrollback gains up to 64KB of duplicated history
  and the pane visibly repaints.

## What is authoritative

Stated as the code should behave, not as it does:

- The **daemon emulator** is authoritative for grid, cursor, modes and
  scrollback. It is the only thing that has seen every byte.
- The **ring** is authoritative for nothing. It is a latency device: it lets a
  client that never let go of a pane resume without a round trip.
- A client emulator that has been through `Close()` holds nothing, and no resume
  position may be claimed for it.

The rule that follows: **a route uses the snapshot or the stream, never both.**
Use the stream when the client provably holds the pane at a known position
(`R > 0`, and `R` was recorded by this same client for this same emulator).
Otherwise use the snapshot, and subscribe from the position the snapshot was
taken at.

## Known gaps in the wire format

- **`TerminalState.Scrollback` has no reader.** The daemon serializes up to 1000
  rows of `CellState` per pane per state fetch and `restoreTerminalContent` never
  dereferences the field. `ScrollbackLen` is read once, into a log line.
  `GetTerminalStatePayload.IncludeScrollback` is written by the client and never
  read by the daemon; `MaxScrollbackLines` is never written and never read.
- **Cursor position is serialized and never applied.**
- **Scroll position is not on the wire at all.** `Window.ScrollbackOffset` and
  `CopyMode.ScrollOffset` are client-local. They survive a workspace switch by
  accident, because the window object survives, and are lost by construction on
  every route that rebuilds windows.
- **Alt-screen content is deliberately not restored.** When `IsAltScreen` is set,
  `restoreTerminalContent` skips the cell blit entirely and relies on a SIGWINCH
  from `TriggerAltScreenRedraws` to make the guest repaint. That makes the pane's
  correctness depend on the guest cooperating, and on a resize actually being
  announced.

## Sizes

A pane whose size changed while hidden is rehydrated from a snapshot taken at
the daemon's size, into an emulator at the client's size, and then resized. The
snapshot carries `Width`/`Height` and `RestoreTerminalStates` seeds them with
`SeedAnnouncedSize` so a same-size retile does not re-announce. Nothing checks
that the snapshot's size is the size the client's emulator is at when the blit
happens.
