package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// ctxOS builds an OS with a registry, a visible pane and a minimized one, which
// between them can reach every context menu target.
func ctxOS(t *testing.T) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	o := app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
	o.Width, o.Height = 120, 40
	o.EffectiveWidth, o.EffectiveHeight = 120, 40
	o.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: 60, Height: 30, Workspace: 1},
		{ID: "b", CustomName: "logs", Width: 20, Height: 10, Workspace: 1, Minimized: true},
	}
	o.CurrentWorkspace, o.FocusedWindow = 1, 0
	return o
}

// ctxMenuAnchors is one anchor per target.
var ctxMenuAnchors = []struct {
	name string
	x, y int
}{
	{"pane inside", 5, 5},
	{"pane top row", 5, 0},
	{"desktop", 100, 20},
	{"dock", 1, 39},
}

// TestContextMenuActionsAreRegistered is the guard that makes a context menu
// row more than a string.
//
// Every row names an action ID and nothing else, and the input layer hands that
// ID to the same ActionDispatcher a keybinding goes through. If a row named an
// action nobody registered, clicking it would silently do nothing, and the only
// way to find out would be to click it. This checks every row of every menu
// against the dispatcher.
func TestContextMenuActionsAreRegistered(t *testing.T) {
	o := ctxOS(t)
	dispatcher := GetDispatcher()

	seen := 0
	for _, a := range ctxMenuAnchors {
		o.OpenContextMenu(a.x, a.y)
		if o.ContextMenu == nil {
			t.Fatalf("%s: no menu opened at (%d,%d)", a.name, a.x, a.y)
		}
		for _, it := range o.ContextMenu.Items {
			if it.Sep || it.Action == "" {
				continue
			}
			seen++
			if !dispatcher.HasAction(it.Action) {
				t.Errorf("%s: row %q names action %q, which the dispatcher does not have; "+
					"clicking it would do nothing", a.name, it.Label, it.Action)
			}
			if _, ok := config.ActionDescriptions[it.Action]; !ok {
				t.Errorf("%s: row %q names action %q, which has no description in the registry",
					a.name, it.Label, it.Action)
			}
		}
		o.CloseContextMenu()
	}
	if seen == 0 {
		t.Fatal("no menu rows were checked; the menus are empty")
	}
}

// TestContextMenuSwallowsKeys checks the menu is modal to the keyboard: a key
// that would otherwise be a window-manager binding must not reach it while the
// menu is up. "n" is new_window, which would be silently destructive to the
// user's sense of what the menu is doing.
func TestContextMenuSwallowsKeys(t *testing.T) {
	o := ctxOS(t)
	before := len(o.Windows)

	o.OpenContextMenu(100, 20)
	for _, k := range []string{"n", "q", "t", "z"} {
		o, _ = HandleKeyPress(ctxKey(k), o)
		if !o.ContextMenuActive() {
			t.Fatalf("%q closed the context menu", k)
		}
	}
	if len(o.Windows) != before {
		t.Errorf("keys leaked past the open menu: window count %d -> %d", before, len(o.Windows))
	}
}

// TestRightClickGesture pins the click-vs-drag split on the right button over a
// pane. A plain right press arms a resize (so a drag resizes exactly as it
// always has), and a release without movement is a click that opens the pane
// menu. Over a pane whose app requested mouse tracking the right button belongs
// to that app, so there the menu still needs ctrl or shift.
func TestRightClickGesture(t *testing.T) {
	t.Run("terminal mode plain right-click opens the menu when the option is on", func(t *testing.T) {
		o := ctxOS(t)
		o.Mode = app.TerminalMode
		o.Settings.RightClickOpensMenu = true
		o, _ = handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseRight}, o)
		if !o.ContextMenuActive() {
			t.Fatal("a plain right-click in terminal mode did not open the pane menu")
		}
		if o.ContextMenu.Target != app.CtxTargetPane {
			t.Errorf("menu target = %v, want the pane menu", o.ContextMenu.Target)
		}
		for _, it := range o.ContextMenu.Items {
			if it.Action == "paste_clipboard" {
				if it.Dim {
					t.Error("paste is dimmed on the pane menu opened from terminal mode")
				}
				return
			}
		}
		t.Error("the pane menu opened from terminal mode has no paste row")
	})

	t.Run("mouse-mode pane keeps the modifier requirement", func(t *testing.T) {
		o := ctxOS(t)
		em := vt.NewEmulator(58, 28)
		t.Cleanup(func() { _ = em.Close() })
		if _, err := em.Write([]byte("\x1b[?1000h")); err != nil {
			t.Fatalf("enable mouse tracking: %v", err)
		}
		o.Windows[0].Terminal = em

		o, _ = handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseRight}, o)
		o, _ = handleMouseRelease(tea.MouseReleaseMsg{X: 5, Y: 5, Button: tea.MouseRight}, o)
		if o.ContextMenuActive() {
			t.Fatal("a plain right-click over a mouse-tracking app opened the menu; the app owns that button")
		}

		o, _ = handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseRight, Mod: tea.ModCtrl}, o)
		if !o.ContextMenuActive() {
			t.Fatal("ctrl+right-click over a mouse-tracking app did not open the pane menu")
		}
	})

	t.Run("desktop right click opens the desktop menu on the press", func(t *testing.T) {
		o := ctxOS(t)
		o, _ = handleMouseClick(tea.MouseClickMsg{X: 100, Y: 20, Button: tea.MouseRight}, o)
		if !o.ContextMenuActive() {
			t.Fatal("right-click on empty desktop did not open the desktop menu")
		}
		if o.ContextMenu.Target != app.CtxTargetDesktop {
			t.Errorf("menu target = %v, want the desktop menu", o.ContextMenu.Target)
		}
	})
}

// ctxKey builds a KeyPressMsg for a named key, matching what msg.String() returns
// for the keys the menu handles.
func ctxKey(name string) tea.KeyPressMsg {
	switch name {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		return tea.KeyPressMsg{Code: rune(name[0]), Text: name}
	}
}
