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

func capture(t vt.Terminal, width, height, maxScrollback, have int, packed bool) (*State, func()) {
	state := &State{
		Width:                width,
		Height:               height,
		CursorX:              t.CursorPosition().X,
		CursorY:              t.CursorPosition().Y,
		PendingWrap:          t.CursorPendingWrap(),
		ScrollbackLen:        t.ScrollbackLen(),
		IsAltScreen:          t.IsAltScreen(),
		Modes:                t.GetModes(),
		KittyKbdStack:        t.KittyKeyboardStack(),
		KittyKbdMainStack:    t.KittyKeyboardMainStack(),
		KittyKbdUnavailable:  t.KittyKeyboardUnavailable(),
		KittyImages:          t.KittyGuestImagesState(),
		ModifyOtherKeys:      t.ModifyOtherKeys(),
		ModifyOtherKeysKnown: true,
	}

	pen, link := t.CursorPen()
	ps := styleToWire(pen, link)
	state.Pen = &ps

	if m := t.ScrollRegion(); m != t.Bounds() {
		state.Margins = []int{m.Min.X, m.Min.Y, m.Dx(), m.Dy()}
	}

	ids, gl, gr := t.Charsets()
	state.Charsets = []int{int(ids[0]), int(ids[1]), int(ids[2]), int(ids[3]), gl, gr}

	state.PenProtected = t.CursorProtected()
	state.Protected = runsToWire(t.ProtectedCells(false))
	if state.IsAltScreen {
		state.MainProtected = runsToWire(t.ProtectedCells(true))
	}
	state.LastPrinted = t.LastPrinted()
	state.LastPrintedKnown = true

	state.SavedCursor = savedCursorToWire(t.SavedCursor(false))
	if state.IsAltScreen {
		state.MainSavedCursor = savedCursorToWire(t.SavedCursor(true))
	}

	state.CursorShape = decscusrParam(t.CursorStyle())

	state.TabStops = tabStopBits(t.TabStops(), t.Width())
	titlesToWire(state, t.Titles())
	state.GuestColors = guestColorsToWire(t.GuestColors())
	state.ANSIModes = t.ANSIModes()

	state.PendingInput = t.PendingInput()
	if oc, ok := t.(openClusters); ok {
		if c, open := oc.OpenCluster(); open {
			state.OpenCluster = &OpenClusterState{Text: c.Text, X: c.X, Y: c.Y, Width: c.Width, Left: c.Left, Right: c.Right}
		}
	}

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

type rowFlags uint8

const (
	rowWrapped rowFlags = 1 << iota
	rowPadded
)

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

func screenRowFlags(t vt.Terminal, y int) rowFlags {
	w, _ := t.RowSoftWrapped(y)
	return newRowFlags(w, t.RowPadded(y))
}

func historyRowFlags(t vt.Terminal, i int) rowFlags {
	w, _ := t.ScrollbackSoftWrapped(i)
	return newRowFlags(w, t.ScrollbackPadded(i))
}

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

func stateCells(t vt.Terminal, state *State, colors colorWireCache, first, end int) {
	width, height := state.Width, state.Height
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

var blankCellState = CellState{Content: " ", Width: 1}

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
