package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scratch terminal as a popup, for a session that cannot have scratch
// workspaces: a daemon from before them (the usual state right after an
// upgrade, until the daemon restarts), or a session with a client from
// before them attached. The daemon then makes the scratch terminal a popup on
// the current workspace, as those clients expect, and this client hides it by
// minimizing it, as they do. A scratch pane that is a popup always goes this
// way, so a popup from an older daemon hides whatever the session says now.

// scratchWorkspaces reports whether this client's scratch terminals are
// groups on their own workspaces. A session without a daemon always has them.
func (m *OS) scratchWorkspaces() bool {
	return !m.IsDaemonSession || m.DaemonClient == nil || !m.sessionScratchWSOff
}

// legacyScratchIndex is the index of the scratch popup kept under name, or -1.
func (m *OS) legacyScratchIndex(name string) int {
	m.forgetGoneDeadScratch()
	for i, w := range m.Windows {
		if isScratch(w) && w.IsPopup && scratchNameOf(w) == name && !m.deadScratch[w.ID] {
			return i
		}
	}
	return -1
}

// legacyScratchToggle is toggleScratch for a scratch popup.
func (m *OS) legacyScratchToggle(spec scratchSpec) tea.Cmd {
	if i := m.legacyScratchIndex(spec.Name); i >= 0 {
		w := m.Windows[i]
		if !w.Minimized && w.Workspace == m.CurrentWorkspace {
			m.legacyHideScratch(i)
		} else {
			m.rememberScratchReturn()
			m.legacyShowScratch(i)
		}
		return nil
	}
	// A group made while scratch workspaces were on cannot show now: an
	// older client attached would draw its panes nowhere.
	if m.scratchIndexNamed(spec.Name) >= 0 {
		m.ShowNotification("An older tuios client is attached to this session. Detach it to show this scratch terminal.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	plan := m.planScratch(spec.Name)
	switch plan.action {
	case scratchRefuse:
		m.ShowNotification(plan.refuse, "warning", m.Settings.NotificationDuration)
		return nil
	case scratchNothing:
		return nil
	}
	m.rememberScratchReturn()
	return m.createScratch(spec)
}

// legacyShowScratch puts the scratch popup at i on the current workspace, on
// top, with the keyboard.
func (m *OS) legacyShowScratch(i int) {
	w := m.Windows[i]
	w.Workspace = m.CurrentWorkspace
	w.Minimized = false
	if spec, ok := m.scratchSpecFor(scratchNameOf(w)); ok && m.UserConfig != nil {
		w.PopupWidth, w.PopupHeight = spec.Width, spec.Height
	}
	// One scratch terminal is on the screen at a time.
	for j, o := range m.Windows {
		if j != i && isScratch(o) && o.IsPopup && !o.Minimized {
			o.Minimized = true
			o.InvalidateCache()
		}
	}
	m.applyPopupRect(w, false)
	w.InvalidateCache()
	m.FocusWindow(i)
	if m.Mode != TerminalMode {
		m.EnterTerminalMode()
	}
	m.MarkAllDirty()
	m.SyncStateToDaemon()
}

// legacyHideScratch minimizes the scratch popup at i and gives the focus back
// to the pane that had it before the show, in the mode it was in.
func (m *OS) legacyHideScratch(i int) {
	m.Windows[i].Minimized = true
	m.Windows[i].InvalidateCache()
	back := -1
	for j, o := range m.Windows {
		if m.scratchReturnID != "" && o.ID == m.scratchReturnID && o.Workspace == m.CurrentWorkspace && !o.Minimized {
			back = j
			break
		}
	}
	if back >= 0 {
		m.FocusWindow(back)
	} else {
		m.FocusNextVisibleWindow()
	}
	switch {
	case !m.hasFocusedWindow() || isScratch(m.GetFocusedWindow()):
		m.FocusedWindow = -1
		if m.Mode == TerminalMode {
			m.ExitTerminalMode()
		}
	case m.scratchReturnMode != TerminalMode && m.Mode == TerminalMode:
		m.ExitTerminalMode()
	}
	m.scratchReturnID = ""
	m.MarkAllDirty()
	m.SyncStateToDaemon()
}

// shownLegacyScratch is the index of a scratch popup on the screen, or -1.
func (m *OS) shownLegacyScratch() int {
	for i, w := range m.Windows {
		if isScratch(w) && w.IsPopup && !w.Minimized && w.Workspace == m.CurrentWorkspace {
			return i
		}
	}
	return -1
}

// isLegacyScratch reports whether w is a scratch popup.
func isLegacyScratch(w *terminal.Window) bool {
	return isScratch(w) && w.IsPopup
}
