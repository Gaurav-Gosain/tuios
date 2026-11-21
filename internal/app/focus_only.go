package app

import "fmt"

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
