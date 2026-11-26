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

// FilesWheel moves the cursor three rows.
func (m *OS) FilesWheel(up bool) {
	if up {
		m.FilesMove(-3)
		return
	}
	m.FilesMove(3)
}
