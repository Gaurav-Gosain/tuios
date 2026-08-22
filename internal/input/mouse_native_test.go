package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// twoPaneOS builds an OS with two visible floating panes side by side.
func twoPaneOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "left", X: 0, Y: 0, Width: 50, Height: 30, Workspace: 1},
		{ID: "b", CustomName: "right", X: 55, Y: 0, Width: 50, Height: 30, Workspace: 1},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// TestContentClickEntersTerminalMode checks the newcomer path: in
// window-management mode, a plain left click on a pane's content (press and
// release without a drag) focuses the pane and enters terminal mode, so
// clicking is enough to start typing.
func TestContentClickEntersTerminalMode(t *testing.T) {
	o := twoPaneOS(t)
	if o.Mode != app.WindowManagementMode {
		t.Fatal("expected to start in window-management mode")
	}

	// (60, 10) is inside pane b's content area (border offset 1).
	o, _ = handleMouseClick(tea.MouseClickMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)
	if o.FocusedWindow != 1 {
		t.Fatalf("click did not focus pane b (focused=%d)", o.FocusedWindow)
	}
	o, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)

	if o.Mode != app.TerminalMode {
		t.Error("click on pane content did not enter terminal mode")
	}
}

// TestContentDragDoesNotEnterTerminalMode checks the other half of the
// gesture: a drag from the content area is a window move, and the release
// must not drop the user into terminal mode.
func TestContentDragDoesNotEnterTerminalMode(t *testing.T) {
	o := twoPaneOS(t)

	o, _ = handleMouseClick(tea.MouseClickMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)
	o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 70, Y: 15, Button: tea.MouseLeft}, o)
	o, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 70, Y: 15, Button: tea.MouseLeft}, o)

	if o.Mode != app.WindowManagementMode {
		t.Error("a content drag entered terminal mode; only a click may")
	}
}

// TestTitleBarClickStaysInWindowMode checks the title bar keeps its role as a
// drag handle: clicking it focuses but does not enter terminal mode.
func TestTitleBarClickStaysInWindowMode(t *testing.T) {
	o := twoPaneOS(t)

	// (60, 0) is pane b's top border row: outside the content area.
	o, _ = handleMouseClick(tea.MouseClickMsg{X: 60, Y: 0, Button: tea.MouseLeft}, o)
	o, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 60, Y: 0, Button: tea.MouseLeft}, o)

	if o.Mode != app.WindowManagementMode {
		t.Error("a title-bar click entered terminal mode; only content clicks may")
	}
}

// TestTerminalModeRightClickWithSelectionOpensMenu checks the terminal-mode
// right button: with an active selection it opens the selection menu; without
// one it opens nothing and never starts a resize under the shell.
//
// The selection is made with the mouse rather than by filling a field. Filling
// Window.SelectedText is what this test used to do, and that field is written
// by nothing a user can reach, so it stood in for a selection the pane could
// never actually be in.
func TestTerminalModeRightClickWithSelectionOpensMenu(t *testing.T) {
	o, _ := selectPane(t, "alpha bravo charlie")
	pressAt(o, 0, 0)
	dragTo(o, 10, 0)
	release(o, 10, 0)

	o, _ = handleMouseClick(tea.MouseClickMsg{X: 10, Y: 10, Button: tea.MouseRight}, o)
	if !o.ContextMenuActive() {
		t.Fatal("right-click with a selection did not open the selection menu")
	}
	seen := map[string]bool{}
	for _, it := range o.ContextMenu.Items {
		seen[it.Action] = true
	}
	for _, action := range []string{"copy_selection", "paste_clipboard", "clear_selection"} {
		if !seen[action] {
			t.Errorf("selection menu is missing the %q row", action)
		}
	}
	if o.Resizing {
		t.Error("the selection menu right-click also started a resize")
	}
}

// TestTerminalModeRightClickWithoutSelectionPassesThrough checks a plain
// right-click in terminal mode with nothing selected opens no menu and starts
// no resize: the click belongs to the pane.
func TestTerminalModeRightClickWithoutSelectionPassesThrough(t *testing.T) {
	o := twoPaneOS(t)
	o.Mode = app.TerminalMode

	o, _ = handleMouseClick(tea.MouseClickMsg{X: 10, Y: 10, Button: tea.MouseRight}, o)
	if o.ContextMenuActive() {
		t.Error("right-click without a selection opened a menu")
	}
	if o.Resizing || o.Dragging {
		t.Error("right-click without a selection started a window gesture under the shell")
	}
	if o.Mode != app.TerminalMode {
		t.Error("right-click without a selection left terminal mode")
	}
}

// TestFocusFollowsMouse covers the opt-in hover-to-focus behavior: on, motion
// over another pane focuses it (without entering terminal mode); off, motion
// changes nothing; and chrome or an in-progress gesture never steals focus.
func TestFocusFollowsMouse(t *testing.T) {
	setFFM := func(t *testing.T, v bool) {
		t.Helper()
		prev := config.FocusFollowsMouse
		config.FocusFollowsMouse = v
		t.Cleanup(func() { config.FocusFollowsMouse = prev })
	}

	t.Run("on: motion over pane b focuses it", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60, Y: 10}, o)
		if o.FocusedWindow != 1 {
			t.Errorf("focus = %d, want pane b (1)", o.FocusedWindow)
		}
		if o.Mode != app.WindowManagementMode {
			t.Error("hover focus entered terminal mode; only a click may")
		}
	})

	t.Run("off: motion never changes focus", func(t *testing.T) {
		setFFM(t, false)
		o := twoPaneOS(t)
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60, Y: 10}, o)
		if o.FocusedWindow != 0 {
			t.Errorf("focus = %d, want unchanged (0)", o.FocusedWindow)
		}
	})

	// A setting called focus-follows-mouse that stops working in the mode the
	// user spends their time in reads as broken, so it glides there too.
	t.Run("on: terminal mode glides too", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		o.Mode = app.TerminalMode
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60, Y: 10}, o)
		if o.FocusedWindow != 1 {
			t.Errorf("focus = %d, want the hovered pane (1)", o.FocusedWindow)
		}
	})

	t.Run("rail focus suppresses hover-focus", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		o.SidebarFocused = true
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60, Y: 10}, o)
		if o.FocusedWindow != 0 {
			t.Errorf("focus = %d, want unchanged (0) while the rail owns the keyboard", o.FocusedWindow)
		}
	})

	t.Run("on: motion over the sidebar band does not steal focus", func(t *testing.T) {
		setFFM(t, true)
		prevEnabled, prevPos, prevWidth := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = true, "left", 28
		t.Cleanup(func() {
			config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = prevEnabled, prevPos, prevWidth
		})
		o := twoPaneOS(t)
		o.FocusedWindow = 1
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 3, Y: 10}, o) // in the band, over pane a
		if o.FocusedWindow != 1 {
			t.Errorf("motion over the sidebar band changed focus to %d", o.FocusedWindow)
		}
	})

	t.Run("on: motion over the dock band does not steal focus", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		o.FocusedWindow = 1
		o.Windows[0].Height = o.Height // pane a extends under the dock band
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 10, Y: o.Height - 1}, o)
		if o.FocusedWindow != 1 {
			t.Errorf("motion over the dock band changed focus to %d", o.FocusedWindow)
		}
	})

	t.Run("on: motion over an open overlay does not steal focus", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		o.FocusedWindow = 1
		o.OpenCommandPalette()
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 10, Y: 10}, o)
		if o.FocusedWindow != 1 {
			t.Errorf("motion with an overlay open changed focus to %d", o.FocusedWindow)
		}
	})

	t.Run("on: no focus change during a drag", func(t *testing.T) {
		setFFM(t, true)
		o := twoPaneOS(t)
		// Grab pane a's title bar and drag across pane b.
		o, _ = handleMouseClick(tea.MouseClickMsg{X: 10, Y: 0, Button: tea.MouseLeft}, o)
		if o.FocusedWindow != 0 || !o.Dragging {
			t.Fatalf("drag setup failed: focused=%d dragging=%v", o.FocusedWindow, o.Dragging)
		}
		o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)
		if o.FocusedWindow != 0 {
			t.Errorf("a drag crossing pane b moved focus to %d", o.FocusedWindow)
		}
		o, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)
	})
}
