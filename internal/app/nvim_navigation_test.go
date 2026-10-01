package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func TestNvimNavigationMovesOnlyTheFocusedPane(t *testing.T) {
	left := newTestWindow(t, "left", 40, 20)
	right := newTestWindow(t, "right", 40, 20)
	left.Workspace, right.Workspace = 1, 1
	left.X, right.X = 0, 40

	m := newTestOS(left)
	m.Windows = []*terminal.Window{left, right}
	m.CurrentWorkspace = 1
	m.Mode = TerminalMode
	m.FocusWindow(0)

	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, Direction: "right"})
	if m.FocusedWindow != 1 {
		t.Fatalf("focused window = %d, want right pane", m.FocusedWindow)
	}

	m.FocusWindow(0)
	m.onNvimNavigation(NvimNavigationMsg{WindowID: right.ID, Direction: "right"})
	if m.FocusedWindow != 0 {
		t.Fatal("a background pane moved focus")
	}
}

func TestNvimNavigatorStateIsPerPane(t *testing.T) {
	left := newTestWindow(t, "left", 40, 20)
	right := newTestWindow(t, "right", 40, 20)
	m := newTestOS(left)
	m.Windows = []*terminal.Window{left, right}
	m.FocusWindow(0)

	active := true
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	if !m.NvimNavigatorActive() {
		t.Fatal("focused navigator is not active")
	}

	m.FocusWindow(1)
	if m.NvimNavigatorActive() {
		t.Fatal("navigator state leaked to another pane")
	}
}
