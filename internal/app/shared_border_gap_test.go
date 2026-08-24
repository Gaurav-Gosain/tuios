package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// gapTestOS builds a tiled session of n panes under shared borders, each
// pane holding a marker that starts in its own first column.
func gapTestOS(t *testing.T, n int) *OS {
	t.Helper()
	origAnim := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = origAnim })

	m := &OS{
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.SharedBorders,
		PaneGap:          config.PaneGap,
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            160,
		Height:           48,
		AutoTiling:       true,
		MasterRatio:      0.5,

		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceHasCustom:   map[int]bool{},
		WorkspaceMasterRatio: map[int]float64{},
		PendingResizes:       map[string][2]int{},
	}
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("gap-%d-%d", n, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	return m
}

// paneMarker is the text a pane paints into its own top-left cell.
func paneMarker(i int) string { return fmt.Sprintf("PANE%dEDGE", i) }

func paintMarkers(m *OS) {
	for i, w := range m.Windows {
		w.LockIO()
		_, _ = w.Terminal.Write([]byte(paneMarker(i)))
		w.UnlockIO()
		w.MarkContentDirty()
	}
}

// TestSharedBordersKeepEveryPanesFirstColumn is the regression test for the
// divider eating a column of pane content.
//
// The BSP splitter reserved a cell between two panes for the line drawn between
// them; the master-stack tiler butted its panes together and the separator
// overlay - which kept reading the BSP tree, because the tree outlives a switch
// to master-stack - drew straight down the right-hand pane's first column. The
// frame showed "laude" where the pane had written "claude".
//
// This asserts the composed frame, per mode: every pane's first column is still
// the pane's own text.
func TestSharedBordersKeepEveryPanesFirstColumn(t *testing.T) {
	old := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = old })

	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack} {
		for _, n := range []int{2, 3, 4, 5, 7} {
			t.Run(fmt.Sprintf("%s/%d-panes", mode, n), func(t *testing.T) {
				m := gapTestOS(t, n)
				// Both modes get the BSP tree built first: in the real app the
				// tree is there from BSP being the default, and it stays behind
				// when the user switches to master-stack.
				m.UseBSPLayout = true
				m.TileAllWindows()
				m.ApplyLayoutModeName(mode)
				m.TileAllWindows()
				paintMarkers(m)

				lines := strings.Split(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n")
				for i, w := range m.Windows {
					want := paneMarker(i)
					if w.Y >= len(lines) {
						t.Fatalf("pane %d is at row %d, past the %d-row frame", i, w.Y, len(lines))
					}
					row := []rune(ansi.Strip(lines[w.Y]))
					if w.X+len(want) > len(row) {
						t.Fatalf("pane %d row is %d cells, too short for its marker at column %d", i, len(row), w.X)
					}
					if got := string(row[w.X : w.X+len(want)]); got != want {
						t.Errorf("pane %d first columns are %q, want %q: the divider is drawn on pane content", i, got, want)
					}
				}
			})
		}
	}
}

// TestSeparatorsOnlyDrawWherePanesLeftRoom pins the invariant the layouts have
// to agree on: a divider cell belongs to no pane. Whichever tiler placed the
// panes has to have reserved the cell the line lands on, so the check runs
// against every layout mode rather than the one the bug was found in.
func TestSeparatorsOnlyDrawWherePanesLeftRoom(t *testing.T) {
	old := config.SharedBorders
	config.SharedBorders = true
	t.Cleanup(func() { config.SharedBorders = old })

	for _, mode := range []string{LayoutModeBSP, LayoutModeMasterStack, LayoutModeScrolling} {
		for _, n := range []int{2, 3, 4, 5, 7} {
			t.Run(fmt.Sprintf("%s/%d-panes", mode, n), func(t *testing.T) {
				m := gapTestOS(t, n)
				m.UseBSPLayout = true
				m.TileAllWindows()
				m.ApplyLayoutModeName(mode)
				m.TileAllWindows()

				splits := m.separatorSplits()
				if mode == LayoutModeScrolling {
					// Scrolling panes keep their own borders and are never
					// borderless, so a shared divider would have nowhere to go.
					if len(splits) != 0 {
						t.Fatalf("scrolling layout offered %d dividers; its panes draw their own borders", len(splits))
					}
					return
				}
				if len(splits) == 0 {
					t.Fatalf("%d panes under shared borders drew no divider at all", n)
				}
				for _, s := range splits {
					for along := s.From; along <= s.To; along++ {
						x, y := s.Pos, along
						if !s.Vertical {
							x, y = along, s.Pos
						}
						for i, w := range m.Windows {
							if w.Minimized || w.IsFloating {
								continue
							}
							if x >= w.X && x < w.X+w.Width && y >= w.Y && y < w.Y+w.Height {
								t.Fatalf("divider cell (%d,%d) sits inside pane %d at (%d,%d %dx%d)", x, y, i, w.X, w.Y, w.Width, w.Height)
							}
						}
					}
				}
			})
		}
	}
}
