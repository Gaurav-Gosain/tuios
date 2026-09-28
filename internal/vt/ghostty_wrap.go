//go:build ghostty

package vt

import gh "go.mitchellh.com/libghostty"

// The library records a soft wrap on every row it wraps, so both answers are
// a row lookup. A row primed from a snapshot was never wrapped by the library
// and reads as ending, which is the safe answer.

// RowSoftWrapped reports whether active-screen row y carries on to row y+1 by
// autowrap.
func (t *GhosttyTerminal) RowSoftWrapped(y int) (wrapped, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed.Load() || y < 0 || y >= t.height {
		return false, true
	}
	t.flushRestoreLocked()
	return ghosttyRowWrap(t.term, gh.Point{Tag: gh.PointTagActive, Y: uint32(y)}), true
}

// ScrollbackSoftWrapped reports whether history line index (oldest first)
// carries on to the next line by autowrap.
func (t *GhosttyTerminal) ScrollbackSoftWrapped(index int) (wrapped, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushRestoreLocked()
	if t.closed.Load() || index < 0 || index >= t.scrollbackLenLocked() {
		return false, true
	}
	src := t.term
	if t.activeAltLiveLocked() {
		src = t.altHistoryLocked()
		if src == nil {
			return false, false
		}
	}
	return ghosttyRowWrap(src, gh.Point{Tag: gh.PointTagHistory, Y: uint32(index)}), true
}

// ghosttyRowWrap reads the wrap flag of the row at p.
func ghosttyRowWrap(term *gh.Terminal, p gh.Point) bool {
	ref, err := term.GridRef(p)
	if err != nil || ref == nil {
		return false
	}
	row, err := ref.Row()
	if err != nil || row == nil {
		return false
	}
	wrapped, err := row.Wrap()
	return err == nil && wrapped
}
