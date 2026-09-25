package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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
