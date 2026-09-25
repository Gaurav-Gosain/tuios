package input

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestRailKillKeyOnATerminalRowShowsThatPanesMenu drives the key the maintainer
// pressed: x with the cursor on a terminal row used to put the session's menu
// on screen. The assertion is the rendered frame, because the complaint was
// about what the menu said, not about which struct it was built from.
func TestRailKillKeyOnATerminalRowShowsThatPanesMenu(t *testing.T) {
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SessionName = "main"
	o.IsDaemonSession = true
	o.EnterSidebarFocus()
	_ = o.View() // the rail publishes its rows as it draws

	// Park the cursor on the "right" pane's terminal row.
	if !railCursorToWindow(t, o, "b") {
		t.Fatal("the rail drew no terminal row for the right pane")
	}

	o, _ = HandleKeyPress(press("x"), o)

	frame := o.View().Content
	if !strings.Contains(frame, "Close pane") {
		t.Error("x on a terminal row drew no pane menu")
	}
	if strings.Contains(frame, "Kill session") {
		t.Error("x on a terminal row drew the session menu, not the row's own")
	}
	if strings.Contains(frame, "Detach") {
		t.Error("x on a terminal row drew session lifecycle rows")
	}
}

// TestRailKillKeyOnASessionRowStillShowsTheSessionMenu is the other half: the
// fix must not cost the row kind the key already handled.
func TestRailKillKeyOnASessionRowStillShowsTheSessionMenu(t *testing.T) {
	prev := config.Global.SidebarEnabled
	config.Global.SidebarEnabled = true
	t.Cleanup(func() { config.Global.SidebarEnabled = prev })

	o := twoPaneOS(t)
	o.SessionName = "main"
	o.IsDaemonSession = true
	o.EnterSidebarFocus()
	_ = o.View()

	o, _ = HandleKeyPress(press("x"), o)

	if frame := o.View().Content; !strings.Contains(frame, "Kill session") {
		t.Error("x on the session row the rail opens on drew no session menu")
	}
}

// railCursorToWindow parks the rail cursor on a pane's terminal row.
func railCursorToWindow(t *testing.T, o *app.OS, windowID string) bool {
	t.Helper()
	for i, r := range o.SidebarNav {
		if r.WindowID == windowID {
			o.SidebarCursor = i
			return true
		}
	}
	return false
}
