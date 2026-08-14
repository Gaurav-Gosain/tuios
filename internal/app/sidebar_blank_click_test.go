package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A rail taller than its rows is mostly blank, and a click there did nothing at
// all: no row to route to, so the event was swallowed and the rail sat there
// looking inert. The user aimed at the rail, which is enough to say what they
// meant.

// blankBandCell finds a screen cell inside the rail's band that no row claims.
func blankBandCell(t *testing.T, m *OS) (int, int) {
	t.Helper()
	w := m.GetSidebarWidth()
	x := 1
	if config.SidebarPosition == "right" {
		x = m.GetRenderWidth() - w + 1
	}
	top := m.GetTopMargin()
	for y := top + m.GetUsableHeight() - 1; y >= top; y-- {
		if _, ok := m.sidebarRowAt(x, y); !ok && !m.sidebarOnEdge(x) {
			return x, y
		}
	}
	t.Fatal("the fixture left no blank line in the rail's band")
	return 0, 0
}

func TestClickOnBlankRailTakesTheKeyboard(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		t.Run(pos, func(t *testing.T) {
			m, tree := sidebarMultiSessionOS(t, 120, 40)
			withSidebar(t, true, pos, config.SidebarDefaultWidth)
			m.sidebarPanelLinesForTree(tree)
			x, y := blankBandCell(t, m)

			if m.SidebarFocused {
				t.Fatal("the fixture already gave the rail the keyboard")
			}
			if !m.SidebarClick(x, y, false) {
				t.Fatal("the click on blank rail was not consumed")
			}
			if !m.SidebarFocused {
				t.Error("a click on blank rail did not focus the rail")
			}

			// Focus is visible: the rail's edge rule burns accent while it owns the
			// keyboard, so the frame says so and not only the flag.
			focused, _ := m.sidebarPanelLinesForTree(tree)
			m.SidebarFocused = false
			idle, _ := m.sidebarPanelLinesForTree(tree)
			if strings.Join(focused, "\n") == strings.Join(idle, "\n") {
				t.Error("the frame draws a focused rail exactly like an idle one")
			}
		})
	}
}

// TestClickOnBlankRailLeavesTheCursorAlone is the other half of the rule: the
// click named no row, so there is nothing to move the cursor to, and moving it
// would throw away the row the user was already steering.
func TestClickOnBlankRailLeavesTheCursorAlone(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.SidebarFocused = true
	m.sidebarPanelLinesForTree(tree)

	target := m.sidebarFirstRowOfKind(sidebarRowWindow)
	if target < 0 {
		t.Fatal("the fixture drew no terminal rows")
	}
	m.sidebarSetCursor(target)
	want, _ := m.sidebarCursorRow()

	x, y := blankBandCell(t, m)
	m.SidebarClick(x, y, false)

	if !m.SidebarFocused {
		t.Error("a click on blank rail dropped the focus it already had")
	}
	got, ok := m.sidebarCursorRow()
	if !ok || !sidebarNavRowsEqual(got, want) {
		t.Errorf("the cursor moved to %+v, want it left on %+v", got, want)
	}
}

// TestRightClickOnBlankRailStillOpensItsSettings: the pointer's other button
// already meant something here, and taking the keyboard is not what a menu is
// for.
func TestRightClickOnBlankRailStillOpensItsSettings(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)
	m.sidebarPanelLinesForTree(tree)
	x, y := blankBandCell(t, m)

	m.SidebarClick(x, y, true)

	if m.ContextMenu == nil || m.ContextMenu.Title != "Sidebar" {
		t.Fatalf("right-click on blank rail opened %v, want the rail's own menu", m.ContextMenu)
	}
	if m.SidebarFocused {
		t.Error("opening a menu also took the keyboard")
	}
}
