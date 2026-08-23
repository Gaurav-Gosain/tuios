package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// nPaneTiledOS builds a tiled client with n panes on one workspace.
func nPaneTiledOS(t testing.TB, n, w, h int) *OS {
	t.Helper()
	wins := make([]*terminal.Window, 0, n)
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("stale-%d-%d", n, i), 40, 20)
		win.Workspace = 1
		wins = append(wins, win)
	}
	m := &OS{
		Windows:          wins,
		FocusedWindow:    0,
		CurrentWorkspace: 1,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		NumWorkspaces:    9,
		Width:            w,
		Height:           h,
		AutoTiling:       true,
		UseBSPLayout:     true,
		MasterRatio:      0.5,
	}
	m.TileAllWindows()
	return m
}

// TestSettledTiledLayoutIsNeverStale is the false-positive guard on
// tiledLayoutStale, and it is the important direction: judging a settled layout
// stale would retile on every state sync a peer sends, which is churn traded
// for churn.
//
// Every setting that changes how much of the box the panes cover is here - a
// gap between panes, shared borders, both, and a sidebar taking columns off the
// side - because the check is an equality against the box's edges and each of
// these moves one.
//
// NEGATIVE CONTROL: none, deliberately. tiledLayoutStale did not exist before,
// so this cannot fail on the unfixed tree. It fails on any version of the check
// that mistakes a reserved gap or a border allowance for a layout computed at
// the wrong size, which is exactly what it is here to rule out.
func TestSettledTiledLayoutIsNeverStale(t *testing.T) {
	cases := []struct {
		what    string
		gap     int
		shared  bool
		sidebar bool
	}{
		{what: "plain"},
		{what: "with a pane gap", gap: 2},
		{what: "with shared borders", shared: true},
		{what: "with shared borders and a gap", gap: 3, shared: true},
		{what: "with a sidebar", sidebar: true},
		{what: "with a sidebar and a gap", gap: 2, sidebar: true},
	}

	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			prevGap, prevShared := config.PaneGap, config.SharedBorders
			prevSidebar, prevSide := config.SidebarEnabled, config.SidebarPosition
			prevAnim := config.AnimationsEnabled
			t.Cleanup(func() {
				config.PaneGap, config.SharedBorders = prevGap, prevShared
				config.SidebarEnabled, config.SidebarPosition = prevSidebar, prevSide
				config.AnimationsEnabled = prevAnim
			})
			config.PaneGap, config.SharedBorders = tc.gap, tc.shared
			config.AnimationsEnabled = false
			if tc.sidebar {
				config.SidebarEnabled, config.SidebarPosition = true, "left"
			}

			for _, panes := range []int{1, 2, 3, 5} {
				m := nPaneTiledOS(t, panes, 160, 48)
				if m.tiledLayoutStale() {
					right, bottom := 0, 0
					for _, w := range m.Windows {
						right = max(right, w.X+w.Width)
						bottom = max(bottom, w.Y+w.Height)
					}
					t.Errorf("%d panes: a layout this client just computed reads as stale "+
						"(panes reach %d,%d; the box is %+v), so every peer sync would retile",
						panes, right, bottom, m.GetBSPBounds())
				}
			}
		})
	}
}

// TestLayoutFromASmallerScreenIsStale is the true-positive direction, taken at
// the check itself rather than through a whole sync.
func TestLayoutFromASmallerScreenIsStale(t *testing.T) {
	prevAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prevAnim })

	m := nPaneTiledOS(t, 2, 160, 48)
	if m.tiledLayoutStale() {
		t.Fatal("the fixture reads as stale before anything is done to it")
	}

	// Halve every pane, as adopting a layout from a client half the size does.
	for _, w := range m.Windows {
		w.Width /= 2
		w.Height /= 2
	}
	if !m.tiledLayoutStale() {
		t.Error("panes covering a quarter of the box read as a settled layout, so a " +
			"client whose session had grown back would keep the smaller client's rectangles")
	}
}
