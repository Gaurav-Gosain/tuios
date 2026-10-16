package snapshot

import (
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// DefaultScrollback is how many scrollback lines a state request carries
// when it does not ask for a number.
const DefaultScrollback = 1000

// Capture serializes everything a client needs to arrive at the picture
// this emulator holds. It is one half of the wire contract, and Apply is the
// other. docs/REHYDRATION.md in the tuios repository states the contract.
//
// Width and height are the pane's, which the caller knows. The emulator's own
// size lags a resize the program has not acknowledged yet.
//
// maxScrollback bounds the scrollback rows the state carries: zero means
// DefaultScrollback and a negative number means none. The rows carried are
// the newest ones. have is how many scrollback rows the receiving emulator
// already holds. Only the rows past it are serialized, because only those can
// be used. ScrollbackLen in the state is always the true length.
func Capture(t vt.Terminal, width, height, maxScrollback, have int) *State {
	state, finish := capture(t, width, height, maxScrollback, have, false)
	finish()
	return state
}

// CapturePacked is Capture with the cells packed as they are read: the
// result is the same as Capture followed by Pack, without building every cell
// first. It returns in two halves. It reads the emulator and returns the state
// with finish, which completes it without reading the emulator again, so a
// caller can release the emulator's lock before it calls finish. The state is
// complete only once finish has run.
func CapturePacked(t vt.Terminal, width, height, maxScrollback, have int) (*State, func()) {
	return capture(t, width, height, maxScrollback, have, true)
}

// capture is Capture and CapturePacked.
func capture(t vt.Terminal, width, height, maxScrollback, have int, packed bool) (*State, func()) {
	state := &State{
		Width:         width,
		Height:        height,
		CursorX:       t.CursorPosition().X,
		CursorY:       t.CursorPosition().Y,
		PendingWrap:   t.CursorPendingWrap(),
		ScrollbackLen: t.ScrollbackLen(),
		IsAltScreen:   t.IsAltScreen(),        // Capture alt screen state for mouse event forwarding
		Modes:         t.GetModes(),           // Capture terminal modes (mouse tracking, bracketed paste, etc.)
		KittyKbdStack: t.KittyKeyboardStack(), // Capture kitty keyboard protocol flag stack
		// The main screen's stack, under a program on the alternate one.
		KittyKbdMainStack:   t.KittyKeyboardMainStack(),
		KittyKbdUnavailable: t.KittyKeyboardUnavailable(),
		KittyImages:         t.KittyGuestImagesState(),
		// Set once by an editor at start, like the kitty flags, so it
		// cannot be recovered from the output buffer either.
		ModifyOtherKeys:      t.ModifyOtherKeys(),
		ModifyOtherKeysKnown: true,
	}

	// None of these is recoverable from the cells. They are what the guest set
	// and has not reset, and they decide how the output that has not arrived
	// yet is painted, where it lands, and which glyphs it draws.
	pen, link := t.CursorPen()
	ps := styleToWire(pen, link)
	state.Pen = &ps

	// Carried only when the guest set one. A region that is simply the whole
	// screen says nothing, and sending it pins a client that has been resized
	// since to whatever size this pane was when the snapshot was taken.
	if m := t.ScrollRegion(); m != t.Bounds() {
		state.Margins = []int{m.Min.X, m.Min.Y, m.Dx(), m.Dy()}
	}

	ids, gl, gr := t.Charsets()
	state.Charsets = []int{int(ids[0]), int(ids[1]), int(ids[2]), int(ids[3]), gl, gr}

	// What DECSCA protects, which only a selective erase reads, and the
	// character REP repeats. Neither shows in a cell until the guest sends
	// the sequence that uses it.
	state.PenProtected = t.CursorProtected()
	state.Protected = runsToWire(t.ProtectedCells(false))
	if state.IsAltScreen {
		state.MainProtected = runsToWire(t.ProtectedCells(true))
	}
	state.LastPrinted = t.LastPrinted()
	state.LastPrintedKnown = true

	// The cursor DECSC saved, which DECRC puts back. A shell's screen under a
	// full-screen program has one too: entering the alternate screen with
	// 1049 saved it, and leaving puts the shell's cursor back from it.
	state.SavedCursor = savedCursorToWire(t.SavedCursor(false))
	if state.IsAltScreen {
		state.MainSavedCursor = savedCursorToWire(t.SavedCursor(true))
	}

	// The cursor shape is set once, by a shell's prompt or by an editor
	// changing mode, and is long out of the output buffer's reach by the time
	// anyone reattaches. Without it here a pane that asked for a bar comes back
	// as a block, because that is what a fresh emulator is.
	state.CursorShape = decscusrParam(t.CursorStyle())

	// Set once and read later, like the modes: the tab stops a program
	// lines its columns up on, the title stack a program pops when it
	// quits, the colours it set for its palette, and insert and newline
	// mode.
	state.TabStops = tabStopBits(t.TabStops(), t.Width())
	titlesToWire(state, t.Titles())
	state.GuestColors = guestColorsToWire(t.GuestColors())
	state.ANSIModes = t.ANSIModes()

	// The sequence the parser is in the middle of. Its end arrives after Seq
	// on the stream, and a client that had not seen its start would print
	// that end as text.
	state.PendingInput = t.PendingInput()
	if oc, ok := t.(openClusters); ok {
		if c, open := oc.OpenCluster(); open {
			state.OpenCluster = &OpenClusterState{Text: c.Text, X: c.X, Y: c.Y, Width: c.Width, Left: c.Left, Right: c.Right}
		}
	}

	// Colours are encoded through one cache for the whole snapshot: a
	// truecolor cell's hex string was one allocation per cell, eleven
	// thousand per screen, for what is usually a few dozen distinct colours.
	colors := colorWireCache{}
	first, end := scrollbackWindow(t.ScrollbackLen(), maxScrollback, have)
	finish := func() {}
	if packed {
		finish = packStateCells(t, state, colors, first, end)
	} else {
		stateCells(t, state, colors, first, end)
	}
	screen := make([]rowFlags, state.Height)
	for y := range screen {
		screen[y] = screenRowFlags(t, y)
	}
	state.ScreenWraps, state.ScreenPads = rowFlagBits(screen)
	return state, finish
}

// rowFlags is one row's soft-wrap and padding flags (vt.Terminal's
// RowSoftWrapped and RowPadded). A capture collects them together, and the
// wire carries each as a bitset of its own, split at encode time
// (rowFlagBits).
type rowFlags uint8

const (
	rowWrapped rowFlags = 1 << iota
	rowPadded
)

// newRowFlags is the flags of a row that is wrapped and padded as said.
func newRowFlags(wrapped, padded bool) rowFlags {
	var f rowFlags
	if wrapped {
		f |= rowWrapped
	}
	if padded {
		f |= rowPadded
	}
	return f
}

// screenRowFlags is the flags of screen row y.
func screenRowFlags(t vt.Terminal, y int) rowFlags {
	w, _ := t.RowSoftWrapped(y)
	return newRowFlags(w, t.RowPadded(y))
}

// historyRowFlags is the flags of scrollback row i. The capture loops append
// them as they send each row, so entry n is the n-th row sent.
func historyRowFlags(t vt.Terminal, i int) rowFlags {
	w, _ := t.ScrollbackSoftWrapped(i)
	return newRowFlags(w, t.ScrollbackPadded(i))
}

// rowFlagBits splits flags into the wire's wrap and pad bitsets, each as
// wrapBits builds it: nil when no row has the flag.
func rowFlagBits(flags []rowFlags) (wraps, pads []byte) {
	for i, f := range flags {
		if f&rowWrapped != 0 {
			if wraps == nil {
				wraps = make([]byte, (len(flags)+7)/8)
			}
			wraps[i/8] |= 1 << (i % 8)
		}
		if f&rowPadded != 0 {
			if pads == nil {
				pads = make([]byte, (len(flags)+7)/8)
			}
			pads[i/8] |= 1 << (i % 8)
		}
	}
	return wraps, pads
}

// scrollbackWindow returns the scrollback rows [first, end) a snapshot carries.
// maxScrollback is as GetTerminalState documents it.
//
// Rows the caller already holds (have) are rows it will discard on arrival: it
// keeps its own history and merges only what scrolled off while it was away.
// Sending them anyway is what made a workspace switch move megabytes per pane
// to be thrown away at the far end. state.ScrollbackLen is still the true
// length, which is what the caller subtracts against.
func scrollbackWindow(scrollbackLen, maxScrollback, have int) (first, end int) {
	if maxScrollback == 0 {
		maxScrollback = DefaultScrollback
	}
	want := max(scrollbackLen-have, 0)
	if maxScrollback >= 0 && want < maxScrollback {
		maxScrollback = want
	}
	if maxScrollback < 0 {
		first = scrollbackLen
	} else if scrollbackLen > maxScrollback {
		first = scrollbackLen - maxScrollback
	}
	return first, scrollbackLen
}

// stateCells captures the screen, the main screen under an alternate one, and
// scrollback rows [first, end) as cells.
func stateCells(t vt.Terminal, state *State, colors colorWireCache, first, end int) {
	width, height := state.Width, state.Height
	// Capture visible screen with full styling. The rows share one backing
	// array: gob writes each row by its own length, so the shape on the wire
	// is the same and the allocation count is one instead of one per row.
	state.Screen = make([][]CellState, height)
	cells := make([]CellState, width*height)
	for y := range height {
		state.Screen[y] = cells[y*width : (y+1)*width : (y+1)*width]
		for x := range width {
			cell := t.CellAt(x, y)
			if cell != nil {
				state.Screen[y][x] = colors.cellState(cell)
			}
		}
	}

	if state.IsAltScreen {
		state.MainScreen = make([][]CellState, height)
		cells := make([]CellState, width*height)
		for y := range height {
			state.MainScreen[y] = cells[y*width : (y+1)*width : (y+1)*width]
			for x := range width {
				if cell := t.MainCellAt(x, y); cell != nil {
					state.MainScreen[y][x] = colors.cellState(cell)
				}
			}
		}
	}

	// One backing array for the history too, cut into rows as they are read.
	// A row wider than the screen, from before a narrowing resize, is rare and
	// merely starts a new array.
	state.Scrollback = make([][]CellState, 0)
	var pool []CellState
	end = min(end, t.ScrollbackLen())
	t.ScrollbackRows(first, end, func(i int, line uv.Line) bool {
		if cap(pool) < len(line) {
			pool = make([]CellState, max(len(line), width*(end-i)))
		}
		row := pool[:len(line):len(line)]
		pool = pool[len(line):]
		for x := range line {
			row[x] = colors.cellState(&line[x])
		}
		state.Scrollback = append(state.Scrollback, row)
		return true
	})
	var flags []rowFlags
	for i := first; i < end; i++ {
		flags = append(flags, historyRowFlags(t, i))
	}
	state.ScrollbackWraps, state.ScrollbackPads = rowFlagBits(flags)
}

// packStateCells is stateCells followed by Pack, done a row at a time through
// one scratch row, so the cells never exist as [][]CellState. The grids are
// packed in the order Pack packs them (screen, scrollback, main screen), which
// is what makes the style table, and so every byte, the same as Pack's.
// TestDirectPackMatchesPack holds it to that.
//
// It packs the screen and copies what the rest needs from the emulator, and
// returns finish, which packs the history and the main screen from those
// copies without reading the emulator. The history is copied in its encoded
// form, a few bytes a cell, rather than decoded: 1000 rows of a 207-column
// pane read through ScrollbackLine were 23 MB of cells, and the line cache
// kept the last 256 of them.
func packStateCells(t vt.Terminal, state *State, colors colorWireCache, first, end int) (finish func()) {
	width, height := state.Width, state.Height
	p := newRowPacker()
	row := make([]CellState, width)
	readRow := func(at func(x, y int) *uv.Cell, y int, row []CellState) {
		for x := range width {
			if cell := at(x, y); cell != nil {
				row[x] = colors.cellState(cell)
			} else {
				row[x] = CellState{}
			}
		}
	}

	b := newPackedRows(height * 32)
	for y := range height {
		readRow(t.CellAt, y, row)
		b.add(p, row[:width])
	}
	state.PackedScreen = b.blob()

	end = min(end, t.ScrollbackLen())
	history := t.CopyScrollback(first, end)
	// The main screen under an alternate one is packed after the history,
	// so its cells are read now.
	var main []CellState
	if state.IsAltScreen {
		main = make([]CellState, width*height)
		for y := range height {
			readRow(t.MainCellAt, y, main[y*width:(y+1)*width])
		}
	}

	return func() {
		n := history.Len()
		b := newPackedRows(n * 32)
		flags := make([]rowFlags, n)
		history.Rows(0, n, func(i int, line uv.Line) bool {
			flags[i] = newRowFlags(history.Wrapped(i), history.Padded(i))
			if cap(row) < len(line) {
				row = make([]CellState, len(line))
			}
			r := row[:len(line)]
			// A history row is mostly blank tail, and the packer drops that
			// tail anyway, so only the cells before it are converted. The
			// rest are written as the blank the packer compares against,
			// which keeps the bytes identical to Pack's.
			used := usedCells(line)
			for x := range used {
				r[x] = colors.cellState(&line[x])
			}
			for x := used; x < len(r); x++ {
				r[x] = blankCellState
			}
			b.add(p, r)
			return true
		})
		state.PackedScrollback = b.blob()
		state.ScrollbackWraps, state.ScrollbackPads = rowFlagBits(flags)

		if main != nil {
			b := newPackedRows(height * 32)
			for y := range height {
				b.add(p, main[y*width:(y+1)*width])
			}
			state.PackedMain = b.blob()
		}
		state.Styles = p.styles
	}
}

// blankCellState is a never-written cell as the wire holds it: the cell the
// packer trims from a row's tail.
var blankCellState = CellState{Content: " ", Width: 1}

// usedCells is the length of a line without its blank tail: the cells that
// convert to blankCellState, a space of width one with no style and no link.
func usedCells(line uv.Line) int {
	n := len(line)
	for n > 0 {
		c := &line[n-1]
		if c.Content != " " || c.Width != 1 || c.Link != (uv.Link{}) ||
			c.Style.Fg != nil || c.Style.Bg != nil || c.Style.UnderlineColor != nil ||
			c.Style.Attrs != 0 || c.Style.Underline != 0 {
			break
		}
		n--
	}
	return n
}
