package snapshot

import (
	"errors"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// Apply brings an emulator to the state a snapshot describes. It
// is the reading half of the wire contract Capture writes, and it is
// the whole of what rehydration does to an emulator: the caller owns the
// locking, the window-level flags and the stream that resumes afterwards.
//
// The emulator may be fresh or may be one that survived a workspace switch and
// already holds most of this, so every step is written to be idempotent.
//
// A part of the snapshot that cannot be read is skipped and the rest is
// applied. The error names the parts that were skipped, and is nil when none
// was.
func Apply(t vt.Terminal, state *State) error {
	if t == nil || state == nil {
		return nil
	}
	var dropped []error
	// The cells are read in their packed form only (pack.go), which
	// is how the client asks for them. A snapshot that arrived as cells, from
	// a daemon older than the packed form, is packed here first. Each style in
	// the table is resolved for t once, rather than once per cell.
	state.Pack()
	styles := resolveStyles(t, state.Styles)

	// A snapshot too big for the emulator it is going into used to be taken
	// silently, because writing a cell outside the buffer is a no-op: every row
	// past the client's own height was dropped and the pane came back with its
	// bottom blank. An editor came back without the last line of the file or
	// its status line, which is the shape this was reported in, and it stayed
	// that way: the alternate screen keeps no scrollback to recover those rows
	// from, and the guest does not redraw a size it was never told changed.
	//
	// Grown to fit and never shrunk. How much room a pane has is the client's
	// layout to decide and it resizes this emulator on the next pass either
	// way, so growing is transient; dropping the content is not.
	if state.Width > t.Width() || state.Height > t.Height() {
		t.Resize(max(state.Width, t.Width()), max(state.Height, t.Height()))
	}
	// A client bigger than the snapshot is brought down to its size. The
	// snapshot's cells, and the stream that resumes on top of it, were laid
	// out at that size until the stream says otherwise (MsgPTYResized), so the
	// client has to wrap and scroll where the daemon does. Left wider, a
	// pending wrap at the snapshot's last column was not at the client's
	// margin: the next character went one column right instead of to the next
	// row, and every line after it wrapped somewhere else. Left taller, a line
	// feed on the snapshot's last row moved the cursor down instead of
	// scrolling. primePaneFromDaemon sizes the emulator to the snapshot before
	// it gets here; this covers every other caller.
	if state.Width > 0 && state.Height > 0 && (state.Width < t.Width() || state.Height < t.Height()) {
		t.Resize(state.Width, state.Height)
	}

	// Sending ESC[?1049h instead would clear the buffer it is switching to.
	//
	// Applied in both directions. Only entering was applied, so an emulator
	// that survived a workspace switch and whose guest had quit its full-screen
	// program while the pane was hidden stayed pointed at the alternate buffer,
	// and the shell's screen was blitted into the wrong one.
	t.RestoreAltScreenMode(state.IsAltScreen)

	// Modes come after the screen switch so the map lands on top of it. They
	// are what apps like vim and htop need to receive mouse events at all, and
	// the guest set them once, long out of the output buffer's reach.
	if len(state.Modes) > 0 {
		t.RestoreModes(state.Modes)
	}
	// Insert mode and newline mode, which Modes cannot carry. Absent from an
	// older daemon, and this emulator keeps its own.
	if state.ANSIModes != nil {
		t.RestoreANSIModes(state.ANSIModes)
	}
	// After the resizes above, because a resize to a new width resets the
	// table. Absent from an older daemon, and this emulator keeps its own.
	if len(state.TabStops) > 0 {
		t.RestoreTabStops(tabStopCols(state.TabStops))
	}
	if state.TitlesKnown {
		t.RestoreTitles(titlesFromWire(state))
	}

	// Kitty keyboard flags travel outside the DEC mode map and are set once by
	// the guest (CSI > u / CSI = u), so like the modes above they cannot be
	// recovered from the bounded output buffer. Without this a reattached
	// client encodes keys in legacy form for a pane that negotiated the
	// protocol.
	// Before the stacks, which a pane without the protocol does not take.
	if state.KittyKbdUnavailable {
		t.DisableKittyKeyboardProtocol()
	}
	t.RestoreKittyKeyboardState(state.KittyKbdStack)
	// Each screen has its own stack, and the one above is the alternate
	// screen's while a program runs there. Without the main one a client
	// that attached under the program left the shell's flags behind when it
	// quit.
	t.RestoreKittyKeyboardMainStack(state.KittyKbdMainStack)
	t.RestoreKittyGuestImages(state.KittyImages)
	// modifyOtherKeys is the other way a guest asks for keys the legacy
	// encoding cannot tell apart (vim sends CSI > 4 ; 2 m once at start). It
	// is applied when it is off too: an emulator that survived a workspace
	// switch still holds the level of an editor that quit while the pane was
	// hidden, and Ctrl+C then reached the shell as CSI 27 ; 5 ; 99 ~. An older
	// daemon reports nothing, and the level the emulator has stays.
	if state.ModifyOtherKeysKnown || state.ModifyOtherKeys > 0 {
		t.RestoreModifyOtherKeys(state.ModifyOtherKeys)
	}

	// The rendition the guest left in force, which paints everything that
	// arrives after this snapshot. Without it the stream resuming on top of a
	// restore was written in whatever colour this emulator happened to be left
	// in: default on a pane rebuilt from nothing, and stale on one that
	// survived. That is corruption on new output rather than on restored
	// content, which is why it looked random.
	if state.Pen != nil {
		t.RestoreCursorPen(styleFromWire(t, *state.Pen))
	}
	if len(state.Margins) == 4 {
		t.RestoreScrollRegion(uv.Rect(state.Margins[0], state.Margins[1], state.Margins[2], state.Margins[3]))
	} else {
		// No margins on the wire means the guest set none, so this emulator
		// scrolls its whole screen. Left alone, a pane that had margins before
		// the route would keep them after it.
		t.ResetScrollRegion()
	}
	if len(state.Charsets) == 6 {
		ids := [4]byte{
			byte(state.Charsets[0]), byte(state.Charsets[1]),
			byte(state.Charsets[2]), byte(state.Charsets[3]),
		}
		t.RestoreCharsets(ids, state.Charsets[4], state.Charsets[5])
	}
	if state.CursorShape > 0 {
		t.RestoreCursorStyle(decscusrStyle(state.CursorShape))
	}
	// DECSCA on the pen. An older daemon says nothing, which reads as off:
	// it could not report protection, and off is what a fresh emulator has.
	t.RestoreCursorProtected(state.PenProtected)
	// The character REP repeats. Taken only from a daemon that reports it,
	// so a surviving emulator keeps its own against an older one.
	if state.LastPrintedKnown {
		t.RestoreLastPrinted(state.LastPrinted)
	}
	// The cursors DECSC saved, after the alternate screen switch above, so
	// each lands on the screen it was saved on.
	if sc := state.SavedCursor; sc != nil {
		t.RestoreSavedCursor(false, savedCursorFromWire(t, *sc))
	}
	if sc := state.MainSavedCursor; sc != nil && state.IsAltScreen {
		t.RestoreSavedCursor(true, savedCursorFromWire(t, *sc))
	}

	// The scrollback goes back first, and it is the main screen's either way:
	// the alternate screen keeps none, and both sides read the same buffer.
	// Seeding it is what makes a pane's history survive a route that builds it
	// on a new emulator.
	//
	// A pane whose emulator survived keeps the history it already holds and is
	// only handed the lines that scrolled off while it was away. The daemon
	// sends a bounded window of its scrollback and a client keeps far more than
	// that, so replacing the whole buffer would cut a long history down to the
	// size of the window on every workspace switch.
	//
	// skip is how many of the rows sent are already held: none on an empty
	// emulator, and otherwise all but the last missing ones.
	skip := 0
	if have := t.ScrollbackLen(); have > 0 {
		missing := state.ScrollbackLen - have
		skip = packedRowCount(state.PackedScrollback) - max(missing, 0)
	}
	if err := walkPacked(state.PackedScrollback, len(styles), func(y int, row []packedCell) error {
		if y >= skip {
			// A line of its own for every row: the ghostty terminal keeps what
			// it is pushed until its next flush.
			t.PushScrollbackLine(packedLine(row, styles, make(uv.Line, len(row))))
		}
		return nil
	}); err != nil {
		dropped = append(dropped, fmt.Errorf("snapshot scrollback dropped: %w", err))
	}

	// The alternate screen is restored the same way as the normal one. It used
	// to be skipped, on the grounds that a resize would make vim or htop
	// repaint itself, which asks the guest to do the client's job: a program
	// that does not redraw on SIGWINCH, or one that is between frames, leaves
	// the pane blank.
	//
	// grid writes one packed screen through set and reports whether it held
	// any rows. The emulators copy what SetCell is handed, so one line serves
	// every row.
	var line uv.Line
	grid := func(blob []byte, set func(x, y int, c *uv.Cell)) bool {
		rows := false
		err := walkPacked(blob, len(styles), func(y int, row []packedCell) error {
			rows = true
			if y >= state.Height {
				return nil
			}
			if cap(line) < len(row) {
				line = make(uv.Line, len(row))
			}
			line = packedLine(row, styles, line[:len(row)])
			for x := 0; x < len(line) && x < state.Width; x++ {
				// A wide rune's continuation column is empty and is written by
				// SetCell from the lead cell's width, so skipping it is right.
				if line[x].Content == "" {
					continue
				}
				set(x, y, &line[x])
			}
			return nil
		})
		if err != nil {
			dropped = append(dropped, fmt.Errorf("snapshot cells dropped: %w", err))
		}
		return rows
	}
	if grid(state.PackedScreen, t.SetCell) {
		// The cursor was serialized and thrown away. Whatever came next was
		// written from wherever this client's emulator happened to be left,
		// which on a pane rebuilt from nothing is the top left corner.
		t.RestoreCursorPosition(state.CursorX, state.CursorY)
		t.RestoreCursorPendingWrap(state.PendingWrap)
	}

	// The shell's screen under a running full-screen program. Quitting the
	// program reveals it, and the client had never been sent it: a pane where
	// vim was open across a switch came back correct and went blank the moment
	// vim exited, because the buffer underneath had nothing in it.
	grid(state.PackedMain, t.SetMainCell)

	// Protection after the cells, because writing a cell leaves it
	// unprotected. It is replaced whole on every route: a surviving emulator
	// held protection for what its cells showed before, and a snapshot with
	// none, an older daemon's included, means no cell is protected.
	t.RestoreProtectedCells(false, runsFromWire(state.Protected))
	if state.IsAltScreen {
		t.RestoreProtectedCells(true, runsFromWire(state.MainProtected))
	}

	// The soft-wrap flags, last, because writing cells does not touch them.
	// A surviving emulator's rows held flags for what they showed before, and
	// left in place they joined a row the snapshot ends with a newline to the
	// row under it, which made one link out of two. The history flags cover
	// every row sent, the ones this emulator already held included, which is
	// what lines them up with the end of its history.
	t.RestoreSoftWraps(
		wrapFlags(state.ScreenWraps, state.Height),
		wrapFlags(state.ScrollbackWraps, packedRowCount(state.PackedScrollback)),
	)
	t.RestorePads(
		wrapFlags(state.ScreenPads, state.Height),
		wrapFlags(state.ScrollbackPads, packedRowCount(state.PackedScrollback)),
	)

	// The guest's colours after the cells, which were resolved against the
	// palette the cells were painted with: a cell the guest painted in a
	// slot before it set the slot keeps the slot, as it does on the daemon.
	// Absent from an older daemon, and this emulator keeps its own.
	if state.GuestColors != nil {
		t.RestoreGuestColors(guestColorsFromWire(t, state.GuestColors))
	}

	// Last, the sequence the daemon's parser was in the middle of, so the
	// stream that resumes at Seq finishes it. It goes in on every route:
	// with nothing pending, it still drops a sequence this emulator was
	// left in by a stream that stopped part way through one, which the
	// snapshot has replaced.
	pending := state.PendingInput
	if len(pending) > vt.MaxPendingInput {
		dropped = append(dropped, fmt.Errorf("snapshot pending input of %d bytes dropped", len(pending)))
		pending = nil
	}
	t.RestorePendingInput(pending)
	// After the pending input, which closes any open cluster.
	if c := state.OpenCluster; c != nil {
		if oc, ok := t.(openClusters); ok {
			oc.RestoreOpenCluster(vt.OpenCluster{Text: c.Text, X: c.X, Y: c.Y, Width: c.Width, Left: c.Left, Right: c.Right})
		}
	}
	return errors.Join(dropped...)
}

// wireStyle is one entry of a snapshot's style table, resolved for the
// emulator the snapshot is being applied to.
type wireStyle struct {
	style uv.Style
	link  uv.Link
}

// resolveStyles resolves a snapshot's style table for t.
func resolveStyles(t vt.Terminal, styles []StyleState) []wireStyle {
	out := make([]wireStyle, len(styles))
	for i, ss := range styles {
		out[i].style, out[i].link = styleFromWire(t, ss)
	}
	return out
}

// packedLine fills line, which is len(row) long, with one packed row.
func packedLine(row []packedCell, styles []wireStyle, line uv.Line) uv.Line {
	for x, pc := range row {
		ws := &styles[pc.style]
		line[x] = uv.Cell{Content: pc.content, Width: pc.width, Style: ws.style, Link: ws.link}
	}
	return line
}
