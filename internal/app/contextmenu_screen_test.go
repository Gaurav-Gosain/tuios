package app_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The context menu, opened with shift and a right click as a person opens it,
// and read off the screen. e2e/tui covers the targets, running a row, the
// arrows over dimmed rows, the screen edges and hover through the real
// program's motion filter; these cover the click and the hint rules.

// openMenu opens the context menu for whatever is at a cell.
func openMenu(f *apptest.Fixture, x, y int) {
	m := tea.Mouse{X: x, Y: y, Button: tea.MouseRight, Mod: tea.ModShift}
	f.Send(tea.MouseClickMsg(m))
	f.Send(tea.MouseReleaseMsg(m))
}

// menuRow is one row of the open menu as drawn.
type menuRow struct {
	y           int
	label, hint string
	sep         bool
	selected    bool
}

// menuOnScreen finds the open menu: its frame, title and rows.
func menuOnScreen(t *testing.T, f *apptest.Fixture) (apptest.Box, string, []menuRow) {
	t.Helper()
	if !f.M.ContextMenuActive() {
		t.Fatalf("no context menu is open:\n%s", f.Text())
	}
	screen := f.Screen()
	whole, cut := f.Panels()
	if len(cut) > 0 {
		t.Fatalf("a panel is cut by the screen: %v\n%s", cut, f.Text())
	}
	for _, b := range whole {
		title := strings.TrimSpace(panelRow(screen[b.Y0+1], b))
		if title == "" || f.M.ContextMenu == nil || title != strings.TrimSpace(f.M.ContextMenu.Title) {
			continue
		}
		var rows []menuRow
		for y := b.Y0 + 3; y < b.Y1; y++ {
			text := strings.TrimSpace(panelRow(screen[y], b))
			r := menuRow{y: y}
			switch {
			case text != "" && strings.Trim(text, "─") == "":
				r.sep = true
			default:
				text, r.selected = strings.CutPrefix(text, "›")
				// A row may start with its icon, a private-use glyph.
				text = strings.TrimLeftFunc(strings.TrimSpace(text), func(r rune) bool {
					return unicode.Is(unicode.Co, r) || unicode.IsSpace(r)
				})
				label, hint, _ := strings.Cut(text, "  ")
				r.label, r.hint = strings.TrimSpace(label), strings.TrimSpace(hint)
			}
			rows = append(rows, r)
		}
		return b, title, rows
	}
	t.Fatalf("the open menu %q is not a whole panel on screen:\n%s", f.M.ContextMenu.Title, f.Text())
	return apptest.Box{}, "", nil
}

// menuItem is the model's row with a label, for what the screen does not
// say: the action a row names and whether it is dimmed.
func menuItem(t *testing.T, f *apptest.Fixture, label string) app.ContextMenuItem {
	t.Helper()
	for _, it := range f.M.ContextMenu.Items {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("the menu has no row %q", label)
	return app.ContextMenuItem{}
}

func rowLabeled(t *testing.T, rows []menuRow, label string) menuRow {
	t.Helper()
	for _, r := range rows {
		if r.label == label {
			return r
		}
	}
	t.Fatalf("no row %q on screen in %+v", label, rows)
	return menuRow{}
}

// desktopCell is a cell of the desktop: right of the pane, left of the rail.
func desktopCell(t *testing.T, f *apptest.Fixture) (int, int) {
	t.Helper()
	w := f.M.GetFocusedWindow()
	return w.X + w.Width + 1, w.Y + w.Height/2
}

// menuAnchors are the cells a menu is opened from: one per target, and the
// screen's corners, where placement has to flip rather than draw off the
// edge.
func menuAnchors(f *apptest.Fixture, w, h int) map[string][2]int {
	px, py := f.PaneCenter()
	pane := f.M.GetFocusedWindow()
	return map[string][2]int{
		"pane-top-row": {pane.X + 2, pane.Y}, "pane-inside": {px, py},
		"dock": {1, h - 1}, "center": {w / 2, h / 2},
		"top-left": {0, 0}, "top-right": {w - 1, 0},
		"bottom-left": {0, h - 1}, "bottom-right": {w - 1, h - 1},
	}
}

// TestContextMenuFitsNarrowScreens opens a menu from every anchor on screens
// from tall and narrow to short and wide, and checks every one is a whole
// panel on screen. A panel wider or taller than the screen has the missing
// part dropped by the terminal, and tuios has shipped that bug before.
// Negative control: skip the clamp to the screen in contextMenuOrigin.
func TestContextMenuFitsNarrowScreens(t *testing.T) {
	for _, sc := range []struct{ w, h int }{{51, 37}, {90, 20}, {30, 24}, {100, 12}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", sc.w, sc.h), func(t *testing.T) {
			f := apptest.New(t, apptest.Panes(1), apptest.Size(sc.w, sc.h),
				apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
			opened := 0
			for name, a := range menuAnchors(f, sc.w, sc.h) {
				openMenu(f, a[0], a[1])
				if !f.M.ContextMenuActive() {
					continue
				}
				opened++
				// A menu pushed off an edge loses that edge's frame, so it
				// has to be found whole, not only found uncut.
				t.Logf("opened at %s (%d,%d)", name, a[0], a[1])
				menuOnScreen(t, f)
				f.Keys("esc")
			}
			if opened == 0 {
				t.Fatal("no anchor opened a menu")
			}
		})
	}
}

// menuFixture is a pane with the desktop menu open beside it.
func menuFixture(t *testing.T, opts ...apptest.Option) *apptest.Fixture {
	t.Helper()
	opts = append([]apptest.Option{apptest.Panes(1),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true })}, opts...)
	f := apptest.New(t, opts...)
	dx, dy := desktopCell(t, f)
	openMenu(f, dx, dy)
	if _, title, _ := menuOnScreen(t, f); title != "Desktop" {
		t.Fatalf("the desktop cell opened the %q menu", title)
	}
	return f
}

// hoverEveryRow points at each row of the open menu in turn and checks the
// selection follows: a runnable row is selected where it is drawn, and a
// separator or a dimmed row selects nothing new. Hover and click resolve a
// cell to a row the same way, so this is the click's hit test without
// running every row.
func hoverEveryRow(t *testing.T, f *apptest.Fixture) {
	t.Helper()
	box, _, rows := menuOnScreen(t, f)
	for _, r := range rows {
		before := selectedLabel(t, f)
		f.Hover(box.X0+4, r.y)
		got := selectedLabel(t, f)
		switch {
		case r.sep:
			if got != before {
				t.Errorf("pointing at the separator on row %d selected %q", r.y, got)
			}
		case menuItem(t, f, r.label).Dim:
			if got != before {
				t.Errorf("pointing at the dimmed row %q selected %q; a dimmed row must not be clickable", r.label, got)
			}
		default:
			if got != r.label {
				t.Errorf("pointing at %q on row %d selected %q", r.label, r.y, got)
			}
		}
	}
}

func selectedLabel(t *testing.T, f *apptest.Fixture) string {
	t.Helper()
	_, _, rows := menuOnScreen(t, f)
	for _, r := range rows {
		if r.selected {
			return r.label
		}
	}
	return ""
}

// TestContextMenuClickRunsTheRowUnderThePointer: a click on a row runs that
// row and closes the menu; one on a separator, a dimmed row or the title
// runs nothing and leaves it open. Negative control: make HitTest return the
// row above the one under the pointer.
func TestContextMenuClickRunsTheRowUnderThePointer(t *testing.T) {
	f := menuFixture(t)
	hoverEveryRow(t, f)

	box, _, rows := menuOnScreen(t, f)
	tiling := f.M.AutoTiling
	for _, r := range rows {
		if r.sep {
			f.Click(box.X0+4, r.y)
			if !f.M.ContextMenuActive() || f.M.AutoTiling != tiling {
				t.Error("a click on the separator closed the menu or ran something")
			}
		}
	}
	f.Click(box.X0+4, box.Y0+1)
	if !f.M.ContextMenuActive() || f.M.AutoTiling != tiling {
		t.Error("a click on the menu's title closed it or ran something")
	}
	f.Click(box.X0+4, rowLabeled(t, rows, "Toggle tiling").y)
	if f.M.ContextMenuActive() {
		t.Error("the menu stayed open after a row ran")
	}
	if f.M.AutoTiling == tiling {
		t.Error("a click on Toggle tiling did not toggle tiling")
	}
}

// TestContextMenuClickOutsideClosesWithoutFiring is the guard on the worst
// way this could go wrong: a click meant to dismiss the menu running the
// selected row. Negative control: run the selected row on a click outside in
// ContextMenuClick.
func TestContextMenuClickOutsideClosesWithoutFiring(t *testing.T) {
	f := menuFixture(t)
	box, _, rows := menuOnScreen(t, f)
	f.Hover(box.X0+4, rowLabeled(t, rows, "Toggle tiling").y)
	tiling := f.M.AutoTiling

	f.Click(box.X0-3, box.Y0+2)
	if f.M.ContextMenuActive() {
		t.Error("clicking away from the menu left it open")
	}
	if f.M.AutoTiling != tiling {
		t.Error("clicking away from the menu ran its selected row")
	}
}

// TestContextMenuHitTestWhileScrolled: on a screen too short for the pane
// menu, the menu scrolls, and a row drawn at the top of it is not item 0. A
// cell must still resolve to the row drawn there, or a click runs something
// the person did not point at. Negative control: drop ScrollFrom from
// HitTest.
func TestContextMenuHitTestWhileScrolled(t *testing.T) {
	f := apptest.New(t, apptest.Panes(1), apptest.Size(120, 12),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
	px, py := f.PaneCenter()
	openMenu(f, px, py)
	_, _, rows := menuOnScreen(t, f)
	if len(f.M.ContextMenu.Items) <= len(rows) {
		t.Fatalf("the pane menu fits a 12-row screen (%d rows); nothing scrolls", len(rows))
	}
	f.Keys("up") // the selection wraps to the last row, and the menu scrolls to it
	if _, _, after := menuOnScreen(t, f); after[0] == rows[0] {
		t.Fatalf("the menu did not scroll: its first row is still %+v", after[0])
	}
	hoverEveryRow(t, f)
}

// TestContextMenuScrollKeepsSelectionVisible: the arrows through a menu
// taller than the screen keep the selected row on screen. A selection
// scrolled out of view leaves enter running a row nobody can see. Negative
// control: skip the scroll in ContextMenuMove.
func TestContextMenuScrollKeepsSelectionVisible(t *testing.T) {
	f := apptest.New(t, apptest.Panes(1), apptest.Size(120, 12),
		apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
	px, py := f.PaneCenter()
	openMenu(f, px, py)
	for step := range len(f.M.ContextMenu.Items) * 2 {
		f.Keys("down")
		if selectedLabel(t, f) == "" {
			t.Fatalf("step %d: the selected row is not on screen:\n%s", step, f.Text())
		}
	}
}

// TestContextMenuAllDimmedHasNoSelection: a menu whose rows are all
// unavailable selects nothing rather than falling back to a dimmed row.
func TestContextMenuAllDimmedHasNoSelection(t *testing.T) {
	cm := &app.ContextMenu{Items: []app.ContextMenuItem{
		{Label: "a", Dim: true},
		{Sep: true},
		{Label: "b", Dim: true},
	}, Selected: -1}

	if got := cm.Next(1); got != -1 {
		t.Errorf("Next on an all-dimmed menu returned %d, want -1", got)
	}
	cm.Move(1)
	if cm.Selected != -1 {
		t.Errorf("Move on an all-dimmed menu selected row %d", cm.Selected)
	}
}

// TestContextMenuHintsMatchRegistry keeps the menu from becoming a second
// hand-written table of keys: a row's hint on screen is the key the registry
// binds to the row's action, and a row with nothing bound shows none. An
// action behind the leader shows the whole chord, with the configured
// leader. Negative control: return a fixed hint from contextMenuHint.
func TestContextMenuHintsMatchRegistry(t *testing.T) {
	f := menuFixture(t)
	leader := f.M.Settings.LeaderKey
	for name, a := range menuAnchors(f, 120, 40) {
		if name != "pane-inside" && name != "pane-top-row" && name != "dock" && name != "center" {
			continue
		}
		openMenu(f, a[0], a[1])
		if !f.M.ContextMenuActive() {
			continue
		}
		_, title, rows := menuOnScreen(t, f)
		for _, r := range rows {
			if r.sep {
				continue
			}
			it := menuItem(t, f, r.label)
			if it.Action == "" {
				if !it.Dim {
					t.Errorf("%s: row %q is runnable but names no action", title, r.label)
				}
				continue
			}
			keys := f.M.KeybindRegistry.GetKeys(it.Action)
			switch {
			case len(keys) == 0 && r.hint != "":
				t.Errorf("%s: row %q has nothing bound but shows %q", title, r.label, r.hint)
			case len(keys) > 0 && !strings.Contains(r.hint, keys[0]):
				t.Errorf("%s: row %q is bound to %q but shows %q", title, r.label, keys[0], r.hint)
			}
			if strings.HasPrefix(it.Action, "prefix_") && !strings.HasPrefix(r.hint, leader+" ") {
				t.Errorf("%s: row %q runs %s behind the leader, but shows %q, not %q first", title, r.label, it.Action, r.hint, leader)
			}
		}
		f.Keys("esc")
	}
}

// TestContextMenuHintFollowsRebind: an action rebound in the config shows
// its new key, which a hand-written table would not. Negative control: as
// for TestContextMenuHintsMatchRegistry.
func TestContextMenuHintFollowsRebind(t *testing.T) {
	f := menuFixture(t, apptest.Config(func(c *config.UserConfig) {
		c.Keybindings.WindowManagement["new_window"] = []string{"ctrl+t"}
	}))
	_, _, rows := menuOnScreen(t, f)
	if r := rowLabeled(t, rows, "New window"); r.hint != "ctrl+t" {
		t.Errorf("after rebinding new_window to ctrl+t, the menu shows %q", r.hint)
	}
}

// TestContextMenuDimsUnavailableActions: in window management, with tiling
// off and no selection, copy, paste and the splits are dimmed on a pane's
// menu, while zoom and close, which always apply, are not. With tiling on and
// a selection dragged in the pane, copy and the splits come alive. The
// selection is made by a drag because that is the only kind the mouse
// makes. Negative control: dim copy_selection unconditionally in paneMenu.
func TestContextMenuDimsUnavailableActions(t *testing.T) {
	f := apptest.New(t, apptest.Panes(1), apptest.Config(func(c *config.UserConfig) { c.Appearance.FramePanels = true }))
	f.Output("hello world")
	f.Keys("ctrl+b", "space") // tiling off
	if f.M.AutoTiling {
		t.Fatal("ctrl+b space did not turn tiling off")
	}
	windowMode(f)
	px, py := f.PaneCenter()
	openMenu(f, px, py)
	for _, it := range f.M.ContextMenu.Items {
		switch it.Action {
		case "copy_selection", "split_vertical", "split_horizontal", "paste_clipboard":
			if !it.Dim {
				t.Errorf("%s is live with no selection, no tiling, and not in terminal mode", it.Action)
			}
		case "toggle_zoom", "close_window":
			if it.Dim {
				t.Errorf("%s is dimmed; it always applies to a pane", it.Action)
			}
		}
	}
	f.Keys("esc")

	f.Keys("ctrl+b", "space") // tiling on
	x, y, ok := f.Find("hello world")
	if !ok {
		t.Fatalf("the pane's text is not on screen:\n%s", f.Text())
	}
	f.Keys("i") // back to the terminal, where a drag selects
	f.Drag(x, y, x+4, y)
	openMenu(f, x+2, y)
	for _, it := range f.M.ContextMenu.Items {
		if (it.Action == "copy_selection" || it.Action == "split_vertical") && it.Dim {
			t.Errorf("%s is dimmed with tiling on and a selection in the pane", it.Action)
		}
	}
}
