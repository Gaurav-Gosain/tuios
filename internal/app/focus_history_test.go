package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func TestDeleteFocusedWindowRestoresMostRecentFocus(t *testing.T) {
	m := NewOS(OSOptions{})
	m.CurrentWorkspace = 1
	m.Windows = []*terminal.Window{{ID: "a", Workspace: 1}, {ID: "b", Workspace: 1}, {ID: "c", Workspace: 1}}
	m.FocusedWindow = 2
	m.FocusHistory = map[int][]string{1: {"c", "b", "a"}}

	m.DeleteWindow(2)
	if m.FocusedWindow < 0 || m.Windows[m.FocusedWindow].ID != "b" {
		t.Fatalf("focused window after closing c = %v, want b", m.FocusedWindow)
	}
}
