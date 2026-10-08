package vt

import uv "github.com/charmbracelet/ultraviolet"

// The state a snapshot carries that no cell shows: DECSCA protection on the
// pen and on the cells, the cursor DECSC saved, and the character REP repeats.
// Each decides what output that has not arrived yet does, so a client that
// comes back without it diverges at the next byte that uses it.

// CursorProtected reports whether DECSCA protects what is printed next.
func (e *Emulator) CursorProtected() bool {
	return e.scr.cur.Protected
}

// RestoreCursorProtected puts back the pen's DECSCA protection.
func (e *Emulator) RestoreCursorProtected(on bool) {
	e.scr.cur.Protected = on
}

// ProtectedCells lists the cells DECSCA protected, on the active screen or
// with main set on the main screen.
func (e *Emulator) ProtectedCells(main bool) []CellRun {
	if main {
		return e.scrs[0].buf.protectedRuns()
	}
	return e.scr.buf.protectedRuns()
}

// RestoreProtectedCells replaces the protection of the active screen, or with
// main set of the main screen, with runs.
func (e *Emulator) RestoreProtectedCells(main bool, runs []CellRun) {
	if main {
		e.scrs[0].buf.restoreProtected(runs)
		return
	}
	e.scr.buf.restoreProtected(runs)
}

// LastPrinted is the cluster REP repeats.
func (e *Emulator) LastPrinted() string {
	return e.lastCluster
}

// RestoreLastPrinted puts back the cluster REP repeats. Its width is not
// needed: REP prints the cluster again, which measures it.
func (e *Emulator) RestoreLastPrinted(cluster string) {
	e.lastCluster = cluster
	e.lastClusterWidth = 0
}

// SavedCursor is what DECSC saved on the active screen, or with main set on
// the main screen.
func (e *Emulator) SavedCursor(main bool) SavedCursor {
	s := e.scr
	if main {
		s = &e.scrs[0]
	}
	x := s.savedExtra
	ids := x.charsetIDs
	for i, id := range ids {
		if id == 0 {
			ids[i] = 'B'
		}
	}
	return SavedCursor{
		X:           s.saved.X,
		Y:           s.saved.Y,
		Pen:         s.saved.Pen,
		Link:        s.saved.Link,
		PendingWrap: x.phantom,
		Origin:      x.origin,
		Protected:   s.saved.Protected,
		Charsets:    ids,
		GL:          x.gl,
		GR:          x.gr,
	}
}

// RestoreSavedCursor puts back what DECSC saved on the active screen, or with
// main set on the main screen.
func (e *Emulator) RestoreSavedCursor(main bool, c SavedCursor) {
	s := e.scr
	if main {
		s = &e.scrs[0]
	}
	s.saved = Cursor{
		Pen:       c.Pen,
		Link:      c.Link,
		Position:  uv.Pos(max(c.X, 0), max(c.Y, 0)),
		Protected: c.Protected,
	}
	x := savedExtras{phantom: c.PendingWrap, origin: c.Origin}
	for i, id := range c.Charsets {
		switch id {
		case 'A':
			x.charsets[i] = UK
		case '0':
			x.charsets[i] = SpecialDrawing
		default:
			id = 'B'
		}
		x.charsetIDs[i] = id
	}
	if c.GL >= 0 && c.GL < 4 {
		x.gl = c.GL
	}
	if c.GR >= 0 && c.GR < 4 {
		x.gr = c.GR
	}
	s.savedExtra = x
}
