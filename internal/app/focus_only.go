package app

import "fmt"

// Focus only (ctrl+b m f, minimize_prefix_focus_only, issue 1723) minimizes
// every other pane on the workspace and keeps the focused one. A second
// press brings back the panes it hid, and only those: a pane minimized
// before stays minimized. The panes it hid are kept per workspace by id, so
// a pane closed or restored by hand in between is skipped.

// ToggleFocusOnly hides the other panes on the current workspace, or brings
// back the ones the last press there hid.
func (m *OS) ToggleFocusOnly() {
	ws := m.CurrentWorkspace
	if hidden := m.focusOnlyHidden[ws]; len(hidden) > 0 {
		delete(m.focusOnlyHidden, ws)
		if n := m.restoreFocusOnly(hidden); n > 0 {
			m.ShowNotification(fmt.Sprintf("Restored %s.", countOf(n, "pane")), "info", m.Settings.NotificationDuration)
			return
		}
		// Every pane it hid came back or closed some other way, so the press
		// hides again.
	}

	focused := m.GetFocusedWindow()
	if focused == nil {
		return
	}
	var hidden []string
	for i := 0; i < len(m.Windows); i++ {
		w := m.Windows[i]
		if w == nil || w == focused || w.Workspace != ws || w.Minimized || w.IsScratch || w.IsPopup {
			continue
		}
		m.MinimizeWindow(i)
		if w.Minimized {
			hidden = append(hidden, w.ID)
		}
	}
	if len(hidden) == 0 {
		m.ShowNotification("No other pane to hide.", "info", m.Settings.NotificationDuration)
		return
	}
	if m.focusOnlyHidden == nil {
		m.focusOnlyHidden = map[int][]string{}
	}
	m.focusOnlyHidden[ws] = hidden
	if i := m.windowIndex(focused); i >= 0 {
		m.FocusWindow(i)
	}
	m.ShowNotification(fmt.Sprintf("Minimized %s. Press again to restore.", countOf(len(hidden), "other pane")), "info", m.Settings.NotificationDuration)
}

// restoreFocusOnly restores the panes in ids that are still minimized and
// gives the focus back to the pane that had it. It returns how many came back.
func (m *OS) restoreFocusOnly(ids []string) int {
	focused := m.GetFocusedWindow()
	n := 0
	for _, id := range ids {
		if i := m.windowIndexByID(id); i >= 0 && m.Windows[i].Minimized {
			m.RestoreWindow(i)
			n++
		}
	}
	if focused != nil {
		if i := m.windowIndex(focused); i >= 0 {
			m.FocusWindow(i)
		}
	}
	return n
}
