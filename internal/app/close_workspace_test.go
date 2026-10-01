package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// close_workspace asks first, then closes the panes of the current workspace
// and nothing else: another workspace and a scratch pane stay.
func TestCloseWorkspaceAsksThenClosesItsPanes(t *testing.T) {
	m := scratchOS(t, false)
	m.CurrentWorkspace = 2
	m.Windows = []*terminal.Window{
		{ID: "one", Workspace: 1},
		{ID: "a", Workspace: 2},
		{ID: "b", Workspace: 2},
		{ID: "scratch", Workspace: 2, IsScratch: true, IsPopup: true, IsFloating: true},
	}
	m.FocusedWindow = 1

	m.OpenWorkspaceClose()
	if !m.ShowSessionClose || m.SessionCloseWorkspace != 2 {
		t.Fatalf("no confirmation: show %v, workspace %d", m.ShowSessionClose, m.SessionCloseWorkspace)
	}
	if q := m.sessionCloseQuestion(); q != "Close every pane on workspace 2?" {
		t.Fatalf("question = %q", q)
	}
	if line := m.workspaceToll(2).Line(); !strings.HasPrefix(line, "2 panes") {
		t.Fatalf("toll = %q, want 2 panes", line)
	}
	if len(m.Windows) != 4 {
		t.Fatal("opening the dialog closed a pane")
	}

	m.SessionCloseActivate(SessionCloseRowClose)
	var left []string
	for _, w := range m.Windows {
		left = append(left, w.ID)
	}
	if strings.Join(left, ",") != "one,scratch" {
		t.Fatalf("windows left = %v, want one and scratch", left)
	}
	if m.ShowSessionClose || m.SessionCloseWorkspace != 0 {
		t.Fatal("the dialog stayed open")
	}

	// Cancel closes nothing, and an empty workspace asks nothing.
	m.CurrentWorkspace = 1
	m.OpenWorkspaceClose()
	m.SessionCloseActivate(SessionCloseRowCancel)
	if len(m.Windows) != 2 {
		t.Fatalf("cancel closed a pane: %d left", len(m.Windows))
	}
	m.CurrentWorkspace = 3
	m.OpenWorkspaceClose()
	if m.ShowSessionClose {
		t.Fatal("an empty workspace raised the dialog")
	}
}
