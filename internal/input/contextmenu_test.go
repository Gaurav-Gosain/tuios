package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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

// TestContextMenuEscapeClosesWithoutFiring checks the escape key dismisses the
// menu and runs nothing. The evidence is that the window count does not change:
// the desktop menu's selected row is New window, so a menu that fired on escape
// would leave a window behind.
func TestContextMenuEscapeClosesWithoutFiring(t *testing.T) {
	o := ctxOS(t)
	before := len(o.Windows)

	o.OpenContextMenu(100, 20) // desktop
	if got := o.ContextMenuSelectedAction(); got != "new_window" {
		t.Fatalf("the desktop menu opened with %q selected, want new_window", got)
	}

	o, _ = HandleKeyPress(ctxKey("esc"), o)

	if o.ContextMenuActive() {
		t.Error("esc did not close the context menu")
	}
	if len(o.Windows) != before {
		t.Errorf("esc created a window (%d -> %d); it must fire nothing", before, len(o.Windows))
	}
}

// TestContextMenuEnterRunsSelectedAction checks enter runs the highlighted row
// through the dispatcher and closes the menu.
func TestContextMenuEnterRunsSelectedAction(t *testing.T) {
	o := ctxOS(t)
	before := len(o.Windows)

	o.OpenContextMenu(100, 20) // desktop, New window selected
	o, _ = HandleKeyPress(ctxKey("enter"), o)

	if o.ContextMenuActive() {
		t.Error("enter left the context menu open")
	}
	if len(o.Windows) != before+1 {
		t.Errorf("enter on New window changed the count %d -> %d, want %d",
			before, len(o.Windows), before+1)
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

// TestContextMenuArrowKeysMoveSelection checks the arrow keys reach the menu
// and skip its dimmed rows, through the real key handler rather than by calling
// the mover directly.
func TestContextMenuArrowKeysMoveSelection(t *testing.T) {
	o := ctxOS(t)
	o.AutoTiling = false // dims both split rows
	o.OpenContextMenu(5, 5)

	cm := o.ContextMenu
	if cm == nil {
		t.Fatal("no pane menu opened")
	}
	for step := range len(cm.Items) * 2 {
		o, _ = HandleKeyPress(ctxKey("down"), o)
		sel := o.ContextMenu.Selected
		if o.ContextMenu.Items[sel].Dim || o.ContextMenu.Items[sel].Sep {
			t.Fatalf("step %d: down arrow landed on row %d (%q), which is dimmed or a separator",
				step, sel, o.ContextMenu.Items[sel].Label)
		}
	}
}

// TestShiftRightClickOpensMenuAndPlainRightClickDoesNot pins the chord. Plain
// right-click has always started a window resize, and the menu must not take
// that away.
func TestShiftRightClickOpensMenuAndPlainRightClickDoesNot(t *testing.T) {
	o := ctxOS(t)

	o, _ = handleMouseClick(tea.MouseClickMsg{X: 5, Y: 5, Button: tea.MouseRight}, o)
	if o.ContextMenuActive() {
		t.Error("a plain right-click opened a context menu; it must still start a resize")
	}
	if !o.Resizing {
		t.Error("a plain right-click on a pane no longer starts a resize")
	}

	o = ctxOS(t)
	o, _ = handleMouseClick(tea.MouseClickMsg{
		X: 5, Y: 5, Button: tea.MouseRight, Mod: tea.ModShift,
	}, o)
	if !o.ContextMenuActive() {
		t.Fatal("shift+right-click did not open a context menu")
	}
	if o.Resizing {
		t.Error("shift+right-click started a resize as well as opening the menu")
	}
}

// TestContextMenuClickOutsideFiresNothing checks the mouse path: a click away
// from an open menu closes it and runs nothing, and does not fall through to
// the pane underneath.
func TestContextMenuClickOutsideFiresNothing(t *testing.T) {
	o := ctxOS(t)
	before := len(o.Windows)

	o, _ = handleMouseClick(tea.MouseClickMsg{
		X: 100, Y: 20, Button: tea.MouseRight, Mod: tea.ModShift,
	}, o)
	if !o.ContextMenuActive() {
		t.Fatal("shift+right-click did not open a context menu")
	}

	// The renderer records the menu's bounds each frame; this test is about
	// where a click is routed, not about layout, so it stands the bounds up
	// directly rather than driving a whole render. The layout itself is checked
	// against the real renderer in internal/app.
	cm := o.ContextMenu
	cm.BoundsX, cm.BoundsY, cm.BoundsW, cm.BoundsH = 40, 10, 24, 10
	cm.FirstRowY, cm.ItemH = 13, 1

	away := cm.BoundsX + cm.BoundsW + 4
	o, _ = handleMouseClick(tea.MouseClickMsg{X: away, Y: cm.BoundsY + 2, Button: tea.MouseLeft}, o)

	if o.ContextMenuActive() {
		t.Error("a click away from the menu left it open")
	}
	if len(o.Windows) != before {
		t.Errorf("a click away from the menu fired an action: window count %d -> %d",
			before, len(o.Windows))
	}
	if o.Dragging || o.Resizing {
		t.Error("the dismissing click fell through to the layer underneath and started a gesture")
	}
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

// TestContextMenuHoverTracksPointer checks moving the pointer over the menu
// highlights the row under it, so the row that runs on a click is the row the
// cursor is on, and that motion does not fall through to the pane underneath.
func TestContextMenuHoverTracksPointer(t *testing.T) {
	o := ctxOS(t)
	o, _ = handleMouseClick(tea.MouseClickMsg{
		X: 100, Y: 20, Button: tea.MouseRight, Mod: tea.ModShift,
	}, o)
	if !o.ContextMenuActive() {
		t.Fatal("shift+right-click did not open a context menu")
	}

	cm := o.ContextMenu
	cm.BoundsX, cm.BoundsY, cm.BoundsW, cm.BoundsH = 40, 10, 24, 12
	cm.FirstRowY, cm.ItemH = 13, 1

	// Find a runnable row that is not the one already selected.
	target := -1
	for i, it := range cm.Items {
		if !it.Sep && !it.Dim && i != cm.Selected {
			target = i
			break
		}
	}
	if target < 0 {
		t.Skip("menu has only one runnable row")
	}

	o, _ = handleMouseMotion(tea.MouseMotionMsg{X: cm.BoundsX + 3, Y: cm.FirstRowY + target}, o)
	if o.ContextMenu.Selected != target {
		t.Errorf("hovering row %d left the selection on %d", target, o.ContextMenu.Selected)
	}

	// Hovering the menu's chrome must not move the selection off a real row.
	o, _ = handleMouseMotion(tea.MouseMotionMsg{X: cm.BoundsX + 3, Y: cm.BoundsY}, o)
	if o.ContextMenu.Selected != target {
		t.Errorf("hovering the menu chrome moved the selection to %d", o.ContextMenu.Selected)
	}
}
