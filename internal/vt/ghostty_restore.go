//go:build ghostty

package vt

import (
	"bytes"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// Snapshot restore. The pure emulator pokes restored state straight into its
// structs; libghostty accepts only a byte stream. The Restore*/SetCell
// family therefore buffers everything and the first operation that needs
// terminal state flushes the buffer as one synthesized stream, in a fixed
// order that does not depend on the order ApplyTerminalState called the
// primitives. The synthesis starts from a hard reset when the emulator holds
// no history, which is the situation a fresh attach restores into. An
// emulator that already holds history is one that survived a workspace
// switch: ApplyTerminalState hands it only the rows it missed, and the
// synthesis extends what it kept instead of resetting it.
type ghosttyRestore struct {
	scrollback []uv.Line
	// grids[0] is the main screen, grids[1] the alternate.
	grids            [2]map[[2]int]*uv.Cell
	modes            map[int]bool
	charsets         [4]byte
	gl, gr           int
	hasCharsets      bool
	scrollRegion     uv.Rectangle
	hasScrollRegion  bool
	altScreen        bool
	hasAltScreen     bool
	cursorX, cursorY int
	hasCursor        bool
	// pendingWrap arms a wrap at the restored cursor. The library takes no
	// sequence that sets the flag, so the synthesis prints the cell under the
	// cursor again, which leaves the cursor and the flag where the guest's
	// own print of it did.
	pendingWrap   bool
	pen           uv.Style
	penLink       uv.Link
	hasPen        bool
	kittyKbdStack []int
	// kittyKbdMainStack is the main screen's stack, carried while the
	// alternate screen is in use.
	kittyKbdMainStack []int
	// modifyOtherKeys is the XTMODKEYS level, when hasModifyOtherKeys says
	// the snapshot carried one.
	modifyOtherKeys    int
	hasModifyOtherKeys bool
	// screenWraps and historyWraps are the soft-wrap flags the snapshot
	// carries (see RestoreSoftWraps). The library keeps a wrap flag only for
	// a row it wrapped itself, so the synthesis reproduces each one by
	// typing the row out to the edge and letting the next row carry on.
	screenWraps  []bool
	historyWraps []bool
}

func (t *GhosttyTerminal) pendingRestore() *ghosttyRestore {
	if t.restore == nil {
		t.restore = &ghosttyRestore{}
		t.restorePending.Store(true)
	}
	return t.restore
}

// setActiveCell buffers one restored cell for the active screen. The target
// honors a pending alt-screen restore, since ApplyTerminalState switches
// screens before it writes cells.
func (r *ghosttyRestore) setActiveCell(activeNow, x, y int, c *uv.Cell) {
	idx := activeNow
	if r.hasAltScreen {
		idx = 0
		if r.altScreen {
			idx = 1
		}
	}
	r.setGridCell(idx, x, y, c)
}

// setGridCell buffers one restored cell on an explicit screen.
func (r *ghosttyRestore) setGridCell(idx, x, y int, c *uv.Cell) {
	if r.grids[idx] == nil {
		r.grids[idx] = make(map[[2]int]*uv.Cell)
	}
	var copied *uv.Cell
	if c != nil {
		cc := *c
		copied = &cc
	}
	r.grids[idx][[2]int{x, y}] = copied
}

// flushRestoreLocked synthesizes and applies the pending restore. Call with
// mu held.
func (t *GhosttyTerminal) flushRestoreLocked() {
	r := t.restore
	if r == nil {
		return
	}
	t.restore = nil
	t.restorePending.Store(false)
	if t.closed.Load() {
		return
	}

	var seq bytes.Buffer

	// A hard reset gives the synthesis a known ground state, and on the
	// library it also drops the history. That is right for a fresh emulator
	// and wrong for one that survived a workspace switch: it is handed only
	// the rows that scrolled off while it was away, and the reset would
	// throw away everything it kept, so the pane came back with the
	// daemon's bounded window as its whole history. The surviving emulator
	// gets the same ground state built by hand, minus the history: back on
	// the main screen, no margins, absolute addressing, ASCII in every
	// charset slot, a clean pen, and a cleared screen.
	//
	// ED 2 pushes nothing into history on the library, as on the pure
	// emulator (TestGhosttyDiffEraseDisplayKeepsHistory pins it). The clear
	// has to come before the lines are typed, not only after: the live rows
	// are inside the tail the daemon sent, and typing over them would scroll
	// them into history a second time.
	extend := t.scrollbackLenLocked() > 0
	if extend {
		if t.activeAltLiveLocked() {
			seq.WriteString("\x1b[?1049l\x1b[?1047l")
		}
		seq.WriteString("\x1b[?69l\x1b[r\x1b[?6l\x1b(B\x1b)B\x1b*B\x1b+B\x0f\x1b[0m\x1b]8;;\x1b\\\x1b[2J\x1b[H")
	} else {
		seq.WriteString("\x1bc")
	}

	// Scrollback replays as printed lines pushed off the top. A line that
	// wrapped is typed out to the edge with no newline, so the next line
	// carries on from it and the library records the wrap itself. The last
	// line cannot carry on into the screen, which is painted separately, so
	// its wrap is not reproduced and it reads as ending.
	if len(r.scrollback) > 0 {
		off := len(r.historyWraps) - len(r.scrollback)
		for i, line := range r.scrollback {
			if i < len(r.scrollback)-1 && off+i >= 0 && r.historyWraps[off+i] {
				appendStyledLine(&seq, padLine(line, t.width))
				seq.WriteString("\x1b[0m")
				continue
			}
			appendStyledLine(&seq, trimTrailingBlanks(line))
			seq.WriteString("\x1b[0m\r\n")
		}
		// The last rows are still on screen; scroll them into history.
		rows := min(len(r.scrollback), t.height-1)
		fmt.Fprintf(&seq, "\x1b[%d;1H", t.height)
		for range rows {
			seq.WriteByte('\n')
		}
		seq.WriteString("\x1b[2J\x1b[H")
	}

	// Main screen cells. The snapshot's wrap flags are the active screen's,
	// so they go with the main screen only while it is the active one.
	altActive := r.hasAltScreen && r.altScreen
	var mainWraps []bool
	if !altActive {
		mainWraps = r.screenWraps
	}
	appendGridPaint(&seq, r.grids[0], t.width, t.height, mainWraps)

	// The alternate screen switches on before its cells paint, so region,
	// pen and cursor below land on the screen the snapshot took them from.
	// The switch also ends the shadow's view of the main screen, so the
	// stream so far is applied and the main shadow captured first.
	if altActive {
		t.term.VTWrite(seq.Bytes())
		seq.Reset()
		t.captureScreenLocked(0)
		// The history length is read from the library only while the main
		// screen is up, so bank it now: the lines just typed are the last
		// thing the main screen shows before the switch.
		if n, err := t.term.ScrollbackRows(); err == nil {
			t.mainSbLen = int(n)
		}
		seq.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
		appendGridPaint(&seq, r.grids[1], t.width, t.height, r.screenWraps)
	}

	// Charsets, kept aside as well so a pending wrap can put them back after it
	// prints with ASCII selected.
	var charsets bytes.Buffer
	charsetStart := seq.Len()
	if r.hasCharsets {
		inters := [4]byte{'(', ')', '*', '+'}
		for i, id := range r.charsets {
			switch id {
			case 'A', '0':
			default:
				id = 'B'
			}
			seq.WriteByte(0x1b)
			seq.WriteByte(inters[i])
			seq.WriteByte(id)
		}
		switch r.gl {
		case 1:
			seq.WriteByte(0x0e)
		case 2:
			seq.WriteString("\x1bn")
		case 3:
			seq.WriteString("\x1bo")
		default:
			seq.WriteByte(0x0f)
		}
		switch r.gr {
		case 1:
			seq.WriteString("\x1b~")
		case 2:
			seq.WriteString("\x1b}")
		case 3:
			seq.WriteString("\x1b|")
		}
	}

	charsets.Write(seq.Bytes()[charsetStart:])

	// Kitty keyboard: the library only needs the effective flags for its
	// query answers; the full stack lives in the shadow.
	if len(r.kittyKbdStack) > 0 {
		top := r.kittyKbdStack[len(r.kittyKbdStack)-1]
		fmt.Fprintf(&seq, "\x1b[=%d;1u", top)
	}

	if r.hasModifyOtherKeys {
		fmt.Fprintf(&seq, "\x1b[>4;%dm", r.modifyOtherKeys)
	}

	// Modes. Origin mode last: enabling it homes the cursor, and the
	// cursor restore below compensates for it.
	decom := false
	if r.modes != nil {
		for _, m := range ghosttyModeNumbers {
			v, ok := r.modes[m.num]
			if !ok {
				continue
			}
			switch m.num {
			case 47, 1047, 1049:
				// Screen selection already synthesized.
				continue
			case 6:
				decom = v
				continue
			}
			ch := byte('l')
			if v {
				ch = 'h'
			}
			fmt.Fprintf(&seq, "\x1b[?%d%c", m.num, ch)
		}
	}

	// Scroll region. DECSTBM homes the cursor; restore order puts the
	// cursor after it.
	regionTop, regionLeft := 0, 0
	if r.hasScrollRegion {
		reg := r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
		if !reg.Empty() && (reg.Min.Y > 0 || reg.Max.Y < t.height) {
			fmt.Fprintf(&seq, "\x1b[%d;%dr", reg.Min.Y+1, reg.Max.Y)
			regionTop = reg.Min.Y
		}
		// The left and right margins, after the mode loop above set
		// DECLRMM: the library takes DECSLRM only with the mode on. Sending
		// only DECSTBM left the library at the full width while the copy
		// below said otherwise, so the client wrapped where the guest did
		// not.
		if lrmm := r.modes[69]; lrmm && !reg.Empty() && (reg.Min.X > 0 || reg.Max.X < t.width) {
			fmt.Fprintf(&seq, "\x1b[%d;%ds", reg.Min.X+1, reg.Max.X)
			regionLeft = reg.Min.X
		}
	}

	if decom {
		seq.WriteString("\x1b[?6h")
	}

	// Pen.
	if r.hasPen {
		seq.WriteString("\x1b[0m")
		seq.WriteString(penStyleSequence(&r.pen))
	}

	// Cursor. With origin mode on, addressing is region-relative.
	if r.hasCursor {
		y, x := r.cursorY, r.cursorX
		if decom {
			y -= regionTop
			x -= regionLeft
		}
		if y < 0 {
			y = 0
		}
		fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, x+1)
		if r.pendingWrap {
			appendPendingWrap(&seq, r, altActive, y, x-r.cursorX, charsets.Bytes())
		}
	}

	// Shadow state follows the synthesized stream, which bypassed the
	// scanner deliberately. The extending ground state above selected
	// ASCII into the four slots and GL; it left the saved charsets and GR
	// where the emulator had them, and the shadow keeps them too.
	t.charsetIDs = defaultCharsetIDs
	t.gl = 0
	if !extend {
		t.savedCharsets = defaultCharsetIDs
		t.gr = 0
	}
	if r.hasCharsets {
		for i, id := range r.charsets {
			switch id {
			case 'A', '0':
				t.charsetIDs[i] = id
			default:
				t.charsetIDs[i] = 'B'
			}
		}
		if r.gl >= 0 && r.gl < 4 {
			t.gl = r.gl
		}
		if r.gr >= 0 && r.gr < 4 {
			t.gr = r.gr
		}
	}
	t.scrollRegion = uv.Rect(0, 0, t.width, t.height)
	if r.hasScrollRegion {
		t.scrollRegion = r.scrollRegion.Intersect(uv.Rect(0, 0, t.width, t.height))
	}
	// A snapshot that carries no kitty keyboard stack leaves a surviving
	// emulator's flags alone, on both backends: the library was sent nothing
	// for them, and the pure emulator's RestoreKittyKeyboardState returns
	// early on an empty stack.
	if r.hasModifyOtherKeys {
		t.modifyOtherKeys.Store(int32(r.modifyOtherKeys)) //nolint:gosec // bounded by RestoreModifyOtherKeys
	} else if !extend {
		t.modifyOtherKeys.Store(0)
	}
	// A ConPTY pane never offered the protocol, so a saved stack describes a
	// negotiation that could not have happened; leave the (empty) state alone.
	if !t.kkpUnavailable {
		if len(r.kittyKbdStack) > 0 {
			t.kittyKbd.Reset()
			t.kittyKbd.SelectScreen(altActive)
			t.kittyKbd.SetStack(r.kittyKbdStack)
		} else if !extend {
			t.kittyKbd.Reset()
			t.kittyKbd.SelectScreen(altActive)
		}
		if len(r.kittyKbdMainStack) > 0 {
			t.kittyKbd.SetMainStack(r.kittyKbdMainStack)
		}
	}

	t.term.VTWrite(seq.Bytes())
	t.gridStale = true
	t.scrollGeneration++
	t.markAllDirtyLocked()
	t.refreshCachesLocked()
}

// appendGridPaint paints buffered cells row by row with minimal style churn.
// A row wraps says wrapped is typed out to the edge, and the row after it is
// typed straight on without a cursor move, so the library wraps it and keeps
// the flag. The last row cannot wrap, since that would scroll the screen.
func appendGridPaint(seq *bytes.Buffer, grid map[[2]int]*uv.Cell, width, height int, wraps []bool) {
	if len(grid) == 0 {
		return
	}
	carrying := false
	for y := 0; y < height; y++ {
		wrapped := y < len(wraps) && wraps[y] && y < height-1
		rowHas := wrapped || carrying
		for x := 0; x < width && !rowHas; x++ {
			if _, ok := grid[[2]int{x, y}]; ok {
				rowHas = true
			}
		}
		if !rowHas {
			continue
		}
		if !carrying {
			fmt.Fprintf(seq, "\x1b[%d;1H", y+1)
		}
		line := make(uv.Line, width)
		for x := 0; x < width; x++ {
			if c, ok := grid[[2]int{x, y}]; ok && c != nil {
				line[x] = *c
			} else {
				line[x] = uv.Cell{Content: " ", Width: 1}
			}
		}
		if wrapped {
			appendStyledLine(seq, line)
		} else {
			appendStyledLine(seq, trimTrailingBlanks(line))
		}
		seq.WriteString("\x1b[0m")
		carrying = wrapped
	}
}

// padLine is line cut or padded with blanks to exactly width cells, for a row
// that has to be typed out to the edge.
func padLine(line uv.Line, width int) uv.Line {
	if len(line) >= width {
		return line[:width]
	}
	out := make(uv.Line, width)
	copy(out, line)
	for x := len(line); x < width; x++ {
		out[x] = uv.Cell{Content: " ", Width: 1}
	}
	return out
}

// appendStyledLine emits one line's cells with SGR changes only at style
// boundaries. Zero-width cells (wide-cell tails) emit nothing; the leading
// cell advanced the cursor for them.
func appendStyledLine(seq *bytes.Buffer, line uv.Line) {
	var cur uv.Style
	curSet := false
	link := ""
	skipNext := false
	for x := 0; x < len(line); x++ {
		c := line[x]
		if skipNext {
			// The wide glyph before this cell advanced the cursor over it,
			// whatever spacer convention the snapshot used.
			skipNext = false
			continue
		}
		if c.Width == 2 {
			skipNext = true
		}
		if c.Width == 0 {
			// A zero-width cell with no preceding wide glyph still holds a
			// column.
			seq.WriteByte(' ')
			continue
		}
		if !curSet || !c.Style.Equal(&cur) {
			seq.WriteString("\x1b[0m")
			seq.WriteString(penStyleSequence(&c.Style))
			cur = c.Style
			curSet = true
		}
		if c.Link.URL != link {
			if c.Link.URL != "" {
				seq.WriteString("\x1b]8;;" + c.Link.URL + "\x1b\\")
			} else {
				seq.WriteString("\x1b]8;;\x1b\\")
			}
			link = c.Link.URL
		}
		if c.Content == "" {
			seq.WriteByte(' ')
		} else {
			seq.WriteString(c.Content)
		}
	}
	if link != "" {
		seq.WriteString("\x1b]8;;\x1b\\")
	}
}

// captureScreenLocked snapshots the library's current screen into one shadow
// buffer, regardless of dirty state. Used when a synthesized stream is about
// to switch screens and the one being left would otherwise never be read.
func (t *GhosttyTerminal) captureScreenLocked(idx int) {
	if t.closed.Load() {
		return
	}
	_ = t.rs.SetDirty(gh.RenderStateDirtyFull)
	if err := t.rs.Update(t.term); err != nil {
		return
	}
	// Same rule as syncLocked: style IDs do not survive a snapshot.
	clear(t.styleCache)
	if err := t.rs.RowIterator(t.ri); err != nil {
		return
	}
	for {
		y, ok := t.ri.NextDirty()
		if !ok {
			break
		}
		if int(y) >= t.height {
			continue
		}
		t.syncRowLocked(t.bufAt(idx), int(y))
	}
	_ = t.rs.Clean()
}

// trimTrailingBlanks drops unstyled trailing blanks from a line before it is
// painted. Painting a row through its last column would leave the sink's
// pending-wrap machinery treating the row as a wrapped logical line, and the
// next resize would reflow restored rows into each other.
func trimTrailingBlanks(line uv.Line) uv.Line {
	end := len(line)
	for end > 0 {
		c := line[end-1]
		if (c.Content == "" || c.Content == " ") && c.Style.IsZero() && c.Link.URL == "" {
			end--
			continue
		}
		break
	}
	return line[:end]
}

// appendPendingWrap prints the cell under the restored cursor again, so the
// library is left with the cursor on it and a wrap pending, as the guest's own
// print left the daemon's emulator. A wide glyph is printed from its lead cell.
// The cell goes out with its own style and with ASCII selected, so the charset
// in force does not translate it a second time, and the pen and the charsets
// are then put back. row is the cursor row as addressed, which origin mode
// makes relative to the scroll region.
func appendPendingWrap(seq *bytes.Buffer, r *ghosttyRestore, altActive bool, row, colOff int, charsets []byte) {
	grid := r.grids[0]
	if altActive {
		grid = r.grids[1]
	}
	x := r.cursorX
	cell := grid[[2]int{x, r.cursorY}]
	if x > 0 && (cell == nil || cell.Width == 0) {
		if lead := grid[[2]int{x - 1, r.cursorY}]; lead != nil && lead.Width == 2 {
			x, cell = x-1, lead
		}
	}
	if cell == nil || cell.Content == "" {
		cell = &uv.Cell{Content: " ", Width: 1}
	}
	fmt.Fprintf(seq, "\x1b[%d;%dH\x1b(B\x0f", row+1, x+colOff+1)
	appendStyledLine(seq, uv.Line{*cell})
	seq.WriteString("\x1b[0m")
	seq.Write(charsets)
	if r.hasPen {
		seq.WriteString(penStyleSequence(&r.pen))
	}
}
