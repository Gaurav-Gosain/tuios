package app

import (
	"testing"
	"time"

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
	m.Settings.NvimNavigation = true
	m.FocusWindow(0)

	active := true
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	m.ArmNvimNavigation("right")
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
	m.Settings.NvimNavigation = true
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

func TestNvimNavigationRequiresMatchingForwardedKey(t *testing.T) {
	left := newTestWindow(t, "left", 40, 20)
	right := newTestWindow(t, "right", 40, 20)
	left.Workspace, right.Workspace = 1, 1
	left.X, right.X = 0, 40

	m := newTestOS(left)
	m.Windows = []*terminal.Window{left, right}
	m.CurrentWorkspace = 1
	m.Mode = TerminalMode
	m.Settings.NvimNavigation = true
	m.FocusWindow(0)
	active := true
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})

	// Terminal output alone never moves focus.
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, Direction: "right"})
	if m.FocusedWindow != 0 {
		t.Fatal("an unsolicited OSC focus request moved focus")
	}

	m.ArmNvimNavigation("right")
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, Direction: "left"})
	if m.FocusedWindow != 0 {
		t.Fatal("an OSC request in a different direction moved focus")
	}
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, Direction: "right"})
	if m.FocusedWindow != 1 {
		t.Fatal("the matching reply to a forwarded focus key did not move focus")
	}
}

func TestNvimNavigationRejectsExpiredOrDisabledRequests(t *testing.T) {
	left := newTestWindow(t, "left", 40, 20)
	right := newTestWindow(t, "right", 40, 20)
	left.Workspace, right.Workspace = 1, 1
	left.X, right.X = 0, 40

	m := newTestOS(left)
	m.Windows = []*terminal.Window{left, right}
	m.CurrentWorkspace = 1
	m.Mode = TerminalMode
	m.Settings.NvimNavigation = true
	m.FocusWindow(0)
	active := true
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	m.pendingNvimNavigation = &pendingNvimNavigation{WindowID: left.ID, Direction: "right", ExpiresAt: time.Now().Add(-time.Millisecond)}
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, Direction: "right"})
	if m.FocusedWindow != 0 {
		t.Fatal("an expired focus authorization moved focus")
	}

	m.Settings.NvimNavigation = false
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	if m.NvimNavigatorActive() {
		t.Fatal("disabled navigation accepted an active announcement")
	}
}

func TestNvimNavigationClearsOnAltScreenExitAndWindowClose(t *testing.T) {
	left := newTestWindow(t, "left", 40, 20)
	m := newTestOS(left)
	m.Settings.NvimNavigation = true
	m.setupNvimNavigation(left)
	active := true
	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	m.ArmNvimNavigation("right")

	_, _ = left.Terminal.Write([]byte("\x1b[?1049h\x1b[?1049l"))
	msg := <-m.PendingNvimNavigation
	if !msg.Reset {
		t.Fatal("leaving the alternate screen did not reset navigation")
	}
	m.onNvimNavigation(msg)
	if m.NvimNavigatorActive() || m.pendingNvimNavigation != nil {
		t.Fatal("alternate-screen exit kept navigation state")
	}

	m.onNvimNavigation(NvimNavigationMsg{WindowID: left.ID, State: &active})
	m.ArmNvimNavigation("right")
	m.DeleteWindow(0)
	if m.nvimNavigators[left.ID] || m.pendingNvimNavigation != nil {
		t.Fatal("closing a pane kept navigation state")
	}
}
