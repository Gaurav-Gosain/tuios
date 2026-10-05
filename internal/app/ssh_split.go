package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ssh-aware splits (discussion #468).
//
// split_ssh_horizontal, split_ssh_vertical and new_window_ssh open a pane that
// runs the same ssh as the focused pane, to the same machine with the same
// options. With appearance.new_window_follow_ssh on, the ordinary split and
// new-window actions do the same. A pane that does not run ssh gets an
// ordinary new pane, so the keys are safe to use everywhere.
//
// Whoever runs the pane's processes reads them: the daemon in a daemon session
// (see session/ssh_follow.go), this client when it runs the pane itself. The
// argv is exec'd as it is and never goes through a shell here.

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

// newWindowFollowingSSH makes a new window that runs src's ssh, or an
// ordinary one when src runs none.
func (m *OS) newWindowFollowingSSH(src *terminal.Window) {
	if m.IsDaemonSession && m.DaemonClient != nil {
		// A global session asks which machine first, as for every new pane,
		// and the answer is the machine.
		if m.newWindowShouldPickHost() {
			m.OpenHostPicker()
			return
		}
		m.addDaemonWindow("", "", src.ID, nil)
		return
	}
	if argv, ok := localSSHArgv(src); ok {
		m.AddWindow("", argv...)
		return
	}
	m.AddWindow("")
}

// localSSHArgv is the ssh command line of a pane this client runs itself.
func localSSHArgv(w *terminal.Window) ([]string, bool) {
	if w == nil || w.Cmd == nil || w.Cmd.Process == nil || w.ProcessExited() {
		return nil, false
	}
	dir := ""
	if w.CwdHost != "" {
		dir = w.CwdElsewhereDir
	}
	return session.SSHFollowArgv(w.Cmd.Process.Pid, w.CwdHost, dir)
}
