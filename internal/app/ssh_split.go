package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// SplitFocusedHorizontalSSH is SplitFocusedHorizontal that follows the
// focused pane into ssh.
func (m *OS) SplitFocusedHorizontalSSH() {
	m.splitFocused(layout.PreselectionDown, true)
}

// SplitFocusedVerticalSSH is SplitFocusedVertical that follows the focused
// pane into ssh.
func (m *OS) SplitFocusedVerticalSSH() {
	m.splitFocused(layout.PreselectionRight, true)
}

// NewWindowSSH is NewWindowHere that follows the focused pane into ssh.
func (m *OS) NewWindowSSH() {
	src := m.GetFocusedWindow()
	if src == nil {
		m.NewWindowHere()
		return
	}
	m.newWindowFollowingSSH(src)
}

// FollowSSHOnNewWindow reports appearance.new_window_follow_ssh: the ordinary
// split and new-window actions follow the focused pane into ssh.
func (m *OS) FollowSSHOnNewWindow() bool {
	return m.Settings.NewWindowFollowSSH
}

func (m *OS) newWindowFollowingSSH(src *terminal.Window) {
	if m.IsDaemonSession && m.DaemonClient != nil {
		if m.newWindowShouldPickHost() {
			m.OpenHostPicker()
			return
		}
		m.addDaemonWindow("", "", src.ID, nil)
		return
	}
	argv, why, ok := localSSHArgv(src)
	if ok {
		m.AddWindow("", argv...)
		return
	}
	m.AddWindow("")
	if why != "" {
		m.ShowNotification(why, "info", m.Settings.NotificationDuration)
	}
}

func localSSHArgv(w *terminal.Window) ([]string, string, bool) {
	if w == nil || w.Cmd == nil || w.Cmd.Process == nil || w.ProcessExited() {
		return nil, "", false
	}
	dir := ""
	if w.CwdHost != "" {
		dir = w.CwdElsewhereDir
	}
	argv, _, why, ok := session.SSHFollowArgv(w.Cmd.Process.Pid, w.CwdHost, dir)
	return argv, why, ok
}
