package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// filesDoubleClick is how soon a second click on the same row opens it.
const filesDoubleClick = 400 * time.Millisecond

// filesCovered reports whether a panel is open over the Files view.
func (m *OS) filesCovered() bool {
	stack := m.OverlayStack()
	return len(stack) > 0 && stack[len(stack)-1] != overlayKindFiles
}

// FilesClick acts on a click at x, y on the screen: a crumb goes to its
// folder, a row takes the cursor, and a second click on the cursor row
// opens it.
func (m *OS) FilesClick(x, y int) tea.Cmd {
	e := &m.explorer
	if y == e.hintsY {
		for _, h := range e.hints {
			if x >= h.x0 && x < h.x1 {
				return h.run(m)
			}
		}
		return nil
	}
	if y == e.crumbsY {
		for _, c := range e.crumbs {
			if x >= c.x0 && x < c.x1 {
				if c.dir == e.side.Dir {
					return nil
				}
				return m.filesRead(c.dir, "")
			}
		}
		return nil
	}
	if x < e.listX || x >= e.listX+e.listW || y < e.rowsTop || y >= e.rowsTop+e.rowsN {
		return nil
	}
	s := &e.side
	at := s.scroll + y - e.rowsTop
	if at >= len(s.rows) {
		return nil
	}
	now := time.Now()
	again := at == s.cursor && now.Sub(e.clickAt) < filesDoubleClick
	s.cursor, e.clickAt = at, now
	e.gen++
	if again {
		e.clickAt = time.Time{}
		return m.FilesOpen()
	}
	return nil
}

// filesWheelRows is how far one notch of the wheel scrolls a side.
const filesWheelRows = 3

// FilesWheel scrolls the rows three at a time. The cursor stays on its row
// while that row is on screen, and moves onto the screen when it is not.
func (m *OS) FilesWheel(up bool) {
	e := &m.explorer
	s := &e.side
	n := filesWheelRows
	if up {
		n = -n
	}
	last := max(len(s.rows)-e.rowsN, 0)
	s.scroll = max(min(s.scroll+n, last), 0)
	s.cursor = max(min(s.cursor, s.scroll+e.rowsN-1), s.scroll)
	s.cursor = max(min(s.cursor, len(s.rows)-1), 0)
	e.gen++
}
