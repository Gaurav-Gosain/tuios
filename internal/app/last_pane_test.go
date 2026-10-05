package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// lastPaneTestOS is three panes on one workspace, focus on the first.
func lastPaneTestOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 120, 40)
	m.SessionName = "main"
	m.WorkspaceFocus = map[int]int{}
	m.Windows = []*terminal.Window{
		{ID: "one", CustomName: "one", Width: 40, Height: 20, Workspace: 1},
		{ID: "two", CustomName: "two", Width: 40, Height: 20, Workspace: 1},
		{ID: "three", CustomName: "three", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	return m
}

// TestLastPaneAlternates checks the flip invariant: alternating presses walk
// between the last two panes, whatever the route focus took between them.
func TestLastPaneAlternates(t *testing.T) {
	m := lastPaneTestOS(t)

	if m.LastPane() {
		t.Fatal("nothing was focused before, yet LastPane claimed a target")
	}
	m.FocusWindow(1)
	m.FocusWindow(2)
	if !m.LastPane() || m.FocusedWindow != 1 {
		t.Fatalf("first flip landed on %d, want 1", m.FocusedWindow)
	}
	if !m.LastPane() || m.FocusedWindow != 2 {
		t.Fatalf("second flip landed on %d, want 2", m.FocusedWindow)
	}
}

// TestLastPaneAcrossWorkspaces checks a flip that has to cross a workspace
// switch lands on the right pane, since every jump funnels through
// FocusWindow's workspace switch.
func TestLastPaneAcrossWorkspaces(t *testing.T) {
	m := lastPaneTestOS(t)
	m.Windows[2].Workspace = 3

	m.FocusWindow(2)
	if m.CurrentWorkspace != 3 {
		t.Fatalf("workspace = %d, want 3 after the jump", m.CurrentWorkspace)
	}
	if !m.LastPane() || m.FocusedWindow != 0 || m.CurrentWorkspace != 1 {
		t.Fatalf("flip back landed on pane %d workspace %d, want pane 0 workspace 1",
			m.FocusedWindow, m.CurrentWorkspace)
	}
}

// TestLastPaneDeadTarget checks a closed previous pane reports no move instead
// of landing on whatever pane reused the index.
func TestLastPaneDeadTarget(t *testing.T) {
	m := lastPaneTestOS(t)
	m.FocusWindow(1)
	m.FocusWindow(2)                             // previous is now pane two
	m.Windows = []*terminal.Window{m.Windows[0]} // two and three closed
	m.FocusedWindow = 0

	if m.LastPane() {
		t.Fatal("a closed target claimed to move")
	}
	if m.FocusedWindow != 0 {
		t.Fatalf("focus moved to %d on a dead target", m.FocusedWindow)
	}
}

// TestTuiosLinkTarget checks the two URI shapes the wait bar links with, and
// that foreign schemes and malformed paths pass through unclaimed.
func TestTuiosLinkTarget(t *testing.T) {
	cases := []struct {
		url  string
		want NotifTarget
		ok   bool
	}{
		{"tuios://window/abc", NotifTarget{WindowID: "abc"}, true},
		{"tuios://session/main/window/abc", NotifTarget{SessionID: "main", WindowID: "abc"}, true},
		{"tuios://session/main/window/abc?label=x", NotifTarget{SessionID: "main", WindowID: "abc"}, true},
		{"tuios://pip/abc", NotifTarget{WindowID: "abc", Preview: true}, true},
		{"tuios://session/main/pip/abc", NotifTarget{SessionID: "main", WindowID: "abc", Preview: true}, true},
		{"tuios://pip/", NotifTarget{}, false},
		{"tuios://session/main/pip/", NotifTarget{}, false},
		{"https://example.com", NotifTarget{}, false},
		{"tuios://window/", NotifTarget{}, false},
		{"tuios://session/main", NotifTarget{}, false},
		{"tuios://pane/abc", NotifTarget{}, false},
		{"file:///tmp/x", NotifTarget{}, false},
	}
	for _, tc := range cases {
		got, ok := tuiosLinkTarget(tc.url)
		if ok != tc.ok || got != tc.want {
			t.Errorf("tuiosLinkTarget(%q) = %+v, %v; want %+v, %v", tc.url, got, ok, tc.want, tc.ok)
		}
	}
}

// TestOpenLinkTuiosURIFocuses checks the runtime half of the wait bar's click
// path: OpenLink on our own scheme focuses the named pane in-process, and a
// dead target reports the miss instead of moving.
func TestOpenLinkTuiosURIFocuses(t *testing.T) {
	m := lastPaneTestOS(t)

	m.OpenLink("tuios://window/two")
	if m.FocusedWindow != 1 {
		t.Fatalf("focus = %d, want the pane the link named", m.FocusedWindow)
	}

	m.OpenLink("tuios://window/ghost")
	if m.FocusedWindow != 1 {
		t.Fatalf("a dead link moved focus to %d", m.FocusedWindow)
	}
	found := false
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "closed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no notification about the dead pane, got %v", m.Notifications)
	}
	if len(m.Notifications) != 1 {
		t.Fatalf("dead pane produced %d notifications, want one (the jump's own)", len(m.Notifications))
	}
}
