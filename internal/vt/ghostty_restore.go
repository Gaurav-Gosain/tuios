//go:build ghostty

package vt

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
	// penProtected is DECSCA on the pen, when hasPenProtected says the
	// snapshot carried it.
	penProtected    bool
	hasPenProtected bool
	// prot is each screen's protected cells, main first. The painter sends
	// DECSCA around them.
	prot [2][]CellRun
	// lastPrinted is the character REP repeats, when hasLastPrinted says
	// the snapshot carried it. The library sets it only by printing, so the
	// synthesis prints it and erases it again.
	lastPrinted    string
	hasLastPrinted bool
	// saved is each screen's saved cursor, main first, when the snapshot
	// carried it. The library takes one only by saving the live cursor, so
	// the synthesis puts the live cursor into that state and saves it.
	saved [2]*SavedCursor
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
		seq.WriteString("\x1b[?69l\x1b[r\x1b[?6l\x1b(B\x1b)B\x1b*B\x1b+B\x0f\x1b[0m\x1b[0\"q\x1b]8;;\x1b\\\x1b[2J\x1b[H")
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
	mainProt := protRows(r.prot[0], t.width, t.height)
	appendGridPaint(&seq, r.grids[0], mainProt, t.width, t.height, mainWraps)

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
		// The switch uses the mode the guest used. Leaving answers to the
		// mode: a guest that entered with 1047 leaves with 1047, and the
		// library keeps a screen entered with 1049 marked as the alternate
		// one after 1047 is reset.
		altMode := 1049
		switch {
		case r.modes[1049]:
		case r.modes[1047]:
			altMode = 1047
		case r.modes[47]:
			altMode = 47
		}
		// The main screen's saved cursor is put into the live one first and
		// saved: entering with 1049 saves it, and the other two modes are
		// sent a DECSC. The scroll region is not set yet, so origin mode
		// addresses the whole screen.
		if sc := r.saved[0]; sc != nil {
			appendSavedCursor(&seq, *sc, r.grids[0], mainProt, 0, 0)
			if altMode != 1049 {
				seq.WriteString("\x1b7")
			}
		}
		fmt.Fprintf(&seq, "\x1b[?%dh\x1b[?6l\x1b[0m\x1b[0\"q\x1b[2J\x1b[H", altMode)
		appendGridPaint(&seq, r.grids[1], protRows(r.prot[1], t.width, t.height), t.width, t.height, r.screenWraps)
	}
	active := 0
	if altActive {
		active = 1
	}
	activeProt := protRows(r.prot[active], t.width, t.height)

	// Charsets, kept aside as well so a pending wrap can put them back after it
	// prints with ASCII selected.
	var charsets bytes.Buffer
	if r.hasCharsets {
		appendCharsets(&charsets, r.charsets, r.gl, r.gr)
	}
	seq.Write(charsets.Bytes())

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
			case 1048:
				// Setting it saves the cursor and resetting it restores one,
				// pen, character sets and origin mode with it, which would
				// change the state being restored halfway through. The saved
				// cursor is synthesized below.
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

	// The active screen's saved cursor: the live cursor is put into its
	// state and saved, and the live state follows below.
	if sc := r.saved[active]; sc != nil {
		appendSavedCursor(&seq, *sc, r.grids[active], activeProt, regionTop, regionLeft)
		seq.WriteString("\x1b7\x1b[?6l\x1b[0m\x1b[0\"q")
		seq.Write(charsets.Bytes())
	}

	// The character REP repeats, which only a print sets. A pending wrap
	// below prints the cell under the cursor again, and when that cell is
	// the character, the reprint sets it and this is not needed.
	reprintAs := ""
	if r.hasLastPrinted && r.lastPrinted != "" {
		if cell := cursorCell(r.grids[active], r.cursorX, r.cursorY); r.hasCursor && r.pendingWrap && cell != nil {
			switch {
			case cell.Content == r.lastPrinted:
				reprintAs = cell.Content
			case mapThroughCharset(r.lastPrinted, r.charsets, r.gl) == cell.Content:
				reprintAs = r.lastPrinted
			}
		}
		if reprintAs == "" {
			cols := max(cellWidthOf(r.lastPrinted), 1)
			if y, ok := lastPrintedRow(r.grids[active], activeProt, r.screenWraps, t.width, t.height, cols); ok {
				appendLastPrinted(&seq, r.lastPrinted, y, cols)
				seq.Write(charsets.Bytes())
			}
		}
	}

	if decom {
		seq.WriteString("\x1b[?6h")
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
			if reprintAs != "" && reprintAs != cursorCell(r.grids[active], r.cursorX, r.cursorY).Content {
				// The cell holds the character as the charset in force
				// maps it, so it is printed as the guest sent it, with
				// that charset, and REP's character comes out right.
				lead, _ := cursorLead(r.grids[active], r.cursorX, r.cursorY)
				fmt.Fprintf(&seq, "\x1b[%d;%dH", y+1, lead+(x-r.cursorX)+1)
				seq.Write(charsets.Bytes())
				cell := *cursorCell(r.grids[active], r.cursorX, r.cursorY)
				cell.Content = reprintAs
				var p []bool
				if row := activeProt[r.cursorY]; row != nil {
					p = row[lead : lead+1]
				}
				appendStyledLineProt(&seq, uv.Line{cell}, p)
				seq.WriteString("\x1b[0m")
			} else {
				appendReprint(&seq, r.grids[active], activeProt, r.cursorX, r.cursorY, y, x-r.cursorX)
				seq.Write(charsets.Bytes())
			}
		}
	}

	// The pen, last, because everything above prints with a pen of its own:
	// the rendition, the hyperlink and DECSCA.
	if r.hasPen {
		seq.WriteString("\x1b[0m")
		seq.WriteString(penStyleSequence(&r.pen))
		if r.penLink.URL != "" {
			seq.WriteString("\x1b]8;" + r.penLink.Params + ";" + r.penLink.URL + "\x1b\\")
		}
	}
	if r.hasPenProtected {
		if r.penProtected {
			seq.WriteString("\x1b[1\"q")
		} else {
			seq.WriteString("\x1b[0\"q")
		}
	}

	// Shadow state follows the synthesized stream, which bypassed the
	// scanner deliberately. The extending ground state above selected
	// ASCII into the four slots and GL; it left the saved charsets and GR
	// where the emulator had them, and the shadow keeps them too.
	t.charsetIDs = defaultCharsetIDs
	t.gl = 0
	if !extend {
		t.savedCur = [2]SavedCursor{{Charsets: defaultCharsetIDs}, {Charsets: defaultCharsetIDs}}
		t.penProtected = false
		t.scanner.lastPrint = 0
		t.gr = 0
	}
	for i, sc := range r.saved {
		if sc != nil {
			c := *sc
			for k, id := range c.Charsets {
				if id != 'A' && id != '0' {
					c.Charsets[k] = 'B'
				}
			}
			t.savedCur[i] = c
		}
	}
	if r.hasPenProtected {
		t.penProtected = r.penProtected
	}
	if r.hasLastPrinted {
		t.scanner.lastPrint = 0
		if ch, _ := utf8.DecodeRuneInString(r.lastPrinted); r.lastPrinted != "" && ch != utf8.RuneError {
			t.scanner.lastPrint = ch
		}
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
func appendGridPaint(seq *bytes.Buffer, grid map[[2]int]*uv.Cell, prot map[int][]bool, width, height int, wraps []bool) {
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
		if !wrapped {
			line = trimTrailingBlanks(line)
		}
		var p []bool
		if row := prot[y]; row != nil {
			p = row[:len(line)]
		}
		appendStyledLineProt(seq, line, p)
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
	appendStyledLineProt(seq, line, nil)
}

// appendStyledLineProt is appendStyledLine with DECSCA around the cells prot
// marks, cell by cell, and off again at the end. A nil prot protects none.
func appendStyledLineProt(seq *bytes.Buffer, line uv.Line, prot []bool) {
	protecting := false
	setProt := func(x int) {
		on := x < len(prot) && prot[x]
		if on == protecting {
			return
		}
		protecting = on
		if on {
			seq.WriteString("\x1b[1\"q")
		} else {
			seq.WriteString("\x1b[0\"q")
		}
	}
	defer func() {
		if protecting {
			seq.WriteString("\x1b[0\"q")
		}
	}()
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
		setProt(x)
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

// cursorLead is the column of the cell under the cursor at x, y, moved to the
// lead of a wide glyph when the cursor stands on its second half, and that
// cell.
func cursorLead(grid map[[2]int]*uv.Cell, x, y int) (int, *uv.Cell) {
	cell := grid[[2]int{x, y}]
	if x > 0 && (cell == nil || cell.Width == 0) {
		if lead := grid[[2]int{x - 1, y}]; lead != nil && lead.Width == 2 {
			return x - 1, lead
		}
	}
	return x, cell
}

// cursorCell is the cell cursorLead finds, as a blank when the grid holds
// none there.
func cursorCell(grid map[[2]int]*uv.Cell, x, y int) *uv.Cell {
	_, cell := cursorLead(grid, x, y)
	if cell == nil || cell.Content == "" {
		return &uv.Cell{Content: " ", Width: 1}
	}
	return cell
}

// mapThroughCharset is ch as the set in GL draws it, for a single byte the
// set maps. Anything else draws as itself.
func mapThroughCharset(ch string, ids [4]byte, gl int) string {
	if len(ch) != 1 || gl < 0 || gl > 3 {
		return ch
	}
	var set CharSet
	switch ids[gl] {
	case '0':
		set = SpecialDrawing
	case 'A':
		set = UK
	}
	if m, ok := set[ch[0]]; ok {
		return m
	}
	return ch
}

// cellWidthOf is how many columns ch takes.
func cellWidthOf(ch string) int {
	_, w := ansi.FirstGraphemeCluster(ch, ansi.GraphemeWidth)
	return w
}
