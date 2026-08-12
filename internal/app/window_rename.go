package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// BeginRenameWindow starts an inline rename of a window, seeded with the name it
// already carries. There is one rename in flight at a time and it names the
// window it targets, so the editor can be drawn wherever that window shows: the
// pane's title bar, its sidebar row, or both at once.
func (m *OS) BeginRenameWindow(w *terminal.Window) {
	if w == nil {
		return
	}
	m.RenamingWindow = true
	m.RenameTargetID = w.ID
	m.RenameBuffer = w.CustomName
	w.InvalidateCache()
}

// RenameTarget is the window an in-progress rename applies to, or nil when no
// rename is running or the window went away under it.
func (m *OS) RenameTarget() *terminal.Window {
	if !m.RenamingWindow {
		return nil
	}
	if i := m.windowIndexByID(m.RenameTargetID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// EndRenameWindow clears the rename state, committed or cancelled.
func (m *OS) EndRenameWindow() {
	m.RenamingWindow = false
	m.RenameTargetID = ""
	m.RenameBuffer = ""
	m.renameHit = overlay.Rect{}
}

// RenameMouseClick routes a click while the rename dialog is up: inside is a
// no-op (the field has no clickable parts), outside cancels. The dialog is
// modal to the mouse either way, so a stray click can never leave an editor
// open over a pane the user has moved on to. The mouse is never required: the
// same keys that opened it commit or cancel it.
func (m *OS) RenameMouseClick(x, y int) bool {
	if !m.RenamingWindow {
		return false
	}
	if !m.renameHit.Contains(x, y) {
		m.EndRenameWindow()
	}
	return true
}
