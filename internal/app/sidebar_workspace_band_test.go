package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// bandTestOS is sidebarTestOS with its windows spread over workspaces 1, 2 and
// 4, which is what gives the band something to draw.
func bandTestOS(t *testing.T, w, h int, pos string) *OS {
	t.Helper()
	m := sidebarTestOS(t, w, h, pos)
	m.Windows[1].Workspace = 2
	m.Windows[2].Workspace = 4
	return m
}

// bandHits returns the recorded chip rectangles, in drawn order.
func bandHits(m *OS) []sidebarRowHit {
	var out []sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWorkspace {
			out = append(out, h)
		}
	}
	return out
}

// TestSidebarBandChipClickSwitchesWorkspace is the band's mouse contract: every
// column of a chip switches to that chip's workspace, and the chips cover the
// occupied set.
func TestSidebarBandChipClickSwitchesWorkspace(t *testing.T) {
	m := bandTestOS(t, 120, 40, "left")
	sidebarText(t, m)

	chips := bandHits(m)
	if len(chips) != 3 {
		t.Fatalf("workspaces 1, 2 and 4 are occupied; band drew %d chips", len(chips))
	}
	for i, want := range []int{1, 2, 4} {
		if chips[i].Workspace != want {
			t.Fatalf("chip %d is workspace %d, want %d", i, chips[i].Workspace, want)
		}
	}

	target := chips[2]
	for x := target.X0; x < target.X1; x++ {
		m := bandTestOS(t, 120, 40, "left")
		sidebarText(t, m)
		if !m.SidebarClick(x, target.Y0, false) {
			t.Fatalf("column %d of the workspace-4 chip was not consumed", x)
		}
		if m.CurrentWorkspace != 4 {
			t.Errorf("clicking column %d left the workspace at %d, want 4", x, m.CurrentWorkspace)
		}
	}
}

// TestSidebarBandChipKeyboardActivate is the same switch by keyboard: the cursor
// reaches a chip and enter runs it, without leaving the rail (switching
// workspace is navigation, not a request for a pane).
func TestSidebarBandChipKeyboardActivate(t *testing.T) {
	m := bandTestOS(t, 120, 40, "left")
	m.SidebarFocused = true
	sidebarText(t, m)

	idx := -1
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowWorkspace && r.Workspace == 2 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no nav row for the workspace-2 chip; the cursor cannot reach the band")
	}

	m.SidebarCursor = idx
	if exit := m.SidebarActivateCursor(); exit {
		t.Error("activating a chip asked to leave the rail")
	}
	if m.CurrentWorkspace != 2 {
		t.Errorf("enter on the workspace-2 chip left the workspace at %d", m.CurrentWorkspace)
	}
}

// TestSidebarNavAndHitsStayParallel is the invariant the band could most easily
// break: the drawn rows, the hit rectangles, and the nav rows are one target
// list. Every hit must name a nav row, and the hits must appear in nav order.
func TestSidebarNavAndHitsStayParallel(t *testing.T) {
	for _, pos := range []string{"left", "right"} {
		for _, size := range []struct {
			name string
			w, h int
		}{{"full", 120, 40}, {"narrow", 80, 24}, {"glyph", 51, 37}} {
			t.Run(pos+"/"+size.name, func(t *testing.T) {
				m := bandTestOS(t, size.w, size.h, pos)
				m.SidebarFocused = true
				lines, w := m.sidebarPanelLines()
				if lines == nil || w <= 0 {
					t.Skip("sidebar reserves nothing at this size")
				}

				last := -1
				for _, h := range m.SidebarHits {
					target := navRowOf(h)
					at := -1
					for i, r := range m.SidebarNav {
						if sidebarNavRowsEqual(r, target) {
							at = i
							break
						}
					}
					if at < 0 {
						t.Fatalf("hit %+v has no nav row", target)
					}
					if at <= last {
						t.Fatalf("hit %+v is at nav index %d, out of drawn order after %d", target, at, last)
					}
					last = at
				}
			})
		}
	}
}

// TestSidebarBandRidesTheCache proves the band is part of the cache key: an
// unrelated frame reuses the rail, a workspace switch rebuilds it, and so does a
// window moving between workspaces (which changes no window's id or title, so
// only the occupancy fold can see it).
func TestSidebarBandRidesTheCache(t *testing.T) {
	m := bandTestOS(t, 120, 40, "left")
	sidebarText(t, m)
	if !m.sidebarCache.valid {
		t.Fatal("cache not populated after the first render")
	}
	sig := m.sidebarCache.sig

	sidebarText(t, m)
	if m.sidebarCache.sig != sig {
		t.Error("an unrelated frame rebuilt the rail")
	}

	m.SwitchToWorkspace(2)
	sidebarText(t, m)
	if m.sidebarCache.sig == sig {
		t.Error("a workspace switch did not rebuild the rail")
	}
	sig = m.sidebarCache.sig

	// Vacating workspace 4 removes its chip; nothing else about the window changes.
	m.Windows[2].Workspace = 2
	sidebarText(t, m)
	if m.sidebarCache.sig == sig {
		t.Fatal("moving a window between workspaces did not rebuild the rail")
	}
	if got := len(bandHits(m)); got != 2 {
		t.Errorf("workspaces 1 and 2 are occupied; band drew %d chips", got)
	}
}

// TestSidebarBandStaysOffWithOneWorkspace keeps the rail from spending a row on
// a chip there is no alternative to.
func TestSidebarBandStaysOffWithOneWorkspace(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left") // every window on workspace 1
	sidebarText(t, m)
	if got := len(bandHits(m)); got != 0 {
		t.Errorf("a single occupied workspace drew %d chips", got)
	}
}

// TestSidebarBandIsAbsentFromTheGlyphRail: three columns have no room for chips,
// and the glyph rail shows no windows either.
func TestSidebarBandIsAbsentFromTheGlyphRail(t *testing.T) {
	m := bandTestOS(t, 51, 37, "left")
	sidebarText(t, m)
	if got := len(bandHits(m)); got != 0 {
		t.Errorf("the glyph rail drew %d chips", got)
	}
}

// TestSidebarBandSurvivesWindowRowsOff: the band is workspaces, not windows, so
// hiding window rows must not take it with them.
func TestSidebarBandSurvivesWindowRowsOff(t *testing.T) {
	prev := config.SidebarShowWindows
	config.SidebarShowWindows = false
	t.Cleanup(func() { config.SidebarShowWindows = prev })

	m := bandTestOS(t, 120, 40, "left")
	sidebarText(t, m)
	if got := len(bandHits(m)); got != 3 {
		t.Errorf("band drew %d chips with window rows off, want 3", got)
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowWindow {
			t.Fatal("window rows are off but one was drawn")
		}
	}
}
