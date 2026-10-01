package app

import (
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// close_workspace closes every pane on the current workspace, like tmux
// kill-window. It asks first, in the session-close dialog, every time: the
// dialog counts the panes and the agents it would take down, which is the
// reason to read it. tuios close-workspace is the same for a script.
//
// A scratch pane stays unless the workspace is its scratch workspace, as in
// the close-workspace verb (session.WindowsToCloseOnWorkspace).

// workspacePanes is the panes close_workspace closes on ws, in window order.
func (m *OS) workspacePanes(ws int) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || w.Workspace != ws || (w.IsScratch && !session.IsScratchWorkspace(ws)) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// workspaceToll counts what closing ws would take down.
func (m *OS) workspaceToll(ws int) sessionToll {
	var t sessionToll
	for _, w := range m.workspacePanes(ws) {
		t.Panes++
		t.count(w.AgentState)
	}
	return t
}

// workspaceLabel names a workspace in the dialog: "workspace 2", or its name
// after it when it has one.
func (m *OS) workspaceLabel(ws int) string {
	label := fmt.Sprintf("workspace %d", ws)
	if name := m.WorkspaceNames[ws]; name != "" {
		label += " (" + printableTitle(name) + ")"
	}
	return label
}

// sessionCloseTitle and sessionCloseLabel are the dialog's frame title and its
// destructive row, for the session or for a workspace.
func (m *OS) sessionCloseTitle() string {
	if m.SessionCloseWorkspace != 0 {
		return "close workspace"
	}
	return "close session"
}

func (m *OS) sessionCloseLabel() string {
	if m.SessionCloseWorkspace != 0 {
		return "Close workspace"
	}
	return "Close session"
}

// OpenWorkspaceClose raises the confirmation for the current workspace. A
// workspace with no pane to close says so and asks nothing.
func (m *OS) OpenWorkspaceClose() {
	ws := m.CurrentWorkspace
	if len(m.workspacePanes(ws)) == 0 {
		m.ShowNotification(fmt.Sprintf("Workspace %d has no panes to close.", ws), "info", m.Settings.NotificationDuration)
		return
	}
	m.OpenSessionClose()
	m.SessionCloseWorkspace = ws
}

// CloseWorkspacePanes closes every pane close_workspace closes on ws. In a
// daemon session each close is an intent the daemon answers, as for one pane.
func (m *OS) CloseWorkspacePanes(ws int) {
	panes := m.workspacePanes(ws)
	for _, w := range panes {
		idx := m.windowIndex(w)
		if idx < 0 {
			continue
		}
		m.FireHook(hooks.AfterCloseWindow, w.ID, w.Title())
		m.DeleteWindow(idx)
	}
	m.ShowNotification(fmt.Sprintf("Closed %s on workspace %d.", countOf(len(panes), "pane"), ws), "info", m.Settings.NotificationDuration)
}

// windowIndex is the index of w in m.Windows, or -1.
func (m *OS) windowIndex(w *terminal.Window) int {
	return slices.Index(m.Windows, w)
}
