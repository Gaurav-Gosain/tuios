package app

import (
	"fmt"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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

func (m *OS) workspaceToll(ws int) sessionToll {
	var t sessionToll
	for _, w := range m.workspacePanes(ws) {
		t.Panes++
		t.count(w.AgentState)
	}
	return t
}

func (m *OS) workspaceLabel(ws int) string {
	label := fmt.Sprintf("workspace %d", m.workspaceDisplayNumber(ws))
	if name := m.workspaceGivenName(ws); name != "" {
		label += " (" + printableTitle(name) + ")"
	}
	return label
}

func (m *OS) sessionCloseTitle() string {
	if m.SessionCloseWindow != "" {
		return "close pane"
	}
	if m.SessionCloseWorkspace != 0 {
		return "close " + m.workspaceWord()
	}
	return "close session"
}

func (m *OS) sessionCloseLabel() string {
	if m.SessionCloseWindow != "" {
		return "Close pane"
	}
	if m.SessionCloseWorkspace != 0 {
		return "Close " + m.workspaceWord()
	}
	return "Close session"
}

// OpenWorkspaceClose raises the confirmation for the workspace the user meant:
// the one whose tab menu the action came from, or the one they are on when it
// came from a key or the palette. The dialog counts, and its answer closes, that
// workspace's panes. A workspace with no pane to close says so and asks
// nothing.
func (m *OS) OpenWorkspaceClose() {
	ws := m.TakeMenuWorkspace()
	if ws <= 0 {
		ws = m.CurrentWorkspace
	}
	if len(m.workspacePanes(ws)) == 0 {
		m.ShowNotification(fmt.Sprintf("Workspace %d has no panes to close.", m.workspaceDisplayNumber(ws)), "info", m.Settings.NotificationDuration)
		return
	}
	m.SessionCloseWorkspace = ws
	m.OpenSessionClose()
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
	m.ShowNotification(fmt.Sprintf("Closed %s on workspace %d.", countOf(len(panes), "pane"), m.workspaceDisplayNumber(ws)), "info", m.Settings.NotificationDuration)
}

func (m *OS) windowIndex(w *terminal.Window) int {
	return slices.Index(m.Windows, w)
}
