package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// WindowModeOff reports whether keybindings.window_mode is false.
func (m *OS) WindowModeOff() bool { return m.Settings.NoWindowMode }

func (m *OS) windowModeRefused() bool {
	return m.Settings.NoWindowMode && m.GetFocusedWindow() != nil
}

func (m *OS) settleWindowModeOff() {
	if !m.Settings.NoWindowMode || m.Mode != WindowManagementMode || m.modeBorrowed() {
		return
	}
	if m.GetFocusedWindow() == nil {
		return
	}
	m.ChooseMode(TerminalMode, ModeCauseAction)
}

// NoteWindowModeOff is what a key that only enters window mode does while
// window mode is off. The first such key in a client shows a notice that
// says so and names the leader. The keys after it do nothing.
func (m *OS) NoteWindowModeOff() {
	if m.windowModeNoted {
		return
	}
	m.windowModeNoted = true
	m.ShowNotification("Window mode is off. Press "+config.KeyLabel(m.Settings.LeaderKey)+" for window actions.",
		"info", m.Settings.NotificationDuration)
}
