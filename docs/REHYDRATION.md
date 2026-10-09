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
alternate one is active, cursor position, the cursor shape, the pen, DEC modes,
the scroll region, the character set selection, the kitty keyboard stack, the
modifyOtherKeys level, the alternate-screen flag, the cells DECSCA protected,
the cursor DECSC saved on each screen, the character REP repeats and up to 1000
scrollback rows.
`ApplyTerminalState` reads it back, and
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
3. **Cursor.** The client's cursor is at the daemon's cursor, in the shape the
   guest asked for.
4. **Modes.** Alternate-screen flag, DEC modes, the kitty keyboard stack and the
   modifyOtherKeys level match.
5. **No duplication.** Content the pane produced once appears once.
6. **What paints the next byte.** The pen with its hyperlink and its DECSCA
   protection, the scroll region, the character set selection, the cells
   DECSCA protected, the cursor DECSC saved and the character REP repeats
   match. None of these can be read back off the cells, and each decides how
   output that has not arrived yet is painted, where it lands, which glyphs it
   draws and what a selective erase keeps.
7. **The screen underneath.** While the alternate screen is active, the normal
   screen matches too. It is what quitting the guest's program puts back on
   display.

Invariant 5 is not implied by 1-4 read loosely; it is the one the first round of
known bugs all broke, so it is stated separately. Invariants 6 and 7 are the
second round: a pane can satisfy 1-5 exactly and still be wrong the moment it
prints its next line or the guest quits.

Invariant 2 is a floor and not the whole rule. It permits a route to throw the
client's own history away and keep only what the daemon sent, because a shorter
suffix is still a suffix. The stronger rule is:

8. **A surviving emulator's history only ever gets longer.** A route that finds
   an emulator that already holds history hands it the lines that scrolled off
   while it was away, and never replaces what it has. The daemon sends a bounded
   window of its scrollback and a client keeps far more than that, so replacing
   the buffer would cut a long history down to the size of that window on every
   workspace switch.

`ApplyTerminalState` has said this in a comment for as long as its incremental
branch has existed. It is written here because the ghostty backend broke it and
invariant 2 alone did not call that a bug: the backend's synthesized restore
started from a hard reset, which drops the library's history, so the tail the
daemon sent became the whole history (issue #146). The synthesis now starts from
a hard reset only when the emulator holds no history; an emulator that holds
history is cleared and extended. `TestGhosttyWireAgain*`
(`internal/session/ghostty_wire_test.go`) asserts this on every pairing of the
two backends, in the shape a workspace switch takes: the destination is brought
level with the source, the source moves on alone, and a second snapshot carries
only the rows the destination lacks.

Two more tests assert the rest, and they are not redundant.

`TestRehydrationMatrix` (`internal/app/rehydration_matrix_test.go`) proves a
*route*. It runs a real daemon in process and takes a pane through every route
below crossed with every shape a pane can be in: at the live tail, scrolled back,
in the alternate screen, holding wide runes, under heavy SGR, in 256-colour and
truecolour, with a colour left in force, with a scroll region, in origin mode,
with a full-screen program caught mid-draw, still producing, having outrun the
ring while hidden, having outrun it inside the alternate screen, having been
resized while hidden, and being resized while producing.

`TestWireCarriesTheWholeCell` (`internal/session/wire_fidelity_test.go`) proves
the *wire*, and it exists because the matrix structurally cannot. The matrix
reads the daemon through the same serialization it checks the client against, so
anything that serialization cannot express is lost identically on both sides and
compares equal. That is exactly how a snapshot that turned every palette colour
into a fixed RGB passed all 32 cases while repainting the user's screen. This
test feeds a guest's output to one emulator, takes it through the wire into a
second, and compares the two emulators to each other, with no wire in the middle
of the comparison.

`TestWireCarriesTheCursorState` (`internal/session/cursor_state_wire_test.go`)
is the wire test for the state no cell shows. A restore of that state looks
right until the guest sends the sequence that reads it, so each case restores
a snapshot, feeds both emulators that sequence (REP, DECRC, a selective erase,
leaving the alternate screen), and compares them after it. It runs on both
wire forms, and `TestGhosttyWireCarriesTheCursorState` runs it across the two
backends in each direction.

`e2e/tui/session_switch_fidelity_test.go` is the third rung: a real client in a
real terminal, switched away and back, read for what it actually painted.

Two targeted tests sit beside the matrix, for the seams its shapes structurally
cannot hold. `TestResizeSeamStaysClosed` resizes a pane that produces little
enough that every row it ever made is still held on both sides, so a line laid
out at a width the other side never had is a row the comparison reads rather
than one it lost to eviction; the resized-while-producing shape floods too many
rows to keep its own seam. `TestSaturatedSwitchNoResize` takes a pane already
at its scrollback cap through a workspace switch with no resize anywhere,
which is the shape that found the restore discarding more history than the
snapshot carries.

## The routes

Seven routes reach a pane. They collapse into exactly two client-side
mechanisms.

**M1: `primePaneFromDaemon`.** Snapshot, then subscribe with whatever
`ptyResume` the daemon still holds for this pane.

**M2: `RestoreTerminalStates` then `SetupPTYOutputHandlers`.** Snapshot for
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

### stream-pane: a client that is not tuios

`stream-pane` (`internal/session/verb_stream_pane.go`, docs/protocol.md) is an
eighth route, for a client that cannot read a `TerminalState`. It keeps the
same pairing on the daemon side. The snapshot is `GetTerminalState`, turned
into bytes by `snapshotVT`, and its `Seq` is the position the subscription
starts at. A subscription that cannot start exactly there, because the ring
rolled past it, is dropped and the snapshot is taken again
(`PTY.subscribeAt`). Every output frame carries the position after its last
byte, and the stream skips bytes it already sent, so a catch-up that overlaps
the snapshot paints nothing twice. A client that reconnects with the position
it reached resumes from the ring, without a snapshot, when the ring still
holds that position.

`snapshotVT` reproduces invariants 1 to 4 and 7 on any xterm-compatible
emulator. Of invariant 6 it carries the pen, the scroll region's top and
bottom and the character sets, and not the protected cells, the saved cursor
or the character REP repeats.

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
- Output already queued for a client's emulator is applied before the pane is
  primed (`Window.DrainPendingOutput`), not discarded. It is older than the
  snapshot, but the snapshot carries a bounded scrollback window, and the queue
  of a pane that outpaced its client holds history the snapshot does not bring
  back: discarding it froze the client's scrollback at wherever its emulator
  had got to, with a silent hole from there to the snapshot's window. The pane
  is unsubscribed by the time it is primed, so the queue is finite and the
  drain returns. The restore's epoch bump still discards anything that races
  in after the drain, which is what keeps the blit from painting over live
  output.

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
- **The cursor shape is carried.** A shell's prompt or an editor changing mode
  sets it once with DECSCUSR and never repeats it, so it is long out of the
  output buffer's reach by the time anyone reattaches. Without it every restored
  pane came back as a block whatever the guest asked for. It travels as the
  DECSCUSR parameter the guest would have sent, so zero means the snapshot does
  not say and the client leaves the pane on its default rather than forcing a
  blinking block onto it, which is what an older daemon's snapshot gets.
- **A pending wrap is carried.** A guest that prints into the last column leaves
  the cursor on that cell with a wrap pending, and the next character it prints
  starts the next row. The cursor position alone cannot say this. Without it a
  snapshot taken at that moment, such as a reattach that caught the shell
  echoing a command longer than the pane, restored a cursor that printed over
  the last cell, and every byte the stream delivered after it landed one column
  out. The libghostty backend takes no sequence that sets the flag, so its
  restore prints the last cell again. A snapshot from an older daemon has no
  flag, which reads as no wrap pending.
- **The pen's hyperlink is restored on the libghostty backend.** The pen
  carried its OSC 8 link on the wire already, but the libghostty backend did
  not read it when it took a snapshot and did not write it when it restored
  one. Text the guest printed after a reattach inside an open link was not a
  link. The library exposes the pen's link only through a formatter that
  emits it after a cell, so the backend reads it from one, and its restore
  sends the OSC 8 after the pen.
- **DECSCA protection is carried.** A guest that sends DECSCA 1 protects
  what it prints next from a selective erase (DECSED, DECSEL), and a plain
  erase still clears it. `PenProtected` is the pen's protection and
  `Protected` the protected cells, as runs of row, column and length;
  `MainProtected` is the screen under an active alternate one. The pure
  emulator did not implement DECSCA at all, so a selective erase cleared
  cells the guest protected, and a pure client restored from a libghostty
  daemon lost every protected cell to the next DECSED. It now keeps the
  protection beside the cells (`grid.prot`), and moves it with them through
  scrolls, line and character inserts and deletes. A reflow drops it. The
  libghostty backend reads it off the library's cells, and its restore sends
  DECSCA around the cells that have it. Protection is replaced whole on every
  route, so a snapshot from an older daemon, which sends none, leaves no cell
  protected. The wire says only whether a cell is protected, not by which
  form. On libghostty, ISO protection (SPA, ESC V) also protects a cell, and
  it also makes ED, EL and ECH keep the cell. A libghostty client restores
  such a cell with DECSCA, which is DEC protection, so after a reattach a
  plain erase clears it where the daemon keeps it. The pure emulator
  implements neither SPA nor EPA.
- **The character REP repeats is carried.** `CSI b` repeats the last
  character the guest printed, as it sent it, before a character set maps
  it. A client restored without it printed nothing for the next REP, or the
  wrong character. `LastPrintedKnown` tells an empty one from an older daemon
  that says nothing, and against an older daemon a surviving emulator keeps
  its own. The library keeps the character where no query reaches it, so the
  libghostty backend reads it off the stream as the bytes go past, and its
  restore sets it the only way the library takes it: it prints the character
  in a blank cell and erases the cell again. A pending wrap prints the cell
  under the cursor last, so when that cell holds the character the reprint
  sets it instead, through the character set when the set maps it. A
  combining mark or a joiner joins the character before it and is not
  recorded, on either backend. A C1 control sent as UTF-8 (U+0080 to
  U+009F) is a control, and it is not recorded either. The print and erase
  needs a row with a blank cell in column 0 that does not wrap. When every
  row holds text there, a libghostty client keeps the character the restore
  printed last, which is the last cell it painted, and the next REP repeats
  that character.
- **The cursor DECSC saved is carried, on each screen.** It is the position,
  the pen, the pending wrap, origin mode, DECSCA and the character set
  selection, which DECRC, SCORC and 1048 put back. `SavedCursor` is the
  active screen's. `MainSavedCursor` is the main screen's while the
  alternate one is active: entering the alternate screen with 1049 saved the
  shell's cursor there, and leaving puts it back. Without it a pane with vim
  open across a reattach put the shell's cursor at the top left when vim
  quit. The pure emulator did not restore the cursor when 1049 was reset,
  unlike xterm and libghostty, which also left the alternate screen's
  character sets in force after it; it restores it now. The library keeps
  the saved cursor where no query reaches it, so the libghostty backend
  records the cursor at each save as the stream goes past, and its restore
  puts the live cursor into the saved state and saves it, before the switch
  to the alternate screen for the main screen. That switch uses the mode the
  guest used: the library keeps a screen entered with 1049 marked as the
  alternate one after 1047 is reset, so a guest that entered with 1047 and
  left with 1047 after a reattach stayed on it. Each screen keeps its own
  saved character sets on the pure emulator too; they were held once for
  both, so a save on the alternate screen gave the program's sets to the
  shell when it left. A peer from before these fields sends neither, and the
  client keeps the saved cursor it has.
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
- **The cells travel packed when the client asks.** As `[][]CellState`, gob
  writes every cell as a struct, about thirteen bytes for a plain letter: a
  207x55 screen was 147 KB and a thousand rows of history 2.9 MB, per pane, and
  the client decoded every struct by reflection on its UI goroutine during a
  workspace switch. `GetTerminalStatePayload.Packed` asks for the same cells in
  the form `snapshot_pack.go` describes: a style table once, cells in one style
  run together, two bytes for a plain letter, blank row tails left off. The
  request field is what keeps it compatible in both directions. A daemon that
  predates it does not see the request and answers with cells, which the client
  still reads; a client that predates it never asks. `TUIClient.GetTerminalState`
  keeps the reply packed and only checks that every row decodes, so a malformed
  snapshot still fails the request. `ApplyTerminalState` walks the packed rows
  straight into the emulator, and packs a snapshot that arrived as cells first.
  A reader that wants `Screen` as cells, which is only test oracles, calls
  `Unpack`. The daemon packs a requested snapshot as it reads the rows
  (`GetTerminalStatePacked`), and `TestDirectPackMatchesPack` holds that to the
  same bytes as `GetTerminalState` followed by `Pack`.
  `TestWireCarriesTheWholeCell` runs every shape under both forms.
- **The soft-wrap flags are carried.** `ScreenWraps` and `ScrollbackWraps` are
  one bit per row: the active screen's rows, and the history rows the snapshot
  sends, in order. A set bit says the emulator wrapped that row onto the next;
  a row that is merely full ended with a newline. Writing cells does not touch
  the flags, so a pane whose emulator survived a workspace switch kept the
  flags of what its rows showed before, and hints and the link hover joined a
  row the snapshot ended to the row under it. `ApplyTerminalState` sets them
  last, through `RestoreSoftWraps`. A daemon from before the fields sends
  neither, which reads as no row wrapped. The ghostty backend reproduces each
  flag by typing the row out to the edge in its restore synthesis; the one it
  cannot reproduce is the newest history row carrying on into the screen,
  which it reads as ending. `TestApplyTerminalStateClearsStaleWraps` and
  `TestApplyTerminalStateCarriesWraps` run on both backends.
- **A snapshot is matched to the request it answers.** The client matched
  replies by message type, and the daemon answers a failed subscribe, which
  nothing waits for, with `MsgError`. So during a restore the error about pane
  A answered pane B's state request, and B's real snapshot then went to
  whichever request was waiting next, which painted B's screen into pane C. A
  reply after its request timed out did the same. A request the client waits
  for now carries a request id in the frame header (`Message.ReqID`, codec byte
  2), and the daemon puts the same id on everything it sends in answer,
  `MsgError` included. A tagged reply nothing waits for is dropped. A client
  tags only for a daemon whose welcome sets `RequestIDs`, and the daemon tags
  only the answer to a tagged request, so neither side writes a tagged frame to
  a peer that would misread it. Against an older daemon the client still
  matches by type, and drops a snapshot about any pane but the one it asked
  for. An error from such a daemon cannot be told apart, so the A-for-B error
  is still possible there. `TestSubscribeErrorDoesNotAnswerAStateRequest`,
  `TestLateReplyIsNotTakenByTheNextRequest` and
  `TestRequestIDsAcrossPeerVintages` hold this.

## A resize is a point in the stream

Where a line wraps is not a property of the line. It is decided once, by the
width the emulator had when it consumed the bytes, and never revisited: nothing
lays a scrollback line out again on either side. So two emulators fed the same
bytes hold the same history only if they change width at the same byte.

They did not. A client resized its own emulator the moment its layout asked, and
told the daemon over `TUIClient.ResizePTY`, which is fire-and-forget. Everything
the guest produced between the two was laid out by the client at the new width
and by the daemon at the old one. A line long enough to wrap at one width and not
the other went into the client's scrollback as two lines and the daemon's as one,
and stayed that way: `TestRehydrationMatrix/workspace-switch/scrolled-back` saw a
client holding 54 lines against the daemon's 53, and the extra line was the
second half of a wrapped command echo rather than a duplicate of anything.

A pane with a live subscription is now sized by that subscription:

- `PTY.Resize` does not touch the daemon's emulator. It queues the resize on
  `vtWriteChan`, behind the bytes already read, and broadcasts it to every
  subscriber under the same lock `readOutput` appends and broadcasts under. One
  lock is what puts it at one byte in both streams.
- The real PTY is resized straight away regardless, so the guest's SIGWINCH does
  not wait for the emulator to work through a backlog. The one exception is a
  burst: a size change within 30 ms of the last one written to the kernel, or
  within the first 250 ms of the pane's life, is held and written once, as the
  burst's last size, when that period ends (`internal/session/pty_winsize.go`).
  The first size change is never held, and input written to the pane writes a
  held size first, so a guest never reads its size after a keystroke and gets
  the one from before a resize. The emulator and the subscribers are not
  held; only the guest hears about a burst later. The reason is macOS
  `/bin/bash` 3.2, which a SIGWINCH during its startup can kill, and a new pane
  gets one resize per attached client in its first milliseconds.
- The daemon announces it as `MsgPTYResized`, which ends the output batch in
  front of it and travels on its own. Coalescing it into the bytes either side
  would put the client's emulator at the wrong width for one of them.
- The client queues it into the same channel its output goes through
  (`Window.ResizeFromStream`), so it is applied in the order it arrived.
- `Window.Resize` stays the one choke point for the announcement, `announcedW/H`
  and SIGWINCH. Only the emulator grid moved, and only while `StreamOwnsSize` is
  set. A pane with no subscription has no stream to be ordered against and is
  sized by the layout, as before.

Three ways a resize could go missing, all closed, because a resize is not
recoverable from the stream the way bytes are:

- `Subscribe` registered the subscriber inside the streaming goroutine, so a
  resize sent straight after a subscribe was broadcast to nobody. It is
  registered in the handler now, where the connection's message order decides.
- A subscribe states the size the emulator is at, behind the catch-up, for a
  resize that landed between a client's snapshot and its subscribe.
- A restore discards queued output, and a queued resize with it, so
  `primePaneFromDaemon` takes the emulator to the snapshot's own bounds. That is
  the only route back down for a streamed pane, since the layout no longer
  resizes it.

The ring holds bytes only, so a catch-up used to be one flat replay laid out at
one width even when the daemon had changed width inside the span. Every resize
now leaves a mark at its stream position (`resizeMarks`), pruned with the ring,
and a catch-up is cut at each mark inside it with the resize sent between the
segments, so the client lays each segment out at the width the daemon laid it
out at. A rolled client is started at the newest mark behind the ring, which is
the width the ring's first byte was laid out at.

A subscriber whose channel is full still drops one, the way it drops output.
A re-subscribe heals it now, because the catch-up carries the marks; until
then the pane keeps the width it had, which is the hole left in this.

A resize to the size an emulator already has is not a no-op inside it: it resets
the scroll region and the tab stops, which are the guest's. Both sides skip it.

`GetTerminalState` reports the emulator's own size rather than the pane's
announced one. They differ only while a resize is still behind output in the
stream, and a snapshot has to describe the grid it is serializing.

`Window.ResizeVisual`, the drag path, leaves a streamed pane's emulator at the
size the stream gave it, as `Window.Resize` does. The renderer clips or pads
the grid to the pane while the drag runs, and the emulator changes size when
the daemon's does, at the same byte. It used to resize the emulator at every
motion step. With reflow, that laid the client's screen and history out through
widths the daemon never had, and a drag that ended at the size it started at
told the daemon nothing, so the two stayed apart. The cost is the look of the
pane during a drag: its content does not rewrap until the drag ends.

## Known and not fixed

`primePaneFromDaemon` announces a pane's size and re-fetches the snapshot
immediately, and its comment says the resize happens before the snapshot is taken
for real; nothing makes that true. A pane resized while hidden, whose guest does
not redraw on SIGWINCH, can come back holding the pre-resize screen. Shrinking an
alternate screen destroys its bottom rows on both sides, so the two copies can
also disagree about a resize they saw in different orders. Reproducing it needs a
guest that does not repaint, which is why it is stated here rather than asserted
in the matrix: the shape that provokes it cannot tell that bug from ordinary
resize semantics.

`ApplyTerminalState` seeds a surviving emulator with the lines that scrolled
off while it was away, counted as the daemon's scrollback length minus the
client's. Both lengths saturate at the cap, so a pane already full on both
sides that produced while the client was away computes zero missing rows and
seeds nothing, and what scrolled off while it was away is absent from the
client's history. Counting cannot say more once either side is at its cap;
doing it right needs the wire to carry how many rows scrolled off since a
stream position.

## A pane on another machine after a dropped link

A pane whose process runs on another machine has no emulator there, so there
is no snapshot to fall back to when its link drops: the machine running the
process keeps a stream, not a screen. It keeps a 64 KB ring of the process's
output and a count of every byte, the same shape as the PTY ring above, and the
daemon holding the window counts every byte it has read. On a reattach the
count goes back, and the ring is replayed from it, so the window's emulator
takes each byte once and in order. The window's own emulator, scrollback and
subscribers never see the drop, only a pause.

When more was written during the drop than the ring holds, the whole ring is
written and the bytes before it are gone. The emulator then applies a stream
with a hole in it, which is exactly the double-application problem's opposite
and just as wrong for a screen that was drawn with cursor movement. The daemon
holding the window resizes the pane a row and back after such a reattach, so a
program that repaints on SIGWINCH draws its screen whole again. A shell prompt
is not repainted, and what scrolled by during the hole is not in the
scrollback. See [SESSIONS.md](SESSIONS.md#limits).

## What the wire still does not carry

Known and deliberate, so the next person does not have to rediscover them: tab
stops, the window title, the guest's OSC 4/10/11/12 colour overrides, and the
ANSI (non-DEC) modes, which
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
to fit: how much room a pane has is the client's layout to decide and it
resizes the emulator on the next pass regardless, so growing is transient
where dropping content is permanent.

A client bigger than the snapshot is brought down to the snapshot's size. The
snapshot's rows, and the stream that resumes on top of it, were laid out at
that size until the stream says otherwise, so the client has to wrap and
scroll where the daemon does. Left wider, a wrap pending at the snapshot's
last column was not at the client's margin: the pure client kept it pending
one column short of its edge, the libghostty client's reprint moved the cursor
one column right, and the next character and every line that reached the edge
after it landed somewhere the daemon did not put it. Left taller, a line feed
on the snapshot's last row moved the client's cursor down where the daemon
scrolled. `primePaneFromDaemon` and `RestoreTerminalStates` both size the
emulator to the snapshot before they apply it, and `ApplyTerminalState` does
it for any other caller. `TestWireNarrowsAWiderClient` holds this for a wider,
a taller and a bigger client, on both backends.

A pane can be resized while it is hidden, by another client or by the daemon.
`Window.Resize` measures against what this client last announced, so it cannot
see that, and the pane came back with the client's emulator at one size and the
shell at another. The snapshot carries the daemon's real size, so priming a pane
seeds the announcement record from it and reconciles before the snapshot it will
restore from is taken.

## Where the history should come from next

A client still holds a full second copy of every pane's scrollback, seeded on
attach and topped up on every switch, and the daemon holds the first. That is
accepted for now because it is what lets the pane scroll without a round trip.
The shape that removes it is to fetch history on scroll: the snapshot carries
the screen and the count of rows behind it, the client's emulator keeps only
what has scrolled off since it was primed, and a scroll that reaches past what
it holds asks the daemon for a page of rows above a given position. The packed
form is what makes such a page cheap, and the `have` field is already the
client telling the daemon how much it holds; the missing piece is a request
that names a row range instead of a count, and a scrollback anchor the client
can hold across a page arriving. Nothing here is built yet.

## The ring's size

64KB, unchanged, but it is now sized for a different job. It used to have to
cover a whole workspace switch, which is why a pane that outran it left a hole
and needed the screen cleared in front of the catch-up. It now only has to cover
the gap between a snapshot being read and the subscribe that follows, which is
one round trip on a unix socket. A pane would have to sustain tens of megabytes
a second to outrun it. There is no reason to grow it, and shrinking it buys back
64KB per pane for no benefit worth the churn.
