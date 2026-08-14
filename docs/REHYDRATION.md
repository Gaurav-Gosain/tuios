# Pane Rehydration

Rehydration is how a client comes to hold a pane's content. The daemon owns the
pane: it runs the shell, feeds a VT emulator with every byte the shell produces,
and keeps that emulator for the pane's whole life. A client owns a second VT
emulator per pane and is expected to arrive at the same picture.

This document states the contract that every route into a pane must satisfy, and
records where the implementations differ.

## The two sources a client can be filled from

**The snapshot.** `TerminalStateOf` (`internal/session/session.go`) serializes the
daemon emulator's visible grid, the normal screen underneath it when the
alternate one is active, cursor position, the pen, DEC modes, the scroll region,
the character set selection, the kitty keyboard stack, the alternate-screen flag
and up to 1000 scrollback rows. `ApplyTerminalState` reads it back, and
`OS.restoreTerminalContent` (`internal/app/session.go`) is the window around
that. It is a snapshot of *now*: applying it is idempotent and carries no
history.

The two halves live next to each other on purpose. Serializing lived here and
restoring lived in `internal/app`, which is how they came to disagree about which
fields exist: `Scrollback` was written and never read, and the cursor was
serialized and thrown away.

**The stream.** Every PTY keeps a 64KB ring of the bytes it has produced
(`PTY.appendToBuffer`) and a monotonic `outputSeq` counting every byte ever
produced. `PTY.Subscribe(clientID, fromSeq)` replays the ring from `fromSeq`
onward and then streams live. `PTY.Unsubscribe` returns the position the client
reached, which the daemon parks in `connState.ptyResume`. The stream is history:
applying it advances the emulator, so applying the same bytes twice paints them
twice.

The two are not interchangeable and they are not additive. That is the whole
subject.

A snapshot carries the stream position it was taken at (`TerminalState.Seq`),
and a subscribe names the position the client has been restored to
(`SubscribePTYPayload.FromSeq`). That pairing is what keeps them from
overlapping.

## The invariant

For every route, once the route has completed and the pane is quiet:

1. **Grid.** The client emulator's visible cells equal the daemon emulator's
   visible cells, for the same size. A cell is equal when it holds the same
   thing *and is painted the same way*: the same foreground and background down
   to the encoding, the same underline style and colour, the same attribute bits
   and the same hyperlink.
2. **Scrollback.** The client emulator's scrollback lines are a suffix of the
   daemon's, and every line they share is equal, by the same definition of a
   cell. A client may hold less history than the daemon; it may never hold
   history the daemon does not have, and it may never hold a line the daemon
   does not have at that offset.
3. **Cursor.** The client's cursor is at the daemon's cursor.
4. **Modes.** Alternate-screen flag, DEC modes and the kitty keyboard stack match.
5. **No duplication.** Content the pane produced once appears once.
6. **What paints the next byte.** The pen, the scroll region and the character
   set selection match. None of these can be read back off the cells, and each
   decides how output that has not arrived yet is painted, where it lands and
   which glyphs it draws.
7. **The screen underneath.** While the alternate screen is active, the normal
   screen matches too. It is what quitting the guest's program puts back on
   display.

Invariant 5 is not implied by 1-4 read loosely; it is the one the first round of
known bugs all broke, so it is stated separately. Invariants 6 and 7 are the
second round: a pane can satisfy 1-5 exactly and still be wrong the moment it
prints its next line or the guest quits.

Two tests assert this, and they are not redundant.

`TestRehydrationMatrix` (`internal/app/rehydration_matrix_test.go`) proves a
*route*. It runs a real daemon in process and takes a pane through every route
below crossed with every shape a pane can be in: at the live tail, scrolled back,
in the alternate screen, holding wide runes, under heavy SGR, in 256-colour and
truecolour, with a colour left in force, with a scroll region, in origin mode,
with a full-screen program caught mid-draw, still producing, having outrun the
ring while hidden, having outrun it inside the alternate screen, and having been
resized while hidden.

`TestWireCarriesTheWholeCell` (`internal/session/wire_fidelity_test.go`) proves
the *wire*, and it exists because the matrix structurally cannot. The matrix
reads the daemon through the same serialization it checks the client against, so
anything that serialization cannot express is lost identically on both sides and
compares equal. That is exactly how a snapshot that turned every palette colour
into a fixed RGB passed all 32 cases while repainting the user's screen. This
test feeds a guest's output to one emulator, takes it through the wire into a
second, and compares the two emulators to each other, with no wire in the middle
of the comparison.

`e2e/tui/session_switch_fidelity_test.go` is the third rung: a real client in a
real terminal, switched away and back, read for what it actually painted.

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

| Route | Client emulator | Mechanism | Resume position |
|---|---|---|---|
| First attach | fresh | M2 | the snapshot's `Seq` |
| Reattach after detach | fresh | M2 | the snapshot's `Seq` |
| Session switch | fresh | M2 | the snapshot's `Seq` |
| Daemon restart with restore | fresh | M2 | the snapshot's `Seq`, and a respawned shell has no history to carry |
| Workspace switch | **surviving** | M1 | the snapshot's `Seq` |
| Pane created by another client | fresh | M1 | the snapshot's `Seq` |
| Second client attaching to a live session | fresh | M2 | the snapshot's `Seq` |

The expectation going in was that the invariant is the same for all routes and
only the implementations differ. That was half right. The invariant is the same,
but the two implementations were not merely spelled differently: **both applied
the snapshot and then the stream to the same emulator**, and the stream they
applied was history the snapshot had already accounted for. The routes differed
only in how much history got painted twice.

## Why the snapshot and the stream cannot both be applied

The snapshot is the daemon emulator's state after consuming bytes `0..S`. The
ring replay used to hand the client bytes `R..S` for some `R <= S`. Writing those
bytes into an emulator that already holds the state at `S` re-runs output the
client already has.

The failure was not cosmetic overdraw, because the snapshot restored cells
without restoring the cursor: the replayed bytes were written from wherever the
client's own emulator had been left. Three shapes followed, and the matrix in
`internal/app/rehydration_matrix_test.go` reproduced all three:

- `R == S` (pane idle while hidden): nothing replayed, the blit was the whole
  answer, and the cursor was left stale.
- `R < S` (pane produced while hidden): the delta was painted a second time from
  a stale cursor. This is the stacked-prompt symptom, and it survived the resume
  fix because that fix only shrank the replay from the whole ring to the delta.
- `R == 0` with a non-empty ring (every M2 route): the whole ring was replayed
  over the blit, so the scrollback gained up to 64KB of duplicated history.

The rule now is that a route uses the snapshot, and the stream resumes where the
snapshot ends. Nothing is applied twice because nothing overlaps.

Two things had to be true for that rule to hold, and neither was:

- The daemon's emulator dropped a chunk whenever its feed queue was full, on the
  grounds that the client's emulator was what rendered. It is the authority on
  every route here, so it now blocks instead.
- A chunk appended between a subscriber's catch-up being copied and the
  broadcast that was waiting on the subscriber lock landed in both, so a pane
  shown while it was producing came back with the line at the seam duplicated.
  A subscriber the chunk is already behind is now skipped.

## What is authoritative

- The **daemon emulator** is authoritative for grid, cursor, pen, modes, margins,
  character sets and scrollback. It is the only thing that has seen every byte,
  which is why its feed blocks rather than dropping a chunk when it falls behind.
- The **ring** is authoritative for nothing. Its only job is to bridge the gap
  between a snapshot being taken and the subscribe that follows it.
- A client emulator that has been through `Close()` holds nothing, and no resume
  position may be claimed for it.
- Output already queued for a client's emulator belongs to the subscription it
  came from. A restore discards it, because it is older than the snapshot and
  applying it afterwards paints it twice.

## What the wire carries, and who reads it

- **`TerminalState.Scrollback` is what carries a pane's history.** It had no
  reader at all: up to 1000 rows of `CellState` per pane per fetch, serialized by
  the daemon and dropped by the client, while the history a pane came back with
  was whatever the ring replay happened to redraw. It is now seeded into the
  client's emulator, which is what makes history survive a route that builds the
  pane on a new emulator. A pane whose emulator survived keeps what it holds and
  is handed only the lines that scrolled off while it was away: the daemon sends
  a bounded window and a client keeps far more, so replacing the buffer would cut
  a long history down to the size of that window on every workspace switch.
- **The cursor is restored.** It was serialized and never applied.
- **The request's own fields are honoured.** `IncludeScrollback` was written by
  the client and never read; `MaxScrollbackLines` was never written or read; and
  the 1000-row cap kept the *oldest* rows, so a pane with a long history would
  have been handed its most ancient screenfuls had anyone read them.
- **Scroll position is not on the wire, by design.** `Window.ScrollbackOffset`
  and `CopyMode.ScrollOffset` are where *this viewer* is looking, not properties
  of the pane; putting them on the wire would drag a second client watching the
  same pane to wherever the first one scrolled. They survive a workspace switch,
  where the window object survives, and are lost on every route that rebuilds
  windows. Recovering that means anchoring to a scrollback line rather than to a
  distance from the bottom, because a pane that produced output while the client
  was away has moved under the offset.
- **Alt-screen content is restored like any other.** It used to be skipped, on
  the grounds that a resize would make vim or htop repaint itself. That asks the
  guest to do the client's job, and it only ever looked correct because the ring
  replay was redrawing the pane underneath it.
- **A colour keeps its encoding.** `CellState` flattened every colour through
  `RGBA()` into a hex string and read it back as a fixed RGB. The render path
  branches on exactly that: a palette entry is re-emitted as a palette entry and
  follows the user's terminal theme, and an RGB does not. So `31m` red arrived as
  the maroon the default palette resolves red to, and a pane came back repainted
  in shades the user never chose. `colorToWire` keeps the three encodings apart,
  and `colorFromWire` resolves a palette entry through the emulator that will
  hold it, so a restored cell is coloured by the same rule as a cell the guest
  writes live into that emulator.
- **A cell's attributes travel as the emulator's own bitmask.** Spelling them out
  one bool at a time is what left blink, conceal and strikethrough off the wire
  entirely, collapsed the five underline styles into a plain underline, and
  dropped the underline colour and the OSC 8 hyperlink.
- **The pen is carried.** A guest sets a rendition and everything written next
  inherits it. Without it the stream resuming on top of a restore was painted in
  whatever the client's emulator was left holding: default on a pane rebuilt from
  nothing, stale on one that survived a workspace switch. This is the half of the
  colour report that shows up on new output rather than on restored content,
  which is why it looked random.
- **The scroll region is carried, and only when a guest set one.** A region that
  is simply the whole screen says nothing, and sending it pinned a pane that had
  been resized since to whatever size the daemon was when the snapshot was taken.
  When none is on the wire the client resets to its own bounds, so a pane that
  had margins before the route does not keep them after it.
- **The character set selection is carried.** `ESC ( 0` selects the DEC
  line-drawing set once and every box character after it travels as an ASCII
  letter, so a client that came back with G0 at US ASCII drew `qqqq` where the
  guest drew a horizontal rule. The sets are maps and cannot be compared back to
  the set they came from, so the emulator records the designator byte each was
  selected by.
- **The normal screen is carried while the alternate one is active.** It is the
  shell's screen under a running full-screen program. Only the screen the guest
  was drawing into was sent, so a pane with vim open across a route came back
  correct and went blank the moment vim exited. The alternate screen needs no
  equivalent: entering it clears it, so what it held before is never seen again.
- **Leaving the alternate screen is applied like entering it.** Only entering
  was, so a pane whose emulator survived a workspace switch and whose guest quit
  while the pane was hidden stayed pointed at the alternate buffer while its
  modes said it had left. The blit landed in the buffer nobody was looking at,
  and everything the pane printed next scrolled into the alternate screen's
  scrollback, which is switched off.

## Known and not fixed

`TUIClient.ResizePTY` is fire-and-forget. `primePaneFromDaemon` announces a
pane's size and re-fetches the snapshot immediately, and its comment says the
resize happens before the snapshot is taken for real; nothing makes that true.
A pane resized while hidden, whose guest does not redraw on SIGWINCH, can come
back holding the pre-resize screen. Shrinking an alternate screen destroys its
bottom rows on both sides, so the two copies can also disagree about a resize
they saw in different orders. Reproducing it needs a guest that does not repaint,
which is why it is stated here rather than asserted in the matrix: the shape that
provokes it cannot tell that bug from ordinary resize semantics.

## What the wire still does not carry

Known and deliberate, so the next person does not have to rediscover them: the
saved cursor (DECSC), tab stops, the window title, the guest's OSC 4/10/11/12
colour overrides, the pending-wrap latch, and the ANSI (non-DEC) modes, which
`GetModes` drops because they share an int keyspace with the DEC modes. Insert
mode (IRM) and reverse video (DECSCNM) are in that last group and are not
implemented by the emulator at all, so nothing is lost by not carrying them.

## Sizes

**A snapshot is applied whole.** The emulator is grown to fit what it is given,
because writing a cell outside a grid is a no-op and half a snapshot is never
right. A window's emulator is built at `height - 2`, a border allowance
hardcoded while the window is not yet marked tiled; the tiler then marks it
tiled and takes that allowance back out of the content height, so the snapshot
arrives two rows taller than the grid. Those two rows were dropped silently, and
the resize that grows the grid back is silent too, because the announced size was
seeded from the daemon and so nothing tells the guest to redraw. An editor came
back without its last line or its status line and stayed that way. It is grown
and never shrunk: how much room a pane has is the client's layout to decide and
it resizes the emulator on the next pass regardless, so growing is transient
where dropping content is permanent.

A pane can be resized while it is hidden, by another client or by the daemon.
`Window.Resize` measures against what this client last announced, so it cannot
see that, and the pane came back with the client's emulator at one size and the
shell at another. The snapshot carries the daemon's real size, so priming a pane
seeds the announcement record from it and reconciles before the snapshot it will
restore from is taken.

## The ring's size

64KB, unchanged, but it is now sized for a different job. It used to have to
cover a whole workspace switch, which is why a pane that outran it left a hole
and needed the screen cleared in front of the catch-up. It now only has to cover
the gap between a snapshot being read and the subscribe that follows, which is
one round trip on a unix socket. A pane would have to sustain tens of megabytes
a second to outrun it. There is no reason to grow it, and shrinking it buys back
64KB per pane for no benefit worth the churn.
