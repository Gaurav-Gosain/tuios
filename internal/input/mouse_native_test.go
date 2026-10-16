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
		prev := config.Global.FocusFollowsMouse
		config.Global.FocusFollowsMouse = v
		t.Cleanup(func() { config.Global.FocusFollowsMouse = prev })
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
		prevEnabled, prevPos, prevWidth := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = true, "left", 28
		t.Cleanup(func() {
			config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = prevEnabled, prevPos, prevWidth
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
		_, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 60, Y: 10, Button: tea.MouseLeft}, o)
	})

	// appearance.focus_follows_mouse_pause: each step is one motion report
	// over pane b, and want is the focus after it. A release is only seen on
	// the next motion, so the step after a held one is what proves it resumes.
	type step struct {
		mod  tea.KeyMod
		want int
	}
	pauseRows := []struct {
		name  string
		key   string
		mode  app.Mode
		steps []step
	}{
		{"ctrl held keeps focus, release resumes", "ctrl", app.WindowManagementMode,
			[]step{{tea.ModCtrl, 0}, {tea.ModCtrl | tea.ModShift, 0}, {0, 1}}},
		{"ctrl held in terminal mode keeps focus", "ctrl", app.TerminalMode,
			[]step{{tea.ModCtrl, 0}, {0, 1}}},
		{"alt held keeps focus, release resumes", "alt", app.WindowManagementMode,
			[]step{{tea.ModAlt, 0}, {0, 1}}},
		{"ctrl key ignores alt", "ctrl", app.WindowManagementMode,
			[]step{{tea.ModAlt, 1}}},
		{"alt key ignores ctrl", "alt", app.WindowManagementMode,
			[]step{{tea.ModCtrl, 1}}},
		{"off never pauses", "off", app.WindowManagementMode,
			[]step{{tea.ModCtrl, 1}}},
		{"empty never pauses", "", app.WindowManagementMode,
			[]step{{tea.ModCtrl | tea.ModAlt, 1}}},
		{"shift is not a pause key", "shift", app.WindowManagementMode,
			[]step{{tea.ModShift, 1}}},
	}
	for _, row := range pauseRows {
		t.Run("pause: "+row.name, func(t *testing.T) {
			setFFM(t, true)
			o := twoPaneOS(t)
			// Through the config, the way the file and the settings page set it.
			o.UserConfig.Appearance.FocusFollowsMousePause = row.key
			config.ApplyAppearanceConfig(o.UserConfig, &o.Settings)
			o.Settings.FocusFollowsMouse = true
			o.Mode = row.mode
			for i, st := range row.steps {
				o, _ = handleMouseMotion(tea.MouseMotionMsg{X: 60 + i, Y: 10, Mod: st.mod}, o)
				if o.FocusedWindow != st.want {
					t.Fatalf("step %d (mod %v): focus = %d, want %d", i, st.mod, o.FocusedWindow, st.want)
				}
			}
		})
	}
}
