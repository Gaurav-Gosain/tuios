package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scrolling layout has two rules for where the strip sits after focus
// moves, and which one applies is about what the user did rather than about
// what happened.
//
// A focus the user did not ask for, such as a workspace switch restoring its
// own focus, gets the least-scroll rule: a column already on screen does not
// move the strip under them. A key that says "take me to the next pane" is the
// other kind, and it was going through the least-scroll rule too, so cycling
// landed on panes still half off the edge.

// scrollingOS is a client in the scrolling layout with count panes, each wide
// enough that the strip is longer than the view.
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
	// A strip parked so the focused column straddles an edge: some of it on
	// screen and some of it off. That is the state the least-scroll rule
	// leaves alone, and the state the report was about. A column entirely off
	// screen proves nothing here, because least-scroll brings that one back
	// too.
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

// parkStraddling scrolls the strip so the nth window's pane is partly on
// screen and partly off, and reports whether it managed to.
func parkStraddling(m *OS, n int) bool {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	id := m.getWindowIntID(m.Windows[n].ID)
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

// columnFullyVisible reports whether the nth window's pane lies entirely
// within the view, which is what the strip positions say after a reveal.
func columnFullyVisible(m *OS, n int) bool {
	sl := m.GetOrCreateScrollingLayout()
	viewW := m.ScrollingViewWidth()
	rects := sl.ComputePositions(viewW, m.GetUsableHeight(), 0)
	r, ok := rects[m.getWindowIntID(m.Windows[n].ID)]
	if !ok {
		return false
	}
	return r.X >= 0 && r.X+r.W <= viewW
}

// TestAFocusNobodyAskedForLeavesTheStripAlone. The other rule, which this must
// not have broken: a workspace coming back restores its own focus, and
// scrolling the strip on that throws away wherever the user had left it.
func TestAFocusNobodyAskedForLeavesTheStripAlone(t *testing.T) {
	m := scrollingOS(t, 4)
	sl := m.GetOrCreateScrollingLayout()

	// Park the strip somewhere of the user's own, with some of the focused
	// column on screen. The least-scroll rule leaves that alone; anything that
	// scrolls here is throwing away where the user had got to.
	m.FocusWindow(1)
	viewW := m.ScrollingViewWidth()
	sl.ViewportX = max(sl.ViewportX+viewW/4, 0)
	sl.ClampViewport(viewW)
	before := sl.ViewportX

	m.ScrollingOnFocusChange()

	if sl.ViewportX != before {
		t.Errorf("a focus change moved the strip from %d to %d with the column already visible",
			before, sl.ViewportX)
	}
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

// TestHoverRevealHonoursItsSettings checks both switches: the reveal is off
// when the user turned it off, and it does nothing at all when nothing focuses
// on hover in the first place.
func TestHoverRevealHonoursItsSettings(t *testing.T) {
	for _, c := range []struct {
		name              string
		hoverReveals, ffm bool
	}{
		{"hover reveal off", false, true},
		{"focus does not follow the mouse", true, false},
		{"both off", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := scrollingOS(t, 4)
			m.Settings.NiriHoverReveals = c.hoverReveals
			m.Settings.FocusFollowsMouse = c.ffm

			last := len(m.Windows) - 1
			m.FocusWindow(last)
			if !parkStraddling(m, last) {
				t.Fatal("ASSERTION: could not park the strip with the column half on screen")
			}
			before := m.GetOrCreateScrollingLayout().ViewportX

			m.RevealHoveredColumn()

			if got := m.GetOrCreateScrollingLayout().ViewportX; got != before {
				t.Errorf("the strip moved from %d to %d with the reveal switched off", before, got)
			}
		})
	}
}
