package app

import "github.com/Gaurav-Gosain/tuios/internal/terminal"

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
}
