package snapshot

import (
	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// History is a pane's history as read from its emulator for saving, before
// it is packed. The screen rows are copies cut to their used cells. The history
// rows are a copy of the emulator's encoded lines, a byte or so a cell, which
// state decodes a row at a time after the emulator's lock is released.
//
// They were decoded under the lock into rows of 112-byte cells: 1000 rows of
// a 200-column pane were 26 MB and 12 ms of a pane's output held up, and a
// save of 20 busy panes allocated 562 MB.
type History struct {
	width, height int
	// cursorY is the cursor's row, -1 when the screen saved is the main one
	// under an alternate screen, where the cursor is somewhere else.
	cursorY     int
	screen      []uv.Line
	screenFlags []rowFlags
	history     *vt.ScrollbackCopy
}

// CaptureHistory reads the main screen and up to lines history rows, for a
// history saved to disk. The caller holds the emulator's lock for this call
// only. See History.State for the rest.
//
// A full-screen program on the alternate screen at the moment of the save is
// left out. Its screen is gone the moment the program is, and what the user
// had before it, the shell's screen, is the main one.
func CaptureHistory(t vt.Terminal, lines int) *History {
	w, h := t.Width(), t.Height()
	r := &History{width: w, height: h, cursorY: t.CursorPosition().Y}
	at := t.CellAt
	if t.IsAltScreen() {
		at = t.MainCellAt
		// The main screen's cursor and wrap flags are not reachable while it is
		// hidden. No flag is the safe answer: a wrongly joined row glues two
		// lines into one.
		r.cursorY = -1
	} else {
		r.screenFlags = make([]rowFlags, h)
		for y := range h {
			r.screenFlags[y] = screenRowFlags(t, y)
		}
	}
	r.screen = make([]uv.Line, h)
	// The pure emulator holds the cells of an open prompt's row that do not
	// fit a narrower pane. They are saved with the row, which then comes
	// back wider than the pane, into the history (see ApplyHistory).
	tails, _ := t.(interface{ MainRowTail(int) uv.Line })
	for y := range h {
		row := make(uv.Line, w)
		for x := range w {
			if c := at(x, y); c != nil {
				row[x] = *c
				// A saved history holds no image: its cells come back blank.
				vt.BlankSixelCell(&row[x])
			} else {
				row[x] = uv.Cell{Content: " ", Width: 1}
			}
		}
		if tails != nil {
			for _, c := range tails.MainRowTail(y) {
				vt.BlankSixelCell(&c)
				row = append(row, c)
			}
		}
		r.screen[y] = row[:usedCells(row)]
	}
	n := t.ScrollbackLen()
	r.history = t.CopyScrollback(max(n-max(lines, 0), 0), n)
	return r
}

// Len is how many history rows were read.
func (r *History) Len() int { return r.history.Len() }

// State packs the screen and the newest lines of the history rows into a
// packed State, the form a saved history holds. It reads nothing from the
// emulator, so it can run after the emulator's lock is released.
func (r *History) State(lines int) *State {
	held := r.history.Len()
	lines = min(max(lines, 0), held)
	first := held - lines
	st := &State{
		Width:         r.width,
		Height:        r.height,
		CursorY:       r.cursorY,
		ScrollbackLen: lines,
	}
	st.ScreenWraps, st.ScreenPads = rowFlagBits(r.screenFlags)
	flags := make([]rowFlags, lines)
	for k := range flags {
		flags[k] = newRowFlags(r.history.Wrapped(first+k), r.history.Padded(first+k))
	}
	st.ScrollbackWraps, st.ScrollbackPads = rowFlagBits(flags)
	colors := colorWireCache{}
	p := newRowPacker()
	var row []CellState
	add := func(b *packedRows, line uv.Line) {
		row = row[:0]
		for x := range line {
			row = append(row, colors.cellState(&line[x]))
		}
		// The blank tail is cut. Put back to the pane's width it records
		// the row's width, as a snapshot does, and the packer drops it
		// again.
		for len(row) < r.width {
			row = append(row, blankCellState)
		}
		b.add(p, row)
	}
	// Screen first and then history, the order Pack packs them in.
	b := newPackedRows(len(r.screen) * 32)
	for _, line := range r.screen {
		add(&b, line)
	}
	st.PackedScreen = b.blob()
	b = newPackedRows(lines * 32)
	r.history.Rows(first, held, func(_ int, line uv.Line) bool {
		// The row is the copy's reused buffer, so the image cells are
		// blanked in place: a saved history holds no image.
		for x := range line {
			vt.BlankSixelCell(&line[x])
		}
		add(&b, line[:usedCells(line)])
		return true
	})
	st.PackedScrollback = b.blob()
	st.Styles = p.styles
	return st
}

// ApplyHistory lays a saved history (History.State) into a new emulator: the
// saved history rows go into its history, the saved screen's used rows onto
// its screen, and the cursor under them, where the caller writes a divider.
// It returns the error of a state that does not unpack, and then changes
// nothing.
//
// The emulator is at the saved size while this happens, and the caller
// resizes it afterwards, so a pane that comes back at another width reflows
// the restored screen through the emulator's own reflow rather than being
// cut. The pure emulator keeps the older history at its saved width.
func ApplyHistory(t vt.Terminal, st *State) error {
	if err := st.Unpack(); err != nil {
		return err
	}
	rows := st.Scrollback
	wraps := wrapFlags(st.ScrollbackWraps, len(st.Scrollback))
	pads := wrapFlags(st.ScrollbackPads, len(st.Scrollback))
	used := -1
	for y, row := range st.Screen {
		if !blankRow(row) {
			used = y
		}
	}
	if st.CursorY >= 0 && st.CursorY < len(st.Screen) && st.CursorY > used {
		// The prompt the shell was sitting at, even with nothing typed on it.
		used = st.CursorY
	}
	screenWraps := wrapFlags(st.ScreenWraps, len(st.Screen))
	rows = append(rows[:len(rows):len(rows)], st.Screen[:used+1]...)
	wraps = append(wraps, screenWraps[:used+1]...)
	pads = append(pads, wrapFlags(st.ScreenPads, len(st.Screen))[:used+1]...)
	if len(wraps) > 0 {
		// The divider goes on the next row, so the last saved row does not
		// carry on into it.
		wraps[len(wraps)-1] = false
	}

	// Room under the saved rows for the divider and the new prompt. Only
	// rows that fit the saved width go onto the screen: a history row keeps
	// the width it was written at, which is wider than the screen when the
	// pane narrowed before the save, and the screen would cut it.
	keep := min(len(rows), max(st.Height-2, 0))
	split := len(rows) - keep
	for i := len(rows) - 1; i >= split; i-- {
		if rowExtent(rows[i]) > st.Width {
			split = i + 1
			break
		}
	}
	keep = len(rows) - split
	out := &State{
		Width:         st.Width,
		Height:        st.Height,
		CursorX:       0,
		CursorY:       keep,
		ScrollbackLen: split,
		Scrollback:    rows[:split],
		Screen:        rows[split:],
	}
	out.ScrollbackWraps = wrapBits(wraps[:split])
	out.ScreenWraps = wrapBits(wraps[split:])
	out.ScrollbackPads = wrapBits(pads[:split])
	out.ScreenPads = wrapBits(pads[split:])
	return Apply(t, out)
}

// rowExtent is the number of columns a row needs to show everything it
// holds: up to the end of its last cell that is not a plain blank.
func rowExtent(row []CellState) int {
	for i := len(row) - 1; i >= 0; i-- {
		c := &row[i]
		if (c.Content != "" && c.Content != " ") || c.StyleState != (StyleState{}) {
			return i + max(c.Width, 1)
		}
	}
	return 0
}

// blankRow reports whether a row shows nothing.
func blankRow(row []CellState) bool { return rowExtent(row) == 0 }
