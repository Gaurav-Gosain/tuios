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

// A divider is the whole boundary between two panes, so it has to reach both
// ends of the region it splits: the dock's hairline where the dock closes the
// content region, the sidebar's edge rule where the sidebar does, and the last
// cell where nothing does. These read the composed frame, because the extent the
// user sees is the glyph in the cell and not the numbers in the split.

// extentOS builds a shared-border session of n tiled panes with the dock and the
// sidebar on the given sides ("" for no sidebar), so the separator overlay is
// exercised against every shape the content region takes.
func extentOS(t *testing.T, n int, dock, sidebar string) *OS {
	t.Helper()
	return extentOSStyled(t, n, dock, sidebar, "rounded")
}

// extentOSStyled is the same session drawn in a given border style.
func extentOSStyled(t *testing.T, n int, dock, sidebar, borderStyle string) *OS {
	t.Helper()
	shared, anim, ascii := config.Global.SharedBorders, config.Global.AnimationsEnabled, config.Global.UseASCIIOnly
	style, dockPos := config.Global.BorderStyle, config.Global.DockbarPosition
	sidebarOn, sidebarPos := config.Global.SidebarEnabled, config.Global.SidebarPosition
	config.Global.SharedBorders = true
	config.Global.AnimationsEnabled = false
	config.Global.UseASCIIOnly = false
	config.Global.BorderStyle = borderStyle
	config.Global.DockbarPosition = dock
	config.Global.SidebarEnabled = sidebar != ""
	if sidebar != "" {
		config.Global.SidebarPosition = sidebar
	}
	t.Cleanup(func() {
		config.Global.SharedBorders, config.Global.AnimationsEnabled, config.Global.UseASCIIOnly = shared, anim, ascii
		config.Global.BorderStyle, config.Global.DockbarPosition = style, dockPos
		config.Global.SidebarEnabled, config.Global.SidebarPosition = sidebarOn, sidebarPos
	})

	m := &OS{
		Settings: config.Global,
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.Global.SharedBorders,
		PaneGap:          config.Global.PaneGap,
		Windows:          make([]*terminal.Window, 0, n),
		FocusedWindow:    0,
		WorkspaceFocus:   map[int]int{},
		WorkspaceTrees:   map[int]*layout.BSPTree{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            160,
		Height:           48,
		AutoTiling:       true,
		UseBSPLayout:     true,
		MasterRatio:      0.5,
	}
	for i := range n {
		win := newTestWindow(t, fmt.Sprintf("extent-%s-%s-%s-%d-%d", borderStyle, dock, sidebar, n, i), 40, 20)
		win.Workspace = 1
		m.Windows = append(m.Windows, win)
	}
	m.TileAllWindows()
	return m
}

// frameCells composes the frame with its chrome and returns it as rows of runes,
// so a cell can be read where the user sees it.
func frameCells(t *testing.T, m *OS) [][]rune {
	t.Helper()
	var g [][]rune
	for line := range strings.SplitSeq(lipgloss.Sprint(m.GetCanvas(true).Render()), "\n") {
		g = append(g, []rune(ansi.Strip(line)))
	}
	return g
}

func sidebarName(side string) string {
	if side == "" {
		return "no-sidebar"
	}
	return side + "-sidebar"
}

// TestDividerNeverPaintsPaneCells is the invariant the extension must not break:
// a divider cell belongs to no pane. It runs with the sidebar on each side and
// off, because the content region's bounds move with it and the extension is
// measured from them.
func TestDividerNeverPaintsPaneCells(t *testing.T) {
	for _, dock := range []string{"top", "bottom"} {
		for _, side := range []string{"left", "right", ""} {
			for _, n := range []int{2, 3, 4} {
				t.Run(fmt.Sprintf("%s-dock/%s/%d-panes", dock, sidebarName(side), n), func(t *testing.T) {
					m := extentOS(t, n, dock, side)
					paintMarkers(m)
					g := frameCells(t, m)
					for i, w := range m.Windows {
						want := paneMarker(i)
						if w.Y >= len(g) {
							t.Fatalf("pane %d is at row %d, past the %d-row frame", i, w.Y, len(g))
						}
						row := g[w.Y]
						if w.X+len(want) > len(row) {
							t.Fatalf("pane %d row is %d cells, too short for its marker at column %d", i, len(row), w.X)
						}
						if got := string(row[w.X : w.X+len(want)]); got != want {
							t.Errorf("pane %d first columns are %q, want %q: a divider is drawn on pane content", i, got, want)
						}
					}
				})
			}
		}
	}
}
