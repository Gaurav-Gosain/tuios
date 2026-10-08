//go:build ghostty

package vt

import (
	"bytes"
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

// The state a snapshot carries that no cell shows, on the library: DECSCA
// protection on the pen and the cells, the cursor DECSC saved, the character
// REP repeats and the pen's hyperlink. The library exposes only the cells'
// protection. The pen's hyperlink and protection are read through a formatter
// that emits them, and the saved cursor and REP's character are read off the
// stream as it goes past. The restore puts each back the only way the library
// takes it, by sending what a guest would send.

// liveScreenLocked is the screen the library is drawing to right now, 0 for
// the main one and 1 for the alternate. It reads the cache rather than the
// library: the scanner flips the cache at the switch, mid-write, and asking
// the library cost three cgo calls on every save and restore of the cursor,
// which a program redrawing a status line does once a line.
func (t *GhosttyTerminal) liveScreenLocked() int {
	if t.cachedAltScreen.Load() {
		return 1
	}
	return 0
}

// saveCursorShadowLocked records what a save of the cursor is about to save,
// on the screen it is saved on. Hooks call it before the sequence reaches the
// library, so the library still shows the cursor being saved.
func (t *GhosttyTerminal) saveCursorShadowLocked() {
	if t.closed.Load() {
		return
	}
	t.scanner.flushOut()
	c := SavedCursor{Charsets: t.charsetIDs, GL: t.gl, GR: t.gr, Protected: t.penProtected}
	if x, err := t.term.CursorX(); err == nil {
		c.X = int(x)
	}
	if y, err := t.term.CursorY(); err == nil {
		c.Y = int(y)
	}
	c.PendingWrap, _ = t.term.CursorPendingWrap()
	c.Origin, _ = t.term.Mode(gh.ModeOrigin)
	if gs, err := t.term.CursorStyle(); err == nil && gs != nil {
		c.Pen = t.convertStyle(gs)
	}
	t.savedCur[t.liveScreenLocked()] = c
}

// restoreCursorShadowLocked follows a restore of screen idx's saved cursor
// into the copies of what the library does not expose.
func (t *GhosttyTerminal) restoreCursorShadowLocked(idx int) {
	c := t.savedCur[idx]
	t.charsetIDs = c.Charsets
	t.gl, t.gr = c.GL, c.GR
	t.penProtected = c.Protected
}

// penExtrasLocked reads the pen's hyperlink and DECSCA protection, which no
// query exposes, from a formatter over one cell that emits them after it.
func (t *GhosttyTerminal) penExtrasLocked() (uv.Link, bool) {
	if t.closed.Load() {
		return uv.Link{}, false
	}
	ref, err := t.term.GridRef(gh.Point{Tag: gh.PointTagActive})
	if err != nil || ref == nil {
		return uv.Link{}, false
	}
	f, err := gh.NewFormatter(t.term,
		gh.WithFormatterFormat(gh.FormatterFormatVT),
		gh.WithFormatterSelection(&gh.Selection{Start: *ref, End: *ref}),
		gh.WithFormatterExtraHyperlink(true),
		gh.WithFormatterExtraProtection(true))
	if err != nil {
		return uv.Link{}, false
	}
	defer f.Close()
	out, err := f.Format()
	if err != nil {
		return uv.Link{}, false
	}
	return parsePenExtras(out)
}

// parsePenExtras reads what penExtrasLocked's formatter emits after the cell:
// OSC 8 with the open hyperlink, and DECSCA 1 when the pen protects. A cell
// holds no escape, so neither can come from the cell.
func parsePenExtras(out []byte) (uv.Link, bool) {
	var link uv.Link
	if i := bytes.LastIndex(out, []byte("\x1b]8;")); i >= 0 {
		rest := out[i+4:]
		if end := bytes.Index(rest, []byte("\x1b\\")); end >= 0 {
			params, uri, ok := strings.Cut(string(rest[:end]), ";")
			if ok {
				link = uv.Link{URL: StripControls(uri), Params: params}
			}
		}
	}
	return link, bytes.Contains(out, []byte("\x1b[1\"q"))
}

func (t *GhosttyTerminal) CursorProtected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	_, protected := t.penExtrasLocked()
	return protected
}

func (t *GhosttyTerminal) RestoreCursorProtected(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.penProtected, r.hasPenProtected = on, true
}

// ProtectedCells reads the protection the shadow grid copied from the
// library's cells.
func (t *GhosttyTerminal) ProtectedCells(main bool) []CellRun {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.syncLocked()
	if main {
		return t.bufAt(0).protectedRuns()
	}
	return t.bufs[t.active].protectedRuns()
}

func (t *GhosttyTerminal) RestoreProtectedCells(main bool, runs []CellRun) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	idx := t.active
	switch {
	case main:
		idx = 0
	case r.hasAltScreen && r.altScreen:
		idx = 1
	case r.hasAltScreen:
		idx = 0
	}
	r.prot[idx] = runs
}

func (t *GhosttyTerminal) LastPrinted() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if t.scanner.lastPrint == 0 {
		return ""
	}
	return string(t.scanner.lastPrint)
}

func (t *GhosttyTerminal) RestoreLastPrinted(cluster string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.lastPrinted, r.hasLastPrinted = cluster, true
}

func (t *GhosttyTerminal) SavedCursor(main bool) SavedCursor {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if main {
		return t.savedCur[0]
	}
	return t.savedCur[t.liveScreenLocked()]
}

func (t *GhosttyTerminal) RestoreSavedCursor(main bool, c SavedCursor) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	idx := t.active
	switch {
	case main:
		idx = 0
	case r.hasAltScreen && r.altScreen:
		idx = 1
	case r.hasAltScreen:
		idx = 0
	}
	r.saved[idx] = &c
}

// protRows turns runs into one protection flag per cell, by row, for the
// painter. It is nil when no run lands on the grid.
func protRows(runs []CellRun, width, height int) map[int][]bool {
	var rows map[int][]bool
	for _, run := range runs {
		if run.Y < 0 || run.Y >= height || run.N <= 0 {
			continue
		}
		x0, x1 := max(run.X, 0), min(run.X+run.N, width)
		if x0 >= x1 {
			continue
		}
		if rows == nil {
			rows = make(map[int][]bool)
		}
		row := rows[run.Y]
		if row == nil {
			row = make([]bool, width)
			rows[run.Y] = row
		}
		for x := x0; x < x1; x++ {
			row[x] = true
		}
	}
	return rows
}

// appendCharsets designates the four sets and invokes GL and GR as ids, gl
// and gr say. A set the library does not have reads as US ASCII.
func appendCharsets(seq *bytes.Buffer, ids [4]byte, gl, gr int) {
	inters := [4]byte{'(', ')', '*', '+'}
	for i, id := range ids {
		switch id {
		case 'A', '0':
		default:
			id = 'B'
		}
		seq.WriteByte(0x1b)
		seq.WriteByte(inters[i])
		seq.WriteByte(id)
	}
	switch gl {
	case 1:
		seq.WriteByte(0x0e)
	case 2:
		seq.WriteString("\x1bn")
	case 3:
		seq.WriteString("\x1bo")
	default:
		seq.WriteByte(0x0f)
	}
	switch gr {
	case 1:
		seq.WriteString("\x1b~")
	case 2:
		seq.WriteString("\x1b}")
	case 3:
		seq.WriteString("\x1b|")
	}
}

// appendSavedCursor puts the live cursor into the state c describes, so a
// save that follows (DECSC, or entering the alternate screen with 1049) saves
// c. top and left are the scroll region's, for addressing under origin mode.
// The caller puts the live state back afterwards.
func appendSavedCursor(seq *bytes.Buffer, c SavedCursor, grid map[[2]int]*uv.Cell, prot map[int][]bool, top, left int) {
	if c.Origin {
		seq.WriteString("\x1b[?6h")
	} else {
		seq.WriteString("\x1b[?6l")
	}
	row, col := c.Y, c.X
	if c.Origin {
		row, col = row-top, col-left
	}
	row, col = max(row, 0), max(col, 0)
	fmt.Fprintf(seq, "\x1b[%d;%dH", row+1, col+1)
	if c.PendingWrap {
		appendReprint(seq, grid, prot, c.X, c.Y, row, col-c.X)
	}
	seq.WriteString("\x1b[0m")
	seq.WriteString(penStyleSequence(&c.Pen))
	if c.Protected {
		seq.WriteString("\x1b[1\"q")
	} else {
		seq.WriteString("\x1b[0\"q")
	}
	appendCharsets(seq, c.Charsets, c.GL, c.GR)
}

// appendReprint prints the cell at x, y of grid again, so the library is left
// with the cursor on it and a wrap pending, as the guest's own print left the
// daemon's emulator. A wide glyph is printed from its lead cell. The cell goes
// out with its own style and protection, with ASCII selected so the charset
// in force does not translate it a second time. row is the cursor row as
// addressed, which origin mode makes relative to the scroll region, and
// colOff turns a column of the grid into one as addressed. It leaves the pen
// at the default and ASCII selected; the caller puts back its own.
func appendReprint(seq *bytes.Buffer, grid map[[2]int]*uv.Cell, prot map[int][]bool, x, y, row, colOff int) {
	cell := grid[[2]int{x, y}]
	if x > 0 && (cell == nil || cell.Width == 0) {
		if lead := grid[[2]int{x - 1, y}]; lead != nil && lead.Width == 2 {
			x, cell = x-1, lead
		}
	}
	if cell == nil || cell.Content == "" {
		cell = &uv.Cell{Content: " ", Width: 1}
	}
	var p []bool
	if row := prot[y]; row != nil && x < len(row) {
		p = row[x : x+1]
	}
	fmt.Fprintf(seq, "\x1b[%d;%dH\x1b(B\x0f", row+1, x+colOff+1)
	appendStyledLineProt(seq, uv.Line{*cell}, p)
	seq.WriteString("\x1b[0m")
}

// lastPrintedRow picks a row where the restore can print REP's character in
// column 0 and erase it again without changing what the screen shows: the
// columns it takes hold a plain blank, and the row does not wrap, which the
// erase would end. It reports false when no row qualifies.
func lastPrintedRow(grid map[[2]int]*uv.Cell, prot map[int][]bool, wraps []bool, width, height, cols int) (int, bool) {
	if cols > width {
		return 0, false
	}
	for y := height - 1; y >= 0; y-- {
		if y < len(wraps) && wraps[y] {
			continue
		}
		if y > 0 && y-1 < len(wraps) && wraps[y-1] {
			continue
		}
		ok := true
		for x := range cols {
			if c := grid[[2]int{x, y}]; c != nil && !plainBlank(c) {
				ok = false
				break
			}
			if row := prot[y]; row != nil && row[x] {
				ok = false
				break
			}
		}
		if ok {
			return y, true
		}
	}
	return 0, false
}

// plainBlank reports whether c is a blank with no style and no link.
func plainBlank(c *uv.Cell) bool {
	return (c.Content == "" || c.Content == " ") && c.Style.IsZero() && c.Link.URL == ""
}

// appendLastPrinted makes the library's REP character ch: it prints ch in
// column 0 of row y and erases it again. Printing is the only way to set it,
// and nothing else in the restore prints after this, except the pending
// wrap's reprint, which sets it to the same character.
func appendLastPrinted(seq *bytes.Buffer, ch string, y, cols int) {
	fmt.Fprintf(seq, "\x1b[%d;1H\x1b(B\x0f\x1b[0m\x1b[0\"q%s\x1b[%d;1H\x1b[%dX", y+1, ch, y+1, cols)
}
