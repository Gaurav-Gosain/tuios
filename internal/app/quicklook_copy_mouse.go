package app

import (
	tea "charm.land/bubbletea/v2"
)

// The mouse in copy mode over quick look's text: a click moves the cursor,
// a drag selects, a double click a word and a triple click a line, and the
// release copies with appearance.copy_on_select, as in a pane. The wheel
// scrolls the text and copy mode stays on. internal/input moves the cursor
// (copymode_grid_mouse.go); this file maps the screen to the text.

// QuickLookMouse is the press on the text in copy mode.
func (m *OS) QuickLookMouse() *CopyMouseGesture {
	if q := m.quickLook.copy; q != nil {
		return &q.mouse
	}
	return nil
}

// QuickLookCopyCell is screen cell x, y as a cell of copy mode's view in the
// preview, and the rows the view shows. ok is false when copy mode is off or
// the cell is off the panel. The cell may lie above or below the view.
func (m *OS) QuickLookCopyCell(x, y int) (cx, cy, rows int, ok bool) {
	ql := &m.quickLook
	q := ql.copy
	if cm, _ := m.QuickLookCopy(); cm == nil || q == nil {
		return 0, 0, 0, false
	}
	// The body starts one cell in from the panel's border.
	cx, cy = x-(ql.box.X0+1+q.x0), y-(ql.box.Y0+1+q.y0)
	return cx, cy, q.h, true
}

// QuickLookCopyOnText reports whether screen cell x, y is on the text copy
// mode shows.
func (m *OS) QuickLookCopyOnText(x, y int) bool {
	cx, cy, rows, ok := m.QuickLookCopyCell(x, y)
	return ok && cx >= 0 && cx < m.quickLook.copy.w && cy >= 0 && cy < rows
}

// QuickLookCopyFinish copies text, a mouse selection's, when
// appearance.copy_on_select is on.
func (m *OS) QuickLookCopyFinish(text string) tea.Cmd {
	if text == "" || !m.Settings.CopyOnSelect {
		return nil
	}
	return tea.Batch(m.CopyToClipboard(text), m.SaveToPasteBuffers(text))
}

// quickLookCopyWheel scrolls copy mode's view by delta rows, up negative.
func (m *OS) quickLookCopyWheel(delta int) bool {
	q := m.quickLook.copy
	if cm, _ := m.QuickLookCopy(); cm == nil || q == nil {
		return false
	}
	q.SetScrollOffset(min(max(q.offset-delta, 0), q.ScrollbackLen()))
	q.cm.ScrollOffset = q.offset
	return true
}
