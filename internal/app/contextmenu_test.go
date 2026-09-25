package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ctxMenuOS builds an OS sized to a screen with one visible window filling the
// left half, one minimized window, and a registry, which between them can reach
// every context menu target.
func ctxMenuOS(t *testing.T, w, h int) *OS {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: 0, Width: max(w/2, 10), Height: max(h-2, 4), Workspace: 1},
		{ID: "b", CustomName: "logs", Width: 20, Height: 10, Workspace: 1, Minimized: true},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0
	return m
}

// selectInPane leaves a pane holding the kind of selection a mouse drag leaves:
// an implicit copy-mode session in visual-character state. Menus and the copy
// action are asked about this and nothing else.
func selectInPane(win *terminal.Window) {
	win.EnterCopyModeImplicit()
	win.CopyMode.State = terminal.CopyModeVisualChar
	win.CopyMode.VisualStart = terminal.Position{X: 0, Y: 0}
	win.CopyMode.VisualEnd = terminal.Position{X: 4, Y: 0}
}

// ctxAnchors are one anchor per target, plus the screen corners, which is where
// placement has to flip rather than draw off the edge.
func ctxAnchors(m *OS, w, h int) []struct {
	name string
	x, y int
} {
	return []struct {
		name string
		x, y int
	}{
		{"pane-top-row", 2, 0},
		{"pane-inside", 2, 2},
		{"desktop", w - 2, h / 2},
		{"dock", 1, h - 1},
		{"top-left", 0, 0},
		{"top-right", w - 1, 0},
		{"bottom-left", 0, h - 1},
		{"bottom-right", w - 1, h - 1},
		{"center", w / 2, h / 2},
	}
}

// TestContextMenuFitsNarrowScreens renders a context menu for every target at
// every anchor on every screen size and checks it never draws outside the
// screen. An overlay wider or taller than the screen has the missing part
// simply discarded by the terminal, and this codebase has shipped that bug
// before.
func TestContextMenuFitsNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := ctxMenuOS(t, sc.w, sc.h)
			for _, a := range ctxAnchors(m, sc.w, sc.h) {
				m.OpenContextMenu(a.x, a.y)
				out, geo := m.renderContextMenu()
				name := fmt.Sprintf("context menu[%s]", a.name)
				assertFitsScreen(t, name, out, sc.w, sc.h)

				x, y := m.contextMenuOrigin(m.ContextMenu, geo)
				if x < 0 || y < 0 {
					t.Errorf("%s: placed off the top-left at (%d,%d)", name, x, y)
				}
				if x+geo.Width > sc.w {
					t.Errorf("%s: spans x=%d..%d, screen is %d wide", name, x, x+geo.Width, sc.w)
				}
				if y+geo.Height > sc.h {
					t.Errorf("%s: spans y=%d..%d, screen is %d tall", name, y, y+geo.Height, sc.h)
				}
				m.CloseContextMenu()
			}
		})
	}
}

// TestContextMenuHitTest checks a click lands on the row the user aimed at, and
// that clicks on the menu's chrome, on a separator, on a dimmed row, and off
// the menu entirely all resolve to no row.
func TestContextMenuHitTest(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.OpenContextMenu(10, 5)
	m.renderOverlays() // places the menu and records its bounds
	cm := m.ContextMenu

	if cm.BoundsW == 0 || cm.ItemH != 1 {
		t.Fatalf("menu bounds were not recorded: %+v", cm)
	}

	x := cm.BoundsX + 3 // a column inside the rows
	for i, it := range cm.Items {
		y := cm.FirstRowY + i
		got := cm.HitTest(x, y)
		switch {
		case it.Sep:
			if got != -1 {
				t.Errorf("row %d is a separator, hit test returned %d", i, got)
			}
		case it.Dim:
			if got != -1 {
				t.Errorf("row %d (%q) is dimmed, hit test returned %d; a dimmed row must not be clickable",
					i, it.Label, got)
			}
		default:
			if got != i {
				t.Errorf("click on row %d (%q) hit %d", i, it.Label, got)
			}
		}
	}

	// Chrome above the first row, and past the last row.
	if got := cm.HitTest(x, cm.BoundsY); got != -1 {
		t.Errorf("click on the title chrome hit row %d", got)
	}
	if got := cm.HitTest(x, cm.FirstRowY+len(cm.Items)); got != -1 {
		t.Errorf("click below the last row hit row %d", got)
	}
	// Outside the menu in both axes.
	for _, p := range [][2]int{
		{cm.BoundsX - 1, cm.FirstRowY},
		{cm.BoundsX + cm.BoundsW, cm.FirstRowY},
		{x, cm.BoundsY - 1},
		{x, cm.BoundsY + cm.BoundsH},
	} {
		if got := cm.HitTest(p[0], p[1]); got != -1 {
			t.Errorf("click outside the menu at (%d,%d) hit row %d", p[0], p[1], got)
		}
		if cm.Contains(p[0], p[1]) {
			t.Errorf("(%d,%d) is outside the menu but Contains says otherwise", p[0], p[1])
		}
	}
}

// TestContextMenuClickOutsideClosesWithoutFiring is the guard on the most
// destructive way this could go wrong: a click meant to dismiss the menu
// running whatever row happened to be selected.
func TestContextMenuClickOutsideClosesWithoutFiring(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.OpenContextMenu(10, 5)
	m.renderOverlays()
	cm := m.ContextMenu

	action, consumed := m.ContextMenuClick(cm.BoundsX+cm.BoundsW+5, cm.BoundsY+2)
	if !consumed {
		t.Error("a click while the menu is open must be consumed by it")
	}
	if action != "" {
		t.Errorf("clicking away from the menu returned action %q; it must fire nothing", action)
	}
	if m.ContextMenuActive() {
		t.Error("clicking away from the menu left it open")
	}
}

// TestContextMenuClickOnChromeFiresNothing checks a click that lands on the
// menu but not on a row neither runs anything nor closes the menu. Closing on a
// stray click on the panel's own padding would be a surprise.
func TestContextMenuClickOnChromeFiresNothing(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.OpenContextMenu(10, 5)
	m.renderOverlays()
	cm := m.ContextMenu

	action, consumed := m.ContextMenuClick(cm.BoundsX+2, cm.BoundsY)
	if !consumed || action != "" {
		t.Errorf("click on menu chrome: action=%q consumed=%v, want \"\" and true", action, consumed)
	}
	if !m.ContextMenuActive() {
		t.Error("a click on the menu's own chrome closed it")
	}
}

// TestContextMenuAllDimmedHasNoSelection checks a menu whose rows are all
// unavailable selects nothing rather than falling back to a dimmed row.
func TestContextMenuAllDimmedHasNoSelection(t *testing.T) {
	cm := &ContextMenu{Items: []ContextMenuItem{
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

// TestContextMenuHintsMatchRegistry is the guard against this menu becoming the
// second thing in this codebase that documents keys from a hand-written table.
//
// Every row's hint has to be what the registry actually has bound to that row's
// action, and every row has to name an action the registry knows a description
// for. Rebinding an action in the config must move the hint with it, which the
// second half checks by rebinding one and rebuilding the menus.
func TestContextMenuHintsMatchRegistry(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)

	for _, a := range ctxAnchors(m, 120, 40)[:4] {
		m.OpenContextMenu(a.x, a.y)
		for _, it := range m.ContextMenu.Items {
			if it.Sep {
				continue
			}
			if it.Action == "" {
				// Only a dimmed placeholder may carry no action.
				if !it.Dim {
					t.Errorf("%s: row %q is runnable but names no action", a.name, it.Label)
				}
				continue
			}
			if want := contextMenuHint(m.KeybindRegistry, it.Action, &config.Global); it.Hint != want {
				t.Errorf("%s: row %q hint = %q, registry says %q", a.name, it.Label, it.Hint, want)
			}
			keys := m.KeybindRegistry.GetKeys(it.Action)
			if len(keys) == 0 {
				if it.Hint != "" {
					t.Errorf("%s: row %q has nothing bound but shows hint %q", a.name, it.Label, it.Hint)
				}
				continue
			}
			// The hint must contain the bound key, not some other string.
			if !strings.Contains(it.Hint, keys[0]) {
				t.Errorf("%s: row %q is bound to %q but its hint is %q",
					a.name, it.Label, keys[0], it.Hint)
			}
		}
		m.CloseContextMenu()
	}
}

// TestContextMenuHintFollowsRebind rebinds an action and checks the menu says
// so. This is the half of the registry guard that a hand-written table would
// fail.
func TestContextMenuHintFollowsRebind(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Keybindings.WindowManagement["new_window"] = []string{"ctrl+t"}
	m := ctxMenuOS(t, 120, 40)
	m.KeybindRegistry = config.NewKeybindRegistry(cfg)

	m.OpenContextMenu(60, 20) // desktop menu carries New window
	var hint string
	var found bool
	for _, it := range m.ContextMenu.Items {
		if it.Action == "new_window" {
			hint, found = it.Hint, true
		}
	}
	if !found {
		t.Fatal("the desktop menu no longer has a New window row")
	}
	if hint != "ctrl+t" {
		t.Errorf("after rebinding new_window to ctrl+t, the menu shows %q; the hint is not "+
			"coming from the registry", hint)
	}
}

// TestContextMenuPrefixHintIncludesLeader checks an action that lives behind
// the leader key shows the whole chord, not just the last keystroke, and that
// the leader shown is the configured one.
func TestContextMenuPrefixHintIncludesLeader(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.OpenContextMenu(60, 20)

	for _, it := range m.ContextMenu.Items {
		if !strings.HasPrefix(it.Action, "prefix_") {
			continue
		}
		if !strings.HasPrefix(it.Hint, config.Global.LeaderKey+" ") {
			t.Errorf("row %q runs the prefix action %q but its hint %q does not start with the leader %q",
				it.Label, it.Action, it.Hint, config.Global.LeaderKey)
		}
	}
}

// TestContextMenuOnPaneFocusesIt checks that opening a menu on a pane focuses
// it, so the actions on the menu act on the pane the user pointed at rather
// than on whichever one happened to have focus.
func TestContextMenuOnPaneFocusesIt(t *testing.T) {
	m := newNarrowOS(t, 120, 40)
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "left", X: 0, Y: 0, Width: 50, Height: 30, Workspace: 1, Z: 1},
		{ID: "b", CustomName: "right", X: 60, Y: 0, Width: 50, Height: 30, Workspace: 1, Z: 2},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0

	m.OpenContextMenu(65, 5) // inside the second window
	if m.FocusedWindow != 1 {
		t.Errorf("opening a menu on the right pane left focus on window %d", m.FocusedWindow)
	}
	if m.ContextMenu.Title != "right" {
		t.Errorf("menu title = %q, want the pane's name %q", m.ContextMenu.Title, "right")
	}
}

// TestContextMenuDimsUnavailableActions pins the states that dim a row, since
// the dim flag is what the arrow-skipping and hit-testing rules key off.
func TestContextMenuDimsUnavailableActions(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.AutoTiling = false
	m.Mode = WindowManagementMode

	m.OpenContextMenu(2, 2) // pane content
	dim := map[string]bool{}
	for _, it := range m.ContextMenu.Items {
		if !it.Sep {
			dim[it.Action] = it.Dim
		}
	}
	for _, action := range []string{"copy_selection", "split_vertical", "split_horizontal", "paste_clipboard"} {
		if !dim[action] {
			t.Errorf("%s should be dimmed: no selection, no tiling, and not in terminal mode", action)
		}
	}
	if dim["toggle_zoom"] || dim["close_window"] {
		t.Error("zoom and close always apply to a pane and must not be dimmed")
	}

	// With a selection and tiling on, the same rows come alive. The selection is
	// a copy-mode visual one because that is the only kind the mouse makes; a
	// test that filled SelectedText instead passed for months while every real
	// drag left the row dimmed.
	m.CloseContextMenu()
	m.AutoTiling = true
	selectInPane(m.Windows[0])
	m.OpenContextMenu(2, 2)
	for _, it := range m.ContextMenu.Items {
		if it.Action == "copy_selection" && it.Dim {
			t.Error("copy is dimmed even though the pane has a selection")
		}
		if it.Action == "split_vertical" && it.Dim {
			t.Error("split is dimmed even though tiling is on")
		}
	}
}

// TestPaneMenuReadsItsOwnTargetsSelection pins the menu to the pane it was
// built for. Every caller focuses that pane first, so focus and target agree
// today and the builder reading either one looks the same from outside; this
// asserts the property directly so a caller that stops focusing first cannot
// quietly start offering one pane's copy over another pane's contents.
func TestPaneMenuReadsItsOwnTargetsSelection(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.AutoTiling = true
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "left", X: 0, Y: 0, Width: 50, Height: 30, Workspace: 1, Z: 1},
		{ID: "b", CustomName: "right", X: 60, Y: 0, Width: 50, Height: 30, Workspace: 1, Z: 2},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0
	selectInPane(m.Windows[1]) // the selection is on the pane that is NOT focused

	title, items := m.paneMenu(1)
	if title != "right" {
		t.Fatalf("menu title = %q, want the target pane %q", title, "right")
	}
	for _, it := range items {
		if it.Action == "copy_selection" && it.Dim {
			t.Error("copy is dimmed although the pane the menu targets holds a selection")
		}
	}

	// And the converse: the target has none, so the row dims even though the
	// focused pane does have one.
	selectInPane(m.Windows[0])
	m.Windows[1].ExitCopyMode()
	_, items = m.paneMenu(1)
	for _, it := range items {
		if it.Action == "copy_selection" && !it.Dim {
			t.Error("copy is live on a pane with no selection, reading the focused pane's")
		}
	}
}

// TestContextMenuHitTestWhileScrolled checks a click resolves to the row the
// user can see when the menu is taller than the screen and has scrolled.
//
// The rows drawn at the top of a scrolled menu are not items 0, 1, 2, so a hit
// test that maps screen position straight to item index runs the wrong action.
// That is a click running something the user did not point at, which is the
// worst failure this menu has available to it.
func TestContextMenuHitTestWhileScrolled(t *testing.T) {
	// A screen short enough that the pane menu cannot show all its rows.
	m := ctxMenuOS(t, 120, 12)
	m.OpenContextMenu(2, 2) // the pane
	cm := m.ContextMenu

	if len(cm.Items) <= max(12-panelChromeRows, 1) {
		t.Skip("the pane menu fits this screen; nothing scrolls")
	}

	// Put the selection on the last row so the view has to scroll to it. Arrow
	// navigation wraps, so stepping a fixed number of times lands wherever the
	// runnable rows happen to fall; this test is about the scrolled hit test,
	// not about navigation.
	cm.Selected = len(cm.Items) - 1
	m.renderOverlays()

	if cm.ScrollFrom == 0 {
		t.Fatalf("the menu never scrolled: %d items on a %d-row screen", len(cm.Items), 12)
	}

	// Every visible row must hit test to the item actually drawn on it.
	_, visible := m.contextMenuRows(cm)
	for row := range visible {
		want := cm.ScrollFrom + row
		got := cm.HitTest(cm.BoundsX+3, cm.FirstRowY+row)
		if cm.Items[want].Sep || cm.Items[want].Dim {
			if got != -1 {
				t.Errorf("visible row %d holds a separator or dimmed item, hit test returned %d", row, got)
			}
			continue
		}
		if got != want {
			t.Errorf("visible row %d shows item %d (%q) but hit test returned %d",
				row, want, cm.Items[want].Label, got)
		}
	}
}

// TestContextMenuScrollKeepsSelectionVisible checks arrow navigation through a
// menu taller than the screen keeps the highlighted row on screen. A selection
// that scrolls out of view leaves the user pressing enter on something they
// cannot see.
func TestContextMenuScrollKeepsSelectionVisible(t *testing.T) {
	m := ctxMenuOS(t, 120, 12)
	m.OpenContextMenu(2, 2)
	cm := m.ContextMenu

	for step := range len(cm.Items) * 2 {
		m.ContextMenuMove(1)
		start, visible := m.contextMenuRows(cm)
		if cm.Selected < start || cm.Selected >= start+visible {
			t.Fatalf("step %d: selection %d is outside the drawn window [%d,%d)",
				step, cm.Selected, start, start+visible)
		}
	}
}
