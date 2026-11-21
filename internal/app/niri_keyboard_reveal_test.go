package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func scrollingOS(t *testing.T, count int) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.AutoTiling = true
	m.UseScrollingLayout = true
	m.CurrentWorkspace = 1
	for i := range count {
		m.Windows = append(m.Windows, &terminal.Window{
			ID: string(rune('a' + i)), Width: 80, Height: 30, Workspace: 1,
		})
	}
	m.FocusedWindow = 0
	m.TileAllWindows()
	return m
}

// TestWalkingToTheNextPaneBringsAllOfItOnScreen.
//
// Negative control: dropping the RevealFocusedColumn call from
// afterFocusCommand leaves the strip where it was and this fails.
func TestWalkingToTheNextPaneBringsAllOfItOnScreen(t *testing.T) {
	m := scrollingOS(t, 4)
	last := len(m.Windows) - 1
	m.FocusWindow(last)
	if !parkStraddling(m, last) {
		t.Fatal("ASSERTION: could not park the strip with the column half on screen, so this proves nothing")
	}

	m.RevealFocusedColumn()

	if !columnFullyVisible(m, last) {
		t.Error("the focused column is still not all on screen after being asked for")
	}
}

func parkStraddling(m *OS, n int) bool {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	id := m.GetWindowIntID(m.Windows[n].ID)
	for x := range sl.TotalStripWidth(viewW) {
		sl.ViewportX = x
		r, ok := sl.ComputePositions(viewW, m.GetUsableHeight(), 0)[id]
		if !ok {
			continue
		}
		if r.X < viewW && r.X+r.W > viewW {
			return true
		}
		if r.X < 0 && r.X+r.W > 0 {
			return true
		}
	}
	return false
}

func columnFullyVisible(m *OS, n int) bool {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	rects := sl.ComputePositions(viewW, m.GetUsableHeight(), 0)
	r, ok := rects[m.GetWindowIntID(m.Windows[n].ID)]
	if !ok {
		return false
	}
	return r.X >= 0 && r.X+r.W <= viewW
}

// TestHoveringAPaneBringsAllOfItOnScreen is the same rule for focus follows
// mouse: pointing at a column with that setting on is how you pick the pane to
// work in, and there is no other gesture to make.
//
// Focus went through the least-scroll rule, which leaves a column that is
// already partly visible where it is, so hovering one at the edge focused a
// pane the user could not see.
func TestHoveringAPaneBringsAllOfItOnScreen(t *testing.T) {
	m := scrollingOS(t, 4)
	m.Settings.FocusFollowsMouse = true
	m.Settings.NiriHoverReveals = true

	last := len(m.Windows) - 1
	m.FocusWindow(last)
	if !parkStraddling(m, last) {
		t.Fatal("ASSERTION: could not park the strip with the column half on screen, so this proves nothing")
	}

	m.RevealHoveredColumn()

	if !columnFullyVisible(m, last) {
		t.Error("the hovered column is still not all on screen")
	}
}
