//go:build ghostty

package vt

import (
	"bytes"
	"fmt"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	gh "go.mitchellh.com/libghostty"
)

func (t *GhosttyTerminal) liveScreenLocked() int {
	if t.cachedAltScreen.Load() {
		return 1
	}
	return 0
}

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
	if gs, err := t.term.CursorSGRStyle(); err == nil && gs != nil {
		c.Pen = t.convertStyle(gs)
	}
	t.savedCur[t.liveScreenLocked()] = c
}

func (t *GhosttyTerminal) restoreCursorShadowLocked(idx int) {
	c := t.savedCur[idx]
	t.charsetIDs = c.Charsets
	t.gl, t.gr = c.GL, c.GR
	t.penProtected = c.Protected
}

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
		gh.WithFormatterExtras(gh.FormatterExtraHyperlink|gh.FormatterExtraProtection))
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

func parsePenExtras(out []byte) (uv.Link, bool) {
	var link uv.Link
	if i := bytes.LastIndex(out, []byte("\x1b]8;")); i >= 0 {
		rest := out[i+4:]
		if end := bytes.Index(rest, []byte("\x1b\\")); end >= 0 {
			params, uri, ok := strings.Cut(string(rest[:end]), ";")
			if ok {
				link = uv.Link{URL: LinkURI(uri), Params: LinkParams(params)}
			}
		}
	}
	return link, bytes.Contains(out, []byte("\x1b[1\"q"))
}

// CursorProtected implements [Terminal.CursorProtected].
func (t *GhosttyTerminal) CursorProtected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	_, protected := t.penExtrasLocked()
	return protected
}

// RestoreCursorProtected implements [Terminal.RestoreCursorProtected].
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

// RestoreProtectedCells implements [Terminal.RestoreProtectedCells].
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

// LastPrinted implements [Terminal.LastPrinted].
func (t *GhosttyTerminal) LastPrinted() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if t.scanner.lastPrint == 0 {
		return ""
	}
	return string(t.scanner.lastPrint)
}

// RestoreLastPrinted implements [Terminal.RestoreLastPrinted].
func (t *GhosttyTerminal) RestoreLastPrinted(cluster string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.pendingRestore()
	r.lastPrinted, r.hasLastPrinted = cluster, true
}

// SavedCursor implements [Terminal.SavedCursor].
func (t *GhosttyTerminal) SavedCursor(main bool) SavedCursor {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if main {
		return t.savedCur[0]
	}
	return t.savedCur[t.liveScreenLocked()]
}

// RestoreSavedCursor implements [Terminal.RestoreSavedCursor].
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

func plainBlank(c *uv.Cell) bool {
	return (c.Content == "" || c.Content == " ") && c.Style.IsZero() && c.Link.URL == ""
}

func appendLastPrinted(seq *bytes.Buffer, ch string, y, cols int) {
	fmt.Fprintf(seq, "\x1b[%d;1H\x1b(B\x0f\x1b[0m\x1b[0\"q%s\x1b[%d;1H\x1b[%dX", y+1, ch, y+1, cols)
}
