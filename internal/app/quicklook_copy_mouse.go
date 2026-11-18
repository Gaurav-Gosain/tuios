package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The mouse over quick look. The panel is on top, so the pointer over it is
// the panel's: no hover, focus or motion reaches what is under it, and a
// press on it keeps its release.
//
// A drag over the text or the Markdown selects, as in a pane: the first move
// starts copy mode, implicit, over the rows as the panel draws them, and the
// release copies with appearance.copy_on_select. A double click selects a
// word and a triple click a line. The next plain click or key ends the
// selection. In copy mode the copy_mode key started, a click moves the
// cursor. The wheel scrolls the text and copy mode stays on. internal/input
// moves the cursor (copymode_grid_mouse.go); this file maps the screen to the
// text.

// QuickLookMouse is the press on the text, nil while quick look is closed.
func (m *OS) QuickLookMouse() *CopyMouseGesture {
	if !m.quickLook.open {
		return nil
	}
	return &m.quickLook.mouse
}

// QuickLookCopyCell is screen cell x, y as a cell of the text in the preview,
// and the rows the view shows. ok is false when the body shows no text. The
// cell may lie above or below the view.
func (m *OS) QuickLookCopyCell(x, y int) (cx, cy, rows int, ok bool) {
	ql := &m.quickLook
	if q := ql.copy; q != nil {
		if cm, _ := m.QuickLookCopy(); cm == nil {
			return 0, 0, 0, false
		}
		// The body starts one cell in from the panel's border.
		return x - (ql.box.X0 + 1 + q.x0), y - (ql.box.Y0 + 1 + q.y0), q.h, true
	}
	if !ql.open || ql.textW <= 0 {
		return 0, 0, 0, false
	}
	return x - (ql.box.X0 + 1 + ql.textX), y - (ql.box.Y0 + 1 + ql.textY), ql.bodyRows, true
}

// QuickLookCopyOnText reports whether screen cell x, y is on the text the
// preview shows.
func (m *OS) QuickLookCopyOnText(x, y int) bool {
	w := m.quickLook.textW
	if q := m.quickLook.copy; q != nil {
		w = q.w
	}
	cx, cy, rows, ok := m.QuickLookCopyCell(x, y)
	return ok && cx >= 0 && cx < w && cy >= 0 && cy < rows
}

// QuickLookMouseCopy is copy mode over the text for a mouse selection: the
// copy mode that runs, or a new one, implicit. ok is false when the body
// shows no text.
func (m *OS) QuickLookMouseCopy() (*terminal.CopyMode, terminal.CopyGrid, bool) {
	if cm, grid := m.QuickLookCopy(); cm != nil {
		return cm, grid, true
	}
	if !m.quickLookCopyBegin(true) {
		return nil, nil, false
	}
	cm, grid := m.QuickLookCopy()
	return cm, grid, cm != nil
}

// QuickLookCopyText is the text of copy mode's selection over Markdown, as
// the file says it: the rows of one paragraph joined with a space. ok is
// false over text, which the pane's own reading copies.
func (m *OS) QuickLookCopyText() (string, bool) {
	q := m.quickLook.copy
	if q == nil || q.md == nil {
		return "", false
	}
	cm := &q.cm
	return ckCopyRowsText(q.md, q.w, cm.VisualStart, cm.VisualEnd, cm.State == terminal.CopyModeVisualLine, cm.State == terminal.CopyModeVisualBlock), true
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

// QuickLookTakesMotion reports whether motion at x, y is quick look's: the
// pointer is over the panel, or a press on it holds the pointer. A pane drag
// or resize that crosses the panel goes on. Whatever the pointer hovered on
// its way in lets go of it: a link under the panel, a window button, the
// pointer's shape.
func (m *OS) QuickLookTakesMotion(x, y int) bool {
	ql := &m.quickLook
	if !ql.open {
		return false
	}
	if !ql.held && (m.Dragging || m.Resizing || m.ScrollbarDragging || !m.QuickLookContains(x, y)) {
		return false
	}
	m.clearLinkHover()
	m.WindowButtonHoverAt(-1, -1)
	m.ResetPointerShape()
	return true
}

// QuickLookContains reports whether screen cell x, y is on the open panel.
func (m *OS) QuickLookContains(x, y int) bool {
	return m.quickLook.open && m.quickLook.box.Contains(x, y)
}

// QuickLookLetGo ends a press on the panel, and reports whether there was one:
// its release is the panel's.
func (m *OS) QuickLookLetGo() bool {
	held := m.quickLook.held
	m.quickLook.held = false
	return held
}
