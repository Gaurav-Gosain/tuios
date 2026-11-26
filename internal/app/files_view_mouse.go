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

// filesSideAt is the index of the side drawn under x, y on the tab on
// screen, or -1.
func (m *OS) filesSideAt(x, y int) int {
	t := m.filesTabCur()
	for i, s := range t.sides {
		if s != nil && s.box.w > 0 && s.box.contains(x, y) && (t.shown() || i == t.focus) {
			return i
		}
	}
	return -1
}

// FilesClick acts on a click at x, y on the screen: a footer hint runs, a
// tab goes on screen, the divider is held, a click in a side gives it the
// keys, a crumb goes to its folder, a row takes the cursor, and a second
// click on the cursor row opens it.
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
	if cmd, ok := m.FilesCopiesClick(x, y); ok {
		return cmd
	}
	if y == e.tabsY {
		for i, h := range e.segHits {
			if h.x1 > h.x0 && x >= h.x0 && x < h.x1 {
				m.filesFocusSide(i)
				return nil
			}
		}
		for i, h := range e.tabHits {
			if x >= h.x0 && x < h.x1 {
				m.FilesGoTab(i)
				return nil
			}
		}
		return nil
	}
	now := time.Now()
	// The divider takes the cells either side of it too: they are the sides'
	// margins, and a line one cell wide is hard to hit.
	if e.divX >= 0 && x >= e.divX-1 && x <= e.divX+1 && y > e.tabsY+1 {
		if now.Sub(e.clickAt) < filesDoubleClick && e.clickDiv {
			m.FilesDividerReset()
			e.clickAt, e.clickDiv = time.Time{}, false
			return nil
		}
		e.dividerDrag, e.clickAt, e.clickDiv = true, now, true
		return nil
	}
	e.clickDiv = false
	i := m.filesSideAt(x, y)
	if i < 0 {
		return nil
	}
	m.filesFocusSide(i)
	s := m.filesCur()
	if y == s.crumbsY {
		for _, c := range s.crumbs {
			if x >= c.x0 && x < c.x1 {
				if c.dir == s.Dir {
					return nil
				}
				return m.filesRead(c.dir, "")
			}
		}
		return nil
	}
	if y < s.rowsTop || y >= s.rowsTop+s.rowsN {
		return nil
	}
	at := s.scroll + y - s.rowsTop
	if at >= len(s.rows) {
		return nil
	}
	again := at == s.cursor && now.Sub(e.clickAt) < filesDoubleClick
	s.cursor, e.clickAt = at, now
	e.gen++
	if again {
		e.clickAt = time.Time{}
		return m.FilesOpen()
	}
	return nil
}

// FilesMotion moves a held divider. It reports whether it did.
func (m *OS) FilesMotion(x int) bool {
	if !m.explorer.dividerDrag {
		return false
	}
	m.filesDividerAt(x)
	return true
}

// FilesRelease lets go of a held divider.
func (m *OS) FilesRelease() { m.explorer.dividerDrag = false }

// filesWheelRows is how far one notch of the wheel scrolls a side.
const filesWheelRows = 3

// FilesWheel scrolls the rows of the side under x, y three at a time. The
// cursor stays on its row while that row is on screen, and moves onto the
// screen when it is not.
func (m *OS) FilesWheel(x, y int, up bool) {
	e := &m.explorer
	t := m.filesTabCur()
	s := t.cur()
	if i := m.filesSideAt(x, y); i >= 0 {
		s = t.sides[i]
	}
	n := filesWheelRows
	if up {
		n = -n
	}
	last := max(len(s.rows)-s.rowsN, 0)
	s.scroll = max(min(s.scroll+n, last), 0)
	s.cursor = max(min(s.cursor, s.scroll+s.rowsN-1), s.scroll)
	s.cursor = max(min(s.cursor, len(s.rows)-1), 0)
	e.gen++
}
