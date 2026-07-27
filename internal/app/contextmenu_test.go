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

// TestContextMenuLayerFitsNarrowScreens checks the placed layer, which is what
// the compositor actually draws, rather than the panel string alone.
func TestContextMenuLayerFitsNarrowScreens(t *testing.T) {
	for _, sc := range narrowScreens {
		t.Run(sc.name, func(t *testing.T) {
			m := ctxMenuOS(t, sc.w, sc.h)
			for _, a := range ctxAnchors(m, sc.w, sc.h) {
				m.OpenContextMenu(a.x, a.y)
				var found bool
				for _, layer := range m.renderOverlays() {
					if layer.GetID() != "context-menu" {
						continue
					}
					found = true
					assertFitsScreen(t, "context-menu layer", layer.GetContent(), sc.w, sc.h)
					if layer.GetX() < 0 || layer.GetY() < 0 ||
						layer.GetX()+layer.Width() > sc.w || layer.GetY()+layer.Height() > sc.h {
						t.Errorf("%s/%s: layer at (%d,%d) size %dx%d, screen is %dx%d",
							sc.name, a.name, layer.GetX(), layer.GetY(),
							layer.Width(), layer.Height(), sc.w, sc.h)
					}
				}
				if !found {
					t.Errorf("%s/%s: no context-menu layer rendered", sc.name, a.name)
				}
				m.CloseContextMenu()
			}
		})
	}
}

// TestContextMenuAnchorPlacement pins the flip behaviour: the menu opens down
// and right of the pointer when there is room, and flips to the other side of
// the anchor when there is not, rather than being slid along the edge.
func TestContextMenuAnchorPlacement(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)

	// Plenty of room below and to the right: the anchor is the top-left corner.
	m.OpenContextMenu(10, 5)
	_, geo := m.renderContextMenu()
	if x, y := m.contextMenuOrigin(m.ContextMenu, geo); x != 10 || y != 5 {
		t.Errorf("with room, menu origin = (%d,%d), want the anchor (10,5)", x, y)
	}
	m.CloseContextMenu()

	// Hard against the right and bottom: the anchor becomes the bottom-right
	// corner instead.
	m.OpenContextMenu(119, 39)
	_, geo = m.renderContextMenu()
	x, y := m.contextMenuOrigin(m.ContextMenu, geo)
	if wantX := 119 - geo.Width + 1; x != wantX {
		t.Errorf("at the right edge, menu x = %d, want %d (flipped left of the anchor)", x, wantX)
	}
	if wantY := 39 - geo.Height + 1; y != wantY {
		t.Errorf("at the bottom edge, menu y = %d, want %d (flipped above the anchor)", y, wantY)
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

// TestContextMenuClickRunsTheClickedRow checks the click path returns the
// action of the row under the cursor and closes the menu.
func TestContextMenuClickRunsTheClickedRow(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	m.OpenContextMenu(120/2, 20) // desktop: no dimmed rows at the top
	m.renderOverlays()
	cm := m.ContextMenu

	want := ""
	row := -1
	for i, it := range cm.Items {
		if !it.Sep && !it.Dim {
			want, row = it.Action, i
			break
		}
	}
	if row < 0 {
		t.Fatal("no runnable row in the desktop menu")
	}

	action, consumed := m.ContextMenuClick(cm.BoundsX+3, cm.FirstRowY+row)
	if !consumed {
		t.Fatal("click on a row was not consumed")
	}
	if action != want {
		t.Errorf("click on row %d returned %q, want %q", row, action, want)
	}
	if m.ContextMenuActive() {
		t.Error("running a row left the menu open")
	}
}

// TestContextMenuArrowsSkipDimmedAndSeparators checks arrow navigation over
// every menu: it must never rest on a separator or a dimmed row, and a full lap
// in either direction must return to where it started.
func TestContextMenuArrowsSkipDimmedAndSeparators(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)

	for _, a := range ctxAnchors(m, 120, 40)[:4] {
		t.Run(a.name, func(t *testing.T) {
			m.OpenContextMenu(a.x, a.y)
			cm := m.ContextMenu

			runnable := 0
			for _, it := range cm.Items {
				if !it.Sep && !it.Dim {
					runnable++
				}
			}
			if runnable == 0 {
				t.Skip("menu has no runnable rows")
			}

			if cm.Selected < 0 || cm.Items[cm.Selected].Sep || cm.Items[cm.Selected].Dim {
				t.Fatalf("menu opened on row %d, which is a separator or dimmed", cm.Selected)
			}

			start := cm.Selected
			for _, dir := range []int{1, -1} {
				for step := range runnable {
					m.ContextMenuMove(dir)
					sel := cm.Selected
					if sel < 0 || sel >= len(cm.Items) {
						t.Fatalf("dir %d step %d: selection out of range: %d", dir, step, sel)
					}
					if cm.Items[sel].Sep {
						t.Fatalf("dir %d step %d: selection landed on a separator (row %d)", dir, step, sel)
					}
					if cm.Items[sel].Dim {
						t.Fatalf("dir %d step %d: selection landed on the dimmed row %q (row %d)",
							dir, step, cm.Items[sel].Label, sel)
					}
				}
				if cm.Selected != start {
					t.Errorf("dir %d: a full lap of %d runnable rows ended on %d, started on %d",
						dir, runnable, cm.Selected, start)
				}
			}
			m.CloseContextMenu()
		})
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
			if want := contextMenuHint(m.KeybindRegistry, it.Action); it.Hint != want {
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
		if !strings.HasPrefix(it.Hint, config.LeaderKey+" ") {
			t.Errorf("row %q runs the prefix action %q but its hint %q does not start with the leader %q",
				it.Label, it.Action, it.Hint, config.LeaderKey)
		}
	}
}

// TestContextMenuTargetResolution checks the pointer position picks the menu,
// which is the whole idea of a context menu.
func TestContextMenuTargetResolution(t *testing.T) {
	m := ctxMenuOS(t, 120, 40)
	win := m.Windows[0]

	cases := []struct {
		name string
		x, y int
		want ContextMenuTarget
	}{
		{"inside the pane", win.X + 2, win.Y + 3, CtxTargetPane},
		// The pane's border rows are part of the pane, not targets of their own.
		{"the pane's top row", win.X + 2, win.Y, CtxTargetPane},
		{"the pane's bottom row", win.X + 2, win.Y + win.Height - 1, CtxTargetPane},
		{"empty space right of the pane", win.X + win.Width + 5, 10, CtxTargetDesktop},
		{"the dock band", 1, 39, CtxTargetDock},
	}
	for _, tc := range cases {
		got, _ := m.contextMenuTargetAt(tc.x, tc.y)
		if got != tc.want {
			t.Errorf("%s: (%d,%d) resolved to target %d, want %d", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

// TestContextMenuDockBandExcludesTopWindowRow is the regression test for the
// defect that made a pane unreachable at the top of the screen.
//
// A dock of DockHeight rows at the top of the screen occupies rows 0 to
// DockHeight-1. The band test used to be inclusive, so it also claimed row
// DockHeight, which is exactly where the topmost window starts when the dock
// pushes the layout down. Every shift+right-click on that row opened the dock's
// menu, and the window under the pointer was never consulted.
func TestContextMenuDockBandExcludesTopWindowRow(t *testing.T) {
	oldPos := config.DockbarPosition
	defer func() { config.DockbarPosition = oldPos }()
	config.DockbarPosition = "top"

	m := newNarrowOS(t, 120, 40)
	// With the dock at the top the layout starts below it, so the window's first
	// row is row DockHeight.
	top := config.DockHeight
	m.Windows = []*terminal.Window{
		{ID: "a", CustomName: "editor", X: 0, Y: top, Width: 60, Height: 20, Workspace: 1},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0

	for y := range config.DockHeight {
		if !m.inDockBand(y) {
			t.Errorf("row %d is drawn on by a %d-row top dock but is not in the dock band",
				y, config.DockHeight)
		}
	}
	if m.inDockBand(top) {
		t.Errorf("row %d is the first row of the topmost window, but the dock band claims it; "+
			"the pane is unreachable there", top)
	}
	if got, idx := m.contextMenuTargetAt(2, top); got != CtxTargetPane || idx != 0 {
		t.Errorf("the topmost window's first row resolved to target %d (window %d), want the pane (%d, window 0)",
			got, idx, CtxTargetPane)
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
	m.Windows[0].SelectedText = ""

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

	// With a selection and tiling on, the same rows come alive.
	m.CloseContextMenu()
	m.AutoTiling = true
	m.Windows[0].SelectedText = "hello"
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
